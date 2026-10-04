package aistudio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

var schemaTypeCodes = map[string]int64{
	"string":  1,
	"number":  2,
	"integer": 3,
	"boolean": 4,
	"array":   5,
	"object":  6,
}

func encodeJSONSchema(raw json.RawMessage) ([]any, error) {
	var err error
	raw, err = cleanJSONSchemaInput(raw)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(raw, []byte("true")) {
		return []any{int64(0)}, nil
	}
	if bytes.Equal(raw, []byte("false")) {
		return nil, fmt.Errorf("schema false 禁止所有值")
	}
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(raw, &schema); err != nil || schema == nil {
		return nil, fmt.Errorf("schema 必须是 JSON object")
	}
	if err := normalizeConstAndMetadata(schema); err != nil {
		return nil, err
	}
	if err := normalizeNullableVariants(schema); err != nil {
		return nil, err
	}
	if err := normalizeNotSchema(schema); err != nil {
		return nil, err
	}
	if raw, ok := schema["items"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("false")) {
		if schema["prefixItems"] != nil {
			return nil, &UnverifiedProtocolError{Feature: "prefixItems 与 items=false 的位置约束"}
		}
		if minimum, ok := schema["minItems"]; ok {
			value, err := schemaInteger(minimum, "minItems")
			if err != nil || value > 0 {
				return nil, fmt.Errorf("schema.items=false 与 minItems 不兼容")
			}
		}
		schema["items"], schema["maxItems"] = json.RawMessage("true"), json.RawMessage("0")
	}
	if err := normalizeImplicitType(schema); err != nil {
		return nil, err
	}
	allowed := map[string]bool{
		"type": true, "format": true, "description": true, "nullable": true,
		"enum": true, "items": true, "properties": true, "required": true,
		"minItems": true, "maxItems": true, "minProperties": true, "maxProperties": true,
		"minimum": true, "maximum": true, "minLength": true, "maxLength": true,
		"pattern": true, "example": true, "oneOf": true, "anyOf": true,
		"allOf": true, "not": true, "propertyOrdering": true,
		"$schema": true, "additionalProperties": true, "default": true, "exclusiveMinimum": true,
		"propertyNames": true, "prefixItems": true,
	}
	for name := range schema {
		if !allowed[name] {
			return nil, &UnverifiedProtocolError{Feature: "JSON schema 字段 " + name}
		}
	}
	typeName, err := schemaType(schema)
	if err != nil {
		return nil, err
	}
	typeName = strings.ToLower(typeName)
	typeCode, ok := schemaTypeCodes[typeName]
	if !ok && typeName != "" {
		return nil, fmt.Errorf("未知 schema.type %q", typeName)
	}
	wire := []any{typeCode}
	if value, ok := schema["format"]; ok {
		format, err := schemaString(value, "format")
		if err != nil {
			return nil, err
		}
		wire = setWireField(wire, 1, format)
	}
	if value, ok := schema["description"]; ok {
		description, err := schemaString(value, "description")
		if err != nil {
			return nil, err
		}
		wire = setWireField(wire, 2, description)
	}
	if value, ok := schema["nullable"]; ok {
		var nullable bool
		if err := json.Unmarshal(value, &nullable); err != nil {
			return nil, fmt.Errorf("schema.nullable 必须是布尔值")
		}
		wire = setWireField(wire, 3, nullable)
	}
	if value, ok := schema["enum"]; ok {
		values, err := schemaStrings(value, "enum")
		if err != nil {
			return nil, err
		}
		wire = setWireField(wire, 4, values)
	}
	if value, ok := schema["items"]; ok {
		items, err := encodeJSONSchema(value)
		if err != nil {
			return nil, fmt.Errorf("schema.items: %w", err)
		}
		wire = setWireField(wire, 5, items)
	}
	for _, field := range []struct {
		name  string
		index int
	}{
		{name: "minItems", index: 21},
		{name: "maxItems", index: 20},
		{name: "minProperties", index: 8},
		{name: "maxProperties", index: 9},
		{name: "minLength", index: 12},
		{name: "maxLength", index: 13},
	} {
		if value, ok := schema[field.name]; ok {
			integer, err := schemaInteger(value, field.name)
			if err != nil {
				return nil, err
			}
			wire = setWireField(wire, field.index, integer)
		}
	}
	if value, ok := schema["properties"]; ok {
		var properties map[string]json.RawMessage
		if err := json.Unmarshal(value, &properties); err != nil || properties == nil {
			return nil, fmt.Errorf("schema.properties 必须是 JSON object")
		}
		names := make([]string, 0, len(properties))
		for name := range properties {
			names = append(names, name)
		}
		sort.Strings(names)
		entries := make([]any, 0, len(names))
		for _, name := range names {
			property, err := encodeJSONSchema(properties[name])
			if err != nil {
				return nil, fmt.Errorf("schema.properties.%s: %w", name, err)
			}
			entries = append(entries, []any{name, property})
		}
		if len(entries) > 0 {
			wire = setWireField(wire, 6, entries)
		}
	}
	if value, ok := schema["required"]; ok {
		required, err := schemaStrings(value, "required")
		if err != nil {
			return nil, err
		}
		wire = setWireField(wire, 7, required)
	}
	for _, field := range []struct {
		name  string
		index int
	}{
		{name: "minimum", index: 10},
		{name: "maximum", index: 11},
	} {
		if value, ok := schema[field.name]; ok {
			number, err := schemaNumber(value, field.name)
			if err != nil {
				return nil, err
			}
			wire = setWireField(wire, field.index, number)
		}
	}
	if value, ok := schema["pattern"]; ok {
		pattern, err := schemaString(value, "pattern")
		if err != nil {
			return nil, err
		}
		wire = setWireField(wire, 14, pattern)
	}
	if value, ok := schema["example"]; ok {
		var example any
		if err := json.Unmarshal(value, &example); err != nil {
			return nil, fmt.Errorf("schema.example 必须是 JSON value")
		}
		wire = setWireField(wire, 15, encodeWireValue(example))
	}
	for _, field := range []struct {
		name  string
		index int
	}{
		{name: "oneOf", index: 16},
		{name: "anyOf", index: 17},
		{name: "allOf", index: 18},
	} {
		if value, ok := schema[field.name]; ok {
			variants, err := encodeSchemaVariants(value, field.name)
			if err != nil {
				return nil, err
			}
			if len(variants) > 0 {
				wire = setWireField(wire, field.index, variants)
			}
		}
	}
	if value, ok := schema["not"]; ok {
		notSchema, err := encodeJSONSchema(value)
		if err != nil {
			return nil, fmt.Errorf("schema.not: %w", err)
		}
		wire = setWireField(wire, 19, notSchema)
	}
	if value, ok := schema["propertyOrdering"]; ok {
		ordering, err := schemaStrings(value, "propertyOrdering")
		if err != nil {
			return nil, err
		}
		wire = setWireField(wire, 22, ordering)
	}
	return wire, nil
}

// normalizeNullableVariants 将 JSON Schema null 联合映射为 AI Studio nullable
func normalizeNullableVariants(schema map[string]json.RawMessage) error {
	if raw, ok := schema["type"]; ok {
		var typeName string
		if err := json.Unmarshal(raw, &typeName); err != nil {
			var typeNames []string
			if arrayErr := json.Unmarshal(raw, &typeNames); arrayErr != nil || len(typeNames) == 0 {
				return fmt.Errorf("schema.type 必须是字符串或字符串数组")
			}
			nonNull := make([]string, 0, len(typeNames))
			nullable := false
			for _, name := range typeNames {
				if strings.EqualFold(name, "null") {
					nullable = true
					continue
				}
				nonNull = append(nonNull, name)
			}
			if len(nonNull) == 0 {
				return fmt.Errorf("schema.type 必须包含非 null 类型")
			}
			encodedType, marshalErr := json.Marshal(nonNull[0])
			if marshalErr != nil {
				return marshalErr
			}
			schema["type"] = encodedType
			if len(nonNull) > 1 {
				delete(schema, "type")
				variants := make([]map[string]string, 0, len(nonNull))
				for _, name := range nonNull {
					variants = append(variants, map[string]string{"type": name})
				}
				encodedVariants, marshalErr := json.Marshal(variants)
				if marshalErr != nil {
					return marshalErr
				}
				schema["anyOf"] = encodedVariants
			}
			if nullable {
				schema["nullable"] = json.RawMessage("true")
			}
		}
	}
	for _, name := range []string{"anyOf", "oneOf"} {
		raw, ok := schema[name]
		if !ok {
			continue
		}
		var variants []json.RawMessage
		if err := json.Unmarshal(raw, &variants); err != nil {
			return fmt.Errorf("schema.%s 必须是 JSON object 数组", name)
		}
		filtered := variants[:0]
		nullable := false
		for _, variant := range variants {
			if bytes.Equal(bytes.TrimSpace(variant), []byte("true")) || bytes.Equal(bytes.TrimSpace(variant), []byte("false")) {
				filtered = append(filtered, variant)
				continue
			}
			var value map[string]json.RawMessage
			if err := json.Unmarshal(variant, &value); err != nil || value == nil {
				return fmt.Errorf("schema.%s 必须是 JSON object 数组", name)
			}
			typeValue, exists := value["type"]
			if exists {
				typeName, err := schemaString(typeValue, "type")
				if err != nil {
					return err
				}
				if strings.EqualFold(typeName, "null") {
					nullable = true
					continue
				}
			}
			filtered = append(filtered, variant)
		}
		if !nullable {
			continue
		}
		if len(filtered) == 0 {
			return fmt.Errorf("schema.%s 必须包含非 null 类型", name)
		}
		encoded, err := json.Marshal(filtered)
		if err != nil {
			return err
		}
		schema[name] = encoded
		schema["nullable"] = json.RawMessage("true")
	}
	return nil
}

// normalizeConstAndMetadata 将字符串常量和说明字段转换为可发送结构
func normalizeConstAndMetadata(schema map[string]json.RawMessage) error {
	delete(schema, "title")
	delete(schema, "$id")
	delete(schema, "$comment")
	if raw, ok := schema["const"]; ok {
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return fmt.Errorf("schema.const 必须是字符串")
		}
		value, ok := decoded.(string)
		if !ok {
			return fmt.Errorf("schema.const 只支持字符串")
		}
		if rawType, exists := schema["type"]; exists {
			typeName, err := schemaString(rawType, "type")
			if err != nil || !strings.EqualFold(typeName, "string") {
				return fmt.Errorf("schema.const 只支持 string 类型")
			}
		} else {
			schema["type"] = json.RawMessage(`"string"`)
		}
		enum, err := json.Marshal([]string{value})
		if err != nil {
			return err
		}
		schema["enum"] = enum
		delete(schema, "const")
	}
	for _, name := range []string{"anyOf", "oneOf", "allOf"} {
		raw, ok := schema[name]
		if !ok {
			continue
		}
		var variants []json.RawMessage
		if err := json.Unmarshal(raw, &variants); err != nil {
			return fmt.Errorf("schema.%s 必须是 JSON object 数组", name)
		}
		for index, variant := range variants {
			if bytes.Equal(bytes.TrimSpace(variant), []byte("true")) || bytes.Equal(bytes.TrimSpace(variant), []byte("false")) {
				continue
			}
			var subSchema map[string]json.RawMessage
			if err := json.Unmarshal(variant, &subSchema); err != nil || subSchema == nil {
				return fmt.Errorf("schema.%s 必须是 JSON object 数组", name)
			}
			if err := normalizeConstAndMetadata(subSchema); err != nil {
				return fmt.Errorf("schema.%s[%d]: %w", name, index, err)
			}
			encoded, err := json.Marshal(subSchema)
			if err != nil {
				return err
			}
			variants[index] = encoded
		}
		encoded, err := json.Marshal(variants)
		if err != nil {
			return err
		}
		schema[name] = encoded
	}
	return nil
}

// cleanJSONSchemaInput 清理 Schema 可选空值并保留数据值与属性名称
func cleanJSONSchemaInput(raw json.RawMessage) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return json.RawMessage(`{"type":"object"}`), nil
	}
	if bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("false")) {
		return raw, nil
	}
	var schema map[string]json.RawMessage
	if json.Unmarshal(raw, &schema) != nil || schema == nil {
		return nil, fmt.Errorf("schema 必须是 JSON object 或 boolean")
	}
	for _, name := range []string{"type", "format", "description", "nullable", "enum", "items", "properties", "required", "minItems", "maxItems", "minProperties", "maxProperties", "minimum", "maximum", "minLength", "maxLength", "pattern", "oneOf", "anyOf", "allOf", "not", "propertyOrdering"} {
		if bytes.Equal(bytes.TrimSpace(schema[name]), []byte("null")) {
			delete(schema, name)
		}
	}
	for _, name := range []string{"items", "not"} {
		if nested := bytes.TrimSpace(schema[name]); len(nested) > 0 && nested[0] == '{' {
			cleaned, err := cleanJSONSchemaInput(nested)
			if err != nil {
				return nil, fmt.Errorf("schema.%s: %w", name, err)
			}
			schema[name] = cleaned
		}
	}
	if value, ok := schema["properties"]; ok {
		var properties map[string]json.RawMessage
		if json.Unmarshal(value, &properties) != nil || properties == nil {
			return nil, fmt.Errorf("schema.properties 必须是 JSON object")
		}
		for name, property := range properties {
			if bytes.Equal(bytes.TrimSpace(property), []byte("null")) {
				property = json.RawMessage("true")
			}
			cleaned, err := cleanJSONSchemaInput(property)
			if err != nil {
				return nil, fmt.Errorf("schema.properties.%s: %w", name, err)
			}
			properties[name] = cleaned
		}
		schema["properties"], _ = json.Marshal(properties)
	}
	for _, name := range []string{"anyOf", "oneOf", "allOf", "prefixItems"} {
		if raw, ok := schema[name]; ok {
			var variants []json.RawMessage
			if json.Unmarshal(raw, &variants) != nil {
				return nil, fmt.Errorf("schema.%s 必须是 Schema 数组", name)
			}
			filtered := make([]json.RawMessage, 0, len(variants))
			unrestricted := false
			for index, variant := range variants {
				cleaned, err := cleanJSONSchemaInput(variant)
				if err != nil {
					return nil, fmt.Errorf("schema.%s[%d]: %w", name, index, err)
				}
				if name == "anyOf" && bytes.Equal(cleaned, []byte("true")) {
					unrestricted = true
					break
				}
				if name == "allOf" && bytes.Equal(cleaned, []byte("false")) {
					return nil, fmt.Errorf("schema.allOf 禁止所有值")
				}
				if (name == "anyOf" || name == "oneOf") && bytes.Equal(cleaned, []byte("false")) || name == "allOf" && bytes.Equal(cleaned, []byte("true")) {
					continue
				}
				filtered = append(filtered, cleaned)
			}
			if unrestricted || name == "allOf" && len(filtered) == 0 {
				delete(schema, name)
			} else if len(filtered) == 0 && (name == "anyOf" || name == "oneOf") {
				return nil, fmt.Errorf("schema.%s 禁止所有值", name)
			} else {
				schema[name], _ = json.Marshal(filtered)
			}
		}
	}
	return json.Marshal(schema)
}

// normalizeNotSchema 保留排除值并处理不接受任何值的约束
func normalizeNotSchema(schema map[string]json.RawMessage) error {
	raw, ok := schema["not"]
	if !ok {
		return nil
	}
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("false")) {
		delete(schema, "not")
		return nil
	}
	if bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("{}")) {
		return fmt.Errorf("schema.not 禁止所有值")
	}
	var sub map[string]json.RawMessage
	if json.Unmarshal(raw, &sub) == nil && sub != nil {
		var typeName string
		if len(sub) == 1 && json.Unmarshal(sub["type"], &typeName) == nil && strings.EqualFold(typeName, "null") {
			delete(schema, "not")
			schema["nullable"] = json.RawMessage("false")
		}
		return nil
	}
	var values []string
	if json.Unmarshal(raw, &values) != nil {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return fmt.Errorf("schema.not 必须是 Schema 或字符串排除值")
		}
		values = []string{value}
	}
	if len(values) == 0 {
		delete(schema, "not")
		return nil
	}
	schema["not"], _ = json.Marshal(map[string]any{"type": "string", "enum": values})
	return nil
}

// normalizeImplicitType 从节点约束推导类型并保留开放的数组元素
func normalizeImplicitType(schema map[string]json.RawMessage) error {
	if _, ok := schema["type"]; !ok {
		var typed map[string]json.RawMessage
		conflictingType, conflictingItems := false, false
		for _, name := range []string{"anyOf", "oneOf", "allOf"} {
			var variants []map[string]json.RawMessage
			if raw, ok := schema[name]; ok && json.Unmarshal(raw, &variants) == nil {
				for _, variant := range variants {
					if value, ok := variant["type"]; ok {
						if typed == nil {
							typed = variant
						} else {
							conflictingType = conflictingType || !bytes.Equal(bytes.TrimSpace(value), bytes.TrimSpace(typed["type"]))
							conflictingItems = conflictingItems || !bytes.Equal(bytes.TrimSpace(variant["items"]), bytes.TrimSpace(typed["items"]))
						}
					}
				}
			}
		}
		switch {
		case schema["properties"] != nil:
			schema["type"] = json.RawMessage(`"object"`)
		case schema["items"] != nil || schema["prefixItems"] != nil:
			schema["type"] = json.RawMessage(`"array"`)
		default:
			if schema["enum"] != nil || schema["pattern"] != nil || schema["format"] != nil || schema["minLength"] != nil || schema["maxLength"] != nil {
				schema["type"] = json.RawMessage(`"string"`)
			}
		}
		if typed != nil && !conflictingType {
			schema["type"] = typed["type"]
			if items, ok := typed["items"]; ok && schema["items"] == nil && !conflictingItems {
				schema["items"] = items
			}
		}
	}
	var typeName string
	if json.Unmarshal(schema["type"], &typeName) != nil || !strings.EqualFold(typeName, "array") || schema["items"] != nil {
		return nil
	}
	schema["items"] = json.RawMessage(`true`)
	var prefix []map[string]json.RawMessage
	if raw, ok := schema["prefixItems"]; ok && json.Unmarshal(raw, &prefix) == nil {
		typed := make([]map[string]json.RawMessage, 0, len(prefix))
		for _, item := range prefix {
			if _, ok := item["type"]; ok {
				typed = append(typed, item)
			}
		}
		if len(typed) > 0 {
			encoded, err := json.Marshal(map[string]any{"anyOf": typed})
			if err != nil {
				return err
			}
			schema["items"] = encoded
		}
	}
	return nil
}

func schemaType(schema map[string]json.RawMessage) (string, error) {
	if value, ok := schema["type"]; ok {
		typeName, err := schemaString(value, "type")
		if err != nil || typeName == "" {
			return "", fmt.Errorf("schema.type 必须是字符串")
		}
		return typeName, nil
	}
	return "", nil
}

func schemaInteger(raw json.RawMessage, name string) (int64, error) {
	value, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("schema.%s 必须是非负整数", name)
	}
	return value, nil
}

func schemaNumber(raw json.RawMessage, name string) (float64, error) {
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("schema.%s 必须是数字", name)
	}
	return value, nil
}

func encodeSchemaVariants(raw json.RawMessage, name string) ([]any, error) {
	var variants []json.RawMessage
	if err := json.Unmarshal(raw, &variants); err != nil {
		return nil, fmt.Errorf("schema.%s 必须是 JSON object 数组", name)
	}
	encoded := make([]any, 0, len(variants))
	for index, variant := range variants {
		wire, err := encodeJSONSchema(variant)
		if err != nil {
			return nil, fmt.Errorf("schema.%s[%d]: %w", name, index, err)
		}
		encoded = append(encoded, wire)
	}
	return encoded, nil
}

func schemaString(raw json.RawMessage, name string) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("schema.%s 必须是字符串", name)
	}
	return value, nil
}

func schemaStrings(raw json.RawMessage, name string) ([]string, error) {
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("schema.%s 必须是字符串数组", name)
	}
	return values, nil
}

func setWireField(wire []any, index int, value any) []any {
	for len(wire) <= index {
		wire = append(wire, nil)
	}
	wire[index] = value
	return wire
}

// normalizeFunctionParameters 将零参数定义归一化为对象
func normalizeFunctionParameters(raw json.RawMessage) (json.RawMessage, error) {
	cleaned, err := cleanJSONSchemaInput(raw)
	if err == nil && (bytes.Equal(cleaned, []byte("{}")) || bytes.Equal(cleaned, []byte("true"))) {
		cleaned = json.RawMessage(`{"type":"object"}`)
	}
	return cleaned, err
}

// requestNeedsBuildSchema 按开放节点选择可表达该结构的通道
func requestNeedsBuildSchema(request GenerateRequest) bool {
	schemas := []json.RawMessage{request.Config.ResponseSchema}
	for _, declaration := range request.Tools.Functions {
		if request.Tools.ToolConfig.Mode == "none" {
			break
		}
		raw, err := normalizeFunctionParameters(declaration.Parameters)
		if err == nil {
			if schemaHasBooleanNode(raw) {
				return true
			}
			schemas = append(schemas, raw)
		}
	}
	for _, raw := range schemas {
		if len(raw) == 0 {
			continue
		}
		wire, err := encodeJSONSchema(raw)
		if err == nil && schemaWireNeedsBuild(wire) {
			return true
		}
	}
	return false
}

// schemaHasBooleanNode 查找函数参数中需要原生 JSON Schema 的布尔节点
func schemaHasBooleanNode(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("false")) {
		return true
	}
	var schema map[string]json.RawMessage
	if json.Unmarshal(raw, &schema) != nil {
		return false
	}
	for _, name := range []string{"items", "not"} {
		if name == "not" && (bytes.Equal(bytes.TrimSpace(schema[name]), []byte("false")) || bytes.Equal(bytes.TrimSpace(schema[name]), []byte("true"))) {
			continue
		}
		if schemaHasBooleanNode(schema[name]) {
			return true
		}
	}
	var properties map[string]json.RawMessage
	_ = json.Unmarshal(schema["properties"], &properties)
	for _, property := range properties {
		if schemaHasBooleanNode(property) {
			return true
		}
	}
	for _, name := range []string{"anyOf", "oneOf", "allOf", "prefixItems"} {
		var variants []json.RawMessage
		_ = json.Unmarshal(schema[name], &variants)
		for _, variant := range variants {
			if schemaHasBooleanNode(variant) {
				return true
			}
		}
	}
	return false
}

// schemaWireNeedsBuild 查找 Playground 不接受的无类型节点
func schemaWireNeedsBuild(wire []any) bool {
	if len(wire) > 0 && wire[0] == int64(0) {
		return true
	}
	for _, index := range []int{5, 19} {
		if len(wire) > index && wire[index] != nil && schemaWireNeedsBuild(wire[index].([]any)) {
			return true
		}
	}
	if len(wire) > 6 && wire[6] != nil {
		for _, entry := range wire[6].([]any) {
			if schemaWireNeedsBuild(entry.([]any)[1].([]any)) {
				return true
			}
		}
	}
	for _, index := range []int{16, 17, 18} {
		if len(wire) > index && wire[index] != nil {
			for _, variant := range wire[index].([]any) {
				if schemaWireNeedsBuild(variant.([]any)) {
					return true
				}
			}
		}
	}
	return false
}
