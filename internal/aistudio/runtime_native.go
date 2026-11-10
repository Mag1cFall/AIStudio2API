package aistudio

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

// NativeWorker 将单个账户的 Camoufox 或 SpiderMonkey WAA runtime 适配为受保护请求 preparer
type NativeWorker struct {
	accountID string
	// runtimeMu 保护 runtime，重新载入页面后切换为新页面的 runtime
	runtimeMu   sync.RWMutex
	runtime     workerRuntime
	reloadMu    sync.Mutex
	operationMu sync.Mutex
	stateMu     sync.RWMutex
	state       WorkerState
}

// ErrProtectedTransport 表示 SpiderMonkey 后端受保护请求在出口连接上失败，VM 与页面状态不受影响
var ErrProtectedTransport = errors.New("受保护请求网络错误")

var _ ProtectedPreparer = (*NativeWorker)(nil)
var _ ProtocolHeaderProvider = (*NativeWorker)(nil)

// camoufoxRuntime 是 Camoufox 后端的 workerRuntime，重新载入在同一浏览器中进行
type camoufoxRuntime struct {
	*camoufoxnative.Worker
}

// Reload 按官网冷启动重新载入页面：当前标签页空闲时在其中重新载入，有进行中的请求时在新标签页中载入并切换过去，原标签页在请求结束后关闭；返回同一 runtime
func (runtime camoufoxRuntime) Reload(ctx context.Context) (workerRuntime, error) {
	return runtime, runtime.Worker.Reload(ctx)
}

// MergeSetCookieHeaders 把账户在页面之外取得的响应 Cookie 写入浏览器
func (runtime camoufoxRuntime) MergeSetCookieHeaders(ctx context.Context, headers []string, requestURL string) error {
	var state StorageState
	if err := state.MergeSetCookieHeaders(headers, requestURL, time.Now()); err != nil {
		return err
	}
	encoded, err := json.Marshal(state.Cookies)
	if err != nil {
		return err
	}
	return runtime.Worker.SetCookies(ctx, encoded)
}

// ProtocolHeaders 返回页面的公共协议头，X-Goog-Ext-519733851-Bin 与官网页面一样由页面载入时 GetLoggingContext 的响应得出
func (runtime camoufoxRuntime) ProtocolHeaders(ctx context.Context) (http.Header, error) {
	headers, err := runtime.Worker.ProtocolHeaders(ctx)
	if err != nil {
		return nil, err
	}
	encoded, err := encodeJSPBFields(runtime.PageCatalog().Logging)
	if err != nil {
		return nil, fmt.Errorf("编码官网日志上下文: %w", err)
	}
	headers.Set("X-Goog-Ext-519733851-Bin", base64.StdEncoding.EncodeToString(encoded))
	return headers, nil
}

// NewNativeWorker 启动单个账户的 Camoufox WAA runtime
func NewNativeWorker(ctx context.Context, accountID string, options camoufoxnative.Options) (*NativeWorker, error) {
	runtime, err := camoufoxnative.Start(ctx, options)
	if err != nil {
		return nil, err
	}
	runtimeState := runtime.State()
	return &NativeWorker{
		accountID: accountID,
		runtime:   camoufoxRuntime{runtime},
		state: WorkerState{
			AccountID: accountID,
			Phase:     WorkerReady,
			PID:       runtimeState.PID,
			RuntimeID: "native-webdriver-bidi",
			PageURL:   runtimeState.PageURL,
		},
	}, nil
}

// pageGenerator 是在页面界面运行 GenerateContent 的 workerRuntime，页面应用点击 Run 后自己取 proof 并发出请求
type pageGenerator interface {
	Generate(context.Context, camoufoxnative.GenerateTurn) (*camoufoxnative.ProtectedResponse, error)
}

// generator 返回在页面界面运行该请求的 runtime：Camoufox 后端、未带 proof 的 GenerateContent 且最后一个轮次是带文本的 user 轮次
func (worker *NativeWorker) generator(request ProtectedRequest) (pageGenerator, bool) {
	generator, ok := worker.current().(pageGenerator)
	if !ok || request.Proof != "" || !strings.HasSuffix(request.URL, "/GenerateContent") {
		return nil, false
	}
	_, typed := camoufoxnative.GeneratePrompt(request.Body)
	return generator, typed
}

// Prepare 生成 fresh proof 并写入请求指定的 WAA field，请求带 Proof 时写入该 proof；在页面界面运行的 GenerateContent 由页面应用取 proof，只返回公共协议头
func (worker *NativeWorker) Prepare(ctx context.Context, request ProtectedRequest) (PreparedProtectedRequest, error) {
	start, end, err := arrayElementSpan(request.Body, request.ProofField-1)
	if err != nil {
		return PreparedProtectedRequest{}, fmt.Errorf("受保护请求缺少 WAA field %d: %w", request.ProofField, err)
	}
	if _, ok := worker.generator(request); ok {
		headers, err := worker.current().ProtocolHeaders(ctx)
		if err != nil {
			return PreparedProtectedRequest{}, err
		}
		return PreparedProtectedRequest{Body: request.Body, Headers: headers}, nil
	}
	proof := request.Proof
	var headers http.Header
	if proof == "" {
		proof, headers, err = worker.proofAndHeaders(ctx, request.Prompt, request.PagePrompt, strings.HasSuffix(request.URL, "/GenerateContent") && request.PagePrompt != "")
	} else {
		headers, err = worker.current().ProtocolHeaders(ctx)
	}
	if err != nil {
		return PreparedProtectedRequest{}, err
	}
	encoded, _ := json.Marshal(proof)
	body := make([]byte, 0, len(request.Body)-(end-start)+len(encoded))
	body = append(append(append(body, request.Body[:start]...), encoded...), request.Body[end:]...)
	return PreparedProtectedRequest{Body: body, Headers: headers, Proof: proof}, nil
}

// pageRunner 是写入提示词并点击 Run 后取 GenerateContent proof 的 workerRuntime
type pageRunner interface {
	RunProof(context.Context, string, string) (string, error)
}

// proofAndHeaders 把 page 写入页面提示词后为 binding 的 SHA-256 生成 fresh proof，run 时写入后点击 Run，再取公共协议头
func (worker *NativeWorker) proofAndHeaders(ctx context.Context, binding, page string, run bool) (string, http.Header, error) {
	worker.operationMu.Lock()
	defer worker.operationMu.Unlock()
	worker.setPhase(WorkerBusy)
	digest := sha256.Sum256([]byte(binding))
	runtime := worker.current()
	var proof string
	var err error
	if runner, ok := runtime.(pageRunner); ok && run {
		proof, err = runner.RunProof(ctx, fmt.Sprintf("%x", digest), page)
	} else {
		proof, err = runtime.Proof(ctx, fmt.Sprintf("%x", digest), page)
	}
	var headers http.Header
	if err == nil {
		headers, err = runtime.ProtocolHeaders(ctx)
	}
	if err != nil {
		if ctx.Err() != nil {
			worker.setPhase(WorkerReady)
		} else {
			worker.setPhase(WorkerFailed)
		}
		return "", nil, err
	}
	worker.setPhase(WorkerReady)
	return proof, headers, nil
}

// arrayElementSpan 返回紧凑 JSON 顶层数组第 index 个元素的字节范围
func arrayElementSpan(body []byte, index int) (int, int, error) {
	depth, element, start := 0, 0, -1
	inString, escaped := false, false
	for position, char := range body {
		if inString {
			switch {
			case escaped:
				escaped = false
			case char == '\\':
				escaped = true
			case char == '"':
				inString = false
			}
			continue
		}
		switch char {
		case '"':
			inString = true
		case '[', '{':
			depth++
			if depth == 1 {
				continue
			}
		case ']', '}':
			depth--
			if depth == 0 {
				if element == index && start >= 0 {
					return start, position, nil
				}
				return 0, 0, fmt.Errorf("顶层数组只有 %d 个元素", element+1)
			}
		case ',':
			if depth == 1 {
				if element == index {
					return start, position, nil
				}
				element++
				start = -1
				continue
			}
		}
		if depth >= 1 && element == index && start < 0 {
			start = position
		}
	}
	return 0, 0, fmt.Errorf("JSON 顶层数组不完整")
}

// pageCountTokensDelay 是官网页面在 GenerateContent 发出后统计用户轮次 token 的时延
const pageCountTokensDelay = 2200 * time.Millisecond

// SendProtected 经账户 WAA runtime 流式发送已准备的请求，响应带实际写入的 proof：Camoufox 后端未带 proof 的 GenerateContent 在页面界面运行，请求头、proof 与随后的 CountTokens 都由页面应用产生；其余 GenerateContent 带官网点击运行时的 Priority: u=0，发出 pageCountTokensDelay 后由 runtime 以相同请求头统计页面提示词的 token
func (worker *NativeWorker) SendProtected(ctx context.Context, request ProtectedRequest) (*RPCResponse, error) {
	if generator, ok := worker.generator(request); ok {
		return worker.generate(ctx, generator, request)
	}
	if body, ok := countTokensBody(request); ok {
		ctx = withFollowUp(ctx, followUp{URL: makerSuiteRPCURL + "CountTokens", Body: body, Delay: pageCountTokensDelay})
	}
	headers := request.Headers
	if strings.HasSuffix(request.URL, "/GenerateContent") {
		headers = headers.Clone()
		headers.Set("Priority", "u=0")
	}
	response, err := worker.current().SendProtected(ctx, request.URL, headers, request.Body)
	if err != nil {
		var transport *url.Error
		if errors.As(err, &transport) {
			return nil, fmt.Errorf("%w: %w", ErrProtectedTransport, err)
		}
		if ctx.Err() == nil {
			worker.setPhase(WorkerFailed)
		}
		return nil, err
	}
	return &RPCResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header,
		Body:       response.Body,
		Proof:      request.Proof,
	}, nil
}

// generate 在页面界面运行 GenerateContent，proof 绑定 request.Prompt 的 SHA-256
func (worker *NativeWorker) generate(ctx context.Context, generator pageGenerator, request ProtectedRequest) (*RPCResponse, error) {
	start, end, err := arrayElementSpan(request.Body, request.ProofField-1)
	if err != nil {
		return nil, fmt.Errorf("受保护请求缺少 WAA field %d: %w", request.ProofField, err)
	}
	digest := sha256.Sum256([]byte(request.Prompt))
	worker.setPhase(WorkerBusy)
	response, err := generator.Generate(ctx, camoufoxnative.GenerateTurn{Body: request.Body, ProofStart: start, ProofEnd: end, Digest: fmt.Sprintf("%x", digest)})
	if err != nil {
		if ctx.Err() != nil {
			worker.setPhase(WorkerReady)
		} else {
			worker.setPhase(WorkerFailed)
		}
		return nil, err
	}
	worker.setPhase(WorkerReady)
	return &RPCResponse{StatusCode: response.StatusCode, Header: response.Header, Body: response.Body, Proof: response.Proof}, nil
}

// countTokensBody 按官网页面对用户轮次的统计，以 GenerateContent 的模型构造页面提示词的 CountTokens 正文；不是带页面提示词的 GenerateContent 时返回 false
func countTokensBody(generate ProtectedRequest) ([]byte, bool) {
	if !strings.HasSuffix(generate.URL, "/GenerateContent") || generate.PagePrompt == "" {
		return nil, false
	}
	var fields []json.RawMessage
	var model string
	if json.Unmarshal(generate.Body, &fields) != nil || len(fields) == 0 || json.Unmarshal(fields[0], &model) != nil {
		return nil, false
	}
	body, err := json.Marshal([]any{model, []any{[]any{[]any{[]any{nil, generate.PagePrompt}}, "user"}}})
	return body, err == nil
}

// BrowserStorageState 返回账户 WAA runtime 当前 Cookie 状态
func (worker *NativeWorker) BrowserStorageState(ctx context.Context) (StorageState, error) {
	encoded, err := worker.current().StorageCookies(ctx)
	if err != nil {
		return StorageState{}, err
	}
	var cookies []StateCookie
	if err := json.Unmarshal(encoded, &cookies); err != nil {
		return StorageState{}, fmt.Errorf("解析浏览器 Cookie: %w", err)
	}
	state := StorageState{Cookies: cookies}
	if err := state.Validate(); err != nil {
		return StorageState{}, err
	}
	return state, nil
}

// MergeSetCookieHeaders 把账户在页面之外取得的响应 Cookie 写入当前页面
func (worker *NativeWorker) MergeSetCookieHeaders(ctx context.Context, headers []string, requestURL string) error {
	return worker.current().MergeSetCookieHeaders(ctx, headers, requestURL)
}

// PageCatalog 返回账户当前页面载入时目录 RPC 的响应正文
func (worker *NativeWorker) PageCatalog() camoufoxnative.PageCatalog {
	return worker.current().PageCatalog()
}

// ProtocolHeaders 返回当前账户官网请求的动态公共头
func (worker *NativeWorker) ProtocolHeaders(ctx context.Context, _ string) (http.Header, error) {
	return worker.current().ProtocolHeaders(ctx)
}

// current 返回账户当前页面的 runtime
func (worker *NativeWorker) current() workerRuntime {
	worker.runtimeMu.RLock()
	defer worker.runtimeMu.RUnlock()
	return worker.runtime
}

// Reload 按官网冷启动重新载入账户页面，新页面就绪后切换，切换前已发出的请求在原页面上完成
func (worker *NativeWorker) Reload(ctx context.Context) error {
	worker.reloadMu.Lock()
	defer worker.reloadMu.Unlock()
	previous := worker.current()
	next, err := previous.Reload(ctx)
	if err != nil || next == previous {
		return err
	}
	worker.operationMu.Lock()
	if phase := worker.State().Phase; phase == WorkerClosing || phase == WorkerClosed {
		worker.operationMu.Unlock()
		return errors.Join(errors.New("WAA runtime 已关闭"), next.Close())
	}
	worker.runtimeMu.Lock()
	worker.runtime = next
	worker.runtimeMu.Unlock()
	worker.operationMu.Unlock()
	return previous.Close()
}

// State 返回账户 WAA runtime 状态
func (worker *NativeWorker) State() WorkerState {
	worker.stateMu.RLock()
	defer worker.stateMu.RUnlock()
	return worker.state
}

// Close 关闭账户 WAA runtime
func (worker *NativeWorker) Close() error {
	worker.operationMu.Lock()
	defer worker.operationMu.Unlock()
	worker.setPhase(WorkerClosing)
	err := worker.current().Close()
	if err == nil {
		worker.setPhase(WorkerClosed)
	}
	return err
}

// setPhase 更新账户 WAA runtime 阶段，进入关闭后只接受 WorkerClosed
func (worker *NativeWorker) setPhase(phase WorkerPhase) {
	worker.stateMu.Lock()
	defer worker.stateMu.Unlock()
	if current := worker.state.Phase; (current == WorkerClosing || current == WorkerClosed) && phase != WorkerClosed {
		return
	}
	worker.state.Phase = phase
}
