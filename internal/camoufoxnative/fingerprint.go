package camoufoxnative

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/brainplusplus/go-browserforge/fingerprints"
	bfheaders "github.com/brainplusplus/go-browserforge/headers"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/rhythm"
)

var firefoxVersionPattern = regexp.MustCompile(`\b1[0-9]{2}\.0\b`)

const camoufoxFirefoxMajor = 158

type savedFingerprint struct {
	FirefoxVersion int            `json:"firefox_version"`
	Locale         string         `json:"locale"`
	Timezone       string         `json:"timezone"`
	Config         map[string]any `json:"config"`
	// Rhythm 是账户点击与粘贴的事件节奏
	Rhythm *rhythm.Rhythm `json:"rhythm,omitempty"`
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
	config := make(map[string]any)
	for key, value := range map[string]any{
		"navigator.userAgent":           userAgent,
		"navigator.appCodeName":         fingerprint.Navigator.AppCodeName,
		"navigator.appName":             fingerprint.Navigator.AppName,
		"navigator.appVersion":          appVersion,
		"navigator.oscpu":               fingerprint.Navigator.Oscpu,
		"navigator.platform":            fingerprint.Navigator.Platform,
		"navigator.hardwareConcurrency": firefoxHardwareConcurrency(),
		"navigator.product":             fingerprint.Navigator.Product,
		"screen.colorDepth":             nonNegative(fingerprint.Screen.ColorDepth),
		"screen.pixelDepth":             nonNegative(fingerprint.Screen.PixelDepth),
		"headers.User-Agent":            userAgent,
		"headers.Accept-Encoding":       headerValue(fingerprint.Headers, "accept-encoding"),
	} {
		if truthy(value) {
			config[key] = value
		}
	}
	applyWindowsDisplay(config)
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
	config["fonts"] = windowsFontSubset(locale)
	maps.Copy(config, windowsWebGLByVendor())
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
		account := rhythm.New()
		if writeErr := writeAccountCamoufoxConfig(path, savedFingerprint{FirefoxVersion: ffVersion, Locale: locale, Timezone: timezone, Config: config, Rhythm: &account}); writeErr != nil {
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
	if saved.Rhythm == nil {
		account := rhythm.New()
		saved.Rhythm = &account
		changed = true
	}
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
	config["voices"] = windowsVoices(locale)
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

// sessionVariables 是 Firefox 以 root 运行时检查属主的环境变量
var sessionVariables = []string{"HOME", "XDG_RUNTIME_DIR", "XAUTHORITY"}

// camoufoxEnvironment 将指纹 JSON 分片写入 Camoufox 环境变量
func camoufoxEnvironment(config map[string]any) ([]string, error) {
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("编码 Camoufox 指纹: %w", err)
	}
	values := make(map[string]string)
	for _, item := range os.Environ() {
		name, value, ok := strings.Cut(item, "=")
		if ok && !strings.HasPrefix(name, "CAMOU_CONFIG_") && (!slices.Contains(sessionVariables, name) || !foreignUserPath(value)) {
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

// windowsProfileJSON 为 Camoufox 官方启动器的 Windows 字体清单
//
//go:embed windows_profile.json
var windowsProfileJSON []byte

// windowsProfileData 为解析后的 Windows 字体清单
type windowsProfileData struct {
	Fonts []string `json:"fonts"`
}

var windowsProfile = func() windowsProfileData {
	var profile windowsProfileData
	if err := json.Unmarshal(windowsProfileJSON, &profile); err != nil {
		panic(err)
	}
	return profile
}()

// windowsWebGLJSON 为 Camoufox 官方 WebGL 数据中支持 WebGL2 的 Windows 条目及其权重
//
//go:embed windows_webgl.json
var windowsWebGLJSON []byte

// windowsWebGLProfile 为一组 Windows 显卡的 WebGL 配置及其出现权重
type windowsWebGLProfile struct {
	Weight float64        `json:"weight"`
	Config map[string]any `json:"config"`
}

var windowsWebGLProfiles = func() []windowsWebGLProfile {
	var profiles []windowsWebGLProfile
	if err := json.Unmarshal(windowsWebGLJSON, &profiles); err != nil {
		panic(err)
	}
	return profiles
}()

// windowsFontsJSON 为 Windows 版 Firefox 字体偏好、Camoufox 发行包 Windows 字体的字体族名分组与 Windows 字体替换表
//
//go:embed windows_fonts.json
var windowsFontsJSON []byte

// windowsFontTable 为 Linux 上按 Windows 解析字体所需的偏好、字体族名分组与替换表
type windowsFontTable struct {
	Prefs       map[string]any    `json:"prefs"`
	Families    [][]string        `json:"families"`
	Substitutes map[string]string `json:"substitutes"`
}

var windowsFontData = func() windowsFontTable {
	var table windowsFontTable
	if err := json.Unmarshal(windowsFontsJSON, &table); err != nil {
		panic(err)
	}
	for key, value := range table.Prefs {
		if number, ok := value.(float64); ok {
			table.Prefs[key] = int(number)
		}
	}
	return table
}()

// windowsFontAliases 返回发行包中所在字体文件都带有已声明名称的其他字体族名
func windowsFontAliases(fonts []string) []string {
	declared := map[string]bool{}
	for _, font := range fonts {
		declared[strings.ToLower(font)] = true
	}
	allowed := map[string]bool{}
	var names []string
	for _, family := range windowsFontData.Families {
		hit := slices.ContainsFunc(family, func(name string) bool { return declared[strings.ToLower(name)] })
		for _, name := range family {
			key := strings.ToLower(name)
			if declared[key] {
				continue
			}
			if previous, seen := allowed[key]; seen {
				allowed[key] = previous && hit
				continue
			}
			allowed[key] = hit
			names = append(names, name)
		}
	}
	return slices.DeleteFunc(names, func(name string) bool { return !allowed[strings.ToLower(name)] })
}

// windowsEssentialFonts 为 Windows 字体子集必须包含的字体，含各语言版本 Windows 都预装的东亚界面字体
var windowsEssentialFonts = []string{
	"Arial", "Times New Roman", "Courier New", "Verdana", "Georgia",
	"Trebuchet MS", "Tahoma", "Segoe UI", "Calibri", "Cambria Math",
	"Nirmala UI", "Consolas",
	"Microsoft JhengHei", "Microsoft JhengHei UI", "Microsoft YaHei", "Microsoft YaHei UI", "SimSun", "NSimSun", "PMingLiU-ExtB",
	"Yu Gothic", "Yu Gothic UI", "MS Gothic", "MS PGothic", "MS UI Gothic", "Malgun Gothic", "Segoe UI Emoji", "Segoe UI Symbol",
}

// windowsMarkerFonts 为识别 Windows 系统的标记字体
var windowsMarkerFonts = []string{"Segoe UI", "Tahoma", "Cambria Math", "Nirmala UI"}

// windowsFontSubset 按官方启动器规则生成必需字体加 30% 至 78% 其余字体的子集，再加上该语言 Windows 自动安装的补充字体
func windowsFontSubset(locale string) []string {
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
	return append(result, localeFonts(locale)...)
}

// withWindowsEssentialFonts 在字体列表末尾补上缺少的 Windows 必有字体与标记字体
func withWindowsEssentialFonts(fonts []string) []string {
	for _, font := range slices.Concat(windowsEssentialFonts, windowsMarkerFonts) {
		if !slices.Contains(fonts, font) {
			fonts = append(fonts, font)
		}
	}
	return fonts
}

// fontList 读取配置中的字体名列表
func fontList(value any) []string {
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
	return fonts
}

// windowsFonts 判断字体列表是否全部来自官方 Windows 字体清单或该语言的 Windows 补充字体
func windowsFonts(value any, locale string) bool {
	fonts := fontList(value)
	if len(fonts) == 0 {
		return false
	}
	for _, font := range fonts {
		if !slices.Contains(windowsProfile.Fonts, font) && !slices.Contains(localeFonts(locale), font) {
			return false
		}
	}
	return true
}

// windowsVoiceListsJSON 为真实 Windows Firefox 指纹中的语音清单及其出现次数
//
//go:embed windows_voices.json
var windowsVoiceListsJSON []byte

// windowsVoiceList 为一台 Windows 上 Firefox 列出的全部语音（名称:语言:local）与它在真实指纹中的出现次数
type windowsVoiceList struct {
	Weight float64  `json:"weight"`
	Voices []string `json:"voices"`
}

var windowsVoiceLists = func() []windowsVoiceList {
	var lists []windowsVoiceList
	if err := json.Unmarshal(windowsVoiceListsJSON, &lists); err != nil {
		panic(err)
	}
	return lists
}()

// voiceLanguages 返回语音清单所属系统的小写语言标签，各语言系统都带的 Zira Desktop 不计，只有它或为空的清单属于英文系统
func voiceLanguages(list windowsVoiceList) []string {
	var languages []string
	for _, entry := range list.Voices {
		parts := strings.Split(entry, ":")
		language := strings.ToLower(parts[len(parts)-2])
		if !strings.HasPrefix(entry, "Microsoft Zira Desktop ") && !slices.Contains(languages, language) {
			languages = append(languages, language)
		}
	}
	if len(languages) == 0 {
		return []string{"en-us"}
	}
	return languages
}

// windowsVoices 按出现次数从含账户语言语音的真实 Windows Firefox 语音清单中抽取一份：先找同一语言标签，再找同一语言，都没有时取只有英文语音的清单；Firefox 158 不标默认语音
func windowsVoices(locale string) []map[string]any {
	tag := strings.ToLower(locale)
	prefix, _, _ := strings.Cut(tag, "-")
	english := func(language string) bool { return strings.HasPrefix(language, "en-") }
	matches := []func([]string) bool{
		func(languages []string) bool { return slices.Contains(languages, tag) },
		func(languages []string) bool {
			return slices.ContainsFunc(languages, func(language string) bool { return strings.HasPrefix(language, prefix+"-") })
		},
		func(languages []string) bool {
			return !slices.ContainsFunc(languages, func(language string) bool { return !english(language) })
		},
	}
	var lists []weighted[windowsVoiceList]
	for _, match := range matches {
		for _, list := range windowsVoiceLists {
			if match(voiceLanguages(list)) {
				lists = append(lists, weighted[windowsVoiceList]{list, list.Weight})
			}
		}
		if len(lists) > 0 {
			break
		}
	}
	list := draw(lists)
	voices := make([]map[string]any, 0, len(list.Voices))
	for _, entry := range list.Voices {
		last := strings.LastIndex(entry, ":")
		separator := strings.LastIndex(entry[:last], ":")
		name, language := entry[:separator], entry[separator+1:last]
		voices = append(voices, map[string]any{
			"name":           name,
			"lang":           language,
			"voiceUri":       "urn:moz-tts:sapi:" + name + "?" + language,
			"isDefault":      false,
			"isLocalService": entry[last+1:] == "local",
		})
	}
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

// normalizeCamoufoxConfig 按官方启动器修正指纹中的空值、隐私信号、窗口几何、字体、WebGL 与媒体设备，删除 Camoufox 已移除的配置项；指纹为没有触屏、窗口在主屏上的 Windows 桌面，可用区域取 Firefox 158 指纹保护报告的值，字体含 Windows 必有字体
func normalizeCamoufoxConfig(config map[string]any) {
	for key, value := range config {
		derived := strings.HasPrefix(key, "navigator.") || strings.HasPrefix(key, "screen.") ||
			strings.HasPrefix(key, "window.") || strings.HasPrefix(key, "battery:")
		if derived && key != "window.screenX" && key != "window.screenY" && !truthy(value) {
			delete(config, key)
		}
	}
	for _, key := range []string{"window.history.length", "canvas:aaOffset", "canvas:aaCapOffset", "canvas:seed", "audio:seed", "fonts:spacing_seed", "navigator.doNotTrack", "navigator.globalPrivacyControl"} {
		delete(config, key)
	}
	language, _ := config["navigator.language"].(string)
	if !windowsFonts(config["fonts"], language) {
		config["fonts"] = windowsFontSubset(language)
	}
	config["fonts"] = withWindowsEssentialFonts(fontList(config["fonts"]))
	delete(config, "navigator.maxTouchPoints")
	delete(config, "screen.availLeft")
	delete(config, "screen.availTop")
	if _, ok := config["webGl:renderer"]; !ok {
		maps.Copy(config, windowsWebGLByVendor())
	}
	hasMediaDevices := false
	for key := range config {
		hasMediaDevices = hasMediaDevices || strings.HasPrefix(key, "mediaDevices:")
	}
	if !hasMediaDevices {
		maps.Copy(config, windowsMediaDevices())
	}
	reportAvailArea(config)
	clampWindowDimensions(config)
	clampWindowPosition(config)
}

// integer 读取配置中的整数值
func integer(config map[string]any, key string) (int, bool) {
	value, ok := number(config[key])
	return int(value), ok
}

// reportAvailArea 按 Firefox 158 在 Windows 上的指纹保护写入可用区域：宽为屏幕宽，高为屏幕高减 firefoxTaskbar
func reportAvailArea(config map[string]any) {
	width, _ := integer(config, "screen.width")
	height, _ := integer(config, "screen.height")
	if width == 0 || height == 0 {
		return
	}
	config["screen.availWidth"] = width
	config["screen.availHeight"] = height - firefoxTaskbar
}

// windowBorder 返回 Firefox 窗口 9 个物理像素边框按 devicePixelRatio 折算的 CSS 像素数，没有 devicePixelRatio 时为 0
func windowBorder(config map[string]any) int {
	ratio, ok := number(config["window.devicePixelRatio"])
	if !ok || ratio <= 0 {
		return 0
	}
	return int(math.Round(9 / ratio))
}

// windowsTaskbarMin 是 Windows 任务栏以 DIP 计的最小高度（Windows 10）
const windowsTaskbarMin = 40

// clampWindowDimensions 保证两个方向上 inner 不大于 outer、outer 不大于 Windows 工作区加两侧边框：工作区宽为屏幕宽，高最多为屏幕高减 windowsTaskbarMin
func clampWindowDimensions(config map[string]any) {
	border := windowBorder(config)
	for _, axis := range []string{"Width", "Height"} {
		screen, _ := integer(config, "screen."+strings.ToLower(axis))
		outer, _ := integer(config, "window.outer"+axis)
		inner, _ := integer(config, "window.inner"+axis)
		outerCap := 0
		if screen != 0 {
			outerCap = screen + 2*border
			if axis == "Height" {
				outerCap -= windowsTaskbarMin
			}
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

// clampWindowPosition 保证窗口除两侧边框外位于所报告的屏幕内
func clampWindowPosition(config map[string]any) {
	border := windowBorder(config)
	for axis, key := range map[string]string{"Width": "window.screenX", "Height": "window.screenY"} {
		screen, _ := integer(config, "screen."+strings.ToLower(axis))
		outer, _ := integer(config, "window.outer"+axis)
		position, ok := integer(config, key)
		if !ok || screen == 0 || outer == 0 {
			continue
		}
		config[key] = max(-border, min(position, screen-outer+border))
	}
}
