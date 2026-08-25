package camoufoxnative

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/brainplusplus/go-browserforge/fingerprints"
	bfheaders "github.com/brainplusplus/go-browserforge/headers"
)

var firefoxVersionPattern = regexp.MustCompile(`\b1[0-9]{2}\.0\b`)

type savedFingerprint struct {
	FirefoxVersion int            `json:"firefox_version"`
	Locale         string         `json:"locale"`
	Timezone       string         `json:"timezone"`
	Config         map[string]any `json:"config"`
}

// PersistAccountFingerprint 将隔离登录指纹保存到账户目录
func PersistAccountFingerprint(sourceDirectory string, targetDirectory string) error {
	source := filepath.Join(sourceDirectory, "camoufox-fingerprint.json")
	data, err := os.ReadFile(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取隔离登录 Camoufox 指纹: %w", err)
	}
	var saved savedFingerprint
	if err := json.Unmarshal(data, &saved); err != nil {
		return fmt.Errorf("解析隔离登录 Camoufox 指纹: %w", err)
	}
	if len(saved.Config) == 0 {
		return fmt.Errorf("隔离登录 Camoufox 指纹为空")
	}
	return writeAccountCamoufoxConfig(filepath.Join(targetDirectory, "camoufox-fingerprint.json"), saved)
}

// browserMajor 从 Camoufox 发行元数据读取实际 Firefox 主版本
func browserMajor(executablePath string) (int, error) {
	directory := filepath.Dir(executablePath)
	var data []byte
	var err error
	for range 5 {
		data, err = os.ReadFile(filepath.Join(directory, "version.json"))
		if err == nil {
			break
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	if err != nil {
		return 0, fmt.Errorf("读取 Camoufox 版本: %w", err)
	}
	var metadata struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return 0, fmt.Errorf("解析 Camoufox 版本: %w", err)
	}
	var major int
	if _, err := fmt.Sscanf(metadata.Version, "%d", &major); err != nil || major <= 0 {
		return 0, fmt.Errorf("Camoufox 版本无效: %s", metadata.Version)
	}
	return major, nil
}

// buildCamoufoxConfig 生成与实际 Camoufox 版本一致的 Windows Firefox 指纹
func buildCamoufoxConfig(ffVersion int, locale string, timezone string) (map[string]any, error) {
	locale = normalizeLocale(locale)
	locales := localeValues(locale)
	fingerprint, err := fingerprints.Generate(fingerprints.Options{
		Screen: &fingerprints.Screen{
			MinWidth:  1280,
			MaxWidth:  1920,
			MinHeight: 720,
			MaxHeight: 1200,
		},
		Headers: bfheaders.Options{
			Browsers:         []bfheaders.Browser{{Name: "firefox", HTTPVersion: "2"}},
			OperatingSystems: []string{"windows"},
			Devices:          []string{"desktop"},
			Locales:          locales,
			HTTPVersion:      "2",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("生成 BrowserForge 指纹: %w", err)
	}
	version := fmt.Sprintf("%d.0", ffVersion)
	userAgent := replaceFirefoxVersion(fingerprint.Navigator.UserAgent, version)
	appVersion := replaceFirefoxVersion(fingerprint.Navigator.AppVersion, version)
	screen := fingerprint.Screen
	config := map[string]any{
		"navigator.userAgent":           userAgent,
		"navigator.appCodeName":         fingerprint.Navigator.AppCodeName,
		"navigator.appName":             fingerprint.Navigator.AppName,
		"navigator.appVersion":          appVersion,
		"navigator.oscpu":               fingerprint.Navigator.Oscpu,
		"navigator.language":            fingerprint.Navigator.Language,
		"navigator.languages":           fingerprint.Navigator.Languages,
		"navigator.platform":            fingerprint.Navigator.Platform,
		"navigator.hardwareConcurrency": fingerprint.Navigator.HardwareConcurrency,
		"navigator.product":             fingerprint.Navigator.Product,
		"navigator.maxTouchPoints":      fingerprint.Navigator.MaxTouchPoints,
		"screen.availHeight":            nonNegative(screen.AvailHeight),
		"screen.availWidth":             nonNegative(screen.AvailWidth),
		"screen.availTop":               nonNegative(screen.AvailTop),
		"screen.availLeft":              nonNegative(screen.AvailLeft),
		"screen.height":                 nonNegative(screen.Height),
		"screen.width":                  nonNegative(screen.Width),
		"screen.colorDepth":             nonNegative(screen.ColorDepth),
		"screen.pixelDepth":             nonNegative(screen.PixelDepth),
		"screen.pageXOffset":            screen.PageXOffset,
		"screen.pageYOffset":            screen.PageYOffset,
		"window.outerHeight":            nonNegative(screen.OuterHeight),
		"window.outerWidth":             nonNegative(screen.OuterWidth),
		"window.innerHeight":            nonNegative(screen.InnerHeight),
		"window.innerWidth":             nonNegative(screen.InnerWidth),
		"window.screenX":                screen.ScreenX,
		"window.screenY":                screenY(screen),
		"window.history.length":         randomInt(5) + 1,
		"headers.User-Agent":            userAgent,
		"headers.Accept-Language":       headerValue(fingerprint.Headers, "accept-language"),
		"headers.Accept-Encoding":       headerValue(fingerprint.Headers, "accept-encoding"),
		"fonts":                         fingerprint.Fonts,
		"fonts:spacing_seed":            randomInt(1_073_741_824),
		"canvas:aaOffset":               randomInt(101) - 50,
		"canvas:aaCapOffset":            true,
		"allowMainWorld":                true,
		"showcursor":                    false,
	}
	applyLocaleTimezone(config, locale, timezone)
	if fingerprint.Navigator.DoNotTrack != nil {
		config["navigator.doNotTrack"] = *fingerprint.Navigator.DoNotTrack
	}
	if fingerprint.Navigator.ExtraProperties != nil {
		if value, ok := fingerprint.Navigator.ExtraProperties["globalPrivacyControl"].(bool); ok {
			config["navigator.globalPrivacyControl"] = value
		}
	}
	for key, target := range map[string]string{
		"charging":        "battery:charging",
		"chargingTime":    "battery:chargingTime",
		"dischargingTime": "battery:dischargingTime",
	} {
		if value, ok := fingerprint.Battery[key]; ok {
			config[target] = value
		}
	}
	return config, nil
}

// loadAccountCamoufoxConfig 按账户复用非敏感 Camoufox 指纹
func loadAccountCamoufoxConfig(storageStatePath string, ffVersion int, locale string, timezone string) (map[string]any, error) {
	locale = normalizeLocale(locale)
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		timezone = "UTC"
	}
	path := filepath.Join(filepath.Dir(storageStatePath), "camoufox-fingerprint.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		config, buildErr := buildCamoufoxConfig(ffVersion, locale, timezone)
		if buildErr != nil {
			return nil, buildErr
		}
		if writeErr := writeAccountCamoufoxConfig(path, savedFingerprint{FirefoxVersion: ffVersion, Locale: locale, Timezone: timezone, Config: config}); writeErr != nil {
			return nil, writeErr
		}
		return config, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取账户 Camoufox 指纹: %w", err)
	}
	var saved savedFingerprint
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, fmt.Errorf("解析账户 Camoufox 指纹: %w", err)
	}
	if len(saved.Config) == 0 {
		return nil, fmt.Errorf("账户 Camoufox 指纹为空")
	}
	changed := false
	if saved.FirefoxVersion != ffVersion {
		version := fmt.Sprintf("%d.0", ffVersion)
		for _, key := range []string{"navigator.userAgent", "navigator.appVersion", "headers.User-Agent"} {
			if value, ok := saved.Config[key].(string); ok {
				saved.Config[key] = replaceFirefoxVersion(value, version)
			}
		}
		saved.FirefoxVersion = ffVersion
		changed = true
	}
	if saved.Locale != locale || saved.Timezone != timezone {
		applyLocaleTimezone(saved.Config, locale, timezone)
		saved.Locale = locale
		saved.Timezone = timezone
		changed = true
	}
	if changed {
		if err := writeAccountCamoufoxConfig(path, saved); err != nil {
			return nil, err
		}
	}
	return saved.Config, nil
}

func normalizeLocale(locale string) string {
	locale = strings.TrimSpace(locale)
	if locale == "" {
		return "en-US"
	}
	return locale
}

func localeValues(locale string) []string {
	language, _, found := strings.Cut(locale, "-")
	if !found {
		return []string{locale}
	}
	return []string{locale, strings.ToLower(language)}
}

func applyLocaleTimezone(config map[string]any, locale string, timezone string) {
	language, region, found := strings.Cut(locale, "-")
	config["navigator.language"] = locale
	config["navigator.languages"] = localeValues(locale)
	config["headers.Accept-Language"] = locale
	if found {
		config["headers.Accept-Language"] = locale + "," + strings.ToLower(language) + ";q=0.9"
	}
	config["locale:language"] = strings.ToLower(language)
	if found {
		config["locale:region"] = strings.ToUpper(region)
	} else {
		delete(config, "locale:region")
	}
	config["locale:all"] = strings.Join(localeValues(locale), ", ")
	config["timezone"] = timezone
}

func writeAccountCamoufoxConfig(path string, saved savedFingerprint) error {
	encoded, err := json.Marshal(saved)
	if err != nil {
		return fmt.Errorf("编码账户 Camoufox 指纹: %w", err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return fmt.Errorf("写入账户 Camoufox 指纹: %w", err)
	}
	return nil
}

// camoufoxEnvironment 将指纹 JSON 分片写入 Camoufox 环境变量
func camoufoxEnvironment(config map[string]any) ([]string, error) {
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("编码 Camoufox 指纹: %w", err)
	}
	values := make(map[string]string)
	for _, item := range os.Environ() {
		name, value, ok := strings.Cut(item, "=")
		if ok && !strings.HasPrefix(name, "CAMOU_CONFIG_") {
			values[name] = value
		}
	}
	const chunkSize = 2047
	for offset, index := 0, 1; offset < len(encoded); offset, index = offset+chunkSize, index+1 {
		end := offset + chunkSize
		if end > len(encoded) {
			end = len(encoded)
		}
		values[fmt.Sprintf("CAMOU_CONFIG_%d", index)] = string(encoded[offset:end])
	}
	env := make([]string, 0, len(values))
	for name, value := range values {
		env = append(env, name+"="+value)
	}
	return env, nil
}

func replaceFirefoxVersion(value, version string) string {
	return firefoxVersionPattern.ReplaceAllString(value, version)
}

func nonNegative(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func screenY(screen fingerprints.ScreenFingerprint) int {
	if screen.ScreenX >= -50 && screen.ScreenX <= 50 {
		return screen.ScreenX
	}
	return 0
}

func headerValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func randomInt(limit int) int {
	return rand.IntN(limit)
}
