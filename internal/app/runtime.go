package app

import (
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
	camoufoxPath, login, err := prepareWAABackend(launchCtx, cfg, requests, len(accounts), globalProxy)
	if err != nil {
		return nil, nil, nil, err
	}
	requests.log("service", "INFO", "运行时装配 | 3/3 | 创建协议客户端 | 出口="+egress)
	pool := aistudio.NewAccountPool(accounts, cfg.PerAccountConcurrency)
	pool.SetRoutingStrategy(cfg.RoutingStrategy)
	pool.SetUpstreamChannels(upstreamChannels(cfg.UpstreamChannels))
	pool.SetModelChannels(modelChannels(cfg.ModelChannels))
	proxies := aistudio.NewProxyPool(pool, proxyList)
	if len(proxyList) > 1 {
		if _, err := proxies.Probe(launchCtx, checkProxy); err != nil {
			return nil, nil, nil, err
		}
	}
	if _, err := proxies.Assign(); err != nil {
		return nil, nil, nil, err
	}
	headers, err := newAccountHeaderProvider(accounts, globalProxy)
	if err != nil {
		return nil, nil, nil, err
	}
	transport, err := aistudio.NewMakerSuiteHTTPTransport(aistudio.HTTPTransportOptions{
		Pool: pool, Signer: aistudio.NewSigner(), Headers: headers, GlobalProxy: globalProxy,
	})
	if err != nil {
		headers.Close()
		return nil, nil, nil, err
	}
	workers := newAccountWorkerManager(
		pool, accounts, requests, camoufoxPath, globalProxy, cfg.InitTimeout,
		cfg.WarmWorkerLimit, cfg.MaxActiveWorkers, cfg.WarmStartupConcurrency, cfg.TemporaryChat,
	)
	protected, err := aistudio.NewWorkerProtectedTransport(aistudio.WorkerProtectedTransportOptions{
		Transport: transport, Workers: workers, SetupTimeout: cfg.InitTimeout,
	})
	if err != nil {
		transport.CloseIdleConnections()
		headers.Close()
		return nil, nil, nil, errors.Join(err, workers.Close())
	}
	requestContext, err := aistudio.NewPoolRequestContextProvider(pool)
	if err != nil {
		transport.CloseIdleConnections()
		headers.Close()
		return nil, nil, nil, errors.Join(err, workers.Close())
	}
	refresher := newAuthRuntimeRefresher(workers, headers, requests, globalProxy)
	workers.refresher = refresher
	client, err := aistudio.NewClient(aistudio.ClientOptions{
		Transport:       &authRetryTransport{transport: transport, refresher: refresher},
		Protected:       &authRetryProtectedTransport{transport: protected, refresher: refresher},
		ContextProvider: requestContext,
	})
	if err != nil {
		transport.CloseIdleConnections()
		headers.Close()
		return nil, nil, nil, errors.Join(err, workers.Close())
	}
	pooled, err := aistudio.NewPooledService(pool, client)
	if err != nil {
		transport.CloseIdleConnections()
		headers.Close()
		return nil, nil, nil, errors.Join(err, workers.Close())
	}
	pooled.BuildNativeNonstream = cfg.BuildNativeNonstream
	pooled.YouTubeLinks = cfg.YouTubeLinks
	service := newTrackedService(lifecycle, pooled, pool, requests, workers, cfg.RequestTimeout)
	service.buildNativeNonstream = cfg.BuildNativeNonstream
	service.hedgeAuto, service.hedgeAfter, _ = config.ParseFirstEventHedge(cfg.FirstEventHedge)
	service.modelMapping, _ = config.ParseModelMapping(cfg.ModelMapping)
	admin := newRuntimeAdmin(lifecycle, pool, store, service, requests, login, workers, headers, cfg)
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
		headers.Close()
		return err
	}
	return service, admin, closeRuntime, nil
}

// accountWorkerManager 管理每账户的长驻 WAA worker
type accountWorkerManager struct {
	mu             sync.RWMutex
	fillMu         sync.Mutex
	rebalanceMu    sync.Mutex
	pool           *aistudio.AccountPool
	accounts       map[string]*accountWorker
	openings       map[string]chan struct{}
	requests       *requestRegistry
	refresher      *authRuntimeRefresher
	proxyFailed    func(proxy string, accountID string, err error)
	camoufox       string
	globalProxy    string
	initTimeout    time.Duration
	warmTarget     atomic.Int64
	maxActive      atomic.Int64
	startup        startupLimiter
	temporaryChat  bool
	lifecycle      context.Context
	cancel         context.CancelFunc
	closed         bool
	signalMu       sync.Mutex
	signal         chan struct{}
	dispatch       *dispatchQueue
	victimMu       sync.Mutex
	victims        map[string]struct{}
	backgroundMu   sync.Mutex
	background     context.Context
	stopBackground context.CancelFunc
}

type accountWorker struct {
	mu             sync.Mutex
	startupMu      sync.Mutex
	id             string
	label          string
	config         camoufoxnative.Options
	worker         *aistudio.NativeWorker
	bootstrapModel string
	runtimeLease   *aistudio.AccountRuntimeLease
	cleanupWorker  *aistudio.NativeWorker
	cleanupLease   *aistudio.AccountRuntimeLease
	warm           atomic.Bool
	readyAt        atomic.Int64
	generation     atomic.Uint64
	busyUntil      time.Time
	busyDelay      time.Duration
	busyErr        error
}

type accountWorkerPreparer struct {
	account        *accountWorker
	worker         *aistudio.NativeWorker
	bootstrapModel string
	manager        *accountWorkerManager
	runtimeLease   *aistudio.AccountRuntimeLease
	ownsLease      bool
	startedAt      time.Time
	pending        bool
}

type accountWorkerUpdate struct {
	account *accountWorker
	config  camoufoxnative.Options
	label   string
	pending bool
}

var errAccountWorkerReplaced = errors.New("WAA worker 已更新")
var errAccountWorkerOpening = errors.New("WAA worker 正在启动")

// errAccountWorkerCapacity 表示当前没有可立即使用的 Worker 槽位
var errAccountWorkerCapacity = errors.New("WAA worker 槽位已满")
var errAccountWorkerCleanupPending = errors.New("WAA worker 清理未完成")

// errAccountRuntimeBusy 表示账户 WAA runtime 租约由其他进程持有
var errAccountRuntimeBusy = errors.New("WAA runtime 由其他进程占用")

const (
	// runtimeBusyInitialDelay 为账户被其他进程占用后首次重新探测的间隔
	runtimeBusyInitialDelay = 5 * time.Second
	// runtimeBusyMaxDelay 为占用账户重新探测间隔的上限
	runtimeBusyMaxDelay = time.Minute
)

const (
	workerEvictionTimeout = 100 * time.Millisecond
)

// Prepare 在账户 Worker 有效期间生成 proof
func (preparer *accountWorkerPreparer) Prepare(ctx context.Context, request aistudio.ProtectedRequest) (aistudio.PreparedProtectedRequest, error) {
	if !preparer.current() {
		return aistudio.PreparedProtectedRequest{}, errAccountWorkerReplaced
	}
	startedAt := time.Now()
	defer func() { api.AddAccessLogProof(ctx, time.Since(startedAt)) }()
	prepared, err := preparer.worker.Prepare(ctx, request)
	if err != nil && !preparer.current() {
		return aistudio.PreparedProtectedRequest{}, errAccountWorkerReplaced
	}
	return prepared, err
}

// SendProtected 校验当前账户 Worker 后发送浏览器请求
func (preparer *accountWorkerPreparer) SendProtected(ctx context.Context, request aistudio.ProtectedRequest) (*aistudio.RPCResponse, error) {
	if !preparer.current() {
		return nil, errAccountWorkerReplaced
	}
	return preparer.worker.SendProtected(ctx, request)
}

// BrowserStorageState 返回同一有效账户 Worker 的浏览器 Cookie 状态
func (preparer *accountWorkerPreparer) BrowserStorageState(ctx context.Context) (aistudio.StorageState, error) {
	if !preparer.current() {
		return aistudio.StorageState{}, errAccountWorkerReplaced
	}
	state, err := preparer.worker.BrowserStorageState(ctx)
	if err != nil && !preparer.current() {
		return aistudio.StorageState{}, errAccountWorkerReplaced
	}
	return state, err
}

// current 在账户锁内核对 Worker 与页面模型未被替换，浏览器 RPC 在锁外执行，失败后再次核对
func (preparer *accountWorkerPreparer) current() bool {
	preparer.account.mu.Lock()
	defer preparer.account.mu.Unlock()
	return preparer.account.worker == preparer.worker && preparer.account.bootstrapModel == preparer.bootstrapModel
}

// accountWorkerInitError 表示单个账户的 WAA worker 初始化失败
type accountWorkerInitError struct {
	err         error
	authHandled bool
	proxy       string
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
	requests *requestRegistry,
	camoufoxPath string,
	globalProxy string,
	initTimeout time.Duration,
	warmTarget int,
	maxActive int,
	warmConcurrency int,
	temporaryChat bool,
) *accountWorkerManager {
	lifecycle, cancel := context.WithCancel(context.Background())
	manager := &accountWorkerManager{
		pool: pool, accounts: make(map[string]*accountWorker, len(accounts)), requests: requests, camoufox: camoufoxPath,
		globalProxy: globalProxy, initTimeout: initTimeout, temporaryChat: temporaryChat,
		openings:  make(map[string]chan struct{}),
		lifecycle: lifecycle, cancel: cancel, signal: make(chan struct{}), dispatch: newDispatchQueue(),
		victims: make(map[string]struct{}),
	}
	manager.setLimits(warmTarget, maxActive, warmConcurrency)
	manager.background, manager.stopBackground = context.WithCancel(lifecycle)
	for _, account := range accounts {
		if account == nil {
			continue
		}
		manager.accounts[account.ID] = manager.newAccountWorker(account)
	}
	go manager.forwardPoolChanges()
	go manager.reapIdleWorkers()
	return manager
}

// SetCapacity 设置常驻 Worker 数、峰值 Worker 上限与 Camoufox 启动并发，随后关闭超出峰值上限的空闲 Worker
func (manager *accountWorkerManager) SetCapacity(warmTarget int, maxActive int, startupConcurrency int) {
	manager.setLimits(warmTarget, maxActive, startupConcurrency)
	manager.dispatch.wakeHeads()
	go func() {
		for manager.reapIdleWorker(time.Now().Add(-workerIdleTimeout)) {
		}
	}()
}

// setLimits 保存 Worker 数量上限，纯 Go 后端同时启动的 Worker 数只受峰值上限约束
func (manager *accountWorkerManager) setLimits(warmTarget int, maxActive int, startupConcurrency int) {
	if manager.camoufox == "" {
		startupConcurrency = maxActive
	}
	manager.warmTarget.Store(int64(warmTarget))
	manager.maxActive.Store(int64(maxActive))
	manager.startup.setLimit(max(startupConcurrency, 1))
}

// warmLimit 返回常驻 Worker 数
func (manager *accountWorkerManager) warmLimit() int {
	return int(manager.warmTarget.Load())
}

// activeLimit 返回峰值 Worker 上限
func (manager *accountWorkerManager) activeLimit() int {
	return int(manager.maxActive.Load())
}

// expandInBackground 在后台为账户启动 Worker，就绪后由调度信号唤醒排队请求
func (manager *accountWorkerManager) expandInBackground(accountID string, modelID string) {
	ctx := manager.backgroundContext()
	go func() {
		_, err := manager.ensureWorker(ctx, accountID, modelID, false)
		if err == nil || ctx.Err() != nil || errors.Is(err, errAccountWorkerOpening) ||
			errors.Is(err, errAccountWorkerCapacity) || errors.Is(err, errAccountRuntimeBusy) {
			return
		}
		manager.requests.log(accountID, "WARN", fmt.Sprintf("WAA Worker 后台扩容失败 | 错误=%v", err))
		var startup *accountWorkerInitError
		if manager.proxyFailed != nil && errors.As(err, &startup) && proxyNetworkError(ctx, err) {
			manager.proxyFailed(startup.proxy, accountID, err)
		}
	}()
}

// backgroundContext 返回后台扩容使用的 context，ResetAll 时取消
func (manager *accountWorkerManager) backgroundContext() context.Context {
	manager.backgroundMu.Lock()
	defer manager.backgroundMu.Unlock()
	return manager.background
}

// cancelBackgroundStartups 取消进行中的后台扩容并为后续扩容建立新 context
func (manager *accountWorkerManager) cancelBackgroundStartups() {
	manager.backgroundMu.Lock()
	manager.stopBackground()
	manager.background, manager.stopBackground = context.WithCancel(manager.lifecycle)
	manager.backgroundMu.Unlock()
}

// markRuntimeBusy 让被其他进程占用的账户按指数退避暂时退出候选，每段占用只记录一次
func (manager *accountWorkerManager) markRuntimeBusy(account *accountWorker, cause error) error {
	account.mu.Lock()
	first := account.busyDelay == 0
	if first {
		account.busyDelay = runtimeBusyInitialDelay
	} else {
		account.busyDelay = min(account.busyDelay*2, runtimeBusyMaxDelay)
	}
	delay := account.busyDelay
	account.busyUntil = time.Now().Add(delay)
	account.busyErr = fmt.Errorf("%w: %w", errAccountRuntimeBusy, cause)
	busyErr := account.busyErr
	label := account.label
	account.mu.Unlock()
	if first {
		manager.requests.log(label, "WARN", fmt.Sprintf(
			"WAA Worker 账户由其他进程占用 | 暂停调度=%s | 错误=%s", delay, strings.TrimSpace(cause.Error()),
		))
	}
	time.AfterFunc(delay, manager.notifyScheduling)
	return busyErr
}

// clearRuntimeBusy 在重新取得 runtime 租约后恢复账户调度
func (manager *accountWorkerManager) clearRuntimeBusy(account *accountWorker) {
	account.mu.Lock()
	wasBusy := account.busyDelay != 0
	account.busyDelay = 0
	account.busyUntil = time.Time{}
	account.busyErr = nil
	label := account.label
	account.mu.Unlock()
	if wasBusy {
		manager.requests.log(label, "INFO", "WAA Worker 账户占用解除")
	}
}

// runtimeAvailable 过滤仍在占用退避期内的账户，并返回其中一个占用原因
func (manager *accountWorkerManager) runtimeAvailable(accountIDs []string) ([]string, error) {
	now := time.Now()
	available := make([]string, 0, len(accountIDs))
	var busyErr error
	for _, accountID := range accountIDs {
		manager.mu.RLock()
		account := manager.accounts[accountID]
		manager.mu.RUnlock()
		if account == nil {
			available = append(available, accountID)
			continue
		}
		account.mu.Lock()
		busy := now.Before(account.busyUntil)
		if busy && busyErr == nil {
			busyErr = account.busyErr
		}
		account.mu.Unlock()
		if !busy {
			available = append(available, accountID)
		}
	}
	return available, busyErr
}

// workerIdleTimeout 为超出热池目标的 Worker 空闲多久后关闭
const workerIdleTimeout = 5 * time.Minute

// reapIdleWorkers 定期关闭超出热池目标且空闲超时的 Worker
func (manager *accountWorkerManager) reapIdleWorkers() {
	ticker := time.NewTicker(workerIdleTimeout / 10)
	defer ticker.Stop()
	for {
		select {
		case <-manager.lifecycle.Done():
			return
		case <-ticker.C:
			for manager.reapIdleWorker(time.Now().Add(-workerIdleTimeout)) {
			}
		}
	}
}

// reapIdleWorker 关闭一个多余的空闲 Worker 并返回是否关闭：超出峰值上限时立即关闭，否则只关闭最近使用早于 before 的
func (manager *accountWorkerManager) reapIdleWorker(before time.Time) bool {
	manager.rebalanceMu.Lock()
	defer manager.rebalanceMu.Unlock()
	warm := len(manager.WarmAccountIDs())
	if len(manager.openings) > 0 || warm <= manager.warmLimit() {
		return false
	}
	victim := manager.idleWarmVictim("")
	if victim == "" {
		return false
	}
	manager.mu.RLock()
	account := manager.accounts[victim]
	manager.mu.RUnlock()
	_, lastUsed := manager.pool.Activity(victim)
	if warm <= manager.activeLimit() &&
		(lastUsed.After(before) || account != nil && time.Unix(0, account.readyAt.Load()).After(before)) {
		return false
	}
	evicted, err := manager.evictIdleWorker(manager.lifecycle, victim)
	if err != nil {
		manager.requests.log(victim, "WARN", fmt.Sprintf("WAA Worker 空闲回收失败 | 错误=%v", err))
		return false
	}
	if evicted {
		manager.requests.log("service", "INFO", fmt.Sprintf(
			"WAA Worker 空闲回收 | Worker=%d/%d", len(manager.WarmAccountIDs()), manager.activeLimit(),
		))
	}
	return evicted
}

// forwardPoolChanges 在账户池租约或状态变化后唤醒排队请求
func (manager *accountWorkerManager) forwardPoolChanges() {
	for {
		changed := manager.pool.Changed()
		select {
		case <-manager.lifecycle.Done():
			return
		case <-changed:
			manager.dispatch.wakeHeads()
		}
	}
}

// schedulingChanged 返回下一次 Worker 容量或热池状态变化时关闭的通道
func (manager *accountWorkerManager) schedulingChanged() <-chan struct{} {
	manager.signalMu.Lock()
	defer manager.signalMu.Unlock()
	return manager.signal
}

// notifyScheduling 在 Worker 容量或热池变化后唤醒等待请求
func (manager *accountWorkerManager) notifyScheduling() {
	manager.signalMu.Lock()
	close(manager.signal)
	manager.signal = make(chan struct{})
	manager.signalMu.Unlock()
	manager.dispatch.wakeHeads()
}

// finishOpening 结束账户 Worker 启动占位并唤醒等待容量的请求
func (manager *accountWorkerManager) finishOpening(accountID string, opening chan struct{}) {
	delete(manager.openings, accountID)
	close(opening)
	manager.notifyScheduling()
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
	account.startupMu.Lock()
	defer account.startupMu.Unlock()
	account.mu.Lock()
	defer account.mu.Unlock()
	if account.worker == nil && account.cleanupWorker == nil && account.runtimeLease == nil && account.cleanupLease == nil {
		return nil
	}
	startedAt := time.Now()
	pid := 0
	if account.worker != nil {
		pid = account.worker.State().PID
	} else if account.cleanupWorker != nil {
		pid = account.cleanupWorker.State().PID
	}
	manager.requests.log(account.label, "INFO", fmt.Sprintf("WAA Worker 停止 | PID=%d", pid))
	err := manager.closeAccountWorker(account)
	if err != nil {
		manager.requests.log(account.label, "ERROR", fmt.Sprintf(
			"WAA Worker 停止失败 | PID=%d | 耗时=%s | 错误=%s",
			pid, time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
		))
		return err
	}
	manager.requests.log(account.label, "INFO", fmt.Sprintf(
		"WAA Worker 已停止 | PID=%d | 耗时=%s",
		pid, time.Since(startedAt).Round(time.Millisecond),
	))
	return err
}

// WorkerGeneration 返回账户当前 Worker 版本号
func (manager *accountWorkerManager) WorkerGeneration(accountID string) uint64 {
	manager.mu.RLock()
	account := manager.accounts[accountID]
	manager.mu.RUnlock()
	if account == nil {
		return 0
	}
	return account.generation.Load()
}

// ResetIfGeneration 等待活动请求结束后仅关闭产生当前失败的 Worker
func (manager *accountWorkerManager) ResetIfGeneration(ctx context.Context, accountID string, generation uint64) (reset bool, err error) {
	lease, err := manager.pool.AcquireAccount(ctx, accountID)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, lease.Release()) }()
	manager.mu.RLock()
	account := manager.accounts[accountID]
	manager.mu.RUnlock()
	if account == nil {
		return false, fmt.Errorf("WAA worker 账户不存在: %s", accountID)
	}
	account.startupMu.Lock()
	defer account.startupMu.Unlock()
	account.mu.Lock()
	defer account.mu.Unlock()
	if account.generation.Load() != generation {
		return false, nil
	}
	if account.worker == nil && account.cleanupWorker == nil && account.runtimeLease == nil && account.cleanupLease == nil {
		return false, nil
	}
	startedAt := time.Now()
	pid := 0
	if account.worker != nil {
		pid = account.worker.State().PID
	} else if account.cleanupWorker != nil {
		pid = account.cleanupWorker.State().PID
	}
	manager.requests.log(account.label, "INFO", fmt.Sprintf("WAA Worker 停止 | PID=%d", pid))
	if err := manager.closeAccountWorker(account); err != nil {
		manager.requests.log(account.label, "ERROR", fmt.Sprintf(
			"WAA Worker 停止失败 | PID=%d | 耗时=%s | 错误=%s",
			pid, time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
		))
		return false, err
	}
	manager.requests.log(account.label, "INFO", fmt.Sprintf(
		"WAA Worker 已停止 | PID=%d | 耗时=%s",
		pid, time.Since(startedAt).Round(time.Millisecond),
	))
	return true, nil
}

// prepareUpdate 关闭旧 Worker 并保持新配置待发布
func (manager *accountWorkerManager) prepareUpdate(
	account *aistudio.Account,
	config aistudio.AccountConfig,
) (*accountWorkerUpdate, error) {
	if account == nil {
		return nil, fmt.Errorf("账户未初始化")
	}
	manager.mu.RLock()
	worker := manager.accounts[account.ID]
	manager.mu.RUnlock()
	if worker == nil {
		return nil, fmt.Errorf("WAA worker 账户不存在: %s", account.ID)
	}
	update := &accountWorkerUpdate{
		account: worker, config: manager.workerConfigFor(account, config), label: config.Label, pending: true,
	}
	worker.startupMu.Lock()
	worker.mu.Lock()
	if worker.worker != nil || worker.cleanupWorker != nil || worker.runtimeLease != nil || worker.cleanupLease != nil {
		if err := manager.closeAccountWorker(worker); err != nil {
			worker.mu.Unlock()
			worker.startupMu.Unlock()
			return nil, err
		}
	}
	worker.mu.Unlock()
	return update, nil
}

// Commit 发布已准备的 Worker 配置
func (update *accountWorkerUpdate) Commit() {
	if update == nil || !update.pending {
		return
	}
	update.account.mu.Lock()
	update.account.config = update.config
	update.account.label = update.label
	update.account.mu.Unlock()
	update.pending = false
	update.account.startupMu.Unlock()
}

// Discard 放弃已准备的 Worker 配置
func (update *accountWorkerUpdate) Discard() {
	if update == nil || !update.pending {
		return
	}
	update.pending = false
	update.account.startupMu.Unlock()
}

// ResetAll 关闭全部账户当前 worker 并保留后续按需重建能力
func (manager *accountWorkerManager) ResetAll() error {
	manager.cancelBackgroundStartups()
	manager.mu.RLock()
	accountIDs := make([]string, 0, len(manager.accounts))
	for accountID := range manager.accounts {
		accountIDs = append(accountIDs, accountID)
	}
	manager.mu.RUnlock()
	resetResults := make(chan error, len(accountIDs))
	var resets sync.WaitGroup
	for _, accountID := range accountIDs {
		resets.Add(1)
		go func(accountID string) {
			defer resets.Done()
			resetResults <- manager.Reset(accountID)
		}(accountID)
	}
	resets.Wait()
	close(resetResults)
	var resetErrors []error
	for resetErr := range resetResults {
		resetErrors = append(resetErrors, resetErr)
	}
	return errors.Join(resetErrors...)
}

// Remove 删除账户的 WAA worker 配置
func (manager *accountWorkerManager) Remove(accountID string) error {
	manager.rebalanceMu.Lock()
	manager.mu.Lock()
	account := manager.accounts[accountID]
	if account == nil {
		manager.mu.Unlock()
		manager.rebalanceMu.Unlock()
		return fmt.Errorf("WAA worker 账户不存在: %s", accountID)
	}
	delete(manager.accounts, accountID)
	manager.mu.Unlock()
	manager.rebalanceMu.Unlock()

	account.startupMu.Lock()
	defer account.startupMu.Unlock()
	account.mu.Lock()
	defer account.mu.Unlock()
	return manager.closeAccountWorker(account)
}

func (manager *accountWorkerManager) newAccountWorker(account *aistudio.Account) *accountWorker {
	return &accountWorker{id: account.ID, label: account.Config.Label, config: manager.workerConfig(account)}
}

func (manager *accountWorkerManager) workerConfig(account *aistudio.Account) camoufoxnative.Options {
	return manager.workerConfigFor(account, account.Config)
}

func (manager *accountWorkerManager) workerConfigFor(
	account *aistudio.Account,
	config aistudio.AccountConfig,
) camoufoxnative.Options {
	proxy := strings.TrimSpace(config.Proxy)
	if proxy == "" {
		proxy = account.PoolProxy(manager.globalProxy)
	}
	return camoufoxnative.Options{
		ExecutablePath:   manager.camoufox,
		StorageStatePath: account.StoragePath,
		Locale:           config.Locale,
		Timezone:         config.Timezone,
		Proxy:            proxy,
		Headless:         true,
		TemporaryChat:    manager.temporaryChat,
		AuthUser:         config.AuthUser,
	}
}

// WarmAccountIDs 返回当前驻留的健康 WAA worker
func (manager *accountWorkerManager) WarmAccountIDs() []string {
	manager.mu.RLock()
	accounts := make([]*accountWorker, 0, len(manager.accounts))
	for _, account := range manager.accounts {
		accounts = append(accounts, account)
	}
	manager.mu.RUnlock()
	warm := make([]string, 0, len(accounts))
	for _, account := range accounts {
		if account.warm.Load() {
			warm = append(warm, account.id)
		}
	}
	return warm
}

type workerOccupancy struct {
	accountIDs []string
	slots      int
}

// occupiedWorkers 返回仍持有进程或运行锁的账户与容量槽位
func (manager *accountWorkerManager) occupiedWorkers() workerOccupancy {
	manager.mu.RLock()
	accounts := make([]*accountWorker, 0, len(manager.accounts))
	for _, account := range manager.accounts {
		accounts = append(accounts, account)
	}
	manager.mu.RUnlock()
	occupied := workerOccupancy{accountIDs: make([]string, 0, len(accounts))}
	for _, account := range accounts {
		account.mu.Lock()
		if account.worker != nil || account.cleanupWorker != nil || account.runtimeLease != nil || account.cleanupLease != nil {
			occupied.accountIDs = append(occupied.accountIDs, account.id)
		}
		if account.worker != nil {
			occupied.slots++
		}
		if account.cleanupWorker != nil {
			occupied.slots++
		}
		if account.worker == nil && account.cleanupWorker == nil && account.runtimeLease != nil {
			occupied.slots++
		}
		if account.cleanupWorker == nil && account.cleanupLease != nil {
			occupied.slots++
		}
		account.mu.Unlock()
	}
	return occupied
}

// ReadyWarmAccountIDs 返回可生成 proof 的预热账户
func (manager *accountWorkerManager) ReadyWarmAccountIDs() []string {
	manager.mu.RLock()
	accounts := make([]*accountWorker, 0, len(manager.accounts))
	for _, account := range manager.accounts {
		accounts = append(accounts, account)
	}
	manager.mu.RUnlock()
	warm := make([]string, 0, len(accounts))
	for _, account := range accounts {
		if !account.warm.Load() {
			continue
		}
		account.mu.Lock()
		worker := account.worker
		matches := worker != nil
		if matches {
			phase := worker.State().Phase
			matches = phase == aistudio.WorkerReady || phase == aistudio.WorkerBusy
		}
		account.mu.Unlock()
		if matches {
			warm = append(warm, account.id)
		}
	}
	return warm
}

func (manager *accountWorkerManager) coldAccounts(accountIDs []string) []string {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	cold := make([]string, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		account := manager.accounts[accountID]
		if account != nil && !account.warm.Load() {
			cold = append(cold, accountID)
		}
	}
	return cold
}

// PrewarmTarget 返回当前配置需要预热的账户数
func (manager *accountWorkerManager) PrewarmTarget() int {
	available := 0
	for _, status := range manager.pool.Status() {
		if !status.Enabled || (status.State != aistudio.AccountReady && status.State != aistudio.AccountBusy && status.State != aistudio.AccountCooldown) {
			continue
		}
		if models, err := manager.pool.BootstrapModels(status.ID); err == nil && len(models) > 0 {
			available++
		}
	}
	return min(manager.warmLimit(), available)
}

func (manager *accountWorkerManager) classifyBootstrapCandidates(
	ctx context.Context,
	warm []string,
) (aistudio.AccountCandidateGroups, error) {
	combined := aistudio.AccountCandidateGroups{}
	seenWarmReady := make(map[string]struct{})
	seenWarmAvailable := make(map[string]struct{})
	seenWarmBusy := make(map[string]struct{})
	seenStandbyReady := make(map[string]struct{})
	seenStandbyBusy := make(map[string]struct{})
	appendUnique := func(target *[]string, seen map[string]struct{}, values []string) {
		for _, value := range values {
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			*target = append(*target, value)
		}
	}
	modelIDs := make([]string, 0)
	seenModels := make(map[string]struct{})
	for _, status := range manager.pool.Status() {
		models, err := manager.pool.BootstrapModels(status.ID)
		if err != nil {
			continue
		}
		for _, modelID := range models {
			if _, exists := seenModels[modelID]; exists {
				continue
			}
			seenModels[modelID] = struct{}{}
			modelIDs = append(modelIDs, modelID)
		}
	}
	var matched bool
	for _, modelID := range modelIDs {
		groups, err := manager.pool.ClassifyCandidates(ctx, aistudio.AccountSelection{
			ModelID: modelID, Method: "generateContent",
		}, warm)
		if errors.Is(err, aistudio.ErrModelNotFound) {
			continue
		}
		if err != nil {
			return aistudio.AccountCandidateGroups{}, err
		}
		matched = true
		combined.Eligible = combined.Eligible || groups.Eligible
		if combined.EarliestCooldown.Until.IsZero() || !groups.EarliestCooldown.Until.IsZero() && groups.EarliestCooldown.Until.Before(combined.EarliestCooldown.Until) {
			combined.EarliestCooldown = groups.EarliestCooldown
		}
		appendUnique(&combined.WarmReady, seenWarmReady, groups.WarmReady)
		appendUnique(&combined.WarmAvailable, seenWarmAvailable, groups.WarmAvailable)
		appendUnique(&combined.WarmBusy, seenWarmBusy, groups.WarmBusy)
		appendUnique(&combined.StandbyReady, seenStandbyReady, groups.StandbyReady)
		appendUnique(&combined.StandbyBusy, seenStandbyBusy, groups.StandbyBusy)
	}
	if !matched {
		return aistudio.AccountCandidateGroups{}, aistudio.ErrNoEligibleAccount
	}
	combined.StandbyReady = manager.pool.PreferWarmPool(combined.StandbyReady)
	combined.StandbyBusy = manager.pool.PreferWarmPool(combined.StandbyBusy)
	return combined, nil
}

// WorkerFailed 返回账户驻留 worker 是否已经失败
func (manager *accountWorkerManager) WorkerFailed(accountID string) bool {
	manager.mu.RLock()
	account := manager.accounts[accountID]
	manager.mu.RUnlock()
	if account == nil {
		return false
	}
	account.mu.Lock()
	defer account.mu.Unlock()
	return account.worker != nil && account.worker.State().Phase == aistudio.WorkerFailed
}

// Worker 在活动上限内返回账户的通用 WAA preparer
func (manager *accountWorkerManager) Worker(ctx context.Context, accountID string, modelID string) (aistudio.ProtectedPreparer, error) {
	return manager.ensureWorker(ctx, accountID, modelID, true)
}

func (manager *accountWorkerManager) readyWorker(accountID string, bootstrapModel string) (aistudio.ProtectedPreparer, bool, error) {
	manager.mu.RLock()
	if manager.closed {
		manager.mu.RUnlock()
		return nil, false, fmt.Errorf("WAA worker manager 已关闭")
	}
	account := manager.accounts[accountID]
	manager.mu.RUnlock()
	if account == nil {
		return nil, false, fmt.Errorf("账户不存在: %s", accountID)
	}
	account.mu.Lock()
	defer account.mu.Unlock()
	manager.mu.RLock()
	active := !manager.closed && manager.accounts[accountID] == account
	manager.mu.RUnlock()
	if !active {
		return nil, false, fmt.Errorf("账户不存在: %s", accountID)
	}
	if accountWorkerCleanupPending(account) {
		return nil, false, fmt.Errorf("%w: %s", errAccountWorkerCleanupPending, accountID)
	}
	if account.worker != nil {
		phase := account.worker.State().Phase
		if (phase == aistudio.WorkerReady || phase == aistudio.WorkerBusy) && account.bootstrapModel == bootstrapModel {
			return &accountWorkerPreparer{
				account: account, worker: account.worker, bootstrapModel: account.bootstrapModel,
			}, true, nil
		}
	}
	return nil, false, nil
}

func (manager *accountWorkerManager) startReservedWorker(
	ctx context.Context,
	accountID string,
	bootstrapModel string,
) (*accountWorkerPreparer, error) {
	manager.mu.RLock()
	if manager.closed {
		manager.mu.RUnlock()
		return nil, fmt.Errorf("WAA worker manager 已关闭")
	}
	account := manager.accounts[accountID]
	manager.mu.RUnlock()
	if account == nil {
		return nil, fmt.Errorf("账户不存在: %s", accountID)
	}
	account.startupMu.Lock()
	account.mu.Lock()
	manager.mu.RLock()
	active := !manager.closed && manager.accounts[accountID] == account
	manager.mu.RUnlock()
	if !active {
		account.mu.Unlock()
		account.startupMu.Unlock()
		return nil, fmt.Errorf("账户不存在: %s", accountID)
	}
	if accountWorkerCleanupPending(account) {
		account.mu.Unlock()
		account.startupMu.Unlock()
		return nil, fmt.Errorf("%w: %s", errAccountWorkerCleanupPending, accountID)
	}
	if account.worker != nil {
		phase := account.worker.State().Phase
		if (phase == aistudio.WorkerReady || phase == aistudio.WorkerBusy) && account.bootstrapModel == bootstrapModel {
			preparer := &accountWorkerPreparer{
				account: account, worker: account.worker, bootstrapModel: account.bootstrapModel,
			}
			account.mu.Unlock()
			account.startupMu.Unlock()
			return preparer, nil
		}
	}
	startedAt := time.Now()
	label := account.label
	options := account.config
	runtimeLease := account.runtimeLease
	ownsLease := false
	account.mu.Unlock()
	if runtimeLease == nil {
		var err error
		runtimeLease, err = aistudio.AcquireAccountRuntimeLease(account.id)
		if errors.Is(err, aistudio.ErrAccountLeased) {
			account.startupMu.Unlock()
			return nil, manager.markRuntimeBusy(account, err)
		}
		if err != nil {
			account.startupMu.Unlock()
			manager.requests.log(label, "ERROR", fmt.Sprintf(
				"WAA Worker 启动失败 | 耗时=%s | 错误=%s",
				time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
			))
			return nil, &accountWorkerInitError{err: err}
		}
		ownsLease = true
		manager.clearRuntimeBusy(account)
	}
	if err := manager.startup.acquire(ctx); err != nil {
		if ownsLease {
			_ = runtimeLease.Release()
		}
		account.startupMu.Unlock()
		return nil, err
	}
	manager.requests.log(label, "INFO", "WAA Worker 启动 | 1/7 | 初始化页面 | 页面模型="+bootstrapModel)
	initCtx, cancel := context.WithTimeout(ctx, manager.initTimeout)
	options.Model = bootstrapModel
	launched, _ := ctx.Value(browserLaunchedKey{}).(func())
	options.StartupProgress = func(stage camoufoxnative.StartupStage) {
		step, message := workerStartupProgress(stage)
		manager.requests.log(label, "INFO", fmt.Sprintf("WAA Worker 启动 | %d/7 | %s", step, message))
		if (stage == camoufoxnative.StartupConnectingBiDi || stage == camoufoxnative.StartupLoadingAIStudio) && launched != nil {
			launched()
		}
	}
	worker, initErr := newWAAWorker(initCtx, account.id, options)
	cancel()
	manager.startup.release()
	if initErr != nil {
		if ownsLease {
			_ = runtimeLease.Release()
		}
		account.startupMu.Unlock()
		if err := ctx.Err(); err != nil {
			manager.requests.log(label, "WARN", fmt.Sprintf(
				"WAA Worker 启动已取消 | 页面模型=%s | 耗时=%s",
				bootstrapModel, time.Since(startedAt).Round(time.Millisecond),
			))
			return nil, err
		}
		manager.requests.log(label, "ERROR", fmt.Sprintf(
			"WAA Worker 启动失败 | 页面模型=%s | 耗时=%s | 错误=%s",
			bootstrapModel, time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(initErr.Error()),
		))
		return nil, &accountWorkerInitError{err: initErr, proxy: options.Proxy}
	}
	return &accountWorkerPreparer{
		account: account, worker: worker, bootstrapModel: bootstrapModel, manager: manager,
		runtimeLease: runtimeLease, ownsLease: ownsLease, startedAt: startedAt, pending: true,
	}, nil
}

// activateAccountWorker 将已启动 Worker 发布到热池
func activateAccountWorker(preparer *accountWorkerPreparer) error {
	if !preparer.pending {
		return nil
	}
	preparer.account.mu.Lock()
	preparer.manager.mu.RLock()
	active := !preparer.manager.closed && preparer.manager.accounts[preparer.account.id] == preparer.account
	preparer.manager.mu.RUnlock()
	if !active {
		preparer.account.mu.Unlock()
		return errors.Join(fmt.Errorf("账户不存在: %s", preparer.account.id), discardAccountWorker(preparer))
	}
	oldWorker := preparer.account.worker
	if oldWorker != nil {
		if closeErr := oldWorker.Close(); closeErr != nil {
			label := preparer.account.label
			preparer.account.mu.Unlock()
			cleanupErr := discardAccountWorker(preparer)
			preparer.manager.requests.log(label, "ERROR", fmt.Sprintf(
				"WAA Worker 旧实例停止失败 | 错误=%s", strings.TrimSpace(closeErr.Error()),
			))
			return errors.Join(closeErr, cleanupErr)
		}
	}
	preparer.account.worker = preparer.worker
	preparer.account.bootstrapModel = preparer.bootstrapModel
	if preparer.ownsLease {
		preparer.account.runtimeLease = preparer.runtimeLease
	}
	if oldWorker != nil {
		preparer.account.generation.Add(1)
	}
	preparer.account.warm.Store(true)
	preparer.account.readyAt.Store(time.Now().UnixNano())
	label := preparer.account.label
	preparer.account.mu.Unlock()
	preparer.manager.notifyScheduling()
	preparer.manager.requests.log(label, "INFO", fmt.Sprintf(
		"WAA Worker 就绪 | 页面模型=%s | PID=%d | 耗时=%s",
		preparer.bootstrapModel, preparer.worker.State().PID, time.Since(preparer.startedAt).Round(time.Millisecond),
	))
	preparer.pending = false
	preparer.account.startupMu.Unlock()
	return nil
}

// discardAccountWorker 关闭尚未发布的 Worker
func discardAccountWorker(preparer *accountWorkerPreparer) error {
	if !preparer.pending {
		return nil
	}
	workerErr := preparer.worker.Close()
	var leaseErr error
	if workerErr == nil && preparer.ownsLease {
		leaseErr = preparer.runtimeLease.Release()
	}
	cleanupErr := errors.Join(workerErr, leaseErr)
	if cleanupErr != nil {
		preparer.account.mu.Lock()
		if workerErr != nil {
			preparer.account.cleanupWorker = preparer.worker
		}
		if preparer.ownsLease {
			preparer.account.cleanupLease = preparer.runtimeLease
		}
		preparer.account.mu.Unlock()
	}
	preparer.account.mu.Lock()
	label := preparer.account.label
	preparer.account.mu.Unlock()
	preparer.manager.requests.log(label, "WARN", fmt.Sprintf(
		"WAA Worker 启动已取消 | 页面模型=%s | 耗时=%s",
		preparer.bootstrapModel, time.Since(preparer.startedAt).Round(time.Millisecond),
	))
	preparer.pending = false
	preparer.account.startupMu.Unlock()
	return cleanupErr
}

func accountWorkerCleanupPending(account *accountWorker) bool {
	if account.cleanupWorker != nil || account.cleanupLease != nil || account.worker == nil && account.runtimeLease != nil {
		return true
	}
	return account.worker != nil && account.worker.State().Phase == aistudio.WorkerClosing
}

func workerStartupProgress(stage camoufoxnative.StartupStage) (int, string) {
	switch stage {
	case camoufoxnative.StartupPreparingBrowser:
		return 2, "准备浏览器配置"
	case camoufoxnative.StartupLaunchingBrowser:
		return 3, "启动 Camoufox"
	case camoufoxnative.StartupConnectingBiDi:
		return 4, "连接 WebDriver BiDi"
	case camoufoxnative.StartupLoadingAIStudio:
		return 5, "载入 AI Studio"
	case camoufoxnative.StartupLocatingWAA:
		return 6, "定位 WAA 服务"
	case camoufoxnative.StartupBootstrappingWAA:
		return 7, "执行 WAA Bootstrap"
	}
	panic(fmt.Sprintf("未知 WAA Worker 启动阶段: %s", stage))
}

func (manager *accountWorkerManager) idleWarmVictim(excludeID string) string {
	return manager.idleWarmVictimFor(excludeID, "", false)
}

// reserveVictim 标记已被某次替换选中的旧 Worker
func (manager *accountWorkerManager) reserveVictim(accountID string) {
	manager.victimMu.Lock()
	manager.victims[accountID] = struct{}{}
	manager.victimMu.Unlock()
}

// releaseVictim 解除旧 Worker 的替换标记
func (manager *accountWorkerManager) releaseVictim(accountID string) {
	manager.victimMu.Lock()
	delete(manager.victims, accountID)
	manager.victimMu.Unlock()
}

func (manager *accountWorkerManager) victimReserved(accountID string) bool {
	manager.victimMu.Lock()
	defer manager.victimMu.Unlock()
	_, reserved := manager.victims[accountID]
	return reserved
}

// idleWarmVictimFor 选择空闲热 Worker，优先冷却中的账户，其次最久未用；coolingOnly 时只返回冷却中的账户
func (manager *accountWorkerManager) idleWarmVictimFor(excludeID string, modelID string, coolingOnly bool) string {
	warm := manager.WarmAccountIDs()
	var selected string
	var selectedUsed time.Time
	selectedCooling := false
	for _, accountID := range warm {
		if accountID == excludeID || manager.victimReserved(accountID) {
			continue
		}
		manager.mu.RLock()
		account := manager.accounts[accountID]
		manager.mu.RUnlock()
		if account == nil {
			continue
		}
		account.mu.Lock()
		cleanupPending := accountWorkerCleanupPending(account)
		account.mu.Unlock()
		if cleanupPending {
			continue
		}
		busy, lastUsed := manager.pool.Activity(accountID)
		if busy {
			continue
		}
		cooling := manager.pool.AccountCoolingDown(accountID, modelID)
		if coolingOnly && !cooling {
			continue
		}
		if selected == "" || cooling && !selectedCooling || cooling == selectedCooling && lastUsed.Before(selectedUsed) {
			selected = accountID
			selectedUsed = lastUsed
			selectedCooling = cooling
		}
	}
	return selected
}

func (manager *accountWorkerManager) evictIdleWorker(ctx context.Context, accountID string) (bool, error) {
	evictionCtx, cancel := context.WithTimeout(ctx, workerEvictionTimeout)
	defer cancel()
	lease, err := manager.pool.AcquireAccount(evictionCtx, accountID)
	if err != nil {
		if ctx.Err() == nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			return false, nil
		}
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, errors.Join(err, lease.Release())
	}
	resetErr := manager.Reset(accountID)
	releaseErr := lease.Release()
	return true, errors.Join(resetErr, releaseErr)
}

func (manager *accountWorkerManager) ensureWorker(
	ctx context.Context,
	accountID string,
	modelID string,
	waitForOpening bool,
) (aistudio.ProtectedPreparer, error) {
	workerCtx, cancel := context.WithCancel(ctx)
	stopLifecycle := context.AfterFunc(manager.lifecycle, cancel)
	defer func() {
		stopLifecycle()
		cancel()
	}()
	ctx = workerCtx
	bootstrapModel, err := manager.pool.BootstrapModel(accountID)
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		manager.rebalanceMu.Lock()
		if opening := manager.openings[accountID]; opening != nil {
			manager.rebalanceMu.Unlock()
			if !waitForOpening {
				return nil, errAccountWorkerOpening
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-opening:
				continue
			}
		}
		preparer, ready, err := manager.readyWorker(accountID, bootstrapModel)
		if err != nil {
			manager.rebalanceMu.Unlock()
			return nil, err
		}
		if ready {
			manager.rebalanceMu.Unlock()
			return preparer, nil
		}
		occupancy := manager.occupiedWorkers()
		occupied := make(map[string]struct{}, len(occupancy.accountIDs)+len(manager.openings))
		for _, occupiedAccountID := range occupancy.accountIDs {
			occupied[occupiedAccountID] = struct{}{}
		}
		for openingAccountID := range manager.openings {
			occupied[openingAccountID] = struct{}{}
			occupancy.slots++
		}
		_, replacing := occupied[accountID]
		if replacing || occupancy.slots < manager.activeLimit() {
			opening := make(chan struct{})
			manager.openings[accountID] = opening
			manager.rebalanceMu.Unlock()
			preparer, err := manager.startAuthenticatedWorker(ctx, accountID, bootstrapModel)
			manager.rebalanceMu.Lock()
			if err == nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					err = errors.Join(ctxErr, discardAccountWorker(preparer))
				} else {
					err = activateAccountWorker(preparer)
				}
			}
			manager.finishOpening(accountID, opening)
			warmCount := len(manager.WarmAccountIDs())
			manager.rebalanceMu.Unlock()
			if err == nil && warmCount > manager.warmLimit() {
				manager.requests.log("service", "INFO", fmt.Sprintf(
					"WAA Worker 按需扩容 | Worker=%d/%d", warmCount, manager.activeLimit(),
				))
			}
			return preparer, err
		}
		victim := ""
		if len(manager.openings) == 0 {
			victim = manager.idleWarmVictimFor(accountID, modelID, false)
		} else {
			victim = manager.idleWarmVictimFor(accountID, modelID, true)
		}
		if victim == "" {
			manager.rebalanceMu.Unlock()
			if !waitForOpening {
				return nil, errAccountWorkerCapacity
			}
			if err := waitWarmCandidate(ctx, 100*time.Millisecond); err != nil {
				return nil, err
			}
			continue
		}
		manager.reserveVictim(victim)
		defer func() { manager.releaseVictim(victim) }()
		opening := make(chan struct{})
		manager.openings[accountID] = opening
		manager.rebalanceMu.Unlock()
		pending, startErr := manager.startAuthenticatedWorker(ctx, accountID, bootstrapModel)
		if startErr != nil {
			manager.rebalanceMu.Lock()
			manager.finishOpening(accountID, opening)
			manager.rebalanceMu.Unlock()
			return nil, startErr
		}
		for {
			manager.rebalanceMu.Lock()
			if ctxErr := ctx.Err(); ctxErr != nil {
				discardErr := discardAccountWorker(pending)
				manager.finishOpening(accountID, opening)
				manager.rebalanceMu.Unlock()
				return nil, errors.Join(ctxErr, discardErr)
			}
			if victim == "" {
				victim = manager.idleWarmVictimFor(accountID, modelID, false)
				if victim != "" {
					manager.reserveVictim(victim)
				}
			}
			if victim == "" {
				manager.rebalanceMu.Unlock()
				if err := waitWarmCandidate(ctx, 100*time.Millisecond); err != nil {
					manager.rebalanceMu.Lock()
					discardErr := discardAccountWorker(pending)
					manager.finishOpening(accountID, opening)
					manager.rebalanceMu.Unlock()
					return nil, errors.Join(err, discardErr)
				}
				continue
			}
			evicted, evictionErr := manager.evictIdleWorker(ctx, victim)
			if evictionErr == nil && evicted {
				activationErr := activateAccountWorker(pending)
				manager.finishOpening(accountID, opening)
				warmCount := len(manager.WarmAccountIDs())
				manager.rebalanceMu.Unlock()
				if activationErr != nil {
					return nil, activationErr
				}
				if warmCount > manager.warmLimit() {
					manager.requests.log("service", "INFO", fmt.Sprintf(
						"WAA Worker 按需替换 | Worker=%d/%d", warmCount, manager.activeLimit(),
					))
				}
				return pending, nil
			}
			if evictionErr != nil || ctx.Err() != nil {
				discardErr := discardAccountWorker(pending)
				manager.finishOpening(accountID, opening)
				manager.rebalanceMu.Unlock()
				return nil, errors.Join(evictionErr, ctx.Err(), discardErr)
			}
			manager.releaseVictim(victim)
			victim = ""
			manager.rebalanceMu.Unlock()
		}
	}
}

// startAuthenticatedWorker 在启动锁释放后恢复同一账户的认证
func (manager *accountWorkerManager) startAuthenticatedWorker(ctx context.Context, accountID, model string) (*accountWorkerPreparer, error) {
	lease, leased := aistudio.AccountLeaseFromContext(ctx)
	if !leased {
		var err error
		lease, err = manager.pool.AcquireFor(ctx, aistudio.AccountSelection{AccountID: accountID})
		if err != nil {
			return nil, err
		}
		defer lease.Release()
		ctx = aistudio.ContextWithAccountLease(ctx, lease)
	}
	if lease.Account().ID != accountID {
		return nil, fmt.Errorf("Worker 与账户租约不匹配: %s", accountID)
	}
	if _, busyErr := manager.runtimeAvailable([]string{accountID}); busyErr != nil {
		return nil, busyErr
	}
	generation := lease.AuthGeneration()
	worker, err := manager.startReservedWorker(ctx, accountID, model)
	if !aistudio.DefinitiveAuthenticationFailure(err) {
		return worker, err
	}
	if manager.refresher != nil {
		if recoverErr := manager.refresher.Recover(ctx, err, generation); recoverErr == nil {
			worker, err = manager.startReservedWorker(ctx, accountID, model)
			if err == nil {
				return worker, nil
			}
			err = manager.refresher.markAuthenticationRequired(ctx, err)
		} else {
			err = recoverErr
		}
	} else if lease, ok := aistudio.AccountLeaseFromContext(ctx); ok && ctx.Err() == nil {
		err = errors.Join(err, lease.MarkAuthenticationRequired(err.Error()))
	}
	return nil, &accountWorkerInitError{err: err, authHandled: true}
}

func (manager *accountWorkerManager) promote(ctx context.Context, accountID string, modelID string) (aistudio.ProtectedPreparer, error) {
	return manager.ensureWorker(ctx, accountID, modelID, false)
}

func (manager *accountWorkerManager) withoutOpening(accountIDs []string) ([]string, bool) {
	manager.rebalanceMu.Lock()
	defer manager.rebalanceMu.Unlock()
	result := make([]string, 0, len(accountIDs))
	pending := false
	for _, accountID := range accountIDs {
		if manager.openings[accountID] != nil {
			pending = true
			continue
		}
		result = append(result, accountID)
	}
	return result, pending
}

// StartPrewarm 启动有界预热并在当前可用账户完成后返回
func (manager *accountWorkerManager) StartPrewarm(ctx context.Context) <-chan error {
	first := make(chan error, 1)
	manager.mu.RLock()
	closed := manager.closed
	manager.mu.RUnlock()
	if closed {
		first <- fmt.Errorf("WAA worker manager 已关闭")
		close(first)
		return first
	}
	if !manager.fillMu.TryLock() {
		if len(manager.WarmAccountIDs()) > 0 {
			first <- nil
		} else {
			first <- fmt.Errorf("WAA 预热已在进行")
		}
		close(first)
		return first
	}
	fillContext, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(manager.lifecycle, cancel)
	go manager.fillWarm(fillContext, first, func() {
		stop()
		cancel()
	})
	return first
}

// browserLaunchedKey 是 context 中 Worker 的浏览器进程完成启动、开始连接 WebDriver BiDi 或载入 AI Studio 时调用的函数的键
type browserLaunchedKey struct{}

// fillWarm 按启动名额补足常驻 Worker，候选账户的目录就绪后立即启动，不等待已在启动的 Worker
func (manager *accountWorkerManager) fillWarm(ctx context.Context, first chan<- error, cleanup func()) {
	startedAt := time.Now()
	defer cleanup()
	defer manager.fillMu.Unlock()
	defer close(first)
	notified := false
	notify := func(err error) {
		if notified {
			return
		}
		notified = true
		first <- err
	}
	var failures []error
	failedAccounts := make(map[string]struct{})
	starting := make(map[string]struct{})
	launched := 0
	head := ""
	var headLaunched chan struct{}
	results := make(chan warmResult)
	collect := func(result warmResult) {
		delete(starting, result.accountID)
		if result.err == nil {
			notify(nil)
			return
		}
		if ctx.Err() != nil {
			return
		}
		failures = append(failures, fmt.Errorf("预热账户 %s: %w", result.accountID, result.err))
		failedAccounts[result.accountID] = struct{}{}
	}
	defer func() {
		for len(starting) > 0 {
			collect(<-results)
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			if !notified {
				notify(errors.Join(append(failures, err)...))
			}
			return
		}
		warm := manager.WarmAccountIDs()
		limit := manager.warmLimit()
		if len(warm) >= limit {
			if launched > 0 {
				manager.requests.log("service", "INFO", fmt.Sprintf(
					"WAA Worker 预热完成 | Worker=%d/%d | 耗时=%s",
					len(warm), manager.PrewarmTarget(), time.Since(startedAt).Round(time.Millisecond),
				))
			}
			notify(nil)
			return
		}
		launch := min(manager.startup.size(), limit-len(warm)) - len(starting)
		if head != "" {
			select {
			case <-headLaunched:
				head, headLaunched = "", nil
			default:
				if _, ok := starting[head]; !ok {
					head, headLaunched = "", nil
				}
			}
		}
		if head != "" {
			launch = 0
		}
		groups, err := manager.classifyBootstrapCandidates(ctx, warm)
		var runtimeBusyErr error
		if err != nil {
			failures = append(failures, err)
		} else {
			skipped := maps.Clone(failedAccounts)
			maps.Copy(skipped, starting)
			groups.StandbyReady = excludeAccountIDs(groups.StandbyReady, skipped)
			groups.StandbyBusy = excludeAccountIDs(groups.StandbyBusy, skipped)
			var readyBusyErr, standbyBusyErr error
			groups.StandbyReady, readyBusyErr = manager.runtimeAvailable(groups.StandbyReady)
			groups.StandbyBusy, standbyBusyErr = manager.runtimeAvailable(groups.StandbyBusy)
			runtimeBusyErr = errors.Join(readyBusyErr, standbyBusyErr)
		}
		pendingBusy := err == nil && len(groups.StandbyBusy) > 0
		if err == nil {
			ready := groups.StandbyReady[:max(0, min(launch, len(groups.StandbyReady)))]
			workerCtx := ctx
			if len(ready) > 0 && launched == 0 && len(warm) == 0 {
				ready = ready[:1]
				head, headLaunched = ready[0], make(chan struct{})
				workerCtx = context.WithValue(ctx, browserLaunchedKey{}, sync.OnceFunc(func() { close(headLaunched) }))
			}
			for _, accountID := range ready {
				starting[accountID] = struct{}{}
				launched++
				go func() {
					_, err := manager.Worker(workerCtx, accountID, "")
					results <- warmResult{accountID: accountID, err: err}
				}()
			}
		}
		if len(starting) > 0 {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
			case result := <-results:
				collect(result)
			case <-headLaunched:
			case <-timer.C:
			}
			timer.Stop()
			continue
		}
		if pendingBusy {
			if err := waitWarmCandidate(ctx, 100*time.Millisecond); err != nil && !notified {
				notify(errors.Join(append(failures, err)...))
			}
			continue
		}
		warm = manager.WarmAccountIDs()
		if len(warm) > 0 {
			if launched > 0 {
				manager.requests.log("service", "INFO", fmt.Sprintf(
					"WAA Worker 预热完成 | Worker=%d/%d | 失败=%d | 耗时=%s",
					len(warm), manager.PrewarmTarget(), len(failures), time.Since(startedAt).Round(time.Millisecond),
				))
			}
			notify(nil)
			return
		}
		if err == nil && runtimeBusyErr == nil && !groups.EarliestCooldown.Until.IsZero() {
			if !notified {
				manager.requests.log("service", "INFO", fmt.Sprintf(
					"WAA Worker 预热等待账户冷却 | 恢复=%s", groups.EarliestCooldown.Until.Local().Format(time.RFC3339),
				))
			}
			notify(nil)
			if err := waitWarmCandidate(ctx, min(time.Until(groups.EarliestCooldown.Until), schedulingRecheck)); err != nil {
				return
			}
			continue
		}
		if runtimeBusyErr != nil {
			failures = append(failures, runtimeBusyErr)
		}
		if len(failures) == 0 {
			failures = append(failures, aistudio.ErrNoEligibleAccount)
		}
		notify(fmt.Errorf("没有可预热的账户: %w", errors.Join(failures...)))
		return
	}
}

type warmResult struct {
	accountID string
	err       error
}

func excludeAccountIDs(accountIDs []string, excluded map[string]struct{}) []string {
	result := make([]string, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		if _, exists := excluded[accountID]; !exists {
			result = append(result, accountID)
		}
	}
	return result
}

func (manager *accountWorkerManager) waitPrewarm() {
	manager.fillMu.Lock()
	manager.fillMu.Unlock()
}

// Close 关闭全部账户 worker
func (manager *accountWorkerManager) Close() error {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return nil
	}
	manager.closed = true
	manager.cancel()
	accounts := make([]*accountWorker, 0, len(manager.accounts))
	for _, account := range manager.accounts {
		accounts = append(accounts, account)
	}
	manager.mu.Unlock()
	manager.waitPrewarm()
	closeResults := make(chan error, len(accounts))
	var closes sync.WaitGroup
	for _, account := range accounts {
		closes.Add(1)
		go func(account *accountWorker) {
			defer closes.Done()
			account.startupMu.Lock()
			defer account.startupMu.Unlock()
			account.mu.Lock()
			defer account.mu.Unlock()
			if account.worker != nil || account.cleanupWorker != nil || account.runtimeLease != nil || account.cleanupLease != nil {
				closeResults <- manager.closeAccountWorker(account)
			}
		}(account)
	}
	closes.Wait()
	close(closeResults)
	var closeErrors []error
	for closeErr := range closeResults {
		closeErrors = append(closeErrors, closeErr)
	}
	return errors.Join(closeErrors...)
}

func (manager *accountWorkerManager) closeAccountWorker(account *accountWorker) error {
	var closeErrors []error
	if account.worker != nil {
		if closeErr := account.worker.Close(); closeErr != nil {
			closeErrors = append(closeErrors, closeErr)
		} else {
			account.worker = nil
			account.generation.Add(1)
		}
	}
	if account.cleanupWorker != nil {
		if closeErr := account.cleanupWorker.Close(); closeErr != nil {
			closeErrors = append(closeErrors, closeErr)
		} else {
			account.cleanupWorker = nil
		}
	}
	if account.cleanupWorker == nil && account.cleanupLease != nil {
		if releaseErr := account.cleanupLease.Release(); releaseErr != nil {
			closeErrors = append(closeErrors, releaseErr)
		} else {
			account.cleanupLease = nil
		}
	}
	if account.worker == nil && account.cleanupWorker == nil && account.cleanupLease == nil && account.runtimeLease != nil {
		if releaseErr := account.runtimeLease.Release(); releaseErr != nil {
			closeErrors = append(closeErrors, releaseErr)
		} else {
			account.runtimeLease = nil
		}
	}
	if account.worker == nil && account.cleanupWorker == nil && account.runtimeLease == nil && account.cleanupLease == nil {
		account.warm.Store(false)
		manager.notifyScheduling()
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

type accountHeaderUpdate struct {
	provider  *accountHeaderProvider
	accountID string
	state     *accountHeaderState
	pending   bool
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
			provider.Close()
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

// prepareUpdate 创建待发布的账户固定出口
func (provider *accountHeaderProvider) prepareUpdate(
	account *aistudio.Account,
	config aistudio.AccountConfig,
) (*accountHeaderUpdate, error) {
	if account == nil {
		return nil, fmt.Errorf("账户未初始化")
	}
	provider.mu.RLock()
	current := provider.accounts[account.ID]
	provider.mu.RUnlock()
	if current == nil {
		return nil, fmt.Errorf("账户固定出口不存在: %s", account.ID)
	}
	proxy := strings.TrimSpace(config.Proxy)
	if proxy == "" {
		proxy = account.PoolProxy(provider.globalProxy)
	}
	client, err := aistudio.NewProxyHTTPClient(proxy)
	if err != nil {
		return nil, fmt.Errorf("创建账户 %s 的固定出口: %w", account.ID, err)
	}
	return &accountHeaderUpdate{
		provider: provider, accountID: account.ID, state: &accountHeaderState{client: client}, pending: true,
	}, nil
}

// Commit 发布已准备的账户固定出口
func (update *accountHeaderUpdate) Commit() {
	if update == nil || !update.pending {
		return
	}
	update.provider.mu.Lock()
	current := update.provider.accounts[update.accountID]
	update.provider.accounts[update.accountID] = update.state
	update.provider.mu.Unlock()
	update.pending = false
	current.client.CloseIdleConnections()
}

// Discard 关闭未发布的账户固定出口
func (update *accountHeaderUpdate) Discard() {
	if update == nil || !update.pending {
		return
	}
	update.pending = false
	update.state.client.CloseIdleConnections()
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

// Close 关闭全部账户固定出口
func (provider *accountHeaderProvider) Close() {
	provider.mu.Lock()
	accounts := provider.accounts
	provider.accounts = nil
	provider.mu.Unlock()
	for _, account := range accounts {
		account.client.CloseIdleConnections()
	}
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
	stateRefreshMu       sync.Mutex
	stateRefreshAt       map[string]time.Time
	// hedgeAuto 与 hedgeAfter 是首事件超时备用请求的自适应开关与自定义等待
	hedgeAuto  bool
	hedgeAfter time.Duration
	// hedges 是正在进行的备用请求数
	hedges atomic.Int32
}

type modelCatalogService interface {
	RefreshAccountModels(context.Context, string) ([]aistudio.Model, error)
	RefreshAccountState(context.Context, string, bool) (aistudio.AccountRefresh, error)
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
		timeout: timeout, modelRetries: make(map[string]struct{}), stateRefreshAt: make(map[string]time.Time),
	}
}

// serviceStoppedError 表示生成服务未运行，launching 表示正在启动
type serviceStoppedError struct {
	launching bool
}

var errServiceTransitioning = errors.New("生成服务正在切换状态")

const (
	serviceStopped int32 = iota
	serviceLaunching
	serviceRunning
	modelCatalogRetryInterval        = 30 * time.Second
	accountStateRefreshInterval      = 10 * time.Minute
	modelCatalogShutdownTimeout      = 2 * time.Second
	serviceTransitionShutdownTimeout = 12 * time.Second
)

func (e *serviceStoppedError) Error() string {
	if e.launching {
		return "AIStudio2API 生成服务正在启动"
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
	if launching != nil {
		launching()
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
	case err, ok := <-firstWarm:
		warmErr = err
		if !ok {
			warmErr = fmt.Errorf("WAA 预热未返回就绪账户")
		}
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
		return nil, &serviceStoppedError{launching: service.state.Load() == serviceLaunching}
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

// startModelCatalogRefresh 并发刷新全部账户并持续处理失败目录
func (service *trackedService) startModelCatalogRefresh(ctx context.Context) (<-chan error, <-chan struct{}, <-chan struct{}) {
	ready := make(chan error, 1)
	synced := make(chan struct{})
	done := make(chan struct{})
	accountIDs := service.modelCatalogAccountIDs()
	go func() {
		defer close(done)
		startedAt := time.Now()
		firstReady := false
		synchronized := 0
		refreshed := 0
		failures := make([]error, 0)
		authRequiredBefore := service.authRequiredAccountIDs()
		for result := range service.refreshAccountModelCatalogs(ctx, accountIDs, false) {
			if result.err != nil {
				failures = append(failures, fmt.Errorf("账户 %s: %w", result.accountID, result.err))
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
				launchErr = errors.Join(failures...)
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
			"模型目录后台同步完成 | 同步=%d | 非空=%d | 模型=%d | 待重试账户=%d | 耗时=%s",
			synchronized, refreshed, len(service.modelSnapshot()), service.pendingModelRetryCount(), time.Since(startedAt).Round(time.Millisecond),
		))
		service.retryModelCatalogs(ctx)
	}()
	return ready, synced, done
}

func (service *trackedService) modelCatalogAccountIDs() []string {
	statuses := service.pool.Status()
	accountIDs := make([]string, 0, len(statuses))
	for _, status := range statuses {
		if status.Enabled && (status.State == aistudio.AccountReady || status.State == aistudio.AccountBusy) {
			accountIDs = append(accountIDs, status.ID)
		}
	}
	sort.Strings(accountIDs)
	return accountIDs
}

func (service *trackedService) refreshAccountModelCatalogs(
	ctx context.Context,
	accountIDs []string,
	pendingOnly bool,
) <-chan accountModelRefreshResult {
	results := make(chan accountModelRefreshResult, len(accountIDs))
	var refreshes sync.WaitGroup
	for _, accountID := range accountIDs {
		refreshes.Add(1)
		go func(accountID string) {
			defer refreshes.Done()
			if pendingOnly && !service.modelRetryPending(accountID) {
				results <- accountModelRefreshResult{accountID: accountID, skipped: true}
				return
			}
			models, err := service.refreshAccountModelCatalog(ctx, accountID)
			results <- accountModelRefreshResult{accountID: accountID, models: models, err: err}
		}(accountID)
	}
	go func() {
		refreshes.Wait()
		close(results)
	}()
	return results
}

func (service *trackedService) refreshAccountModelCatalog(ctx context.Context, accountID string) ([]aistudio.Model, error) {
	requestCtx, cancel := service.lifecycleRequestContext(ctx)
	defer cancel()
	models, err := service.catalog.RefreshAccountModels(requestCtx, accountID)
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
	models, err := service.refreshAccountModelCatalog(ctx, accountID)
	service.modelSyncMu.Lock()
	service.applyCachedModelCatalog()
	service.modelSyncMu.Unlock()
	return models, err
}

// retryModelCatalogs 每 30 秒重试待同步账户的目录，每 10 分钟巡检启用账户的权益与目录
func (service *trackedService) retryModelCatalogs(ctx context.Context) {
	ticker := time.NewTicker(modelCatalogRetryInterval)
	defer ticker.Stop()
	stateTicker := time.NewTicker(accountStateRefreshInterval)
	defer stateTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stateTicker.C:
			service.refreshAccountStates(ctx)
		case <-ticker.C:
			if !service.retryAccountModelCatalogs(ctx) {
				return
			}
		}
	}
}

// refreshAccountStates 并发读取启用账户的权益与模型目录，没有空闲槽位的账户跳过本轮
func (service *trackedService) refreshAccountStates(ctx context.Context) {
	var refreshes sync.WaitGroup
	for _, accountID := range service.modelCatalogAccountIDs() {
		refreshes.Go(func() { _, _ = service.refreshAccountState(ctx, accountID, false) })
	}
	refreshes.Wait()
}

// refreshAccountState 读取账户权益与模型目录，失败或变化时发布账户与模型事件并记录日志
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

// refreshAccountStateAfterLimit 在订阅模型撞到额度限制后于后台读取该账户权益与目录，同一账户每分钟最多一次
func (service *trackedService) refreshAccountStateAfterLimit(accountID string) {
	now := time.Now()
	service.stateRefreshMu.Lock()
	if now.Sub(service.stateRefreshAt[accountID]) < time.Minute {
		service.stateRefreshMu.Unlock()
		return
	}
	service.stateRefreshAt[accountID] = now
	service.stateRefreshMu.Unlock()
	go func() { _, _ = service.refreshAccountState(service.lifecycle, accountID, false) }()
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
	request.Model = service.pool.CanonicalModelID(model)
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
	titles, ok := service.service.(aistudio.TitleService)
	if !ok {
		return "", fmt.Errorf("title service 不可用")
	}
	request.Model = service.pool.CanonicalModelID(service.mappedModel(request.Model))
	requestCtx, cancel, err := service.dataRequestContext(ctx)
	if err != nil {
		return "", err
	}
	defer cancel()
	return titles.GenerateTitle(requestCtx, request)
}

// GenerateVideo 创建一个 Veo 长任务
func (service *trackedService) GenerateVideo(ctx context.Context, request aistudio.VideoRequest) (aistudio.VideoOperation, error) {
	request.Model = service.mappedModel(request.Model)
	api.SetAccessLogTarget(ctx, request.Model, "")
	request.Model = service.pool.CanonicalModelID(request.Model)
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
	workerGenerations := make(map[string]uint64)
	recoveredWorkers := make(map[string]struct{})
	selectedAccountID := ""
	selectedAccountLabel := ""
	selectedAccessGeneration := uint64(0)
	requestCtx = aistudio.ContextWithAccountSelectionObserver(requestCtx, func(account *aistudio.Account) {
		workerGenerations[account.ID] = service.workers.WorkerGeneration(account.ID)
		selectedAccountID = account.ID
		selectedAccountLabel = account.Config.Label
		selectedAccessGeneration = service.pool.ModelAccessGeneration(account.ID)
		api.SetAccessLogTarget(requestCtx, request.Model, account.Config.Label)
	})
	request.RecoverWAARuntime = func(recoveryCtx context.Context, accountID string, cause error) (bool, error) {
		workerFailed := service.workers.WorkerFailed(accountID)
		workerReplaced := errors.Is(cause, errAccountWorkerReplaced)
		recoverCurrentGeneration := needsWAARuntimeRecovery(cause, false, workerFailed, workerReplaced)
		if recoveryCtx.Err() != nil || !recoverCurrentGeneration {
			return false, nil
		}
		waaRuntimeFailed := aistudio.DefinitiveWAARuntimeFailure(cause)
		expectedGeneration := workerGenerations[accountID]
		recovered, _, recoveryErr := service.recoverWorkerOnce(
			recoveryCtx, accountID, expectedGeneration, recoveredWorkers,
			recoverCurrentGeneration, workerFailed || waaRuntimeFailed,
		)
		return recovered, recoveryErr
	}
	request.TaskContext = func() (context.Context, context.CancelFunc, error) {
		return service.dataRequestContext(context.Background())
	}
	video, ok := service.service.(aistudio.VideoService)
	if !ok {
		return aistudio.VideoOperation{}, fmt.Errorf("video service 不可用")
	}
	operation, requestErr := video.GenerateVideo(requestCtx, request)
	if requestErr == nil && selectedAccountID != "" && !operation.ModelAccessCheckedAt().IsZero() {
		service.markModelAccessVerifiedAsync(
			selectedAccountID, selectedAccountLabel,
			strings.TrimPrefix(strings.TrimSpace(request.Model), "models/"),
			selectedAccessGeneration, operation.ModelAccessCheckedAt(),
		)
	}
	api.SetAccessLogError(requestCtx, requestErr)
	return operation, requestErr
}

// videoCooling 在视频任务的全部候选账户都冷却且最早恢复晚于排队上限时返回 AllCoolingError
func (service *trackedService) videoCooling(ctx context.Context, request aistudio.VideoRequest) error {
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	groups, err := service.pool.ClassifyCandidates(ctx, aistudio.AccountSelection{
		ModelID: modelID, Method: service.pool.VideoMethod(modelID), AccountID: strings.TrimSpace(request.AccountID),
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

func needsWAARuntimeRecovery(cause error, generationChanged bool, workerFailed bool, workerReplaced bool) bool {
	return generationChanged || workerFailed || workerReplaced ||
		aistudio.DefinitiveWAARuntimeFailure(cause)
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
		reset, err := service.workers.ResetIfGeneration(ctx, accountID, expectedGeneration)
		if err != nil {
			return false, false, err
		}
		if !reset {
			recoveredWorkers[accountID] = struct{}{}
			return true, true, nil
		}
	}
	recoveredWorkers[accountID] = struct{}{}
	return true, true, nil
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

// ListVideos 列出视频任务
func (service *trackedService) ListVideos(ctx context.Context) ([]aistudio.VideoOperation, error) {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, "")
	if err != nil {
		return nil, err
	}
	defer cancel()
	video, ok := service.service.(aistudio.VideoService)
	if !ok {
		return nil, fmt.Errorf("video service 不可用")
	}
	videos, requestErr := video.ListVideos(requestCtx)
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
	video, ok := service.service.(aistudio.VideoService)
	if !ok {
		return fmt.Errorf("video service 不可用")
	}
	requestErr := video.DeleteVideo(requestCtx, operationID)
	api.SetAccessLogError(requestCtx, requestErr)
	return requestErr
}

// DownloadFile 下载生成任务绑定的 Drive 文件
func (service *trackedService) DownloadFile(ctx context.Context, fileID string) (aistudio.MediaStream, error) {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, "")
	if err != nil {
		return aistudio.MediaStream{}, err
	}
	video, ok := service.service.(aistudio.VideoService)
	if !ok {
		cancel()
		return aistudio.MediaStream{}, fmt.Errorf("video service 不可用")
	}
	media, requestErr := video.DownloadFile(requestCtx, fileID)
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

func (service *trackedService) acquireWarmLease(ctx context.Context, selection aistudio.AccountSelection) (*aistudio.AccountLease, error) {
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
			_, promoteErr := service.workers.promote(aistudio.ContextWithAccountLease(ctx, lease), accountID, selection.ModelID)
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
			if remaining > cooldownQueueLimit {
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
			if aistudio.DefinitiveAuthenticationFailure(promoteFailure) {
				return nil, errors.Join(service.pool.NoEligibleError(selection), promoteFailure)
			}
			return nil, promoteFailure
		}
		if runtimeBusyErr != nil {
			return nil, runtimeBusyErr
		}
		return nil, service.pool.NoEligibleError(selection)
	}
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

func waitWarmCandidate(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
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
	if activity == nil {
		return "网络字节=0"
	}
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
	request.Model = service.pool.CanonicalModelID(request.Model)
	requestCtx, cancel, err := service.dataRequestContextFor(ctx, service.generateTimeout(request))
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
		service.requests.finish(request.ID, "failed", err)
		return nil, err
	}
	service.requests.start(request, cancel)
	resourceID, err := service.pool.ResourceIDForContents(requestCtx, request.Contents)
	if err != nil {
		api.SetAccessLogError(requestCtx, err)
		cancel()
		service.requests.finish(request.ID, finalRequestState(err), err)
		return nil, err
	}
	events := make(chan aistudio.Event, 8)
	go service.generateWithRetry(ctx, requestCtx, cancel, generationStartedAt, request, resourceID, events)
	return events, nil
}

// generateTimeout 返回生成请求的期限，自动续写请求按续写轮数倍增
func (service *trackedService) generateTimeout(request aistudio.GenerateRequest) time.Duration {
	return service.timeout * time.Duration(service.pool.OutputContinuationRounds(request.Model, request.Config.MaxOutputTokens))
}

// generateAttempt 是一次上游尝试的账户租约、准备与发送、等待首事件的结果，done 关闭后结果字段可读
type generateAttempt struct {
	lease            *aistudio.AccountLease
	selection        aistudio.AccountSelection
	hedge            bool
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
	assistant := service.pool.AssistantModel(modelID)
	hedgeable := !request.Background && !assistant && !service.pool.InteractionAgentModel(modelID) && (service.hedgeAuto || service.hedgeAfter > 0)
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
			AccountID: selectionAccountID, ResourceID: selectionResourceID,
		}
		if resourceID != "" || request.Config.SpeechConfig != nil && request.Config.SpeechConfig.Mode != "" {
			selection.PlaygroundOnly = true
		}
		if continuationRounds > 1 {
			selection.PlaygroundOnly = true
		}
		if !assistant && !selection.PlaygroundOnly && service.pool.BuildEnabled() && aistudio.RequestNeedsBuild(request) {
			selection.Channel = aistudio.ChannelBuild
			selection.BuildOnly = retry.buildOnly
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
			enabled, _ := service.pool.EnabledAccounts()
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
		lease, acquireErr := service.acquireWarmLease(requestCtx, selection)
		var buildCooling *aistudio.AllCoolingError
		if acquireErr != nil && selection.Channel == aistudio.ChannelBuild && requestCtx.Err() == nil && !aistudio.RequestBuildOnly(request) &&
			(errors.Is(acquireErr, aistudio.ErrNoEligibleAccount) || errors.As(acquireErr, &buildCooling)) {
			// 没有账户能经 Build 服务时改用 Playground，Playground 无法表达的设置按其最接近的形式发送
			selection.Channel = ""
			fallbackReason = "Build 通道没有可用账户"
			lease, acquireErr = service.acquireWarmLease(requestCtx, selection)
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
		primary := service.startAttempt(requestCtx, request, resourceID, modelID, fallbackReason, lease, selection, false)
		var plan *hedgePlan
		if hedgeable && !hedged {
			if delay, ok := service.hedgeDelay(modelID, string(lease.Channel())); ok {
				fixed := resourceID != "" || requestedAccountID != ""
				plan = &hedgePlan{delay: delay, selections: service.hedgeSelections(selection, lease.Account().ID, fixed, retry.attempted)}
				plan.start = func(hedgeLease *aistudio.AccountLease, hedgeSelection aistudio.AccountSelection) *generateAttempt {
					hedged = true
					retry.attempted[hedgeLease.Account().ID] = struct{}{}
					return service.startAttempt(requestCtx, request, resourceID, modelID, fallbackReason, hedgeLease, hedgeSelection, true)
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
				service.markAttemptAccount(requestCtx, request.ID, modelID, winner.lease)
			}
			api.SetAccessLogFirstEvent(requestCtx, winner.firstAt.Sub(generationStartedAt))
			api.SetAccessLogTarget(requestCtx, winner.first.ProviderModel, winner.accountLabel)
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
		api.SetAccessLogError(requestCtx, err)
		service.requests.finish(request.ID, finalRequestState(err), err)
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
		winner.first, winner.source, destination, winner.lease, winner.copies, winner.activity, modelID,
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

// startAttempt 在租约账户上准备并发送请求，后台等待首事件，结束时关闭 done
func (service *trackedService) startAttempt(
	requestCtx context.Context,
	request aistudio.GenerateRequest,
	resourceID string,
	modelID string,
	fallbackReason string,
	lease *aistudio.AccountLease,
	selection aistudio.AccountSelection,
	hedge bool,
) *generateAttempt {
	request.AccountID = lease.Account().ID
	attempt := &generateAttempt{
		lease: lease, selection: selection, hedge: hedge, accountLabel: lease.Account().Config.Label,
		proxy: service.proxies.Binding(request.AccountID), workerGeneration: service.workers.WorkerGeneration(request.AccountID),
		startedAt: time.Now(), activity: &upstreamActivity{}, done: make(chan struct{}),
	}
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
	originalContents := request.Contents
	ctx = aistudio.ContextWithAccountLease(ctx, lease)
	ctx = aistudio.ContextWithUpstreamModeObserver(ctx, func(method, mode, reason string) {
		level := "INFO"
		message := fmt.Sprintf("上游调用 | 通道=%s | 模式=%s | RPC=%s", lease.Channel(), mode, method)
		if reason != "" && service.pool.BuildEnabled() {
			level = "WARN"
			if fallbackReason != "" {
				reason = fallbackReason + "; " + reason
			}
			message += " | 回退流式 | 原因=" + reason
		}
		service.requests.logRequestProgress(request.ID, accountLabel, level, message)
	})
	var err error
	copiedFileCount := 0
	if resourceID != "" {
		fileCopies, ok := service.service.(interface {
			CopyFileReferencesToLease(context.Context, *aistudio.AccountLease, []aistudio.Content) ([]aistudio.Content, *aistudio.TemporaryFileCopies, error)
		})
		if !ok {
			err = fmt.Errorf("文件引用跨账户服务不可用")
		} else {
			request.Contents, attempt.copies, err = fileCopies.CopyFileReferencesToLease(ctx, lease, originalContents)
			if err == nil {
				copiedFileCount = attempt.copies.Count()
			}
		}
	}
	mediaCount, mediaBytes := inlineMediaInput(request.Contents)
	if err == nil && mediaCount > 0 {
		uploader, ok := service.service.(interface {
			UploadInlineMediaToLease(context.Context, *aistudio.AccountLease, []aistudio.Content, *aistudio.TemporaryFileCopies) ([]aistudio.Content, *aistudio.TemporaryFileCopies, error)
		})
		if !ok {
			err = fmt.Errorf("内联附件上传服务不可用")
		} else {
			uploadStartedAt := time.Now()
			request.Contents, attempt.copies, err = uploader.UploadInlineMediaToLease(ctx, lease, request.Contents, attempt.copies)
			if err == nil {
				service.requests.log(accountLabel, "INFO", fmt.Sprintf(
					"内联附件处理完成 | 附件=%d | 原始=%dB | 耗时=%s",
					mediaCount, mediaBytes, time.Since(uploadStartedAt).Round(time.Millisecond),
				))
			}
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
		statuses := service.pool.Status()
		for _, sourceID := range attempt.copies.SourceAccountIDs() {
			sourceLabel := sourceID
			for _, status := range statuses {
				if status.ID == sourceID {
					sourceLabel = status.Label
					break
				}
			}
			sourceLabels = append(sourceLabels, sourceLabel)
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

// settleAttempt 处理失败的尝试：记录访问日志与冷却，释放租约，mayRecover 为真时按需重建 Worker
func (service *trackedService) settleAttempt(
	requestCtx context.Context,
	requestID string,
	modelID string,
	attempt *generateAttempt,
	retry *generateRetry,
	mayRecover bool,
) attemptOutcome {
	err := attempt.err
	lease := attempt.lease
	accountID, accountLabel := lease.Account().ID, attempt.accountLabel
	if attempt.copies != nil {
		err = errors.Join(err, attempt.copies.Cleanup())
	}
	api.AddAccessLogAttempt(requestCtx, api.RequestAttempt{
		Account: accountLabel, Channel: string(lease.Channel()), Error: err.Error(),
		DurationMS: time.Since(attempt.startedAt).Milliseconds(), Hedge: attempt.hedge,
	})
	if proxyNetworkError(requestCtx, err) {
		service.reportProxyFailure(attempt.proxy, accountLabel, err)
	}
	workerFailed := service.workers.WorkerFailed(accountID)
	waaRuntimeFailed := aistudio.DefinitiveWAARuntimeFailure(err)
	workerReplaced := errors.Is(err, errAccountWorkerReplaced)
	localWorkerFailure := (workerFailed || workerReplaced) && requestCtx.Err() == nil
	retryable := retryableGenerateAccountError(requestCtx, err) || localWorkerFailure
	if aistudio.DefinitiveAuthenticationFailure(err) {
		if stateErr := lease.MarkAuthenticationRequired(err.Error()); stateErr != nil {
			err = errors.Join(err, stateErr)
			retryable = false
		}
	}
	cooldown, quotaFailure := aistudio.QuotaCooldownForError(err, time.Now())
	if quotaFailure {
		modelAccessScope := lease.CooldownScope(modelID)
		scopeLabel := modelAccessScope
		if cooldown.Global {
			modelAccessScope = ""
			scopeLabel = "全局"
		}
		until, stateErr := service.pool.MarkQuotaCooldownIfGeneration(
			accountID, modelAccessScope, lease.ModelAccessGeneration(), lease.CheckedAt(), cooldown,
		)
		if stateErr != nil {
			err = errors.Join(err, stateErr)
			retryable = false
		} else {
			if !until.IsZero() {
				service.requests.logRequestProgress(requestID, accountLabel, "WARN", fmt.Sprintf(
					"账号冷却 | 类型=%s | 范围=%s | 恢复=%s",
					cooldown.Kind, scopeLabel, until.Local().Format(time.RFC3339),
				))
			}
			if service.pool.ModelRequiresSubscription(accountID, modelID) {
				service.refreshAccountStateAfterLimit(accountID)
			}
			if !cooldown.Global && service.pool.AccountChannelAvailable(accountID, attempt.selection) {
				delete(retry.attempted, accountID)
				retry.maxAttempts++
			}
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
			requestCtx, accountID, attempt.workerGeneration, retry.recoveredWorker,
			needsWAARuntimeRecovery(err, false, workerFailed, workerReplaced),
			workerFailed || waaRuntimeFailed,
		)
		if resetErr != nil {
			err = errors.Join(err, resetErr)
			retryable = false
		}
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
			if event.Err == nil {
				event.Err = errors.New("AI Studio stream returned an empty error event")
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

func (service *trackedService) dataRequestContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	return service.dataRequestContextFor(ctx, service.timeout)
}

// dataRequestContextFor 返回绑定数据面生命周期、期限为 timeout 的请求上下文
func (service *trackedService) dataRequestContextFor(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc, error) {
	service.lifecycleMu.Lock()
	if service.state.Load() != serviceRunning || service.dataContext == nil {
		service.lifecycleMu.Unlock()
		return nil, nil, &serviceStoppedError{launching: service.state.Load() == serviceLaunching}
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
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
	temporaryCopies *aistudio.TemporaryFileCopies,
	activity *upstreamActivity,
	requestedModelID string,
) {
	state := "completed"
	var requestErr error
	terminal := false
	accountLabel := lease.Account().Config.Label
	accessGeneration := lease.ModelAccessGeneration()
	modelID := strings.TrimPrefix(strings.TrimSpace(first.ProviderModel), "models/")
	var lastEventAt time.Time
	lastEventKind := "-"
	reasoningEvents := 0
	contentEvents := 0
	var usage *aistudio.Usage
	toolCalls := 0
	stalled := false
	stallTimer := time.NewTimer(streamStallThreshold)
	if !stallTimer.Stop() {
		<-stallTimer.C
	}
	defer stallTimer.Stop()
	var stall <-chan time.Time
	resetStallTimer := func() {
		if !stallTimer.Stop() {
			select {
			case <-stallTimer.C:
			default:
			}
		}
		stallTimer.Reset(streamStallThreshold)
		stall = stallTimer.C
	}
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
				service.requests.log(accountLabel, "WARN", "临时文件清理失败 | 错误="+err.Error())
			}
		}
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
	verified := false
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
				service.requests.logRequestProgress(requestID, accountLabel, "WARN", fmt.Sprintf(
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
			service.requests.logRequestProgress(requestID, accountLabel, "INFO", fmt.Sprintf(
				"事件流恢复 | 模型=%s | 停顿=%s | 当前事件=%s",
				modelID, now.Sub(lastEventAt).Round(time.Millisecond), event.Kind,
			))
			stalled = false
		}
		lastEventAt = now
		lastEventKind = string(event.Kind)
		switch event.Kind {
		case aistudio.EventReasoning:
			reasoningEvents++
		case aistudio.EventText:
			contentEvents++
		case aistudio.EventUsage:
			if event.Usage != nil {
				usage = event.Usage
			}
		case aistudio.EventToolCall:
			if event.FunctionCall != nil {
				toolCalls++
			}
		}
		api.SetAccessLogTarget(requestCtx, event.ProviderModel, lease.Account().Config.Label)
		if terminal {
			continue
		}
		if event.Kind == aistudio.EventError {
			requestErr = event.Err
			if aistudio.DefinitiveAuthenticationFailure(event.Err) {
				if stateErr := lease.MarkAuthenticationRequired(event.Err.Error()); stateErr != nil {
					requestErr = errors.Join(requestErr, stateErr)
					event.Err = requestErr
				}
			}
			state = finalRequestState(event.Err)
			terminal = true
		}
		if event.Kind == aistudio.EventFinish {
			api.SetAccessLogFinishReason(requestCtx, event.FinishReason)
			state = "completed"
			terminal = true
		}
		if terminal {
			stall = nil
		} else {
			resetStallTimer()
		}
		select {
		case destination <- event:
		case <-requestCtx.Done():
			finishFromContext()
			return
		}
		if !verified && event.Kind == aistudio.EventFinish {
			verified = true
			service.markModelAccessVerifiedAsync(
				lease.Account().ID, accountLabel, lease.CooldownScope(requestedModelID), accessGeneration, lease.CheckedAt(),
			)
		}
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
