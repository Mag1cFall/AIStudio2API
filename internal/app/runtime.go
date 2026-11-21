package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

// newRuntime 装配单个生成服务实例
func newRuntime(
	launchCtx context.Context,
	lifecycle context.Context,
	cfg config.Config,
	requests *requestRegistry,
) (*trackedService, *runtimeAdmin, func() error, error) {
	if err := launchCtx.Err(); err != nil {
		return nil, nil, nil, err
	}
	startedAt := time.Now()
	requests.logStage("service", "INFO", api.LogStage{Name: "运行时装配", Step: 1, Steps: 3}, "载入账户")
	store := aistudio.NewAccountStore(strings.Split(cfg.AuthStates, ",")...)
	accounts, err := store.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	for _, failure := range store.Failures() {
		requests.log("auth", "WARN", "账户目录无法载入，已跳过", "account", failure.ID, "error", failure.Err)
	}
	if err := launchCtx.Err(); err != nil {
		return nil, nil, nil, err
	}
	egress := []any{"egress", "直连"}
	proxyList := config.ProxyList(cfg.Proxy)
	if len(proxyList) == 0 {
		if system := systemProxy(); system != "" {
			proxyList = []string{system}
			egress = []any{"egress", redactedProxy(system), "proxy_source", "系统代理"}
		}
	} else {
		redacted := make([]string, 0, len(proxyList))
		for _, proxy := range proxyList {
			redacted = append(redacted, redactedProxy(proxy))
		}
		egress = []any{"egress", strings.Join(redacted, ",")}
	}
	globalProxy := ""
	if len(proxyList) > 0 {
		globalProxy = proxyList[0]
	}
	cfg.Proxy = strings.Join(proxyList, ",")
	camoufoxPath, login := prepareWAABackend(cfg, requests, accounts, globalProxy)
	requests.logStage("service", "INFO", api.LogStage{Name: "运行时装配", Step: 3, Steps: 3}, "创建协议客户端", egress...)
	pool := aistudio.NewAccountPool(accounts, cfg.PerAccountConcurrency)
	pool.SetRoutingStrategy(cfg.RoutingStrategy)
	pool.SetAccountOrder(accountOrder(cfg.AccountOrder))
	pool.SetUpstreamChannels(upstreamChannels(cfg.UpstreamChannels))
	pool.SetModelChannels(modelChannels(cfg.ModelChannels))
	pool.SetModelAccounts(modelAccounts(cfg.ModelAccounts))
	proxies := aistudio.NewProxyPool(pool, proxyList)
	if _, err := proxies.Assign(); err != nil {
		return nil, nil, nil, err
	}
	workers := newAccountWorkerManager(
		pool, accounts, requests, camoufoxPath, globalProxy, cfg.InitTimeout,
		cfg.WarmWorkerLimit, cfg.MaxActiveWorkers, cfg.WarmStartupConcurrency,
	)
	transport, err := aistudio.NewMakerSuiteHTTPTransport(aistudio.HTTPTransportOptions{
		Pool: pool, Signer: aistudio.NewSigner(), Headers: workers, GlobalProxy: globalProxy,
	})
	if err != nil {
		return nil, nil, nil, errors.Join(err, workers.Close())
	}
	pool.SetSessionCheck(func(ctx context.Context, account *aistudio.Account) error {
		return aistudio.CheckSession(ctx, workers.workerConfig(account))
	})
	protected := aistudio.NewWorkerProtectedTransport(aistudio.WorkerProtectedTransportOptions{
		Transport: transport, Workers: workers, SetupTimeout: cfg.InitTimeout,
	})
	refresher := newAuthRuntimeRefresher(workers, requests, globalProxy)
	workers.refresher = refresher
	client := aistudio.NewClient(aistudio.ClientOptions{
		Transport:       &authRetryTransport{transport: transport, refresher: refresher},
		Protected:       &authRetryProtectedTransport{transport: protected, refresher: refresher},
		ContextProvider: aistudio.NewPoolRequestContextProvider(pool),
		Pages:           workers,
		Static:          transport,
	})
	pooled := aistudio.NewPooledService(pool, client)
	pooled.BuildNativeNonstream = cfg.BuildNativeNonstream
	pooled.YouTubeLinks = cfg.YouTubeLinks
	pooled.DiscoverHiddenModels = cfg.DiscoverHiddenModels
	pooled.StripThoughtSignatures = cfg.StripThoughtSignatures
	pooled.ContinuationRewrite = cfg.ContinuationRewrite
	pooled.ContinuationPrompt = cfg.ContinuationPrompt
	service := newTrackedService(lifecycle, pooled, pool, requests, workers, cfg.RequestTimeout)
	service.buildNativeNonstream = cfg.BuildNativeNonstream
	service.hedgeAuto, service.hedgeAfter, _ = config.ParseFirstEventHedge(cfg.FirstEventHedge)
	service.hedgeModels = config.ModelList(cfg.FirstEventHedgeModels)
	service.discoverHiddenModels = cfg.DiscoverHiddenModels
	mapping, _ := config.ParseModelMapping(cfg.ModelMapping)
	service.SetRequestPolicy(mapping, cfg.MaxAccountSwitches)
	if camoufoxPath != "" {
		service.camoufoxProxy, service.camoufoxWait = globalProxy, make(chan struct{})
	}
	admin := newRuntimeAdmin(lifecycle, pool, store, service, requests, login, workers, cfg)
	service.publishStatus = func() { admin.publishStatus(lifecycle) }
	admin.proxies, admin.globalProxy = proxies, globalProxy
	service.proxies, service.rebindAccounts = proxies, admin.rebindAccounts
	workers.proxyFailed = service.reportProxyFailure
	probeCtx, stopProbe := context.WithCancel(lifecycle)
	if len(proxyList) > 1 {
		go admin.probeProxies(probeCtx)
	}
	requests.logStage("service", "INFO", api.LogStage{Name: "运行时装配", State: "completed"}, "协议运行时就绪", "accounts", len(accounts), "duration", time.Since(startedAt))
	closeRuntime := func() error {
		stopProbe()
		err := workers.Close()
		camoufoxnative.WaitRetired()
		transport.CloseIdleConnections()
		return err
	}
	return service, admin, closeRuntime, nil
}

// trackedService 跟踪生成请求及其唯一账户租约
type trackedService struct {
	buildNativeNonstream bool
	proxies              *aistudio.ProxyPool
	rebindAccounts       func([]string)
	lifecycle            context.Context
	service              aistudio.Service
	catalog              modelCatalogService
	pool                 *aistudio.AccountPool
	requests             *requestRegistry
	workers              *accountWorkerManager
	timeout              time.Duration
	state                atomic.Int32
	lifecycleMu          sync.Mutex
	transitionDone       chan struct{}
	transitionErr        error
	transitionTimedOut   bool
	dataContext          context.Context
	dataCancel           context.CancelFunc
	modelsMu             sync.RWMutex
	models               []aistudio.Model
	modelSyncMu          sync.Mutex
	modelRetriesMu       sync.Mutex
	modelRetries         map[string]struct{}
	modelRefreshDone     <-chan struct{}
	modelChangeMu        sync.Mutex
	modelRevision        uint64
	modelApplied         uint64
	// catalogRoundMu 保护 catalogRoundAt 与 catalogRoundDone
	catalogRoundMu sync.Mutex
	// catalogRoundAt 是上一轮启用账户目录读取的开始时间，巡检循环未运行时为零
	catalogRoundAt time.Time
	// catalogRoundDone 在待开始或进行中的一轮目录读取（启动时的目录同步或巡检）结束时关闭，没有这样的一轮时为 nil
	catalogRoundDone chan struct{}
	// catalogRoundWake 让巡检循环立即开始请求缺少模型时排定的一轮
	catalogRoundWake chan struct{}
	// hedgeAuto 与 hedgeAfter 是首事件超时备用请求的自适应开关与自定义等待
	hedgeAuto  bool
	hedgeAfter time.Duration
	// hedgeModels 是发起备用请求的模型
	hedgeModels []string
	// hedges 是正在进行的备用请求数
	hedges atomic.Int32
	// maxAccountSwitches 是一个请求因非额度失败换号的次数上限，保存设置后立即替换
	maxAccountSwitches atomic.Int64
	// modelMapping 是 MODEL_MAPPING 解析出的模型别名，保存设置后立即替换
	modelMapping atomic.Pointer[[]config.ModelAlias]
	// disconnects 记录各模型与通道最近的请求终态，判断成片断连
	disconnects disconnectTracker
	// discoverHiddenModels 表示同步账户目录时探测官网隐藏模型
	discoverHiddenModels bool
	// camoufoxWait 在 Camoufox 后端的启动开始等待 Camoufox 下载时关闭，SpiderMonkey 后端为 nil
	camoufoxWait chan struct{}
	// camoufoxProxy 是 Camoufox 后端下载 Camoufox 的出口代理
	camoufoxProxy string
	// step 是生成服务正在进行的启动或停止步骤，没有时为 nil，取值见 api.AdminStatus.Transition
	// 约束：启动步骤在持有 lifecycleMu 且服务仍在启动时写入，停止步骤由停止流程写入
	step atomic.Pointer[serviceStep]
	// publishStatus 向管理页推送生成服务状态快照
	publishStatus func()
}

// serviceStep 是生成服务的一个启动或停止步骤与进入该步骤的时刻
type serviceStep struct {
	name  string
	since time.Time
}

type modelCatalogService interface {
	RefreshAccountModels(context.Context, string, bool) ([]aistudio.Model, error)
	RefreshAccountState(context.Context, string, bool) (aistudio.AccountRefresh, error)
	ReclaimExpiredResources(context.Context) error
	CachedModels() []aistudio.Model
}

// newTrackedService 创建带超时和生命周期的协议服务
func newTrackedService(
	lifecycle context.Context,
	service aistudio.Service,
	pool *aistudio.AccountPool,
	requests *requestRegistry,
	workers *accountWorkerManager,
	timeout time.Duration,
) *trackedService {
	catalog := service.(modelCatalogService)
	tracked := &trackedService{
		lifecycle: lifecycle, service: service, catalog: catalog, pool: pool, requests: requests, workers: workers,
		timeout: timeout, modelRetries: make(map[string]struct{}), catalogRoundWake: make(chan struct{}, 1),
		publishStatus: func() {},
	}
	tracked.SetRequestPolicy(nil, config.DefaultMaxAccountSwitches)
	return tracked
}

// Step 返回生成服务正在进行的启动或停止步骤与进入该步骤的时刻，没有步骤时 name 为空
func (service *trackedService) Step() (name string, since time.Time) {
	if step := service.step.Load(); step != nil {
		return step.name, step.since
	}
	return "", time.Time{}
}

// storeStep 记录进入步骤 name 的时刻，name 为空时清除步骤
func (service *trackedService) storeStep(name string) {
	if name == "" {
		service.step.Store(nil)
		return
	}
	service.step.Store(&serviceStep{name: name, since: time.Now()})
}

// enterStopStep 进入停止步骤 name、推送状态并写一行日志 message，staged 为 true 时日志属于停止阶段
func (service *trackedService) enterStopStep(staged bool, name, message string, attrs ...any) {
	service.storeStep(name)
	service.publishStatus()
	if staged {
		service.requests.logStage("service", "INFO", api.LogStage{Name: stopStage}, message, attrs...)
		return
	}
	service.requests.log("service", "INFO", message, attrs...)
}

// unfinished 返回 done 是否属于一个尚未结束的后台任务
func unfinished(done <-chan struct{}) bool {
	if done == nil {
		return false
	}
	select {
	case <-done:
		return false
	default:
		return true
	}
}

// enterLaunchStep 在生成服务仍在启动时进入启动步骤 name 并推送状态，停止开始后不再改动步骤
func (service *trackedService) enterLaunchStep(name string) {
	service.lifecycleMu.Lock()
	launching := service.state.Load() == serviceLaunching
	if launching {
		service.storeStep(name)
	}
	service.lifecycleMu.Unlock()
	if launching {
		service.publishStatus()
	}
}

// SetRequestPolicy 替换之后的请求使用的模型别名与换号次数上限
func (service *trackedService) SetRequestPolicy(mapping []config.ModelAlias, maxAccountSwitches int) {
	service.modelMapping.Store(&mapping)
	service.maxAccountSwitches.Store(int64(maxAccountSwitches))
}

// aliases 返回之后的请求使用的模型别名
func (service *trackedService) aliases() []config.ModelAlias {
	if mapping := service.modelMapping.Load(); mapping != nil {
		return *mapping
	}
	return nil
}

// serviceStoppedError 表示生成服务未运行，launching 表示正在启动，camoufox 是 Camoufox 后端正在准备 Camoufox 的进度或准备失败的原因
type serviceStoppedError struct {
	launching bool
	camoufox  error
}

var errServiceTransitioning = errors.New("生成服务正在切换状态")

const (
	serviceStopped int32 = iota
	serviceLaunching
	serviceRunning
	modelCatalogRetryInterval        = 30 * time.Second
	accountStateRefreshInterval      = 24 * time.Hour
	missingModelCatalogInterval      = 10 * time.Minute
	idleResourceReclaimInterval      = 10 * time.Minute
	modelCatalogShutdownTimeout      = 2 * time.Second
	serviceTransitionShutdownTimeout = 12 * time.Second
)

func (e *serviceStoppedError) Error() string {
	switch {
	case e.launching && e.camoufox != nil:
		return "AIStudio2API 生成服务正在启动：" + e.camoufox.Error()
	case e.launching:
		return "AIStudio2API 生成服务正在启动"
	case e.camoufox != nil:
		return "AIStudio2API 生成服务已停止：" + e.camoufox.Error()
	}
	return "AIStudio2API 生成服务已停止：在管理页面点击“启动服务”，或在服务配置中开启“自动启动生成服务”"
}

func (*serviceStoppedError) HTTPStatus() int {
	return http.StatusServiceUnavailable
}

func (e *serviceStoppedError) ErrorCode() string {
	if e.launching {
		return "service_starting"
	}
	return "service_stopped"
}

// RetryAt 在 Camoufox 正在下载且能估计剩余时间时返回预计下载完成的时刻，其余情况返回 now
func (e *serviceStoppedError) RetryAt(now time.Time) time.Time {
	var preparing *camoufoxnative.PreparingError
	if errors.As(e.camoufox, &preparing) {
		return now.Add(preparing.Remaining)
	}
	return now
}

// stoppedError 返回生成服务未运行的错误：Camoufox 后端启动期间带上 Camoufox 的准备进度，停止时带上 Camoufox 准备失败的原因
func (service *trackedService) stoppedError() error {
	err := &serviceStoppedError{launching: service.state.Load() == serviceLaunching}
	if service.camoufoxWait == nil {
		return err
	}
	_, camoufox := camoufoxnative.ReadyPath()
	var preparing *camoufoxnative.PreparingError
	if err.launching || camoufox != nil && !errors.As(camoufox, &preparing) {
		err.camoufox = camoufox
	}
	return err
}

// prepareCamoufox 在 Camoufox 后端有启用的账户时等 Camoufox 就绪，需要下载时先关闭 camoufoxWait 放行等待启动的公开请求
func (service *trackedService) prepareCamoufox(ctx context.Context) error {
	if service.camoufoxWait == nil {
		return nil
	}
	if len(service.pool.EnabledAccounts()) == 0 {
		return nil
	}
	if camoufoxnative.PreparationState().Ready {
		_, err := camoufoxnative.Prepare(ctx, service.camoufoxProxy)
		return err
	}
	select {
	case <-service.camoufoxWait:
	default:
		close(service.camoufoxWait)
	}
	startedAt := time.Now()
	service.enterLaunchStep("wait_camoufox")
	service.requests.logStage("service", "INFO", api.LogStage{Name: "生成服务启动"}, "等待 Camoufox 下载")
	if _, err := camoufoxnative.Prepare(ctx, service.camoufoxProxy); err != nil {
		return err
	}
	service.requests.logStage("service", "INFO", api.LogStage{Name: "生成服务启动"}, "Camoufox 已就绪", "duration", time.Since(startedAt))
	return nil
}

// Running 返回公开生成服务是否接受请求
func (service *trackedService) Running() bool {
	return service.state.Load() == serviceRunning
}

// State 返回生成服务生命周期状态
func (service *trackedService) State() string {
	switch service.state.Load() {
	case serviceLaunching:
		return "LAUNCHING"
	case serviceRunning:
		return "RUNNING"
	default:
		return "STOPPED"
	}
}

// Start 刷新模型并创建本次公开生成服务
func (service *trackedService) Start(ctx context.Context, launching func()) ([]aistudio.Model, bool, error) {
	service.lifecycleMu.Lock()
	if service.state.Load() == serviceRunning {
		service.lifecycleMu.Unlock()
		return service.modelSnapshot(), false, nil
	}
	if service.state.Load() == serviceLaunching || service.transitionDone != nil {
		service.lifecycleMu.Unlock()
		return service.modelSnapshot(), false, errServiceTransitioning
	}
	dataContext, dataCancel := context.WithCancel(service.lifecycle)
	transitionDone := make(chan struct{})
	service.dataContext = dataContext
	service.dataCancel = dataCancel
	service.transitionDone = transitionDone
	service.transitionErr = nil
	service.transitionTimedOut = false
	service.state.Store(serviceLaunching)
	service.lifecycleMu.Unlock()
	service.requests.logStage("service", "INFO", api.LogStage{Name: "生成服务启动"}, "开始启动生成服务", "accounts", len(service.pool.EnabledAccounts()))
	stopCaller := context.AfterFunc(ctx, dataCancel)
	service.replaceModelSnapshot(service.catalog.CachedModels())
	launching()
	service.probeProxyPool(dataContext)
	if err := service.prepareCamoufox(dataContext); err != nil {
		stopCaller()
		return nil, false, service.finishLaunch(transitionDone, dataCancel, nil, err)
	}
	models := service.modelSnapshot()
	cached := len(models) > 0
	var catalogSynced, catalogDone <-chan struct{}
	if cached {
		service.requests.logStage("service", "INFO", api.LogStage{Name: "生成服务启动", Step: 1, Steps: 2}, "准备模型目录", "cached_models", len(models))
	} else {
		service.enterLaunchStep("wait_catalog")
		service.requests.logStage("service", "INFO", api.LogStage{Name: "生成服务启动", Step: 1, Steps: 2}, "等待首个账户读取模型目录")
		var catalogReady <-chan error
		catalogReady, catalogSynced, catalogDone = service.startLaunchCatalogRefresh(dataContext)
		select {
		case <-dataContext.Done():
			stopCaller()
			return nil, false, service.finishLaunch(transitionDone, dataCancel, catalogDone, dataContext.Err())
		case err := <-catalogReady:
			if err != nil {
				stopCaller()
				return nil, false, service.finishLaunch(transitionDone, dataCancel, catalogDone, err)
			}
		}
		models = service.modelSnapshot()
		if len(models) == 0 {
			stopCaller()
			return nil, false, service.finishLaunch(transitionDone, dataCancel, catalogDone, aistudio.ErrNoEligibleAccount)
		}
	}
	service.enterLaunchStep("start_prewarm")
	service.requests.logStage("service", "INFO", api.LogStage{Name: "生成服务启动", Step: 2, Steps: 2}, "预热 WAA Worker",
		"models", len(models), "target_workers", service.workers.PrewarmTarget())
	if !stopCaller() {
		return nil, false, service.finishLaunch(transitionDone, dataCancel, catalogDone, ctx.Err())
	}
	var round chan struct{}
	if cached {
		round = service.beginCatalogRound()
	}
	firstWarm := service.workers.StartPrewarm(dataContext)
	models, err := service.finishModelLaunch(dataContext, transitionDone)
	if err != nil {
		if round != nil {
			service.endCatalogRound(round)
		}
		return models, false, service.finishLaunch(transitionDone, dataCancel, catalogDone, err)
	}
	service.publishStatus()
	go service.warmAfterLaunch(dataContext, firstWarm, round, catalogSynced)
	return models, true, nil
}

// warmAfterLaunch 在生成服务运行后等进入运行前开始的预热 firstWarm 给出首个 Worker 的结果：以目录缓存启动时 round 为进入运行前登记的一轮目录读取，在首个 Worker 有结果后开始，服务已停止时直接结束这一轮；预热在目录同步完成前失败时等同步完成再预热一次，仍失败时写入日志
func (service *trackedService) warmAfterLaunch(dataContext context.Context, firstWarm <-chan error, round chan struct{}, catalogSynced <-chan struct{}) {
	synced, warmErr := waitFirstWarm(dataContext, firstWarm, catalogSynced)
	if round != nil {
		if dataContext.Err() != nil {
			service.endCatalogRound(round)
			return
		}
		_, catalogSynced, _ = service.startLaunchCatalogRefresh(dataContext)
	}
	if warmErr != nil && !synced && dataContext.Err() == nil {
		select {
		case <-dataContext.Done():
		case <-catalogSynced:
			_, warmErr = waitFirstWarm(dataContext, service.workers.StartPrewarm(dataContext), catalogSynced)
		}
	}
	if warmErr != nil && dataContext.Err() == nil {
		service.requests.log("service", "WARN", "WAA Worker 预热失败", "error", warmErr)
	}
}

// startLaunchCatalogRefresh 开始本次启动的模型目录同步，并记录其结束信号供停止服务时等待
func (service *trackedService) startLaunchCatalogRefresh(dataContext context.Context) (<-chan error, <-chan struct{}, <-chan struct{}) {
	ready, synced, done := service.startModelCatalogRefresh(dataContext)
	service.lifecycleMu.Lock()
	if service.dataContext == dataContext {
		service.modelRefreshDone = done
	}
	service.lifecycleMu.Unlock()
	return ready, synced, done
}

// waitFirstWarm 等待一轮预热 firstWarm 给出首个 Worker 的结果，同时返回结束时首轮目录同步是否已完成
func waitFirstWarm(ctx context.Context, firstWarm <-chan error, catalogSynced <-chan struct{}) (bool, error) {
	var warmErr error
	select {
	case <-ctx.Done():
		return true, ctx.Err()
	case warmErr = <-firstWarm:
	}
	select {
	case <-catalogSynced:
		return true, warmErr
	default:
		return false, warmErr
	}
}

// finishModelLaunch 刷新启动期变化并原子启用生成服务
func (service *trackedService) finishModelLaunch(
	dataContext context.Context,
	transitionDone chan struct{},
) ([]aistudio.Model, error) {
	service.modelSyncMu.Lock()
	defer service.modelSyncMu.Unlock()
	for {
		modelRevision := service.currentModelRevision()
		models := service.catalog.CachedModels()
		service.replaceModelSnapshot(models)
		if len(models) == 0 {
			return models, aistudio.ErrNoEligibleAccount
		}
		service.modelChangeMu.Lock()
		if service.modelRevision != modelRevision {
			service.modelChangeMu.Unlock()
			continue
		}
		service.lifecycleMu.Lock()
		if service.transitionDone != transitionDone || service.state.Load() != serviceLaunching || dataContext.Err() != nil {
			launchErr := dataContext.Err()
			if launchErr == nil {
				launchErr = context.Canceled
			}
			service.lifecycleMu.Unlock()
			service.modelChangeMu.Unlock()
			return service.modelSnapshot(), launchErr
		}
		service.modelApplied = modelRevision
		service.state.Store(serviceRunning)
		service.storeStep("")
		service.transitionDone = nil
		close(transitionDone)
		service.lifecycleMu.Unlock()
		service.modelChangeMu.Unlock()
		return service.modelSnapshot(), nil
	}
}

func (service *trackedService) finishLaunch(
	transitionDone chan struct{},
	dataCancel context.CancelFunc,
	modelRefreshDone <-chan struct{},
	launchErr error,
) error {
	dataCancel()
	return errors.Join(launchErr, service.cleanup(transitionDone, modelRefreshDone))
}

// cleanup 等模型目录同步停止并关闭全部 Worker，再把生成服务置为停止、结束 transitionDone 代表的启动或停止，返回清理中的错误；由停止引起且停止没有超时返回时各步日志属于停止阶段
func (service *trackedService) cleanup(transitionDone chan struct{}, modelRefreshDone <-chan struct{}) error {
	service.lifecycleMu.Lock()
	stopping := service.state.Load() == serviceStopped && !service.transitionTimedOut
	service.lifecycleMu.Unlock()
	if unfinished(modelRefreshDone) {
		service.enterStopStep(stopping, "wait_catalog_stop", "等待模型目录同步停止")
	}
	refreshErr := waitServiceTransition(modelRefreshDone, modelCatalogShutdownTimeout, "模型目录刷新停止")
	service.lifecycleMu.Lock()
	if service.transitionDone == transitionDone {
		service.transitionErr = refreshErr
	}
	service.lifecycleMu.Unlock()
	service.enterStopStep(stopping, "close_workers", "关闭 WAA Worker", "workers", len(service.workers.WarmAccountIDs()))
	service.workers.waitPrewarm()
	resetErr := service.workers.ResetAll()
	service.lifecycleMu.Lock()
	if service.transitionDone == transitionDone {
		service.state.Store(serviceStopped)
		service.storeStep("")
		service.dataContext = nil
		service.dataCancel = nil
		service.modelRefreshDone = nil
		service.transitionErr = errors.Join(refreshErr, resetErr)
		service.transitionDone = nil
		close(transitionDone)
	}
	service.lifecycleMu.Unlock()
	service.publishStatus()
	return errors.Join(refreshErr, resetErr)
}

func waitServiceTransition(done <-chan struct{}, timeout time.Duration, operation string) error {
	if done == nil {
		return nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return fmt.Errorf("%s超时", operation)
	}
}

func (service *trackedService) markModelAccessVerifiedAsync(
	accountID string,
	accountLabel string,
	modelID string,
	accessGeneration uint64,
	checkedAt time.Time,
) {
	go func() {
		changed, err := service.pool.MarkModelAccessVerifiedIfGeneration(
			accountID, modelID, accessGeneration, checkedAt,
		)
		if err != nil {
			service.requests.log(accountLabel, "ERROR", "模型资格保存失败", "model", modelID, "error", err)
			return
		}
		if changed {
			service.publishModelAccess()
		}
	}()
}

// Stop 停止公开生成服务并释放活动 worker
func (service *trackedService) Stop() (bool, error) {
	service.lifecycleMu.Lock()
	state := service.state.Load()
	if state == serviceStopped {
		done := service.transitionDone
		if done == nil && service.transitionTimedOut {
			cleanupErr := service.transitionErr
			service.transitionErr = nil
			service.transitionTimedOut = false
			service.lifecycleMu.Unlock()
			return false, cleanupErr
		}
		service.lifecycleMu.Unlock()
		transitionErr := waitServiceTransition(done, serviceTransitionShutdownTimeout, "生成服务切换停止")
		if done != nil {
			service.lifecycleMu.Lock()
			cleanupErr := service.transitionErr
			if transitionErr == nil {
				service.transitionErr = nil
				service.transitionTimedOut = false
			} else {
				service.transitionTimedOut = true
			}
			service.lifecycleMu.Unlock()
			return false, errors.Join(transitionErr, cleanupErr)
		}
		resetErr := service.workers.ResetAll()
		service.lifecycleMu.Lock()
		service.transitionErr = resetErr
		service.transitionTimedOut = false
		service.lifecycleMu.Unlock()
		return false, resetErr
	}
	dataCancel := service.dataCancel
	if state == serviceLaunching {
		done := service.transitionDone
		service.state.Store(serviceStopped)
		service.storeStep("cancel_requests")
		service.lifecycleMu.Unlock()
		service.publishStatus()
		service.requests.logStage("service", "INFO", api.LogStage{Name: stopStage}, "停止在途请求", "requests", service.requests.count())
		dataCancel()
		transitionErr := waitServiceTransition(done, serviceTransitionShutdownTimeout, "生成服务启动停止")
		service.requests.cancelAll()
		service.lifecycleMu.Lock()
		cleanupErr := service.transitionErr
		if transitionErr == nil {
			service.transitionErr = nil
			service.transitionTimedOut = false
		} else {
			service.transitionTimedOut = true
		}
		service.lifecycleMu.Unlock()
		return true, errors.Join(transitionErr, cleanupErr)
	}
	transitionDone := make(chan struct{})
	service.transitionDone = transitionDone
	service.state.Store(serviceStopped)
	service.storeStep("cancel_requests")
	modelRefreshDone := service.modelRefreshDone
	service.lifecycleMu.Unlock()
	service.publishStatus()
	service.requests.logStage("service", "INFO", api.LogStage{Name: stopStage}, "停止在途请求", "requests", service.requests.count())
	dataCancel()
	go service.finishStop(transitionDone, modelRefreshDone)
	transitionErr := waitServiceTransition(transitionDone, serviceTransitionShutdownTimeout, "生成服务停止")
	service.lifecycleMu.Lock()
	cleanupErr := service.transitionErr
	if transitionErr == nil {
		service.transitionErr = nil
		service.transitionTimedOut = false
	} else {
		service.transitionTimedOut = true
	}
	service.lifecycleMu.Unlock()
	return true, errors.Join(transitionErr, cleanupErr)
}

// finishStop 完成运行态服务的后台清理
func (service *trackedService) finishStop(transitionDone chan struct{}, modelRefreshDone <-chan struct{}) {
	service.requests.cancelAll()
	_ = service.cleanup(transitionDone, modelRefreshDone)
}

// Models 返回已启用通道可以提供的公开目录，生成服务未运行且目录为空时返回服务停止错误
func (service *trackedService) Models(context.Context) ([]aistudio.Model, error) {
	models := service.modelSnapshot()
	if len(models) == 0 && service.state.Load() != serviceRunning {
		return nil, service.stoppedError()
	}
	return service.withAliases(service.pool.PublicModels(models)), nil
}

// withAliases 把 MODEL_MAPPING 中目标在目录内的别名加入公开目录，字段取自目标模型，与目录 ID 同名的别名替换该条目，同名别名以先写的为准
func (service *trackedService) withAliases(models []aistudio.Model) []aistudio.Model {
	index := make(map[string]int, len(models))
	for i, model := range models {
		index[model.ID] = i
	}
	targets := slices.Clone(models)
	done := make(map[string]bool)
	for _, alias := range service.aliases() {
		if done[alias.Alias] {
			continue
		}
		done[alias.Alias] = true
		target, _, _ := aistudio.ApplyModelSuffix(alias.Model, aistudio.GenerationConfig{})
		model, ok := api.LookupPublicModel(targets, target)
		if !ok {
			continue
		}
		model.ID = alias.Alias
		if i, ok := index[alias.Alias]; ok {
			models[i] = model
			continue
		}
		index[alias.Alias] = len(models)
		models = append(models, model)
	}
	return models
}

// mappedModel 返回 MODEL_MAPPING 中别名映射到的模型名，别名带思考后缀时该后缀替换目标自带的后缀，不是别名时原样返回
func (service *trackedService) mappedModel(name string) string {
	trimmed := strings.TrimPrefix(strings.TrimSpace(name), "models/")
	for _, alias := range service.aliases() {
		if alias.Alias == trimmed {
			return alias.Model
		}
	}
	open := strings.LastIndex(trimmed, "(")
	if open <= 0 || !strings.HasSuffix(trimmed, ")") {
		return name
	}
	base := strings.TrimSpace(trimmed[:open])
	for _, alias := range service.aliases() {
		if alias.Alias == base {
			target, _, _ := aistudio.ApplyModelSuffix(alias.Model, aistudio.GenerationConfig{})
			return target + trimmed[open:]
		}
	}
	return name
}

// catalogModels 返回管理页使用的完整目录与各模型可用通道
func (service *trackedService) catalogModels() []aistudio.Model {
	return service.pool.CatalogModels(service.modelSnapshot())
}

func (service *trackedService) modelSnapshot() []aistudio.Model {
	service.modelsMu.RLock()
	models := append([]aistudio.Model{}, service.models...)
	service.modelsMu.RUnlock()
	return models
}

func (service *trackedService) publishModelAccess() {
	if service.lifecycle.Err() != nil {
		return
	}
	statuses := service.pool.Status()
	accounts := make([]api.AdminAccount, 0, len(statuses))
	for _, status := range statuses {
		accounts = append(accounts, adminAccountDTO(status))
	}
	service.requests.publish(api.AdminEvent{Type: "accounts", Data: map[string]any{"accounts": accounts}})
	service.requests.publish(api.AdminEvent{Type: "models", Data: map[string]any{"models": service.catalogModels()}})
}

type accountModelRefreshResult struct {
	accountID string
	models    []aistudio.Model
	err       error
	skipped   bool
}

// startModelCatalogRefresh 刷新目录缓存缺失或已满 accountStateRefreshInterval 的账户目录并持续处理失败目录
func (service *trackedService) startModelCatalogRefresh(ctx context.Context) (<-chan error, <-chan struct{}, <-chan struct{}) {
	ready := make(chan error, 1)
	synced := make(chan struct{})
	done := make(chan struct{})
	accountIDs := service.modelCatalogAccountIDs()
	cached := len(accountIDs)
	accountIDs = service.staleCatalogAccountIDs(accountIDs)
	cached -= len(accountIDs)
	round := service.beginCatalogRound()
	go func() {
		defer close(done)
		startedAt := time.Now()
		firstReady := false
		synchronized := 0
		refreshed := 0
		var failures aistudio.GroupedErrors
		authRequiredBefore := service.authRequiredAccountIDs()
		for result := range service.refreshAccountModelCatalogs(ctx, accountIDs, false) {
			if result.err != nil {
				failures.Add(result.accountID, result.err)
				continue
			}
			synchronized++
			if len(result.models) == 0 {
				continue
			}
			refreshed++
			if ctx.Err() == nil {
				service.applyCachedModelCatalog()
				service.publishModelAccess()
			}
			if !firstReady && ctx.Err() == nil {
				ready <- nil
				firstReady = true
			}
		}
		if !firstReady {
			launchErr := ctx.Err()
			if launchErr == nil && len(failures) > 0 {
				launchErr = failures.Join(aistudio.AccountError)
			}
			if launchErr == nil {
				launchErr = aistudio.ErrNoEligibleAccount
			}
			ready <- launchErr
		}
		close(ready)
		close(synced)
		service.endCatalogRound(round)
		if ctx.Err() != nil {
			return
		}
		if !maps.Equal(service.authRequiredAccountIDs(), authRequiredBefore) {
			service.publishModelAccess()
		}
		service.requests.log("service", "INFO", "模型目录后台同步完成",
			"cached_accounts", cached, "synced_accounts", synchronized, "nonempty_accounts", refreshed,
			"models", len(service.modelSnapshot()), "retry_accounts", service.pendingModelRetryCount(), "duration", time.Since(startedAt))
		service.retryModelCatalogs(ctx, startedAt)
	}()
	return ready, synced, done
}

// modelCatalogAccountIDs 返回读取目录的就绪、正忙与冷却中的启用账户，权益高的在前，同权益按 ID 排列
func (service *trackedService) modelCatalogAccountIDs() []string {
	statuses := service.pool.Status()
	accountIDs := make([]string, 0, len(statuses))
	for _, status := range statuses {
		if status.Enabled && (status.State == aistudio.AccountReady || status.State == aistudio.AccountBusy || status.State == aistudio.AccountCooldown) {
			accountIDs = append(accountIDs, status.ID)
		}
	}
	sort.Strings(accountIDs)
	return service.pool.ByBenefitTier(accountIDs)
}

// staleCatalogAccountIDs 返回 accountIDs 中没有目录缓存、缓存最近一次同步已满 accountStateRefreshInterval 或缓存与官网隐藏模型探测设置不一致的账户
func (service *trackedService) staleCatalogAccountIDs(accountIDs []string) []string {
	now := time.Now()
	fresh := make(map[string]bool)
	for _, status := range service.pool.Status() {
		fresh[status.ID] = len(status.Models) > 0 && now.Sub(status.CatalogSyncedAt) < accountStateRefreshInterval &&
			status.HiddenDiscovered == service.discoverHiddenModels && (service.discoverHiddenModels || status.HiddenModels == 0)
	}
	return slices.DeleteFunc(accountIDs, func(accountID string) bool { return fresh[accountID] })
}

// refreshAccountModelCatalogs 同步账户目录并按完成顺序发出结果：还没有目录的账户并发载入页面，已有目录的账户逐个载入
func (service *trackedService) refreshAccountModelCatalogs(
	ctx context.Context,
	accountIDs []string,
	pendingOnly bool,
) <-chan accountModelRefreshResult {
	results := make(chan accountModelRefreshResult, len(accountIDs))
	refresh := func(accountID string) {
		if pendingOnly && !service.modelRetryPending(accountID) {
			results <- accountModelRefreshResult{accountID: accountID, skipped: true}
			return
		}
		models, err := service.refreshAccountModelCatalog(ctx, accountID, pendingOnly)
		results <- accountModelRefreshResult{accountID: accountID, models: models, err: err}
	}
	catalogued := make(map[string]bool)
	for _, status := range service.pool.Status() {
		catalogued[status.ID] = len(status.Models) > 0
	}
	var refreshes sync.WaitGroup
	var known []string
	for _, accountID := range accountIDs {
		if catalogued[accountID] {
			known = append(known, accountID)
			continue
		}
		refreshes.Go(func() { refresh(accountID) })
	}
	refreshes.Go(func() {
		for _, accountID := range known {
			refresh(accountID)
		}
	})
	go func() {
		refreshes.Wait()
		close(results)
	}()
	return results
}

// refreshAccountModelCatalog 以账户页面载入时的响应同步权益与模型目录，reload 为 true 时重新载入页面，失败或目录为空时登记 30 秒后重试
func (service *trackedService) refreshAccountModelCatalog(ctx context.Context, accountID string, reload bool) ([]aistudio.Model, error) {
	requestCtx, cancel := service.lifecycleRequestContext(ctx)
	defer cancel()
	models, err := service.catalog.RefreshAccountModels(requestCtx, accountID, reload)
	service.modelRetriesMu.Lock()
	if err != nil {
		if ctx.Err() == nil {
			service.modelRetries[accountID] = struct{}{}
		}
		service.modelRetriesMu.Unlock()
		if ctx.Err() == nil {
			service.requests.log(accountID, "WARN", "模型目录同步失败", "error", err)
		}
		return nil, err
	}
	if len(models) == 0 {
		service.modelRetries[accountID] = struct{}{}
	} else {
		delete(service.modelRetries, accountID)
	}
	service.modelRetriesMu.Unlock()
	if len(models) == 0 && ctx.Err() == nil {
		service.requests.log(accountID, "WARN", "模型目录为空，等待重新同步")
	}
	return models, nil
}

func (service *trackedService) applyCachedModelCatalog() {
	service.replaceModelSnapshot(service.catalog.CachedModels())
	service.lifecycleMu.Lock()
	state := service.state.Load()
	dataContext := service.dataContext
	running := state == serviceRunning && dataContext != nil && dataContext.Err() == nil
	service.lifecycleMu.Unlock()
	if running {
		service.workers.StartPrewarm(dataContext)
	}
}

// SetAPIKeyAccounts 设置各 API key 可用的账户 ID
func (service *trackedService) SetAPIKeyAccounts(accounts map[string][]string) {
	service.pool.SetAPIKeyAccounts(accounts)
}

// ApplyCapacity 让容量设置作用于后续调度，常驻 Worker 数调高时补足预热
func (service *trackedService) ApplyCapacity(warmTarget int, maxActive int, startupConcurrency int, perAccountConcurrency int) {
	service.pool.SetPerAccountConcurrency(perAccountConcurrency)
	service.workers.SetCapacity(warmTarget, maxActive, startupConcurrency)
	service.lifecycleMu.Lock()
	dataContext := service.dataContext
	running := service.state.Load() == serviceRunning && dataContext != nil && dataContext.Err() == nil
	service.lifecycleMu.Unlock()
	if running && len(service.workers.WarmAccountIDs()) < warmTarget {
		service.workers.StartPrewarm(dataContext)
	}
}

func (service *trackedService) syncAccountModels(ctx context.Context, accountID string) ([]aistudio.Model, error) {
	models, err := service.refreshAccountModelCatalog(ctx, accountID, false)
	service.modelSyncMu.Lock()
	service.applyCachedModelCatalog()
	service.modelSyncMu.Unlock()
	return models, err
}

// retryModelCatalogs 每 30 秒重新载入待同步账户的页面重试目录，自 startedAt 起每 24 小时重新载入启用账户的页面读取权益与目录；到期资源在启动时与每 10 分钟删除
func (service *trackedService) retryModelCatalogs(ctx context.Context, startedAt time.Time) {
	var patrol sync.WaitGroup
	defer patrol.Wait()
	patrol.Go(func() { service.patrolAccountStates(ctx, startedAt) })
	service.reclaimExpiredResources(ctx)
	ticker := time.NewTicker(modelCatalogRetryInterval)
	defer ticker.Stop()
	reclaimTicker := time.NewTicker(idleResourceReclaimInterval)
	defer reclaimTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-reclaimTicker.C:
			service.reclaimExpiredResources(ctx)
		case <-ticker.C:
			if !service.retryAccountModelCatalogs(ctx) {
				return
			}
		}
	}
}

// reclaimExpiredResources 删除到期的内联附件、上传文件、生成视频与 Live 续接绑定，失败的留待下一轮
func (service *trackedService) reclaimExpiredResources(ctx context.Context) {
	if err := service.catalog.ReclaimExpiredResources(ctx); err != nil && ctx.Err() == nil {
		service.requests.log("service", "WARN", "到期资源删除失败", "error", err)
	}
}

// patrolAccountStates 自 startedAt 起每 24 小时开始一轮账户状态巡检，请求缺少模型时排定的一轮立即开始，下一轮从最近一轮开始时计时
func (service *trackedService) patrolAccountStates(ctx context.Context, startedAt time.Time) {
	service.catalogRoundMu.Lock()
	service.catalogRoundAt = startedAt
	service.catalogRoundMu.Unlock()
	defer func() {
		service.catalogRoundMu.Lock()
		service.catalogRoundAt = time.Time{}
		if service.catalogRoundDone != nil {
			close(service.catalogRoundDone)
			service.catalogRoundDone = nil
		}
		select {
		case <-service.catalogRoundWake:
		default:
		}
		service.catalogRoundMu.Unlock()
	}()
	timer := time.NewTimer(time.Until(startedAt.Add(accountStateRefreshInterval)))
	defer timer.Stop()
	for {
		woken := false
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-service.catalogRoundWake:
			woken = true
		}
		service.catalogRoundMu.Lock()
		if service.catalogRoundDone == nil {
			if woken {
				service.catalogRoundMu.Unlock()
				continue
			}
			service.catalogRoundDone = make(chan struct{})
		}
		done := service.catalogRoundDone
		round := time.Now()
		service.catalogRoundAt = round
		service.catalogRoundMu.Unlock()
		service.refreshAccountStates(ctx)
		service.endCatalogRound(done)
		timer.Reset(time.Until(round.Add(accountStateRefreshInterval)))
	}
}

// catalogModelID 返回请求模型在实时目录中的正式 ID；目录中没有该模型时先等 awaitCatalogModel 再查找
func (service *trackedService) catalogModelID(ctx context.Context, model string) string {
	if service.pool.MissingModel(model) {
		service.awaitCatalogModel(ctx, model)
	}
	return service.pool.CanonicalModelID(model)
}

// awaitCatalogModel 在进行中的一轮目录读取期间等到账户目录出现 model、这一轮结束或等满 REQUEST_TIMEOUT；没有进行中的一轮且距上一轮开始已超过 10 分钟时让巡检循环立即开始一轮
func (service *trackedService) awaitCatalogModel(ctx context.Context, model string) {
	service.catalogRoundMu.Lock()
	done := service.catalogRoundDone
	if done == nil && !service.catalogRoundAt.IsZero() && time.Since(service.catalogRoundAt) >= missingModelCatalogInterval {
		done = make(chan struct{})
		service.catalogRoundDone = done
		select {
		case service.catalogRoundWake <- struct{}{}:
		default:
		}
	}
	service.catalogRoundMu.Unlock()
	if done == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, service.timeout)
	defer cancel()
	for {
		changed := service.pool.Changed()
		if !service.pool.MissingModel(model) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-changed:
		}
	}
}

// awaitCatalogSync 在进行中的一轮目录读取还有启用账户没有目录时，等到账户池变化或这一轮结束并返回 true；没有这样的一轮或 ctx 结束时返回 false
func (service *trackedService) awaitCatalogSync(ctx context.Context) bool {
	changed := service.pool.Changed()
	service.catalogRoundMu.Lock()
	done := service.catalogRoundDone
	service.catalogRoundMu.Unlock()
	if done == nil || !slices.ContainsFunc(service.pool.Status(), func(status aistudio.AccountStatus) bool {
		return status.Enabled && len(status.Models) == 0
	}) {
		return false
	}
	select {
	case <-ctx.Done():
		return false
	case <-done:
	case <-changed:
	}
	return true
}

// beginCatalogRound 登记一轮进行中的目录读取，已有待开始或进行中的一轮时沿用它，返回这一轮结束时由 endCatalogRound 关闭的通道
func (service *trackedService) beginCatalogRound() chan struct{} {
	service.catalogRoundMu.Lock()
	defer service.catalogRoundMu.Unlock()
	if service.catalogRoundDone == nil {
		service.catalogRoundDone = make(chan struct{})
	}
	return service.catalogRoundDone
}

// endCatalogRound 结束 done 对应的一轮目录读取，放行等这一轮的请求；这一轮已被结束时不做任何事
func (service *trackedService) endCatalogRound(done chan struct{}) {
	service.catalogRoundMu.Lock()
	if service.catalogRoundDone == done {
		service.catalogRoundDone = nil
		close(done)
	}
	service.catalogRoundMu.Unlock()
}

// refreshAccountStates 逐个重新载入启用账户的页面读取权益与模型目录，没有空闲槽位或正在冷却的账户在本轮最后等到槽位空闲再读
func (service *trackedService) refreshAccountStates(ctx context.Context) {
	var busy []string
	for _, accountID := range service.modelCatalogAccountIDs() {
		if ctx.Err() != nil {
			return
		}
		if refresh, _ := service.refreshAccountState(ctx, accountID, false); refresh.Skipped {
			busy = append(busy, accountID)
		}
	}
	for _, accountID := range busy {
		if ctx.Err() != nil {
			return
		}
		_, _ = service.refreshAccountState(ctx, accountID, true)
	}
}

// refreshAccountState 重新载入账户页面读取权益与模型目录，失败或变化时发布账户与模型事件并记录日志，失败的账户登记 30 秒后重试目录
func (service *trackedService) refreshAccountState(ctx context.Context, accountID string, wait bool) (aistudio.AccountRefresh, error) {
	requestCtx, cancel := service.lifecycleRequestContext(ctx)
	defer cancel()
	refresh, err := service.catalog.RefreshAccountState(requestCtx, accountID, wait)
	if ctx.Err() != nil || refresh.Skipped {
		return refresh, err
	}
	if err != nil {
		service.modelRetriesMu.Lock()
		service.modelRetries[accountID] = struct{}{}
		service.modelRetriesMu.Unlock()
		service.publishModelAccess()
		service.requests.log(accountID, "WARN", "账户状态刷新失败", "error", err)
		return refresh, err
	}
	if refresh.PreviousTier == refresh.Tier && !refresh.ModelsChanged {
		return refresh, nil
	}
	service.modelSyncMu.Lock()
	service.applyCachedModelCatalog()
	service.modelSyncMu.Unlock()
	service.publishModelAccess()
	var changes []any
	if refresh.PreviousTier != refresh.Tier {
		changes = append(changes, "tier", fmt.Sprintf("%s → %s", refresh.PreviousTier, refresh.Tier))
	}
	if refresh.ModelsChanged {
		changes = append(changes, "models", fmt.Sprintf("%d → %d", refresh.PreviousModels, refresh.Models))
	}
	service.requests.log(accountID, "INFO", "账户状态已更新", changes...)
	return refresh, nil
}

func (service *trackedService) retryAccountModelCatalogs(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	accountIDs := service.pendingModelRetryIDs()
	authRequiredBefore := service.authRequiredAccountIDs()
	for result := range service.refreshAccountModelCatalogs(ctx, accountIDs, true) {
		if ctx.Err() != nil {
			return false
		}
		if !result.skipped && result.err == nil && len(result.models) > 0 {
			service.applyCachedModelCatalog()
			service.publishModelAccess()
			service.requests.log(result.accountID, "INFO", "模型目录已更新", "models", len(result.models))
		}
	}
	authChanged := !maps.Equal(service.authRequiredAccountIDs(), authRequiredBefore)
	if authChanged {
		service.publishModelAccess()
	}
	return true
}

func (service *trackedService) removeAccountModelRetry(accountID string) {
	service.modelRetriesMu.Lock()
	delete(service.modelRetries, accountID)
	service.modelRetriesMu.Unlock()
}

// pendingModelRetryIDs 清理失效账户并返回当前可同步的重试任务
func (service *trackedService) pendingModelRetryIDs() []string {
	states := make(map[string]aistudio.AccountState)
	for _, status := range service.pool.Status() {
		if status.Enabled {
			states[status.ID] = status.State
		}
	}
	service.modelRetriesMu.Lock()
	accountIDs := make([]string, 0, len(service.modelRetries))
	for accountID := range service.modelRetries {
		switch states[accountID] {
		case aistudio.AccountReady, aistudio.AccountBusy:
			accountIDs = append(accountIDs, accountID)
		case aistudio.AccountCooldown:
		default:
			delete(service.modelRetries, accountID)
		}
	}
	service.modelRetriesMu.Unlock()
	sort.Strings(accountIDs)
	return accountIDs
}

func (service *trackedService) pendingModelRetryCount() int {
	service.pendingModelRetryIDs()
	service.modelRetriesMu.Lock()
	pending := len(service.modelRetries)
	service.modelRetriesMu.Unlock()
	return pending
}

func (service *trackedService) modelRetryPending(accountID string) bool {
	service.modelRetriesMu.Lock()
	_, pending := service.modelRetries[accountID]
	service.modelRetriesMu.Unlock()
	return pending
}

func (service *trackedService) authRequiredAccountIDs() map[string]struct{} {
	accountIDs := make(map[string]struct{})
	for _, status := range service.pool.Status() {
		if status.State == aistudio.AccountAuthRequired {
			accountIDs[status.ID] = struct{}{}
		}
	}
	return accountIDs
}

func (service *trackedService) replaceModelSnapshot(models []aistudio.Model) {
	service.modelsMu.Lock()
	service.models = append([]aistudio.Model(nil), models...)
	service.modelsMu.Unlock()
}

// changeModels 原子登记影响模型目录的账户变化
func (service *trackedService) changeModels(update func() error) error {
	service.modelChangeMu.Lock()
	defer service.modelChangeMu.Unlock()
	err := update()
	service.modelRevision++
	return err
}

// SyncModels 合并刷新已登记的模型目录变化
func (service *trackedService) SyncModels(ctx context.Context) error {
	service.modelSyncMu.Lock()
	defer service.modelSyncMu.Unlock()
	return service.syncPendingModels(ctx)
}

// syncPendingModels 刷新当前生命周期内尚未提交的模型变化
func (service *trackedService) syncPendingModels(ctx context.Context) error {
	service.lifecycleMu.Lock()
	state := service.state.Load()
	if state == serviceLaunching {
		service.lifecycleMu.Unlock()
		return nil
	}
	if state != serviceRunning {
		service.lifecycleMu.Unlock()
		service.replaceModelSnapshot(service.catalog.CachedModels())
		service.applyModelRevision(service.currentModelRevision())
		return nil
	}
	dataContext := service.dataContext
	service.lifecycleMu.Unlock()
	for {
		modelRevision, modelApplied := service.modelRevisions()
		if modelApplied >= modelRevision {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := dataContext.Err(); err != nil {
			return err
		}
		service.replaceModelSnapshot(service.catalog.CachedModels())
		service.applyModelRevision(modelRevision)
	}
	service.lifecycleMu.Lock()
	running := service.state.Load() == serviceRunning && service.dataContext == dataContext
	service.lifecycleMu.Unlock()
	if running {
		service.workers.StartPrewarm(dataContext)
	}
	return nil
}

// currentModelRevision 返回最新模型变化版本号
func (service *trackedService) currentModelRevision() uint64 {
	service.modelChangeMu.Lock()
	defer service.modelChangeMu.Unlock()
	return service.modelRevision
}

// modelRevisions 返回模型变化与已提交版本号
func (service *trackedService) modelRevisions() (uint64, uint64) {
	service.modelChangeMu.Lock()
	defer service.modelChangeMu.Unlock()
	return service.modelRevision, service.modelApplied
}

// applyModelRevision 提交成功刷新的模型版本号
func (service *trackedService) applyModelRevision(revision uint64) {
	service.modelChangeMu.Lock()
	if service.modelApplied < revision {
		service.modelApplied = revision
	}
	service.modelChangeMu.Unlock()
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
	observed := aistudio.ContextWithAccountSelectionObserver(requestCtx, func(lease *aistudio.AccountLease) {
		api.SetAccessLogChannel(requestCtx, string(lease.Channel()))
		api.SetAccessLogTarget(requestCtx, model, lease.Account().Config.Label)
	})
	return observed, cancel, nil
}

// CountTokens 返回上游权威输入 token 数
func (service *trackedService) CountTokens(ctx context.Context, request aistudio.TokenCountRequest) (aistudio.TokenCount, error) {
	model, _, err := aistudio.ApplyModelSuffix(service.mappedModel(request.Model), aistudio.GenerationConfig{})
	if err != nil {
		return aistudio.TokenCount{}, err
	}
	request.Model = service.catalogModelID(ctx, model)
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, request.Model)
	if err != nil {
		return aistudio.TokenCount{}, err
	}
	defer cancel()
	count, requestErr := service.service.CountTokens(requestCtx, request)
	requestErr = endedRequestError(requestCtx, requestErr)
	api.SetAccessLogError(requestCtx, requestErr)
	return count, requestErr
}

// GenerateTitle 生成试用页页头标题
func (service *trackedService) GenerateTitle(ctx context.Context, request aistudio.TitleRequest) (string, error) {
	request.Model = service.catalogModelID(ctx, service.mappedModel(request.Model))
	requestCtx, cancel, err := service.dataRequestContext(ctx)
	if err != nil {
		return "", err
	}
	defer cancel()
	title, err := service.service.(aistudio.TitleService).GenerateTitle(requestCtx, request)
	return title, endedRequestError(requestCtx, err)
}

// GenerateImage 生成 Imagen 图片并登记请求目标与账户
func (service *trackedService) GenerateImage(ctx context.Context, request aistudio.ImageRequest) ([]aistudio.GeneratedImage, error) {
	request.Model = service.catalogModelID(ctx, service.mappedModel(request.Model))
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, request.Model)
	if err != nil {
		return nil, err
	}
	defer cancel()
	images, requestErr := service.service.(aistudio.ImageService).GenerateImage(requestCtx, request)
	requestErr = endedRequestError(requestCtx, requestErr)
	api.SetAccessLogError(requestCtx, requestErr)
	return images, requestErr
}

// CreateDecision 回答决策问题并登记请求目标与账户，每次请求在运行日志中提醒该模型在 AI Studio 官网中隐藏
func (service *trackedService) CreateDecision(ctx context.Context, request aistudio.DecisionRequest) (aistudio.DecisionResult, error) {
	request.Model = service.catalogModelID(ctx, service.mappedModel(request.Model))
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, request.Model)
	if err != nil {
		return aistudio.DecisionResult{}, err
	}
	defer cancel()
	result, requestErr := service.service.(aistudio.DecisionService).CreateDecision(requestCtx, request)
	requestErr = endedRequestError(requestCtx, requestErr)
	api.SetAccessLogError(requestCtx, requestErr)
	service.requests.log("service", "WARN", "Decision 请求使用 AI Studio 官网中隐藏的模型", "model", request.Model)
	return result, requestErr
}

// GenerateVideo 创建一个视频长任务
func (service *trackedService) GenerateVideo(ctx context.Context, request aistudio.VideoRequest) (aistudio.VideoOperation, error) {
	request.Model = service.mappedModel(request.Model)
	api.SetAccessLogTarget(ctx, request.Model, "")
	request.Model = service.catalogModelID(ctx, request.Model)
	requestCtx, cancel, err := service.dataRequestContext(ctx)
	if err != nil {
		api.SetAccessLogError(ctx, err)
		return aistudio.VideoOperation{}, err
	}
	defer cancel()
	if err := service.videoCooling(requestCtx, request); err != nil {
		err = endedRequestError(requestCtx, err)
		api.SetAccessLogError(requestCtx, err)
		return aistudio.VideoOperation{}, err
	}
	requestCtx = aistudio.ContextWithAccountSelectionObserver(requestCtx, func(lease *aistudio.AccountLease) {
		api.SetAccessLogChannel(requestCtx, string(lease.Channel()))
		api.SetAccessLogTarget(requestCtx, request.Model, lease.Account().Config.Label)
	})
	request.TaskContext = func() (context.Context, context.CancelFunc, error) {
		return service.dataRequestContext(context.Background())
	}
	operation, requestErr := service.service.(aistudio.VideoService).GenerateVideo(requestCtx, request)
	requestErr = endedRequestError(requestCtx, requestErr)
	api.SetAccessLogError(requestCtx, requestErr)
	return operation, requestErr
}

// videoCooling 在视频任务的全部候选账户都冷却且最早恢复晚于排队上限时返回 AllCoolingError
func (service *trackedService) videoCooling(ctx context.Context, request aistudio.VideoRequest) error {
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	groups, err := service.pool.ClassifyCandidates(ctx, aistudio.AccountSelection{
		ModelID: modelID, Method: "generateContent", AccountID: strings.TrimSpace(request.AccountID),
	}, nil)
	if err != nil {
		return err
	}
	available := len(groups.WarmReady) + len(groups.WarmAvailable) + len(groups.WarmBusy) +
		len(groups.StandbyReady) + len(groups.StandbyBusy)
	if available > 0 || groups.EarliestCooldown.Until.IsZero() || time.Until(groups.EarliestCooldown.Until) <= cooldownQueueLimit {
		return nil
	}
	return &aistudio.AllCoolingError{ModelID: modelID, Until: groups.EarliestCooldown.Until, Reason: groups.EarliestCooldown.Reason}
}

func needsWAARuntimeRecovery(cause error, workerFailed bool, workerReplaced bool) bool {
	return workerFailed || workerReplaced || aistudio.DefinitiveWAARuntimeFailure(cause)
}

// recoverWorkerOnce 对指定账户的失败 Worker 版本执行至多一次恢复
func (service *trackedService) recoverWorkerOnce(
	ctx context.Context,
	accountID string,
	expectedGeneration uint64,
	recoveredWorkers map[string]struct{},
	currentGenerationEligible bool,
	resetCurrentGeneration bool,
) (recovered bool, currentGeneration bool, err error) {
	if _, recovered := recoveredWorkers[accountID]; recovered {
		return false, false, nil
	}
	if service.workers.WorkerGeneration(accountID) != expectedGeneration {
		recoveredWorkers[accountID] = struct{}{}
		return true, false, nil
	}
	if !currentGenerationEligible {
		return false, false, nil
	}
	if resetCurrentGeneration {
		if err := service.workers.ResetIfGeneration(ctx, accountID, expectedGeneration); err != nil {
			return false, false, err
		}
	}
	recoveredWorkers[accountID] = struct{}{}
	return true, true, nil
}

// GetGenerateVideoOperation 读取视频长任务状态
func (service *trackedService) GetGenerateVideoOperation(ctx context.Context, operationID string) (aistudio.VideoOperation, error) {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, "")
	if err != nil {
		return aistudio.VideoOperation{}, err
	}
	defer cancel()
	operation, requestErr := service.service.(aistudio.VideoService).GetGenerateVideoOperation(requestCtx, operationID)
	requestErr = endedRequestError(requestCtx, requestErr)
	api.SetAccessLogError(requestCtx, requestErr)
	return operation, requestErr
}

// ListVideos 列出视频任务
func (service *trackedService) ListVideos(ctx context.Context) ([]aistudio.VideoOperation, error) {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, "")
	if err != nil {
		return nil, err
	}
	defer cancel()
	videos, requestErr := service.service.(aistudio.VideoService).ListVideos(requestCtx)
	requestErr = endedRequestError(requestCtx, requestErr)
	api.SetAccessLogError(requestCtx, requestErr)
	return videos, requestErr
}

// DeleteVideo 删除视频任务及其结果文件
func (service *trackedService) DeleteVideo(ctx context.Context, operationID string) error {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, "")
	if err != nil {
		return err
	}
	defer cancel()
	requestErr := service.service.(aistudio.VideoService).DeleteVideo(requestCtx, operationID)
	requestErr = endedRequestError(requestCtx, requestErr)
	api.SetAccessLogError(requestCtx, requestErr)
	return requestErr
}

// DownloadFile 下载生成任务绑定的 Drive 文件
func (service *trackedService) DownloadFile(ctx context.Context, fileID string) (aistudio.MediaStream, error) {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, "")
	if err != nil {
		return aistudio.MediaStream{}, err
	}
	media, requestErr := service.service.(aistudio.VideoService).DownloadFile(requestCtx, fileID)
	requestErr = endedRequestError(requestCtx, requestErr)
	api.SetAccessLogError(requestCtx, requestErr)
	if requestErr != nil {
		cancel()
		return aistudio.MediaStream{}, requestErr
	}
	media.Body = &trackedMediaReadCloser{body: media.Body, ctx: requestCtx, cancel: cancel}
	return media, requestErr
}

type trackedMediaReadCloser struct {
	body   io.ReadCloser
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
	err    error
}

func (closer *trackedMediaReadCloser) Read(target []byte) (int, error) {
	read, err := closer.body.Read(target)
	if err != io.EOF {
		err = endedRequestError(closer.ctx, err)
	}
	return read, err
}

func (closer *trackedMediaReadCloser) Close() error {
	closer.once.Do(func() {
		closer.err = closer.body.Close()
		closer.cancel()
	})
	return closer.err
}

// acquireWarmLease 取得 Worker 已就绪的账户租约，可用账户重叠的同条件请求按到达先后取用共同的账户
// 全部账户冷却时排队等待恢复，一个请求累计等待不超过 cooldownQueueLimit，cooled 是它已等的时长
// 最早恢复的是权限拒绝冷却或晚于请求期限时不排队
// quotaFallback 为 true 时停止排队后改用套餐限额、每日限额或权限拒绝冷却中的账户
// 冷 Worker 启动失败后换到其他账户计入 failures，超过换号上限时返回启动失败
func (service *trackedService) acquireWarmLease(ctx context.Context, selection aistudio.AccountSelection, quotaFallback bool, failures *int, cooled *time.Duration) (*aistudio.AccountLease, error) {
	fixedAccount := strings.TrimSpace(selection.AccountID) != "" || strings.TrimSpace(selection.ResourceID) != ""
	failedWorkers := make(map[string]struct{})
	var promoteFailure error
	dispatch := service.workers.dispatch
	waiter := dispatch.join(service.pool.ScopedSelection(ctx, selection))
	defer dispatch.leave(waiter)
	wait := func(delay time.Duration) error {
		return waitScheduling(ctx, waiter.wake, delay)
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		reserved, ok := dispatch.turn(waiter)
		if !ok {
			if err := wait(schedulingRecheck); err != nil {
				return nil, err
			}
			continue
		}
		warm := service.workers.ReadyWarmAccountIDs()
		active := service.workers.WarmAccountIDs()
		groups, err := service.pool.ClassifyCandidates(ctx, selection, warm)
		if err != nil {
			return nil, err
		}
		// 预热给出第一个结果之前，未指定账户的请求等它的第一个 Worker，不另起冷启动
		if len(warm) == 0 && !fixedAccount && service.workers.FirstWarmPending() {
			if err := wait(schedulingRecheck); err != nil {
				return nil, err
			}
			continue
		}
		groups.WarmReady = excludeAccountIDs(groups.WarmReady, failedWorkers)
		groups.WarmAvailable = excludeAccountIDs(groups.WarmAvailable, failedWorkers)
		groups.WarmBusy = excludeAccountIDs(groups.WarmBusy, failedWorkers)
		groups.StandbyReady = excludeAccountIDs(groups.StandbyReady, failedWorkers)
		groups.StandbyBusy = excludeAccountIDs(groups.StandbyBusy, failedWorkers)
		var readyBusyErr, standbyBusyErr error
		groups.StandbyReady, readyBusyErr = service.workers.runtimeAvailable(groups.StandbyReady)
		groups.StandbyBusy, standbyBusyErr = service.workers.runtimeAvailable(groups.StandbyBusy)
		runtimeBusyErr := errors.Join(readyBusyErr, standbyBusyErr)
		candidates := len(groups.WarmReady) + len(groups.WarmAvailable) + len(groups.WarmBusy) +
			len(groups.StandbyReady) + len(groups.StandbyBusy)
		groups.WarmReady = excludeAccountIDs(groups.WarmReady, reserved)
		groups.WarmAvailable = excludeAccountIDs(groups.WarmAvailable, reserved)
		groups.StandbyReady = excludeAccountIDs(groups.StandbyReady, reserved)
		standbyReady, opening := service.workers.withoutOpening(groups.StandbyReady)
		groups.StandbyReady = standbyReady
		// preferred 是 -build 对话 trajectory 所在、尚未载入 Worker 的空闲账户，先于已载入 Worker 的账户使用以续接对话
		preferred := ""
		if selection.PreferredAccountID != "" && slices.Contains(groups.StandbyReady, selection.PreferredAccountID) && service.pool.AssistantModel(selection.ModelID) &&
			(len(active) < service.workers.activeLimit() || service.workers.idleWarmVictim("") != "") {
			preferred = selection.PreferredAccountID
		}
		warmAvailable := append(append([]string(nil), groups.WarmReady...), groups.WarmAvailable...)
		if len(warmAvailable) > 0 && preferred == "" {
			candidate := selection
			candidate.AllowedAccountIDs = warmAvailable
			// 会话粘性没有指定账户时先选没有请求在等待或生成 proof 的账户，同账户的 proof 不排队
			var lease *aistudio.AccountLease
			var acquireErr error
			if idle := service.pool.WithoutPendingProofs(warmAvailable); len(idle) > 0 && len(idle) < len(warmAvailable) &&
				!slices.Contains(warmAvailable, selection.PreferredAccountID) {
				candidate.AllowedAccountIDs = idle
				lease, _, acquireErr = service.pool.TryAcquireFor(ctx, candidate)
				candidate.AllowedAccountIDs = warmAvailable
			}
			if lease == nil && acquireErr == nil {
				lease, _, acquireErr = service.pool.TryAcquireFor(ctx, candidate)
			}
			if errors.Is(acquireErr, aistudio.ErrAccountNotFound) && !fixedAccount {
				continue
			}
			if lease != nil || acquireErr != nil {
				return lease, acquireErr
			}
		}
		if len(groups.StandbyReady) == 0 && opening {
			if err := wait(schedulingRecheck); err != nil {
				return nil, err
			}
			continue
		}
		warmCandidates := len(warmAvailable) + len(groups.WarmBusy)
		coolingVictim := len(active) >= service.workers.activeLimit() &&
			service.workers.idleWarmVictimFor("", selection.ModelID, true) != ""
		if len(groups.StandbyReady) > 0 && (len(active) < service.workers.activeLimit() || coolingVictim) && warmCandidates > 0 && !fixedAccount && preferred == "" {
			standby := groups.StandbyReady
			if cold := service.workers.coldAccounts(standby); len(cold) > 0 {
				standby = cold
			}
			service.workers.expandInBackground(standby[0], selection.ModelID)
			if err := wait(schedulingRecheck); err != nil {
				return nil, err
			}
			continue
		}
		if len(groups.StandbyReady) > 0 && (len(active) < service.workers.activeLimit() ||
			(warmCandidates == 0 || preferred != "") && service.workers.idleWarmVictim("") != "") {
			standby := groups.StandbyReady
			if len(active) < service.workers.activeLimit() {
				if cold := service.workers.coldAccounts(standby); len(cold) > 0 {
					standby = cold
				}
			}
			accountID := cmp.Or(preferred, standby[0])
			candidate := selection
			candidate.AccountID = accountID
			lease, _, acquireErr := service.pool.TryAcquireFor(ctx, candidate)
			if acquireErr != nil {
				if errors.Is(acquireErr, aistudio.ErrAccountNotFound) && !fixedAccount {
					continue
				}
				return nil, acquireErr
			}
			if lease == nil {
				if err := wait(schedulingRecheck); err != nil {
					return nil, err
				}
				continue
			}
			workersChanged := service.workers.schedulingChanged()
			dispatch.setStarting(waiter, true)
			_, promoteErr := service.workers.ensureWorker(aistudio.ContextWithAccountLease(ctx, lease), accountID, selection.ModelID, false, false)
			dispatch.setStarting(waiter, false)
			if promoteErr == nil {
				return lease, nil
			}
			if releaseErr := lease.Release(); releaseErr != nil {
				return nil, errors.Join(promoteErr, releaseErr)
			}
			if errors.Is(promoteErr, errAccountWorkerOpening) {
				continue
			}
			if errors.Is(promoteErr, errAccountWorkerCapacity) {
				if err := waitScheduling(ctx, workersChanged, schedulingRecheck); err != nil {
					return nil, err
				}
				continue
			}
			var startup *accountWorkerInitError
			if errors.As(promoteErr, &startup) && proxyNetworkError(ctx, promoteErr) {
				service.reportProxyFailure(startup.proxy, lease.Account().Config.Label, promoteErr)
			}
			if fixedAccount {
				return nil, promoteErr
			}
			failedWorkers[accountID] = struct{}{}
			promoteFailure = promoteErr
			if *failures++; int64(*failures) > service.maxAccountSwitches.Load() {
				return nil, promotionFailure(service.pool, selection, promoteErr)
			}
			continue
		}
		// 还有未冷却的候选账户时等它空闲或让出 Worker，不改用冷却中的账户
		if len(groups.WarmBusy) > 0 || len(groups.StandbyBusy) > 0 || opening || candidates > 0 {
			if err := wait(schedulingRecheck); err != nil {
				return nil, err
			}
			continue
		}
		if !groups.EarliestCooldown.Until.IsZero() {
			if err := aistudio.QueuedCoolingError(ctx, selection.ModelID, groups.EarliestCooldown, *cooled); err != nil {
				if quotaFallback && !selection.IgnoreQuotaCooldown {
					selection.IgnoreQuotaCooldown = true
					continue
				}
				return nil, err
			}
			waitStarted := time.Now()
			if err := wait(min(time.Until(groups.EarliestCooldown.Until), schedulingRecheck)); err != nil {
				return nil, err
			}
			*cooled += time.Since(waitStarted)
			continue
		}
		if promoteFailure != nil {
			return nil, promotionFailure(service.pool, selection, promoteFailure)
		}
		if runtimeBusyErr != nil {
			return nil, runtimeBusyErr
		}
		return nil, service.pool.NoEligibleError(selection)
	}
}

// promotionFailure 返回冷 Worker 启动失败时请求的错误，登录失效时附上没有可用账户的原因
func promotionFailure(pool *aistudio.AccountPool, selection aistudio.AccountSelection, err error) error {
	if aistudio.DefinitiveAuthenticationFailure(err) {
		return errors.Join(pool.NoEligibleError(selection), err)
	}
	return err
}

// schedulingRecheck 为等待调度信号时的定时复查间隔
const schedulingRecheck = time.Second

// cooldownQueueLimit 为全部候选账户冷却时一个请求排队等待恢复的累计上限，覆盖分钟限额窗口
const cooldownQueueLimit = aistudio.CooldownQueueLimit

// waitScheduling 等待账户或 Worker 可调度状态变化、定时复查或请求取消
func waitScheduling(ctx context.Context, changed <-chan struct{}, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-changed:
		return nil
	case <-timer.C:
		return nil
	}
}

// waitAllCooling 在候选账户全部冷却时等到最早恢复或下一次复查，等待计入 cooled；没有冷却中的候选时返回 ErrNoEligibleAccount，不再排队时返回 AllCoolingError
func waitAllCooling(ctx context.Context, modelID string, cooldown aistudio.CooldownState, cooled *time.Duration) error {
	if cooldown.Until.IsZero() {
		return aistudio.ErrNoEligibleAccount
	}
	if err := aistudio.QueuedCoolingError(ctx, modelID, cooldown, *cooled); err != nil {
		return err
	}
	started := time.Now()
	err := waitScheduling(ctx, nil, min(time.Until(cooldown.Until), schedulingRecheck))
	*cooled += time.Since(started)
	return err
}

const streamStallThreshold = 15 * time.Second

// waitReportInterval 是等待超过 streamStallThreshold 后重复记录仍在等待的间隔
const waitReportInterval = 30 * time.Second

// waitReporter 在等待达到 streamStallThreshold 时与之后每隔 waitReportInterval 记录一次已等待时长
type waitReporter struct {
	stop     chan struct{}
	done     chan struct{}
	reported bool
}

// startWaitReporter 开始计时，report 收到自开始起已等待的时长
func startWaitReporter(report func(waited time.Duration)) *waitReporter {
	reporter := &waitReporter{stop: make(chan struct{}), done: make(chan struct{})}
	started := time.Now()
	go func() {
		defer close(reporter.done)
		timer := time.NewTimer(streamStallThreshold)
		defer timer.Stop()
		for {
			select {
			case <-reporter.stop:
				return
			case <-timer.C:
				reporter.reported = true
				report(time.Since(started).Round(time.Second))
				timer.Reset(waitReportInterval)
			}
		}
	}()
	return reporter
}

// finish 停止计时，返回等待期间是否记录过
func (reporter *waitReporter) finish() bool {
	close(reporter.stop)
	<-reporter.done
	return reporter.reported
}

type upstreamActivity struct {
	bytes    atomic.Int64
	lastNano atomic.Int64
}

type requestPreparationTiming struct {
	mu             sync.Mutex
	phase          aistudio.RequestPhase
	phaseStarted   time.Time
	waa            time.Duration
	responseHeader time.Duration
}

func newRequestPreparationTiming(startedAt time.Time) *requestPreparationTiming {
	return &requestPreparationTiming{phase: aistudio.RequestPhasePreparingWAA, phaseStarted: startedAt}
}

func (timing *requestPreparationTiming) observe(phase aistudio.RequestPhase) {
	timing.mu.Lock()
	defer timing.mu.Unlock()
	if phase == timing.phase {
		return
	}
	now := time.Now()
	timing.finishPhaseLocked(now)
	timing.phase = phase
	timing.phaseStarted = now
}

func (timing *requestPreparationTiming) snapshot(now time.Time) (string, time.Duration, time.Duration) {
	timing.mu.Lock()
	defer timing.mu.Unlock()
	waa, responseHeader := timing.phaseElapsed(now)
	waa += timing.waa
	responseHeader += timing.responseHeader
	current := string(timing.phase)
	switch timing.phase {
	case aistudio.RequestPhasePreparingWAA:
		current = "WAA proof"
	case aistudio.RequestPhaseSendingUpstream:
		current = "等待上游响应头"
	case aistudio.RequestPhaseStreaming:
		current = "流已建立"
	}
	return current, waa, responseHeader
}

// finishPhaseLocked 把当前阶段到 now 的耗时计入请求准备或等待响应头；调用方持有 mu
func (timing *requestPreparationTiming) finishPhaseLocked(now time.Time) {
	waa, responseHeader := timing.phaseElapsed(now)
	timing.waa += waa
	timing.responseHeader += responseHeader
}

// phaseElapsed 把当前阶段到 now 的耗时归为请求准备或等待响应头：等待响应头阶段计入后者，流已建立后两者都为零，其余阶段（含 Camoufox 界面运行的界面步骤）计入请求准备；调用方持有 mu
func (timing *requestPreparationTiming) phaseElapsed(now time.Time) (waa time.Duration, responseHeader time.Duration) {
	elapsed := now.Sub(timing.phaseStarted)
	switch timing.phase {
	case aistudio.RequestPhaseSendingUpstream:
		return 0, elapsed
	case aistudio.RequestPhaseStreaming:
		return 0, 0
	}
	return elapsed, 0
}

func (activity *upstreamActivity) observe(count int) {
	if count <= 0 {
		return
	}
	now := time.Now().UnixNano()
	activity.lastNano.Store(now)
	activity.bytes.Add(int64(count))
}

// logFields 返回已收到的网络字节数与距最近一次收到数据的时长，用作日志字段
func (activity *upstreamActivity) logFields(now time.Time) []any {
	lastNano := activity.lastNano.Load()
	if lastNano == 0 {
		return []any{"network_bytes", 0}
	}
	return []any{"network_bytes", activity.bytes.Load(), "network_idle", now.Sub(time.Unix(0, lastNano))}
}

// inlineMediaInput 统计规范消息中的内联附件数量和原始大小
func inlineMediaInput(contents []aistudio.Content) (int, int64) {
	count := 0
	var bytes int64
	for _, content := range contents {
		for _, part := range content.Parts {
			if part.InlineData == nil {
				continue
			}
			count++
			bytes += int64(len(part.InlineData.Data))
		}
	}
	return count, bytes
}

// requestInputBytes 返回请求系统指令、各轮文本、函数调用参数、函数结果与内联附件的字节数，作为同一账户额度拒绝比较请求大小的依据
func requestInputBytes(request aistudio.GenerateRequest) int64 {
	var size int64
	for _, text := range request.System {
		size += int64(len(text))
	}
	for _, content := range request.Contents {
		for _, part := range content.Parts {
			size += int64(len(part.Text))
			if part.InlineData != nil {
				size += int64(len(part.InlineData.Data))
			}
			if part.FunctionCall != nil {
				size += int64(len(part.FunctionCall.Arguments))
			}
			if part.FunctionResult != nil {
				size += int64(len(part.FunctionResult.Content))
			}
		}
	}
	return size
}

// Generate 获取唯一账户并转发规范事件流
func (service *trackedService) Generate(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	generationStartedAt := time.Now()
	model, config, suffixErr := aistudio.ApplyModelSuffix(service.mappedModel(request.Model), request.Config)
	request.Model, request.Config = model, config
	api.SetAccessLogTarget(ctx, request.Model, "")
	api.SetAccessLogGenerationConfig(ctx, request.Config)
	api.SetAccessLogGenerationInput(ctx, request)
	api.StartAccessLog(ctx)
	request.Model = service.catalogModelID(ctx, request.Model)
	requestCtx, cancel, err := service.dataRequestContext(ctx)
	if err == nil && suffixErr != nil {
		err = suffixErr
		cancel()
	}
	if err == nil && aistudio.RequestBuildOnly(request) && !service.pool.BuildEnabled() {
		err = fmt.Errorf("%w: File Search 只能经 Build 通道发送，UPSTREAM_CHANNELS 没有启用 build", aistudio.ErrInvalidArgument)
		cancel()
	}
	if validator, ok := service.service.(interface {
		ValidateGenerateRequest(aistudio.GenerateRequest) error
	}); ok && err == nil {
		if err = validator.ValidateGenerateRequest(request); err != nil {
			cancel()
		}
	}
	if err != nil {
		api.SetAccessLogError(ctx, err)
		service.requests.start(request, func() {})
		service.requests.finish(request.ID, "failed")
		return nil, err
	}
	service.requests.start(request, cancel)
	resourceID, err := service.pool.ResourceIDForContents(requestCtx, request.Contents)
	if err != nil {
		err = endedRequestError(requestCtx, err)
		api.SetAccessLogError(requestCtx, err)
		cancel()
		service.requests.finish(request.ID, finalRequestState(err))
		return nil, err
	}
	events := make(chan aistudio.Event, 8)
	go service.generateWithRetry(ctx, requestCtx, cancel, generationStartedAt, request, resourceID, events)
	return events, nil
}

// generateAttempt 是一次上游尝试的账户租约、准备与发送、等待首事件的结果，done 关闭后结果字段可读
type generateAttempt struct {
	lease            *aistudio.AccountLease
	selection        aistudio.AccountSelection
	hedge            bool
	unbound          bool
	accountLabel     string
	proxy            string
	workerGeneration uint64
	startedAt        time.Time
	copies           *aistudio.TemporaryFileCopies
	activity         *upstreamActivity
	source           <-chan aistudio.Event
	first            aistudio.Event
	firstAt          time.Time
	err              error
	cancel           context.CancelFunc
	done             chan struct{}
	// serving 是正在生成回答的账户租约，续写轮换账户后为新账户的租约
	serving atomic.Pointer[aistudio.AccountLease]
	// failures 是请求已用掉的换号次数，续写轮换号在此基础上计数
	failures int
}

// generateRetry 是生成请求跨尝试的换号与恢复状态
type generateRetry struct {
	maxAttempts       int
	attempted         map[string]struct{}
	recoveredWorker   map[string]struct{}
	recoveryAccountID string
	buildRejection    *rejectedBuildAttempt
	// buildOnly 表示请求只能经 Build 通道发送，Build 拒绝参数时不改用 Playground
	buildOnly bool
	// attemptFailure 是最近一次既非额度冷却也非 Build 参数拒绝的尝试错误
	attemptFailure error
	// missingInlineMedia 表示已因复用的内联附件文件不存在而在同一账户重试过
	missingInlineMedia bool
	// quotaUntil 是本次请求中因额度失败而冷却的账户最早的恢复时刻
	quotaUntil time.Time
	// quotaReason 是本次请求中最近一次额度失败的冷却原因
	quotaReason string
	// cooled 是本次请求排队等冷却恢复累计的时长
	cooled time.Duration
	// rejectionUntil 是本次请求中因上游 403、协议错误码 7 而冷却的账户最早的恢复时刻
	rejectionUntil time.Time
	// rejectedAccountID 是本次请求中第一次被上游 403、协议错误码 7 拒绝且没有冷却的账户，没有其他账户可换时在它上面重试一次
	rejectedAccountID string
	// rejectedAccountLabel 是 rejectedAccountID 的账户标签
	rejectedAccountLabel string
	// rejectionRetried 表示已在被拒账户上重试过
	rejectionRetried bool
	// failures 是本次请求中换号重试的非额度失败次数
	failures int
}

// attemptOutcome 是失败尝试处理后的错误与换号决定
type attemptOutcome struct {
	err           error
	retryable     bool
	recoverWorker bool
	accountID     string
	accountLabel  string
}

func (service *trackedService) generateWithRetry(
	clientCtx context.Context,
	requestCtx context.Context,
	cancel context.CancelFunc,
	generationStartedAt time.Time,
	request aistudio.GenerateRequest,
	resourceID string,
	destination chan<- aistudio.Event,
) {
	requestedAccountID := strings.TrimSpace(request.AccountID)
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	continuationRounds := service.pool.OutputContinuationRounds(modelID, request.Config.MaxOutputTokens)
	unbound := requestedAccountID == "" && resourceID == ""
	fileBound := requestedAccountID == "" && resourceID != ""
	retry := &generateRetry{maxAttempts: 1, recoveredWorker: make(map[string]struct{}), buildOnly: aistudio.RequestBuildOnly(request)}
	if unbound || fileBound {
		retry.maxAttempts = max(len(service.pool.EnabledAccounts()), 1)
	}
	retry.attempted = make(map[string]struct{}, retry.maxAttempts)
	requestSize := requestInputBytes(request)
	assistant := service.pool.AssistantModel(modelID)
	preferredAccountID := ""
	if assistant {
		preferredAccountID = service.service.(aistudio.AssistantService).AssistantAccount(requestCtx, request)
	} else {
		preferredAccountID = service.pool.InlineMediaAccount(request.Contents)
	}
	hedgeable := !request.Background && !assistant && !service.pool.InteractionAgentModel(modelID) && (service.hedgeAuto || service.hedgeAfter > 0) && slices.Contains(service.hedgeModels, modelID)
	hedged := false
	var winner *generateAttempt
	var activity *upstreamActivity
	var err error
	copyFiles := false
	// bursting 是请求开始时成片断连的通道，settled 是请求非备用尝试用过的账户，broken 是其中前两个账户里首次尝试在这些通道上以连接断开失败的账户
	bursting := service.disconnects.bursting(modelID, generationStartedAt)
	var lastChannel aistudio.Channel
	settled, broken := make(map[string]struct{}), make(map[string]struct{})
	for attempt := 0; attempt < retry.maxAttempts; attempt++ {
		selectionAccountID := requestedAccountID
		recoveringAccount := retry.recoveryAccountID != ""
		if retry.recoveryAccountID != "" {
			selectionAccountID = retry.recoveryAccountID
			retry.recoveryAccountID = ""
		}
		selectionResourceID := resourceID
		if copyFiles {
			selectionResourceID = ""
		}
		selection := aistudio.AccountSelection{
			ModelID: modelID, Method: "generateContent",
			AccountID: selectionAccountID, ResourceID: selectionResourceID, PreferredAccountID: preferredAccountID,
			RequestSize: requestSize,
		}
		if resourceID != "" || request.Config.SpeechConfig != nil && request.Config.SpeechConfig.Mode != "" {
			selection.PlaygroundOnly = true
		}
		if continuationRounds > 1 && (request.Config.MaxOutputTokens != nil || !aistudio.RequestNeedsBuild(request)) {
			selection.PlaygroundOnly = true
		}
		if !assistant && !selection.PlaygroundOnly && service.pool.BuildEnabled() && aistudio.RequestNeedsBuild(request) {
			selection.Channel = aistudio.ChannelBuild
		}
		fallbackReason := ""
		if request.Unary {
			switch {
			case assistant:
			case selection.PlaygroundOnly:
				fallbackReason = "请求包含 Playground 专用能力或文件引用"
			case selection.Channel != "":
			case !service.pool.BuildEnabled():
			case !service.buildNativeNonstream:
				fallbackReason = "Build 非流式优先选项已关闭"
			default:
				selection.PreferredChannel = aistudio.ChannelBuild
			}
		}
		if retry.buildRejection != nil {
			selection.PlaygroundOnly = true
			selection.PreferredChannel = ""
			fallbackReason = "Build 拒绝请求参数"
		}
		if (unbound || fileBound) && len(retry.attempted) > 0 {
			selection.AllowedAccountIDs = []string{}
			for _, accountID := range service.pool.EnabledAccounts() {
				if _, exists := retry.attempted[accountID]; !exists {
					selection.AllowedAccountIDs = append(selection.AllowedAccountIDs, accountID)
				}
			}
		}
		if len(retry.attempted) > 0 {
			service.requests.markQueued(request.ID, request.AccountID)
		}
		queueStartedAt := time.Now()
		queueWait := startWaitReporter(func(waited time.Duration) {
			service.requests.logRequestProgress(request.ID, "request", "WARN", "排队等待", "waited", waited, "model", modelID)
		})
		quotaFallback := selection.ResourceID == ""
		lease, acquireErr := service.acquireWarmLease(requestCtx, selection, quotaFallback, &retry.failures, &retry.cooled)
		var buildCooling *aistudio.AllCoolingError
		if acquireErr != nil && selection.Channel == aistudio.ChannelBuild && requestCtx.Err() == nil && !aistudio.RequestBuildOnly(request) &&
			(errors.Is(acquireErr, aistudio.ErrNoEligibleAccount) || errors.As(acquireErr, &buildCooling)) {
			// 没有账户能经 Build 服务时改用 Playground，Playground 无法表达的设置按其最接近的形式发送
			selection.Channel = ""
			fallbackReason = "Build 通道没有可用账户"
			lease, acquireErr = service.acquireWarmLease(requestCtx, selection, quotaFallback, &retry.failures, &retry.cooled)
		}
		queueDelayed := queueWait.finish()
		if acquireErr == nil && selection.PreferredChannel == aistudio.ChannelBuild && lease.Channel() != aistudio.ChannelBuild {
			fallbackReason = "账户 Build 通道冷却或不支持此模型"
		}
		if acquireErr != nil {
			if errors.Is(acquireErr, aistudio.ErrNoEligibleAccount) && service.awaitCatalogSync(requestCtx) {
				retry.maxAttempts++
				continue
			}
			var ownerCooling *aistudio.AllCoolingError
			if fileBound && !copyFiles && (errors.Is(acquireErr, aistudio.ErrNoEligibleAccount) || errors.As(acquireErr, &ownerCooling)) && requestCtx.Err() == nil {
				copyFiles = true
				continue
			}
			var cooling *aistudio.AllCoolingError
			switch {
			case err != nil && errors.Is(acquireErr, aistudio.ErrNoEligibleAccount):
			case retry.attemptFailure != nil && errors.As(acquireErr, &cooling):
				err = retry.attemptFailure
			case retry.quotaReason != "" && errors.As(acquireErr, &cooling):
				// 本次请求的尝试都因额度失败时按额度返回全部冷却，恢复时刻取已试账户与其余账户中最早的一个
				until := cooling.Until
				if !retry.quotaUntil.IsZero() && retry.quotaUntil.Before(until) {
					until = retry.quotaUntil
				}
				err = &aistudio.AllCoolingError{ModelID: cooling.ModelID, Until: until, Reason: retry.quotaReason}
			default:
				err = acquireErr
			}
			if recoveringAccount && unbound && requestCtx.Err() == nil {
				retry.attempted[selectionAccountID] = struct{}{}
				continue
			}
			if requestCtx.Err() == nil && service.retryRejectedAccount(request.ID, modelID, retry, err) {
				continue
			}
			break
		}
		err = nil
		accountLabel := lease.Account().Config.Label
		retry.attempted[lease.Account().ID] = struct{}{}
		service.markAttemptAccount(requestCtx, request.ID, modelID, lease)
		api.MarkAccessLogScheduled(requestCtx)
		if queueDelayed {
			service.requests.logRequestProgress(request.ID, accountLabel, "INFO", "排队结束", "waited", time.Since(queueStartedAt))
		}
		service.requests.logRequestProgress(request.ID, accountLabel, "INFO", "等待上游响应")
		primary := service.startAttempt(requestCtx, request, resourceID, modelID, fallbackReason, lease, selection, false, retry.failures)
		var plan *hedgePlan
		if hedgeable && !hedged {
			if delay, ok := service.hedgeDelay(modelID, string(lease.Channel())); ok {
				fixed := resourceID != "" || requestedAccountID != ""
				plan = &hedgePlan{delay: delay, selections: service.hedgeSelections(selection, lease.Account().ID, fixed, retry.attempted)}
				plan.start = func(hedgeLease *aistudio.AccountLease, hedgeSelection aistudio.AccountSelection) *generateAttempt {
					hedged = true
					retry.attempted[hedgeLease.Account().ID] = struct{}{}
					return service.startAttempt(requestCtx, request, resourceID, modelID, fallbackReason, hedgeLease, hedgeSelection, true, retry.failures)
				}
			}
		}
		var last attemptOutcome
		winner = service.awaitFirstEvent(requestCtx, request, modelID, primary, plan, func(failed *generateAttempt, sameAccountPending bool) {
			activity = failed.activity
			last = service.settleAttempt(requestCtx, request.ID, modelID, failed, retry, !sameAccountPending)
			lastChannel = failed.serving.Load().Channel()
			if _, seen := settled[last.accountID]; !failed.hedge && !seen {
				settled[last.accountID] = struct{}{}
				if len(settled) <= 2 && bursting[lastChannel] && disconnected(last.err) {
					broken[last.accountID] = struct{}{}
				}
			}
		})
		if winner != nil {
			activity = winner.activity
			if winner.hedge {
				service.markAttemptAccount(requestCtx, request.ID, modelID, winner.serving.Load())
			}
			var firstOutput int64
			if usage := winner.first.FrameUsage; usage != nil {
				firstOutput = usage.OutputTokens + usage.ReasoningTokens
			}
			api.SetAccessLogFirstEvent(requestCtx, winner.firstAt.Sub(generationStartedAt), firstOutput)
			api.SetAccessLogTarget(requestCtx, winner.first.ProviderModel, winner.serving.Load().Account().Config.Label)
			if request.Unary {
				service.requests.markData(request.ID, winner.firstAt)
				collected, collectErr := collectGenerateEvents(requestCtx, winner.source, func(at time.Time) {
					service.requests.markData(request.ID, at)
				}, func(waited time.Duration, events []aistudio.Event) {
					reasoning, content := 0, 0
					for _, event := range events {
						switch event.Kind {
						case aistudio.EventReasoning:
							reasoning++
						case aistudio.EventText:
							content++
						}
					}
					service.requests.logRequestProgress(request.ID, winner.accountLabel, "INFO", "收集完整响应",
						append([]any{"waited", waited, "reasoning_events", reasoning, "text_events", content, "model", modelID}, winner.activity.logFields(time.Now())...)...)
				})
				if collectErr != nil {
					winner.err = collectErr
					last = service.settleAttempt(requestCtx, request.ID, modelID, winner, retry, true)
					winner = nil
				} else {
					winner.source = collected
				}
			}
		}
		if winner != nil {
			service.proxies.ReportSuccess(winner.proxy)
			service.disconnects.record(modelID, winner.serving.Load().Channel(), false, time.Now())
			if retry.buildRejection != nil {
				service.coolRejectedBuild(request.ID, retry.buildRejection, modelID, winner.accountLabel)
			}
			break
		}
		err = last.err
		if !last.retryable {
			break
		}
		if len(broken) == 2 {
			service.requests.logRequestProgress(request.ID, last.accountLabel, "WARN", "同模型同通道最近两个请求都因连接断开失败，本请求也连续两次连接断开，停止重试", attemptFields(modelID, err)...)
			break
		}
		if last.recoverWorker {
			retry.recoveryAccountID = last.accountID
			delete(retry.attempted, last.accountID)
			retry.maxAttempts++
		}
		if attempt+1 == retry.maxAttempts {
			if service.retryRejectedAccount(request.ID, modelID, retry, err) {
				continue
			}
			break
		}
		if last.recoverWorker && errors.Is(err, aistudio.ErrProtectedTransport) {
			service.requests.logRequestProgress(request.ID, last.accountLabel, "WARN", "受保护请求网络错误，同账户重放当前请求", attemptFields(modelID, err)...)
			continue
		}
		if last.recoverWorker {
			service.requests.logRequestProgress(request.ID, last.accountLabel, "WARN", "WAA Worker 重建，重放当前请求", attemptFields(modelID, err)...)
			continue
		}
		service.requests.logRequestProgress(request.ID, last.accountLabel, "WARN", "账号切换", attemptFields(modelID, err)...)
	}
	if winner == nil {
		if disconnected(err) {
			service.disconnects.record(modelID, lastChannel, true, time.Now())
		}
		if activity != nil {
			api.SetAccessLogUpstreamBytes(requestCtx, activity.bytes.Load())
		}
		err = accountsUnavailable(endedRequestError(requestCtx, err), retry.rejectionUntil)
		api.SetAccessLogError(requestCtx, err)
		service.requests.finish(request.ID, finalRequestState(err))
		select {
		case destination <- aistudio.Event{Kind: aistudio.EventError, Err: err}:
		case <-clientCtx.Done():
		}
		cancel()
		close(destination)
		return
	}
	service.forwardEvents(
		clientCtx, requestCtx, func() { winner.cancel(); cancel() }, request.ID,
		winner.first, winner.source, destination, &winner.serving, winner.copies, winner.activity, modelID,
	)
}

// markAttemptAccount 把访问日志与活动请求的执行账户与通道记为该租约
func (service *trackedService) markAttemptAccount(requestCtx context.Context, requestID, modelID string, lease *aistudio.AccountLease) {
	label := lease.Account().Config.Label
	api.SetAccessLogChannel(requestCtx, string(lease.Channel()))
	api.SetAccessLogTarget(requestCtx, modelID, label)
	service.requests.markRunning(requestID, lease.Account().ID, label)
	service.requests.markChannel(requestID, string(lease.Channel()))
}

// accountLabel 返回账户的显示标签，账户已不在账户池时返回账户 ID
func (service *trackedService) accountLabel(accountID string) string {
	for _, status := range service.pool.Status() {
		if status.ID == accountID {
			return status.Label
		}
	}
	return accountID
}

// startAttempt 在租约账户上准备并发送请求，后台等待首事件，结束时关闭 done；failures 是发起这次尝试前请求已用掉的换号次数
func (service *trackedService) startAttempt(
	requestCtx context.Context,
	request aistudio.GenerateRequest,
	resourceID string,
	modelID string,
	fallbackReason string,
	lease *aistudio.AccountLease,
	selection aistudio.AccountSelection,
	hedge bool,
	failures int,
) *generateAttempt {
	unbound := strings.TrimSpace(request.AccountID) == "" && resourceID == ""
	request.AccountID = lease.Account().ID
	attempt := &generateAttempt{
		lease: lease, selection: selection, hedge: hedge, unbound: unbound, accountLabel: lease.Account().Config.Label,
		proxy: service.proxies.Binding(request.AccountID), workerGeneration: service.workers.WorkerGeneration(request.AccountID),
		startedAt: time.Now(), activity: &upstreamActivity{}, done: make(chan struct{}), failures: failures,
	}
	attempt.serving.Store(lease)
	attemptCtx, cancel := context.WithCancel(requestCtx)
	attempt.cancel = cancel
	go func() {
		defer close(attempt.done)
		service.runAttempt(attemptCtx, request, resourceID, modelID, fallbackReason, attempt)
	}()
	return attempt
}

// runAttempt 复制文件引用、上传内联附件、发送请求并等待首事件，结果写入 attempt
func (service *trackedService) runAttempt(
	ctx context.Context,
	request aistudio.GenerateRequest,
	resourceID string,
	modelID string,
	fallbackReason string,
	attempt *generateAttempt,
) {
	lease, accountLabel := attempt.lease, attempt.accountLabel
	inlineImages := false
	for _, model := range service.modelSnapshot() {
		if model.ID != modelID {
			continue
		}
		if model.Capabilities["interaction_task"] {
			// agent 后台任务只发送 AgentInput 选出的内容，其余轮次的文件不复制、附件不上传
			request.Contents = aistudio.AgentInput(request.Contents, request.PreviousInteractionID != "")
		}
		inlineImages = model.Capabilities["interaction_stream"]
	}
	originalContents := request.Contents
	ctx = aistudio.ContextWithAccountLease(ctx, lease)
	ctx = aistudio.ContextWithUpstreamModeObserver(ctx, service.upstreamModeObserver(request.ID, lease, fallbackReason))
	var err error
	copiedFileCount := 0
	if resourceID != "" {
		request.Contents, attempt.copies, err = service.service.(interface {
			CopyFileReferencesToLease(context.Context, *aistudio.AccountLease, []aistudio.Content) ([]aistudio.Content, *aistudio.TemporaryFileCopies, error)
		}).CopyFileReferencesToLease(ctx, lease, originalContents)
		if err == nil {
			copiedFileCount = attempt.copies.Count()
		}
	}
	mediaCount, mediaBytes := inlineMediaInput(request.Contents)
	if attempt.unbound && mediaCount == 0 {
		failed := make(map[string]struct{})
		ctx = aistudio.ContextWithContinuationAccounts(ctx, func(roundCtx context.Context, err error) (context.Context, error) {
			return service.continuationAccount(roundCtx, request.ID, modelID, attempt, failed, err)
		})
	}
	if err == nil && mediaCount > 0 {
		uploadStartedAt := time.Now()
		request.Contents, attempt.copies, err = service.service.(interface {
			UploadInlineMediaToLease(context.Context, *aistudio.AccountLease, []aistudio.Content, *aistudio.TemporaryFileCopies, bool) ([]aistudio.Content, *aistudio.TemporaryFileCopies, error)
		}).UploadInlineMediaToLease(ctx, lease, request.Contents, attempt.copies, inlineImages)
		if err == nil {
			service.requests.logRequestProgress(request.ID, accountLabel, "INFO", "内联附件处理完成",
				"attachments", mediaCount, "bytes", fmt.Sprintf("%d B", mediaBytes), "duration", time.Since(uploadStartedAt))
		}
	}
	prepareStartedAt := time.Now()
	prepareTiming := newRequestPreparationTiming(prepareStartedAt)
	prepareWait := startWaitReporter(func(waited time.Duration) {
		current, _, _ := prepareTiming.snapshot(time.Now())
		service.requests.logRequestProgress(request.ID, accountLabel, "WARN", "请求准备等待", "waited", waited, "current_step", current, "model", modelID)
	})
	ctx = aistudio.ContextWithStreamActivityObserver(ctx, attempt.activity.observe)
	ctx = aistudio.ContextWithRequestPhaseObserver(ctx, prepareTiming.observe)
	if err == nil {
		attempt.source, err = service.service.Generate(ctx, request)
	}
	if err == nil && copiedFileCount > 0 {
		sourceLabels := make([]string, 0, len(attempt.copies.SourceAccountIDs()))
		for _, sourceID := range attempt.copies.SourceAccountIDs() {
			sourceLabels = append(sourceLabels, service.accountLabel(sourceID))
		}
		service.requests.log(accountLabel, "INFO", "文件引用复制", "from_accounts", strings.Join(sourceLabels, ","), "files", copiedFileCount)
	}
	prepareElapsed := time.Since(prepareStartedAt)
	if prepareWait.finish() {
		_, waa, responseHeader := prepareTiming.snapshot(time.Now())
		service.requests.logRequestProgress(request.ID, accountLabel, "INFO", "请求准备结束",
			"waited", prepareElapsed, "waa", waa, "response_headers", responseHeader, "model", modelID)
	}
	if err == nil {
		upstreamStartedAt := time.Now()
		firstEventDelayed := false
		attempt.first, err = firstGenerateEvent(ctx, attempt.source, func(waited time.Duration) {
			firstEventDelayed = true
			service.requests.logRequestProgress(request.ID, accountLabel, "WARN", "上游首事件等待",
				append([]any{"waited", waited, "model", modelID}, attempt.activity.logFields(time.Now())...)...)
		})
		attempt.firstAt = time.Now()
		if firstEventDelayed && err == nil {
			service.requests.logRequestProgress(request.ID, accountLabel, "INFO", "上游首事件到达",
				"waited", time.Since(upstreamStartedAt), "first_event", attempt.first.Kind, "model", modelID)
		}
	}
	attempt.err = err
}

// upstreamModeObserver 返回把租约账户的上游调用方式记入请求日志的观察函数，回退流式时附上 fallbackReason
func (service *trackedService) upstreamModeObserver(requestID string, lease *aistudio.AccountLease, fallbackReason string) func(string, string, int, string) {
	accountLabel := lease.Account().Config.Label
	return func(method, mode string, round int, reason string) {
		level, message := "INFO", "上游调用"
		attrs := []any{"channel", lease.Channel(), "mode", mode, "rpc", method}
		if round > 0 {
			attrs = append(attrs, "continuation", round)
		}
		if reason != "" && service.pool.BuildEnabled() {
			level, message = "WARN", "上游调用回退流式"
			if fallbackReason != "" {
				reason = fallbackReason + "; " + reason
			}
			attrs = append(attrs, "reason", reason)
		}
		service.requests.logRequestProgress(requestID, accountLabel, level, message, attrs...)
	}
}

// continuationAccount 把续写轮在当前账户上的失败记为一次尝试，额度失败时冷却该账户，释放其租约后从未在续写轮失败过的其余账户取得租约，返回改由该账户发送续写轮的上下文；非额度失败计入换号次数，超过上限时返回 ErrNoEligibleAccount
func (service *trackedService) continuationAccount(ctx context.Context, requestID string, modelID string, attempt *generateAttempt, failed map[string]struct{}, err error) (context.Context, error) {
	current := attempt.serving.Load()
	label := current.Account().Config.Label
	api.AddAccessLogAttempt(ctx, api.RequestAttempt{Account: label, Channel: string(current.Channel()), Error: err.Error()})
	failed[current.Account().ID] = struct{}{}
	if denial := aistudio.RestrictionDenial(err); denial != nil {
		if stateErr := service.pool.MarkRestricted(current.Account().ID, denial); stateErr != nil {
			return nil, stateErr
		}
	}
	cooldown, quota := aistudio.QuotaCooldownForError(err, time.Now())
	cooling := quota
	if !cooling {
		cooldown, cooling = aistudio.AccountRejectionCooldown(err)
	}
	if cooling {
		if _, stateErr := service.markQuotaCooldown(requestID, modelID, current, cooldown); stateErr != nil {
			return nil, stateErr
		}
	}
	if releaseErr := current.Release(); releaseErr != nil {
		return nil, releaseErr
	}
	if !quota && !errors.Is(err, aistudio.ErrAccountCoolingDown) {
		if attempt.failures++; int64(attempt.failures) > service.maxAccountSwitches.Load() {
			return nil, aistudio.ErrNoEligibleAccount
		}
	}
	selection := aistudio.AccountSelection{ModelID: modelID, Method: "generateContent", PlaygroundOnly: true, AllowedAccountIDs: []string{}}
	for _, accountID := range service.pool.EnabledAccounts() {
		if _, exists := failed[accountID]; !exists {
			selection.AllowedAccountIDs = append(selection.AllowedAccountIDs, accountID)
		}
	}
	lease, acquireErr := service.acquireWarmLease(ctx, selection, false, &attempt.failures, new(time.Duration))
	if acquireErr != nil {
		return nil, acquireErr
	}
	attempt.serving.Store(lease)
	service.requests.logRequestProgress(requestID, label, "WARN", "续写轮账号切换", attemptFields(modelID, err)...)
	service.markAttemptAccount(ctx, requestID, modelID, lease)
	next := aistudio.ContextWithAccountLease(ctx, lease)
	return aistudio.ContextWithUpstreamModeObserver(next, service.upstreamModeObserver(requestID, lease, "")), nil
}

// markQuotaCooldown 以收到失败的时刻按额度失败冷却租约账户在该模型或全局范围上的调度，冷却改变时记入请求日志，返回该范围生效的恢复时刻
func (service *trackedService) markQuotaCooldown(requestID, modelID string, lease *aistudio.AccountLease, cooldown aistudio.QuotaCooldown) (time.Time, error) {
	scope := lease.CooldownScope(modelID)
	scopeLabel := scope
	if cooldown.Global {
		scope, scopeLabel = "", "全局"
	}
	until, changed, err := service.pool.MarkQuotaCooldownIfGeneration(lease.Account().ID, scope, lease.ModelAccessGeneration(), time.Now(), cooldown)
	if err == nil && changed {
		service.requests.logRequestProgress(requestID, lease.Account().Config.Label, "WARN", "账号冷却",
			"kind", cooldown.Kind, "scope", scopeLabel, "until", until.Local().Format(time.RFC3339))
	}
	return until, err
}

// quotaCooldownError 是带恢复时刻的额度失败，恢复时刻取本次请求中额度失败账户最早的冷却恢复时刻
type quotaCooldownError struct {
	err   error
	until time.Time
}

func (err *quotaCooldownError) Error() string {
	return err.err.Error()
}

func (err *quotaCooldownError) Unwrap() error {
	return err.err
}

// RetryAt 返回额度失败账户最早的冷却恢复时刻
func (err *quotaCooldownError) RetryAt(time.Time) time.Time {
	return err.until
}

// retryRejectedAccount 在没有其他账户可换时把本次请求第一次被上游 403、协议错误码 7 拒绝且没有冷却的账户排为下一次尝试，返回是否已排入
func (service *trackedService) retryRejectedAccount(requestID string, modelID string, retry *generateRetry, err error) bool {
	accountID := retry.rejectedAccountID
	if accountID == "" {
		return false
	}
	retry.recoveryAccountID = accountID
	delete(retry.attempted, accountID)
	retry.maxAttempts++
	service.requests.logRequestProgress(requestID, retry.rejectedAccountLabel, "WARN", "没有其他账户可换，同账户重试被上游拒绝的请求", attemptFields(modelID, err)...)
	retry.rejectedAccountID, retry.rejectedAccountLabel, retry.rejectionRetried = "", "", true
	return true
}

// accountsUnavailableError 是最后一次失败为上游暂时拒绝账户（403、协议错误码 7）或认证失败（401）的请求交给客户端的 503
type accountsUnavailableError struct {
	rpc   *aistudio.RPCError
	err   error
	until time.Time
}

// accountsUnavailable 把最后一次失败为上游 403 协议错误码 7 或 401 的错误改为 503，until 为被拒账户最早的冷却恢复时刻；其余错误原样返回
func accountsUnavailable(err error, until time.Time) error {
	var rpcError *aistudio.RPCError
	if !errors.As(err, &rpcError) {
		return err
	}
	if _, rejected := aistudio.AccountRejectionCooldown(err); !rejected && aistudio.RestrictionDenial(err) == nil && rpcError.StatusCode != http.StatusUnauthorized {
		return err
	}
	return &accountsUnavailableError{rpc: rpcError, err: err, until: until}
}

// Error 返回上游方法、gRPC 状态名、协议错误码与原因
func (err *accountsUnavailableError) Error() string {
	return err.rpc.StatusError()
}

func (err *accountsUnavailableError) Unwrap() error {
	return err.err
}

// HTTPStatus 返回账户暂时不可用对应的 503
func (err *accountsUnavailableError) HTTPStatus() int {
	return http.StatusServiceUnavailable
}

// ErrorCode 返回 OpenAI 兼容的账户暂时不可用错误代码
func (err *accountsUnavailableError) ErrorCode() string {
	return "account_unavailable"
}

// RetryAt 返回被拒账户最早的冷却恢复时刻，晚于 cooldownQueueLimit 时返回零值
func (err *accountsUnavailableError) RetryAt(now time.Time) time.Time {
	if err.until.Sub(now) > cooldownQueueLimit {
		return time.Time{}
	}
	return err.until
}

// settleAttempt 处理失败的尝试：在正在生成回答的账户上记录访问日志、冷却与认证失败，释放其租约，mayRecover 为真时按需重建 Worker
func (service *trackedService) settleAttempt(
	requestCtx context.Context,
	requestID string,
	modelID string,
	attempt *generateAttempt,
	retry *generateRetry,
	mayRecover bool,
) attemptOutcome {
	err := attempt.err
	lease := attempt.serving.Load()
	accountID, accountLabel := lease.Account().ID, lease.Account().Config.Label
	proxy, workerGeneration := attempt.proxy, attempt.workerGeneration
	if lease != attempt.lease {
		proxy, workerGeneration = service.proxies.Binding(accountID), service.workers.WorkerGeneration(accountID)
	}
	missingInlineMedia := false
	if attempt.copies != nil {
		err = errors.Join(err, attempt.copies.Cleanup())
		if !retry.missingInlineMedia && requestCtx.Err() == nil {
			var forgetErr error
			missingInlineMedia, forgetErr = attempt.copies.ForgetMissingInlineMedia(requestCtx, attempt.err)
			err = errors.Join(err, forgetErr)
		}
	}
	api.AddAccessLogAttempt(requestCtx, api.RequestAttempt{
		Account: accountLabel, Channel: string(lease.Channel()), Error: err.Error(),
		DurationMS: time.Since(attempt.startedAt).Milliseconds(), Hedge: attempt.hedge,
	})
	var policy *aistudio.ContentPolicyError
	var abnormal *aistudio.AbnormalFinishError
	var feedback *aistudio.PromptFeedbackError
	switch {
	case errors.As(err, &policy) && policy.Usage != nil:
		api.SetAccessLogGenerationResult(requestCtx, policy.Usage, 0)
	case errors.As(err, &abnormal) && abnormal.Usage != nil:
		api.SetAccessLogGenerationResult(requestCtx, abnormal.Usage, 0)
	case errors.As(err, &feedback) && feedback.Usage != nil:
		api.SetAccessLogGenerationResult(requestCtx, feedback.Usage, 0)
	}
	if proxyNetworkError(requestCtx, err) {
		service.reportProxyFailure(proxy, accountLabel, err)
	}
	workerFailed := service.workers.WorkerFailed(accountID)
	waaRuntimeFailed := aistudio.DefinitiveWAARuntimeFailure(err)
	workerReplaced := errors.Is(err, errAccountWorkerReplaced)
	protectedTransport := errors.Is(err, aistudio.ErrProtectedTransport) && requestCtx.Err() == nil
	localWorkerFailure := (workerFailed || workerReplaced) && requestCtx.Err() == nil
	retryable := retryableGenerateAccountError(requestCtx, err) || localWorkerFailure
	if aistudio.DefinitiveAuthenticationFailure(err) {
		if stateErr := lease.ConfirmAuthenticationFailure(requestCtx, err); stateErr != nil {
			err = errors.Join(err, stateErr)
			retryable = false
		}
	}
	// cooledUntil 是这次失败使账户在该模型上冷却到的时刻
	var cooledUntil time.Time
	cooldown, quotaFailure := aistudio.QuotaCooldownForError(err, time.Now())
	if quotaFailure {
		cooldown.RequestSize = attempt.selection.RequestSize
		until, stateErr := service.markQuotaCooldown(requestID, modelID, lease, cooldown)
		if stateErr != nil {
			err = errors.Join(err, stateErr)
			retryable = false
		} else {
			cooledUntil = until
			retry.quotaReason = cooldown.Reason
			if !until.IsZero() {
				if retry.quotaUntil.IsZero() || until.Before(retry.quotaUntil) {
					retry.quotaUntil = until
				}
				err = &quotaCooldownError{err: err, until: retry.quotaUntil}
			}
			if !cooldown.Global && service.pool.AccountChannelAvailable(accountID, attempt.selection) {
				delete(retry.attempted, accountID)
				retry.maxAttempts++
			}
		}
	}
	denial := aistudio.RestrictionDenial(err)
	if denial != nil {
		if stateErr := service.pool.MarkRestricted(accountID, denial); stateErr != nil {
			err = errors.Join(err, stateErr)
			retryable = false
		}
	}
	rejection, rejected := aistudio.AccountRejectionCooldown(err)
	if rejected {
		until, stateErr := service.markQuotaCooldown(requestID, modelID, lease, rejection)
		if stateErr == nil {
			cooledUntil = until
		}
		switch {
		case stateErr != nil:
			err = errors.Join(err, stateErr)
			retryable = false
		case !until.After(time.Now()):
			if !retry.rejectionRetried && retry.rejectedAccountID == "" {
				retry.rejectedAccountID, retry.rejectedAccountLabel = accountID, accountLabel
			}
		case retry.rejectionUntil.IsZero() || until.Before(retry.rejectionUntil):
			retry.rejectionUntil = until
		}
	}
	var rpcErr *aistudio.RPCError
	buildRejected := lease.Channel() == aistudio.ChannelBuild && service.pool.PlaygroundEnabled() && requestCtx.Err() == nil &&
		errors.As(err, &rpcErr) && rpcErr.StatusCode == http.StatusBadRequest && rpcErr.Code == 3 && !retry.buildOnly &&
		!aistudio.BuildGenerationDisabled(err)
	if buildRejected {
		retry.buildRejection = &rejectedBuildAttempt{
			accountID: accountID, generation: lease.ModelAccessGeneration(), checkedAt: lease.CheckedAt(), err: err,
		}
		playground := attempt.selection
		playground.PlaygroundOnly = true
		if service.pool.AccountChannelAvailable(accountID, playground) {
			retry.recoveryAccountID = accountID
		}
		delete(retry.attempted, accountID)
		retry.maxAttempts++
		retryable = true
	}
	if missingInlineMedia {
		retry.missingInlineMedia = true
		retry.recoveryAccountID = accountID
		delete(retry.attempted, accountID)
		retry.maxAttempts++
		retryable = true
	}
	// coolingDown 表示租约账户在发送前进入冷却、请求没有发往上游，该账户不计为已尝试
	coolingDown := errors.Is(err, aistudio.ErrAccountCoolingDown)
	if coolingDown {
		delete(retry.attempted, accountID)
		retry.maxAttempts++
	}
	if !quotaFailure && !buildRejected && !coolingDown {
		retry.attemptFailure = err
	}
	if releaseErr := lease.Release(); releaseErr != nil {
		return attemptOutcome{err: errors.Join(err, releaseErr), accountID: accountID, accountLabel: accountLabel}
	}
	service.workers.spareForCooling(requestCtx, attempt.selection, accountID, cooledUntil)
	recoverWorker := false
	// 上游以额度、权限拒绝或服务受限拒绝账户时不重建 Worker 重放，直接换号
	if requestCtx.Err() == nil && mayRecover && !quotaFailure && !rejected && denial == nil {
		var resetErr error
		recoverWorker, _, resetErr = service.recoverWorkerOnce(
			requestCtx, accountID, workerGeneration, retry.recoveredWorker,
			needsWAARuntimeRecovery(err, workerFailed, workerReplaced) || protectedTransport,
			workerFailed || waaRuntimeFailed,
		)
		if resetErr != nil {
			err = errors.Join(err, resetErr)
			retryable = false
		}
	}
	if retryable && !quotaFailure && !buildRejected && !missingInlineMedia && !recoverWorker && !coolingDown {
		retry.failures++
		retryable = int64(retry.failures) <= service.maxAccountSwitches.Load()
	}
	return attemptOutcome{err: err, retryable: retryable, recoverWorker: recoverWorker, accountID: accountID, accountLabel: accountLabel}
}

// rejectedBuildAttempt 表示 Build 以参数错误拒绝、改由 Playground 重试的尝试
type rejectedBuildAttempt struct {
	accountID  string
	generation uint64
	checkedAt  time.Time
	err        error
}

// coolRejectedBuild 在 Playground 对被 Build 拒绝的请求返回首个事件后递增冷却被拒账户该模型的 Build 通道
func (service *trackedService) coolRejectedBuild(requestID string, rejection *rejectedBuildAttempt, modelID string, accountLabel string) {
	cooldown, ok := aistudio.BuildRejectionCooldown(rejection.err)
	if !ok {
		return
	}
	scope := aistudio.ChannelCooldownScope(aistudio.ChannelBuild, modelID)
	until, changed, err := service.pool.MarkQuotaCooldownIfGeneration(rejection.accountID, scope, rejection.generation, rejection.checkedAt, cooldown)
	if err != nil {
		service.requests.logRequestProgress(requestID, accountLabel, "WARN", "账号冷却失败", "kind", cooldown.Kind, "scope", scope, "error", err)
		return
	}
	if !changed {
		return
	}
	service.requests.logRequestProgress(requestID, accountLabel, "WARN", "账号冷却",
		"kind", cooldown.Kind, "scope", scope, "until", until.Local().Format(time.RFC3339))
}

var errStreamClosedBeforeFirstEvent = errors.New("AI Studio stream closed before first event")
var errStreamClosedBeforeFinish = errors.New("AI Studio stream closed before finish")

// firstGenerateEvent 等待首事件并在请求期限内完成错误流清理，等待达到 streamStallThreshold 后每隔 waitReportInterval 以已等待时长调用 onWait
func firstGenerateEvent(ctx context.Context, source <-chan aistudio.Event, onWait func(time.Duration)) (aistudio.Event, error) {
	startedAt := time.Now()
	timer := time.NewTimer(streamStallThreshold)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-source:
			if !ok {
				return aistudio.Event{}, errStreamClosedBeforeFirstEvent
			}
			if event.Kind != aistudio.EventError {
				return event, nil
			}
			for {
				select {
				case _, ok := <-source:
					if !ok {
						return aistudio.Event{}, event.Err
					}
				case <-ctx.Done():
					return aistudio.Event{}, ctx.Err()
				}
			}
		case <-timer.C:
			onWait(time.Since(startedAt).Round(time.Second))
			timer.Reset(waitReportInterval)
		case <-ctx.Done():
			return aistudio.Event{}, ctx.Err()
		}
	}
}

// collectGenerateEvents 收齐非流式请求首事件之后的上游事件，每收到一个事件以到达时刻调用 onEvent，上游流中断时返回该错误；收集达到 streamStallThreshold 后每隔 waitReportInterval 以已等待时长与已收到的事件调用 onWait
func collectGenerateEvents(ctx context.Context, source <-chan aistudio.Event, onEvent func(time.Time), onWait func(time.Duration, []aistudio.Event)) (<-chan aistudio.Event, error) {
	startedAt := time.Now()
	timer := time.NewTimer(streamStallThreshold)
	defer timer.Stop()
	var events []aistudio.Event
	for {
		select {
		case event, ok := <-source:
			if !ok {
				replay := make(chan aistudio.Event, len(events))
				for _, event := range events {
					replay <- event
				}
				close(replay)
				return replay, nil
			}
			var interrupted *aistudio.StreamInterruptedError
			if event.Kind == aistudio.EventError && errors.As(event.Err, &interrupted) {
				for range source {
				}
				return nil, event.Err
			}
			onEvent(time.Now())
			events = append(events, event)
		case <-timer.C:
			onWait(time.Since(startedAt).Round(time.Second), events)
			timer.Reset(waitReportInterval)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func retryableGenerateAccountError(ctx context.Context, err error) bool {
	if errors.Is(err, errStreamClosedBeforeFirstEvent) || errors.Is(err, aistudio.ErrAccountCoolingDown) {
		return true
	}
	var interrupted *aistudio.StreamInterruptedError
	if errors.As(err, &interrupted) {
		return ctx.Err() == nil
	}
	var workerInitError *accountWorkerInitError
	if errors.As(err, &workerInitError) {
		return ctx.Err() == nil
	}
	if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var transportError *url.Error
	if errors.As(err, &transportError) {
		return ctx.Err() == nil
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

// dataRequestContext 返回绑定数据面生命周期、期限为 REQUEST_TIMEOUT 的请求上下文，自动续写每开始一轮期限重新计时
func (service *trackedService) dataRequestContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	service.lifecycleMu.Lock()
	if service.state.Load() != serviceRunning || service.dataContext == nil {
		service.lifecycleMu.Unlock()
		return nil, nil, service.stoppedError()
	}
	deadline, cancel := newRoundDeadlineContext(ctx, service.timeout)
	run := service.dataContext
	stopData := context.AfterFunc(run, cancel)
	service.lifecycleMu.Unlock()
	return aistudio.ContextWithServiceRun(aistudio.ContextWithContinuationObserver(deadline, deadline.extend), run), func() {
		stopData()
		cancel()
	}, nil
}

// endedRequestError 在请求所属的生成服务已停止时把非空的 err 换成生成服务已停止的错误，其余原样返回 err
func endedRequestError(ctx context.Context, err error) error {
	if err != nil && aistudio.ServiceStopped(ctx) {
		return &serviceStoppedError{}
	}
	return err
}

// roundDeadlineContext 是期限可在自动续写每轮开始时重新计时的请求上下文，到期时 Err 为 context.DeadlineExceeded
type roundDeadlineContext struct {
	parent   context.Context
	timeout  time.Duration
	done     chan struct{}
	mu       sync.Mutex
	timer    *time.Timer
	stop     func() bool
	deadline time.Time
	err      error
}

// newRoundDeadlineContext 返回期限为 timeout、随 parent 结束的请求上下文
func newRoundDeadlineContext(parent context.Context, timeout time.Duration) (*roundDeadlineContext, context.CancelFunc) {
	ctx := &roundDeadlineContext{parent: parent, timeout: timeout, done: make(chan struct{}), deadline: time.Now().Add(timeout)}
	ctx.mu.Lock()
	ctx.timer = time.AfterFunc(timeout, func() { ctx.finish(context.DeadlineExceeded) })
	ctx.stop = context.AfterFunc(parent, func() { ctx.finish(parent.Err()) })
	ctx.mu.Unlock()
	return ctx, func() { ctx.finish(context.Canceled) }
}

func (ctx *roundDeadlineContext) finish(err error) {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if ctx.err != nil {
		return
	}
	ctx.err = err
	ctx.timer.Stop()
	ctx.stop()
	close(ctx.done)
}

// extend 把期限重新计为 timeout
func (ctx *roundDeadlineContext) extend() {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if ctx.err == nil {
		ctx.deadline = time.Now().Add(ctx.timeout)
		ctx.timer.Reset(ctx.timeout)
	}
}

func (ctx *roundDeadlineContext) Deadline() (time.Time, bool) {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	return ctx.deadline, true
}

func (ctx *roundDeadlineContext) Done() <-chan struct{} {
	return ctx.done
}

func (ctx *roundDeadlineContext) Err() error {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	return ctx.err
}

func (ctx *roundDeadlineContext) Value(key any) any {
	return ctx.parent.Value(key)
}

func (service *trackedService) forwardEvents(
	clientCtx context.Context,
	requestCtx context.Context,
	cancel context.CancelFunc,
	requestID string,
	first aistudio.Event,
	source <-chan aistudio.Event,
	destination chan<- aistudio.Event,
	serving *atomic.Pointer[aistudio.AccountLease],
	temporaryCopies *aistudio.TemporaryFileCopies,
	activity *upstreamActivity,
	requestedModelID string,
) {
	state := "completed"
	var requestErr error
	terminal := false
	accountLabel := func() string { return serving.Load().Account().Config.Label }
	modelID := strings.TrimPrefix(strings.TrimSpace(first.ProviderModel), "models/")
	var lastEventAt time.Time
	lastEventKind := "-"
	reasoningEvents := 0
	contentEvents := 0
	var usage *aistudio.Usage
	toolCalls := 0
	stalled := false
	stallTimer := time.NewTimer(streamStallThreshold)
	stallTimer.Stop()
	defer stallTimer.Stop()
	var stall <-chan time.Time
	finishFromContext := func() {
		requestErr = endedRequestError(requestCtx, requestCtx.Err())
		state = finalRequestState(requestErr)
		if requestErr == nil || terminal || clientCtx.Err() != nil {
			return
		}
		select {
		case destination <- aistudio.Event{Kind: aistudio.EventError, Err: requestErr}:
		case <-clientCtx.Done():
		}
	}
	defer cancel()
	defer func() {
		api.SetAccessLogUpstreamBytes(requestCtx, activity.bytes.Load())
		api.SetAccessLogGenerationResult(requestCtx, usage, toolCalls)
		if temporaryCopies != nil {
			release, label := serving.Load().Retain(), accountLabel()
			// cleanup 删除临时 Drive 副本后归还保留的租约，有副本时在后台运行，下游事件通道不等删除
			cleanup := func() {
				if err := temporaryCopies.Cleanup(); err != nil {
					service.requests.log(label, "WARN", "临时文件清理失败", "error", err)
				}
				if err := release(); err != nil {
					service.requests.log(label, "WARN", "账户租约释放失败", "error", err)
				}
			}
			if temporaryCopies.Count() > 0 {
				go cleanup()
			} else {
				cleanup()
			}
		}
		if err := serving.Load().Release(); err != nil {
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
		service.requests.finish(requestID, state)
		close(destination)
	}()
	pendingFirst := true
	verified := false
	// frameOutput 是最近一帧累计的思考与回复 token 数，变小表示上游从头重新生成
	var frameOutput int64
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
			case <-stall:
				stalled = true
				stallTimer.Reset(waitReportInterval)
				service.requests.logRequestProgress(requestID, accountLabel(), "INFO", "等待模型继续输出",
					append([]any{"model", modelID, "waited", time.Since(lastEventAt).Round(time.Second), "last_event", lastEventKind,
						"reasoning_events", reasoningEvents, "text_events", contentEvents}, activity.logFields(time.Now())...)...)
				continue
			case <-requestCtx.Done():
				finishFromContext()
				return
			}
		}
		if !ok {
			if err := requestCtx.Err(); err != nil {
				finishFromContext()
			} else if !terminal {
				requestErr = errStreamClosedBeforeFinish
				state = "failed"
				select {
				case destination <- aistudio.Event{Kind: aistudio.EventError, Err: requestErr}:
				case <-clientCtx.Done():
				}
			}
			return
		}
		now := time.Now()
		if stalled {
			service.requests.logRequestProgress(requestID, accountLabel(), "INFO", "模型继续输出",
				"model", modelID, "waited", now.Sub(lastEventAt), "current_event", event.Kind)
			stalled = false
		}
		lastEventAt = now
		service.requests.markData(requestID, now)
		lastEventKind = string(event.Kind)
		if frame := event.FrameUsage; frame != nil {
			output := frame.OutputTokens + frame.ReasoningTokens
			if output < frameOutput {
				api.ResetAccessLogFirstOutput(requestCtx)
			}
			frameOutput = output
		}
		switch event.Kind {
		case aistudio.EventReasoning:
			reasoningEvents++
		case aistudio.EventText:
			contentEvents++
		case aistudio.EventUsage:
			usage = event.Usage
		case aistudio.EventToolCall:
			toolCalls++
		}
		api.SetAccessLogTarget(requestCtx, event.ProviderModel, accountLabel())
		if terminal {
			continue
		}
		if event.Kind == aistudio.EventError {
			event.Err = endedRequestError(requestCtx, event.Err)
			event.Err = service.settleStreamError(requestCtx, requestID, requestedModelID, serving.Load(), event.Err)
			requestErr = event.Err
			state = finalRequestState(event.Err)
			terminal = true
		}
		if event.Kind == aistudio.EventFinish {
			api.SetAccessLogFinishReason(requestCtx, cmp.Or(trailingFinishReason(event.TrailingError), event.FinishReason))
			if event.TrailingError != nil {
				api.SetAccessLogTrailingError(requestCtx, event.TrailingError)
				service.requests.logRequestProgress(requestID, accountLabel(), "WARN", "函数调用之后上游结束生成", "error", event.TrailingError)
			}
			state = "completed"
			terminal = true
		}
		if terminal {
			stall = nil
		} else {
			stallTimer.Reset(streamStallThreshold)
			stall = stallTimer.C
		}
		select {
		case destination <- event:
		case <-requestCtx.Done():
			finishFromContext()
			return
		}
		if !verified && event.Kind == aistudio.EventFinish {
			verified = true
			lease := serving.Load()
			service.markModelAccessVerifiedAsync(
				lease.Account().ID, lease.Account().Config.Label, lease.CooldownScope(requestedModelID), lease.ModelAccessGeneration(), lease.CheckedAt(),
			)
		}
	}
}

// settleStreamError 按首个事件之前的规则处理首个事件之后的上游错误：认证失败时确认账户会话，服务受限时标记账户，权限拒绝时冷却账户，403 协议错误码 7 与 401 改为账户暂时不可用的 503
func (service *trackedService) settleStreamError(requestCtx context.Context, requestID string, modelID string, lease *aistudio.AccountLease, err error) error {
	if aistudio.DefinitiveAuthenticationFailure(err) {
		if stateErr := lease.ConfirmAuthenticationFailure(requestCtx, err); stateErr != nil {
			return errors.Join(err, stateErr)
		}
	}
	if denial := aistudio.RestrictionDenial(err); denial != nil {
		if stateErr := service.pool.MarkRestricted(lease.Account().ID, denial); stateErr != nil {
			return errors.Join(err, stateErr)
		}
	}
	var until time.Time
	if rejection, rejected := aistudio.AccountRejectionCooldown(err); rejected {
		var stateErr error
		if until, stateErr = service.markQuotaCooldown(requestID, modelID, lease, rejection); stateErr != nil {
			return errors.Join(err, stateErr)
		}
	}
	return accountsUnavailable(err, until)
}

// trailingFinishReason 返回函数调用之后上游结束生成的内容策略原因，其他错误返回空字符串
func trailingFinishReason(err error) string {
	var policy *aistudio.ContentPolicyError
	if errors.As(err, &policy) {
		return policy.Reason
	}
	return ""
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
