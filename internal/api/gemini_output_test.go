package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestGeminiOutputParts_MergeTextAndReasoning(t *testing.T) {
	result := generationResult{
		events: []aistudio.Event{
			{Kind: aistudio.EventReasoning, Text: "I think "},
			{Kind: aistudio.EventReasoning, Text: "therefore "},
			{Kind: aistudio.EventReasoning, Text: "I am."},
			{Kind: aistudio.EventThoughtSignature, ThoughtSignature: "sig_abc123"},
			{Kind: aistudio.EventText, Text: "Hello "},
			{Kind: aistudio.EventText, Text: "world!"},
		},
	}

	parts := geminiOutputParts(result)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d: %+v", len(parts), parts)
	}

	// First part: merged thought
	expectedThought := map[string]any{
		"thought":          true,
		"text":             "I think therefore I am.",
		"thoughtSignature": "sig_abc123",
	}
	if !reflect.DeepEqual(parts[0], expectedThought) {
		t.Errorf("parts[0] = %+v, want %+v", parts[0], expectedThought)
	}

	// Second part: merged text
	expectedText := map[string]any{
		"text": "Hello world!",
	}
	if !reflect.DeepEqual(parts[1], expectedText) {
		t.Errorf("parts[1] = %+v, want %+v", parts[1], expectedText)
	}
}

func TestGeminiOutputParts_TextOnly(t *testing.T) {
	result := generationResult{
		events: []aistudio.Event{
			{Kind: aistudio.EventText, Text: "Chunk 1 "},
			{Kind: aistudio.EventText, Text: "Chunk 2 "},
			{Kind: aistudio.EventText, Text: "Chunk 3"},
		},
	}

	parts := geminiOutputParts(result)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d: %+v", len(parts), parts)
	}

	expectedText := map[string]any{
		"text": "Chunk 1 Chunk 2 Chunk 3",
	}
	if !reflect.DeepEqual(parts[0], expectedText) {
		t.Errorf("parts[0] = %+v, want %+v", parts[0], expectedText)
	}
}

func TestGeminiOutputParts_LeadingThoughtSignature(t *testing.T) {
	result := generationResult{
		events: []aistudio.Event{
			{Kind: aistudio.EventThoughtSignature, ThoughtSignature: "sig_pre"},
			{Kind: aistudio.EventReasoning, Text: "Thinking step."},
			{Kind: aistudio.EventText, Text: "Final text."},
		},
	}

	parts := geminiOutputParts(result)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d: %+v", len(parts), parts)
	}

	if parts[0]["thoughtSignature"] != "sig_pre" {
		t.Errorf("expected thoughtSignature 'sig_pre' on parts[0], got %v", parts[0]["thoughtSignature"])
	}
	if parts[0]["text"] != "Thinking step." {
		t.Errorf("expected text 'Thinking step.', got %v", parts[0]["text"])
	}
	if parts[1]["text"] != "Final text." {
		t.Errorf("expected text 'Final text.', got %v", parts[1]["text"])
	}
}

func TestGeminiOutputParts_StandaloneThoughtSignatureFallback(t *testing.T) {
	result := generationResult{
		events: []aistudio.Event{
			{Kind: aistudio.EventThoughtSignature, ThoughtSignature: "sig_only"},
		},
	}

	parts := geminiOutputParts(result)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d: %+v", len(parts), parts)
	}
	if parts[0]["thoughtSignature"] != "sig_only" {
		t.Errorf("expected thoughtSignature 'sig_only', got %v", parts[0]["thoughtSignature"])
	}
}

func TestGeminiOutputParts_WithToolCall(t *testing.T) {
	call := aistudio.FunctionCall{
		ID:        "call_1",
		Name:      "get_weather",
		Arguments: json.RawMessage(`{"location":"Tokyo"}`),
	}
	result := generationResult{
		events: []aistudio.Event{
			{Kind: aistudio.EventReasoning, Text: "Need weather for Tokyo."},
			{Kind: aistudio.EventThoughtSignature, ThoughtSignature: "sig_call"},
			{Kind: aistudio.EventToolCall, ToolCall: &call},
		},
	}

	parts := geminiOutputParts(result)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d: %+v", len(parts), parts)
	}

	if parts[0]["thought"] != true || parts[0]["text"] != "Need weather for Tokyo." {
		t.Errorf("unexpected parts[0]: %+v", parts[0])
	}
	if parts[0]["thoughtSignature"] != "sig_call" {
		t.Errorf("expected sig_call on parts[0], got %v", parts[0]["thoughtSignature"])
	}
	if parts[1]["functionCall"] == nil {
		t.Errorf("expected functionCall on parts[1], got %+v", parts[1])
	}
}
