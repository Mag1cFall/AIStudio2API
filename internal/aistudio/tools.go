package aistudio

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

func encodeRequestedTools(tools Tools) ([]any, bool, error) {
	switch tools.ToolConfig.Mode {
	case "none":
		return nil, true, nil
	case "", "auto", "required", "validated":
	default:
		return nil, false, fmt.Errorf("未知 tool choice %q", tools.ToolConfig.Mode)
	}
	if len(tools.Functions) == 0 && len(tools.Google) == 0 && tools.GoogleSearch == nil {
		return nil, false, nil
	}
	wire := make([]any, 0, len(tools.Google)+2)
	if len(tools.Functions) > 0 {
		declarations := make([]any, 0, len(tools.Functions))
		for index, declaration := range tools.Functions {
			encoded, err := encodeFunctionDeclaration(declaration)
			if err != nil {
				return nil, false, fmt.Errorf("编码 function declaration %d: %w", index, err)
			}
			declarations = append(declarations, encoded)
		}
		wire = append(wire, []any{nil, declarations})
	}
	search := GoogleSearchOptions{}
	searchRequested := tools.GoogleSearch != nil
	if tools.GoogleSearch != nil {
		search = *tools.GoogleSearch
	}
	for _, name := range tools.Google {
		search.WebSearch = search.WebSearch || name == "google_search"
		search.ImageSearch = search.ImageSearch || name == "image_search"
		searchRequested = searchRequested || name == "google_search" || name == "image_search"
	}
	if searchRequested && !search.WebSearch && !search.ImageSearch {
		search.WebSearch = true
	}
	searchEncoded := false
	for _, name := range tools.Google {
		switch name {
		case "code_execution":
			wire = append(wire, []any{[]any{}})
		case "google_search", "image_search":
			if !searchEncoded {
				wire = append(wire, encodeSearchTool(search))
				searchEncoded = true
			}
		case "url_context":
			tool := make([]any, 8)
			tool[7] = []any{}
			wire = append(wire, tool)
		case "google_maps":
			tool := make([]any, 11)
			tool[10] = []any{}
			wire = append(wire, tool)
		default:
			return nil, false, fmt.Errorf("未知 Google tool %q", name)
		}
	}
	if searchRequested && !searchEncoded {
		wire = append(wire, encodeSearchTool(search))
	}
	return wire, true, nil
}

func encodeSearchTool(options GoogleSearchOptions) []any {
	length := 1
	if options.ImageSearch {
		length = 2
	}
	searchTypes := make([]any, length)
	if options.WebSearch {
		searchTypes[0] = []any{}
	}
	if options.ImageSearch {
		searchTypes[1] = []any{}
	}
	searchConfig := []any{nil, searchTypes}
	if options.TimeRange != nil {
		searchConfig[0] = encodeGoogleSearchTimeRange(*options.TimeRange)
	}
	return []any{nil, nil, nil, searchConfig}
}

func encodeGoogleSearchTimeRange(value GoogleSearchTimeRange) []any {
	wire := make([]any, 2)
	if !value.StartTime.IsZero() {
		wire[0] = encodeGoogleTimestamp(value.StartTime)
	}
	if !value.EndTime.IsZero() {
		wire[1] = encodeGoogleTimestamp(value.EndTime)
	}
	return wire
}

func encodeGoogleTimestamp(value time.Time) []any {
	return []any{strconv.FormatInt(value.Unix(), 10)}
}

func encodeFunctionDeclaration(declaration FunctionDeclaration) ([]any, error) {
	if declaration.Name == "" {
		return nil, fmt.Errorf("function declaration 缺少名称")
	}
	raw, err := normalizeFunctionParameters(declaration.Parameters)
	if err != nil {
		return nil, fmt.Errorf("parameters: %w", err)
	}
	parameters, encodingError := encodeJSONSchema(raw)
	description := declaration.Description
	if encodingError != nil {
		var schema any
		if err := json.Unmarshal(raw, &schema); err != nil {
			return nil, err
		}
		shape, err := json.Marshal(toolSchemaShape(schema))
		if err != nil {
			return nil, err
		}
		parameters, err = encodeJSONSchema(shape)
		if err != nil {
			return nil, fmt.Errorf("parameters: %w", err)
		}
	}
	if encodingError != nil || schemaWireNeedsBuild(parameters) || declaration.Strict || strings.Contains(string(raw), `"additionalProperties"`) || strings.Contains(string(raw), `"prefixItems"`) || strings.Contains(string(raw), `"exclusiveMinimum"`) || strings.Contains(string(raw), `"propertyNames"`) {
		description += "\nArguments must follow this JSON Schema: " + string(raw)
	}
	projectToolSchema(parameters)
	return []any{declaration.Name, description, parameters}, nil
}

// toolSchemaShape 提取工具参数的层级与类型供 Playground 编码
func toolSchemaShape(value any) map[string]any {
	result := map[string]any{}
	object, ok := value.(map[string]any)
	if !ok {
		return result
	}
	for _, key := range []string{"type", "format", "nullable", "description", "required", "minimum", "maximum", "minLength", "maxLength", "pattern", "minItems", "maxItems", "minProperties", "maxProperties", "propertyOrdering"} {
		if item, exists := object[key]; exists {
			result[key] = item
		}
	}
	if constant, exists := object["const"]; exists && constant != nil {
		encoded, _ := json.Marshal(constant)
		description, _ := result["description"].(string)
		result["description"] = description + "\nThe value must be: " + string(encoded)
		switch value := constant.(type) {
		case float64:
			result["type"] = "number"
			if math.Trunc(value) == value {
				result["type"] = "integer"
			}
			result["minimum"], result["maximum"] = value, value
		case bool:
			result["type"] = "boolean"
		case string:
			result["type"], result["enum"] = "string", []any{value}
		case []any:
			result["type"] = "array"
		case map[string]any:
			result["type"] = "object"
		}
	}
	if values, ok := object["enum"].([]any); ok && len(values) > 0 {
		stringsOnly := true
		for _, value := range values {
			_, isString := value.(string)
			stringsOnly = stringsOnly && isString
		}
		if stringsOnly {
			result["enum"] = values
		}
	}
	if typeName, ok := result["type"].(string); ok && strings.EqualFold(typeName, "null") {
		result["type"], result["nullable"] = "string", true
		result["description"] = "The value must be JSON null"
	}
	if types, ok := result["type"].([]any); ok && len(types) > 0 {
		nullOnly := true
		for _, name := range types {
			nullOnly = nullOnly && strings.EqualFold(fmt.Sprint(name), "null")
		}
		if nullOnly {
			result["type"], result["nullable"] = "string", true
			result["description"] = "The value must be JSON null"
		}
	}
	if constant, exists := object["const"]; exists && constant == nil {
		result["type"], result["nullable"] = "string", true
		result["description"] = "The value must be JSON null"
	}
	if properties, ok := object["properties"].(map[string]any); ok {
		children := map[string]any{}
		for key, child := range properties {
			children[key] = toolSchemaShape(child)
		}
		result["properties"] = children
	}
	if child, exists := object["items"]; exists {
		result["items"] = toolSchemaShape(child)
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if branches, ok := object[key].([]any); ok {
			children := make([]any, len(branches))
			for index, branch := range branches {
				children[index] = toolSchemaShape(branch)
			}
			result[key] = children
		}
	}
	return result
}

// projectToolSchema 补齐 Playground 载体类型并保留联合分支
func projectToolSchema(wire []any) {
	for _, index := range []int{5, 19} {
		if len(wire) > index && wire[index] != nil {
			projectToolSchema(wire[index].([]any))
		}
	}
	if len(wire) > 6 && wire[6] != nil {
		for _, property := range wire[6].([]any) {
			projectToolSchema(property.([]any)[1].([]any))
		}
	}
	for _, index := range []int{16, 17, 18} {
		if len(wire) <= index || wire[index] == nil {
			continue
		}
		for _, variant := range wire[index].([]any) {
			projectToolSchema(variant.([]any))
		}
		if wire[0] == int64(0) {
			first := wire[index].([]any)[0].([]any)
			wire[0] = first[0]
			if first[0] == int64(5) && len(first) > 5 {
				setWireField(wire, 5, first[5])
			}
		}
	}
	if wire[0] == int64(0) {
		wire[0] = int64(1)
	}
}

func hasMethod(model Model, method string) bool {
	for _, candidate := range model.Methods {
		if candidate == method {
			return true
		}
	}
	return false
}

func validateFunctionCall(call *FunctionCall) error {
	if call == nil || call.Name == "" {
		return fmt.Errorf("function call 缺少名称")
	}
	return nil
}

func decodeFunctionCall(raw json.RawMessage, path string, evidence json.RawMessage) (FunctionCall, error) {
	values, err := rawArray(raw, path, evidence)
	if err != nil {
		return FunctionCall{}, withMethod(err, "GenerateContent")
	}
	if len(values) == 0 {
		return FunctionCall{}, &ProtocolEvidenceError{Method: "GenerateContent", Path: path, Detail: "function call 缺少名称", Raw: raw}
	}
	name, err := rawString(values[0], path+"[0]", raw)
	if err != nil {
		return FunctionCall{}, withMethod(err, "GenerateContent")
	}
	arguments := json.RawMessage(`{}`)
	if argumentsRaw := rawAt(values, 1); !isJSONNull(argumentsRaw) {
		arguments, err = decodeWireStruct(argumentsRaw, path+"[1]", evidence)
		if err != nil {
			return FunctionCall{}, err
		}
	}
	id := ""
	if idRaw := rawAt(values, 2); !isJSONNull(idRaw) {
		id, err = rawString(idRaw, path+"[2]", raw)
		if err != nil {
			return FunctionCall{}, withMethod(err, "GenerateContent")
		}
	}
	call := FunctionCall{ID: id, Name: name, Arguments: arguments}
	if err := validateFunctionCall(&call); err != nil {
		return FunctionCall{}, &ProtocolEvidenceError{Method: "GenerateContent", Path: path, Detail: err.Error(), Raw: raw}
	}
	return call, nil
}

func encodeWireStructJSON(raw json.RawMessage) ([]any, error) {
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, fmt.Errorf("必须是 JSON object")
	}
	return encodeWireStruct(object), nil
}

func encodeWireStruct(object map[string]any) []any {
	if len(object) == 0 {
		return []any{}
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]any, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, []any{key, encodeWireValue(object[key])})
	}
	return []any{entries}
}

func encodeWireValue(value any) []any {
	switch typed := value.(type) {
	case nil:
		return []any{int64(0)}
	case float64:
		return []any{nil, typed}
	case string:
		return []any{nil, nil, typed}
	case bool:
		return []any{nil, nil, nil, typed}
	case map[string]any:
		return []any{nil, nil, nil, nil, encodeWireStruct(typed)}
	case []any:
		values := make([]any, 0, len(typed))
		for _, item := range typed {
			values = append(values, encodeWireValue(item))
		}
		return []any{nil, nil, nil, nil, nil, []any{values}}
	default:
		panic(fmt.Sprintf("json.Unmarshal 返回未识别类型 %T", value))
	}
}

func decodeWireStruct(raw json.RawMessage, path string, evidence json.RawMessage) (json.RawMessage, error) {
	object, err := decodeWireStructObject(raw, path, evidence)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("编码 function arguments: %w", err)
	}
	return encoded, nil
}

func decodeWireStructObject(raw json.RawMessage, path string, evidence json.RawMessage) (map[string]any, error) {
	fields, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	object := make(map[string]any)
	entriesRaw := rawAt(fields, 0)
	if isJSONNull(entriesRaw) {
		return object, nil
	}
	entries, err := rawArray(entriesRaw, path+"[0]", evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	for index, entryRaw := range entries {
		entryPath := fmt.Sprintf("%s[0][%d]", path, index)
		entry, err := rawArray(entryRaw, entryPath, evidence)
		if err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
		if len(entry) != 2 {
			return nil, &ProtocolEvidenceError{Method: "GenerateContent", Path: entryPath, Detail: "Struct map entry 字段数量错误", Raw: evidence}
		}
		key, err := rawString(entry[0], entryPath+"[0]", evidence)
		if err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
		value, err := decodeWireValue(entry[1], entryPath+"[1]", evidence)
		if err != nil {
			return nil, err
		}
		object[key] = value
	}
	return object, nil
}

func decodeWireValue(raw json.RawMessage, path string, evidence json.RawMessage) (any, error) {
	fields, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	variant := -1
	for index := 0; index < 6; index++ {
		if !isJSONNull(rawAt(fields, index)) {
			if variant >= 0 {
				return nil, &ProtocolEvidenceError{Method: "GenerateContent", Path: path, Detail: "Value 同时设置多个 oneof 字段", Raw: evidence}
			}
			variant = index
		}
	}
	switch variant {
	case 0:
		code, err := rawInt64(fields[0], path+"[0]", evidence)
		if err != nil || code != 0 {
			return nil, &ProtocolEvidenceError{Method: "GenerateContent", Path: path + "[0]", Detail: "null Value 枚举无效", Raw: evidence}
		}
		return nil, nil
	case 1:
		var number float64
		if err := json.Unmarshal(fields[1], &number); err != nil {
			return nil, &ProtocolEvidenceError{Method: "GenerateContent", Path: path + "[1]", Detail: "number Value 无效", Raw: evidence}
		}
		return number, nil
	case 2:
		value, err := rawString(fields[2], path+"[2]", evidence)
		if err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
		return value, nil
	case 3:
		value, err := rawBool(fields[3], path+"[3]", evidence)
		if err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
		return value, nil
	case 4:
		return decodeWireStructObject(fields[4], path+"[4]", evidence)
	case 5:
		return decodeWireList(fields[5], path+"[5]", evidence)
	default:
		return nil, &ProtocolEvidenceError{Method: "GenerateContent", Path: path, Detail: "Value 缺少 oneof 字段", Raw: evidence}
	}
}

func decodeWireList(raw json.RawMessage, path string, evidence json.RawMessage) ([]any, error) {
	fields, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	itemsRaw := rawAt(fields, 0)
	if isJSONNull(itemsRaw) {
		return []any{}, nil
	}
	items, err := rawArray(itemsRaw, path+"[0]", evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	values := make([]any, 0, len(items))
	for index, item := range items {
		value, err := decodeWireValue(item, fmt.Sprintf("%s[0][%d]", path, index), evidence)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}
