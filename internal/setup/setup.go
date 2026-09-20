package setup

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
		return fmt.Errorf("value cannot be empty")
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
	proxy        string
	locale       string
	localeSet    bool
	timezone     string
}

// Run executes local Chrome import, storage state file import, or isolated login.
func Run(ctx context.Context, cfg config.Config, args []string) error {
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
		driver, err := defaultSetupLoginDriver(ctx, cfg)
		if err != nil {
			return err
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
		return fmt.Errorf("storage state cannot be used with AI Studio: %w", err)
	}

	label := defaultSetupLabel(options.storageState)
	if extension, exists, err := state.AuthExtension(); err != nil {
		return err
	} else if exists && strings.TrimSpace(extension.Source.Email) != "" {
		label = extension.Source.Email
	}

	account, publishLease, err := store.Create(setupAccountConfig(label, options), state)
	if err != nil {
		return err
	}

	if err := publishLease.Release(); err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "Account saved: %s\n", account.Config.Label)
	return nil
}

func importIsolatedLogin(
	ctx context.Context,
	store *aistudio.AccountStore,
	options setupOptions,
	driver aistudio.IsolatedLoginDriver,
) (resultErr error) {
	loginDirectory, err := os.MkdirTemp("", "aistudio2api-login-*")
	if err != nil {
		return fmt.Errorf("create isolated login directory: %w", err)
	}
	defer os.RemoveAll(loginDirectory)

	result, err := driver.Login(ctx, aistudio.IsolatedLoginRequest{
		AccountID: "setup",
		Directory: loginDirectory,
		Proxy:     options.proxy,
		Locale:    options.locale,
		Timezone:  options.timezone,
	})
	if err != nil {
		return err
	}

	if _, err := aistudio.NewSigner().Sign(result.StorageState); err != nil {
		return fmt.Errorf("storage state cannot be used with AI Studio: %w", err)
	}

	account, publishLease, err := store.Create(setupAccountConfig(result.Email, options), result.StorageState)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, publishLease.Release())
	}()

	if err := camoufoxnative.PersistAccountFingerprint(loginDirectory, account.Directory); err != nil {
		return errors.Join(err, store.Delete(account))
	}

	fmt.Fprintf(os.Stdout, "Account saved: %s\n", account.Config.Label)
	return nil
}

func importChromeAccounts(
	ctx context.Context,
	cfg config.Config,
	store *aistudio.AccountStore,
	options setupOptions,
	input io.Reader,
	output io.Writer,
) error {
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
		ChromeRoot: root,
		Proxy:      options.proxy,
		Profiles:   options.profiles,
		Emails:     options.emails,
	})
	if err != nil {
		return err
	}

	modelCounts := make([]int, len(results))
	for index := range results {
		verifyContext, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
		verification, verifyErr := chromeauth.Verify(verifyContext, &results[index].State, options.proxy)
		cancel()

		if verifyErr != nil {
			return fmt.Errorf("verify %s: %w", results[index].Email, verifyErr)
		}

		modelCounts[index] = verification.ModelCount
	}

	for index, result := range results {
		accountOptions := options
		if !options.localeSet && result.Locale != "" {
			accountOptions.locale = result.Locale
		}

		_, publishLease, err := store.Create(setupAccountConfig(result.Email, accountOptions), result.State)
		if err != nil {
			return err
		}

		if err := publishLease.Release(); err != nil {
			return err
		}

		fmt.Fprintf(output, "Imported: %s (%s), %d models\n", result.Email, result.Profile, modelCounts[index])
	}

	return nil
}

func promptChromeProfiles(accounts []chromeauth.Account, input io.Reader, output io.Writer) (setupStrings, error) {
	available := make([]chromeauth.Account, 0, len(accounts))
	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)

	fmt.Fprintln(table, "No.\tStatus\tProfile\tDisplay Name\tEmail")
	for _, account := range accounts {
		index := "-"
		status := "Not Importable"
		if account.Importable {
			available = append(available, account)
			index = strconv.Itoa(len(available))
			status = "Importable"
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", index, status, account.Profile, account.DisplayName, account.Email)
	}

	if err := table.Flush(); err != nil {
		return nil, fmt.Errorf("write Chrome account list: %w", err)
	}

	if len(available) == 0 {
		return nil, fmt.Errorf("no importable accounts found in local Chrome")
	}

	fmt.Fprint(output, "Enter comma-separated numbers: ")
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read account selection: %w", err)
	}

	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("no accounts selected")
	}

	selected := make(setupStrings, 0)
	seen := make(map[int]struct{})

	for _, raw := range strings.Split(line, ",") {
		index, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || index < 1 || index > len(available) {
			return nil, fmt.Errorf("invalid account number %q", strings.TrimSpace(raw))
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
		fmt.Fprintln(flags.Output(), "Chrome import:  aistudio2api setup")
		fmt.Fprintln(flags.Output(), "File import:    aistudio2api setup --storage-state <file>")
		fmt.Fprintln(flags.Output(), "Isolated login: aistudio2api setup --login")
		flags.PrintDefaults()
	}

	var profiles setupStrings
	var emails setupStrings

	flags.Var(&profiles, "profile", "Chrome profile to import, can be repeated")
	flags.Var(&emails, "email", "Google email to import, can be repeated")
	storageState := flags.String("storage-state", "", "Playwright storage state file")
	login := flags.Bool("login", false, "Log in using isolated Camoufox browser")
	chromeRoot := flags.String("chrome-root", "", "Chrome User Data directory")
	proxy := flags.String("proxy", cfg.Proxy, "Fixed HTTP, HTTPS, or SOCKS5 proxy for accounts")
	locale := flags.String("locale", aistudio.DefaultAccountLocale(), "Account locale")
	timezone := flags.String("timezone", aistudio.DefaultAccountTimezone(), "Account timezone")

	if err := flags.Parse(args); err != nil {
		return setupOptions{}, err
	}

	if flags.NArg() != 0 {
		return setupOptions{}, fmt.Errorf("unknown argument %q", flags.Arg(0))
	}

	options := setupOptions{
		storageState: strings.TrimSpace(*storageState),
		login:        *login,
		chromeRoot:   strings.TrimSpace(*chromeRoot),
		profiles:     profiles,
		emails:       emails,
		proxy:        strings.TrimSpace(*proxy),
		locale:       strings.TrimSpace(*locale),
		timezone:     strings.TrimSpace(*timezone),
	}

	flags.Visit(func(value *flag.Flag) {
		if value.Name == "locale" {
			options.localeSet = true
		}
	})

	chromeSelection := options.chromeRoot != "" || len(options.profiles) != 0 || len(options.emails) != 0
	if options.storageState != "" && (options.login || chromeSelection) {
		return setupOptions{}, fmt.Errorf("--storage-state cannot be used together with Chrome import or --login")
	}

	if options.login && chromeSelection {
		return setupOptions{}, fmt.Errorf("--login cannot be used together with Chrome import flags")
	}

	if options.locale == "" || options.timezone == "" {
		return setupOptions{}, fmt.Errorf("locale and timezone cannot be empty")
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

func defaultSetupLoginDriver(ctx context.Context, cfg config.Config) (aistudio.IsolatedLoginDriver, error) {
	camoufoxPath, err := camoufoxnative.FindExecutable(ctx)
	if err != nil {
		return nil, err
	}

	return aistudio.NewNativeLoginDriver(camoufoxPath, cfg.RequestTimeout)
}

func validateSetupRoot(root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return fmt.Errorf("AISTUDIO_AUTH_STATES cannot be empty")
	}

	if strings.Contains(root, ",") {
		return fmt.Errorf("setup requires AISTUDIO_AUTH_STATES to point to a single account directory")
	}

	info, err := os.Stat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read account directory: %w", err)
	}

	if !info.IsDir() {
		return fmt.Errorf("setup requires AISTUDIO_AUTH_STATES to point to an account directory")
	}

	return nil
}
