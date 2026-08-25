package aistudio

import (
	"encoding/json"
	"fmt"
	"sort"
)

func encodeRequestedTools(tools Tools) ([]any, bool, error) {
	switch tools.ToolConfig.Mode {
	case "none":
		return nil, true, nil
	case "", "auto":
	default:
		return nil, false, fmt.Errorf("tool choice 只支持 auto 或 none")
	}
	if len(tools.Functions) == 0 && len(tools.Google) == 0 {
		return nil, false, nil
	}
	wire := make([]any, 0, len(tools.Google)+1)
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
	searchEnabled := false
	imageSearchEnabled := false
	for _, name := range tools.Google {
		searchEnabled = searchEnabled || name == "google_search"
		imageSearchEnabled = imageSearchEnabled || name == "image_search"
	}
	searchEncoded := false
	for _, name := range tools.Google {
		switch name {
		case "code_execution":
			wire = append(wire, []any{[]any{}})
		case "google_search", "image_search":
			if !searchEncoded {
				wire = append(wire, encodeSearchTool(searchEnabled, imageSearchEnabled))
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
	return wire, true, nil
}

func encodeSearchTool(searchEnabled bool, imageSearchEnabled bool) []any {
	length := 1
	if imageSearchEnabled {
		length = 2
	}
	searchTypes := make([]any, length)
	if searchEnabled {
		searchTypes[0] = []any{}
	}
	if imageSearchEnabled {
		searchTypes[1] = []any{}
	}
	return []any{nil, nil, nil, []any{nil, searchTypes}}
}

func encodeFunctionDeclaration(declaration FunctionDeclaration) ([]any, error) {
	if declaration.Name == "" {
		return nil, fmt.Errorf("function declaration 缺少名称")
	}
	length := 1
	if declaration.Description != "" {
		length = 2
	}
	if len(declaration.Parameters) > 0 {
		length = 3
	}
	wire := make([]any, length)
	wire[0] = declaration.Name
	if declaration.Description != "" {
		wire[1] = declaration.Description
	}
	if len(declaration.Parameters) > 0 {
		parameters, err := encodeJSONSchema(declaration.Parameters)
		if err != nil {
			return nil, fmt.Errorf("parameters: %w", err)
		}
		wire[2] = parameters
	}
	return wire, nil
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
	if len(values) < 2 {
		return FunctionCall{}, &ProtocolEvidenceError{Method: "GenerateContent", Path: path, Detail: "function call 字段不足", Raw: raw}
	}
	name, err := rawString(values[0], path+"[0]", raw)
	if err != nil {
		return FunctionCall{}, withMethod(err, "GenerateContent")
	}
	arguments, err := decodeWireStruct(values[1], path+"[1]", evidence)
	if err != nil {
		return FunctionCall{}, err
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
