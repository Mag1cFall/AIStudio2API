package api

import (
	"encoding/json"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// TestOpenAIEmptyMessagesFiltered 验证 OpenAI 空历史消息的过滤
func TestOpenAIEmptyMessagesFiltered(t *testing.T) {
	req := chatRequest{
		Model: "gemini-3.8-flash",
		Messages: []chatMessage{
			{Role: "system", Content: json.RawMessage(`"you are an assistant"`)},
			{Role: "user", Content: json.RawMessage(`"hello"`)},
			{Role: "assistant", Content: json.RawMessage(`""`)},
			{Role: "assistant", Content: json.RawMessage(`null`)},
			{Role: "assistant", Content: json.RawMessage(`[]`)},
			{Role: "assistant", Content: json.RawMessage(`[{"type":"text","text":""}]`)},
			{Role: "user", Content: json.RawMessage(`"how are you?"`)},
		},
	}
	genReq, err := req.toGenerateRequest("test-id")
	if err != nil {
		t.Fatalf("toGenerateRequest failed: %v", err)
	}
	if len(genReq.Contents) != 2 {
		t.Fatalf("expected 2 valid contents (2 user messages), got %d: %#v", len(genReq.Contents), genReq.Contents)
	}
	if genReq.Contents[0].Parts[0].Text != "hello" || genReq.Contents[1].Parts[0].Text != "how are you?" {
		t.Fatalf("unexpected contents: %#v", genReq.Contents)
	}
}

// TestAnthropicEmptyMessagesFiltered 验证 Anthropic 空历史消息的过滤
func TestAnthropicEmptyMessagesFiltered(t *testing.T) {
	req := anthropicRequest{
		Model: "gemini-3.8-flash",
		Messages: []anthropicMessage{
			{Role: "user", Content: json.RawMessage(`"hello"`)},
			{Role: "assistant", Content: json.RawMessage(`""`)},
			{Role: "assistant", Content: json.RawMessage(`null`)},
			{Role: "assistant", Content: json.RawMessage(`[]`)},
			{Role: "assistant", Content: json.RawMessage(`[{"type":"text","text":""}]`)},
			{Role: "user", Content: json.RawMessage(`"world"`)},
		},
	}
	genReq, err := req.toGenerateRequest("test-id")
	if err != nil {
		t.Fatalf("toGenerateRequest failed: %v", err)
	}
	if len(genReq.Contents) != 2 {
		t.Fatalf("expected 2 valid contents, got %d: %#v", len(genReq.Contents), genReq.Contents)
	}
	if genReq.Contents[0].Parts[0].Text != "hello" || genReq.Contents[1].Parts[0].Text != "world" {
		t.Fatalf("unexpected contents: %#v", genReq.Contents)
	}
}

// TestMessageWhitespacePreserved 验证字符串与内容块保留空白文本
func TestMessageWhitespacePreserved(t *testing.T) {
	for name, convert := range map[string]func(json.RawMessage) ([]aistudio.Part, error){"openai": openAIContentParts, "anthropic": anthropicParts} {
		for _, raw := range []json.RawMessage{json.RawMessage(`" \t "`), json.RawMessage(`[{"type":"text","text":" \t "}]`)} {
			parts, err := convert(raw)
			if err != nil || len(parts) != 1 || parts[0].Text != " \t " {
				t.Errorf("%s whitespace=%#v err=%v", name, parts, err)
			}
		}
	}
}

// TestOpenAIToolMessageNameInference 验证 tool 消息缺少 name 时自动关联前置工具调用名称
func TestOpenAIToolMessageNameInference(t *testing.T) {
	req := chatRequest{
		Model: "gemini-3.8-flash",
		Messages: []chatMessage{
			{
				Role: "assistant",
				ToolCalls: []openAIToolCall{
					{
						ID:   "call_abc_1",
						Type: "function",
						Function: struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						}{
							Name:      "query_image_presets",
							Arguments: `{"category":"preset"}`,
						},
					},
				},
			},
			{
				Role:       "tool",
				ToolCallID: "call_mismatched_xyz",
				Content:    json.RawMessage(`"ok"`),
			},
		},
	}
	genReq, err := req.toGenerateRequest("test-id")
	if err != nil {
		t.Fatalf("toGenerateRequest failed: %v", err)
	}
	if len(genReq.Contents) != 2 {
		t.Fatalf("expected 2 contents, got %d", len(genReq.Contents))
	}
	wire, err := aistudio.EncodeGenerateContentRequest(genReq, aistudio.GenerationDefaults{MaxOutputTokens: 1024}, aistudio.RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var root []any
	if err := json.Unmarshal(wire, &root); err != nil {
		t.Fatal(err)
	}
	result := root[1].([]any)[1].([]any)[0].([]any)[0].([]any)[11].([]any)
	if result[0] != "query_image_presets" || result[2] != "call_mismatched_xyz" {
		t.Fatalf("unexpected function result: %#v", result)
	}
}
