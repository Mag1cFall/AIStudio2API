package aistudio

import (
	"encoding/json"
)

func localCompleteUsage(request GenerateRequest, output Output) *Usage {
	inputTokens := localContentsTokens(request.Contents)
	if system := SystemText(request.System); system != "" {
		inputTokens += localTextTokens(system) + 1
	}
	toolTokens := localToolTokens(request.Tools)
	reasoningTokens := localPartsTokens(output.Reasoning())
	outputTokens := localPartsTokens(output.Visible())
	return &Usage{
		InputTokens: inputTokens, ToolTokens: toolTokens, ReasoningTokens: reasoningTokens,
		OutputTokens: outputTokens, TotalTokens: inputTokens + toolTokens + reasoningTokens + outputTokens,
	}
}

// countedCompleteUsage 使用权威输入总数补全本地停止用量
func countedCompleteUsage(request GenerateRequest, output Output, count TokenCount) *Usage {
	toolTokens := localToolTokens(request.Tools)
	if toolTokens > count.InputTokens {
		toolTokens = count.InputTokens
	}
	inputTokens := count.InputTokens - toolTokens
	reasoningTokens := localPartsTokens(output.Reasoning())
	outputTokens := localPartsTokens(output.Visible())
	return &Usage{
		InputTokens: inputTokens, ToolTokens: toolTokens, ReasoningTokens: reasoningTokens,
		OutputTokens: outputTokens, TotalTokens: count.InputTokens + reasoningTokens + outputTokens,
	}
}

// StartInputTokenEstimate 取下请求中计入本地输入 Token 估算的文本、工具与引用元数据，在后台分词并把估算结果写入返回的通道
func StartInputTokenEstimate(request GenerateRequest) <-chan int64 {
	inputTokens := int64(len(request.Contents))
	var texts []string
	for _, content := range request.Contents {
		for _, part := range content.Parts {
			texts = append(texts, partTexts(part)...)
		}
	}
	if system := SystemText(request.System); system != "" {
		inputTokens++
		texts = append(texts, system)
	}
	if tools := toolText(request.Tools); tools != "" {
		texts = append(texts, tools)
	}
	result := make(chan int64, 1)
	go func() {
		for _, text := range texts {
			inputTokens += localTextTokens(text)
		}
		result <- inputTokens
	}()
	return result
}

func localContentsTokens(contents []Content) int64 {
	var total int64
	for _, content := range contents {
		total++
		total += localPartsTokens(content.Parts)
	}
	return total
}

func localPartsTokens(parts []Part) int64 {
	var total int64
	for _, part := range parts {
		total += localPartTokens(part)
	}
	return total
}

func localPartTokens(part Part) int64 {
	var total int64
	for _, text := range partTexts(part) {
		total += localTextTokens(text)
	}
	return total
}

// partTexts 返回 part 中计入本地 Token 估算的文本
func partTexts(part Part) []string {
	switch {
	case part.Text != "":
		return []string{part.Text}
	case part.ExternalMedia != nil:
		return []string{part.ExternalMedia.URL}
	case part.File != nil:
		return []string{part.File.ID, part.File.Name}
	case part.FunctionCall != nil:
		return []string{part.FunctionCall.Name, string(part.FunctionCall.Arguments)}
	case part.FunctionResult != nil:
		return []string{part.FunctionResult.Name, string(part.FunctionResult.Content)}
	case part.ExecutableCode != nil:
		return []string{part.ExecutableCode.Language, part.ExecutableCode.Code}
	case part.CodeExecutionResult != nil:
		return []string{part.CodeExecutionResult.Outcome, part.CodeExecutionResult.Output}
	default:
		return nil
	}
}

func localToolTokens(tools Tools) int64 {
	if text := toolText(tools); text != "" {
		return localTextTokens(text)
	}
	return 0
}

// toolText 返回计入本地 Token 估算的工具 JSON，没有生效的工具时为空
func toolText(tools Tools) string {
	if tools.ToolConfig.Mode == "none" ||
		(len(tools.Functions) == 0 && len(tools.Google) == 0 && tools.GoogleSearch == nil && tools.ComputerUse == nil && len(tools.MCPServers) == 0 && len(tools.FileSearch) == 0) {
		return ""
	}
	encoded, _ := json.Marshal(tools)
	return string(encoded)
}
