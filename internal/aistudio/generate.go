package aistudio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

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
	length := 11
	if runtime.Timezone != "" {
		length = 14
	}
	wire := make([]any, length)
	wire[0] = wireModelName(request.Model)
	wire[1] = contents
	wire[2] = observedSafetySettings()
	wire[3] = config
	if request.System != "" {
		wire[5] = encodeSystemInstruction(request.System)
	}
	if explicitTools {
		wire[6] = tools
	}
	wire[10] = int64(1)
	if runtime.Timezone != "" {
		wire[13] = []any{[]any{nil, nil, runtime.Timezone}}
	}
	return json.Marshal(wire)
}

func encodeGenerationConfig(config GenerationConfig, defaults GenerationDefaults) ([]any, error) {
	var responseSchema []any
	var err error
	if len(config.ResponseSchema) > 0 {
		responseSchema, err = encodeJSONSchema(config.ResponseSchema)
		if err != nil {
			return nil, fmt.Errorf("response schema: %w", err)
		}
	}
	thinkingLevel := defaults.DefaultThinkingLevel
	hasReasoningEffort := strings.TrimSpace(config.ReasoningEffort) != ""
	switch strings.ToLower(strings.TrimSpace(config.ReasoningEffort)) {
	case "":
	case "low":
		thinkingLevel = 1
	case "medium":
		thinkingLevel = 2
	case "high":
		thinkingLevel = 3
	case "minimal":
		thinkingLevel = 4
	default:
		return nil, fmt.Errorf("reasoning effort 必须是 minimal、low、medium 或 high")
	}
	if hasReasoningEffort && !defaults.ThinkingLevel {
		return nil, fmt.Errorf("模型不支持 thinking level")
	}
	if config.ThinkingBudget != nil && !defaults.ThinkingBudget {
		return nil, fmt.Errorf("模型不支持 thinking budget")
	}
	maxOutput := defaults.MaxOutputTokens
	if config.MaxOutputTokens != nil {
		maxOutput = *config.MaxOutputTokens
	}
	if maxOutput <= 0 {
		return nil, fmt.Errorf("模型目录缺少有效 output token limit")
	}
	if maxOutput > defaults.MaxOutputTokens {
		return nil, fmt.Errorf("max output tokens %d 超过模型上限 %d", maxOutput, defaults.MaxOutputTokens)
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
	speechConfig, err := encodeSpeechConfig(config.SpeechConfig)
	if err != nil {
		return nil, err
	}
	length := 14
	if responseModalities != nil {
		length = 15
	}
	if speechConfig != nil {
		length = 16
	}
	if defaults.Thinking || defaults.ThinkingBudget || defaults.ThinkingLevel || config.ThinkingBudget != nil || hasReasoningEffort {
		if length < 17 {
			length = 17
		}
	}
	if config.Seed != nil {
		if length < 19 {
			length = 19
		}
	}
	if imageConfig != nil {
		length = 27
	}
	wire := make([]any, length)
	if len(config.StopSequences) > 0 {
		wire[1] = append([]string(nil), config.StopSequences...)
	}
	wire[3] = maxOutput
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
	if length >= 17 {
		thinking := []any{int64(1)}
		if defaults.ThinkingLevel {
			thinking = []any{int64(1), nil, nil, thinkingLevel}
		}
		if config.ThinkingBudget != nil {
			if len(thinking) < 2 {
				thinking = append(thinking, nil)
			}
			thinking[1] = *config.ThinkingBudget
		}
		wire[16] = thinking
	}
	if config.Seed != nil {
		wire[18] = *config.Seed
	}
	if imageConfig != nil {
		wire[26] = imageConfig
	}
	return wire, nil
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
		wire[2] = []any{nil, speakers}
	}
	return wire, nil
}

func applyModelMediaDefaults(config GenerationConfig, model Model) GenerationConfig {
	if config.ResponseModalities != nil {
		return config
	}
	switch {
	case model.Capabilities["speech_route"], model.Capabilities["music_route"]:
		config.ResponseModalities = []ResponseModality{ResponseModalityAudio}
	case model.Capabilities["image_route"]:
		config.ResponseModalities = []ResponseModality{ResponseModalityImage}
	}
	return config
}

func observedSafetySettings() []any {
	settings := make([]any, 0, 4)
	for category := int64(7); category <= 10; category++ {
		settings = append(settings, []any{nil, nil, category, int64(5)})
	}
	return settings
}

func (c *Client) Generate(ctx context.Context, request GenerateRequest) (<-chan Event, error) {
	entry, err := c.modelEntry(ctx, request.AccountID, request.Model)
	if err != nil {
		return nil, err
	}
	if entry.model.Capabilities["interaction_route"] {
		return nil, fmt.Errorf("%w: 模型 %q 使用 private Interaction 路由", ErrInvalidArgument, entry.model.ID)
	}
	if !hasMethod(entry.model, "generateContent") {
		return nil, fmt.Errorf("%w: 模型 %q 的实时目录没有 generateContent 方法", ErrInvalidArgument, entry.model.ID)
	}
	request.Config = applyModelMediaDefaults(request.Config, entry.model)
	runtime := RequestContext{}
	if c.contextProvider != nil {
		runtime, err = c.contextProvider.RequestContext(ctx, request.AccountID)
		if err != nil {
			return nil, fmt.Errorf("读取 AI Studio 请求上下文: %w", err)
		}
	}
	body, err := EncodeGenerateContentRequest(request, entry.defaults, runtime)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	response, err := c.doProtected(ctx, request, body)
	if err != nil {
		return nil, err
	}
	events := make(chan Event, 8)
	go func() {
		defer close(events)
		stopClose := context.AfterFunc(ctx, func() {
			_ = response.Body.Close()
		})
		defer stopClose()
		decoder := NewFrameDecoder()
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
		emit := func(event Event) error {
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
				return send(event)
			}
		}
		err := DecodeGenerateStream(observeStreamActivity(ctx, response.Body), decoder, emit)
		if err == nil {
			err = decoder.End()
		}
		if closeErr := response.Body.Close(); err == nil {
			err = closeErr
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
	return events, nil
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
