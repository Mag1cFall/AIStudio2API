package aistudio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	thinkingBudget := config.ThinkingBudget
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
		if thinkingBudget == nil || !defaults.ThinkingBudget {
			return nil, fmt.Errorf("模型不支持 thinking level")
		}
		hasReasoningEffort = false
	}
	if thinkingBudget != nil && !defaults.ThinkingBudget {
		if !hasReasoningEffort || !defaults.ThinkingLevel {
			return nil, fmt.Errorf("模型不支持 thinking budget")
		}
		thinkingBudget = nil
	}
	audioOnly := len(config.ResponseModalities) == 1 &&
		strings.EqualFold(strings.TrimSpace(string(config.ResponseModalities[0])), string(ResponseModalityAudio))
	includeMaxOutput := config.MaxOutputTokens != nil || !audioOnly
	maxOutput := defaults.MaxOutputTokens
	if config.MaxOutputTokens != nil {
		maxOutput = *config.MaxOutputTokens
	}
	if includeMaxOutput && maxOutput <= 0 {
		return nil, fmt.Errorf("模型目录缺少有效 output token limit")
	}
	if includeMaxOutput && maxOutput > defaults.MaxOutputTokens {
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
	transcriptionConfig, err := encodeTranscriptionConfig(config.TranscriptionConfig)
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

func applySpeechTranscript(contents []Content, model Model, config GenerationConfig) []Content {
	if !model.Capabilities["speech_route"] || config.SpeechConfig == nil {
		return contents
	}
	result := append([]Content(nil), contents...)
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
	if err := validateRequestedTools(request.Tools, entry.model); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if err := validateTranscriptionConfig(request.Config.TranscriptionConfig, entry.model); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	request.Config = applyModelMediaDefaults(request.Config, entry.model)
	request.Contents = applySpeechTranscript(request.Contents, entry.model, request.Config)
	runtime := RequestContext{}
	if c.contextProvider != nil {
		runtime, err = c.contextProvider.RequestContext(ctx, request.AccountID)
		if err != nil {
			return nil, fmt.Errorf("读取 AI Studio 请求上下文: %w", err)
		}
	}
	wireRequest := request
	wireRequest.Config.StopSequences = nil
	body, err := EncodeGenerateContentRequest(wireRequest, entry.defaults, runtime)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	response, err := c.doProtected(ctx, request, body)
	if err != nil {
		return nil, err
	}
	matcher := newStopSequenceMatcher(request.Config.StopSequences)
	var stopTokenCount <-chan tokenCountResult
	cancelTokenCount := func() {}
	if matcher != nil {
		countContext, cancel := context.WithCancel(ctx)
		cancelTokenCount = cancel
		results := make(chan tokenCountResult, 1)
		stopTokenCount = results
		countRequest := TokenCountRequest{
			Model: request.Model, System: request.System, Contents: request.Contents, Tools: request.Tools,
		}
		go func() {
			count, countErr := c.CountTokensForAccount(countContext, request.AccountID, countRequest)
			results <- tokenCountResult{count: count, err: countErr}
			close(results)
		}()
	}
	events := make(chan Event, 8)
	go func() {
		defer close(events)
		defer cancelTokenCount()
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
				return send(event)
			}
		}
		emit := func(event Event) error {
			if matcher == nil {
				return emitEvent(event)
			}
			if pending := matcher.boundary(event.Kind); pending != "" {
				if err := emitEvent(Event{Kind: EventText, Text: pending, ProviderModel: event.ProviderModel}); err != nil {
					return err
				}
			}
			if event.Kind != EventText {
				return emitEvent(event)
			}
			text, matched := matcher.write(event.Text)
			if text != "" {
				event.Text = text
				if err := emitEvent(event); err != nil {
					return err
				}
			}
			if matched != "" {
				matchedStopSequence = matched
				return errStopSequenceMatched
			}
			return nil
		}
		err := DecodeGenerateStream(observeStreamActivity(ctx, response.Body), decoder, emit)
		if errors.Is(err, errStopSequenceMatched) {
			_ = response.Body.Close()
			select {
			case result := <-stopTokenCount:
				if result.err == nil {
					usage = countedCompleteUsage(request, output, result.count)
					if err := send(Event{Kind: EventUsage, Usage: usage}); err != nil {
						return
					}
				}
			case <-ctx.Done():
				return
			}
			_ = send(Event{Kind: EventFinish, FinishReason: "stop_sequence", StopSequence: matchedStopSequence})
			return
		}
		if err == nil {
			err = decoder.End()
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
