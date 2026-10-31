//go:build windows

package chromeauth

import (
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unsafe"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"golang.org/x/sys/windows"
	_ "github.com/mattn/go-sqlite3"
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

var (
	procCryptUnprotect = windows.NewLazySystemDLL("crypt32.dll").NewProc("CryptUnprotectData")
	procLocalFree      = kernel32.NewProc("LocalFree")
)

func defaultChromeRoot() (string, error) {
	return defaultBrowserRoot(BrowserChrome)
}

func defaultBrowserRoot(browser string) (string, error) {
	localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if localAppData == "" {
		return "", fmt.Errorf("环境变量 LOCALAPPDATA 为空")
	}
	switch strings.ToLower(strings.TrimSpace(browser)) {
	case BrowserChrome, "":
		return filepath.Join(localAppData, "Google", "Chrome", "User Data"), nil
	case BrowserBrave:
		return filepath.Join(localAppData, "BraveSoftware", "Brave-Browser", "User Data"), nil
	case BrowserEdge:
		return filepath.Join(localAppData, "Microsoft", "Edge", "User Data"), nil
	default:
		return "", fmt.Errorf("不支持的浏览器: %s", browser)
	}
}

func ensurePlatformImport() error {
	return nil
}

func decryptDPAPIMasterKey(root string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(root, "Local State"))
	if err != nil {
		return nil, fmt.Errorf("读取 Local State: %w", err)
	}
	var state struct {
		OSCrypt struct {
			EncryptedKey string `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("解析 Local State: %w", err)
	}
	raw := strings.TrimSpace(state.OSCrypt.EncryptedKey)
	if raw == "" {
		return nil, fmt.Errorf("Local State 缺少 encrypted_key")
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("解析 encrypted_key: %w", err)
	}
	const dpapiPrefix = "DPAPI"
	if len(decoded) <= len(dpapiPrefix) || string(decoded[:len(dpapiPrefix)]) != dpapiPrefix {
		return nil, fmt.Errorf("encrypted_key 缺少 DPAPI 前缀")
	}
	cipherBytes := decoded[len(dpapiPrefix):]
	var inB, outB dataBlob
	inB.cbData = uint32(len(cipherBytes))
	inB.pbData = &cipherBytes[0]
	r, _, callErr := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(&inB)), 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&outB)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptUnprotectData 失败: %w", callErr)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(outB.pbData)))
	key := make([]byte, outB.cbData)
	copy(key, unsafe.Slice(outB.pbData, outB.cbData))
	if len(key) != 32 {
		return nil, fmt.Errorf("DPAPI 密钥长度异常 (%d)", len(key))
	}
	return key, nil
}

func retrieveMasterKey(browser string, root string) ([]byte, error) {
	v20Key, v20Err := retrieveV20KeyForBrowser(browser, root)
	if v20Err == nil && len(v20Key) == 32 {
		return v20Key, nil
	}
	dpapiKey, dpapiErr := decryptDPAPIMasterKey(root)
	if dpapiErr == nil && len(dpapiKey) == 32 {
		return dpapiKey, nil
	}
	if v20Err != nil {
		return nil, fmt.Errorf("解密 %s 主密钥失败 (ABE: %v, DPAPI: %v)", browser, v20Err, dpapiErr)
	}
	return nil, fmt.Errorf("解密 %s 主密钥失败: %w", browser, dpapiErr)
}

func openChromiumCookies(browser string, root string, profile string) (*sql.DB, func(), error) {
	tempDir, err := os.MkdirTemp("", "aistudio2api-cookies-*")
	if err != nil {
		return nil, nil, fmt.Errorf("创建 Cookie 临时目录: %w", err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tempDir)
		}
	}()
	cookiePath := filepath.Join(root, profile, "Network", "Cookies")
	if _, statErr := os.Stat(cookiePath); statErr != nil {
		cookiePath = filepath.Join(root, profile, "Cookies")
		if _, statErr2 := os.Stat(cookiePath); statErr2 != nil {
			return nil, nil, fmt.Errorf("未找到 Cookie 数据库: %w", statErr2)
		}
	}
	copyPath := filepath.Join(tempDir, "Cookies")
	exeName := browserExeName(browser)
	if err = copyLockedFile(cookiePath, copyPath, exeName); err != nil {
		if isSharingViolation(err) {
			b := browser
			if b == "" {
				b = "浏览器"
			}
			return nil, nil, fmt.Errorf("%s 正在运行中并锁定了 Cookie 数据库，请先关闭 %s 浏览器后再重试导入", b, b)
		}
		return nil, nil, fmt.Errorf("复制 %s Cookies: %w", profile, err)
	}
	_ = copyLockedFile(cookiePath+"-wal", copyPath+"-wal", exeName)
	uri := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(copyPath), RawQuery: "mode=ro"}
	database, err := sql.Open("sqlite3", uri.String())
	if err != nil {
		return nil, nil, fmt.Errorf("打开 %s Cookies: %w", profile, err)
	}
	database.SetMaxOpenConns(1)
	return database, func() {
		database.Close()
		os.RemoveAll(tempDir)
	}, nil
}

func checkChromiumCookies(browser string, root string, profile string) (hasCookies bool, isLocked bool, err error) {
	database, closeDatabase, err := openChromiumCookies(browser, root, profile)
	if err != nil {
		if errors.Is(err, errBrowserRunning) || isSharingViolation(err) {
			return false, true, nil
		}
		return false, false, err
	}
	defer closeDatabase()

	rows, err := database.Query("SELECT name FROM cookies WHERE host_key LIKE '%google.com'")
	if err != nil {
		return false, false, err
	}
	defer rows.Close()

	names := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			names[name] = true
		}
	}
	return names["SAPISID"] && (names["__Secure-1PAPISID"] || names["__Secure-3PAPISID"] || names["__Secure-1PSID"]), false, nil
}

func readChromiumCookies(browser string, root string, profile string, masterKey []byte) ([]aistudio.StateCookie, error) {
	dpapiKey, _ := decryptDPAPIMasterKey(root)

	database, closeDatabase, err := openChromiumCookies(browser, root, profile)
	if err != nil {
		return nil, err
	}
	defer closeDatabase()

	rows, err := database.Query("SELECT name, encrypted_value, host_key, path, expires_utc, is_secure, is_httponly, samesite FROM cookies WHERE host_key LIKE '%google.com'")
	if err != nil {
		return nil, fmt.Errorf("读取 %s cookies: %w", profile, err)
	}
	defer rows.Close()

	var cookies []aistudio.StateCookie
	for rows.Next() {
		var name, hostKey, path string
		var encrypted []byte
		var expiresUtc int64
		var isSecure, isHttpOnly, samesite int
		if err := rows.Scan(&name, &encrypted, &hostKey, &path, &expiresUtc, &isSecure, &isHttpOnly, &samesite); err != nil {
			return nil, fmt.Errorf("解析 %s cookies: %w", profile, err)
		}
		if name == "" || len(encrypted) == 0 {
			continue
		}

		val := ""
		if len(encrypted) >= 3+12+16 {
			ver := string(encrypted[:3])
			var key []byte
			if ver == "v20" {
				key = masterKey
			} else if ver == "v10" {
				if len(dpapiKey) == 32 {
					key = dpapiKey
				} else {
					key = masterKey
				}
			}
			if len(key) == 32 {
				block, err := aes.NewCipher(key)
				if err == nil {
					gcm, err := cipher.NewGCM(block)
					if err == nil {
						plain, err := gcm.Open(nil, encrypted[3:15], encrypted[15:], nil)
						if err == nil {
							if ver == "v20" && len(plain) > 32 && !isPrintableASCII(plain[:32]) {
								plain = plain[32:]
							}
							val = string(plain)
						}
					}
				}
			}
		}
		if val == "" {
			continue
		}

		expiresUnix := float64(0)
		if expiresUtc > 11644473600000000 {
			expiresUnix = float64((expiresUtc - 11644473600000000) / 1000000)
		}
		sameSiteStr := ""
		switch samesite {
		case 1:
			sameSiteStr = "Lax"
		case 2:
			sameSiteStr = "Strict"
		}
		cookies = append(cookies, aistudio.StateCookie{
			Name:     name,
			Value:    val,
			Domain:   hostKey,
			Path:     path,
			Expires:  expiresUnix,
			Secure:   isSecure != 0,
			HTTPOnly: isHttpOnly != 0,
			SameSite: sameSiteStr,
		})
	}
	names := make(map[string]bool)
	for _, c := range cookies {
		names[c.Name] = true
	}
	if !names["SAPISID"] {
		return nil, fmt.Errorf("%s 缺少核心 Google Cookie (SAPISID)", profile)
	}
	return cookies, nil
}

func isPrintableASCII(data []byte) bool {
	for _, b := range data {
		if b < 32 || b > 126 {
			return false
		}
	}
	return true
}

func discoverPlatform(chromeRoot string) ([]Account, error) {
	return discoverPlatformBrowser(BrowserChrome, chromeRoot)
}

func discoverPlatformBrowser(browser string, root string) ([]Account, error) {
	data, err := os.ReadFile(filepath.Join(root, "Local State"))
	if err != nil {
		return nil, fmt.Errorf("读取 %s Local State: %w", browser, err)
	}
	var state struct {
		Variations struct {
			SafeSeedLocale string `json:"safe_seed_locale"`
		} `json:"variations"`
		Profile struct {
			InfoCache map[string]struct {
				Name     string `json:"name"`
				UserName string `json:"user_name"`
			} `json:"info_cache"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("解析 %s Local State: %w", browser, err)
	}
	if state.Profile.InfoCache == nil {
		return nil, fmt.Errorf("%s Local State 缺少 profile.info_cache", browser)
	}

	profiles := make([]string, 0, len(state.Profile.InfoCache))
	for profile := range state.Profile.InfoCache {
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(i int, j int) bool {
		return naturalLess(profiles[i], profiles[j])
	})
	accounts := make([]Account, 0, len(profiles))
	for _, profile := range profiles {
		info := state.Profile.InfoCache[profile]
		locale := profileLocale(root, profile)
		if locale == "" {
			locale = state.Variations.SafeSeedLocale
		}
		identities, err := profileIdentities(root, profile)
		if err == nil && len(identities) > 0 {
			hasCoreCookies, isLocked, _ := checkChromiumCookies(browser, root, profile)
			seen := map[string]bool{}
			for _, identity := range identities {
				if identity.Gaia == "" || seen[identity.Gaia] {
					continue
				}
				seen[identity.Gaia] = true
				_, encrypted, _, tokenErr := readTokenService(root, profile, identity.Gaia)
				displayName := identity.DisplayName
				if displayName == "" {
					displayName = info.Name
				}
				acc := Account{
					ID:          profile + "/" + identity.Gaia,
					Browser:     browser,
					GaiaID:      identity.Gaia,
					Profile:     profile,
					DisplayName: displayName,
					Email:       identity.Email,
					Locale:      locale,
					AuthUser:    identity.AuthUser,
				}
				if tokenErr == nil && (strings.HasPrefix(string(encrypted), "v20") || strings.HasPrefix(string(encrypted), "v10")) {
					acc.Importable = true
					acc.SourceType = "oauth"
				} else if isLocked {
					acc.Reason = ReasonBrowserRunning
					acc.SourceType = "cookie"
				} else if hasCoreCookies {
					acc.Importable = true
					acc.SourceType = "cookie"
				}
				accounts = append(accounts, acc)
			}
		}

		if len(identities) == 0 {
			hasCoreCookies, isLocked, _ := checkChromiumCookies(browser, root, profile)
			acc := Account{
				ID:          profile,
				Browser:     browser,
				Profile:     profile,
				DisplayName: info.Name,
				Email:       info.UserName,
				Locale:      locale,
			}
			if isLocked {
				acc.Reason = ReasonBrowserRunning
				acc.SourceType = "cookie"
			} else if hasCoreCookies {
				acc.Importable = true
				acc.SourceType = "cookie"
			}
			accounts = append(accounts, acc)
		}
	}
	return accounts, nil
}

// profileIdentity 为 Preferences 中的一个 Google 账号
type profileIdentity struct {
	Gaia        string `json:"gaia"`
	Email       string `json:"email"`
	DisplayName string `json:"name,omitempty"`
	AuthUser    int    `json:"authuser,omitempty"`
}

// profileIdentities 读取 Profile 内全部 Google 账号的 Gaia ID 与邮箱
func profileIdentities(chromeRoot string, profile string) ([]profileIdentity, error) {
	data, err := os.ReadFile(filepath.Join(chromeRoot, profile, "Preferences"))
	if err != nil {
		return nil, fmt.Errorf("读取 %s Preferences: %w", profile, err)
	}
	var preferences struct {
		Accounts   []profileIdentity `json:"account_info"`
		GaiaCookie struct {
			LastListAccountsBinary string `json:"last_list_accounts_binary_data"`
		} `json:"gaia_cookie"`
	}
	if err := json.Unmarshal(data, &preferences); err != nil {
		return nil, fmt.Errorf("解析 %s Preferences: %w", profile, err)
	}
	if len(preferences.Accounts) > 0 {
		return preferences.Accounts, nil
	}
	if preferences.GaiaCookie.LastListAccountsBinary != "" {
		if raw, err := base64.StdEncoding.DecodeString(preferences.GaiaCookie.LastListAccountsBinary); err == nil {
			parsed := parseGaiaAccounts(raw)
			if len(parsed) > 0 {
				identities := make([]profileIdentity, len(parsed))
				for i, p := range parsed {
					identities[i] = profileIdentity{
						Gaia:        p.GaiaID,
						Email:       p.Email,
						DisplayName: p.DisplayName,
						AuthUser:    p.AuthUser,
					}
				}
				return identities, nil
			}
		}
	}
	return nil, nil
}

func profileLocale(chromeRoot string, profile string) string {
	data, err := os.ReadFile(filepath.Join(chromeRoot, profile, "Preferences"))
	if err != nil {
		return ""
	}
	var preferences struct {
		Intl struct {
			AcceptLanguages   string `json:"accept_languages"`
			SelectedLanguages string `json:"selected_languages"`
		} `json:"intl"`
	}
	if json.Unmarshal(data, &preferences) != nil {
		return ""
	}
	for _, value := range []string{preferences.Intl.AcceptLanguages, preferences.Intl.SelectedLanguages} {
		if locale := strings.TrimSpace(strings.Split(value, ",")[0]); locale != "" {
			return locale
		}
	}
	return ""
}

func openWebData(chromeRoot string, profile string) (*sql.DB, func(), error) {
	tempDir, err := os.MkdirTemp("", "aistudio2api-webdata-*")
	if err != nil {
		return nil, nil, fmt.Errorf("创建 Web Data 临时目录: %w", err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tempDir)
		}
	}()
	databasePath := filepath.Join(chromeRoot, profile, "Web Data")
	copyPath := filepath.Join(tempDir, "Web Data")
	if err = copyLockedFile(databasePath, copyPath); err != nil {
		return nil, nil, fmt.Errorf("复制 %s Web Data: %w", profile, err)
	}
	_ = copyLockedFile(databasePath+"-wal", copyPath+"-wal")
	_ = copyLockedFile(databasePath+"-shm", copyPath+"-shm")
	uri := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(copyPath), RawQuery: "mode=ro"}
	database, err := sql.Open("sqlite3", uri.String())
	if err != nil {
		return nil, nil, fmt.Errorf("打开 %s Web Data: %w", profile, err)
	}
	database.SetMaxOpenConns(1)
	return database, func() {
		database.Close()
		os.RemoveAll(tempDir)
	}, nil
}

func readTokenService(chromeRoot string, profile string, gaiaID string) (string, []byte, []byte, error) {
	database, closeDb, err := openWebData(chromeRoot, profile)
	if err != nil {
		return "", nil, nil, err
	}
	defer closeDb()

	rows, err := database.Query("SELECT service, encrypted_token, binding_key FROM token_service WHERE service = ?", "AccountId-"+gaiaID)
	if err != nil {
		return "", nil, nil, fmt.Errorf("读取 %s token_service: %w", profile, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", nil, nil, fmt.Errorf("读取 %s token_service: %w", profile, err)
		}
		return "", nil, nil, fmt.Errorf("%s 的 token_service 没有 %s 的记录", profile, gaiaID)
	}
	var service string
	var encryptedToken []byte
	var bindingKey []byte
	if err := rows.Scan(&service, &encryptedToken, &bindingKey); err != nil {
		return "", nil, nil, fmt.Errorf("解析 %s token_service: %w", profile, err)
	}
	if rows.Next() {
		return "", nil, nil, fmt.Errorf("%s 的 token_service 记录数大于 1", profile)
	}
	if err := rows.Err(); err != nil {
		return "", nil, nil, fmt.Errorf("读取 %s token_service: %w", profile, err)
	}
	if !strings.HasPrefix(service, "AccountId-") {
		return "", nil, nil, fmt.Errorf("%s 的 token_service service 格式异常", profile)
	}
	if len(encryptedToken) == 0 {
		return "", nil, nil, fmt.Errorf("%s 的 token_service 缺少认证材料", profile)
	}
	return strings.TrimPrefix(service, "AccountId-"), encryptedToken, bindingKey, nil
}

func naturalLess(left string, right string) bool {
	leftParts := splitNatural(left)
	rightParts := splitNatural(right)
	for index := 0; index < len(leftParts) && index < len(rightParts); index++ {
		leftNumber, leftErr := strconv.Atoi(leftParts[index])
		rightNumber, rightErr := strconv.Atoi(rightParts[index])
		if leftErr == nil && rightErr == nil {
			if leftNumber != rightNumber {
				return leftNumber < rightNumber
			}
			continue
		}
		comparison := strings.Compare(strings.ToLower(leftParts[index]), strings.ToLower(rightParts[index]))
		if comparison != 0 {
			return comparison < 0
		}
	}
	return len(leftParts) < len(rightParts)
}

func splitNatural(value string) []string {
	if value == "" {
		return nil
	}
	parts := make([]string, 0)
	start := 0
	lastDigit := unicode.IsDigit(rune(value[0]))
	for index, character := range value {
		isDigit := unicode.IsDigit(character)
		if isDigit == lastDigit {
			continue
		}
		parts = append(parts, value[start:index])
		start = index
		lastDigit = isDigit
	}
	return append(parts, value[start:])
}
