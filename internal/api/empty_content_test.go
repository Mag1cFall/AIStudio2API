package api

import (
	"encoding/json"
	"testing"
)

// TestOpenAIEmptyMessagesFiltered 验证 OpenAI 兼容协议中包含空字符串、null 或纯空白的消息自动跳过
func TestOpenAIEmptyMessagesFiltered(t *testing.T) {
	req := chatRequest{
		Model: "gemini-3.8-flash",
		Messages: []chatMessage{
			{Role: "system", Content: json.RawMessage(`"you are an assistant"`)},
			{Role: "user", Content: json.RawMessage(`"hello"`)},
			{Role: "assistant", Content: json.RawMessage(`""`)},
			{Role: "assistant", Content: json.RawMessage(`null`)},
			{Role: "assistant", Content: json.RawMessage(`"   "`)},
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

// TestAnthropicEmptyMessagesFiltered 验证 Anthropic 协议中包含空字符串或纯空白的消息自动跳过
func TestAnthropicEmptyMessagesFiltered(t *testing.T) {
	req := anthropicRequest{
		Model: "gemini-3.8-flash",
		Messages: []anthropicMessage{
			{Role: "user", Content: json.RawMessage(`"hello"`)},
			{Role: "assistant", Content: json.RawMessage(`""`)},
			{Role: "assistant", Content: json.RawMessage(`"   "`)},
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
	toolPart := genReq.Contents[1].Parts[0]
	if toolPart.FunctionResult == nil {
		t.Fatalf("expected FunctionResult part")
	}
	if toolPart.FunctionResult.Name != "query_image_presets" {
		t.Fatalf("expected inferred name 'query_image_presets', got %q", toolPart.FunctionResult.Name)
	}
}
