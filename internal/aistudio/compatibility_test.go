package aistudio

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestAgentToolSchemaCompatibility 验证常见 Agent 工具 Schema 的稳定转换规则
func TestAgentToolSchemaCompatibility(t *testing.T) {
	t.Run("string const", func(t *testing.T) {
		wire, err := encodeJSONSchema(json.RawMessage(`{
			"properties":{"mode":{"const":"agent_probe","title":"Mode","type":"string"}},
			"required":["mode"],"title":"record_literal_args","type":"object"
		}`))
		if err != nil {
			t.Fatal(err)
		}
		if len(wire) <= 6 || wire[0] != int64(6) {
			t.Fatalf("schema wire=%#v", wire)
		}
		properties, ok := wire[6].([]any)
		if !ok || len(properties) != 1 {
			t.Fatalf("schema properties=%#v", wire[6])
		}
		entry, ok := properties[0].([]any)
		if !ok || len(entry) != 2 || entry[0] != "mode" {
			t.Fatalf("schema property=%#v", properties[0])
		}
		mode, ok := entry[1].([]any)
		if !ok || len(mode) <= 4 || mode[0] != int64(1) {
			t.Fatalf("mode schema=%#v", entry[1])
		}
		values, ok := mode[4].([]string)
		if !ok || len(values) != 1 || values[0] != "agent_probe" {
			t.Fatalf("mode enum=%#v", mode[4])
		}
	})

	t.Run("nested const", func(t *testing.T) {
		wire, err := encodeJSONSchema(json.RawMessage(`{"anyOf":[{"const":"fast"},{"const":"slow"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if len(wire) <= 17 || wire[0] != int64(1) {
			t.Fatalf("schema wire=%#v", wire)
		}
		variants, ok := wire[17].([]any)
		if !ok || len(variants) != 2 {
			t.Fatalf("schema variants=%#v", wire[17])
		}
		for index, want := range []string{"fast", "slow"} {
			variant, ok := variants[index].([]any)
			if !ok || len(variant) <= 4 || variant[0] != int64(1) {
				t.Fatalf("schema variant=%#v", variants[index])
			}
			values, ok := variant[4].([]string)
			if !ok || len(values) != 1 || values[0] != want {
				t.Fatalf("schema variant enum=%#v", variant[4])
			}
		}
	})

	t.Run("non-string const", func(t *testing.T) {
		for _, raw := range []string{`{"const":1}`, `{"const":true}`, `{"const":null}`} {
			if _, err := encodeJSONSchema(json.RawMessage(raw)); err == nil || !strings.Contains(err.Error(), "const") {
				t.Fatalf("schema=%s error=%v", raw, err)
			}
		}
	})
}

// TestFunctionCallThoughtSignature 验证历史工具调用的签名补齐与原值保留
func TestFunctionCallThoughtSignature(t *testing.T) {
	for _, test := range []struct {
		name      string
		signature string
		want      string
	}{
		{name: "missing", want: "skip_thought_signature_validator"},
		{name: "present", signature: "real-signature", want: "real-signature"},
	} {
		t.Run(test.name, func(t *testing.T) {
			wire, err := encodePart(Part{FunctionCall: &FunctionCall{
				ID: "call_123", Name: "fixture", Arguments: json.RawMessage(`{"value":"ok"}`),
				ThoughtSignature: test.signature,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if len(wire) <= 14 || wire[14] != test.want {
				t.Fatalf("thought signature=%#v", wire)
			}
		})
	}
}

// TestRPCErrorCompatibility 验证真实错误帧解码和 Drive 授权边界
func TestRPCErrorCompatibility(t *testing.T) {
	t.Run("direct", func(t *testing.T) {
		err := DecodeRPCError("GenerateAccessToken", http.StatusUnauthorized, []byte(
			`[16,"OAuth error: unauthorized_client.",[["type.googleapis.com/google.internal.alkali.applications.makersuite.v1.AiStudioErrorDetails",[null,[]]]]]`,
		))
		if err.Code != 16 || err.Message != "OAuth error: unauthorized_client." || !driveAuthorizationMissing(err) {
			t.Fatalf("RPC error=%+v", err)
		}
	})

	t.Run("wrapped", func(t *testing.T) {
		err := DecodeRPCError("GenerateContent", http.StatusForbidden, []byte(
			`[null,[7,"Permission denied",[["type.googleapis.com/google.rpc.ErrorInfo",["DENIED","google.com",[["reason","account_permission"]]]]]]]`,
		))
		if err.Code != 7 || err.Message != "Permission denied" || err.Metadata["reason"] != "account_permission" {
			t.Fatalf("RPC error=%+v", err)
		}
	})

	t.Run("login failure", func(t *testing.T) {
		err := DecodeRPCError("GenerateAccessToken", http.StatusUnauthorized, []byte(
			`[16,"Request had invalid authentication credentials."]`,
		))
		if driveAuthorizationMissing(err) {
			t.Fatalf("login error classified as Drive authorization: %+v", err)
		}
	})

	t.Run("different scope", func(t *testing.T) {
		for _, err := range []*RPCError{
			{Method: "GenerateContent", StatusCode: http.StatusUnauthorized, Code: 16, Message: "OAuth error: unauthorized_client."},
			{Method: "GenerateAccessToken", StatusCode: http.StatusForbidden, Code: 16, Message: "OAuth error: unauthorized_client."},
		} {
			if driveAuthorizationMissing(err) {
				t.Fatalf("unrelated error classified as Drive authorization: %+v", err)
			}
		}
	})
}

// TestEncodeContentsEmptyPartsFilter 验证多轮历史中空 parts 内容被自动忽略而不触发编码校验失败
func TestEncodeContentsEmptyPartsFilter(t *testing.T) {
	contents := []Content{
		{Role: RoleUser, Parts: []Part{{Text: "hello"}}},
		{Role: RoleAssistant, Parts: nil},
		{Role: RoleAssistant, Parts: []Part{}},
		{Role: RoleUser, Parts: []Part{{Text: "world"}}},
	}
	wire, err := encodeContents(contents)
	if err != nil {
		t.Fatalf("encodeContents failed: %v", err)
	}
	if len(wire) != 2 {
		t.Fatalf("expected 2 valid wire contents, got %d: %#v", len(wire), wire)
	}
}

// TestAttachYouTubeMediaPreservesText 验证普通文本与非空白字符在附加 YouTube 媒体时不被截断为空
func TestAttachYouTubeMediaPreservesText(t *testing.T) {
	content := Content{
		Role: RoleUser,
		Parts: []Part{
			{Text: "  normal text with space  "},
			{Text: "https://www.youtube.com/watch?v=dQw4w9WgXcQ"},
		},
	}
	attached := attachYouTubeMedia(content)
	if len(attached.Parts) != 2 {
		t.Fatalf("expected 2 parts (1 text + 1 external media), got %d: %#v", len(attached.Parts), attached.Parts)
	}
	if attached.Parts[0].Text != "  normal text with space  " {
		t.Fatalf("unexpected text: %q", attached.Parts[0].Text)
	}
	if attached.Parts[1].ExternalMedia == nil {
		t.Fatalf("expected external media part")
	}
	for _, text := range []string{"  https://example.com/article  ", "  https://youtube.com/playlist?list=abc  "} {
		got := attachYouTubeMedia(Content{Role: RoleUser, Parts: []Part{{Text: text}}})
		if len(got.Parts) != 1 || got.Parts[0].Text != text {
			t.Errorf("ordinary text changed: %#v", got)
		}
	}
}

// TestFunctionResultNameInference 验证工具结果的唯一关联与原始输入保留
func TestFunctionResultNameInference(t *testing.T) {
	call := func(id, name string) Part {
		return Part{FunctionCall: &FunctionCall{ID: id, Name: name, Arguments: json.RawMessage(`{}`)}}
	}
	result := func(id string) Part {
		return Part{FunctionResult: &FunctionResult{ID: id, Content: json.RawMessage(`{"ok":true}`)}}
	}
	for _, test := range []struct {
		name     string
		contents []Content
		want     string
	}{
		{"matching id", []Content{{Role: RoleAssistant, Parts: []Part{call("one", "lookup"), call("two", "weather")}}, {Role: RoleTool, Parts: []Part{result("one")}}}, "lookup"},
		{"unique mismatched id", []Content{{Role: RoleAssistant, Parts: []Part{call("one", "lookup")}}, {Role: RoleTool, Parts: []Part{result("opaque-id")}}}, "lookup"},
		{"missing id", []Content{{Role: RoleAssistant, Parts: []Part{call("one", "lookup")}}, {Role: RoleTool, Parts: []Part{result("")}}}, "lookup"},
		{"remaining call", []Content{{Role: RoleAssistant, Parts: []Part{call("one", "lookup"), call("two", "weather")}}, {Role: RoleTool, Parts: []Part{result("one")}}, {Role: RoleTool, Parts: []Part{result("opaque-id")}}}, "weather"},
		{"split calls", []Content{{Role: RoleAssistant, Parts: []Part{call("one", "lookup")}}, {Role: RoleAssistant, Parts: []Part{call("two", "weather")}}, {Role: RoleTool, Parts: []Part{result("two")}}, {Role: RoleTool, Parts: []Part{result("one")}}}, "lookup"},
		{"consumed call", []Content{{Role: RoleAssistant, Parts: []Part{call("one", "lookup")}}, {Role: RoleTool, Parts: []Part{result("one")}}, {Role: RoleTool, Parts: []Part{result("call_other")}}}, ""},
		{"user result", []Content{{Role: RoleAssistant, Parts: []Part{call("one", "lookup")}}, {Role: RoleUser, Parts: []Part{result("one")}}}, "lookup"},
		{"ambiguous id", []Content{{Role: RoleAssistant, Parts: []Part{call("one", "lookup"), call("two", "weather")}}, {Role: RoleTool, Parts: []Part{result("call_other")}}}, ""},
		{"orphan id", []Content{{Role: RoleTool, Parts: []Part{result("opaque-id")}}}, ""},
		{"closed turn", []Content{{Role: RoleAssistant, Parts: []Part{call("one", "lookup")}}, {Role: RoleTool, Parts: []Part{result("one")}}, {Role: RoleAssistant, Parts: []Part{{Text: "done"}}}, {Role: RoleTool, Parts: []Part{result("call_other")}}}, ""},
		{"new turn", []Content{{Role: RoleAssistant, Parts: []Part{call("one", "lookup")}}, {Role: RoleUser, Parts: []Part{{Text: "new question"}}}, {Role: RoleTool, Parts: []Part{result("call_other")}}}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			wire, err := encodeContents(test.contents)
			if test.want == "" {
				if err == nil {
					t.Fatalf("unresolved result accepted: %#v", wire)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			last := wire[len(wire)-1].([]any)[0].([]any)[0].([]any)[11].([]any)
			if last[0] != test.want {
				t.Fatalf("name=%v want=%s", last[0], test.want)
			}
			if test.contents[len(test.contents)-1].Parts[0].FunctionResult.Name != "" {
				t.Fatal("input mutated")
			}
		})
	}
}
