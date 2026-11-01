package aistudio

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

// TestEncodeJSONSchema_NullFields 核对可选空值与数据空值的编码字段
func TestEncodeJSONSchema_NullFields(t *testing.T) {
	fields := []string{"format", "description", "nullable", "enum", "items", "properties", "required", "minItems", "maxItems", "minProperties", "maxProperties", "minimum", "maximum", "minLength", "maxLength", "pattern", "oneOf", "anyOf", "allOf", "not", "default"}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			wire, err := encodeJSONSchema(json.RawMessage(`{"type":"string","` + field + `":null}`))
			if err != nil || !reflect.DeepEqual(wire, []any{int64(1)}) {
				t.Fatalf("wire=%#v error=%v", wire, err)
			}
		})
	}
	wire, err := encodeJSONSchema(json.RawMessage(`{"type":"string","example":null}`))
	if err != nil || len(wire) <= 15 || !reflect.DeepEqual(wire[15], []any{int64(0)}) {
		t.Fatalf("null example wire=%#v error=%v", wire, err)
	}
}

// TestEncodeJSONSchema_NotVariants 核对禁止约束和排除值的实际语义
func TestEncodeJSONSchema_NotVariants(t *testing.T) {
	for _, raw := range []string{`{"type":"string","not":null}`, `{"type":"string","not":false}`} {
		wire, err := encodeJSONSchema(json.RawMessage(raw))
		if err != nil || !reflect.DeepEqual(wire, []any{int64(1)}) {
			t.Fatalf("schema=%s wire=%#v error=%v", raw, wire, err)
		}
	}
	for _, raw := range []string{`{"type":"string","not":true}`, `{"type":"string","not":{}}`, `{"type":"object","properties":{"deny":false},"required":["deny"]}`} {
		if wire, err := encodeResponseSchema(json.RawMessage(raw)); err == nil {
			t.Fatalf("forbidden schema=%s wire=%#v", raw, wire)
		}
	}
	if _, err := encodeResponseSchema(json.RawMessage(`{"type":"object","properties":{"deny":false}}`)); err != nil {
		t.Fatalf("optional false property: %v", err)
	}
	wire, err := encodeJSONSchema(json.RawMessage(`{"type":["string","null"],"not":{"type":"null"}}`))
	if err != nil || len(wire) <= 3 || wire[3] != false {
		t.Fatalf("not null wire=%#v error=%v", wire, err)
	}
	for _, raw := range []string{`{"type":"string","not":""}`, `{"type":"string","not":{"enum":[""]}}`} {
		wire, err := encodeJSONSchema(json.RawMessage(raw))
		if err != nil || len(wire) > 19 || len(wire) <= 12 || wire[12] != int64(1) {
			t.Fatalf("not empty schema=%s wire=%#v error=%v", raw, wire, err)
		}
	}
	for _, value := range []string{`"denied"`, `["a","b"]`, `["","a","b"]`} {
		wire, err := encodeJSONSchema(json.RawMessage(`{"type":"string","not":` + value + `}`))
		if err != nil || len(wire) <= 19 {
			t.Fatalf("not enum wire=%#v error=%v", wire, err)
		}
		excluded := wire[19].([]any)
		var expected []string
		if value[0] == '[' {
			_ = json.Unmarshal([]byte(value), &expected)
			expected = slices.DeleteFunc(expected, func(item string) bool { return item == "" })
		} else {
			var text string
			_ = json.Unmarshal([]byte(value), &text)
			expected = []string{text}
		}
		if len(excluded) <= 4 || !reflect.DeepEqual(excluded[4], expected) {
			t.Fatalf("excluded=%#v expected=%#v", excluded, expected)
		}
	}
}

// TestGeminiSchemaNotEmptyString 核对 not 排除空串只收紧空串：联合类型仍接受其余类型，带其他约束的排除不额外要求非空
func TestGeminiSchemaNotEmptyString(t *testing.T) {
	union, err := geminiSchema(json.RawMessage(`{"type":["integer","string"],"not":{"enum":["","reserved"]}}`))
	if err != nil || union["type"] == "STRING" || union["minLength"] != nil {
		t.Fatalf("联合类型被收窄为字符串 schema=%#v error=%v", union, err)
	}
	variants, _ := union["anyOf"].([]any)
	if !slices.ContainsFunc(variants, func(variant any) bool { return variant.(map[string]any)["type"] == "INTEGER" }) {
		t.Fatalf("联合类型缺少整数分支 anyOf=%#v", union["anyOf"])
	}
	pattern, err := geminiSchema(json.RawMessage(`{"type":"string","not":{"enum":["","reserved"],"pattern":"^reserved$"}}`))
	not, _ := pattern["not"].(map[string]any)
	if err != nil || pattern["minLength"] != nil || not == nil || !reflect.DeepEqual(not["enum"], []string{"reserved"}) || not["pattern"] != "^reserved$" {
		t.Fatalf("带 pattern 的排除 schema=%#v error=%v", pattern, err)
	}
}

// TestEncodeJSONSchema_TopLevelNullOrEmpty 核对省略参数使用对象结构
func TestEncodeJSONSchema_TopLevelNullOrEmpty(t *testing.T) {
	for _, raw := range []string{"", "   ", "null"} {
		wire, err := encodeJSONSchema(json.RawMessage(raw))
		if err != nil || !reflect.DeepEqual(wire, []any{int64(6)}) {
			t.Fatalf("schema=%q wire=%#v error=%v", raw, wire, err)
		}
	}
}

// TestEncodeJSONSchema_DirectConst 核对常量属性的类型和枚举值
func TestEncodeJSONSchema_DirectConst(t *testing.T) {
	wire, err := encodeJSONSchema(json.RawMessage(`{"type":"object","properties":{"type":{"const":"tool_call","title":"Type"}}}`))
	if err != nil || len(wire) <= 6 {
		t.Fatalf("wire=%#v error=%v", wire, err)
	}
	properties := wire[6].([]any)
	if len(properties) != 1 {
		t.Fatalf("properties=%#v", properties)
	}
	property := properties[0].([]any)
	value := property[1].([]any)
	if property[0] != "type" || len(value) <= 4 || value[0] != int64(1) || !reflect.DeepEqual(value[4], []string{"tool_call"}) {
		t.Fatalf("property=%#v", property)
	}
}
