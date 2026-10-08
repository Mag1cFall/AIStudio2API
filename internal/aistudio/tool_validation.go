package aistudio

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// supportedTools 丢弃模型不支持的函数声明与内置工具，声明的工具全部丢弃时一并清除工具选择，未知内置工具与不能同时使用的工具返回错误
func supportedTools(tools Tools, model Model) (Tools, error) {
	if tools.ToolConfig.Mode == "none" {
		return tools, nil
	}
	declared := len(tools.Functions) > 0 || len(tools.Google) > 0 || tools.GoogleSearch != nil
	if !model.Capabilities["function_declarations"] && len(tools.Functions) > 0 {
		tools.Functions = nil
		tools.ToolConfig.AllowedFunctionNames = nil
	}
	if search := tools.GoogleSearch; search != nil {
		filtered := *search
		filtered.WebSearch = (search.WebSearch || !search.ImageSearch) && model.Capabilities["google_search"]
		filtered.ImageSearch = search.ImageSearch && model.Capabilities["image_search"]
		tools.GoogleSearch = nil
		if filtered.WebSearch || filtered.ImageSearch {
			tools.GoogleSearch = &filtered
		}
	}
	google := make([]string, 0, len(tools.Google))
	hasMaps := false
	hasCode := false
	hasURLContext := false
	for _, tool := range tools.Google {
		capability := ""
		switch tool {
		case "code_execution", "google_search", "image_search", "google_maps":
			capability = tool
		case "url_context":
			capability = "browse"
		default:
			return Tools{}, fmt.Errorf("未知 Google tool %q", tool)
		}
		if !model.Capabilities[capability] {
			continue
		}
		google = append(google, tool)
		hasMaps = hasMaps || tool == "google_maps"
		hasCode = hasCode || tool == "code_execution"
		hasURLContext = hasURLContext || tool == "url_context"
	}
	tools.Google = google
	if hasMaps && (hasCode || hasURLContext) {
		return Tools{}, fmt.Errorf("google_maps 不能与 code_execution 或 url_context 同时使用")
	}
	if declared && len(tools.Functions) == 0 && len(tools.Google) == 0 && tools.GoogleSearch == nil {
		tools.ToolConfig = ToolConfig{}
	}
	return tools, nil
}

// transcribeFunctionParts 把函数调用与函数结果 part 改写为文本转录，缺少名称的函数结果按待返回调用补齐名称
func transcribeFunctionParts(contents []Content) ([]Content, error) {
	transcribed := cloneContentsForFileCopies(contents)
	var pendingCalls []FunctionCall
	for contentIndex, content := range transcribed {
		linked, err := linkFunctionResults(content, &pendingCalls)
		if err != nil {
			return nil, fmt.Errorf("contents[%d]: %w", contentIndex, err)
		}
		for partIndex, part := range linked.Parts {
			if part.FunctionCall == nil && part.FunctionResult == nil {
				continue
			}
			transcript, err := toolPartTranscript(part)
			if err != nil {
				return nil, fmt.Errorf("contents[%d].parts[%d]: %w", contentIndex, partIndex, err)
			}
			content.Parts[partIndex] = Part{Text: transcript}
		}
	}
	return transcribed, nil
}

// toolPartTranscript 返回函数调用、函数结果与代码执行 part 的 Gemini API JSON 文本
func toolPartTranscript(part Part) (string, error) {
	var value map[string]any
	switch {
	case part.FunctionCall != nil:
		value = map[string]any{"functionCall": map[string]any{"name": part.FunctionCall.Name, "args": transcriptJSON(part.FunctionCall.Arguments)}}
	case part.FunctionResult != nil:
		value = map[string]any{"functionResponse": map[string]any{"name": part.FunctionResult.Name, "response": transcriptJSON(part.FunctionResult.Content)}}
	case part.ExecutableCode != nil:
		value = map[string]any{"executableCode": part.ExecutableCode}
	case part.CodeExecutionResult != nil:
		value = map[string]any{"codeExecutionResult": part.CodeExecutionResult}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("工具 part 转录: %w", err)
	}
	return string(encoded), nil
}

// transcriptJSON 返回转录中的 JSON 值，空值记为空对象
func transcriptJSON(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}
