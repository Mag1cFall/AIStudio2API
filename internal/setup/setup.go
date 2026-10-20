package setup

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/Mag1cFall/AIStudio2API/internal/chromeauth"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

type setupStrings []string

func (values *setupStrings) String() string {
	return strings.Join(*values, ",")
}

func (values *setupStrings) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("参数值不能为空")
	}
	*values = append(*values, value)
	return nil
}

type setupOptions struct {
	storageState string
	cookies      string
	cdp          string
	login        bool
	chromeRoot   string
	profiles     setupStrings
	emails       setupStrings
	proxy        string
	egress       string
	locale       string
	localeSet    bool
	timezone     string
	timezoneSet  bool
	remote       string
	token        string
}

// accountTarget 保存 setup 得到的账户并返回账户名称
type accountTarget interface {
	Save(ctx context.Context, accountConfig aistudio.AccountConfig, state aistudio.StorageState, fingerprintDirectory string) (string, error)
}

// Run 执行本机 Chrome 导入、文件导入或隔离登录，结果写入本机账户目录或远程实例
func Run(ctx context.Context, cfg config.Config, args []string) error {
	options, err := parseSetupFlags(args, cfg)
	if err != nil {
		return err
	}
	input := bufio.NewReader(os.Stdin)
	var target accountTarget
	if options.remote != "" {
		token := options.token
		if token == "" {
			if token, err = readPairingToken(input, os.Stdout); err != nil {
				return err
			}
		}
		target = remoteTarget{
			client: &http.Client{Timeout: cfg.RequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}},
			origin: options.remote, token: token,
		}
	} else {
		if err := validateSetupRoot(cfg.AuthStates); err != nil {
			return err
		}
		target = localTarget{store: aistudio.NewAccountStore(cfg.AuthStates)}
	}
	if options.storageState != "" {
		return importStorageState(ctx, target, options)
	}
	if options.cookies != "" || options.cdp != "" {
		return importCookies(ctx, target, options)
	}
	if options.login {
		if !camoufoxnative.HasDisplay() {
			return camoufoxnative.ErrNoDisplay
		}
		driver, err := defaultSetupLoginDriver(ctx, cfg, options.egress)
		if err != nil {
			return err
		}
		return importIsolatedLogin(ctx, target, options, driver)
	}
	return importChromeAccounts(ctx, cfg, target, options, input, os.Stdout)
}

// localTarget 将账户写入本机账户目录
type localTarget struct {
	store *aistudio.AccountStore
}

// Save 创建本机账户目录并保存隔离登录指纹
func (target localTarget) Save(
	_ context.Context,
	accountConfig aistudio.AccountConfig,
	state aistudio.StorageState,
	fingerprintDirectory string,
) (string, error) {
	account, publishLease, err := target.store.Create(accountConfig, state)
	if err != nil {
		return "", err
	}
	if fingerprintDirectory != "" {
		if err := camoufoxnative.PersistAccountFingerprint(fingerprintDirectory, account.Directory); err != nil {
			return "", errors.Join(err, target.store.Delete(account), publishLease.Release())
		}
	}
	return account.Config.Label, publishLease.Release()
}

// remoteTarget 凭配对令牌将账户上传到远程实例
type remoteTarget struct {
	client *http.Client
	origin string
	token  string
}

// Save 上传认证状态与隔离登录指纹
func (target remoteTarget) Save(
	ctx context.Context,
	accountConfig aistudio.AccountConfig,
	state aistudio.StorageState,
	fingerprintDirectory string,
) (string, error) {
	input := api.AccountStateInput{
		Label: accountConfig.Label, Locale: accountConfig.Locale, Timezone: accountConfig.Timezone, StorageState: state,
	}
	if fingerprintDirectory != "" {
		data, err := os.ReadFile(filepath.Join(fingerprintDirectory, "camoufox-fingerprint.json"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("读取 Camoufox 指纹: %w", err)
		}
		input.Fingerprint = data
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("编码账户: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.origin+"/api/pairing/accounts", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+target.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := target.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("连接远程实例: %w", err)
	}
	defer response.Body.Close()
	var result struct {
		Account api.AdminAccount `json:"account"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result)
	if response.StatusCode != http.StatusCreated {
		if result.Error.Message != "" {
			return "", fmt.Errorf("远程实例导入失败: HTTP %d: %s", response.StatusCode, result.Error.Message)
		}
		return "", fmt.Errorf("远程实例导入失败: HTTP %d", response.StatusCode)
	}
	if decodeErr != nil {
		return "", fmt.Errorf("解析远程实例响应: %w", decodeErr)
	}
	return result.Account.Label, nil
}

// readPairingToken 从标准输入读取配对令牌，令牌不进入命令历史
func readPairingToken(input *bufio.Reader, output io.Writer) (string, error) {
	fmt.Fprint(output, "请粘贴远程实例账户页生成的配对令牌: ")
	line, err := input.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("读取配对令牌: %w", err)
	}
	token := strings.TrimSpace(line)
	if token == "" {
		return "", fmt.Errorf("配对令牌不能为空")
	}
	return token, nil
}

// remoteOrigin 校验远程实例地址只含协议与主机，非本机地址必须使用 HTTPS
func remoteOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("--remote 必须是远程实例的 origin，例如 https://example.com")
	}
	ip := net.ParseIP(parsed.Hostname())
	loopback := strings.EqualFold(parsed.Hostname(), "localhost") || ip != nil && ip.IsLoopback()
	if parsed.Scheme != "https" && (parsed.Scheme != "http" || !loopback) {
		return "", fmt.Errorf("--remote 必须使用 HTTPS；只有本机地址可以使用 HTTP")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func importStorageState(ctx context.Context, target accountTarget, options setupOptions) error {
	state, err := aistudio.LoadStorageState(options.storageState)
	if err != nil {
		return err
	}
	if _, err := aistudio.NewSigner().Sign(state); err != nil {
		return fmt.Errorf("认证状态无法用于 AI Studio: %w", err)
	}
	label := defaultSetupLabel(options.storageState)
	if extension, exists, err := state.AuthExtension(); err != nil {
		return err
	} else if exists && strings.TrimSpace(extension.Source.Email) != "" {
		label = extension.Source.Email
	}
	saved, err := target.Save(ctx, setupAccountConfig(label, options), state, "")
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "账户已保存: %s\n", saved)
	return nil
}

// importCookies 把 --cookie、--cookie-file 或 --cdp 浏览器中的 Cookie 导入为账户，未给 --email 时以 Cookie 登录的账户邮箱为账户；--cdp 未给 --locale、--timezone 时沿用该浏览器页面的语言与时区
func importCookies(ctx context.Context, target accountTarget, options setupOptions) error {
	if options.cdp != "" {
		browser, err := chromeauth.ReadCDPBrowser(ctx, options.cdp)
		if err != nil {
			return err
		}
		options.cookies = browser.Cookies
		if !options.localeSet && browser.Locale != "" {
			options.locale = browser.Locale
		}
		if !options.timezoneSet && browser.Timezone != "" {
			options.timezone = browser.Timezone
		}
	}
	state, err := aistudio.ParseCookieText(options.cookies)
	if err != nil {
		return err
	}
	if _, err := aistudio.NewSigner().Sign(state); err != nil {
		return fmt.Errorf("认证状态无法用于 AI Studio: %w", err)
	}
	var email string
	if len(options.emails) == 1 {
		email = options.emails[0]
	} else {
		verification, err := chromeauth.Verify(ctx, &state, options.egress)
		if err != nil {
			return fmt.Errorf("识别 Cookie 登录的账户: %w", err)
		}
		email = strings.ToLower(verification.Email)
	}
	saved, err := target.Save(ctx, setupAccountConfig(email, options), state, "")
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "账户已保存: %s\n", saved)
	return nil
}

func importIsolatedLogin(
	ctx context.Context,
	target accountTarget,
	options setupOptions,
	driver aistudio.IsolatedLoginDriver,
) error {
	loginDirectory, err := os.MkdirTemp("", "aistudio2api-login-*")
	if err != nil {
		return fmt.Errorf("创建隔离登录目录: %w", err)
	}
	defer os.RemoveAll(loginDirectory)
	result, err := driver.Login(ctx, aistudio.IsolatedLoginRequest{
		AccountID: "setup", Directory: loginDirectory, Proxy: options.egress,
		Locale: options.locale, Timezone: options.timezone,
	})
	if err != nil {
		return err
	}
	if _, err := aistudio.NewSigner().Sign(result.StorageState); err != nil {
		return fmt.Errorf("认证状态无法用于 AI Studio: %w", err)
	}
	saved, err := target.Save(ctx, setupAccountConfig(result.Email, options), result.StorageState, loginDirectory)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "账户已保存: %s\n", saved)
	if result.DriveError != "" {
		fmt.Fprintf(os.Stdout, "Drive 授权失败: %s\n", result.DriveError)
	}
	if result.CloseError != "" {
		fmt.Fprintf(os.Stdout, "关闭 Camoufox 失败: %s\n", result.CloseError)
	}
	return nil
}

func importChromeAccounts(ctx context.Context, cfg config.Config, target accountTarget, options setupOptions, input io.Reader, output io.Writer) error {
	root := options.chromeRoot
	if root == "" {
		var err error
		root, err = chromeauth.DefaultChromeRoot()
		if err != nil {
			return err
		}
	}
	var accountIDs []string
	if len(options.profiles) == 0 && len(options.emails) == 0 {
		accounts, err := chromeauth.Discover(root)
		if err != nil {
			return err
		}
		accountIDs, err = promptChromeAccounts(accounts, input, output)
		if err != nil {
			return err
		}
	}
	results, err := chromeauth.Import(ctx, chromeauth.ImportOptions{
		ChromeRoot: root, Proxy: options.egress, Profiles: options.profiles, Emails: options.emails, AccountIDs: accountIDs,
	})
	if err != nil {
		return err
	}
	modelCounts := make([]int, len(results))
	for index := range results {
		verifyContext, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
		verification, verifyErr := chromeauth.Verify(verifyContext, &results[index].State, options.egress)
		cancel()
		if verifyErr != nil {
			return fmt.Errorf("验证 %s: %w", results[index].Email, verifyErr)
		}
		modelCounts[index] = verification.ModelCount
	}
	for index, result := range results {
		accountOptions := options
		if !options.localeSet && result.Locale != "" {
			accountOptions.locale = result.Locale
		}
		if _, err := target.Save(ctx, setupAccountConfig(result.Email, accountOptions), result.State, ""); err != nil {
			return err
		}
		fmt.Fprintf(output, "已导入: %s (%s)，%d 个模型\n", result.Email, result.Profile, modelCounts[index])
	}
	return nil
}

func promptChromeAccounts(accounts []chromeauth.Account, input io.Reader, output io.Writer) ([]string, error) {
	available := make([]chromeauth.Account, 0, len(accounts))
	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "编号\t状态\tProfile\t显示名\t邮箱")
	for _, account := range accounts {
		index := "-"
		status := "不可导入"
		if account.Importable {
			available = append(available, account)
			index = strconv.Itoa(len(available))
			status = "可导入"
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", index, status, account.Profile, account.DisplayName, account.Email)
	}
	if err := table.Flush(); err != nil {
		return nil, fmt.Errorf("输出 Chrome 账号列表: %w", err)
	}
	if len(available) == 0 {
		return nil, fmt.Errorf("本机 Chrome 没有可导入账号")
	}
	fmt.Fprint(output, "请输入逗号分隔的编号: ")
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("读取账号选择: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("未选择账号")
	}
	selected := make([]string, 0)
	seen := make(map[int]struct{})
	for _, raw := range strings.Split(line, ",") {
		index, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || index < 1 || index > len(available) {
			return nil, fmt.Errorf("账号编号 %q 无效", strings.TrimSpace(raw))
		}
		if _, exists := seen[index]; exists {
			continue
		}
		seen[index] = struct{}{}
		selected = append(selected, available[index-1].ID)
	}
	return selected, nil
}

func parseSetupFlags(args []string, cfg config.Config) (setupOptions, error) {
	flags := flag.NewFlagSet("aistudio2api setup", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Chrome 导入: aistudio2api setup")
		fmt.Fprintln(flags.Output(), "文件导入: aistudio2api setup --storage-state <file>")
		fmt.Fprintln(flags.Output(), "Cookie 导入: aistudio2api setup --cookie <Cookie 请求头>、--cookie-file <file> 或 --cdp <调试地址>")
		fmt.Fprintln(flags.Output(), "隔离登录: aistudio2api setup --login")
		fmt.Fprintln(flags.Output(), "导入远程实例: 以上命令加 --remote <origin>")
		flags.PrintDefaults()
	}
	var profiles setupStrings
	var emails setupStrings
	flags.Var(&profiles, "profile", "要导入的 Chrome Profile，可重复")
	flags.Var(&emails, "email", "要导入的 Google 邮箱，可重复；Cookie 导入时指定账户邮箱，省略时按 Cookie 识别")
	storageState := flags.String("storage-state", "", "Playwright storage state 文件")
	cookieHeader := flags.String("cookie", "", "一行 Cookie 请求头")
	cookieFile := flags.String("cookie-file", "", "浏览器导出的 Cookie JSON 或 Netscape cookies.txt 文件")
	cdp := flags.String("cdp", "", "指纹浏览器或 Chromium 的远程调试地址，读取其中的 Cookie")
	login := flags.Bool("login", false, "使用隔离 Camoufox 登录")
	chromeRoot := flags.String("chrome-root", "", "Chrome User Data 目录")
	proxy := flags.String("proxy", "", "账户固定 HTTP、HTTPS 或 SOCKS5 代理")
	locale := flags.String("locale", aistudio.DefaultAccountLocale(), "账户语言")
	timezone := flags.String("timezone", aistudio.DefaultAccountTimezone(), "账户时区")
	remote := flags.String("remote", "", "远程实例 origin，账户上传到该实例")
	token := flags.String("token", "", "远程实例账户页生成的配对令牌，省略时从标准输入读取")
	if err := flags.Parse(args); err != nil {
		return setupOptions{}, err
	}
	if flags.NArg() != 0 {
		return setupOptions{}, fmt.Errorf("未知参数 %q", flags.Arg(0))
	}
	options := setupOptions{
		storageState: strings.TrimSpace(*storageState), login: *login,
		chromeRoot: strings.TrimSpace(*chromeRoot), profiles: profiles, emails: emails,
		proxy:  strings.TrimSpace(*proxy),
		locale: strings.TrimSpace(*locale), timezone: strings.TrimSpace(*timezone),
		token: strings.TrimSpace(*token),
	}
	options.egress = options.proxy
	if proxies := config.ProxyList(cfg.Proxy); options.egress == "" && len(proxies) > 0 {
		options.egress = proxies[0]
	}
	if strings.TrimSpace(*remote) != "" {
		origin, err := remoteOrigin(*remote)
		if err != nil {
			return setupOptions{}, err
		}
		options.remote = origin
	} else if options.token != "" {
		return setupOptions{}, fmt.Errorf("--token 需要与 --remote 同时使用")
	}
	options.cookies = strings.TrimSpace(*cookieHeader)
	if path := strings.TrimSpace(*cookieFile); path != "" {
		if options.cookies != "" {
			return setupOptions{}, fmt.Errorf("--cookie 与 --cookie-file 只能选一个")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return setupOptions{}, fmt.Errorf("读取 Cookie 文件: %w", err)
		}
		if options.cookies = strings.TrimSpace(string(data)); options.cookies == "" {
			return setupOptions{}, fmt.Errorf("Cookie 文件为空")
		}
	}
	options.cdp = strings.TrimSpace(*cdp)
	if options.cdp != "" && options.cookies != "" {
		return setupOptions{}, fmt.Errorf("--cdp 不能与 --cookie、--cookie-file 同时使用")
	}
	if options.cookies != "" || options.cdp != "" {
		if options.storageState != "" || options.login || options.chromeRoot != "" || len(options.profiles) != 0 {
			return setupOptions{}, fmt.Errorf("--cookie、--cookie-file、--cdp 不能与 --storage-state、--login 或 Chrome 导入参数同时使用")
		}
		if len(options.emails) > 1 {
			return setupOptions{}, fmt.Errorf("Cookie 导入最多用 --email 指定一个账户邮箱")
		}
	}
	flags.Visit(func(value *flag.Flag) {
		switch value.Name {
		case "locale":
			options.localeSet = true
		case "timezone":
			options.timezoneSet = true
		}
	})
	chromeSelection := options.cookies == "" && options.cdp == "" && (options.chromeRoot != "" || len(options.profiles) != 0 || len(options.emails) != 0)
	if options.storageState != "" && (options.login || chromeSelection) {
		return setupOptions{}, fmt.Errorf("--storage-state 不能与 Chrome 导入或 --login 同时使用")
	}
	if options.login && chromeSelection {
		return setupOptions{}, fmt.Errorf("--login 不能与 Chrome 导入参数同时使用")
	}
	if options.locale == "" || options.timezone == "" {
		return setupOptions{}, fmt.Errorf("locale 和 timezone 不能为空")
	}
	if err := config.ValidateProxy(options.proxy); err != nil {
		return setupOptions{}, err
	}
	return options, nil
}

func setupAccountConfig(label string, options setupOptions) aistudio.AccountConfig {
	accountConfig := aistudio.DefaultAccountConfig(label)
	accountConfig.Proxy = options.proxy
	accountConfig.Locale = options.locale
	accountConfig.Timezone = options.timezone
	return accountConfig
}

func defaultSetupLabel(storageState string) string {
	parent := filepath.Base(filepath.Clean(filepath.Dir(storageState)))
	if parent == "" || parent == "." || parent == string(filepath.Separator) {
		return "Default"
	}
	return parent
}

func defaultSetupLoginDriver(ctx context.Context, cfg config.Config, egress string) (aistudio.IsolatedLoginDriver, error) {
	camoufoxPath, err := camoufoxnative.FindExecutable(ctx, egress)
	if err != nil {
		return nil, err
	}
	return aistudio.NewNativeLoginDriver(camoufoxPath, cfg.RequestTimeout)
}

func validateSetupRoot(root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return fmt.Errorf("AISTUDIO_AUTH_STATES 不能为空")
	}
	if strings.Contains(root, ",") {
		return fmt.Errorf("setup 需要 AISTUDIO_AUTH_STATES 指向单个账户目录")
	}
	info, err := os.Stat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取账户目录: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("setup 需要 AISTUDIO_AUTH_STATES 指向账户目录")
	}
	return nil
}
