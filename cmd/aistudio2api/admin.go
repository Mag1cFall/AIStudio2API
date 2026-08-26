package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

// runtimeAdmin 投影运行时权威状态
type runtimeAdmin struct {
	lifecycle  context.Context
	pool       *aistudio.AccountPool
	store      *aistudio.AccountStore
	service    *trackedService
	requests   *requestRegistry
	login      aistudio.IsolatedLoginDriver
	workers    *accountWorkerManager
	headers    *accountHeaderProvider
	configPath string
	configMu   sync.RWMutex
	config     config.Config
}

// requestRegistry 保存活动请求与事件订阅
type requestRegistry struct {
	mu          sync.Mutex
	active      map[string]trackedRequest
	logs        []api.AdminLog
	subscribers map[chan api.AdminEvent]struct{}
	console     chan api.AdminLog
}

type trackedRequest struct {
	request api.AdminRequest
	cancel  context.CancelFunc
}

type adminOperationError struct {
	status  int
	code    string
	message string
}

func (err *adminOperationError) Error() string {
	return err.message
}

func (err *adminOperationError) HTTPStatus() int {
	return err.status
}

func (err *adminOperationError) ErrorCode() string {
	return err.code
}

// newRuntimeAdmin 创建管理端服务
func newRuntimeAdmin(
	lifecycle context.Context,
	pool *aistudio.AccountPool,
	store *aistudio.AccountStore,
	service *trackedService,
	registry *requestRegistry,
	login aistudio.IsolatedLoginDriver,
	workers *accountWorkerManager,
	headers *accountHeaderProvider,
	cfg config.Config,
) *runtimeAdmin {
	return &runtimeAdmin{
		lifecycle: lifecycle, pool: pool, store: store, service: service, requests: registry, login: login,
		workers: workers, headers: headers,
		configPath: ".env", config: cfg,
	}
}

// newRequestRegistry 创建活动请求注册表
func newRequestRegistry(ctx context.Context) *requestRegistry {
	registry := &requestRegistry{
		active:      make(map[string]trackedRequest),
		logs:        make([]api.AdminLog, 0, 128),
		subscribers: make(map[chan api.AdminEvent]struct{}),
		console:     make(chan api.AdminLog, 256),
	}
	go registry.writeConsole(ctx)
	return registry
}

func (admin *runtimeAdmin) Status(context.Context) (api.AdminStatus, error) {
	counts := api.AdminAccountCounts{}
	for _, account := range admin.pool.Status() {
		counts.Total++
		switch account.State {
		case aistudio.AccountReady:
			counts.Ready++
		case aistudio.AccountBusy:
			counts.Busy++
		case aistudio.AccountCooldown:
			counts.Cooldown++
		case aistudio.AccountAuthRequired:
			counts.AuthRequired++
		}
	}
	state := admin.service.State()
	running := state == "RUNNING"
	return api.AdminStatus{
		State:          state,
		Running:        running,
		Ready:          running && counts.Ready+counts.Busy > 0,
		Version:        buildVersion(),
		ActiveRequests: admin.requests.count(),
		Accounts:       counts,
	}, nil
}

func (admin *runtimeAdmin) Accounts(context.Context) ([]api.AdminAccount, error) {
	statuses := admin.pool.Status()
	accounts := make([]api.AdminAccount, 0, len(statuses))
	for _, status := range statuses {
		accounts = append(accounts, adminAccountDTO(status))
	}
	return accounts, nil
}

func (admin *runtimeAdmin) CreateAccount(ctx context.Context, input api.AccountInput) (api.AdminAccount, error) {
	accountConfig := aistudio.DefaultAccountConfig(input.Label)
	accountConfig.Enabled = input.Enabled
	accountConfig.Proxy = strings.TrimSpace(input.Proxy)
	if locale := strings.TrimSpace(input.Locale); locale != "" {
		accountConfig.Locale = locale
	}
	if timezone := strings.TrimSpace(input.Timezone); timezone != "" {
		accountConfig.Timezone = timezone
	}
	if err := accountConfig.Validate(); err != nil {
		return api.AdminAccount{}, invalidAccount(err)
	}
	directory, err := os.MkdirTemp("", "aistudio2api-account-login-*")
	if err != nil {
		return api.AdminAccount{}, fmt.Errorf("创建隔离登录目录: %w", err)
	}
	defer os.RemoveAll(directory)
	startedAt := time.Now()
	admin.requests.log("auth", "INFO", "账户添加 | 1/2 | 等待隔离登录")
	result, err := admin.login.Login(ctx, aistudio.IsolatedLoginRequest{
		AccountID: "new", Directory: directory, Proxy: admin.effectiveProxy(accountConfig.Proxy),
		Locale: accountConfig.Locale, Timezone: accountConfig.Timezone,
	})
	if err != nil {
		admin.requests.log("auth", "ERROR", fmt.Sprintf(
			"账户添加失败 | 耗时=%s | 错误=%s",
			time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
		))
		return api.AdminAccount{}, err
	}
	admin.requests.log("auth", "INFO", "账户添加 | 2/2 | 保存认证状态")
	if _, err := aistudio.NewSigner().Sign(result.StorageState); err != nil {
		return api.AdminAccount{}, fmt.Errorf("认证状态无法用于 AI Studio: %w", err)
	}
	account, err := admin.store.Create(accountConfig, result.StorageState)
	if err != nil {
		return api.AdminAccount{}, err
	}
	if err := camoufoxnative.PersistAccountFingerprint(directory, account.Directory); err != nil {
		return api.AdminAccount{}, errors.Join(err, admin.store.Delete(account))
	}
	if err := admin.headers.Add(account); err != nil {
		return api.AdminAccount{}, errors.Join(err, admin.store.Delete(account))
	}
	if err := admin.workers.Add(account); err != nil {
		return api.AdminAccount{}, errors.Join(err, admin.headers.Remove(account.ID), admin.store.Delete(account))
	}
	if err := admin.pool.Add(account); err != nil {
		return api.AdminAccount{}, errors.Join(
			err, admin.workers.Remove(account.ID), admin.headers.Remove(account.ID), admin.store.Delete(account),
		)
	}
	admin.requests.log("auth", "INFO", fmt.Sprintf(
		"账户添加完成 | 账户=%s | 耗时=%s",
		account.Config.Label, time.Since(startedAt).Round(time.Millisecond),
	))
	admin.syncModelCache(ctx)
	return admin.account(account.ID)
}

func (admin *runtimeAdmin) UpdateAccount(ctx context.Context, accountID string, input api.AccountInput) (api.AdminAccount, error) {
	if !strings.EqualFold(strings.TrimSpace(input.Label), strings.TrimSpace(accountID)) {
		return api.AdminAccount{}, invalidAccount(fmt.Errorf("账户邮箱不可修改"))
	}
	accountConfig := aistudio.DefaultAccountConfig(strings.ToLower(strings.TrimSpace(accountID)))
	accountConfig.Enabled = input.Enabled
	accountConfig.Proxy = strings.TrimSpace(input.Proxy)
	accountConfig.Locale = strings.TrimSpace(input.Locale)
	accountConfig.Timezone = strings.TrimSpace(input.Timezone)
	if err := accountConfig.Validate(); err != nil {
		return api.AdminAccount{}, invalidAccount(err)
	}
	lease, err := admin.pool.AcquireAccount(ctx, accountID)
	if err != nil {
		return api.AdminAccount{}, accountOperationError(err)
	}
	account := lease.Account()
	if err := lease.SaveConfig(accountConfig); err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	if err := admin.workers.Update(account); err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	if err := admin.headers.Update(account); err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	if err := lease.Release(); err != nil {
		return api.AdminAccount{}, err
	}
	admin.requests.log("auth", "INFO", "账户配置已更新 | 账户="+accountConfig.Label)
	admin.syncModelCache(ctx)
	return admin.account(account.ID)
}

func (admin *runtimeAdmin) DeleteAccount(ctx context.Context, accountID string) error {
	account, err := admin.account(accountID)
	if err != nil {
		return err
	}
	_, err = admin.pool.Remove(accountID, func(account *aistudio.Account) error {
		if err := admin.workers.Reset(account.ID); err != nil {
			return err
		}
		return admin.store.Delete(account)
	})
	if err != nil {
		return accountOperationError(err)
	}
	if err := errors.Join(admin.workers.Remove(accountID), admin.headers.Remove(accountID)); err != nil {
		return err
	}
	admin.requests.log("auth", "INFO", "账户已删除 | 账户="+account.Label)
	admin.syncModelCache(ctx)
	return nil
}

func (admin *runtimeAdmin) LoginAccount(ctx context.Context, accountID string) (api.AdminAccount, error) {
	lease, err := admin.pool.AcquireAccount(ctx, accountID)
	if err != nil {
		return api.AdminAccount{}, accountOperationError(err)
	}
	account := lease.Account()
	if err := admin.workers.Reset(account.ID); err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	directory, err := os.MkdirTemp("", "aistudio2api-account-login-*")
	if err != nil {
		return api.AdminAccount{}, errors.Join(fmt.Errorf("创建隔离登录目录: %w", err), lease.Release())
	}
	defer os.RemoveAll(directory)
	if err := camoufoxnative.PersistAccountFingerprint(account.Directory, directory); err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	startedAt := time.Now()
	admin.requests.log(account.Config.Label, "INFO", "账户登录 | 1/2 | 等待隔离登录")
	result, err := admin.login.Login(ctx, admin.loginRequest(account, directory))
	if err != nil {
		admin.requests.log(account.Config.Label, "ERROR", fmt.Sprintf(
			"账户登录失败 | 耗时=%s | 错误=%s",
			time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
		))
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	admin.requests.log(account.Config.Label, "INFO", "账户登录 | 2/2 | 保存认证状态")
	if _, err := aistudio.NewSigner().Sign(result.StorageState); err != nil {
		return api.AdminAccount{}, errors.Join(fmt.Errorf("认证状态无法用于 AI Studio: %w", err), lease.Release())
	}
	if err := camoufoxnative.PersistAccountFingerprint(directory, account.Directory); err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	if err := lease.SaveStorageState(result.StorageState); err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	if err := admin.pool.MarkReady(account.ID); err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	if err := admin.pool.ResetModelAccess(account.ID); err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	if err := lease.Release(); err != nil {
		return api.AdminAccount{}, err
	}
	admin.requests.log(account.Config.Label, "INFO", fmt.Sprintf(
		"账户登录完成 | 耗时=%s",
		time.Since(startedAt).Round(time.Millisecond),
	))
	admin.syncModelCache(ctx)
	return admin.account(account.ID)
}

func (admin *runtimeAdmin) VerifyAccount(ctx context.Context, accountID string) (api.AdminAccount, error) {
	lease, err := admin.pool.AcquireAccount(ctx, accountID)
	if err != nil {
		return api.AdminAccount{}, accountOperationError(err)
	}
	account := lease.Account()
	startedAt := time.Now()
	admin.requests.log(account.Config.Label, "INFO", "账户验证 | 访问 AI Studio")
	verification, err := admin.login.Verify(ctx, admin.loginRequest(account, account.Directory), account.StorageState)
	if err != nil {
		admin.requests.log(account.Config.Label, "ERROR", fmt.Sprintf(
			"账户验证失败 | 耗时=%s | 错误=%s",
			time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
		))
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	if verification.Authenticated {
		err = errors.Join(admin.pool.MarkReady(account.ID), admin.pool.ResetModelAccess(account.ID))
	} else {
		reason := strings.TrimSpace(verification.Reason)
		if reason == "" {
			reason = "AI Studio 登录已失效"
		}
		err = admin.pool.MarkAuthRequired(account.ID, reason)
	}
	if err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	if err := lease.Release(); err != nil {
		return api.AdminAccount{}, err
	}
	admin.requests.log(account.Config.Label, "INFO", fmt.Sprintf(
		"账户验证完成 | 已认证=%t | 耗时=%s",
		verification.Authenticated, time.Since(startedAt).Round(time.Millisecond),
	))
	admin.syncModelCache(ctx)
	return admin.account(account.ID)
}

func (admin *runtimeAdmin) StartService(ctx context.Context) (api.AdminStatus, error) {
	startedAt := time.Now()
	models, started, err := admin.service.Start(ctx, func() {
		status, statusErr := admin.Status(ctx)
		if statusErr == nil {
			admin.publishRuntimeSnapshot(ctx, status)
		}
	})
	if errors.Is(err, errServiceTransitioning) {
		return admin.Status(ctx)
	}
	if err != nil {
		status, statusErr := admin.Status(ctx)
		if statusErr == nil {
			admin.publishRuntimeSnapshot(ctx, status)
		}
		if errors.Is(err, context.Canceled) && admin.service.State() == "STOPPED" {
			admin.requests.log("service", "INFO", fmt.Sprintf(
				"生成服务启动已取消 | 耗时=%s",
				time.Since(startedAt).Round(time.Millisecond),
			))
			return status, statusErr
		}
		admin.requests.log("service", "ERROR", fmt.Sprintf(
			"生成服务启动失败 | 耗时=%s | 错误=%s",
			time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
		))
		if errors.Is(err, aistudio.ErrNoEligibleAccount) {
			return api.AdminStatus{}, &adminOperationError{
				status: http.StatusBadRequest, code: "account_required", message: "请先启用一个可用账户",
			}
		}
		return api.AdminStatus{}, err
	}
	if len(models) == 0 {
		admin.requests.log("service", "ERROR", fmt.Sprintf(
			"生成服务启动失败 | 耗时=%s | 错误=没有可用账户",
			time.Since(startedAt).Round(time.Millisecond),
		))
		return api.AdminStatus{}, &adminOperationError{
			status: http.StatusBadRequest, code: "account_required", message: "请先添加一个可用账户",
		}
	}
	if started {
		admin.requests.log("service", "INFO", fmt.Sprintf(
			"生成服务就绪 | 模型=%d | Worker=%d/%d | 耗时=%s",
			len(models), len(admin.workers.WarmAccountIDs()), admin.workers.PrewarmTarget(),
			time.Since(startedAt).Round(time.Millisecond),
		))
	} else {
		admin.requests.log("service", "INFO", fmt.Sprintf(
			"生成服务运行中 | 模型=%d | Worker=%d/%d",
			len(models), len(admin.workers.WarmAccountIDs()), admin.workers.PrewarmTarget(),
		))
	}
	status, err := admin.Status(ctx)
	if err == nil {
		admin.publishRuntimeSnapshot(ctx, status)
	}
	return status, err
}

func (admin *runtimeAdmin) StopService(ctx context.Context) (api.AdminStatus, error) {
	startedAt := time.Now()
	admin.requests.log("service", "INFO", fmt.Sprintf(
		"生成服务停止 | Worker=%d",
		len(admin.workers.WarmAccountIDs()),
	))
	stopped, err := admin.service.Stop()
	if err != nil {
		admin.requests.log("service", "ERROR", fmt.Sprintf(
			"生成服务停止失败 | 耗时=%s | 错误=%s",
			time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
		))
		return api.AdminStatus{}, err
	}
	if stopped {
		admin.requests.log("service", "INFO", fmt.Sprintf(
			"生成服务已停止 | 耗时=%s",
			time.Since(startedAt).Round(time.Millisecond),
		))
	} else {
		admin.requests.log("service", "INFO", "生成服务已处于停止状态")
	}
	status, err := admin.Status(ctx)
	if err == nil {
		admin.publishRuntimeSnapshot(ctx, status)
	}
	return status, err
}

func (admin *runtimeAdmin) ClearLogs(context.Context) error {
	admin.requests.clearLogs()
	return nil
}

// RecordAccessStart 保存公开 API 请求的开始记录
func (admin *runtimeAdmin) RecordAccessStart(entry api.AccessLog) {
	source := strings.TrimSpace(entry.Account)
	if source == "" {
		source = "request"
	}
	message := fmt.Sprintf("请求开始 | %s %q", entry.Method, entry.Path)
	if model := strings.TrimSpace(entry.Model); model != "" {
		message += " | " + model
	}
	if entry.Generation {
		message += fmt.Sprintf(
			" | 温度=%s | TopP=%s | 思考=%s | 最大=%s",
			entry.Temperature, entry.TopP, entry.Thinking, entry.MaxOutputTokens,
		)
	}
	admin.requests.log(source, "INFO", message)
}

// RecordAccessLog 保存公开 API 请求的最终访问记录
func (admin *runtimeAdmin) RecordAccessLog(entry api.AccessLog) {
	source := strings.TrimSpace(entry.Account)
	if source == "" {
		source = "service"
	}
	model := strings.TrimSpace(entry.Model)
	if model == "" {
		model = "-"
	}
	requestErr := strings.TrimSpace(entry.Error)
	level := "INFO"
	if entry.Canceled {
		level = "WARN"
	} else if entry.Status >= http.StatusBadRequest || requestErr != "" {
		level = "ERROR"
	}
	message := fmt.Sprintf(
		"%3d | %s | %s %q",
		entry.Status, entry.Latency.Round(time.Millisecond), entry.Method, entry.Path,
	)
	if entry.Generation {
		message += fmt.Sprintf(
			" | %s | 首事件=%s | 首正文=%s | %d字/正文%dt",
			model, logDuration(entry.FirstEvent), logDuration(entry.FirstContent),
			entry.ContentChars, entry.OutputTokens,
		)
		if entry.ReasoningTokens > 0 {
			message += fmt.Sprintf("/思考%dt", entry.ReasoningTokens)
		}
		if finishReason := strings.TrimSpace(entry.FinishReason); finishReason != "" {
			message += " | 终止=" + finishReason
		}
	} else if model != "-" {
		message += " | " + model
	}
	if entry.Canceled {
		message += " | client_canceled"
	} else if requestErr != "" {
		message += "\n错误: " + requestErr
	} else if entry.Status >= http.StatusBadRequest {
		message += fmt.Sprintf("\n错误: HTTP %d", entry.Status)
	}
	admin.requests.log(source, level, message)
}

func (admin *runtimeAdmin) syncModelCache(ctx context.Context) {
	_ = admin.service.SyncModels(ctx)
	status, err := admin.Status(ctx)
	if err == nil {
		admin.publishRuntimeSnapshot(ctx, status)
	}
}

// publishRuntimeSnapshot 推送管理页权威运行状态
func (admin *runtimeAdmin) publishRuntimeSnapshot(ctx context.Context, status api.AdminStatus) {
	models, err := admin.service.Models(ctx)
	if err != nil {
		return
	}
	accounts, err := admin.Accounts(ctx)
	if err != nil {
		return
	}
	admin.requests.publish(api.AdminEvent{Type: "status", Data: status})
	admin.requests.publish(api.AdminEvent{Type: "models", Data: map[string]any{"models": models}})
	admin.requests.publish(api.AdminEvent{Type: "accounts", Data: map[string]any{"accounts": accounts}})
}

func (admin *runtimeAdmin) account(accountID string) (api.AdminAccount, error) {
	for _, status := range admin.pool.Status() {
		if status.ID == accountID {
			return adminAccountDTO(status), nil
		}
	}
	return api.AdminAccount{}, accountOperationError(fmt.Errorf("%w: %s", aistudio.ErrAccountNotFound, accountID))
}

func (admin *runtimeAdmin) effectiveProxy(accountProxy string) string {
	if proxy := strings.TrimSpace(accountProxy); proxy != "" {
		return proxy
	}
	admin.configMu.RLock()
	proxy := admin.config.Proxy
	admin.configMu.RUnlock()
	return strings.TrimSpace(proxy)
}

func (admin *runtimeAdmin) loginRequest(account *aistudio.Account, directory string) aistudio.IsolatedLoginRequest {
	return aistudio.IsolatedLoginRequest{
		AccountID: account.ID, Directory: directory, Proxy: admin.effectiveProxy(account.Config.Proxy),
		Locale: account.Config.Locale, Timezone: account.Config.Timezone,
	}
}

func adminAccountDTO(status aistudio.AccountStatus) api.AdminAccount {
	models := make([]string, len(status.Models))
	copy(models, status.Models)
	return api.AdminAccount{
		ID: status.ID, Label: status.Label, Enabled: status.Enabled, State: string(status.State),
		Proxy: status.Proxy, Locale: status.Locale, Timezone: status.Timezone,
		Models: models, BenefitTier: status.BenefitTier, Message: status.Message,
	}
}

func invalidAccount(err error) error {
	return &adminOperationError{
		status: http.StatusBadRequest, code: "invalid_account", message: err.Error(),
	}
}

func accountOperationError(err error) error {
	switch {
	case errors.Is(err, aistudio.ErrAccountNotFound):
		return &adminOperationError{
			status: http.StatusNotFound, code: "account_not_found", message: err.Error(),
		}
	case errors.Is(err, aistudio.ErrAccountLeased):
		return &adminOperationError{
			status: http.StatusConflict, code: "account_busy", message: err.Error(),
		}
	default:
		return err
	}
}

func (admin *runtimeAdmin) RuntimeConfig(context.Context) (api.RuntimeConfig, error) {
	admin.configMu.RLock()
	cfg := admin.config
	admin.configMu.RUnlock()
	return runtimeConfigDTO(cfg), nil
}

func (admin *runtimeAdmin) UpdateRuntimeConfig(_ context.Context, value api.RuntimeConfig) (api.RuntimeConfig, error) {
	initTimeout, err := time.ParseDuration(value.InitTimeout)
	if err != nil {
		return api.RuntimeConfig{}, fmt.Errorf("INIT_TIMEOUT 无效: %w", err)
	}
	requestTimeout, err := time.ParseDuration(value.RequestTimeout)
	if err != nil {
		return api.RuntimeConfig{}, fmt.Errorf("REQUEST_TIMEOUT 无效: %w", err)
	}
	cfg := config.Config{
		AuthStates: value.AuthStates, ListenAddr: value.ListenAddr, ProxyAPIKey: value.APIKey,
		Proxy: value.Proxy, InitTimeout: initTimeout, RequestTimeout: requestTimeout,
		WarmWorkerLimit: value.WarmWorkerLimit, WarmStartupConcurrency: value.WarmStartupConcurrency,
		PerAccountConcurrency: value.PerAccountConcurrency,
		TemporaryChat:         value.TemporaryChat,
	}
	if err := cfg.Save(admin.configPath); err != nil {
		return api.RuntimeConfig{}, err
	}
	admin.configMu.Lock()
	admin.config = cfg
	admin.configMu.Unlock()
	admin.requests.log("service", "INFO", "服务配置已保存 | 重启后生效")
	return runtimeConfigDTO(cfg), nil
}

func (admin *runtimeAdmin) Cooldowns(context.Context) ([]api.AdminCooldown, error) {
	statuses := admin.pool.Status()
	cooldowns := make([]api.AdminCooldown, 0)
	now := time.Now()
	for _, account := range statuses {
		models := make(map[string]struct{}, len(account.Models))
		for _, modelID := range account.Models {
			models[modelID] = struct{}{}
		}
		effective := make(map[string]aistudio.CooldownState)
		if global, ok := account.Cooldowns["*"]; ok && global.Active(now) {
			for modelID := range models {
				effective[modelID] = global
			}
		}
		for modelID, cooldown := range account.Cooldowns {
			if modelID == "*" || !cooldown.Active(now) {
				continue
			}
			if _, ok := models[modelID]; !ok {
				continue
			}
			if current, ok := effective[modelID]; !ok || cooldown.Until.After(current.Until) {
				effective[modelID] = cooldown
			}
		}
		modelIDs := make([]string, 0, len(effective))
		for modelID := range effective {
			modelIDs = append(modelIDs, modelID)
		}
		sort.Strings(modelIDs)
		for _, modelID := range modelIDs {
			cooldown := effective[modelID]
			cooldowns = append(cooldowns, api.AdminCooldown{
				AccountID: account.ID, AccountLabel: account.Label,
				ModelID: modelID, Until: cooldown.Until, Reason: cooldown.Reason,
			})
		}
	}
	return cooldowns, nil
}

func (admin *runtimeAdmin) Requests(context.Context) ([]api.AdminRequest, error) {
	return admin.requests.list(), nil
}

func (admin *runtimeAdmin) CancelRequest(_ context.Context, id string) error {
	return admin.requests.cancel(id)
}

func (admin *runtimeAdmin) Events(ctx context.Context) (<-chan api.AdminEvent, error) {
	eventCtx, cancel := context.WithCancel(ctx)
	stopLifecycle := context.AfterFunc(admin.lifecycle, cancel)
	live := admin.requests.subscribe(eventCtx)
	models, err := admin.service.Models(eventCtx)
	if err != nil {
		stopLifecycle()
		cancel()
		return nil, err
	}
	status, err := admin.Status(eventCtx)
	if err != nil {
		stopLifecycle()
		cancel()
		return nil, err
	}
	accounts, err := admin.Accounts(eventCtx)
	if err != nil {
		stopLifecycle()
		cancel()
		return nil, err
	}
	cooldowns, err := admin.Cooldowns(eventCtx)
	if err != nil {
		stopLifecycle()
		cancel()
		return nil, err
	}
	logs := admin.requests.logsSnapshot()
	events := make(chan api.AdminEvent, 16)
	go func() {
		defer stopLifecycle()
		defer cancel()
		defer close(events)
		initial := []api.AdminEvent{
			{Type: "status", Data: status},
			{Type: "models", Data: map[string]any{"models": models}},
			{Type: "accounts", Data: map[string]any{"accounts": accounts}},
		}
		for _, entry := range logs {
			initial = append(initial, api.AdminEvent{Type: "log", Data: entry})
		}
		initial = append(initial, api.AdminEvent{Type: "cooldowns", Data: cooldowns})
		for _, request := range admin.requests.list() {
			initial = append(initial, api.AdminEvent{Type: "request", Data: request})
		}
		for _, event := range initial {
			select {
			case events <- event:
			case <-eventCtx.Done():
				return
			}
		}
		for {
			select {
			case event, ok := <-live:
				if !ok {
					return
				}
				updates, err := admin.requestUpdates(eventCtx, event)
				if err != nil {
					return
				}
				for _, update := range updates {
					select {
					case events <- update:
					case <-eventCtx.Done():
						return
					}
				}
			case <-eventCtx.Done():
				return
			}
		}
	}()
	return events, nil
}

func (admin *runtimeAdmin) requestUpdates(ctx context.Context, request api.AdminEvent) ([]api.AdminEvent, error) {
	if request.Type != "request" {
		return []api.AdminEvent{request}, nil
	}
	status, err := admin.Status(ctx)
	if err != nil {
		return nil, err
	}
	accounts, err := admin.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	cooldowns, err := admin.Cooldowns(ctx)
	if err != nil {
		return nil, err
	}
	updates := []api.AdminEvent{
		request,
		{Type: "status", Data: status},
		{Type: "accounts", Data: map[string]any{"accounts": accounts}},
		{Type: "cooldowns", Data: cooldowns},
	}
	return updates, nil
}

func (registry *requestRegistry) start(request aistudio.GenerateRequest, cancel context.CancelFunc) {
	tracked := trackedRequest{
		request: api.AdminRequest{
			ID: request.ID, Model: request.Model, AccountID: request.AccountID,
			State: "queued", StartedAt: time.Now().UTC(),
		},
		cancel: cancel,
	}
	registry.mu.Lock()
	registry.active[request.ID] = tracked
	registry.publishLocked(api.AdminEvent{Type: "request", Data: tracked.request})
	registry.mu.Unlock()
}

func (registry *requestRegistry) markRunning(id string, accountID string, accountLabel string) {
	registry.mu.Lock()
	tracked, exists := registry.active[id]
	if exists {
		tracked.request.AccountID = accountID
		tracked.request.AccountLabel = accountLabel
		tracked.request.State = "running"
		registry.active[id] = tracked
		registry.publishLocked(api.AdminEvent{Type: "request", Data: tracked.request})
	}
	registry.mu.Unlock()
}

func (registry *requestRegistry) finish(id string, state string, requestErr error) {
	registry.mu.Lock()
	tracked, exists := registry.active[id]
	if exists {
		delete(registry.active, id)
		tracked.request.State = state
		registry.publishLocked(api.AdminEvent{Type: "request", Data: tracked.request})
	}
	registry.mu.Unlock()
}

func (registry *requestRegistry) list() []api.AdminRequest {
	registry.mu.Lock()
	requests := make([]api.AdminRequest, 0, len(registry.active))
	for _, tracked := range registry.active {
		requests = append(requests, tracked.request)
	}
	registry.mu.Unlock()
	sort.Slice(requests, func(left int, right int) bool {
		return requests[left].StartedAt.Before(requests[right].StartedAt)
	})
	return requests
}

func (registry *requestRegistry) count() int {
	registry.mu.Lock()
	count := len(registry.active)
	registry.mu.Unlock()
	return count
}

func (registry *requestRegistry) cancel(id string) error {
	registry.mu.Lock()
	tracked, exists := registry.active[id]
	registry.mu.Unlock()
	if !exists {
		return &adminOperationError{
			status: http.StatusNotFound, code: "request_not_found",
			message: fmt.Sprintf("活动请求不存在: %s", id),
		}
	}
	tracked.cancel()
	return nil
}

func logDuration(value time.Duration) string {
	if value <= 0 {
		return "-"
	}
	return value.Round(time.Millisecond).String()
}

func (registry *requestRegistry) cancelAll() {
	registry.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(registry.active))
	for _, tracked := range registry.active {
		cancels = append(cancels, tracked.cancel)
	}
	registry.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (registry *requestRegistry) log(source string, level string, message string) {
	registry.mu.Lock()
	entry := registry.appendLogLocked(source, level, message)
	registry.mu.Unlock()
	select {
	case registry.console <- entry:
	default:
	}
}

func (registry *requestRegistry) appendLogLocked(source string, level string, message string) api.AdminLog {
	entry := api.AdminLog{Time: time.Now().UTC(), Level: level, Source: source, Message: message}
	registry.logs = append(registry.logs, entry)
	if len(registry.logs) >= 2200 {
		copy(registry.logs, registry.logs[len(registry.logs)-2000:])
		registry.logs = registry.logs[:2000]
	}
	registry.publishLocked(api.AdminEvent{Type: "log", Data: entry})
	return entry
}

func (registry *requestRegistry) writeConsole(ctx context.Context) {
	for {
		select {
		case entry := <-registry.console:
			switch strings.ToUpper(entry.Level) {
			case "ERROR":
				slog.Error(entry.Message, "source", entry.Source)
			case "WARN":
				slog.Warn(entry.Message, "source", entry.Source)
			default:
				slog.Info(entry.Message, "source", entry.Source)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (registry *requestRegistry) logsSnapshot() []api.AdminLog {
	registry.mu.Lock()
	logs := append([]api.AdminLog(nil), registry.logs...)
	registry.mu.Unlock()
	return logs
}

func (registry *requestRegistry) clearLogs() {
	registry.mu.Lock()
	registry.logs = registry.logs[:0]
	registry.mu.Unlock()
}

func (registry *requestRegistry) subscribe(ctx context.Context) <-chan api.AdminEvent {
	events := make(chan api.AdminEvent, 256)
	registry.mu.Lock()
	registry.subscribers[events] = struct{}{}
	registry.mu.Unlock()
	go func() {
		<-ctx.Done()
		registry.mu.Lock()
		if _, ok := registry.subscribers[events]; ok {
			delete(registry.subscribers, events)
			close(events)
		}
		registry.mu.Unlock()
	}()
	return events
}

func (registry *requestRegistry) publishLocked(event api.AdminEvent) {
	for subscriber := range registry.subscribers {
		select {
		case subscriber <- event:
		default:
			delete(registry.subscribers, subscriber)
			close(subscriber)
		}
	}
}

// publish 向管理页订阅者发布增量事件
func (registry *requestRegistry) publish(event api.AdminEvent) {
	registry.mu.Lock()
	registry.publishLocked(event)
	registry.mu.Unlock()
}

func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "(devel)"
	}
	return info.Main.Version
}

func runtimeConfigDTO(cfg config.Config) api.RuntimeConfig {
	return api.RuntimeConfig{
		AuthStates: cfg.AuthStates, ListenAddr: cfg.ListenAddr, APIKey: cfg.ProxyAPIKey,
		Proxy: cfg.Proxy, InitTimeout: cfg.InitTimeout.String(), RequestTimeout: cfg.RequestTimeout.String(),
		WarmWorkerLimit: cfg.WarmWorkerLimit, WarmStartupConcurrency: cfg.WarmStartupConcurrency,
		PerAccountConcurrency: cfg.PerAccountConcurrency,
		TemporaryChat:         cfg.TemporaryChat,
	}
}

var _ api.AdminService = (*runtimeAdmin)(nil)
