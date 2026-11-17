package camoufoxnative

import (
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// TestPromptHeadFitsPasteLimit 断言写入官网输入框的提示词开头是放得下的最长完整字符前缀，UTF-16 长度不超过官网把粘贴转为文本附件的 25000
func TestPromptHeadFitsPasteLimit(t *testing.T) {
	const emoji = "\U0001F600"
	cases := []struct {
		name   string
		prompt string
		units  int
	}{
		{name: "ascii", prompt: strings.Repeat("a", 30000), units: 25000},
		{name: "emoji head", prompt: emoji + strings.Repeat("a", 30000), units: 25000},
		{name: "surrogate pair at limit", prompt: "a" + strings.Repeat(emoji, 20000), units: 24999},
		{name: "exactly limit", prompt: emoji + strings.Repeat("a", 24998), units: 25000},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			head := promptHead(item.prompt)
			if !strings.HasPrefix(item.prompt, head) || !utf8.ValidString(head) {
				t.Fatal("开头不是提示词的完整字符前缀")
			}
			if got := len(utf16.Encode([]rune(head))); got != item.units {
				t.Fatalf("开头为 %d 个 UTF-16 码元，应为 %d", got, item.units)
			}
		})
	}
}
