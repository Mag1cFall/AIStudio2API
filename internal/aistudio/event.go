package aistudio

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
)

// FrameDecoder 将 field 1 repeated 帧转换为规范事件
type FrameDecoder struct {
	usage     *Usage
	finished  bool
	frames    int
	lastFrame json.RawMessage
	// continuable 表示请求开启了自动续写
	continuable bool
	// continueAtMaxTokens 表示本轮输出上限低于请求的输出总上限
	continueAtMaxTokens bool
	// latestToken 为已出现的最近一个续写 token
	latestToken string
	// continuation 为本轮结束后下一轮使用的续写 token
	continuation string
	// continuationRequired 表示上游要求续写
	continuationRequired bool
	// safetyRatings 是最近一帧候选的安全评级
	safetyRatings []SafetyRating
	// systemRoleTokens 是请求系统指令的 user 角色计入上游输入用量的 token 数
	systemRoleTokens int64
}

// NewFrameDecoder 创建单次 GenerateContent 的有状态解码器
func NewFrameDecoder() *FrameDecoder {
	return &FrameDecoder{}
}

// Decode 解码一条 repeated 流帧
func (d *FrameDecoder) Decode(raw json.RawMessage) ([]Event, error) {
	d.frames++
	d.lastFrame = append(json.RawMessage(nil), raw...)
	frame, err := rawArray(raw, "$[0][]", raw)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	if len(frame) == 0 {
		return nil, d.protocolError("$[0][]", "空流帧", raw)
	}
	if isJSONNull(frame[0]) {
		if feedbackRaw := rawAt(frame, 1); !isJSONNull(feedbackRaw) {
			return nil, decodePromptFeedback(feedbackRaw, raw)
		}
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
	var turnToken string
	_ = json.Unmarshal(rawAt(frame, 7), &turnToken)
	frameToken := ""
	if d.continuable {
		_ = json.Unmarshal(rawAt(candidate, 8), &frameToken)
		if frameToken != "" {
			d.latestToken = frameToken
		}
	}
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
	if ratingsRaw := rawAt(candidate, 4); !isJSONNull(ratingsRaw) {
		if d.safetyRatings, err = decodeSafetyRatings(ratingsRaw, "$[0][][0][0][4]", raw); err != nil {
			return nil, err
		}
	}
	var frameUsage *Usage
	if usageRaw := rawAt(frame, 2); !isJSONNull(usageRaw) {
		usage, complete, err := decodeUsage(usageRaw, raw)
		if err != nil {
			return nil, err
		}
		if complete {
			usage = excludeSystemRole(usage, d.systemRoleTokens)
			d.usage = &usage
			frameUsage = &usage
		}
	}
	if !isJSONNull(finishRaw) {
		finishCode, err := rawInt64(finishRaw, "$[0][][0][0][1]", raw)
		if err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
		if d.continuable && (finishCode == 22 || finishCode == 2 && frameToken != "" && d.continueAtMaxTokens) {
			d.continuationRequired = true
			d.continuation = d.latestToken
			d.finished = true
			return withFrame(events, turnToken, frameUsage), nil
		}
		finishReason := decodeFinishReason(finishCode)
		var finishMessage string
		_ = json.Unmarshal(rawAt(candidate, 3), &finishMessage)
		if !d.finished {
			for index := range events {
				events[index].FinishFrame = true
			}
			if d.usage != nil {
				usage := *d.usage
				events = append(events, Event{Kind: EventUsage, Usage: &usage})
			}
			events = append(events, Event{Kind: EventFinish, FinishReason: finishReason, FinishMessage: finishMessage, SafetyRatings: d.safetyRatings})
			d.finished = true
		}
	}
	return withFrame(events, turnToken, frameUsage), nil
}

// withFrame 把流帧 field 8 的轮次令牌、令牌中的 responseId 与该帧用量写入该帧的全部事件
func withFrame(events []Event, token string, usage *Usage) []Event {
	responseID := turnResponseID(token)
	for index := range events {
		events[index].TurnToken = token
		events[index].ResponseID = responseID
		events[index].FrameUsage = usage
	}
	return events
}

// turnResponseID 返回轮次令牌 v1_ 之后 Base64 protobuf 的 field 2，即该响应的 responseId
func turnResponseID(token string) string {
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, "v1_"))
	if err != nil {
		return ""
	}
	for len(data) > 0 {
		tag, n := binary.Uvarint(data)
		if n <= 0 || tag&7 != 2 {
			return ""
		}
		size, m := binary.Uvarint(data[n:])
		if m <= 0 || size > uint64(len(data)-n-m) {
			return ""
		}
		value := data[n+m : n+m+int(size)]
		if tag>>3 == 2 {
			return string(value)
		}
		data = data[n+m+int(size):]
	}
	return ""
}

// excludeSystemRole 从上游用量的输入、总量与 TEXT 输入模态中减去系统指令的角色 token
func excludeSystemRole(usage Usage, tokens int64) Usage {
	usage.InputTokens -= tokens
	usage.TotalTokens -= tokens
	if usage.Details != nil {
		details := *usage.Details
		details.PromptTokensDetails = subtractTextTokens(details.PromptTokensDetails, tokens)
		usage.Details = &details
	}
	return usage
}

func decodePromptFeedback(raw json.RawMessage, evidence json.RawMessage) error {
	feedback, err := rawArray(raw, "$[0][][1]", evidence)
	if err != nil {
		return withMethod(err, "GenerateContent")
	}
	reason, err := rawInt64(rawAt(feedback, 0), "$[0][][1][0]", evidence)
	if err != nil {
		return withMethod(err, "GenerateContent")
	}
	var message string
	_ = json.Unmarshal(rawAt(feedback, 2), &message)
	var ratings []SafetyRating
	if ratingsRaw := rawAt(feedback, 1); !isJSONNull(ratingsRaw) {
		if ratings, err = decodeSafetyRatings(ratingsRaw, "$[0][][1][1]", evidence); err != nil {
			return err
		}
	}
	return &PromptFeedbackError{Reason: decodePromptBlockReason(reason), Message: message, SafetyRatings: ratings, Raw: append(json.RawMessage(nil), raw...)}
}

func decodePromptBlockReason(reason int64) string {
	switch reason {
	case 0:
		return "unspecified"
	case 1:
		return "safety"
	case 2:
		return "other"
	case 3:
		return "blocklist"
	case 4:
		return "prohibited_content"
	case 5:
		return "image_safety"
	default:
		return fmt.Sprintf("provider_%d", reason)
	}
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
	case 19:
		return "malformed_response"
	case 22:
		return "continuation"
	default:
		return fmt.Sprintf("provider_%d", code)
	}
}

// End 校验流已经出现正常完成帧
func (d *FrameDecoder) End() error {
	if d.continuationRequired && d.continuation == "" {
		return d.protocolError("$[0][][0][0][8]", "上游要求续写但响应没有续写 token", d.lastFrame)
	}
	if d.finished {
		return nil
	}
	return &StreamInterruptedError{Method: "GenerateContent", Frames: d.frames, Err: errMissingFinishFrame}
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
			events = append(events, Event{Kind: kind, Text: text, Thought: thought, ThoughtSignature: signature})
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
		events = append(events, mediaEvent(Part{InlineData: &media, Thought: thought, ThoughtSignature: signature}))
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
		events = append(events, Event{Kind: EventToolCall, FunctionCall: &call, ThoughtSignature: signature})
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
	for _, field := range []struct {
		index int
		kind  EventKind
	}{{25, EventServerToolCall}, {26, EventServerToolResponse}} {
		if value := rawAt(part, field.index); !isJSONNull(value) {
			step, err := decodeServerToolStep(value, fmt.Sprintf("%s[%d]", path, field.index), raw)
			if err != nil {
				return nil, err
			}
			event := Event{Kind: field.kind, ThoughtSignature: signature}
			if field.kind == EventServerToolCall {
				event.ToolCall = step
			} else {
				event.ToolResponse = step
			}
			events = append(events, event)
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
	cached := int64(0)
	if cachedRaw := rawAt(values, 3); !isJSONNull(cachedRaw) {
		cached, err = rawInt64(cachedRaw, "$[0][][2][3]", raw)
		if err != nil {
			return Usage{}, false, withMethod(err, "GenerateContent")
		}
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
	usage := Usage{
		InputTokens: input, CachedTokens: cached, OutputTokens: output, ReasoningTokens: reasoning,
		ToolTokens: toolTokens, TotalTokens: total, OutputTokensMissing: outputMissing,
	}
	var details UsageDetails
	for index, target := range []*[]ModalityTokenCount{&details.PromptTokensDetails, &details.CacheTokensDetails, &details.CandidatesTokensDetails, nil, &details.ToolUsePromptTokensDetails} {
		if target == nil {
			continue
		}
		path := fmt.Sprintf("$[0][][2][%d]", index+4)
		if *target, err = decodeModalityTokenCounts(rawAt(values, index+4), path, raw); err != nil {
			return Usage{}, false, withMethod(err, "GenerateContent")
		}
	}
	if details.present() {
		usage.Details = &details
	}
	return usage, true, nil
}

// modalityNames 是 ModalityTokenCount.modality 的枚举名
var modalityNames = map[int64]string{0: "MODALITY_UNSPECIFIED", 1: "TEXT", 2: "IMAGE", 3: "VIDEO", 4: "AUDIO", 5: "DOCUMENT"}

// decodeModalityTokenCounts 解码 repeated ModalityTokenCount 的 [modality, token_count] 数组，字段为空时返回 nil
func decodeModalityTokenCounts(raw json.RawMessage, path string, evidence json.RawMessage) ([]ModalityTokenCount, error) {
	if isJSONNull(raw) {
		return nil, nil
	}
	items, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, err
	}
	counts := make([]ModalityTokenCount, 0, len(items))
	for index, item := range items {
		fields, err := rawArray(item, fmt.Sprintf("%s[%d]", path, index), evidence)
		if err != nil {
			return nil, err
		}
		var code, count int64
		if value := rawAt(fields, 0); !isJSONNull(value) {
			if code, err = rawInt64(value, fmt.Sprintf("%s[%d][0]", path, index), evidence); err != nil {
				return nil, err
			}
		}
		if value := rawAt(fields, 1); !isJSONNull(value) {
			if count, err = rawInt64(value, fmt.Sprintf("%s[%d][1]", path, index), evidence); err != nil {
				return nil, err
			}
		}
		name, ok := modalityNames[code]
		if !ok {
			return nil, &ProtocolEvidenceError{Path: fmt.Sprintf("%s[%d][0]", path, index), Detail: fmt.Sprintf("未识别的 modality %d", code), Raw: evidence}
		}
		counts = append(counts, ModalityTokenCount{Modality: name, TokenCount: count})
	}
	return counts, nil
}

func (d *FrameDecoder) protocolError(path string, detail string, raw json.RawMessage) error {
	return &ProtocolEvidenceError{Method: "GenerateContent", Path: path, Detail: detail, Raw: append(json.RawMessage(nil), raw...)}
}
