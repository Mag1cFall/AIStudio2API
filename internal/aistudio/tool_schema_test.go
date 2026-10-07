package aistudio

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// questionToolParameters 表示支持文字或对象选项的问题工具
const questionToolParameters = `{"type":"object","properties":{"questions":{"type":"array","items":{"type":"object","properties":{"multiSelect":{"type":"boolean"},"options":{"type":"array","items":{"anyOf":[{"type":"string"},{"type":"object","properties":{"description":{"type":"string"},"label":{"type":"string"}},"required":["label"]}]}},"question":{"type":"string"}},"required":["question","options"]}}},"required":["questions"]}`

// TestToolSchemaMixedOptions 验证复杂问题工具保持原始声明且直接使用 Playground
func TestToolSchemaMixedOptions(t *testing.T) {
	declaration := FunctionDeclaration{Name: "asktool", Parameters: json.RawMessage(questionToolParameters)}
	before, _ := json.Marshal(declaration)
	wire, err := encodeFunctionDeclaration(declaration)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(declaration)
	if string(before) != string(after) {
		t.Fatal("原始工具声明发生变化")
	}
	normalized, err := normalizeFunctionParameters(declaration.Parameters)
	if err != nil || !strings.Contains(wire[1].(string), string(normalized)) || schemaWireNeedsBuild(wire[2].([]any)) {
		t.Fatalf("完整约束或可发送的参数结构缺失: %#v", wire)
	}
	request := GenerateRequest{Tools: Tools{Functions: []FunctionDeclaration{declaration}}}
	if RequestNeedsBuildSchema(request) {
		t.Fatal("工具声明触发 Build 分流")
	}
	request.Config.ResponseSchema = json.RawMessage(`{"type":"object","properties":{"value":{}}}`)
	if !RequestNeedsBuildSchema(request) {
		t.Fatal("结构化输出的原生通道选择丢失")
	}
}

// TestToolSchemaUnionBranches 验证联合分支与首个数组元素类型保留
func TestToolSchemaUnionBranches(t *testing.T) {
	for _, test := range []struct {
		name, schema string
		index        int
	}{
		{"string object", `{"anyOf":[{"type":"string","minLength":2},{"type":"object","properties":{"label":{"type":"string"}},"required":["label"]}]}`, 17},
		{"object string", `{"anyOf":[{"type":"object","properties":{"label":{"type":"string"}},"required":["label"]},{"type":"string"}]}`, 17},
		{"overlapping oneOf", `{"oneOf":[{"type":"string"},{"type":"string","minLength":3},{"type":"object"}]}`, 16},
		{"array first", `{"anyOf":[{"type":"array","items":{"type":"integer"},"minItems":1},{"type":"string"}]}`, 17},
	} {
		t.Run(test.name, func(t *testing.T) {
			wire, err := encodeFunctionDeclaration(FunctionDeclaration{Name: "pick", Parameters: json.RawMessage(test.schema)})
			if err != nil {
				t.Fatal(err)
			}
			parameters := wire[2].([]any)
			var original map[string][]json.RawMessage
			_ = json.Unmarshal([]byte(test.schema), &original)
			key := "anyOf"
			if test.index == 16 {
				key = "oneOf"
			}
			actual := parameters[test.index].([]any)
			if len(actual) != len(original[key]) {
				t.Fatal("联合分支数量变化")
			}
			for index, branch := range original[key] {
				expected, err := encodeJSONSchema(branch)
				if err != nil || !reflect.DeepEqual(actual[index], expected) {
					t.Fatalf("联合分支 %d 变化: %#v, %v", index, actual[index], err)
				}
			}
			if test.name == "array first" && parameters[5].([]any)[0] != int64(3) {
				t.Fatal("数组元素类型变化")
			}
		})
	}
}

// TestToolSchemaOpenNodes 验证开放、引用与组合节点携带完整原始约束
func TestToolSchemaOpenNodes(t *testing.T) {
	for _, raw := range []string{
		`{"type":"object","properties":{"value":true}}`,
		`{"type":"array"}`,
		`{"anyOf":[{},{"type":"string"}]}`,
		`{"anyOf":[{"type":"array"},{"type":"string"}]}`,
		`{"anyOf":[{"type":"string"},{"type":"object"}],"not":{"type":"string"}}`,
		`{"type":"object","properties":{"value":{"const":7}}}`,
		`{"type":"object","properties":{"value":{"const":null}}}`,
		`{"type":"object","properties":{"value":{"$ref":"#/$defs/item"}},"$defs":{"item":{"type":"string"}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			wire, err := encodeFunctionDeclaration(FunctionDeclaration{Name: "pick", Parameters: json.RawMessage(raw)})
			if err != nil {
				t.Fatal(err)
			}
			normalized, err := normalizeFunctionParameters(json.RawMessage(raw))
			if err != nil || !strings.Contains(wire[1].(string), string(normalized)) || schemaWireNeedsBuild(wire[2].([]any)) {
				t.Fatalf("约束或参数结构缺失: %#v", wire)
			}
		})
	}
}

// TestToolSchemaKeepsFileTools 验证问题工具与文件工具共同编码时保留普通声明
func TestToolSchemaKeepsFileTools(t *testing.T) {
	tools := Tools{Functions: []FunctionDeclaration{
		{Name: "Read", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
		{Name: "asktool", Parameters: json.RawMessage(questionToolParameters)},
		{Name: "Write", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)},
	}}
	before, _ := json.Marshal(tools)
	wire, _, err := encodeRequestedTools(tools)
	if err != nil {
		t.Fatal(err)
	}
	declarations := wire[0].([]any)[1].([]any)
	for _, index := range []int{0, 2} {
		expected, err := encodeFunctionDeclaration(tools.Functions[index])
		if err != nil || !reflect.DeepEqual(declarations[index], expected) {
			t.Fatalf("文件工具 %d 变化", index)
		}
	}
	after, _ := json.Marshal(tools)
	if string(before) != string(after) {
		t.Fatal("原始工具集发生变化")
	}
}
