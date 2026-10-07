package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// TestChatAssistantTailCompatibility reproduces the interleaved-system request
// shape without storing any real conversation text.
func TestChatAssistantTailCompatibility(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, suffix := range []string{"", `,{"role":"system","content":"final instruction"}`, `,{"role":"developer","content":"final instruction"}`, `,{"role":"user","content":""}`} {
			raw := `{"model":"gemini-3.8-flash","messages":[{"role":"user","content":"Start a story."},{"role":"system","content":"first instruction"},{"role":"assistant","content":"  Once upon a time,\n"}` + suffix + `]}`
			var input chatRequest
			if err := json.Unmarshal([]byte(raw), &input); err != nil {
				t.Fatal(err)
			}
			input.Stream = stream
			before, _ := json.Marshal(input)
			got, err := input.toGenerateRequest("test-tail")
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Contents) != 3 || got.Contents[2].Role != aistudio.RoleUser || len(got.Contents[2].Parts) != 1 || got.Contents[2].Parts[0].Text == "" {
				t.Fatalf("assistant tail was not made acceptable to upstream: %#v", got.Contents)
			}
			if got.Contents[1].Role != aistudio.RoleAssistant || got.Contents[1].Parts[0].Text != "  Once upon a time,\n" {
				t.Fatal("assistant text/role/whitespace changed")
			}
			wantSystem := "first instruction"
			if suffix != "" && suffix != `,{"role":"user","content":""}` {
				wantSystem += "\nfinal instruction"
			}
			if got.System != wantSystem {
				t.Fatalf("system instructions changed: %q", got.System)
			}
			after, _ := json.Marshal(input)
			if string(before) != string(after) {
				t.Fatal("caller request mutated")
			}
			wire, err := aistudio.EncodeGenerateContentRequest(got, aistudio.GenerationDefaults{MaxOutputTokens: 1024}, aistudio.RequestContext{})
			if err != nil {
				t.Fatal(err)
			}
			var root []json.RawMessage
			var contents [][]json.RawMessage
			if json.Unmarshal(wire, &root) != nil || json.Unmarshal(root[1], &contents) != nil || string(contents[len(contents)-1][1]) != `"user"` {
				t.Fatal("encoded request still ends with model")
			}
		}
	}
}

func TestChatAssistantTailDoesNotCompleteToolCalls(t *testing.T) {
	for _, tail := range []string{
		`{"role":"user","content":"continue"}`,
		`{"role":"assistant","content":""}`,
		`{"role":"assistant","content":" \t "}`,
		`{"role":"assistant","content":"checking","tool_calls":[{"id":"c1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}`,
		`{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"assistant","content":"checking"}`,
		`{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","name":"lookup","content":"result"}`,
	} {
		var input chatRequest
		if err := json.Unmarshal([]byte(`{"model":"gemini-3.8-flash","messages":[{"role":"user","content":"hello"},`+tail+`]}`), &input); err != nil {
			t.Fatal(err)
		}
		var want []aistudio.Content
		for _, message := range input.Messages {
			content, err := chatMessageContent(message)
			if err != nil {
				t.Fatal(err)
			}
			if len(content.Parts) > 0 {
				want = append(want, content)
			}
		}
		got, err := input.toGenerateRequest("test-tail")
		if err != nil || !reflect.DeepEqual(got.Contents, want) {
			t.Fatalf("ordinary/unfinished-tool request changed: %s, error=%v", tail, err)
		}
	}
}

func TestChatAssistantTailIsolation(t *testing.T) {
	backing := make([]aistudio.Content, 3)
	backing[0] = aistudio.Content{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "start"}}}
	backing[1] = aistudio.Content{Role: aistudio.RoleAssistant, Parts: []aistudio.Part{{Text: "prefix"}}}
	backing[2] = aistudio.Content{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "sentinel"}}}
	input := backing[:2]
	for range 12 {
		t.Run("shared history", func(t *testing.T) {
			t.Parallel()
			got := continueChatAssistantTail(input)
			if len(got) != 3 || got[2].Role != aistudio.RoleUser || !reflect.DeepEqual(got[:2], input) {
				t.Fatal("history was rewritten")
			}
			if !reflect.DeepEqual(continueChatAssistantTail(got), got) {
				t.Fatal("continuation applied twice")
			}
			if backing[2].Parts[0].Text != "sentinel" {
				t.Fatal("append overwrote caller backing array")
			}
		})
	}
}

func TestChatAssistantTailNonTextExcluded(t *testing.T) {
	for _, part := range []aistudio.Part{
		{Text: "prefix", InlineData: &aistudio.Blob{}},
		{Text: "prefix", File: &aistudio.FileRef{}},
		{Text: "prefix", ExternalMedia: &aistudio.ExternalMedia{}},
		{Text: "prefix", FunctionCall: &aistudio.FunctionCall{}},
		{Text: "prefix", FunctionResult: &aistudio.FunctionResult{}},
		{Text: "prefix", ExecutableCode: &aistudio.ExecutableCode{}},
		{Text: "prefix", CodeExecutionResult: &aistudio.CodeExecutionResult{}},
		{Text: "prefix", Thought: true},
		{Text: "prefix", ThoughtSignature: "test-signature"},
		{Text: "prefix", SpeechMetadata: &aistudio.SpeechMetadata{}},
	} {
		input := []aistudio.Content{{Role: aistudio.RoleAssistant, Parts: []aistudio.Part{part}}}
		for range 2 {
			if !reflect.DeepEqual(continueChatAssistantTail(input), input) {
				t.Fatal("non-text assistant turn changed")
			}
			input = append(input, aistudio.Content{Role: aistudio.RoleAssistant, Parts: []aistudio.Part{{Text: "plain text"}}})
		}
	}
	if continueChatAssistantTail(nil) != nil {
		t.Fatal("empty history changed")
	}
}

func TestChatAssistantTailConsecutiveText(t *testing.T) {
	input := []aistudio.Content{
		{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "start"}}},
		{Role: aistudio.RoleAssistant, Parts: []aistudio.Part{{Text: "part one"}}},
		{Role: aistudio.RoleAssistant, Parts: []aistudio.Part{{Text: "part two"}, {Text: " \n"}}},
	}
	got := continueChatAssistantTail(input)
	if len(got) != 4 || !reflect.DeepEqual(got[:3], input) || got[3].Role != aistudio.RoleUser {
		t.Fatalf("consecutive assistant text was not preserved and continued: %#v", got)
	}
	for _, parts := range [][]aistudio.Part{nil, {{Text: " \n\t"}}} {
		input[2].Parts = parts
		if !reflect.DeepEqual(continueChatAssistantTail(input), input) {
			t.Fatal("nonblank earlier assistant incorrectly made an empty final turn eligible")
		}
	}
}

func TestChatAssistantTailObservedRoleSequence(t *testing.T) {
	roles := []string{"user", "system", "user", "system", "assistant", "system", "user", "system", "assistant", "system", "user", "system", "assistant", "system"}
	input := chatRequest{Model: "gemini-3.8-flash"}
	for _, role := range roles {
		input.Messages = append(input.Messages, chatMessage{Role: role, Content: json.RawMessage(`"synthetic text"`)})
	}
	got, err := input.toGenerateRequest("role-only-fixture")
	if err != nil || len(got.Contents) != 8 || got.Contents[6].Role != aistudio.RoleAssistant || got.Contents[7].Role != aistudio.RoleUser {
		t.Fatalf("observed role sequence not normalized: %v", err)
	}
	for _, content := range got.Contents[:7] {
		if content.Parts[0].Text != "synthetic text" {
			t.Fatal("history text changed")
		}
	}
}
