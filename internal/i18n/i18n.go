// Package i18n 按翻译表把服务生成的简体中文文案换成界面语言
package i18n

import (
	"embed"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// Source 是服务文案的原文语言
const Source = "zh-CN"

// Locales 是界面与命令行支持的语言
var Locales = []string{"zh-CN", "zh-TW", "en", "ja", "ko", "fr", "de"}

//go:embed messages/*.json
var messages embed.FS

// verb 匹配 Go 格式串中的一个格式动词
var verb = regexp.MustCompile(`%(?:\[\d+\])?[-+# 0]*\d*(?:\.\d+)?[a-zA-Z%]`)

// placeholder 匹配译文中的编号占位符
var placeholder = regexp.MustCompile(`\{(\d+)\}`)

// separators 是整句没有匹配时依次尝试的列表切分符，各段分别翻译
var separators = []string{"\n", " | ", "; ", "；"}

// wrappers 是错误链的切分符，在第一次出现处拆成外层说明与内层原因
var wrappers = []string{": ", "："}

// halfWidth 是全角切分符在不用全角标点的语言中的写法
var halfWidth = map[string]string{"；": "; ", "：": ": "}

// entry 是一条格式串的匹配规则与译文
type entry struct {
	pattern *regexp.Regexp
	anchor  string
	weight  int
	plain   bool
	text    string
}

// catalog 是一种语言按具体程度排序的翻译规则
type catalog struct {
	entries []entry
	wide    bool
}

var (
	catalogMu sync.Mutex
	catalogs  = map[string]*catalog{}
)

// Messages 返回 locale 的翻译表原文，键为格式串，值为带编号占位符的译文
func Messages(locale string) ([]byte, bool) {
	if !slices.Contains(Locales, locale) || locale == Source {
		return nil, false
	}
	data, err := messages.ReadFile("messages/" + locale + ".json")
	return data, err == nil
}

// Translate 返回 text 在 locale 下的译文，简体中文、不含汉字或没有匹配的文案原样返回
func Translate(locale, text string) string {
	if locale == Source || !hasHan(text) {
		return text
	}
	current := load(locale)
	if current == nil {
		return text
	}
	return current.translate(text)
}

// load 读取并编译 locale 的翻译表，不支持的语言返回 nil
func load(locale string) *catalog {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	if current, ok := catalogs[locale]; ok {
		return current
	}
	data, ok := Messages(locale)
	var table map[string]string
	if !ok || json.Unmarshal(data, &table) != nil {
		catalogs[locale] = nil
		return nil
	}
	current := &catalog{entries: make([]entry, 0, len(table)), wide: locale == "zh-TW" || locale == "ja"}
	for template, text := range table {
		pattern, anchor, weight := compile(template)
		current.entries = append(current.entries, entry{pattern: pattern, anchor: anchor, weight: weight, plain: pattern.NumSubexp() == 0, text: text})
	}
	slices.SortFunc(current.entries, func(a, b entry) int { return b.weight - a.weight })
	catalogs[locale] = current
	return current
}

// compile 把格式串编译为整句匹配的正则，返回最长的字面片段与字面字符数
func compile(template string) (*regexp.Regexp, string, int) {
	var pattern, literal strings.Builder
	anchor, weight := "", 0
	flush := func() {
		text := literal.String()
		if len([]rune(text)) > len([]rune(anchor)) {
			anchor = text
		}
		weight += len([]rune(text))
		pattern.WriteString(regexp.QuoteMeta(text))
		literal.Reset()
	}
	pattern.WriteString(`^`)
	last := 0
	for _, location := range verb.FindAllStringIndex(template, -1) {
		literal.WriteString(template[last:location[0]])
		last = location[1]
		token := template[location[0]:location[1]]
		if token == "%%" {
			literal.WriteString("%")
			continue
		}
		flush()
		switch token[len(token)-1] {
		case 'd':
			pattern.WriteString(`(-?\d+)`)
		case 'x':
			pattern.WriteString(`([0-9a-f]+)`)
		case 'q':
			pattern.WriteString(`("(?:[^"\\]|\\.)*")`)
		default:
			pattern.WriteString(`([\s\S]*?)`)
		}
	}
	literal.WriteString(template[last:])
	flush()
	pattern.WriteString(`$`)
	return regexp.MustCompile(pattern.String()), anchor, weight
}

// translate 按整句匹配格式串翻译，没有匹配时拆分后逐段翻译
func (current *catalog) translate(text string) string {
	if !hasHan(text) {
		return text
	}
	for _, item := range current.entries {
		if !strings.Contains(text, item.anchor) {
			continue
		}
		groups := item.pattern.FindStringSubmatch(text)
		if groups == nil {
			continue
		}
		return placeholder.ReplaceAllStringFunc(item.text, func(token string) string {
			index, _ := strconv.Atoi(token[1 : len(token)-1])
			if index+1 >= len(groups) {
				return token
			}
			return current.translate(groups[index+1])
		})
	}
	if trimmed := strings.TrimSpace(text); trimmed != text {
		start := strings.Index(text, trimmed)
		return text[:start] + current.translate(trimmed) + text[start+len(trimmed):]
	}
	for _, separator := range separators {
		if parts := strings.Split(text, separator); len(parts) > 1 {
			for index, part := range parts {
				parts[index] = current.translate(part)
			}
			return strings.Join(parts, current.punctuation(separator))
		}
	}
	for _, wrapper := range wrappers {
		if outer, inner, ok := strings.Cut(text, wrapper); ok {
			return current.translate(outer) + current.punctuation(wrapper) + current.translate(inner)
		}
	}
	for _, item := range current.entries {
		if item.plain && strings.HasPrefix(text, item.anchor+" ") {
			return item.text + " " + current.translate(text[len(item.anchor)+1:])
		}
	}
	if key, value, ok := strings.Cut(text, "="); ok {
		return current.translate(key) + "=" + current.translate(value)
	}
	return text
}

// punctuation 返回切分符在译文中的写法，不用全角标点的语言把全角分号与冒号写成半角加空格
func (current *catalog) punctuation(separator string) string {
	if half, ok := halfWidth[separator]; ok && !current.wide {
		return half
	}
	return separator
}

// hasHan 判断文案是否含汉字
func hasHan(text string) bool {
	return strings.ContainsFunc(text, func(r rune) bool { return unicode.Is(unicode.Han, r) })
}

// Match 返回与语言标签最接近的支持语言，简体中文变体为 zh-CN，繁体中文变体为 zh-TW，其余按语言主标签选择，都不支持时返回空字符串
func Match(tag string) string {
	tag = strings.ReplaceAll(strings.TrimSpace(strings.SplitN(tag, ".", 2)[0]), "_", "-")
	lower := strings.ToLower(tag)
	switch {
	case lower == "":
		return ""
	case lower == "zh-tw" || lower == "zh-hk" || lower == "zh-mo" || strings.HasPrefix(lower, "zh-hant"):
		return "zh-TW"
	case lower == "zh" || strings.HasPrefix(lower, "zh-"):
		return "zh-CN"
	}
	primary, _, _ := strings.Cut(lower, "-")
	if slices.Contains(Locales, primary) {
		return primary
	}
	return ""
}
