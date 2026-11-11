package aistudio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
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

// fakeSequenceTransport 按顺序返回预设的受保护与普通 RPC 响应
type fakeSequenceTransport struct {
	responses []string
}

func (t *fakeSequenceTransport) next() (*RPCResponse, error) {
	body := ""
	if len(t.responses) > 0 {
		body = t.responses[0]
		t.responses = t.responses[1:]
	}
	return &RPCResponse{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{JSONProtobufContentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func (t *fakeSequenceTransport) Do(_ context.Context, rpc RPCRequest) (*RPCResponse, error) {
	switch rpc.Method {
	case "CreateApplet":
		return &RPCResponse{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{JSONProtobufContentType}},
			Body:       io.NopCloser(strings.NewReader(`["applet-1"]`)),
		}, nil
	case assistantStreamMethod:
		return t.next()
	default:
		return &RPCResponse{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{JSONProtobufContentType}},
			Body:       io.NopCloser(strings.NewReader(`[]`)),
		}, nil
	}
}

func (t *fakeSequenceTransport) DoProtected(_ context.Context, _ GenerateRequest, rpc RPCRequest) (*RPCResponse, error) {
	if rpc.Method == assistantOfflineMethod {
		return &RPCResponse{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{JSONProtobufContentType}},
			Body:       io.NopCloser(strings.NewReader(`["gen-1","applet-1"]`)),
		}, nil
	}
	return t.next()
}

// TestGeneratePartialTextMalformedFunctionCallContinuation 验证已输出纯文本后遇到 malformed_function_call 时续写补出函数调用、去重重复前缀并合并用量
func TestGeneratePartialTextMalformedFunctionCallContinuation(t *testing.T) {
	malformedCode := int64(10)
	stopCode := int64(1)
	frame := func(parts [][]any, code *int64, usage *Usage) []any {
		candidate := []any{[]any{parts, "model"}}
		if code != nil {
			candidate = append(candidate, *code)
		}
		item := make([]any, 3)
		item[0] = []any{candidate}
		if usage != nil {
			item[2] = []any{usage.InputTokens, usage.OutputTokens, usage.TotalTokens}
		}
		return item
	}
	streamBody := func(frames ...[]any) string {
		raw, err := json.Marshal([]any{frames, []any{int64(0)}})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	callPart := make([]any, 11)
	callPart[10] = []any{"get_weather", encodeWireStruct(map[string]any{"city": "Tokyo"}), "call_1"}
	sigPart := make([]any, 15)
	sigPart[1] = "Let me check the weather. "
	sigPart[14] = "sig-kept"

	round1 := streamBody(
		frame([][]any{{nil, "Let me check the weather. "}}, nil, nil),
		frame([][]any{{nil, "Hold on"}}, &malformedCode, &Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}),
	)
	round2 := streamBody(
		frame([][]any{sigPart}, nil, nil),
		frame([][]any{{nil, "Hold on, calling tool now."}}, nil, nil),
		frame([][]any{callPart}, &stopCode, &Usage{InputTokens: 25, OutputTokens: 8, TotalTokens: 33}),
	)
	transport := &fakeSequenceTransport{responses: []string{round1, round2}}
	client := NewClient(ClientOptions{Protected: transport})
	model := Model{
		ID: "gemini-test", Name: "Gemini Test", Methods: []string{"generateContent"}, OutputTokenLimit: 8192,
		Capabilities: map[string]bool{"chat_model": true, "function_declarations": true, "thinking": true},
	}
	client.setModelCatalog("acc@example.com", modelCatalog{
		models:  []Model{model},
		entries: map[string]modelEntry{model.ID: {model: model, defaults: GenerationDefaults{MaxOutputTokens: 8192, Thinking: true}}},
	})
	stream, err := client.Generate(context.Background(), GenerateRequest{
		ID: "req-1", AccountID: "acc@example.com", Model: model.ID,
		Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "Weather in Tokyo?"}}}},
		Tools: Tools{
			Functions:  []FunctionDeclaration{{Name: "get_weather", Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`)}},
			ToolConfig: ToolConfig{Mode: "required"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var signatures []string
	var calls []FunctionCall
	var usage *Usage
	var finish string
	for event := range stream {
		switch event.Kind {
		case EventText:
			text.WriteString(event.Text)
			if event.ThoughtSignature != "" {
				signatures = append(signatures, event.ThoughtSignature)
			}
		case EventThoughtSignature:
			signatures = append(signatures, event.ThoughtSignature)
		case EventToolCall:
			calls = append(calls, *event.FunctionCall)
		case EventUsage:
			value := *event.Usage
			usage = &value
		case EventFinish:
			finish = event.FinishReason
		case EventError:
			t.Fatal(event.Err)
		}
	}
	if text.String() != "Let me check the weather. Hold on, calling tool now." {
		t.Fatalf("text=%q", text.String())
	}
	if !reflect.DeepEqual(signatures, []string{"sig-kept"}) {
		t.Fatalf("signatures=%v", signatures)
	}
	if len(calls) != 1 || calls[0].Name != "get_weather" {
		t.Fatalf("calls=%+v", calls)
	}
	if usage == nil || usage.InputTokens != 24 || usage.OutputTokens != 13 || usage.TotalTokens != 37 {
		t.Fatalf("usage=%+v", usage)
	}
	if finish != "stop" {
		t.Fatalf("finish=%q", finish)
	}
}

// TestAssistantFunctionCallRepairAndContinuation 验证写代码助手修复 JSON 尾随逗号，并在调用块损坏或调用被禁止的工作区工具时在同一应用内续出客户端函数调用
func TestAssistantFunctionCallRepairAndContinuation(t *testing.T) {
	step := func(number, status int64, text string, action []any) []any {
		item := make([]any, 16)
		if text != "" {
			item[0] = []any{[]any{[]any{[]any{nil, text}}, "model"}}
		}
		item[9] = action
		item[11] = number
		item[12] = status
		return item
	}
	actionTool := func(tag, questions string) []any {
		action := make([]any, 22)
		action[21] = []any{"Tool", nil, nil, [][]string{{"tag", tag}, {"questions", questions}}}
		return action
	}
	streamPayload := func(steps ...[]any) string {
		raw, err := json.Marshal([]any{[]any{[]any{nil, nil, steps, "applet-1"}}, []any{int64(0)}})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	callQuestion, _ := json.Marshal([]map[string]any{{"question": assistantCallQuestion}})

	for _, test := range []struct {
		name      string
		responses []string
	}{
		{
			name: "trailing comma repair",
			responses: []string{
				streamPayload(step(1, 1, "Checking file.\n```function_calls\n[{\"name\":\"read_file\",\"arguments\":{\"path\":\"config.yaml\",},},]\n```\n", nil)),
			},
		},
		{
			name: "malformed function_calls block",
			responses: []string{
				streamPayload(step(1, 1, "Checking file.\n```function_calls\n[{\"name\":\"read_file\",\"arguments\":{bad}]\n```\n", nil)),
				streamPayload(step(1, 1, "Checking file.\n```function_calls\n[{\"name\":\"read_file\",\"arguments\":{\"path\":\"config.yaml\"}}]\n```\n", nil)),
			},
		},
		{
			name: "missing function_calls before ask_question",
			responses: []string{
				streamPayload(step(1, 1, "Checking file.\n", actionTool(assistantQuestionTool, string(callQuestion)))),
				streamPayload(step(1, 1, "Checking file.\n```function_calls\n[{\"name\":\"read_file\",\"arguments\":{\"path\":\"config.yaml\"}}]\n```\n", nil)),
			},
		},
		{
			name: "forbidden workspace tool",
			responses: []string{
				streamPayload(
					step(1, 1, "Checking file.\n", nil),
					step(2, 1, "", actionTool("run_command", "ls")),
					step(3, 1, "BOGUS WORKSPACE OUTPUT", nil),
				),
				streamPayload(step(1, 1, "Checking file.\n```function_calls\n[{\"name\":\"read_file\",\"arguments\":{\"path\":\"config.yaml\"}}]\n```\n", nil)),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &fakeSequenceTransport{responses: test.responses}
			baseModel := Model{
				ID: "gemini-3.8-pro", Name: "Gemini 3.8 Pro", Methods: []string{"generateContent"},
				Capabilities: map[string]bool{"chat_model": true, "code_assistant": true},
			}
			entry := assistantModelEntry(modelEntry{
				model:    baseModel,
				defaults: GenerationDefaults{MaxOutputTokens: 65536, Thinking: true, ThinkingLevel: true, ThinkingLevels: []int64{1, 2, 3, 4}},
			})
			account := &Account{
				ID: "acc@example.com", Config: AccountConfig{Enabled: true}, State: AccountReady,
				Models: []Model{baseModel, entry.model}, modelAccessGeneration: 1,
			}
			pool := NewAccountPool([]*Account{account}, 1)
			lease, _, err := pool.TryAcquireFor(context.Background(), AccountSelection{ModelID: entry.model.ID, Method: "generateContent"})
			if err != nil || lease == nil {
				t.Fatalf("lease=%v err=%v", lease, err)
			}
			defer func() { _ = lease.Release() }()
			client := NewClient(ClientOptions{Transport: transport, Protected: transport})
			client.setModelCatalog(account.ID, modelCatalog{
				models:  []Model{baseModel, entry.model},
				entries: map[string]modelEntry{entry.model.ID: entry},
			})
			stream, err := client.Generate(ContextWithAccountLease(context.Background(), lease), GenerateRequest{
				ID: fmt.Sprintf("req-%s", test.name), AccountID: account.ID, Model: entry.model.ID,
				Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "Read config.yaml"}}}},
				Tools:    Tools{Functions: []FunctionDeclaration{{Name: "read_file"}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			var text strings.Builder
			var calls []*FunctionCall
			var finish string
			for event := range stream {
				switch event.Kind {
				case EventText:
					text.WriteString(event.Text)
				case EventToolCall:
					calls = append(calls, event.FunctionCall)
				case EventFinish:
					finish = event.FinishReason
				case EventError:
					t.Fatal(event.Err)
				}
			}
			if text.String() != "Checking file.\n" {
				t.Fatalf("text=%q", text.String())
			}
			if len(calls) != 1 || calls[0].Name != "read_file" {
				t.Fatalf("calls=%+v", calls)
			}
			var gotArgs bytes.Buffer
			if err := json.Compact(&gotArgs, calls[0].Arguments); err != nil || gotArgs.String() != `{"path":"config.yaml"}` {
				t.Fatalf("args=%s err=%v", string(calls[0].Arguments), err)
			}
			if finish != "stop" {
				t.Fatalf("finish=%q", finish)
			}
		})
	}
}
