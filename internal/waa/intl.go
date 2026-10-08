package waa

import (
	"embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

//go:embed intl
var intlFiles embed.FS

//go:embed intl.js
var intlJavaScript string

// intlScriptName 是 Intl 宿主脚本与 FormatJS 的来源名
const intlScriptName = hostScriptName + "-intl"

// intlInstaller 是预编译的 intl.js，各 Realm 共享
var intlInstaller = goja.MustCompile(intlScriptName, intlJavaScript, false)

// intlCommonData 是 Firefox Intl 的取值列表、可用区域设置、默认值、数字系统与时区别名
type intlCommonData struct {
	Lists          map[string][]string `json:"lists"`
	Available      []string            `json:"available"`
	Collator       []string            `json:"collator"`
	LocaleDefaults map[string][]any    `json:"localeDefaults"`
	Digits         map[string]string   `json:"digits"`
	TimeZones      map[string]string   `json:"timeZones"`
	DataLocales    []string            `json:"dataLocales"`
	available      map[string]bool
	collator       map[string]bool
	zoneByLower    map[string]string
}

// readIntlFile 解压并解析 intl 目录中的数据文件
func readIntlFile(name string, value any) error {
	raw, err := intlFiles.ReadFile("intl/" + name + ".json.gz")
	if err != nil {
		return err
	}
	data, err := gunzip(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

// intlCommon 读取全部 Realm 共享的 Intl 公共数据
var intlCommon = sync.OnceValues(func() (*intlCommonData, error) {
	var data intlCommonData
	if err := readIntlFile("common", &data); err != nil {
		return nil, fmt.Errorf("读取 Intl 公共数据: %w", err)
	}
	data.available = make(map[string]bool, len(data.Available))
	for _, tag := range data.Available {
		data.available[tag] = true
	}
	data.collator = make(map[string]bool, len(data.Collator))
	for _, tag := range data.Collator {
		data.collator[tag] = true
	}
	data.zoneByLower = make(map[string]string, len(data.TimeZones)+len(data.Lists["timeZone"]))
	for _, zone := range data.Lists["timeZone"] {
		data.zoneByLower[strings.ToLower(zone)] = zone
	}
	for name, zone := range data.TimeZones {
		data.zoneByLower[strings.ToLower(name)] = zone
	}
	return &data, nil
})

// localeSyntax 是 Unicode BCP 47 区域设置标识的语法
var localeSyntax = regexp.MustCompile(`(?i)^([a-z]{2,3}|[a-z]{5,8})(-[a-z]{4})?(-([a-z]{2}|[0-9]{3}))?(-([a-z0-9]{5,8}|[0-9][a-z0-9]{3}))*(-[a-wy-z0-9](-[a-z0-9]{2,8})+)*(-x(-[a-z0-9]{1,8})+)?$`)

// canonicalLocale 按 Unicode BCP 47 区域设置标识规范化标签，非法标签返回空串
func canonicalLocale(tag string) string {
	if !localeSyntax.MatchString(tag) {
		return ""
	}
	if parsed, err := language.Parse(tag); err == nil {
		return parsed.String()
	}
	parts := strings.Split(strings.ToLower(tag), "-")
	for index := 1; index < len(parts) && len(parts[index]) > 1; index++ {
		switch {
		case len(parts[index]) == 4 && unicode.IsLetter(rune(parts[index][0])):
			parts[index] = strings.ToUpper(parts[index][:1]) + parts[index][1:]
		case len(parts[index]) == 2 || len(parts[index]) == 3 && unicode.IsDigit(rune(parts[index][0])):
			parts[index] = strings.ToUpper(parts[index])
		}
	}
	return strings.Join(parts, "-")
}

// baseLocale 去除标签中的扩展与私用子标签
func baseLocale(tag string) string {
	parts := strings.Split(tag, "-")
	for index, part := range parts {
		if index > 0 && len(part) == 1 {
			return strings.Join(parts[:index], "-")
		}
	}
	return tag
}

// lookupLocale 按 BCP 47 Lookup 规则在 Firefox 可用区域设置中查找，未找到时返回空串
func lookupLocale(tag string, collator bool) string {
	data, _ := intlCommon()
	available := data.available
	if collator {
		available = data.collator
	}
	candidate := baseLocale(tag)
	for candidate != "" {
		if available[candidate] {
			return candidate
		}
		index := strings.LastIndexByte(candidate, '-')
		if index < 0 {
			return ""
		}
		candidate = candidate[:index]
		if index >= 2 && candidate[len(candidate)-2] == '-' {
			candidate = candidate[:len(candidate)-2]
		}
	}
	return ""
}

// intlDefaultLocale 返回 Camoufox 以账户语言给出的 Intl 默认区域设置，中文补全书写系统
func intlDefaultLocale(locale string) string {
	canonical := canonicalLocale(locale)
	if canonical == "" {
		return "en-US"
	}
	parsed := language.Make(canonical)
	base, script, region := parsed.Raw()
	if base.String() == "zh" && script.String() == "Zzzz" {
		likely, _ := parsed.Script()
		if composed, err := language.Compose(base, likely, region); err == nil {
			return composed.String()
		}
	}
	return canonical
}

// timeZoneID 按 Firefox 规则校验并规范化时区标识，非法时返回空串
func timeZoneID(name string) string {
	if offset, ok := offsetTimeZone(name); ok {
		return offset
	}
	data, _ := intlCommon()
	return data.zoneByLower[strings.ToLower(name)]
}

// offsetTimeZone 规范化 ±HH、±HHMM 与 ±HH:MM 形式的偏移时区
func offsetTimeZone(name string) (string, bool) {
	if len(name) < 3 || (name[0] != '+' && name[0] != '-') {
		return "", false
	}
	digits := strings.ReplaceAll(name[1:], ":", "")
	if len(digits) != 2 && len(digits) != 4 || strings.Count(name, ":") > 1 || strings.Contains(name, ":") && len(name) != 6 {
		return "", false
	}
	for _, character := range digits {
		if character < '0' || character > '9' {
			return "", false
		}
	}
	if len(digits) == 2 {
		digits += "00"
	}
	if digits[0] > '2' || digits[0] == '2' && digits[1] > '3' || digits[2] > '5' {
		return "", false
	}
	return name[:1] + digits[:2] + ":" + digits[2:], true
}

// localeParts 拆分规范化标签的语言、书写系统、地区与变体
func localeParts(tag string) map[string]any {
	parsed := language.Make(tag)
	base, script, region := parsed.Raw()
	result := map[string]any{"language": base.String(), "script": "", "region": ""}
	if script.String() != "Zzzz" {
		result["script"] = script.String()
	}
	if region.String() != "ZZ" {
		result["region"] = region.String()
	}
	variants := make([]string, 0, len(parsed.Variants()))
	for _, variant := range parsed.Variants() {
		variants = append(variants, variant.String())
	}
	result["variants"] = strings.Join(variants, "-")
	return result
}

// likelyTag 按 CLDR 似然子标签补全语言、书写系统与地区
func likelyTag(tag string) (language.Base, language.Script, language.Region) {
	parsed := language.Make(tag)
	base, _ := parsed.Base()
	script, _ := parsed.Script()
	region, _ := parsed.Region()
	return base, script, region
}

// spoofedTag 按 Camoufox 的区域设置伪装把标签的语言与地区替换为账户的语言与地区
func spoofedTag(tag string, account string) string {
	parsed := language.Make(tag)
	_, script, _ := parsed.Raw()
	accountBase, _, accountRegion := language.Make(account).Raw()
	parts := []any{accountBase}
	if script.String() != "Zzzz" {
		parts = append(parts, script)
	}
	if accountRegion.String() != "ZZ" {
		parts = append(parts, accountRegion)
	}
	for _, variant := range parsed.Variants() {
		parts = append(parts, variant)
	}
	composed, err := language.Compose(parts...)
	if err != nil {
		return tag
	}
	return composed.String()
}

// maximizeLocale 返回 Camoufox 中 Intl.Locale.prototype.maximize 的基础标签
func maximizeLocale(tag, account string) string {
	base, script, region := likelyTag(spoofedTag(tag, account))
	composed, err := language.Compose(base, script, region)
	if err != nil {
		return tag
	}
	return composed.String()
}

// minimizeLocale 返回 Camoufox 中 Intl.Locale.prototype.minimize 的基础标签
func minimizeLocale(tag, account string) string {
	spoofed := spoofedTag(tag, account)
	base, script, region := likelyTag(spoofed)
	full := base.String() + "-" + script.String() + "-" + region.String()
	maximized := func(parts ...any) string {
		composed, err := language.Compose(parts...)
		if err != nil {
			return ""
		}
		b, s, r := likelyTag(composed.String())
		return b.String() + "-" + s.String() + "-" + r.String()
	}
	if maximized(base) == full {
		return base.String()
	}
	if maximized(base, region) == full {
		return base.String() + "-" + region.String()
	}
	if maximized(base, script) == full {
		return base.String() + "-" + script.String()
	}
	return spoofed
}

// collatorScriptOrder 是 CLDR 中文、日文与韩文排序规则提前到拉丁字母之前的书写系统
var collatorScriptOrder = map[string][]*unicode.RangeTable{
	"zh": {unicode.Han, unicode.Bopomofo},
	"ja": {unicode.Hiragana, unicode.Katakana, unicode.Han},
	"ko": {unicode.Hangul, unicode.Han},
}

// swapCase 交换字符串中字母的大小写
func swapCase(text string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsUpper(character) {
			return unicode.ToLower(character)
		}
		if unicode.IsLower(character) {
			return unicode.ToUpper(character)
		}
		return character
	}, text)
}

// newCollator 用 x/text 排序规则构造与 Intl.Collator 选项对应的比较函数，caseFirst 为 upper 时比较交换大小写后的字符串
func newCollator(locale, sensitivity string, ignorePunctuation, numeric bool, collation, caseFirst string) func(string, string) int {
	compare := baseCollator(locale, sensitivity, ignorePunctuation, numeric, collation)
	if caseFirst == "upper" {
		return func(left, right string) int { return compare(swapCase(left), swapCase(right)) }
	}
	return compare
}

// baseCollator 构造不区分大写优先的比较函数，中文、日文与韩文按 CLDR 把本书写系统排在拉丁字母之前
func baseCollator(locale, sensitivity string, ignorePunctuation, numeric bool, collation string) func(string, string) int {
	parsed := language.Make(baseLocale(locale))
	if collation != "" && collation != "default" {
		parsed, _ = parsed.SetTypeForKey("co", collation)
	}
	switch sensitivity {
	case "base":
		parsed, _ = parsed.SetTypeForKey("ks", "level1")
	case "accent":
		parsed, _ = parsed.SetTypeForKey("ks", "level2")
	case "case":
		parsed, _ = parsed.SetTypeForKey("ks", "level1")
		parsed, _ = parsed.SetTypeForKey("kc", "true")
	}
	if ignorePunctuation {
		parsed, _ = parsed.SetTypeForKey("ka", "shifted")
	}
	options := []collate.Option{collate.OptionsFromTag(parsed)}
	if numeric {
		options = append(options, collate.Numeric)
	}
	collator := collate.New(parsed, options...)
	base, _ := parsed.Base()
	order := collatorScriptOrder[base.String()]
	if order == nil {
		return collator.CompareString
	}
	primary := collate.New(parsed, collate.IgnoreCase, collate.IgnoreDiacritics, collate.IgnoreWidth)
	rank := func(character rune) int {
		if !unicode.IsLetter(character) {
			return -1
		}
		for index, table := range order {
			if unicode.Is(table, character) {
				return index
			}
		}
		return len(order)
	}
	return func(left, right string) int {
		a, b := []rune(left), []rune(right)
		for index := 0; index < len(a) && index < len(b); index++ {
			if primary.CompareString(string(a[index]), string(b[index])) == 0 {
				continue
			}
			first, second := rank(a[index]), rank(b[index])
			if first >= 0 && second >= 0 && first != second {
				if first < second {
					return -1
				}
				return 1
			}
			break
		}
		return collator.CompareString(left, right)
	}
}

// formatjsSources 是按构造器打包的 FormatJS 宿主脚本源码
var formatjsSources = sync.OnceValues(func() (map[string]string, error) {
	var sources map[string]string
	if err := readIntlFile("formatjs", &sources); err != nil {
		return nil, fmt.Errorf("读取 FormatJS: %w", err)
	}
	return sources, nil
})

// intlPrograms 缓存进程内共享的 FormatJS 预编译脚本
var intlPrograms sync.Map

// intlProgram 返回指定名称的预编译宿主脚本
func intlProgram(name string, source func() (string, error)) (*goja.Program, error) {
	if cached, ok := intlPrograms.Load(name); ok {
		return cached.(*goja.Program), nil
	}
	text, err := source()
	if err != nil {
		return nil, err
	}
	program, err := goja.Compile(intlScriptName, text, false)
	if err != nil {
		return nil, fmt.Errorf("编译 %s: %w", name, err)
	}
	actual, _ := intlPrograms.LoadOrStore(name, program)
	return actual.(*goja.Program), nil
}

// formatjsLocaleData 缓存各数据区域设置的 FormatJS locale 数据
var formatjsLocaleData sync.Map

// formatjsData 返回数据区域设置在 FormatJS 某个包中的 locale 名称与数据脚本源码
func formatjsData(locale, pkg string) (string, string, error) {
	cached, ok := formatjsLocaleData.Load(locale)
	if !ok {
		var data map[string][2]string
		if err := readIntlFile("formatjs-"+locale, &data); err != nil {
			return "", "", fmt.Errorf("读取 FormatJS %s 数据: %w", locale, err)
		}
		cached, _ = formatjsLocaleData.LoadOrStore(locale, data)
	}
	entry, ok := cached.(map[string][2]string)[pkg]
	if !ok {
		return "", "", fmt.Errorf("FormatJS %s 缺少 %s 数据", locale, pkg)
	}
	return entry[0], entry[1], nil
}

// installIntlHost 为一个 Realm 创建 intl.js 使用的 Go 接口
func (state *hostState) installIntlHost(vm *goja.Runtime) (*goja.Object, error) {
	common, err := intlCommon()
	if err != nil {
		return nil, err
	}
	host := vm.NewObject()
	account := intlDefaultLocale(state.profile.Locale)
	set := func(name string, value any) {
		_ = host.Set(name, value)
	}
	dateData := func(tag string) *dateLocale {
		data, err := dateLocaleFor(tag)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return data
	}
	set("defaultLocale", account)
	set("defaultTimeZone", timeZoneID(state.location.String()))
	set("supportedValues", func(key string) goja.Value {
		values, ok := common.Lists[key]
		if !ok {
			return goja.Null()
		}
		return vm.ToValue(values)
	})
	set("likely", func(tag string) string {
		base, script, region := likelyTag(tag)
		composed, err := language.Compose(base, script, region)
		if err != nil {
			return tag
		}
		return composed.String()
	})
	set("canonicalize", canonicalLocale)
	set("lookup", lookupLocale)
	set("timeZone", timeZoneID)
	set("localeParts", localeParts)
	set("maximize", func(tag string) string { return maximizeLocale(tag, account) })
	set("minimize", func(tag string) string { return minimizeLocale(tag, account) })
	set("localeDefaults", func(tag string) []any {
		if values, ok := common.LocaleDefaults[tag]; ok {
			return values
		}
		data := dateData(tag)
		return []any{data.Calendar, data.NumberingSystem, data.NumberingSystem, false}
	})
	set("dataLocale", func(tag string) string { return localeKey(common.DataLocales, tag) })
	set("collator", newCollator)
	set("dateHourCycles", func(tag string) []string { return dateData(tag).HourCycles })
	set("dateRangeSeparator", func(tag string) string { return dateData(tag).Range })
	set("datePattern", func(tag string, key []string) []string {
		pattern, resolved := dateData(tag).pattern(key)
		return []string{pattern, resolved}
	})
	set("formatDate", func(tag, pattern string, epoch float64, zoneName, calendar, numbering string) [][2]string {
		return dateData(tag).format(pattern, epoch, zoneName, calendar, common.Digits[numbering])
	})
	set("formatjsChunk", func(name string) goja.Value {
		program, err := intlProgram("chunk/"+name, func() (string, error) {
			sources, err := formatjsSources()
			if err != nil {
				return "", err
			}
			source, ok := sources[name]
			if !ok {
				return "", fmt.Errorf("缺少 FormatJS 构造器 %s", name)
			}
			return source, nil
		})
		if err != nil {
			panic(vm.NewGoError(err))
		}
		value, err := vm.RunProgram(program)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return value
	})
	set("formatjsData", func(locale, pkg string) []goja.Value {
		name, _, err := formatjsData(locale, pkg)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		program, err := intlProgram("data/"+locale+"/"+pkg, func() (string, error) {
			_, source, err := formatjsData(locale, pkg)
			return source, err
		})
		if err != nil {
			panic(vm.NewGoError(err))
		}
		loader, err := vm.RunProgram(program)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return []goja.Value{vm.ToValue(name), loader}
	})
	return host, nil
}
