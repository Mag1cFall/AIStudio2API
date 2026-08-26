package main

import (
	"context"
	"errors"
	"fmt"
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

// newRuntime 装配账户池、协议客户端与管理服务
func newRuntime(ctx context.Context, cfg config.Config) (aistudio.Service, *runtimeAdmin, func() error, error) {
	startedAt := time.Now()
	requests := newRequestRegistry(ctx)
	requests.log("service", "INFO", "应用启动 | 1/4 | 载入账户")
	store := aistudio.NewAccountStore(strings.Split(cfg.AuthStates, ",")...)
	accounts, err := store.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	requests.log("service", "INFO", fmt.Sprintf("应用启动 | 2/4 | 校验 Camoufox | 账户=%d", len(accounts)))
	camoufoxPath, err := findCamoufoxExecutable()
	if err != nil {
		return nil, nil, nil, err
	}
	login, err := aistudio.NewNativeLoginDriver(camoufoxPath, cfg.RequestTimeout)
	if err != nil {
		return nil, nil, nil, err
	}

	requests.log("service", "INFO", "应用启动 | 3/4 | 装配协议运行时")
	pool := aistudio.NewAccountPool(accounts, cfg.PerAccountConcurrency)
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
	workers := newAccountWorkerManager(
		pool, accounts, requests, camoufoxPath, cfg.Proxy, cfg.InitTimeout,
		cfg.WarmWorkerLimit, cfg.WarmStartupConcurrency, cfg.TemporaryChat,
	)
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
	refresher := newAuthRuntimeRefresher(workers, headers, requests, cfg.Proxy)
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
	service := newTrackedService(ctx, pooled, pool, requests, workers, cfg.RequestTimeout)
	admin := newRuntimeAdmin(ctx, pool, store, service, requests, login, workers, headers, cfg)
	requests.log("service", "INFO", fmt.Sprintf(
		"协议运行时就绪 | 账户=%d | 耗时=%s",
		len(accounts), time.Since(startedAt).Round(time.Millisecond),
	))
	closeRuntime := func() error {
		err := workers.Close()
		transport.CloseIdleConnections()
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
	requests        *requestRegistry
	camoufox        string
	globalProxy     string
	initTimeout     time.Duration
	warmLimit       int
	warmConcurrency int
	temporaryChat   bool
	lifecycle       context.Context
	cancel          context.CancelFunc
	closed          bool
}

type accountWorker struct {
	mu           sync.Mutex
	id           string
	label        string
	config       camoufoxnative.Options
	worker       *aistudio.NativeWorker
	runtimeLease *aistudio.AccountRuntimeLease
	warm         atomic.Bool
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
	warmLimit int,
	warmConcurrency int,
	temporaryChat bool,
) *accountWorkerManager {
	lifecycle, cancel := context.WithCancel(context.Background())
	manager := &accountWorkerManager{
		pool: pool, accounts: make(map[string]*accountWorker, len(accounts)), requests: requests, camoufox: camoufoxPath,
		globalProxy: globalProxy, initTimeout: initTimeout,
		warmLimit: warmLimit, warmConcurrency: warmConcurrency, temporaryChat: temporaryChat,
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
	account.mu.Lock()
	defer account.mu.Unlock()
	if account.worker == nil {
		return nil
	}
	startedAt := time.Now()
	pid := account.worker.State().PID
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
	if worker.worker != nil || worker.runtimeLease != nil {
		if err := closeAccountWorker(worker); err != nil {
			return err
		}
	}
	worker.config = manager.workerConfig(account)
	worker.label = account.Config.Label
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
	return &accountWorker{id: account.ID, label: account.Config.Label, config: manager.workerConfig(account)}
}

func (manager *accountWorkerManager) workerConfig(account *aistudio.Account) camoufoxnative.Options {
	return camoufoxnative.Options{
		ExecutablePath:   manager.camoufox,
		StorageStatePath: account.StoragePath,
		Locale:           account.Config.Locale,
		Timezone:         account.Config.Timezone,
		Proxy:            account.EffectiveProxy(manager.globalProxy),
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
	return min(manager.warmLimit, available)
}

func (manager *accountWorkerManager) classifyBootstrapCandidates(warm []string) (aistudio.AccountCandidateGroups, error) {
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
	var matched bool
	for _, modelID := range aistudio.BootstrapModelIDs() {
		groups, err := manager.pool.ClassifyCandidates(aistudio.AccountSelection{
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
	combined.StandbyReady = manager.pool.PreferBroadCoverage(combined.StandbyReady)
	combined.StandbyBusy = manager.pool.PreferBroadCoverage(combined.StandbyBusy)
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

// Worker 返回账户当前可用的 WAA preparer
func (manager *accountWorkerManager) Worker(ctx context.Context, accountID string) (aistudio.ProtectedPreparer, error) {
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
	account.mu.Lock()
	defer account.mu.Unlock()
	if account.worker != nil {
		phase := account.worker.State().Phase
		if phase == aistudio.WorkerReady || phase == aistudio.WorkerBusy {
			return account.worker, nil
		}
		if err := closeAccountWorker(account); err != nil {
			return nil, err
		}
	}
	startedAt := time.Now()
	runtimeLease, err := aistudio.AcquireAccountRuntimeLease(account.id)
	if err != nil {
		manager.requests.log(account.label, "ERROR", fmt.Sprintf(
			"WAA Worker 启动失败 | 耗时=%s | 错误=%s",
			time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
		))
		return nil, &accountWorkerInitError{err: err}
	}
	account.runtimeLease = runtimeLease
	workerReady := false
	defer func() {
		if !workerReady {
			_ = account.runtimeLease.Release()
			account.runtimeLease = nil
		}
	}()
	manager.requests.log(account.label, "INFO", "WAA Worker 启动 | 1/7 | 选择启动模型")
	models, err := manager.pool.BootstrapModels(accountID)
	if err != nil {
		manager.requests.log(account.label, "ERROR", fmt.Sprintf(
			"WAA Worker 启动失败 | 耗时=%s | 错误=%s",
			time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
		))
		return nil, err
	}
	var failures []error
	for index, model := range models {
		if err := ctx.Err(); err != nil {
			manager.requests.log(account.label, "INFO", fmt.Sprintf(
				"WAA Worker 启动已取消 | 耗时=%s",
				time.Since(startedAt).Round(time.Millisecond),
			))
			return nil, err
		}
		manager.requests.log(account.label, "INFO", fmt.Sprintf(
			"WAA Worker 启动模型 | %d/%d | 模型=%s", index+1, len(models), model,
		))
		modelStartedAt := time.Now()
		initCtx, cancel := context.WithTimeout(ctx, manager.initTimeout)
		options := account.config
		options.Model = model
		options.StartupProgress = func(stage camoufoxnative.StartupStage) {
			step, message := workerStartupProgress(stage)
			manager.requests.log(account.label, "INFO", fmt.Sprintf("WAA Worker 启动 | %d/7 | %s", step, message))
		}
		worker, initErr := aistudio.NewNativeWorker(initCtx, account.id, options)
		cancel()
		if initErr != nil {
			if err := ctx.Err(); err != nil {
				manager.requests.log(account.label, "INFO", fmt.Sprintf(
					"WAA Worker 启动已取消 | 耗时=%s",
					time.Since(startedAt).Round(time.Millisecond),
				))
				return nil, err
			}
			manager.requests.log(account.label, "WARN", fmt.Sprintf(
				"WAA Worker 启动模型失败 | %d/%d | 模型=%s | 耗时=%s | 原因=%s",
				index+1, len(models), model, time.Since(modelStartedAt).Round(time.Millisecond), strings.TrimSpace(initErr.Error()),
			))
			failures = append(failures, fmt.Errorf("模型 %s: %w", model, initErr))
			continue
		}
		account.worker = worker
		account.warm.Store(true)
		workerReady = true
		manager.requests.log(account.label, "INFO", fmt.Sprintf(
			"WAA Worker 就绪 | 模型=%s | PID=%d | 耗时=%s",
			model, worker.State().PID, time.Since(startedAt).Round(time.Millisecond),
		))
		return worker, nil
	}
	err = errors.Join(failures...)
	manager.requests.log(account.label, "ERROR", fmt.Sprintf(
		"WAA Worker 启动失败 | 耗时=%s | 错误=%s",
		time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
	))
	return nil, &accountWorkerInitError{err: err}
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

func (manager *accountWorkerManager) promote(ctx context.Context, accountID string) error {
	manager.rebalanceMu.Lock()
	defer manager.rebalanceMu.Unlock()
	warm := manager.WarmAccountIDs()
	for _, warmAccountID := range warm {
		if warmAccountID == accountID {
			return nil
		}
	}
	for len(warm) >= manager.warmLimit {
		victim := manager.idleWarmVictim(accountID)
		if victim != "" {
			if err := manager.Reset(victim); err != nil {
				return err
			}
			break
		}
		if err := waitWarmCandidate(ctx, 100*time.Millisecond); err != nil {
			return err
		}
		warm = manager.WarmAccountIDs()
	}
	_, err := manager.Worker(ctx, accountID)
	return err
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
	for {
		if err := ctx.Err(); err != nil {
			if !notified {
				notify(errors.Join(append(failures, err)...))
			}
			return
		}
		manager.rebalanceMu.Lock()
		warm := manager.WarmAccountIDs()
		if len(warm) >= manager.warmLimit {
			manager.rebalanceMu.Unlock()
			manager.requests.log("service", "INFO", fmt.Sprintf(
				"WAA Worker 预热完成 | Worker=%d/%d | 耗时=%s",
				len(warm), manager.PrewarmTarget(), time.Since(startedAt).Round(time.Millisecond),
			))
			notify(nil)
			return
		}
		groups, err := manager.classifyBootstrapCandidates(warm)
		if err != nil {
			manager.rebalanceMu.Unlock()
			failures = append(failures, err)
			if !notified {
				notify(errors.Join(failures...))
			}
			return
		}
		if len(groups.StandbyReady) == 0 {
			manager.rebalanceMu.Unlock()
			if len(groups.StandbyBusy) > 0 {
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
		remaining := manager.warmLimit - len(warm)
		batchSize := min(manager.warmConcurrency, remaining, len(groups.StandbyReady))
		results := make(chan warmResult, batchSize)
		for _, accountID := range groups.StandbyReady[:batchSize] {
			go func(accountID string) {
				_, err := manager.Worker(ctx, accountID)
				results <- warmResult{accountID: accountID, err: err}
			}(accountID)
		}
		for range batchSize {
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
			if cooldownErr := manager.pool.MarkCooldown(result.accountID, "", time.Now().Add(5*time.Minute), result.err.Error()); cooldownErr != nil {
				failures = append(failures, cooldownErr)
			}
		}
		manager.rebalanceMu.Unlock()
	}
}

type warmResult struct {
	accountID string
	err       error
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
	var closeErrors []error
	for _, account := range accounts {
		account.mu.Lock()
		if account.worker != nil || account.runtimeLease != nil {
			closeErrors = append(closeErrors, closeAccountWorker(account))
		}
		account.mu.Unlock()
	}
	return errors.Join(closeErrors...)
}

func closeAccountWorker(account *accountWorker) error {
	var closeErr error
	if account.worker != nil {
		closeErr = account.worker.Close()
		account.worker = nil
	}
	if account.runtimeLease != nil {
		closeErr = errors.Join(closeErr, account.runtimeLease.Release())
		account.runtimeLease = nil
	}
	account.warm.Store(false)
	return closeErr
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
	lifecycle      context.Context
	service        aistudio.Service
	pool           *aistudio.AccountPool
	requests       *requestRegistry
	workers        *accountWorkerManager
	timeout        time.Duration
	state          atomic.Int32
	lifecycleMu    sync.Mutex
	transitionDone chan struct{}
	transitionErr  error
	dataContext    context.Context
	dataCancel     context.CancelFunc
	modelsMu       sync.RWMutex
	models         []aistudio.Model
	performanceMu  sync.RWMutex
	performance    map[string]map[string]generationPerformance
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
	return &trackedService{
		lifecycle: lifecycle, service: service, pool: pool, requests: requests, workers: workers,
		timeout: timeout, performance: make(map[string]map[string]generationPerformance),
	}
}

type serviceStoppedError struct{}

var errServiceTransitioning = errors.New("生成服务正在切换状态")

const (
	serviceStopped int32 = iota
	serviceLaunching
	serviceRunning
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

// Running 返回公开生成数据面是否接受请求
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

// Start 刷新模型并创建本次公开生成数据面
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
	service.state.Store(serviceLaunching)
	service.lifecycleMu.Unlock()
	stopCaller := context.AfterFunc(ctx, dataCancel)
	service.clearPerformance()
	service.clearModels()
	if launching != nil {
		launching()
	}
	service.requests.log("service", "INFO", "生成服务启动 | 1/2 | 同步模型目录")
	models, err := service.refreshModels(dataContext)
	if err != nil || len(models) == 0 {
		stopCaller()
		return models, false, service.finishLaunch(transitionDone, dataCancel, err)
	}
	service.requests.log("service", "INFO", fmt.Sprintf(
		"生成服务启动 | 2/2 | 预热 WAA Worker | 模型=%d | 目标=%d",
		len(models), service.workers.PrewarmTarget(),
	))
	firstWarm := service.workers.StartPrewarm(dataContext)
	select {
	case <-dataContext.Done():
		stopCaller()
		return nil, false, service.finishLaunch(transitionDone, dataCancel, dataContext.Err())
	case warmErr, ok := <-firstWarm:
		if !ok {
			warmErr = fmt.Errorf("WAA 预热未返回就绪账户")
		}
		if warmErr != nil {
			stopCaller()
			return nil, false, service.finishLaunch(transitionDone, dataCancel, warmErr)
		}
	}
	if !stopCaller() {
		return nil, false, service.finishLaunch(transitionDone, dataCancel, ctx.Err())
	}
	service.lifecycleMu.Lock()
	if service.transitionDone != transitionDone || service.state.Load() != serviceLaunching || dataContext.Err() != nil {
		service.lifecycleMu.Unlock()
		return nil, false, service.finishLaunch(transitionDone, dataCancel, dataContext.Err())
	}
	service.state.Store(serviceRunning)
	service.transitionDone = nil
	close(transitionDone)
	service.lifecycleMu.Unlock()
	return models, true, nil
}

func (service *trackedService) finishLaunch(transitionDone chan struct{}, dataCancel context.CancelFunc, launchErr error) error {
	dataCancel()
	service.workers.waitPrewarm()
	resetErr := service.workers.ResetAll()
	service.lifecycleMu.Lock()
	if service.transitionDone == transitionDone {
		service.state.Store(serviceStopped)
		service.dataContext = nil
		service.dataCancel = nil
		service.transitionErr = resetErr
		service.transitionDone = nil
		close(transitionDone)
	}
	service.lifecycleMu.Unlock()
	return errors.Join(launchErr, resetErr)
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
	service.performance[accountID][model] = generationPerformance{firstEvent: firstEvent, observedAt: time.Now()}
	service.performanceMu.Unlock()
}

func (service *trackedService) preferFast(accountIDs []string, model string) []string {
	result := append([]string(nil), accountIDs...)
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	access := service.pool.ModelAccessStates(result, model)
	service.performanceMu.RLock()
	sort.SliceStable(result, func(left int, right int) bool {
		leftVerified := access[result[left]] == aistudio.ModelAccessVerified
		rightVerified := access[result[right]] == aistudio.ModelAccessVerified
		if leftVerified != rightVerified {
			return leftVerified
		}
		leftObserved, leftExact, leftLatency, leftTime := service.performanceRankLocked(result[left], model)
		rightObserved, rightExact, rightLatency, rightTime := service.performanceRankLocked(result[right], model)
		if leftObserved != rightObserved {
			return leftObserved
		}
		if !leftObserved {
			return false
		}
		if leftExact != rightExact {
			return leftExact
		}
		if leftLatency != rightLatency {
			return leftLatency < rightLatency
		}
		return leftTime.After(rightTime)
	})
	service.performanceMu.RUnlock()
	return result
}

func (service *trackedService) performanceRankLocked(accountID string, model string) (bool, bool, time.Duration, time.Time) {
	models := service.performance[accountID]
	if len(models) == 0 {
		return false, false, 0, time.Time{}
	}
	if observed, ok := models[model]; ok {
		return true, true, observed.firstEvent, observed.observedAt
	}
	var latest generationPerformance
	for _, observed := range models {
		if observed.observedAt.After(latest.observedAt) {
			latest = observed
		}
	}
	return true, false, latest.firstEvent, latest.observedAt
}

func (service *trackedService) clearPerformance() {
	service.performanceMu.Lock()
	clear(service.performance)
	service.performanceMu.Unlock()
}

// Stop 停止公开生成数据面并释放活动 worker
func (service *trackedService) Stop() (bool, error) {
	service.lifecycleMu.Lock()
	state := service.state.Load()
	if state == serviceStopped {
		done := service.transitionDone
		service.lifecycleMu.Unlock()
		if done != nil {
			<-done
		}
		return false, nil
	}
	dataCancel := service.dataCancel
	if state == serviceLaunching {
		done := service.transitionDone
		service.state.Store(serviceStopped)
		service.lifecycleMu.Unlock()
		dataCancel()
		<-done
		service.requests.cancelAll()
		service.lifecycleMu.Lock()
		cleanupErr := service.transitionErr
		service.lifecycleMu.Unlock()
		return true, cleanupErr
	}
	transitionDone := make(chan struct{})
	service.transitionDone = transitionDone
	service.state.Store(serviceStopped)
	service.lifecycleMu.Unlock()
	dataCancel()
	service.workers.waitPrewarm()
	service.requests.cancelAll()
	resetErr := service.workers.ResetAll()
	service.lifecycleMu.Lock()
	service.dataContext = nil
	service.dataCancel = nil
	service.transitionErr = nil
	service.transitionDone = nil
	close(transitionDone)
	service.lifecycleMu.Unlock()
	return true, resetErr
}

// Models 返回最近一次启动时确认的模型目录
func (service *trackedService) Models(context.Context) ([]aistudio.Model, error) {
	return service.modelSnapshot(), nil
}

func (service *trackedService) modelSnapshot() []aistudio.Model {
	service.modelsMu.RLock()
	models := make([]aistudio.Model, 0, len(service.models))
	for _, model := range service.models {
		if service.pool.HasEligibleModel(model.ID) {
			models = append(models, model)
		}
	}
	service.modelsMu.RUnlock()
	return models
}

func (service *trackedService) publishModelAccess() {
	statuses := service.pool.Status()
	accounts := make([]api.AdminAccount, 0, len(statuses))
	for _, status := range statuses {
		accounts = append(accounts, adminAccountDTO(status))
	}
	service.requests.publish(api.AdminEvent{Type: "accounts", Data: map[string]any{"accounts": accounts}})
	service.requests.publish(api.AdminEvent{Type: "models", Data: map[string]any{"models": service.modelSnapshot()}})
}

func (service *trackedService) refreshModels(ctx context.Context) ([]aistudio.Model, error) {
	startedAt := time.Now()
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
		if requestCtx.Err() != nil {
			service.requests.log("service", "INFO", fmt.Sprintf(
				"模型目录同步已取消 | 耗时=%s",
				time.Since(startedAt).Round(time.Millisecond),
			))
			return nil, requestCtx.Err()
		}
		service.requests.log("service", "ERROR", fmt.Sprintf(
			"模型目录同步失败 | 耗时=%s | 错误=%s",
			time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
		))
		return nil, err
	}
	service.modelsMu.Lock()
	service.models = append([]aistudio.Model(nil), models...)
	service.modelsMu.Unlock()
	service.requests.log("service", "INFO", fmt.Sprintf(
		"模型目录同步完成 | 模型=%d | 耗时=%s",
		len(models), time.Since(startedAt).Round(time.Millisecond),
	))
	return append([]aistudio.Model(nil), models...), nil
}

// SyncModels 在账户变化后刷新或清空公开模型目录
func (service *trackedService) SyncModels(ctx context.Context) error {
	service.lifecycleMu.Lock()
	if service.state.Load() != serviceRunning {
		service.lifecycleMu.Unlock()
		service.clearModels()
		return nil
	}
	dataContext := service.dataContext
	service.lifecycleMu.Unlock()
	syncContext, cancel := context.WithCancel(ctx)
	stopData := context.AfterFunc(dataContext, cancel)
	_, err := service.refreshModels(syncContext)
	stopData()
	cancel()
	if err != nil {
		service.clearModels()
		return err
	}
	service.lifecycleMu.Lock()
	running := service.state.Load() == serviceRunning && service.dataContext == dataContext
	service.lifecycleMu.Unlock()
	if running {
		service.workers.StartPrewarm(dataContext)
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

func (service *trackedService) warmCandidates(ctx context.Context, selection aistudio.AccountSelection) ([]string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		warm := service.workers.WarmAccountIDs()
		groups, err := service.pool.ClassifyCandidates(selection, warm)
		if err != nil {
			return nil, err
		}
		if len(groups.WarmReady) > 0 {
			return service.preferFast(groups.WarmReady, selection.ModelID), nil
		}
		if len(groups.WarmAvailable) > 0 {
			return service.preferFast(groups.WarmAvailable, selection.ModelID), nil
		}
		if len(groups.StandbyReady) > 0 && len(warm) < service.workers.warmLimit {
			accountID := service.preferFast(groups.StandbyReady, selection.ModelID)[0]
			if err := service.workers.promote(ctx, accountID); err == nil {
				continue
			} else {
				if cooldownErr := service.pool.MarkCooldown(accountID, "", time.Now().Add(5*time.Minute), err.Error()); cooldownErr != nil {
					return nil, errors.Join(err, cooldownErr)
				}
				continue
			}
		}
		if len(groups.WarmBusy) > 0 {
			return service.preferFast(groups.WarmBusy, selection.ModelID), nil
		}
		if len(groups.StandbyReady) > 0 {
			accountID := service.preferFast(groups.StandbyReady, selection.ModelID)[0]
			if err := service.workers.promote(ctx, accountID); err == nil {
				continue
			} else {
				if cooldownErr := service.pool.MarkCooldown(accountID, "", time.Now().Add(5*time.Minute), err.Error()); cooldownErr != nil {
					return nil, errors.Join(err, cooldownErr)
				}
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				continue
			}
		}
		if len(groups.StandbyBusy) > 0 {
			if err := waitWarmCandidate(ctx, 100*time.Millisecond); err != nil {
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
	resourceID, err := service.pool.ResourceIDForContents(request.Contents)
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
	if unbound {
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
	var err error
	attempted := make(map[string]struct{}, maxAttempts)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		selection := aistudio.AccountSelection{
			ModelID: modelID, Method: "generateContent", AccountID: requestedAccountID, ResourceID: resourceID,
		}
		if unbound && len(attempted) > 0 {
			for _, status := range service.pool.Status() {
				if _, exists := attempted[status.ID]; status.Enabled && !exists {
					selection.AllowedAccountIDs = append(selection.AllowedAccountIDs, status.ID)
				}
			}
		}
		warm, warmErr := service.warmCandidates(requestCtx, selection)
		if warmErr != nil {
			if err == nil || !errors.Is(warmErr, aistudio.ErrNoEligibleAccount) {
				err = warmErr
			}
			break
		}
		selection.AllowedAccountIDs = warm
		nextLease, acquireErr := service.pool.AcquireFor(requestCtx, selection)
		if acquireErr != nil {
			if err == nil || !errors.Is(acquireErr, aistudio.ErrNoEligibleAccount) {
				err = acquireErr
			}
			break
		}
		lease = nextLease
		request.AccountID = lease.Account().ID
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
		source, err = service.service.Generate(attemptCtx, request)
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
				if changed, accessErr := service.pool.MarkModelAccess(request.AccountID, modelID, aistudio.ModelAccessVerified, ""); accessErr != nil {
					err = accessErr
				} else {
					if changed {
						service.publishModelAccess()
					}
					service.observePerformance(request.AccountID, modelID, time.Since(prepareStartedAt))
					break
				}
			}
		}
		workerFailed := service.workers.WorkerFailed(request.AccountID)
		retryable := retryableGenerateAccountError(requestCtx, err) || workerFailed && requestCtx.Err() == nil
		if workerFailed {
			if resetErr := service.workers.Reset(request.AccountID); resetErr != nil {
				err = errors.Join(err, resetErr)
				retryable = false
			}
		}
		if aistudio.DefinitiveAuthenticationFailure(err) {
			if stateErr := service.pool.MarkAuthRequired(request.AccountID, err.Error()); stateErr != nil {
				err = errors.Join(err, stateErr)
				retryable = false
			}
		}
		modelAccessDenied := aistudio.DefinitiveModelAccessFailure(err)
		if modelAccessDenied {
			if changed, stateErr := service.pool.MarkModelAccess(request.AccountID, modelID, aistudio.ModelAccessDenied, err.Error()); stateErr != nil {
				err = errors.Join(err, stateErr)
				retryable = false
			} else if changed {
				service.publishModelAccess()
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
		if !aistudio.DefinitiveAuthenticationFailure(err) && !modelAccessDenied {
			cooldownModel := modelID
			cooldownDuration := 30 * time.Second
			var workerInitError *accountWorkerInitError
			if errors.As(err, &workerInitError) || workerFailed {
				cooldownModel = ""
				cooldownDuration = 5 * time.Minute
			}
			if cooldownErr := service.pool.MarkCooldown(request.AccountID, cooldownModel, time.Now().Add(cooldownDuration), err.Error()); cooldownErr != nil {
				err = errors.Join(err, cooldownErr)
				break
			}
		}
		if attempt+1 == maxAttempts {
			break
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
		first, source, destination, lease, activity,
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
	activity *upstreamActivity,
) {
	state := "completed"
	var requestErr error
	terminal := false
	accountLabel := lease.Account().Config.Label
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
	defer cancel()
	defer func() {
		api.SetAccessLogGenerationResult(requestCtx, firstContent, contentChars, outputTokens, reasoningTokens)
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
				requestErr = requestCtx.Err()
				state = finalRequestState(requestErr)
				return
			}
		}
		if !ok {
			if err := requestCtx.Err(); err != nil {
				requestErr = err
				state = finalRequestState(err)
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
			if aistudio.DefinitiveAuthenticationFailure(event.Err) {
				if stateErr := service.pool.MarkAuthRequired(lease.Account().ID, event.Err.Error()); stateErr != nil {
					requestErr = errors.Join(requestErr, stateErr)
					event.Err = requestErr
				}
			}
			if aistudio.DefinitiveModelAccessFailure(event.Err) {
				if changed, stateErr := service.pool.MarkModelAccess(lease.Account().ID, modelID, aistudio.ModelAccessDenied, event.Err.Error()); stateErr != nil {
					requestErr = errors.Join(requestErr, stateErr)
					event.Err = requestErr
				} else if changed {
					service.publishModelAccess()
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
			requestErr = requestCtx.Err()
			state = finalRequestState(requestErr)
			return
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
