package aistudio

import (
	"encoding/json"
	"testing"
)

func TestEncodeJSONSchema_ConstInAnyOf(t *testing.T) {
	input := `{
		"type": "object",
		"properties": {
			"mode": {
				"anyOf": [
					{
						"const": "fast",
						"title": "Fast Mode"
					},
					{
						"const": "slow"
					}
				]
			}
		}
	}`

	wire, err := encodeJSONSchema(json.RawMessage(input))
	if err != nil {
		t.Fatalf("encodeJSONSchema failed: %v", err)
	}
	if wire == nil {
		t.Fatal("expected non-nil wire schema")
	}
}

func TestEncodeJSONSchema_DirectConst(t *testing.T) {
	input := `{
		"type": "object",
		"properties": {
			"type": {
				"const": "tool_call",
				"title": "Type"
			},
			"version": {
				"const": 1
			},
			"enabled": {
				"const": true
			}
		}
	}`

	wire, err := encodeJSONSchema(json.RawMessage(input))
	if err != nil {
		t.Fatalf("encodeJSONSchema failed: %v", err)
	}
	if wire == nil {
		t.Fatal("expected non-nil wire schema")
	}
}
