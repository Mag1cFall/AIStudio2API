package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

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
	requests.log("service", "INFO", fmt.Sprintf("运行时装配 | 2/3 | 校验 Camoufox | 账户=%d", len(accounts)))
	camoufoxPath, err := camoufoxnative.FindExecutable(launchCtx)
	if err != nil {
		return nil, nil, nil, err
	}
	login, err := aistudio.NewNativeLoginDriver(camoufoxPath, cfg.RequestTimeout)
	if err != nil {
		return nil, nil, nil, err
	}

	requests.log("service", "INFO", "运行时装配 | 3/3 | 创建协议客户端")
	pool := aistudio.NewAccountPool(accounts, cfg.PerAccountConcurrency)
	headers, err := newAccountHeaderProvider(accounts, cfg.Proxy)
	if err != nil {
		return nil, nil, nil, err
	}
	transport, err := aistudio.NewMakerSuiteHTTPTransport(aistudio.HTTPTransportOptions{
		Pool: pool, Signer: aistudio.NewSigner(), Headers: headers, GlobalProxy: cfg.Proxy,
	})
	if err != nil {
		headers.Close()
		return nil, nil, nil, err
	}
	workers := newAccountWorkerManager(
		pool, accounts, requests, camoufoxPath, cfg.Proxy, cfg.InitTimeout,
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
	refresher := newAuthRuntimeRefresher(workers, headers, requests, cfg.Proxy)
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
	service := newTrackedService(lifecycle, pooled, pool, requests, workers, cfg.RequestTimeout)
	admin := newRuntimeAdmin(lifecycle, pool, store, service, requests, login, workers, headers, cfg)
	requests.log("service", "INFO", fmt.Sprintf(
		"协议运行时就绪 | 账户=%d | 耗时=%s",
		len(accounts), time.Since(startedAt).Round(time.Millisecond),
	))
	closeRuntime := func() error {
		err := workers.Close()
		transport.CloseIdleConnections()
		headers.Close()
		return err
	}
	return service, admin, closeRuntime, nil
}

// accountWorkerManager 管理每账户的长驻 WAA worker
type accountWorkerManager struct {
	mu              sync.RWMutex
	fillMu          sync.Mutex
	rebalanceMu     sync.Mutex
	pool            *aistudio.AccountPool
	accounts        map[string]*accountWorker
	openings        map[string]chan struct{}
	requests        *requestRegistry
	camoufox        string
	globalProxy     string
	initTimeout     time.Duration
	warmTarget      int
	maxActive       int
	warmConcurrency int
	temporaryChat   bool
	lifecycle       context.Context
	cancel          context.CancelFunc
	closed          bool
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
	generation     atomic.Uint64
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
var errAccountWorkerCleanupPending = errors.New("WAA worker 清理未完成")

const (
	workerEvictionTimeout = 100 * time.Millisecond
)

// Prepare 在账户 Worker 有效期间生成 proof
func (preparer *accountWorkerPreparer) Prepare(ctx context.Context, request aistudio.ProtectedRequest) (aistudio.PreparedProtectedRequest, error) {
	preparer.account.mu.Lock()
	defer preparer.account.mu.Unlock()
	if preparer.account.worker != preparer.worker {
		return aistudio.PreparedProtectedRequest{}, errAccountWorkerReplaced
	}
	if preparer.account.bootstrapModel != preparer.bootstrapModel {
		return aistudio.PreparedProtectedRequest{}, errAccountWorkerReplaced
	}
	return preparer.worker.Prepare(ctx, request)
}

// SendProtected 保证浏览器请求仍绑定当前有效账户 Worker
func (preparer *accountWorkerPreparer) SendProtected(ctx context.Context, request aistudio.ProtectedRequest) (*aistudio.RPCResponse, error) {
	preparer.account.mu.Lock()
	defer preparer.account.mu.Unlock()
	if preparer.account.worker != preparer.worker || preparer.account.bootstrapModel != preparer.bootstrapModel {
		return nil, errAccountWorkerReplaced
	}
	return preparer.worker.SendProtected(ctx, request)
}

// BrowserStorageState 返回同一有效账户 Worker 的浏览器 Cookie 状态
func (preparer *accountWorkerPreparer) BrowserStorageState(ctx context.Context) (aistudio.StorageState, error) {
	preparer.account.mu.Lock()
	defer preparer.account.mu.Unlock()
	if preparer.account.worker != preparer.worker || preparer.account.bootstrapModel != preparer.bootstrapModel {
		return aistudio.StorageState{}, errAccountWorkerReplaced
	}
	return preparer.worker.BrowserStorageState(ctx)
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
		globalProxy: globalProxy, initTimeout: initTimeout,
		warmTarget: warmTarget, maxActive: maxActive, warmConcurrency: warmConcurrency, temporaryChat: temporaryChat,
		openings:  make(map[string]chan struct{}),
		lifecycle: lifecycle, cancel: cancel,
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
	err := closeAccountWorker(account)
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

// ResetIfGeneration 仅关闭产生当前失败的 Worker
func (manager *accountWorkerManager) ResetIfGeneration(accountID string, generation uint64) (bool, error) {
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
	if err := closeAccountWorker(account); err != nil {
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
		if err := closeAccountWorker(worker); err != nil {
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
	return closeAccountWorker(account)
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
		proxy = strings.TrimSpace(manager.globalProxy)
	}
	return camoufoxnative.Options{
		ExecutablePath:   manager.camoufox,
		StorageStatePath: account.StoragePath,
		Locale:           config.Locale,
		Timezone:         config.Timezone,
		Proxy:            proxy,
		Headless:         true,
		TemporaryChat:    manager.temporaryChat,
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
		if !status.Enabled || (status.State != aistudio.AccountReady && status.State != aistudio.AccountBusy) {
			continue
		}
		if models, err := manager.pool.BootstrapModels(status.ID); err == nil && len(models) > 0 {
			available++
		}
	}
	return min(manager.warmTarget, available)
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
		if combined.EarliestCooldown.IsZero() || !groups.EarliestCooldown.IsZero() && groups.EarliestCooldown.Before(combined.EarliestCooldown) {
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
func (manager *accountWorkerManager) Worker(ctx context.Context, accountID string, _ string) (aistudio.ProtectedPreparer, error) {
	return manager.ensureWorker(ctx, accountID, true)
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
		if err != nil {
			account.startupMu.Unlock()
			manager.requests.log(label, "ERROR", fmt.Sprintf(
				"WAA Worker 启动失败 | 耗时=%s | 错误=%s",
				time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
			))
			return nil, &accountWorkerInitError{err: err}
		}
		ownsLease = true
	}
	manager.requests.log(label, "INFO", "WAA Worker 启动 | 1/7 | 初始化页面 | 页面模型="+bootstrapModel)
	initCtx, cancel := context.WithTimeout(ctx, manager.initTimeout)
	options.Model = bootstrapModel
	options.StartupProgress = func(stage camoufoxnative.StartupStage) {
		step, message := workerStartupProgress(stage)
		manager.requests.log(label, "INFO", fmt.Sprintf("WAA Worker 启动 | %d/7 | %s", step, message))
	}
	worker, initErr := aistudio.NewNativeWorker(initCtx, account.id, options)
	cancel()
	if initErr != nil {
		if ownsLease {
			_ = runtimeLease.Release()
		}
		account.startupMu.Unlock()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		manager.requests.log(label, "ERROR", fmt.Sprintf(
			"WAA Worker 启动失败 | 页面模型=%s | 耗时=%s | 错误=%s",
			bootstrapModel, time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(initErr.Error()),
		))
		return nil, &accountWorkerInitError{err: initErr}
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
	label := preparer.account.label
	preparer.account.mu.Unlock()
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
	statusByID := make(map[string]aistudio.AccountStatus)
	for _, status := range manager.pool.Status() {
		statusByID[status.ID] = status
	}
	warm := manager.WarmAccountIDs()
	var selected string
	var selectedUsed time.Time
	for _, accountID := range warm {
		if accountID == excludeID {
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
		status := statusByID[accountID]
		if status.State == aistudio.AccountBusy {
			continue
		}
		lastUsed := time.Time{}
		if status.LastUsed != nil {
			lastUsed = *status.LastUsed
		}
		if selected == "" || lastUsed.Before(selectedUsed) {
			selected = accountID
			selectedUsed = lastUsed
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
	waitForOpening bool,
) (aistudio.ProtectedPreparer, error) {
	bootstrapModel, err := manager.pool.BootstrapModel(accountID)
	if err != nil {
		return nil, err
	}
	workerCtx, cancel := context.WithCancel(ctx)
	stopLifecycle := context.AfterFunc(manager.lifecycle, cancel)
	defer func() {
		stopLifecycle()
		cancel()
	}()
	ctx = workerCtx
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
		if replacing || occupancy.slots < manager.maxActive {
			opening := make(chan struct{})
			manager.openings[accountID] = opening
			manager.rebalanceMu.Unlock()
			preparer, err := manager.startReservedWorker(ctx, accountID, bootstrapModel)
			manager.rebalanceMu.Lock()
			if err == nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					err = errors.Join(ctxErr, discardAccountWorker(preparer))
				} else {
					err = activateAccountWorker(preparer)
				}
			}
			delete(manager.openings, accountID)
			close(opening)
			warmCount := len(manager.WarmAccountIDs())
			manager.rebalanceMu.Unlock()
			if err == nil && warmCount > manager.warmTarget {
				manager.requests.log("service", "INFO", fmt.Sprintf(
					"WAA Worker 按需扩容 | Worker=%d/%d", warmCount, manager.maxActive,
				))
			}
			return preparer, err
		}
		if len(manager.openings) > 0 {
			manager.rebalanceMu.Unlock()
			if err := waitWarmCandidate(ctx, 100*time.Millisecond); err != nil {
				return nil, err
			}
			continue
		}
		victim := manager.idleWarmVictim(accountID)
		if victim == "" {
			manager.rebalanceMu.Unlock()
			if err := waitWarmCandidate(ctx, 100*time.Millisecond); err != nil {
				return nil, err
			}
			continue
		}
		opening := make(chan struct{})
		manager.openings[accountID] = opening
		manager.rebalanceMu.Unlock()
		pending, startErr := manager.startReservedWorker(ctx, accountID, bootstrapModel)
		if startErr != nil {
			manager.rebalanceMu.Lock()
			delete(manager.openings, accountID)
			close(opening)
			manager.rebalanceMu.Unlock()
			return nil, startErr
		}
		for {
			manager.rebalanceMu.Lock()
			if ctxErr := ctx.Err(); ctxErr != nil {
				discardErr := discardAccountWorker(pending)
				delete(manager.openings, accountID)
				close(opening)
				manager.rebalanceMu.Unlock()
				return nil, errors.Join(ctxErr, discardErr)
			}
			evicted, evictionErr := manager.evictIdleWorker(ctx, victim)
			if evictionErr == nil && evicted {
				activationErr := activateAccountWorker(pending)
				delete(manager.openings, accountID)
				close(opening)
				warmCount := len(manager.WarmAccountIDs())
				manager.rebalanceMu.Unlock()
				if activationErr != nil {
					return nil, activationErr
				}
				if warmCount > manager.warmTarget {
					manager.requests.log("service", "INFO", fmt.Sprintf(
						"WAA Worker 按需替换 | Worker=%d/%d", warmCount, manager.maxActive,
					))
				}
				return pending, nil
			}
			if evictionErr != nil || ctx.Err() != nil {
				discardErr := discardAccountWorker(pending)
				delete(manager.openings, accountID)
				close(opening)
				manager.rebalanceMu.Unlock()
				return nil, errors.Join(evictionErr, ctx.Err(), discardErr)
			}
			victim = manager.idleWarmVictim(accountID)
			manager.rebalanceMu.Unlock()
			if victim == "" {
				if err := waitWarmCandidate(ctx, 100*time.Millisecond); err != nil {
					manager.rebalanceMu.Lock()
					discardErr := discardAccountWorker(pending)
					delete(manager.openings, accountID)
					close(opening)
					manager.rebalanceMu.Unlock()
					return nil, errors.Join(err, discardErr)
				}
				victim = manager.idleWarmVictim(accountID)
			}
		}
	}
}

func (manager *accountWorkerManager) promote(ctx context.Context, accountID string, _ string) (aistudio.ProtectedPreparer, error) {
	return manager.ensureWorker(ctx, accountID, false)
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
	for {
		if err := ctx.Err(); err != nil {
			if !notified {
				notify(errors.Join(append(failures, err)...))
			}
			return
		}
		warm := manager.WarmAccountIDs()
		if len(warm) >= manager.warmTarget {
			manager.requests.log("service", "INFO", fmt.Sprintf(
				"WAA Worker 预热完成 | Worker=%d/%d | 耗时=%s",
				len(warm), manager.PrewarmTarget(), time.Since(startedAt).Round(time.Millisecond),
			))
			notify(nil)
			return
		}
		remaining := manager.warmTarget - len(warm)
		batchSize := min(manager.warmConcurrency, remaining)
		groups, err := manager.classifyBootstrapCandidates(ctx, warm)
		if err != nil {
			failures = append(failures, err)
		} else {
			groups.StandbyReady = excludeAccountIDs(groups.StandbyReady, failedAccounts)
			groups.StandbyBusy = excludeAccountIDs(groups.StandbyBusy, failedAccounts)
		}
		pendingBusy := false
		tasks := make([]warmTask, 0, batchSize)
		if err == nil {
			pendingBusy = len(groups.StandbyBusy) > 0
			for _, accountID := range groups.StandbyReady[:min(batchSize, len(groups.StandbyReady))] {
				tasks = append(tasks, warmTask{accountID: accountID})
			}
		}
		if len(tasks) == 0 {
			if pendingBusy {
				if err := waitWarmCandidate(ctx, 100*time.Millisecond); err != nil && !notified {
					notify(errors.Join(append(failures, err)...))
				}
				continue
			}
			warm = manager.WarmAccountIDs()
			if len(warm) > 0 {
				manager.requests.log("service", "INFO", fmt.Sprintf(
					"WAA Worker 预热完成 | Worker=%d/%d | 失败=%d | 耗时=%s",
					len(warm), manager.PrewarmTarget(), len(failures), time.Since(startedAt).Round(time.Millisecond),
				))
				notify(nil)
				return
			}
			if len(failures) == 0 {
				failures = append(failures, aistudio.ErrNoEligibleAccount)
			}
			notify(errors.Join(failures...))
			return
		}
		results := make(chan warmResult, len(tasks))
		for _, task := range tasks {
			go func(task warmTask) {
				_, err := manager.Worker(ctx, task.accountID, "")
				results <- warmResult{accountID: task.accountID, err: err}
			}(task)
		}
		for range len(tasks) {
			result := <-results
			if result.err == nil {
				notify(nil)
				continue
			}
			if ctx.Err() != nil {
				continue
			}
			failure := fmt.Errorf("预热账户 %s: %w", result.accountID, result.err)
			failures = append(failures, failure)
			failedAccounts[result.accountID] = struct{}{}
		}
	}
}

type warmResult struct {
	accountID string
	err       error
}

type warmTask struct {
	accountID string
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
				closeResults <- closeAccountWorker(account)
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

func closeAccountWorker(account *accountWorker) error {
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
		proxy = strings.TrimSpace(provider.globalProxy)
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
	lifecycle          context.Context
	service            aistudio.Service
	catalog            modelCatalogService
	pool               *aistudio.AccountPool
	requests           *requestRegistry
	workers            *accountWorkerManager
	timeout            time.Duration
	state              atomic.Int32
	lifecycleMu        sync.Mutex
	transitionDone     chan struct{}
	transitionErr      error
	transitionTimedOut bool
	dataContext        context.Context
	dataCancel         context.CancelFunc
	modelsMu           sync.RWMutex
	models             []aistudio.Model
	modelSyncMu        sync.Mutex
	modelRetriesMu     sync.Mutex
	modelRetries       map[string]struct{}
	modelRefreshDone   <-chan struct{}
	modelChangeMu      sync.Mutex
	modelRevision      uint64
	modelApplied       uint64
	performanceMu      sync.RWMutex
	performance        map[string]map[string]generationPerformance
}

type modelCatalogService interface {
	RefreshAccountModels(context.Context, string) ([]aistudio.Model, error)
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
		timeout: timeout, modelRetries: make(map[string]struct{}), performance: make(map[string]map[string]generationPerformance),
	}
}

type serviceStoppedError struct{}

var errServiceTransitioning = errors.New("生成服务正在切换状态")

const (
	serviceStopped int32 = iota
	serviceLaunching
	serviceRunning
	modelCatalogRetryInterval        = 30 * time.Second
	modelCatalogShutdownTimeout      = 2 * time.Second
	serviceTransitionShutdownTimeout = 12 * time.Second
)

func (*serviceStoppedError) Error() string {
	return "AIStudio2API 服务已停止"
}

func (*serviceStoppedError) HTTPStatus() int {
	return http.StatusServiceUnavailable
}

func (*serviceStoppedError) ErrorCode() string {
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
	service.clearPerformance()
	service.replaceModelSnapshot(service.catalog.CachedModels())
	if launching != nil {
		launching()
	}
	models := service.modelSnapshot()
	service.requests.log("service", "INFO", fmt.Sprintf(
		"生成服务启动 | 1/2 | 准备模型目录 | 缓存=%d", len(models),
	))
	catalogReady, catalogDone := service.startModelCatalogRefresh(dataContext)
	service.lifecycleMu.Lock()
	if service.dataContext == dataContext {
		service.modelRefreshDone = catalogDone
	}
	service.lifecycleMu.Unlock()
	if len(models) == 0 {
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
	firstWarm := service.workers.StartPrewarm(dataContext)
	select {
	case <-dataContext.Done():
		stopCaller()
		return nil, false, service.finishLaunch(transitionDone, dataCancel, catalogDone, dataContext.Err())
	case warmErr, ok := <-firstWarm:
		if !ok {
			warmErr = fmt.Errorf("WAA 预热未返回就绪账户")
		}
		if warmErr != nil {
			stopCaller()
			return nil, false, service.finishLaunch(transitionDone, dataCancel, catalogDone, warmErr)
		}
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

type generationPerformance struct {
	firstEvent time.Duration
	observedAt time.Time
}

func (service *trackedService) observePerformance(accountID string, model string, firstEvent time.Duration) {
	accountID = strings.TrimSpace(accountID)
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if accountID == "" || model == "" || firstEvent <= 0 {
		return
	}
	service.performanceMu.Lock()
	if service.performance[accountID] == nil {
		service.performance[accountID] = make(map[string]generationPerformance)
	}
	observed := service.performance[accountID][model]
	if observed.firstEvent == 0 {
		observed.firstEvent = firstEvent
	} else {
		observed.firstEvent = (observed.firstEvent*3 + firstEvent) / 4
	}
	observed.observedAt = time.Now()
	service.performance[accountID][model] = observed
	service.performanceMu.Unlock()
}

func (service *trackedService) preferFast(accountIDs []string, model string) []string {
	result := append([]string(nil), accountIDs...)
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	states := service.pool.CandidateStates(result, model)
	service.performanceMu.RLock()
	sort.SliceStable(result, func(left int, right int) bool {
		leftState := states[result[left]]
		rightState := states[result[right]]
		leftPerformance, leftObserved := service.performanceForModelLocked(result[left], model)
		rightPerformance, rightObserved := service.performanceForModelLocked(result[right], model)
		leftVerified := leftState.ModelAccess == aistudio.ModelAccessVerified
		rightVerified := rightState.ModelAccess == aistudio.ModelAccessVerified
		if leftVerified != rightVerified {
			return leftVerified
		}
		if leftObserved != rightObserved {
			return leftObserved
		}
		if leftObserved && leftPerformance.firstEvent != rightPerformance.firstEvent {
			return leftPerformance.firstEvent < rightPerformance.firstEvent
		}
		if leftState.AvailableSlot != rightState.AvailableSlot {
			return leftState.AvailableSlot > rightState.AvailableSlot
		}
		if leftState.Active != rightState.Active {
			return leftState.Active < rightState.Active
		}
		if leftObserved && !leftPerformance.observedAt.Equal(rightPerformance.observedAt) {
			return leftPerformance.observedAt.After(rightPerformance.observedAt)
		}
		return false
	})
	service.performanceMu.RUnlock()
	return result
}

func (service *trackedService) performanceForModelLocked(accountID string, model string) (generationPerformance, bool) {
	models := service.performance[accountID]
	observed, ok := models[model]
	return observed, ok
}

func (service *trackedService) forgetPerformance(accountID string, model string) {
	accountID = strings.TrimSpace(accountID)
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	service.performanceMu.Lock()
	if models := service.performance[accountID]; models != nil {
		delete(models, model)
		if len(models) == 0 {
			delete(service.performance, accountID)
		}
	}
	service.performanceMu.Unlock()
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

func (service *trackedService) clearPerformance() {
	service.performanceMu.Lock()
	clear(service.performance)
	service.performanceMu.Unlock()
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

// Models 返回最近一次成功同步的模型目录
func (service *trackedService) Models(context.Context) ([]aistudio.Model, error) {
	return service.modelSnapshot(), nil
}

func (service *trackedService) modelSnapshot() []aistudio.Model {
	service.modelsMu.RLock()
	models := append([]aistudio.Model(nil), service.models...)
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
	service.requests.publish(api.AdminEvent{Type: "models", Data: map[string]any{"models": service.modelSnapshot()}})
}

type accountModelRefreshResult struct {
	accountID string
	models    []aistudio.Model
	err       error
	skipped   bool
}

// startModelCatalogRefresh 并发刷新全部账户并持续处理失败目录
func (service *trackedService) startModelCatalogRefresh(ctx context.Context) (<-chan error, <-chan struct{}) {
	ready := make(chan error, 1)
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
	return ready, done
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
		return nil, err
	}
	if len(models) == 0 {
		service.modelRetries[accountID] = struct{}{}
	} else {
		delete(service.modelRetries, accountID)
	}
	service.modelRetriesMu.Unlock()
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

func (service *trackedService) syncAccountModels(ctx context.Context, accountID string) ([]aistudio.Model, error) {
	models, err := service.refreshAccountModelCatalog(ctx, accountID)
	service.modelSyncMu.Lock()
	service.applyCachedModelCatalog()
	service.modelSyncMu.Unlock()
	return models, err
}

func (service *trackedService) retryModelCatalogs(ctx context.Context) {
	ticker := time.NewTicker(modelCatalogRetryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !service.retryAccountModelCatalogs(ctx) {
			return
		}
	}
}

func (service *trackedService) retryAccountModelCatalogs(ctx context.Context) bool {
	accountIDs := service.pendingModelRetryIDs()
	authRequiredBefore := service.authRequiredAccountIDs()
	synchronized := 0
	refreshed := 0
	for result := range service.refreshAccountModelCatalogs(ctx, accountIDs, true) {
		if ctx.Err() != nil {
			return false
		}
		if !result.skipped && result.err == nil {
			synchronized++
			if len(result.models) > 0 {
				refreshed++
				service.applyCachedModelCatalog()
				service.publishModelAccess()
			}
		}
	}
	authChanged := !maps.Equal(service.authRequiredAccountIDs(), authRequiredBefore)
	if authChanged {
		service.publishModelAccess()
	}
	if len(accountIDs) == 0 && !authChanged {
		return true
	}
	service.requests.log("service", "INFO", fmt.Sprintf(
		"模型目录重试完成 | 同步=%d | 非空=%d | 待重试账户=%d",
		synchronized, refreshed, service.pendingModelRetryCount(),
	))
	return true
}

func (service *trackedService) removeAccountModelRetry(accountID string) {
	service.modelRetriesMu.Lock()
	delete(service.modelRetries, accountID)
	service.modelRetriesMu.Unlock()
}

func (service *trackedService) pendingModelRetryIDs() []string {
	service.modelRetriesMu.Lock()
	accountIDs := make([]string, 0, len(service.modelRetries))
	for accountID := range service.modelRetries {
		accountIDs = append(accountIDs, accountID)
	}
	service.modelRetriesMu.Unlock()
	sort.Strings(accountIDs)
	return accountIDs
}

func (service *trackedService) pendingModelRetryCount() int {
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

// currentModelRevision 返回最新模型变化代际
func (service *trackedService) currentModelRevision() uint64 {
	service.modelChangeMu.Lock()
	defer service.modelChangeMu.Unlock()
	return service.modelRevision
}

// modelRevisions 返回模型变化与已提交代际
func (service *trackedService) modelRevisions() (uint64, uint64) {
	service.modelChangeMu.Lock()
	defer service.modelChangeMu.Unlock()
	return service.modelRevision, service.modelApplied
}

// applyModelRevision 提交成功刷新的模型代际
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
	api.SetAccessLogTarget(ctx, request.Model, "")
	requestCtx, cancel, err := service.dataRequestContext(ctx)
	if err != nil {
		api.SetAccessLogError(ctx, err)
		return aistudio.VideoOperation{}, err
	}
	defer cancel()
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
			accountID, expectedGeneration, recoveredWorkers,
			recoverCurrentGeneration, workerFailed || waaRuntimeFailed,
		)
		return recovered, recoveryErr
	}
	video, ok := service.service.(aistudio.VideoService)
	if !ok {
		return aistudio.VideoOperation{}, fmt.Errorf("video service 不可用")
	}
	operation, requestErr := video.GenerateVideo(requestCtx, request)
	if requestErr == nil && selectedAccountID != "" {
		service.markModelAccessVerifiedAsync(
			selectedAccountID, selectedAccountLabel,
			strings.TrimPrefix(strings.TrimSpace(request.Model), "models/"),
			selectedAccessGeneration, operation.ModelAccessCheckedAt(),
		)
	}
	api.SetAccessLogError(requestCtx, requestErr)
	return operation, requestErr
}

func needsWAARuntimeRecovery(cause error, generationChanged bool, workerFailed bool, workerReplaced bool) bool {
	if aistudio.DefinitiveModelAccessFailure(cause) {
		return false
	}
	return generationChanged || workerFailed || workerReplaced ||
		aistudio.DefinitiveWAARuntimeFailure(cause)
}

// recoverWorkerOnce 对指定账户的失败 Worker 版本执行至多一次恢复
func (service *trackedService) recoverWorkerOnce(
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
		reset, err := service.workers.ResetIfGeneration(accountID, expectedGeneration)
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
		standbyReady, opening := service.workers.withoutOpening(groups.StandbyReady)
		groups.StandbyReady = standbyReady
		warmAvailable := append(append([]string(nil), groups.WarmReady...), groups.WarmAvailable...)
		for _, accountID := range service.preferFast(warmAvailable, selection.ModelID) {
			candidate := selection
			candidate.AccountID = accountID
			lease, _, acquireErr := service.pool.TryAcquireFor(ctx, candidate)
			if errors.Is(acquireErr, aistudio.ErrAccountNotFound) && !fixedAccount {
				continue
			}
			if lease != nil || acquireErr != nil {
				return lease, acquireErr
			}
		}
		if len(groups.StandbyReady) == 0 && opening {
			if err := waitWarmCandidate(ctx, 100*time.Millisecond); err != nil {
				return nil, err
			}
			continue
		}
		if len(groups.StandbyReady) > 0 && (len(active) < service.workers.maxActive || service.workers.idleWarmVictim("") != "") {
			standby := groups.StandbyReady
			if len(active) < service.workers.maxActive {
				if cold := service.workers.coldAccounts(standby); len(cold) > 0 {
					standby = cold
				}
			}
			accountID := service.preferFast(standby, selection.ModelID)[0]
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
				if err := waitWarmCandidate(ctx, 100*time.Millisecond); err != nil {
					return nil, err
				}
				continue
			}
			_, promoteErr := service.workers.promote(ctx, accountID, selection.ModelID)
			if promoteErr == nil {
				return lease, nil
			}
			if releaseErr := lease.Release(); releaseErr != nil {
				return nil, errors.Join(promoteErr, releaseErr)
			}
			if errors.Is(promoteErr, errAccountWorkerOpening) {
				continue
			}
			if fixedAccount {
				return nil, promoteErr
			}
			failedWorkers[accountID] = struct{}{}
			continue
		}
		if len(groups.WarmBusy) > 0 || len(groups.StandbyBusy) > 0 || opening {
			if err := waitWarmCandidate(ctx, 100*time.Millisecond); err != nil {
				return nil, err
			}
			continue
		}
		if !groups.EarliestCooldown.IsZero() {
			if err := waitWarmCandidate(ctx, min(time.Until(groups.EarliestCooldown), 100*time.Millisecond)); err != nil {
				return nil, err
			}
			continue
		}
		return nil, aistudio.ErrNoEligibleAccount
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

// Generate 获取唯一账户并转发规范事件流
func (service *trackedService) Generate(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	generationStartedAt := time.Now()
	api.SetAccessLogTarget(ctx, request.Model, "")
	api.SetAccessLogGenerationConfig(ctx, request.Config)
	api.StartAccessLog(ctx)
	requestCtx, cancel, err := service.dataRequestContext(ctx)
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

func (service *trackedService) generateWithRetry(
	clientCtx context.Context,
	requestCtx context.Context,
	cancel context.CancelFunc,
	generationStartedAt time.Time,
	request aistudio.GenerateRequest,
	resourceID string,
	destination chan<- aistudio.Event,
) {
	maxAttempts := 1
	requestedAccountID := strings.TrimSpace(request.AccountID)
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	unbound := requestedAccountID == "" && resourceID == ""
	fileBound := requestedAccountID == "" && resourceID != ""
	if unbound || fileBound {
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
	var activity *upstreamActivity
	var temporaryCopies *aistudio.TemporaryFileCopies
	var err error
	originalContents := request.Contents
	attempted := make(map[string]struct{}, maxAttempts)
	recoveredWorker := make(map[string]struct{})
	recoveryAccountID := ""
	copyFiles := false
	for attempt := 0; attempt < maxAttempts; attempt++ {
		request.Contents = originalContents
		selectionAccountID := requestedAccountID
		recoveringAccount := recoveryAccountID != ""
		if recoveryAccountID != "" {
			selectionAccountID = recoveryAccountID
			recoveryAccountID = ""
		}
		selectionResourceID := resourceID
		if copyFiles {
			selectionResourceID = ""
		}
		selection := aistudio.AccountSelection{
			ModelID: modelID, Method: "generateContent",
			AccountID: selectionAccountID, ResourceID: selectionResourceID,
		}
		if (unbound || fileBound) && len(attempted) > 0 {
			for _, status := range service.pool.Status() {
				if _, exists := attempted[status.ID]; status.Enabled && !exists {
					selection.AllowedAccountIDs = append(selection.AllowedAccountIDs, status.ID)
				}
			}
		}
		nextLease, acquireErr := service.acquireWarmLease(requestCtx, selection)
		if acquireErr != nil {
			if fileBound && !copyFiles && errors.Is(acquireErr, aistudio.ErrNoEligibleAccount) && requestCtx.Err() == nil {
				copyFiles = true
				continue
			}
			if err == nil || !errors.Is(acquireErr, aistudio.ErrNoEligibleAccount) {
				err = acquireErr
			}
			if recoveringAccount && unbound && requestCtx.Err() == nil {
				attempted[selectionAccountID] = struct{}{}
				continue
			}
			break
		}
		lease = nextLease
		err = nil
		source = nil
		request.AccountID = lease.Account().ID
		workerGeneration := service.workers.WorkerGeneration(request.AccountID)
		attempted[request.AccountID] = struct{}{}
		accountLabel := lease.Account().Config.Label
		api.SetAccessLogTarget(requestCtx, modelID, accountLabel)
		service.requests.markRunning(request.ID, request.AccountID, accountLabel)
		prepareStartedAt := time.Now()
		prepareTiming := newRequestPreparationTiming(prepareStartedAt)
		prepareWarningDone := make(chan struct{})
		prepareWarning := time.AfterFunc(streamStallThreshold, func() {
			current, _, _ := prepareTiming.snapshot(time.Now())
			service.requests.log(accountLabel, "WARN", fmt.Sprintf(
				"请求准备等待 | 已等待=%s | 当前=%s | 模型=%s",
				streamStallThreshold, current, modelID,
			))
			close(prepareWarningDone)
		})
		activity = &upstreamActivity{}
		attemptCtx := aistudio.ContextWithAccountLease(requestCtx, lease)
		attemptCtx = aistudio.ContextWithStreamActivityObserver(attemptCtx, activity.observe)
		attemptCtx = aistudio.ContextWithRequestPhaseObserver(attemptCtx, prepareTiming.observe)
		var attemptCopies *aistudio.TemporaryFileCopies
		if copyFiles {
			fileCopies, ok := service.service.(interface {
				CopyFileReferencesToLease(context.Context, *aistudio.AccountLease, []aistudio.Content) ([]aistudio.Content, *aistudio.TemporaryFileCopies, error)
			})
			if !ok {
				err = fmt.Errorf("文件引用跨账户服务不可用")
			} else {
				request.Contents, attemptCopies, err = fileCopies.CopyFileReferencesToLease(
					requestCtx, lease, originalContents,
				)
			}
		}
		if err == nil {
			source, err = service.service.Generate(attemptCtx, request)
		}
		if err == nil && attemptCopies != nil && attemptCopies.Count() > 0 {
			sourceLabels := make([]string, 0, len(attemptCopies.SourceAccountIDs()))
			statuses := service.pool.Status()
			for _, sourceID := range attemptCopies.SourceAccountIDs() {
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
				strings.Join(sourceLabels, ","), accountLabel, attemptCopies.Count(),
			))
		}
		prepareElapsed := time.Since(prepareStartedAt)
		if !prepareWarning.Stop() {
			<-prepareWarningDone
			_, waa, responseHeader := prepareTiming.snapshot(time.Now())
			service.requests.log(accountLabel, "INFO", fmt.Sprintf(
				"请求准备结束 | 等待=%s | WAA=%s | 响应头=%s | 模型=%s",
				prepareElapsed.Round(time.Millisecond), waa.Round(time.Millisecond),
				responseHeader.Round(time.Millisecond), modelID,
			))
		}
		if err == nil {
			upstreamStartedAt := time.Now()
			firstEventDelayed := false
			first, err = firstGenerateEvent(requestCtx, source, func() {
				firstEventDelayed = true
				service.requests.log(accountLabel, "WARN", fmt.Sprintf(
					"上游首事件等待 | 已等待=%s | 模型=%s | %s",
					streamStallThreshold, modelID, activity.logFields(time.Now()),
				))
			})
			if firstEventDelayed && err == nil {
				service.requests.log(accountLabel, "INFO", fmt.Sprintf(
					"上游首事件到达 | 等待=%s | 事件=%s | 模型=%s",
					time.Since(upstreamStartedAt).Round(time.Millisecond), first.Kind, modelID,
				))
			}
			if err == nil {
				api.SetAccessLogFirstEvent(requestCtx, time.Since(generationStartedAt))
				api.SetAccessLogTarget(requestCtx, first.ProviderModel, accountLabel)
				service.observePerformance(request.AccountID, modelID, time.Since(prepareStartedAt))
				temporaryCopies = attemptCopies
				break
			}
		}
		if attemptCopies != nil {
			err = errors.Join(err, attemptCopies.Cleanup())
		}
		workerFailed := service.workers.WorkerFailed(request.AccountID)
		waaRuntimeFailed := aistudio.DefinitiveWAARuntimeFailure(err)
		permissionDenied := aistudio.DefinitiveModelAccessFailure(err)
		if permissionDenied {
			service.forgetPerformance(request.AccountID, modelID)
			forgotten, stateErr := service.pool.ForgetModelAccessVerifiedIfGeneration(
				request.AccountID, modelID, lease.ModelAccessGeneration(), lease.CheckedAt(),
			)
			if stateErr != nil {
				service.requests.log(accountLabel, "ERROR", fmt.Sprintf(
					"模型成功记录更新失败 | 模型=%s | 错误=%s",
					modelID, strings.TrimSpace(stateErr.Error()),
				))
			} else if forgotten {
				service.publishModelAccess()
			}
		}
		workerReplaced := errors.Is(err, errAccountWorkerReplaced)
		localWorkerFailure := (workerFailed || workerReplaced) && requestCtx.Err() == nil
		retryable := retryableGenerateAccountError(requestCtx, err) || localWorkerFailure
		recoverWorker := false
		if requestCtx.Err() == nil {
			var resetErr error
			recoverWorker, _, resetErr = service.recoverWorkerOnce(
				request.AccountID, workerGeneration, recoveredWorker,
				needsWAARuntimeRecovery(err, false, workerFailed, workerReplaced),
				workerFailed || waaRuntimeFailed || permissionDenied,
			)
			if resetErr != nil {
				err = errors.Join(err, resetErr)
				retryable = false
			}
		}
		if aistudio.DefinitiveAuthenticationFailure(err) {
			if stateErr := lease.MarkAuthenticationRequired(err.Error()); stateErr != nil {
				err = errors.Join(err, stateErr)
				retryable = false
			}
		}
		if fileBound && permissionDenied {
			copyFiles = true
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
		if recoverWorker {
			recoveryAccountID = request.AccountID
			delete(attempted, request.AccountID)
			maxAttempts++
		}
		if attempt+1 == maxAttempts {
			break
		}
		if recoverWorker {
			service.requests.log(accountLabel, "WARN", fmt.Sprintf(
				"WAA Worker 重建 | 模型=%s | 重放当前请求", modelID,
			))
			continue
		}
		switchMessage := fmt.Sprintf(
			"账号切换 | 模型=%s\n原因: %s",
			modelID, strings.TrimSpace(err.Error()),
		)
		service.requests.log(accountLabel, "WARN", switchMessage)
	}
	if err != nil {
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
		clientCtx, requestCtx, cancel, request.ID, generationStartedAt,
		first, source, destination, lease, temporaryCopies, activity, modelID,
	)
}

var errStreamClosedBeforeFirstEvent = errors.New("AI Studio stream closed before first event")
var errStreamClosedBeforeFinish = errors.New("AI Studio stream closed before finish")

func firstGenerateEvent(ctx context.Context, source <-chan aistudio.Event, onWait func()) (aistudio.Event, error) {
	timer := time.NewTimer(streamStallThreshold)
	defer timer.Stop()
	wait := timer.C
	for {
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
		case <-wait:
			if onWait != nil {
				onWait()
			}
			wait = nil
		case <-ctx.Done():
			return aistudio.Event{}, ctx.Err()
		}
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
	if service.state.Load() != serviceRunning || service.dataContext == nil {
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
	generationStartedAt time.Time,
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
	var firstContent time.Duration
	var lastEventAt time.Time
	lastEventKind := "-"
	reasoningEvents := 0
	contentEvents := 0
	contentChars := 0
	var outputTokens int64
	var reasoningTokens int64
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
		api.SetAccessLogGenerationResult(requestCtx, firstContent, contentChars, outputTokens, reasoningTokens)
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
				stall = nil
				service.requests.log(accountLabel, "WARN", fmt.Sprintf(
					"事件流停顿 | 模型=%s | 已等待=%s | 最近事件=%s | 推理=%d | 正文=%d | %s",
					modelID, streamStallThreshold, lastEventKind, reasoningEvents, contentEvents,
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
			service.requests.log(accountLabel, "INFO", fmt.Sprintf(
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
			contentChars += utf8.RuneCountInString(event.Text)
			if firstContent == 0 {
				firstContent = now.Sub(generationStartedAt)
			}
		case aistudio.EventUsage:
			if event.Usage != nil {
				outputTokens = event.Usage.OutputTokens
				reasoningTokens = event.Usage.ReasoningTokens
			}
		}
		api.SetAccessLogTarget(requestCtx, event.ProviderModel, lease.Account().Config.Label)
		if terminal {
			continue
		}
		if event.Kind == aistudio.EventError {
			requestErr = event.Err
			if aistudio.DefinitiveModelAccessFailure(event.Err) {
				service.forgetPerformance(lease.Account().ID, requestedModelID)
				forgotten, stateErr := service.pool.ForgetModelAccessVerifiedIfGeneration(
					lease.Account().ID, requestedModelID, accessGeneration, lease.CheckedAt(),
				)
				if stateErr != nil {
					requestErr = errors.Join(requestErr, stateErr)
					event.Err = requestErr
				} else if forgotten {
					service.publishModelAccess()
				}
			}
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
				lease.Account().ID, accountLabel, requestedModelID, accessGeneration, lease.CheckedAt(),
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
