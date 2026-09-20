//go:build windows

package chromeauth

import (
	"database/sql"
	"encoding/json"
	"fmt"
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
		return "", fmt.Errorf("environment variable LOCALAPPDATA is empty")
	}
	return filepath.Join(localAppData, "Google", "Chrome", "User Data"), nil
}

func ensurePlatformImport() error {
	return nil
}

func discoverPlatform(chromeRoot string) ([]Account, error) {
	data, err := os.ReadFile(filepath.Join(chromeRoot, "Local State"))
	if err != nil {
		return nil, fmt.Errorf("read Chrome Local State: %w", err)
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
		return nil, fmt.Errorf("parse Chrome Local State: %w", err)
	}
	if state.Profile.InfoCache == nil {
		return nil, fmt.Errorf("Chrome Local State missing profile.info_cache")
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
		_, encrypted, bindingKey, tokenErr := readTokenService(chromeRoot, profile)
		accounts = append(accounts, Account{
			Profile: profile, DisplayName: info.Name, Email: info.UserName, Locale: locale,
			Importable: tokenErr == nil && strings.HasPrefix(string(encrypted), "v20") && len(bindingKey) != 0,
		})
	}
	return accounts, nil
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

func readTokenService(chromeRoot string, profile string) (string, []byte, []byte, error) {
	databasePath := filepath.Join(chromeRoot, profile, "Web Data")
	uri := "file:" + filepath.ToSlash(databasePath) + "?mode=ro&immutable=1"
	database, err := sql.Open("sqlite", uri)
	if err != nil {
		return "", nil, nil, fmt.Errorf("open %s Web Data: %w", profile, err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)

	rows, err := database.Query("SELECT service, encrypted_token, binding_key FROM token_service")
	if err != nil {
		return "", nil, nil, fmt.Errorf("read %s token_service: %w", profile, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", nil, nil, fmt.Errorf("read %s token_service: %w", profile, err)
		}
		return "", nil, nil, fmt.Errorf("%s token_service record count is 0", profile)
	}
	var service string
	var encryptedToken []byte
	var bindingKey []byte
	if err := rows.Scan(&service, &encryptedToken, &bindingKey); err != nil {
		return "", nil, nil, fmt.Errorf("parse %s token_service: %w", profile, err)
	}
	if rows.Next() {
		return "", nil, nil, fmt.Errorf("%s token_service record count greater than 1", profile)
	}
	if err := rows.Err(); err != nil {
		return "", nil, nil, fmt.Errorf("read %s token_service: %w", profile, err)
	}
	if !strings.HasPrefix(service, "AccountId-") {
		return "", nil, nil, fmt.Errorf("%s token_service service format is invalid", profile)
	}
	if len(encryptedToken) == 0 || len(bindingKey) == 0 {
		return "", nil, nil, fmt.Errorf("%s token_service missing authentication credentials", profile)
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
		if index != 0 && isDigit != lastDigit {
			parts = append(parts, value[start:index])
			start = index
		}
		lastDigit = isDigit
	}
	return append(parts, value[start:])
}
