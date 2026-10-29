package api

import (
	"cmp"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// interactionToolSteps 是 Gemini API 服务端工具类型对应的 Interactions 调用与结果步骤类型
var interactionToolSteps = map[string][2]string{
	"GOOGLE_SEARCH_WEB":   {"google_search_call", "google_search_result"},
	"GOOGLE_SEARCH_IMAGE": {"google_search_call", "google_search_result"},
	"URL_CONTEXT":         {"url_context_call", "url_context_result"},
	"GOOGLE_MAPS":         {"google_maps_call", "google_maps_result"},
	"FILE_SEARCH":         {"file_search_call", "file_search_result"},
	"MEDIA_PROCESSING":    {"processing_call", "processing_result"},
}

// mcpToolType 是 MCP 服务器工具调用的 toolType，对应结果不带 toolType
const mcpToolType = "CUSTOM"

// interactionSearchTypes 是 Google Search 工具类型对应的 google_search_call.search_type
var interactionSearchTypes = map[string]string{"GOOGLE_SEARCH_WEB": "web_search", "GOOGLE_SEARCH_IMAGE": "image_search"}

// interactionToolStepKind 报告步骤类型是否为服务端工具或代码执行步骤
func interactionToolStepKind(kind string) bool {
	if strings.HasPrefix(kind, "code_execution_") || strings.HasPrefix(kind, "mcp_server_tool_") {
		return true
	}
	for _, names := range interactionToolSteps {
		if kind == names[0] || kind == names[1] {
			return true
		}
	}
	return false
}

// codeExecutionIDs 为没有 ID 的代码执行调用生成 ID，并关联到其后的执行结果
type codeExecutionIDs struct {
	last string
}

// assign 返回补全代码执行 ID 后的 Part
func (ids *codeExecutionIDs) assign(part aistudio.Part) aistudio.Part {
	switch {
	case part.ExecutableCode != nil:
		code := *part.ExecutableCode
		if code.ID == "" {
			code.ID = newID("call")
		}
		ids.last, part.ExecutableCode = code.ID, &code
	case part.CodeExecutionResult != nil && part.CodeExecutionResult.ID == "":
		result := *part.CodeExecutionResult
		result.ID = ids.last
		part.CodeExecutionResult = &result
	}
	return part
}

// interactionToolStep 把服务端工具步骤与代码执行 Part 投影为 Interactions 步骤
func interactionToolStep(part aistudio.Part) (map[string]any, error) {
	switch {
	case part.ExecutableCode != nil:
		code := part.ExecutableCode
		return map[string]any{"type": "code_execution_call", "id": code.ID, "arguments": map[string]any{"code": code.Code, "language": strings.ToLower(code.Language)}}, nil
	case part.CodeExecutionResult != nil:
		result := part.CodeExecutionResult
		step := map[string]any{"type": "code_execution_result", "call_id": result.ID, "result": result.Output}
		if result.Outcome != "OUTCOME_OK" {
			step["is_error"] = true
		}
		return step, nil
	case part.ToolCall != nil && part.ToolCall.Type == mcpToolType:
		call := part.ToolCall
		server, name := mcpToolName(call.Name)
		step := map[string]any{"type": "mcp_server_tool_call", "id": call.ID, "name": name, "server_name": server, "arguments": map[string]any{}}
		if len(call.Data) > 0 {
			step["arguments"] = call.Data
		}
		return step, nil
	case part.ToolResponse != nil && (part.ToolResponse.Type == "" || part.ToolResponse.Type == mcpToolType):
		response := part.ToolResponse
		step := map[string]any{"type": "mcp_server_tool_result", "call_id": response.ID, "result": mcpToolResult(response.Data)}
		if response.Name != "" {
			step["server_name"], step["name"] = mcpToolName(response.Name)
		}
		return step, nil
	case part.ToolCall != nil:
		call := part.ToolCall
		names, ok := interactionToolSteps[call.Type]
		if !ok {
			return nil, fmt.Errorf("Interactions cannot represent server tool type %q", call.Type)
		}
		step := map[string]any{"type": names[0], "id": call.ID}
		if names[0] == "processing_call" || names[0] == "file_search_call" {
			return step, nil
		}
		step["arguments"] = map[string]any{}
		if len(call.Data) > 0 {
			step["arguments"] = call.Data
		}
		if searchType := interactionSearchTypes[call.Type]; searchType != "" {
			step["search_type"] = searchType
		}
		return step, nil
	case part.ToolResponse != nil:
		response := part.ToolResponse
		names, ok := interactionToolSteps[response.Type]
		if !ok {
			return nil, fmt.Errorf("Interactions cannot represent server tool type %q", response.Type)
		}
		step := map[string]any{"type": names[1], "call_id": response.ID}
		if names[1] == "processing_result" || names[1] == "file_search_result" {
			return step, nil
		}
		result, err := interactionToolResult(response)
		if err != nil {
			return nil, err
		}
		step["result"] = result
		return step, nil
	}
	return nil, nil
}

// mcpToolName 把 "服务器:工具" 形式的 toolName 拆为服务器名与工具名
func mcpToolName(value string) (string, string) {
	server, name, ok := strings.Cut(value, ":")
	if !ok {
		return "", value
	}
	return server, name
}

// mcpToolResult 把 toolResponse.response.call_tool_result_json 中的 MCP 内容转为 Interactions 内容数组，无法解析时返回原始文本
func mcpToolResult(raw json.RawMessage) any {
	var response struct {
		Result string `json:"call_tool_result_json"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Result == "" {
		return string(raw)
	}
	var result struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Data     string `json:"data"`
			MIMEType string `json:"mimeType"`
		} `json:"content"`
	}
	if json.Unmarshal([]byte(response.Result), &result) != nil || len(result.Content) == 0 {
		return response.Result
	}
	contents := make([]map[string]any, 0, len(result.Content))
	for _, item := range result.Content {
		switch item.Type {
		case "image", "audio":
			contents = append(contents, map[string]any{"type": item.Type, "data": item.Data, "mime_type": item.MIMEType})
		default:
			contents = append(contents, map[string]any{"type": "text", "text": item.Text})
		}
	}
	return contents
}

// mcpCallToolResult 把 Interactions mcp_server_tool_result 的结果还原为 MCP CallToolResult：字符串与内容数组为 content，其余 JSON 值为 structuredContent
func mcpCallToolResult(raw json.RawMessage) (json.RawMessage, error) {
	var text string
	var content []map[string]any
	if json.Unmarshal(raw, &text) == nil {
		content = []map[string]any{{"type": "text", "text": text}}
	} else if json.Unmarshal(raw, &content) != nil {
		var structured any
		if err := json.Unmarshal(raw, &structured); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"content": []any{}, "structuredContent": structured})
	}
	for _, item := range content {
		if mimeType, ok := item["mime_type"]; ok {
			item["mimeType"] = mimeType
			delete(item, "mime_type")
		}
	}
	return json.Marshal(map[string]any{"content": content})
}

// mcpFunctionPart 把没有签名的 MCP 调用与其结果转为函数调用与函数结果，functions 记录调用 ID 对应的函数名
func mcpFunctionPart(item interactionInput, functions map[string]string) (aistudio.Role, []aistudio.Part, error) {
	if item.Type == "mcp_server_tool_result" {
		if !rawJSONConfigured(item.Result) {
			return "", nil, fmt.Errorf("mcp_server_tool_result requires call_id and result")
		}
		result, err := mcpCallToolResult(item.Result)
		if err != nil {
			return "", nil, fmt.Errorf("mcp_server_tool_result result: %w", err)
		}
		return aistudio.RoleTool, []aistudio.Part{{FunctionResult: &aistudio.FunctionResult{ID: item.CallID, Name: functions[item.CallID], Content: result}}}, nil
	}
	if item.ID == "" || item.Name == "" {
		return "", nil, fmt.Errorf("mcp_server_tool_call requires id and name")
	}
	name := item.Name
	if item.ServerName != "" {
		name = item.ServerName + ":" + item.Name
	}
	functions[item.ID] = name
	arguments := item.Arguments
	if !rawJSONConfigured(arguments) {
		arguments = json.RawMessage(`{}`)
	}
	return aistudio.RoleAssistant, []aistudio.Part{{FunctionCall: &aistudio.FunctionCall{ID: item.ID, Name: name, Arguments: arguments}}}, nil
}

// interactionToolResult 把 Gemini API toolResponse.response 转为 Interactions 结果数组
func interactionToolResult(response *aistudio.ServerToolStep) ([]map[string]any, error) {
	var data struct {
		SearchSuggestions string `json:"search_suggestions"`
		URLMetadata       []struct {
			RetrievedURL string `json:"retrieved_url"`
			Status       string `json:"url_retrieval_status"`
		} `json:"url_metadata"`
		Places []struct {
			PlaceID string `json:"place_id"`
			Title   string `json:"title"`
			URI     string `json:"uri"`
		} `json:"places"`
		WidgetContextToken string `json:"widget_context_token"`
	}
	if len(response.Data) > 0 {
		if err := json.Unmarshal(response.Data, &data); err != nil {
			return nil, fmt.Errorf("server tool %s response: %w", response.Type, err)
		}
	}
	switch response.Type {
	case "URL_CONTEXT":
		results := make([]map[string]any, 0, len(data.URLMetadata))
		for _, item := range data.URLMetadata {
			results = append(results, map[string]any{"url": item.RetrievedURL, "status": strings.ToLower(strings.TrimPrefix(item.Status, "URL_RETRIEVAL_STATUS_"))})
		}
		return results, nil
	case "GOOGLE_MAPS":
		places := make([]map[string]any, 0, len(data.Places))
		for _, place := range data.Places {
			value := map[string]any{"place_id": place.PlaceID, "url": place.URI}
			if place.Title != "" {
				value["name"] = place.Title
			}
			places = append(places, value)
		}
		result := map[string]any{"places": places}
		if data.WidgetContextToken != "" {
			result["widget_context_token"] = data.WidgetContextToken
		}
		return []map[string]any{result}, nil
	}
	result := map[string]any{}
	if data.SearchSuggestions != "" {
		result["search_suggestions"] = data.SearchSuggestions
	}
	return []map[string]any{result}, nil
}

// interactionToolPart 把输入中的服务端工具与代码执行步骤还原为 Gemini API Part
func interactionToolPart(item interactionInput) (aistudio.Part, error) {
	part := aistudio.Part{ThoughtSignature: item.Signature}
	switch item.Type {
	case "code_execution_call":
		var arguments struct {
			Code     string `json:"code"`
			Language string `json:"language"`
		}
		if rawJSONConfigured(item.Arguments) {
			if err := json.Unmarshal(item.Arguments, &arguments); err != nil {
				return part, fmt.Errorf("code_execution_call arguments: %w", err)
			}
		}
		part.ExecutableCode = &aistudio.ExecutableCode{ID: item.ID, Language: strings.ToUpper(cmp.Or(arguments.Language, "python")), Code: arguments.Code}
		return part, nil
	case "code_execution_result":
		var output string
		if rawJSONConfigured(item.Result) {
			if err := json.Unmarshal(item.Result, &output); err != nil {
				return part, fmt.Errorf("code_execution_result result: %w", err)
			}
		}
		part.CodeExecutionResult = &aistudio.CodeExecutionResult{ID: item.CallID, Outcome: "OUTCOME_OK", Output: output}
		if item.IsError {
			part.CodeExecutionResult.Outcome = "OUTCOME_FAILED"
		}
		return part, nil
	case "mcp_server_tool_call":
		if item.ID == "" || item.Name == "" {
			return part, fmt.Errorf("mcp_server_tool_call requires id and name")
		}
		part.ToolCall = &aistudio.ServerToolStep{ID: item.ID, Type: mcpToolType, Name: item.ServerName + ":" + item.Name}
		if rawJSONConfigured(item.Arguments) {
			part.ToolCall.Data = item.Arguments
		}
		return part, nil
	case "mcp_server_tool_result":
		if item.CallID == "" || !rawJSONConfigured(item.Result) {
			return part, fmt.Errorf("mcp_server_tool_result requires call_id and result")
		}
		result, err := mcpCallToolResult(item.Result)
		if err != nil {
			return part, fmt.Errorf("mcp_server_tool_result result: %w", err)
		}
		data, _ := json.Marshal(map[string]string{"call_tool_result_json": string(result)})
		part.ToolResponse = &aistudio.ServerToolStep{ID: item.CallID, Data: data}
		return part, nil
	}
	toolType := ""
	call := false
	for name, steps := range interactionToolSteps {
		if item.Type == steps[0] || item.Type == steps[1] {
			toolType, call = name, item.Type == steps[0]
		}
	}
	if toolType == "GOOGLE_SEARCH_WEB" || toolType == "GOOGLE_SEARCH_IMAGE" {
		toolType = "GOOGLE_SEARCH_WEB"
		if item.SearchType == "image_search" {
			toolType = "GOOGLE_SEARCH_IMAGE"
		}
	}
	if call {
		if item.ID == "" {
			return part, fmt.Errorf("%s requires id", item.Type)
		}
		part.ToolCall = &aistudio.ServerToolStep{ID: item.ID, Type: toolType}
		if rawJSONConfigured(item.Arguments) {
			part.ToolCall.Data = item.Arguments
		}
		return part, nil
	}
	if item.CallID == "" {
		return part, fmt.Errorf("%s requires call_id", item.Type)
	}
	part.ToolResponse = &aistudio.ServerToolStep{ID: item.CallID, Type: toolType}
	if !rawJSONConfigured(item.Result) {
		return part, nil
	}
	data, err := interactionToolResponse(toolType, item.Result)
	if err != nil {
		return part, fmt.Errorf("%s result: %w", item.Type, err)
	}
	part.ToolResponse.Data = data
	return part, nil
}

// interactionToolResponse 把 Interactions 结果数组还原为 Gemini API toolResponse.response
func interactionToolResponse(toolType string, raw json.RawMessage) (json.RawMessage, error) {
	var results []struct {
		SearchSuggestions string `json:"search_suggestions"`
		URL               string `json:"url"`
		Status            string `json:"status"`
		Places            []struct {
			Name    string `json:"name"`
			PlaceID string `json:"place_id"`
			URL     string `json:"url"`
		} `json:"places"`
		WidgetContextToken string `json:"widget_context_token"`
	}
	if err := json.Unmarshal(raw, &results); err != nil {
		return nil, err
	}
	response := map[string]any{}
	switch toolType {
	case "URL_CONTEXT":
		metadata := make([]map[string]any, 0, len(results))
		for _, result := range results {
			metadata = append(metadata, map[string]any{"retrieved_url": result.URL, "url_retrieval_status": "URL_RETRIEVAL_STATUS_" + strings.ToUpper(result.Status)})
		}
		response["url_metadata"] = metadata
	case "GOOGLE_MAPS":
		places := make([]map[string]any, 0)
		for _, result := range results {
			for _, place := range result.Places {
				places = append(places, map[string]any{"place_id": place.PlaceID, "title": place.Name, "uri": place.URL})
			}
			if result.WidgetContextToken != "" {
				response["widget_context_token"] = result.WidgetContextToken
			}
		}
		response["places"] = places
	default:
		for _, result := range results {
			if result.SearchSuggestions != "" {
				response["search_suggestions"] = result.SearchSuggestions
			}
		}
	}
	return json.Marshal(response)
}
