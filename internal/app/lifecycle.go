package app

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
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
	SetAPIKeyAccounts(map[string][]string)
	SetRequestPolicy(mapping []config.ModelAlias, maxAccountSwitches int)
}

// generationAdmin 表示进程级管理器转交给当前生成服务的管理操作
type generationAdmin interface {
	Status(context.Context) (api.AdminStatus, error)
	CatalogModels(context.Context) ([]aistudio.Model, error)
	Accounts(context.Context) ([]api.AdminAccount, error)
	AccountsChanged() <-chan struct{}
	AccountExits(context.Context) ([]api.AccountExit, error)
	CreateAccount(context.Context, api.AccountCreateInput, *camoufoxnative.LiveView) (api.AdminAccount, error)
	ChromeImportProfiles(context.Context) ([]api.ChromeImportProfile, error)
	CheckChromeImportTiers(context.Context, api.ChromeTierCheckInput, func(api.ChromeProfileTier)) error
	CookieBrowsers(ctx context.Context, proxy string) ([]api.CookieBrowser, error)
	DiscoverCookieAccounts(context.Context, api.CookieImportInput) ([]api.CookieBrowser, error)
	ImportChromeAccounts(context.Context, api.ChromeImportInput) ([]api.AdminAccount, error)
	ImportAccountState(context.Context, api.AccountStateInput) (api.AdminAccount, error)
	RenewAccountCookies(ctx context.Context, accountID string, token string, cookies []aistudio.StateCookie) (api.AdminAccount, error)
	UpdateAccount(context.Context, string, api.AccountInput) (api.AdminAccount, error)
	SetAccountState(ctx context.Context, accountID string, state string) (api.AdminAccount, error)
	DeleteAccount(context.Context, string) error
	LoginAccount(context.Context, string, *camoufoxnative.LiveView) (api.AdminAccount, error)
	VerifyAccount(context.Context, string, *camoufoxnative.LiveView) (api.AdminAccount, error)
	AttachAccountLiveView(ctx context.Context, accountID string, view *camoufoxnative.LiveView) (func(), error)
	LiveViewAvailability(ctx context.Context, accountID string) (api.LiveViewAvailability, error)
	RefreshAccount(context.Context, string) (api.AdminAccountRefresh, error)
	StartService(context.Context) (api.AdminStatus, error)
	StopService(context.Context) (api.AdminStatus, error)
	RuntimeConfig(context.Context) (api.RuntimeConfig, error)
	UpdateRuntimeConfig(context.Context, api.RuntimeConfig) (api.RuntimeConfig, error)
	ApplyAccountSelection(config.Config)
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
	// camoufoxWait 在启动开始等待 Camoufox 下载时关闭，不需要 Camoufox 时为 nil
	camoufoxWait <-chan struct{}
	// calls 是经 runtimeManager 转交给该实例、尚未结束的调用
	calls sync.WaitGroup
}

// runtimeFactory 创建一个完整生成服务实例
type runtimeFactory func(context.Context, context.Context, config.Config, *requestRegistry) (*runtimeGeneration, error)

// Close 取消当前生成服务实例，等经它进行中的调用结束后释放运行时
func (generation *runtimeGeneration) Close() error {
	generation.lifecycleCancel()
	generation.calls.Wait()
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
	lifecycle context.Context
	// pickPath 以系统对话框选择账户目录，命令行版为 nil
	pickPath         func(start string) (string, error)
	configPath       string
	activeManagement config.Config
	// reservedListenAddr 是被 Windows 保留而改用 activeManagement.ListenAddr 监听的配置地址，没有改用时为空
	reservedListenAddr string
	previewAddress     string
	overrides          dataConfigOverrides
	requests           *requestRegistry
	factory            runtimeFactory
	startMu            sync.Mutex
	// configMu 让保存配置逐次进行
	configMu sync.Mutex
	// mu 保护 current，只在取得与发布生成服务实例引用期间持有
	mu      sync.RWMutex
	current *runtimeGeneration
	// accountsMu 由账户的续签与删除在操作期间、新增、导入、登录与更新在提交结果期间持读锁，生成服务换代从停止旧生成服务到发布新生成服务持写锁
	accountsMu sync.RWMutex
	// initial 是进程启动时装配、尚未启动过的生成服务，自动启动时配置未变就直接启动它而不重新载入账户
	initial *runtimeGeneration
	// launchMu 保护 startCancel、launching、launchReleased 与 autoStartCancel
	launchMu    sync.Mutex
	startCancel context.CancelFunc
	// launching 在生成服务启动期间存在，启动结束时关闭
	launching chan struct{}
	// launchReleased 在本轮启动开始等待 Camoufox 下载时关闭，Camoufox 就绪前等待启动的公开请求不再等待
	launchReleased  chan struct{}
	autoStartCancel context.CancelFunc
	// settings 是已保存的只决定新请求行为的设置
	settings atomic.Pointer[requestSettings]
	ledger   *requestdb.Store
	prompts  *promptdb.Store
	states   api.StateStore
	logs     *logOutput
}

// newRuntimeManager 创建进程级管理器与初始生成服务
func newRuntimeManager(
	ctx context.Context,
	configPath string,
	cfg config.Config,
	overrides dataConfigOverrides,
) (*runtimeManager, error) {
	requests := newRequestRegistry()
	manager := &runtimeManager{
		lifecycle: ctx, configPath: configPath, activeManagement: cfg,
		overrides: overrides, requests: requests, factory: buildRuntimeGeneration,
	}
	generation, err := manager.factory(ctx, ctx, cfg, requests)
	if err != nil {
		requests.close()
		return nil, err
	}
	generation.service.SetAPIKeyAccounts(apiKeyAccounts(cfg.ProxyAPIKey, cfg.APIKeyAccounts, cfg))
	manager.settings.Store(requestSettingsOf(cfg))
	manager.current = generation
	manager.wireAccounts(generation)
	if cfg.AutoStart {
		manager.initial = generation
	}
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
		lifecycleCancel: lifecycleCancel, closeRuntime: closeRuntime, camoufoxWait: service.camoufoxWait,
	}, nil
}

// StartService 取消自动启动的重试后从最新配置启动生成服务
func (manager *runtimeManager) StartService(ctx context.Context) (api.AdminStatus, error) {
	manager.stopAutoStart()
	return manager.startService(ctx)
}

// startService 从最新配置创建并启动新生成服务，发布新生成服务时与保存设置互斥并按此时已保存的配置应用立即生效的设置
func (manager *runtimeManager) startService(ctx context.Context) (api.AdminStatus, error) {
	manager.startMu.Lock()
	defer manager.startMu.Unlock()

	manager.mu.RLock()
	current := manager.current
	manager.mu.RUnlock()
	if current.service.State() != "STOPPED" {
		return current.admin.StartService(ctx)
	}
	launchCtx, launchCancel := context.WithCancel(manager.lifecycle)
	launching, released := make(chan struct{}), make(chan struct{})
	manager.launchMu.Lock()
	manager.startCancel, manager.launching, manager.launchReleased = launchCancel, launching, released
	manager.launchMu.Unlock()
	initial := manager.initial
	manager.initial = nil
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

	next := current
	release := func() {}
	if initial != current || !reflect.DeepEqual(cfg, current.config) {
		manager.accountsMu.Lock()
		release = manager.accountsMu.Unlock
		if _, err := current.admin.StopService(ctx); err != nil {
			release()
			manager.finishStart(launchCancel)
			return api.AdminStatus{}, err
		}
		next, err = manager.factory(launchCtx, manager.lifecycle, cfg, manager.requests)
		if err != nil {
			release()
			manager.finishStart(launchCancel)
			return api.AdminStatus{}, err
		}
		manager.wireAccounts(next)
	}
	if launchCtx.Err() != nil {
		release()
		manager.finishStart(launchCancel)
		if next != current {
			_ = next.Close()
		}
		return manager.Status(ctx)
	}

	manager.configMu.Lock()
	saved, err := config.Load(manager.configPath)
	if err != nil {
		manager.configMu.Unlock()
		release()
		manager.finishStart(launchCancel)
		if next != current {
			_ = next.Close()
		}
		return api.AdminStatus{}, err
	}
	next.admin.ApplyAccountSelection(saved)
	next.service.SetAPIKeyAccounts(apiKeyAccounts(saved.ProxyAPIKey, saved.APIKeyAccounts, manager.activeManagement))
	next.service.ApplyCapacity(saved.WarmWorkerLimit, saved.MaxActiveWorkers, saved.WarmStartupConcurrency, saved.PerAccountConcurrency)
	mapping, _ := config.ParseModelMapping(saved.ModelMapping)
	next.service.SetRequestPolicy(mapping, saved.MaxAccountSwitches)
	manager.settings.Store(requestSettingsOf(saved))
	manager.mu.Lock()
	current = manager.current
	manager.current = next
	manager.mu.Unlock()
	manager.configMu.Unlock()
	release()
	if next.camoufoxWait != nil {
		go func() {
			select {
			case <-next.camoufoxWait:
				close(released)
			case <-launching:
			}
		}()
	}

	if next != current {
		current.lifecycleCancel()
	}
	status, startErr := next.admin.StartService(launchCtx)
	manager.finishStart(launchCancel)
	if next != current {
		if err := current.Close(); err != nil {
			manager.requests.log("service", "WARN", "旧生成服务关闭失败", "error", err)
		}
	}
	return status, startErr
}

// finishStart 清理本轮生成服务启动取消句柄并放行等待启动结束的请求
func (manager *runtimeManager) finishStart(cancel context.CancelFunc) {
	manager.launchMu.Lock()
	manager.startCancel = nil
	close(manager.launching)
	manager.launching, manager.launchReleased = nil, nil
	manager.launchMu.Unlock()
	cancel()
}

// WaitLaunch 在生成服务启动期间等到启动结束或 ctx 结束，启动正在等待 Camoufox 下载时立即返回
func (manager *runtimeManager) WaitLaunch(ctx context.Context) {
	manager.launchMu.Lock()
	launching, released := manager.launching, manager.launchReleased
	manager.launchMu.Unlock()
	if launching == nil {
		return
	}
	select {
	case <-launching:
		return
	case <-released:
	case <-ctx.Done():
		return
	}
	if !camoufoxnative.PreparationState().Ready {
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
	manager.launchMu.Lock()
	cancel := manager.startCancel
	manager.launchMu.Unlock()
	manager.mu.RLock()
	current := manager.current
	manager.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	return current.admin.StopService(ctx)
}

// DownloadCamoufox 在 Camoufox 未就绪且没有进行中的下载时经出口代理在后台开始下载
func (manager *runtimeManager) DownloadCamoufox(ctx context.Context) (api.AdminStatus, error) {
	camoufoxnative.PrepareInBackground(manager.egressProxy())
	return manager.Status(ctx)
}

// autoStartContext 返回手动启动或停止生成服务时取消的自动启动上下文
func (manager *runtimeManager) autoStartContext(parent context.Context) context.Context {
	ctx, cancel := context.WithCancel(parent)
	manager.launchMu.Lock()
	manager.autoStartCancel = cancel
	manager.launchMu.Unlock()
	return ctx
}

// stopAutoStart 取消自动启动及其重试
func (manager *runtimeManager) stopAutoStart() {
	manager.launchMu.Lock()
	cancel := manager.autoStartCancel
	manager.autoStartCancel = nil
	manager.launchMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Close 停止生成服务并释放当前生成服务实例
func (manager *runtimeManager) Close() error {
	_, stopErr := manager.StopService(context.Background())
	manager.mu.RLock()
	current := manager.current
	manager.mu.RUnlock()
	return errors.Join(stopErr, current.Close())
}

// requestSettings 是只决定新请求行为的设置，保存后下一个请求即采用，进行中的请求沿用开始时的值
type requestSettings struct {
	modes                   api.ResponseModes
	searchModelAliases      bool
	googleSearch            bool
	mediaOutput             string
	mediaBaseURL            string
	replyTool          bool
	userMessageRewrite string
	thinkingGuard      bool
}

// requestSettingsOf 返回 cfg 中只决定新请求行为的设置
func requestSettingsOf(cfg config.Config) *requestSettings {
	return &requestSettings{
		modes:              api.ResponseModes{NonstreamKeepalive: cfg.NonstreamKeepalive, FakeStream: cfg.FakeStream},
		searchModelAliases: cfg.SearchModelAliases,
		googleSearch:       cfg.GoogleSearch,
		mediaOutput:        cfg.MediaOutput,
		mediaBaseURL:       cfg.MediaBaseURL,
		replyTool:          cfg.ReplyTool,
		userMessageRewrite: cfg.UserMessageRewrite,
		thinkingGuard:      cfg.ThinkingGuard,
	}
}

// responseModes 返回已保存的非流式保活与假流式设置
func (manager *runtimeManager) responseModes() api.ResponseModes {
	return manager.settings.Load().modes
}

// searchSettings 返回已保存的搜索开关
func (manager *runtimeManager) searchSettings() (modelAliases, googleSearch bool) {
	settings := manager.settings.Load()
	return settings.searchModelAliases, settings.googleSearch
}

// mediaLinks 返回已保存的媒体输出是否使用 URL 与设置的服务地址
func (manager *runtimeManager) mediaLinks() (bool, string) {
	settings := manager.settings.Load()
	return settings.mediaOutput == config.MediaOutputURL, settings.mediaBaseURL
}

// generation 返回当前生成服务实例并登记一次调用，调用结束时执行返回的函数；全局锁只在取得实例引用期间持有
func (manager *runtimeManager) generation() (*runtimeGeneration, func()) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	current := manager.current
	current.calls.Add(1)
	return current, current.calls.Done
}

// Models 返回当前生成服务模型
func (manager *runtimeManager) Models(ctx context.Context) ([]aistudio.Model, error) {
	current, done := manager.generation()
	defer done()
	return current.service.Models(ctx)
}

// CatalogModels 返回当前生成服务的管理目录
func (manager *runtimeManager) CatalogModels(ctx context.Context) ([]aistudio.Model, error) {
	current, done := manager.generation()
	defer done()
	return current.admin.CatalogModels(ctx)
}

// CountTokens 由当前生成服务计数
func (manager *runtimeManager) CountTokens(ctx context.Context, request aistudio.TokenCountRequest) (aistudio.TokenCount, error) {
	current, done := manager.generation()
	defer done()
	return current.service.CountTokens(ctx, request)
}

// Generate 由当前生成服务生成事件流
func (manager *runtimeManager) Generate(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	if settings := manager.settings.Load(); settings != nil {
		request.ReplyTool = request.ReplyTool || settings.replyTool
		if request.UserMessageRewrite == "" {
			request.UserMessageRewrite = settings.userMessageRewrite
		}
		request.ThinkingGuard = request.ThinkingGuard || settings.thinkingGuard
	}
	current, done := manager.generation()
	defer done()
	return current.service.Generate(ctx, request)
}

// GenerateTitle 由当前生成服务生成试用页页头标题
func (manager *runtimeManager) GenerateTitle(ctx context.Context, request aistudio.TitleRequest) (string, error) {
	current, done := manager.generation()
	defer done()
	return current.service.(aistudio.TitleService).GenerateTitle(ctx, request)
}

// GenerateImage 由当前生成服务生成 Imagen 图片
func (manager *runtimeManager) GenerateImage(ctx context.Context, request aistudio.ImageRequest) ([]aistudio.GeneratedImage, error) {
	current, done := manager.generation()
	defer done()
	return current.service.(aistudio.ImageService).GenerateImage(ctx, request)
}

// CreateDecision 由当前生成服务回答决策问题
func (manager *runtimeManager) CreateDecision(ctx context.Context, request aistudio.DecisionRequest) (aistudio.DecisionResult, error) {
	current, done := manager.generation()
	defer done()
	return current.service.(aistudio.DecisionService).CreateDecision(ctx, request)
}

// GenerateVideo 由当前生成服务创建视频任务
func (manager *runtimeManager) GenerateVideo(ctx context.Context, request aistudio.VideoRequest) (aistudio.VideoOperation, error) {
	current, done := manager.generation()
	defer done()
	return current.service.(aistudio.VideoService).GenerateVideo(ctx, request)
}

// GetGenerateVideoOperation 由当前生成服务读取视频任务
func (manager *runtimeManager) GetGenerateVideoOperation(ctx context.Context, id string) (aistudio.VideoOperation, error) {
	current, done := manager.generation()
	defer done()
	return current.service.(aistudio.VideoService).GetGenerateVideoOperation(ctx, id)
}

// ListVideos 由当前生成服务列出视频任务
func (manager *runtimeManager) ListVideos(ctx context.Context) ([]aistudio.VideoOperation, error) {
	current, done := manager.generation()
	defer done()
	return current.service.(aistudio.VideoService).ListVideos(ctx)
}

// DeleteVideo 由当前生成服务删除视频任务
func (manager *runtimeManager) DeleteVideo(ctx context.Context, id string) error {
	current, done := manager.generation()
	defer done()
	return current.service.(aistudio.VideoService).DeleteVideo(ctx, id)
}

// DownloadFile 由当前生成服务下载文件
func (manager *runtimeManager) DownloadFile(ctx context.Context, id string) (aistudio.MediaStream, error) {
	current, done := manager.generation()
	defer done()
	return current.service.(aistudio.VideoService).DownloadFile(ctx, id)
}

// UploadFile 由当前生成服务上传文件
func (manager *runtimeManager) UploadFile(ctx context.Context, request aistudio.UploadRequest) (aistudio.FileRef, error) {
	current, done := manager.generation()
	defer done()
	return current.service.(aistudio.FileService).UploadFile(ctx, request)
}

// VideoFrameTokens 由当前生成服务统计视频文件按视频设置抽帧后的 token 数
func (manager *runtimeManager) VideoFrameTokens(ctx context.Context, request aistudio.VideoFramesRequest) (int64, error) {
	current, done := manager.generation()
	defer done()
	return current.service.(aistudio.VideoFrameService).VideoFrameTokens(ctx, request)
}

// FileMetadata 由当前生成服务读取文件元数据
func (manager *runtimeManager) FileMetadata(ctx context.Context, id string) (aistudio.FileMetadata, error) {
	current, done := manager.generation()
	defer done()
	return current.service.(aistudio.FileService).FileMetadata(ctx, id)
}

// ListFiles 由当前生成服务列出上传文件
func (manager *runtimeManager) ListFiles(ctx context.Context) ([]aistudio.FileMetadata, error) {
	current, done := manager.generation()
	defer done()
	return current.service.(aistudio.FileService).ListFiles(ctx)
}

// DeleteFile 由当前生成服务删除文件
func (manager *runtimeManager) DeleteFile(ctx context.Context, id string) error {
	current, done := manager.generation()
	defer done()
	return current.service.(aistudio.FileService).DeleteFile(ctx, id)
}

// OpenBidi 由当前生成服务创建实时会话
func (manager *runtimeManager) OpenBidi(ctx context.Context, request aistudio.BidiRequest) (*aistudio.BidiSession, error) {
	current, done := manager.generation()
	defer done()
	return current.service.(aistudio.BidiService).OpenBidi(ctx, request)
}

// Transcribe 由当前生成服务执行音频转录
func (manager *runtimeManager) Transcribe(ctx context.Context, request aistudio.TranscriptionRequest) (aistudio.TranscriptionResult, error) {
	current, done := manager.generation()
	defer done()
	return current.service.(aistudio.TranscriptionService).Transcribe(ctx, request)
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
	current, done := manager.generation()
	defer done()
	return current.admin.Status(ctx)
}

// Accounts 返回当前生成服务账户
func (manager *runtimeManager) Accounts(ctx context.Context) ([]api.AdminAccount, error) {
	current, done := manager.generation()
	defer done()
	return current.admin.Accounts(ctx)
}

// AccountsChanged 返回当前生成服务账户的租约或状态下一次变化时关闭的通道
func (manager *runtimeManager) AccountsChanged() <-chan struct{} {
	current, done := manager.generation()
	defer done()
	return current.admin.AccountsChanged()
}

// AccountExits 查询当前生成服务各账户出口的公网 IP 与地理位置
func (manager *runtimeManager) AccountExits(ctx context.Context) ([]api.AccountExit, error) {
	current, done := manager.generation()
	defer done()
	return current.admin.AccountExits(ctx)
}

// accountAdmin 返回当前生成服务的管理接口与结束本次账户操作的函数，结束前生成服务不换代
func (manager *runtimeManager) accountAdmin() (generationAdmin, func()) {
	manager.accountsMu.RLock()
	return manager.currentAdmin(), manager.accountsMu.RUnlock
}

// currentConfig 返回当前生成服务实例的配置
func (manager *runtimeManager) currentConfig() config.Config {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.config
}

// currentAdmin 返回当前生成服务的管理接口，不阻止之后的换代
func (manager *runtimeManager) currentAdmin() generationAdmin {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin
}

// wireAccounts 让生成服务的浏览器登录与导入在准备完成后经 accountAdmin 提交到提交时的当前生成服务
func (manager *runtimeManager) wireAccounts(generation *runtimeGeneration) {
	if admin, ok := generation.admin.(*runtimeAdmin); ok {
		admin.accountCommit = func() (*runtimeAdmin, func()) {
			target, done := manager.accountAdmin()
			return target.(*runtimeAdmin), done
		}
	}
}

// CreateAccount 等 Camoufox 就绪后以浏览器登录创建账户，登录期间不阻止生成服务换代，结果提交到提交时的生成服务
func (manager *runtimeManager) CreateAccount(ctx context.Context, input api.AccountCreateInput, view *camoufoxnative.LiveView) (api.AdminAccount, error) {
	if err := manager.prepareLoginCamoufox(ctx, "auth"); err != nil {
		return api.AdminAccount{}, err
	}
	return manager.currentAdmin().CreateAccount(ctx, input, view)
}

// ChromeImportProfiles 返回当前生成服务可导入的 Chrome 账号
func (manager *runtimeManager) ChromeImportProfiles(ctx context.Context) ([]api.ChromeImportProfile, error) {
	current, done := manager.generation()
	defer done()
	return current.admin.ChromeImportProfiles(ctx)
}

// CheckChromeImportTiers 按需检测选中的本机 Chrome 账号的 AI Studio 权益并逐条回调结果
func (manager *runtimeManager) CheckChromeImportTiers(ctx context.Context, input api.ChromeTierCheckInput, emit func(api.ChromeProfileTier)) error {
	current, done := manager.generation()
	defer done()
	return current.admin.CheckChromeImportTiers(ctx, input, emit)
}

// CookieBrowsers 返回当前生成服务发现的本机调试浏览器，proxy 为识别账户的请求出口
func (manager *runtimeManager) CookieBrowsers(ctx context.Context, proxy string) ([]api.CookieBrowser, error) {
	current, done := manager.generation()
	defer done()
	return current.admin.CookieBrowsers(ctx, proxy)
}

// DiscoverCookieAccounts 返回粘贴 Cookie 中各会话序号登录的账户并标记已导入的账户
func (manager *runtimeManager) DiscoverCookieAccounts(ctx context.Context, input api.CookieImportInput) ([]api.CookieBrowser, error) {
	current, done := manager.generation()
	defer done()
	return current.admin.DiscoverCookieAccounts(ctx, input)
}

// ImportChromeAccounts 在当前生成服务批量导入 Chrome 账号
func (manager *runtimeManager) ImportChromeAccounts(ctx context.Context, input api.ChromeImportInput) ([]api.AdminAccount, error) {
	return manager.currentAdmin().ImportChromeAccounts(ctx, input)
}

// ImportAccountState 在当前生成服务导入远程配对或 Cookie 导入的账户
func (manager *runtimeManager) ImportAccountState(ctx context.Context, input api.AccountStateInput) (api.AdminAccount, error) {
	return manager.currentAdmin().ImportAccountState(ctx, input)
}

// RenewAccountCookies 在当前生成服务以配对续签凭据替换账户的会话 Cookie
func (manager *runtimeManager) RenewAccountCookies(ctx context.Context, accountID string, token string, cookies []aistudio.StateCookie) (api.AdminAccount, error) {
	admin, done := manager.accountAdmin()
	defer done()
	return admin.RenewAccountCookies(ctx, accountID, token, cookies)
}

// UpdateAccount 在当前生成服务更新账户，保存结果时取账户读锁，之后同步模型目录不阻止生成服务换代
func (manager *runtimeManager) UpdateAccount(ctx context.Context, id string, input api.AccountInput) (api.AdminAccount, error) {
	return manager.currentAdmin().UpdateAccount(ctx, id, input)
}

// SetAccountState 在当前生成服务把账户改为就绪或停用
func (manager *runtimeManager) SetAccountState(ctx context.Context, id string, state string) (api.AdminAccount, error) {
	return manager.currentAdmin().SetAccountState(ctx, id, state)
}

// DeleteAccount 在当前生成服务删除账户
func (manager *runtimeManager) DeleteAccount(ctx context.Context, id string) error {
	admin, done := manager.accountAdmin()
	defer done()
	return admin.DeleteAccount(ctx, id)
}

// LoginAccount 等 Camoufox 就绪后重新登录账户，登录期间不阻止生成服务换代，结果提交到提交时的生成服务
func (manager *runtimeManager) LoginAccount(ctx context.Context, id string, view *camoufoxnative.LiveView) (api.AdminAccount, error) {
	if err := manager.prepareLoginCamoufox(ctx, id); err != nil {
		return api.AdminAccount{}, err
	}
	return manager.currentAdmin().LoginAccount(ctx, id, view)
}

// VerifyAccount 等 Camoufox 就绪后打开账户验证页并自动再核对，期间不阻止生成服务换代
func (manager *runtimeManager) VerifyAccount(ctx context.Context, id string, view *camoufoxnative.LiveView) (api.AdminAccount, error) {
	if err := manager.prepareLoginCamoufox(ctx, id); err != nil {
		return api.AdminAccount{}, err
	}
	return manager.currentAdmin().VerifyAccount(ctx, id, view)
}

// AttachAccountLiveView 把远程操作窗口接到当前生成服务中账户的常驻浏览器
func (manager *runtimeManager) AttachAccountLiveView(ctx context.Context, id string, view *camoufoxnative.LiveView) (func(), error) {
	return manager.currentAdmin().AttachAccountLiveView(ctx, id, view)
}

// LiveViewAvailability 返回当前生成服务中账户远程操作的可用性
func (manager *runtimeManager) LiveViewAvailability(ctx context.Context, id string) (api.LiveViewAvailability, error) {
	return manager.currentAdmin().LiveViewAvailability(ctx, id)
}

// prepareLoginCamoufox 在浏览器登录前等 Camoufox 就绪，需要下载时记录等待，失败时记录原因
func (manager *runtimeManager) prepareLoginCamoufox(ctx context.Context, source string) error {
	if !camoufoxnative.PreparationState().Ready {
		manager.requests.log(source, "INFO", "浏览器登录等待 Camoufox 下载")
		camoufoxnative.PrepareInBackground(manager.egressProxy())
		if status, err := manager.Status(ctx); err == nil {
			manager.requests.publish(api.AdminEvent{Type: "status", Data: status})
		}
	}
	_, err := camoufoxnative.Prepare(ctx, manager.egressProxy())
	if err != nil {
		manager.requests.log(source, "ERROR", "浏览器登录失败", "error", err)
	}
	return err
}

// RefreshAccount 在当前生成服务立即刷新账户状态，刷新期间不阻止生成服务换代
func (manager *runtimeManager) RefreshAccount(ctx context.Context, id string) (api.AdminAccountRefresh, error) {
	return manager.currentAdmin().RefreshAccount(ctx, id)
}

// ClearLogs 清空进程级管理日志
func (manager *runtimeManager) ClearLogs(context.Context) error {
	manager.requests.clearLogs()
	return nil
}

// RuntimeConfig 返回已保存配置与进程级生效状态
func (manager *runtimeManager) RuntimeConfig(ctx context.Context) (api.RuntimeConfig, error) {
	current, done := manager.generation()
	defer done()
	value, err := current.admin.RuntimeConfig(ctx)
	if err == nil {
		value = manager.decorateRuntimeConfig(value, current.config)
	}
	return value, err
}

// UpdateRuntimeConfig 保存生成服务配置，账户选择策略、容量设置与只决定新请求行为的设置立即生效
func (manager *runtimeManager) UpdateRuntimeConfig(ctx context.Context, value api.RuntimeConfig) (api.RuntimeConfig, error) {
	manager.configMu.Lock()
	defer manager.configMu.Unlock()
	current, done := manager.generation()
	defer done()
	updated, err := current.admin.UpdateRuntimeConfig(ctx, value)
	if err != nil {
		return updated, err
	}
	current.service.SetAPIKeyAccounts(apiKeyAccounts(updated.APIKey, updated.APIKeyAccounts, manager.activeManagement))
	current.service.ApplyCapacity(
		updated.WarmWorkerLimit, updated.MaxActiveWorkers, updated.WarmStartupConcurrency, updated.PerAccountConcurrency,
	)
	mapping, _ := config.ParseModelMapping(updated.ModelMapping)
	current.service.SetRequestPolicy(mapping, updated.MaxAccountSwitches)
	manager.settings.Store(&requestSettings{
		modes:              api.ResponseModes{NonstreamKeepalive: updated.NonstreamKeepalive, FakeStream: updated.FakeStream},
		searchModelAliases: updated.SearchModelAliases,
		googleSearch:       updated.GoogleSearch,
		mediaOutput:        updated.MediaOutput,
		mediaBaseURL:       updated.MediaBaseURL,
		replyTool:          updated.ReplyTool,
		userMessageRewrite: updated.UserMessageRewrite,
		thinkingGuard:      updated.ThinkingGuard,
	})
	updated = manager.decorateRuntimeConfig(updated, current.config)
	manager.ledger.SetBodyCapture(updated.RequestBodyLog)
	return updated, manager.logs.set(updated.LogFile, updated.LogMaxTotalMB)
}

// Cooldowns 返回当前生成服务冷却状态
func (manager *runtimeManager) Cooldowns(ctx context.Context) ([]api.AdminCooldown, error) {
	current, done := manager.generation()
	defer done()
	return current.admin.Cooldowns(ctx)
}

// ClearCooldown 手动清除账户在一个作用域上的冷却
func (manager *runtimeManager) ClearCooldown(ctx context.Context, accountID string, scope string) error {
	current, done := manager.generation()
	defer done()
	return current.admin.ClearCooldown(ctx, accountID, scope)
}

// Requests 返回进程级活动请求
func (manager *runtimeManager) Requests(ctx context.Context) ([]api.AdminRequest, error) {
	current, done := manager.generation()
	defer done()
	return current.admin.Requests(ctx)
}

// CancelRequest 取消进程级活动请求
func (manager *runtimeManager) CancelRequest(ctx context.Context, id string) error {
	current, done := manager.generation()
	defer done()
	return current.admin.CancelRequest(ctx, id)
}

// Events 创建生成服务实例切换期间持续可用的管理事件流
func (manager *runtimeManager) Events(ctx context.Context) (<-chan api.AdminEvent, error) {
	return openAdminEvents(ctx, manager.lifecycle, manager.requests, manager)
}

// RecordAccessStart 记录公开 API 请求开始
func (manager *runtimeManager) RecordAccessStart(entry api.AccessLog) {
	current, done := manager.generation()
	defer done()
	current.admin.RecordAccessStart(entry)
}

// RecordAccessLog 记录公开 API 请求结果，通过 API key 校验的 POST 请求同时写入用量账本
func (manager *runtimeManager) RecordAccessLog(entry api.AccessLog) {
	current, done := manager.generation()
	current.admin.RecordAccessLog(entry)
	done()
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
		InitTimeout: initTimeout, RequestTimeout: requestTimeout, StripThoughtSignatures: value.StripThoughtSignatures,
		UpstreamChannels:      value.UpstreamChannels,
		WAABackend:            value.WAABackend,
		BuildNativeNonstream:  value.BuildNativeNonstream,
		YouTubeLinks:          value.YouTubeLinks,
		DiscoverHiddenModels:  value.DiscoverHiddenModels,
		FirstEventHedge:       value.FirstEventHedge,
		FirstEventHedgeModels: value.FirstEventHedgeModels,
		FirstEventTimeout:     value.FirstEventTimeout,
		ContinuationRewrite:   value.ContinuationRewrite,
		ContinuationPrompt:    value.ContinuationPrompt,
	}
	overrides.Apply(&saved)
	return saved.AuthStates == active.AuthStates && saved.Proxy == active.Proxy &&
		saved.InitTimeout == active.InitTimeout && saved.RequestTimeout == active.RequestTimeout &&
		slices.Equal(saved.UpstreamChannels, active.UpstreamChannels) &&
		saved.WAABackend == active.WAABackend &&
		saved.BuildNativeNonstream == active.BuildNativeNonstream &&
		saved.YouTubeLinks == active.YouTubeLinks && saved.StripThoughtSignatures == active.StripThoughtSignatures && saved.FirstEventHedge == active.FirstEventHedge && saved.FirstEventHedgeModels == active.FirstEventHedgeModels && saved.FirstEventTimeout == active.FirstEventTimeout &&
		saved.DiscoverHiddenModels == active.DiscoverHiddenModels &&
		saved.ContinuationRewrite == active.ContinuationRewrite && saved.ContinuationPrompt == active.ContinuationPrompt
}

var _ aistudio.Service = (*runtimeManager)(nil)
var _ aistudio.VideoService = (*runtimeManager)(nil)
var _ aistudio.FileService = (*runtimeManager)(nil)
var _ aistudio.BidiService = (*runtimeManager)(nil)
var _ aistudio.TranscriptionService = (*runtimeManager)(nil)
var _ api.AdminService = (*runtimeManager)(nil)
