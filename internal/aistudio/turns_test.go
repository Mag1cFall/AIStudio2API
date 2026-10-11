package aistudio

import (
	"encoding/json"
	"testing"
)

func TestSanitizeThoughtSignatures(t *testing.T) {
	contents := []Content{
		{
			Role: RoleUser,
			Parts: []Part{
				{Text: "Hello, search for something"},
			},
		},
		{
			Role: RoleAssistant,
			Parts: []Part{
				{Text: "Thinking...", Thought: true, ThoughtSignature: "sig-draft"},
				{Text: "Here is the result", ThoughtSignature: "sig-text"},
				{ThoughtSignature: "sig-standalone"},
			},
		},
		{
			Role: RoleAssistant,
			Parts: []Part{
				{
					FunctionCall: &FunctionCall{
						ID:               "call_1",
						Name:             "search",
						Arguments:        json.RawMessage(`{}`),
						ThoughtSignature: "sig-call",
					},
					ThoughtSignature: "sig-call",
				},
			},
		},
	}

	cleaned := sanitizeThoughtSignatures(contents)
	if len(cleaned) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(cleaned))
	}

	// Turn 0: User message unchanged
	if len(cleaned[0].Parts) != 1 || cleaned[0].Parts[0].Text != "Hello, search for something" {
		t.Errorf("unexpected turn 0: %+v", cleaned[0])
	}

	// Turn 1: Model message should have dropped standalone signature part and stripped signatures from text
	if len(cleaned[1].Parts) != 2 {
		t.Fatalf("expected 2 parts in turn 1, got %d", len(cleaned[1].Parts))
	}
	if cleaned[1].Parts[0].Text != "Thinking..." || cleaned[1].Parts[0].ThoughtSignature != "" {
		t.Errorf("part 0 thought signature not cleared: %+v", cleaned[1].Parts[0])
	}
	if cleaned[1].Parts[1].Text != "Here is the result" || cleaned[1].Parts[1].ThoughtSignature != "" {
		t.Errorf("part 1 thought signature not cleared: %+v", cleaned[1].Parts[1])
	}

	// Turn 2: Function call should have "skip_thought_signature_validator"
	if len(cleaned[2].Parts) != 1 {
		t.Fatalf("expected 1 part in turn 2, got %d", len(cleaned[2].Parts))
	}
	call := cleaned[2].Parts[0].FunctionCall
	if call == nil || call.ThoughtSignature != "skip_thought_signature_validator" {
		t.Errorf("expected skip_thought_signature_validator on call, got %+v", call)
	}
	if cleaned[2].Parts[0].ThoughtSignature != "skip_thought_signature_validator" {
		t.Errorf("expected skip_thought_signature_validator on part, got %s", cleaned[2].Parts[0].ThoughtSignature)
	}
}
