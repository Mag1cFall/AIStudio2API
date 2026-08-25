package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

// newRuntime 装配账户池、协议客户端与管理服务
func newRuntime(ctx context.Context, cfg config.Config) (aistudio.Service, api.AdminService, func() error, error) {
	store := aistudio.NewAccountStore(strings.Split(cfg.AuthStates, ",")...)
	accounts, err := store.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	camoufoxPath, err := findCamoufoxExecutable()
	if err != nil {
		return nil, nil, nil, err
	}
	login, err := defaultSetupLoginDriver(cfg)
	if err != nil {
		return nil, nil, nil, err
	}

	pool := aistudio.NewAccountPool(accounts)
	headers, err := newAccountHeaderProvider(accounts, cfg.Proxy)
	if err != nil {
		return nil, nil, nil, err
	}
	transport, err := aistudio.NewMakerSuiteHTTPTransport(aistudio.HTTPTransportOptions{
		Pool: pool, Signer: aistudio.NewSigner(), Headers: headers, GlobalProxy: cfg.Proxy,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	workers := newAccountWorkerManager(pool, accounts, camoufoxPath, cfg.Proxy, cfg.InitTimeout)
	protected, err := aistudio.NewWorkerProtectedTransport(aistudio.WorkerProtectedTransportOptions{
		Transport: transport, Workers: workers,
	})
	if err != nil {
		transport.CloseIdleConnections()
		return nil, nil, nil, errors.Join(err, workers.Close())
	}
	requestContext, err := aistudio.NewPoolRequestContextProvider(pool)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, nil, nil, errors.Join(err, workers.Close())
	}
	refresher := newAuthRuntimeRefresher(workers, headers, cfg.Proxy)
	client, err := aistudio.NewClient(aistudio.ClientOptions{
		Transport:       &authRetryTransport{transport: transport, refresher: refresher},
		Protected:       &authRetryProtectedTransport{transport: protected, refresher: refresher},
		ContextProvider: requestContext,
	})
	if err != nil {
		transport.CloseIdleConnections()
		return nil, nil, nil, errors.Join(err, workers.Close())
	}
	pooled, err := aistudio.NewPooledService(pool, client)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, nil, nil, errors.Join(err, workers.Close())
	}
	requests := newRequestRegistry()
	service := newTrackedService(ctx, pooled, pool, requests, workers, cfg.RequestTimeout)
	admin := newRuntimeAdmin(ctx, pool, store, service, requests, login, workers, headers, cfg)
	requests.log("service", "INFO", "管理页面已启动")
	closeRuntime := func() error {
		err := workers.Close()
		transport.CloseIdleConnections()
		return err
	}
	return service, admin, closeRuntime, nil
}

// accountWorkerManager 管理每账户的长驻 WAA worker
type accountWorkerManager struct {
	mu          sync.RWMutex
	pool        *aistudio.AccountPool
	accounts    map[string]*accountWorker
	camoufox    string
	globalProxy string
	initTimeout time.Duration
	closed      bool
}

type accountWorker struct {
	mu     sync.Mutex
	id     string
	config camoufoxnative.Options
	worker *aistudio.NativeWorker
}

// accountWorkerInitError 表示单个账户的 WAA worker 初始化失败
type accountWorkerInitError struct {
	err error
}

func (err *accountWorkerInitError) Error() string {
	return err.err.Error()
}

func (err *accountWorkerInitError) Unwrap() error {
	return err.err
}

// newAccountWorkerManager 创建账户 worker 配置
func newAccountWorkerManager(
	pool *aistudio.AccountPool,
	accounts []*aistudio.Account,
	camoufoxPath string,
	globalProxy string,
	initTimeout time.Duration,
) *accountWorkerManager {
	manager := &accountWorkerManager{
		pool: pool, accounts: make(map[string]*accountWorker, len(accounts)), camoufox: camoufoxPath,
		globalProxy: globalProxy, initTimeout: initTimeout,
	}
	for _, account := range accounts {
		if account == nil {
			continue
		}
		manager.accounts[account.ID] = manager.newAccountWorker(account)
	}
	return manager
}

// Add 注册新账户的 WAA worker 配置
func (manager *accountWorkerManager) Add(account *aistudio.Account) error {
	if account == nil {
		return fmt.Errorf("账户未初始化")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return fmt.Errorf("WAA worker manager 已关闭")
	}
	if _, exists := manager.accounts[account.ID]; exists {
		return fmt.Errorf("WAA worker 账户已存在: %s", account.ID)
	}
	manager.accounts[account.ID] = manager.newAccountWorker(account)
	return nil
}

// Reset 关闭账户当前 WAA worker 并保留重建配置
func (manager *accountWorkerManager) Reset(accountID string) error {
	manager.mu.RLock()
	account := manager.accounts[accountID]
	manager.mu.RUnlock()
	if account == nil {
		return fmt.Errorf("WAA worker 账户不存在: %s", accountID)
	}
	account.mu.Lock()
	defer account.mu.Unlock()
	if account.worker == nil {
		return nil
	}
	err := account.worker.Close()
	account.worker = nil
	return err
}

// Update 关闭账户当前 worker 并替换固定运行配置
func (manager *accountWorkerManager) Update(account *aistudio.Account) error {
	if account == nil {
		return fmt.Errorf("账户未初始化")
	}
	manager.mu.RLock()
	worker := manager.accounts[account.ID]
	manager.mu.RUnlock()
	if worker == nil {
		return fmt.Errorf("WAA worker 账户不存在: %s", account.ID)
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.worker != nil {
		if err := worker.worker.Close(); err != nil {
			return err
		}
		worker.worker = nil
	}
	worker.config = manager.workerConfig(account)
	return nil
}

// ResetAll 关闭全部账户当前 worker 并保留后续按需重建能力
func (manager *accountWorkerManager) ResetAll() error {
	manager.mu.RLock()
	accountIDs := make([]string, 0, len(manager.accounts))
	for accountID := range manager.accounts {
		accountIDs = append(accountIDs, accountID)
	}
	manager.mu.RUnlock()
	var resetErrors []error
	for _, accountID := range accountIDs {
		resetErrors = append(resetErrors, manager.Reset(accountID))
	}
	return errors.Join(resetErrors...)
}

// Remove 删除账户的 WAA worker 配置
func (manager *accountWorkerManager) Remove(accountID string) error {
	if err := manager.Reset(accountID); err != nil {
		return err
	}
	manager.mu.Lock()
	delete(manager.accounts, accountID)
	manager.mu.Unlock()
	return nil
}

func (manager *accountWorkerManager) newAccountWorker(account *aistudio.Account) *accountWorker {
	return &accountWorker{id: account.ID, config: manager.workerConfig(account)}
}

func (manager *accountWorkerManager) workerConfig(account *aistudio.Account) camoufoxnative.Options {
	return camoufoxnative.Options{
		ExecutablePath:   manager.camoufox,
		StorageStatePath: account.StoragePath,
		Locale:           account.Config.Locale,
		Timezone:         account.Config.Timezone,
		Proxy:            account.EffectiveProxy(manager.globalProxy),
		Headless:         true,
	}
}

// Worker 返回账户当前可用的 WAA preparer
func (manager *accountWorkerManager) Worker(ctx context.Context, accountID string) (aistudio.ProtectedPreparer, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	if manager.closed {
		return nil, fmt.Errorf("WAA worker manager 已关闭")
	}
	account := manager.accounts[accountID]
	if account == nil {
		return nil, fmt.Errorf("账户不存在: %s", accountID)
	}
	model, err := manager.pool.BootstrapModel(accountID)
	if err != nil {
		return nil, err
	}
	account.mu.Lock()
	defer account.mu.Unlock()
	if account.worker != nil && account.worker.State().Phase == aistudio.WorkerReady {
		return account.worker, nil
	}
	if account.worker != nil {
		if err := account.worker.Close(); err != nil {
			slog.Warn("关闭旧 WAA worker 失败", "error", err)
		}
		account.worker = nil
	}
	initCtx, cancel := context.WithTimeout(ctx, manager.initTimeout)
	defer cancel()
	account.config.Model = model
	worker, err := aistudio.NewNativeWorker(initCtx, account.id, account.config)
	if err != nil {
		return nil, &accountWorkerInitError{err: err}
	}
	account.worker = worker
	return worker, nil
}

// Close 关闭全部账户 worker
func (manager *accountWorkerManager) Close() error {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return nil
	}
	manager.closed = true
	accounts := make([]*accountWorker, 0, len(manager.accounts))
	for _, account := range manager.accounts {
		accounts = append(accounts, account)
	}
	manager.mu.Unlock()
	var closeErrors []error
	for _, account := range accounts {
		account.mu.Lock()
		if account.worker != nil {
			closeErrors = append(closeErrors, account.worker.Close())
			account.worker = nil
		}
		account.mu.Unlock()
	}
	return errors.Join(closeErrors...)
}

type accountHeaderProvider struct {
	mu          sync.RWMutex
	accounts    map[string]*accountHeaderState
	globalProxy string
}

type accountHeaderState struct {
	mu      sync.Mutex
	client  *http.Client
	headers http.Header
}

// newAccountHeaderProvider 创建每账户固定出口的公开头提供器
func newAccountHeaderProvider(accounts []*aistudio.Account, globalProxy string) (*accountHeaderProvider, error) {
	provider := &accountHeaderProvider{
		accounts: make(map[string]*accountHeaderState, len(accounts)), globalProxy: globalProxy,
	}
	for _, account := range accounts {
		if account == nil {
			continue
		}
		if err := provider.Add(account); err != nil {
			return nil, err
		}
	}
	return provider, nil
}

// Add 注册新账户的固定出口
func (provider *accountHeaderProvider) Add(account *aistudio.Account) error {
	if account == nil {
		return fmt.Errorf("账户未初始化")
	}
	client, err := aistudio.NewProxyHTTPClient(account.EffectiveProxy(provider.globalProxy))
	if err != nil {
		return fmt.Errorf("创建账户 %s 的固定出口: %w", account.ID, err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if _, exists := provider.accounts[account.ID]; exists {
		client.CloseIdleConnections()
		return fmt.Errorf("账户固定出口已存在: %s", account.ID)
	}
	provider.accounts[account.ID] = &accountHeaderState{client: client}
	return nil
}

// Update 替换账户固定出口并清除已发现公共头
func (provider *accountHeaderProvider) Update(account *aistudio.Account) error {
	if account == nil {
		return fmt.Errorf("账户未初始化")
	}
	client, err := aistudio.NewProxyHTTPClient(account.EffectiveProxy(provider.globalProxy))
	if err != nil {
		return fmt.Errorf("创建账户 %s 的固定出口: %w", account.ID, err)
	}
	provider.mu.Lock()
	current := provider.accounts[account.ID]
	if current == nil {
		provider.mu.Unlock()
		client.CloseIdleConnections()
		return fmt.Errorf("账户固定出口不存在: %s", account.ID)
	}
	provider.accounts[account.ID] = &accountHeaderState{client: client}
	provider.mu.Unlock()
	current.client.CloseIdleConnections()
	return nil
}

// Remove 删除账户的固定出口
func (provider *accountHeaderProvider) Remove(accountID string) error {
	provider.mu.Lock()
	account := provider.accounts[accountID]
	if account != nil {
		delete(provider.accounts, accountID)
	}
	provider.mu.Unlock()
	if account == nil {
		return fmt.Errorf("账户固定出口不存在: %s", accountID)
	}
	account.client.CloseIdleConnections()
	return nil
}

// Invalidate 清除账户公共头并让下一次请求重新发现
func (provider *accountHeaderProvider) Invalidate(accountID string) error {
	provider.mu.RLock()
	account := provider.accounts[accountID]
	provider.mu.RUnlock()
	if account == nil {
		return fmt.Errorf("账户固定出口不存在: %s", accountID)
	}
	account.mu.Lock()
	account.headers = nil
	account.mu.Unlock()
	return nil
}

// ProtocolHeaders 返回账户当前使用的公开协议头
func (provider *accountHeaderProvider) ProtocolHeaders(ctx context.Context, accountID string) (http.Header, error) {
	provider.mu.RLock()
	account := provider.accounts[accountID]
	provider.mu.RUnlock()
	if account == nil {
		return nil, fmt.Errorf("账户不存在: %s", accountID)
	}
	account.mu.Lock()
	defer account.mu.Unlock()
	if len(account.headers) == 0 {
		headers, err := aistudio.DiscoverPublicHeaders(ctx, account.client)
		if err != nil {
			return nil, err
		}
		account.headers = headers.Clone()
	}
	return account.headers.Clone(), nil
}

// trackedService 跟踪生成请求及其唯一账户租约
type trackedService struct {
	lifecycle   context.Context
	service     aistudio.Service
	pool        *aistudio.AccountPool
	requests    *requestRegistry
	workers     *accountWorkerManager
	timeout     time.Duration
	running     atomic.Bool
	lifecycleMu sync.Mutex
	dataContext context.Context
	dataCancel  context.CancelFunc
	modelsMu    sync.RWMutex
	models      []aistudio.Model
}

// newTrackedService 创建带超时和生命周期的协议服务
func newTrackedService(lifecycle context.Context, service aistudio.Service, pool *aistudio.AccountPool, requests *requestRegistry, workers *accountWorkerManager, timeout time.Duration) *trackedService {
	return &trackedService{
		lifecycle: lifecycle, service: service, pool: pool, requests: requests, workers: workers, timeout: timeout,
	}
}

type serviceStoppedError struct{}

func (*serviceStoppedError) Error() string {
	return "AIStudio2API 服务已停止"
}

func (*serviceStoppedError) HTTPStatus() int {
	return http.StatusServiceUnavailable
}

func (*serviceStoppedError) ErrorCode() string {
	return "service_stopped"
}

// Running 返回公开生成数据面是否接受请求
func (service *trackedService) Running() bool {
	return service.running.Load()
}

// Start 刷新模型并创建本次公开生成数据面
func (service *trackedService) Start(ctx context.Context) ([]aistudio.Model, bool, error) {
	service.lifecycleMu.Lock()
	defer service.lifecycleMu.Unlock()
	if service.running.Load() {
		return service.modelSnapshot(), false, nil
	}
	models, err := service.refreshModels(ctx)
	if err != nil || len(models) == 0 {
		service.clearModels()
		return models, false, err
	}
	service.dataContext, service.dataCancel = context.WithCancel(service.lifecycle)
	service.running.Store(true)
	return models, true, nil
}

// Stop 停止公开生成数据面并释放活动 worker
func (service *trackedService) Stop() (bool, error) {
	service.lifecycleMu.Lock()
	defer service.lifecycleMu.Unlock()
	if !service.running.Load() {
		return false, nil
	}
	service.running.Store(false)
	service.dataCancel()
	service.dataContext = nil
	service.dataCancel = nil
	service.requests.cancelAll()
	return true, service.workers.ResetAll()
}

// Models 返回最近一次启动时确认的模型目录
func (service *trackedService) Models(context.Context) ([]aistudio.Model, error) {
	return service.modelSnapshot(), nil
}

func (service *trackedService) modelSnapshot() []aistudio.Model {
	service.modelsMu.RLock()
	models := make([]aistudio.Model, len(service.models))
	copy(models, service.models)
	service.modelsMu.RUnlock()
	return models
}

func (service *trackedService) refreshModels(ctx context.Context) ([]aistudio.Model, error) {
	if len(service.pool.Status()) == 0 {
		models := []aistudio.Model{}
		service.modelsMu.Lock()
		service.models = models
		service.modelsMu.Unlock()
		return models, nil
	}
	requestCtx, cancel := service.lifecycleRequestContext(ctx)
	defer cancel()
	models, err := service.service.Models(requestCtx)
	if err != nil {
		return nil, err
	}
	service.modelsMu.Lock()
	service.models = append([]aistudio.Model(nil), models...)
	service.modelsMu.Unlock()
	return append([]aistudio.Model(nil), models...), nil
}

// SyncModels 在账户变化后刷新或清空公开模型目录
func (service *trackedService) SyncModels(ctx context.Context) error {
	service.lifecycleMu.Lock()
	defer service.lifecycleMu.Unlock()
	if !service.running.Load() {
		service.clearModels()
		return nil
	}
	if _, err := service.refreshModels(ctx); err != nil {
		service.clearModels()
		return err
	}
	return nil
}

func (service *trackedService) clearModels() {
	service.modelsMu.Lock()
	service.models = nil
	service.modelsMu.Unlock()
}

func (service *trackedService) observedDataRequestContext(
	ctx context.Context,
	model string,
) (context.Context, context.CancelFunc, error) {
	api.SetAccessLogTarget(ctx, model, "")
	requestCtx, cancel, err := service.dataRequestContext(ctx)
	if err != nil {
		api.SetAccessLogError(ctx, err)
		return nil, nil, err
	}
	observed := aistudio.ContextWithAccountSelectionObserver(requestCtx, func(account *aistudio.Account) {
		api.SetAccessLogTarget(requestCtx, model, account.Config.Label)
	})
	return observed, cancel, nil
}

// CountTokens 返回上游权威输入 token 数
func (service *trackedService) CountTokens(ctx context.Context, request aistudio.TokenCountRequest) (aistudio.TokenCount, error) {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, request.Model)
	if err != nil {
		return aistudio.TokenCount{}, err
	}
	defer cancel()
	count, requestErr := service.service.CountTokens(requestCtx, request)
	api.SetAccessLogError(requestCtx, requestErr)
	return count, requestErr
}

// GenerateVideo 创建一个 Veo 长任务
func (service *trackedService) GenerateVideo(ctx context.Context, request aistudio.VideoRequest) (aistudio.VideoOperation, error) {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, request.Model)
	if err != nil {
		return aistudio.VideoOperation{}, err
	}
	defer cancel()
	video, ok := service.service.(aistudio.VideoService)
	if !ok {
		return aistudio.VideoOperation{}, fmt.Errorf("video service 不可用")
	}
	operation, requestErr := video.GenerateVideo(requestCtx, request)
	api.SetAccessLogError(requestCtx, requestErr)
	return operation, requestErr
}

// GetGenerateVideoOperation 读取 Veo 长任务状态
func (service *trackedService) GetGenerateVideoOperation(ctx context.Context, operationID string) (aistudio.VideoOperation, error) {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, "")
	if err != nil {
		return aistudio.VideoOperation{}, err
	}
	defer cancel()
	video, ok := service.service.(aistudio.VideoService)
	if !ok {
		return aistudio.VideoOperation{}, fmt.Errorf("video service 不可用")
	}
	operation, requestErr := video.GetGenerateVideoOperation(requestCtx, operationID)
	api.SetAccessLogError(requestCtx, requestErr)
	return operation, requestErr
}

// DownloadFile 下载生成任务绑定的 Drive 文件
func (service *trackedService) DownloadFile(ctx context.Context, fileID string) (aistudio.Media, error) {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, "")
	if err != nil {
		return aistudio.Media{}, err
	}
	defer cancel()
	video, ok := service.service.(aistudio.VideoService)
	if !ok {
		return aistudio.Media{}, fmt.Errorf("video service 不可用")
	}
	media, requestErr := video.DownloadFile(requestCtx, fileID)
	api.SetAccessLogError(requestCtx, requestErr)
	return media, requestErr
}

// Generate 获取唯一账户并转发规范事件流
func (service *trackedService) Generate(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	api.SetAccessLogTarget(ctx, request.Model, "")
	requestCtx, cancel, err := service.dataRequestContext(ctx)
	if err != nil {
		api.SetAccessLogError(ctx, err)
		service.requests.start(request, func() {})
		service.requests.finish(request.ID, "failed", err)
		return nil, err
	}
	service.requests.start(request, cancel)
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	requestedAccountID := strings.TrimSpace(request.AccountID)
	resourceID, err := service.pool.ResourceIDForContents(request.Contents)
	if err != nil {
		api.SetAccessLogError(requestCtx, err)
		cancel()
		service.requests.finish(request.ID, finalRequestState(err), err)
		return nil, err
	}
	maxAttempts := 1
	if requestedAccountID == "" && resourceID == "" {
		eligible := 0
		for _, status := range service.pool.Status() {
			if status.Enabled && (status.State == aistudio.AccountReady || status.State == aistudio.AccountBusy) {
				eligible++
			}
		}
		if eligible > 1 {
			maxAttempts = eligible
		}
	}
	var lease *aistudio.AccountLease
	var source <-chan aistudio.Event
	var first aistudio.Event
	for attempt := 0; attempt < maxAttempts; attempt++ {
		nextLease, acquireErr := service.pool.AcquireFor(requestCtx, aistudio.AccountSelection{
			ModelID: modelID, Method: "generateContent", AccountID: requestedAccountID, ResourceID: resourceID,
		})
		if acquireErr != nil {
			if err == nil || !errors.Is(acquireErr, aistudio.ErrNoEligibleAccount) {
				err = acquireErr
			}
			break
		}
		lease = nextLease
		request.AccountID = lease.Account().ID
		accountLabel := lease.Account().Config.Label
		api.SetAccessLogTarget(requestCtx, modelID, accountLabel)
		service.requests.markRunning(request.ID, request.AccountID)
		source, err = service.service.Generate(aistudio.ContextWithAccountLease(requestCtx, lease), request)
		if err == nil {
			first, err = firstGenerateEvent(requestCtx, source)
			if err == nil {
				api.SetAccessLogTarget(requestCtx, first.ProviderModel, accountLabel)
				break
			}
		}
		retryable := retryableGenerateAccountError(requestCtx, err)
		if aistudio.DefinitiveAuthenticationFailure(err) {
			if stateErr := service.pool.MarkAuthRequired(request.AccountID, err.Error()); stateErr != nil {
				err = errors.Join(err, stateErr)
				retryable = false
			}
		}
		releaseErr := lease.Release()
		lease = nil
		if releaseErr != nil {
			err = errors.Join(err, releaseErr)
			break
		}
		if !retryable {
			break
		}
		if !aistudio.DefinitiveAuthenticationFailure(err) {
			cooldownModel := modelID
			cooldownDuration := 30 * time.Second
			var workerInitError *accountWorkerInitError
			if errors.As(err, &workerInitError) {
				cooldownModel = ""
				cooldownDuration = 5 * time.Minute
			}
			var rpcError *aistudio.RPCError
			if errors.As(err, &rpcError) && rpcError.StatusCode == http.StatusForbidden && rpcError.Code == 7 {
				cooldownDuration = time.Hour
			}
			if cooldownErr := service.pool.MarkCooldown(request.AccountID, cooldownModel, time.Now().Add(cooldownDuration), err.Error()); cooldownErr != nil {
				err = errors.Join(err, cooldownErr)
				break
			}
		}
		if attempt+1 == maxAttempts {
			break
		}
		accessRequestID := api.AccessLogRequestID(requestCtx)
		if accessRequestID == "" {
			accessRequestID = request.ID
		}
		switchMessage := fmt.Sprintf(
			"账号切换 | %s | %s | #%s\n原因: %s",
			modelID, accountLabel, shortRequestID(accessRequestID), strings.TrimSpace(err.Error()),
		)
		slog.Warn(switchMessage)
		service.requests.log(accountLabel, "WARN", switchMessage)
	}
	if err != nil {
		api.SetAccessLogError(requestCtx, err)
		cancel()
		service.requests.finish(request.ID, finalRequestState(err), err)
		return nil, err
	}
	events := make(chan aistudio.Event, 8)
	go service.forwardEvents(ctx, requestCtx, cancel, request.ID, first, source, events, lease)
	return events, nil
}

var errStreamClosedBeforeFirstEvent = errors.New("AI Studio stream closed before first event")

func firstGenerateEvent(ctx context.Context, source <-chan aistudio.Event) (aistudio.Event, error) {
	select {
	case event, ok := <-source:
		if !ok {
			return aistudio.Event{}, errStreamClosedBeforeFirstEvent
		}
		if event.Kind != aistudio.EventError {
			return event, nil
		}
		for range source {
		}
		if event.Err != nil {
			return aistudio.Event{}, event.Err
		}
		return aistudio.Event{}, errors.New("AI Studio stream returned an empty error event")
	case <-ctx.Done():
		return aistudio.Event{}, ctx.Err()
	}
}

func retryableGenerateAccountError(ctx context.Context, err error) bool {
	if errors.Is(err, errStreamClosedBeforeFirstEvent) {
		return true
	}
	var workerInitError *accountWorkerInitError
	if errors.As(err, &workerInitError) {
		return ctx.Err() == nil
	}
	if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var rpcError *aistudio.RPCError
	if !errors.As(err, &rpcError) {
		return false
	}
	return rpcError.StatusCode == http.StatusUnauthorized || rpcError.StatusCode == http.StatusForbidden || rpcError.StatusCode == http.StatusNotFound ||
		rpcError.StatusCode == http.StatusTooManyRequests || rpcError.StatusCode >= http.StatusInternalServerError
}

func (service *trackedService) lifecycleRequestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	requestCtx, cancel := context.WithTimeout(ctx, service.timeout)
	stopLifecycle := context.AfterFunc(service.lifecycle, cancel)
	return requestCtx, func() {
		stopLifecycle()
		cancel()
	}
}

func (service *trackedService) dataRequestContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	service.lifecycleMu.Lock()
	if !service.running.Load() || service.dataContext == nil {
		service.lifecycleMu.Unlock()
		return nil, nil, &serviceStoppedError{}
	}
	requestCtx, cancel := context.WithTimeout(ctx, service.timeout)
	stopData := context.AfterFunc(service.dataContext, cancel)
	service.lifecycleMu.Unlock()
	return requestCtx, func() {
		stopData()
		cancel()
	}, nil
}

func (service *trackedService) forwardEvents(
	clientCtx context.Context,
	requestCtx context.Context,
	cancel context.CancelFunc,
	requestID string,
	first aistudio.Event,
	source <-chan aistudio.Event,
	destination chan<- aistudio.Event,
	lease *aistudio.AccountLease,
) {
	state := "completed"
	var requestErr error
	terminal := false
	defer cancel()
	defer func() {
		if err := lease.Release(); err != nil {
			state = "failed"
			requestErr = errors.Join(requestErr, err)
			if clientCtx.Err() == nil {
				select {
				case destination <- aistudio.Event{Kind: aistudio.EventError, Err: err}:
				case <-clientCtx.Done():
				}
			}
		}
		api.SetAccessLogError(requestCtx, requestErr)
		service.requests.finish(requestID, state, requestErr)
		close(destination)
	}()
	pendingFirst := true
	for {
		var event aistudio.Event
		var ok bool
		if pendingFirst {
			event = first
			ok = true
			pendingFirst = false
		} else {
			select {
			case event, ok = <-source:
			case <-requestCtx.Done():
				requestErr = requestCtx.Err()
				state = finalRequestState(requestErr)
				service.drainCanceled(clientCtx, requestCtx, source, destination)
				return
			}
		}
		if !ok {
			if err := requestCtx.Err(); err != nil {
				requestErr = err
				state = finalRequestState(err)
			}
			return
		}
		api.SetAccessLogTarget(requestCtx, event.ProviderModel, lease.Account().Config.Label)
		if terminal {
			continue
		}
		if event.Kind == aistudio.EventError {
			requestErr = event.Err
			if aistudio.DefinitiveAuthenticationFailure(event.Err) {
				if stateErr := service.pool.MarkAuthRequired(lease.Account().ID, event.Err.Error()); stateErr != nil {
					requestErr = errors.Join(requestErr, stateErr)
					event.Err = requestErr
				}
			}
			state = finalRequestState(event.Err)
			terminal = true
		}
		if event.Kind == aistudio.EventFinish {
			state = "completed"
			terminal = true
		}
		select {
		case destination <- event:
		case <-requestCtx.Done():
			requestErr = requestCtx.Err()
			state = finalRequestState(requestErr)
			service.drainCanceled(clientCtx, requestCtx, source, destination)
			return
		}
	}
}

func (*trackedService) drainCanceled(
	clientCtx context.Context,
	requestCtx context.Context,
	source <-chan aistudio.Event,
	destination chan<- aistudio.Event,
) {
	if clientCtx.Err() == nil {
		select {
		case destination <- aistudio.Event{Kind: aistudio.EventError, Err: requestCtx.Err()}:
		case <-clientCtx.Done():
		}
	}
	for range source {
	}
}

func finalRequestState(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if err != nil {
		return "failed"
	}
	return "completed"
}

var _ aistudio.Service = (*trackedService)(nil)
