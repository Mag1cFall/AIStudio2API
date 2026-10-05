package aistudio

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// Structural fixture only: a common question tool supports plain labels or
// {label, description} objects. No real task or user prompt is included.
const questionToolParameters = `{"type":"object","properties":{"questions":{"type":"array","items":{"type":"object","properties":{"multiSelect":{"type":"boolean"},"options":{"type":"array","items":{"anyOf":[{"type":"string"},{"type":"object","properties":{"description":{"type":"string"},"label":{"type":"string"}},"required":["label"]}]}},"question":{"type":"string"}},"required":["question","options"]}}},"required":["questions"]}`

func TestFallbackMixedToolOptions(t *testing.T) {
	request := fallbackTestRequest(questionToolParameters)
	request.Tools.Functions[0].Name = "asktool"
	before, _ := json.Marshal(request)
	s := &PooledService{pool: NewAccountPool(nil, 1), SchemaFallback: true}
	got, used, err := s.prepareSchemaRequest(request)
	if err != nil || !used || requestNeedsBuildSchema(got) {
		t.Fatalf("mixed question options should use explicit legacy fallback: used=%v err=%v", used, err)
	}
	after, _ := json.Marshal(request)
	if string(before) != string(after) {
		t.Fatal("caller-owned declaration changed")
	}
	var expected map[string]any
	if err := json.Unmarshal([]byte(questionToolParameters), &expected); err != nil {
		t.Fatal(err)
	}
	questions := expected["properties"].(map[string]any)["questions"].(map[string]any)["items"].(map[string]any)
	options := questions["properties"].(map[string]any)["options"].(map[string]any)["items"].(map[string]any)
	options["type"] = "string"
	var actual any
	if err := json.Unmarshal(got.Tools.Functions[0].Parameters, &actual); err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("expected only the disjunction root type to be added, got %s", got.Tools.Functions[0].Parameters)
	}
	wire, err := encodeFunctionDeclaration(got.Tools.Functions[0])
	if err != nil || len(wire) == 0 {
		t.Fatalf("Playground tool encoding: %v", err)
	}
	// Strict mode must still reject; Build must retain all native alternatives.
	s.SchemaFallback = false
	if _, _, err := s.prepareSchemaRequest(request); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("strict mode changed: %v", err)
	}
	s.SchemaFallback = true
	s.pool.SetUpstreamChannels([]Channel{ChannelPlayground, ChannelBuild})
	got, used, err = s.prepareSchemaRequest(request)
	if err != nil || used || !reflect.DeepEqual(got, request) {
		t.Fatalf("native Build schema changed: %v", err)
	}
}

func TestFallbackUnionPreservesBranches(t *testing.T) {
	for _, test := range []struct{ name, schema, rootType string }{
		{"anyOf string/object", `{"anyOf":[{"type":"string","minLength":2},{"type":"object","properties":{"label":{"type":"string"}},"required":["label"]}]}`, "string"},
		{"anyOf object/string", `{"anyOf":[{"type":"object","properties":{"label":{"type":"string"}},"required":["label"]},{"type":"string"}]}`, "object"},
		{"oneOf with overlap", `{"oneOf":[{"type":"string"},{"type":"string","minLength":3},{"type":"object"}]}`, "string"},
		{"typed array first", `{"anyOf":[{"type":"array","items":{"type":"integer"},"minItems":1},{"type":"string"}]}`, "array"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := fallbackToolSchema(json.RawMessage(test.schema))
			if err != nil {
				t.Fatal(err)
			}
			var before, after map[string]any
			_ = json.Unmarshal([]byte(test.schema), &before)
			_ = json.Unmarshal(got, &after)
			if after["type"] != test.rootType {
				t.Fatalf("type=%v", after["type"])
			}
			for k, v := range before {
				if !reflect.DeepEqual(after[k], v) {
					t.Fatalf("constraint %s changed or removed", k)
				}
			}
			wire, err := encodeJSONSchema(got)
			if err != nil || schemaWireNeedsBuild(wire) {
				t.Fatalf("unrepresentable fallback: %v", err)
			}
			if test.rootType == "array" && !reflect.DeepEqual(after["items"], map[string]any{"type": "integer"}) {
				t.Fatal("array element type was replaced by string")
			}
		})
	}
	got, err := fallbackToolSchema(json.RawMessage(`{"type":["string","integer","null"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var nullable map[string]any
	_ = json.Unmarshal(got, &nullable)
	if nullable["nullable"] != true || nullable["type"] != "string" || len(nullable["anyOf"].([]any)) != 2 {
		t.Fatalf("nullable union lost: %s", got)
	}
}

func TestFallbackUnionUnsafeCases(t *testing.T) {
	for _, raw := range []string{
		`{"anyOf":[{}, {"type":"string"}]}`,
		`{"anyOf":[true, {"type":"string"}]}`,
		`{"anyOf":[{"type":"array"},{"type":"string"}]}`,
		`{"anyOf":[{"type":"array","items":true},{"type":"string"}]}`,
		`{"anyOf":[{"type":"array","items":{}},{"type":"string"}]}`,
		`{"allOf":[{"type":"string"},{"type":"object"}]}`,
		`{"anyOf":[{"type":"string"},{"type":"object"}],"not":{"type":"string"}}`,
		`{"anyOf":[{"type":"string"},{"type":"object"}],"oneOf":[{"type":"number"},{"type":"boolean"}]}`,
	} {
		if got, err := fallbackToolSchema(json.RawMessage(raw)); err == nil {
			t.Fatalf("unsupported combination silently approximated: %s -> %s", raw, got)
		}
	}
	request := fallbackTestRequest(questionToolParameters)
	request.Config.ResponseSchema = json.RawMessage(`{"anyOf":[{"type":"string"},{"type":"object"}]}`)
	s := &PooledService{pool: NewAccountPool(nil, 1), SchemaFallback: true}
	if got, used, err := s.prepareSchemaRequest(request); !errors.Is(err, ErrInvalidArgument) || used || !reflect.DeepEqual(got, request) {
		t.Fatalf("response schema approximated: used=%v err=%v", used, err)
	}
}

func TestMixedQuestionSchemaKeepsFileTools(t *testing.T) {
	request := fallbackTestRequest(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
	request.Tools.Functions[0].Name = "Read"
	request.Tools.Functions = append(request.Tools.Functions,
		FunctionDeclaration{Name: "asktool", Parameters: json.RawMessage(questionToolParameters)},
		FunctionDeclaration{Name: "Write", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)},
	)
	s := &PooledService{pool: NewAccountPool(nil, 1), SchemaFallback: true}
	got, used, err := s.prepareSchemaRequest(request)
	if err != nil || !used || requestNeedsBuildSchema(got) {
		t.Fatalf("tool set still blocked: %v", err)
	}
	for _, index := range []int{0, 2} {
		if !reflect.DeepEqual(request.Tools.Functions[index], got.Tools.Functions[index]) {
			t.Fatalf("unrelated file tool %d changed", index)
		}
	}
}
