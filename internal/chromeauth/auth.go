package chromeauth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

const (
	maxCookieAge = 400 * 24 * time.Hour
	userAgent    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"

	BrowserChrome = "chrome"
	BrowserBrave  = "brave"
	BrowserEdge   = "edge"

	ReasonBrowserRunning = "browser_running"
)

// Account 描述本机浏览器中可发现的 Google 账号
type Account struct {
	ID          string `json:"id"`
	Browser     string `json:"browser,omitempty"`
	GaiaID      string `json:"-"`
	Profile     string `json:"profile"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Locale      string `json:"locale"`
	AuthUser    int    `json:"auth_user,omitempty"`
	Importable  bool   `json:"importable"`
	Reason      string `json:"reason,omitempty"`
	SourceType  string `json:"source_type,omitempty"`
}

// ImportFailure 描述批量导入时单个账号失败的原因
type ImportFailure struct {
	Target string `json:"target"`
	Error  string `json:"error"`
}

// BrowserImportResult 包含批量导入的结果与失败详情
type BrowserImportResult struct {
	Results  []ImportResult  `json:"results"`
	Failures []ImportFailure `json:"failures,omitempty"`
}

// ImportOptions 保存浏览器批量导入参数
type ImportOptions struct {
	AccountIDs []string
	Browser    string
	ChromeRoot string
	Proxy      string
	Profiles   []string
	Emails     []string
}

// ImportResult 返回一个已验证前的账号状态
type ImportResult struct {
	ID          string
	Browser     string
	Profile     string
	DisplayName string
	Email       string
	Locale      string
	CookieCount int
	State       aistudio.StorageState
}

// DefaultChromeRoot 返回稳定版 Chrome User Data 目录
func DefaultChromeRoot() (string, error) {
	return DefaultBrowserRoot(BrowserChrome)
}

// DefaultBrowserRoot 返回指定浏览器的默认根目录
func DefaultBrowserRoot(browser string) (string, error) {
	return defaultBrowserRoot(browser)
}

// Discover 只读列出本机 Chrome Google 账号
func Discover(chromeRoot string) ([]Account, error) {
	return DiscoverBrowser(BrowserChrome, chromeRoot)
}

// DiscoverBrowser 只读列出指定浏览器中的 Google 账号
func DiscoverBrowser(browser string, root string) ([]Account, error) {
	if browser == "" {
		browser = BrowserChrome
	}
	if root == "" {
		var err error
		root, err = DefaultBrowserRoot(browser)
		if err != nil {
			return nil, err
		}
	}
	return discoverPlatformBrowser(browser, root)
}

// ReadBrowserCookies 读取指定浏览器 Profile 中的 Google Cookie
func ReadBrowserCookies(browser string, profile string) ([]aistudio.StateCookie, error) {
	if browser == "" {
		browser = BrowserChrome
	}
	root, err := DefaultBrowserRoot(browser)
	if err != nil {
		return nil, err
	}
	masterKey, err := retrieveMasterKey(browser, root)
	if err != nil {
		return nil, err
	}
	return readChromiumCookies(browser, root, profile, masterKey)
}

// Import 用本机 Chrome 的 OAuth 续签材料生成 Playwright storage state
func Import(ctx context.Context, options ImportOptions) ([]ImportResult, error) {
	res, err := ImportWithDetails(ctx, options)
	return res.Results, err
}

// ImportWithDetails 批量导入指定浏览器的账号，具备单账号容错
func ImportWithDetails(ctx context.Context, options ImportOptions) (BrowserImportResult, error) {
	if options.Browser == "" {
		options.Browser = BrowserChrome
	}
	if err := ensurePlatformImport(); err != nil {
		return BrowserImportResult{}, err
	}
	proxyURL, err := validateProxy(options.Proxy)
	if err != nil {
		return BrowserImportResult{}, err
	}
	root := options.ChromeRoot
	if root == "" {
		var err error
		root, err = DefaultBrowserRoot(options.Browser)
		if err != nil {
			return BrowserImportResult{}, err
		}
		options.ChromeRoot = root
	}
	accounts, err := DiscoverBrowser(options.Browser, root)
	if err != nil {
		return BrowserImportResult{}, err
	}
	selected, err := selectAccounts(accounts, options.Profiles, options.Emails, options.AccountIDs)
	if err != nil {
		return BrowserImportResult{}, err
	}
	masterKey, err := retrieveMasterKey(options.Browser, options.ChromeRoot)
	if err != nil {
		return BrowserImportResult{}, err
	}
	if len(masterKey) != 32 {
		return BrowserImportResult{}, fmt.Errorf("%s 主密钥长度异常", options.Browser)
	}

	results := make([]ImportResult, 0, len(selected))
	var failures []ImportFailure
	for _, account := range selected {
		targetLabel := account.Email
		if targetLabel == "" {
			targetLabel = account.Profile
		}
		result, err := importAccount(ctx, options.Browser, options.ChromeRoot, proxyURL, account, masterKey)
		if err != nil {
			failures = append(failures, ImportFailure{Target: targetLabel, Error: err.Error()})
			continue
		}
		results = append(results, result)
	}

	if len(results) == 0 && len(failures) > 0 {
		return BrowserImportResult{Failures: failures}, fmt.Errorf("所有所选账号均导入失败: %s", failures[0].Error)
	}
	return BrowserImportResult{Results: results, Failures: failures}, nil
}

// Refresh 使用保存的 OAuth 续签材料重新签发 Google Cookie
func Refresh(ctx context.Context, source aistudio.AuthSource, material aistudio.ChromeOAuthMaterial, proxy string) ([]aistudio.StateCookie, error) {
	if err := ensurePlatformImport(); err != nil {
		return nil, err
	}
	proxyURL, err := validateProxy(proxy)
	if err != nil {
		return nil, err
	}
	root := source.Root
	if root == "" {
		browser := source.Browser
		if browser == "" {
			browser = BrowserChrome
		}
		var err error
		root, err = DefaultBrowserRoot(browser)
		if err != nil {
			return nil, err
		}
	}
	identity, err := readChromeIdentity(root, source.Profile)
	if err != nil {
		return nil, err
	}
	cookies, err := fetchGoogleCookies(ctx, identity, material.GaiaID, material.RefreshToken, material.WrappedBindingKey, proxyURL)
	if err != nil {
		return nil, err
	}
	return toStorageCookies(cookies), nil
}

func importAccount(ctx context.Context, browser string, root string, proxy string, account Account, masterKey []byte) (ImportResult, error) {
	if browser == "" {
		browser = BrowserChrome
	}
	email := strings.ToLower(strings.TrimSpace(account.Email))

	if account.SourceType == "cookie" {
		cookies, err := readChromiumCookies(browser, root, account.Profile, masterKey)
		if err != nil {
			return ImportResult{}, err
		}
		state := aistudio.StorageState{Cookies: cookies, Origins: []aistudio.StorageOrigin{}}
		state.SetAuthExtension(aistudio.AuthExtension{
			Source: aistudio.AuthSource{
				Browser: browser, Root: root, Profile: account.Profile, Email: email,
			},
		})
		return ImportResult{
			ID: account.ID, Browser: browser, Profile: account.Profile, DisplayName: account.DisplayName,
			Email: email, Locale: account.Locale, CookieCount: len(cookies), State: state,
		}, nil
	}

	gaiaID, encryptedToken, wrappedKey, err := readTokenService(root, account.Profile, account.GaiaID)
	if err != nil {
		return ImportResult{}, err
	}
	token, err := decryptToken(root, masterKey, encryptedToken)
	if err != nil {
		return ImportResult{}, err
	}
	identity, err := readChromeIdentity(root, account.Profile)
	if err != nil {
		return ImportResult{}, err
	}
	cookies, err := fetchGoogleCookies(ctx, identity, gaiaID, token, wrappedKey, proxy)
	if err != nil {
		return ImportResult{}, err
	}
	state := aistudio.StorageState{Cookies: toStorageCookies(cookies), Origins: []aistudio.StorageOrigin{}}
	state.SetAuthExtension(aistudio.AuthExtension{
		Source: aistudio.AuthSource{Browser: browser, Root: root, Profile: account.Profile, Email: email},
		OAuth: &aistudio.ChromeOAuthMaterial{
			GaiaID: gaiaID, RefreshToken: token, WrappedBindingKey: append([]byte(nil), wrappedKey...),
		},
	})
	return ImportResult{
		ID: account.ID, Browser: browser, Profile: account.Profile, DisplayName: account.DisplayName,
		Email: email, Locale: account.Locale,
		CookieCount: len(cookies), State: state,
	}, nil
}

func decryptToken(root string, masterKey []byte, encrypted []byte) (string, error) {
	if len(encrypted) < 3+12+16 {
		return "", fmt.Errorf("refresh token 密文长度不足")
	}
	ver := string(encrypted[:3])
	key := masterKey
	if ver == "v10" && root != "" {
		if dpapi, err := decryptDPAPIMasterKey(root); err == nil && len(dpapi) == 32 {
			key = dpapi
		}
	} else if ver != "v20" && ver != "v10" {
		return "", fmt.Errorf("不支持的 refresh token 密文版本 %s", ver)
	}
	if len(key) != 32 {
		return "", fmt.Errorf("缺少 %s 解密密钥", ver)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("创建 AES 解密器: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("创建 GCM 解密器: %w", err)
	}
	plaintext, err := gcm.Open(nil, encrypted[3:15], encrypted[15:], nil)
	if err != nil {
		return "", fmt.Errorf("refresh token 解密失败")
	}
	token := string(plaintext)
	if len(token) != 103 || !strings.HasPrefix(token, "1//0") {
		return "", fmt.Errorf("refresh token 解密结果格式异常")
	}
	return token, nil
}

func selectAccounts(accounts []Account, profiles []string, emails []string, ids []string) ([]Account, error) {
	requestedIDs := normalizedSet(ids)
	requestedProfiles := normalizedSet(profiles)
	requestedEmails := normalizedSet(emails)
	if len(requestedProfiles) == 0 && len(requestedEmails) == 0 && len(requestedIDs) == 0 {
		return nil, fmt.Errorf("未选择账号")
	}
	selected := make([]Account, 0, len(accounts))
	foundIDs := make(map[string]struct{})
	foundProfiles := make(map[string]struct{})
	foundEmails := make(map[string]struct{})
	for _, account := range accounts {
		profile := strings.ToLower(strings.TrimSpace(account.Profile))
		email := strings.ToLower(strings.TrimSpace(account.Email))
		id := strings.ToLower(strings.TrimSpace(account.ID))
		_, profileMatch := requestedProfiles[profile]
		_, emailMatch := requestedEmails[email]
		_, idMatch := requestedIDs[id]
		if !profileMatch && !emailMatch && !idMatch {
			continue
		}
		if !account.Importable {
			if account.Reason == ReasonBrowserRunning {
				b := account.Browser
				if b == "" {
					b = "浏览器"
				}
				return nil, fmt.Errorf("%s 正在运行中并锁定了数据库，请先关闭 %s 浏览器后再重试导入", b, b)
			}
			return nil, fmt.Errorf("%s %s 缺少可导入的认证材料", account.Profile, account.Email)
		}
		if account.SourceType != "cookie" && !strings.Contains(email, "@") {
			return nil, fmt.Errorf("%s 缺少账号邮箱", account.Profile)
		}
		if id != "" {
			foundIDs[id] = struct{}{}
		}
		foundProfiles[profile] = struct{}{}
		if email != "" {
			foundEmails[email] = struct{}{}
		}
		selected = append(selected, account)
	}
	if len(requestedIDs) > 0 && len(foundIDs) == 0 {
		return nil, fmt.Errorf("找不到选中的账号")
	}
	if len(requestedProfiles) > 0 && len(foundProfiles) == 0 {
		return nil, fmt.Errorf("找不到选中的 Profile")
	}
	if len(requestedEmails) > 0 && len(foundEmails) == 0 {
		return nil, fmt.Errorf("找不到选中的账号")
	}
	return selected, nil
}

func normalizedSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

func missingValues(requested map[string]struct{}, found map[string]struct{}) []string {
	missing := make([]string, 0)
	for value := range requested {
		if _, ok := found[value]; !ok {
			missing = append(missing, value)
		}
	}
	sort.Strings(missing)
	return missing
}

func validateProxy(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("proxy 必须是 http、https 或 socks5 URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5":
		return value, nil
	default:
		return "", fmt.Errorf("proxy 必须是 http、https 或 socks5 URL")
	}
}

func decryptV20Token(masterKey []byte, encrypted []byte) (string, error) {
	if len(encrypted) < 3+12+16 || string(encrypted[:3]) != "v20" {
		return "", fmt.Errorf("refresh token 密文版本不是 v20")
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return "", fmt.Errorf("创建 AES 解密器: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("创建 GCM 解密器: %w", err)
	}
	plaintext, err := gcm.Open(nil, encrypted[3:15], encrypted[15:], nil)
	if err != nil {
		return "", fmt.Errorf("refresh token 解密失败")
	}
	token := string(plaintext)
	if len(token) != 103 || !strings.HasPrefix(token, "1//0") {
		return "", fmt.Errorf("refresh token 解密结果格式异常")
	}
	return token, nil
}

func toStorageCookies(cookies []multiloginCookie) []aistudio.StateCookie {
	now := time.Now()
	result := make([]aistudio.StateCookie, 0, len(cookies))
	for _, cookie := range cookies {
		domain := cookie.Domain
		if domain == "" && cookie.Host != "" && !strings.HasPrefix(cookie.Host, ".") {
			domain = cookie.Host
		}
		expires := float64(-1)
		if cookie.MaxAge != nil {
			lifetime := time.Duration(*cookie.MaxAge * float64(time.Second))
			if lifetime > maxCookieAge {
				lifetime = maxCookieAge
			}
			expires = float64(now.Add(lifetime).UnixNano()) / 1e9
		}
		path := cookie.Path
		if path == "" {
			path = "/"
		}
		sameSite := strings.ToLower(cookie.SameSite)
		if sameSite != "none" && sameSite != "lax" && sameSite != "strict" {
			sameSite = ""
		} else {
			sameSite = strings.ToUpper(sameSite[:1]) + sameSite[1:]
		}
		result = append(result, aistudio.StateCookie{
			Name: cookie.Name, Value: cookie.Value, Domain: domain, Path: path,
			Expires: expires, HTTPOnly: cookie.IsHTTPOnly, Secure: cookie.IsSecure, SameSite: sameSite,
		})
	}
	return result
}
