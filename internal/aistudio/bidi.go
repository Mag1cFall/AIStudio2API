package aistudio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// BidiMode distinguishes independent session configuration between Gemini Live and Robotics Streaming
type BidiMode string

const (
	// BidiModeLive indicates an audio-output Gemini Live session
	BidiModeLive BidiMode = "live"
	// BidiModeRobotics indicates a text-output Robotics Streaming session
	BidiModeRobotics BidiMode = "robotics"
)

// BidiRequest defines a bidirectional real-time session
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

// BidiEventKind represents bidirectional real-time protocol event kinds
type BidiEventKind string

const (
	// BidiEventSetupComplete indicates that upstream accepted the session setup
	BidiEventSetupComplete BidiEventKind = "setup_complete"
	// BidiEventText indicates model text delta
	BidiEventText BidiEventKind = "text"
	// BidiEventMedia indicates model media delta
	BidiEventMedia BidiEventKind = "media"
	// BidiEventInputTranscription indicates input transcription delta
	BidiEventInputTranscription BidiEventKind = "input_transcription"
	// BidiEventOutputTranscription indicates output transcription delta
	BidiEventOutputTranscription BidiEventKind = "output_transcription"
	// BidiEventGenerationComplete indicates that current generation is complete
	BidiEventGenerationComplete BidiEventKind = "generation_complete"
	// BidiEventTurnComplete indicates that current conversation turn is complete
	BidiEventTurnComplete BidiEventKind = "turn_complete"
	// BidiEventInterrupted indicates that current model output was interrupted
	BidiEventInterrupted BidiEventKind = "interrupted"
	// BidiEventToolCall indicates that the model initiated a function call
	BidiEventToolCall BidiEventKind = "tool_call"
	// BidiEventToolCallCancellation indicates that the model canceled an uncompleted function call
	BidiEventToolCallCancellation BidiEventKind = "tool_call_cancellation"
	// BidiEventSessionResumption indicates that upstream updated the session resumption token
	BidiEventSessionResumption BidiEventKind = "session_resumption"
	// BidiEventGoAway indicates that upstream requested terminating the current connection
	BidiEventGoAway BidiEventKind = "go_away"
	// BidiEventUsage indicates upstream returned usage fields
	BidiEventUsage BidiEventKind = "usage"
	// BidiEventProvider indicates preserved unnormalized upstream fields
	BidiEventProvider BidiEventKind = "provider"
	// BidiEventClosed indicates that the WebChannel connection closed
	BidiEventClosed BidiEventKind = "closed"
	// BidiEventError represents a bidirectional real-time protocol error
	BidiEventError BidiEventKind = "error"
)

// BidiTranscription stores real-time transcription fields
type BidiTranscription struct {
	Text         string `json:"text"`
	Finished     bool   `json:"finished,omitempty"`
	DurationMS   int64  `json:"duration_ms,omitempty"`
	LanguageCode string `json:"language_code,omitempty"`
}

// BidiEvent stores real-time events emitted in upstream order
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

// EncodeBidiSetupRequest encodes verified setup frame for Live or Robotics
func EncodeBidiSetupRequest(request BidiRequest, runtime RequestContext) ([]byte, string, error) {
	model := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	if model == "" {
		return nil, "", fmt.Errorf("%w: bidi model cannot be empty", ErrInvalidArgument)
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
		return nil, "", fmt.Errorf("%w: unrecognized bidi mode %q", ErrInvalidArgument, request.Mode)
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
				return nil, "", fmt.Errorf("encode bidi function declaration: %w", err)
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
		return nil, "", fmt.Errorf("encode bidi setup: %w", err)
	}
	return body, strings.Join(bindingParts, " "), nil
}

// EncodeBidiTextRequest encodes official text input frame
func EncodeBidiTextRequest(text string) ([]byte, string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, "", fmt.Errorf("%w: bidi text cannot be empty", ErrInvalidArgument)
	}
	wire := make([]any, 6)
	wire[2] = []any{nil, nil, nil, nil, text}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, "", fmt.Errorf("encode bidi text: %w", err)
	}
	return body, "", nil
}

// EncodeBidiMediaRequest encodes official real-time audio or image input frame
func EncodeBidiMediaRequest(mimeType string, data []byte) ([]byte, string, error) {
	mimeType = strings.TrimSpace(mimeType)
	if len(data) == 0 {
		return nil, "", fmt.Errorf("%w: bidi media cannot be empty", ErrInvalidArgument)
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
		return nil, "", fmt.Errorf("%w: unrecognized bidi media type %q", ErrInvalidArgument, mimeType)
	}
	wire := make([]any, 6)
	wire[2] = realtimeInput
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, "", fmt.Errorf("encode bidi media: %w", err)
	}
	return body, "", nil
}

// EncodeBidiMediaEndRequest encodes official real-time media end frame
func EncodeBidiMediaEndRequest() ([]byte, string, error) {
	wire := make([]any, 6)
	wire[2] = []any{nil, nil, int64(1)}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, "", fmt.Errorf("encode bidi media end: %w", err)
	}
	return body, "", nil
}

// EncodeBidiToolResponseRequest encodes official function response frame
func EncodeBidiToolResponseRequest(results []FunctionResult) ([]byte, string, error) {
	if len(results) == 0 {
		return nil, "", fmt.Errorf("%w: bidi function response list is empty", ErrInvalidArgument)
	}
	functionResponses := make([]any, 0, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.ID) == "" {
			return nil, "", fmt.Errorf("%w: bidi function response missing call ID", ErrInvalidArgument)
		}
		if strings.TrimSpace(result.Name) == "" {
			return nil, "", fmt.Errorf("%w: bidi function response missing function name", ErrInvalidArgument)
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
		return nil, "", fmt.Errorf("encode bidi function response: %w", err)
	}
	return body, results[0].ID, nil
}

// ParseBidiServerPayload decodes a WebChannel business payload
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
			Method: "BidiGenerateContent", Path: "$.__sm__", Detail: "expected object", Raw: cloneRaw(raw),
		}
	}
	statusRaw, exists := sm["status"]
	if !exists {
		return BidiEvent{}, true, &ProtocolEvidenceError{
			Method: "BidiGenerateContent", Path: "$.__sm__.status", Detail: "missing status", Raw: cloneRaw(raw),
		}
	}
	outer, err := rawArray(statusRaw, "$.__sm__.status", raw)
	if err != nil || len(outer) != 1 {
		return BidiEvent{}, true, &ProtocolEvidenceError{
			Method: "BidiGenerateContent", Path: "$.__sm__.status", Detail: "invalid status envelope", Raw: cloneRaw(raw),
		}
	}
	middle, err := rawArray(outer[0], "$.__sm__.status[0]", raw)
	if err != nil || len(middle) != 1 {
		return BidiEvent{}, true, &ProtocolEvidenceError{
			Method: "BidiGenerateContent", Path: "$.__sm__.status[0]", Detail: "invalid status envelope", Raw: cloneRaw(raw),
		}
	}
	status, err := rawArray(middle[0], "$.__sm__.status[0][0]", raw)
	if err != nil || len(status) < 2 {
		return BidiEvent{}, true, &ProtocolEvidenceError{
			Method: "BidiGenerateContent", Path: "$.__sm__.status[0][0]", Detail: "insufficient status fields", Raw: cloneRaw(raw),
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
			Detail: fmt.Sprintf("unrecognized status code %d", code), Raw: cloneRaw(raw),
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
			Method: "BidiGenerateContent", Path: "$message[3][1]", Detail: "tool call list is empty", Raw: cloneRaw(raw),
		}
	}
	calls, err := rawArray(callsRaw, "$message[3][1]", evidence)
	if err != nil {
		return nil, withBidiMethod(err)
	}
	if len(calls) == 0 {
		return nil, &ProtocolEvidenceError{
			Method: "BidiGenerateContent", Path: "$message[3][1]", Detail: "tool call list is empty", Raw: cloneRaw(callsRaw),
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
				Method: "BidiGenerateContent", Path: path + "[2]", Detail: "tool call missing call ID", Raw: cloneRaw(callRaw),
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
			Method: "BidiGenerateContent", Path: "$message[4][0]", Detail: "tool call cancellation list is empty", Raw: cloneRaw(raw),
		}
	}
	encodedIDs, err := rawArray(idsRaw, "$message[4][0]", evidence)
	if err != nil {
		return BidiEvent{}, withBidiMethod(err)
	}
	if len(encodedIDs) == 0 {
		return BidiEvent{}, &ProtocolEvidenceError{
			Method: "BidiGenerateContent", Path: "$message[4][0]", Detail: "tool call cancellation list is empty", Raw: cloneRaw(idsRaw),
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
				Detail: "tool call cancellation ID is empty", Raw: cloneRaw(encoded),
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
					return nil, fmt.Errorf("encode bidi provider event: %w", marshalErr)
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
