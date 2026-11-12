package aistudio

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var errStopSequenceMatched = errors.New("stop sequence matched")

// continuationRoundTokens 为官网自动续写每轮增加的输出上限
const continuationRoundTokens = 32768

// regenerations 是上游以函数调用格式错误、意外的工具调用或响应格式异常结束、流中断或流尾 5xx 错误且客户端没有收到内容时重新生成的次数上限
const regenerations = 3

// tokenCountResult 保存并发输入计数结果
type tokenCountResult struct {
	count TokenCount
	err   error
}

// EncodeGenerateContentRequest 编码 GenerateContent 数组
func EncodeGenerateContentRequest(request GenerateRequest, defaults GenerationDefaults, runtime RequestContext) ([]byte, error) {
	tools, explicitTools, err := encodeRequestedTools(request.Tools)
	if err != nil {
		return nil, err
	}
	contents, err := encodeContents(request.Contents)
	if err != nil {
		return nil, err
	}
	if len(contents) == 0 {
		return nil, fmt.Errorf("GenerateContent contents 不能为空")
	}
	request.Config.MediaResolution = cmp.Or(request.Config.MediaResolution, partMediaResolution(request.Contents))
	config, err := encodeGenerationConfig(request.Config, defaults)
	if err != nil {
		return nil, err
	}
	if config[8] != nil && responseSchemaBeyondPlayground(request.Config, config[8].([]any)) {
		config[7], config[8] = "application/json", nil
		request.System = append(slices.Clone(request.System), describedSchema("", request.Config.responseSchema()))
	}
	if defaults.ContinuedOutputTokens > 0 {
		config = continueGenerationConfig(config, request.continuationRound)
	}
	serverSideTools := explicitTools && playgroundServerSteps(request.Tools, defaults.ImageRoute)
	if !explicitTools && defaults.OutputResolution {
		// 可设置分辨率模型的默认扩展工具字段
		tools, explicitTools = []any{[]any{nil, nil, nil, []any{nil, []any{}}}}, true
	}
	var retrieval []any
	if len(tools) > 0 {
		retrieval = encodeRetrievalConfig(request.Tools.ToolConfig.Retrieval, runtime.Timezone)
	}
	length := 11
	if request.turnToken != "" {
		length = 12
	}
	if retrieval != nil || serverSideTools {
		length = 14
	}
	if request.continuationToken != "" {
		length = 17
	}
	wire := make([]any, length)
	wire[0] = wireModelName(request.Model)
	wire[1] = contents
	safety, err := resolveSafetySettings(request.SafetySettings, defaults.ImageRoute)
	if err != nil {
		return nil, err
	}
	if len(safety) > 0 {
		wire[2] = encodeSafetySettings(safety)
	}
	wire[3] = config
	if len(request.System) > 0 {
		wire[5] = encodeSystemInstruction(request.System)
	}
	if explicitTools {
		wire[6] = tools
	}
	if defaults.ContinuedOutputTokens == 0 {
		wire[10] = int64(1)
	}
	if request.turnToken != "" {
		wire[11] = request.turnToken
	}
	if retrieval != nil || serverSideTools {
		toolConfig := []any{retrieval}
		if serverSideTools {
			// 客户端要求或内置工具与函数调用同时使用时开启 include_server_side_tool_invocations
			toolConfig = append(toolConfig, nil, true)
		}
		wire[13] = toolConfig
	}
	if request.continuationToken != "" {
		wire[16] = request.continuationToken
	}
	return json.Marshal(wire)
}

// encodeRetrievalConfig 编码 ToolConfig.retrieval_config：用户坐标、语言代码与时区，都未设置时为 nil
func encodeRetrievalConfig(config *RetrievalConfig, timezone string) []any {
	wire := []any{nil, nil, nil}
	if config != nil && config.Latitude != nil && config.Longitude != nil {
		wire[0] = []any{*config.Latitude, *config.Longitude}
	}
	if config != nil && config.LanguageCode != "" {
		wire[1] = config.LanguageCode
	}
	if timezone != "" {
		wire[2] = timezone
	}
	if wire[0] == nil && wire[1] == nil && wire[2] == nil {
		return nil
	}
	return wire
}

// continueGenerationConfig 为自动续写模型开启续写，并把输出总上限换算为第 round 轮的累计上限
func continueGenerationConfig(config []any, round int) []any {
	for len(config) < 46 {
		config = append(config, nil)
	}
	config[45] = int64(1)
	if total, ok := config[3].(int64); ok {
		config[3] = min(total, int64(round+1)*continuationRoundTokens)
	}
	return config
}

// responseSchema 返回请求的结构化输出约束，OpenAPI Schema 优先
func (config GenerationConfig) responseSchema() json.RawMessage {
	if len(config.ResponseSchema) > 0 {
		return config.ResponseSchema
	}
	return config.ResponseJSONSchema
}

// generationSettings 是按模型默认值校验并补齐后的有效生成配置，Playground 与 Build 各自由它直接编码
type generationSettings struct {
	maxOutput       *int64
	temperature     *float64
	topP            *float64
	topK            *int
	responseSchema  map[string]any
	modalities      []int64
	speech          []any
	transcription   []any
	mediaResolution any
	image           *ImageConfig
	thinking        *thinkingSettings
}

// thinkingSettings 是有效的思考设置：level 为支持思考档位的模型使用的档位，budget 为思考预算
type thinkingSettings struct {
	level  *int64
	budget *int64
}

// resolveGenerationSettings 校验生成配置并按模型默认值计算输出上限、采样参数、思考档位与预算和默认图片尺寸
func resolveGenerationSettings(config GenerationConfig, defaults GenerationDefaults) (generationSettings, error) {
	var settings generationSettings
	var err error
	if schema := config.responseSchema(); len(schema) > 0 {
		settings.responseSchema, err = encodeResponseSchema(schema)
		if err != nil {
			return settings, fmt.Errorf("response schema: %w", err)
		}
	}
	thinkingLevel := defaults.DefaultThinkingLevel
	effort := normalizedReasoningEffort(config.ReasoningEffort)
	hasReasoningEffort := effort != ""
	thinkingBudget := config.ThinkingBudget
	switch effort {
	case "":
	case "low":
		thinkingLevel = 1
	case "medium":
		thinkingLevel = 2
	case "high":
		thinkingLevel = 3
	case "minimal":
		thinkingLevel = 4
	case "none":
		thinkingLevel = 4
		if !defaults.ThinkingLevel && defaults.ThinkingBudget && thinkingBudget == nil {
			zero := int64(0)
			thinkingBudget = &zero
		}
	default:
		return settings, fmt.Errorf("reasoning effort 必须是 none、minimal、low、medium、high、xhigh 或 max")
	}
	if hasReasoningEffort && defaults.ThinkingLevel {
		thinkingLevel = closestSupportedThinkingLevel(thinkingLevel, defaults.ThinkingLevels)
	}
	if !defaults.ThinkingLevel {
		hasReasoningEffort = false
	}
	if thinkingBudget != nil && !defaults.ThinkingBudget {
		if defaults.ThinkingLevel && !hasReasoningEffort {
			thinkingLevel = closestSupportedThinkingLevel(thinkingLevelForBudget(*thinkingBudget), defaults.ThinkingLevels)
		}
		thinkingBudget = nil
	}
	if config.SpeechConfig == nil || config.MaxOutputTokens != nil {
		limit := max(defaults.MaxOutputTokens, defaults.ContinuedOutputTokens)
		if limit <= 0 {
			return settings, fmt.Errorf("模型目录缺少有效 output token limit")
		}
		maxOutput := limit
		if config.MaxOutputTokens != nil {
			if *config.MaxOutputTokens <= 0 {
				return settings, fmt.Errorf("max output tokens %d 超出模型范围 1-%d", *config.MaxOutputTokens, limit)
			}
			maxOutput = min(*config.MaxOutputTokens, limit)
		}
		settings.maxOutput = &maxOutput
	}
	settings.temperature = cmp.Or(config.Temperature, defaults.Temperature)
	if settings.temperature != nil && (*settings.temperature < 0 || *settings.temperature > 2) {
		return settings, fmt.Errorf("temperature 必须在 0 到 2 之间")
	}
	settings.topP = cmp.Or(config.TopP, defaults.TopP)
	if settings.topP != nil && (*settings.topP < 0 || *settings.topP > 1) {
		return settings, fmt.Errorf("top_p 必须在 0 到 1 之间")
	}
	settings.topK = cmp.Or(config.TopK, defaults.TopK)
	if settings.topK != nil && *settings.topK < 0 {
		return settings, fmt.Errorf("top_k 不能为负数")
	}
	if settings.modalities, err = encodeResponseModalities(config.ResponseModalities); err != nil {
		return settings, err
	}
	if image := encodeImageConfig(config.ImageConfig); image != nil {
		settings.image = &ImageConfig{AspectRatio: strings.TrimSpace(config.ImageConfig.AspectRatio), ImageSize: strings.TrimSpace(config.ImageConfig.ImageSize)}
	} else if defaults.OutputResolution {
		// 可设置分辨率模型的默认输出尺寸
		settings.image = &ImageConfig{ImageSize: "1K"}
	}
	if settings.speech, err = encodeSpeechConfig(config.SpeechConfig); err != nil {
		return settings, err
	}
	if settings.transcription, err = encodeTranscriptionConfig(config.TranscriptionConfig); err != nil {
		return settings, err
	}
	if settings.mediaResolution, err = encodeMediaResolution(config.MediaResolution); err != nil {
		return settings, err
	}
	if defaults.Thinking || defaults.ThinkingBudget || defaults.ThinkingLevel || thinkingBudget != nil || hasReasoningEffort {
		settings.thinking = &thinkingSettings{budget: thinkingBudget}
		if defaults.ThinkingLevel {
			settings.thinking.level = &thinkingLevel
		}
	}
	return settings, nil
}

// encodeGenerationConfig 把有效生成配置编码为 Playground GenerationConfig 数组
func encodeGenerationConfig(config GenerationConfig, defaults GenerationDefaults) ([]any, error) {
	settings, err := resolveGenerationSettings(config, defaults)
	if err != nil {
		return nil, err
	}
	image := encodeImageConfig(settings.image)
	length := 14
	if settings.modalities != nil {
		length = 15
	}
	if settings.speech != nil {
		length = 16
	}
	if settings.thinking != nil {
		length = max(length, 17)
	}
	if settings.mediaResolution != nil {
		length = max(length, 18)
	}
	if config.Seed != nil {
		length = max(length, 19)
	}
	if image != nil {
		length = 27
	}
	if settings.transcription != nil {
		length = 32
	}
	wire := make([]any, length)
	if len(config.StopSequences) > 0 {
		wire[1] = append([]string(nil), config.StopSequences...)
	}
	if settings.maxOutput != nil {
		wire[3] = *settings.maxOutput
	}
	if settings.temperature != nil {
		wire[4] = *settings.temperature
	}
	if settings.topP != nil {
		wire[5] = *settings.topP
	}
	if settings.topK != nil {
		wire[6] = *settings.topK
	}
	if config.ResponseMIMEType != "" {
		wire[7] = config.ResponseMIMEType
	}
	if settings.responseSchema != nil {
		wire[8] = playgroundSchema(settings.responseSchema)
	}
	if !defaults.ImageRoute {
		wire[13] = int64(1)
	}
	if settings.modalities != nil {
		wire[14] = settings.modalities
	}
	if settings.speech != nil {
		wire[15] = settings.speech
	}
	if thinking := settings.thinking; thinking != nil {
		value := []any{int64(1)}
		if thinking.level != nil {
			value = []any{int64(1), nil, nil, *thinking.level}
		}
		if thinking.budget != nil {
			if len(value) < 2 {
				value = append(value, nil)
			}
			value[1] = *thinking.budget
		}
		wire[16] = value
	}
	if settings.mediaResolution != nil {
		wire[17] = settings.mediaResolution
	}
	if config.Seed != nil {
		wire[18] = *config.Seed
	}
	if image != nil {
		wire[26] = image
	}
	if settings.transcription != nil {
		wire[31] = settings.transcription
	}
	return wire, nil
}

var thinkingLevelsByEffort = []int64{4, 1, 2, 3}

// normalizedReasoningEffort 把各协议的思考强度归一为 none、minimal、low、medium、high，xhigh 与 max 取 high，未设置时为空
func normalizedReasoningEffort(value string) string {
	effort := strings.ToLower(strings.TrimSpace(value))
	switch effort {
	case "thinking_level_unspecified":
		return ""
	case "xhigh", "max":
		return "high"
	}
	return effort
}

// ApplyModelSuffix 把模型名末尾的 (思考设置) 写入生成参数并返回去掉后缀的模型 ID，后缀优先于请求体中的思考参数
func ApplyModelSuffix(model string, config GenerationConfig) (string, GenerationConfig, error) {
	open := strings.LastIndex(model, "(")
	if open < 0 || !strings.HasSuffix(model, ")") {
		return model, config, nil
	}
	value := strings.ToLower(strings.TrimSpace(model[open+1 : len(model)-1]))
	model = strings.TrimSpace(model[:open])
	switch value {
	case "auto", "-1":
		config.ReasoningEffort, config.ThinkingBudget = "", nil
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		config.ReasoningEffort, config.ThinkingBudget = value, nil
	default:
		budget, err := strconv.ParseInt(value, 10, 64)
		if err != nil || budget < 0 {
			return model, config, fmt.Errorf("%w: 模型名后缀 (%s) 必须是 none、auto、minimal、low、medium、high、xhigh、max 或非负整数思考预算", ErrInvalidArgument, value)
		}
		config.ReasoningEffort, config.ThinkingBudget = "", &budget
	}
	return model, config, nil
}

// thinkingLevelForBudget 按 Gemini OpenAI 兼容层的 1024、8192、24576 档位把思考预算换算为 thinking level
func thinkingLevelForBudget(budget int64) int64 {
	switch {
	case budget <= 0:
		return 4
	case budget <= 1024:
		return 1
	case budget <= 8192:
		return 2
	default:
		return 3
	}
}

func closestSupportedThinkingLevel(requested int64, supported []int64) int64 {
	requestedRank := slices.Index(thinkingLevelsByEffort, requested)
	if requestedRank < 0 || len(supported) == 0 || slices.Contains(supported, requested) {
		return requested
	}
	for distance := 1; distance < len(thinkingLevelsByEffort); distance++ {
		lower := requestedRank - distance
		if lower >= 0 && slices.Contains(supported, thinkingLevelsByEffort[lower]) {
			return thinkingLevelsByEffort[lower]
		}
		upper := requestedRank + distance
		if upper < len(thinkingLevelsByEffort) && slices.Contains(supported, thinkingLevelsByEffort[upper]) {
			return thinkingLevelsByEffort[upper]
		}
	}
	return requested
}

func encodeResponseModalities(modalities []ResponseModality) ([]int64, error) {
	if modalities == nil {
		return nil, nil
	}
	hasText := false
	hasImage := false
	hasAudio := false
	for _, modality := range modalities {
		switch modality {
		case ResponseModalityText:
			hasText = true
		case ResponseModalityImage:
			hasImage = true
		case ResponseModalityAudio:
			hasAudio = true
		default:
			return nil, fmt.Errorf("response modality %q 不受支持", modality)
		}
	}
	if hasAudio && (hasText || hasImage) {
		return nil, fmt.Errorf("AUDIO 不能和其他 response modality 同时使用")
	}
	switch {
	case hasAudio:
		return []int64{3}, nil
	case hasImage && hasText:
		return []int64{2, 1}, nil
	case hasImage:
		return []int64{2}, nil
	case hasText:
		return []int64{1}, nil
	default:
		return []int64{}, nil
	}
}

func encodeImageConfig(config *ImageConfig) []any {
	if config == nil {
		return nil
	}
	aspectRatio := strings.TrimSpace(config.AspectRatio)
	imageSize := strings.TrimSpace(config.ImageSize)
	if aspectRatio == "" && imageSize == "" {
		return nil
	}
	if imageSize == "" {
		return []any{aspectRatio}
	}
	var aspect any
	if aspectRatio != "" {
		aspect = aspectRatio
	}
	return []any{aspect, imageSize}
}

func encodeSpeechConfig(config *SpeechConfig) ([]any, error) {
	if config == nil {
		return nil, nil
	}
	voice := encodeVoiceConfig(config.VoiceName, config.Voice)
	var wire []any
	if voice != nil {
		wire = []any{voice}
	}
	if config.LanguageCode != "" {
		wire = setWireField(wire, 1, config.LanguageCode)
	}
	if len(config.Speakers) > 0 {
		speakers := make([]any, 0, len(config.Speakers))
		for _, speaker := range config.Speakers {
			speakers = append(speakers, []any{strings.TrimSpace(speaker.Speaker), encodeVoiceConfig(speaker.VoiceName, speaker.Voice)})
		}
		multi := []any{nil, speakers}
		switch strings.ToUpper(strings.TrimSpace(config.Mode)) {
		case "":
		case "VERBATIM":
			multi = append(multi, int64(1))
		case "CONVERSATIONAL":
			multi = append(multi, int64(2))
		default:
			return nil, fmt.Errorf("speech config mode 必须是 VERBATIM 或 CONVERSATIONAL")
		}
		wire = setWireField(wire, 2, multi)
	} else if strings.TrimSpace(config.Mode) != "" {
		return nil, fmt.Errorf("speech config mode 只能用于 multi-speaker")
	}
	return wire, nil
}

// encodeVoiceConfig 编码 VoiceConfig：预置音色名写入 field 1，说话人标识写入 field 5，都为空时返回 nil
func encodeVoiceConfig(name, voice string) []any {
	if name = strings.TrimSpace(name); name != "" {
		return []any{[]any{name}}
	}
	if voice = strings.TrimSpace(voice); voice != "" {
		return []any{nil, nil, nil, nil, voice}
	}
	return nil
}

// mediaResolutions 为 Gemini API 输入媒体分辨率名与 generation config 字段 18 的编号
var mediaResolutions = map[string]int64{"MEDIA_RESOLUTION_LOW": 1, "MEDIA_RESOLUTION_MEDIUM": 2, "MEDIA_RESOLUTION_HIGH": 3, "MEDIA_RESOLUTION_ULTRA_HIGH": 4}

// encodeMediaResolution 校验输入媒体分辨率并返回 wire 编号，未设置时为 nil
func encodeMediaResolution(value string) (any, error) {
	name := strings.ToUpper(strings.TrimSpace(value))
	if name == "" || name == "MEDIA_RESOLUTION_UNSPECIFIED" {
		return nil, nil
	}
	code, ok := mediaResolutions[name]
	if !ok {
		return nil, fmt.Errorf("mediaResolution %q 不受支持", value)
	}
	return code, nil
}

// partMediaResolution 返回全部媒体 Part 共同指定的分辨率，各 Part 不一致或都未指定时为空
func partMediaResolution(contents []Content) string {
	level := ""
	for _, content := range contents {
		for _, part := range content.Parts {
			if part.MediaResolution == "" {
				continue
			}
			if level != "" && level != part.MediaResolution {
				return ""
			}
			level = part.MediaResolution
		}
	}
	return level
}

// mixedMediaResolution 表示 Part 级分辨率与请求级设置不能合并为一个请求级分辨率
func mixedMediaResolution(global string, contents []Content) bool {
	for _, content := range contents {
		for _, part := range content.Parts {
			if part.MediaResolution != "" && part.MediaResolution != cmp.Or(global, partMediaResolution(contents)) {
				return true
			}
		}
	}
	return false
}

// defaultSpeechVoice 是官网单说话人的默认声音
const defaultSpeechVoice = "Zephyr"

// applyModelMediaDefaults 按 EffectiveResponseModalities 设置输出模态，TTS 模型未设置声音时使用默认声音
func applyModelMediaDefaults(config GenerationConfig, model Model) GenerationConfig {
	config.ResponseModalities = EffectiveResponseModalities(config.ResponseModalities, model)
	if speech := config.SpeechConfig; model.Capabilities["speech_route"] && (speech == nil || strings.TrimSpace(speech.VoiceName) == "" && strings.TrimSpace(speech.Voice) == "" && len(speech.Speakers) == 0) {
		config.SpeechConfig = &SpeechConfig{VoiceName: defaultSpeechVoice}
		if speech != nil {
			config.SpeechConfig.LanguageCode = speech.LanguageCode
		}
	}
	return config
}

// EffectiveResponseModalities 返回对模型实际生效的输出模态：未指定时为媒体模型的默认模态，指定时去掉模型不能生成的模态，与模型能力没有交集时保持原样
func EffectiveResponseModalities(modalities []ResponseModality, model Model) []ResponseModality {
	supported := []ResponseModality{ResponseModalityText}
	switch {
	case model.Capabilities["speech_route"], model.Capabilities["music_route"]:
		supported = []ResponseModality{ResponseModalityAudio}
	case model.Capabilities["image_route"]:
		supported = []ResponseModality{ResponseModalityImage, ResponseModalityText}
	}
	if modalities == nil {
		if !slices.Equal(supported, []ResponseModality{ResponseModalityText}) {
			return supported
		}
		return nil
	}
	kept := make([]ResponseModality, 0, len(modalities))
	matched := false
	for _, modality := range modalities {
		switch name := ResponseModality(strings.ToUpper(strings.TrimSpace(string(modality)))); {
		case slices.Contains(supported, name):
			matched = true
		case name == ResponseModalityText || name == ResponseModalityImage || name == ResponseModalityAudio:
			continue
		}
		kept = append(kept, modality)
	}
	if matched {
		return kept
	}
	return modalities
}

func applySpeechTranscript(contents []Content, model Model, config GenerationConfig) []Content {
	if !model.Capabilities["speech_route"] || config.SpeechConfig == nil {
		return contents
	}
	if model.Capabilities["speech_metadata"] {
		return splitSpeakerSegments(contents, config.SpeechConfig.Speakers)
	}
	result := foldSpeechMetadata(contents)
	for contentIndex, content := range result {
		parts := append([]Part(nil), content.Parts...)
		for partIndex, part := range parts {
			text := strings.TrimSpace(part.Text)
			if text == "" || strings.HasPrefix(text, "## Transcript:") {
				continue
			}
			parts[partIndex].Text = "## Transcript:\n" + text
			result[contentIndex].Parts = parts
			return result
		}
	}
	return result
}

// foldSpeechMetadata 把分段说话人与风格写回旧 TTS 模型的台词文本
func foldSpeechMetadata(contents []Content) []Content {
	result := append([]Content(nil), contents...)
	for contentIndex, content := range result {
		if !slices.ContainsFunc(content.Parts, func(part Part) bool { return part.SpeechMetadata != nil }) {
			continue
		}
		parts := append([]Part(nil), content.Parts...)
		for index, part := range parts {
			if part.SpeechMetadata == nil {
				continue
			}
			if part.SpeechMetadata.Speaker != "" {
				part.Text = part.SpeechMetadata.Speaker + ": " + part.Text
			}
			if part.SpeechMetadata.Style != "" {
				part.Text = part.SpeechMetadata.Style + "\n\n" + part.Text
			}
			part.SpeechMetadata = nil
			parts[index] = part
		}
		result[contentIndex].Parts = parts
	}
	return result
}

// splitSpeakerSegments 把 "说话人: 台词" 文本按多说话人配置拆成带 SpeechMetadata 的分段
func splitSpeakerSegments(contents []Content, speakers []SpeakerVoiceConfig) []Content {
	if len(speakers) == 0 {
		return contents
	}
	names := make([]string, 0, len(speakers))
	for _, speaker := range speakers {
		names = append(names, regexp.QuoteMeta(strings.TrimSpace(speaker.Speaker)))
	}
	pattern := regexp.MustCompile(`^\s*(` + strings.Join(names, "|") + `)\s*:\s*(.*)$`)
	result := append([]Content(nil), contents...)
	for contentIndex, content := range result {
		parts := make([]Part, 0, len(content.Parts))
		changed := false
		for _, part := range content.Parts {
			if part.Text == "" || part.SpeechMetadata != nil && part.SpeechMetadata.Speaker != "" {
				parts = append(parts, part)
				continue
			}
			segments := speakerSegments(part, pattern)
			if len(segments) == 0 {
				parts = append(parts, part)
				continue
			}
			parts = append(parts, segments...)
			changed = true
		}
		if changed {
			result[contentIndex].Parts = parts
		}
	}
	return result
}

// speakerSegments 返回文本中以说话人开头的各段台词，首个说话人之前的文本不进入分段
func speakerSegments(part Part, pattern *regexp.Regexp) []Part {
	style := ""
	if part.SpeechMetadata != nil {
		style = part.SpeechMetadata.Style
	}
	var segments []Part
	for _, line := range strings.Split(part.Text, "\n") {
		line = strings.TrimRight(line, "\r")
		if match := pattern.FindStringSubmatch(line); match != nil {
			segments = append(segments, Part{Text: match[2], SpeechMetadata: &SpeechMetadata{Speaker: match[1], Style: style}})
			continue
		}
		if len(segments) > 0 {
			segments[len(segments)-1].Text += "\n" + line
		}
	}
	result := segments[:0]
	for _, segment := range segments {
		segment.Text = strings.TrimSpace(segment.Text)
		if segment.Text != "" {
			result = append(result, segment)
		}
	}
	return result
}

// validateGenerateRequest 用各账户已载入的模型条目校验请求，任一条目接受即通过，目录未载入时交给生成路径
func (c *Client) validateGenerateRequest(request GenerateRequest) error {
	var first error
	for _, entry := range c.cachedModelEntries(request.Model) {
		err := validateGenerateEntry(request, entry)
		if err == nil {
			return nil
		}
		if first == nil {
			first = err
		}
	}
	return first
}

// validateGenerateEntry 按 Generate 的顺序校验工具、转写、生成参数与安全设置
func validateGenerateEntry(request GenerateRequest, entry modelEntry) error {
	if err := validateVideoParts(request.Contents); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if _, err := supportedTools(request.Tools, entry.model); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if err := validateTranscriptionConfig(request.Config.TranscriptionConfig, entry.model); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if entry.defaults.InteractionStream || entry.defaults.InteractionTask {
		return nil
	}
	if _, err := encodeGenerationConfig(applyModelMediaDefaults(request.Config, entry.model), entry.defaults); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if _, err := resolveSafetySettings(request.SafetySettings, entry.defaults.ImageRoute); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	return nil
}

func (c *Client) Generate(ctx context.Context, request GenerateRequest) (<-chan Event, error) {
	lease, leased := AccountLeaseFromContext(ctx)
	build := leased && lease.Channel() == ChannelBuild
	var entry modelEntry
	var err error
	if build {
		entry, err = c.buildModelEntry(ctx, request, lease)
	} else {
		entry, err = c.modelEntry(ctx, request.AccountID, request.Model)
	}
	if err != nil {
		return nil, err
	}
	apiKey, _ := ctx.Value(apiKeyContextKey{}).(string)
	restoring := request.restoreModelTurns && !build && !entry.model.Capabilities["assistant_route"] && !entry.defaults.InteractionStream && !entry.defaults.InteractionTask
	if restoring {
		request.Contents, request.turnToken = c.turns.restore(apiKey, request.Contents)
	}
	history := request.Contents
	if err := validateVideoParts(request.Contents); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	request.Tools, err = supportedTools(request.Tools, entry.model)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if !entry.model.Capabilities["function_declarations"] {
		request.Contents, err = transcribeFunctionParts(request.Contents)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
		}
	}
	interaction := entry.defaults.InteractionStream && !build
	task := entry.defaults.InteractionTask && !build
	if interaction {
		request.Tools = Tools{}
	}
	request, contract, err := prepareToolRequest(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if err := validateTranscriptionConfig(request.Config.TranscriptionConfig, entry.model); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if request.Truncate && entry.model.InputTokenLimit > 0 {
		request, err = c.truncateRequest(ctx, request, entry.model.InputTokenLimit)
		if err != nil {
			return nil, err
		}
	}
	if interaction {
		return c.generateInteraction(ctx, request, entry)
	}
	if task {
		return c.generateInteractionTask(ctx, request, entry)
	}
	request.Config = applyModelMediaDefaults(request.Config, entry.model)
	request.ImageRoute = entry.defaults.ImageRoute
	request.Contents = applySpeechTranscript(request.Contents, entry.model, request.Config)
	wireRequest := request
	wireRequest.Config.StopSequences = nil
	// accountCtx 是发送请求所用的上下文，续写轮换账户后为新账户的上下文
	accountCtx := ctx
	switched := func(next context.Context, accountID string) {
		accountCtx, request.AccountID, wireRequest.AccountID = next, accountID, accountID
	}
	sendRequest := func() (*RPCResponse, func(io.Reader, func(Event) error) error, error) {
		switch {
		case entry.model.Capabilities["assistant_route"]:
			return c.sendAssistant(accountCtx, wireRequest, entry)
		case build:
			return c.sendBuild(accountCtx, wireRequest, entry)
		default:
			return c.sendPlayground(accountCtx, wireRequest, entry, switched)
		}
	}
	response, decodeStream, err := sendRequest()
	if err != nil {
		return nil, err
	}
	matcher := newStopSequenceMatcher(request.Config.StopSequences)
	var stopTokenCount <-chan tokenCountResult
	cancelTokenCount := context.CancelFunc(func() {})
	if matcher != nil {
		stopTokenCount, cancelTokenCount = c.countStopInput(ctx, request)
	}
	events := make(chan Event, 8)
	go func() {
		defer close(events)
		defer cancelTokenCount()
		closeOnCancel := func(response *RPCResponse) func() bool {
			return context.AfterFunc(ctx, func() {
				_ = response.Body.Close()
			})
		}
		stopClose := closeOnCancel(response)
		defer func() { stopClose() }()
		providerModel := entry.model.ID
		// sentOutput 表示下游已收到本次生成的事件
		sentOutput := false
		send := func(event Event) error {
			providerModel = cmp.Or(event.ProviderModel, providerModel)
			event.ProviderModel = providerModel
			select {
			case events <- event:
				sentOutput = true
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		var usage *Usage
		var previousUsage *Usage
		var finish *Event
		var output Output
		// turn 是按上游顺序未合并的本轮 Part 与轮次令牌，生成完整结束后保存供下一轮补回
		var turn modelTurn
		// committed 表示本次生成已有不能随重新生成丢弃的输出：客户端收到的正文、函数调用与媒体，或重新执行有外部副作用的服务端工具调用
		committed := false
		// sources 暂存 committed 之前收到的网页来源与引用，重新生成时丢弃
		var sources []Event
		releaseSources := func() error {
			for _, source := range sources {
				if err := send(source); err != nil {
					return err
				}
			}
			sources = nil
			return nil
		}
		matchedStopSequence := ""
		emitEvent := func(event Event) error {
			turn.turnToken = cmp.Or(event.TurnToken, turn.turnToken)
			if previousUsage != nil && event.FrameUsage != nil {
				merged := mergeContinuedUsage(*previousUsage, *event.FrameUsage)
				event.FrameUsage = &merged
			}
			switch event.Kind {
			case EventUsage:
				value := *event.Usage
				if previousUsage != nil {
					value = mergeContinuedUsage(*previousUsage, value)
				}
				usage = &value
				return nil
			case EventFinish:
				value := event
				finish = &value
				return nil
			default:
				hiddenTool := serverToolHidden(request, event)
				if hiddenTool && !serverSideInvocations(request.Tools, entry.defaults.ImageRoute) {
					// 只为取得 URL context 读取状态而开启的工具步骤与不开启时一样不输出，签名属于工具步骤，回传时上游不接受
					return nil
				}
				if !hiddenTool || !serverToolReplayable(event) {
					output.Add(event)
					if restoring && event.HasContent() {
						turn.parts = append(turn.parts, clonePart(event.Part))
					}
					committed = committed || !event.Thought && event.payload()
				}
				if !committed {
					if event.Kind == EventGrounding || event.Kind == EventCitation {
						sources = append(sources, event)
						return nil
					}
				} else if err := releaseSources(); err != nil {
					return err
				}
				if request.Config.HideThinking && event.Kind == EventReasoning || hiddenTool {
					if event.ThoughtSignature == "" {
						return nil
					}
					event = Event{
						Kind: EventThoughtSignature, Part: Part{ThoughtSignature: event.ThoughtSignature},
						TurnToken: event.TurnToken, DebugInfo: event.DebugInfo, ResponseID: event.ResponseID, FrameUsage: event.FrameUsage, FinishFrame: event.FinishFrame,
					}
				}
				return send(event)
			}
		}
		var stripper *continuationPrefixStripper
		// roundUsage 是本次尝试最近一帧的上游用量，流中断时没有用量事件，续写按它合并
		var roundUsage *Usage
		roundEmit := func() func(Event) error {
			emit := stopSequenceEmitter(matcher, emitEvent, &matchedStopSequence)
			return func(event Event) error {
				turn.turnToken = cmp.Or(event.TurnToken, turn.turnToken)
				roundUsage = cmp.Or(event.FrameUsage, roundUsage)
				return stripper.filter(event, emit)
			}
		}
		err := decodeStream(observeStreamActivity(ctx, response.Body), roundEmit())
		assistant := entry.model.Capabilities["assistant_route"]
		for regenerated := 0; regenerated < regenerations; regenerated++ {
			// 写代码助手的调用块错误已在应用内续写，这里只重新生成发送与读取失败
			if assistant && err == nil {
				break
			}
			// emittedText 是续写时已向客户端输出的纯文本，为空时从头重新生成
			emittedText := ""
			if !regenerable(ctx, err, finish, committed) {
				text, ok := committedTextOnly(output)
				if !ok || assistant || !regenerable(ctx, err, finish, false) {
					break
				}
				emittedText = text
			}
			stopClose()
			_ = response.Body.Close()
			if emittedText != "" {
				trimTrailingSignatureParts(&output.Parts)
				trimTrailingSignatureParts(&turn.parts)
				turn.turnToken = ""
				switch {
				case usage != nil:
					previousUsage = usage
				case roundUsage != nil && previousUsage != nil:
					merged := mergeContinuedUsage(*previousUsage, *roundUsage)
					previousUsage = &merged
				case roundUsage != nil:
					previousUsage = roundUsage
				}
				wireRequest = request
				wireRequest.Config.StopSequences = nil
				wireRequest.turnToken = ""
				contents := make([]Content, 0, len(request.Contents)+2)
				contents = append(contents, request.Contents...)
				contents = append(contents,
					Content{Role: RoleAssistant, Parts: []Part{{Text: emittedText}}},
					Content{Role: RoleUser, Parts: []Part{{Text: partialContinuationPrompt(emittedText, finish)}}},
				)
				wireRequest.Contents = contents
				usage, finish = nil, nil
				stripper = newContinuationPrefixStripper(emittedText)
			} else {
				usage, finish, sources, turn = nil, nil, nil, modelTurn{}
				stripper = nil
			}
			roundUsage = nil
			if response, decodeStream, err = sendRequest(); err != nil {
				if ctx.Err() == nil {
					_ = send(Event{Kind: EventError, Err: err})
				}
				return
			}
			stopClose = closeOnCancel(response)
			matcher = newStopSequenceMatcher(request.Config.StopSequences)
			err = decodeStream(observeStreamActivity(ctx, response.Body), roundEmit())
		}
		if errors.Is(err, errStopSequenceMatched) {
			_ = response.Body.Close()
			if releaseSources() != nil {
				return
			}
			finishAtStopSequence(ctx, request, output, stopTokenCount, matchedStopSequence, send)
			return
		}
		if closeErr := response.Body.Close(); err == nil {
			err = closeErr
		}
		if ctx.Err() == nil && matcher != nil {
			if pending := matcher.flush(); pending != "" {
				if flushErr := emitEvent(Event{Kind: EventText, Text: pending}); err == nil {
					err = flushErr
				}
			}
		}
		if err != nil {
			if ctx.Err() == nil {
				_ = send(Event{Kind: EventError, Err: err})
			}
			return
		}
		if finish != nil && contentPolicyFinish(finish.FinishReason) {
			if usage == nil {
				usage = previousUsage
			}
			policy := &ContentPolicyError{Reason: finish.FinishReason, Message: finish.FinishMessage}
			if !sentOutput {
				policy.Usage = usage
			} else if usage != nil && send(Event{Kind: EventUsage, Usage: usage}) != nil {
				return
			}
			if ctx.Err() == nil {
				_ = send(Event{Kind: EventError, Err: policy})
			}
			return
		}
		if releaseSources() != nil {
			return
		}
		if usage == nil {
			usage = previousUsage
		}
		if usage == nil {
			usage = localCompleteUsage(request, output)
		} else if usage.OutputTokensMissing {
			outputTokens := usage.TotalTokens - usage.InputTokens - usage.ToolTokens - usage.ReasoningTokens
			if outputTokens < 0 {
				outputTokens = localPartsTokens(output.Visible())
			}
			usage.OutputTokens = outputTokens
			usage.OutputTokensMissing = false
		}
		if err := send(Event{Kind: EventUsage, Usage: usage}); err != nil {
			return
		}
		if finish != nil {
			_ = send(*finish)
		}
		if restoring {
			c.turns.store(apiKey, history, turn)
		}
	}()
	return contract.forward(ctx, events), nil
}

// contentPolicyFinish 表示结束原因属于内容策略类：安全、背诵、屏蔽词、禁止内容、个人敏感信息与对应的图片原因
func contentPolicyFinish(reason string) bool {
	switch reason {
	case "safety", "recitation", "blocklist", "prohibited_content", "spii", "image_safety", "image_prohibited_content", "image_recitation":
		return true
	}
	return false
}

// abnormalGenerationFinish 表示结束原因属于异常工具调用或格式错误响应：格式错误的函数调用、意外的工具调用与格式错误的响应
func abnormalGenerationFinish(reason string) bool {
	switch reason {
	case "malformed_function_call", "unexpected_tool_call", "malformed_response":
		return true
	}
	return false
}

// committedTextOnly 返回已向客户端提交的纯文本输出，没有可见文本或含函数调用、媒体、代码与服务端工具步骤时返回 false
func committedTextOnly(output Output) (string, bool) {
	visible := output.Visible()
	if len(visible) == 0 {
		return "", false
	}
	var text strings.Builder
	for _, part := range visible {
		if part.SignatureOnly() {
			continue
		}
		if !part.textOnly() {
			return "", false
		}
		text.WriteString(part.Text)
	}
	if text.Len() == 0 {
		return "", false
	}
	return text.String(), true
}

// trimTrailingSignatureParts 去掉失败尝试末尾只有思考签名的 Part
func trimTrailingSignatureParts(parts *[]Part) {
	items := *parts
	for len(items) > 0 && items[len(items)-1].SignatureOnly() {
		items = items[:len(items)-1]
	}
	*parts = items
}

// partialReplyRunes 是续写恢复提示带上的已输出正文末尾的字符数上限
const partialReplyRunes = 20000

// partialReplyTail 返回已输出正文末尾至多 partialReplyRunes 个 Unicode 字符
func partialReplyTail(text string) string {
	runes := []rune(text)
	if len(runes) > partialReplyRunes {
		return string(runes[len(runes)-partialReplyRunes:])
	}
	return text
}

// partialContinuationPrompt 构造在已输出部分正文后继续生成或补发有效工具调用的用户提示
func partialContinuationPrompt(emittedText string, finish *Event) string {
	reason := "Your last turn was cut off by an internal error."
	if finish != nil {
		switch finish.FinishReason {
		case "malformed_function_call":
			reason = "Your last turn was cut off because the function call was malformed."
		case "unexpected_tool_call":
			reason = "Your last turn was cut off because of an unexpected tool call."
		case "malformed_response":
			reason = "Your last turn was cut off because the response was malformed."
		}
	}
	return fmt.Sprintf(
		"%s The user already received your reply up to the end of this text:\n<partial_reply>\n%s\n</partial_reply>\nContinue immediately right after <partial_reply> without repeating it, and emit the valid tool call or remaining response.",
		reason, partialReplyTail(emittedText),
	)
}

// continuationPrefixStripper 在续写恢复轮次去掉开头重复已输出正文的前缀，尚未确定是否完全匹配时暂存前缀
type continuationPrefixStripper struct {
	targets    []string
	pending    string
	pendingSig string
	active     bool
}

// newContinuationPrefixStripper 返回按已输出正文全文、去尾空白与末尾片段去重的前缀过滤器
func newContinuationPrefixStripper(emittedText string) *continuationPrefixStripper {
	if emittedText == "" {
		return nil
	}
	tail := partialReplyTail(emittedText)
	candidates := []string{
		emittedText,
		strings.TrimRight(emittedText, " \t\r\n"),
		tail,
		strings.TrimRight(tail, " \t\r\n"),
	}
	targets := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" || slices.Contains(targets, candidate) {
			continue
		}
		targets = append(targets, candidate)
	}
	if len(targets) == 0 {
		return nil
	}
	slices.SortStableFunc(targets, func(left string, right string) int {
		return cmp.Compare(len(right), len(left))
	})
	return &continuationPrefixStripper{targets: targets, active: true}
}

// longerPrefixPending 表示 combined 是 targets[:index] 中某个更长候选的前缀
func (s *continuationPrefixStripper) longerPrefixPending(index int, combined string) bool {
	for _, longer := range s.targets[:index] {
		if len(combined) < len(longer) && strings.HasPrefix(longer, combined) {
			return true
		}
	}
	return false
}

// flush 停止去重并输出尚未确认重复的暂存正文
func (s *continuationPrefixStripper) flush(emit func(Event) error) error {
	s.active = false
	if s.pending == "" {
		return nil
	}
	text, sig := s.pending, s.pendingSig
	s.pending, s.pendingSig = "", ""
	return emit(Event{Kind: EventText, Text: text, ThoughtSignature: sig})
}

// filter 过滤续写轮开头重复的已输出正文前缀并保留思考签名
func (s *continuationPrefixStripper) filter(event Event, emit func(Event) error) error {
	if s == nil || !s.active {
		return emit(event)
	}
	if event.Kind != EventText {
		switch event.Kind {
		case EventUsage, EventReasoning, EventGrounding, EventCitation:
			return emit(event)
		case EventThoughtSignature:
			if s.pending == "" {
				return emit(event)
			}
		}
		if err := s.flush(emit); err != nil {
			return err
		}
		return emit(event)
	}
	if event.Transcript != nil {
		if err := s.flush(emit); err != nil {
			return err
		}
		return emit(event)
	}
	combined := s.pending + event.Text
	sig := cmp.Or(event.ThoughtSignature, s.pendingSig)
	for index, target := range s.targets {
		if strings.HasPrefix(combined, target) {
			if index > 0 && s.longerPrefixPending(index, combined) {
				break
			}
			rest := strings.TrimPrefix(combined, target)
			s.pending = ""
			s.pendingSig = ""
			s.active = false
			if rest == "" {
				if sig != "" {
					return emit(Event{
						Kind:          EventThoughtSignature,
						Part:          Part{ThoughtSignature: sig},
						ProviderModel: event.ProviderModel,
						TurnToken:     event.TurnToken,
						DebugInfo:     event.DebugInfo,
						ResponseID:    event.ResponseID,
						FrameUsage:    event.FrameUsage,
						FinishFrame:   event.FinishFrame,
					})
				}
				return nil
			}
			event.Text = rest
			event.ThoughtSignature = sig
			return emit(event)
		}
	}
	for _, target := range s.targets {
		if strings.HasPrefix(target, combined) {
			s.pending = combined
			s.pendingSig = sig
			return nil
		}
	}
	s.pending = ""
	s.pendingSig = ""
	s.active = false
	event.Text = combined
	event.ThoughtSignature = sig
	return emit(event)
}

// regenerable 表示上游以函数调用格式错误、意外的工具调用或响应格式异常结束、流在完成帧之前中断或流尾为 5xx 错误，且本次生成还没有不能丢弃的输出
func regenerable(ctx context.Context, err error, finish *Event, committed bool) bool {
	if err != nil {
		var interrupted *StreamInterruptedError
		var rpcError *RPCError
		if ctx.Err() != nil || !errors.As(err, &interrupted) && !(errors.As(err, &rpcError) && rpcError.StatusCode >= http.StatusInternalServerError) {
			return false
		}
	} else if finish == nil || !abnormalGenerationFinish(finish.FinishReason) {
		return false
	}
	return !committed
}

// countStopInput 并发计数请求输入，供本地命中 stop sequence 后补全用量
func (c *Client) countStopInput(ctx context.Context, request GenerateRequest) (<-chan tokenCountResult, context.CancelFunc) {
	countContext, cancel := context.WithCancel(ctx)
	results := make(chan tokenCountResult, 1)
	countRequest := TokenCountRequest{
		Model: request.Model, System: request.System, Contents: request.Contents, Tools: request.Tools,
	}
	go func() {
		count, err := c.CountTokensForAccount(countContext, request.AccountID, countRequest)
		results <- tokenCountResult{count: count, err: err}
		close(results)
	}()
	return results, cancel
}

// stopSequenceEmitter 在 stop sequence 处截断正文，非正文事件前补发暂存文本，命中时记录序列并返回 errStopSequenceMatched
func stopSequenceEmitter(matcher *stopSequenceMatcher, forward func(Event) error, matched *string) func(Event) error {
	return func(event Event) error {
		if matcher == nil {
			return forward(event)
		}
		if pending := matcher.boundary(event.Kind); pending != "" {
			if err := forward(Event{Kind: EventText, ProviderModel: event.ProviderModel, Text: pending}); err != nil {
				return err
			}
		}
		if event.Kind != EventText {
			return forward(event)
		}
		text, sequence := matcher.write(event.Text)
		if text != "" {
			event.Text = text
			if err := forward(event); err != nil {
				return err
			}
		}
		if sequence != "" {
			*matched = sequence
			return errStopSequenceMatched
		}
		return nil
	}
}

// finishAtStopSequence 以权威输入计数补发用量，再发送 stop_sequence 终态；计数失败时只发送终态
func finishAtStopSequence(ctx context.Context, request GenerateRequest, output Output, counts <-chan tokenCountResult, sequence string, send func(Event) error) {
	select {
	case result := <-counts:
		if result.err == nil {
			if err := send(Event{Kind: EventUsage, Usage: countedCompleteUsage(request, output, result.count)}); err != nil {
				return
			}
		}
	case <-ctx.Done():
		return
	}
	_ = send(Event{Kind: EventFinish, FinishReason: "stop_sequence", StopSequence: sequence})
}

// truncateRequest 按权威计数删除最早的完整对话轮次，倍增定位后二分查找最少的删除轮数
func (c *Client) truncateRequest(ctx context.Context, request GenerateRequest, limit int64) (GenerateRequest, error) {
	starts := []int{0}
	for start := 0; ; {
		next := nextConversationTurn(request.Contents[start:])
		if next < 0 {
			break
		}
		start += next
		starts = append(starts, start)
	}
	fits := func(index int) (bool, error) {
		count, err := c.CountTokensForAccount(ctx, request.AccountID, TokenCountRequest{
			Model: request.Model, System: request.System, Contents: request.Contents[starts[index]:], Tools: request.Tools,
		})
		return err == nil && count.InputTokens <= limit, err
	}
	if ok, err := fits(0); err != nil || ok {
		return request, err
	}
	failed, fitted := 0, 1
	for {
		fitted = min(fitted, len(starts)-1)
		if fitted == failed {
			return request, fmt.Errorf("%w: 最新对话轮次超过模型上下文窗口 %d", ErrInvalidArgument, limit)
		}
		ok, err := fits(fitted)
		if err != nil {
			return request, err
		}
		if ok {
			break
		}
		failed, fitted = fitted, fitted*2
	}
	for fitted-failed > 1 {
		middle := (failed + fitted) / 2
		ok, err := fits(middle)
		if err != nil {
			return request, err
		}
		if ok {
			fitted = middle
		} else {
			failed = middle
		}
	}
	request.Contents = request.Contents[starts[fitted]:]
	return request, nil
}

// nextConversationTurn 查找保留工具调用与结果配对的下一轮用户消息
func nextConversationTurn(contents []Content) int {
	for index := 1; index < len(contents); index++ {
		if contents[index].Role == RoleUser && !slices.ContainsFunc(contents[index].Parts, func(part Part) bool { return part.FunctionResult != nil }) {
			return index
		}
	}
	return -1
}

// sendPlayground 编码并发送 Playground GenerateContent，返回响应与含完成帧校验的流解码，续写轮换账户后以新账户的上下文与 ID 调用 switched
func (c *Client) sendPlayground(ctx context.Context, request GenerateRequest, entry modelEntry, switched func(context.Context, string)) (*RPCResponse, func(io.Reader, func(Event) error) error, error) {
	reason := ""
	if request.Unary {
		reason = "Playground GenerateContent 使用服务端流，收集完成后返回"
	}
	reportUpstreamMode(ctx, "GenerateContent", "stream", reason)
	runtime := RequestContext{}
	if c.contextProvider != nil {
		var err error
		runtime, err = c.contextProvider.RequestContext(ctx, request.AccountID)
		if err != nil {
			return nil, nil, fmt.Errorf("读取 AI Studio 请求上下文: %w", err)
		}
	}
	body, err := EncodeGenerateContentRequest(request, entry.defaults, runtime)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	response, err := c.doProtected(ctx, request, body)
	if err != nil {
		return nil, nil, err
	}
	request.continuationProof = response.Proof
	return response, func(source io.Reader, emit func(Event) error) error {
		return c.decodePlaygroundRounds(ctx, request, entry.defaults, runtime, source, restoreFunctionNames(request.Tools.Functions, emit), switched)
	}, nil
}

// continuationAccountsKey 是上下文中续写轮换账户函数的键
type continuationAccountsKey struct{}

// ContinuationAccounts 在续写轮以可换账户的错误失败后返回改由其他账户发送该轮的上下文
type ContinuationAccounts func(context.Context, error) (context.Context, error)

// ContextWithContinuationAccounts 返回续写轮可经 accounts 换账户发送的上下文
func ContextWithContinuationAccounts(ctx context.Context, accounts ContinuationAccounts) context.Context {
	return context.WithValue(ctx, continuationAccountsKey{}, accounts)
}

// decodePlaygroundRounds 解码 GenerateContent 流，上游要求续写时携带续写 token 发送下一轮，续写轮以可换账户的错误失败时经上下文中的 ContinuationAccounts 换账户重发并调用 switched，没有其他账户时返回该轮的上游错误，各轮输出依次转发
func (c *Client) decodePlaygroundRounds(ctx context.Context, request GenerateRequest, defaults GenerationDefaults, runtime RequestContext, source io.Reader, emit func(Event) error, switched func(context.Context, string)) error {
	total := int64(0)
	if defaults.ContinuedOutputTokens > 0 {
		config, err := encodeGenerationConfig(request.Config, defaults)
		if err != nil {
			return err
		}
		total, _ = config[3].(int64)
	}
	var previous *Usage
	closeRound := func() {}
	defer func() { closeRound() }()
	for {
		decoder := NewFrameDecoder()
		decoder.continuable = defaults.ContinuedOutputTokens > 0
		if len(request.System) > 0 {
			decoder.systemRoleTokens = 1
		}
		decoder.continueAtMaxTokens = int64(request.continuationRound+1)*continuationRoundTokens < total
		forward := emit
		if previous != nil {
			forward = func(event Event) error {
				if event.Kind == EventUsage {
					merged := mergeContinuedUsage(*previous, *event.Usage)
					event.Usage = &merged
				}
				if event.FrameUsage != nil {
					merged := mergeContinuedUsage(*previous, *event.FrameUsage)
					event.FrameUsage = &merged
				}
				return emit(event)
			}
		}
		if err := DecodeGenerateStream(source, decoder, forward); err != nil {
			return err
		}
		if err := decoder.End(); err != nil {
			return err
		}
		if decoder.continuation == "" {
			return nil
		}
		if decoder.usage != nil {
			merged := *decoder.usage
			if previous != nil {
				merged = mergeContinuedUsage(*previous, merged)
			}
			previous = &merged
		}
		request.continuationRound++
		request.continuationToken = decoder.continuation
		reportContinuationRound(ctx)
		reportUpstreamMode(ctx, "GenerateContent", fmt.Sprintf("stream | 续写=%d", request.continuationRound), "")
		body, err := EncodeGenerateContentRequest(request, defaults, runtime)
		if err != nil {
			return err
		}
		closeRound()
		response, err := c.doProtected(ctx, request, body)
		for err != nil && retryableAccountError(err) {
			accounts, ok := ctx.Value(continuationAccountsKey{}).(ContinuationAccounts)
			if !ok {
				break
			}
			next, switchErr := accounts(ctx, err)
			if switchErr != nil {
				if !errors.Is(switchErr, ErrNoEligibleAccount) {
					err = errors.Join(err, switchErr)
				}
				break
			}
			ctx = next
			lease, _ := AccountLeaseFromContext(ctx)
			request.AccountID, request.continuationProof = lease.Account().ID, ""
			switched(ctx, request.AccountID)
			if c.contextProvider != nil {
				if runtime, err = c.contextProvider.RequestContext(ctx, request.AccountID); err != nil {
					return fmt.Errorf("读取 AI Studio 请求上下文: %w", err)
				}
			}
			if body, err = EncodeGenerateContentRequest(request, defaults, runtime); err != nil {
				return err
			}
			reportUpstreamMode(ctx, "GenerateContent", fmt.Sprintf("stream | 续写=%d", request.continuationRound), "")
			response, err = c.doProtected(ctx, request, body)
		}
		if err != nil {
			return err
		}
		request.continuationProof = response.Proof
		stopClose := context.AfterFunc(ctx, func() {
			_ = response.Body.Close()
		})
		closeRound = func() {
			stopClose()
			_ = response.Body.Close()
		}
		source = observeStreamActivity(ctx, response.Body)
	}
}

// mergeContinuedUsage 合并续写前后两轮用量，以新一轮为基础累加输出、思考与工具用量，总量为新一轮总量加上之前各轮的这三项
func mergeContinuedUsage(previous Usage, next Usage) Usage {
	if next.OutputTokensMissing {
		next.OutputTokens = max(0, next.TotalTokens-next.InputTokens-next.ToolTokens-next.ReasoningTokens)
	}
	if previous.OutputTokensMissing {
		previous.OutputTokens = max(0, previous.TotalTokens-previous.InputTokens-previous.ToolTokens-previous.ReasoningTokens)
	}
	return Usage{
		InputTokens:     next.InputTokens,
		CachedTokens:    next.CachedTokens,
		OutputTokens:    previous.OutputTokens + next.OutputTokens,
		ReasoningTokens: previous.ReasoningTokens + next.ReasoningTokens,
		ToolTokens:      previous.ToolTokens + next.ToolTokens,
		TotalTokens:     next.TotalTokens + previous.OutputTokens + previous.ReasoningTokens + previous.ToolTokens,
	}
}

// DecodeGenerateStream 按网络到达顺序解码 GenerateContent repeated 帧
func DecodeGenerateStream(source io.Reader, decoder *FrameDecoder, emit func(Event) error) error {
	return decodeGenerateItems(source, func(raw json.RawMessage) error {
		events, err := decoder.Decode(raw)
		if err != nil {
			return err
		}
		for _, event := range events {
			if err := emit(event); err != nil {
				return err
			}
		}
		return nil
	})
}
