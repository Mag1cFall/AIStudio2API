package aistudio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// BidiMode 区分 Gemini Live 与 Robotics Streaming 的独立会话配置
type BidiMode string

const (
	// BidiModeLive 表示音频输出的 Gemini Live 会话
	BidiModeLive BidiMode = "live"
	// BidiModeRobotics 表示文本输出的 Robotics Streaming 会话
	BidiModeRobotics BidiMode = "robotics"
)

// BidiRequest 定义一条双向实时会话
type BidiRequest struct {
	Model                    string
	Mode                     BidiMode
	Tools                    []FunctionDeclaration
	AccountID                string
	AllowedAccountIDs        []string
	SessionToken             string
	ModelAccessScope         string
	RecoverWAARuntime        func(context.Context, string, error) (bool, error)
	ObserveWAARuntime        func(string, uint64)
	ObserveModelAccessChange func()
	ObserveAccountFailure    func(string, error)
}

// BidiEventKind 表示双向实时协议事件
type BidiEventKind string

const (
	// BidiEventSetupComplete 表示上游已接受会话配置
	BidiEventSetupComplete BidiEventKind = "setup_complete"
	// BidiEventText 表示模型文本增量
	BidiEventText BidiEventKind = "text"
	// BidiEventMedia 表示模型媒体增量
	BidiEventMedia BidiEventKind = "media"
	// BidiEventInputTranscription 表示输入转写增量
	BidiEventInputTranscription BidiEventKind = "input_transcription"
	// BidiEventOutputTranscription 表示输出转写增量
	BidiEventOutputTranscription BidiEventKind = "output_transcription"
	// BidiEventGenerationComplete 表示当前生成已完成
	BidiEventGenerationComplete BidiEventKind = "generation_complete"
	// BidiEventTurnComplete 表示当前对话轮次已完成
	BidiEventTurnComplete BidiEventKind = "turn_complete"
	// BidiEventInterrupted 表示当前模型输出被打断
	BidiEventInterrupted BidiEventKind = "interrupted"
	// BidiEventToolCall 表示模型发起函数调用
	BidiEventToolCall BidiEventKind = "tool_call"
	// BidiEventToolCallCancellation 表示模型取消尚未完成的函数调用
	BidiEventToolCallCancellation BidiEventKind = "tool_call_cancellation"
	// BidiEventSessionResumption 表示上游更新恢复令牌
	BidiEventSessionResumption BidiEventKind = "session_resumption"
	// BidiEventGoAway 表示上游要求结束当前连接
	BidiEventGoAway BidiEventKind = "go_away"
	// BidiEventUsage 表示上游返回用量字段
	BidiEventUsage BidiEventKind = "usage"
	// BidiEventProvider 表示已保留的未归一化上游字段
	BidiEventProvider BidiEventKind = "provider"
	// BidiEventClosed 表示 WebChannel 已结束
	BidiEventClosed BidiEventKind = "closed"
	// BidiEventError 表示双向实时协议错误
	BidiEventError BidiEventKind = "error"
)

// BidiTranscription 保存实时转写字段
type BidiTranscription struct {
	Text         string `json:"text"`
	Finished     bool   `json:"finished,omitempty"`
	DurationMS   int64  `json:"duration_ms,omitempty"`
	LanguageCode string `json:"language_code,omitempty"`
}

// BidiEvent 保存按上游顺序输出的实时事件
type BidiEvent struct {
	Kind          BidiEventKind      `json:"kind"`
	Text          string             `json:"text,omitempty"`
	Media         *Media             `json:"media,omitempty"`
	Transcription *BidiTranscription `json:"transcription,omitempty"`
	ToolCall      *FunctionCall      `json:"tool_call,omitempty"`
	ToolCallIDs   []string           `json:"tool_call_ids,omitempty"`
	SessionToken  string             `json:"session_token,omitempty"`
	Resumable     bool               `json:"resumable,omitempty"`
	Raw           json.RawMessage    `json:"raw,omitempty"`
	Err           error              `json:"-"`
}

// EncodeBidiSetupRequest 编码 Live 或 Robotics 的已验证 setup 帧
func EncodeBidiSetupRequest(request BidiRequest, runtime RequestContext) ([]byte, string, error) {
	model := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	if model == "" {
		return nil, "", fmt.Errorf("%w: bidi model 不能为空", ErrInvalidArgument)
	}
	configuration := make([]any, 18)
	setup := make([]any, 16)
	switch request.Mode {
	case BidiModeLive:
		configuration[14] = []any{int64(3)}
		configuration[15] = []any{[]any{[]any{"Zephyr"}}}
		configuration[16] = []any{int64(1), nil, nil, int64(4)}
	case BidiModeRobotics:
		configuration[14] = []any{int64(1)}
		configuration[16] = []any{int64(1), nil, nil, int64(3)}
	default:
		return nil, "", fmt.Errorf("%w: 未识别的 bidi mode %q", ErrInvalidArgument, request.Mode)
	}
	configuration[17] = int64(2)
	wireModel := wireModelName(model)
	setup[0] = wireModel
	setup[1] = configuration
	bindingParts := []string{wireModel}
	if len(request.Tools) > 0 {
		declarations := make([]any, 0, len(request.Tools))
		for _, declaration := range request.Tools {
			encoded, err := encodeFunctionDeclaration(declaration)
			if err != nil {
				return nil, "", fmt.Errorf("编码 bidi function declaration: %w", err)
			}
			declarations = append(declarations, encoded)
			bindingParts = append(bindingParts, declaration.Name+" "+declaration.Description)
		}
		tool := make([]any, 2)
		tool[1] = declarations
		setup[2] = []any{tool}
	}
	if sessionToken := strings.TrimSpace(request.SessionToken); sessionToken != "" {
		setup[6] = []any{sessionToken}
	} else {
		setup[6] = []any{}
	}
	setup[7] = []any{int64(104857), []any{int64(52428)}}
	setup[9] = []any{}
	setup[10] = []any{}
	if timezone := strings.TrimSpace(runtime.Timezone); timezone != "" {
		setup[15] = []any{nil, nil, nil, nil, []any{timezone}}
	}
	wire := make([]any, 7)
	wire[6] = setup
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, "", fmt.Errorf("编码 bidi setup: %w", err)
	}
	return body, strings.Join(bindingParts, " "), nil
}

// EncodeBidiTextRequest 编码官网文本输入帧
func EncodeBidiTextRequest(text string) ([]byte, string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, "", fmt.Errorf("%w: bidi text 不能为空", ErrInvalidArgument)
	}
	wire := make([]any, 6)
	wire[2] = []any{nil, nil, nil, nil, text}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, "", fmt.Errorf("编码 bidi text: %w", err)
	}
	return body, "", nil
}

// EncodeBidiMediaRequest 编码官网实时音频或图像输入帧
func EncodeBidiMediaRequest(mimeType string, data []byte) ([]byte, string, error) {
	mimeType = strings.TrimSpace(mimeType)
	if len(data) == 0 {
		return nil, "", fmt.Errorf("%w: bidi media 不能为空", ErrInvalidArgument)
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	var realtimeInput []any
	switch mimeType {
	case "audio/pcm":
		realtimeInput = make([]any, 2)
		realtimeInput[1] = []any{mimeType, encoded}
	case "image/jpeg":
		realtimeInput = make([]any, 4)
		realtimeInput[3] = []any{mimeType, encoded}
	default:
		return nil, "", fmt.Errorf("%w: 未识别的 bidi media type %q", ErrInvalidArgument, mimeType)
	}
	wire := make([]any, 6)
	wire[2] = realtimeInput
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, "", fmt.Errorf("编码 bidi media: %w", err)
	}
	return body, "", nil
}

// EncodeBidiMediaEndRequest 编码官网实时媒体结束帧
func EncodeBidiMediaEndRequest() ([]byte, string, error) {
	wire := make([]any, 6)
	wire[2] = []any{nil, nil, int64(1)}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, "", fmt.Errorf("编码 bidi media end: %w", err)
	}
	return body, "", nil
}

// EncodeBidiToolResponseRequest 编码官网函数响应帧
func EncodeBidiToolResponseRequest(results []FunctionResult) ([]byte, string, error) {
	if len(results) == 0 {
		return nil, "", fmt.Errorf("%w: bidi function response 列表为空", ErrInvalidArgument)
	}
	functionResponses := make([]any, 0, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.ID) == "" {
			return nil, "", fmt.Errorf("%w: bidi function response 缺少调用 ID", ErrInvalidArgument)
		}
		if strings.TrimSpace(result.Name) == "" {
			return nil, "", fmt.Errorf("%w: bidi function response 缺少函数名", ErrInvalidArgument)
		}
		response, err := encodeWireStructJSON(result.Content)
		if err != nil {
			return nil, "", fmt.Errorf("%w: bidi function response content %v", ErrInvalidArgument, err)
		}
		functionResponses = append(functionResponses, []any{result.Name, response, result.ID})
	}
	responses := make([]any, 2)
	responses[1] = functionResponses
	wire := make([]any, 6)
	wire[3] = responses
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, "", fmt.Errorf("编码 bidi function response: %w", err)
	}
	return body, results[0].ID, nil
}

// ParseBidiServerPayload 解码一条 WebChannel 业务 payload
func ParseBidiServerPayload(raw json.RawMessage) ([]BidiEvent, error) {
	if event, matched, err := parseBidiStatusPayload(raw); matched {
		if err != nil {
			return nil, err
		}
		return []BidiEvent{event}, nil
	}
	messages, err := rawArray(raw, "$payload", raw)
	if err != nil {
		return nil, withBidiMethod(err)
	}
	if len(messages) == 1 {
		if marker, markerErr := rawString(messages[0], "$payload[0]", raw); markerErr == nil {
			switch marker {
			case "noop":
				return nil, nil
			case "close":
				return []BidiEvent{{Kind: BidiEventClosed}}, nil
			case "stop":
				return []BidiEvent{{Kind: BidiEventError, Err: fmt.Errorf("bidi WebChannel server sent stop")}}, nil
			}
		}
	}
	events := make([]BidiEvent, 0, len(messages))
	for index, messageRaw := range messages {
		message, err := rawArray(messageRaw, fmt.Sprintf("$payload[%d]", index), raw)
		if err != nil {
			return nil, withBidiMethod(err)
		}
		decoded, err := parseBidiServerMessage(message, messageRaw)
		if err != nil {
			return nil, err
		}
		events = append(events, decoded...)
	}
	return events, nil
}

func parseBidiStatusPayload(raw json.RawMessage) (BidiEvent, bool, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return BidiEvent{}, false, nil
	}
	smRaw, matched := root["__sm__"]
	if !matched {
		return BidiEvent{}, false, nil
	}
	var sm map[string]json.RawMessage
	if err := json.Unmarshal(smRaw, &sm); err != nil {
		return BidiEvent{}, true, &ProtocolEvidenceError{
			Method: "BidiGenerateContent", Path: "$.__sm__", Detail: "期望对象", Raw: cloneRaw(raw),
		}
	}
	statusRaw, exists := sm["status"]
	if !exists {
		return BidiEvent{}, true, &ProtocolEvidenceError{
			Method: "BidiGenerateContent", Path: "$.__sm__.status", Detail: "缺少状态", Raw: cloneRaw(raw),
		}
	}
	outer, err := rawArray(statusRaw, "$.__sm__.status", raw)
	if err != nil || len(outer) != 1 {
		return BidiEvent{}, true, &ProtocolEvidenceError{
			Method: "BidiGenerateContent", Path: "$.__sm__.status", Detail: "状态 envelope 无效", Raw: cloneRaw(raw),
		}
	}
	middle, err := rawArray(outer[0], "$.__sm__.status[0]", raw)
	if err != nil || len(middle) != 1 {
		return BidiEvent{}, true, &ProtocolEvidenceError{
			Method: "BidiGenerateContent", Path: "$.__sm__.status[0]", Detail: "状态 envelope 无效", Raw: cloneRaw(raw),
		}
	}
	status, err := rawArray(middle[0], "$.__sm__.status[0][0]", raw)
	if err != nil || len(status) < 2 {
		return BidiEvent{}, true, &ProtocolEvidenceError{
			Method: "BidiGenerateContent", Path: "$.__sm__.status[0][0]", Detail: "状态字段不足", Raw: cloneRaw(raw),
		}
	}
	code, err := rawInt64(status[0], "$.__sm__.status[0][0][0]", raw)
	if err != nil {
		return BidiEvent{}, true, withBidiMethod(err)
	}
	message, err := rawString(status[1], "$.__sm__.status[0][0][1]", raw)
	if err != nil {
		return BidiEvent{}, true, withBidiMethod(err)
	}
	statusCode := 0
	switch code {
	case 5:
		statusCode = 404
	case 7:
		statusCode = 403
	default:
		return BidiEvent{}, true, &ProtocolEvidenceError{
			Method: "BidiGenerateContent", Path: "$.__sm__.status[0][0][0]",
			Detail: fmt.Sprintf("未识别的状态码 %d", code), Raw: cloneRaw(raw),
		}
	}
	return BidiEvent{
		Kind: BidiEventError,
		Err:  &RPCError{Method: "BidiGenerateContent", StatusCode: statusCode, Code: code, Message: message},
		Raw:  cloneRaw(raw),
	}, true, nil
}

func parseBidiServerMessage(message []json.RawMessage, evidence json.RawMessage) ([]BidiEvent, error) {
	events := make([]BidiEvent, 0, 4)
	if setup := rawAt(message, 1); !isJSONNull(setup) {
		if _, err := rawArray(setup, "$message[1]", evidence); err != nil {
			return nil, withBidiMethod(err)
		}
		events = append(events, BidiEvent{Kind: BidiEventSetupComplete})
	}
	if serverContent := rawAt(message, 2); !isJSONNull(serverContent) {
		decoded, err := parseBidiServerContent(serverContent, evidence)
		if err != nil {
			return nil, err
		}
		events = append(events, decoded...)
	}
	if toolCall := rawAt(message, 3); !isJSONNull(toolCall) {
		decoded, err := parseBidiToolCalls(toolCall, evidence)
		if err != nil {
			return nil, err
		}
		events = append(events, decoded...)
	}
	if toolCancellation := rawAt(message, 4); !isJSONNull(toolCancellation) {
		event, err := parseBidiToolCallCancellation(toolCancellation, evidence)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if usage := rawAt(message, 5); !isJSONNull(usage) {
		events = append(events, BidiEvent{Kind: BidiEventUsage, Raw: cloneRaw(usage)})
	}
	if goAway := rawAt(message, 6); !isJSONNull(goAway) {
		events = append(events, BidiEvent{Kind: BidiEventGoAway, Raw: cloneRaw(goAway)})
	}
	if resumption := rawAt(message, 7); !isJSONNull(resumption) {
		event, err := parseBidiSessionResumption(resumption, evidence)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if len(events) == 0 && len(message) > 0 {
		events = append(events, BidiEvent{Kind: BidiEventProvider, Raw: cloneRaw(evidence)})
	}
	return events, nil
}

func parseBidiToolCalls(raw json.RawMessage, evidence json.RawMessage) ([]BidiEvent, error) {
	values, err := rawArray(raw, "$message[3]", evidence)
	if err != nil {
		return nil, withBidiMethod(err)
	}
	callsRaw := rawAt(values, 1)
	if isJSONNull(callsRaw) {
		return nil, &ProtocolEvidenceError{
			Method: "BidiGenerateContent", Path: "$message[3][1]", Detail: "tool call 列表为空", Raw: cloneRaw(raw),
		}
	}
	calls, err := rawArray(callsRaw, "$message[3][1]", evidence)
	if err != nil {
		return nil, withBidiMethod(err)
	}
	if len(calls) == 0 {
		return nil, &ProtocolEvidenceError{
			Method: "BidiGenerateContent", Path: "$message[3][1]", Detail: "tool call 列表为空", Raw: cloneRaw(callsRaw),
		}
	}
	events := make([]BidiEvent, 0, len(calls))
	for index, callRaw := range calls {
		path := fmt.Sprintf("$message[3][1][%d]", index)
		call, err := decodeFunctionCall(callRaw, path, evidence)
		if err != nil {
			return nil, withBidiMethod(err)
		}
		if strings.TrimSpace(call.ID) == "" {
			return nil, &ProtocolEvidenceError{
				Method: "BidiGenerateContent", Path: path + "[2]", Detail: "tool call 缺少调用 ID", Raw: cloneRaw(callRaw),
			}
		}
		events = append(events, BidiEvent{Kind: BidiEventToolCall, ToolCall: &call})
	}
	return events, nil
}

func parseBidiToolCallCancellation(raw json.RawMessage, evidence json.RawMessage) (BidiEvent, error) {
	values, err := rawArray(raw, "$message[4]", evidence)
	if err != nil {
		return BidiEvent{}, withBidiMethod(err)
	}
	idsRaw := rawAt(values, 0)
	if isJSONNull(idsRaw) {
		return BidiEvent{}, &ProtocolEvidenceError{
			Method: "BidiGenerateContent", Path: "$message[4][0]", Detail: "tool call cancellation 列表为空", Raw: cloneRaw(raw),
		}
	}
	encodedIDs, err := rawArray(idsRaw, "$message[4][0]", evidence)
	if err != nil {
		return BidiEvent{}, withBidiMethod(err)
	}
	if len(encodedIDs) == 0 {
		return BidiEvent{}, &ProtocolEvidenceError{
			Method: "BidiGenerateContent", Path: "$message[4][0]", Detail: "tool call cancellation 列表为空", Raw: cloneRaw(idsRaw),
		}
	}
	ids := make([]string, 0, len(encodedIDs))
	for index, encoded := range encodedIDs {
		id, err := rawString(encoded, fmt.Sprintf("$message[4][0][%d]", index), evidence)
		if err != nil {
			return BidiEvent{}, withBidiMethod(err)
		}
		if id == "" {
			return BidiEvent{}, &ProtocolEvidenceError{
				Method: "BidiGenerateContent", Path: fmt.Sprintf("$message[4][0][%d]", index),
				Detail: "tool call cancellation ID 为空", Raw: cloneRaw(encoded),
			}
		}
		ids = append(ids, id)
	}
	return BidiEvent{Kind: BidiEventToolCallCancellation, ToolCallIDs: ids}, nil
}

func parseBidiServerContent(raw json.RawMessage, evidence json.RawMessage) ([]BidiEvent, error) {
	content, err := rawArray(raw, "$message[2]", evidence)
	if err != nil {
		return nil, withBidiMethod(err)
	}
	events := make([]BidiEvent, 0, 6)
	if modelContent := rawAt(content, 0); !isJSONNull(modelContent) {
		decoded, err := parseBidiContent(modelContent, evidence)
		if err != nil {
			return nil, err
		}
		events = append(events, decoded...)
	}
	for _, field := range []struct {
		index int
		kind  BidiEventKind
	}{
		{index: 5, kind: BidiEventInputTranscription},
		{index: 6, kind: BidiEventOutputTranscription},
	} {
		transcriptionRaw := rawAt(content, field.index)
		if isJSONNull(transcriptionRaw) {
			continue
		}
		transcription, err := parseBidiTranscription(transcriptionRaw, evidence)
		if err != nil {
			return nil, err
		}
		events = append(events, BidiEvent{
			Kind: field.kind, Text: transcription.Text, Transcription: &transcription,
		})
	}
	for _, field := range []struct {
		index int
		kind  BidiEventKind
	}{
		{index: 2, kind: BidiEventInterrupted},
		{index: 4, kind: BidiEventGenerationComplete},
		{index: 1, kind: BidiEventTurnComplete},
	} {
		valueRaw := rawAt(content, field.index)
		if isJSONNull(valueRaw) {
			continue
		}
		value, err := rawBool(valueRaw, fmt.Sprintf("$message[2][%d]", field.index), evidence)
		if err != nil {
			return nil, withBidiMethod(err)
		}
		if value {
			events = append(events, BidiEvent{Kind: field.kind})
		}
	}
	if len(events) == 0 {
		events = append(events, BidiEvent{Kind: BidiEventProvider, Raw: cloneRaw(raw)})
	}
	return events, nil
}

func parseBidiContent(raw json.RawMessage, evidence json.RawMessage) ([]BidiEvent, error) {
	content, err := rawArray(raw, "$message[2][0]", evidence)
	if err != nil {
		return nil, withBidiMethod(err)
	}
	partsRaw := rawAt(content, 0)
	if isJSONNull(partsRaw) {
		return nil, nil
	}
	parts, err := rawArray(partsRaw, "$message[2][0][0]", evidence)
	if err != nil {
		return nil, withBidiMethod(err)
	}
	decoder := NewFrameDecoder()
	events := make([]BidiEvent, 0, len(parts))
	for index, partRaw := range parts {
		decoded, err := decoder.decodePart(partRaw, fmt.Sprintf("$message[2][0][0][%d]", index), evidence)
		if err != nil {
			return nil, withBidiMethod(err)
		}
		for _, event := range decoded {
			switch event.Kind {
			case EventText:
				events = append(events, BidiEvent{Kind: BidiEventText, Text: event.Text})
			case EventMedia:
				events = append(events, BidiEvent{Kind: BidiEventMedia, Media: event.Media})
			case EventToolCall:
				events = append(events, BidiEvent{Kind: BidiEventToolCall, ToolCall: event.ToolCall})
			default:
				encoded, marshalErr := json.Marshal(event)
				if marshalErr != nil {
					return nil, fmt.Errorf("编码 bidi provider event: %w", marshalErr)
				}
				events = append(events, BidiEvent{Kind: BidiEventProvider, Raw: encoded})
			}
		}
	}
	return events, nil
}

func parseBidiTranscription(raw json.RawMessage, evidence json.RawMessage) (BidiTranscription, error) {
	values, err := rawArray(raw, "$transcription", evidence)
	if err != nil {
		return BidiTranscription{}, withBidiMethod(err)
	}
	text, err := rawString(rawAt(values, 0), "$transcription[0]", evidence)
	if err != nil {
		return BidiTranscription{}, withBidiMethod(err)
	}
	transcription := BidiTranscription{Text: text}
	if finished := rawAt(values, 1); !isJSONNull(finished) {
		transcription.Finished, err = rawBool(finished, "$transcription[1]", evidence)
		if err != nil {
			return BidiTranscription{}, withBidiMethod(err)
		}
	}
	if duration := rawAt(values, 2); !isJSONNull(duration) {
		transcription.DurationMS, err = rawInt64(duration, "$transcription[2]", evidence)
		if err != nil {
			return BidiTranscription{}, withBidiMethod(err)
		}
	}
	if language := rawAt(values, 3); !isJSONNull(language) {
		transcription.LanguageCode, err = rawString(language, "$transcription[3]", evidence)
		if err != nil {
			return BidiTranscription{}, withBidiMethod(err)
		}
	}
	return transcription, nil
}

func parseBidiSessionResumption(raw json.RawMessage, evidence json.RawMessage) (BidiEvent, error) {
	values, err := rawArray(raw, "$message[7]", evidence)
	if err != nil {
		return BidiEvent{}, withBidiMethod(err)
	}
	event := BidiEvent{Kind: BidiEventSessionResumption}
	if token := rawAt(values, 0); !isJSONNull(token) {
		event.SessionToken, err = rawString(token, "$message[7][0]", evidence)
		if err != nil {
			return BidiEvent{}, withBidiMethod(err)
		}
	}
	if resumable := rawAt(values, 1); !isJSONNull(resumable) {
		event.Resumable, err = rawBool(resumable, "$message[7][1]", evidence)
		if err != nil {
			return BidiEvent{}, withBidiMethod(err)
		}
	}
	return event, nil
}

func withBidiMethod(err error) error {
	return withMethod(err, "BidiGenerateContent")
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}
