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
	requests.log("service", "INFO", "运行时装配 | 1/3 | 载入账户")
	store := aistudio.NewAccountStore(strings.Split(cfg.AuthStates, ",")...)
	accounts, err := store.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := launchCtx.Err(); err != nil {
		return nil, nil, nil, err
	}
	egress := "直连"
	proxyList := config.ProxyList(cfg.Proxy)
	if len(proxyList) == 0 {
		if system := systemProxy(); system != "" {
			proxyList = []string{system}
			egress = redactedProxy(system) + " | 来源=系统代理"
		}
	} else {
		redacted := make([]string, 0, len(proxyList))
		for _, proxy := range proxyList {
			redacted = append(redacted, redactedProxy(proxy))
		}
		egress = strings.Join(redacted, ",")
	}
	globalProxy := ""
	if len(proxyList) > 0 {
		globalProxy = proxyList[0]
	}
	cfg.Proxy = strings.Join(proxyList, ",")
	camoufoxPath, login := prepareWAABackend(cfg, requests, len(accounts), globalProxy)
	requests.log("service", "INFO", "运行时装配 | 3/3 | 创建协议客户端 | 出口="+egress)
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
	service.maxAccountSwitches = cfg.MaxAccountSwitches
	service.discoverHiddenModels = cfg.DiscoverHiddenModels
	service.modelMapping, _ = config.ParseModelMapping(cfg.ModelMapping)
	if camoufoxPath != "" {
		service.camoufoxProxy, service.camoufoxWait = globalProxy, make(chan struct{})
	}
	admin := newRuntimeAdmin(lifecycle, pool, store, service, requests, login, workers, cfg)
	admin.proxies, admin.globalProxy = proxies, globalProxy
	service.proxies, service.rebindAccounts = proxies, admin.rebindAccounts
	workers.proxyFailed = service.reportProxyFailure
	probeCtx, stopProbe := context.WithCancel(lifecycle)
	if len(proxyList) > 1 {
		go admin.probeProxies(probeCtx)
	}
	requests.log("service", "INFO", fmt.Sprintf(
		"协议运行时就绪 | 账户=%d | 耗时=%s",
		len(accounts), time.Since(startedAt).Round(time.Millisecond),
	))
	closeRuntime := func() error {
		stopProbe()
		err := workers.Close()
		transport.CloseIdleConnections()
		return err
	}
	return service, admin, closeRuntime, nil
}

// trackedService 跟踪生成请求及其唯一账户租约
type trackedService struct {
	buildNativeNonstream bool
	modelMapping         []config.ModelAlias
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
	// catalogRoundDone 在待开始或进行中的一轮目录读取读完第一个账户时关闭，没有这样的一轮时为 nil
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
	// maxAccountSwitches 是一个请求因非额度失败换号的次数上限
	maxAccountSwitches int
	// discoverHiddenModels 表示同步账户目录时探测官网隐藏模型
	discoverHiddenModels bool
	// camoufoxWait 在 Camoufox 后端的启动开始等待 Camoufox 下载时关闭，SpiderMonkey 后端为 nil
	camoufoxWait chan struct{}
	// camoufoxProxy 是 Camoufox 后端下载 Camoufox 的出口代理
	camoufoxProxy string
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
	return &trackedService{
		lifecycle: lifecycle, service: service, catalog: catalog, pool: pool, requests: requests, workers: workers,
		timeout: timeout, modelRetries: make(map[string]struct{}), catalogRoundWake: make(chan struct{}, 1),
		maxAccountSwitches: config.DefaultMaxAccountSwitches,
	}
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
	if accounts, _ := service.pool.EnabledAccounts(); len(accounts) == 0 {
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
	service.requests.log("service", "INFO", "生成服务启动 | 等待 Camoufox 下载")
	if _, err := camoufoxnative.Prepare(ctx, service.camoufoxProxy); err != nil {
		return err
	}
	service.requests.log("service", "INFO", fmt.Sprintf("生成服务启动 | Camoufox 已就绪 | 耗时=%s", time.Since(startedAt).Round(time.Millisecond)))
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
	stopCaller := context.AfterFunc(ctx, dataCancel)
	service.replaceModelSnapshot(service.catalog.CachedModels())
	launching()
	service.probeProxyPool(dataContext)
	if err := service.prepareCamoufox(dataContext); err != nil {
		stopCaller()
		return nil, false, service.finishLaunch(transitionDone, dataCancel, nil, err)
	}
	models := service.modelSnapshot()
	service.requests.log("service", "INFO", fmt.Sprintf(
		"生成服务启动 | 1/2 | 准备模型目录 | 缓存=%d", len(models),
	))
	cached := len(models) > 0
	var catalogSynced, catalogDone <-chan struct{}
	if !cached {
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
	service.requests.log("service", "INFO", fmt.Sprintf(
		"生成服务启动 | 2/2 | 预热 WAA Worker | 模型=%d | 目标=%d",
		len(models), service.workers.PrewarmTarget(),
	))
	synced, warmErr := service.waitFirstWarm(dataContext, catalogSynced)
	if cached {
		_, catalogSynced, catalogDone = service.startLaunchCatalogRefresh(dataContext)
	}
	if warmErr != nil && !synced && dataContext.Err() == nil {
		select {
		case <-dataContext.Done():
		case <-catalogSynced:
			_, warmErr = service.waitFirstWarm(dataContext, catalogSynced)
		}
	}
	if dataContext.Err() != nil {
		stopCaller()
		return nil, false, service.finishLaunch(transitionDone, dataCancel, catalogDone, dataContext.Err())
	}
	if warmErr != nil {
		stopCaller()
		return nil, false, service.finishLaunch(transitionDone, dataCancel, catalogDone, warmErr)
	}
	if !stopCaller() {
		return nil, false, service.finishLaunch(transitionDone, dataCancel, catalogDone, ctx.Err())
	}
	models, err := service.finishModelLaunch(dataContext, transitionDone)
	if err != nil {
		return models, false, service.finishLaunch(transitionDone, dataCancel, catalogDone, err)
	}
	return models, true, nil
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

// waitFirstWarm 启动预热并等待首个就绪 Worker，同时返回结束时首轮目录同步是否已完成
func (service *trackedService) waitFirstWarm(ctx context.Context, catalogSynced <-chan struct{}) (bool, error) {
	firstWarm := service.workers.StartPrewarm(ctx)
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
	refreshErr := waitServiceTransition(modelRefreshDone, modelCatalogShutdownTimeout, "模型目录刷新停止")
	service.workers.waitPrewarm()
	resetErr := service.workers.ResetAll()
	service.lifecycleMu.Lock()
	if service.transitionDone == transitionDone {
		service.state.Store(serviceStopped)
		service.dataContext = nil
		service.dataCancel = nil
		service.modelRefreshDone = nil
		service.transitionErr = errors.Join(refreshErr, resetErr)
		service.transitionDone = nil
		close(transitionDone)
	}
	service.lifecycleMu.Unlock()
	return errors.Join(launchErr, refreshErr, resetErr)
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
			service.requests.log(accountLabel, "ERROR", fmt.Sprintf(
				"模型资格保存失败 | 模型=%s | 错误=%s", modelID, strings.TrimSpace(err.Error()),
			))
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
		service.lifecycleMu.Unlock()
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
	modelRefreshDone := service.modelRefreshDone
	service.lifecycleMu.Unlock()
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
	refreshErr := waitServiceTransition(modelRefreshDone, modelCatalogShutdownTimeout, "模型目录刷新停止")
	service.lifecycleMu.Lock()
	if service.transitionDone == transitionDone {
		service.transitionErr = refreshErr
	}
	service.lifecycleMu.Unlock()
	service.workers.waitPrewarm()
	resetErr := service.workers.ResetAll()
	service.lifecycleMu.Lock()
	if service.transitionDone == transitionDone {
		service.dataContext = nil
		service.dataCancel = nil
		service.modelRefreshDone = nil
		service.transitionErr = errors.Join(refreshErr, resetErr)
		service.transitionDone = nil
		close(transitionDone)
	}
	service.lifecycleMu.Unlock()
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
	for _, alias := range service.modelMapping {
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
	for _, alias := range service.modelMapping {
		if alias.Alias == trimmed {
			return alias.Model
		}
	}
	open := strings.LastIndex(trimmed, "(")
	if open <= 0 || !strings.HasSuffix(trimmed, ")") {
		return name
	}
	base := strings.TrimSpace(trimmed[:open])
	for _, alias := range service.modelMapping {
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
		if ctx.Err() != nil {
			return
		}
		if !maps.Equal(service.authRequiredAccountIDs(), authRequiredBefore) {
			service.publishModelAccess()
		}
		service.requests.log("service", "INFO", fmt.Sprintf(
			"模型目录后台同步完成 | 沿用缓存=%d | 同步=%d | 非空=%d | 模型=%d | 待重试账户=%d | 耗时=%s",
			cached, synchronized, refreshed, len(service.modelSnapshot()), service.pendingModelRetryCount(), time.Since(startedAt).Round(time.Millisecond),
		))
		service.retryModelCatalogs(ctx, startedAt)
	}()
	return ready, synced, done
}

// modelCatalogAccountIDs 返回读取目录的启用账户，权益高的在前，同权益按 ID 排列
func (service *trackedService) modelCatalogAccountIDs() []string {
	statuses := service.pool.Status()
	accountIDs := make([]string, 0, len(statuses))
	for _, status := range statuses {
		if status.Enabled && (status.State == aistudio.AccountReady || status.State == aistudio.AccountBusy) {
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
			service.requests.log(accountID, "WARN", "模型目录同步失败 | 错误="+err.Error())
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

// ApplyCapacity 让容量设置作用于后续调度，常驻 Worker 数调高时补足预热
// SetAPIKeyAccounts 设置各 API key 可用的账户 ID
func (service *trackedService) SetAPIKeyAccounts(accounts map[string][]string) {
	service.pool.SetAPIKeyAccounts(accounts)
}

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
		service.requests.log("service", "WARN", fmt.Sprintf("到期资源删除失败: %v", err))
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
		service.refreshAccountStates(ctx, func() {
			service.catalogRoundMu.Lock()
			service.catalogRoundDone = nil
			service.catalogRoundMu.Unlock()
			close(done)
		})
		timer.Reset(time.Until(round.Add(accountStateRefreshInterval)))
	}
}

// catalogModelID 返回请求模型在实时目录中的正式 ID；目录中没有该模型时先等 awaitCatalogRound 再查找
func (service *trackedService) catalogModelID(ctx context.Context, model string) string {
	if service.pool.MissingModel(model) {
		service.awaitCatalogRound(ctx)
	}
	return service.pool.CanonicalModelID(model)
}

// awaitCatalogRound 等待进行中的一轮启用账户目录读取读完第一个账户；没有进行中的一轮且距上一轮开始已超过 10 分钟时让巡检循环立即开始一轮并等它读完第一个账户
func (service *trackedService) awaitCatalogRound(ctx context.Context) {
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
	select {
	case <-ctx.Done():
	case <-done:
	}
}

// refreshAccountStates 逐个重新载入启用账户的页面读取权益与模型目录，没有空闲槽位的账户跳过本轮；first 在第一个账户读完或本轮结束时调用一次
func (service *trackedService) refreshAccountStates(ctx context.Context, first func()) {
	first = sync.OnceFunc(first)
	defer first()
	for _, accountID := range service.modelCatalogAccountIDs() {
		if ctx.Err() != nil {
			return
		}
		_, _ = service.refreshAccountState(ctx, accountID, false)
		first()
	}
}

// refreshAccountState 重新载入账户页面读取权益与模型目录，失败或变化时发布账户与模型事件并记录日志
func (service *trackedService) refreshAccountState(ctx context.Context, accountID string, wait bool) (aistudio.AccountRefresh, error) {
	requestCtx, cancel := service.lifecycleRequestContext(ctx)
	defer cancel()
	refresh, err := service.catalog.RefreshAccountState(requestCtx, accountID, wait)
	if ctx.Err() != nil || refresh.Skipped {
		return refresh, err
	}
	if err != nil {
		service.publishModelAccess()
		service.requests.log(accountID, "WARN", "账户状态刷新失败 | 错误="+err.Error())
		return refresh, err
	}
	if refresh.PreviousTier == refresh.Tier && !refresh.ModelsChanged {
		return refresh, nil
	}
	service.modelSyncMu.Lock()
	service.applyCachedModelCatalog()
	service.modelSyncMu.Unlock()
	service.publishModelAccess()
	service.requests.log(accountID, "INFO", fmt.Sprintf(
		"账户状态已更新 | 权益=%s→%s | 模型=%d→%d", refresh.PreviousTier, refresh.Tier, refresh.PreviousModels, refresh.Models,
	))
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
			service.requests.log(result.accountID, "INFO", fmt.Sprintf("模型目录已更新 | 模型=%d", len(result.models)))
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
	observed := aistudio.ContextWithAccountSelectionObserver(requestCtx, func(account *aistudio.Account) {
		api.SetAccessLogTarget(requestCtx, model, account.Config.Label)
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
	return service.service.(aistudio.TitleService).GenerateTitle(requestCtx, request)
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
	api.SetAccessLogError(requestCtx, requestErr)
	return images, requestErr
}

// CreateDecision 回答决策问题并登记请求目标与账户，每次请求在运行日志中提醒官网前端尚未调用该模型
func (service *trackedService) CreateDecision(ctx context.Context, request aistudio.DecisionRequest) (aistudio.DecisionResult, error) {
	request.Model = service.catalogModelID(ctx, service.mappedModel(request.Model))
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, request.Model)
	if err != nil {
		return aistudio.DecisionResult{}, err
	}
	defer cancel()
	result, requestErr := service.service.(aistudio.DecisionService).CreateDecision(requestCtx, request)
	api.SetAccessLogError(requestCtx, requestErr)
	service.requests.log("service", "WARN", fmt.Sprintf("Decision 请求 | 模型=%s | 官网前端尚未调用该模型", request.Model))
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
		api.SetAccessLogError(requestCtx, err)
		return aistudio.VideoOperation{}, err
	}
	requestCtx = aistudio.ContextWithAccountSelectionObserver(requestCtx, func(account *aistudio.Account) {
		api.SetAccessLogTarget(requestCtx, request.Model, account.Config.Label)
	})
	request.TaskContext = func() (context.Context, context.CancelFunc, error) {
		return service.dataRequestContext(context.Background())
	}
	operation, requestErr := service.service.(aistudio.VideoService).GenerateVideo(requestCtx, request)
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
	api.SetAccessLogError(requestCtx, requestErr)
	if requestErr != nil {
		cancel()
		return aistudio.MediaStream{}, requestErr
	}
	media.Body = &trackedMediaReadCloser{body: media.Body, cancel: cancel}
	return media, requestErr
}

type trackedMediaReadCloser struct {
	body   io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
	err    error
}

func (closer *trackedMediaReadCloser) Read(target []byte) (int, error) {
	return closer.body.Read(target)
}

func (closer *trackedMediaReadCloser) Close() error {
	closer.once.Do(func() {
		closer.err = closer.body.Close()
		closer.cancel()
	})
	return closer.err
}

// acquireWarmLease 取得 Worker 已就绪的账户租约，全部账户冷却时在上限内排队等待恢复，最早恢复的是权限拒绝冷却时不排队；quotaFallback 为 true 时排队超过上限改用套餐限额或每日限额冷却中的账户；冷 Worker 启动失败后换到其他账户计入 failures，超过换号上限时返回启动失败
func (service *trackedService) acquireWarmLease(ctx context.Context, selection aistudio.AccountSelection, quotaFallback bool, failures *int) (*aistudio.AccountLease, error) {
	fixedAccount := strings.TrimSpace(selection.AccountID) != "" || strings.TrimSpace(selection.ResourceID) != ""
	failedWorkers := make(map[string]struct{})
	var promoteFailure error
	var waiter *dispatchWaiter
	defer func() { service.workers.dispatch.leave(waiter) }()
	wait := func(delay time.Duration) error {
		if waiter == nil {
			waiter = service.workers.dispatch.join(dispatchKey(selection))
		}
		return waitScheduling(ctx, waiter.wake, delay)
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		warm := service.workers.ReadyWarmAccountIDs()
		active := service.workers.WarmAccountIDs()
		groups, err := service.pool.ClassifyCandidates(ctx, selection, warm)
		if err != nil {
			return nil, err
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
		standbyReady, opening := service.workers.withoutOpening(groups.StandbyReady)
		groups.StandbyReady = standbyReady
		warmAvailable := append(append([]string(nil), groups.WarmReady...), groups.WarmAvailable...)
		if len(warmAvailable) > 0 {
			candidate := selection
			candidate.AllowedAccountIDs = warmAvailable
			lease, _, acquireErr := service.pool.TryAcquireFor(ctx, candidate)
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
		if len(groups.StandbyReady) > 0 && (len(active) < service.workers.activeLimit() || coolingVictim) && warmCandidates > 0 && !fixedAccount {
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
			warmCandidates == 0 && service.workers.idleWarmVictim("") != "") {
			standby := groups.StandbyReady
			if len(active) < service.workers.activeLimit() {
				if cold := service.workers.coldAccounts(standby); len(cold) > 0 {
					standby = cold
				}
			}
			accountID := standby[0]
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
			_, promoteErr := service.workers.ensureWorker(aistudio.ContextWithAccountLease(ctx, lease), accountID, selection.ModelID, false, false)
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
			if *failures++; *failures > service.maxAccountSwitches {
				return nil, promotionFailure(service.pool, selection, promoteErr)
			}
			continue
		}
		if len(groups.WarmBusy) > 0 || len(groups.StandbyBusy) > 0 || opening {
			if err := wait(schedulingRecheck); err != nil {
				return nil, err
			}
			continue
		}
		if !groups.EarliestCooldown.Until.IsZero() {
			remaining := time.Until(groups.EarliestCooldown.Until)
			if remaining > cooldownQueueLimit || strings.HasPrefix(groups.EarliestCooldown.Reason, aistudio.AccountRejectionKind) {
				if quotaFallback && !selection.IgnoreQuotaCooldown {
					selection.IgnoreQuotaCooldown = true
					continue
				}
				return nil, &aistudio.AllCoolingError{ModelID: selection.ModelID, Until: groups.EarliestCooldown.Until, Reason: groups.EarliestCooldown.Reason}
			}
			if err := wait(min(remaining, schedulingRecheck)); err != nil {
				return nil, err
			}
			continue
		}
		if candidates > 0 {
			if err := wait(schedulingRecheck); err != nil {
				return nil, err
			}
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

// cooldownQueueLimit 为全部候选账户冷却时请求排队等待恢复的上限，覆盖分钟限额窗口
const cooldownQueueLimit = time.Minute

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
	waa := timing.waa
	responseHeader := timing.responseHeader
	elapsed := now.Sub(timing.phaseStarted)
	switch timing.phase {
	case aistudio.RequestPhasePreparingWAA:
		waa += elapsed
	case aistudio.RequestPhaseSendingUpstream:
		responseHeader += elapsed
	}
	current := "流已建立"
	switch timing.phase {
	case aistudio.RequestPhasePreparingWAA:
		current = "WAA proof"
	case aistudio.RequestPhaseSendingUpstream:
		current = "等待上游响应头"
	}
	return current, waa, responseHeader
}

func (timing *requestPreparationTiming) finishPhaseLocked(now time.Time) {
	elapsed := now.Sub(timing.phaseStarted)
	switch timing.phase {
	case aistudio.RequestPhasePreparingWAA:
		timing.waa += elapsed
	case aistudio.RequestPhaseSendingUpstream:
		timing.responseHeader += elapsed
	}
}

func (activity *upstreamActivity) observe(count int) {
	if count <= 0 {
		return
	}
	now := time.Now().UnixNano()
	activity.lastNano.Store(now)
	activity.bytes.Add(int64(count))
}

func (activity *upstreamActivity) logFields(now time.Time) string {
	lastNano := activity.lastNano.Load()
	if lastNano == 0 {
		return "网络字节=0"
	}
	return fmt.Sprintf(
		"网络字节=%d | 最近网络=%s",
		activity.bytes.Load(), now.Sub(time.Unix(0, lastNano)).Round(time.Millisecond),
	)
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
	// rejectionUntil 是本次请求中因上游 403、协议错误码 7 而冷却的账户最早的恢复时刻
	rejectionUntil time.Time
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
		_, eligible := service.pool.EnabledAccounts()
		if eligible > 1 {
			retry.maxAttempts = eligible
		}
	}
	retry.attempted = make(map[string]struct{}, retry.maxAttempts)
	preferredAccountID := service.pool.InlineMediaAccount(request.Contents)
	hedgeable := !request.Background && !service.pool.InteractionAgentModel(modelID) && (service.hedgeAuto || service.hedgeAfter > 0) && slices.Contains(service.hedgeModels, modelID)
	hedged := false
	var winner *generateAttempt
	var activity *upstreamActivity
	var err error
	copyFiles := false
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
		}
		if resourceID != "" || request.Config.SpeechConfig != nil && request.Config.SpeechConfig.Mode != "" {
			selection.PlaygroundOnly = true
		}
		if continuationRounds > 1 && (request.Config.MaxOutputTokens != nil || !aistudio.RequestNeedsBuild(request)) {
			selection.PlaygroundOnly = true
		}
		if !selection.PlaygroundOnly && service.pool.BuildEnabled() && aistudio.RequestNeedsBuild(request) {
			selection.Channel = aistudio.ChannelBuild
		}
		fallbackReason := ""
		if request.Unary {
			switch {
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
			enabled, _ := service.pool.EnabledAccounts()
			selection.AllowedAccountIDs = []string{}
			for _, accountID := range enabled {
				if _, exists := retry.attempted[accountID]; !exists {
					selection.AllowedAccountIDs = append(selection.AllowedAccountIDs, accountID)
				}
			}
		}
		queueStartedAt := time.Now()
		queueWait := startWaitReporter(func(waited time.Duration) {
			service.requests.logRequestProgress(request.ID, "request", "WARN", fmt.Sprintf(
				"排队等待 | 已等待=%s | 模型=%s", waited, modelID,
			))
		})
		quotaFallback := selection.ResourceID == ""
		lease, acquireErr := service.acquireWarmLease(requestCtx, selection, quotaFallback, &retry.failures)
		var buildCooling *aistudio.AllCoolingError
		if acquireErr != nil && selection.Channel == aistudio.ChannelBuild && requestCtx.Err() == nil && !aistudio.RequestBuildOnly(request) &&
			(errors.Is(acquireErr, aistudio.ErrNoEligibleAccount) || errors.As(acquireErr, &buildCooling)) {
			// 没有账户能经 Build 服务时改用 Playground，Playground 无法表达的设置按其最接近的形式发送
			selection.Channel = ""
			fallbackReason = "Build 通道没有可用账户"
			lease, acquireErr = service.acquireWarmLease(requestCtx, selection, quotaFallback, &retry.failures)
		}
		queueDelayed := queueWait.finish()
		if acquireErr == nil && selection.PreferredChannel == aistudio.ChannelBuild && lease.Channel() != aistudio.ChannelBuild {
			fallbackReason = "账户 Build 通道冷却或不支持此模型"
		}
		if acquireErr != nil {
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
			default:
				err = acquireErr
			}
			if recoveringAccount && unbound && requestCtx.Err() == nil {
				retry.attempted[selectionAccountID] = struct{}{}
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
			service.requests.logRequestProgress(request.ID, accountLabel, "INFO", fmt.Sprintf(
				"排队结束 | 等待=%s | 账户=%s", time.Since(queueStartedAt).Round(time.Millisecond), accountLabel,
			))
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
				collected, collectErr := collectGenerateEvents(requestCtx, winner.source, func(waited time.Duration, events []aistudio.Event) {
					reasoning, content := 0, 0
					for _, event := range events {
						switch event.Kind {
						case aistudio.EventReasoning:
							reasoning++
						case aistudio.EventText:
							content++
						}
					}
					service.requests.logRequestProgress(request.ID, winner.accountLabel, "INFO", fmt.Sprintf(
						"收集完整响应 | 已等待=%s | 推理=%d | 正文=%d | 模型=%s | %s",
						waited, reasoning, content, modelID, winner.activity.logFields(time.Now()),
					))
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
			if retry.buildRejection != nil {
				service.coolRejectedBuild(request.ID, retry.buildRejection, modelID, winner.accountLabel)
			}
			break
		}
		err = last.err
		if !last.retryable {
			break
		}
		if last.recoverWorker {
			retry.recoveryAccountID = last.accountID
			delete(retry.attempted, last.accountID)
			retry.maxAttempts++
		}
		if attempt+1 == retry.maxAttempts {
			break
		}
		if last.recoverWorker && errors.Is(err, aistudio.ErrProtectedTransport) {
			service.requests.logRequestProgress(request.ID, last.accountLabel, "WARN", fmt.Sprintf(
				"受保护请求网络错误 | 模型=%s | 同账户重放当前请求\n原因: %s", modelID, strings.TrimSpace(err.Error()),
			))
			continue
		}
		if last.recoverWorker {
			service.requests.logRequestProgress(request.ID, last.accountLabel, "WARN", fmt.Sprintf(
				"WAA Worker 重建 | 模型=%s | 重放当前请求\n原因: %s", modelID, strings.TrimSpace(err.Error()),
			))
			continue
		}
		switchMessage := fmt.Sprintf(
			"账号切换 | 模型=%s\n原因: %s",
			modelID, strings.TrimSpace(err.Error()),
		)
		service.requests.logRequestProgress(request.ID, last.accountLabel, "WARN", switchMessage)
	}
	if winner == nil {
		if activity != nil {
			api.SetAccessLogUpstreamBytes(requestCtx, activity.bytes.Load())
		}
		err = accountsUnavailable(err, retry.rejectionUntil)
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
			service.requests.logRequestProgress(request.ID, accountLabel, "INFO", fmt.Sprintf(
				"内联附件处理完成 | 附件=%d | 原始=%dB | 耗时=%s",
				mediaCount, mediaBytes, time.Since(uploadStartedAt).Round(time.Millisecond),
			))
		}
	}
	prepareStartedAt := time.Now()
	prepareTiming := newRequestPreparationTiming(prepareStartedAt)
	prepareWait := startWaitReporter(func(waited time.Duration) {
		current, _, _ := prepareTiming.snapshot(time.Now())
		service.requests.logRequestProgress(request.ID, accountLabel, "WARN", fmt.Sprintf(
			"请求准备等待 | 已等待=%s | 当前=%s | 模型=%s", waited, current, modelID,
		))
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
		service.requests.log(accountLabel, "INFO", fmt.Sprintf(
			"文件引用复制 | 来源=%s | 目标=%s | 文件=%d",
			strings.Join(sourceLabels, ","), accountLabel, copiedFileCount,
		))
	}
	prepareElapsed := time.Since(prepareStartedAt)
	if prepareWait.finish() {
		_, waa, responseHeader := prepareTiming.snapshot(time.Now())
		service.requests.logRequestProgress(request.ID, accountLabel, "INFO", fmt.Sprintf(
			"请求准备结束 | 等待=%s | WAA=%s | 响应头=%s | 模型=%s",
			prepareElapsed.Round(time.Millisecond), waa.Round(time.Millisecond),
			responseHeader.Round(time.Millisecond), modelID,
		))
	}
	if err == nil {
		upstreamStartedAt := time.Now()
		firstEventDelayed := false
		attempt.first, err = firstGenerateEvent(ctx, attempt.source, func(waited time.Duration) {
			firstEventDelayed = true
			service.requests.logRequestProgress(request.ID, accountLabel, "WARN", fmt.Sprintf(
				"上游首事件等待 | 已等待=%s | 模型=%s | %s",
				waited, modelID, attempt.activity.logFields(time.Now()),
			))
		})
		attempt.firstAt = time.Now()
		if firstEventDelayed && err == nil {
			service.requests.logRequestProgress(request.ID, accountLabel, "INFO", fmt.Sprintf(
				"上游首事件到达 | 等待=%s | 事件=%s | 模型=%s",
				time.Since(upstreamStartedAt).Round(time.Millisecond), attempt.first.Kind, modelID,
			))
		}
	}
	attempt.err = err
}

// upstreamModeObserver 返回把租约账户的上游调用方式记入请求日志的观察函数，回退流式时附上 fallbackReason
func (service *trackedService) upstreamModeObserver(requestID string, lease *aistudio.AccountLease, fallbackReason string) func(string, string, string) {
	accountLabel := lease.Account().Config.Label
	return func(method, mode, reason string) {
		level := "INFO"
		message := fmt.Sprintf("上游调用 | 通道=%s | 模式=%s | RPC=%s", lease.Channel(), mode, method)
		if reason != "" && service.pool.BuildEnabled() {
			level = "WARN"
			if fallbackReason != "" {
				reason = fallbackReason + "; " + reason
			}
			message += " | 回退流式 | 原因=" + reason
		}
		service.requests.logRequestProgress(requestID, accountLabel, level, message)
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
		if attempt.failures++; attempt.failures > service.maxAccountSwitches {
			return nil, aistudio.ErrNoEligibleAccount
		}
	}
	selection := aistudio.AccountSelection{ModelID: modelID, Method: "generateContent", PlaygroundOnly: true, AllowedAccountIDs: []string{}}
	enabled, _ := service.pool.EnabledAccounts()
	for _, accountID := range enabled {
		if _, exists := failed[accountID]; !exists {
			selection.AllowedAccountIDs = append(selection.AllowedAccountIDs, accountID)
		}
	}
	lease, acquireErr := service.acquireWarmLease(ctx, selection, false, &attempt.failures)
	if acquireErr != nil {
		return nil, acquireErr
	}
	attempt.serving.Store(lease)
	service.requests.logRequestProgress(requestID, label, "WARN", fmt.Sprintf(
		"账号切换 | 续写轮 | 模型=%s\n原因: %s", modelID, strings.TrimSpace(err.Error()),
	))
	service.markAttemptAccount(ctx, requestID, modelID, lease)
	next := aistudio.ContextWithAccountLease(ctx, lease)
	return aistudio.ContextWithUpstreamModeObserver(next, service.upstreamModeObserver(requestID, lease, "")), nil
}

// markQuotaCooldown 按额度失败冷却租约账户在该模型或全局范围上的调度并记入请求日志，返回实际生效的恢复时刻
func (service *trackedService) markQuotaCooldown(requestID, modelID string, lease *aistudio.AccountLease, cooldown aistudio.QuotaCooldown) (time.Time, error) {
	scope := lease.CooldownScope(modelID)
	scopeLabel := scope
	if cooldown.Global {
		scope, scopeLabel = "", "全局"
	}
	until, err := service.pool.MarkQuotaCooldownIfGeneration(lease.Account().ID, scope, lease.ModelAccessGeneration(), lease.CheckedAt(), cooldown)
	if err == nil && !until.IsZero() {
		service.requests.logRequestProgress(requestID, lease.Account().Config.Label, "WARN", fmt.Sprintf(
			"账号冷却 | 类型=%s | 范围=%s | 恢复=%s", cooldown.Kind, scopeLabel, until.Local().Format(time.RFC3339),
		))
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
	if errors.As(err, &policy) && policy.Usage != nil {
		api.SetAccessLogGenerationResult(requestCtx, policy.Usage, 0)
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
	cooldown, quotaFailure := aistudio.QuotaCooldownForError(err, time.Now())
	if quotaFailure {
		until, stateErr := service.markQuotaCooldown(requestID, modelID, lease, cooldown)
		if stateErr != nil {
			err = errors.Join(err, stateErr)
			retryable = false
		} else {
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
	if denial := aistudio.RestrictionDenial(err); denial != nil {
		if stateErr := service.pool.MarkRestricted(accountID, denial); stateErr != nil {
			err = errors.Join(err, stateErr)
			retryable = false
		}
	}
	if rejection, rejected := aistudio.AccountRejectionCooldown(err); rejected {
		until, stateErr := service.markQuotaCooldown(requestID, modelID, lease, rejection)
		if stateErr != nil {
			err = errors.Join(err, stateErr)
			retryable = false
		} else if !until.IsZero() && (retry.rejectionUntil.IsZero() || until.Before(retry.rejectionUntil)) {
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
	if !quotaFailure && !buildRejected {
		retry.attemptFailure = err
	}
	if releaseErr := lease.Release(); releaseErr != nil {
		return attemptOutcome{err: errors.Join(err, releaseErr), accountID: accountID, accountLabel: accountLabel}
	}
	recoverWorker := false
	if requestCtx.Err() == nil && mayRecover {
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
	if retryable && !quotaFailure && !buildRejected && !missingInlineMedia && !recoverWorker && !errors.Is(err, aistudio.ErrAccountCoolingDown) {
		retry.failures++
		retryable = retry.failures <= service.maxAccountSwitches
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
	until, err := service.pool.MarkQuotaCooldownIfGeneration(rejection.accountID, scope, rejection.generation, rejection.checkedAt, cooldown)
	if err != nil {
		service.requests.logRequestProgress(requestID, accountLabel, "WARN", fmt.Sprintf("账号冷却 | 类型=%s | 范围=%s | 错误=%s", cooldown.Kind, scope, err))
		return
	}
	if until.IsZero() {
		return
	}
	service.requests.logRequestProgress(requestID, accountLabel, "WARN", fmt.Sprintf(
		"账号冷却 | 类型=%s | 范围=%s | 恢复=%s", cooldown.Kind, scope, until.Local().Format(time.RFC3339),
	))
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

// collectGenerateEvents 收齐非流式请求首事件之后的上游事件，上游流中断时返回该错误；收集达到 streamStallThreshold 后每隔 waitReportInterval 以已等待时长与已收到的事件调用 onWait
func collectGenerateEvents(ctx context.Context, source <-chan aistudio.Event, onWait func(time.Duration, []aistudio.Event)) (<-chan aistudio.Event, error) {
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
	stopData := context.AfterFunc(service.dataContext, cancel)
	service.lifecycleMu.Unlock()
	return aistudio.ContextWithContinuationObserver(deadline, deadline.extend), func() {
		stopData()
		cancel()
	}, nil
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
		requestErr = requestCtx.Err()
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
			if err := temporaryCopies.Cleanup(); err != nil {
				service.requests.log(accountLabel(), "WARN", "临时文件清理失败 | 错误="+err.Error())
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
				service.requests.logRequestProgress(requestID, accountLabel(), "WARN", fmt.Sprintf(
					"事件流停顿 | 模型=%s | 已等待=%s | 最近事件=%s | 推理=%d | 正文=%d | %s",
					modelID, time.Since(lastEventAt).Round(time.Second), lastEventKind, reasoningEvents, contentEvents,
					activity.logFields(time.Now()),
				))
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
			service.requests.logRequestProgress(requestID, accountLabel(), "INFO", fmt.Sprintf(
				"事件流恢复 | 模型=%s | 停顿=%s | 当前事件=%s",
				modelID, now.Sub(lastEventAt).Round(time.Millisecond), event.Kind,
			))
			stalled = false
		}
		lastEventAt = now
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
			requestErr = event.Err
			if aistudio.DefinitiveAuthenticationFailure(event.Err) {
				if stateErr := serving.Load().ConfirmAuthenticationFailure(requestCtx, event.Err); stateErr != nil {
					requestErr = errors.Join(requestErr, stateErr)
					event.Err = requestErr
				}
			}
			state = finalRequestState(event.Err)
			terminal = true
		}
		if event.Kind == aistudio.EventFinish {
			api.SetAccessLogFinishReason(requestCtx, cmp.Or(trailingFinishReason(event.TrailingError), event.FinishReason))
			if event.TrailingError != nil {
				api.SetAccessLogTrailingError(requestCtx, event.TrailingError)
				service.requests.logRequestProgress(requestID, accountLabel(), "WARN", fmt.Sprintf("函数调用之后上游结束生成 | 错误=%s", event.TrailingError))
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
