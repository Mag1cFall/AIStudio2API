package aistudio

import (
	"encoding/json"
	"testing"
)

func TestEncodeJSONSchema_NullFields(t *testing.T) {
	fields := []string{
		`"format": null`,
		`"description": null`,
		`"nullable": null`,
		`"enum": null`,
		`"items": null`,
		`"properties": null`,
		`"required": null`,
		`"minItems": null`,
		`"maxItems": null`,
		`"minProperties": null`,
		`"maxProperties": null`,
		`"minimum": null`,
		`"maximum": null`,
		`"minLength": null`,
		`"maxLength": null`,
		`"pattern": null`,
		`"example": null`,
		`"oneOf": null`,
		`"anyOf": null`,
		`"allOf": null`,
		`"not": null`,
		`"default": null`,
	}

	for _, f := range fields {
		schema := `{
			"type": "string",
			` + f + `
		}`
		wire, err := encodeJSONSchema(json.RawMessage(schema))
		if err != nil {
			t.Fatalf("Field %s caused error: %v", f, err)
		}
		if wire == nil {
			t.Fatalf("Field %s resulted in nil wire schema", f)
		}
	}
}

func TestEncodeJSONSchema_NotVariants(t *testing.T) {
	cases := []struct {
		name   string
		schema string
	}{
		{
			name: "not null",
			schema: `{
				"type": "object",
				"properties": {
					"model": {
						"type": "string",
						"not": null
					}
				}
			}`,
		},
		{
			name: "not boolean false",
			schema: `{
				"type": "object",
				"properties": {
					"model": {
						"type": "string",
						"not": false
					}
				}
			}`,
		},
		{
			name: "not boolean true",
			schema: `{
				"type": "object",
				"properties": {
					"model": {
						"type": "string",
						"not": true
					}
				}
			}`,
		},
		{
			name: "not empty object",
			schema: `{
				"type": "object",
				"properties": {
					"model": {
						"type": "string",
						"not": {}
					}
				}
			}`,
		},
		{
			name: "not type null",
			schema: `{
				"type": "object",
				"properties": {
					"model": {
						"type": "string",
						"not": {
							"type": "null"
						}
					}
				}
			}`,
		},
		{
			name: "not string array",
			schema: `{
				"type": "object",
				"properties": {
					"model": {
						"type": "string",
						"not": ["gpt-3.5", "gpt-4"]
					}
				}
			}`,
		},
		{
			name: "not single string",
			schema: `{
				"type": "object",
				"properties": {
					"model": {
						"type": "string",
						"not": "gpt-3.5"
					}
				}
			}`,
		},
		{
			name: "not valid schema object",
			schema: `{
				"type": "object",
				"properties": {
					"model": {
						"type": "string",
						"not": {
							"enum": ["gpt-3.5"]
						}
					}
				}
			}`,
		},
		{
			name: "null property in properties",
			schema: `{
				"type": "object",
				"properties": {
					"model": null,
					"valid": {
						"type": "string"
					}
				}
			}`,
		},
		{
			name: "boolean properties",
			schema: `{
				"type": "object",
				"properties": {
					"allow": true,
					"disallow": false
				}
			}`,
		},
		{
			name: "boolean items",
			schema: `{
				"type": "array",
				"items": true
			}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wire, err := encodeJSONSchema(json.RawMessage(tc.schema))
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.name, err)
			}
			if wire == nil {
				t.Fatalf("expected non-nil wire schema for %q", tc.name)
			}
		})
	}
}

func TestEncodeJSONSchema_TopLevelNullOrEmpty(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"null", `null`},
		{"empty string", ``},
		{"spaces", `   `},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wire, err := encodeJSONSchema(json.RawMessage(tc.raw))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if wire == nil {
				t.Fatal("expected non-nil wire")
			}
		})
	}
}

func TestEncodeJSONSchema_DirectConst(t *testing.T) {
	input := `{
		"type": "object",
		"properties": {
			"type": {
				"const": "tool_call",
				"title": "Type"
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
