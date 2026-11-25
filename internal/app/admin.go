package app

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
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
	"github.com/Mag1cFall/AIStudio2API/internal/egress"
)

// runtimeAdmin 投影运行时权威状态
type runtimeAdmin struct {
	// publishMu 串行化状态快照的生成与推送
	publishMu   sync.Mutex
	lifecycle   context.Context
	pool        *aistudio.AccountPool
	store       *aistudio.AccountStore
	service     *trackedService
	requests    *requestRegistry
	login       aistudio.IsolatedLoginDriver
	workers     *accountWorkerManager
	configPath  string
	config      config.Config
	proxies     *aistudio.ProxyPool
	proxyMu     sync.RWMutex
	globalProxy string
	activeProxy string
	probeMu     sync.Mutex
	probeCancel context.CancelFunc

	transitionMu sync.Mutex
	// transitions 是各账户进行中的后台收尾：启停收尾与添加、导入、登录后的目录同步
	transitions map[string]*accountTransition
	// accountCommit 返回提交账户新增、导入与登录结果所用的管理接口与结束提交的函数，结束前生成服务不换代
	accountCommit func() (*runtimeAdmin, func())
}

// accountTransition 是一次进行中的账户后台收尾
type accountTransition struct {
	cancel context.CancelFunc
	done   chan struct{}
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
	cfg config.Config,
) *runtimeAdmin {
	admin := &runtimeAdmin{
		lifecycle: lifecycle, pool: pool, store: store, service: service, requests: registry, login: login,
		workers:    workers,
		configPath: ".env", config: cfg,
	}
	admin.accountCommit = func() (*runtimeAdmin, func()) { return admin, func() {} }
	return admin
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
		Time:           time.Now(),
		ActiveRequests: admin.requests.count(),
		Accounts:       counts,
		Camoufox:       camoufoxnative.PreparationState(),
		ChromeImport:   chromeauth.Supported,
	}
	if step, since := admin.service.Step(); step != "" {
		status.Transition, status.TransitionSince = step, &since
	}
	return status, nil
}

// Accounts 返回各账户的状态，无法载入的账户目录列为不可用，原因写明出错的文件
func (admin *runtimeAdmin) Accounts(context.Context) ([]api.AdminAccount, error) {
	statuses := admin.pool.Status()
	accounts := make([]api.AdminAccount, 0, len(statuses))
	for _, status := range statuses {
		accounts = append(accounts, adminAccountDTO(status))
	}
	for _, failure := range admin.store.Failures() {
		accounts = append(accounts, api.AdminAccount{ID: failure.ID, Label: failure.ID, State: string(aistudio.AccountUnavailable), Models: []string{}, Message: failure.Err.Error()})
	}
	return accounts, nil
}

// AccountExits 并发查询各账户出口代理的公网 IP 与地理位置，每个代理一行，查询失败的代理带原因
func (admin *runtimeAdmin) AccountExits(ctx context.Context) ([]api.AccountExit, error) {
	var proxies []string
	for _, status := range admin.pool.Status() {
		if !slices.Contains(proxies, status.Egress) {
			proxies = append(proxies, status.Egress)
		}
	}
	exits := make([]api.AccountExit, len(proxies))
	var wait sync.WaitGroup
	for index, proxy := range proxies {
		wait.Go(func() {
			exits[index] = api.AccountExit{Proxy: proxy}
			exit, err := egress.Lookup(ctx, proxy)
			if err != nil {
				exits[index].Error = err.Error()
				return
			}
			exits[index].IP, exits[index].Country, exits[index].Timezone, exits[index].Locale = exit.IP, exit.Country, exit.Timezone, exit.Locale
		})
	}
	wait.Wait()
	return exits, nil
}

// accountEnvironment 在后台确定新账户的语言与时区并返回等待结果的函数，该函数只调用一次：locale 或 timezone 未给出时按 proxy 出口的地理位置填写，查询出口失败时取本机的语言与时区；取结果时写入日志，查询失败为 WARN
func (admin *runtimeAdmin) accountEnvironment(ctx context.Context, proxy string, locale string, timezone string) func() egress.Exit {
	type lookup struct {
		exit egress.Exit
		err  error
	}
	result := make(chan lookup, 1)
	go func() {
		exit, err := egress.Environment(ctx, proxy, strings.TrimSpace(locale), timezone)
		result <- lookup{exit, err}
	}()
	return func() egress.Exit {
		found := <-result
		exit := found.exit
		if found.err != nil {
			admin.requests.log("auth", "WARN", "查询出口位置失败，账户语言与时区使用本机设置", "locale", exit.Locale, "timezone", exit.Timezone, "error", found.err)
		} else if exit.IP != "" {
			admin.requests.log("auth", "INFO", "账户语言与时区按出口位置填写", "egress", exit.IP, "country", exit.Country, "locale", exit.Locale, "timezone", exit.Timezone)
		}
		return exit
	}
}

func (admin *runtimeAdmin) CreateAccount(ctx context.Context, input api.AccountCreateInput, view *camoufoxnative.LiveView) (_ api.AdminAccount, err error) {
	accountConfig := aistudio.DefaultAccountConfig("")
	accountConfig.Proxy = strings.TrimSpace(input.Proxy)
	timezone, err := accountTimezone(input.Timezone)
	if err != nil {
		return api.AdminAccount{}, invalidAccount(err)
	}
	if err := config.ValidateProxy(accountConfig.Proxy); err != nil {
		return api.AdminAccount{}, invalidAccount(err)
	}
	environment := admin.accountEnvironment(ctx, admin.effectiveProxy(accountConfig.Proxy), input.Locale, timezone)()
	accountConfig.Locale, accountConfig.Timezone = environment.Locale, environment.Timezone
	directory, err := os.MkdirTemp("", "aistudio2api-account-login-*")
	if err != nil {
		return api.AdminAccount{}, fmt.Errorf("创建隔离登录目录: %w", err)
	}
	defer os.RemoveAll(directory)
	startedAt := time.Now()
	defer func() {
		if err != nil {
			admin.requests.logStage("auth", "ERROR", api.LogStage{Name: "账户添加", State: "failed"}, "账户添加失败", "duration", time.Since(startedAt), "error", err)
		}
	}()
	admin.requests.logStage("auth", "INFO", api.LogStage{Name: "账户添加", Step: 1, Steps: 3}, "等待隔离登录")
	api.ReportTaskStep(ctx, api.TaskStepLogin)
	var savedID string
	var savedLabel string
	saveAccount := func(saveCtx context.Context, logged aistudio.IsolatedLoginResult) (*runtimeAdmin, *aistudio.Account, error) {
		if logged.DriveError != "" {
			admin.requests.logStage("auth", "WARN", api.LogStage{Name: "账户添加"}, "Drive 授权失败", "error", logged.DriveError)
		}
		admin.requests.logStage("auth", "INFO", api.LogStage{Name: "账户添加", Step: 2, Steps: 3}, "保存认证状态")
		api.ReportTaskStep(ctx, api.TaskStepSaving)
		if _, authErr := aistudio.NewSigner().Authorization(logged.StorageState); authErr != nil {
			return nil, nil, fmt.Errorf("认证状态无法用于 AI Studio: %w", authErr)
		}
		accountConfig.Label = logged.Email
		if valErr := accountConfig.Validate(); valErr != nil {
			return nil, nil, invalidAccount(valErr)
		}
		target, done := admin.accountCommit()
		account, upsertErr := target.upsertAccount(saveCtx, accountConfig.Label, accountConfig, logged.StorageState, directory)
		done()
		if upsertErr != nil {
			return nil, nil, upsertErr
		}
		return target, account, nil
	}
	result, err := admin.login.Login(ctx, aistudio.IsolatedLoginRequest{
		Directory: directory,
		Proxy:     admin.effectiveProxy(accountConfig.Proxy),
		Locale:    accountConfig.Locale,
		Timezone:  accountConfig.Timezone,
		View:      view,
		OnLoggedIn: func(saveCtx context.Context, logged aistudio.IsolatedLoginResult) error {
			target, account, saveErr := saveAccount(saveCtx, logged)
			if saveErr != nil {
				return saveErr
			}
			savedID, savedLabel = account.ID, account.Config.Label
			api.ReportTaskLabel(ctx, savedLabel)
			target.publishRuntimeSnapshot(target.lifecycle)
			return nil
		},
		CheckVerification: func(checkCtx context.Context, updated aistudio.StorageState) (bool, error) {
			if savedID == "" {
				return false, nil
			}
			admin.requests.logStage(savedLabel, "INFO", api.LogStage{Name: "账户添加", Step: 3, Steps: 3}, "核对账户验证状态")
			target, done := admin.accountCommit()
			defer done()
			return target.verifyAccountNow(checkCtx, savedID, updated)
		},
		OnVerifyStep: func(step string) { reportAccountVerifyStep(ctx, step) },
	})
	if err != nil {
		return api.AdminAccount{}, err
	}
	if result.CloseError != "" {
		admin.requests.logStage(cmp.Or(savedLabel, "auth"), "WARN", api.LogStage{Name: "账户添加"}, "关闭 Camoufox 失败", "error", result.CloseError)
	}
	var created api.AdminAccount
	if result.Saved {
		if result.VerificationError != "" {
			admin.requests.logStage(savedLabel, "WARN", api.LogStage{Name: "账户添加"}, "账户验证核对失败", "error", result.VerificationError)
		}
		target, done := admin.accountCommit()
		created, err = target.account(savedID)
		done()
	} else {
		var target *runtimeAdmin
		var account *aistudio.Account
		target, account, err = saveAccount(ctx, result)
		if err == nil {
			created, err = target.verifiedAccount(account)
		}
	}
	if err != nil {
		return api.AdminAccount{}, err
	}
	admin.requests.logStage(cmp.Or(savedLabel, "auth"), "INFO", api.LogStage{Name: "账户添加", State: "completed"}, "账户添加完成", "account", created.Label, "duration", time.Since(startedAt))
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
	recorded, err := admin.store.RestrictedImports()
	if err != nil {
		return nil, err
	}
	profiles := chromeImportProfiles(accounts, admin.accountIDs(), recorded)
	return enrichChromeImportProfiles(profiles, admin.pool.Status()), nil
}

// enrichChromeImportProfiles 为已存在于账户池的 Chrome 候选补上已导入标记与已知权益
func enrichChromeImportProfiles(profiles []api.ChromeImportProfile, statuses []aistudio.AccountStatus) []api.ChromeImportProfile {
	existing := make(map[string]aistudio.AccountStatus, len(statuses))
	for _, status := range statuses {
		existing[strings.ToLower(strings.TrimSpace(status.ID))] = status
	}
	for index := range profiles {
		status, ok := existing[strings.ToLower(strings.TrimSpace(profiles[index].Email))]
		if !ok {
			continue
		}
		profiles[index].Imported = true
		if status.BenefitTier != "" && status.BenefitTier != "Unknown" {
			profiles[index].BenefitTier = status.BenefitTier
		}
	}
	return profiles
}

// CheckChromeImportTiers 按需并发查询选中本机 Chrome 账号的 AI Studio 权益，每完成一个即通过 emit 回调输出
func (admin *runtimeAdmin) CheckChromeImportTiers(ctx context.Context, input api.ChromeTierCheckInput, emit func(api.ChromeProfileTier)) error {
	root, err := chromeauth.DefaultChromeRoot()
	if err != nil {
		return err
	}
	proxyURL := admin.effectiveProxy(input.Proxy)
	return chromeauth.CheckProfileTiers(ctx, chromeauth.ImportOptions{
		ChromeRoot: root, Proxy: proxyURL, AccountIDs: input.AccountIDs,
	}, func(result chromeauth.ProfileTierResult) {
		if emit != nil {
			emit(api.ChromeProfileTier{
				ID:          result.ID,
				BenefitTier: result.BenefitTier,
				Restricted:  result.Restricted,
				Error:       result.Error,
			})
		}
	})
}

// chromeImportProfiles 返回可导入的 Chrome 账户，已导入且未受限的账户不列出，服务受限的已导入账户与导入时被拒绝的账户带受限原因
func chromeImportProfiles(accounts []chromeauth.Account, existing map[string]string, recorded map[string]string) []api.ChromeImportProfile {
	seen := make(map[string]struct{}, len(accounts))
	profiles := make([]api.ChromeImportProfile, 0, len(accounts))
	for _, account := range accounts {
		email := strings.ToLower(strings.TrimSpace(account.Email))
		if !account.Importable || email == "" {
			continue
		}
		restricted, exists := existing[email]
		if _, duplicate := seen[email]; duplicate || exists && restricted == "" {
			continue
		}
		seen[email] = struct{}{}
		profiles = append(profiles, api.ChromeImportProfile{
			ID: account.ID, Profile: account.Profile, DisplayName: account.DisplayName, Email: email,
			Restricted: cmp.Or(restricted, recorded[email]),
		})
	}
	return profiles
}

// restrictedChromeSelection 在 Chrome 认证交换之前拒绝所选账户中服务受限的已导入账户与导入时被记下服务受限的账户
func restrictedChromeSelection(accounts []chromeauth.Account, ids []string, existing map[string]string, recorded map[string]string) error {
	for _, account := range accounts {
		if !slices.ContainsFunc(ids, func(id string) bool { return strings.EqualFold(strings.TrimSpace(id), account.ID) }) {
			continue
		}
		email := strings.ToLower(strings.TrimSpace(account.Email))
		if reason := cmp.Or(existing[email], recorded[email]); reason != "" {
			return accountRestricted(email, reason)
		}
	}
	return nil
}

// browserDebugReadTimeout 是扫描单个本机远程调试浏览器读取 Cookie 的最长时间
const browserDebugReadTimeout = 15 * time.Second

// CookieBrowsers 列出本机开启远程调试的浏览器，读取其 Cookie 并经 proxy 识别其中各会话登录 AI Studio 的账户，每个账户一项，proxy 为空时用默认出口
func (admin *runtimeAdmin) CookieBrowsers(ctx context.Context, proxy string) ([]api.CookieBrowser, error) {
	addresses, err := chromeauth.DebugAddresses(ctx)
	if err != nil {
		admin.requests.log("auth", "ERROR", "查找本机调试浏览器失败", "error", err)
		return nil, err
	}
	statuses := admin.pool.Status()
	browsers := make([][]api.CookieBrowser, len(addresses))
	var group sync.WaitGroup
	for index, address := range addresses {
		group.Go(func() {
			browser := api.CookieBrowser{Address: address}
			readCtx, cancel := context.WithTimeout(ctx, browserDebugReadTimeout)
			source, err := chromeauth.ReadBrowserCookies(readCtx, address)
			cancel()
			browser.Browser = source.Product
			if err != nil {
				browser.Error = err.Error()
				browsers[index] = []api.CookieBrowser{browser}
				return
			}
			accounts, err := admin.readCookieBrowser(ctx, &browser, proxy, source.Cookies)
			browser.SignedOut = errors.Is(err, chromeauth.ErrSignedOut) || errors.Is(err, aistudio.ErrLoginCookieMissing)
			if err != nil && !browser.SignedOut {
				browser.Error = err.Error()
			}
			browsers[index] = cookieAccountEntries(browser, accounts, statuses)
		})
	}
	group.Wait()
	return append([]api.CookieBrowser{}, slices.Concat(browsers...)...), nil
}

// readCookieBrowser 解析调试浏览器的 Cookie 文本，经 proxy 识别其中各会话登录 AI Studio 的账户
func (admin *runtimeAdmin) readCookieBrowser(ctx context.Context, browser *api.CookieBrowser, proxy, cookies string) ([]chromeauth.CookieAccount, error) {
	state, err := aistudio.ParseCookieText(cookies)
	if err != nil {
		return nil, err
	}
	browser.DeviceBound = deviceBoundSession(state)
	return chromeauth.DiscoverCookieAccounts(ctx, &state, admin.effectiveProxy(proxy))
}

// DiscoverCookieAccounts 解析粘贴的 Cookie，返回其中各会话登录 AI Studio 的账户
func (admin *runtimeAdmin) DiscoverCookieAccounts(ctx context.Context, input api.CookieImportInput) ([]api.CookieBrowser, error) {
	state, err := aistudio.ParseCookieText(input.Cookies)
	if err != nil {
		return nil, &adminOperationError{status: http.StatusBadRequest, code: "invalid_cookies", message: err.Error()}
	}
	accounts, err := chromeauth.DiscoverCookieAccounts(ctx, &state, admin.effectiveProxy(input.Proxy))
	if errors.Is(err, chromeauth.ErrSignedOut) {
		return nil, &adminOperationError{status: http.StatusBadRequest, code: "signed_out", message: err.Error()}
	}
	if err != nil {
		admin.requests.log("auth", "ERROR", "识别 Cookie 中的账户失败", "error", err)
		return nil, invalidAccount(err)
	}
	return cookieAccountEntries(api.CookieBrowser{DeviceBound: deviceBoundSession(state)}, accounts, admin.pool.Status()), nil
}

// deviceBoundSession 表示 Cookie 带 __Secure-1PSIDRTS，会话绑定签发它的设备
func deviceBoundSession(state aistudio.StorageState) bool {
	return state.HasBoundCookie()
}

// cookieAccountEntries 以 template 为模板为每个会话账户生成一项，按账户池状态 statuses 标记已导入、需要重新登录与服务受限的账户，没有账户时只返回 template
func cookieAccountEntries(template api.CookieBrowser, accounts []chromeauth.CookieAccount, statuses []aistudio.AccountStatus) []api.CookieBrowser {
	if len(accounts) == 0 {
		return []api.CookieBrowser{template}
	}
	existing := make(map[string]aistudio.AccountStatus, len(statuses))
	for _, status := range statuses {
		existing[strings.ToLower(strings.TrimSpace(status.ID))] = status
	}
	entries := make([]api.CookieBrowser, len(accounts))
	for index, account := range accounts {
		entry := template
		entry.Email, entry.AuthUser, entry.Restricted, entry.BenefitTier = account.Email, account.AuthUser, account.Restricted, account.BenefitTier
		if status, imported := existing[account.Email]; imported {
			entry.Imported, entry.SignInRequired = true, status.State == aistudio.AccountAuthRequired
			if entry.BenefitTier == "" && status.BenefitTier != "" && status.BenefitTier != "Unknown" {
				entry.BenefitTier = status.BenefitTier
			}
			if status.State == aistudio.AccountRestricted && status.Message != "" {
				entry.Restricted = status.Message
			}
		}
		entries[index] = entry
	}
	return entries
}

// accountIDs 返回已导入且不需要重新登录的账户小写 ID 与其服务受限的原因，没有受限时原因为空
func (admin *runtimeAdmin) accountIDs() map[string]string {
	existing := make(map[string]string)
	for _, status := range admin.pool.Status() {
		if status.State == aistudio.AccountAuthRequired {
			continue
		}
		restricted := ""
		if status.State == aistudio.AccountRestricted {
			restricted = status.Message
		}
		existing[strings.ToLower(strings.TrimSpace(status.ID))] = restricted
	}
	return existing
}

// checkAccountAccess 在导入前确认账户不在服务受限状态，再以认证状态打开 AI Studio 并返回页面中登录账户的邮箱，Google 拒绝该账户使用 AI Studio 时返回说明原因的错误，服务受限时另在账户根目录记下原因
func (admin *runtimeAdmin) checkAccountAccess(ctx context.Context, email string, state *aistudio.StorageState, proxy string, authUser int) (string, error) {
	if restricted := admin.accountIDs()[strings.ToLower(email)]; restricted != "" {
		return "", accountRestricted(email, restricted)
	}
	signedIn, err := chromeauth.CheckAccess(ctx, state, proxy, authUser)
	if denial := aistudio.RestrictionDenial(err); denial != nil {
		if !errors.Is(denial, camoufoxnative.ErrServiceRestricted) {
			return "", accountRestricted(email, denial.Error())
		}
		return "", errors.Join(accountRestricted(email, denial.Error()), admin.store.RecordRestrictedImport(email, denial.Error()))
	}
	return signedIn, err
}

// accountRestricted 返回账户服务受限、拒绝导入的错误
func accountRestricted(email string, reason string) error {
	return &adminOperationError{status: http.StatusForbidden, code: "account_restricted", message: fmt.Sprintf("%s 服务受限，未导入：%s", email, reason)}
}

func (admin *runtimeAdmin) ImportChromeAccounts(ctx context.Context, input api.ChromeImportInput) (_ []api.AdminAccount, err error) {
	startedAt := time.Now()
	var emails []string
	stage := admin.importStage(ctx, func() string { return strings.Join(emails, ", ") })
	defer func() {
		if err != nil {
			stage("ERROR", api.LogStage{State: "failed"}, "账户导入失败", "duration", time.Since(startedAt), "error", err)
		}
	}()
	if len(input.AccountIDs) == 0 {
		return nil, invalidAccount(fmt.Errorf("未选择 Chrome 账号"))
	}
	root, err := chromeauth.DefaultChromeRoot()
	if err != nil {
		return nil, err
	}
	accountProxy := strings.TrimSpace(input.Proxy)
	timezone, err := accountTimezone(input.Timezone)
	if err != nil {
		return nil, invalidAccount(err)
	}
	discovered, err := chromeauth.Discover(root)
	if err != nil {
		return nil, err
	}
	recorded, err := admin.store.RestrictedImports()
	if err != nil {
		return nil, err
	}
	if err := restrictedChromeSelection(discovered, input.AccountIDs, admin.accountIDs(), recorded); err != nil {
		return nil, err
	}
	environment := admin.accountEnvironment(ctx, admin.effectiveProxy(accountProxy), input.Locale, timezone)
	stage("INFO", api.LogStage{Step: 1, Steps: 3}, "读取 Chrome 登录状态")
	api.ReportTaskStep(ctx, api.TaskStepReading)
	results, err := chromeauth.Import(ctx, chromeauth.ImportOptions{
		ChromeRoot: root, Proxy: admin.effectiveProxy(accountProxy), AccountIDs: input.AccountIDs,
	})
	if err != nil {
		return nil, err
	}
	exit := environment()
	locale, timezone := exit.Locale, exit.Timezone
	for _, result := range results {
		emails = append(emails, strings.ToLower(strings.TrimSpace(result.Email)))
	}
	stage("INFO", api.LogStage{Step: 2, Steps: 3}, "检查 AI Studio 访问")
	api.ReportTaskStep(ctx, api.TaskStepChecking)
	configs := make([]aistudio.AccountConfig, len(results))
	seen := make(map[string]struct{}, len(results))
	for index, result := range results {
		email := strings.ToLower(strings.TrimSpace(result.Email))
		if _, exists := seen[email]; exists {
			return nil, invalidAccount(fmt.Errorf("Chrome 账号重复: %s", email))
		}
		seen[email] = struct{}{}
		accountConfig := aistudio.DefaultAccountConfig(email)
		accountConfig.Note = aistudio.TruncateAccountNote(result.DisplayName)
		accountConfig.Proxy = accountProxy
		accountConfig.Locale, accountConfig.Timezone = locale, timezone
		if err := accountConfig.Validate(); err != nil {
			return nil, invalidAccount(err)
		}
		if _, err := aistudio.NewSigner().Authorization(result.State); err != nil {
			return nil, fmt.Errorf("认证状态无法用于 AI Studio: %w", err)
		}
		if _, err := admin.checkAccountAccess(ctx, email, &results[index].State, admin.effectiveProxy(accountProxy), 0); err != nil {
			return nil, err
		}
		configs[index] = accountConfig
	}
	stage("INFO", api.LogStage{Step: 3, Steps: 3}, "保存账户")
	api.ReportTaskStep(ctx, api.TaskStepSaving)
	target, done := admin.accountCommit()
	imported := make([]*aistudio.Account, 0, len(results))
	for index, result := range results {
		account, err := target.upsertAccount(ctx, configs[index].Label, configs[index], result.State, "")
		if err != nil {
			done()
			return nil, err
		}
		imported = append(imported, account)
	}
	done()
	stage("INFO", api.LogStage{State: "completed"}, "Chrome 账户已导入", "duration", time.Since(startedAt))
	accounts := make([]api.AdminAccount, 0, len(imported))
	for _, account := range imported {
		synced, err := target.syncedAccount(account)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, synced)
	}
	return accounts, nil
}

// ImportAccountState 导入远程配对上传或 Cookie 导入的认证状态与 Camoufox 指纹，未给标签时以 Cookie 中第 auth_user 个会话登录的账户邮箱为标签
func (admin *runtimeAdmin) ImportAccountState(ctx context.Context, input api.AccountStateInput) (_ api.AdminAccount, err error) {
	startedAt := time.Now()
	label := strings.TrimSpace(input.Label)
	stage := admin.importStage(ctx, func() string { return label })
	defer func() {
		if err != nil {
			stage("ERROR", api.LogStage{State: "failed"}, "账户导入失败", "duration", time.Since(startedAt), "error", err)
		}
	}()
	timezone, err := accountTimezone(input.Timezone)
	if err != nil {
		return api.AdminAccount{}, invalidAccount(err)
	}
	environment := admin.accountEnvironment(ctx, admin.effectiveProxy(input.Proxy), input.Locale, timezone)
	stage("INFO", api.LogStage{Step: 1, Steps: 2}, "检查 AI Studio 访问")
	api.ReportTaskStep(ctx, api.TaskStepChecking)
	if label == "" {
		verification, err := chromeauth.VerifyAuthUser(ctx, &input.StorageState, admin.effectiveProxy(input.Proxy), input.AuthUser)
		if errors.Is(err, chromeauth.ErrSignedOut) {
			return api.AdminAccount{}, &adminOperationError{status: http.StatusBadRequest, code: "signed_out", message: err.Error()}
		}
		if denial := aistudio.RestrictionDenial(err); denial != nil {
			return api.AdminAccount{}, accountRestricted(fmt.Sprintf("u/%d", input.AuthUser), denial.Error())
		}
		if err != nil {
			return api.AdminAccount{}, invalidAccount(err)
		}
		label = strings.ToLower(verification.Email)
	}
	accountConfig := aistudio.DefaultAccountConfig(label)
	accountConfig.Note = aistudio.TruncateAccountNote(input.Note)
	accountConfig.AuthUser = input.AuthUser
	accountConfig.Proxy = strings.TrimSpace(input.Proxy)
	exit := environment()
	accountConfig.Locale, accountConfig.Timezone = exit.Locale, exit.Timezone
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
	signedIn, err := admin.checkAccountAccess(ctx, email, &input.StorageState, admin.effectiveProxy(input.Proxy), input.AuthUser)
	if err != nil {
		return api.AdminAccount{}, err
	}
	if !strings.EqualFold(signedIn, email) {
		return api.AdminAccount{}, invalidAccount(fmt.Errorf("登录邮箱与账户不一致: %s", signedIn))
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
	label = email
	stage("INFO", api.LogStage{Step: 2, Steps: 2}, "保存账户")
	api.ReportTaskStep(ctx, api.TaskStepSaving)
	target, done := admin.accountCommit()
	if existing := target.accountID(email); existing != "" && input.RenewalToken != "" {
		done()
		return api.AdminAccount{}, &adminOperationError{
			status: http.StatusConflict, code: "account_exists", message: "账户已存在: " + existing,
		}
	}
	account, err := target.upsertAccount(ctx, email, accountConfig, input.StorageState, fingerprintDirectory)
	if err == nil && input.RenewalToken != "" {
		err = target.savePairingRenewal(account.ID, input.RenewalToken)
	}
	done()
	if err != nil {
		return api.AdminAccount{}, err
	}
	stage("INFO", api.LogStage{State: "completed"}, "认证状态账户已导入", "duration", time.Since(startedAt))
	return target.syncedAccount(account)
}

// importStage 返回写“账户导入”阶段日志的函数：账户任务中以正在处理的账户为来源，其余调用以 auth 为来源并在 accounts 非空时附上账户
func (admin *runtimeAdmin) importStage(ctx context.Context, accounts func() string) func(level string, stage api.LogStage, message string, attrs ...any) {
	source := cmp.Or(api.TaskLabel(ctx), "auth")
	return func(level string, stage api.LogStage, message string, attrs ...any) {
		stage.Name = "账户导入"
		if label := accounts(); source == "auth" && label != "" {
			attrs = append([]any{"account", label}, attrs...)
		}
		admin.requests.logStage(source, level, stage, message, attrs...)
	}
}

// syncedAccount 在后台同步账户模型目录并立即返回账户，新增、导入、登录与启用在提交结束后调用；同步期间账户显示同步阶段，模型与权益经管理事件推送到账户行
func (admin *runtimeAdmin) syncedAccount(account *aistudio.Account) (api.AdminAccount, error) {
	admin.cancelTransition(account.ID)
	admin.startTransition(account.ID, aistudio.AccountSyncing, func(ctx context.Context) {
		admin.syncAccountModelCatalog(ctx, account)
	})
	return admin.account(account.ID)
}

// upsertAccount 在 id 对应的账户已存在时就地更新其认证状态并保留运行态资源绑定（原备注为空时才填入默认备注），否则以 accountConfig 新增账户
func (admin *runtimeAdmin) upsertAccount(
	ctx context.Context,
	id string,
	accountConfig aistudio.AccountConfig,
	state aistudio.StorageState,
	fingerprintDirectory string,
) (*aistudio.Account, error) {
	if existing := admin.accountID(id); existing != "" {
		return admin.updateImportedAccount(ctx, existing, accountConfig.AuthUser, accountConfig.Note, state, fingerprintDirectory)
	}
	return admin.addAccount(accountConfig, state, fingerprintDirectory)
}

// accountID 返回与 id 不区分大小写相同的已有账户 ID，没有时为空
func (admin *runtimeAdmin) accountID(id string) string {
	for _, status := range admin.pool.Status() {
		if strings.EqualFold(status.ID, strings.TrimSpace(id)) {
			return status.ID
		}
	}
	return ""
}

// updateImportedAccount 就地替换已有账户的 Cookie、认证材料与会话序号，保留 runtime-state.json 中的文件资源绑定与模型资格，仅在账户备注为空时补填 defaultNote
func (admin *runtimeAdmin) updateImportedAccount(
	ctx context.Context,
	accountID string,
	authUser int,
	defaultNote string,
	state aistudio.StorageState,
	fingerprintDirectory string,
) (*aistudio.Account, error) {
	lease, err := admin.acquireAccount(ctx, accountID)
	if err != nil {
		return nil, accountOperationError(err)
	}
	account := lease.Account()
	if err := admin.workers.Reset(account.ID); err != nil {
		return nil, errors.Join(err, lease.Release())
	}
	if err := replaceAccountState(lease, state, fingerprintDirectory); err != nil {
		return nil, errors.Join(err, lease.Release())
	}
	if err := setImportedAccountConfig(lease, authUser, defaultNote, admin.workers.prepareUpdate); err != nil {
		return nil, errors.Join(err, lease.Release())
	}
	if err := admin.service.changeModels(func() error {
		return admin.pool.MarkReady(account.ID)
	}); err != nil {
		return nil, errors.Join(err, lease.Release())
	}
	if err := lease.Release(); err != nil {
		return nil, err
	}
	return account, nil
}

func (admin *runtimeAdmin) addAccount(
	accountConfig aistudio.AccountConfig,
	state aistudio.StorageState,
	fingerprintDirectory string,
) (created *aistudio.Account, resultErr error) {
	account, publishLease, err := admin.store.Create(accountConfig, state)
	if errors.Is(err, aistudio.ErrAccountExists) {
		return nil, &adminOperationError{status: http.StatusConflict, code: "account_exists", message: err.Error()}
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		if publishLease != nil {
			resultErr = errors.Join(resultErr, publishLease.Release())
		}
	}()
	if fingerprintDirectory != "" {
		if err := camoufoxnative.PersistAccountFingerprint(fingerprintDirectory, account.Directory); err != nil {
			return nil, errors.Join(err, admin.store.Delete(account))
		}
	}
	if strings.TrimSpace(account.Config.Proxy) == "" {
		account.SetPoolProxy(admin.proxies.Next())
	}
	if err := admin.workers.Add(account); err != nil {
		return nil, errors.Join(err, admin.store.Delete(account))
	}
	if err := admin.service.changeModels(func() error {
		return admin.pool.Add(account)
	}); err != nil {
		return nil, errors.Join(err, admin.workers.Remove(account.ID), admin.store.Delete(account))
	}
	if err := publishLease.Release(); err != nil {
		return nil, err
	}
	publishLease = nil
	if _, err := admin.proxies.Assign(); err != nil {
		return nil, err
	}
	return account, nil
}

// UpdateAccount 在提交时的生成服务保存账户配置，启用账户并同时修改其他配置时在提交结束、释放账户读锁后同步模型目录
func (admin *runtimeAdmin) UpdateAccount(ctx context.Context, accountID string, input api.AccountInput) (api.AdminAccount, error) {
	target, done := admin.accountCommit()
	updated, enabled, err := target.saveAccountConfig(ctx, accountID, input)
	done()
	if err != nil || enabled == nil {
		return updated, err
	}
	return target.syncedAccount(enabled)
}

// SetAccountState 在提交时的生成服务把账户改为 state：disabled 停用账户；ready 先启用停用的账户，再清除服务受限、全局冷却与需要重新登录的标记（各模型的冷却保留），之后由下一次请求按上游结果重新判定
func (admin *runtimeAdmin) SetAccountState(_ context.Context, accountID string, state string) (api.AdminAccount, error) {
	target, done := admin.accountCommit()
	defer done()
	switch state {
	case "disabled":
		return target.setAccountEnabled(accountID, false)
	case string(aistudio.AccountReady):
		return target.restoreAccount(accountID)
	}
	return api.AdminAccount{}, invalidAccount(fmt.Errorf("账户状态只能改为 ready 或 disabled: %q", state))
}

// restoreAccount 启用停用的账户并清除它的服务受限、全局冷却与需要重新登录的标记，已启用而没有模型目录的账户在后台同步目录；写一条日志并推送冷却、目录与账户状态
func (admin *runtimeAdmin) restoreAccount(accountID string) (api.AdminAccount, error) {
	current, err := admin.account(accountID)
	if err != nil {
		return api.AdminAccount{}, err
	}
	if !current.Enabled {
		if _, err := admin.setAccountEnabled(accountID, true); err != nil {
			return api.AdminAccount{}, err
		}
	}
	if err := admin.pool.ClearAccountState(accountID); err != nil {
		return api.AdminAccount{}, accountOperationError(err)
	}
	if current.Enabled && len(current.Models) == 0 {
		account, err := admin.pool.Account(accountID)
		if err != nil {
			return api.AdminAccount{}, accountOperationError(err)
		}
		if _, err := admin.syncedAccount(account); err != nil {
			return api.AdminAccount{}, err
		}
	}
	admin.requests.log(current.Label, "INFO", "账户状态已改为就绪", "previous", current.State)
	cooldowns, _ := admin.Cooldowns(admin.lifecycle)
	admin.requests.publish(api.AdminEvent{Type: "cooldowns", Data: cooldowns})
	admin.syncModelCache()
	return admin.account(accountID)
}

// saveAccountConfig 保存账户配置并返回账户，启用账户并同时修改其他配置时另返回需要同步模型目录的账户
func (admin *runtimeAdmin) saveAccountConfig(ctx context.Context, accountID string, input api.AccountInput) (api.AdminAccount, *aistudio.Account, error) {
	if !strings.EqualFold(strings.TrimSpace(input.Label), strings.TrimSpace(accountID)) {
		return api.AdminAccount{}, nil, invalidAccount(fmt.Errorf("账户邮箱不可修改"))
	}
	accountConfig := aistudio.DefaultAccountConfig(strings.ToLower(strings.TrimSpace(accountID)))
	accountConfig.Note = strings.TrimSpace(input.Note)
	accountConfig.Enabled = input.Enabled
	accountConfig.Proxy = strings.TrimSpace(input.Proxy)
	accountConfig.Locale = strings.TrimSpace(input.Locale)
	timezone, err := accountTimezone(input.Timezone)
	if err != nil {
		return api.AdminAccount{}, nil, invalidAccount(err)
	}
	accountConfig.Timezone = timezone
	if err := accountConfig.Validate(); err != nil {
		return api.AdminAccount{}, nil, invalidAccount(err)
	}
	current, err := admin.account(accountID)
	if err != nil {
		return api.AdminAccount{}, nil, err
	}
	if current.Proxy == accountConfig.Proxy && current.Locale == accountConfig.Locale && current.Timezone == accountConfig.Timezone {
		if current.Note != accountConfig.Note {
			if _, err := admin.pool.SetNote(accountID, accountConfig.Note); err != nil {
				return api.AdminAccount{}, nil, accountOperationError(err)
			}
		}
		if current.Enabled != accountConfig.Enabled {
			updated, err := admin.setAccountEnabled(accountID, accountConfig.Enabled)
			return updated, nil, err
		}
		if current.Note != accountConfig.Note {
			admin.requests.log("auth", "INFO", "账户配置已更新", "account", accountConfig.Label)
			admin.publishRuntimeSnapshot(admin.lifecycle)
		}
		updated, err := admin.account(accountID)
		return updated, nil, err
	}
	lease, err := admin.acquireAccount(ctx, accountID)
	if err != nil {
		return api.AdminAccount{}, nil, accountOperationError(err)
	}
	account := lease.Account()
	accountConfig.AuthUser = account.Config.AuthUser
	enabling := accountConfig.Enabled && !account.Config.Enabled
	if accountConfig.Proxy == "" && !admin.proxies.Available(account.PoolProxy("")) {
		account.SetPoolProxy(admin.proxies.Next())
	}
	workerUpdate, err := admin.workers.prepareUpdate(account, accountConfig)
	if err != nil {
		return api.AdminAccount{}, nil, errors.Join(err, lease.Release())
	}
	if err := ctx.Err(); err != nil {
		workerUpdate.Discard()
		return api.AdminAccount{}, nil, errors.Join(err, lease.Release())
	}
	if err := admin.service.changeModels(func() error {
		return lease.SaveConfig(accountConfig)
	}); err != nil {
		workerUpdate.Discard()
		return api.AdminAccount{}, nil, errors.Join(err, lease.Release())
	}
	workerUpdate.Commit()
	if err := lease.Release(); err != nil {
		return api.AdminAccount{}, nil, err
	}
	if _, err := admin.proxies.Assign(); err != nil {
		return api.AdminAccount{}, nil, err
	}
	admin.syncModelCache()
	admin.requests.log("auth", "INFO", "账户配置已更新", "account", accountConfig.Label)
	if enabling {
		return api.AdminAccount{}, account, nil
	}
	updated, err := admin.account(account.ID)
	return updated, nil, err
}

// setAccountEnabled 立即保存账户启用状态并返回，等待在途请求、关闭 Worker 与同步模型目录转入后台，阶段显示在账户状态上
func (admin *runtimeAdmin) setAccountEnabled(accountID string, enabled bool) (api.AdminAccount, error) {
	admin.cancelTransition(accountID)
	var account *aistudio.Account
	if err := admin.service.changeModels(func() (err error) {
		account, err = admin.pool.SetEnabled(accountID, enabled)
		return err
	}); err != nil {
		return api.AdminAccount{}, accountOperationError(err)
	}
	if enabled {
		admin.startTransition(accountID, aistudio.AccountSyncing, func(ctx context.Context) { admin.finishEnable(ctx, account) })
	} else {
		admin.startTransition(accountID, aistudio.AccountDraining, func(ctx context.Context) { admin.finishDisable(ctx, account) })
	}
	admin.requests.log("auth", "INFO", "账户配置已更新", "account", account.Config.Label)
	return admin.account(accountID)
}

// startTransition 把账户的后台收尾阶段设为 stage 并推送状态，在生命周期内后台运行 run，结束后清除阶段并再次推送；调用方先以 cancelTransition 结束该账户进行中的收尾
func (admin *runtimeAdmin) startTransition(accountID string, stage aistudio.AccountTransition, run func(context.Context)) {
	admin.pool.SetTransition(accountID, stage, "")
	ctx, cancel := context.WithCancel(admin.lifecycle)
	transition := &accountTransition{cancel: cancel, done: make(chan struct{})}
	admin.transitionMu.Lock()
	if admin.transitions == nil {
		admin.transitions = make(map[string]*accountTransition)
	}
	admin.transitions[accountID] = transition
	admin.transitionMu.Unlock()
	go func() {
		defer close(transition.done)
		defer cancel()
		run(ctx)
		admin.pool.SetTransition(accountID, "", "")
		admin.transitionMu.Lock()
		if admin.transitions[accountID] == transition {
			delete(admin.transitions, accountID)
		}
		admin.transitionMu.Unlock()
		admin.publishRuntimeSnapshot(admin.lifecycle)
	}()
	admin.publishRuntimeSnapshot(admin.lifecycle)
}

// finishDisable 等账户在途请求结束后关闭 Worker 并刷新目录
func (admin *runtimeAdmin) finishDisable(ctx context.Context, account *aistudio.Account) {
	startedAt := time.Now()
	lease, err := admin.pool.AcquireAccount(ctx, account.ID)
	if err != nil {
		if ctx.Err() == nil {
			admin.requests.log("auth", "ERROR", "账户停用收尾失败", "account", account.ID, "error", err)
		}
		return
	}
	if err := errors.Join(admin.workers.Reset(account.ID), lease.Release()); err != nil {
		admin.requests.log("auth", "ERROR", "账户停用收尾失败", "account", account.ID, "error", err)
	} else {
		admin.requests.log("auth", "INFO", "账户停用完成", "account", account.ID, "duration", time.Since(startedAt))
	}
	admin.syncModelCache()
}

// finishEnable 为启用的账户分配代理池出口、刷新目录并同步账户模型目录
func (admin *runtimeAdmin) finishEnable(ctx context.Context, account *aistudio.Account) {
	if strings.TrimSpace(account.Config.Proxy) == "" && !admin.proxies.Available(account.PoolProxy("")) {
		account.SetPoolProxy(admin.proxies.Next())
	}
	if _, err := admin.proxies.Assign(); err != nil {
		admin.requests.log("auth", "ERROR", "账户启用收尾失败", "account", account.ID, "error", err)
	}
	admin.syncModelCache()
	admin.syncAccountModelCatalog(ctx, account)
}

// acquireAccount 结束账户进行中的后台收尾并取得账户独占
// 账户有在途请求或其他操作时，等待期间账户显示等待阶段，日志记下开始等待
func (admin *runtimeAdmin) acquireAccount(ctx context.Context, accountID string) (*aistudio.AccountLease, error) {
	admin.cancelTransition(accountID)
	if busy, _ := admin.pool.Activity(accountID); busy {
		admin.pool.SetTransition(accountID, aistudio.AccountSaving, aistudio.AccountStepWaiting)
		defer admin.pool.SetTransition(accountID, "", "")
		admin.requests.log("auth", "INFO", refreshStepMessages[aistudio.AccountStepWaiting], "account", accountID)
	}
	return admin.pool.AcquireAccount(ctx, accountID)
}

// cancelTransition 取消账户进行中的后台收尾并等待它结束
func (admin *runtimeAdmin) cancelTransition(accountID string) {
	admin.transitionMu.Lock()
	transition := admin.transitions[accountID]
	admin.transitionMu.Unlock()
	if transition != nil {
		transition.cancel()
		<-transition.done
	}
}

// DeleteAccount 删除账户与它的目录，无法载入的账户目录直接删除
func (admin *runtimeAdmin) DeleteAccount(ctx context.Context, accountID string) error {
	if failed, err := admin.store.DeleteFailure(accountID); failed {
		if err == nil {
			admin.requests.log("auth", "INFO", "无法载入的账户目录已删除", "account", accountID)
		}
		return err
	}
	admin.cancelTransition(accountID)
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
	if err == nil {
		admin.service.removeAccountModelRetry(accountID)
		err = admin.workers.Remove(accountID)
	}
	if err != nil {
		admin.requests.log("auth", "ERROR", "账户删除失败", "account", account.Label, "error", err)
		return accountOperationError(err)
	}
	admin.requests.log("auth", "INFO", "账户已删除", "account", account.Label)
	return nil
}

func (admin *runtimeAdmin) LoginAccount(ctx context.Context, accountID string, view *camoufoxnative.LiveView) (_ api.AdminAccount, err error) {
	account, err := admin.pool.Account(accountID)
	if err != nil {
		return api.AdminAccount{}, accountOperationError(err)
	}
	directory, err := os.MkdirTemp("", "aistudio2api-account-login-*")
	if err != nil {
		return api.AdminAccount{}, fmt.Errorf("创建隔离登录目录: %w", err)
	}
	defer os.RemoveAll(directory)
	if err := camoufoxnative.PersistAccountFingerprint(account.Directory, directory); err != nil {
		return api.AdminAccount{}, err
	}
	startedAt := time.Now()
	label := account.Config.Label
	defer func() {
		if err != nil {
			admin.requests.logStage(label, "ERROR", api.LogStage{Name: "账户登录", State: "failed"}, "账户登录失败", "duration", time.Since(startedAt), "error", err)
		}
	}()
	admin.requests.logStage(label, "INFO", api.LogStage{Name: "账户登录", Step: 1, Steps: 3}, "等待隔离登录")
	api.ReportTaskStep(ctx, api.TaskStepLogin)
	request := admin.loginRequest(account, directory)
	request.View = view
	saveAccount := func(saveCtx context.Context, logged aistudio.IsolatedLoginResult) (*runtimeAdmin, *aistudio.Account, error) {
		if !strings.EqualFold(strings.TrimSpace(logged.Email), account.ID) {
			return nil, nil, invalidAccount(fmt.Errorf("登录邮箱与账户不一致: %s", logged.Email))
		}
		if logged.DriveError != "" {
			admin.requests.logStage(label, "WARN", api.LogStage{Name: "账户登录"}, "Drive 授权失败", "error", logged.DriveError)
		}
		admin.requests.logStage(label, "INFO", api.LogStage{Name: "账户登录", Step: 2, Steps: 3}, "保存认证状态")
		api.ReportTaskStep(ctx, api.TaskStepSaving)
		if _, authErr := aistudio.NewSigner().Authorization(logged.StorageState); authErr != nil {
			return nil, nil, fmt.Errorf("认证状态无法用于 AI Studio: %w", authErr)
		}
		target, done := admin.accountCommit()
		saved, saveErr := target.saveLogin(saveCtx, accountID, logged.StorageState, directory)
		done()
		if saveErr != nil {
			return nil, nil, saveErr
		}
		return target, saved, nil
	}
	request.OnLoggedIn = func(saveCtx context.Context, logged aistudio.IsolatedLoginResult) error {
		target, _, saveErr := saveAccount(saveCtx, logged)
		if saveErr != nil {
			return saveErr
		}
		target.publishRuntimeSnapshot(target.lifecycle)
		return nil
	}
	request.CheckVerification = func(checkCtx context.Context, updated aistudio.StorageState) (bool, error) {
		admin.requests.logStage(label, "INFO", api.LogStage{Name: "账户登录", Step: 3, Steps: 3}, "核对账户验证状态")
		target, done := admin.accountCommit()
		defer done()
		return target.verifyAccountNow(checkCtx, accountID, updated)
	}
	request.OnVerifyStep = func(step string) { reportAccountVerifyStep(ctx, step) }
	result, err := admin.login.Login(ctx, request)
	if err != nil {
		return api.AdminAccount{}, err
	}
	if result.CloseError != "" {
		admin.requests.logStage(label, "WARN", api.LogStage{Name: "账户登录"}, "关闭 Camoufox 失败", "error", result.CloseError)
	}
	var saved api.AdminAccount
	if result.Saved {
		if result.VerificationError != "" {
			admin.requests.logStage(label, "WARN", api.LogStage{Name: "账户登录"}, "账户验证核对失败", "error", result.VerificationError)
		}
		target, done := admin.accountCommit()
		saved, err = target.account(accountID)
		done()
	} else {
		var target *runtimeAdmin
		target, account, err = saveAccount(ctx, result)
		if err == nil {
			saved, err = target.verifiedAccount(account)
		}
	}
	if err != nil {
		return api.AdminAccount{}, err
	}
	admin.requests.logStage(label, "INFO", api.LogStage{Name: "账户登录", State: "completed"}, "账户登录完成", "duration", time.Since(startedAt))
	return saved, nil
}

// VerifyAccount 打开停在官网验证页的 Camoufox 窗口供用户完成验证，并在页面变化或定时自动再核对，验证通过后自动关闭窗口恢复就绪
func (admin *runtimeAdmin) VerifyAccount(ctx context.Context, accountID string, view *camoufoxnative.LiveView) (_ api.AdminAccount, err error) {
	account, err := admin.pool.Account(accountID)
	if err != nil {
		return api.AdminAccount{}, accountOperationError(err)
	}
	verifier, ok := admin.login.(aistudio.AccountVerifyDriver)
	if !ok {
		return api.AdminAccount{}, fmt.Errorf("当前登录驱动不支持打开验证窗口")
	}
	state, loadErr := aistudio.LoadStorageState(account.StoragePath)
	if loadErr != nil {
		state = account.StorageState
	}
	if len(state.Cookies) == 0 {
		return api.AdminAccount{}, fmt.Errorf("账户没有可用于验证的 Cookie")
	}
	directory, err := os.MkdirTemp("", "aistudio2api-account-verify-*")
	if err != nil {
		return api.AdminAccount{}, fmt.Errorf("创建隔离验证目录: %w", err)
	}
	defer os.RemoveAll(directory)
	if err := camoufoxnative.PersistAccountFingerprint(account.Directory, directory); err != nil {
		return api.AdminAccount{}, err
	}
	startedAt := time.Now()
	label := account.Config.Label
	defer func() {
		if err != nil {
			admin.requests.logStage(label, "ERROR", api.LogStage{Name: "账户验证", State: "failed"}, "账户验证失败", "duration", time.Since(startedAt), "error", err)
		}
	}()
	admin.requests.logStage(label, "INFO", api.LogStage{Name: "账户验证", Step: 1, Steps: 2}, "等待完成账户验证")
	api.ReportTaskStep(ctx, api.TaskStepVerify)
	pending, err := verifier.Verify(ctx, aistudio.AccountVerifyRequest{
		Directory:    directory,
		StorageState: state,
		AuthUser:     account.Config.AuthUser,
		Proxy:        account.EffectiveProxy(admin.currentGlobalProxy()),
		Locale:       account.Config.Locale,
		Timezone:     account.Config.Timezone,
		View:         view,
		CheckVerification: func(checkCtx context.Context, updated aistudio.StorageState) (bool, error) {
			admin.requests.logStage(label, "INFO", api.LogStage{Name: "账户验证", Step: 2, Steps: 2}, "核对账户验证状态")
			target, done := admin.accountCommit()
			defer done()
			return target.verifyAccountNow(checkCtx, accountID, updated)
		},
		OnVerifyStep: func(step string) { reportAccountVerifyStep(ctx, step) },
	})
	if err != nil {
		return api.AdminAccount{}, err
	}
	target, done := admin.accountCommit()
	current, err := target.account(accountID)
	done()
	if err != nil {
		return api.AdminAccount{}, err
	}
	message := "账户验证完成"
	if pending || current.VerificationRequired {
		message = "账户验证窗口已关闭，账户仍需要验证"
	}
	admin.requests.logStage(label, "INFO", api.LogStage{Name: "账户验证", State: "completed"}, message, "duration", time.Since(startedAt))
	return current, nil
}

// reportAccountVerifyStep 把验证阶段的步骤映射并报告到账户任务
func reportAccountVerifyStep(ctx context.Context, step string) {
	switch step {
	case "verifying":
		api.ReportTaskStep(ctx, api.TaskStepVerifying)
	case "verify":
		api.ReportTaskStep(ctx, api.TaskStepVerify)
	}
}

// verifiedAccount 在后台同步账户目录并核对一次验证状态，立即返回账户
func (admin *runtimeAdmin) verifiedAccount(account *aistudio.Account) (api.AdminAccount, error) {
	admin.cancelTransition(account.ID)
	admin.startTransition(account.ID, aistudio.AccountSyncing, func(ctx context.Context) {
		verifyCtx := aistudio.ContextWithRefreshSteps(ctx, func(step aistudio.AccountStep, _, _ int) {
			if step == aistudio.AccountStepWaiting {
				admin.requests.log("auth", "INFO", refreshStepMessages[step], "account", account.ID)
			}
			admin.pool.SetTransition(account.ID, aistudio.AccountSyncing, step)
			admin.publishRuntimeSnapshot(admin.lifecycle)
		})
		if err := admin.service.verifyAccount(verifyCtx, account.ID); err != nil {
			if ctx.Err() == nil {
				admin.requests.log(account.Config.Label, "WARN", "账户验证核对失败", "error", err)
			}
			return
		}
		if admin.pool.VerificationRequired(account.ID) {
			admin.requests.log(account.Config.Label, "WARN", "账户需要验证")
			return
		}
		admin.requests.log(account.Config.Label, "INFO", "账户核对完成")
	})
	return admin.account(account.ID)
}

// verifyAccountNow 在账户行显示核对步骤并执行一次目录同步与生成核对，返回账户是否仍需要验证
func (admin *runtimeAdmin) verifyAccountNow(ctx context.Context, accountID string, updated aistudio.StorageState) (bool, error) {
	admin.cancelTransition(accountID)
	if len(updated.Cookies) > 0 {
		if err := admin.syncVerifiedCookies(ctx, accountID, updated); err != nil {
			return admin.pool.VerificationRequired(accountID), err
		}
	}
	admin.pool.SetTransition(accountID, aistudio.AccountSyncing, aistudio.AccountStepCatalog)
	admin.publishRuntimeSnapshot(admin.lifecycle)
	verifyCtx := aistudio.ContextWithRefreshSteps(ctx, func(step aistudio.AccountStep, _, _ int) {
		if step == aistudio.AccountStepWaiting {
			admin.requests.log("auth", "INFO", refreshStepMessages[step], "account", accountID)
		}
		admin.pool.SetTransition(accountID, aistudio.AccountSyncing, step)
		admin.publishRuntimeSnapshot(admin.lifecycle)
	})
	err := admin.service.verifyAccount(verifyCtx, accountID)
	admin.pool.SetTransition(accountID, "", "")
	admin.publishRuntimeSnapshot(admin.lifecycle)
	return admin.pool.VerificationRequired(accountID), err
}

// syncVerifiedCookies 把验证浏览器导出的最新 Cookie 写回账户（保留原 AuthExtension），认证签名 Cookie 变化时重置 Worker
func (admin *runtimeAdmin) syncVerifiedCookies(ctx context.Context, accountID string, updated aistudio.StorageState) error {
	if len(updated.Cookies) == 0 {
		return nil
	}
	lease, err := admin.pool.AcquireAccount(ctx, accountID)
	if err != nil {
		return err
	}
	previous := lease.Account().StorageState.Cookies
	if slices.Equal(previous, updated.Cookies) {
		return lease.Release()
	}
	changed := signingCookiesChanged(previous, updated.Cookies)
	err = lease.ReplaceCookies(updated.Cookies)
	releaseErr := lease.Release()
	if err = errors.Join(err, releaseErr); err != nil {
		return err
	}
	if changed {
		return admin.workers.Reset(accountID)
	}
	return nil
}

// signingCookiesChanged 判断两次 Cookie 集合中 Google 认证签名 Cookie 是否变化
func signingCookiesChanged(previous, next []aistudio.StateCookie) bool {
	names := []string{"SAPISID", "__Secure-1PAPISID", "__Secure-3PAPISID", "__Secure-1PSID", "__Secure-3PSID"}
	lookup := func(list []aistudio.StateCookie, name string) string {
		for _, cookie := range list {
			if cookie.Name == name && cookie.Domain == ".google.com" {
				return cookie.Value
			}
		}
		return ""
	}
	for _, name := range names {
		if lookup(previous, name) != lookup(next, name) {
			return true
		}
	}
	return false
}

// saveLogin 以浏览器登录得到的认证状态与指纹目录替换账户的登录状态，重建该账户的 Worker 并返回账户
func (admin *runtimeAdmin) saveLogin(ctx context.Context, accountID string, state aistudio.StorageState, fingerprintDirectory string) (*aistudio.Account, error) {
	lease, err := admin.acquireAccount(ctx, accountID)
	if err != nil {
		return nil, accountOperationError(err)
	}
	account := lease.Account()
	if err := admin.workers.Reset(account.ID); err != nil {
		return nil, errors.Join(err, lease.Release())
	}
	if err := replaceAccountState(lease, state, fingerprintDirectory); err != nil {
		return nil, errors.Join(err, lease.Release())
	}
	if err := setImportedAccountConfig(lease, 0, "", admin.workers.prepareUpdate); err != nil {
		return nil, errors.Join(err, lease.Release())
	}
	if err := admin.service.changeModels(func() error {
		return errors.Join(
			admin.pool.MarkReady(account.ID),
			admin.pool.ResetModelAccess(account.ID),
			admin.pool.SetCatalog(account.ID, account.BenefitTier, nil),
		)
	}); err != nil {
		return nil, errors.Join(err, lease.Release())
	}
	if err := lease.Release(); err != nil {
		return nil, err
	}
	return account, nil
}

// replaceAccountState 以新登录状态替换账户的认证状态，fingerprintDirectory 非空时一并替换 Camoufox 指纹
func replaceAccountState(lease *aistudio.AccountLease, state aistudio.StorageState, fingerprintDirectory string) error {
	account := lease.Account()
	if fingerprintDirectory != "" {
		if err := camoufoxnative.PersistAccountFingerprint(fingerprintDirectory, account.Directory); err != nil {
			return err
		}
	}
	return lease.SaveStorageState(state)
}

// setImportedAccountConfig 在重新导入时同步会话序号，并在原备注为空时填入 defaultNote
func setImportedAccountConfig(lease *aistudio.AccountLease, authUser int, defaultNote string, prepare func(*aistudio.Account, aistudio.AccountConfig) (*accountWorkerUpdate, error)) error {
	account := lease.Account()
	fillNote := strings.TrimSpace(account.Config.Note) == "" && strings.TrimSpace(defaultNote) != ""
	if account.Config.AuthUser == authUser && !fillNote {
		return nil
	}
	accountConfig := account.Config
	authUserChanged := accountConfig.AuthUser != authUser
	accountConfig.AuthUser = authUser
	if fillNote {
		accountConfig.Note = aistudio.TruncateAccountNote(defaultNote)
	}
	if !authUserChanged {
		return lease.SaveConfig(accountConfig)
	}
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

// refreshStepMessages 是立即刷新、目录同步与账户保存各步骤写入日志的说明
var refreshStepMessages = map[aistudio.AccountStep]string{
	aistudio.AccountStepWaiting:   "等待账户的在途请求结束",
	aistudio.AccountStepCatalog:   "读取权益与模型目录",
	aistudio.AccountStepVerifying: "发送一次生成请求核对验证状态",
}

// RefreshAccount 在服务生命周期中立即读取账户权益与模型目录，管理页断开不中止
// 每进入一步把步骤写入账户的后台阶段与日志，结束时日志写明账户是否解除服务受限
func (admin *runtimeAdmin) RefreshAccount(_ context.Context, accountID string) (api.AdminAccountRefresh, error) {
	startedAt := time.Now()
	stage := func(level string, stage api.LogStage, message string, attrs ...any) {
		stage.Name = "账户刷新"
		admin.requests.logStage(accountID, level, stage, message, attrs...)
	}
	before, _ := admin.account(accountID)
	ctx := aistudio.ContextWithRefreshSteps(admin.service.lifecycle, func(step aistudio.AccountStep, index, total int) {
		admin.pool.SetRefreshStep(accountID, step, index, total)
		stage("INFO", api.LogStage{Step: index, Steps: total}, refreshStepMessages[step])
	})
	refresh, err := admin.service.refreshAccountState(ctx, accountID, true)
	admin.pool.SetRefreshStep(accountID, "", 0, 0)
	if err != nil {
		stage("ERROR", api.LogStage{State: "failed"}, "账户刷新失败", "duration", time.Since(startedAt), "error", err)
		return api.AdminAccountRefresh{}, accountOperationError(err)
	}
	account, err := admin.account(accountID)
	if err != nil {
		return api.AdminAccountRefresh{}, err
	}
	message := "账户刷新完成"
	switch {
	case account.State == string(aistudio.AccountRestricted):
		message = "账户刷新完成，账户仍为服务受限"
	case before.State == string(aistudio.AccountRestricted) && account.Enabled:
		message = "账户刷新完成，已解除服务受限"
	}
	stage("INFO", api.LogStage{State: "completed"}, message, "duration", time.Since(startedAt))
	return api.AdminAccountRefresh{
		Account: account, PreviousTier: refresh.PreviousTier.String(), Tier: refresh.Tier.String(),
		PreviousModels: refresh.PreviousModels, Models: refresh.Models,
	}, nil
}

// StartService 使用管理器提供的启动生命周期启动生成服务
func (admin *runtimeAdmin) StartService(ctx context.Context) (api.AdminStatus, error) {
	startedAt := time.Now()
	models, started, err := admin.service.Start(ctx, func() {
		admin.publishRuntimeSnapshot(ctx)
	})
	if errors.Is(err, errServiceTransitioning) {
		return admin.Status(ctx)
	}
	if err != nil {
		admin.publishRuntimeSnapshot(ctx)
		if errors.Is(err, context.Canceled) && admin.service.State() == "STOPPED" {
			admin.requests.logStage("service", "INFO", api.LogStage{Name: "生成服务启动", State: "cancelled"}, "生成服务启动已取消", "duration", time.Since(startedAt))
			return admin.Status(ctx)
		}
		admin.requests.logStage("service", "ERROR", api.LogStage{Name: "生成服务启动", State: "failed"}, "生成服务启动失败", "duration", time.Since(startedAt), "error", err)
		if errors.Is(err, aistudio.ErrNoEligibleAccount) {
			return api.AdminStatus{}, &adminOperationError{
				status: http.StatusBadRequest, code: "account_required", message: "请先启用一个可用账户",
			}
		}
		return api.AdminStatus{}, err
	}
	if len(models) == 0 {
		admin.requests.logStage("service", "ERROR", api.LogStage{Name: "生成服务启动", State: "failed"}, "生成服务启动失败", "duration", time.Since(startedAt), "error", "没有可用账户")
		return api.AdminStatus{}, &adminOperationError{
			status: http.StatusBadRequest, code: "account_required", message: "请先添加一个可用账户",
		}
	}
	if started {
		admin.requests.logStage("service", "INFO", api.LogStage{Name: "生成服务启动", State: "completed"}, "生成服务就绪",
			"models", len(models), "workers", fmt.Sprintf("%d/%d", len(admin.workers.WarmAccountIDs()), admin.workers.PrewarmTarget()),
			"duration", time.Since(startedAt))
	} else {
		admin.requests.log("service", "INFO", "生成服务运行中",
			"models", len(models), "workers", fmt.Sprintf("%d/%d", len(admin.workers.WarmAccountIDs()), admin.workers.PrewarmTarget()))
	}
	return admin.publishRuntimeSnapshot(ctx), nil
}

// stopStage 是停止生成服务各行日志所属的阶段名
const stopStage = "生成服务停止"

// StopService 停止生成服务并把经过写成一个日志阶段，服务本来就停着时不写日志
func (admin *runtimeAdmin) StopService(ctx context.Context) (api.AdminStatus, error) {
	startedAt := time.Now()
	stage := func(level, state, message string, attrs ...any) {
		admin.requests.logStage("service", level, api.LogStage{Name: stopStage, State: state}, message, attrs...)
	}
	active := admin.service.State() != "STOPPED"
	if active {
		stage("INFO", "", "生成服务停止", "workers", len(admin.workers.WarmAccountIDs()))
	}
	stopped, err := admin.service.Stop()
	if err != nil {
		stage("ERROR", "failed", "生成服务停止失败", "duration", time.Since(startedAt), "error", err)
		return api.AdminStatus{}, err
	}
	if stopped || active {
		stage("INFO", "completed", "生成服务已停止", "duration", time.Since(startedAt))
	}
	return admin.publishRuntimeSnapshot(ctx), nil
}

// syncModelCache 在账户写入后刷新权威快照
func (admin *runtimeAdmin) syncModelCache() {
	ctx := admin.lifecycle
	_ = admin.service.SyncModels(ctx)
	admin.publishRuntimeSnapshot(ctx)
}

func (admin *runtimeAdmin) syncAccountModelCatalog(ctx context.Context, account *aistudio.Account) {
	ctx = aistudio.ContextWithRefreshSteps(ctx, func(step aistudio.AccountStep, _, _ int) {
		if step != aistudio.AccountStepWaiting {
			step = ""
		} else {
			admin.requests.log("auth", "INFO", refreshStepMessages[step], "account", account.ID)
		}
		admin.pool.SetTransition(account.ID, aistudio.AccountSyncing, step)
	})
	models, err := admin.service.syncAccountModels(ctx, account.ID)
	if err != nil {
		admin.service.publishModelAccess()
		return
	}
	if len(models) > 0 {
		admin.requests.log(account.Config.Label, "INFO", "账户模型目录同步完成", "models", len(models))
	}
	admin.service.publishModelAccess()
}

// publishStatus 生成并推送生成服务状态快照
// 约束：快照在 publishMu 内生成并推送，后生成的快照后到达管理页
func (admin *runtimeAdmin) publishStatus(ctx context.Context) {
	admin.publishMu.Lock()
	defer admin.publishMu.Unlock()
	status, _ := admin.Status(ctx)
	admin.requests.publish(api.AdminEvent{Type: "status", Data: status})
}

// publishRuntimeSnapshot 推送管理页权威运行状态并返回其中的服务状态
func (admin *runtimeAdmin) publishRuntimeSnapshot(ctx context.Context) api.AdminStatus {
	admin.publishMu.Lock()
	defer admin.publishMu.Unlock()
	status, _ := admin.Status(ctx)
	models, _ := admin.CatalogModels(ctx)
	accounts, _ := admin.Accounts(ctx)
	admin.requests.publish(api.AdminEvent{Type: "status", Data: status})
	admin.requests.publish(api.AdminEvent{Type: "models", Data: map[string]any{"models": models}})
	admin.requests.publish(api.AdminEvent{Type: "accounts", Data: map[string]any{"accounts": accounts}})
	return status
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

func (admin *runtimeAdmin) setProxyState(globalProxy, activeProxy string) {
	admin.proxyMu.Lock()
	admin.globalProxy = strings.TrimSpace(globalProxy)
	admin.activeProxy = strings.TrimSpace(activeProxy)
	admin.config.Proxy = admin.activeProxy
	admin.proxyMu.Unlock()
}

func (admin *runtimeAdmin) currentGlobalProxy() string {
	admin.proxyMu.RLock()
	defer admin.proxyMu.RUnlock()
	return admin.globalProxy
}

func (admin *runtimeAdmin) currentActiveProxy() string {
	admin.proxyMu.RLock()
	active := admin.activeProxy
	fallback := admin.config.Proxy
	admin.proxyMu.RUnlock()
	if active != "" {
		return active
	}
	return fallback
}

// ProxyState 返回当前生效的代理展示串与代理池健康状态
func (admin *runtimeAdmin) ProxyState() (string, []api.AdminProxy) {
	return admin.currentActiveProxy(), admin.proxyPoolStatus()
}

// ApplyProxy 热更新全局默认代理与代理池：更新传输、续签与 Worker 配置，并通过后台排空换绑重建受影响账户的 Worker，不中断进行中的请求
func (admin *runtimeAdmin) ApplyProxy(rawProxy string) error {
	if admin.proxies == nil || admin.service == nil {
		return nil
	}
	proxyList, globalProxy, activeProxy, _ := resolveProxyConfig(rawProxy)
	prevGlobal := admin.currentGlobalProxy()
	prevActive := admin.currentActiveProxy()
	if activeProxy == prevActive && globalProxy == prevGlobal {
		return nil
	}
	if err := admin.service.setSharedGlobalProxy(globalProxy); err != nil {
		return err
	}
	admin.setProxyState(globalProxy, activeProxy)
	changed, err := admin.proxies.SetProxies(prevGlobal, proxyList)
	admin.syncProxyProbe(len(proxyList), true)
	if len(changed) > 0 {
		admin.rebindAccounts(changed)
	} else {
		admin.publishRuntimeSnapshot(admin.lifecycle)
	}
	return err
}

func (admin *runtimeAdmin) effectiveProxy(accountProxy string) string {
	if proxy := strings.TrimSpace(accountProxy); proxy != "" {
		return proxy
	}
	if proxy := admin.proxies.Next(); proxy != "" {
		return proxy
	}
	return admin.currentGlobalProxy()
}

func (admin *runtimeAdmin) loginRequest(account *aistudio.Account, directory string) aistudio.IsolatedLoginRequest {
	return aistudio.IsolatedLoginRequest{
		Directory: directory, Proxy: account.EffectiveProxy(admin.currentGlobalProxy()),
		Locale: account.Config.Locale, Timezone: account.Config.Timezone,
	}
}

func adminAccountDTO(status aistudio.AccountStatus) api.AdminAccount {
	account := api.AdminAccount{
		ID: status.ID, Label: status.Label, Note: status.Note, Enabled: status.Enabled, State: string(status.State),
		Proxy: status.Proxy, Egress: status.Egress, Locale: status.Locale, Timezone: status.Timezone,
		Models: status.Models, BenefitTier: status.BenefitTier, Message: status.Message,
		PairingRenewedAt: pairingRenewedAt(status.Directory),
		Transition:       string(status.Transition), ActiveRequests: status.ActiveRequests, CatalogError: status.CatalogError,
		TransitionStep: string(status.TransitionStep), TransitionIndex: status.TransitionIndex, TransitionTotal: status.TransitionTotal,
		VerificationRequired: status.VerificationRequired,
		DeviceBoundSession:   status.DeviceBoundSession && !hasPairingRenewal(status.Directory),
	}
	if status.Transition != "" {
		account.TransitionSince = &status.TransitionSince
	}
	return account
}

// accountTimezone 返回去掉不可见字符后的账户时区，为空时返回空串，无法加载时返回带原值的错误
func accountTimezone(value string) (string, error) {
	timezone := aistudio.NormalizeTimezone(value)
	if timezone == "" {
		return "", nil
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return "", fmt.Errorf("账户 timezone 无效: %q", value)
	}
	return timezone, nil
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
	value.ActiveProxy = admin.currentActiveProxy()
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
	switchInterval := strings.ToLower(strings.TrimSpace(value.AccountSwitchInterval))
	if switchInterval == "" {
		switchInterval = config.AccountSwitchIntervalOff
	}
	maxQuotaSwitches := strings.ToLower(strings.TrimSpace(value.MaxQuotaSwitches))
	if maxQuotaSwitches == "" {
		maxQuotaSwitches = config.MaxQuotaSwitchesOff
	}
	cfg := config.Config{
		AutoStart:      value.AutoStart,
		HideDockIcon:   value.HideDockIcon,
		RequestBodyLog: value.RequestBodyLog,
		LogFile:        value.LogFile, LogMaxTotalMB: value.LogMaxTotalMB,
		AdminAuthEnabled: value.AdminAuthEnabled, AdminUsername: strings.TrimSpace(value.AdminUsername), AdminPassword: password,
		BuildNativeNonstream: value.BuildNativeNonstream,
		NonstreamKeepalive:   value.NonstreamKeepalive, FakeStream: value.FakeStream, YouTubeLinks: value.YouTubeLinks,
		SearchModelAliases: value.SearchModelAliases, DiscoverHiddenModels: value.DiscoverHiddenModels, GoogleSearch: value.GoogleSearch, StripThoughtSignatures: value.StripThoughtSignatures,
		ContinuationRewrite: value.ContinuationRewrite, ContinuationPrompt: strings.TrimSpace(value.ContinuationPrompt),
		ReplyTool: value.ReplyTool, UserMessageRewrite: strings.ToLower(strings.TrimSpace(value.UserMessageRewrite)), ThinkingGuard: value.ThinkingGuard,
		FirstEventHedge: strings.ToLower(strings.TrimSpace(value.FirstEventHedge)), FirstEventHedgeModels: strings.TrimSpace(value.FirstEventHedgeModels), FirstEventTimeout: strings.ToLower(strings.TrimSpace(value.FirstEventTimeout)), AuthStates: value.AuthStates, ListenAddr: value.ListenAddr, PreviewListenAddr: value.PreviewListenAddr,
		ProxyAPIKey: value.APIKey, Proxy: value.Proxy, InitTimeout: initTimeout, RequestTimeout: requestTimeout,
		WarmWorkerLimit: value.WarmWorkerLimit, MaxActiveWorkers: value.MaxActiveWorkers,
		WarmStartupConcurrency: value.WarmStartupConcurrency,
		PerAccountConcurrency:  value.PerAccountConcurrency,
		RoutingStrategy:        value.RoutingStrategy,
		AccountOrder:           strings.TrimSpace(value.AccountOrder),
		MaxAccountSwitches:     value.MaxAccountSwitches,
		AccountSwitchInterval:  switchInterval,
		MaxQuotaSwitches:       maxQuotaSwitches,
		UpstreamChannels:       value.UpstreamChannels,
		ModelMapping:           strings.TrimSpace(value.ModelMapping),
		ModelChannels:          strings.TrimSpace(value.ModelChannels),
		ModelAccounts:          strings.TrimSpace(value.ModelAccounts),
		APIKeyAccounts:         strings.TrimSpace(value.APIKeyAccounts),
		WAABackend:             value.WAABackend,
		MediaOutput:            strings.ToLower(strings.TrimSpace(value.MediaOutput)),
		MediaBaseURL:           strings.TrimSpace(value.MediaBaseURL),
	}.KeepEnvironment(saved)
	if err := cfg.Validate(); err != nil {
		return api.RuntimeConfig{}, invalidConfigError(err)
	}
	if err := cfg.Save(admin.configPath); err != nil {
		return api.RuntimeConfig{}, err
	}
	admin.ApplyAccountSelection(cfg)
	admin.requests.log("service", "INFO", "服务配置已保存")
	models, _ := admin.CatalogModels(context.Background())
	admin.requests.publish(api.AdminEvent{Type: "models", Data: map[string]any{"models": models}})
	value = runtimeConfigDTO(cfg)
	value.ActiveProxy = admin.currentActiveProxy()
	value.ProxyPool = admin.proxyPoolStatus()
	return value, nil
}

// ApplyAccountSelection 让 cfg 中的账户选择策略、账户顺序与模型限定的通道和账户作用于之后的选号
func (admin *runtimeAdmin) ApplyAccountSelection(cfg config.Config) {
	admin.pool.SetRoutingStrategy(cfg.RoutingStrategy)
	admin.pool.SetAccountOrder(accountOrder(cfg.AccountOrder))
	admin.pool.SetModelChannels(modelChannels(cfg.ModelChannels))
	admin.pool.SetModelAccounts(modelAccounts(cfg.ModelAccounts))
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
	admin.requests.log(account.Label, "INFO", "冷却已手动清除", "scope", scope)
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
	// AccountsChanged 返回账户的租约或状态下一次变化时关闭的通道
	AccountsChanged() <-chan struct{}
	Cooldowns(context.Context) ([]api.AdminCooldown, error)
}

// AccountsChanged 返回账户池的租约或状态下一次变化时关闭的通道
func (admin *runtimeAdmin) AccountsChanged() <-chan struct{} {
	return admin.pool.Changed()
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
	changed := source.AccountsChanged()
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
		// sent 是本连接各类快照最近一次发出的内容，内容相同的快照跳过发送，状态快照不比较生成时刻
		sent := map[string][]byte{}
		send := func(event api.AdminEvent) bool {
			switch event.Type {
			case "status", "models", "accounts", "cooldowns":
				data := event.Data
				if status, ok := data.(api.AdminStatus); ok {
					status.Time = time.Time{}
					data = status
				}
				encoded, err := json.Marshal(data)
				if err == nil && bytes.Equal(encoded, sent[event.Type]) {
					return true
				}
				sent[event.Type] = encoded
			}
			select {
			case events <- event:
				return true
			case <-eventCtx.Done():
				return false
			}
		}
		// scheduleRefresh 在 adminRequestRefreshLatency 后推送一次快照，已排定时沿用
		scheduleRefresh := func() {
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
		// seen 是本连接各活动请求最近一次的状态与执行账户，只有输出时刻变化的请求事件不刷新快照
		seen := map[string][2]string{}
		scheduleRequestRefresh := func(event api.AdminEvent) {
			request := event.Data.(api.AdminRequest)
			current := [2]string{request.State, request.AccountID}
			if seen[request.ID] == current {
				return
			}
			if request.State == "queued" || request.State == "running" {
				seen[request.ID] = current
			} else {
				delete(seen, request.ID)
			}
			if request.State != "queued" {
				refreshAccounts = true
			}
			if request.State != "queued" && request.State != "running" {
				refreshCooldowns = true
			}
			scheduleRefresh()
		}
		for {
			if current := source.AccountsChanged(); current != changed {
				changed = current
				refreshAccounts = true
				scheduleRefresh()
			}
			select {
			case <-changed:
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
		HideDockIcon:   cfg.HideDockIcon,
		RequestBodyLog: cfg.RequestBodyLog,
		LogFile:        cfg.LogFile, LogMaxTotalMB: cfg.LogMaxTotalMB,
		AdminAuthEnabled: cfg.AdminAuthEnabled, AdminUsername: cfg.AdminUsername,
		AdminPasswordSet: cfg.AdminPassword != "", SavedAdminPassword: cfg.AdminPassword,
		BuildNativeNonstream: cfg.BuildNativeNonstream,
		NonstreamKeepalive:   cfg.NonstreamKeepalive, FakeStream: cfg.FakeStream, YouTubeLinks: cfg.YouTubeLinks,
		SearchModelAliases: cfg.SearchModelAliases, DiscoverHiddenModels: cfg.DiscoverHiddenModels, GoogleSearch: cfg.GoogleSearch, StripThoughtSignatures: cfg.StripThoughtSignatures,
		ContinuationRewrite: cfg.ContinuationRewrite, ContinuationPrompt: cfg.ContinuationPrompt,
		ReplyTool: cfg.ReplyTool, UserMessageRewrite: cfg.UserMessageRewrite, ThinkingGuard: cfg.ThinkingGuard,
		FirstEventHedge: cfg.FirstEventHedge, FirstEventHedgeModels: cfg.FirstEventHedgeModels, FirstEventTimeout: cfg.FirstEventTimeout, AuthStates: cfg.AuthStates, ListenAddr: cfg.ListenAddr, APIKey: cfg.ProxyAPIKey,
		ActiveListenAddr:  cfg.ListenAddr,
		PreviewListenAddr: cfg.PreviewListenAddr, ActivePreviewListenAddr: cfg.PreviewListenAddr,
		Proxy: cfg.Proxy, InitTimeout: cfg.InitTimeout.String(), RequestTimeout: cfg.RequestTimeout.String(),
		WarmWorkerLimit: cfg.WarmWorkerLimit, MaxActiveWorkers: cfg.MaxActiveWorkers,
		WarmStartupConcurrency: cfg.WarmStartupConcurrency,
		PerAccountConcurrency:  cfg.PerAccountConcurrency,
		RoutingStrategy:        cfg.RoutingStrategy,
		AccountOrder:           cfg.AccountOrder,
		MaxAccountSwitches:     cfg.MaxAccountSwitches,
		AccountSwitchInterval:  cfg.AccountSwitchInterval,
		MaxQuotaSwitches:       cfg.MaxQuotaSwitches,
		UpstreamChannels:       cfg.UpstreamChannels,
		ModelMapping:           cfg.ModelMapping,
		ModelChannels:          cfg.ModelChannels,
		ModelAccounts:          cfg.ModelAccounts,
		APIKeyAccounts:         cfg.APIKeyAccounts,
		WAABackend:             cfg.WAABackend,
		MediaOutput:            cfg.MediaOutput,
		MediaBaseURL:           cfg.MediaBaseURL,
		Environment:            append([]string{}, cfg.Environment...),
		Invalid:                invalidConfigDTO(cfg.Invalid),
	}
}

// invalidConfigDTO 把加载时无效的配置项转为管理接口的表示
func invalidConfigDTO(values []config.InvalidValue) []api.InvalidConfigValue {
	result := make([]api.InvalidConfigValue, 0, len(values))
	for _, value := range values {
		result = append(result, api.InvalidConfigValue{Key: value.Key, Value: value.Value, Reason: value.Reason})
	}
	return result
}

var _ generationAdmin = (*runtimeAdmin)(nil)
