package app

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
	"github.com/Mag1cFall/AIStudio2API/internal/chromeauth"
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
	config     config.Config
}

// requestRegistry 保存活动请求与事件订阅
type requestRegistry struct {
	mu          sync.Mutex
	active      map[string]trackedRequest
	logs        []api.AdminLog
	subscribers map[*eventSubscriber]struct{}
	console     chan api.AdminLog
}

type eventSubscriber struct {
	ctx             context.Context
	mu              sync.Mutex
	events          chan api.AdminEvent
	wake            chan struct{}
	pending         []api.AdminEvent
	pendingLogs     int
	pendingRequests int
}

const (
	adminLogRetain             = 2000
	adminLogCompactAt          = 2200
	adminRequestRetain         = 512
	adminRequestCompactAt      = 640
	adminRequestRefreshLatency = 50 * time.Millisecond
)

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
		subscribers: make(map[*eventSubscriber]struct{}),
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

func (admin *runtimeAdmin) CreateAccount(ctx context.Context, input api.AccountCreateInput) (api.AdminAccount, error) {
	accountConfig := aistudio.DefaultAccountConfig("")
	accountConfig.Proxy = strings.TrimSpace(input.Proxy)
	if locale := strings.TrimSpace(input.Locale); locale != "" {
		accountConfig.Locale = locale
	}
	if timezone := strings.TrimSpace(input.Timezone); timezone != "" {
		accountConfig.Timezone = timezone
	}
	if err := config.ValidateProxy(accountConfig.Proxy); err != nil {
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
	accountConfig.Label = result.Email
	if err := accountConfig.Validate(); err != nil {
		return api.AdminAccount{}, invalidAccount(err)
	}
	created, err := admin.addAccount(ctx, accountConfig, result.StorageState, directory)
	if err != nil {
		return api.AdminAccount{}, err
	}
	admin.requests.log("auth", "INFO", fmt.Sprintf(
		"账户添加完成 | 账户=%s | 耗时=%s",
		created.Label, time.Since(startedAt).Round(time.Millisecond),
	))
	return created, nil
}

func (admin *runtimeAdmin) ChromeImportProfiles(context.Context) ([]api.ChromeImportProfile, error) {
	root, err := chromeauth.DefaultChromeRoot()
	if err != nil {
		return nil, err
	}
	accounts, err := chromeauth.Discover(root)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]struct{})
	for _, status := range admin.pool.Status() {
		existing[strings.ToLower(strings.TrimSpace(status.ID))] = struct{}{}
	}
	profiles := make([]api.ChromeImportProfile, 0, len(accounts))
	for _, account := range accounts {
		email := strings.ToLower(strings.TrimSpace(account.Email))
		if !account.Importable || email == "" {
			continue
		}
		if _, exists := existing[email]; exists {
			continue
		}
		profiles = append(profiles, api.ChromeImportProfile{
			Profile: account.Profile, DisplayName: account.DisplayName, Email: email, Locale: account.Locale,
		})
	}
	return profiles, nil
}

func (admin *runtimeAdmin) ImportChromeAccounts(ctx context.Context, input api.ChromeImportInput) ([]api.AdminAccount, error) {
	if len(input.Profiles) == 0 {
		return nil, invalidAccount(fmt.Errorf("未选择 Chrome 账号"))
	}
	root, err := chromeauth.DefaultChromeRoot()
	if err != nil {
		return nil, err
	}
	accountProxy := strings.TrimSpace(input.Proxy)
	results, err := chromeauth.Import(ctx, chromeauth.ImportOptions{
		ChromeRoot: root, Proxy: admin.effectiveProxy(accountProxy), Profiles: input.Profiles,
	})
	if err != nil {
		return nil, err
	}
	configs := make([]aistudio.AccountConfig, len(results))
	seen := make(map[string]struct{}, len(results))
	for index, result := range results {
		email := strings.ToLower(strings.TrimSpace(result.Email))
		if _, exists := seen[email]; exists {
			return nil, invalidAccount(fmt.Errorf("Chrome 账号重复: %s", email))
		}
		seen[email] = struct{}{}
		accountConfig := aistudio.DefaultAccountConfig(email)
		accountConfig.Proxy = accountProxy
		if locale := strings.TrimSpace(input.Locale); locale != "" {
			accountConfig.Locale = locale
		} else if locale := strings.TrimSpace(result.Locale); locale != "" {
			accountConfig.Locale = locale
		}
		if timezone := strings.TrimSpace(input.Timezone); timezone != "" {
			accountConfig.Timezone = timezone
		}
		if err := accountConfig.Validate(); err != nil {
			return nil, invalidAccount(err)
		}
		if _, err := aistudio.NewSigner().Sign(result.State); err != nil {
			return nil, fmt.Errorf("认证状态无法用于 AI Studio: %w", err)
		}
		configs[index] = accountConfig
	}
	accounts := make([]api.AdminAccount, 0, len(results))
	for index, result := range results {
		account, err := admin.addAccount(ctx, configs[index], result.State, "")
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
		admin.requests.log("auth", "INFO", "Chrome 账户已导入 | 账户="+account.Label)
	}
	return accounts, nil
}

func (admin *runtimeAdmin) addAccount(
	ctx context.Context,
	accountConfig aistudio.AccountConfig,
	state aistudio.StorageState,
	fingerprintDirectory string,
) (created api.AdminAccount, resultErr error) {
	account, publishLease, err := admin.store.Create(accountConfig, state)
	if err != nil {
		return api.AdminAccount{}, err
	}
	defer func() {
		if publishLease != nil {
			resultErr = errors.Join(resultErr, publishLease.Release())
		}
	}()
	if fingerprintDirectory != "" {
		if err := camoufoxnative.PersistAccountFingerprint(fingerprintDirectory, account.Directory); err != nil {
			return api.AdminAccount{}, errors.Join(err, admin.store.Delete(account))
		}
	}
	if err := admin.headers.Add(account); err != nil {
		return api.AdminAccount{}, errors.Join(err, admin.store.Delete(account))
	}
	if err := admin.workers.Add(account); err != nil {
		return api.AdminAccount{}, errors.Join(err, admin.headers.Remove(account.ID), admin.store.Delete(account))
	}
	if err := admin.service.changeModels(func() error {
		return admin.pool.Add(account)
	}); err != nil {
		return api.AdminAccount{}, errors.Join(
			err, admin.workers.Remove(account.ID), admin.headers.Remove(account.ID), admin.store.Delete(account),
		)
	}
	if err := publishLease.Release(); err != nil {
		return api.AdminAccount{}, err
	}
	publishLease = nil
	admin.syncAccountModelCatalog(ctx, account)
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
	headerUpdate, err := admin.headers.prepareUpdate(account, accountConfig)
	if err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	workerUpdate, err := admin.workers.prepareUpdate(account, accountConfig)
	if err != nil {
		headerUpdate.Discard()
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	if err := admin.service.changeModels(func() error {
		return lease.SaveConfig(accountConfig)
	}); err != nil {
		workerUpdate.Discard()
		headerUpdate.Discard()
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	workerUpdate.Commit()
	headerUpdate.Commit()
	if err := lease.Release(); err != nil {
		return api.AdminAccount{}, err
	}
	admin.syncModelCache()
	admin.requests.log("auth", "INFO", "账户配置已更新 | 账户="+accountConfig.Label)
	return admin.account(account.ID)
}

func (admin *runtimeAdmin) DeleteAccount(_ context.Context, accountID string) error {
	account, err := admin.account(accountID)
	if err != nil {
		return err
	}
	defer admin.syncModelCache()
	err = admin.service.changeModels(func() error {
		_, removeErr := admin.pool.Remove(accountID, func(account *aistudio.Account) error {
			if workerErr := admin.workers.Reset(account.ID); workerErr != nil {
				return workerErr
			}
			return admin.store.Delete(account)
		})
		return removeErr
	})
	if err != nil {
		return accountOperationError(err)
	}
	admin.service.removeAccountModelRetry(accountID)
	if err := errors.Join(admin.workers.Remove(accountID), admin.headers.Remove(accountID)); err != nil {
		return err
	}
	admin.requests.log("auth", "INFO", "账户已删除 | 账户="+account.Label)
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
	if !strings.EqualFold(strings.TrimSpace(result.Email), account.ID) {
		return api.AdminAccount{}, errors.Join(
			invalidAccount(fmt.Errorf("登录邮箱与账户不一致: %s", result.Email)), lease.Release(),
		)
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
	if err := admin.service.changeModels(func() error {
		return errors.Join(
			admin.pool.MarkReady(account.ID),
			admin.pool.ResetModelAccess(account.ID),
			admin.pool.SetCatalog(account.ID, account.BenefitTier, nil),
		)
	}); err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	if err := lease.Release(); err != nil {
		return api.AdminAccount{}, err
	}
	admin.syncAccountModelCatalog(ctx, account)
	admin.requests.log(account.Config.Label, "INFO", fmt.Sprintf(
		"账户登录完成 | 耗时=%s",
		time.Since(startedAt).Round(time.Millisecond),
	))
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
	err = admin.service.changeModels(func() error {
		if verification.Authenticated {
			return errors.Join(
				admin.pool.MarkReady(account.ID),
				admin.pool.ResetModelAccess(account.ID),
				admin.pool.SetCatalog(account.ID, account.BenefitTier, nil),
			)
		}
		reason := strings.TrimSpace(verification.Reason)
		if reason == "" {
			reason = "AI Studio 登录已失效"
		}
		return admin.pool.MarkAuthRequired(account.ID, reason)
	})
	if err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	if err := lease.Release(); err != nil {
		return api.AdminAccount{}, err
	}
	if verification.Authenticated {
		admin.syncAccountModelCatalog(ctx, account)
	} else {
		admin.service.publishModelAccess()
	}
	admin.requests.log(account.Config.Label, "INFO", fmt.Sprintf(
		"账户验证完成 | 已认证=%t | 耗时=%s",
		verification.Authenticated, time.Since(startedAt).Round(time.Millisecond),
	))
	return admin.account(account.ID)
}

// StartService 使用管理器提供的启动生命周期启动生成服务
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
	if requestID := strings.TrimSpace(entry.RequestID); requestID != "" {
		message += " | ID=" + requestID
	}
	if model := strings.TrimSpace(entry.Model); model != "" {
		message += " | " + model
	}
	if entry.Generation {
		message += fmt.Sprintf(
			" | 输入=%d条/%d字/%d媒体/%dB/%d文件 | 温度=%s | TopP=%s | 思考=%s | 最大=%s",
			entry.InputMessages, entry.InputTextChars, entry.InputMedia, entry.InputMediaBytes, entry.InputFiles,
			entry.Temperature, entry.TopP, entry.Thinking, entry.MaxOutputTokens,
		)
	}
	admin.requests.log(source, "INFO", message)
}

// RecordAccessLog 保存公开 API 请求的最终访问记录
func (admin *runtimeAdmin) RecordAccessLog(entry api.AccessLog) {
	source := strings.TrimSpace(entry.Account)
	if source == "" {
		source = "request"
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
	if requestID := strings.TrimSpace(entry.RequestID); requestID != "" {
		message += " | ID=" + requestID
	}
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
		if entry.UpstreamBytes > 0 {
			message += fmt.Sprintf(" | 上游=%dB", entry.UpstreamBytes)
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

// syncModelCache 在账户写入后刷新权威快照
func (admin *runtimeAdmin) syncModelCache() {
	ctx := admin.lifecycle
	_ = admin.service.SyncModels(ctx)
	status, err := admin.Status(ctx)
	if err == nil {
		admin.publishRuntimeSnapshot(ctx, status)
	}
}

func (admin *runtimeAdmin) syncAccountModelCatalog(ctx context.Context, account *aistudio.Account) {
	models, err := admin.service.syncAccountModels(ctx, account.ID)
	if err != nil {
		admin.requests.log(account.Config.Label, "WARN", fmt.Sprintf(
			"账户模型目录等待重试 | 错误=%s", strings.TrimSpace(err.Error()),
		))
		admin.service.publishModelAccess()
		return
	}
	admin.requests.log(account.Config.Label, "INFO", fmt.Sprintf("账户模型目录同步完成 | 模型=%d", len(models)))
	admin.service.publishModelAccess()
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
	return strings.TrimSpace(admin.config.Proxy)
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
	cfg, err := config.Load(admin.configPath)
	if err != nil {
		return api.RuntimeConfig{}, err
	}
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
		WarmWorkerLimit: value.WarmWorkerLimit, MaxActiveWorkers: value.MaxActiveWorkers,
		WarmStartupConcurrency: value.WarmStartupConcurrency,
		PerAccountConcurrency:  value.PerAccountConcurrency,
		TemporaryChat:          value.TemporaryChat,
	}
	if err := cfg.Save(admin.configPath); err != nil {
		return api.RuntimeConfig{}, err
	}
	admin.requests.log("service", "INFO", "服务配置已保存")
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

// adminEventSource 提供管理事件流的当前快照
type adminEventSource interface {
	Models(context.Context) ([]aistudio.Model, error)
	Status(context.Context) (api.AdminStatus, error)
	Accounts(context.Context) ([]api.AdminAccount, error)
	Cooldowns(context.Context) ([]api.AdminCooldown, error)
}

// Models 返回当前运行时模型快照
func (admin *runtimeAdmin) Models(ctx context.Context) ([]aistudio.Model, error) {
	return admin.service.Models(ctx)
}

func (admin *runtimeAdmin) Events(ctx context.Context) (<-chan api.AdminEvent, error) {
	return openAdminEvents(ctx, admin.lifecycle, admin.requests, admin)
}

// openAdminEvents 创建绑定进程生命周期的管理事件流
func openAdminEvents(
	ctx context.Context,
	lifecycle context.Context,
	requests *requestRegistry,
	source adminEventSource,
) (<-chan api.AdminEvent, error) {
	eventCtx, cancel := context.WithCancel(ctx)
	stopLifecycle := context.AfterFunc(lifecycle, cancel)
	subscriber := requests.subscribe(eventCtx)
	models, err := source.Models(eventCtx)
	if err != nil {
		stopLifecycle()
		cancel()
		return nil, err
	}
	status, err := source.Status(eventCtx)
	if err != nil {
		stopLifecycle()
		cancel()
		return nil, err
	}
	accounts, err := source.Accounts(eventCtx)
	if err != nil {
		stopLifecycle()
		cancel()
		return nil, err
	}
	cooldowns, err := source.Cooldowns(eventCtx)
	if err != nil {
		stopLifecycle()
		cancel()
		return nil, err
	}
	live := requests.activateSubscriber(
		subscriber,
		[]api.AdminEvent{
			{Type: "status", Data: status},
			{Type: "models", Data: map[string]any{"models": models}},
			{Type: "accounts", Data: map[string]any{"accounts": accounts}},
		},
		[]api.AdminEvent{{Type: "cooldowns", Data: cooldowns}},
	)
	events := make(chan api.AdminEvent, 16)
	go func() {
		defer stopLifecycle()
		defer cancel()
		defer close(events)
		var refreshTimer *time.Timer
		var refresh <-chan time.Time
		refreshAccounts := false
		refreshCooldowns := false
		defer func() {
			if refreshTimer != nil {
				refreshTimer.Stop()
			}
		}()
		send := func(event api.AdminEvent) bool {
			select {
			case events <- event:
				return true
			case <-eventCtx.Done():
				return false
			}
		}
		scheduleRequestRefresh := func(event api.AdminEvent) {
			request := event.Data.(api.AdminRequest)
			if request.State != "queued" {
				refreshAccounts = true
			}
			if request.State != "queued" && request.State != "running" {
				refreshCooldowns = true
			}
			if refresh != nil {
				return
			}
			if refreshTimer == nil {
				refreshTimer = time.NewTimer(adminRequestRefreshLatency)
			} else {
				refreshTimer.Reset(adminRequestRefreshLatency)
			}
			refresh = refreshTimer.C
		}
		for {
			select {
			case event, ok := <-live:
				if !ok {
					return
				}
				if !send(event) {
					return
				}
				if event.Type == "request" {
					scheduleRequestRefresh(event)
				}
			case <-refresh:
				refresh = nil
				accountsChanged := refreshAccounts
				cooldownsChanged := refreshCooldowns
				refreshAccounts = false
				refreshCooldowns = false
				updates, err := adminRequestStateUpdates(eventCtx, source, accountsChanged, cooldownsChanged)
				if err != nil {
					return
				}
				for _, update := range updates {
					if !send(update) {
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

// adminRequestStateUpdates 合并请求状态引起的管理页快照变化
func adminRequestStateUpdates(
	ctx context.Context,
	source adminEventSource,
	accountsChanged bool,
	cooldownsChanged bool,
) ([]api.AdminEvent, error) {
	status, err := source.Status(ctx)
	if err != nil {
		return nil, err
	}
	updates := []api.AdminEvent{{Type: "status", Data: status}}
	if accountsChanged {
		accounts, accountsErr := source.Accounts(ctx)
		if accountsErr != nil {
			return nil, accountsErr
		}
		updates = append(updates, api.AdminEvent{Type: "accounts", Data: map[string]any{"accounts": accounts}})
	}
	if cooldownsChanged {
		cooldowns, cooldownsErr := source.Cooldowns(ctx)
		if cooldownsErr != nil {
			return nil, cooldownsErr
		}
		updates = append(updates, api.AdminEvent{Type: "cooldowns", Data: cooldowns})
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
	if len(registry.logs) >= adminLogCompactAt {
		copy(registry.logs, registry.logs[len(registry.logs)-adminLogRetain:])
		registry.logs = registry.logs[:adminLogRetain]
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

func (registry *requestRegistry) clearLogs() {
	registry.mu.Lock()
	registry.logs = registry.logs[:0]
	registry.mu.Unlock()
}

// newEventSubscriber 创建管理页有界事件队列
func newEventSubscriber(ctx context.Context) *eventSubscriber {
	return &eventSubscriber{
		ctx: ctx, events: make(chan api.AdminEvent, 16), wake: make(chan struct{}, 1),
		pending: make([]api.AdminEvent, 0, 256),
	}
}

func (subscriber *eventSubscriber) enqueue(event api.AdminEvent) {
	subscriber.mu.Lock()
	subscriber.enqueueLocked(event)
	subscriber.mu.Unlock()
	subscriber.notify()
}

func (subscriber *eventSubscriber) enqueueLocked(event api.AdminEvent) {
	switch event.Type {
	case "status", "models", "accounts", "cooldowns":
		for index := len(subscriber.pending) - 1; index >= 0; index-- {
			if subscriber.pending[index].Type == event.Type {
				subscriber.pending[index] = event
				return
			}
		}
	case "request":
		request := event.Data.(api.AdminRequest)
		for index := len(subscriber.pending) - 1; index >= 0; index-- {
			pending := subscriber.pending[index]
			if pending.Type == "request" && pending.Data.(api.AdminRequest).ID == request.ID {
				subscriber.pending[index] = event
				return
			}
		}
		subscriber.pendingRequests++
	case "log":
		subscriber.pendingLogs++
	}
	subscriber.pending = append(subscriber.pending, event)
	if subscriber.pendingLogs >= adminLogCompactAt {
		subscriber.trimPendingLocked("log", adminLogRetain)
	}
	if subscriber.pendingRequests >= adminRequestCompactAt {
		subscriber.trimPendingLocked("request", adminRequestRetain)
	}
}

func (subscriber *eventSubscriber) trimPendingLocked(eventType string, retain int) {
	count := subscriber.pendingLogs
	if eventType == "request" {
		count = subscriber.pendingRequests
	}
	drop := count - retain
	compacted := subscriber.pending[:0]
	for _, event := range subscriber.pending {
		if event.Type == eventType && drop > 0 {
			drop--
			continue
		}
		compacted = append(compacted, event)
	}
	subscriber.pending = compacted
	if eventType == "request" {
		subscriber.pendingRequests = retain
	} else {
		subscriber.pendingLogs = retain
	}
}

func (subscriber *eventSubscriber) activate(initial []api.AdminEvent) {
	subscriber.mu.Lock()
	buffered := append([]api.AdminEvent(nil), subscriber.pending...)
	subscriber.pending = subscriber.pending[:0]
	subscriber.pendingLogs = 0
	subscriber.pendingRequests = 0
	for _, event := range initial {
		subscriber.enqueueLocked(event)
	}
	for _, event := range buffered {
		if event.Type != "log" {
			subscriber.enqueueLocked(event)
		}
	}
	subscriber.mu.Unlock()
	subscriber.notify()
}

func (subscriber *eventSubscriber) notify() {
	select {
	case subscriber.wake <- struct{}{}:
	default:
	}
}

func (subscriber *eventSubscriber) next() (api.AdminEvent, bool) {
	subscriber.mu.Lock()
	defer subscriber.mu.Unlock()
	if len(subscriber.pending) == 0 {
		return api.AdminEvent{}, false
	}
	event := subscriber.pending[0]
	subscriber.pending[0] = api.AdminEvent{}
	subscriber.pending = subscriber.pending[1:]
	if event.Type == "log" {
		subscriber.pendingLogs--
	}
	if event.Type == "request" {
		subscriber.pendingRequests--
	}
	if len(subscriber.pending) == 0 {
		subscriber.pending = nil
	}
	return event, true
}

func (subscriber *eventSubscriber) run() {
	defer close(subscriber.events)
	for {
		if event, ok := subscriber.next(); ok {
			select {
			case subscriber.events <- event:
				continue
			case <-subscriber.ctx.Done():
				return
			}
		}
		select {
		case <-subscriber.wake:
		case <-subscriber.ctx.Done():
			return
		}
	}
}

func (registry *requestRegistry) subscribe(ctx context.Context) *eventSubscriber {
	subscriber := newEventSubscriber(ctx)
	registry.mu.Lock()
	registry.subscribers[subscriber] = struct{}{}
	registry.mu.Unlock()
	go func() {
		<-ctx.Done()
		registry.mu.Lock()
		delete(registry.subscribers, subscriber)
		registry.mu.Unlock()
	}()
	return subscriber
}

// activateSubscriber 原子衔接日志请求快照与实时事件
func (registry *requestRegistry) activateSubscriber(
	subscriber *eventSubscriber,
	prefix []api.AdminEvent,
	suffix []api.AdminEvent,
) <-chan api.AdminEvent {
	registry.mu.Lock()
	initial := make([]api.AdminEvent, 0, len(prefix)+len(registry.logs)+len(suffix)+len(registry.active))
	initial = append(initial, prefix...)
	for _, entry := range registry.logs {
		initial = append(initial, api.AdminEvent{Type: "log", Data: entry})
	}
	initial = append(initial, suffix...)
	requests := make([]api.AdminRequest, 0, len(registry.active))
	for _, tracked := range registry.active {
		requests = append(requests, tracked.request)
	}
	sort.Slice(requests, func(left int, right int) bool {
		return requests[left].StartedAt.Before(requests[right].StartedAt)
	})
	for _, request := range requests {
		initial = append(initial, api.AdminEvent{Type: "request", Data: request})
	}
	subscriber.activate(initial)
	registry.mu.Unlock()
	go subscriber.run()
	return subscriber.events
}

func (registry *requestRegistry) publishLocked(event api.AdminEvent) {
	for subscriber := range registry.subscribers {
		subscriber.enqueue(event)
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
		ActiveListenAddr: cfg.ListenAddr, ActiveAPIKey: cfg.ProxyAPIKey,
		Proxy: cfg.Proxy, InitTimeout: cfg.InitTimeout.String(), RequestTimeout: cfg.RequestTimeout.String(),
		WarmWorkerLimit: cfg.WarmWorkerLimit, MaxActiveWorkers: cfg.MaxActiveWorkers,
		WarmStartupConcurrency: cfg.WarmStartupConcurrency,
		PerAccountConcurrency:  cfg.PerAccountConcurrency,
		TemporaryChat:          cfg.TemporaryChat,
	}
}

var _ api.AdminService = (*runtimeAdmin)(nil)
