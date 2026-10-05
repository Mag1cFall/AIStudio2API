package aistudio

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// prepareSchemaRequest never changes Build routing or response schemas. The opt-in
// fallback narrows missing tool types and typed unions on a private request copy.
func (s *PooledService) prepareSchemaRequest(request GenerateRequest) (GenerateRequest, bool, error) {
	if s.pool.BuildEnabled() || !requestNeedsBuildSchema(request) {
		return request, false, nil
	}
	if !s.SchemaFallback {
		return request, false, fmt.Errorf("%w: 此 JSON Schema 需要 Build 通道；请显式声明工具参数类型，或评估 PLAYGROUND_SCHEMA_FALLBACK 的有损兼容模式", ErrInvalidArgument)
	}
	original := request
	request.Tools.Functions = slices.Clone(request.Tools.Functions)
	if request.Tools.ToolConfig.Mode != "none" {
		for index, declaration := range request.Tools.Functions {
			raw, err := normalizeFunctionParameters(declaration.Parameters)
			if err != nil {
				return original, false, fmt.Errorf("%w: function %d parameters: %w", ErrInvalidArgument, index, err)
			}
			wire, err := encodeJSONSchema(raw)
			if err != nil {
				return original, false, fmt.Errorf("%w: function %d parameters: %w", ErrInvalidArgument, index, err)
			}
			if !schemaHasBooleanNode(raw) && !schemaWireNeedsBuild(wire) {
				continue
			}
			parameters, err := fallbackToolSchema(raw)
			if err != nil {
				return original, false, fmt.Errorf("%w: function %d parameters 无法回退 Playground: %w", ErrInvalidArgument, index, err)
			}
			request.Tools.Functions[index].Parameters = parameters
		}
	}
	if requestNeedsBuildSchema(request) {
		return original, false, fmt.Errorf("%w: 此 JSON Schema 需要 Build 通道；兼容回退不改写 response schema 或无法表示的约束", ErrInvalidArgument)
	}
	return request, true, nil
}

// fallbackToolSchema restores legacy root types only at positive tool nodes.
// Typed disjunctions retain every branch/constraint and gain a root type. This
// narrows alternatives; it is not lossless native JSON Schema support.
func fallbackToolSchema(raw json.RawMessage) (json.RawMessage, error) {
	var schema map[string]json.RawMessage
	if json.Unmarshal(raw, &schema) != nil || schema == nil {
		return nil, fmt.Errorf("原生布尔 Schema 需要 Build 通道")
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
	if err := fallbackUnionRootType(schema); err != nil {
		return nil, err
	}
	_, hadItems := schema["items"]
	if err := normalizeImplicitType(schema); err != nil {
		return nil, err
	}
	if _, typed := schema["type"]; !typed {
		for key := range schema {
			switch key {
			case "description", "nullable", "example", "default", "$schema":
			default:
				return nil, fmt.Errorf("含 %s 的无类型节点需要显式类型或 Build 通道", key)
			}
		}
		schema["type"] = json.RawMessage(`"string"`)
	}
	var typeName string
	_ = json.Unmarshal(schema["type"], &typeName)
	if strings.EqualFold(typeName, "array") && !hadItems && schema["prefixItems"] == nil {
		schema["items"] = json.RawMessage(`{"type":"string"}`)
	}
	if items, ok := schema["items"]; ok {
		value, err := fallbackToolSchema(items)
		if err != nil {
			return nil, fmt.Errorf("items: %w", err)
		}
		schema["items"] = value
	}
	if rawProperties, ok := schema["properties"]; ok {
		var properties map[string]json.RawMessage
		if json.Unmarshal(rawProperties, &properties) != nil || properties == nil {
			return nil, fmt.Errorf("properties 必须是 JSON object")
		}
		names := make([]string, 0, len(properties))
		for name := range properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			value, err := fallbackToolSchema(properties[name])
			if err != nil {
				return nil, fmt.Errorf("properties.%s: %w", name, err)
			}
			properties[name] = value
		}
		schema["properties"], _ = json.Marshal(properties)
	}
	result, err := json.Marshal(schema)
	if err == nil {
		_, err = encodeJSONSchema(result)
	}
	return result, err
}

// fallbackUnionRootType narrows a single anyOf/oneOf to its first concrete type,
// as legacy Playground encoding did, without deleting any alternative. Keeping
// oneOf intact also preserves exclusivity rather than flattening overlapping cases.
func fallbackUnionRootType(schema map[string]json.RawMessage) error {
	if _, typed := schema["type"]; typed {
		return nil
	}
	name := "anyOf"
	if _, exists := schema[name]; !exists {
		name = "oneOf"
	}
	raw, exists := schema[name]
	if !exists {
		return nil
	}
	for key := range schema {
		if key == name {
			continue
		}
		switch key {
		case "description", "nullable", "example", "default", "$schema":
		default:
			return fmt.Errorf("含 %s 的组合节点需要显式类型或 Build 通道", key)
		}
	}
	var variants []json.RawMessage
	if json.Unmarshal(raw, &variants) != nil || len(variants) == 0 {
		return fmt.Errorf("schema.%s 必须是非空 Schema 数组", name)
	}
	var first map[string]json.RawMessage
	if json.Unmarshal(variants[0], &first) != nil || first == nil {
		return fmt.Errorf("schema.%s 首个分支需要明确类型", name)
	}
	if err := normalizeConstAndMetadata(first); err != nil {
		return err
	}
	if err := normalizeImplicitType(first); err != nil {
		return err
	}
	var typeName string
	if json.Unmarshal(first["type"], &typeName) != nil || schemaTypeCodes[strings.ToLower(typeName)] == 0 {
		return fmt.Errorf("schema.%s 首个分支需要明确类型", name)
	}
	schema["type"] = first["type"]
	if strings.EqualFold(typeName, "array") {
		items, err := encodeJSONSchema(first["items"])
		if err != nil || schemaWireNeedsBuild(items) {
			return fmt.Errorf("schema.%s 首个数组分支需要可由 Playground 表达的明确 items", name)
		}
		schema["items"] = first["items"]
	}
	return nil
}
