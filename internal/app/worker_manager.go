package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

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
	return manager.stopAccountWorker(account)
}

// stopAccountWorker 在持有账户启动锁与状态锁时关闭账户当前 Worker 并记录停止日志，没有 Worker 时直接返回
func (manager *accountWorkerManager) stopAccountWorker(account *accountWorker) error {
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
	if err := manager.closeAccountWorker(account); err != nil {
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
	return nil
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
func (manager *accountWorkerManager) ResetIfGeneration(ctx context.Context, accountID string, generation uint64) (err error) {
	lease, err := manager.pool.AcquireAccount(ctx, accountID)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lease.Release()) }()
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
	if account.generation.Load() != generation {
		return nil
	}
	return manager.stopAccountWorker(account)
}

// prepareUpdate 关闭旧 Worker 并保持新配置待发布
func (manager *accountWorkerManager) prepareUpdate(
	account *aistudio.Account,
	config aistudio.AccountConfig,
) (*accountWorkerUpdate, error) {
	manager.mu.RLock()
	worker := manager.accounts[account.ID]
	manager.mu.RUnlock()
	if worker == nil {
		return nil, fmt.Errorf("WAA worker 账户不存在: %s", account.ID)
	}
	update := &accountWorkerUpdate{
		account: worker, config: manager.workerConfigFor(account, config), label: config.Label,
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
	update.account.mu.Lock()
	update.account.config = update.config
	update.account.label = update.label
	update.account.mu.Unlock()
	update.account.startupMu.Unlock()
}

// Discard 放弃已准备的 Worker 配置
func (update *accountWorkerUpdate) Discard() {
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
			return manager.detachStartup(ctx, func(ctx context.Context) (*accountWorkerPreparer, error) {
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
			})
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
			if err := waitScheduling(ctx, nil, 100*time.Millisecond); err != nil {
				return nil, err
			}
			continue
		}
		manager.reserveVictim(victim)
		opening := make(chan struct{})
		manager.openings[accountID] = opening
		manager.rebalanceMu.Unlock()
		return manager.detachStartup(ctx, func(ctx context.Context) (*accountWorkerPreparer, error) {
			defer func() { manager.releaseVictim(victim) }()
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
					if err := waitScheduling(ctx, nil, 100*time.Millisecond); err != nil {
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
		})
	}
}

// detachStartup 运行已占用启动名额的 Worker 启动，调用方离开后启动继续完成并发布到热池，ResetAll 或关闭时取消
func (manager *accountWorkerManager) detachStartup(
	ctx context.Context,
	start func(context.Context) (*accountWorkerPreparer, error),
) (aistudio.ProtectedPreparer, error) {
	startupCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stop := context.AfterFunc(manager.backgroundContext(), cancel)
	type startResult struct {
		preparer *accountWorkerPreparer
		err      error
	}
	done := make(chan startResult, 1)
	go func() {
		defer func() {
			stop()
			cancel()
		}()
		preparer, err := start(startupCtx)
		done <- startResult{preparer: preparer, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-done:
		return result.preparer, result.err
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
	} else if ctx.Err() == nil {
		err = errors.Join(err, lease.ConfirmAuthenticationFailure(ctx, err))
	}
	return nil, &accountWorkerInitError{err: err, authHandled: true}
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
			notify(errors.Join(append(failures, err)...))
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
			if err := waitScheduling(ctx, nil, 100*time.Millisecond); err != nil {
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
			if err := waitScheduling(ctx, nil, min(time.Until(groups.EarliestCooldown.Until), schedulingRecheck)); err != nil {
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
