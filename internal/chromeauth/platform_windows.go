//go:build windows

package chromeauth

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	_ "modernc.org/sqlite"
)

func defaultChromeRoot() (string, error) {
	localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if localAppData == "" {
		return "", fmt.Errorf("环境变量 LOCALAPPDATA 为空")
	}
	return filepath.Join(localAppData, "Google", "Chrome", "User Data"), nil
}

func ensurePlatformImport() error {
	return nil
}

func discoverPlatform(chromeRoot string) ([]Account, error) {
	data, err := os.ReadFile(filepath.Join(chromeRoot, "Local State"))
	if err != nil {
		return nil, fmt.Errorf("读取 Chrome Local State: %w", err)
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
		return nil, fmt.Errorf("解析 Chrome Local State: %w", err)
	}
	if state.Profile.InfoCache == nil {
		return nil, fmt.Errorf("Chrome Local State 缺少 profile.info_cache")
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
		locale := profileLocale(chromeRoot, profile)
		if locale == "" {
			locale = state.Variations.SafeSeedLocale
		}
		identities, err := profileIdentities(chromeRoot, profile)
		if err != nil {
			accounts = append(accounts, Account{
				ID: profile, Profile: profile, DisplayName: info.Name, Email: info.UserName, Locale: locale,
			})
			continue
		}
		records, recordsErr := readTokenServices(chromeRoot, profile)
		seen := map[string]bool{}
		for _, identity := range identities {
			if identity.Gaia == "" || seen[identity.Gaia] {
				continue
			}
			seen[identity.Gaia] = true
			record, found := records[identity.Gaia]
			accounts = append(accounts, Account{
				ID: profile + "/" + identity.Gaia, GaiaID: identity.Gaia,
				Profile: profile, DisplayName: info.Name, Email: identity.Email, Locale: locale,
				Importable: recordsErr == nil && found && strings.HasPrefix(string(record.encryptedToken), "v20"),
			})
		}
	}
	return accounts, nil
}

// profileIdentity 为 Preferences.account_info 中的一个 Google 账号
type profileIdentity struct {
	Gaia  string `json:"gaia"`
	Email string `json:"email"`
}

// profileIdentities 读取 Profile 内全部 Google 账号的 Gaia ID 与邮箱
func profileIdentities(chromeRoot string, profile string) ([]profileIdentity, error) {
	data, err := os.ReadFile(filepath.Join(chromeRoot, profile, "Preferences"))
	if err != nil {
		return nil, fmt.Errorf("读取 %s Preferences: %w", profile, err)
	}
	var preferences struct {
		Accounts []profileIdentity `json:"account_info"`
	}
	if err := json.Unmarshal(data, &preferences); err != nil {
		return nil, fmt.Errorf("解析 %s Preferences: %w", profile, err)
	}
	return preferences.Accounts, nil
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

// copyFile 把 src 复制为 dst
func copyFile(src string, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// openWebData 把 Profile 的 Web Data 与其 WAL 日志复制到临时目录后只读打开，绕过运行中 Chrome 的排他锁并包含尚未检查点的写入
func openWebData(chromeRoot string, profile string) (database *sql.DB, closeDatabase func(), err error) {
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
	if err = copyFile(databasePath, copyPath); err != nil {
		return nil, nil, fmt.Errorf("复制 %s Web Data: %w", profile, err)
	}
	if err = copyFile(databasePath+"-wal", copyPath+"-wal"); err != nil && !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("复制 %s Web Data-wal: %w", profile, err)
	}
	uri := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(copyPath), RawQuery: "mode=ro"}
	if database, err = sql.Open("sqlite", uri.String()); err != nil {
		return nil, nil, fmt.Errorf("打开 %s Web Data: %w", profile, err)
	}
	database.SetMaxOpenConns(1)
	return database, func() {
		database.Close()
		os.RemoveAll(tempDir)
	}, nil
}

// tokenRecord 是 token_service 中一个账号的加密 refresh token 与 wrapped binding key
type tokenRecord struct {
	encryptedToken []byte
	bindingKey     []byte
}

// readTokenServices 读取 Profile 中全部 Google 账号的 token_service 记录，键为 Gaia ID
func readTokenServices(chromeRoot string, profile string) (map[string]tokenRecord, error) {
	database, closeDatabase, err := openWebData(chromeRoot, profile)
	if err != nil {
		return nil, err
	}
	defer closeDatabase()
	rows, err := database.Query("SELECT service, encrypted_token, binding_key FROM token_service WHERE service LIKE 'AccountId-%'")
	if err != nil {
		return nil, fmt.Errorf("读取 %s token_service: %w", profile, err)
	}
	defer rows.Close()
	records := map[string]tokenRecord{}
	for rows.Next() {
		var service string
		var record tokenRecord
		if err := rows.Scan(&service, &record.encryptedToken, &record.bindingKey); err != nil {
			return nil, fmt.Errorf("解析 %s token_service: %w", profile, err)
		}
		records[strings.TrimPrefix(service, "AccountId-")] = record
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取 %s token_service: %w", profile, err)
	}
	return records, nil
}

// readTokenService 返回一个账号的 Gaia ID、加密 refresh token 与 wrapped binding key
func readTokenService(chromeRoot string, profile string, gaiaID string) (string, []byte, []byte, error) {
	records, err := readTokenServices(chromeRoot, profile)
	if err != nil {
		return "", nil, nil, err
	}
	record, found := records[gaiaID]
	if !found {
		return "", nil, nil, fmt.Errorf("%s 的 token_service 没有 %s 的记录", profile, gaiaID)
	}
	if len(record.encryptedToken) == 0 {
		return "", nil, nil, fmt.Errorf("%s 的 token_service 缺少认证材料", profile)
	}
	return gaiaID, record.encryptedToken, record.bindingKey, nil
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
		if index != 0 && isDigit != lastDigit {
			parts = append(parts, value[start:index])
			start = index
		}
		lastDigit = isDigit
	}
	return append(parts, value[start:])
}
