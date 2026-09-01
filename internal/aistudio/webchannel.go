package aistudio

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	bidiWebChannelURL = "https://webchannel-alkalimakersuite-pa.clients6.google.com/v1/bidiGenerateContent"
)

var errBidiWebChannelClosed = errors.New("bidi WebChannel closed")

type bidiBackchannelNetworkError struct {
	err error
}

func (err *bidiBackchannelNetworkError) Error() string {
	return err.err.Error()
}

func (err *bidiBackchannelNetworkError) Unwrap() error {
	return err.err
}

// BidiService 创建 Gemini Live 或 Robotics Streaming 会话
type BidiService interface {
	OpenBidi(context.Context, BidiRequest) (*BidiSession, error)
}

// BidiProtectedTransport 建立持有当前账户租约的 WebChannel 会话
type BidiProtectedTransport interface {
	OpenBidiProtected(context.Context, BidiRequest, RequestContext, *AccountLease, func() error) (*BidiSession, error)
}

// BidiSession 保存一条 Google WebChannel 双向会话
type BidiSession struct {
	ctx                 context.Context
	cancel              context.CancelFunc
	lease               *AccountLease
	release             func() error
	worker              ProtectedPreparer
	client              *http.Client
	headers             http.Header
	gsessionID          string
	sid                 string
	model               string
	mode                BidiMode
	modelAccessScope    string
	accountID           string
	modelAccessObserver func()
	headerMu            sync.RWMutex

	stateMu                sync.Mutex
	aid                    int64
	rid                    int64
	offset                 int64
	sendMu                 sync.Mutex
	latestResumptionToken  string
	qualificationPending   int
	qualificationCheckedAt time.Time
	qualificationLastAt    time.Time

	events      chan BidiEvent
	wireEvents  chan BidiEvent
	forwarding  chan bool
	forwardDone chan struct{}
	done        chan struct{}
	releaseMu   sync.Mutex
	releaseErr  error
	closeErr    error
	closeOnce   sync.Once
}

var _ BidiService = (*PooledService)(nil)
var _ BidiProtectedTransport = (*WorkerProtectedTransport)(nil)

// OpenBidi 使用支持目标模型的账户创建双向会话
func (s *PooledService) OpenBidi(ctx context.Context, request BidiRequest) (*BidiSession, error) {
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	if modelID == "" {
		return nil, fmt.Errorf("%w: bidi model 不能为空", ErrInvalidArgument)
	}
	modelAccessScope := strings.TrimSpace(request.ModelAccessScope)
	if modelAccessScope == "" {
		modelAccessScope = modelID
	}
	request.ModelAccessScope = modelAccessScope
	transport, ok := s.client.protected.(BidiProtectedTransport)
	if !ok {
		return nil, fmt.Errorf("AI Studio protected transport 不支持 bidiGenerateContent")
	}
	selection := AccountSelection{
		ModelID: modelID, Method: "bidiGenerateContent", AccountID: strings.TrimSpace(request.AccountID),
		ModelAccessScope:  modelAccessScope,
		ResourceID:        strings.TrimSpace(request.SessionToken),
		AllowedAccountIDs: append([]string(nil), request.AllowedAccountIDs...),
	}
	pinned := selection.AccountID != "" || selection.ResourceID != ""
	if _, exists := AccountLeaseFromContext(ctx); exists {
		pinned = true
	}
	baseAccountID := selection.AccountID
	maxAttempts := accountAttemptLimit(s.pool, pinned)
	recoveryAccountID := ""
	var requestErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		selection.AccountID = baseAccountID
		if recoveryAccountID != "" {
			selection.AccountID = recoveryAccountID
			recoveryAccountID = ""
		}
		lease, owned, err := resolveAccountLease(ctx, s.pool, selection)
		if err != nil {
			if requestErr != nil && errors.Is(err, ErrNoEligibleAccount) {
				return nil, requestErr
			}
			return nil, err
		}
		request.AccountID = lease.Account().ID
		runtime := RequestContext{}
		if s.client.contextProvider != nil {
			runtime, err = s.client.contextProvider.RequestContext(ctx, request.AccountID)
			if err != nil {
				if owned {
					err = errors.Join(err, lease.Release())
				}
				return nil, fmt.Errorf("读取 AI Studio bidi 请求上下文: %w", err)
			}
		}
		var release func() error
		if owned {
			release = lease.Release
		}
		attemptCtx := ContextWithAccountLease(ctx, lease)
		session, err := transport.OpenBidiProtected(attemptCtx, request, runtime, lease, release)
		if err == nil {
			if stateErr := lease.MarkAuthenticationValid(); stateErr != nil {
				slog.Error("Bidi 账户认证状态保存失败", "account", request.AccountID, "error", stateErr)
			}
			if modelAccessScope == modelID {
				checkedAt := lease.CheckedAt()
				accountID := request.AccountID
				accessGeneration := lease.ModelAccessGeneration()
				go func() {
					changed, stateErr := s.pool.MarkModelAccessVerifiedIfGeneration(
						accountID, modelAccessScope, accessGeneration, checkedAt,
					)
					if stateErr != nil {
						slog.Error("Bidi 模型资格保存失败", "account", accountID, "model", modelID, "error", stateErr)
						return
					}
					if changed {
						session.notifyModelAccessChanged()
					}
				}()
			} else {
				if stateErr := s.pool.ClearCooldownIfGeneration(
					request.AccountID, "", lease.ModelAccessGeneration(), lease.CheckedAt(),
				); stateErr != nil {
					slog.Error("Bidi 账户冷却状态保存失败", "account", request.AccountID, "error", stateErr)
				}
			}
			return session, nil
		}
		requestErr = err
		var stateErr error
		if owned {
			requestErr = errors.Join(requestErr, stateErr, lease.Release())
		}
		if !owned {
			return nil, requestErr
		}
		if stateErr != nil {
			return nil, requestErr
		}
		if request.RecoverWAARuntime != nil {
			recovered, recoveryErr := request.RecoverWAARuntime(ctx, request.AccountID, err)
			if recoveryErr != nil {
				return nil, errors.Join(requestErr, recoveryErr)
			}
			if recovered {
				recoveryAccountID = request.AccountID
				maxAttempts++
				continue
			}
		}
		if !retryableBidiOpenError(ctx, err) {
			return nil, requestErr
		}
		if request.ObserveAccountFailure != nil {
			request.ObserveAccountFailure(request.AccountID, err)
		}
		stateErr = s.markRetryableFailure(lease, modelAccessScope, err)
		if stateErr != nil {
			return nil, errors.Join(requestErr, stateErr)
		}
		selection.AllowedAccountIDs = removeBidiAccount(selection.AllowedAccountIDs, request.AccountID)
	}
	return nil, requestErr
}

func removeBidiAccount(accountIDs []string, accountID string) []string {
	result := accountIDs[:0]
	for _, candidate := range accountIDs {
		if candidate != accountID {
			result = append(result, candidate)
		}
	}
	return result
}

func retryableBidiOpenError(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, ErrInvalidArgument) {
		return false
	}
	if retryableAccountError(err) || DefinitiveWAARuntimeFailure(err) {
		return true
	}
	var rpcError *RPCError
	if errors.As(err, &rpcError) {
		return false
	}
	var evidenceError *ProtocolEvidenceError
	return !errors.As(err, &evidenceError)
}

// Events 返回上游按网络顺序产生的事件
func (s *BidiSession) Events() <-chan BidiEvent {
	if s == nil {
		return nil
	}
	return s.events
}

// Done 在 WebChannel 释放账户后关闭
func (s *BidiSession) Done() <-chan struct{} {
	if s == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return s.done
}

// Model 返回会话使用的模型
func (s *BidiSession) Model() string {
	if s == nil {
		return ""
	}
	return s.model
}

func (s *BidiSession) notifyModelAccessChanged() {
	if s.modelAccessObserver != nil {
		s.modelAccessObserver()
	}
}

// SendText 发送一条官网文本输入帧
func (s *BidiSession) SendText(ctx context.Context, text string) error {
	body, binding, err := EncodeBidiTextRequest(text)
	if err != nil {
		return err
	}
	return s.sendProtected(
		ctx, body, binding,
		s.modelAccessScope != "" && (s.mode == BidiModeRobotics || s.modelAccessScope == s.model),
	)
}

// SendMedia 发送一条官网实时音频或图像输入帧
func (s *BidiSession) SendMedia(ctx context.Context, mimeType string, data []byte) error {
	body, binding, err := EncodeBidiMediaRequest(mimeType, data)
	if err != nil {
		return err
	}
	return s.sendProtected(ctx, body, binding, s.modelAccessScope != "")
}

// SendMediaEnd 发送官网实时媒体结束帧
func (s *BidiSession) SendMediaEnd(ctx context.Context) error {
	body, binding, err := EncodeBidiMediaEndRequest()
	if err != nil {
		return err
	}
	return s.sendProtected(ctx, body, binding, false)
}

// SendToolResponses 发送官网函数响应帧
func (s *BidiSession) SendToolResponses(ctx context.Context, results []FunctionResult) error {
	body, binding, err := EncodeBidiToolResponseRequest(results)
	if err != nil {
		return err
	}
	return s.sendProtected(ctx, body, binding, false)
}

// Close 取消网络读取并等待账户租约释放
func (s *BidiSession) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if s.ctx.Err() == nil {
			s.closeErr = s.terminate()
		}
		s.cancel()
	})
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-s.done:
	case <-timer.C:
		return errors.Join(s.closeErr, fmt.Errorf("等待 bidi WebChannel 关闭超时"))
	}
	s.releaseMu.Lock()
	defer s.releaseMu.Unlock()
	return errors.Join(s.closeErr, s.releaseErr)
}

// OpenBidiProtected 使用当前租约建立 WebChannel 会话
func (t *WorkerProtectedTransport) OpenBidiProtected(
	ctx context.Context,
	request BidiRequest,
	runtime RequestContext,
	lease *AccountLease,
	release func() error,
) (*BidiSession, error) {
	body, binding, err := EncodeBidiSetupRequest(request, runtime)
	if err != nil {
		return nil, err
	}
	worker, err := t.workers.Worker(ctx, lease.Account().ID, request.Model)
	if err != nil {
		return nil, fmt.Errorf("获取账户 WAA preparer: %w", err)
	}
	if request.ObserveWAARuntime != nil {
		if observed, ok := worker.(interface{ WorkerGeneration() uint64 }); ok {
			request.ObserveWAARuntime(lease.Account().ID, observed.WorkerGeneration())
		}
	}
	prepared, err := worker.Prepare(ctx, ProtectedRequest{
		URL: bidiWebChannelURL, Headers: http.Header{"Content-Type": []string{JSONProtobufContentType}},
		Body: body, Prompt: binding, ProofField: 6,
	})
	if err != nil {
		return nil, fmt.Errorf("准备 bidi setup fresh WAA proof: %w", err)
	}
	if prepared.Headers == nil || len(prepared.Body) == 0 {
		return nil, fmt.Errorf("WAA preparer 返回空 bidi setup")
	}
	headerProvider := ProtocolHeaderProviderFunc(func(context.Context, string) (http.Header, error) {
		return prepared.Headers.Clone(), nil
	})
	_, protocolHeaders, err := prepareProtocolHeaders(
		ctx, lease, RPCRequest{Method: "BidiGenerateContent", URL: bidiWebChannelURL},
		t.transport.signer, headerProvider, t.transport.now(), true,
	)
	if err != nil {
		return nil, err
	}
	client, err := t.transport.clientForProxy(lease.Account().EffectiveProxy(t.transport.globalProxy))
	if err != nil {
		return nil, err
	}
	rid, err := newWebChannelRID()
	if err != nil {
		return nil, err
	}
	requestCtx, cancel := context.WithCancel(ctx)
	setupCtx, cancelSetup := context.WithTimeout(ctx, t.setupTimeout)
	defer cancelSetup()
	session := &BidiSession{
		ctx: requestCtx, cancel: cancel, lease: lease, release: release, worker: worker, client: client,
		headers: webChannelHeaders(protocolHeaders), model: strings.TrimPrefix(strings.TrimSpace(request.Model), "models/"),
		mode: request.Mode, modelAccessScope: strings.TrimSpace(request.ModelAccessScope),
		modelAccessObserver: request.ObserveModelAccessChange,
		accountID:           lease.Account().ID, rid: rid, latestResumptionToken: strings.TrimSpace(request.SessionToken),
		events: make(chan BidiEvent, 32), wireEvents: make(chan BidiEvent, 32),
		forwarding: make(chan bool, 1), forwardDone: make(chan struct{}), done: make(chan struct{}),
	}
	if err := session.handshake(setupCtx, protocolHeaders); err != nil {
		cancel()
		return nil, err
	}
	ready := make(chan error, 1)
	go session.runBackchannel(ready)
	var readyErr error
	select {
	case readyErr = <-ready:
	case <-setupCtx.Done():
		readyErr = setupCtx.Err()
	}
	if readyErr != nil {
		session.disableEventForwarding()
		cancel()
		<-session.done
		return nil, readyErr
	}
	if err := session.sendPreparedSetup(setupCtx, prepared.Body); err != nil {
		session.disableEventForwarding()
		cancel()
		<-session.done
		return nil, err
	}
	if err := session.awaitSetup(setupCtx); err != nil {
		session.disableEventForwarding()
		cancel()
		<-session.done
		return nil, err
	}
	return session, nil
}

func (s *BidiSession) awaitSetup(ctx context.Context) error {
	pending := make([]BidiEvent, 0, 4)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-s.wireEvents:
			if !ok {
				return errBidiWebChannelClosed
			}
			pending = append(pending, event)
			switch event.Kind {
			case BidiEventSetupComplete:
				go s.forwardEvents(pending)
				s.forwarding <- true
				return nil
			case BidiEventError:
				if event.Err != nil {
					return event.Err
				}
				return errors.New("bidi setup 返回错误事件")
			case BidiEventClosed:
				return errBidiWebChannelClosed
			}
		}
	}
}

func (s *BidiSession) forwardEvents(pending []BidiEvent) {
	defer close(s.forwardDone)
	defer close(s.events)
	for _, event := range pending {
		if !s.forwardEvent(event) {
			return
		}
	}
	for event := range s.wireEvents {
		if !s.forwardEvent(event) {
			return
		}
	}
}

func (s *BidiSession) disableEventForwarding() {
	select {
	case s.forwarding <- false:
	default:
	}
}

func (s *BidiSession) forwardEvent(event BidiEvent) bool {
	select {
	case s.events <- event:
		return true
	case <-s.ctx.Done():
		return false
	}
}

func (s *BidiSession) handshake(ctx context.Context, protocolHeaders http.Header) error {
	zx, err := newWebChannelZX()
	if err != nil {
		return err
	}
	query := url.Values{
		"VER":               []string{"8"},
		"RID":               []string{strconv.FormatInt(s.rid, 10)},
		"CVER":              []string{"22"},
		"X-HTTP-Session-Id": []string{"gsessionid"},
		"$httpHeaders":      []string{webChannelAuthHeaders(protocolHeaders)},
		"zx":                []string{zx},
		"t":                 []string{"1"},
	}
	requestURL := bidiWebChannelURL + "?" + query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, strings.NewReader("count=0"))
	if err != nil {
		return fmt.Errorf("创建 bidi WebChannel handshake: %w", err)
	}
	request.Header = s.cloneHeaders()
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("执行 bidi WebChannel handshake: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("读取 bidi WebChannel handshake: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return decodeRPCError("BidiGenerateContent", response.StatusCode, body)
	}
	if err := s.mergeCookies(response, requestURL); err != nil {
		return err
	}
	s.gsessionID = strings.TrimSpace(response.Header.Get("X-HTTP-Session-Id"))
	if s.gsessionID == "" {
		return fmt.Errorf("bidi WebChannel handshake 缺少 gsessionid")
	}
	sid, err := parseWebChannelHandshake(bytes.NewReader(body))
	if err != nil {
		return err
	}
	s.sid = sid
	s.rid++
	return nil
}

func (s *BidiSession) sendPreparedSetup(ctx context.Context, body []byte) error {
	return s.postMessage(ctx, body, false)
}

func (s *BidiSession) sendProtected(ctx context.Context, body []byte, binding string, qualifies bool) error {
	requestCtx, cancel := context.WithCancel(ctx)
	stopSession := context.AfterFunc(s.ctx, cancel)
	defer func() {
		stopSession()
		cancel()
	}()
	prepared, err := s.worker.Prepare(requestCtx, ProtectedRequest{
		URL: bidiWebChannelURL, Headers: http.Header{"Content-Type": []string{JSONProtobufContentType}},
		Body: body, Prompt: binding, ProofField: 6,
	})
	if err != nil {
		return fmt.Errorf("准备 bidi fresh WAA proof: %w", err)
	}
	if len(prepared.Body) == 0 {
		return fmt.Errorf("WAA preparer 返回空 bidi 请求")
	}
	return s.postMessage(requestCtx, prepared.Body, qualifies)
}

func (s *BidiSession) postMessage(ctx context.Context, payload []byte, qualifies bool) (resultErr error) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if qualifies {
		s.beginQualificationAttempt()
		defer func() {
			if resultErr != nil {
				s.rollbackQualificationAttempt()
			}
		}()
	}
	requestCtx, cancel := context.WithCancel(ctx)
	stopSession := context.AfterFunc(s.ctx, cancel)
	defer func() {
		stopSession()
		cancel()
	}()
	s.stateMu.Lock()
	aid := s.aid
	rid := s.rid
	offset := s.offset
	s.stateMu.Unlock()
	zx, err := newWebChannelZX()
	if err != nil {
		return err
	}
	query := url.Values{
		"VER":        []string{"8"},
		"gsessionid": []string{s.gsessionID},
		"SID":        []string{s.sid},
		"RID":        []string{strconv.FormatInt(rid, 10)},
		"AID":        []string{strconv.FormatInt(aid, 10)},
		"zx":         []string{zx},
		"t":          []string{"1"},
	}
	form := url.Values{
		"count":         []string{"1"},
		"ofs":           []string{strconv.FormatInt(offset, 10)},
		"req0___data__": []string{string(payload)},
	}
	requestURL := bidiWebChannelURL + "?" + query.Encode()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, requestURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("创建 bidi WebChannel message: %w", err)
	}
	request.Header = s.cloneHeaders()
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("执行 bidi WebChannel message: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("读取 bidi WebChannel ACK: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return decodeRPCError("BidiGenerateContent", response.StatusCode, body)
	}
	if err := s.mergeCookies(response, requestURL); err != nil {
		return err
	}
	if err := parseWebChannelACK(bytes.NewReader(body)); err != nil {
		return err
	}
	s.stateMu.Lock()
	s.rid++
	s.offset++
	s.stateMu.Unlock()
	return nil
}

func (s *BidiSession) runBackchannel(ready chan<- error) {
	readyOnce := sync.Once{}
	reportReady := func(err error) {
		readyOnce.Do(func() {
			ready <- err
			close(ready)
		})
	}
	var streamErr error
	terminalEvent := false
	defer func() {
		readyErr := streamErr
		if readyErr == nil && s.ctx.Err() != nil {
			readyErr = s.ctx.Err()
		}
		reportReady(readyErr)
		if streamErr != nil && s.ctx.Err() == nil {
			s.emit(BidiEvent{Kind: BidiEventError, Err: streamErr})
		}
		if s.ctx.Err() == nil && !terminalEvent {
			s.emit(BidiEvent{Kind: BidiEventClosed})
		}
		close(s.wireEvents)
		if <-s.forwarding {
			<-s.forwardDone
		} else {
			close(s.events)
			close(s.forwardDone)
		}
		s.cancel()
		if s.release != nil {
			s.releaseMu.Lock()
			s.releaseErr = s.release()
			s.releaseMu.Unlock()
		}
		close(s.done)
	}()
	first := true
	for s.ctx.Err() == nil {
		opening := first
		_, err := s.readBackchannel(first, reportReady)
		first = false
		if err != nil {
			if errors.Is(err, errBidiWebChannelClosed) {
				terminalEvent = true
				return
			}
			if s.ctx.Err() != nil {
				return
			}
			var networkErr *bidiBackchannelNetworkError
			if !opening && errors.As(err, &networkErr) {
				timer := time.NewTimer(250 * time.Millisecond)
				select {
				case <-timer.C:
				case <-s.ctx.Done():
					timer.Stop()
					return
				}
				timer.Stop()
				continue
			}
			streamErr = err
			return
		}
	}
}

func (s *BidiSession) readBackchannel(first bool, ready func(error)) (int, error) {
	s.stateMu.Lock()
	aid := s.aid
	s.stateMu.Unlock()
	zx, err := newWebChannelZX()
	if err != nil {
		return 0, err
	}
	query := url.Values{
		"gsessionid": []string{s.gsessionID},
		"VER":        []string{"8"},
		"RID":        []string{"rpc"},
		"SID":        []string{s.sid},
		"AID":        []string{strconv.FormatInt(aid, 10)},
		"CI":         []string{"0"},
		"TYPE":       []string{"xmlhttp"},
		"zx":         []string{zx},
		"t":          []string{"1"},
	}
	requestURL := bidiWebChannelURL + "?" + query.Encode()
	request, err := http.NewRequestWithContext(s.ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return 0, fmt.Errorf("创建 bidi WebChannel backchannel: %w", err)
	}
	request.Header = s.cloneHeaders()
	response, err := s.client.Do(request)
	if err != nil {
		if first {
			ready(err)
		}
		return 0, &bidiBackchannelNetworkError{err: fmt.Errorf("执行 bidi WebChannel backchannel: %w", err)}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			return 0, readErr
		}
		return 0, decodeRPCError("BidiGenerateContent", response.StatusCode, body)
	}
	if err := s.mergeCookies(response, requestURL); err != nil {
		return 0, err
	}
	if first {
		ready(nil)
	}
	parsed := 0
	err = readWebChannelFrames(response.Body, func(frame json.RawMessage) error {
		count, frameErr := s.consumeBackchannelFrame(frame)
		parsed += count
		return frameErr
	})
	if errors.Is(err, io.EOF) {
		err = nil
	} else if err != nil && bidiNetworkReadError(err) {
		err = &bidiBackchannelNetworkError{err: err}
	}
	return parsed, err
}

func bidiNetworkReadError(err error) bool {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}

func (s *BidiSession) consumeBackchannelFrame(raw json.RawMessage) (int, error) {
	envelopes, err := rawArray(raw, "$webchannel", raw)
	if err != nil {
		return 0, withBidiMethod(err)
	}
	parsed := 0
	for index, envelopeRaw := range envelopes {
		envelope, err := rawArray(envelopeRaw, fmt.Sprintf("$webchannel[%d]", index), raw)
		if err != nil {
			return parsed, withBidiMethod(err)
		}
		if len(envelope) < 2 {
			return parsed, &ProtocolEvidenceError{
				Method: "BidiGenerateContent", Path: fmt.Sprintf("$webchannel[%d]", index),
				Detail: "WebChannel envelope 字段不足", Raw: cloneRaw(envelopeRaw),
			}
		}
		aid, err := rawInt64(envelope[0], fmt.Sprintf("$webchannel[%d][0]", index), raw)
		if err != nil {
			return parsed, withBidiMethod(err)
		}
		events, err := ParseBidiServerPayload(envelope[1])
		if err != nil {
			return parsed, err
		}
		s.stateMu.Lock()
		if aid > s.aid {
			s.aid = aid
		}
		s.stateMu.Unlock()
		parsed++
		for _, event := range events {
			s.recordScopedModelAccess(&event)
			if event.Kind == BidiEventSessionResumption {
				token := strings.TrimSpace(event.SessionToken)
				if token != "" {
					s.stateMu.Lock()
					previous := s.latestResumptionToken
					s.stateMu.Unlock()
					if token != previous {
						if err := s.lease.ReplaceResource(previous, token, "bidi-session"); err != nil {
							return parsed, fmt.Errorf("绑定 bidi 恢复令牌: %w", err)
						}
						s.stateMu.Lock()
						s.latestResumptionToken = token
						s.stateMu.Unlock()
					}
				}
			}
			if !s.emit(event) {
				return parsed, s.ctx.Err()
			}
			if event.Kind == BidiEventClosed || event.Kind == BidiEventError {
				return parsed, errBidiWebChannelClosed
			}
		}
	}
	return parsed, nil
}

func (s *BidiSession) recordScopedModelAccess(event *BidiEvent) {
	if event == nil || s.modelAccessScope == "" {
		return
	}
	if event.Kind == BidiEventError && DefinitiveAuthenticationFailure(event.Err) {
		checkedAt := s.finishQualificationAttempt(true)
		if err := s.lease.markAuthenticationRequiredAt(event.Err.Error(), checkedAt); err != nil {
			event.Err = errors.Join(event.Err, err)
		}
		return
	}
	if event.Kind != BidiEventTurnComplete {
		return
	}
	checkedAt := s.finishQualificationAttempt(false)
	if checkedAt.IsZero() {
		return
	}
	if err := s.lease.markAuthenticationValidAt(checkedAt); err != nil {
		slog.Error("Bidi 账户认证状态保存失败", "account", s.accountID, "error", err)
	}
	accountID := s.accountID
	accessScope := s.modelAccessScope
	generation := s.lease.ModelAccessGeneration()
	go func() {
		changed, err := s.lease.pool.MarkModelAccessVerifiedIfGeneration(
			accountID, accessScope, generation, checkedAt,
		)
		if err != nil {
			slog.Error("Bidi 媒体资格保存失败", "account", accountID, "model", s.model, "error", err)
			return
		}
		if changed {
			s.notifyModelAccessChanged()
		}
	}()
}

// beginQualificationAttempt 登记当前双向轮次中的资格请求
func (s *BidiSession) beginQualificationAttempt() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.qualificationPending == 0 {
		s.qualificationCheckedAt = s.nextQualificationCheckedAtLocked()
	}
	s.qualificationPending++
}

// rollbackQualificationAttempt 撤销发送失败的资格请求
func (s *BidiSession) rollbackQualificationAttempt() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.qualificationPending == 0 {
		return
	}
	s.qualificationPending--
	if s.qualificationPending == 0 {
		s.qualificationCheckedAt = time.Time{}
	}
}

// finishQualificationAttempt 结束当前资格轮次并返回其顺序时间
func (s *BidiSession) finishQualificationAttempt(allowUnbound bool) time.Time {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.qualificationPending > 0 {
		checkedAt := s.qualificationCheckedAt
		s.qualificationPending = 0
		s.qualificationCheckedAt = time.Time{}
		return checkedAt
	}
	if allowUnbound {
		last := s.qualificationLastAt
		if leaseCheckedAt := s.lease.CheckedAt(); last.Before(leaseCheckedAt) {
			last = leaseCheckedAt
		}
		checkedAt := last.Add(time.Nanosecond)
		s.qualificationLastAt = checkedAt
		return checkedAt
	}
	return time.Time{}
}

// nextQualificationCheckedAtLocked 返回会话内严格递增的资格顺序时间
func (s *BidiSession) nextQualificationCheckedAtLocked() time.Time {
	last := s.qualificationLastAt
	if leaseCheckedAt := s.lease.CheckedAt(); last.Before(leaseCheckedAt) {
		last = leaseCheckedAt
	}
	checkedAt := time.Now().UTC()
	if !checkedAt.After(last) {
		checkedAt = last.Add(time.Nanosecond)
	}
	s.qualificationLastAt = checkedAt
	return checkedAt
}

func (s *BidiSession) terminate() error {
	zx, err := newWebChannelZX()
	if err != nil {
		return err
	}
	s.stateMu.Lock()
	rid := s.rid
	s.stateMu.Unlock()
	query := url.Values{
		"VER":        []string{"8"},
		"gsessionid": []string{s.gsessionID},
		"SID":        []string{s.sid},
		"RID":        []string{strconv.FormatInt(rid, 10)},
		"TYPE":       []string{"terminate"},
		"zx":         []string{zx},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, bidiWebChannelURL+"?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("创建 bidi WebChannel terminate: %w", err)
	}
	request.Header = s.cloneHeaders()
	request.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("执行 bidi WebChannel terminate: %w", err)
	}
	defer response.Body.Close()
	_, readErr := io.Copy(io.Discard, response.Body)
	return readErr
}

func (s *BidiSession) emit(event BidiEvent) bool {
	select {
	case s.wireEvents <- event:
		return true
	case <-s.ctx.Done():
		return false
	}
}

func (s *BidiSession) mergeCookies(response *http.Response, requestURL string) error {
	setCookies := response.Header.Values("Set-Cookie")
	if len(setCookies) == 0 {
		return nil
	}
	if err := s.lease.MergeSetCookieHeaders(setCookies, requestURL, time.Now()); err != nil {
		return fmt.Errorf("合并 bidi WebChannel Cookie: %w", err)
	}
	state, err := s.lease.ReloadStorageState()
	if err != nil {
		return fmt.Errorf("读取 bidi WebChannel Cookie: %w", err)
	}
	cookie, err := state.CookieHeader(bidiWebChannelURL, time.Now())
	if err != nil {
		return fmt.Errorf("构造 bidi WebChannel Cookie: %w", err)
	}
	s.headerMu.Lock()
	s.headers.Set("Cookie", cookie)
	s.headerMu.Unlock()
	return nil
}

func (s *BidiSession) cloneHeaders() http.Header {
	s.headerMu.RLock()
	defer s.headerMu.RUnlock()
	return s.headers.Clone()
}

func webChannelHeaders(protocolHeaders http.Header) http.Header {
	headers := make(http.Header)
	for _, name := range []string{
		"Accept", "Accept-Language", "User-Agent", "Origin", "Referer", "Sec-Fetch-Dest", "Sec-Fetch-Mode", "Sec-Fetch-Site", "Cookie",
	} {
		for _, value := range protocolHeaders.Values(name) {
			headers.Add(name, value)
		}
	}
	return headers
}

func webChannelAuthHeaders(headers http.Header) string {
	return strings.Join([]string{
		"Authorization:" + headers.Get("Authorization"),
		"X-Goog-Api-Key:" + headers.Get("X-Goog-Api-Key"),
		"X-Goog-AuthUser:" + headers.Get("X-Goog-Authuser"),
		"X-WebChannel-Content-Type:" + JSONProtobufContentType,
	}, "\r\n") + "\r\n"
}

func parseWebChannelHandshake(source io.Reader) (string, error) {
	var frame json.RawMessage
	if err := readWebChannelFrames(source, func(raw json.RawMessage) error {
		if frame == nil {
			frame = cloneRaw(raw)
		}
		return nil
	}); err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	root, err := rawArray(frame, "$handshake", frame)
	if err != nil {
		return "", withBidiMethod(err)
	}
	if len(root) != 1 {
		return "", &ProtocolEvidenceError{Method: "BidiGenerateContent", Path: "$handshake", Detail: "握手 envelope 数量无效", Raw: frame}
	}
	envelope, err := rawArray(root[0], "$handshake[0]", frame)
	if err != nil {
		return "", withBidiMethod(err)
	}
	control, err := rawArray(rawAt(envelope, 1), "$handshake[0][1]", frame)
	if err != nil {
		return "", withBidiMethod(err)
	}
	marker, err := rawString(rawAt(control, 0), "$handshake[0][1][0]", frame)
	if err != nil {
		return "", withBidiMethod(err)
	}
	if marker != "c" {
		return "", &ProtocolEvidenceError{Method: "BidiGenerateContent", Path: "$handshake[0][1][0]", Detail: "握手类型不是 c", Raw: frame}
	}
	sid, err := rawString(rawAt(control, 1), "$handshake[0][1][1]", frame)
	if err != nil || sid == "" {
		if err != nil {
			return "", withBidiMethod(err)
		}
		return "", &ProtocolEvidenceError{Method: "BidiGenerateContent", Path: "$handshake[0][1][1]", Detail: "握手 SID 为空", Raw: frame}
	}
	return sid, nil
}

func parseWebChannelACK(source io.Reader) error {
	var frame json.RawMessage
	if err := readWebChannelFrames(source, func(raw json.RawMessage) error {
		if frame == nil {
			frame = cloneRaw(raw)
		}
		return nil
	}); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	ack, err := rawArray(frame, "$ack", frame)
	if err != nil {
		return withBidiMethod(err)
	}
	if len(ack) != 3 {
		return &ProtocolEvidenceError{Method: "BidiGenerateContent", Path: "$ack", Detail: "WebChannel ACK 字段数量无效", Raw: frame}
	}
	if _, err := rawInt64(ack[0], "$ack[0]", frame); err != nil {
		return withBidiMethod(err)
	}
	if _, err := rawInt64(ack[1], "$ack[1]", frame); err != nil {
		return withBidiMethod(err)
	}
	if _, err := rawInt64(ack[2], "$ack[2]", frame); err != nil {
		return withBidiMethod(err)
	}
	return nil
}

func readWebChannelFrames(source io.Reader, emit func(json.RawMessage) error) error {
	reader := bufio.NewReader(source)
	for {
		lengthLine, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		length, err := strconv.Atoi(strings.TrimSpace(lengthLine))
		if err != nil || length < 0 {
			return fmt.Errorf("bidi WebChannel frame 长度无效: %q", strings.TrimSpace(lengthLine))
		}
		frame := make([]byte, length)
		if _, err := io.ReadFull(reader, frame); err != nil {
			return fmt.Errorf("读取 bidi WebChannel frame: %w", err)
		}
		if !json.Valid(frame) {
			return &ProtocolEvidenceError{Method: "BidiGenerateContent", Path: "$webchannel", Detail: "frame 不是有效 JSON", Raw: cloneRaw(frame)}
		}
		if err := emit(frame); err != nil {
			return err
		}
	}
}

func newWebChannelRID() (int64, error) {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, fmt.Errorf("生成 WebChannel RID: %w", err)
	}
	return int64(binary.BigEndian.Uint32(raw[:])%90000) + 10000, nil
}

func newWebChannelZX() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("生成 WebChannel zx: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}
