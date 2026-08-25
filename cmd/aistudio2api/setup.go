package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
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
	login        bool
	chromeRoot   string
	profiles     setupStrings
	emails       setupStrings
	label        string
	proxy        string
	locale       string
	localeSet    bool
	timezone     string
}

// runSetup 执行本机 Chrome 导入、文件导入或隔离登录
func runSetup(ctx context.Context, cfg config.Config, args []string) error {
	return runSetupCommand(ctx, cfg, args, nil)
}

func runSetupCommand(ctx context.Context, cfg config.Config, args []string, driver aistudio.IsolatedLoginDriver) error {
	options, err := parseSetupFlags(args, cfg)
	if err != nil {
		return err
	}
	if err := validateSetupRoot(cfg.AuthStates); err != nil {
		return err
	}
	store := aistudio.NewAccountStore(cfg.AuthStates)
	if options.storageState != "" {
		return importStorageState(store, options)
	}
	if options.login {
		if driver == nil {
			driver, err = defaultSetupLoginDriver(cfg)
			if err != nil {
				return err
			}
		}
		return importIsolatedLogin(ctx, store, options, driver)
	}
	return importChromeAccounts(ctx, cfg, store, options, os.Stdin, os.Stdout)
}

func importStorageState(store *aistudio.AccountStore, options setupOptions) error {
	state, err := aistudio.LoadStorageState(options.storageState)
	if err != nil {
		return err
	}
	if _, err := aistudio.NewSigner().Sign(state); err != nil {
		return fmt.Errorf("认证状态无法用于 AI Studio: %w", err)
	}
	label := options.label
	if label == "" {
		label = defaultSetupLabel(options.storageState)
	}
	account, err := store.Create(setupAccountConfig(label, options), state)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "账户已保存: %s (%s)\n", account.ID, account.Config.Label)
	return nil
}

func importIsolatedLogin(ctx context.Context, store *aistudio.AccountStore, options setupOptions, driver aistudio.IsolatedLoginDriver) error {
	loginDirectory, err := os.MkdirTemp("", "aistudio2api-login-*")
	if err != nil {
		return fmt.Errorf("创建隔离登录目录: %w", err)
	}
	defer os.RemoveAll(loginDirectory)
	result, err := driver.Login(ctx, aistudio.IsolatedLoginRequest{
		AccountID: "setup", Directory: loginDirectory, Proxy: options.proxy,
		Locale: options.locale, Timezone: options.timezone,
	})
	if err != nil {
		return err
	}
	if err := result.StorageState.SetAuthExtension(aistudio.AuthExtension{
		Source: aistudio.AuthSource{Browser: "camoufox"},
	}); err != nil {
		return err
	}
	if _, err := aistudio.NewSigner().Sign(result.StorageState); err != nil {
		return fmt.Errorf("认证状态无法用于 AI Studio: %w", err)
	}
	label := options.label
	if label == "" {
		label = "Default"
	}
	account, err := store.Create(setupAccountConfig(label, options), result.StorageState)
	if err != nil {
		return err
	}
	if err := camoufoxnative.PersistAccountFingerprint(loginDirectory, account.Directory); err != nil {
		return errors.Join(err, store.Delete(account))
	}
	fmt.Fprintf(os.Stdout, "账户已保存: %s (%s)\n", account.ID, account.Config.Label)
	return nil
}

func importChromeAccounts(ctx context.Context, cfg config.Config, store *aistudio.AccountStore, options setupOptions, input io.Reader, output io.Writer) error {
	root := options.chromeRoot
	if root == "" {
		var err error
		root, err = chromeauth.DefaultChromeRoot()
		if err != nil {
			return err
		}
	}
	if len(options.profiles) == 0 && len(options.emails) == 0 {
		accounts, err := chromeauth.Discover(root)
		if err != nil {
			return err
		}
		options.profiles, err = promptChromeProfiles(accounts, input, output)
		if err != nil {
			return err
		}
	}
	results, err := chromeauth.Import(ctx, chromeauth.ImportOptions{
		ChromeRoot: root, Proxy: options.proxy, Profiles: options.profiles, Emails: options.emails,
	})
	if err != nil {
		return err
	}
	if options.label != "" && len(results) != 1 {
		return fmt.Errorf("--label 只能用于单个 Chrome 账号")
	}
	modelCounts := make([]int, len(results))
	for index := range results {
		verifyContext, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
		verification, verifyErr := chromeauth.Verify(verifyContext, &results[index].State, options.proxy)
		cancel()
		if verifyErr != nil {
			return fmt.Errorf("验证 %s: %w", results[index].Email, verifyErr)
		}
		modelCounts[index] = verification.ModelCount
	}
	for index, result := range results {
		label := result.Email
		if options.label != "" {
			label = options.label
		}
		accountOptions := options
		if !options.localeSet && result.Locale != "" {
			accountOptions.locale = result.Locale
		}
		account, err := store.Create(setupAccountConfig(label, accountOptions), result.State)
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "已导入: %s (%s)，%d 个模型，账户 %s\n", result.Email, result.Profile, modelCounts[index], account.ID)
	}
	return nil
}

func promptChromeProfiles(accounts []chromeauth.Account, input io.Reader, output io.Writer) (setupStrings, error) {
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
	selected := make(setupStrings, 0)
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
		selected = append(selected, available[index-1].Profile)
	}
	return selected, nil
}

func parseSetupFlags(args []string, cfg config.Config) (setupOptions, error) {
	flags := flag.NewFlagSet("aistudio2api setup", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Chrome 导入: aistudio2api setup")
		fmt.Fprintln(flags.Output(), "文件导入: aistudio2api setup --storage-state <file>")
		fmt.Fprintln(flags.Output(), "隔离登录: aistudio2api setup --login")
		flags.PrintDefaults()
	}
	var profiles setupStrings
	var emails setupStrings
	flags.Var(&profiles, "profile", "要导入的 Chrome Profile，可重复")
	flags.Var(&emails, "email", "要导入的 Google 邮箱，可重复")
	storageState := flags.String("storage-state", "", "Playwright storage state 文件")
	login := flags.Bool("login", false, "使用隔离 Camoufox 登录")
	chromeRoot := flags.String("chrome-root", "", "Chrome User Data 目录")
	label := flags.String("label", "", "账户显示名称")
	proxy := flags.String("proxy", cfg.Proxy, "账户固定 HTTP、HTTPS 或 SOCKS5 代理")
	locale := flags.String("locale", "en-US", "账户语言")
	timezone := flags.String("timezone", "UTC", "账户时区")
	if err := flags.Parse(args); err != nil {
		return setupOptions{}, err
	}
	if flags.NArg() != 0 {
		return setupOptions{}, fmt.Errorf("未知参数 %q", flags.Arg(0))
	}
	options := setupOptions{
		storageState: strings.TrimSpace(*storageState), login: *login,
		chromeRoot: strings.TrimSpace(*chromeRoot), profiles: profiles, emails: emails,
		label: strings.TrimSpace(*label), proxy: strings.TrimSpace(*proxy),
		locale: strings.TrimSpace(*locale), timezone: strings.TrimSpace(*timezone),
	}
	flags.Visit(func(value *flag.Flag) {
		if value.Name == "locale" {
			options.localeSet = true
		}
	})
	chromeSelection := options.chromeRoot != "" || len(options.profiles) != 0 || len(options.emails) != 0
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

func defaultSetupLoginDriver(cfg config.Config) (aistudio.IsolatedLoginDriver, error) {
	camoufoxPath, err := findCamoufoxExecutable()
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
