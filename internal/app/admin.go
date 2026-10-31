package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
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
	lifecycle   context.Context
	pool        *aistudio.AccountPool
	store       *aistudio.AccountStore
	service     *trackedService
	requests    *requestRegistry
	login       aistudio.IsolatedLoginDriver
	workers     *accountWorkerManager
	headers     *accountHeaderProvider
	configPath  string
	config      config.Config
	proxies     *aistudio.ProxyPool
	globalProxy string
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

// invalidConfigError 将配置校验失败标记为客户端输入错误
func invalidConfigError(err error) error {
	return &adminOperationError{status: http.StatusBadRequest, code: "invalid_config", message: err.Error()}
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
	status := api.AdminStatus{
		State:          state,
		Running:        running,
		Ready:          running && counts.Ready+counts.Busy > 0,
		Version:        buildVersion(),
		ActiveRequests: admin.requests.count(),
		Accounts:       counts,
		Camoufox:       api.AdminCamoufox{Ready: camoufoxnative.Installed()},
	}
	if download, ok := camoufoxnative.DownloadProgress(); ok {
		status.Camoufox.Download = &download
	}
	return status, nil
}

func (admin *runtimeAdmin) Accounts(context.Context) ([]api.AdminAccount, error) {
	statuses := admin.pool.Status()
	accounts := make([]api.AdminAccount, 0, len(statuses))
	for _, status := range statuses {
		accounts = append(accounts, adminAccountDTO(status))
	}
	return accounts, nil
}

func (admin *runtimeAdmin) CreateAccount(ctx context.Context, input api.AccountCreateInput, view *camoufoxnative.LiveView) (api.AdminAccount, error) {
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
		Directory: directory, Proxy: admin.effectiveProxy(accountConfig.Proxy),
		Locale: accountConfig.Locale, Timezone: accountConfig.Timezone, View: view,
	})
	if err != nil {
		admin.requests.log("auth", "ERROR", fmt.Sprintf(
			"账户添加失败 | 耗时=%s | 错误=%s",
			time.Since(startedAt).Round(time.Millisecond), strings.TrimSpace(err.Error()),
		))
		return api.AdminAccount{}, err
	}
	if result.DriveError != "" {
		admin.requests.log("auth", "WARN", "账户添加 | Drive 授权失败 | 错误="+result.DriveError)
	}
	if result.CloseError != "" {
		admin.requests.log("auth", "WARN", "账户添加 | 关闭 Camoufox 失败 | 错误="+result.CloseError)
	}
	admin.requests.log("auth", "INFO", "账户添加 | 2/2 | 保存认证状态")
	if _, err := aistudio.NewSigner().Authorization(result.StorageState); err != nil {
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

func (admin *runtimeAdmin) BrowserImportProfiles(ctx context.Context, browser string) ([]api.BrowserImportProfile, error) {
	if browser == "" {
		browser = "chrome"
	}
	root, err := chromeauth.DefaultBrowserRoot(browser)
	if err != nil {
		return nil, err
	}
	accounts, err := chromeauth.DiscoverBrowser(browser, root)
	if err != nil {
		return nil, err
	}
	existing := admin.accountIDs()
	profiles := make([]api.BrowserImportProfile, 0, len(accounts))
	for _, account := range accounts {
		email := strings.ToLower(strings.TrimSpace(account.Email))
		if !account.Importable || email == "" {
			continue
		}
		_, isImported := existing[email]
		profiles = append(profiles, api.BrowserImportProfile{
			ID: account.ID, Browser: browser, Profile: account.Profile, DisplayName: account.DisplayName,
			Email: email, Locale: account.Locale, AuthUser: account.AuthUser, Imported: isImported,
		})
	}
	return profiles, nil
}

func (admin *runtimeAdmin) ChromeImportProfiles(ctx context.Context) ([]api.ChromeImportProfile, error) {
	bp, err := admin.BrowserImportProfiles(ctx, "chrome")
	if err != nil || len(bp) == 0 {
		if braveBp, braveErr := admin.BrowserImportProfiles(ctx, "brave"); braveErr == nil && len(braveBp) > 0 {
			bp = braveBp
		}
	}
	profiles := make([]api.ChromeImportProfile, 0, len(bp))
	for _, p := range bp {
		if p.Imported {
			continue
		}
		profiles = append(profiles, api.ChromeImportProfile{
			ID: p.ID, Profile: p.Profile, DisplayName: p.DisplayName, Email: p.Email, Locale: p.Locale,
		})
	}
	return profiles, nil
}

// CookieBrowsers 列出本机开启远程调试的浏览器，读取其 Cookie 并识别其中各会话登录 AI Studio 的账户，每个账户一项
func (admin *runtimeAdmin) CookieBrowsers(ctx context.Context) ([]api.CookieBrowser, error) {
	addresses, err := chromeauth.DebugAddresses(ctx)
	if err != nil {
		return nil, err
	}
	existing := admin.accountIDs()
	browsers := make([][]api.CookieBrowser, len(addresses))
	var group sync.WaitGroup
	for index, address := range addresses {
		group.Go(func() {
			browser := api.CookieBrowser{Address: address}
			name, err := chromeauth.DebugBrowserName(ctx, address)
			if err != nil {
				browser.Error = err.Error()
				browsers[index] = []api.CookieBrowser{browser}
				return
			}
			browser.Browser = name
			accounts, err := admin.readCookieBrowser(ctx, &browser)
			browser.SignedOut = errors.Is(err, chromeauth.ErrSignedOut) || errors.Is(err, aistudio.ErrLoginCookieMissing)
			if err != nil && !browser.SignedOut {
				browser.Error = err.Error()
			}
			browsers[index] = cookieAccountEntries(browser, accounts, existing)
		})
	}
	group.Wait()
	return append([]api.CookieBrowser{}, slices.Concat(browsers...)...), nil
}

// readCookieBrowser 读取调试浏览器的语言、时区与 Cookie，返回其中各会话登录 AI Studio 的账户
func (admin *runtimeAdmin) readCookieBrowser(ctx context.Context, browser *api.CookieBrowser) ([]chromeauth.CookieAccount, error) {
	source, err := chromeauth.ReadCDPBrowser(ctx, browser.Address)
	if err != nil {
		return nil, err
	}
	browser.Locale, browser.Timezone = source.Locale, source.Timezone
	state, err := aistudio.ParseCookieText(source.Cookies)
	if err != nil {
		return nil, err
	}
	return chromeauth.DiscoverCookieAccounts(ctx, &state, admin.effectiveProxy(""))
}

// DiscoverCookieAccounts 解析粘贴的 Cookie，返回其中各会话登录 AI Studio 的账户
func (admin *runtimeAdmin) DiscoverCookieAccounts(ctx context.Context, input api.CookieImportInput) ([]api.CookieBrowser, error) {
	state, err := aistudio.ParseCookieText(input.Cookies)
	if err != nil {
		return nil, &adminOperationError{status: http.StatusBadRequest, code: "invalid_cookies", message: err.Error()}
	}
	if err := state.Validate(); err != nil {
		return nil, invalidAccount(err)
	}
	accounts, err := chromeauth.DiscoverCookieAccounts(ctx, &state, admin.effectiveProxy(input.Proxy))
	if errors.Is(err, chromeauth.ErrSignedOut) {
		return nil, &adminOperationError{status: http.StatusBadRequest, code: "signed_out", message: err.Error()}
	}
	if err != nil {
		return nil, invalidAccount(err)
	}
	return cookieAccountEntries(api.CookieBrowser{}, accounts, admin.accountIDs()), nil
}

// cookieAccountEntries 以 template 为模板为每个会话账户生成一项并标记已导入的账户，没有账户时只返回 template
func cookieAccountEntries(template api.CookieBrowser, accounts []chromeauth.CookieAccount, existing map[string]struct{}) []api.CookieBrowser {
	if len(accounts) == 0 {
		return []api.CookieBrowser{template}
	}
	entries := make([]api.CookieBrowser, len(accounts))
	for index, account := range accounts {
		entry := template
		entry.Email, entry.AuthUser = account.Email, account.AuthUser
		_, entry.Imported = existing[account.Email]
		entries[index] = entry
	}
	return entries
}

// accountIDs 返回已导入账户的小写 ID
func (admin *runtimeAdmin) accountIDs() map[string]struct{} {
	existing := make(map[string]struct{})
	for _, status := range admin.pool.Status() {
		existing[strings.ToLower(strings.TrimSpace(status.ID))] = struct{}{}
	}
	return existing
}

func (admin *runtimeAdmin) ImportChromeAccounts(ctx context.Context, input api.ChromeImportInput) ([]api.AdminAccount, error) {
	accounts, _, err := admin.ImportBrowserAccounts(ctx, api.BrowserImportInput{
		Browser: "chrome", AccountIDs: input.AccountIDs, Proxy: input.Proxy, Locale: input.Locale, Timezone: input.Timezone,
	})
	if err != nil && len(accounts) == 0 {
		if bAccs, _, bErr := admin.ImportBrowserAccounts(ctx, api.BrowserImportInput{
			Browser: "brave", AccountIDs: input.AccountIDs, Proxy: input.Proxy, Locale: input.Locale, Timezone: input.Timezone,
		}); bErr == nil && len(bAccs) > 0 {
			return bAccs, nil
		}
	}
	return accounts, err
}

func (admin *runtimeAdmin) ImportBrowserAccounts(ctx context.Context, input api.BrowserImportInput) ([]api.AdminAccount, []api.ImportFailure, error) {
	if len(input.AccountIDs) == 0 {
		return nil, nil, invalidAccount(fmt.Errorf("未选择账号"))
	}
	browser := input.Browser
	if browser == "" {
		browser = "chrome"
	}
	root, err := chromeauth.DefaultBrowserRoot(browser)
	if err != nil {
		return nil, nil, err
	}
	accountProxy := strings.TrimSpace(input.Proxy)
	res, err := chromeauth.ImportWithDetails(ctx, chromeauth.ImportOptions{
		Browser:    browser,
		ChromeRoot: root,
		Proxy:      admin.effectiveProxy(accountProxy),
		AccountIDs: input.AccountIDs,
	})
	if err != nil && len(res.Results) == 0 {
		var failures []api.ImportFailure
		for _, f := range res.Failures {
			failures = append(failures, api.ImportFailure{Target: f.Target, Error: f.Error})
		}
		return nil, failures, err
	}

	var failures []api.ImportFailure
	for _, f := range res.Failures {
		failures = append(failures, api.ImportFailure{Target: f.Target, Error: f.Error})
	}

	existingAccounts := make(map[string]struct{})
	for _, status := range admin.pool.Status() {
		existingAccounts[strings.ToLower(strings.TrimSpace(status.ID))] = struct{}{}
	}

	accounts := make([]api.AdminAccount, 0, len(res.Results))
	for _, result := range res.Results {
		email := strings.ToLower(strings.TrimSpace(result.Email))
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
			failures = append(failures, api.ImportFailure{Target: email, Error: err.Error()})
			continue
		}
		if _, err := aistudio.NewSigner().Authorization(result.State); err != nil {
			failures = append(failures, api.ImportFailure{Target: email, Error: fmt.Sprintf("认证状态无法用于 AI Studio: %v", err)})
			continue
		}

		if _, exists := existingAccounts[email]; exists {
			lease, leaseErr := admin.pool.AcquireAccount(ctx, email)
			if leaseErr == nil {
				account := lease.Account()
				_ = admin.workers.Reset(account.ID)
				saveErr := lease.SaveStorageState(result.State)
				_ = admin.service.changeModels(func() error {
					return errors.Join(
						admin.pool.MarkReady(account.ID),
						admin.pool.ResetModelAccess(account.ID),
					)
				})
				_ = lease.Release()
				if saveErr == nil {
					admin.syncAccountModelCatalog(ctx, account)
					if updated, err := admin.account(account.ID); err == nil {
						accounts = append(accounts, updated)
						admin.requests.log("auth", "INFO", browser+" 账户已覆盖更新 | 账户="+updated.Label)
						continue
					}
				}
			}
		}

		account, err := admin.addAccount(ctx, accountConfig, result.State, "")
		if err != nil {
			failures = append(failures, api.ImportFailure{Target: email, Error: err.Error()})
			continue
		}
		accounts = append(accounts, account)
		admin.requests.log("auth", "INFO", browser+" 账户已导入 | 账户="+account.Label)
	}

	if len(accounts) == 0 && len(failures) > 0 {
		return nil, failures, fmt.Errorf("所有所选账号均导入失败: %s", failures[0].Error)
	}
	return accounts, failures, nil
}

// ImportAccountState 导入远程配对上传或 Cookie 导入的认证状态与 Camoufox 指纹，未给标签时以 Cookie 中第 auth_user 个会话登录的账户邮箱为标签
func (admin *runtimeAdmin) ImportAccountState(ctx context.Context, input api.AccountStateInput) (api.AdminAccount, error) {
	label := strings.TrimSpace(input.Label)
	if label == "" {
		verification, err := chromeauth.VerifyAuthUser(ctx, &input.StorageState, admin.effectiveProxy(input.Proxy), input.AuthUser)
		if errors.Is(err, chromeauth.ErrSignedOut) {
			return api.AdminAccount{}, &adminOperationError{status: http.StatusBadRequest, code: "signed_out", message: err.Error()}
		}
		if err != nil {
			return api.AdminAccount{}, invalidAccount(err)
		}
		label = strings.ToLower(verification.Email)
	}
	accountConfig := aistudio.DefaultAccountConfig(label)
	accountConfig.AuthUser = input.AuthUser
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
	if err := input.StorageState.Validate(); err != nil {
		return api.AdminAccount{}, invalidAccount(err)
	}
	if _, err := aistudio.NewSigner().Authorization(input.StorageState); err != nil {
		return api.AdminAccount{}, invalidAccount(fmt.Errorf("认证状态无法用于 AI Studio: %w", err))
	}
	email := accountConfig.Label
	if extension, exists, err := input.StorageState.AuthExtension(); err != nil {
		return api.AdminAccount{}, invalidAccount(err)
	} else if exists && strings.TrimSpace(extension.Source.Email) != "" {
		email = extension.Source.Email
	}
	for _, status := range admin.pool.Status() {
		if strings.EqualFold(status.ID, strings.TrimSpace(email)) {
			return api.AdminAccount{}, &adminOperationError{
				status: http.StatusConflict, code: "account_exists", message: "账户已存在: " + status.ID,
			}
		}
	}
	fingerprintDirectory := ""
	if len(input.Fingerprint) != 0 {
		directory, err := os.MkdirTemp("", "aistudio2api-pairing-*")
		if err != nil {
			return api.AdminAccount{}, fmt.Errorf("创建配对指纹目录: %w", err)
		}
		defer os.RemoveAll(directory)
		if err := os.WriteFile(filepath.Join(directory, "camoufox-fingerprint.json"), input.Fingerprint, 0o600); err != nil {
			return api.AdminAccount{}, fmt.Errorf("写入配对指纹: %w", err)
		}
		fingerprintDirectory = directory
	}
	account, err := admin.addAccount(ctx, accountConfig, input.StorageState, fingerprintDirectory)
	if err != nil {
		return api.AdminAccount{}, err
	}
	admin.requests.log("auth", "INFO", "认证状态账户已导入 | 账户="+account.Label)
	return account, nil
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
	if strings.TrimSpace(account.Config.Proxy) == "" {
		account.SetPoolProxy(admin.proxies.Next())
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
	if _, err := admin.proxies.Assign(); err != nil {
		return api.AdminAccount{}, err
	}
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
	accountConfig.AuthUser = account.Config.AuthUser
	enabling := accountConfig.Enabled && !account.Config.Enabled
	if accountConfig.Proxy == "" && !admin.proxies.Available(account.PoolProxy("")) {
		account.SetPoolProxy(admin.proxies.Next())
	}
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
	if _, err := admin.proxies.Assign(); err != nil {
		return api.AdminAccount{}, err
	}
	admin.syncModelCache()
	if enabling {
		admin.syncAccountModelCatalog(ctx, account)
	}
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

func (admin *runtimeAdmin) LoginAccount(ctx context.Context, accountID string, view *camoufoxnative.LiveView) (api.AdminAccount, error) {
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
	request := admin.loginRequest(account, directory)
	request.View = view
	result, err := admin.login.Login(ctx, request)
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
	if result.DriveError != "" {
		admin.requests.log(account.Config.Label, "WARN", "账户登录 | Drive 授权失败 | 错误="+result.DriveError)
	}
	if result.CloseError != "" {
		admin.requests.log(account.Config.Label, "WARN", "账户登录 | 关闭 Camoufox 失败 | 错误="+result.CloseError)
	}
	admin.requests.log(account.Config.Label, "INFO", "账户登录 | 2/2 | 保存认证状态")
	if _, err := aistudio.NewSigner().Authorization(result.StorageState); err != nil {
		return api.AdminAccount{}, errors.Join(fmt.Errorf("认证状态无法用于 AI Studio: %w", err), lease.Release())
	}
	if err := camoufoxnative.PersistAccountFingerprint(directory, account.Directory); err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	state := result.StorageState
	if err := keepAuthExtension(&state, account.StorageState); err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	if err := lease.SaveStorageState(state); err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	if err := resetCookieAuthUser(lease, admin.workers.prepareUpdate); err != nil {
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

// resetCookieAuthUser 在账户认证状态换成单账号 Cookie 后把会话序号归零并同步到 Worker
func resetCookieAuthUser(lease *aistudio.AccountLease, prepare func(*aistudio.Account, aistudio.AccountConfig) (*accountWorkerUpdate, error)) error {
	account := lease.Account()
	if account.Config.AuthUser == 0 {
		return nil
	}
	accountConfig := account.Config
	accountConfig.AuthUser = 0
	workerUpdate, err := prepare(account, accountConfig)
	if err != nil {
		return err
	}
	if err := lease.SaveConfig(accountConfig); err != nil {
		workerUpdate.Discard()
		return err
	}
	workerUpdate.Commit()
	return nil
}

// keepAuthExtension 在新登录状态缺少续签材料时沿用账户已有的 Chrome 来源与 OAuth 材料
func keepAuthExtension(next *aistudio.StorageState, current aistudio.StorageState) error {
	if _, exists, err := next.AuthExtension(); err != nil || exists {
		return err
	}
	extension, exists, err := current.AuthExtension()
	if err != nil || !exists {
		return err
	}
	next.SetAuthExtension(extension)
	return nil
}

// RefreshAccount 在服务生命周期中立即读取账户权益与模型目录，管理页断开不中止
func (admin *runtimeAdmin) RefreshAccount(_ context.Context, accountID string) (api.AdminAccountRefresh, error) {
	refresh, err := admin.service.refreshAccountState(admin.service.lifecycle, accountID, true)
	if err != nil {
		return api.AdminAccountRefresh{}, accountOperationError(err)
	}
	account, err := admin.account(accountID)
	if err != nil {
		return api.AdminAccountRefresh{}, err
	}
	return api.AdminAccountRefresh{
		Account: account, PreviousTier: refresh.PreviousTier.String(), Tier: refresh.Tier.String(),
		PreviousModels: refresh.PreviousModels, Models: refresh.Models,
	}, nil
}

// StartService 使用管理器提供的启动生命周期启动生成服务
func (admin *runtimeAdmin) StartService(ctx context.Context) (api.AdminStatus, error) {
	startedAt := time.Now()
	models, started, err := admin.service.Start(ctx, func() {
		status, _ := admin.Status(ctx)
		admin.publishRuntimeSnapshot(ctx, status)
	})
	if errors.Is(err, errServiceTransitioning) {
		return admin.Status(ctx)
	}
	if err != nil {
		status, _ := admin.Status(ctx)
		admin.publishRuntimeSnapshot(ctx, status)
		if errors.Is(err, context.Canceled) && admin.service.State() == "STOPPED" {
			admin.requests.log("service", "INFO", fmt.Sprintf(
				"生成服务启动已取消 | 耗时=%s",
				time.Since(startedAt).Round(time.Millisecond),
			))
			return status, nil
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
	status, _ := admin.Status(ctx)
	admin.publishRuntimeSnapshot(ctx, status)
	return status, nil
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
	status, _ := admin.Status(ctx)
	admin.publishRuntimeSnapshot(ctx, status)
	return status, nil
}

// syncModelCache 在账户写入后刷新权威快照
func (admin *runtimeAdmin) syncModelCache() {
	ctx := admin.lifecycle
	_ = admin.service.SyncModels(ctx)
	status, _ := admin.Status(ctx)
	admin.publishRuntimeSnapshot(ctx, status)
}

func (admin *runtimeAdmin) syncAccountModelCatalog(ctx context.Context, account *aistudio.Account) {
	models, err := admin.service.syncAccountModels(ctx, account.ID)
	if err != nil {
		admin.service.publishModelAccess()
		return
	}
	if len(models) > 0 {
		admin.requests.log(account.Config.Label, "INFO", fmt.Sprintf("账户模型目录同步完成 | 模型=%d", len(models)))
	}
	admin.service.publishModelAccess()
}

// publishRuntimeSnapshot 推送管理页权威运行状态
func (admin *runtimeAdmin) publishRuntimeSnapshot(ctx context.Context, status api.AdminStatus) {
	models, _ := admin.CatalogModels(ctx)
	accounts, _ := admin.Accounts(ctx)
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

func (admin *runtimeAdmin) proxyPoolStatus() []api.AdminProxy {
	statuses := admin.proxies.Status()
	pool := make([]api.AdminProxy, 0, len(statuses))
	for _, status := range statuses {
		pool = append(pool, api.AdminProxy{
			Proxy: status.Proxy, Available: status.Available, Accounts: status.Accounts, CheckedAt: status.CheckedAt, Error: status.Error,
		})
	}
	return pool
}

func (admin *runtimeAdmin) effectiveProxy(accountProxy string) string {
	if proxy := strings.TrimSpace(accountProxy); proxy != "" {
		return proxy
	}
	if proxy := admin.proxies.Next(); proxy != "" {
		return proxy
	}
	return admin.globalProxy
}

func (admin *runtimeAdmin) loginRequest(account *aistudio.Account, directory string) aistudio.IsolatedLoginRequest {
	return aistudio.IsolatedLoginRequest{
		Directory: directory, Proxy: account.EffectiveProxy(admin.globalProxy),
		Locale: account.Config.Locale, Timezone: account.Config.Timezone,
	}
}

func adminAccountDTO(status aistudio.AccountStatus) api.AdminAccount {
	return api.AdminAccount{
		ID: status.ID, Label: status.Label, Enabled: status.Enabled, State: string(status.State),
		Proxy: status.Proxy, Egress: status.Egress, Locale: status.Locale, Timezone: status.Timezone,
		Models: status.Models, BenefitTier: status.BenefitTier, Message: status.Message,
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
	value := runtimeConfigDTO(cfg)
	value.ActiveProxy = admin.config.Proxy
	value.ProxyPool = admin.proxyPoolStatus()
	return value, nil
}

func (admin *runtimeAdmin) UpdateRuntimeConfig(_ context.Context, value api.RuntimeConfig) (api.RuntimeConfig, error) {
	saved, err := config.Load(admin.configPath)
	if err != nil {
		return api.RuntimeConfig{}, err
	}
	password := saved.AdminPassword
	if value.AdminPassword != nil {
		password = *value.AdminPassword
	}
	initTimeout, err := time.ParseDuration(value.InitTimeout)
	if err != nil {
		return api.RuntimeConfig{}, invalidConfigError(fmt.Errorf("INIT_TIMEOUT 无效: %w", err))
	}
	requestTimeout, err := time.ParseDuration(value.RequestTimeout)
	if err != nil {
		return api.RuntimeConfig{}, invalidConfigError(fmt.Errorf("REQUEST_TIMEOUT 无效: %w", err))
	}
	cfg := config.Config{
		AutoStart:      value.AutoStart,
		RequestBodyLog: value.RequestBodyLog,
		LogFile:        value.LogFile, LogMaxTotalMB: value.LogMaxTotalMB,
		AdminAuthEnabled: value.AdminAuthEnabled, AdminUsername: strings.TrimSpace(value.AdminUsername), AdminPassword: password,
		BuildNativeNonstream: value.BuildNativeNonstream,
		NonstreamKeepalive:   value.NonstreamKeepalive, FakeStream: value.FakeStream, YouTubeLinks: value.YouTubeLinks,
		SearchModelAliases: value.SearchModelAliases, GoogleSearch: value.GoogleSearch,
		ContinuationRewrite: value.ContinuationRewrite, ContinuationPrompt: strings.TrimSpace(value.ContinuationPrompt),
		FirstEventHedge: strings.ToLower(strings.TrimSpace(value.FirstEventHedge)), AuthStates: value.AuthStates, ListenAddr: value.ListenAddr, PreviewListenAddr: value.PreviewListenAddr,
		ProxyAPIKey: value.APIKey, Proxy: value.Proxy, InitTimeout: initTimeout, RequestTimeout: requestTimeout,
		WarmWorkerLimit: value.WarmWorkerLimit, MaxActiveWorkers: value.MaxActiveWorkers,
		WarmStartupConcurrency: value.WarmStartupConcurrency,
		PerAccountConcurrency:  value.PerAccountConcurrency,
		RoutingStrategy:        value.RoutingStrategy,
		UpstreamChannels:       value.UpstreamChannels,
		ModelMapping:           strings.TrimSpace(value.ModelMapping),
		ModelChannels:          strings.TrimSpace(value.ModelChannels),
		TemporaryChat:          value.TemporaryChat,
		WAABackend:             value.WAABackend,
		MediaOutput:            strings.ToLower(strings.TrimSpace(value.MediaOutput)),
		MediaBaseURL:           strings.TrimSpace(value.MediaBaseURL),
	}
	if err := cfg.Validate(); err != nil {
		return api.RuntimeConfig{}, invalidConfigError(err)
	}
	if err := cfg.Save(admin.configPath); err != nil {
		return api.RuntimeConfig{}, err
	}
	admin.pool.SetRoutingStrategy(cfg.RoutingStrategy)
	admin.pool.SetModelChannels(modelChannels(cfg.ModelChannels))
	admin.requests.log("service", "INFO", "服务配置已保存")
	models, _ := admin.CatalogModels(context.Background())
	admin.requests.publish(api.AdminEvent{Type: "models", Data: map[string]any{"models": models}})
	value = runtimeConfigDTO(cfg)
	value.ActiveProxy = admin.config.Proxy
	value.ProxyPool = admin.proxyPoolStatus()
	return value, nil
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
		scopes := make(map[string]string)
		if global, ok := account.Cooldowns["*"]; ok && global.Active(now) {
			for modelID := range models {
				effective[modelID] = global
				scopes[modelID] = "*"
			}
		}
		for key, cooldown := range account.Cooldowns {
			if key == "*" || !cooldown.Active(now) {
				continue
			}
			modelID, build := strings.CutPrefix(key, string(aistudio.ChannelBuild)+":")
			if _, ok := models[modelID]; !ok && !build {
				continue
			}
			if current, ok := effective[key]; !ok || cooldown.Until.After(current.Until) {
				effective[key] = cooldown
				scopes[key] = key
			}
		}
		modelIDs := make([]string, 0, len(effective))
		for modelID := range effective {
			modelIDs = append(modelIDs, modelID)
		}
		sort.Strings(modelIDs)
		for _, key := range modelIDs {
			cooldown := effective[key]
			channel := string(aistudio.ChannelPlayground)
			modelID, build := strings.CutPrefix(key, string(aistudio.ChannelBuild)+":")
			if build {
				channel = string(aistudio.ChannelBuild)
			} else if admin.pool.AssistantModel(modelID) {
				channel = string(aistudio.ChannelAssistant)
			}
			cooldowns = append(cooldowns, api.AdminCooldown{
				AccountID: account.ID, AccountLabel: account.Label, Channel: channel,
				ModelID: modelID, Scope: scopes[key], Until: cooldown.Until, Reason: cooldown.Reason,
			})
		}
	}
	return cooldowns, nil
}

// ClearCooldown 手动清除账户在一个作用域上的冷却，并推送新的冷却列表与账户状态
func (admin *runtimeAdmin) ClearCooldown(ctx context.Context, accountID string, scope string) error {
	account, err := admin.account(accountID)
	if err != nil {
		return err
	}
	if err := admin.pool.ClearCooldown(accountID, scope); err != nil {
		return err
	}
	admin.requests.log(account.Label, "INFO", "冷却已手动清除 | 范围="+scope)
	cooldowns, _ := admin.Cooldowns(ctx)
	admin.requests.publish(api.AdminEvent{Type: "cooldowns", Data: cooldowns})
	accounts, _ := admin.Accounts(ctx)
	admin.requests.publish(api.AdminEvent{Type: "accounts", Data: map[string]any{"accounts": accounts}})
	return nil
}

func (admin *runtimeAdmin) Requests(context.Context) ([]api.AdminRequest, error) {
	return admin.requests.list(), nil
}

func (admin *runtimeAdmin) CancelRequest(_ context.Context, id string) error {
	return admin.requests.cancel(id)
}

// adminEventSource 提供管理事件流的当前快照
type adminEventSource interface {
	CatalogModels(context.Context) ([]aistudio.Model, error)
	Status(context.Context) (api.AdminStatus, error)
	Accounts(context.Context) ([]api.AdminAccount, error)
	Cooldowns(context.Context) ([]api.AdminCooldown, error)
}

// CatalogModels 返回当前运行时完整目录与各模型可用通道
func (admin *runtimeAdmin) CatalogModels(ctx context.Context) ([]aistudio.Model, error) {
	return admin.service.catalogModels(), nil
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
	models, err := source.CatalogModels(eventCtx)
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

// version 是编译时写入的版本号
var version string

// managedBy 是编译时写入的更新管理方，非空时在线更新由该分发方负责
var managedBy string

// buildVersion 返回链接时写入的版本号，没有时读取 Go 构建信息
func buildVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "(devel)"
	}
	return info.Main.Version
}

func runtimeConfigDTO(cfg config.Config) api.RuntimeConfig {
	return api.RuntimeConfig{
		AutoStart:      cfg.AutoStart,
		RequestBodyLog: cfg.RequestBodyLog,
		LogFile:        cfg.LogFile, LogMaxTotalMB: cfg.LogMaxTotalMB,
		AdminAuthEnabled: cfg.AdminAuthEnabled, AdminUsername: cfg.AdminUsername,
		AdminPasswordSet: cfg.AdminPassword != "", SavedAdminPassword: cfg.AdminPassword,
		BuildNativeNonstream: cfg.BuildNativeNonstream,
		NonstreamKeepalive:   cfg.NonstreamKeepalive, FakeStream: cfg.FakeStream, YouTubeLinks: cfg.YouTubeLinks,
		SearchModelAliases: cfg.SearchModelAliases, GoogleSearch: cfg.GoogleSearch,
		ContinuationRewrite: cfg.ContinuationRewrite, ContinuationPrompt: cfg.ContinuationPrompt,
		FirstEventHedge: cfg.FirstEventHedge, AuthStates: cfg.AuthStates, ListenAddr: cfg.ListenAddr, APIKey: cfg.ProxyAPIKey,
		ActiveListenAddr: cfg.ListenAddr, ActiveAPIKey: cfg.ProxyAPIKey,
		PreviewListenAddr: cfg.PreviewListenAddr, ActivePreviewListenAddr: cfg.PreviewListenAddr,
		Proxy: cfg.Proxy, InitTimeout: cfg.InitTimeout.String(), RequestTimeout: cfg.RequestTimeout.String(),
		WarmWorkerLimit: cfg.WarmWorkerLimit, MaxActiveWorkers: cfg.MaxActiveWorkers,
		WarmStartupConcurrency: cfg.WarmStartupConcurrency,
		PerAccountConcurrency:  cfg.PerAccountConcurrency,
		RoutingStrategy:        cfg.RoutingStrategy,
		UpstreamChannels:       cfg.UpstreamChannels,
		ModelMapping:           cfg.ModelMapping,
		ModelChannels:          cfg.ModelChannels,
		TemporaryChat:          cfg.TemporaryChat,
		WAABackend:             cfg.WAABackend,
		MediaOutput:            cfg.MediaOutput,
		MediaBaseURL:           cfg.MediaBaseURL,
	}
}

var _ generationAdmin = (*runtimeAdmin)(nil)
