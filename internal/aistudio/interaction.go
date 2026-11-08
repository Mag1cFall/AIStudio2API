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

// interactionMediaMIME 是 content delta 各媒体字段的 MIME 枚举表，与官网前端相同
var interactionMediaMIME = map[int]interactionMIME{
	1: {codes: map[int64]string{1: "image/png", 2: "image/jpeg", 3: "image/webp", 4: "image/heic", 5: "image/heif", 7: "image/gif", 8: "image/bmp", 9: "image/tiff"}, unlisted: "image/jpeg"},
	2: {codes: map[int64]string{1: "audio/wav", 2: "audio/mp3", 3: "audio/aiff", 4: "audio/aac", 5: "audio/ogg", 6: "audio/flac", 7: "audio/mpeg", 8: "audio/m4a", 9: "audio/l16", 10: "audio/s16le", 11: "audio/opus", 12: "audio/alaw", 13: "audio/mulaw"}, unlisted: "audio/wav"},
	3: {codes: map[int64]string{1: "application/pdf"}, unlisted: "application/pdf"},
	4: {codes: map[int64]string{1: "video/mp4", 2: "video/mpeg", 3: "video/mpg", 4: "video/mov", 5: "video/avi", 6: "video/x-flv", 7: "video/webm", 8: "video/wmv", 9: "video/3gpp"}, unlisted: "video/mp4"},
}

// interactionImageCode 返回内联图片的 MIME 枚举，MIME 不在图片枚举表中时返回 false
func interactionImageCode(mime string) (int64, bool) {
	for code, value := range interactionMediaMIME[1].codes {
		if value == mime {
			return code, true
		}
	}
	return 0, false
}

// InteractionInlineMIME 表示该 MIME 的附件经 CreateInteractionStream 与官网一样以内联数据发送，范围为图片枚举表中的类型
func InteractionInlineMIME(mime string) bool {
	_, ok := interactionImageCode(mime)
	return ok
}

// interactionMIME 是一类媒体的 MIME 枚举与枚举缺省或不在表中时的 MIME
type interactionMIME struct {
	codes    map[int64]string
	unlisted string
}

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
	if level != 0 {
		config[5] = level
	}
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
	if system := strings.TrimSpace(SystemText(request.System)); system != "" {
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
	body, _ := json.Marshal([]any{int64(1), int64(1), nil, interaction, nil, int64(1)})
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

// encodeInteractionSteps 把规范消息编码为用户 step field 1 与模型 step field 2 交替的 input steps
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
	var carriedBinding []Part
	for index, content := range contents {
		if content.Role != RoleUser && content.Role != RoleAssistant && content.Role != RoleTool {
			return nil, nil, false, fmt.Errorf("Interaction 模型不接受 %s 消息", content.Role)
		}
		model := content.Role == RoleAssistant
		parts := make([]any, 0, len(content.Parts))
		bindingParts := make([]Part, 0, len(content.Parts))
		for _, part := range content.Parts {
			switch {
			case part.VideoMetadata != nil || part.MediaProcessing != "" || part.ToolCall != nil || part.ToolResponse != nil:
				return nil, nil, false, fmt.Errorf("Interaction 模型的 contents[%d] 不接受视频截取、采样、处理方式与处理步骤", index)
			case part.Thought:
				continue
			case part.ExecutableCode != nil || part.CodeExecutionResult != nil:
				transcript, err := ToolPartTranscript(part)
				if err != nil {
					return nil, nil, false, fmt.Errorf("Interaction 模型的 contents[%d]: %w", index, err)
				}
				parts = append(parts, []any{[]any{transcript}})
				bindingParts = append(bindingParts, Part{Text: transcript})
			case part.Text != "" && part.InlineData == nil && part.File == nil:
				parts = append(parts, []any{[]any{part.Text}})
				bindingParts = append(bindingParts, Part{Text: part.Text})
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
				code, ok := interactionImageCode(part.InlineData.MIME)
				if !ok {
					return nil, nil, false, fmt.Errorf("Interaction 模型的 contents[%d] 内联附件只接受图片，收到 %s", index, part.InlineData.MIME)
				}
				// 内联图片位于 Content field 2，值为 MIME 枚举与 Base64 数据，与官网一样以 Base64 参与 binding
				media := []any{nil, []any{code, base64.StdEncoding.EncodeToString(part.InlineData.Data)}}
				if model {
					carried = append(carried, media)
					carriedBinding = append(carriedBinding, Part{InlineData: part.InlineData})
				} else {
					parts = append(parts, media)
					bindingParts = append(bindingParts, Part{InlineData: part.InlineData})
				}
			default:
				return nil, nil, false, fmt.Errorf("Interaction 模型的 contents[%d] 只接受文本、附件与工具 part", index)
			}
		}
		if !model && len(carried) > 0 {
			parts = append(carried, parts...)
			bindingParts = append(carriedBinding, bindingParts...)
			carried, carriedBinding = nil, nil
		}
		if len(parts) == 0 {
			continue
		}
		if last := len(steps) - 1; last >= 0 && steps[last].model == model {
			steps[last].parts = append(steps[last].parts, parts...)
		} else {
			steps = append(steps, step{model: model, parts: parts})
		}
		binding = append(binding, Content{Role: content.Role, Parts: bindingParts})
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

// interactionThinkingLevel 按请求的思考强度或预算选择模型支持的 Interaction thinking level，模型没有思考档位能力时与官网一样不发送并返回 0
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
	if !defaults.ThinkingLevel {
		return 0, nil
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

// interactionTask 是 CreateInteraction 后台任务的上游 interaction ID、所属账户与取消任务的函数
type interactionTask struct {
	id      string
	account string
	cancel  func()
}

// interactionEvents 解码 Interaction 流并映射为规范事件；task 为后台任务时结束事件带上 interaction ID 与账户，流在读到最终 interaction 之前因调用方取消而结束时取消任务
func (c *Client) interactionEvents(ctx context.Context, request GenerateRequest, entry modelEntry, method string, response *RPCResponse, task *interactionTask) (<-chan Event, error) {
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
			if event.Kind == EventFinish && task != nil {
				event.InteractionID, event.AccountID = task.id, task.account
			}
			select {
			case events <- event:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		var output Output
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
				output.Add(event)
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
			if !finished && task != nil && (ctx.Err() != nil || err != nil) {
				task.cancel()
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
	environmentID := ""
	if value := rawAt(interaction, 24); !isJSONNull(value) {
		if environmentID, err = rawString(value, "$event[19][0][24]", raw); err != nil {
			return false, withMethod(err, method)
		}
	}
	return true, emit(Event{Kind: EventFinish, FinishReason: "stop", EnvironmentID: environmentID})
}

// decodeInteractionDelta 解码 content delta：field 1 文本、field 2 图片、field 3 音频、field 4 文档、field 5 视频、field 6 思考摘要、field 7 思考签名、field 10 引用
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
		case 1, 2, 3, 4:
			media, err := decodeInteractionMedia(method, value, index, raw)
			if err != nil {
				return err
			}
			if err := emit(Event{Kind: EventMedia, InlineData: media}); err != nil {
				return err
			}
		case 5:
			text := strings.Join(interactionStrings(value), "")
			if text != "" {
				if err := emit(Event{Kind: EventReasoning, Text: text, Thought: true}); err != nil {
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

// agentTools 按官网的构造编码 CreateInteraction field 8 的工具列表，依次为代码执行、函数声明、Google 搜索与网址读取；请求带有工具列表字段时只用列出的工具，否则在目录 field 82 默认开启的工具上加入请求的工具
func agentTools(tools Tools, model Model) ([]any, error) {
	switch {
	case len(tools.MCPServers) > 0:
		return nil, fmt.Errorf("agent 不接受 %s 工具", "mcp_server")
	case len(tools.FileSearch) > 0:
		return nil, fmt.Errorf("agent 不接受 %s 工具", "file_search")
	case tools.ComputerUse != nil:
		return nil, fmt.Errorf("agent 不接受 %s 工具", "computer_use")
	}
	code, search, fetch := false, tools.GoogleSearch != nil && tools.GoogleSearch.WebSearch, false
	for _, name := range tools.Google {
		switch name {
		case "code_execution":
			code = true
		case "google_search":
			search = true
		case "url_context":
			fetch = true
		default:
			return nil, fmt.Errorf("agent 不接受 %s 工具", name)
		}
	}
	if !tools.Explicit {
		code = code || model.Capabilities["default_capability_code_9"]
		search = search || model.Capabilities["default_capability_code_12"]
		fetch = fetch || model.Capabilities["default_capability_code_13"]
	}
	var list []any
	if code {
		list = append(list, []any{nil, []any{}})
	}
	for _, function := range tools.Functions {
		// Tool field 1 为函数声明：field 1 名称、field 2 说明、field 3 以 Value 表示的参数
		declaration := []any{function.Name, nil, nil}
		if function.Description != "" {
			declaration[1] = function.Description
		}
		if len(function.Parameters) > 0 {
			var parameters any
			if err := json.Unmarshal(function.Parameters, &parameters); err != nil {
				return nil, fmt.Errorf("function %q parameters: %w", function.Name, err)
			}
			declaration[2] = encodeWireValue(parameters)
		}
		list = append(list, []any{declaration})
	}
	if search {
		list = append(list, []any{nil, nil, nil, nil, nil, []any{}})
	}
	if fetch {
		list = append(list, []any{nil, nil, []any{}})
	}
	return list, nil
}

// AgentInput 按官网选取 agent 后台任务实际发送的输入内容：续接上游 interaction 时只取最后一个模型轮之后的内容，否则只取用户内容
func AgentInput(contents []Content, continued bool) []Content {
	if continued {
		for index := len(contents) - 1; index >= 0; index-- {
			if contents[index].Role == RoleAssistant {
				return contents[index+1:]
			}
		}
		return contents
	}
	return slices.DeleteFunc(slices.Clone(contents), func(content Content) bool { return content.Role != RoleUser })
}

// deepResearchConfig 返回 Deep Research 的 agent 配置：field 1 思考摘要（1 auto、2 none），field 2 可视化（2 auto、1 off），field 3 协作规划，取值同官网的构造，未设置时为官网默认的 [1,2]
func deepResearchConfig(config *AgentConfig) []any {
	value := []any{int64(1), int64(2)}
	if config == nil {
		return value
	}
	if config.ThinkingSummaries == "none" {
		value[0] = int64(2)
	}
	if config.Visualization == "off" {
		value[1] = int64(1)
	}
	if config.CollaborativePlanning {
		value = append(value, true)
	}
	return value
}

// environmentSourceTypes 是环境源类型在官网请求中的枚举值
var environmentSourceTypes = map[string]int64{"gcs": 2, "inline": 3, "repository": 4}

// setInteractionEnvironment 按官网运行设置写出 agent 环境：只引用已有环境时 field 24 为环境 ID，否则 field 23 为环境配置（field 1 源、field 2 出站白名单、field 3 禁止出站、field 4 已有环境 ID），未设置时为官网默认的禁止出站新环境
func setInteractionEnvironment(interaction []any, environment *AgentEnvironment) error {
	if environment == nil {
		environment = &AgentEnvironment{}
	}
	if environment.ID != "" && len(environment.Sources) == 0 && len(environment.Allowlist) == 0 && !environment.NetworkDisabled {
		interaction[23] = environment.ID
		return nil
	}
	config := make([]any, 4)
	if len(environment.Sources) > 0 {
		sources := make([]any, 0, len(environment.Sources))
		for index, source := range environment.Sources {
			kind, ok := environmentSourceTypes[source.Type]
			if !ok {
				return fmt.Errorf("environment.sources[%d].type %q 应为 gcs、inline 或 repository", index, source.Type)
			}
			sources = append(sources, trimWireNil([]any{kind, emptyNil(source.Source), emptyNil(source.Target), emptyNil(source.Content), emptyNil(source.Encoding)}))
		}
		config[0] = sources
	}
	switch {
	case len(environment.Allowlist) > 0:
		rules := make([]any, 0, len(environment.Allowlist))
		for _, rule := range environment.Allowlist {
			// 与官网一致，设置凭据时只发送凭据，否则发送请求头
			wire := []any{rule.Domain, nil, nil}
			if rule.Credential != "" {
				wire[2] = rule.Credential
			} else if len(rule.Headers) > 0 {
				headers := make([]any, 0, len(rule.Headers))
				for _, name := range slices.Sorted(maps.Keys(rule.Headers)) {
					headers = append(headers, []any{name, rule.Headers[name]})
				}
				wire[1] = headers
			}
			rules = append(rules, trimWireNil(wire))
		}
		config[1] = []any{rules}
	case environment.NetworkDisabled || environment.ID == "":
		config[2] = int64(1)
	}
	if environment.ID != "" {
		config[3] = environment.ID
	}
	interaction[22] = trimWireNil(config)
	return nil
}

// emptyNil 把空字符串换为 nil，使未设置的字段不出现在请求中
func emptyNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// trimWireNil 去掉数组末尾的 nil，与官网 JSPB 序列化一致
func trimWireNil(values []any) []any {
	for len(values) > 0 && values[len(values)-1] == nil {
		values = values[:len(values)-1]
	}
	return values
}

// EncodeCreateInteractionRequest 编码官网后台 agent 的 CreateInteraction 请求并返回参与 WAA 绑定的文本内容
func EncodeCreateInteractionRequest(request GenerateRequest, model Model) ([]byte, []Content, error) {
	agent := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	// kind 是该 agent 的 agent_config 类型：带默认模型的 agent 为 antigravity，其余为 deep-research
	kind := "deep-research"
	if model.AgentModel != "" {
		kind = "antigravity"
	}
	if config := request.AgentConfig; config != nil && config.Type != kind {
		return nil, nil, fmt.Errorf("agent_config.type %q 与 agent %s 不匹配，应为 %q", config.Type, agent, kind)
	}
	steps, binding, _, err := encodeInteractionSteps(AgentInput(request.Contents, request.PreviousInteractionID != ""))
	if err != nil {
		return nil, nil, err
	}
	tools, err := agentTools(request.Tools, model)
	if err != nil {
		return nil, nil, err
	}
	interaction := make([]any, 27)
	if system := strings.TrimSpace(SystemText(request.System)); system != "" {
		interaction[6] = system
	}
	if len(tools) > 0 {
		interaction[7] = tools
	}
	// field 17 为续接的上一轮 interaction ID
	if request.PreviousInteractionID != "" {
		interaction[16] = request.PreviousInteractionID
	}
	// field 19 为 agent 目标：field 1 agent 名称，Deep Research 的 field 3 为 agent 配置
	target := []any{agent, nil, deepResearchConfig(request.AgentConfig)}
	if model.AgentModel != "" {
		// Antigravity：field 12 为所选模型与 token 上限
		target = target[:1]
		if config := request.AgentConfig; config != nil && (config.Model != "" || config.MaxTotalTokens > 0) {
			antigravity := make([]any, 7)
			if config.Model != "" {
				antigravity[0] = strings.TrimPrefix(config.Model, "models/")
			}
			if config.MaxTotalTokens > 0 {
				antigravity[6] = config.MaxTotalTokens
			}
			target = append(target, make([]any, 11)...)
			target[11] = antigravity
		}
	}
	if request.Environment != nil || model.AgentModel != "" {
		if err := setInteractionEnvironment(interaction, request.Environment); err != nil {
			return nil, nil, err
		}
	}
	interaction[18] = target
	interaction[26] = []any{steps}
	body, _ := json.Marshal([]any{nil, int64(1), int64(1), interaction, nil, int64(1)})
	return body, binding, nil
}

// EncodeGetInteractionStreamRequest 编码按 interaction ID 读取后台任务增量与终态的请求
func EncodeGetInteractionStreamRequest(id string, model string) ([]byte, error) {
	return json.Marshal([]any{id, int64(1), nil, nil, nil, int64(1), wireModelName(model)})
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
	body, binding, err := EncodeCreateInteractionRequest(request, entry.model)
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
	return c.interactionEvents(ctx, request, entry, "GetInteractionStream", response, &interactionTask{id: id, account: accountID, cancel: cancel})
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

// decodeInteractionMedia 解码媒体 content：MIME 枚举与 Base64 数据，枚举缺省或不在表中时按官网取该类媒体的默认 MIME
func decodeInteractionMedia(method string, value json.RawMessage, index int, raw json.RawMessage) (*Blob, error) {
	path := fmt.Sprintf("$delta[1][%d]", index)
	media, err := rawArray(value, path, raw)
	if err != nil || len(media) < 2 {
		return nil, &ProtocolEvidenceError{Method: method, Path: path, Detail: "媒体 delta 无效", Raw: cloneRaw(raw)}
	}
	var code int64
	if !isJSONNull(media[0]) {
		if code, err = rawInt64(media[0], path+"[0]", raw); err != nil {
			return nil, withMethod(err, method)
		}
	}
	kind := interactionMediaMIME[index]
	mimeType, ok := kind.codes[code]
	if !ok {
		mimeType = kind.unlisted
	}
	encoded, err := rawString(media[1], path+"[1]", raw)
	if err != nil {
		return nil, withMethod(err, method)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, &ProtocolEvidenceError{Method: method, Path: path + "[1]", Detail: "媒体数据不是 Base64", Raw: cloneRaw(raw)}
	}
	return &Blob{MIME: mimeType, Data: data}, nil
}
