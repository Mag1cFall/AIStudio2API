package aistudio

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// listModelRowWithField79 构造带指定 field 79 的最小 ListModels 模型行用于核对官网路由换算
func listModelRowWithField79(id string, field79 any, extraCaps ...int64) []any {
	row := make([]any, 79)
	row[0] = "models/" + id
	row[3] = id
	row[6] = 65536
	row[7] = []any{"generateContent", "countTokens"}
	caps := append([]int64{1, 8, 9, 10, 12, 13, 25, 52, 64}, extraCaps...)
	row[64] = caps
	row[71] = []any{nil, nil, nil, nil, nil, 3, []any{1, 2, 3, 5}}
	row[78] = field79
	return row
}

// TestField79InteractionRoutingMatrix 按官网 m=_b 四个谓词（_.Ol/_.Pl/Rl/_.Sl）核对 field 79 全部 kind 与 background 组合的选路、能力标记与编码信封目标槽位
func TestField79InteractionRoutingMatrix(t *testing.T) {
	cases := []struct {
		name           string
		field79        any
		wantMethod     InteractionMethod
		wantTarget     InteractionTarget
		wantInteract   bool
		wantStream     bool
		wantTask       bool
		wantAgent      bool
		wantModel      bool
		wantAgentModel string
	}{
		{
			name:       "nil field79 uses GenerateContent",
			field79:    nil,
			wantMethod: InteractionMethodGenerateContent,
			wantTarget: InteractionTargetNone,
		},
		{
			name:       "empty field79 uses GenerateContent",
			field79:    []any{},
			wantMethod: InteractionMethodGenerateContent,
			wantTarget: InteractionTargetNone,
		},
		{
			name:       "kind=0 background=false uses GenerateContent",
			field79:    []any{nil, nil, nil, 0},
			wantMethod: InteractionMethodGenerateContent,
			wantTarget: InteractionTargetNone,
		},
		{
			name:       "kind=nil background=true uses GenerateContent",
			field79:    []any{nil, nil, 1},
			wantMethod: InteractionMethodGenerateContent,
			wantTarget: InteractionTargetNone,
		},
		{
			name:       "kind=0 background=true uses GenerateContent",
			field79:    []any{nil, nil, 1, 0},
			wantMethod: InteractionMethodGenerateContent,
			wantTarget: InteractionTargetNone,
		},
		{
			name:       "unknown kind=3 background=false uses GenerateContent",
			field79:    []any{nil, nil, nil, 3},
			wantMethod: InteractionMethodGenerateContent,
			wantTarget: InteractionTargetNone,
		},
		{
			name:       "unknown kind=3 background=true uses GenerateContent",
			field79:    []any{nil, nil, 1, 3},
			wantMethod: InteractionMethodGenerateContent,
			wantTarget: InteractionTargetNone,
		},
		{
			name:         "kind=2 background=false routes to CreateInteractionStream model target",
			field79:      []any{nil, nil, nil, 2},
			wantMethod:   InteractionMethodCreateInteractionStream,
			wantTarget:   InteractionTargetModel,
			wantInteract: true,
			wantStream:   true,
			wantModel:    true,
		},
		{
			name:         "kind=2 background=true routes to CreateInteraction model target",
			field79:      []any{nil, nil, 1, 2},
			wantMethod:   InteractionMethodCreateInteraction,
			wantTarget:   InteractionTargetModel,
			wantInteract: true,
			wantTask:     true,
			wantModel:    true,
		},
		{
			name:         "kind=1 background=false routes to CreateInteractionStream agent target",
			field79:      []any{nil, nil, nil, 1},
			wantMethod:   InteractionMethodCreateInteractionStream,
			wantTarget:   InteractionTargetAgent,
			wantInteract: true,
			wantStream:   true,
			wantAgent:    true,
		},
		{
			name:           "kind=1 background=true routes to CreateInteraction agent target",
			field79:        []any{nil, nil, 1, 1, 1, "models/gemini-3.8-flash"},
			wantMethod:     InteractionMethodCreateInteraction,
			wantTarget:     InteractionTargetAgent,
			wantInteract:   true,
			wantTask:       true,
			wantAgent:      true,
			wantAgentModel: "gemini-3.8-flash",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal([]any{[]any{listModelRowWithField79("gemini-4-hydrogen", tc.field79)}})
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := parseModelCatalog(strings.NewReader(string(raw)))
			if err != nil {
				t.Fatal(err)
			}
			entry := catalog.entries["gemini-4-hydrogen"]
			route := interactionRouting(entry.model, entry.defaults, ChannelPlayground)
			if route.Method != tc.wantMethod || route.Target != tc.wantTarget {
				t.Fatalf("route=%+v want={Method:%s Target:%s}", route, tc.wantMethod, tc.wantTarget)
			}
			if entry.model.Capabilities["interactions_api"] != tc.wantInteract ||
				entry.model.Capabilities["interaction_stream"] != tc.wantStream ||
				entry.model.Capabilities["interaction_task"] != tc.wantTask ||
				entry.model.Capabilities["interaction_agent"] != tc.wantAgent ||
				entry.model.Capabilities["interaction_model"] != tc.wantModel {
				t.Fatalf("caps=%v", entry.model.Capabilities)
			}
			if entry.defaults.InteractionStream != tc.wantStream ||
				entry.defaults.InteractionTask != tc.wantTask ||
				entry.defaults.InteractionAgent != tc.wantAgent ||
				entry.defaults.InteractionModel != tc.wantModel {
				t.Fatalf("defaults=%+v", entry.defaults)
			}
			if entry.model.AgentModel != tc.wantAgentModel {
				t.Fatalf("agentModel=%q want=%q", entry.model.AgentModel, tc.wantAgentModel)
			}
			if tc.wantInteract {
				body, _, _, err := encodeInteractionRequest(GenerateRequest{
					Model:    "gemini-4-hydrogen",
					Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "hello"}}}},
				}, entry.model, entry.defaults, route)
				if err != nil {
					t.Fatal(err)
				}
				var wire []any
				if err := json.Unmarshal(body, &wire); err != nil {
					t.Fatal(err)
				}
				if tc.wantStream && (wire[0] != float64(1) || wire[2] != nil) {
					t.Fatalf("stream envelope=%v", wire[:3])
				}
				if tc.wantTask && (wire[0] != nil || wire[2] != float64(1)) {
					t.Fatalf("task envelope=%v", wire[:3])
				}
				interaction := wire[3].([]any)
				if tc.wantModel && (interaction[17] == nil || interaction[18] != nil) {
					t.Fatalf("expected model target in index 17, got [17]=%v [18]=%v", interaction[17], interaction[18])
				}
				if tc.wantAgent && (interaction[17] != nil || interaction[18] == nil) {
					t.Fatalf("expected agent target in index 18, got [17]=%v [18]=%v", interaction[17], interaction[18])
				}
			}
			// Build 通道始终走 GenerateContent
			if buildRoute := interactionRouting(entry.model, entry.defaults, ChannelBuild); buildRoute.Method != InteractionMethodGenerateContent || buildRoute.Target != InteractionTargetNone {
				t.Fatalf("build route=%+v", buildRoute)
			}
		})
	}
}

// TestCreateInteractionModelTargetMatchesOfficialShape 对照 2026-11-24 官网 gemini-4-hydrogen 现场核对模型目标、思考档位 1-5、多轮历史、原生工具步骤与省略 field 54
func TestCreateInteractionModelTargetMatchesOfficialShape(t *testing.T) {
	raw, err := json.Marshal([]any{[]any{listModelRowWithField79("gemini-4-hydrogen", []any{nil, nil, 1, 2})}})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := parseModelCatalog(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	model := catalog.entries["gemini-4-hydrogen"].model

	// Turn 1 对照官网现场：默认 High(4) 思考、Google Search、无 field 54、模型名写 interaction[17] 带 models/ 前缀、interaction[18] 为 nil
	body, _, binding, err := EncodeCreateInteractionRequest(GenerateRequest{
		Model:    "gemini-4-hydrogen",
		Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "Reply with the single word OK."}}}},
		Tools:    Tools{Google: []string{"google_search"}},
	}, model)
	if err != nil {
		t.Fatal(err)
	}
	var got []any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	wantInteraction := make([]any, 27)
	wantInteraction[7] = []any{[]any{nil, nil, nil, nil, nil, []any{}}}
	wantInteraction[17] = []any{"models/gemini-4-hydrogen", []any{nil, nil, nil, nil, nil, float64(4), float64(1), float64(65536)}}
	wantInteraction[26] = []any{[]any{[]any{[]any{[]any{[]any{[]any{"Reply with the single word OK."}}}}}}}
	want := []any{nil, float64(1), float64(1), wantInteraction, nil, float64(1)}
	if !reflect.DeepEqual(got, want) {
		wantJSON, _ := json.Marshal(want)
		t.Fatalf("\n got=%s\nwant=%s", body, wantJSON)
	}
	if prompt, err := bindingPrompt(GenerateRequest{Contents: binding}); err != nil || prompt != "Reply with the single word OK." {
		t.Fatalf("binding=%q err=%v", prompt, err)
	}

	// Turn 2 对照官网续接现场：带 previous_interaction_id 写 interaction[16]，只发最后模型轮后的新输入
	prevID := "v1_ChcwRVFGYTVPb0JLZVZtdGtQMk1YR2lBMBIXMEVRRmE1T29CS2VWbXRrUDJNWEdpQTA"
	body, _, binding, err = EncodeCreateInteractionRequest(GenerateRequest{
		Model:                 "gemini-4-hydrogen",
		PreviousInteractionID: prevID,
		Contents: []Content{
			{Role: RoleUser, Parts: []Part{{Text: "Reply with the single word OK."}}},
			{Role: RoleAssistant, Parts: []Part{{Text: "OK"}}},
			{Role: RoleUser, Parts: []Part{{Text: "Now reply with the single word YES."}}},
		},
		Tools: Tools{Google: []string{"google_search"}},
	}, model)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	wantInteraction[16] = prevID
	wantInteraction[26] = []any{[]any{[]any{[]any{[]any{[]any{[]any{"Now reply with the single word YES."}}}}}}}
	if !reflect.DeepEqual(got, want) {
		wantJSON, _ := json.Marshal(want)
		t.Fatalf("Turn 2\n got=%s\nwant=%s", body, wantJSON)
	}
	if prompt, err := bindingPrompt(GenerateRequest{Contents: binding}); err != nil || prompt != "Now reply with the single word YES." {
		t.Fatalf("Turn 2 binding=%q err=%v", prompt, err)
	}

	// Turn 3 Low(2) 与 ExtraHigh(5) 思考档位对照官网 hLb/mMb 枚举映射
	for effort, wantLevel := range map[string]float64{"low": 2, "medium": 3, "high": 4, "xhigh": 5, "extra_high": 5, "max": 5} {
		body, _, _, err := EncodeCreateInteractionRequest(GenerateRequest{
			Model:    "gemini-4-hydrogen",
			Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "hi"}}}},
			Config:   GenerationConfig{ReasoningEffort: effort},
		}, model)
		if err != nil {
			t.Fatalf("effort=%s: %v", effort, err)
		}
		var wire []any
		if err := json.Unmarshal(body, &wire); err != nil {
			t.Fatal(err)
		}
		interaction := wire[3].([]any)
		if len(interaction) != 27 {
			t.Fatalf("effort=%s interaction len=%d, want 27 (no field 54)", effort, len(interaction))
		}
		cfg := interaction[17].([]any)[1].([]any)
		if cfg[5] != wantLevel || cfg[6] != float64(1) || cfg[7] != float64(65536) || len(cfg) != 8 {
			t.Fatalf("effort=%s genConfig=%v", effort, cfg)
		}
	}

	// 无 previous_interaction_id 时保留多轮 user+model 与原生工具步骤，且工具步骤不参与 WAA binding（对齐官网 wLb + $qb + _.Aub）
	body, _, binding, err = EncodeCreateInteractionRequest(GenerateRequest{
		Model: "gemini-4-hydrogen",
		Contents: []Content{
			{Role: RoleUser, Parts: []Part{{Text: "Remember code 7391 and check weather."}}},
			{Role: RoleAssistant, Parts: []Part{
				{Thought: true, Text: "thinking"},
				{ThoughtSignature: "EtMCCtAC"},
				{FunctionCall: &FunctionCall{ID: "call_1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"Tokyo"}`)}},
			}},
			{Role: RoleTool, Parts: []Part{
				{FunctionResult: &FunctionResult{ID: "call_1", Content: json.RawMessage(`{"temp":22}`)}},
			}},
			{Role: RoleAssistant, Parts: []Part{{Text: "Noted 7391. Tokyo is 22C."}}},
			{Role: RoleUser, Parts: []Part{{Text: "What code?"}}},
		},
	}, model)
	if err != nil {
		t.Fatal(err)
	}
	var multi []any
	if err := json.Unmarshal(body, &multi); err != nil {
		t.Fatal(err)
	}
	steps := multi[3].([]any)[26].([]any)[0].([]any)
	if len(steps) != 6 {
		t.Fatalf("expected 6 steps (user, thought, tool_call, tool_result, model, user), got %d: %s", len(steps), body)
	}
	// 上游接受函数调用历史的前提是调用之前带思考步骤签名 Ww field 3 MZa field 1（官网流中签名所在的思考步骤）
	if !reflect.DeepEqual(steps[1], []any{nil, nil, []any{"EtMCCtAC"}}) {
		t.Fatalf("thought step=%v, want signature step before tool_call: %s", steps[1], body)
	}
	if steps[0].([]any)[0] == nil || steps[2].([]any)[3] == nil || steps[3].([]any)[4] == nil || steps[4].([]any)[1] == nil || steps[5].([]any)[0] == nil {
		t.Fatalf("step kinds mismatch: %s", body)
	}
	// WAA binding 仅含 user/model 文本，不含 tool_call/tool_result
	if prompt, err := bindingPrompt(GenerateRequest{Contents: binding}); err != nil || prompt != "Remember code 7391 and check weather. Noted 7391. Tokyo is 22C. What code?" {
		t.Fatalf("binding=%q err=%v", prompt, err)
	}

	// 客户端没有回传签名的函数调用历史以占位签名步骤开头，上游现场接受该形状
	body, _, _, err = EncodeCreateInteractionRequest(GenerateRequest{
		Model: "gemini-4-hydrogen",
		Contents: []Content{
			{Role: RoleUser, Parts: []Part{{Text: "Check weather."}}},
			{Role: RoleAssistant, Parts: []Part{
				{FunctionCall: &FunctionCall{ID: "call_1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"Tokyo"}`)}},
				{FunctionCall: &FunctionCall{ID: "call_2", Name: "get_weather", Arguments: json.RawMessage(`{"city":"Osaka"}`)}},
			}},
			{Role: RoleTool, Parts: []Part{
				{FunctionResult: &FunctionResult{ID: "call_1", Content: json.RawMessage(`{"temp":22}`)}},
				{FunctionResult: &FunctionResult{ID: "call_2", Content: json.RawMessage(`{"temp":25}`)}},
			}},
		},
	}, model)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &multi); err != nil {
		t.Fatal(err)
	}
	steps = multi[3].([]any)[26].([]any)[0].([]any)
	if len(steps) != 6 || !reflect.DeepEqual(steps[1], []any{nil, nil, []any{"skip_thought_signature_validator"}}) || steps[2].([]any)[3] == nil || steps[3].([]any)[3] == nil {
		t.Fatalf("unsigned parallel calls: %s", body)
	}
}
