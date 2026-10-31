package app

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
	"github.com/Mag1cFall/AIStudio2API/internal/promptdb"
	"github.com/Mag1cFall/AIStudio2API/internal/requestdb"
)

// managedService 表示可整体替换的生成服务
type managedService interface {
	aistudio.Service
	State() string
	ApplyCapacity(warmTarget int, maxActive int, startupConcurrency int, perAccountConcurrency int)
}

// generationAdmin 表示进程级管理器转交给当前生成服务的管理操作
type generationAdmin interface {
	Status(context.Context) (api.AdminStatus, error)
	CatalogModels(context.Context) ([]aistudio.Model, error)
	Accounts(context.Context) ([]api.AdminAccount, error)
	CreateAccount(context.Context, api.AccountCreateInput, *camoufoxnative.LiveView) (api.AdminAccount, error)
	ChromeImportProfiles(context.Context) ([]api.ChromeImportProfile, error)
	BrowserImportProfiles(ctx context.Context, browser string) ([]api.BrowserImportProfile, error)
	CookieBrowsers(context.Context) ([]api.CookieBrowser, error)
	DiscoverCookieAccounts(context.Context, api.CookieImportInput) ([]api.CookieBrowser, error)
	ImportChromeAccounts(context.Context, api.ChromeImportInput) ([]api.AdminAccount, error)
	ImportBrowserAccounts(ctx context.Context, input api.BrowserImportInput) ([]api.AdminAccount, []api.ImportFailure, error)
	ImportAccountState(context.Context, api.AccountStateInput) (api.AdminAccount, error)
	UpdateAccount(context.Context, string, api.AccountInput) (api.AdminAccount, error)
	DeleteAccount(context.Context, string) error
	LoginAccount(context.Context, string, *camoufoxnative.LiveView) (api.AdminAccount, error)
	RefreshAccount(context.Context, string) (api.AdminAccountRefresh, error)
	StartService(context.Context) (api.AdminStatus, error)
	StopService(context.Context) (api.AdminStatus, error)
	RuntimeConfig(context.Context) (api.RuntimeConfig, error)
	UpdateRuntimeConfig(context.Context, api.RuntimeConfig) (api.RuntimeConfig, error)
	Cooldowns(context.Context) ([]api.AdminCooldown, error)
	ClearCooldown(ctx context.Context, accountID string, scope string) error
	Requests(context.Context) ([]api.AdminRequest, error)
	CancelRequest(context.Context, string) error
	RecordAccessStart(api.AccessLog)
	RecordAccessLog(api.AccessLog)
}

// runtimeGeneration 保存同一份配置装配的完整运行时
type runtimeGeneration struct {
	service         managedService
	admin           generationAdmin
	config          config.Config
	lifecycleCancel context.CancelFunc
	closeRuntime    func() error
}

// runtimeFactory 创建一个完整生成服务实例
type runtimeFactory func(context.Context, context.Context, config.Config, *requestRegistry) (*runtimeGeneration, error)

// Close 取消当前生成服务实例并释放运行时
func (generation *runtimeGeneration) Close() error {
	generation.lifecycleCancel()
	return generation.closeRuntime()
}

// dataConfigOverrides 保存当前进程的命令行生成服务配置覆盖
type dataConfigOverrides struct {
	authStates *string
	proxy      *string
}

// Apply 将命令行覆盖应用到新实例配置
func (overrides dataConfigOverrides) Apply(cfg *config.Config) {
	if overrides.authStates != nil {
		cfg.AuthStates = *overrides.authStates
	}
	if overrides.proxy != nil {
		cfg.Proxy = *overrides.proxy
	}
}

// runtimeManager 在固定管理监听器内切换完整生成服务
type runtimeManager struct {
	lifecycle        context.Context
	configPath       string
	activeManagement config.Config
	// reservedListenAddr 是被 Windows 保留而改用 activeManagement.ListenAddr 监听的配置地址，没有改用时为空
	reservedListenAddr string
	previewAddress     string
	overrides          dataConfigOverrides
	requests           *requestRegistry
	factory            runtimeFactory
	startMu            sync.Mutex
	mu                 sync.RWMutex
	current            *runtimeGeneration
	startCancel        context.CancelFunc
	// launching 在生成服务启动期间存在，启动结束时关闭
	launching       chan struct{}
	autoStartCancel context.CancelFunc
	ledger          *requestdb.Store
	prompts         *promptdb.Store
	logs            *logOutput
}

// newRuntimeManager 创建进程级管理器与初始生成服务
func newRuntimeManager(
	ctx context.Context,
	configPath string,
	cfg config.Config,
	overrides dataConfigOverrides,
) (*runtimeManager, error) {
	requests := newRequestRegistry(ctx)
	manager := &runtimeManager{
		lifecycle: ctx, configPath: configPath, activeManagement: cfg,
		overrides: overrides, requests: requests, factory: buildRuntimeGeneration,
	}
	generation, err := manager.factory(ctx, ctx, cfg, requests)
	if err != nil {
		return nil, err
	}
	manager.current = generation
	return manager, nil
}

// buildRuntimeGeneration 从配置快照创建账户池、Worker 与协议运行时
func buildRuntimeGeneration(
	launchCtx context.Context,
	parentLifecycle context.Context,
	cfg config.Config,
	requests *requestRegistry,
) (*runtimeGeneration, error) {
	lifecycle, lifecycleCancel := context.WithCancel(parentLifecycle)
	service, admin, closeRuntime, err := newRuntime(launchCtx, lifecycle, cfg, requests)
	if err != nil {
		lifecycleCancel()
		return nil, err
	}
	return &runtimeGeneration{
		service: service, admin: admin, config: cfg,
		lifecycleCancel: lifecycleCancel, closeRuntime: closeRuntime,
	}, nil
}

// StartService 取消自动启动的重试后从最新配置启动生成服务
func (manager *runtimeManager) StartService(ctx context.Context) (api.AdminStatus, error) {
	manager.stopAutoStart()
	return manager.startService(ctx)
}

// startService 从最新配置创建并启动新生成服务
func (manager *runtimeManager) startService(ctx context.Context) (api.AdminStatus, error) {
	manager.startMu.Lock()
	defer manager.startMu.Unlock()

	manager.mu.Lock()
	current := manager.current
	if current.service.State() != "STOPPED" {
		status, err := current.admin.StartService(ctx)
		manager.mu.Unlock()
		return status, err
	}
	if _, err := current.admin.StopService(ctx); err != nil {
		manager.mu.Unlock()
		return api.AdminStatus{}, err
	}
	launchCtx, launchCancel := context.WithCancel(manager.lifecycle)
	manager.startCancel = launchCancel
	manager.launching = make(chan struct{})
	manager.mu.Unlock()

	cfg, err := config.Load(manager.configPath)
	if err != nil {
		manager.finishStart(launchCancel)
		return api.AdminStatus{}, err
	}
	manager.overrides.Apply(&cfg)
	if err := cfg.Validate(); err != nil {
		manager.finishStart(launchCancel)
		return api.AdminStatus{}, err
	}

	next, err := manager.factory(launchCtx, manager.lifecycle, cfg, manager.requests)
	if err != nil {
		manager.finishStart(launchCancel)
		return api.AdminStatus{}, err
	}
	if launchCtx.Err() != nil {
		manager.finishStart(launchCancel)
		_ = next.Close()
		return manager.Status(ctx)
	}

	manager.mu.Lock()
	current = manager.current
	manager.current = next
	manager.mu.Unlock()

	current.lifecycleCancel()
	status, startErr := next.admin.StartService(launchCtx)
	manager.finishStart(launchCancel)
	if err := current.Close(); err != nil {
		manager.requests.log("service", "WARN", "旧生成服务关闭失败 | 错误="+err.Error())
	}
	return status, startErr
}

// finishStart 清理本轮生成服务启动取消句柄并放行等待启动结束的请求
func (manager *runtimeManager) finishStart(cancel context.CancelFunc) {
	manager.mu.Lock()
	manager.startCancel = nil
	close(manager.launching)
	manager.launching = nil
	manager.mu.Unlock()
	cancel()
}

// WaitLaunch 在生成服务启动期间等到启动结束或 ctx 结束
func (manager *runtimeManager) WaitLaunch(ctx context.Context) {
	manager.mu.RLock()
	launching := manager.launching
	manager.mu.RUnlock()
	if launching == nil {
		return
	}
	select {
	case <-launching:
	case <-ctx.Done():
	}
}

// StopService 停止当前生成服务并保持管理监听器运行
func (manager *runtimeManager) StopService(ctx context.Context) (api.AdminStatus, error) {
	manager.stopAutoStart()
	manager.mu.RLock()
	cancel := manager.startCancel
	current := manager.current
	manager.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	return current.admin.StopService(ctx)
}

// autoStartContext 返回手动启动或停止生成服务时取消的自动启动上下文
func (manager *runtimeManager) autoStartContext(parent context.Context) context.Context {
	ctx, cancel := context.WithCancel(parent)
	manager.mu.Lock()
	manager.autoStartCancel = cancel
	manager.mu.Unlock()
	return ctx
}

// stopAutoStart 取消自动启动及其重试
func (manager *runtimeManager) stopAutoStart() {
	manager.mu.Lock()
	cancel := manager.autoStartCancel
	manager.autoStartCancel = nil
	manager.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Close 释放当前生成服务
func (manager *runtimeManager) Close() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.current.Close()
}

// responseModes 返回当前生成服务的非流式保活与假流式设置
func (manager *runtimeManager) responseModes() api.ResponseModes {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return api.ResponseModes{NonstreamKeepalive: manager.current.config.NonstreamKeepalive, FakeStream: manager.current.config.FakeStream}
}

// searchSettings 返回当前生成服务的搜索开关
func (manager *runtimeManager) searchSettings() (modelAliases, googleSearch bool) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.config.SearchModelAliases, manager.current.config.GoogleSearch
}

// mediaLinks 返回当前生成服务的媒体输出是否使用 URL 与设置的服务地址
func (manager *runtimeManager) mediaLinks() (bool, string) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.config.MediaOutput == config.MediaOutputURL, manager.current.config.MediaBaseURL
}

// Models 返回当前生成服务模型
func (manager *runtimeManager) Models(ctx context.Context) ([]aistudio.Model, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.Models(ctx)
}

// CatalogModels 返回当前生成服务的管理目录
func (manager *runtimeManager) CatalogModels(ctx context.Context) ([]aistudio.Model, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.CatalogModels(ctx)
}

// CountTokens 由当前生成服务计数
func (manager *runtimeManager) CountTokens(ctx context.Context, request aistudio.TokenCountRequest) (aistudio.TokenCount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.CountTokens(ctx, request)
}

// Generate 由当前生成服务生成事件流
func (manager *runtimeManager) Generate(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.Generate(ctx, request)
}

// GenerateTitle 由当前生成服务生成试用页页头标题
func (manager *runtimeManager) GenerateTitle(ctx context.Context, request aistudio.TitleRequest) (string, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.(aistudio.TitleService).GenerateTitle(ctx, request)
}

// GenerateImage 由当前生成服务生成 Imagen 图片
func (manager *runtimeManager) GenerateImage(ctx context.Context, request aistudio.ImageRequest) ([]aistudio.GeneratedImage, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.(aistudio.ImageService).GenerateImage(ctx, request)
}

// GenerateVideo 由当前生成服务创建视频任务
func (manager *runtimeManager) GenerateVideo(ctx context.Context, request aistudio.VideoRequest) (aistudio.VideoOperation, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.(aistudio.VideoService).GenerateVideo(ctx, request)
}

// GetGenerateVideoOperation 由当前生成服务读取视频任务
func (manager *runtimeManager) GetGenerateVideoOperation(ctx context.Context, id string) (aistudio.VideoOperation, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.(aistudio.VideoService).GetGenerateVideoOperation(ctx, id)
}

// ListVideos 由当前生成服务列出视频任务
func (manager *runtimeManager) ListVideos(ctx context.Context) ([]aistudio.VideoOperation, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.(aistudio.VideoService).ListVideos(ctx)
}

// DeleteVideo 由当前生成服务删除视频任务
func (manager *runtimeManager) DeleteVideo(ctx context.Context, id string) error {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.(aistudio.VideoService).DeleteVideo(ctx, id)
}

// DownloadFile 由当前生成服务下载文件
func (manager *runtimeManager) DownloadFile(ctx context.Context, id string) (aistudio.MediaStream, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.(aistudio.VideoService).DownloadFile(ctx, id)
}

// UploadFile 由当前生成服务上传文件
func (manager *runtimeManager) UploadFile(ctx context.Context, request aistudio.UploadRequest) (aistudio.FileRef, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.(aistudio.FileService).UploadFile(ctx, request)
}

// FileMetadata 由当前生成服务读取文件元数据
func (manager *runtimeManager) FileMetadata(ctx context.Context, id string) (aistudio.FileMetadata, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.(aistudio.FileService).FileMetadata(ctx, id)
}

// ListFiles 由当前生成服务列出上传文件
func (manager *runtimeManager) ListFiles(ctx context.Context) ([]aistudio.FileMetadata, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.(aistudio.FileService).ListFiles(ctx)
}

// DeleteFile 由当前生成服务删除文件
func (manager *runtimeManager) DeleteFile(ctx context.Context, id string) error {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.(aistudio.FileService).DeleteFile(ctx, id)
}

// OpenBidi 由当前生成服务创建实时会话
func (manager *runtimeManager) OpenBidi(ctx context.Context, request aistudio.BidiRequest) (*aistudio.BidiSession, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.(aistudio.BidiService).OpenBidi(ctx, request)
}

// Transcribe 由当前生成服务执行音频转录
func (manager *runtimeManager) Transcribe(ctx context.Context, request aistudio.TranscriptionRequest) (aistudio.TranscriptionResult, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.(aistudio.TranscriptionService).Transcribe(ctx, request)
}

// egressProxy 返回管理进程访问外网的代理：PROXY 的第一个代理，未设置时为系统代理
func (manager *runtimeManager) egressProxy() string {
	manager.mu.RLock()
	proxies := config.ProxyList(manager.current.config.Proxy)
	manager.mu.RUnlock()
	if len(proxies) > 0 {
		return proxies[0]
	}
	return systemProxy()
}

// Status 返回当前生成服务状态
func (manager *runtimeManager) Status(ctx context.Context) (api.AdminStatus, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.Status(ctx)
}

// Accounts 返回当前生成服务账户
func (manager *runtimeManager) Accounts(ctx context.Context) ([]api.AdminAccount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.Accounts(ctx)
}

// CreateAccount 在当前生成服务创建账户
func (manager *runtimeManager) CreateAccount(ctx context.Context, input api.AccountCreateInput, view *camoufoxnative.LiveView) (api.AdminAccount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.CreateAccount(ctx, input, view)
}

// ChromeImportProfiles 返回当前生成服务可导入的 Chrome 账号
func (manager *runtimeManager) ChromeImportProfiles(ctx context.Context) ([]api.ChromeImportProfile, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.ChromeImportProfiles(ctx)
}

// BrowserImportProfiles 返回当前生成服务可导入的指定浏览器账号
func (manager *runtimeManager) BrowserImportProfiles(ctx context.Context, browser string) ([]api.BrowserImportProfile, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.BrowserImportProfiles(ctx, browser)
}

// ImportBrowserAccounts 在当前生成服务批量导入指定浏览器账号
func (manager *runtimeManager) ImportBrowserAccounts(ctx context.Context, input api.BrowserImportInput) ([]api.AdminAccount, []api.ImportFailure, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.ImportBrowserAccounts(ctx, input)
}

// CookieBrowsers 返回当前生成服务发现的本机调试浏览器
func (manager *runtimeManager) CookieBrowsers(ctx context.Context) ([]api.CookieBrowser, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.CookieBrowsers(ctx)
}

// DiscoverCookieAccounts 返回粘贴 Cookie 中各会话序号登录的账户并标记已导入的账户
func (manager *runtimeManager) DiscoverCookieAccounts(ctx context.Context, input api.CookieImportInput) ([]api.CookieBrowser, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.DiscoverCookieAccounts(ctx, input)
}

// ImportChromeAccounts 在当前生成服务批量导入 Chrome 账号
func (manager *runtimeManager) ImportChromeAccounts(ctx context.Context, input api.ChromeImportInput) ([]api.AdminAccount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.ImportChromeAccounts(ctx, input)
}

// ImportAccountState 在当前生成服务导入远程配对或 Cookie 导入的账户
func (manager *runtimeManager) ImportAccountState(ctx context.Context, input api.AccountStateInput) (api.AdminAccount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.ImportAccountState(ctx, input)
}

// UpdateAccount 在当前生成服务更新账户
func (manager *runtimeManager) UpdateAccount(ctx context.Context, id string, input api.AccountInput) (api.AdminAccount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.UpdateAccount(ctx, id, input)
}

// DeleteAccount 在当前生成服务删除账户
func (manager *runtimeManager) DeleteAccount(ctx context.Context, id string) error {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.DeleteAccount(ctx, id)
}

// LoginAccount 在当前生成服务登录账户
func (manager *runtimeManager) LoginAccount(ctx context.Context, id string, view *camoufoxnative.LiveView) (api.AdminAccount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.LoginAccount(ctx, id, view)
}

// RefreshAccount 在当前生成服务立即刷新账户状态
func (manager *runtimeManager) RefreshAccount(ctx context.Context, id string) (api.AdminAccountRefresh, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.RefreshAccount(ctx, id)
}

// ClearLogs 清空进程级管理日志
func (manager *runtimeManager) ClearLogs(context.Context) error {
	manager.requests.clearLogs()
	return nil
}

// RuntimeConfig 返回已保存配置与进程级生效状态
func (manager *runtimeManager) RuntimeConfig(ctx context.Context) (api.RuntimeConfig, error) {
	manager.mu.RLock()
	value, err := manager.current.admin.RuntimeConfig(ctx)
	if err == nil {
		value = manager.decorateRuntimeConfig(value, manager.current.config)
	}
	manager.mu.RUnlock()
	return value, err
}

// UpdateRuntimeConfig 保存生成服务配置，账户选择策略与容量设置立即作用于当前生成服务
func (manager *runtimeManager) UpdateRuntimeConfig(ctx context.Context, value api.RuntimeConfig) (api.RuntimeConfig, error) {
	manager.mu.Lock()
	updated, err := manager.current.admin.UpdateRuntimeConfig(ctx, value)
	if err == nil {
		manager.current.service.ApplyCapacity(
			updated.WarmWorkerLimit, updated.MaxActiveWorkers, updated.WarmStartupConcurrency, updated.PerAccountConcurrency,
		)
		updated = manager.decorateRuntimeConfig(updated, manager.current.config)
		manager.ledger.SetBodyCapture(updated.RequestBodyLog)
		err = manager.logs.set(updated.LogFile, updated.LogMaxTotalMB)
	}
	manager.mu.Unlock()
	return updated, err
}

// Cooldowns 返回当前生成服务冷却状态
func (manager *runtimeManager) Cooldowns(ctx context.Context) ([]api.AdminCooldown, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.Cooldowns(ctx)
}

// ClearCooldown 手动清除账户在一个作用域上的冷却
func (manager *runtimeManager) ClearCooldown(ctx context.Context, accountID string, scope string) error {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.ClearCooldown(ctx, accountID, scope)
}

// Requests 返回进程级活动请求
func (manager *runtimeManager) Requests(ctx context.Context) ([]api.AdminRequest, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.Requests(ctx)
}

// CancelRequest 取消进程级活动请求
func (manager *runtimeManager) CancelRequest(ctx context.Context, id string) error {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.CancelRequest(ctx, id)
}

// Events 创建生成服务实例切换期间持续可用的管理事件流
func (manager *runtimeManager) Events(ctx context.Context) (<-chan api.AdminEvent, error) {
	return openAdminEvents(ctx, manager.lifecycle, manager.requests, manager)
}

// RecordAccessStart 记录公开 API 请求开始
func (manager *runtimeManager) RecordAccessStart(entry api.AccessLog) {
	manager.mu.RLock()
	manager.current.admin.RecordAccessStart(entry)
	manager.mu.RUnlock()
}

// RecordAccessLog 记录公开 API 请求结果，通过 API key 校验的 POST 请求同时写入用量账本
func (manager *runtimeManager) RecordAccessLog(entry api.AccessLog) {
	manager.mu.RLock()
	manager.current.admin.RecordAccessLog(entry)
	manager.mu.RUnlock()
	if entry.Method == http.MethodPost && entry.Authorized {
		manager.ledger.Record(requestRow(entry, time.Now().UTC()))
	}
}

// decorateRuntimeConfig 标记配置所属的进程级与生成服务生效时机
func (manager *runtimeManager) decorateRuntimeConfig(value api.RuntimeConfig, active config.Config) api.RuntimeConfig {
	value.ActiveListenAddr = manager.activeManagement.ListenAddr
	value.ReservedListenAddr = manager.reservedListenAddr
	value.ActiveAPIKey = manager.activeManagement.ProxyAPIKey
	value.ActivePreviewListenAddr = manager.previewAddress
	value.ManagementRestartRequired = value.ListenAddr != value.ActiveListenAddr && value.ListenAddr != value.ReservedListenAddr || value.APIKey != value.ActiveAPIKey ||
		value.PreviewListenAddr != manager.activeManagement.PreviewListenAddr
	value.ManagementRestartRequired = value.ManagementRestartRequired ||
		value.AdminAuthEnabled != manager.activeManagement.AdminAuthEnabled ||
		value.AdminUsername != manager.activeManagement.AdminUsername ||
		value.SavedAdminPassword != manager.activeManagement.AdminPassword
	value.ServiceRestartRequired = !sameDataConfig(value, active, manager.overrides)
	return value
}

// sameDataConfig 比较已保存配置与当前生成服务配置
func sameDataConfig(value api.RuntimeConfig, active config.Config, overrides dataConfigOverrides) bool {
	initTimeout, _ := time.ParseDuration(value.InitTimeout)
	requestTimeout, _ := time.ParseDuration(value.RequestTimeout)
	saved := config.Config{
		AuthStates: value.AuthStates, Proxy: value.Proxy,
		InitTimeout: initTimeout, RequestTimeout: requestTimeout, TemporaryChat: value.TemporaryChat,
		UpstreamChannels:     value.UpstreamChannels,
		ModelMapping:         value.ModelMapping,
		WAABackend:           value.WAABackend,
		BuildNativeNonstream: value.BuildNativeNonstream,
		NonstreamKeepalive:   value.NonstreamKeepalive,
		FakeStream:           value.FakeStream,
		YouTubeLinks:         value.YouTubeLinks,
		SearchModelAliases:   value.SearchModelAliases,
		GoogleSearch:         value.GoogleSearch,
		FirstEventHedge:      value.FirstEventHedge,
		ContinuationRewrite:  value.ContinuationRewrite,
		ContinuationPrompt:   value.ContinuationPrompt,
		MediaOutput:          value.MediaOutput,
		MediaBaseURL:         value.MediaBaseURL,
	}
	overrides.Apply(&saved)
	return saved.AuthStates == active.AuthStates && saved.Proxy == active.Proxy &&
		saved.InitTimeout == active.InitTimeout && saved.RequestTimeout == active.RequestTimeout &&
		saved.TemporaryChat == active.TemporaryChat &&
		slices.Equal(saved.UpstreamChannels, active.UpstreamChannels) && saved.ModelMapping == active.ModelMapping &&
		saved.WAABackend == active.WAABackend &&
		saved.BuildNativeNonstream == active.BuildNativeNonstream &&
		saved.NonstreamKeepalive == active.NonstreamKeepalive && saved.FakeStream == active.FakeStream &&
		saved.YouTubeLinks == active.YouTubeLinks && saved.FirstEventHedge == active.FirstEventHedge &&
		saved.SearchModelAliases == active.SearchModelAliases && saved.GoogleSearch == active.GoogleSearch &&
		saved.ContinuationRewrite == active.ContinuationRewrite && saved.ContinuationPrompt == active.ContinuationPrompt &&
		saved.MediaOutput == active.MediaOutput && saved.MediaBaseURL == active.MediaBaseURL
}

var _ aistudio.Service = (*runtimeManager)(nil)
var _ aistudio.VideoService = (*runtimeManager)(nil)
var _ aistudio.FileService = (*runtimeManager)(nil)
var _ aistudio.BidiService = (*runtimeManager)(nil)
var _ aistudio.TranscriptionService = (*runtimeManager)(nil)
var _ api.AdminService = (*runtimeManager)(nil)
