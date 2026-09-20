package camoufoxnative

import "testing"

// TestNormalizePromptNewlines 覆盖官网 textarea 把写入值规范化为 LF 的比较场景
func TestNormalizePromptNewlines(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "lf", value: "a\nb", want: "a\nb"},
		{name: "crlf", value: "a\r\nb", want: "a\nb"},
		{name: "cr", value: "a\rb", want: "a\nb"},
		{name: "mixed", value: "a\r\nb\nc\rd", want: "a\nb\nc\nd"},
		{name: "empty", value: "", want: ""},
		{name: "no newline", value: "abc", want: "abc"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := normalizePromptNewlines(item.value); got != item.want {
				t.Fatalf("normalizePromptNewlines(%q) = %q, want %q", item.value, got, item.want)
			}
		})
	}
}

// TestNormalizePromptNewlinesMatchesTextareaValue 确认 CRLF 提示词与官网返回的 LF 值判定为同步
func TestNormalizePromptNewlinesMatchesTextareaValue(t *testing.T) {
	prompt := "line1\r\nline2\r\nline3"
	textarea := "line1\nline2\nline3"
	if prompt == textarea {
		t.Fatal("用例前提失效: CRLF 提示词不应与 textarea 值直接相等")
	}
	if normalizePromptNewlines(prompt) != normalizePromptNewlines(textarea) {
		t.Fatal("CRLF 提示词与 textarea LF 值应判定为已同步")
	}
}
