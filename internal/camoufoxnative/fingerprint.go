package camoufoxnative

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/brainplusplus/go-browserforge/fingerprints"
	bfheaders "github.com/brainplusplus/go-browserforge/headers"
)

var firefoxVersionPattern = regexp.MustCompile(`\b1[0-9]{2}\.0\b`)

const camoufoxFirefoxMajor = 152

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
	config := make(map[string]any)
	for key, value := range map[string]any{
		"navigator.userAgent":           userAgent,
		"navigator.appCodeName":         fingerprint.Navigator.AppCodeName,
		"navigator.appName":             fingerprint.Navigator.AppName,
		"navigator.appVersion":          appVersion,
		"navigator.oscpu":               fingerprint.Navigator.Oscpu,
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
		"window.outerHeight":            screen.OuterHeight,
		"window.outerWidth":             screen.OuterWidth,
		"window.innerHeight":            screen.InnerHeight,
		"window.innerWidth":             screen.InnerWidth,
		"window.screenX":                screen.ScreenX,
		"headers.User-Agent":            userAgent,
		"headers.Accept-Encoding":       headerValue(fingerprint.Headers, "accept-encoding"),
	} {
		if truthy(value) {
			config[key] = value
		}
	}
	applyScreenXY(config, screen)
	if fingerprint.Navigator.DoNotTrack != nil && truthy(*fingerprint.Navigator.DoNotTrack) {
		config["navigator.doNotTrack"] = *fingerprint.Navigator.DoNotTrack
	}
	if fingerprint.Navigator.ExtraProperties != nil {
		if value, ok := fingerprint.Navigator.ExtraProperties["globalPrivacyControl"].(bool); ok && value {
			config["navigator.globalPrivacyControl"] = value
		}
	}
	for key, target := range map[string]string{
		"charging":        "battery:charging",
		"chargingTime":    "battery:chargingTime",
		"dischargingTime": "battery:dischargingTime",
	} {
		if value, ok := fingerprint.Battery[key]; ok && truthy(value) {
			config[target] = value
		}
	}
	config["allowMainWorld"] = true
	config["showcursor"] = false
	config["fonts"] = windowsFontSubset()
	config["fonts:spacing_seed"] = randomSeed()
	applyLocaleTimezone(config, locale, timezone)
	normalizeCamoufoxConfig(config)
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
	before, err := json.Marshal(saved.Config)
	if err != nil {
		return nil, fmt.Errorf("编码账户 Camoufox 指纹: %w", err)
	}
	normalizeCamoufoxConfig(saved.Config)
	after, err := json.Marshal(saved.Config)
	if err != nil {
		return nil, fmt.Errorf("编码账户 Camoufox 指纹: %w", err)
	}
	if changed || string(before) != string(after) {
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

func headerValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

// randomSeed 返回 Camoufox 噪声种子的非零 uint32
func randomSeed() int64 {
	return int64(rand.Uint32N(math.MaxUint32)) + 1
}

// windowsProfileJSON 为 Camoufox 官方启动器的 Windows 字体与语音清单
//
//go:embed windows_profile.json
var windowsProfileJSON []byte

// windowsProfileData 为解析后的 Windows 字体与语音清单
type windowsProfileData struct {
	Fonts  []string `json:"fonts"`
	Voices []string `json:"voices"`
}

var windowsProfile = func() windowsProfileData {
	var profile windowsProfileData
	if err := json.Unmarshal(windowsProfileJSON, &profile); err != nil {
		panic(err)
	}
	return profile
}()

// windowsEssentialFonts 为 Windows 字体子集必须包含的字体
var windowsEssentialFonts = []string{
	"Arial", "Times New Roman", "Courier New", "Verdana", "Georgia",
	"Trebuchet MS", "Tahoma", "Segoe UI", "Calibri", "Cambria Math",
	"Nirmala UI", "Consolas",
}

// windowsMarkerFonts 为识别 Windows 系统的标记字体
var windowsMarkerFonts = []string{"Segoe UI", "Tahoma", "Cambria Math", "Nirmala UI"}

var voiceSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)

// windowsFontSubset 按官方启动器规则生成必需字体加 30% 至 78% 其余字体的子集
func windowsFontSubset() []string {
	var result, rest []string
	for _, font := range windowsProfile.Fonts {
		if slices.Contains(windowsEssentialFonts, font) {
			result = append(result, font)
		} else {
			rest = append(rest, font)
		}
	}
	percent := 30 + rand.IntN(49)
	count := int(math.Round(float64(percent) / 100 * float64(len(rest))))
	rand.Shuffle(len(rest), func(left, right int) { rest[left], rest[right] = rest[right], rest[left] })
	result = append(result, rest[:count]...)
	for _, font := range windowsMarkerFonts {
		if !slices.Contains(result, font) {
			result = append(result, font)
		}
	}
	return result
}

// windowsFonts 判断字体列表是否全部来自官方 Windows 字体清单
func windowsFonts(value any) bool {
	var fonts []string
	switch items := value.(type) {
	case []string:
		fonts = items
	case []any:
		for _, item := range items {
			font, _ := item.(string)
			fonts = append(fonts, font)
		}
	}
	if len(fonts) == 0 {
		return false
	}
	for _, font := range fonts {
		if !slices.Contains(windowsProfile.Fonts, font) {
			return false
		}
	}
	return true
}

// windowsVoices 返回完整 SAPI 语音列表，并把与语言匹配的语音设为默认
func windowsVoices(locale string) []map[string]any {
	voices := make([]map[string]any, 0, len(windowsProfile.Voices))
	for _, entry := range windowsProfile.Voices {
		last := strings.LastIndex(entry, ":")
		separator := strings.LastIndex(entry[:last], ":")
		name, language := entry[:separator], entry[separator+1:last]
		slug := strings.Trim(voiceSlugPattern.ReplaceAllString(strings.ToLower(name), "."), ".")
		voices = append(voices, map[string]any{
			"name":           name,
			"lang":           language,
			"voiceUri":       "urn:moz-tts:sapi:" + slug,
			"isDefault":      false,
			"isLocalService": entry[last+1:] == "local",
		})
	}
	prefix, _, _ := strings.Cut(strings.ToLower(locale), "-")
	if prefix == "" {
		prefix = "en"
	}
	index := slices.IndexFunc(voices, func(voice map[string]any) bool {
		return locale != "" && strings.EqualFold(voice["lang"].(string), locale)
	})
	if index < 0 {
		index = slices.IndexFunc(voices, func(voice map[string]any) bool {
			language, _, _ := strings.Cut(strings.ToLower(voice["lang"].(string)), "-")
			return language == prefix
		})
	}
	if index < 0 {
		index = 0
	}
	voices[index]["isDefault"] = true
	return voices
}

// truthy 按官方启动器规则判断 BrowserForge 取值是否写入配置
func truthy(value any) bool {
	switch item := value.(type) {
	case nil:
		return false
	case bool:
		return item
	case int:
		return item != 0
	case float64:
		return item != 0
	case string:
		return item != ""
	case []string:
		return len(item) > 0
	case []any:
		return len(item) > 0
	default:
		return true
	}
}

// applyScreenXY 按 BrowserForge 的 screenX 推导窗口位置
func applyScreenXY(config map[string]any, screen fingerprints.ScreenFingerprint) {
	screenX := screen.ScreenX
	if screenX == 0 {
		config["window.screenX"] = 0
		config["window.screenY"] = 0
		return
	}
	if screenX >= -50 && screenX <= 50 {
		config["window.screenY"] = screenX
		return
	}
	switch screenY := screen.AvailHeight - screen.OuterHeight; {
	case screenY == 0:
		config["window.screenY"] = 0
	case screenY > 0:
		config["window.screenY"] = rand.IntN(screenY)
	default:
		config["window.screenY"] = screenY + rand.IntN(-screenY)
	}
}

// normalizeCamoufoxConfig 按官方启动器修正指纹中的空值、窗口几何、字体、语音、媒体设备与噪声种子
func normalizeCamoufoxConfig(config map[string]any) {
	for key, value := range config {
		derived := strings.HasPrefix(key, "navigator.") || strings.HasPrefix(key, "screen.") ||
			strings.HasPrefix(key, "window.") || strings.HasPrefix(key, "battery:")
		if derived && key != "window.screenX" && key != "window.screenY" && !truthy(value) {
			delete(config, key)
		}
	}
	for _, key := range []string{"window.history.length", "canvas:aaOffset", "canvas:aaCapOffset"} {
		delete(config, key)
	}
	if !windowsFonts(config["fonts"]) {
		config["fonts"] = windowsFontSubset()
	}
	language, _ := config["navigator.language"].(string)
	config["voices"] = windowsVoices(language)
	hasMediaDevices := false
	for key := range config {
		hasMediaDevices = hasMediaDevices || strings.HasPrefix(key, "mediaDevices:")
	}
	if !hasMediaDevices {
		config["mediaDevices:enabled"] = true
		config["mediaDevices:micros"] = 1
		config["mediaDevices:webcams"] = 1
		config["mediaDevices:speakers"] = 0
	}
	for _, key := range []string{"fonts:spacing_seed", "audio:seed", "canvas:seed"} {
		if !truthy(config[key]) {
			config[key] = randomSeed()
		}
	}
	fixScreenNoTaskbar(config)
	clampWindowDimensions(config)
	clampWindowPosition(config)
}

// integer 读取配置中的整数值
func integer(config map[string]any, key string) (int, bool) {
	value, ok := number(config[key])
	return int(value), ok
}

// fixScreenNoTaskbar 为可用区域等于屏幕的指纹留出 Windows 任务栏高度
func fixScreenNoTaskbar(config map[string]any) {
	width, _ := integer(config, "screen.width")
	height, _ := integer(config, "screen.height")
	availWidth, hasAvailWidth := integer(config, "screen.availWidth")
	availHeight, hasAvailHeight := integer(config, "screen.availHeight")
	if width == 0 || height == 0 || !hasAvailWidth || !hasAvailHeight || availWidth != width || availHeight != height {
		return
	}
	available := height - 40
	config["screen.availHeight"] = available
	outer, _ := integer(config, "window.outerHeight")
	if outer <= available {
		return
	}
	inner, _ := integer(config, "window.innerHeight")
	config["window.outerHeight"] = available
	if inner != 0 {
		config["window.innerHeight"] = available - (outer - inner)
	}
}

// clampWindowDimensions 保证两个方向上 inner 不大于 outer、outer 不大于可用区域、可用区域不大于屏幕
func clampWindowDimensions(config map[string]any) {
	for _, axis := range []string{"Width", "Height"} {
		screen, _ := integer(config, "screen."+strings.ToLower(axis))
		avail, hasAvail := integer(config, "screen.avail"+axis)
		outer, _ := integer(config, "window.outer"+axis)
		inner, _ := integer(config, "window.inner"+axis)
		if screen != 0 && avail != 0 && avail > screen {
			config["screen.avail"+axis] = screen
			avail = screen
		}
		outerCap := screen
		if hasAvail {
			outerCap = avail
		}
		if outer != 0 && outerCap != 0 && outer > outerCap {
			chrome := 0
			if inner != 0 {
				chrome = max(0, outer-inner)
			}
			config["window.outer"+axis] = outerCap
			outer = outerCap
			if inner != 0 {
				inner = max(1, outerCap-chrome)
				config["window.inner"+axis] = inner
			}
		}
		if inner != 0 && outer != 0 && inner > outer {
			config["window.inner"+axis] = outer
		}
	}
}

// clampWindowPosition 保证窗口位于所报告的屏幕内
func clampWindowPosition(config map[string]any) {
	for axis, key := range map[string]string{"Width": "window.screenX", "Height": "window.screenY"} {
		screen, _ := integer(config, "screen."+strings.ToLower(axis))
		outer, _ := integer(config, "window.outer"+axis)
		position, ok := integer(config, key)
		if !ok || screen == 0 || outer == 0 {
			continue
		}
		config[key] = max(0, min(position, screen-outer))
	}
}
