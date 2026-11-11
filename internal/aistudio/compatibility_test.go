package aistudio

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
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

// TestRPCErrorCompatibility 验证真实错误帧的解码与 Drive 授权缺失的判定
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

// TestAttachYouTubeMediaVideoIDs 验证文本中只有 11 位视频 ID 的链接转为视频，链接在汉字与全角标点处结束，其余链接保留为文本
func TestAttachYouTubeMediaVideoIDs(t *testing.T) {
	for _, test := range []struct{ text, media, rest string }{
		{"用一句话总结https://youtu.be/jNQXAC9IVRw这个视频", "https://www.youtube.com/watch?v=jNQXAC9IVRw", "用一句话总结这个视频"},
		{"看看 https://www.youtube.com/watch?v=jNQXAC9IVRw，讲了什么", "https://www.youtube.com/watch?v=jNQXAC9IVRw", "看看 ，讲了什么"},
		{"Embed players with `https://www.youtube.com/embed/${videoId}`.", "", "Embed players with `https://www.youtube.com/embed/${videoId}`."},
		{"Template: https://www.youtube.com/watch?v=VIDEO_ID", "", "Template: https://www.youtube.com/watch?v=VIDEO_ID"},
		{"See https://youtu.be/abc", "", "See https://youtu.be/abc"},
	} {
		got := attachYouTubeMedia(Content{Role: RoleUser, Parts: []Part{{Text: test.text}}})
		var media, rest string
		for _, part := range got.Parts {
			if part.ExternalMedia != nil {
				media = part.ExternalMedia.URL
			}
			rest += part.Text
		}
		if media != test.media || rest != test.rest {
			t.Errorf("%q: media=%q text=%q", test.text, media, rest)
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

// TestAssistantFailureQuotaCooldown 验证写代码助手步骤失败时区分通用额度与每用户额度并按套餐限额递增冷却
func TestAssistantFailureQuotaCooldown(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	t.Run("general quota", func(t *testing.T) {
		raw := json.RawMessage(`["Failed to generate content",["Failed to generate content",null,null,true,false]]`)
		err, decodeErr := decodeAssistantFailure(raw, "$[0][2][0][5]", raw)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if err.StatusCode != http.StatusTooManyRequests || err.Code != 8 || err.PerUserQuota {
			t.Fatalf("failure=%+v", err)
		}
		cooldown, ok := QuotaCooldownForError(err, now)
		if !ok || cooldown.Kind != "短期限额" || cooldown.Escalate || !cooldown.Until.Equal(now.Add(time.Minute)) {
			t.Fatalf("cooldown=%+v ok=%v", cooldown, ok)
		}
	})

	t.Run("per-user quota escalates", func(t *testing.T) {
		raw := json.RawMessage(`["Failed to generate content",["Failed to generate content",null,null,false,true]]`)
		err, decodeErr := decodeAssistantFailure(raw, "$[0][2][0][5]", raw)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if err.StatusCode != http.StatusTooManyRequests || err.Code != 8 || !err.PerUserQuota {
			t.Fatalf("failure=%+v", err)
		}
		cooldown, ok := QuotaCooldownForError(err, now)
		if !ok || cooldown.Kind != PlanLimitKind || !cooldown.Escalate || !RequestQuotaCooldown(cooldown.Reason) {
			t.Fatalf("cooldown=%+v ok=%v", cooldown, ok)
		}
		var state CooldownState
		exists := false
		current := now
		for _, want := range []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour} {
			state = planLimitCooldown(state, exists, current, cooldown.Reason)
			exists = true
			if got := state.Until.Sub(current); got != want {
				t.Fatalf("backoff=%s want=%s state=%+v", got, want, state)
			}
			current = state.Until
		}
	})
}

// TestRepairJSONStringsTrailingComma 验证调用块 JSON 去掉 } 与 ] 之前的尾随逗号后可以解码，字符串中的逗号保持原样
func TestRepairJSONStringsTrailingComma(t *testing.T) {
	var calls []map[string]any
	if err := json.Unmarshal([]byte(repairJSONStrings("[{\"name\": \"read_file\", \"arguments\": {\"path\": \"a,}b.txt\",},},\n]")), &calls); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0]["name"] != "read_file" || calls[0]["arguments"].(map[string]any)["path"] != "a,}b.txt" {
		t.Fatalf("calls=%v", calls)
	}
}
