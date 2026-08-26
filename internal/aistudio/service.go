package aistudio

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const modelCatalogConcurrency = 5

// PooledService 在账户租约内调用协议客户端
type PooledService struct {
	pool   *AccountPool
	client *Client
}

// PoolRequestContextProvider 从租约账户读取协议上下文
type PoolRequestContextProvider struct {
	pool *AccountPool
}

// ProtectedPreparer 为一次请求写入 fresh WAA proof
type ProtectedPreparer interface {
	Prepare(context.Context, ProtectedRequest) (PreparedProtectedRequest, error)
}

// ProtectedPreparerProvider 按账户返回 lazy WAA preparer
type ProtectedPreparerProvider interface {
	Worker(context.Context, string) (ProtectedPreparer, error)
}

// ProtectedPreparerProviderFunc 将函数适配为 ProtectedPreparerProvider
type ProtectedPreparerProviderFunc func(context.Context, string) (ProtectedPreparer, error)

// Worker 返回账户的 lazy WAA preparer
func (f ProtectedPreparerProviderFunc) Worker(ctx context.Context, accountID string) (ProtectedPreparer, error) {
	return f(ctx, accountID)
}

// WorkerProtectedTransportOptions 定义受保护请求的 proof 与 HTTP 依赖
type WorkerProtectedTransportOptions struct {
	Transport *MakerSuiteHTTPTransport
	Workers   ProtectedPreparerProvider
}

// WorkerProtectedTransport 将 fresh proof 交给同租约 HTTP 传输
type WorkerProtectedTransport struct {
	transport *MakerSuiteHTTPTransport
	workers   ProtectedPreparerProvider
}

var _ Service = (*PooledService)(nil)
var _ RequestContextProvider = (*PoolRequestContextProvider)(nil)
var _ ProtectedTransport = (*WorkerProtectedTransport)(nil)
var _ VideoProtectedTransport = (*WorkerProtectedTransport)(nil)

// NewPooledService 创建多账户协议服务
func NewPooledService(pool *AccountPool, client *Client) (*PooledService, error) {
	if pool == nil {
		return nil, fmt.Errorf("AI Studio account pool 不能为空")
	}
	if client == nil {
		return nil, fmt.Errorf("AI Studio client 不能为空")
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
	return &WorkerProtectedTransport{transport: options.Transport, workers: options.Workers}, nil
}

// DoProtected 写入 fresh proof 后返回原生流式响应
func (t *WorkerProtectedTransport) DoProtected(ctx context.Context, request GenerateRequest, rpc RPCRequest) (*RPCResponse, error) {
	prompt, err := bindingPrompt(request)
	if err != nil {
		return nil, err
	}
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	return t.doPrepared(ctx, prompt, 5, AccountSelection{
		ModelID: modelID, Method: "generateContent", AccountID: strings.TrimSpace(request.AccountID),
	}, rpc)
}

// DoProtectedVideo 写入 Veo fresh proof 后发送请求
func (t *WorkerProtectedTransport) DoProtectedVideo(ctx context.Context, request VideoRequest, rpc RPCRequest) (*RPCResponse, error) {
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	return t.doPrepared(ctx, request.Prompt, 8, AccountSelection{
		ModelID: modelID, Method: "predictLongRunning", AccountID: strings.TrimSpace(request.AccountID),
	}, rpc)
}

func (t *WorkerProtectedTransport) doPrepared(
	ctx context.Context,
	prompt string,
	proofField int,
	selection AccountSelection,
	rpc RPCRequest,
) (*RPCResponse, error) {
	lease, ok := AccountLeaseFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("受保护请求缺少账户租约")
	}
	if err := validateLeaseSelection(lease, selection); err != nil {
		return nil, err
	}
	worker, err := t.workers.Worker(ctx, lease.Account().ID)
	if err != nil {
		return nil, fmt.Errorf("获取账户 WAA preparer: %w", err)
	}
	reportRequestPhase(ctx, RequestPhasePreparingWAA)
	prepared, err := worker.Prepare(ctx, ProtectedRequest{
		URL:        rpc.URL,
		Headers:    rpc.Header.Clone(),
		Body:       append([]byte(nil), rpc.Body...),
		Prompt:     prompt,
		ProofField: proofField,
	})
	if err != nil {
		return nil, fmt.Errorf("准备 fresh WAA proof: %w", err)
	}
	if prepared.Headers == nil || len(prepared.Body) == 0 {
		return nil, fmt.Errorf("WAA preparer 返回空请求")
	}
	rpc.AccountID = lease.Account().ID
	rpc.Body = append([]byte(nil), prepared.Body...)
	requestHeaders := rpc.Header
	rpc.Header = prepared.Headers.Clone()
	for name, values := range requestHeaders {
		rpc.Header.Del(name)
		for _, value := range values {
			rpc.Header.Add(name, value)
		}
	}
	rpc.Header.Set("Content-Type", JSONProtobufContentType)
	reportRequestPhase(ctx, RequestPhaseSendingUpstream)
	response, err := t.transport.Do(ctx, rpc)
	if err != nil {
		return nil, err
	}
	reportRequestPhase(ctx, RequestPhaseStreaming)
	return response, nil
}

func bindingPrompt(request GenerateRequest) (string, error) {
	if len(request.Contents) == 0 {
		return "", fmt.Errorf("GenerateContent contents 不能为空")
	}
	values := make([]string, 0)
	for _, content := range request.Contents {
		content = attachYouTubeMedia(content)
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
	jobs := make(chan AccountStatus)
	results := make(chan accountModelsResult, len(statuses))
	workerCount := min(modelCatalogConcurrency, len(statuses))
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for status := range jobs {
				results <- s.modelsForStatus(ctx, status)
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, status := range statuses {
			if !status.Enabled || status.State != AccountReady && status.State != AccountBusy {
				continue
			}
			select {
			case jobs <- status:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()
	models := make([]Model, 0)
	available := 0
	failures := make([]error, 0)
	for result := range results {
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

type accountModelsResult struct {
	models    []Model
	available bool
	err       error
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
		if DefinitiveAuthenticationFailure(requestErr) {
			failure = errors.Join(failure, s.pool.MarkAuthRequired(status.ID, failure.Error()))
		}
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
	return modelsAllowedByTier(cloneAccountModels(account.Models), account.BenefitTier)
}

// DefinitiveAuthenticationFailure 判断上游是否明确要求重新认证
func DefinitiveAuthenticationFailure(err error) bool {
	var rpcError *RPCError
	return errors.As(err, &rpcError) && rpcError.StatusCode == 401
}

// DefinitiveModelAccessFailure 判断上游是否明确拒绝账户模型组合
func DefinitiveModelAccessFailure(err error) bool {
	var rpcError *RPCError
	return errors.As(err, &rpcError) && rpcError.StatusCode == http.StatusForbidden && rpcError.Code == 7
}

// DefinitiveWAARuntimeFailure 判断上游是否明确拒绝当前 WAA 运行态
func DefinitiveWAARuntimeFailure(err error) bool {
	var rpcError *RPCError
	return errors.As(err, &rpcError) && rpcError.StatusCode == http.StatusNotFound && rpcError.Code == 5 &&
		strings.Contains(rpcError.Message, "Ambiguous request for service ''")
}

func (s *PooledService) markRetryableFailure(accountID string, modelID string, err error) error {
	if DefinitiveAuthenticationFailure(err) {
		return s.pool.MarkAuthRequired(accountID, err.Error())
	}
	if DefinitiveModelAccessFailure(err) {
		_, stateErr := s.pool.MarkModelAccess(accountID, modelID, ModelAccessDenied, err.Error())
		return stateErr
	}
	return s.pool.MarkCooldown(accountID, modelID, time.Now().Add(30*time.Second), err.Error())
}

func (s *PooledService) modelsForLease(ctx context.Context, lease *AccountLease) ([]Model, error) {
	account := lease.Account()
	tier, err := s.client.BenefitTierForAccount(ContextWithAccountLease(ctx, lease), account.ID)
	if err != nil {
		return nil, err
	}
	models, err := s.client.ModelsForAccount(ContextWithAccountLease(ctx, lease), account.ID)
	if err != nil {
		return nil, err
	}
	if err := s.pool.SetCatalog(account.ID, tier, models); err != nil {
		return nil, err
	}
	return modelsAllowedByTier(models, tier), nil
}

func modelsAllowedByTier(models []Model, tier BenefitTier) []Model {
	result := make([]Model, 0, len(models))
	for _, model := range models {
		if modelAllowedByTier(model, tier) {
			result = append(result, model)
		}
	}
	return result
}

// CountTokens 使用支持目标模型的独占账户计数
func (s *PooledService) CountTokens(ctx context.Context, request TokenCountRequest) (TokenCount, error) {
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	if modelID == "" {
		return TokenCount{}, fmt.Errorf("%w: CountTokens model 不能为空", ErrInvalidArgument)
	}
	selection := AccountSelection{ModelID: modelID, Method: "countTokens"}
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
		if owned {
			requestErr = errors.Join(requestErr, lease.Release())
		}
		if requestErr == nil || !retryableAccountError(requestErr) {
			if requestErr == nil {
				_, requestErr = s.pool.MarkModelAccess(accountID, modelID, ModelAccessVerified, "")
			}
			return count, requestErr
		}
		if stateErr := s.markRetryableFailure(accountID, modelID, requestErr); stateErr != nil {
			return count, errors.Join(requestErr, stateErr)
		}
	}
	return count, requestErr
}

// Generate 使用支持目标模型的独占账户生成事件流
func (s *PooledService) Generate(ctx context.Context, request GenerateRequest) (<-chan Event, error) {
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	if modelID == "" {
		return nil, fmt.Errorf("%w: GenerateContent model 不能为空", ErrInvalidArgument)
	}
	resourceID, err := s.pool.ResourceIDForContents(request.Contents)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	selection := AccountSelection{
		ModelID:    modelID,
		Method:     "generateContent",
		AccountID:  strings.TrimSpace(request.AccountID),
		ResourceID: resourceID,
	}
	lease, owned, err := resolveAccountLease(ctx, s.pool, selection)
	if err != nil {
		return nil, err
	}
	request.AccountID = lease.Account().ID
	events, err := s.client.Generate(ContextWithAccountLease(ctx, lease), request)
	if err != nil {
		if owned {
			err = errors.Join(err, lease.Release())
		}
		return nil, err
	}
	if !owned {
		return events, nil
	}
	forwarded := make(chan Event, 8)
	go forwardEventsWithLease(ctx, events, forwarded, lease)
	return forwarded, nil
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
	var rpcError *RPCError
	if !errors.As(err, &rpcError) {
		return false
	}
	return rpcError.StatusCode == http.StatusUnauthorized || rpcError.StatusCode == http.StatusForbidden ||
		rpcError.StatusCode == http.StatusTooManyRequests || rpcError.StatusCode >= http.StatusInternalServerError
}

func forwardEventsWithLease(ctx context.Context, source <-chan Event, destination chan<- Event, lease *AccountLease) {
	defer close(destination)
	for event := range source {
		select {
		case destination <- event:
		case <-ctx.Done():
			_ = lease.Release()
			return
		}
	}
	if err := lease.Release(); err != nil {
		select {
		case destination <- Event{Kind: EventError, Err: err}:
		case <-ctx.Done():
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
		current.Methods = unionStrings(current.Methods, model.Methods)
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
			current.CapabilityOptions[name] = unionStrings(current.CapabilityOptions[name], values)
		}
		current.AccessModes = unionInt64(current.AccessModes, model.AccessModes)
		current.Paid = current.Paid || model.Paid
		merged[model.ID] = current
	}
	result := make([]Model, 0, len(merged))
	for _, model := range merged {
		model.Methods = unionStrings(nil, model.Methods)
		for name, values := range model.CapabilityOptions {
			model.CapabilityOptions[name] = unionStrings(nil, values)
		}
		result = append(result, model)
	}
	sort.Slice(result, func(left int, right int) bool {
		return result[left].ID < result[right].ID
	})
	return result
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

func unionStrings(left []string, right []string) []string {
	values := make(map[string]struct{}, len(left)+len(right))
	for _, value := range left {
		values[value] = struct{}{}
	}
	for _, value := range right {
		values[value] = struct{}{}
	}
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func appendStatusError(failures []error, err error) []error {
	if err != nil {
		return append(failures, err)
	}
	return failures
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
