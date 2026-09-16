package aistudio

import (
	"encoding/json"
	"testing"
)

func TestEncodePart_FunctionCallDefaultThoughtSignature(t *testing.T) {
	part := Part{
		FunctionCall: &FunctionCall{
			ID:        "call_123",
			Name:      "test_tool",
			Arguments: json.RawMessage(`{"arg": "val"}`),
		},
	}
	wire, err := encodePart(part)
	if err != nil {
		t.Fatalf("encodePart failed: %v", err)
	}
	if len(wire) <= 14 {
		t.Fatalf("expected wire length > 14, got %d", len(wire))
	}
	if wire[14] != "skip_thought_signature_validator" {
		t.Fatalf("expected wire[14] to be skip_thought_signature_validator, got %v", wire[14])
	}
}

func TestEncodePart_FunctionCallPreserveThoughtSignature(t *testing.T) {
	part := Part{
		FunctionCall: &FunctionCall{
			ID:               "call_123",
			Name:             "test_tool",
			Arguments:        json.RawMessage(`{"arg": "val"}`),
			ThoughtSignature: "real_hmac_sig",
		},
	}
	wire, err := encodePart(part)
	if err != nil {
		t.Fatalf("encodePart failed: %v", err)
	}
	if len(wire) <= 14 {
		t.Fatalf("expected wire length > 14, got %d", len(wire))
	}
	if wire[14] != "real_hmac_sig" {
		t.Fatalf("expected wire[14] to be real_hmac_sig, got %v", wire[14])
	}
}
