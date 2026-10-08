package aistudio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

var errStopSequenceMatched = errors.New("stop sequence matched")

// tokenCountResult 保存并发输入计数结果
type tokenCountResult struct {
	count TokenCount
	err   error
}

// EncodeGenerateContentRequest 编码当前成功基线的 GenerateContent 数组
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
	config, err := encodeGenerationConfig(request.Config, defaults)
	if err != nil {
		return nil, err
	}
	if len(config) > 8 && config[8] != nil {
		config[8] = projectPlaygroundResponseSchema(config[8].([]any), request.Config.ResponseSchema)
	}
	serverSideTools := explicitTools && len(request.Tools.Functions) > 0 && (len(request.Tools.Google) > 0 || request.Tools.GoogleSearch != nil)
	length := 11
	if runtime.Timezone != "" || serverSideTools {
		length = 14
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
	if request.System != "" {
		wire[5] = encodeSystemInstruction(request.System)
	}
	switch {
	case explicitTools:
		wire[6] = tools
	case defaults.OutputResolution:
		// 可设置分辨率模型的默认扩展工具字段
		wire[6] = []any{[]any{nil, nil, nil, []any{nil, []any{}}}}
	}
	wire[10] = int64(1)
	if runtime.Timezone != "" || serverSideTools {
		toolConfig := []any{nil}
		if runtime.Timezone != "" {
			toolConfig[0] = []any{nil, nil, runtime.Timezone}
		}
		if serverSideTools {
			// 同时使用内置工具与函数调用时开启 include_server_side_tool_invocations
			toolConfig = append(toolConfig, nil, true)
		}
		wire[13] = toolConfig
	}
	return json.Marshal(wire)
}

func encodeGenerationConfig(config GenerationConfig, defaults GenerationDefaults) ([]any, error) {
	var responseSchema []any
	var err error
	if len(config.ResponseSchema) > 0 {
		responseSchema, err = encodeResponseSchema(config.ResponseSchema)
		if err != nil {
			return nil, fmt.Errorf("response schema: %w", err)
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
		return nil, fmt.Errorf("reasoning effort 必须是 none、minimal、low、medium、high、xhigh 或 max")
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
	includeMaxOutput := config.SpeechConfig == nil || config.MaxOutputTokens != nil
	maxOutput := defaults.MaxOutputTokens
	if config.MaxOutputTokens != nil {
		maxOutput = *config.MaxOutputTokens
	}
	if includeMaxOutput && maxOutput > defaults.MaxOutputTokens {
		maxOutput = defaults.MaxOutputTokens
	}
	if includeMaxOutput && maxOutput <= 0 {
		return nil, fmt.Errorf("模型目录缺少有效 output token limit")
	}
	temperature := defaults.Temperature
	if config.Temperature != nil {
		temperature = config.Temperature
	}
	if temperature != nil && (*temperature < 0 || *temperature > 2) {
		return nil, fmt.Errorf("temperature 必须在 0 到 2 之间")
	}
	topP := defaults.TopP
	if config.TopP != nil {
		topP = config.TopP
	}
	if topP != nil && (*topP < 0 || *topP > 1) {
		return nil, fmt.Errorf("top_p 必须在 0 到 1 之间")
	}
	topK := defaults.TopK
	if config.TopK != nil {
		topK = config.TopK
	}
	if topK != nil && *topK < 0 {
		return nil, fmt.Errorf("top_k 不能为负数")
	}
	responseModalities, err := encodeResponseModalities(config.ResponseModalities)
	if err != nil {
		return nil, err
	}
	imageConfig := encodeImageConfig(config.ImageConfig)
	if defaults.OutputResolution && imageConfig == nil {
		// 可设置分辨率模型的默认输出尺寸
		imageConfig = []any{nil, "1K"}
	}
	speechConfig, err := encodeSpeechConfig(config.SpeechConfig)
	if err != nil {
		return nil, err
	}
	transcriptionConfig, err := encodeTranscriptionConfig(config.TranscriptionConfig)
	if err != nil {
		return nil, err
	}
	mediaResolution, err := encodeMediaResolution(config.MediaResolution)
	if err != nil {
		return nil, err
	}
	includeThinking := defaults.Thinking || defaults.ThinkingBudget || defaults.ThinkingLevel || thinkingBudget != nil || hasReasoningEffort
	length := 14
	if responseModalities != nil {
		length = 15
	}
	if speechConfig != nil {
		length = 16
	}
	if includeThinking {
		if length < 17 {
			length = 17
		}
	}
	if mediaResolution != nil && length < 18 {
		length = 18
	}
	if config.Seed != nil {
		if length < 19 {
			length = 19
		}
	}
	if imageConfig != nil {
		length = 27
	}
	if transcriptionConfig != nil {
		length = 32
	}
	wire := make([]any, length)
	if len(config.StopSequences) > 0 {
		wire[1] = append([]string(nil), config.StopSequences...)
	}
	if includeMaxOutput {
		wire[3] = maxOutput
	}
	if temperature != nil {
		wire[4] = *temperature
	}
	if topP != nil {
		wire[5] = *topP
	}
	if topK != nil {
		wire[6] = *topK
	}
	if config.ResponseMIMEType != "" {
		wire[7] = config.ResponseMIMEType
	}
	if responseSchema != nil {
		wire[8] = responseSchema
	}
	wire[13] = int64(1)
	if responseModalities != nil {
		wire[14] = responseModalities
	}
	if speechConfig != nil {
		wire[15] = speechConfig
	}
	if includeThinking {
		thinking := []any{int64(1)}
		if defaults.ThinkingLevel {
			thinking = []any{int64(1), nil, nil, thinkingLevel}
		}
		if thinkingBudget != nil {
			if len(thinking) < 2 {
				thinking = append(thinking, nil)
			}
			thinking[1] = *thinkingBudget
		}
		wire[16] = thinking
	}
	if mediaResolution != nil {
		wire[17] = mediaResolution
	}
	if config.Seed != nil {
		wire[18] = *config.Seed
	}
	if imageConfig != nil {
		wire[26] = imageConfig
	}
	if transcriptionConfig != nil {
		wire[31] = transcriptionConfig
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
		switch ResponseModality(strings.ToUpper(strings.TrimSpace(string(modality)))) {
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
	voiceName := strings.TrimSpace(config.VoiceName)
	if voiceName != "" && len(config.Speakers) > 0 {
		return nil, fmt.Errorf("speech config 不能同时设置 voice 和 multi-speaker")
	}
	var wire []any
	if voiceName != "" {
		wire = []any{[]any{[]any{voiceName}}}
	}
	if len(config.Speakers) > 0 {
		speakers := make([]any, 0, len(config.Speakers))
		for index, speaker := range config.Speakers {
			name := strings.TrimSpace(speaker.Speaker)
			voice := strings.TrimSpace(speaker.VoiceName)
			if name == "" || voice == "" {
				return nil, fmt.Errorf("speech config speakers[%d] 需要 speaker 和 voiceName", index)
			}
			speakers = append(speakers, []any{name, []any{[]any{voice}}})
		}
		if wire == nil {
			wire = make([]any, 3)
		} else {
			for len(wire) < 3 {
				wire = append(wire, nil)
			}
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
		wire[2] = multi
	} else if strings.TrimSpace(config.Mode) != "" {
		return nil, fmt.Errorf("speech config mode 只能用于 multi-speaker")
	}
	return wire, nil
}

// mediaResolutions 为 Gemini API 输入媒体分辨率名与 generation config 字段 18 的编号
var mediaResolutions = map[string]int64{"MEDIA_RESOLUTION_LOW": 1, "MEDIA_RESOLUTION_MEDIUM": 2, "MEDIA_RESOLUTION_HIGH": 3}

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

// defaultSpeechVoice 是官网单说话人的默认声音
const defaultSpeechVoice = "Zephyr"

// applyModelMediaDefaults 按 EffectiveResponseModalities 设置输出模态，TTS 模型未设置声音时使用默认声音
func applyModelMediaDefaults(config GenerationConfig, model Model) GenerationConfig {
	config.ResponseModalities = EffectiveResponseModalities(config.ResponseModalities, model)
	if speech := config.SpeechConfig; model.Capabilities["speech_route"] && (speech == nil || strings.TrimSpace(speech.VoiceName) == "" && len(speech.Speakers) == 0) {
		config.SpeechConfig = &SpeechConfig{VoiceName: defaultSpeechVoice}
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
	if _, err := supportedTools(request.Tools, entry.model); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if err := validateTranscriptionConfig(request.Config.TranscriptionConfig, entry.model); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if entry.defaults.InteractionStream {
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
	request.Config = applyModelMediaDefaults(request.Config, entry.model)
	request.ImageRoute = entry.defaults.ImageRoute
	request.Contents = applySpeechTranscript(request.Contents, entry.model, request.Config)
	wireRequest := request
	wireRequest.Config.StopSequences = nil
	var response *RPCResponse
	var decodeStream func(io.Reader, func(Event) error) error
	if build {
		response, decodeStream, err = c.sendBuild(ctx, wireRequest, entry)
	} else {
		response, decodeStream, err = c.sendPlayground(ctx, wireRequest, entry)
	}
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
		stopClose := context.AfterFunc(ctx, func() {
			_ = response.Body.Close()
		})
		defer stopClose()
		send := func(event Event) error {
			event.ProviderModel = entry.model.ID
			select {
			case events <- event:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		var usage *Usage
		var finish *Event
		var output generatedOutputParts
		matchedStopSequence := ""
		emitEvent := func(event Event) error {
			switch event.Kind {
			case EventUsage:
				if event.Usage != nil {
					value := *event.Usage
					usage = &value
				}
				return nil
			case EventFinish:
				value := event
				finish = &value
				return nil
			default:
				output.observe(event)
				if request.Config.HideThinking && event.Kind == EventReasoning {
					if event.ThoughtSignature == "" {
						return nil
					}
					event.Kind, event.Text = EventThoughtSignature, ""
				}
				return send(event)
			}
		}
		emit := stopSequenceEmitter(matcher, emitEvent, &matchedStopSequence)
		err := decodeStream(observeStreamActivity(ctx, response.Body), emit)
		if errors.Is(err, errStopSequenceMatched) {
			_ = response.Body.Close()
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
		if usage == nil {
			usage = localCompleteUsage(request, output)
		} else if usage.OutputTokensMissing {
			outputTokens := usage.TotalTokens - usage.InputTokens - usage.ToolTokens - usage.ReasoningTokens
			if outputTokens < 0 {
				outputTokens = localPartsTokens(output.visible)
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
	}()
	return contract.forward(ctx, events), nil
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
			if err := forward(Event{Kind: EventText, Text: pending, ProviderModel: event.ProviderModel}); err != nil {
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
func finishAtStopSequence(ctx context.Context, request GenerateRequest, output generatedOutputParts, counts <-chan tokenCountResult, sequence string, send func(Event) error) {
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

// sendPlayground 编码并发送 Playground GenerateContent，返回响应与含完成帧校验的流解码
func (c *Client) sendPlayground(ctx context.Context, request GenerateRequest, entry modelEntry) (*RPCResponse, func(io.Reader, func(Event) error) error, error) {
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
	decoder := NewFrameDecoder()
	return response, func(source io.Reader, emit func(Event) error) error {
		if err := DecodeGenerateStream(source, decoder, emit); err != nil {
			return err
		}
		return decoder.End()
	}, nil
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
