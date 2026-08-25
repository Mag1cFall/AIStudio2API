package aistudio

import (
	"encoding/json"
)

type generatedOutputParts struct {
	visible   []Part
	reasoning []Part
}

func (output *generatedOutputParts) observe(event Event) {
	switch event.Kind {
	case EventText:
		output.appendText(&output.visible, event.Text, false, event.ThoughtSignature)
	case EventReasoning:
		output.appendText(&output.reasoning, event.Text, true, event.ThoughtSignature)
	case EventToolCall:
		if event.ToolCall != nil {
			call := *event.ToolCall
			call.Arguments = append(json.RawMessage(nil), call.Arguments...)
			output.visible = append(output.visible, Part{FunctionCall: &call, ThoughtSignature: event.ThoughtSignature})
		}
	case EventExecutableCode:
		if event.ExecutableCode != nil {
			code := *event.ExecutableCode
			output.visible = append(output.visible, Part{ExecutableCode: &code, ThoughtSignature: event.ThoughtSignature})
		}
	case EventCodeExecutionResult:
		if event.CodeExecutionResult != nil {
			result := *event.CodeExecutionResult
			output.visible = append(output.visible, Part{CodeExecutionResult: &result, ThoughtSignature: event.ThoughtSignature})
		}
	case EventMedia:
		output.appendMedia(event)
	}
}

func (output *generatedOutputParts) appendText(parts *[]Part, text string, thought bool, signature string) {
	if text == "" {
		return
	}
	if len(*parts) > 0 {
		last := &(*parts)[len(*parts)-1]
		if last.Text != "" && last.Thought == thought && last.ThoughtSignature == signature {
			last.Text += text
			return
		}
	}
	*parts = append(*parts, Part{Text: text, Thought: thought, ThoughtSignature: signature})
}

func (output *generatedOutputParts) appendMedia(event Event) {
	if event.Media == nil {
		return
	}
	if len(event.Media.Data) == 0 {
		if event.Media.URL != "" {
			output.visible = append(output.visible, Part{
				File:             &FileRef{ID: event.Media.URL, Name: event.Media.Name, MIME: event.Media.MIME},
				ThoughtSignature: event.ThoughtSignature,
			})
		}
		return
	}
	if len(output.visible) > 0 {
		last := &output.visible[len(output.visible)-1]
		if last.InlineData != nil && last.InlineData.MIME == event.Media.MIME && last.ThoughtSignature == event.ThoughtSignature {
			last.InlineData.Data = append(last.InlineData.Data, event.Media.Data...)
			return
		}
	}
	data := append([]byte(nil), event.Media.Data...)
	output.visible = append(output.visible, Part{
		InlineData: &Blob{MIME: event.Media.MIME, Data: data}, ThoughtSignature: event.ThoughtSignature,
	})
}

func localCompleteUsage(request GenerateRequest, output generatedOutputParts) *Usage {
	inputTokens := localContentsTokens(request.Contents)
	if request.System != "" {
		inputTokens += localTextTokens(request.System) + 1
	}
	toolTokens := localToolTokens(request.Tools)
	reasoningTokens := localPartsTokens(output.reasoning)
	outputTokens := localPartsTokens(output.visible)
	return &Usage{
		InputTokens: inputTokens, ToolTokens: toolTokens, ReasoningTokens: reasoningTokens,
		OutputTokens: outputTokens, TotalTokens: inputTokens + toolTokens + reasoningTokens + outputTokens,
	}
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
	switch {
	case part.Text != "":
		return localTextTokens(part.Text)
	case part.ExternalMedia != nil:
		return localTextTokens(part.ExternalMedia.URL)
	case part.File != nil:
		return localTextTokens(part.File.ID) + localTextTokens(part.File.Name)
	case part.FunctionCall != nil:
		return localTextTokens(part.FunctionCall.Name) + localTextTokens(string(part.FunctionCall.Arguments))
	case part.FunctionResult != nil:
		return localTextTokens(part.FunctionResult.Name) + localTextTokens(string(part.FunctionResult.Content))
	case part.ExecutableCode != nil:
		return localTextTokens(part.ExecutableCode.Language) + localTextTokens(part.ExecutableCode.Code)
	case part.CodeExecutionResult != nil:
		return localTextTokens(part.CodeExecutionResult.Outcome) + localTextTokens(part.CodeExecutionResult.Output)
	default:
		return 0
	}
}

func localToolTokens(tools Tools) int64 {
	if len(tools.Functions) == 0 && len(tools.Google) == 0 {
		return 0
	}
	encoded, _ := json.Marshal(tools)
	return localTextTokens(string(encoded))
}
