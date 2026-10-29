package i18n

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
)

// Console 是命令行与控制台日志使用的语言，取自系统语言
var Console = SystemLocale()

// Stdout 与 Stderr 是按 Console 翻译的标准输出与标准错误
var (
	Stdout = Writer(os.Stdout, Console)
	Stderr = Writer(os.Stderr, Console)
)

// jsonString 匹配 JSON 日志行中的字符串字面量
var jsonString = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

// writer 把每次写入的文字翻译为 locale 后写到 out
type writer struct {
	out    io.Writer
	locale string
}

// Writer 返回按 locale 翻译写入内容的写入器：JSON 日志行只翻译含汉字的字符串值，其余文字逐行翻译
func Writer(out io.Writer, locale string) io.Writer {
	if locale == Source {
		return out
	}
	return writer{out: out, locale: locale}
}

// Write 翻译 data 后写出，返回值按原始长度计数
func (current writer) Write(data []byte) (int, error) {
	text := string(data)
	if !hasHan(text) {
		return current.out.Write(data)
	}
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		if strings.HasPrefix(line, "{") && json.Valid([]byte(line)) {
			lines[index] = jsonString.ReplaceAllStringFunc(line, current.jsonValue)
			continue
		}
		lines[index] = Translate(current.locale, line)
	}
	if _, err := io.WriteString(current.out, strings.Join(lines, "\n")); err != nil {
		return 0, err
	}
	return len(data), nil
}

// jsonValue 翻译一个 JSON 字符串字面量并按不转义 HTML 的方式重新编码
func (current writer) jsonValue(literal string) string {
	var value string
	json.Unmarshal([]byte(literal), &value)
	if !hasHan(value) {
		return literal
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.Encode(Translate(current.locale, value))
	return strings.TrimSuffix(encoded.String(), "\n")
}
