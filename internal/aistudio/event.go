package aistudio

import (
	"encoding/json"
	"fmt"
)

// FrameDecoder 将 field 1 repeated 帧转换为规范事件
type FrameDecoder struct {
	usage     *Usage
	finished  bool
	lastFrame json.RawMessage
}

// NewFrameDecoder 创建单次 GenerateContent 的有状态解码器
func NewFrameDecoder() *FrameDecoder {
	return &FrameDecoder{}
}

// Decode 解码一条 repeated 流帧
func (d *FrameDecoder) Decode(raw json.RawMessage) ([]Event, error) {
	d.lastFrame = append(json.RawMessage(nil), raw...)
	frame, err := rawArray(raw, "$[0][]", raw)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	if len(frame) == 0 {
		return nil, d.protocolError("$[0][]", "空流帧", raw)
	}
	if isJSONNull(frame[0]) {
		return nil, nil
	}
	candidates, err := rawArray(frame[0], "$[0][][0]", raw)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	if len(candidates) != 1 {
		return nil, d.protocolError("$[0][][0]", "候选数量不是现场确认的 1", raw)
	}
	candidate, err := rawArray(candidates[0], "$[0][][0][0]", raw)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	finishRaw := rawAt(candidate, 1)
	events := make([]Event, 0, 4)
	if len(candidate) > 0 && !isJSONNull(candidate[0]) {
		contentEvents, err := d.decodeContent(candidate[0], raw)
		if err != nil {
			return nil, err
		}
		events = append(events, contentEvents...)
	}
	if citationsRaw := rawAt(candidate, 6); !isJSONNull(citationsRaw) {
		citations, err := decodeCitations(citationsRaw, raw)
		if err != nil {
			return nil, err
		}
		for index := range citations {
			citation := citations[index]
			events = append(events, Event{Kind: EventCitation, Citation: &citation})
		}
	}
	if groundingRaw := rawAt(candidate, 7); !isJSONNull(groundingRaw) {
		grounding, err := decodeGroundingMetadata(groundingRaw, "$[0][][0][0][7]", raw)
		if err != nil {
			return nil, err
		}
		events = append(events, Event{Kind: EventGrounding, Grounding: &grounding})
	}
	if usageRaw := rawAt(frame, 2); !isJSONNull(usageRaw) {
		usage, complete, err := decodeUsage(usageRaw, raw)
		if err != nil {
			return nil, err
		}
		if complete {
			d.usage = &usage
		}
	}
	if !isJSONNull(finishRaw) {
		finishCode, err := rawInt64(finishRaw, "$[0][][0][0][1]", raw)
		if err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
		finishReason := decodeFinishReason(finishCode)
		if !d.finished {
			if d.usage != nil {
				usage := *d.usage
				events = append(events, Event{Kind: EventUsage, Usage: &usage})
			}
			events = append(events, Event{Kind: EventFinish, FinishReason: finishReason})
			d.finished = true
		}
	}
	return events, nil
}

func decodeFinishReason(code int64) string {
	switch code {
	case 0:
		return "unspecified"
	case 1:
		return "stop"
	case 2:
		return "max_tokens"
	case 3:
		return "safety"
	case 4:
		return "recitation"
	case 5:
		return "other"
	case 6:
		return "language"
	case 7:
		return "blocklist"
	case 8:
		return "prohibited_content"
	case 9:
		return "spii"
	case 10:
		return "malformed_function_call"
	case 11:
		return "image_safety"
	case 12:
		return "unexpected_tool_call"
	case 13:
		return "too_many_tool_calls"
	case 14:
		return "image_prohibited_content"
	case 15:
		return "image_other"
	case 16:
		return "no_image"
	case 17:
		return "image_recitation"
	case 18:
		return "missing_thought_signature"
	default:
		return fmt.Sprintf("provider_%d", code)
	}
}

// End 校验流已经出现正常完成帧
func (d *FrameDecoder) End() error {
	if d.finished {
		return nil
	}
	return d.protocolError("$", "流结束前没有完成帧", d.lastFrame)
}

func (d *FrameDecoder) decodeContent(raw json.RawMessage, evidence json.RawMessage) ([]Event, error) {
	content, err := rawArray(raw, "$[0][][0][0][0]", evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	if len(content) == 0 {
		return nil, nil
	}
	role, err := rawString(rawAt(content, 1), "$[0][][0][0][0][1]", evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	if role != "model" {
		return nil, d.protocolError("$[0][][0][0][0][1]", "响应角色不是 model", rawAt(content, 1))
	}
	partsRaw := rawAt(content, 0)
	if isJSONNull(partsRaw) {
		return nil, nil
	}
	parts, err := rawArray(partsRaw, "$[0][][0][0][0][0]", evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	events := make([]Event, 0, len(parts))
	for index, partRaw := range parts {
		partEvents, err := d.decodePart(partRaw, fmt.Sprintf("$[0][][0][0][0][0][%d]", index), evidence)
		if err != nil {
			return nil, err
		}
		events = append(events, partEvents...)
	}
	return events, nil
}

func (d *FrameDecoder) decodePart(raw json.RawMessage, path string, evidence json.RawMessage) ([]Event, error) {
	part, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	thought := false
	if thoughtRaw := rawAt(part, 12); !isJSONNull(thoughtRaw) {
		thought, err = rawBool(thoughtRaw, path+"[12]", raw)
		if err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
	}
	signature := ""
	if signatureRaw := rawAt(part, 14); !isJSONNull(signatureRaw) {
		signature, err = rawString(signatureRaw, path+"[14]", raw)
		if err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
	}
	events := make([]Event, 0, 2)
	emptyText := false
	textEvent := -1
	if textRaw := rawAt(part, 1); !isJSONNull(textRaw) {
		text, err := rawString(textRaw, path+"[1]", raw)
		if err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
		if text != "" {
			kind := EventText
			if thought {
				kind = EventReasoning
			}
			events = append(events, Event{Kind: kind, Text: text, ThoughtSignature: signature})
			if kind == EventText {
				textEvent = len(events) - 1
			}
		} else {
			emptyText = true
		}
	}
	if inlineRaw := rawAt(part, 2); !isJSONNull(inlineRaw) {
		media, err := decodeInlineMedia(inlineRaw, path+"[2]", raw)
		if err != nil {
			return nil, err
		}
		events = append(events, Event{Kind: EventMedia, Media: &media, ThoughtSignature: signature})
	}
	if codeRaw := rawAt(part, 7); !isJSONNull(codeRaw) {
		code, err := decodeExecutableCode(codeRaw, path+"[7]", raw)
		if err != nil {
			return nil, err
		}
		events = append(events, Event{Kind: EventExecutableCode, ExecutableCode: &code, ThoughtSignature: signature})
	}
	if resultRaw := rawAt(part, 8); !isJSONNull(resultRaw) {
		result, err := decodeCodeExecutionResult(resultRaw, path+"[8]", raw)
		if err != nil {
			return nil, err
		}
		events = append(events, Event{Kind: EventCodeExecutionResult, CodeExecutionResult: &result, ThoughtSignature: signature})
	}
	if callRaw := rawAt(part, 10); !isJSONNull(callRaw) {
		call, err := decodeFunctionCall(callRaw, path+"[10]", raw)
		if err != nil {
			return nil, err
		}
		call.ThoughtSignature = signature
		events = append(events, Event{Kind: EventToolCall, ToolCall: &call, ThoughtSignature: signature})
	}
	if transcriptRaw := rawAt(part, 22); !isJSONNull(transcriptRaw) {
		transcript, err := decodeTranscriptMetadata(transcriptRaw, path+"[22]", raw)
		if err != nil {
			return nil, err
		}
		if textEvent >= 0 && events[textEvent].Text == transcript.Text {
			events[textEvent].Transcript = &transcript
		} else {
			events = append(events, Event{
				Kind: EventText, Text: transcript.Text, Transcript: &transcript, ThoughtSignature: signature,
			})
		}
	}
	if len(events) == 0 {
		if signature != "" {
			return []Event{{Kind: EventThoughtSignature, ThoughtSignature: signature}}, nil
		}
		if emptyText {
			return nil, nil
		}
		return nil, nil
	}
	return events, nil
}

func decodeCitations(raw json.RawMessage, evidence json.RawMessage) ([]Citation, error) {
	metadata, err := rawArray(raw, "$[0][][0][0][6]", evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	entriesRaw := rawAt(metadata, 0)
	if isJSONNull(entriesRaw) {
		return nil, nil
	}
	entries, err := rawArray(entriesRaw, "$[0][][0][0][6][0]", evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	citations := make([]Citation, 0, len(entries))
	for index, entryRaw := range entries {
		path := fmt.Sprintf("$[0][][0][0][6][0][%d]", index)
		entry, err := rawArray(entryRaw, path, evidence)
		if err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
		url, err := rawString(rawAt(entry, 2), path+"[2]", evidence)
		if err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
		title := ""
		if titleRaw := rawAt(entry, 3); !isJSONNull(titleRaw) {
			title, err = rawString(titleRaw, path+"[3]", evidence)
			if err != nil {
				return nil, withMethod(err, "GenerateContent")
			}
		}
		citations = append(citations, Citation{URL: url, Title: title})
	}
	return citations, nil
}

func decodeUsage(raw json.RawMessage, evidence json.RawMessage) (Usage, bool, error) {
	values, err := rawArray(raw, "$[0][][2]", evidence)
	if err != nil {
		return Usage{}, false, withMethod(err, "GenerateContent")
	}
	if isJSONNull(rawAt(values, 0)) || isJSONNull(rawAt(values, 2)) {
		return Usage{}, false, nil
	}
	input, err := rawInt64(values[0], "$[0][][2][0]", raw)
	if err != nil {
		return Usage{}, false, withMethod(err, "GenerateContent")
	}
	output := int64(0)
	outputMissing := isJSONNull(values[1])
	if !outputMissing {
		output, err = rawInt64(values[1], "$[0][][2][1]", raw)
		if err != nil {
			return Usage{}, false, withMethod(err, "GenerateContent")
		}
	}
	total, err := rawInt64(values[2], "$[0][][2][2]", raw)
	if err != nil {
		return Usage{}, false, withMethod(err, "GenerateContent")
	}
	reasoning := int64(0)
	toolTokens := int64(0)
	if toolRaw := rawAt(values, 7); !isJSONNull(toolRaw) {
		toolTokens, err = rawInt64(toolRaw, "$[0][][2][7]", raw)
		if err != nil {
			return Usage{}, false, withMethod(err, "GenerateContent")
		}
	}
	if reasoningRaw := rawAt(values, 9); !isJSONNull(reasoningRaw) {
		reasoning, err = rawInt64(reasoningRaw, "$[0][][2][9]", raw)
		if err != nil {
			return Usage{}, false, withMethod(err, "GenerateContent")
		}
	}
	return Usage{
		InputTokens: input, OutputTokens: output, ReasoningTokens: reasoning,
		ToolTokens: toolTokens, TotalTokens: total, OutputTokensMissing: outputMissing,
	}, true, nil
}

func (d *FrameDecoder) protocolError(path string, detail string, raw json.RawMessage) error {
	return &ProtocolEvidenceError{Method: "GenerateContent", Path: path, Detail: detail, Raw: append(json.RawMessage(nil), raw...)}
}
