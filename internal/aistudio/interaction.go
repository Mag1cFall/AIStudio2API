package aistudio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
)

// interactionThinkingLevels 把 GenerateContent thinking level 换算为 Interaction 枚举
var interactionThinkingLevels = map[int64]int64{4: 1, 1: 2, 2: 3, 3: 4}

// interactionCancelTimeout 是取消后台任务的期限，包含等待账户请求槽位的时间
const interactionCancelTimeout = time.Minute

// interactionVideoMIME 表示 Interaction 视频内容的 MIME 枚举
var interactionVideoMIME = map[int64]string{1: "video/mp4"}

// interactionImageMIME 表示 Interaction 图片内容的 MIME 枚举
var interactionImageMIME = map[int64]string{1: "image/png"}

// interactionStatusHTTP 把流尾 google.rpc 状态码换算为 HTTP 状态
var interactionStatusHTTP = map[int64]int{
	3: http.StatusBadRequest, 4: http.StatusGatewayTimeout, 5: http.StatusNotFound, 7: http.StatusForbidden,
	8: http.StatusTooManyRequests, 9: http.StatusBadRequest, 13: http.StatusInternalServerError,
	14: http.StatusServiceUnavailable, 16: http.StatusUnauthorized,
}

// EncodeCreateInteractionStreamRequest 编码官网 CreateInteractionStream 请求并返回参与 WAA 绑定的文本内容
func EncodeCreateInteractionStreamRequest(request GenerateRequest, defaults GenerationDefaults) ([]byte, []Content, error) {
	steps, binding, hasModelTurn, err := encodeInteractionSteps(request.Contents)
	if err != nil {
		return nil, nil, err
	}
	level, err := interactionThinkingLevel(request.Config, defaults)
	if err != nil {
		return nil, nil, err
	}
	maxOutput := defaults.MaxOutputTokens
	if request.Config.MaxOutputTokens != nil {
		maxOutput = *request.Config.MaxOutputTokens
	}
	if maxOutput > defaults.MaxOutputTokens {
		maxOutput = defaults.MaxOutputTokens
	}
	if maxOutput <= 0 {
		return nil, nil, fmt.Errorf("max output tokens %d 超出模型范围 1-%d", maxOutput, defaults.MaxOutputTokens)
	}
	video, task, err := interactionVideoFormat(request.Config.VideoConfig, defaults.VideoResolution)
	if err != nil {
		return nil, nil, err
	}
	// GenerationConfig：field 6 thinking level、field 7 thinking summaries、field 8 max output tokens、field 25 视频任务
	config := make([]any, 8)
	config[5] = level
	config[6] = int64(1)
	config[7] = maxOutput
	if !hasModelTurn || task != 0 {
		config = append(config, make([]any, 17)...)
		config[24] = []any{}
		if task != 0 {
			config[24] = []any{task}
		}
	}
	interaction := make([]any, 54)
	if system := strings.TrimSpace(request.System); system != "" {
		interaction[6] = system
	}
	interaction[17] = []any{wireModelName(request.Model), config}
	interaction[26] = []any{steps}
	// field 54 输出格式：视频格式 field 4 宽高比、field 5 时长、field 6 分辨率，编辑任务不带输出格式
	if task == interactionVideoTasks["edit"] {
		interaction = interaction[:27]
	} else {
		interaction[53] = []any{[]any{[]any{nil, nil, nil, video}}}
	}
	body, err := json.Marshal([]any{int64(1), int64(1), nil, interaction, nil, int64(1)})
	if err != nil {
		return nil, nil, fmt.Errorf("编码 CreateInteractionStream: %w", err)
	}
	return body, binding, nil
}

// interactionVideoAspectRatios 是 CreateInteractionStream 视频输出格式的宽高比枚举
var interactionVideoAspectRatios = map[string]int64{"16:9": 1, "9:16": 2}

// interactionVideoResolutions 是 CreateInteractionStream 视频输出格式的分辨率枚举
var interactionVideoResolutions = map[string]int64{"360p": 1, "720p": 2, "1080p": 3, "4k": 4}

// interactionVideoTasks 是 CreateInteractionStream 视频任务枚举，名称取 Interactions API video_config.task
var interactionVideoTasks = map[string]int64{"text_to_video": 1, "image_to_video": 2, "reference_to_video": 3, "edit": 4, "extend": 5}

// interactionVideoFormat 编码视频输出格式并返回任务枚举：宽高比与分辨率取最接近的上游取值，未设置分辨率时取模型默认分辨率
func interactionVideoFormat(config *VideoConfig, resolution string) ([]any, int64, error) {
	format := []any{nil, nil, nil, nil, nil, nil}
	if value, ok := interactionVideoResolutions[resolution]; ok {
		format[5] = value
	}
	if config == nil {
		return format, 0, nil
	}
	if config.AspectRatio != "" {
		ratio, ok := AspectRatioLog(config.AspectRatio)
		if !ok {
			return nil, 0, fmt.Errorf("无法解析视频宽高比 %q", config.AspectRatio)
		}
		format[3] = interactionVideoAspectRatios[NearestOption(ratio, slices.Collect(maps.Keys(interactionVideoAspectRatios)), AspectRatioLog)]
	}
	if config.DurationSeconds < 0 {
		return nil, 0, fmt.Errorf("视频时长不能为负数")
	}
	if config.DurationSeconds > 0 {
		format[4] = []any{config.DurationSeconds}
	}
	if config.Resolution != "" {
		height, ok := videoResolutionHeight(config.Resolution)
		if !ok {
			return nil, 0, fmt.Errorf("无法解析视频分辨率 %q", config.Resolution)
		}
		format[5] = interactionVideoResolutions[NearestOption(height, slices.Collect(maps.Keys(interactionVideoResolutions)), videoResolutionHeight)]
	}
	if config.Task == "" {
		return format, 0, nil
	}
	task, ok := interactionVideoTasks[strings.ToLower(config.Task)]
	if !ok {
		return nil, 0, fmt.Errorf("video task 必须是 text_to_video、image_to_video、reference_to_video、edit 或 extend")
	}
	return format, task, nil
}

// encodeInteractionSteps 把规范消息编码为用户 step field 1 与模型 step field 2 交替的 input steps，工具 part 改为文本转录，模型消息的附件移到下一个用户 step
func encodeInteractionSteps(contents []Content) ([]any, []Content, bool, error) {
	type step struct {
		model bool
		parts []any
	}
	contents, err := transcribeFunctionParts(contents)
	if err != nil {
		return nil, nil, false, fmt.Errorf("Interaction 模型的 %w", err)
	}
	steps := make([]step, 0, len(contents))
	binding := make([]Content, 0, len(contents))
	var carried []any
	for index, content := range contents {
		if content.Role != RoleUser && content.Role != RoleAssistant && content.Role != RoleTool {
			return nil, nil, false, fmt.Errorf("Interaction 模型不接受 %s 消息", content.Role)
		}
		model := content.Role == RoleAssistant
		parts := make([]any, 0, len(content.Parts))
		texts := make([]Part, 0, len(content.Parts))
		for _, part := range content.Parts {
			switch {
			case part.Thought:
				continue
			case part.ExecutableCode != nil || part.CodeExecutionResult != nil:
				transcript, err := toolPartTranscript(part)
				if err != nil {
					return nil, nil, false, fmt.Errorf("Interaction 模型的 contents[%d]: %w", index, err)
				}
				parts = append(parts, []any{[]any{transcript}})
				texts = append(texts, Part{Text: transcript})
			case part.Text != "" && part.InlineData == nil && part.File == nil:
				parts = append(parts, []any{[]any{part.Text}})
				texts = append(texts, Part{Text: part.Text})
			case part.Text == "" && part.ThoughtSignature != "" && part.InlineData == nil && part.File == nil:
				continue
			case part.File != nil && strings.TrimSpace(part.File.ID) != "" && part.Text == "":
				// Content field 9 为 Drive 文件引用
				file := []any{nil, nil, nil, nil, nil, nil, nil, nil, []any{strings.TrimSpace(part.File.ID)}}
				if model {
					carried = append(carried, file)
				} else {
					parts = append(parts, file)
				}
			case part.InlineData != nil:
				return nil, nil, false, fmt.Errorf("Interaction 模型的 contents[%d] 附件需要账户 Drive 授权", index)
			default:
				return nil, nil, false, fmt.Errorf("Interaction 模型的 contents[%d] 只接受文本、附件与工具 part", index)
			}
		}
		if !model && len(carried) > 0 {
			parts = append(carried, parts...)
			carried = nil
		}
		if len(parts) == 0 {
			continue
		}
		if last := len(steps) - 1; last >= 0 && steps[last].model == model {
			steps[last].parts = append(steps[last].parts, parts...)
		} else {
			steps = append(steps, step{model: model, parts: parts})
		}
		binding = append(binding, Content{Role: content.Role, Parts: texts})
	}
	if len(steps) == 0 {
		return nil, nil, false, fmt.Errorf("CreateInteractionStream contents 不能为空")
	}
	wire := make([]any, 0, len(steps))
	hasModelTurn := false
	for _, step := range steps {
		if step.model {
			wire = append(wire, []any{nil, []any{step.parts}})
			hasModelTurn = true
		} else {
			wire = append(wire, []any{[]any{step.parts}})
		}
	}
	return wire, binding, hasModelTurn, nil
}

// interactionThinkingLevel 按请求的思考强度或预算选择模型支持的 Interaction thinking level
func interactionThinkingLevel(config GenerationConfig, defaults GenerationDefaults) (int64, error) {
	level := defaults.DefaultThinkingLevel
	switch normalizedReasoningEffort(config.ReasoningEffort) {
	case "":
		if config.ThinkingBudget != nil {
			level = thinkingLevelForBudget(*config.ThinkingBudget)
		}
	case "minimal", "none":
		level = 4
	case "low":
		level = 1
	case "medium":
		level = 2
	case "high":
		level = 3
	default:
		return 0, fmt.Errorf("reasoning effort 必须是 none、minimal、low、medium、high、xhigh 或 max")
	}
	level = closestSupportedThinkingLevel(level, defaults.ThinkingLevels)
	wire, ok := interactionThinkingLevels[level]
	if !ok {
		return 0, fmt.Errorf("未识别的 thinking level %d", level)
	}
	return wire, nil
}

// generateInteraction 经 CreateInteractionStream 生成并映射为规范事件
func (c *Client) generateInteraction(ctx context.Context, request GenerateRequest, entry modelEntry) (<-chan Event, error) {
	reason := ""
	if request.Unary {
		reason = "该模型使用 CreateInteractionStream，收集完成后返回"
	}
	reportUpstreamMode(ctx, "CreateInteractionStream", "stream", reason)
	body, binding, err := EncodeCreateInteractionStreamRequest(request, entry.defaults)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	rpc := newRPCRequest("CreateInteractionStream", request.AccountID, request.ID, body, true)
	c.applyBenefitTier(rpc.Method, request.AccountID, rpc.Header)
	bindingRequest := request
	bindingRequest.Contents = binding
	response, err := c.protected.DoProtected(ctx, bindingRequest, rpc)
	if err != nil {
		return nil, fmt.Errorf("发送 AI Studio CreateInteractionStream: %w", err)
	}
	response, err = validateRPCResponse("CreateInteractionStream", response)
	if err != nil {
		return nil, err
	}
	return c.interactionEvents(ctx, request, entry, "CreateInteractionStream", response, nil)
}

// interactionEvents 解码 Interaction 流并映射为规范事件，aborted 在流读到最终 interaction 之前因调用方取消而结束时调用
func (c *Client) interactionEvents(ctx context.Context, request GenerateRequest, entry modelEntry, method string, response *RPCResponse, aborted func()) (<-chan Event, error) {
	matcher := newStopSequenceMatcher(request.Config.StopSequences)
	var stopTokenCount <-chan tokenCountResult
	cancelTokenCount := context.CancelFunc(func() {})
	if matcher != nil {
		stopTokenCount, cancelTokenCount = c.countStopInput(ctx, request)
	}
	events := make(chan Event, 8)
	ready := make(chan error, 1)
	go func() {
		defer close(events)
		defer cancelTokenCount()
		stopClose := context.AfterFunc(ctx, func() {
			_ = response.Body.Close()
		})
		defer stopClose()
		committed := false
		send := func(event Event) error {
			if request.Config.HideThinking && event.Kind == EventReasoning {
				if event.ThoughtSignature == "" {
					return nil
				}
				event.Kind, event.Text = EventThoughtSignature, ""
			}
			if !committed {
				committed = true
				ready <- nil
			}
			event.ProviderModel = entry.model.ID
			select {
			case events <- event:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		var output generatedOutputParts
		var usage *Event
		finished := false
		matchedStopSequence := ""
		// forward 暂存用量，在补发 stop sequence 暂存文本后随终态发送
		forward := func(event Event) error {
			switch event.Kind {
			case EventUsage:
				usage = &event
				return nil
			case EventFinish:
				finished = true
				if usage != nil {
					if err := send(*usage); err != nil {
						return err
					}
				}
			default:
				output.observe(event)
			}
			return send(event)
		}
		emit := func(event Event) error {
			if event.Kind == EventFinish {
				finished = true
			}
			return send(event)
		}
		if matcher != nil {
			emit = stopSequenceEmitter(matcher, forward, &matchedStopSequence)
		}
		var err error
		defer func() {
			if !finished && aborted != nil && (ctx.Err() != nil || err != nil) {
				aborted()
			}
		}()
		err = DecodeInteractionStream(method, observeStreamActivity(ctx, response.Body), emit)
		if errors.Is(err, errStopSequenceMatched) {
			_ = response.Body.Close()
			finishAtStopSequence(ctx, request, output, stopTokenCount, matchedStopSequence, send)
			err = ctx.Err()
		} else if closeErr := response.Body.Close(); err == nil && ctx.Err() == nil {
			err = closeErr
		}
		if err == nil || ctx.Err() != nil {
			if !committed {
				ready <- ctx.Err()
			}
			return
		}
		if !committed {
			ready <- err
			return
		}
		_ = send(Event{Kind: EventError, Err: err})
	}()
	select {
	case err := <-ready:
		if err != nil {
			for range events {
			}
			return nil, err
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return events, nil
}

// DecodeInteractionStream 按网络顺序解码 CreateInteractionStream 或 GetInteractionStream 的事件与流尾状态
func DecodeInteractionStream(method string, source io.Reader, emit func(Event) error) error {
	decoder := json.NewDecoder(newSparseJSONReader(source))
	decoder.UseNumber()
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return &ProtocolEvidenceError{Method: method, Path: "$", Detail: "根值不是数组"}
	}
	if !decoder.More() {
		return &ProtocolEvidenceError{Method: method, Path: "$", Detail: "根数组缺少事件列表"}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return &ProtocolEvidenceError{Method: method, Path: "$[0]", Detail: "事件列表不是数组"}
	}
	finished := false
	for index := 0; decoder.More(); index++ {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return &ProtocolEvidenceError{Method: method, Path: fmt.Sprintf("$[0][%d]", index), Detail: err.Error()}
		}
		done, err := decodeInteractionEvent(method, raw, emit)
		if err != nil {
			return err
		}
		finished = finished || done
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		return &ProtocolEvidenceError{Method: method, Path: "$[0]", Detail: "事件列表没有正常结束"}
	}
	trailing := make([]json.RawMessage, 0, 3)
	for decoder.More() {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return &ProtocolEvidenceError{Method: method, Path: "$", Detail: err.Error()}
		}
		trailing = append(trailing, raw)
	}
	if len(trailing) > 0 {
		// 流尾 field 2 为 google.rpc.Status：[code, message, details]
		status, err := rawArray(trailing[0], "$[1]", trailing[0])
		if err != nil || len(status) == 0 {
			return &ProtocolEvidenceError{Method: method, Path: "$[1]", Detail: "流尾状态无效", Raw: cloneRaw(trailing[0])}
		}
		code, err := rawInt64(status[0], "$[1][0]", trailing[0])
		if err != nil {
			return withMethod(err, method)
		}
		if code != 0 {
			statusCode := http.StatusBadGateway
			if status, ok := interactionStatusHTTP[code]; ok {
				statusCode = status
			}
			return DecodeRPCError(method, statusCode, trailing[0])
		}
	}
	if !finished {
		return &ProtocolEvidenceError{Method: method, Path: "$", Detail: "流结束前没有最终 interaction"}
	}
	return nil
}

// decodeInteractionEvent 解码单个流事件，返回是否为最终 interaction
func decodeInteractionEvent(method string, raw json.RawMessage, emit func(Event) error) (bool, error) {
	event, err := rawArray(raw, "$event", raw)
	if err != nil {
		return false, withMethod(err, method)
	}
	if delta := rawAt(event, 10); !isJSONNull(delta) {
		return false, decodeInteractionDelta(method, delta, emit)
	}
	final := rawAt(event, 19)
	if isJSONNull(final) {
		final = rawAt(event, 1)
	}
	if isJSONNull(final) {
		return false, nil
	}
	wrapper, err := rawArray(final, "$event[19]", raw)
	if err != nil || len(wrapper) == 0 {
		return false, &ProtocolEvidenceError{Method: method, Path: "$event[19]", Detail: "最终 interaction 为空", Raw: cloneRaw(raw)}
	}
	interaction, err := rawArray(wrapper[0], "$event[19][0]", raw)
	if err != nil {
		return false, withMethod(err, method)
	}
	status, err := optionalIntField(interaction, 1, "$event[19][0]", raw)
	if err != nil {
		return false, withMethod(err, method)
	}
	switch status {
	case 3:
	case 4:
		return false, &RPCError{Method: method, StatusCode: http.StatusBadGateway, Message: "interaction failed"}
	case 5:
		return false, &RPCError{Method: method, StatusCode: http.StatusBadGateway, Message: "interaction cancelled"}
	default:
		return false, &ProtocolEvidenceError{Method: method, Path: "$event[19][0][1]", Detail: fmt.Sprintf("未识别的最终状态 %d", status), Raw: cloneRaw(raw)}
	}
	if usage := rawAt(interaction, 12); !isJSONNull(usage) {
		decoded, err := decodeInteractionUsage(method, usage, raw)
		if err != nil {
			return false, err
		}
		if err := emit(Event{Kind: EventUsage, Usage: decoded}); err != nil {
			return false, err
		}
	}
	return true, emit(Event{Kind: EventFinish, FinishReason: "stop"})
}

// decodeInteractionDelta 解码 content delta：field 1 文本、field 2 图片、field 5 视频、field 6 思考摘要、field 7 思考签名、field 10 引用
func decodeInteractionDelta(method string, raw json.RawMessage, emit func(Event) error) error {
	delta, err := rawArray(raw, "$delta", raw)
	if err != nil || len(delta) < 2 {
		return &ProtocolEvidenceError{Method: method, Path: "$delta", Detail: "content delta 字段不足", Raw: cloneRaw(raw)}
	}
	content, err := rawArray(delta[1], "$delta[1]", raw)
	if err != nil {
		return withMethod(err, method)
	}
	for index, value := range content {
		if isJSONNull(value) {
			continue
		}
		switch index {
		case 0:
			text, err := rawArray(value, "$delta[1][0]", raw)
			if err != nil || len(text) == 0 {
				return &ProtocolEvidenceError{Method: method, Path: "$delta[1][0]", Detail: "文本 delta 无效", Raw: cloneRaw(raw)}
			}
			decoded, err := rawString(text[0], "$delta[1][0][0]", raw)
			if err != nil {
				return withMethod(err, method)
			}
			if err := emit(Event{Kind: EventText, Text: decoded}); err != nil {
				return err
			}
		case 1, 4:
			mimes := interactionImageMIME
			if index == 4 {
				mimes = interactionVideoMIME
			}
			media, err := decodeInteractionMedia(method, value, index, mimes, raw)
			if err != nil {
				return err
			}
			if err := emit(Event{Kind: EventMedia, Media: media}); err != nil {
				return err
			}
		case 5:
			text := strings.Join(interactionStrings(value), "")
			if text != "" {
				if err := emit(Event{Kind: EventReasoning, Text: text}); err != nil {
					return err
				}
			}
		case 6:
		case 9:
			if err := decodeInteractionCitations(method, value, raw, emit); err != nil {
				return err
			}
		default:
			return &ProtocolEvidenceError{Method: method, Path: fmt.Sprintf("$delta[1][%d]", index), Detail: "未识别的 content delta 字段", Raw: cloneRaw(raw)}
		}
	}
	return nil
}

// interactionStrings 按顺序收集嵌套数组中的全部字符串
func interactionStrings(raw json.RawMessage) []string {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	var values []string
	var walk func(any)
	walk = func(item any) {
		switch typed := item.(type) {
		case string:
			values = append(values, typed)
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	return values
}

// decodeInteractionUsage 解码 Usage：输入、输出、思考与总 token 位于索引 0、4、8、9
func decodeInteractionUsage(method string, raw json.RawMessage, evidence json.RawMessage) (*Usage, error) {
	values, err := rawArray(raw, "$usage", evidence)
	if err != nil {
		return nil, withMethod(err, method)
	}
	usage := &Usage{}
	for _, field := range []struct {
		index  int
		target *int64
	}{{0, &usage.InputTokens}, {4, &usage.OutputTokens}, {8, &usage.ReasoningTokens}, {9, &usage.TotalTokens}} {
		value, err := optionalIntField(values, field.index, "$usage", evidence)
		if err != nil {
			return nil, withMethod(err, method)
		}
		*field.target = value
	}
	if usage.TotalTokens == 0 {
		return nil, errors.New(method + " usage 缺少 total tokens")
	}
	return usage, nil
}

// interactionTaskTools 是官网 Deep Research 创建任务时随请求发送的工具配置
var interactionTaskTools = []any{[]any{nil, nil, nil, nil, nil, []any{}}, []any{nil, nil, []any{}}}

// EncodeCreateInteractionRequest 编码官网后台 agent 的 CreateInteraction 请求并返回参与 WAA 绑定的文本内容
func EncodeCreateInteractionRequest(request GenerateRequest) ([]byte, []Content, error) {
	steps, binding, _, err := encodeInteractionSteps(request.Contents)
	if err != nil {
		return nil, nil, err
	}
	interaction := make([]any, 27)
	if system := strings.TrimSpace(request.System); system != "" {
		interaction[6] = system
	}
	interaction[7] = interactionTaskTools
	// field 19 为 agent 名称与官网固定的 agent 配置
	interaction[18] = []any{strings.TrimPrefix(strings.TrimSpace(request.Model), "models/"), nil, []any{int64(1), int64(2)}}
	interaction[26] = []any{steps}
	body, err := json.Marshal([]any{nil, int64(1), int64(1), interaction, nil, int64(1)})
	if err != nil {
		return nil, nil, fmt.Errorf("编码 CreateInteraction: %w", err)
	}
	return body, binding, nil
}

// EncodeGetInteractionStreamRequest 编码按 interaction ID 读取后台任务增量与终态的请求
func EncodeGetInteractionStreamRequest(id string, model string) ([]byte, error) {
	body, err := json.Marshal([]any{id, int64(1), nil, nil, nil, int64(1), wireModelName(model)})
	if err != nil {
		return nil, fmt.Errorf("编码 GetInteractionStream: %w", err)
	}
	return body, nil
}

// decodeCreateInteractionResponse 读取 CreateInteraction 返回的 interaction ID
func decodeCreateInteractionResponse(raw []byte) (string, error) {
	values, err := rawArray(raw, "$", raw)
	if err != nil || len(values) == 0 {
		return "", &ProtocolEvidenceError{Method: "CreateInteraction", Path: "$", Detail: "响应不是 interaction", Raw: cloneRaw(raw)}
	}
	id, err := rawString(values[0], "$[0]", raw)
	if err != nil || strings.TrimSpace(id) == "" {
		return "", &ProtocolEvidenceError{Method: "CreateInteraction", Path: "$[0]", Detail: "缺少 interaction ID", Raw: cloneRaw(raw)}
	}
	return id, nil
}

// generateInteractionTask 经 CreateInteraction 创建后台任务，再经 GetInteractionStream 读取增量与终态，调用方中途取消时发送 CancelInteraction
func (c *Client) generateInteractionTask(ctx context.Context, request GenerateRequest, entry modelEntry) (<-chan Event, error) {
	reason := ""
	if request.Unary {
		reason = "该模型使用 CreateInteraction 后台任务，收集完成后返回"
	}
	reportUpstreamMode(ctx, "GetInteractionStream", "stream", reason)
	body, binding, err := EncodeCreateInteractionRequest(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	rpc := newRPCRequest("CreateInteraction", request.AccountID, request.ID, body, false)
	c.applyBenefitTier(rpc.Method, request.AccountID, rpc.Header)
	bindingRequest := request
	bindingRequest.Contents = binding
	response, err := c.protected.DoProtected(ctx, bindingRequest, rpc)
	if err != nil {
		return nil, fmt.Errorf("发送 AI Studio CreateInteraction: %w", err)
	}
	response, err = validateRPCResponse("CreateInteraction", response)
	if err != nil {
		return nil, err
	}
	created, err := io.ReadAll(response.Body)
	if closeErr := response.Body.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("读取 AI Studio CreateInteraction: %w", err)
	}
	id, err := decodeCreateInteractionResponse(created)
	if err != nil {
		return nil, err
	}
	accountID := request.AccountID
	if lease, ok := AccountLeaseFromContext(ctx); ok {
		accountID = lease.Account().ID
	}
	// cancel 在后台以该账户发送 CancelInteraction，调用方仍持有的账户租约释放后才取得请求槽位
	cancel := func() {
		go func() {
			body, _ := json.Marshal([]any{"interactions/" + id})
			cancelCtx, stop := context.WithTimeout(context.Background(), interactionCancelTimeout)
			defer stop()
			response, err := c.do(cancelCtx, "CancelInteraction", accountID, "", body, false)
			if err == nil {
				err = response.Body.Close()
			}
			if err != nil {
				slog.Warn("Deep Research 任务取消失败", "account", accountID, "interaction", id, "error", err)
			}
		}()
	}
	streamBody, err := EncodeGetInteractionStreamRequest(id, request.Model)
	if err != nil {
		return nil, err
	}
	stream := newRPCRequest("GetInteractionStream", request.AccountID, request.ID, streamBody, true)
	c.applyBenefitTier(stream.Method, request.AccountID, stream.Header)
	// GetInteractionStream 的 WAA 绑定输入是 interaction ID
	streamRequest := request
	streamRequest.Contents = []Content{{Role: RoleUser, Parts: []Part{{Text: id}}}}
	response, err = c.protected.DoProtected(ctx, streamRequest, stream)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("发送 AI Studio GetInteractionStream: %w", err)
	}
	response, err = validateRPCResponse("GetInteractionStream", response)
	if err != nil {
		cancel()
		return nil, err
	}
	return c.interactionEvents(ctx, request, entry, "GetInteractionStream", response, cancel)
}

// decodeInteractionCitations 解码 content field 10 的引用：每项为正文中引用标记的起止字符位置与来源网址
func decodeInteractionCitations(method string, value json.RawMessage, raw json.RawMessage, emit func(Event) error) error {
	groups, err := rawArray(value, "$delta[1][9]", raw)
	if err != nil {
		return withMethod(err, method)
	}
	for _, group := range groups {
		items, err := rawArray(group, "$delta[1][9][]", raw)
		if err != nil {
			return withMethod(err, method)
		}
		for _, item := range items {
			fields, err := rawArray(item, "$delta[1][9][][]", raw)
			if err != nil || len(fields) < 4 {
				return &ProtocolEvidenceError{Method: method, Path: "$delta[1][9][][]", Detail: "引用字段不足", Raw: cloneRaw(raw)}
			}
			start, err := optionalIntField(fields, 0, "$delta[1][9][][]", raw)
			if err != nil {
				return withMethod(err, method)
			}
			end, err := optionalIntField(fields, 1, "$delta[1][9][][]", raw)
			if err != nil {
				return withMethod(err, method)
			}
			urls := interactionStrings(fields[3])
			if len(urls) == 0 {
				return &ProtocolEvidenceError{Method: method, Path: "$delta[1][9][][3]", Detail: "引用缺少网址", Raw: cloneRaw(raw)}
			}
			if err := emit(Event{Kind: EventCitation, Citation: &Citation{URL: urls[0], Start: int(start), End: int(end)}}); err != nil {
				return err
			}
		}
	}
	return nil
}

// decodeInteractionMedia 解码图片或视频 content：MIME 枚举与 Base64 数据
func decodeInteractionMedia(method string, value json.RawMessage, index int, mimes map[int64]string, raw json.RawMessage) (*Media, error) {
	path := fmt.Sprintf("$delta[1][%d]", index)
	media, err := rawArray(value, path, raw)
	if err != nil || len(media) < 2 {
		return nil, &ProtocolEvidenceError{Method: method, Path: path, Detail: "媒体 delta 无效", Raw: cloneRaw(raw)}
	}
	code, err := rawInt64(media[0], path+"[0]", raw)
	if err != nil {
		return nil, withMethod(err, method)
	}
	mimeType, ok := mimes[code]
	if !ok {
		return nil, &ProtocolEvidenceError{Method: method, Path: path + "[0]", Detail: fmt.Sprintf("未识别的媒体 MIME 枚举 %d", code), Raw: cloneRaw(raw)}
	}
	encoded, err := rawString(media[1], path+"[1]", raw)
	if err != nil {
		return nil, withMethod(err, method)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, &ProtocolEvidenceError{Method: method, Path: path + "[1]", Detail: "媒体数据不是 Base64", Raw: cloneRaw(raw)}
	}
	return &Media{MIME: mimeType, Data: data}, nil
}
