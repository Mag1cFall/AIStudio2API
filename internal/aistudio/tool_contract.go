package aistudio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dlclark/regexp2/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// toolContract 保存客户端显式要求的调用模式与参数约束
type toolContract struct {
	required      bool
	parallel      *bool
	names         map[string]bool
	schemas       map[string]*jsonschema.Schema
	serverSide    bool
	replyTool     bool
	stopSequences []string
}

// prepareToolRequest 将工具选择与约束转换为共同生成请求，compile 为 true 时编译严格函数的参数 Schema 供校验函数调用参数
func prepareToolRequest(request GenerateRequest, compile bool) (GenerateRequest, *toolContract, error) {
	return prepareToolRequestWithReplyTool(request, compile, false)
}

// prepareToolRequestWithReplyTool 将工具选择与约束转换为共同生成请求，replyTool 为 true 时单次工具调用提示不约束内部 final 工具
func prepareToolRequestWithReplyTool(request GenerateRequest, compile bool, replyTool bool) (GenerateRequest, *toolContract, error) {
	config := request.Tools.ToolConfig
	if config.Mode == "none" {
		return request, nil, nil
	}
	if search := request.Tools.GoogleSearch; search != nil {
		if search.ContextSize != "" {
			request.System = appendSystem(request.System, "\nUse "+search.ContextSize+" search context depth.")
		}
		if len(search.UserLocation) > 0 {
			request.System = appendSystem(request.System, "\nApproximate search user location: "+string(search.UserLocation))
		}
		if len(search.AllowedDomains) > 0 {
			request.System = appendSystem(request.System, "\nRestrict web searches to these domains using site: queries: "+strings.Join(search.AllowedDomains, ", "))
		}
		if len(search.BlockedDomains) > 0 {
			request.System = appendSystem(request.System, "\nExclude these domains from web searches using -site: queries: "+strings.Join(search.BlockedDomains, ", "))
		}
	}
	if fetch := request.Tools.URLContext; fetch != nil {
		if len(fetch.AllowedDomains) > 0 {
			request.System = appendSystem(request.System, "\nRead URLs only on these domains and their subdomains: "+strings.Join(fetch.AllowedDomains, ", "))
		}
		if len(fetch.BlockedDomains) > 0 {
			request.System = appendSystem(request.System, "\nNever read URLs on these domains or their subdomains: "+strings.Join(fetch.BlockedDomains, ", "))
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
		if !compile {
			continue
		}
		document = validationSchema(document)
		compiler := jsonschema.NewCompiler()
		compiler.UseLoader(toolSchemaLoader{})
		compiler.UseRegexpEngine(compileToolPattern)
		const resource = "https://tools.invalid/parameters.json"
		if err := compiler.AddResource(resource, document); err != nil {
			return request, nil, err
		}
		if schema, err := compiler.Compile(resource); err == nil {
			contract.schemas[declaration.Name] = schema
		}
	}
	for _, name := range config.AllowedFunctionNames {
		if !contract.names[name] {
			return request, nil, fmt.Errorf("tool choice 引用了未声明函数 %q", name)
		}
	}
	contract.serverSide = len(config.AllowedFunctionNames) == 0 && (len(request.Tools.Google) > 0 || request.Tools.GoogleSearch != nil || len(request.Tools.MCPServers) > 0 && config.ServerSideInvocations)
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
			if config.ServerSideInvocations {
				for _, server := range request.Tools.MCPServers {
					names = append(names, "the "+server.Name+" MCP server")
				}
			}
		}
		request.System = appendSystem(request.System, "\nUse one of these tools in this response: "+strings.Join(names, ", ")+". Use the tool before answering.")
	}
	if contract.parallel != nil {
		if replyTool {
			request.System = appendSystem(request.System, "\nCall at most one non-final tool in this response (this limit does not apply to `final`).")
		} else {
			request.System = appendSystem(request.System, "\nCall at most one function in this response.")
		}
	}
	if !replyTool && !contract.required && len(config.AllowedFunctionNames) == 0 && len(contract.schemas) == 0 && contract.parallel == nil {
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

// forward 在完整工具事件发送给客户端前核对显式约束并转换回复工具输出
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
		usedReplyTool := false
		hasFinalText := false
		lastFinalEndedNewline := false
		sawPlainText := false
		bypassReported := false
		matchedStop := ""
		var pendingCalls []Event
		var lastTextEvent Event
		var matcher *stopSequenceMatcher
		if contract.replyTool && len(contract.stopSequences) > 0 {
			matcher = newStopSequenceMatcher(contract.stopSequences)
		}
		send := func(event Event) bool {
			select {
			case destination <- event:
				return true
			case <-ctx.Done():
				return false
			}
		}
		flushTextTail := func() bool {
			if matcher == nil || matchedStop != "" {
				return true
			}
			pending := matcher.flush()
			if pending == "" {
				return true
			}
			textEvent := Event{
				Kind:          EventText,
				Part:          Part{Text: pending},
				ProviderModel: lastTextEvent.ProviderModel,
				TurnToken:     lastTextEvent.TurnToken,
				DebugInfo:     lastTextEvent.DebugInfo,
				ResponseID:    lastTextEvent.ResponseID,
				FrameUsage:    lastTextEvent.FrameUsage,
				FinishFrame:   lastTextEvent.FinishFrame,
			}
			return send(textEvent)
		}
		flushPendingCalls := func() bool {
			if !flushTextTail() {
				return false
			}
			if matchedStop != "" {
				pendingCalls = nil
				return true
			}
			for _, pending := range pendingCalls {
				if !send(pending) {
					return false
				}
			}
			pendingCalls = nil
			return true
		}
		reportBypass := func() {
			if contract.replyTool && !usedReplyTool && sawPlainText && !bypassReported {
				bypassReported = true
				reportReplyToolBypassed(ctx)
			}
		}
		for event := range source {
			if failed {
				continue
			}
			if matchedStop != "" {
				pendingCalls = nil
				switch event.Kind {
				case EventUsage:
					if !send(event) {
						return
					}
				case EventFinish:
					reportBypass()
					event.FinishReason = "stop_sequence"
					event.StopSequence = matchedStop
					if !send(event) {
						return
					}
				}
				continue
			}
			if contract.replyTool && event.Kind == EventToolCall && event.FunctionCall != nil && isReplyToolName(event.FunctionCall.Name) {
				text, ok := replyToolText(event.FunctionCall.Arguments)
				if !ok {
					failed = true
					reportBypass()
					if !send(Event{Kind: EventError, Err: fmt.Errorf("上游 function %q 参数违反 strict Schema: %w", replyToolName, errors.New("reply tool final arguments must include string content"))}) {
						return
					}
					continue
				}
				usedReplyTool = true
				reportReplyToolCall(ctx)
				lastTextEvent = event
				rawText := text
				if rawText != "" {
					if hasFinalText && !lastFinalEndedNewline && !strings.HasPrefix(rawText, "\n") && !strings.HasPrefix(rawText, "\r") {
						text = "\n\n" + rawText
					}
					hasFinalText = true
					lastFinalEndedNewline = strings.HasSuffix(rawText, "\n") || strings.HasSuffix(rawText, "\r")
				}
				if matcher != nil {
					var sequence string
					text, sequence = matcher.write(text)
					if sequence != "" {
						matchedStop = sequence
					}
				}
				if text != "" {
					textEvent := Event{
						Kind:          EventText,
						Part:          Part{Text: text},
						ProviderModel: event.ProviderModel,
						TurnToken:     event.TurnToken,
						DebugInfo:     event.DebugInfo,
						ResponseID:    event.ResponseID,
						FrameUsage:    event.FrameUsage,
						FinishFrame:   event.FinishFrame,
					}
					if !send(textEvent) {
						return
					}
				}
				continue
			}
			if contract.replyTool && event.Kind == EventText && event.Text != "" {
				sawPlainText = true
				lastTextEvent = event
				if matcher != nil {
					text, sequence := matcher.write(event.Text)
					if sequence != "" {
						matchedStop = sequence
					}
					if text == "" {
						continue
					}
					event.Text = text
				}
			}
			var err error
			if event.Kind == EventGrounding || event.Kind == EventExecutableCode || event.Kind == EventCodeExecutionResult || event.Kind == EventServerToolCall {
				serverSideUsed = true
			}
			if event.Kind == EventToolCall && event.FunctionCall != nil {
				calls++
				call := event.FunctionCall
				switch {
				case contract.names != nil && !contract.names[call.Name]:
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
			if contract.replyTool && !failed && event.Kind == EventToolCall && event.FunctionCall != nil {
				pendingCalls = append(pendingCalls, event)
				continue
			}
			if contract.replyTool && (event.Kind == EventUsage || event.Kind == EventFinish) {
				if !flushPendingCalls() {
					return
				}
			} else if contract.replyTool && !failed && event.Kind != EventText && event.Kind != EventError {
				if !flushTextTail() {
					return
				}
			}
			if contract.replyTool && (event.Kind == EventFinish || event.Kind == EventError) {
				reportBypass()
			}
			if !send(event) {
				return
			}
		}
		if !failed {
			if !flushPendingCalls() {
				return
			}
			reportBypass()
		}
	}()
	return destination
}
