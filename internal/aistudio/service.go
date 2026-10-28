package aistudio

import (
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

// PooledService 在账户租约内调用协议客户端
type PooledService struct {
	pool   *AccountPool
	client *Client
	// BuildNativeNonstream 让非流式请求优先使用具备资格的 Build 通道
	BuildNativeNonstream bool
	// YouTubeLinks 把 user 文本中的 YouTube 链接作为视频外部媒体发送
	YouTubeLinks bool
	// omniVideos 记录本进程中尚无结果文件的 Omni 视频任务：值为 nil 时仍在生成，为 error 时生成失败
	omniVideos sync.Map
	// inlineMediaMu 保护 inlineMediaUses
	inlineMediaMu sync.Mutex
	// inlineMediaUses 是本进程进行中的请求引用的内联附件文件 ID 与引用数
	inlineMediaUses map[string]int
}

// PoolRequestContextProvider 从租约账户读取协议上下文
type PoolRequestContextProvider struct {
	pool *AccountPool
}

// ProtectedPreparer 为一次请求写入 fresh WAA proof 并通过账户固定指纹浏览器发送
type ProtectedPreparer interface {
	Prepare(context.Context, ProtectedRequest) (PreparedProtectedRequest, error)
	BrowserStorageState(context.Context) (StorageState, error)
	SendProtected(context.Context, ProtectedRequest) (*RPCResponse, error)
}

// ProtectedPreparerProvider 按账户返回 lazy WAA preparer
type ProtectedPreparerProvider interface {
	Worker(context.Context, string, string) (ProtectedPreparer, error)
}

// ProtectedPreparerProviderFunc 将函数适配为 ProtectedPreparerProvider
type ProtectedPreparerProviderFunc func(context.Context, string, string) (ProtectedPreparer, error)

// Worker 返回账户的 lazy WAA preparer
func (f ProtectedPreparerProviderFunc) Worker(ctx context.Context, accountID string, modelID string) (ProtectedPreparer, error) {
	return f(ctx, accountID, modelID)
}

// WorkerProtectedTransportOptions 定义受保护请求的 proof 与 HTTP 依赖
type WorkerProtectedTransportOptions struct {
	Transport    *MakerSuiteHTTPTransport
	Workers      ProtectedPreparerProvider
	SetupTimeout time.Duration
}

// WorkerProtectedTransport 将 fresh proof 交给同租约 HTTP 传输
type WorkerProtectedTransport struct {
	transport    *MakerSuiteHTTPTransport
	workers      ProtectedPreparerProvider
	setupTimeout time.Duration
}

var _ Service = (*PooledService)(nil)
var _ RequestContextProvider = (*PoolRequestContextProvider)(nil)
var _ ProtectedTransport = (*WorkerProtectedTransport)(nil)
var _ VideoProtectedTransport = (*WorkerProtectedTransport)(nil)
var _ TitleProtectedTransport = (*WorkerProtectedTransport)(nil)

// NewPooledService 创建多账户协议服务
func NewPooledService(pool *AccountPool, client *Client) (*PooledService, error) {
	if pool == nil {
		return nil, fmt.Errorf("AI Studio account pool 不能为空")
	}
	if client == nil {
		return nil, fmt.Errorf("AI Studio client 不能为空")
	}
	for accountID, tier := range pool.cachedBenefitTiers() {
		client.setBenefitTier(accountID, tier)
	}
	for accountID, catalog := range pool.takeCachedCatalogs() {
		client.setModelCatalog(accountID, catalog)
	}
	return &PooledService{pool: pool, client: client}, nil
}

// NewPoolRequestContextProvider 创建账户协议上下文提供者
func NewPoolRequestContextProvider(pool *AccountPool) (*PoolRequestContextProvider, error) {
	if pool == nil {
		return nil, fmt.Errorf("AI Studio account pool 不能为空")
	}
	return &PoolRequestContextProvider{pool: pool}, nil
}

// NewWorkerProtectedTransport 创建基于 lazy WAA preparer 的受保护传输
func NewWorkerProtectedTransport(options WorkerProtectedTransportOptions) (*WorkerProtectedTransport, error) {
	if options.Transport == nil {
		return nil, fmt.Errorf("MakerSuite HTTP transport 不能为空")
	}
	if options.Workers == nil {
		return nil, fmt.Errorf("WAA preparer provider 不能为空")
	}
	if options.SetupTimeout <= 0 {
		return nil, fmt.Errorf("Bidi setup timeout 必须是正数时长")
	}
	return &WorkerProtectedTransport{
		transport: options.Transport, workers: options.Workers, setupTimeout: options.SetupTimeout,
	}, nil
}

// DoProtected 写入 fresh proof 后通过 Camoufox 发送 GenerateContent、Build 代理或写代码助手请求，写入页面输入框的提示词为最后一个轮次的文本
func (t *WorkerProtectedTransport) DoProtected(ctx context.Context, request GenerateRequest, rpc RPCRequest) (*RPCResponse, error) {
	var prompt string
	var err error
	proofField := 5
	switch rpc.Method {
	case buildProxyStreamedMethod, buildProxyUnaryMethod:
		prompt, err = buildBindingPrompt(rpc.Body)
		proofField = buildProofField
	case assistantOfflineMethod:
		prompt, err = bindingPrompt(request)
		proofField = assistantProofField
	default:
		prompt, err = bindingPrompt(request)
	}
	if err != nil {
		return nil, err
	}
	page := contentsText(request.Contents[max(len(request.Contents)-1, 0):])
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	return t.doBrowserPrepared(ctx, prompt, page, proofField, AccountSelection{
		ModelID: modelID, Method: "generateContent", AccountID: strings.TrimSpace(request.AccountID),
	}, rpc)
}

func (t *WorkerProtectedTransport) doBrowserPrepared(
	ctx context.Context,
	prompt string,
	page string,
	proofField int,
	selection AccountSelection,
	rpc RPCRequest,
) (*RPCResponse, error) {
	lease, worker, rpc, err := t.prepareProtectedRequest(ctx, prompt, page, proofField, selection, rpc)
	if err != nil {
		return nil, err
	}
	browserState, err := worker.BrowserStorageState(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取浏览器 Cookie: %w", err)
	}
	authorization, err := t.transport.signer.Authorization(browserState)
	if err != nil {
		return nil, err
	}
	headers := rpc.Header.Clone()
	headers.Set("Authorization", authorization)
	if lease.CoolingDown(selectionAccessScope(selection)) {
		return nil, ErrAccountCoolingDown
	}
	reportRequestPhase(ctx, RequestPhaseSendingUpstream)
	response, err := worker.SendProtected(ctx, ProtectedRequest{
		URL: rpc.URL, Headers: headers, Body: rpc.Body, PagePrompt: page,
	})
	if err != nil {
		return nil, err
	}
	saving := &cookieSavingBody{ReadCloser: response.Body, done: make(chan struct{})}
	go func() {
		defer close(saving.done)
		browserState, err := worker.BrowserStorageState(ctx)
		if err != nil {
			saving.err = fmt.Errorf("导出浏览器 Cookie: %w", err)
			return
		}
		if err := lease.ReplaceCookies(browserState.Cookies); err != nil {
			saving.err = fmt.Errorf("保存浏览器 Cookie: %w", err)
		}
	}()
	response.Body = saving
	reportRequestPhase(ctx, RequestPhaseStreaming)
	return response, nil
}

// cookieSavingBody 是在后台写回响应 Cookie 的上游正文，关闭时等待写回完成并返回其错误
type cookieSavingBody struct {
	io.ReadCloser
	done chan struct{}
	err  error
}

// Close 关闭上游正文并返回 Cookie 写回的结果
func (body *cookieSavingBody) Close() error {
	closeErr := body.ReadCloser.Close()
	<-body.done
	return errors.Join(closeErr, body.err)
}

func (t *WorkerProtectedTransport) prepareProtectedRequest(
	ctx context.Context,
	prompt string,
	page string,
	proofField int,
	selection AccountSelection,
	rpc RPCRequest,
) (*AccountLease, ProtectedPreparer, RPCRequest, error) {
	lease, ok := AccountLeaseFromContext(ctx)
	if !ok {
		return nil, nil, RPCRequest{}, fmt.Errorf("受保护请求缺少账户租约")
	}
	if err := validateLeaseSelection(lease, selection); err != nil {
		return nil, nil, RPCRequest{}, err
	}
	worker, err := t.workers.Worker(ctx, lease.Account().ID, selection.ModelID)
	if err != nil {
		return nil, nil, RPCRequest{}, fmt.Errorf("获取账户 WAA preparer: %w", err)
	}
	reportRequestPhase(ctx, RequestPhasePreparingWAA)
	prepared, err := worker.Prepare(ctx, ProtectedRequest{
		URL: rpc.URL, Headers: rpc.Header.Clone(), Body: rpc.Body,
		Prompt: prompt, PagePrompt: page, ProofField: proofField,
	})
	if err != nil {
		return nil, nil, RPCRequest{}, fmt.Errorf("准备 fresh WAA proof: %w", err)
	}
	if prepared.Headers == nil || len(prepared.Body) == 0 {
		return nil, nil, RPCRequest{}, fmt.Errorf("WAA preparer 返回空请求")
	}
	requestHeaders := rpc.Header
	rpc.AccountID = lease.Account().ID
	rpc.Body = prepared.Body
	rpc.Header = prepared.Headers.Clone()
	for name, values := range requestHeaders {
		rpc.Header.Del(name)
		for _, value := range values {
			rpc.Header.Add(name, value)
		}
	}
	rpc.Header.Set("Content-Type", JSONProtobufContentType)
	return lease, worker, rpc, nil
}

// DoProtectedVideo 写入 Veo fresh proof 后经 Worker 页面发送请求，页面输入框的提示词为视频提示词
func (t *WorkerProtectedTransport) DoProtectedVideo(ctx context.Context, request VideoRequest, rpc RPCRequest) (*RPCResponse, error) {
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	return t.doBrowserPrepared(ctx, request.Prompt, request.Prompt, 8, AccountSelection{
		ModelID: modelID, Method: "predictLongRunning", AccountID: strings.TrimSpace(request.AccountID),
	}, rpc)
}

// DoProtectedTitle 写入 GenerateTitle fresh proof 后经 Worker 页面发送请求，binding 为提示词与回答以空格连接，页面输入框为空
func (t *WorkerProtectedTransport) DoProtectedTitle(ctx context.Context, request TitleRequest, rpc RPCRequest) (*RPCResponse, error) {
	return t.doBrowserPrepared(ctx, request.Prompt+" "+request.Answer, "", 3, AccountSelection{ModelID: request.Model}, rpc)
}

func bindingPrompt(request GenerateRequest) (string, error) {
	if len(request.Contents) == 0 {
		return "", fmt.Errorf("GenerateContent contents 不能为空")
	}
	values := make([]string, 0)
	for _, content := range request.Contents {
		for _, part := range content.Parts {
			switch {
			case part.Text != "":
				values = append(values, part.Text)
			case part.InlineData != nil:
				values = append(values, base64.StdEncoding.EncodeToString(part.InlineData.Data))
			case part.File != nil:
				values = append(values, part.File.ID)
			default:
				values = append(values, "")
			}
		}
	}
	return strings.Join(values, " "), nil
}

// contentsText 返回各 Part 的文本以空格连接，作为写入页面输入框的提示词
func contentsText(contents []Content) string {
	values := make([]string, 0)
	for _, content := range contents {
		for _, part := range content.Parts {
			if part.Text != "" {
				values = append(values, part.Text)
			}
		}
	}
	return strings.Join(values, " ")
}

// RequestContext 返回账户时区
func (p *PoolRequestContextProvider) RequestContext(_ context.Context, accountID string) (RequestContext, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return RequestContext{}, fmt.Errorf("AI Studio 请求上下文缺少账户 ID")
	}
	p.pool.mu.Lock()
	account := p.pool.byID[accountID]
	timezone := ""
	if account != nil {
		timezone = account.Config.Timezone
	}
	p.pool.mu.Unlock()
	if account == nil {
		return RequestContext{}, fmt.Errorf("账户不存在: %s", accountID)
	}
	return RequestContext{Timezone: timezone}, nil
}

// Models 刷新可用账户并返回实时模型并集
func (s *PooledService) Models(ctx context.Context) ([]Model, error) {
	if lease, ok := AccountLeaseFromContext(ctx); ok {
		return s.modelsForLease(ctx, lease)
	}
	statuses := s.pool.Status()
	targets := make([]AccountStatus, 0, len(statuses))
	for _, status := range statuses {
		if status.Enabled && (status.State == AccountReady || status.State == AccountBusy) {
			targets = append(targets, status)
		}
	}
	models := make([]Model, 0)
	available := 0
	failures := make([]error, 0)
	for _, result := range s.refreshModelCatalogs(ctx, targets) {
		models = mergeModels(models, result.models)
		if result.available {
			available++
		}
		if result.err != nil {
			failures = append(failures, result.err)
		}
	}
	if ctx.Err() != nil {
		failures = append(failures, ctx.Err())
	}
	if available == 0 {
		if len(failures) > 0 {
			return nil, errors.Join(failures...)
		}
		return nil, ErrNoEligibleAccount
	}
	return models, nil
}

// RefreshAccountModels 占用指定账户的一个普通槽位刷新其权益与模型目录，不受账户启用、调度状态与冷却限制
func (s *PooledService) RefreshAccountModels(ctx context.Context, accountID string) ([]Model, error) {
	accountID = strings.TrimSpace(accountID)
	lease, err := s.pool.AcquireFor(ctx, AccountSelection{AccountID: accountID, anyState: true})
	if err != nil {
		return nil, err
	}
	models, requestErr := s.modelsForLease(ContextWithAccountLease(ctx, lease), lease)
	releaseErr := lease.Release()
	if requestErr != nil {
		failure := fmt.Errorf("刷新账户 %s 的模型目录: %w", accountID, errors.Join(requestErr, releaseErr))
		return nil, failure
	}
	if releaseErr != nil {
		return models, fmt.Errorf("释放账户 %s 的模型目录租约: %w", accountID, releaseErr)
	}
	return models, nil
}

// AccountRefresh 为一次账户状态刷新前后的权益与模型数，ModelsChanged 表示该账户的目录条目有变化
type AccountRefresh struct {
	PreviousTier   BenefitTier
	Tier           BenefitTier
	PreviousModels int
	Models         int
	ModelsChanged  bool
	Skipped        bool
}

// RefreshAccountState 读取账户权益与模型目录，登录失效时标记账户需要登录；wait 为 true 时等待独占租约，否则只用就绪账户的空闲槽位
func (s *PooledService) RefreshAccountState(ctx context.Context, accountID string, wait bool) (AccountRefresh, error) {
	accountID = strings.TrimSpace(accountID)
	var lease *AccountLease
	var err error
	if wait {
		lease, err = s.pool.AcquireAccount(ctx, accountID)
	} else {
		lease, _, err = s.pool.TryAcquireFor(ctx, AccountSelection{AccountID: accountID})
	}
	if lease == nil {
		return AccountRefresh{Skipped: err == nil}, err
	}
	account := lease.Account()
	s.pool.mu.Lock()
	refresh := AccountRefresh{PreviousTier: account.BenefitTier, PreviousModels: len(account.Models)}
	previous := slices.Clone(account.Models)
	s.pool.mu.Unlock()
	models, err := s.modelsForLease(ContextWithAccountLease(ctx, lease), lease)
	if err != nil {
		return refresh, errors.Join(err, lease.Release())
	}
	s.pool.mu.Lock()
	refresh.Tier = account.BenefitTier
	refresh.ModelsChanged = !reflect.DeepEqual(previous, account.Models)
	s.pool.mu.Unlock()
	refresh.Models = len(models)
	return refresh, lease.Release()
}

// CachedModels 返回启用账户最近同步目录的并集
func (s *PooledService) CachedModels() []Model {
	s.pool.mu.Lock()
	defer s.pool.mu.Unlock()
	models := make([]Model, 0)
	for _, account := range s.pool.accounts {
		if account != nil && account.Config.Enabled {
			models = mergeModels(models, account.Models)
		}
	}
	return mergeModels(models, s.pool.buildOnlyModelsLocked())
}

type accountModelsResult struct {
	models    []Model
	available bool
	err       error
}

func (s *PooledService) refreshModelCatalogs(ctx context.Context, statuses []AccountStatus) []accountModelsResult {
	results := make(chan accountModelsResult, len(statuses))
	var refreshes sync.WaitGroup
	for _, status := range statuses {
		refreshes.Add(1)
		go func(status AccountStatus) {
			defer refreshes.Done()
			results <- s.modelsForStatus(ctx, status)
		}(status)
	}
	go func() {
		refreshes.Wait()
		close(results)
	}()
	collected := make([]accountModelsResult, 0, len(statuses))
	for result := range results {
		collected = append(collected, result)
	}
	return collected
}

func (s *PooledService) modelsForStatus(ctx context.Context, status AccountStatus) accountModelsResult {
	cached := s.cachedModels(status.ID)
	if status.State == AccountBusy {
		if len(cached) > 0 {
			return accountModelsResult{models: cached, available: true}
		}
		return accountModelsResult{err: fmt.Errorf("账户 %s 正在使用且没有缓存模型目录", status.ID)}
	}
	lease, err := s.pool.AcquireFor(ctx, AccountSelection{AccountID: status.ID})
	if err != nil {
		return accountModelsResult{
			models: cached, available: len(cached) > 0,
			err: fmt.Errorf("获取账户 %s 的模型目录租约: %w", status.ID, err),
		}
	}
	accountModels, requestErr := s.modelsForLease(ContextWithAccountLease(ctx, lease), lease)
	releaseErr := lease.Release()
	if requestErr != nil {
		failure := fmt.Errorf("刷新账户 %s 的模型目录: %w", status.ID, errors.Join(requestErr, releaseErr))
		return accountModelsResult{models: cached, available: len(cached) > 0, err: failure}
	}
	if releaseErr != nil {
		return accountModelsResult{
			models: accountModels, available: true,
			err: fmt.Errorf("释放账户 %s 的模型目录租约: %w", status.ID, releaseErr),
		}
	}
	return accountModelsResult{models: accountModels, available: true}
}

func (s *PooledService) cachedModels(accountID string) []Model {
	s.pool.mu.Lock()
	defer s.pool.mu.Unlock()
	account := s.pool.byID[accountID]
	if account == nil {
		return nil
	}
	return cloneAccountModels(account.Models)
}

// DefinitiveAuthenticationFailure 判断上游是否明确要求重新认证
func DefinitiveAuthenticationFailure(err error) bool {
	if errors.Is(err, camoufoxnative.ErrAuthenticationRequired) {
		return true
	}
	var rpcError *RPCError
	return errors.As(err, &rpcError) && !driveAuthorizationMissing(err) &&
		(rpcError.StatusCode == http.StatusUnauthorized || rpcError.Code == 16)
}

// DefinitiveWAARuntimeFailure 判断上游是否明确拒绝当前 WAA 运行态
func DefinitiveWAARuntimeFailure(err error) bool {
	var rpcError *RPCError
	return errors.As(err, &rpcError) && modelBoundRPCMethod(rpcError.Method) &&
		rpcError.StatusCode == http.StatusNotFound && rpcError.Code == 5 &&
		strings.Contains(rpcError.Message, "Ambiguous request for service ''")
}

func modelBoundRPCMethod(method string) bool {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "generatecontent", "generatevideo", "bidigeneratecontent":
		return true
	default:
		return false
	}
}

func (s *PooledService) markRetryableFailure(lease *AccountLease, modelAccessScope string, err error) error {
	accountID := lease.Account().ID
	if DefinitiveAuthenticationFailure(err) {
		return lease.MarkAuthenticationRequired(err.Error())
	}
	if errors.Is(err, ErrAccountCoolingDown) {
		return nil
	}
	now := time.Now()
	cooldown, ok := QuotaCooldownForError(err, now)
	if !ok {
		cooldown = QuotaCooldown{Until: now.Add(30 * time.Second), Reason: err.Error()}
	}
	if cooldown.Global {
		modelAccessScope = ""
	}
	_, markErr := s.pool.MarkQuotaCooldownIfGeneration(
		accountID, modelAccessScope, lease.ModelAccessGeneration(), lease.CheckedAt(), cooldown,
	)
	return markErr
}

func (s *PooledService) modelsForLease(ctx context.Context, lease *AccountLease) ([]Model, error) {
	account := lease.Account()
	leaseCtx := ContextWithAccountLease(ctx, lease)
	if err := s.client.EnsureTermsAccepted(leaseCtx, account.ID); err != nil {
		return nil, s.catalogFailure(lease, err)
	}
	buildEnabled := s.pool.BuildEnabled()
	var tier BenefitTier
	var models, buildModels []Model
	var tierErr, modelsErr, buildErr error
	var reads sync.WaitGroup
	reads.Go(func() { tier, tierErr = s.client.BenefitTierForAccount(leaseCtx, account.ID) })
	reads.Go(func() { models, modelsErr = s.client.ModelsForAccount(leaseCtx, account.ID) })
	if buildEnabled {
		reads.Go(func() { buildModels, buildErr = s.client.BuildModelsForAccount(leaseCtx, account.ID) })
	}
	reads.Wait()
	if err := cmp.Or(tierErr, modelsErr); err != nil {
		return nil, s.catalogFailure(lease, err)
	}
	if err := errors.Join(
		lease.MarkAuthenticationValid(),
		s.pool.ClearCooldownIfGeneration(
			account.ID, "", lease.ModelAccessGeneration(), lease.CheckedAt(),
		),
	); err != nil {
		return nil, err
	}
	if err := s.pool.SetCatalog(account.ID, tier, models); err != nil {
		return nil, err
	}
	if buildEnabled && buildErr == nil {
		if err := s.pool.SetBuildCatalog(account.ID, buildModels); err != nil {
			return models, err
		}
	}
	if err := s.pool.SaveCatalogCache(account.ID, s.client.modelCatalogSource(account.ID)); err != nil {
		slog.Warn("账户模型目录缓存保存失败", "account", account.ID, "error", err)
	}
	if buildEnabled && buildErr != nil {
		return models, fmt.Errorf("读取 Build 模型目录: %w", buildErr)
	}
	return models, nil
}

// catalogFailure 把认证失效的账户标为需要登录，其余失败记为该账户最近一次目录读取错误
func (s *PooledService) catalogFailure(lease *AccountLease, err error) error {
	if DefinitiveAuthenticationFailure(err) {
		return errors.Join(err, lease.MarkAuthenticationRequired(err.Error()))
	}
	s.pool.recordCatalogFailure(lease.Account().ID, err)
	return err
}

// CountTokens 使用支持目标模型的独占账户计数
func (s *PooledService) CountTokens(ctx context.Context, request TokenCountRequest) (TokenCount, error) {
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	if modelID == "" {
		return TokenCount{}, fmt.Errorf("%w: CountTokens model 不能为空", ErrInvalidArgument)
	}
	request.System, request.Contents = normalizeTurns(request.System, request.Contents)
	if s.YouTubeLinks {
		request.Contents = attachYouTubeLinks(request.Contents)
	}
	if len(request.Contents) == 0 {
		return TokenCount{}, fmt.Errorf("%w: 请求没有系统提示或对话内容", ErrInvalidArgument)
	}
	modelAccessScope := ModelAccessKey("count-tokens", modelID)
	selection := AccountSelection{ModelID: modelID, ModelAccessScope: modelAccessScope, Method: "countTokens"}
	if mixedMediaResolution(request.MediaResolution, request.Contents) {
		selection.Channel = ChannelBuild
	}
	var count TokenCount
	var requestErr error
	for attempt := 0; attempt < accountAttemptLimit(s.pool, false); attempt++ {
		lease, owned, err := resolveAccountLease(ctx, s.pool, selection)
		if err != nil {
			if requestErr != nil && errors.Is(err, ErrNoEligibleAccount) {
				return count, requestErr
			}
			return TokenCount{}, err
		}
		accountID := lease.Account().ID
		count, requestErr = s.client.CountTokensForAccount(ContextWithAccountLease(ctx, lease), accountID, request)
		var stateErr error
		retryable := retryableAccountError(requestErr)
		if requestErr == nil {
			stateErr = errors.Join(
				lease.MarkAuthenticationValid(),
				s.pool.ClearCooldownIfGeneration(
					accountID, modelAccessScope, lease.ModelAccessGeneration(), lease.CheckedAt(),
				),
			)
		} else if DefinitiveAuthenticationFailure(requestErr) {
			stateErr = lease.MarkAuthenticationRequired(requestErr.Error())
		} else if retryable {
			stateErr = s.markRetryableFailure(lease, modelAccessScope, requestErr)
		}
		var releaseErr error
		if owned {
			releaseErr = lease.Release()
		}
		if requestErr == nil || !retryable {
			return count, errors.Join(requestErr, stateErr, releaseErr)
		}
		requestErr = errors.Join(requestErr, stateErr, releaseErr)
		if stateErr != nil {
			return count, requestErr
		}
		if releaseErr != nil {
			return count, requestErr
		}
	}
	return count, requestErr
}

// ValidateGenerateRequest 在选择账户前校验与上游通道无关的生成参数
func (s *PooledService) ValidateGenerateRequest(request GenerateRequest) error {
	if strings.TrimPrefix(strings.TrimSpace(request.Model), "models/") == "" {
		return fmt.Errorf("%w: GenerateContent model 不能为空", ErrInvalidArgument)
	}
	request.System, request.Contents = normalizeTurns(request.System, request.Contents)
	if len(request.Contents) == 0 {
		return fmt.Errorf("%w: 请求没有系统提示或对话内容", ErrInvalidArgument)
	}
	request, _, err := prepareToolRequest(request)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	return s.client.validateGenerateRequest(request)
}

// Generate 使用支持目标模型的独占账户生成事件流
func (s *PooledService) Generate(ctx context.Context, request GenerateRequest) (<-chan Event, error) {
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	if modelID == "" {
		return nil, fmt.Errorf("%w: GenerateContent model 不能为空", ErrInvalidArgument)
	}
	request.System, request.Contents = normalizeTurns(request.System, request.Contents)
	if s.YouTubeLinks {
		request.Contents = attachYouTubeLinks(request.Contents)
	}
	if len(request.Contents) == 0 {
		return nil, fmt.Errorf("%w: 请求没有系统提示或对话内容", ErrInvalidArgument)
	}
	resourceID, err := s.pool.ResourceIDForContents(ctx, request.Contents)
	if err != nil {
		return nil, err
	}
	selection := AccountSelection{
		ModelID:    modelID,
		Method:     "generateContent",
		AccountID:  strings.TrimSpace(request.AccountID),
		ResourceID: resourceID,
	}
	if RequestNeedsBuild(request) || request.Unary && s.BuildNativeNonstream {
		selection.PreferredChannel = ChannelBuild
	}
	pinned := selection.AccountID != "" || selection.ResourceID != ""
	if _, ok := AccountLeaseFromContext(ctx); ok {
		pinned = true
	}
	var requestErr error
	for attempt := 0; attempt < accountAttemptLimit(s.pool, pinned); attempt++ {
		lease, owned, err := resolveAccountLease(ctx, s.pool, selection)
		if err != nil {
			if requestErr != nil && errors.Is(err, ErrNoEligibleAccount) {
				return nil, requestErr
			}
			return nil, err
		}
		accountID := lease.Account().ID
		request.AccountID = accountID
		events, err := s.client.Generate(ContextWithAccountLease(ctx, lease), request)
		if err == nil {
			if !owned {
				return events, nil
			}
			forwarded := make(chan Event, 8)
			go forwardEventsWithLease(ctx, events, forwarded, lease, s.pool, modelID)
			return forwarded, nil
		}
		requestErr = err
		retryable := retryableAccountError(err)
		var stateErr error
		if owned && retryable {
			stateErr = s.markRetryableFailure(lease, lease.CooldownScope(modelID), err)
		}
		if owned {
			requestErr = errors.Join(requestErr, stateErr, lease.Release())
		}
		if !retryable {
			return nil, requestErr
		}
		if !owned {
			return nil, requestErr
		}
		if stateErr != nil {
			return nil, requestErr
		}
	}
	return nil, requestErr
}

func accountAttemptLimit(pool *AccountPool, pinned bool) int {
	if pinned {
		return 1
	}
	eligible := 0
	for _, status := range pool.Status() {
		if status.Enabled && (status.State == AccountReady || status.State == AccountBusy) {
			eligible++
		}
	}
	if eligible > 0 {
		return eligible
	}
	return 1
}

func retryableAccountError(err error) bool {
	if driveAuthorizationMissing(err) {
		return false
	}
	if errors.Is(err, ErrAccountCoolingDown) {
		return true
	}
	var rpcError *RPCError
	if !errors.As(err, &rpcError) {
		return false
	}
	return rpcError.StatusCode == http.StatusUnauthorized || rpcError.StatusCode == http.StatusForbidden ||
		rpcError.StatusCode == http.StatusTooManyRequests || rpcError.StatusCode >= http.StatusInternalServerError
}

// forwardEventsWithLease 转发事件并在结束或取消时释放账户租约
func forwardEventsWithLease(
	ctx context.Context,
	source <-chan Event,
	destination chan<- Event,
	lease *AccountLease,
	pool *AccountPool,
	modelID string,
) {
	defer close(destination)
	defer func() {
		if err := lease.Release(); err != nil {
			select {
			case destination <- Event{Kind: EventError, Err: err}:
			case <-ctx.Done():
			}
		}
	}()
	terminal := false
	accountID := lease.Account().ID
	accessGeneration := lease.ModelAccessGeneration()
	checkedAt := lease.CheckedAt()
	for {
		var event Event
		select {
		case next, ok := <-source:
			if !ok {
				return
			}
			event = next
		case <-ctx.Done():
			return
		}
		if terminal {
			continue
		}
		if event.Kind == EventError {
			terminal = true
			if DefinitiveAuthenticationFailure(event.Err) {
				if err := lease.MarkAuthenticationRequired(event.Err.Error()); err != nil {
					event.Err = errors.Join(event.Err, err)
				}
			} else if cooldown, ok := QuotaCooldownForError(event.Err, time.Now()); ok {
				scope := lease.CooldownScope(modelID)
				if cooldown.Global {
					scope = ""
				}
				if _, err := pool.MarkQuotaCooldownIfGeneration(accountID, scope, accessGeneration, checkedAt, cooldown); err != nil {
					event.Err = errors.Join(event.Err, err)
				}
			}
		}
		select {
		case destination <- event:
		case <-ctx.Done():
			return
		}
		if event.Kind == EventFinish {
			terminal = true
			if err := lease.MarkAuthenticationValid(); err != nil {
				slog.Error("账户认证状态保存失败", "account", accountID, "error", err)
			}
		}
		if event.Kind == EventFinish {
			go func() {
				if _, err := pool.MarkModelAccessVerifiedIfGeneration(
					accountID, lease.CooldownScope(modelID), accessGeneration, checkedAt,
				); err != nil {
					slog.Error("账户模型资格保存失败", "account", accountID, "model", modelID, "error", err)
				}
			}()
		}
	}
}

func mergeModels(base []Model, additions []Model) []Model {
	merged := make(map[string]Model, len(base)+len(additions))
	for _, model := range append(cloneModels(base), cloneModels(additions)...) {
		current, exists := merged[model.ID]
		if !exists {
			merged[model.ID] = model
			continue
		}
		current.Methods = unionOrdered(current.Methods, model.Methods)
		if current.Name == "" {
			current.Name = model.Name
		}
		if current.Description == "" {
			current.Description = model.Description
		}
		current.InputTokenLimit = minimumPositive(current.InputTokenLimit, model.InputTokenLimit)
		current.OutputTokenLimit = minimumPositive(current.OutputTokenLimit, model.OutputTokenLimit)
		if current.Capabilities == nil && len(model.Capabilities) > 0 {
			current.Capabilities = make(map[string]bool, len(model.Capabilities))
		}
		for name, enabled := range model.Capabilities {
			current.Capabilities[name] = current.Capabilities[name] || enabled
		}
		if current.CapabilityOptions == nil && len(model.CapabilityOptions) > 0 {
			current.CapabilityOptions = make(map[string][]string, len(model.CapabilityOptions))
		}
		for name, values := range model.CapabilityOptions {
			current.CapabilityOptions[name] = unionOrdered(current.CapabilityOptions[name], values)
		}
		if len(current.VoiceDetails) == 0 {
			current.VoiceDetails = model.VoiceDetails
		}
		if len(current.Languages) == 0 {
			current.Languages = model.Languages
		}
		current.AccessModes = unionInt64(current.AccessModes, model.AccessModes)
		current.Paid = current.Paid || model.Paid
		merged[model.ID] = current
	}
	result := make([]Model, 0, len(merged))
	for _, id := range upstreamModelOrder(base, additions) {
		model := merged[id]
		model.Methods = unionOrdered(nil, model.Methods)
		result = append(result, model)
	}
	return result
}

// upstreamModelOrder 按各目录的上游顺序合并模型 ID
func upstreamModelOrder(lists ...[]Model) []string {
	var order []string
	for _, models := range lists {
		previous := len(order) - 1
		if index := slices.IndexFunc(models, func(model Model) bool { return slices.Contains(order, model.ID) }); index >= 0 {
			previous = slices.Index(order, models[index].ID) - 1
		}
		for _, model := range models {
			if index := slices.Index(order, model.ID); index >= 0 {
				previous = index
				continue
			}
			previous++
			order = slices.Insert(order, previous, model.ID)
		}
	}
	return order
}

func unionInt64(left []int64, right []int64) []int64 {
	values := make(map[int64]struct{}, len(left)+len(right))
	for _, value := range left {
		values[value] = struct{}{}
	}
	for _, value := range right {
		values[value] = struct{}{}
	}
	result := make([]int64, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(left int, right int) bool { return result[left] < result[right] })
	return result
}

// unionOrdered 按首次出现的顺序合并两组取值并去重
func unionOrdered(left []string, right []string) []string {
	result := slices.Clone(left)
	for _, value := range right {
		if !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	return result
}

func minimumPositive(left int64, right int64) int64 {
	if left <= 0 {
		return right
	}
	if right <= 0 || left < right {
		return left
	}
	return right
}
