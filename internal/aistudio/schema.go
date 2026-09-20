package aistudio

import (
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
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(raw, &schema); err != nil || schema == nil {
		return nil, fmt.Errorf("schema must be a JSON object")
	}
	if err := normalizeConstAndMetadata(schema); err != nil {
		return nil, err
	}
	if err := normalizeNullableVariants(schema); err != nil {
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
	}
	for name := range schema {
		if !allowed[name] {
			return nil, &UnverifiedProtocolError{Feature: "JSON schema field " + name}
		}
	}
	typeName, err := schemaType(schema)
	if err != nil {
		return nil, err
	}
	typeName = strings.ToLower(typeName)
	typeCode, ok := schemaTypeCodes[typeName]
	if !ok {
		return nil, fmt.Errorf("unknown schema.type %q", typeName)
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
			return nil, fmt.Errorf("schema.nullable must be a boolean")
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
			return nil, fmt.Errorf("schema.properties must be a JSON object")
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
			return nil, fmt.Errorf("schema.example must be a JSON value")
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

// normalizeNullableVariants maps JSON Schema null unions to AI Studio nullable
func normalizeNullableVariants(schema map[string]json.RawMessage) error {
	if raw, ok := schema["type"]; ok {
		var typeName string
		if err := json.Unmarshal(raw, &typeName); err != nil {
			var typeNames []string
			if arrayErr := json.Unmarshal(raw, &typeNames); arrayErr != nil || len(typeNames) == 0 {
				return fmt.Errorf("schema.type must be a string or string array")
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
				return fmt.Errorf("schema.type must contain a non-null type")
			}
			encodedType, marshalErr := json.Marshal(nonNull[0])
			if marshalErr != nil {
				return marshalErr
			}
			schema["type"] = encodedType
			if len(nonNull) > 1 {
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
			return fmt.Errorf("schema.%s must be an array of JSON objects", name)
		}
		filtered := variants[:0]
		nullable := false
		for _, variant := range variants {
			var value map[string]json.RawMessage
			if err := json.Unmarshal(variant, &value); err != nil || value == nil {
				return fmt.Errorf("schema.%s must be an array of JSON objects", name)
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
			return fmt.Errorf("schema.%s must contain a non-null type", name)
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

// normalizeConstAndMetadata converts string constants and metadata fields into sendable structures
func normalizeConstAndMetadata(schema map[string]json.RawMessage) error {
	delete(schema, "title")
	delete(schema, "$id")
	delete(schema, "$comment")
	if raw, ok := schema["const"]; ok {
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return fmt.Errorf("schema.const must be a string")
		}
		value, ok := decoded.(string)
		if !ok {
			return fmt.Errorf("schema.const only supports strings")
		}
		if rawType, exists := schema["type"]; exists {
			typeName, err := schemaString(rawType, "type")
			if err != nil || !strings.EqualFold(typeName, "string") {
				return fmt.Errorf("schema.const only supports string type")
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
			return fmt.Errorf("schema.%s must be an array of JSON objects", name)
		}
		for index, variant := range variants {
			var subSchema map[string]json.RawMessage
			if err := json.Unmarshal(variant, &subSchema); err != nil || subSchema == nil {
				return fmt.Errorf("schema.%s must be an array of JSON objects", name)
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

func schemaType(schema map[string]json.RawMessage) (string, error) {
	if value, ok := schema["type"]; ok {
		typeName, err := schemaString(value, "type")
		if err != nil || typeName == "" {
			return "", fmt.Errorf("schema.type must be a string")
		}
		return typeName, nil
	}
	for _, name := range []string{"anyOf", "oneOf", "allOf"} {
		value, ok := schema[name]
		if !ok {
			continue
		}
		var variants []map[string]json.RawMessage
		if err := json.Unmarshal(value, &variants); err != nil {
			return "", fmt.Errorf("schema.%s must be an array of JSON objects", name)
		}
		for _, variant := range variants {
			if typeValue, exists := variant["type"]; exists {
				return schemaString(typeValue, "type")
			}
		}
	}
	return "", fmt.Errorf("schema.type must be a string")
}

func schemaInteger(raw json.RawMessage, name string) (int64, error) {
	value, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("schema.%s must be a non-negative integer", name)
	}
	return value, nil
}

func schemaNumber(raw json.RawMessage, name string) (float64, error) {
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("schema.%s must be a number", name)
	}
	return value, nil
}

func encodeSchemaVariants(raw json.RawMessage, name string) ([]any, error) {
	var variants []json.RawMessage
	if err := json.Unmarshal(raw, &variants); err != nil {
		return nil, fmt.Errorf("schema.%s must be an array of JSON objects", name)
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
		return "", fmt.Errorf("schema.%s must be a string", name)
	}
	return value, nil
}

func schemaStrings(raw json.RawMessage, name string) ([]string, error) {
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("schema.%s must be an array of strings", name)
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
