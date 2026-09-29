package aistudio

import "encoding/json"

// buildResponseSchema 将已校验的 Schema 字段投影为 Build 接收的 protobuf JSON
func buildResponseSchema(wire []any) map[string]any {
	names := [...]string{
		"type", "format", "description", "nullable", "enum", "items", "properties", "required",
		"minProperties", "maxProperties", "minimum", "maximum", "minLength", "maxLength", "pattern",
		"example", "oneOf", "anyOf", "allOf", "not", "maxItems", "minItems", "propertyOrdering",
	}
	types := [...]string{"TYPE_UNSPECIFIED", "STRING", "NUMBER", "INTEGER", "BOOLEAN", "ARRAY", "OBJECT"}
	schema := make(map[string]any)
	for index, value := range wire {
		if value == nil {
			continue
		}
		switch index {
		case 0:
			value = types[value.(int64)]
		case 5, 19:
			value = buildResponseSchema(value.([]any))
		case 6:
			properties := make(map[string]any)
			for _, item := range value.([]any) {
				entry := item.([]any)
				properties[entry[0].(string)] = buildResponseSchema(entry[1].([]any))
			}
			value = properties
		case 15:
			raw, _ := json.Marshal(value)
			value, _ = decodeWireValue(raw, "schema.example", raw)
		case 16, 17, 18:
			variants := make([]any, 0, len(value.([]any)))
			for _, variant := range value.([]any) {
				variants = append(variants, buildResponseSchema(variant.([]any)))
			}
			value = variants
		}
		schema[names[index]] = value
	}
	return schema
}
