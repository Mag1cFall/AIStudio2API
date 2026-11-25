package aistudio

import (
	"cmp"
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

// interactionThinkingLevels 把 GenerateContent thinking level 换算为 Interaction 枚举，含 extra_high(5)
var interactionThinkingLevels = map[int64]int64{4: 1, 1: 2, 2: 3, 3: 4, 5: 5}

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

// InteractionInlineMIME 表示该 MIME 的附件经 Interaction 模型路径与官网一样以内联数据发送，范围为图片枚举表中的类型
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

// interactionVideoAspectRatios 是 Interaction 视频输出格式的宽高比枚举
var interactionVideoAspectRatios = map[string]int64{"16:9": 1, "9:16": 2}

// interactionVideoResolutions 是 Interaction 视频输出格式的分辨率枚举
var interactionVideoResolutions = map[string]int64{"360p": 1, "720p": 2, "1080p": 3, "4k": 4}

// interactionVideoTasks 是 Interaction 视频任务枚举，名称取 Interactions API video_config.task
var interactionVideoTasks = map[string]int64{"text_to_video": 1, "image_to_video": 2, "reference_to_video": 3, "edit": 4, "extend": 5}

// thinkingLevelCode 把目录中保存的思考档位名称换算回 ListModels 数值码
func thinkingLevelCode(name string) int64 {
	for code, label := range buildThinkingLevels {
		if strings.EqualFold(label, strings.TrimSpace(name)) {
			return code
		}
	}
	return 0
}

// modelGenerationDefaults 从 Model 还原 Interaction 编码所需的生成默认值
func modelGenerationDefaults(model Model) GenerationDefaults {
	defaults := GenerationDefaults{
		MaxOutputTokens:      model.OutputTokenLimit,
		Temperature:          model.Temperature,
		TopP:                 model.TopP,
		TopK:                 model.TopK,
		Thinking:             model.Capabilities["thinking"],
		ThinkingBudget:       model.Capabilities["thinking_budget"],
		ThinkingLevel:        model.Capabilities["thinking_level"],
		DefaultThinkingLevel: 2,
		ImageRoute:           model.Capabilities["image_route"],
		OutputResolution:     model.Capabilities["output_resolution"],
		InteractionStream:    model.Capabilities["interaction_stream"],
		InteractionTask:      model.Capabilities["interaction_task"],
		InteractionAgent:     model.Capabilities["interaction_agent"],
		InteractionModel:     model.Capabilities["interaction_model"],
		VideoOutput:          model.Capabilities["secondary_capability_code_14"] && model.Capabilities["chat_model"],
	}
	if code := thinkingLevelCode(model.DefaultThinkingLevel); code > 0 {
		defaults.DefaultThinkingLevel = code
	}
	for _, name := range model.ThinkingLevels {
		if code := thinkingLevelCode(name); code > 0 {
			defaults.ThinkingLevels = append(defaults.ThinkingLevels, code)
		}
	}
	if resolutions := model.CapabilityOptions["video_output_resolutions"]; len(resolutions) > 0 {
		defaults.VideoResolution = resolutions[0]
	}
	return defaults
}

// EncodeCreateInteractionStreamRequest 编码官网 CreateInteractionStream 请求并返回参与 WAA 绑定的文本内容
func EncodeCreateInteractionStreamRequest(request GenerateRequest, defaults GenerationDefaults) ([]byte, int, []Content, error) {
	capabilities := map[string]bool{}
	if defaults.VideoOutput || defaults.VideoResolution != "" {
		capabilities["secondary_capability_code_14"] = true
		capabilities["chat_model"] = true
	}
	if defaults.Thinking {
		capabilities["thinking"] = true
	}
	if defaults.ThinkingLevel {
		capabilities["thinking_level"] = true
	}
	model := Model{
		ID:               strings.TrimPrefix(strings.TrimSpace(request.Model), "models/"),
		OutputTokenLimit: defaults.MaxOutputTokens,
		Temperature:      defaults.Temperature,
		TopP:             defaults.TopP,
		TopK:             defaults.TopK,
		Capabilities:     capabilities,
	}
	route := interactionRouting(model, defaults, ChannelPlayground)
	if !route.IsStream() {
		kind := int64(2)
		if route.IsAgent() {
			kind = 1
		}
		route = ResolveInteractionRouting(kind, false)
	}
	return encodeInteractionRequest(request, model, defaults, route)
}

// EncodeCreateInteractionRequest 编码官网 CreateInteraction 请求并返回参与 WAA 绑定的内容，按 field 79 选路写入模型目标（field 18）或 agent 目标（field 19）
func EncodeCreateInteractionRequest(request GenerateRequest, model Model) ([]byte, int, []Content, error) {
	defaults := modelGenerationDefaults(model)
	route := interactionRouting(model, defaults, ChannelPlayground)
	if !route.IsTask() {
		kind := int64(1)
		if route.IsModel() {
			kind = 2
		}
		route = ResolveInteractionRouting(kind, true)
	}
	return encodeInteractionRequest(request, model, defaults, route)
}

// encodeInteractionRequest 统一编码 CreateInteractionStream 与 CreateInteraction 请求：按 route.Target 选择模型（field 18）或 agent（field 19），按 route.Method 选择流式或后台信封
func encodeInteractionRequest(request GenerateRequest, model Model, defaults GenerationDefaults, route InteractionRouting) ([]byte, int, []Content, error) {
	streamFlag, taskFlag := any(nil), any(int64(1))
	if route.IsStream() {
		streamFlag, taskFlag = int64(1), nil
	}
	if route.IsAgent() {
		interaction, binding, err := encodeAgentInteraction(request, model)
		if err != nil {
			return nil, 0, nil, err
		}
		body, proofAt, _ := marshalWAAArray([]any{streamFlag, int64(1), taskFlag, interaction, nil, int64(1)}, 5)
		return body, proofAt, binding, nil
	}
	interaction, binding, err := encodeModelInteraction(request, model, defaults)
	if err != nil {
		return nil, 0, nil, err
	}
	body, proofAt, _ := marshalWAAArray([]any{streamFlag, int64(1), taskFlag, interaction, nil, int64(1)}, 5)
	return body, proofAt, binding, nil
}

// encodeModelInteraction 编码 field 79 kind == 2 模型目标的 Interaction 正文（写入 field 18，保留多轮历史，仅视频输出聊天模型发送 field 54 与 config[24]）
func encodeModelInteraction(request GenerateRequest, model Model, defaults GenerationDefaults) ([]any, []Content, error) {
	if request.AgentConfig != nil {
		return nil, nil, fmt.Errorf("模型 %s 不接受 agent_config", request.Model)
	}
	contents := request.Contents
	if request.PreviousInteractionID != "" {
		// 续接上游 interaction 时与官网 oLb 一样只发送最后一个模型轮之后的新输入与工具结果
		contents = AgentInput(request.Contents, true)
	}
	nativeTools := model.Capabilities["function_declarations"]
	steps, binding, hasModelTurn, err := encodeInteractionSteps(contents, nativeTools)
	if err != nil {
		return nil, nil, err
	}
	level, err := interactionThinkingLevel(request.Config, defaults)
	if err != nil {
		return nil, nil, err
	}
	var maxOutput int64
	if defaults.MaxOutputTokens > 0 || request.Config.MaxOutputTokens != nil {
		maxOutput = defaults.MaxOutputTokens
		if request.Config.MaxOutputTokens != nil {
			maxOutput = *request.Config.MaxOutputTokens
		}
		if defaults.MaxOutputTokens > 0 && maxOutput > defaults.MaxOutputTokens {
			maxOutput = defaults.MaxOutputTokens
		}
		if maxOutput <= 0 {
			return nil, nil, fmt.Errorf("max output tokens %d 超出模型范围 1-%d", maxOutput, defaults.MaxOutputTokens)
		}
	}
	isVideo := defaults.VideoOutput || defaults.VideoResolution != "" || (model.Capabilities["secondary_capability_code_14"] && model.Capabilities["chat_model"])
	var video []any
	var task int64
	if isVideo {
		video, task, err = interactionVideoFormat(request.Config.VideoConfig, defaults.VideoResolution)
		if err != nil {
			return nil, nil, err
		}
	}
	// GenerationConfig (JZa)：按官网 sLb/NMb 门控温度、topP、seed、思考档位、思考摘要、输出上限、图像与视频任务
	config := make([]any, 25)
	if model.Capabilities["capability_code_62"] {
		if temperature := cmp.Or(request.Config.Temperature, defaults.Temperature); temperature != nil {
			if *temperature < 0 || *temperature > 2 {
				return nil, nil, fmt.Errorf("temperature 必须在 0 到 2 之间")
			}
			config[0] = *temperature
		}
	}
	if model.Capabilities["capability_code_23"] {
		if topP := cmp.Or(request.Config.TopP, defaults.TopP); topP != nil {
			if *topP < 0 || *topP > 1 {
				return nil, nil, fmt.Errorf("top_p 必须在 0 到 1 之间")
			}
			config[1] = *topP
		}
	}
	if request.Config.Seed != nil {
		config[2] = *request.Config.Seed
	}
	if level != 0 {
		config[5] = level
	}
	if defaults.Thinking || defaults.ThinkingLevel || len(defaults.ThinkingLevels) > 0 {
		config[6] = int64(1)
	}
	if maxOutput > 0 {
		config[7] = maxOutput
	}
	if image := request.Config.ImageConfig; image != nil {
		aspect := ""
		if model.Capabilities["aspect_ratio"] && !isVideo {
			aspect = strings.TrimSpace(image.AspectRatio)
		}
		resolution := ""
		if model.Capabilities["output_resolution"] {
			resolution = strings.TrimSpace(image.ImageSize)
		}
		if aspect != "" || resolution != "" {
			config[12] = trimWireNil([]any{emptyNil(aspect), emptyNil(resolution)})
		}
	}
	if isVideo && (!hasModelTurn || task != 0) {
		config[24] = []any{}
		if task != 0 {
			config[24] = []any{task}
		}
	}
	config = trimWireNil(config)

	system := request.System
	responseMIME := strings.TrimSpace(request.Config.ResponseMIMEType)
	if schema := request.Config.responseSchema(); len(schema) > 0 {
		if _, err := encodeResponseSchema(schema); err != nil {
			return nil, nil, fmt.Errorf("response schema: %w", err)
		}
		system = append(slices.Clone(system), describedSchema("", schema))
		if responseMIME == "" {
			responseMIME = "application/json"
		}
	}
	var tools []any
	if !isVideo && request.Tools.ToolConfig.Mode != "none" {
		tools, err = interactionTools(request.Tools, model, false)
		if err != nil {
			return nil, nil, err
		}
	}
	var modalities []int64
	if len(request.Config.ResponseModalities) > 0 {
		modalities, err = encodeResponseModalities(request.Config.ResponseModalities)
		if err != nil {
			return nil, nil, err
		}
	}

	interaction := make([]any, 54)
	if text := strings.TrimSpace(SystemText(system)); text != "" {
		interaction[6] = text
	}
	if len(tools) > 0 {
		interaction[7] = tools
	}
	if len(modalities) > 0 {
		interaction[13] = modalities
	}
	if responseMIME != "" {
		interaction[15] = responseMIME
	}
	if request.PreviousInteractionID != "" {
		interaction[16] = request.PreviousInteractionID
	}
	target := []any{wireModelName(request.Model)}
	if len(config) > 0 {
		target = append(target, config)
	}
	interaction[17] = target
	interaction[26] = []any{steps}
	// field 54 输出格式：官网仅对视频输出聊天模型（_.rl）且非编辑任务发送，其余模型截止到索引 26
	if isVideo && task != interactionVideoTasks["edit"] && interactionVideoFormatNonEmpty(video) {
		interaction[53] = []any{[]any{[]any{nil, nil, nil, video}}}
	} else {
		interaction = interaction[:27]
	}
	return interaction, binding, nil
}

// encodeAgentInteraction 编码 field 79 kind == 1 agent 目标的 Interaction 正文（写入 field 19）
func encodeAgentInteraction(request GenerateRequest, model Model) ([]any, []Content, error) {
	agent := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	// kind 是该 agent 的 agent_config 类型：带默认模型的 agent 为 antigravity，其余为 deep-research
	kind := "deep-research"
	if model.AgentModel != "" {
		kind = "antigravity"
	}
	if config := request.AgentConfig; config != nil && config.Type != kind {
		return nil, nil, fmt.Errorf("agent_config.type %q 与 agent %s 不匹配，应为 %q", config.Type, agent, kind)
	}
	steps, binding, _, err := encodeInteractionSteps(AgentInput(request.Contents, request.PreviousInteractionID != ""), false)
	if err != nil {
		return nil, nil, err
	}
	tools, err := interactionTools(request.Tools, model, true)
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
	return interaction, binding, nil
}

// interactionVideoFormatNonEmpty 判断视频输出格式数组中是否至少有一项非空参数
func interactionVideoFormatNonEmpty(format []any) bool {
	for _, item := range format {
		if item != nil {
			return true
		}
	}
	return false
}

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

// encodeInteractionSteps 把规范消息编码为 Interaction input steps（field 1 user、field 2 model、nativeTools 时支持 field 4 tool_call 与 field 5 tool_result），并返回参与 WAA 绑定的内容
func encodeInteractionSteps(contents []Content, nativeTools bool) ([]any, []Content, bool, error) {
	if !nativeTools {
		var err error
		contents, err = transcribeFunctionParts(contents)
		if err != nil {
			return nil, nil, false, fmt.Errorf("Interaction 模型的 %w", err)
		}
	}
	callNames := make(map[string]string)
	if nativeTools {
		for _, content := range contents {
			for _, part := range content.Parts {
				if part.FunctionCall != nil && part.FunctionCall.ID != "" {
					callNames[part.FunctionCall.ID] = part.FunctionCall.Name
				}
			}
		}
	}
	wire := make([]any, 0, len(contents))
	binding := make([]Content, 0, len(contents))
	hasModelTurn := false
	var currentRole bool
	var currentParts []any
	var currentHasStep bool
	flushText := func() {
		if !currentHasStep || len(currentParts) == 0 {
			currentHasStep = false
			currentParts = nil
			return
		}
		if currentRole {
			wire = append(wire, []any{nil, []any{currentParts}})
			hasModelTurn = true
		} else {
			wire = append(wire, []any{[]any{currentParts}})
		}
		currentHasStep = false
		currentParts = nil
	}
	appendTextPart := func(model bool, part any) {
		if currentHasStep && currentRole != model {
			flushText()
		}
		currentRole = model
		currentHasStep = true
		currentParts = append(currentParts, part)
	}
	var carried []any
	var carriedBinding []Part
	// callSeq 为不带 ID 的调用生成序号，pendingCalls 与 pendingCodes 是尚未收到结果的函数调用与代码执行
	callSeq := 0
	type pendingCall struct{ id, name string }
	var pendingCalls []pendingCall
	var pendingCodes []string
	for index, content := range contents {
		if content.Role != RoleUser && content.Role != RoleAssistant && content.Role != RoleTool {
			return nil, nil, false, fmt.Errorf("Interaction 模型不接受 %s 消息", content.Role)
		}
		model := content.Role == RoleAssistant
		bindingParts := make([]Part, 0, len(content.Parts))
		if !model && len(carried) > 0 {
			for _, item := range carried {
				appendTextPart(false, item)
			}
			bindingParts = append(bindingParts, carriedBinding...)
			carried, carriedBinding = nil, nil
		}
		// signature 是本轮紧邻函数调用之前、只带思考签名的 Part 的签名，signed 表示本轮已写出签名步骤
		signature, signed := "", false
		for _, part := range content.Parts {
			if part.Text == "" && part.ThoughtSignature != "" && part.InlineData == nil && part.File == nil && part.FunctionCall == nil && part.FunctionResult == nil &&
				part.ExecutableCode == nil && part.CodeExecutionResult == nil && part.ToolCall == nil && part.ToolResponse == nil {
				signature = part.ThoughtSignature
				continue
			}
			if part.Thought {
				continue
			}
			pending := signature
			signature = ""
			switch {
			case part.VideoMetadata != nil || part.MediaProcessing != "":
				return nil, nil, false, fmt.Errorf("Interaction 模型的 contents[%d] 不接受视频截取、采样、处理方式与处理步骤", index)
			case !nativeTools && (part.ToolCall != nil || part.ToolResponse != nil):
				step := part.ToolCall
				if step == nil {
					step = part.ToolResponse
				}
				return nil, nil, false, fmt.Errorf("Interaction 模型的 contents[%d] 不接受服务端工具步骤 %s", index, step.Type)
			case nativeTools && part.FunctionCall != nil:
				flushText()
				// 函数调用历史之前写出思考步骤签名（Ww field 3 MZa field 1），本轮第一个调用没有签名时写跳过校验的占位签名
				sig := cmp.Or(part.ThoughtSignature, part.FunctionCall.ThoughtSignature, pending)
				if sig == "" && !signed {
					sig = "skip_thought_signature_validator"
				}
				if sig != "" {
					wire = append(wire, []any{nil, nil, []any{sig}})
					signed = true
				}
				id := part.FunctionCall.ID
				if id == "" {
					callSeq++
					id = fmt.Sprintf("call_%d", callSeq)
				}
				pendingCalls = append(pendingCalls, pendingCall{id: id, name: part.FunctionCall.Name})
				step, err := encodeInteractionFunctionCall(part.FunctionCall, id)
				if err != nil {
					return nil, nil, false, fmt.Errorf("Interaction 模型的 contents[%d]: %w", index, err)
				}
				wire = append(wire, step)
				hasModelTurn = true
			case nativeTools && part.FunctionResult != nil:
				flushText()
				// 结果按显式 ID、同名的待完成调用或唯一的待完成调用沿用调用的 ID
				id := part.FunctionResult.ID
				match := slices.IndexFunc(pendingCalls, func(call pendingCall) bool {
					return id != "" && call.id == id || id == "" && call.name == part.FunctionResult.Name
				})
				if match < 0 && id == "" && len(pendingCalls) == 1 {
					match = 0
				}
				name := part.FunctionResult.Name
				if match >= 0 {
					id, name = pendingCalls[match].id, cmp.Or(name, pendingCalls[match].name)
					pendingCalls = slices.Delete(pendingCalls, match, match+1)
				}
				if id == "" {
					callSeq++
					id = fmt.Sprintf("call_%d", callSeq)
				}
				step, err := encodeInteractionFunctionResult(part.FunctionResult, id, cmp.Or(name, callNames[id]))
				if err != nil {
					return nil, nil, false, fmt.Errorf("Interaction 模型的 contents[%d]: %w", index, err)
				}
				wire = append(wire, step)
			case nativeTools && part.ExecutableCode != nil:
				flushText()
				id := part.ExecutableCode.ID
				if id == "" {
					callSeq++
					id = fmt.Sprintf("code_%d", callSeq)
				}
				pendingCodes = append(pendingCodes, id)
				wire = append(wire, encodeInteractionExecutableCode(part.ExecutableCode, id))
				hasModelTurn = true
			case nativeTools && part.CodeExecutionResult != nil:
				flushText()
				id := part.CodeExecutionResult.ID
				if match := slices.Index(pendingCodes, id); match >= 0 {
					pendingCodes = slices.Delete(pendingCodes, match, match+1)
				} else if id == "" && len(pendingCodes) > 0 {
					id, pendingCodes = pendingCodes[0], pendingCodes[1:]
				}
				if id == "" {
					callSeq++
					id = fmt.Sprintf("code_%d", callSeq)
				}
				wire = append(wire, encodeInteractionCodeResult(part.CodeExecutionResult, id))
			case nativeTools && part.ToolCall != nil && part.ToolCall.Type == "GOOGLE_SEARCH_WEB":
				flushText()
				wire = append(wire, encodeInteractionServerToolCall(part.ToolCall))
				hasModelTurn = true
			case nativeTools && part.ToolResponse != nil && part.ToolResponse.Type == "GOOGLE_SEARCH_WEB":
				flushText()
				wire = append(wire, encodeInteractionServerToolResponse(part.ToolResponse))
			case part.ExecutableCode != nil || part.CodeExecutionResult != nil || part.ToolCall != nil || part.ToolResponse != nil:
				// 官网现场确认不了写法的服务端工具步骤与不接受原生工具的模型的代码执行转录为文本
				transcript, err := ToolPartTranscript(part)
				if err != nil {
					return nil, nil, false, fmt.Errorf("Interaction 模型的 contents[%d]: %w", index, err)
				}
				appendTextPart(model, []any{[]any{transcript}})
				bindingParts = append(bindingParts, Part{Text: transcript})
			case part.Text != "" && part.InlineData == nil && part.File == nil:
				appendTextPart(model, []any{[]any{part.Text}})
				bindingParts = append(bindingParts, Part{Text: part.Text})
			case part.File != nil && strings.TrimSpace(part.File.ID) != "" && part.Text == "":
				// Content field 9 为 Drive 文件引用
				file := []any{nil, nil, nil, nil, nil, nil, nil, nil, []any{strings.TrimSpace(part.File.ID)}}
				if model {
					carried = append(carried, file)
				} else {
					appendTextPart(false, file)
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
					appendTextPart(false, media)
					bindingParts = append(bindingParts, Part{InlineData: part.InlineData})
				}
			default:
				return nil, nil, false, fmt.Errorf("Interaction 模型的 contents[%d] 只接受文本、附件与工具 part", index)
			}
		}
		if len(bindingParts) > 0 {
			binding = append(binding, Content{Role: content.Role, Parts: bindingParts})
		}
	}
	flushText()
	if len(wire) == 0 {
		return nil, nil, false, fmt.Errorf("CreateInteractionStream contents 不能为空")
	}
	return wire, binding, hasModelTurn, nil
}

// encodeInteractionFunctionCall 按官网 $qb 编码函数调用为 Ww field 4 (_.a_a field 3)
func encodeInteractionFunctionCall(call *FunctionCall, id string) ([]any, error) {
	args := map[string]any{}
	if trimmed := strings.TrimSpace(string(call.Arguments)); trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
			return nil, fmt.Errorf("function call %q arguments: %w", call.Name, err)
		}
	}
	fn := []any{wireFunctionName(call.Name), encodeWireStruct(args)}
	return []any{nil, nil, nil, []any{id, nil, fn}}, nil
}

// encodeInteractionFunctionResult 按官网 $qb 编码函数结果为 Ww field 5 (_.Pw field 3 _.Gw)
func encodeInteractionFunctionResult(result *FunctionResult, id string, name string) ([]any, error) {
	if name != "" {
		name = wireFunctionName(name)
	}
	gw := make([]any, 12)
	if name != "" {
		gw[1] = name
	}
	trimmed := strings.TrimSpace(string(result.Content))
	if trimmed == "" {
		gw[9] = encodeWireStruct(map[string]any{})
	} else {
		var parsed any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			switch typed := parsed.(type) {
			case map[string]any:
				gw[9] = encodeWireStruct(typed)
			case string:
				gw[11] = typed
			default:
				gw[11] = trimmed
			}
		} else {
			gw[11] = trimmed
		}
	}
	return []any{nil, nil, nil, nil, []any{id, nil, trimWireNil(gw)}}, nil
}

// encodeInteractionExecutableCode 按官网 $qb 编码代码执行为 Ww field 4 (_.a_a field 4)
func encodeInteractionExecutableCode(code *ExecutableCode, id string) []any {
	lang := int64(0)
	if strings.EqualFold(code.Language, "python") {
		lang = 1
	}
	return []any{nil, nil, nil, []any{id, nil, nil, []any{[]any{lang, code.Code}}}}
}

// encodeInteractionCodeResult 按官网 $qb 编码代码执行结果为 Ww field 5 (_.Pw field 4)
func encodeInteractionCodeResult(result *CodeExecutionResult, id string) []any {
	var isError any
	if result.Outcome != "" && result.Outcome != "OUTCOME_OK" {
		isError = true
	}
	return []any{nil, nil, nil, nil, []any{id, nil, nil, trimWireNil([]any{result.Output, isError})}}
}

// encodeInteractionServerToolCall 按官网 $qb 编码 Google Search 服务端工具调用步骤
func encodeInteractionServerToolCall(step *ServerToolStep) []any {
	var data struct {
		Queries []string `json:"queries"`
	}
	_ = json.Unmarshal(step.Data, &data)
	queries := make([]any, 0, len(data.Queries))
	for _, query := range data.Queries {
		queries = append(queries, query)
	}
	return []any{nil, nil, nil, []any{step.ID, nil, nil, nil, nil, nil, []any{[]any{queries}}}}
}

// encodeInteractionServerToolResponse 按官网 $qb 编码 Google Search 服务端工具结果步骤
func encodeInteractionServerToolResponse(step *ServerToolStep) []any {
	var data struct {
		SearchSuggestions string `json:"search_suggestions"`
		Results           []struct {
			SearchSuggestions string `json:"search_suggestions"`
		} `json:"results"`
	}
	_ = json.Unmarshal(step.Data, &data)
	entries := make([]any, 0, len(data.Results)+1)
	if data.SearchSuggestions != "" {
		entries = append(entries, []any{nil, nil, nil, nil, data.SearchSuggestions})
	}
	for _, item := range data.Results {
		entries = append(entries, []any{nil, nil, nil, nil, item.SearchSuggestions})
	}
	return []any{nil, nil, nil, nil, []any{step.ID, nil, nil, nil, nil, []any{entries}}}
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
	case "xhigh", "extra_high", "max":
		level = 5
	default:
		return 0, fmt.Errorf("reasoning effort 必须是 none、minimal、low、medium、high、xhigh、extra_high 或 max")
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
func (c *Client) generateInteraction(ctx context.Context, request GenerateRequest, entry modelEntry, route InteractionRouting, apiKey string, restoring bool) (<-chan Event, error) {
	reason := ""
	if request.Unary {
		reason = "该模型使用 CreateInteractionStream，收集完成后返回"
	}
	reportUpstreamMode(ctx, "CreateInteractionStream", "stream", 0, reason)
	body, proofAt, binding, err := encodeInteractionRequest(request, entry.model, entry.defaults, route)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	rpc := newRPCRequest("CreateInteractionStream", request.AccountID, request.ID, body, true)
	rpc.ProofAt = proofAt
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
	return c.interactionEvents(ctx, request, entry, "CreateInteractionStream", response, nil, apiKey, restoring)
}

// interactionTask 是 CreateInteraction 后台任务的上游 interaction ID、所属账户、是否为 agent 目标与取消任务的函数
type interactionTask struct {
	id      string
	account string
	agent   bool
	cancel  func()
}

// interactionEvents 解码 Interaction 流并映射为规范事件；task 为后台 agent 任务时结束事件带上 interaction ID 与账户，流在读到最终 interaction 之前因调用方取消而结束时取消任务
func (c *Client) interactionEvents(ctx context.Context, request GenerateRequest, entry modelEntry, method string, response *RPCResponse, task *interactionTask, apiKey string, restoring bool) (<-chan Event, error) {
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
		var held []Event
		var turn modelTurn
		writeEvent := func(event Event) error {
			if !committed {
				committed = true
				ready <- nil
			}
			event.ProviderModel = entry.model.ID
			if event.Kind == EventFinish && task != nil && task.agent {
				event.InteractionID, event.AccountID = task.id, task.account
			}
			select {
			case events <- event:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		releaseHeld := func() error {
			for _, event := range held {
				if err := writeEvent(event); err != nil {
					return err
				}
			}
			held = nil
			return nil
		}
		send := func(event Event) error {
			hiddenTool := serverToolHidden(request, event)
			if hiddenTool {
				if event.ThoughtSignature == "" {
					return nil
				}
				event = Event{Kind: EventThoughtSignature, Part: Part{ThoughtSignature: event.ThoughtSignature}}
			}
			if restoring && event.HasContent() {
				turn.parts = append(turn.parts, clonePart(event.Part))
			}
			if request.Config.HideThinking && event.Kind == EventReasoning {
				if event.ThoughtSignature == "" {
					return nil
				}
				event.Kind, event.Text = EventThoughtSignature, ""
			}
			if !committed && event.Kind == EventThoughtSignature {
				held = append(held, event)
				return nil
			}
			if len(held) > 0 {
				if err := releaseHeld(); err != nil {
					return err
				}
			}
			return writeEvent(event)
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
		emit = restoreFunctionNames(request.Tools.Functions, emit)
		var err error
		defer func() {
			if !finished && task != nil && (ctx.Err() != nil || err != nil) {
				task.cancel()
			}
		}()
		err = DecodeInteractionStream(method, observeStreamActivity(ctx, response.Body), emit)
		if errors.Is(err, errStopSequenceMatched) {
			_ = response.Body.Close()
			if len(held) > 0 {
				_ = releaseHeld()
			}
			finishAtStopSequence(ctx, request, output, stopTokenCount, matchedStopSequence, writeEvent)
			err = ctx.Err()
		} else if closeErr := response.Body.Close(); err == nil && ctx.Err() == nil {
			err = closeErr
		}
		if err == nil && finished && restoring && c.turns != nil {
			c.turns.store(apiKey, request.turnChain.tip, turn)
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
		_ = writeEvent(Event{Kind: EventError, Err: err})
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

// interactionStepState 保存流式解码中单个步骤的类型与增量缓冲
type interactionStepState struct {
	kind     string
	toolType string
	callID   string
	funcName string
	argsMap  map[string]any
	argsJSON strings.Builder
	codeLang string
	codeText strings.Builder
	queries  []string
	emitted  bool
}

// interactionStreamDecoder 维护 Interaction 流式解码的步骤与签名状态
type interactionStreamDecoder struct {
	method           string
	steps            map[int64]*interactionStepState
	callSteps        map[string]*interactionStepState
	pendingSignature string
}

// flushSignature 把暂存的思考签名作为独立 EventThoughtSignature 发出
func (d *interactionStreamDecoder) flushSignature(emit func(Event) error) error {
	if d.pendingSignature == "" {
		return nil
	}
	sig := d.pendingSignature
	d.pendingSignature = ""
	return emit(Event{Kind: EventThoughtSignature, ThoughtSignature: sig})
}

// step 返回指定索引的步骤状态，不存在时按默认空步骤初始化
func (d *interactionStreamDecoder) step(index int64) *interactionStepState {
	step, ok := d.steps[index]
	if !ok {
		step = &interactionStepState{}
		d.steps[index] = step
	}
	return step
}

// DecodeInteractionStream 按网络顺序解码 CreateInteractionStream 或 GetInteractionStream 的事件与流尾状态
func DecodeInteractionStream(method string, source io.Reader, emit func(Event) error) error {
	reader := &streamReadRecorder{source: source}
	decoder := json.NewDecoder(newSparseJSONReader(reader))
	decoder.UseNumber()
	state := &interactionStreamDecoder{
		method:    method,
		steps:     make(map[int64]*interactionStepState),
		callSteps: make(map[string]*interactionStepState),
	}
	index := 0
	fail := func(path string, detail string) error {
		return reader.failure(method, index, &ProtocolEvidenceError{Method: method, Path: path, Detail: detail})
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return fail("$", "根值不是数组")
	}
	if !decoder.More() {
		return fail("$", "根数组缺少事件列表")
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return fail("$[0]", "事件列表不是数组")
	}
	finished := false
	for ; decoder.More(); index++ {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return fail(fmt.Sprintf("$[0][%d]", index), err.Error())
		}
		done, err := state.decodeEvent(raw, emit)
		if err != nil {
			return err
		}
		finished = finished || done
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		return fail("$[0]", "事件列表没有正常结束")
	}
	trailing := make([]json.RawMessage, 0, 3)
	for decoder.More() {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return fail("$", err.Error())
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

// decodeEvent 解码单个流事件（step_start、content_delta、step_end 与最终 interaction），返回是否为最终 interaction
func (d *interactionStreamDecoder) decodeEvent(raw json.RawMessage, emit func(Event) error) (bool, error) {
	event, err := rawArray(raw, "$event", raw)
	if err != nil {
		return false, withMethod(err, d.method)
	}
	if start := rawAt(event, 9); !isJSONNull(start) {
		if err := d.decodeStepStart(start, raw, emit); err != nil {
			return false, err
		}
	}
	if delta := rawAt(event, 10); !isJSONNull(delta) {
		if err := d.decodeDelta(delta, raw, emit); err != nil {
			return false, err
		}
	}
	if end := rawAt(event, 11); !isJSONNull(end) {
		if err := d.decodeStepEnd(end, raw, emit); err != nil {
			return false, err
		}
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
		return false, &ProtocolEvidenceError{Method: d.method, Path: "$event[19]", Detail: "最终 interaction 为空", Raw: cloneRaw(raw)}
	}
	interaction, err := rawArray(wrapper[0], "$event[19][0]", raw)
	if err != nil {
		return false, withMethod(err, d.method)
	}
	status, err := optionalIntField(interaction, 1, "$event[19][0]", raw)
	if err != nil {
		return false, withMethod(err, d.method)
	}
	finishReason := "stop"
	switch status {
	case 2, 3:
	case 6:
		// 状态 6（Incomplete）对应输出上限截断，统一映射为规范终态 max_tokens
		finishReason = "max_tokens"
	case 4:
		message := decodeInteractionErrors(rawAt(interaction, 27))
		if message == "" {
			message = "interaction failed"
		}
		return false, &RPCError{Method: d.method, StatusCode: http.StatusBadGateway, Message: message}
	case 5:
		return false, &RPCError{Method: d.method, StatusCode: http.StatusBadGateway, Message: "interaction cancelled"}
	default:
		return false, &ProtocolEvidenceError{Method: d.method, Path: "$event[19][0][1]", Detail: fmt.Sprintf("未识别的最终状态 %d", status), Raw: cloneRaw(raw)}
	}
	if err := d.flushSignature(emit); err != nil {
		return false, err
	}
	if usage := rawAt(interaction, 12); !isJSONNull(usage) {
		decoded, err := decodeInteractionUsage(d.method, usage, raw)
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
			return false, withMethod(err, d.method)
		}
	}
	return true, emit(Event{Kind: EventFinish, FinishReason: finishReason, EnvironmentID: environmentID})
}

// decodeInteractionErrors 提取 interaction field 28 (_.ex) 中的错误码与消息
func decodeInteractionErrors(raw json.RawMessage) string {
	if isJSONNull(raw) {
		return ""
	}
	items, err := rawArray(raw, "$errors", raw)
	if err != nil {
		return ""
	}
	var messages []string
	for _, item := range items {
		fields, err := rawArray(item, "$errors[]", raw)
		if err != nil {
			continue
		}
		code, _ := optionalStringField(fields, 0, "$errors[]", raw)
		msg, _ := optionalStringField(fields, 1, "$errors[]", raw)
		switch {
		case msg != "" && code != "":
			messages = append(messages, code+": "+msg)
		case msg != "":
			messages = append(messages, msg)
		case code != "":
			messages = append(messages, code)
		}
	}
	return strings.Join(messages, "; ")
}

// decodeStepStart 解码 event[9] (_.C1a) 步骤开始：记录步骤类型与工具调用/结果初始信息
func (d *interactionStreamDecoder) decodeStepStart(raw json.RawMessage, evidence json.RawMessage, emit func(Event) error) error {
	fields, err := rawArray(raw, "$step_start", evidence)
	if err != nil {
		return withMethod(err, d.method)
	}
	stepIndex, err := optionalIntField(fields, 0, "$step_start", evidence)
	if err != nil {
		return withMethod(err, d.method)
	}
	stepMsg := rawAt(fields, 1)
	if isJSONNull(stepMsg) {
		return nil
	}
	stepWire, err := rawArray(stepMsg, "$step_start[1]", evidence)
	if err != nil {
		return withMethod(err, d.method)
	}
	state := &interactionStepState{}
	d.steps[stepIndex] = state
	switch {
	case !isJSONNull(rawAt(stepWire, 0)):
		state.kind = "user_input"
	case !isJSONNull(rawAt(stepWire, 1)):
		state.kind = "model_output"
	case !isJSONNull(rawAt(stepWire, 2)):
		state.kind = "thought"
	case !isJSONNull(rawAt(stepWire, 3)):
		state.kind = "tool_call"
		if err := d.decodeToolCallStep(state, rawAt(stepWire, 3), evidence); err != nil {
			return err
		}
		if state.callID != "" {
			d.callSteps[state.callID] = state
		}
	case !isJSONNull(rawAt(stepWire, 4)):
		state.kind = "tool_result"
		if err := d.decodeToolResultStep(state, rawAt(stepWire, 4), evidence, emit); err != nil {
			return err
		}
	}
	return nil
}

// decodeToolCallStep 解析 step_start 中 Ww field 4 (_.a_a) 工具调用元信息与初始参数
func (d *interactionStreamDecoder) decodeToolCallStep(state *interactionStepState, raw json.RawMessage, evidence json.RawMessage) error {
	call, err := rawArray(raw, "$tool_call", evidence)
	if err != nil {
		return withMethod(err, d.method)
	}
	state.callID, err = optionalStringField(call, 0, "$tool_call", evidence)
	if err != nil {
		return withMethod(err, d.method)
	}
	switch {
	case !isJSONNull(rawAt(call, 2)):
		// field 3 (UZa): 函数调用 [name, Struct(args)]
		state.toolType = "function"
		fn, err := rawArray(call[2], "$tool_call[2]", evidence)
		if err != nil {
			return withMethod(err, d.method)
		}
		state.funcName, err = optionalStringField(fn, 0, "$tool_call[2]", evidence)
		if err != nil {
			return withMethod(err, d.method)
		}
		if argsRaw := rawAt(fn, 1); !isJSONNull(argsRaw) {
			if decoded, err := decodeWireStructObject(argsRaw, "$tool_call[2][1]", evidence); err == nil {
				state.argsMap = decoded
			}
		}
	case !isJSONNull(rawAt(call, 3)):
		// field 4 (_.RZa): 代码执行 [[lang, code]]
		state.toolType = "code_execution"
		wrapper, err := rawArray(call[3], "$tool_call[3]", evidence)
		if err == nil && !isJSONNull(rawAt(wrapper, 0)) {
			if inner, err := rawArray(wrapper[0], "$tool_call[3][0]", evidence); err == nil {
				langCode, _ := optionalIntField(inner, 0, "$tool_call[3][0]", evidence)
				state.codeLang = interactionCodeLanguage(langCode)
				if code, _ := optionalStringField(inner, 1, "$tool_call[3][0]", evidence); code != "" {
					state.codeText.WriteString(code)
				}
			}
		}
	case !isJSONNull(rawAt(call, 4)):
		state.toolType = "url_context"
	case !isJSONNull(rawAt(call, 5)):
		// field 6 (ZZa): MCP 工具调用 [name, Struct(args)]
		state.toolType = "mcp"
		if mcp, err := rawArray(call[5], "$tool_call[5]", evidence); err == nil {
			state.funcName, _ = optionalStringField(mcp, 0, "$tool_call[5]", evidence)
			if argsRaw := rawAt(mcp, 1); !isJSONNull(argsRaw) {
				if decoded, err := decodeWireStructObject(argsRaw, "$tool_call[5][1]", evidence); err == nil {
					state.argsMap = decoded
				}
			}
		}
	case !isJSONNull(rawAt(call, 6)):
		// field 7 (_.YZa): Google 搜索 [[queries]]
		state.toolType = "google_search"
		if wrapper, err := rawArray(call[6], "$tool_call[6]", evidence); err == nil && !isJSONNull(rawAt(wrapper, 0)) {
			if inner, err := rawArray(wrapper[0], "$tool_call[6][0]", evidence); err == nil {
				if queries, err := stringSliceField(inner, 0, "$tool_call[6][0]", evidence); err == nil {
					state.queries = append(state.queries, queries...)
				}
			}
		}
	case !isJSONNull(rawAt(call, 7)):
		state.toolType = "file_search"
	case !isJSONNull(rawAt(call, 8)):
		state.toolType = "google_maps"
	}
	return nil
}

// decodeToolResultStep 解析 step_start 中 Ww field 5 (_.Pw) 工具结果
func (d *interactionStreamDecoder) decodeToolResultStep(state *interactionStepState, raw json.RawMessage, evidence json.RawMessage, emit func(Event) error) error {
	result, err := rawArray(raw, "$tool_result", evidence)
	if err != nil {
		return withMethod(err, d.method)
	}
	state.callID, err = optionalStringField(result, 0, "$tool_result", evidence)
	if err != nil {
		return withMethod(err, d.method)
	}
	if linked := d.callSteps[state.callID]; linked != nil {
		state.toolType = linked.toolType
		state.funcName = linked.funcName
	}
	switch {
	case !isJSONNull(rawAt(result, 3)):
		// field 4 (_.c_a): 代码执行结果 [output, isError]
		return d.emitCodeExecutionResult(state.callID, result[3], evidence, emit)
	case !isJSONNull(rawAt(result, 4)):
		// field 5 (l_a): URL 上下文结果 [[[url, status]...], isError]
		if fields, err := rawArray(result[4], "$tool_result[4]", evidence); err == nil {
			return d.emitURLContextResult(state.callID, rawAt(fields, 0), evidence, emit)
		}
	case !isJSONNull(rawAt(result, 5)):
		// field 6 (j_a): Google 搜索结果 [[[..., search_suggestions(4)]...], isError]
		if fields, err := rawArray(result[5], "$tool_result[5]", evidence); err == nil {
			return d.emitSearchResult(state.callID, rawAt(fields, 0), 4, evidence, emit)
		}
	case !isJSONNull(rawAt(result, 6)):
		// field 7 (_.k_a): MCP 结果 [name, nil, struct(2), str(3)]
		if fields, err := rawArray(result[6], "$tool_result[6]", evidence); err == nil {
			name, _ := optionalStringField(fields, 0, "$tool_result[6]", evidence)
			data := decodeToolPayload(rawAt(fields, 2), rawAt(fields, 3), evidence)
			return emit(Event{Kind: EventServerToolResponse, ToolResponse: &ServerToolStep{ID: state.callID, Type: "CUSTOM", Name: name, Data: data}})
		}
	}
	return nil
}

// decodeStepEnd 处理 event[11] (_.oy) 步骤结束：把缓冲的客户端函数调用或服务端工具调用提交为规范事件
func (d *interactionStreamDecoder) decodeStepEnd(raw json.RawMessage, evidence json.RawMessage, emit func(Event) error) error {
	fields, err := rawArray(raw, "$step_end", evidence)
	if err != nil {
		return withMethod(err, d.method)
	}
	stepIndex, err := optionalIntField(fields, 0, "$step_end", evidence)
	if err != nil {
		return withMethod(err, d.method)
	}
	state, ok := d.steps[stepIndex]
	if !ok || state.emitted || state.kind != "tool_call" {
		return nil
	}
	state.emitted = true
	switch state.toolType {
	case "function":
		args := json.RawMessage(`{}`)
		if rawArgs := strings.TrimSpace(state.argsJSON.String()); rawArgs != "" && json.Valid([]byte(rawArgs)) {
			args = json.RawMessage(rawArgs)
		} else if len(state.argsMap) > 0 {
			if encoded, err := json.Marshal(state.argsMap); err == nil {
				args = encoded
			}
		}
		sig := d.pendingSignature
		d.pendingSignature = ""
		call := &FunctionCall{ID: state.callID, Name: state.funcName, Arguments: args, ThoughtSignature: sig}
		return emit(Event{Kind: EventToolCall, FunctionCall: call, ThoughtSignature: sig})
	case "code_execution":
		if err := d.flushSignature(emit); err != nil {
			return err
		}
		lang := state.codeLang
		if lang == "" {
			lang = "PYTHON"
		}
		return emit(Event{Kind: EventExecutableCode, ExecutableCode: &ExecutableCode{ID: state.callID, Language: lang, Code: state.codeText.String()}})
	case "google_search":
		if err := d.flushSignature(emit); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"queries": state.queries})
		if err := emit(Event{Kind: EventServerToolCall, ToolCall: &ServerToolStep{ID: state.callID, Type: "GOOGLE_SEARCH_WEB", Data: payload}}); err != nil {
			return err
		}
		if len(state.queries) > 0 {
			return emit(Event{Kind: EventGrounding, Grounding: &GroundingMetadata{WebSearchQueries: slices.Clone(state.queries)}})
		}
	case "url_context":
		if err := d.flushSignature(emit); err != nil {
			return err
		}
		return emit(Event{Kind: EventServerToolCall, ToolCall: &ServerToolStep{ID: state.callID, Type: "URL_CONTEXT"}})
	case "google_maps":
		if err := d.flushSignature(emit); err != nil {
			return err
		}
		return emit(Event{Kind: EventServerToolCall, ToolCall: &ServerToolStep{ID: state.callID, Type: "GOOGLE_MAPS"}})
	case "file_search":
		if err := d.flushSignature(emit); err != nil {
			return err
		}
		return emit(Event{Kind: EventServerToolCall, ToolCall: &ServerToolStep{ID: state.callID, Type: "FILE_SEARCH"}})
	case "mcp":
		if err := d.flushSignature(emit); err != nil {
			return err
		}
		var data json.RawMessage
		if len(state.argsMap) > 0 {
			data, _ = json.Marshal(state.argsMap)
		}
		return emit(Event{Kind: EventServerToolCall, ToolCall: &ServerToolStep{ID: state.callID, Type: "CUSTOM", Name: state.funcName, Data: data}})
	}
	return nil
}

// decodeDelta 解码 content delta：field 1 文本、field 2-5 媒体、field 6 思考摘要、field 7 思考签名、field 10 引用、field 11-14 工具增量
func (d *interactionStreamDecoder) decodeDelta(raw json.RawMessage, evidence json.RawMessage, emit func(Event) error) error {
	delta, err := rawArray(raw, "$delta", evidence)
	if err != nil || len(delta) < 2 {
		return &ProtocolEvidenceError{Method: d.method, Path: "$delta", Detail: "content delta 字段不足", Raw: cloneRaw(raw)}
	}
	stepIndex, err := optionalIntField(delta, 0, "$delta", evidence)
	if err != nil {
		return withMethod(err, d.method)
	}
	state := d.step(stepIndex)
	content, err := rawArray(delta[1], "$delta[1]", evidence)
	if err != nil {
		return withMethod(err, d.method)
	}
	for index, value := range content {
		if isJSONNull(value) {
			continue
		}
		switch index {
		case 0:
			text, err := rawArray(value, "$delta[1][0]", evidence)
			if err != nil || len(text) == 0 {
				return &ProtocolEvidenceError{Method: d.method, Path: "$delta[1][0]", Detail: "文本 delta 无效", Raw: cloneRaw(raw)}
			}
			decoded, err := rawString(text[0], "$delta[1][0][0]", evidence)
			if err != nil {
				return withMethod(err, d.method)
			}
			if state.kind == "thought" {
				if err := emit(Event{Kind: EventReasoning, Text: decoded, Thought: true}); err != nil {
					return err
				}
			} else {
				if err := d.flushSignature(emit); err != nil {
					return err
				}
				if err := emit(Event{Kind: EventText, Text: decoded}); err != nil {
					return err
				}
			}
		case 1, 2, 3, 4:
			if err := d.flushSignature(emit); err != nil {
				return err
			}
			media, err := decodeInteractionMedia(d.method, value, index, evidence)
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
			// field 7 为思考签名 ["<base64>"]，暂存以便紧随的函数调用附带或在下个非函数事件前发出
			sig := ""
			if items, err := rawArray(value, "$delta[1][6]", evidence); err == nil && len(items) > 0 && !isJSONNull(items[0]) {
				sig, _ = rawString(items[0], "$delta[1][6][0]", evidence)
			}
			if sig != "" {
				if err := d.flushSignature(emit); err != nil {
					return err
				}
				d.pendingSignature = sig
			}
		case 9:
			if err := decodeInteractionCitations(d.method, value, evidence, emit); err != nil {
				return err
			}
		case 10:
			// field 11 (_.u1a): 函数调用参数 JSON 字符串增量，field 2 (idx 1) 为片段
			fields, err := rawArray(value, "$delta[1][10]", evidence)
			if err != nil {
				return withMethod(err, d.method)
			}
			if fragment, err := optionalStringField(fields, 1, "$delta[1][10]", evidence); err != nil {
				return withMethod(err, d.method)
			} else if fragment != "" {
				state.argsJSON.WriteString(fragment)
			}
		case 11:
			// field 12 (_.v1a): 服务端工具调用增量（oneof 2..8）
			if err := d.decodeServerToolCallDelta(state, value, evidence); err != nil {
				return err
			}
		case 12:
			// field 13 (_.w1a): 服务端工具结果增量（oneof 2..8）
			if err := d.decodeServerToolResultDelta(state, value, evidence, emit); err != nil {
				return err
			}
		case 13:
			// field 14 (_.Yx): 工具结果增量 [name(0), isError(1), ..., str(4), struct(5)]
			if err := d.decodeGenericToolResultDelta(state, value, evidence, emit); err != nil {
				return err
			}
		default:
			return &ProtocolEvidenceError{Method: d.method, Path: fmt.Sprintf("$delta[1][%d]", index), Detail: "未识别的 content delta 字段", Raw: cloneRaw(raw)}
		}
	}
	return nil
}

// decodeServerToolCallDelta 解码 content delta field 12 (_.v1a) 的服务端工具调用参数增量
func (d *interactionStreamDecoder) decodeServerToolCallDelta(state *interactionStepState, raw json.RawMessage, evidence json.RawMessage) error {
	fields, err := rawArray(raw, "$delta[1][11]", evidence)
	if err != nil {
		return withMethod(err, d.method)
	}
	switch {
	case !isJSONNull(rawAt(fields, 1)):
		// field 2 (_.Ox): [_.IAa = [lang(0), code(1)]]
		state.toolType = "code_execution"
		if ox, err := rawArray(fields[1], "$delta[1][11][1]", evidence); err == nil && !isJSONNull(rawAt(ox, 0)) {
			if ia, err := rawArray(ox[0], "$delta[1][11][1][0]", evidence); err == nil {
				if langCode, err := optionalIntField(ia, 0, "$delta[1][11][1][0]", evidence); err == nil && langCode != 0 {
					state.codeLang = interactionCodeLanguage(langCode)
				}
				if fragment, err := optionalStringField(ia, 1, "$delta[1][11][1][0]", evidence); err == nil && fragment != "" {
					state.codeText.WriteString(fragment)
				}
			}
		}
	case !isJSONNull(rawAt(fields, 2)):
		state.toolType = "url_context"
	case !isJSONNull(rawAt(fields, 3)):
		// field 4 (_.Rx): [_.NAa = [queries(0)]]
		state.toolType = "google_search"
		if rx, err := rawArray(fields[3], "$delta[1][11][3]", evidence); err == nil && !isJSONNull(rawAt(rx, 0)) {
			if na, err := rawArray(rx[0], "$delta[1][11][3][0]", evidence); err == nil {
				if queries, err := stringSliceField(na, 0, "$delta[1][11][3][0]", evidence); err == nil {
					for _, query := range queries {
						if !slices.Contains(state.queries, query) {
							state.queries = append(state.queries, query)
						}
					}
				}
			}
		}
	case !isJSONNull(rawAt(fields, 4)):
		state.toolType = "mcp"
	case !isJSONNull(rawAt(fields, 5)):
		state.toolType = "file_search"
	case !isJSONNull(rawAt(fields, 6)):
		state.toolType = "google_maps"
	}
	return nil
}

// decodeServerToolResultDelta 解码 content delta field 13 (_.w1a) 的服务端工具结果增量
func (d *interactionStreamDecoder) decodeServerToolResultDelta(state *interactionStepState, raw json.RawMessage, evidence json.RawMessage, emit func(Event) error) error {
	fields, err := rawArray(raw, "$delta[1][12]", evidence)
	if err != nil {
		return withMethod(err, d.method)
	}
	switch {
	case !isJSONNull(rawAt(fields, 1)):
		// field 2 (_.Wx): 代码执行结果 [output(0), isError(1)]
		return d.emitCodeExecutionResult(state.callID, fields[1], evidence, emit)
	case !isJSONNull(rawAt(fields, 2)):
		// field 3 (j1a): URL 上下文结果 [nil, items(1), isError(2)]
		if j1a, err := rawArray(fields[2], "$delta[1][12][2]", evidence); err == nil {
			return d.emitURLContextResult(state.callID, rawAt(j1a, 1), evidence, emit)
		}
	case !isJSONNull(rawAt(fields, 3)):
		// field 4 (h1a): Google 搜索结果 [nil, items(1), isError(2)]，search_suggestions 位于 field 4 (idx 3)
		if h1a, err := rawArray(fields[3], "$delta[1][12][3]", evidence); err == nil {
			return d.emitSearchResult(state.callID, rawAt(h1a, 1), 3, evidence, emit)
		}
	case !isJSONNull(rawAt(fields, 4)):
		// field 5 (_.i1a): MCP 工具结果 [name(0), isError(1), nil, nil, str(4), struct(5)]
		if i1a, err := rawArray(fields[4], "$delta[1][12][4]", evidence); err == nil {
			name, _ := optionalStringField(i1a, 0, "$delta[1][12][4]", evidence)
			data := decodeToolPayload(rawAt(i1a, 5), rawAt(i1a, 4), evidence)
			return emit(Event{Kind: EventServerToolResponse, ToolResponse: &ServerToolStep{ID: state.callID, Type: "CUSTOM", Name: name, Data: data}})
		}
	}
	return nil
}

// decodeGenericToolResultDelta 解码 content delta field 14 (_.Yx) 工具结果增量
func (d *interactionStreamDecoder) decodeGenericToolResultDelta(state *interactionStepState, raw json.RawMessage, evidence json.RawMessage, emit func(Event) error) error {
	fields, err := rawArray(raw, "$delta[1][13]", evidence)
	if err != nil {
		return withMethod(err, d.method)
	}
	name, _ := optionalStringField(fields, 0, "$delta[1][13]", evidence)
	if name == "" {
		name = state.funcName
	}
	data := decodeToolPayload(rawAt(fields, 5), rawAt(fields, 4), evidence)
	return emit(Event{Kind: EventServerToolResponse, ToolResponse: &ServerToolStep{ID: state.callID, Type: "CUSTOM", Name: name, Data: data}})
}

// emitCodeExecutionResult 把代码执行结果 [output, isError] 发为 EventCodeExecutionResult
func (d *interactionStreamDecoder) emitCodeExecutionResult(callID string, raw json.RawMessage, evidence json.RawMessage, emit func(Event) error) error {
	fields, err := rawArray(raw, "$code_result", evidence)
	if err != nil {
		return withMethod(err, d.method)
	}
	output, _ := optionalStringField(fields, 0, "$code_result", evidence)
	isErr := false
	if v := rawAt(fields, 1); !isJSONNull(v) {
		isErr, _ = rawBool(v, "$code_result[1]", evidence)
	}
	outcome := "OUTCOME_OK"
	if isErr {
		outcome = "OUTCOME_FAILED"
	}
	return emit(Event{Kind: EventCodeExecutionResult, CodeExecutionResult: &CodeExecutionResult{ID: callID, Outcome: outcome, Output: output}})
}

// emitURLContextResult 把 URL 上下文读取条目发为 EventServerToolResponse 与 EventGrounding
func (d *interactionStreamDecoder) emitURLContextResult(callID string, itemsRaw json.RawMessage, evidence json.RawMessage, emit func(Event) error) error {
	if isJSONNull(itemsRaw) {
		return nil
	}
	items, err := rawArray(itemsRaw, "$url_results", evidence)
	if err != nil {
		return nil
	}
	var urls []URLMetadata
	var metadata []map[string]any
	for _, item := range items {
		fields, err := rawArray(item, "$url_results[]", evidence)
		if err != nil {
			continue
		}
		url, _ := optionalStringField(fields, 0, "$url_results[]", evidence)
		statusCode, _ := optionalIntField(fields, 1, "$url_results[]", evidence)
		status := interactionURLStatus(statusCode)
		if url != "" {
			urls = append(urls, URLMetadata{RetrievedURL: url, Status: status})
			metadata = append(metadata, map[string]any{"retrieved_url": url, "url_retrieval_status": status})
		}
	}
	data, _ := json.Marshal(map[string]any{"url_metadata": metadata})
	if err := emit(Event{Kind: EventServerToolResponse, ToolResponse: &ServerToolStep{ID: callID, Type: "URL_CONTEXT", Data: data}}); err != nil {
		return err
	}
	if len(urls) > 0 {
		return emit(Event{Kind: EventGrounding, Grounding: &GroundingMetadata{URLContext: urls}})
	}
	return nil
}

// emitSearchResult 把 Google 搜索结果条目发为 EventServerToolResponse，含 search_suggestions 时附带 EventGrounding
func (d *interactionStreamDecoder) emitSearchResult(callID string, itemsRaw json.RawMessage, suggestionIndex int, evidence json.RawMessage, emit func(Event) error) error {
	if isJSONNull(itemsRaw) {
		return nil
	}
	items, err := rawArray(itemsRaw, "$search_results", evidence)
	if err != nil {
		return nil
	}
	var chunks []GroundingChunk
	var rendered string
	var results []map[string]any
	for _, item := range items {
		fields, err := rawArray(item, "$search_results[]", evidence)
		if err != nil {
			continue
		}
		entry := map[string]any{}
		if url, _ := optionalStringField(fields, 0, "$search_results[]", evidence); url != "" {
			title, _ := optionalStringField(fields, 1, "$search_results[]", evidence)
			chunks = append(chunks, GroundingChunk{Source: "web", URI: url, Title: title})
			entry["url"] = url
			if title != "" {
				entry["title"] = title
			}
		}
		if suggestion, _ := optionalStringField(fields, suggestionIndex, "$search_results[]", evidence); suggestion != "" {
			if rendered == "" {
				rendered = suggestion
			}
			entry["search_suggestions"] = suggestion
		}
		if len(entry) > 0 {
			results = append(results, entry)
		}
	}
	data, _ := json.Marshal(map[string]any{"results": results})
	if err := emit(Event{Kind: EventServerToolResponse, ToolResponse: &ServerToolStep{ID: callID, Type: "GOOGLE_SEARCH_WEB", Data: data}}); err != nil {
		return err
	}
	if len(chunks) > 0 || rendered != "" {
		grounding := &GroundingMetadata{Chunks: chunks}
		if rendered != "" {
			grounding.SearchEntryPoint = &SearchEntryPoint{RenderedContent: rendered}
		}
		return emit(Event{Kind: EventGrounding, Grounding: grounding})
	}
	return nil
}

// decodeToolPayload 从 Struct 槽位或字符串槽位提取工具结果 JSON
func decodeToolPayload(structRaw json.RawMessage, strRaw json.RawMessage, evidence json.RawMessage) json.RawMessage {
	if !isJSONNull(structRaw) {
		if decoded, err := decodeWireStruct(structRaw, "$tool_payload", evidence); err == nil {
			return decoded
		}
	}
	if !isJSONNull(strRaw) {
		if text, err := rawString(strRaw, "$tool_payload", evidence); err == nil {
			encoded, _ := json.Marshal(map[string]any{"result": text})
			return encoded
		}
	}
	return nil
}

// interactionCodeLanguage 把代码执行语言枚举码换算为规范名称
func interactionCodeLanguage(code int64) string {
	switch code {
	case 1:
		return "PYTHON"
	case 2:
		return "BASH"
	default:
		return "LANGUAGE_UNSPECIFIED"
	}
}

// interactionURLStatus 把 URL 上下文状态码换算为 Gemini API 枚举名
func interactionURLStatus(code int64) string {
	switch code {
	case 1:
		return "URL_RETRIEVAL_STATUS_SUCCESS"
	case 2:
		return "URL_RETRIEVAL_STATUS_ERROR"
	default:
		return "URL_RETRIEVAL_STATUS_UNSPECIFIED"
	}
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

// interactionTools 按官网 wLb 顺序编码 Interaction field 8 的工具列表：代码执行、函数声明、Google 搜索与网址读取；applyDefaults 为 true 时对未显式传 tools 的 agent 加入目录 field 82 默认开启的工具
func interactionTools(tools Tools, model Model, applyDefaults bool) ([]any, error) {
	subject := "Interaction 模型"
	if applyDefaults {
		subject = "agent"
	}
	switch {
	case len(tools.MCPServers) > 0:
		return nil, fmt.Errorf("%s 不接受 %s 工具", subject, "mcp_server")
	case len(tools.FileSearch) > 0:
		return nil, fmt.Errorf("%s 不接受 %s 工具", subject, "file_search")
	case tools.ComputerUse != nil:
		return nil, fmt.Errorf("%s 不接受 %s 工具", subject, "computer_use")
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
			return nil, fmt.Errorf("%s 不接受 %s 工具", subject, name)
		}
	}
	if applyDefaults && !tools.Explicit {
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
		name := function.Name
		if !applyDefaults {
			name = wireFunctionName(name)
		}
		declaration := []any{name, nil, nil}
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

// EncodeGetInteractionStreamRequest 编码按 interaction ID 读取后台任务增量与终态的请求
func EncodeGetInteractionStreamRequest(id string, model string) ([]byte, int, error) {
	return marshalWAAArray([]any{id, int64(1), nil, nil, nil, int64(1), wireModelName(model)}, 5)
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
func (c *Client) generateInteractionTask(ctx context.Context, request GenerateRequest, entry modelEntry, route InteractionRouting, apiKey string, restoring bool) (<-chan Event, error) {
	reason := ""
	if request.Unary {
		reason = "该模型使用 CreateInteraction 后台任务，收集完成后返回"
	}
	reportUpstreamMode(ctx, "GetInteractionStream", "stream", 0, reason)
	body, proofAt, binding, err := encodeInteractionRequest(request, entry.model, entry.defaults, route)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	rpc := newRPCRequest("CreateInteraction", request.AccountID, request.ID, body, false)
	rpc.ProofAt = proofAt
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
	streamBody, streamProofAt, err := EncodeGetInteractionStreamRequest(id, request.Model)
	if err != nil {
		return nil, err
	}
	stream := newRPCRequest("GetInteractionStream", request.AccountID, request.ID, streamBody, true)
	stream.ProofAt = streamProofAt
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
	return c.interactionEvents(ctx, request, entry, "GetInteractionStream", response, &interactionTask{id: id, account: accountID, agent: route.IsAgent(), cancel: cancel}, apiKey, restoring)
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
