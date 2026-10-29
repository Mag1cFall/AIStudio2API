package aistudio

import (
	"encoding/json"
	"strings"
)

// Output 按上游顺序累积一次生成的内容 Part，同一 Part 的流式正文与 PCM 音频分块合并
type Output struct {
	Parts []Part
}

// Add 并入一个内容事件
func (output *Output) Add(event Event) {
	if !event.HasContent() {
		return
	}
	part := clonePart(event.Part)
	if count := len(output.Parts); count > 0 {
		last := &output.Parts[count-1]
		switch {
		case continuesText(*last, part):
			last.Text += part.Text
			return
		case MergePCM(last, part):
			return
		}
	}
	output.Parts = append(output.Parts, part)
}

// Visible 返回非思考 Part
func (output Output) Visible() []Part {
	parts := make([]Part, 0, len(output.Parts))
	for _, part := range output.Parts {
		if !part.Thought {
			parts = append(parts, part)
		}
	}
	return parts
}

// Reasoning 返回思考 Part
func (output Output) Reasoning() []Part {
	parts := make([]Part, 0)
	for _, part := range output.Parts {
		if part.Thought {
			parts = append(parts, part)
		}
	}
	return parts
}

// History 返回续接历史中的模型轮次：思考 Part 只保留签名
func (output Output) History() Content {
	content := Content{Role: RoleAssistant}
	for _, part := range output.Parts {
		if part.Thought {
			if part.ThoughtSignature != "" {
				content.Parts = append(content.Parts, Part{ThoughtSignature: part.ThoughtSignature})
			}
			continue
		}
		content.Parts = append(content.Parts, part)
	}
	return content
}

// mediaEvent 返回媒体 Part 的事件：思考过程中的草图属于思考内容
func mediaEvent(part Part) Event {
	if part.Thought {
		return Event{Kind: EventReasoning, Part: part}
	}
	return Event{Kind: EventMedia, Part: part}
}

// HasContent 表示事件携带内容或独立签名
func (event Event) HasContent() bool {
	return event.Kind.content() && !event.Part.empty()
}

// SignatureOnly 表示 Part 只携带思考签名
func (part Part) SignatureOnly() bool {
	return part.ThoughtSignature != "" && !part.payload()
}

// content 表示事件携带内容 Part
func (kind EventKind) content() bool {
	switch kind {
	case EventText, EventReasoning, EventToolCall, EventServerToolCall, EventServerToolResponse,
		EventExecutableCode, EventCodeExecutionResult, EventMedia, EventThoughtSignature:
		return true
	}
	return false
}

// empty 表示 Part 既没有内容也没有签名
func (part Part) empty() bool {
	return part.ThoughtSignature == "" && !part.payload()
}

// payload 表示 Part 携带正文或其他内容
func (part Part) payload() bool {
	return part.Text != "" || part.InlineData != nil || part.ExternalMedia != nil || part.File != nil || part.Upload != nil ||
		part.FunctionCall != nil || part.FunctionResult != nil || part.ExecutableCode != nil || part.CodeExecutionResult != nil ||
		part.ToolCall != nil || part.ToolResponse != nil || part.Transcript != nil
}

// textOnly 表示 Part 只携带正文
func (part Part) textOnly() bool {
	text := part.Text
	part.Text, part.Thought, part.ThoughtSignature = "", false, ""
	return text != "" && !part.payload()
}

func continuesText(last Part, next Part) bool {
	return last.textOnly() && next.textOnly() && last.Thought == next.Thought && last.ThoughtSignature == next.ThoughtSignature
}

// MergePCM 将思考标记、签名与格式都相同的 PCM 数据追加到 last，返回是否合并
func MergePCM(last *Part, next Part) bool {
	if last.InlineData == nil || next.InlineData == nil || last.Thought != next.Thought || last.ThoughtSignature != next.ThoughtSignature ||
		last.InlineData.MIME != next.InlineData.MIME || !strings.HasPrefix(strings.ToLower(next.InlineData.MIME), "audio/l16") {
		return false
	}
	last.InlineData.Data = append(last.InlineData.Data, next.InlineData.Data...)
	return true
}

func clonePart(part Part) Part {
	if part.InlineData != nil {
		blob := *part.InlineData
		blob.Data = append([]byte(nil), blob.Data...)
		part.InlineData = &blob
	}
	if part.FunctionCall != nil {
		call := *part.FunctionCall
		call.Arguments = append(json.RawMessage(nil), call.Arguments...)
		part.FunctionCall = &call
	}
	part.ToolCall, part.ToolResponse = cloneServerToolStep(part.ToolCall), cloneServerToolStep(part.ToolResponse)
	return part
}

func cloneServerToolStep(step *ServerToolStep) *ServerToolStep {
	if step == nil {
		return nil
	}
	cloned := *step
	cloned.Data = append(json.RawMessage(nil), step.Data...)
	return &cloned
}
