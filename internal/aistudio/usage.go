package aistudio

import (
	"context"
	"encoding/json"
)

type generatedOutputParts struct {
	parts []Part
}

func (output *generatedOutputParts) observe(event Event) {
	switch event.Kind {
	case EventText:
		output.appendText(event.Text, event.ThoughtSignature)
	case EventToolCall:
		if event.ToolCall != nil {
			call := *event.ToolCall
			call.Arguments = append(json.RawMessage(nil), call.Arguments...)
			output.parts = append(output.parts, Part{FunctionCall: &call, ThoughtSignature: event.ThoughtSignature})
		}
	case EventExecutableCode:
		if event.ExecutableCode != nil {
			code := *event.ExecutableCode
			output.parts = append(output.parts, Part{ExecutableCode: &code, ThoughtSignature: event.ThoughtSignature})
		}
	case EventCodeExecutionResult:
		if event.CodeExecutionResult != nil {
			result := *event.CodeExecutionResult
			output.parts = append(output.parts, Part{CodeExecutionResult: &result, ThoughtSignature: event.ThoughtSignature})
		}
	case EventMedia:
		output.appendMedia(event)
	}
}

func (output *generatedOutputParts) appendText(text string, signature string) {
	if text == "" {
		return
	}
	if len(output.parts) > 0 {
		last := &output.parts[len(output.parts)-1]
		if last.Text != "" && last.ThoughtSignature == signature {
			last.Text += text
			return
		}
	}
	output.parts = append(output.parts, Part{Text: text, ThoughtSignature: signature})
}

func (output *generatedOutputParts) appendMedia(event Event) {
	if event.Media == nil {
		return
	}
	if len(event.Media.Data) == 0 {
		if event.Media.URL != "" {
			output.parts = append(output.parts, Part{
				File:             &FileRef{ID: event.Media.URL, Name: event.Media.Name, MIME: event.Media.MIME},
				ThoughtSignature: event.ThoughtSignature,
			})
		}
		return
	}
	if len(output.parts) > 0 {
		last := &output.parts[len(output.parts)-1]
		if last.InlineData != nil && last.InlineData.MIME == event.Media.MIME && last.ThoughtSignature == event.ThoughtSignature {
			last.InlineData.Data = append(last.InlineData.Data, event.Media.Data...)
			return
		}
	}
	data := append([]byte(nil), event.Media.Data...)
	output.parts = append(output.parts, Part{
		InlineData: &Blob{MIME: event.Media.MIME, Data: data}, ThoughtSignature: event.ThoughtSignature,
	})
}

func (c *Client) measureGeneratedOutput(ctx context.Context, request GenerateRequest, output generatedOutputParts) (int64, error) {
	if len(output.parts) == 0 {
		return 0, nil
	}
	count, err := c.CountTokensForAccount(ctx, request.AccountID, TokenCountRequest{
		Model:    request.Model,
		Contents: []Content{{Role: RoleAssistant, Parts: output.parts}},
	})
	if err != nil {
		return 0, err
	}
	return count.InputTokens, nil
}
