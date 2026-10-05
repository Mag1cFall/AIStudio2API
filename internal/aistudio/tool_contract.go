package aistudio

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dlclark/regexp2/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// toolContract 保存客户端显式要求的调用模式与参数契约
type toolContract struct {
	required   bool
	parallel   *bool
	names      map[string]bool
	schemas    map[string]*jsonschema.Schema
	serverSide bool
}

// prepareToolRequest 将工具选择与约束转换为共同生成请求
func prepareToolRequest(request GenerateRequest) (GenerateRequest, *toolContract, error) {
	config := request.Tools.ToolConfig
	if config.Mode == "none" {
		return request, nil, nil
	}
	if search := request.Tools.GoogleSearch; search != nil {
		if search.ContextSize != "" {
			request.System += "\nUse " + search.ContextSize + " search context depth."
		}
		if len(search.UserLocation) > 0 {
			request.System += "\nApproximate search user location: " + string(search.UserLocation)
		}
		if len(search.AllowedDomains) > 0 {
			request.System += "\nRestrict web searches to these domains using site: queries: " + strings.Join(search.AllowedDomains, ", ")
		}
	}
	contract := &toolContract{required: config.Mode == "required", parallel: config.ParallelCalls, names: map[string]bool{}, schemas: map[string]*jsonschema.Schema{}}
	if contract.parallel != nil && *contract.parallel || len(request.Tools.Functions) == 0 {
		contract.parallel = nil
	}
	functions := make([]FunctionDeclaration, 0, len(request.Tools.Functions))
	for _, declaration := range request.Tools.Functions {
		if len(config.AllowedFunctionNames) > 0 && !slices.Contains(config.AllowedFunctionNames, declaration.Name) {
			continue
		}
		if contract.names[declaration.Name] {
			return request, nil, fmt.Errorf("function %q 重复", declaration.Name)
		}
		if config.Mode == "validated" {
			declaration.Strict = true
		}
		contract.names[declaration.Name] = true
		functions = append(functions, declaration)
		if !declaration.Strict && config.Mode != "validated" {
			continue
		}
		raw, err := normalizeFunctionParameters(declaration.Parameters)
		if err != nil {
			return request, nil, err
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return request, nil, err
		}
		document = validationSchema(document)
		compiler := jsonschema.NewCompiler()
		compiler.UseLoader(toolSchemaLoader{})
		compiler.UseRegexpEngine(compileToolPattern)
		const resource = "https://tools.invalid/parameters.json"
		if err := compiler.AddResource(resource, document); err != nil {
			return request, nil, err
		}
		schema, err := compiler.Compile(resource)
		if err != nil {
			return request, nil, fmt.Errorf("function %q schema: %w", declaration.Name, err)
		}
		contract.schemas[declaration.Name] = schema
	}
	for _, name := range config.AllowedFunctionNames {
		if !contract.names[name] {
			return request, nil, fmt.Errorf("tool choice 引用了未声明函数 %q", name)
		}
	}
	contract.serverSide = len(config.AllowedFunctionNames) == 0 && (len(request.Tools.Google) > 0 || request.Tools.GoogleSearch != nil)
	if contract.required && len(functions) == 0 && !contract.serverSide {
		return request, nil, fmt.Errorf("required tool choice 需要至少一个工具")
	}
	if len(config.AllowedFunctionNames) > 0 || config.Mode == "validated" {
		request.Tools.Functions = functions
	}
	if contract.required {
		names := make([]string, 0, len(functions))
		for _, declaration := range functions {
			names = append(names, declaration.Name)
		}
		if contract.serverSide {
			names = append(names, request.Tools.Google...)
			if request.Tools.GoogleSearch != nil && !slices.Contains(names, "google_search") {
				names = append(names, "google_search")
			}
		}
		request.System += "\nUse one of these tools in this response: " + strings.Join(names, ", ") + ". Use the tool before answering."
	}
	if contract.parallel != nil {
		request.System += "\nCall at most one function in this response."
	}
	if !contract.required && len(config.AllowedFunctionNames) == 0 && len(contract.schemas) == 0 && contract.parallel == nil {
		contract = nil
	}
	return request, contract, nil
}

// toolPattern 使用 ECMA 正则核对 JSON Schema 字符串
type toolPattern struct {
	*regexp2.Regexp
}

// MatchString 返回字符串是否满足参数模式
func (pattern toolPattern) MatchString(value string) bool {
	matched, err := pattern.Regexp.MatchString(value)
	return err == nil && matched
}

// compileToolPattern 编译 JSON Schema 的 ECMA 模式
func compileToolPattern(value string) (jsonschema.Regexp, error) {
	pattern, err := regexp2.Compile(value, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	pattern.MatchTimeout = time.Second
	return toolPattern{pattern}, nil
}

// toolSchemaLoader 将引用解析限定为请求携带的 Schema 资源
type toolSchemaLoader struct{}

// Load 返回未随请求提供的外部 Schema 引用错误
func (toolSchemaLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("Schema 引用 %q 未包含在工具声明中", url)
}

// validationSchema 将 Gemini 类型名称与 nullable 转换为 JSON Schema 校验结构
func validationSchema(value any) any {
	schema, ok := value.(map[string]any)
	if !ok {
		return value
	}
	if name, ok := schema["type"].(string); ok {
		schema["type"] = strings.ToLower(name)
	}
	if names, ok := schema["type"].([]any); ok {
		for index, name := range names {
			if name, ok := name.(string); ok {
				names[index] = strings.ToLower(name)
			}
		}
	}
	for _, key := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"} {
		if entries, ok := schema[key].(map[string]any); ok {
			for name, child := range entries {
				entries[name] = validationSchema(child)
			}
		}
	}
	for _, key := range []string{"items", "not", "additionalProperties", "contains", "if", "then", "else", "unevaluatedProperties", "unevaluatedItems"} {
		if child, ok := schema[key]; ok {
			schema[key] = validationSchema(child)
		}
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf", "prefixItems"} {
		if entries, ok := schema[key].([]any); ok {
			for index, child := range entries {
				entries[index] = validationSchema(child)
			}
		}
	}
	if schema["nullable"] == true {
		delete(schema, "nullable")
		return map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}
	}
	return schema
}

// forward 在完整工具事件发送给客户端前核对显式契约
func (contract *toolContract) forward(ctx context.Context, source <-chan Event) <-chan Event {
	if contract == nil {
		return source
	}
	destination := make(chan Event, 8)
	go func() {
		defer close(destination)
		calls := 0
		serverSideUsed := false
		failed := false
		for event := range source {
			if failed {
				continue
			}
			var err error
			if event.Kind == EventGrounding || event.Kind == EventExecutableCode || event.Kind == EventCodeExecutionResult {
				serverSideUsed = true
			}
			if event.Kind == EventToolCall && event.ToolCall != nil {
				calls++
				call := event.ToolCall
				switch {
				case !contract.names[call.Name]:
					err = fmt.Errorf("上游返回了未选择的函数 %q", call.Name)
				case contract.parallel != nil && !*contract.parallel && calls > 1:
					err = fmt.Errorf("上游违反单次工具调用约束")
				default:
					if schema := contract.schemas[call.Name]; schema != nil {
						value, decodeErr := jsonschema.UnmarshalJSON(bytes.NewReader(call.Arguments))
						if decodeErr != nil {
							err = decodeErr
						} else {
							err = schema.Validate(value)
						}
						if err != nil {
							err = fmt.Errorf("上游 function %q 参数违反 strict Schema: %w", call.Name, err)
						}
					}
				}
			}
			if event.Kind == EventFinish && contract.required && calls == 0 && !(contract.serverSide && serverSideUsed) {
				err = fmt.Errorf("上游未返回 required tool call")
			}
			if err != nil {
				event = Event{Kind: EventError, Err: err}
				failed = true
			}
			select {
			case destination <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return destination
}
