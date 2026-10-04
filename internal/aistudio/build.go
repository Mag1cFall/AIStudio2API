package aistudio

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// buildProxyStreamedMethod 表示 Build 应用流式代理 RPC
const buildProxyStreamedMethod = "ProxyStreamedCall"

// buildProxyUnaryMethod 表示 Build 应用单次代理 RPC
const buildProxyUnaryMethod = "ProxyUnaryCall"

// buildProofField 表示 ProxyRequest 中 WAA proof 的 protobuf field
const buildProofField = 3

// buildThinkingLevels 把 thinking level 枚举换算为 Gemini API 名称
var buildThinkingLevels = map[int64]string{1: "LOW", 2: "MEDIUM", 3: "HIGH", 4: "MINIMAL"}

// buildSafetyCategories 为官网 Playground 关闭过滤的四个安全类别
var buildSafetyCategories = []string{
	"HARM_CATEGORY_HARASSMENT", "HARM_CATEGORY_HATE_SPEECH", "HARM_CATEGORY_SEXUALLY_EXPLICIT", "HARM_CATEGORY_DANGEROUS_CONTENT",
}

// buildModelDefaults 返回 Build 独有模型按目录推导的生成默认值
func buildModelDefaults(model Model) GenerationDefaults {
	thinking := model.Capabilities["thinking"]
	return GenerationDefaults{MaxOutputTokens: model.OutputTokenLimit, Thinking: thinking, ThinkingLevel: thinking}
}

// EncodeBuildGenerateRequest 编码 Build 代理转发的 Gemini API 路径与 JSON 请求体，unary 使用 generateContent
func EncodeBuildGenerateRequest(request GenerateRequest, defaults GenerationDefaults, imageRoute bool, unary bool) (string, []byte, error) {
	model := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	if model == "" {
		return "", nil, fmt.Errorf("Build 请求缺少模型")
	}
	contents, err := encodeBuildContents(request.Contents)
	if err != nil {
		return "", nil, err
	}
	if len(contents) == 0 {
		return "", nil, fmt.Errorf("GenerateContent contents 不能为空")
	}
	body := map[string]any{"contents": contents}
	if system := strings.TrimSpace(request.System); system != "" {
		body["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": request.System}}}
	}
	tools, serverSide, err := encodeBuildTools(request.Tools)
	if err != nil {
		return "", nil, err
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	if serverSide {
		body["toolConfig"] = map[string]any{"includeServerSideToolInvocations": true}
	}
	config, err := encodeBuildGenerationConfig(request.Config, defaults)
	if err != nil {
		return "", nil, err
	}
	if len(config) > 0 {
		body["generationConfig"] = config
	}
	if !imageRoute {
		settings := make([]any, 0, len(buildSafetyCategories))
		for _, category := range buildSafetyCategories {
			settings = append(settings, map[string]any{"category": category, "threshold": "OFF"})
		}
		body["safetySettings"] = settings
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", nil, fmt.Errorf("编码 Build 请求: %w", err)
	}
	if unary {
		return "/v1beta/models/" + model + ":generateContent", encoded, nil
	}
	return "/v1beta/models/" + model + ":streamGenerateContent", encoded, nil
}

// EncodeBuildProxyRequest 编码代理请求：ProxyStreamedCall 为 [路径, 请求体, WAA proof]，ProxyUnaryCall 追加 HTTP 方法
func EncodeBuildProxyRequest(path string, body []byte, unary bool) ([]byte, error) {
	if unary {
		return json.Marshal([]any{path, string(body), nil, http.MethodPost})
	}
	return json.Marshal([]any{path, string(body), nil})
}

// buildUsesUnary 判断模型是否需要订阅权益；权益头只随 ProxyUnaryCall 发送，这类模型经单次代理调用
func buildUsesUnary(model Model) bool {
	return len(model.AccessModes) > 0 && !modelAllowedByTier(model, BenefitTierFree)
}

// buildBindingPrompt 从 Build 代理请求体取出 WAA binding：路径与请求体以空格连接
func buildBindingPrompt(body []byte) (string, error) {
	var wire []any
	if err := json.Unmarshal(body, &wire); err != nil || len(wire) < 2 {
		return "", fmt.Errorf("Build 代理请求不是 [路径, 请求体] 数组")
	}
	path, pathOK := wire[0].(string)
	request, requestOK := wire[1].(string)
	if !pathOK || !requestOK {
		return "", fmt.Errorf("Build 代理请求路径或请求体不是字符串")
	}
	return path + " " + request, nil
}

func encodeBuildContents(contents []Content) ([]any, error) {
	wire := make([]any, 0, len(contents))
	var pendingCalls []FunctionCall
	for index, content := range contents {
		if len(content.Parts) == 0 {
			continue
		}
		if content.Role == RoleUser && !slices.ContainsFunc(content.Parts, func(part Part) bool { return part.FunctionResult != nil }) {
			pendingCalls = nil
		}
		content = attachYouTubeMedia(content)
		role := "user"
		switch content.Role {
		case RoleUser, RoleTool:
		case RoleAssistant:
			role = "model"
		default:
			return nil, fmt.Errorf("未知 content role %q", content.Role)
		}
		parts := make([]any, 0, len(content.Parts))
		for partIndex, part := range content.Parts {
			if part.FunctionCall != nil {
				pendingCalls = append(pendingCalls, *part.FunctionCall)
			}
			if result := part.FunctionResult; result != nil && result.Name == "" {
				matched := slices.IndexFunc(pendingCalls, func(call FunctionCall) bool { return result.ID != "" && call.ID == result.ID })
				if matched < 0 && len(pendingCalls) == 1 {
					matched = 0
				}
				if matched >= 0 {
					part.FunctionResult = cloneFunctionResult(result)
					part.FunctionResult.Name = pendingCalls[matched].Name
				}
			}
			encoded, err := encodeBuildPart(part)
			if err != nil {
				return nil, fmt.Errorf("编码 content %d part %d: %w", index, partIndex, err)
			}
			parts = append(parts, encoded)
		}
		wire = append(wire, map[string]any{"role": role, "parts": parts})
	}
	return wire, nil
}

func encodeBuildPart(part Part) (map[string]any, error) {
	wire := map[string]any{}
	signature := part.ThoughtSignature
	switch {
	case part.Text != "":
		wire["text"] = part.Text
		if metadata := part.SpeechMetadata; metadata != nil && (metadata.Speaker != "" || metadata.Style != "") {
			speech := map[string]any{}
			if metadata.Speaker != "" {
				speech["speaker"] = metadata.Speaker
			}
			if metadata.Style != "" {
				speech["style"] = metadata.Style
			}
			wire["speechMetadata"] = speech
		}
		if part.Thought {
			wire["thought"] = true
		}
	case part.InlineData != nil:
		if part.InlineData.MIME == "" || len(part.InlineData.Data) == 0 {
			return nil, fmt.Errorf("inline data 缺少 MIME 或数据")
		}
		wire["inlineData"] = map[string]any{"mimeType": part.InlineData.MIME, "data": base64.StdEncoding.EncodeToString(part.InlineData.Data)}
	case part.ExternalMedia != nil:
		if part.ExternalMedia.MIME == "" || part.ExternalMedia.URL == "" {
			return nil, fmt.Errorf("外部媒体缺少 MIME 或 URL")
		}
		wire["fileData"] = map[string]any{"mimeType": part.ExternalMedia.MIME, "fileUri": part.ExternalMedia.URL}
	case part.File != nil:
		return nil, fmt.Errorf("Build 通道不接受 Drive 文件引用")
	case part.FunctionCall != nil:
		if err := validateFunctionCall(part.FunctionCall); err != nil {
			return nil, err
		}
		arguments, err := buildJSONObject(part.FunctionCall.Arguments)
		if err != nil {
			return nil, fmt.Errorf("function call arguments: %w", err)
		}
		call := map[string]any{"name": part.FunctionCall.Name, "args": arguments}
		if part.FunctionCall.ID != "" {
			call["id"] = part.FunctionCall.ID
		}
		wire["functionCall"] = call
		if signature == "" {
			signature = part.FunctionCall.ThoughtSignature
		}
		if signature == "" {
			signature = "skip_thought_signature_validator"
		}
	case part.FunctionResult != nil:
		if part.FunctionResult.Name == "" {
			return nil, fmt.Errorf("function result 缺少名称且无法唯一关联调用")
		}
		response, err := buildJSONObject(part.FunctionResult.Content)
		if err != nil {
			return nil, fmt.Errorf("function result content: %w", err)
		}
		result := map[string]any{"name": part.FunctionResult.Name, "response": response}
		if part.FunctionResult.ID != "" {
			result["id"] = part.FunctionResult.ID
		}
		wire["functionResponse"] = result
	case part.ExecutableCode != nil:
		wire["executableCode"] = map[string]any{"language": part.ExecutableCode.Language, "code": part.ExecutableCode.Code}
	case part.CodeExecutionResult != nil:
		output := part.CodeExecutionResult.Output
		if part.CodeExecutionResult.Outcome != "OUTCOME_OK" {
			output = part.CodeExecutionResult.Error
		}
		result := map[string]any{"outcome": part.CodeExecutionResult.Outcome}
		if output != "" {
			result["output"] = output
		}
		wire["codeExecutionResult"] = result
	case signature != "":
		wire["text"] = ""
	default:
		return nil, fmt.Errorf("part 必须且只能设置一种内容")
	}
	if signature != "" {
		wire["thoughtSignature"] = signature
	}
	return wire, nil
}

// buildJSONObject 把 JSON 值转成 Gemini API Struct，非对象值放在 result 字段
func buildJSONObject(raw json.RawMessage) (any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return map[string]any{}, nil
	}
	var value any
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return nil, err
	}
	if object, ok := value.(map[string]any); ok {
		return object, nil
	}
	return map[string]any{"result": value}, nil
}

func encodeBuildTools(tools Tools) ([]any, bool, error) {
	switch tools.ToolConfig.Mode {
	case "none":
		return nil, false, nil
	case "", "auto":
	default:
		return nil, false, fmt.Errorf("tool choice 只支持 auto 或 none")
	}
	var wire []any
	if len(tools.Functions) > 0 {
		declarations := make([]any, 0, len(tools.Functions))
		for index, declaration := range tools.Functions {
			if declaration.Name == "" {
				return nil, false, fmt.Errorf("function declaration %d 缺少名称", index)
			}
			encoded := map[string]any{"name": declaration.Name}
			if declaration.Description != "" {
				encoded["description"] = declaration.Description
			}
			if len(bytes.TrimSpace(declaration.Parameters)) > 0 {
				parameters, err := normalizeFunctionParameters(declaration.Parameters)
				if err != nil {
					return nil, false, fmt.Errorf("function declaration %d parameters: %w", index, err)
				}
				encoded["parametersJsonSchema"] = parameters
			}
			declarations = append(declarations, encoded)
		}
		wire = append(wire, map[string]any{"functionDeclarations": declarations})
	}
	search := GoogleSearchOptions{}
	searchRequested := tools.GoogleSearch != nil
	if tools.GoogleSearch != nil {
		search = *tools.GoogleSearch
	}
	serverSide := false
	for _, name := range tools.Google {
		switch name {
		case "google_search", "image_search":
			searchRequested = true
			search.WebSearch = search.WebSearch || name == "google_search"
			search.ImageSearch = search.ImageSearch || name == "image_search"
		case "code_execution":
			wire = append(wire, map[string]any{"codeExecution": map[string]any{}})
		case "url_context":
			wire = append(wire, map[string]any{"urlContext": map[string]any{}})
		case "google_maps":
			wire = append(wire, map[string]any{"googleMaps": map[string]any{}})
		default:
			return nil, false, fmt.Errorf("未知 Google tool %q", name)
		}
		serverSide = true
	}
	if searchRequested {
		serverSide = true
		options := map[string]any{}
		if search.ImageSearch {
			types := map[string]any{"imageSearch": map[string]any{}}
			if search.WebSearch {
				types["webSearch"] = map[string]any{}
			}
			options["searchTypes"] = types
		}
		if search.TimeRange != nil {
			timeRange := map[string]any{}
			if !search.TimeRange.StartTime.IsZero() {
				timeRange["startTime"] = search.TimeRange.StartTime.UTC().Format(time.RFC3339)
			}
			if !search.TimeRange.EndTime.IsZero() {
				timeRange["endTime"] = search.TimeRange.EndTime.UTC().Format(time.RFC3339)
			}
			options["timeRangeFilter"] = timeRange
		}
		wire = append(wire, map[string]any{"googleSearch": options})
	}
	return wire, serverSide && len(tools.Functions) > 0, nil
}

// encodeBuildGenerationConfig 复用 Playground 的参数校验与默认值，并转换为 Gemini API 字段
func encodeBuildGenerationConfig(config GenerationConfig, defaults GenerationDefaults) (map[string]any, error) {
	wire, err := encodeGenerationConfig(config, defaults)
	if err != nil {
		return nil, err
	}
	encoded := map[string]any{}
	if len(config.StopSequences) > 0 {
		encoded["stopSequences"] = config.StopSequences
	}
	fields := []struct {
		index int
		name  string
	}{{3, "maxOutputTokens"}, {4, "temperature"}, {5, "topP"}, {6, "topK"}, {18, "seed"}}
	for _, field := range fields {
		if field.index < len(wire) && wire[field.index] != nil {
			encoded[field.name] = wire[field.index]
		}
	}
	if config.ResponseMIMEType != "" {
		encoded["responseMimeType"] = config.ResponseMIMEType
	}
	if wire[8] != nil {
		encoded["responseSchema"] = buildResponseSchema(wire[8].([]any))
	}
	if config.ResponseModalities != nil {
		modalities := make([]string, 0, len(config.ResponseModalities))
		for _, modality := range config.ResponseModalities {
			modalities = append(modalities, strings.ToUpper(strings.TrimSpace(string(modality))))
		}
		encoded["responseModalities"] = modalities
	}
	if len(wire) > 26 && wire[26] != nil {
		// 与 Playground 相同的图片配置，含可设置分辨率模型的默认 1K
		image := wire[26].([]any)
		imageConfig := map[string]any{}
		if aspect, ok := image[0].(string); ok && aspect != "" {
			imageConfig["aspectRatio"] = aspect
		}
		if len(image) > 1 {
			if size, ok := image[1].(string); ok && size != "" {
				imageConfig["imageSize"] = size
			}
		}
		encoded["imageConfig"] = imageConfig
	}
	if speech := config.SpeechConfig; speech != nil {
		voice := func(name string) map[string]any {
			return map[string]any{"prebuiltVoiceConfig": map[string]any{"voiceName": name}}
		}
		speechConfig := map[string]any{}
		if name := strings.TrimSpace(speech.VoiceName); name != "" {
			speechConfig["voiceConfig"] = voice(name)
		}
		if len(speech.Speakers) > 0 {
			speakers := make([]any, 0, len(speech.Speakers))
			for _, speaker := range speech.Speakers {
				speakers = append(speakers, map[string]any{"speaker": speaker.Speaker, "voiceConfig": voice(speaker.VoiceName)})
			}
			speechConfig["multiSpeakerVoiceConfig"] = map[string]any{"speakerVoiceConfigs": speakers}
		}
		encoded["speechConfig"] = speechConfig
	}
	if len(wire) > 16 && wire[16] != nil {
		thinking := wire[16].([]any)
		thinkingConfig := map[string]any{"includeThoughts": true}
		if len(thinking) > 1 && thinking[1] != nil {
			thinkingConfig["thinkingBudget"] = thinking[1]
		}
		if len(thinking) > 3 && thinking[3] != nil {
			if name, ok := buildThinkingLevels[thinking[3].(int64)]; ok {
				thinkingConfig["thinkingLevel"] = name
			}
		}
		encoded["thinkingConfig"] = thinkingConfig
	}
	return encoded, nil
}

// buildResponse 表示 Gemini API GenerateContentResponse 的已接入字段
type buildResponse struct {
	Candidates []struct {
		Content *struct {
			Parts []buildResponsePart `json:"parts"`
		} `json:"content"`
		FinishReason     string `json:"finishReason"`
		CitationMetadata *struct {
			CitationSources []struct {
				URI        string `json:"uri"`
				Title      string `json:"title"`
				StartIndex int    `json:"startIndex"`
				EndIndex   int    `json:"endIndex"`
			} `json:"citationSources"`
		} `json:"citationMetadata"`
		GroundingMetadata *buildGrounding `json:"groundingMetadata"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata *struct {
		PromptTokenCount        int64  `json:"promptTokenCount"`
		CandidatesTokenCount    *int64 `json:"candidatesTokenCount"`
		TotalTokenCount         int64  `json:"totalTokenCount"`
		ThoughtsTokenCount      int64  `json:"thoughtsTokenCount"`
		ToolUsePromptTokenCount int64  `json:"toolUsePromptTokenCount"`
	} `json:"usageMetadata"`
}

type buildResponsePart struct {
	Text             *string `json:"text"`
	Thought          bool    `json:"thought"`
	ThoughtSignature string  `json:"thoughtSignature"`
	InlineData       *struct {
		MIMEType string `json:"mimeType"`
		Data     string `json:"data"`
	} `json:"inlineData"`
	ExecutableCode      *ExecutableCode `json:"executableCode"`
	CodeExecutionResult *struct {
		Outcome string `json:"outcome"`
		Output  string `json:"output"`
	} `json:"codeExecutionResult"`
	FunctionCall *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
}

type buildGrounding struct {
	WebSearchQueries []string `json:"webSearchQueries"`
	SearchEntryPoint *struct {
		RenderedContent string `json:"renderedContent"`
		SDKBlob         string `json:"sdkBlob"`
	} `json:"searchEntryPoint"`
	GroundingChunks   []map[string]json.RawMessage `json:"groundingChunks"`
	GroundingSupports []struct {
		Segment struct {
			PartIndex  int    `json:"partIndex"`
			StartIndex int    `json:"startIndex"`
			EndIndex   int    `json:"endIndex"`
			Text       string `json:"text"`
		} `json:"segment"`
		GroundingChunkIndices []int     `json:"groundingChunkIndices"`
		ConfidenceScores      []float64 `json:"confidenceScores"`
	} `json:"groundingSupports"`
	GoogleMapsWidgetContextToken string `json:"googleMapsWidgetContextToken"`
}

// buildChunkSources 把 Gemini API grounding chunk 字段映射为规范来源
var buildChunkSources = map[string]string{"web": "web", "retrievedContext": "retrieved_context", "maps": "maps"}

// BuildStreamDecoder 把 Build 代理返回的 Gemini API 响应块转换为规范事件
type BuildStreamDecoder struct {
	usage    *Usage
	finished bool
}

// Decode 解码一个 Gemini API GenerateContentResponse JSON
func (d *BuildStreamDecoder) Decode(raw []byte) ([]Event, error) {
	var response buildResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, &ProtocolEvidenceError{Method: buildProxyStreamedMethod, Path: "$chunk", Detail: err.Error(), Raw: cloneRaw(raw)}
	}
	if response.UsageMetadata != nil && response.UsageMetadata.TotalTokenCount > 0 {
		metadata := response.UsageMetadata
		usage := Usage{
			InputTokens: metadata.PromptTokenCount, ReasoningTokens: metadata.ThoughtsTokenCount,
			ToolTokens: metadata.ToolUsePromptTokenCount, TotalTokens: metadata.TotalTokenCount,
		}
		if metadata.CandidatesTokenCount != nil {
			usage.OutputTokens = *metadata.CandidatesTokenCount
		} else {
			usage.OutputTokens = max(0, usage.TotalTokens-usage.InputTokens-usage.ReasoningTokens-usage.ToolTokens)
		}
		d.usage = &usage
	}
	if len(response.Candidates) == 0 {
		if response.PromptFeedback != nil && response.PromptFeedback.BlockReason != "" {
			return nil, &PromptFeedbackError{Reason: buildEnumName(response.PromptFeedback.BlockReason, "BLOCK_REASON_"), Raw: cloneRaw(raw)}
		}
		return nil, nil
	}
	if len(response.Candidates) != 1 {
		return nil, &ProtocolEvidenceError{Method: buildProxyStreamedMethod, Path: "$chunk.candidates", Detail: "候选数量不是 1", Raw: cloneRaw(raw)}
	}
	candidate := response.Candidates[0]
	var events []Event
	if candidate.Content != nil {
		for index, part := range candidate.Content.Parts {
			partEvents, err := decodeBuildPart(part, index, raw)
			if err != nil {
				return nil, err
			}
			events = append(events, partEvents...)
		}
	}
	if candidate.CitationMetadata != nil {
		for _, source := range candidate.CitationMetadata.CitationSources {
			if source.URI == "" {
				continue
			}
			citation := Citation{URL: source.URI, Title: source.Title, Start: source.StartIndex, End: source.EndIndex}
			events = append(events, Event{Kind: EventCitation, Citation: &citation})
		}
	}
	if grounding := candidate.GroundingMetadata; grounding != nil {
		events = append(events, Event{Kind: EventGrounding, Grounding: decodeBuildGrounding(*grounding)})
	}
	if candidate.FinishReason != "" && !d.finished {
		if d.usage != nil {
			usage := *d.usage
			events = append(events, Event{Kind: EventUsage, Usage: &usage})
		}
		events = append(events, Event{Kind: EventFinish, FinishReason: buildEnumName(candidate.FinishReason, "FINISH_REASON_")})
		d.finished = true
	}
	return events, nil
}

// End 校验流已经出现 finishReason
func (d *BuildStreamDecoder) End() error {
	if d.finished {
		return nil
	}
	return &ProtocolEvidenceError{Method: buildProxyStreamedMethod, Path: "$", Detail: "流结束前没有 finishReason"}
}

// buildEnumName 把 Gemini API 枚举名转换为规范小写名称
func buildEnumName(value string, prefix string) string {
	value = strings.TrimPrefix(value, prefix)
	if value == "UNSPECIFIED" {
		return "unspecified"
	}
	return strings.ToLower(value)
}

func decodeBuildPart(part buildResponsePart, index int, raw []byte) ([]Event, error) {
	signature := part.ThoughtSignature
	var events []Event
	if part.Text != nil && *part.Text != "" {
		kind := EventText
		if part.Thought {
			kind = EventReasoning
		}
		events = append(events, Event{Kind: kind, Text: *part.Text, ThoughtSignature: signature})
	}
	if part.InlineData != nil && !part.Thought {
		data, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
		if err != nil {
			return nil, &ProtocolEvidenceError{Method: buildProxyStreamedMethod, Path: fmt.Sprintf("$chunk.parts[%d].inlineData", index), Detail: "inline data 不是 Base64", Raw: cloneRaw(raw)}
		}
		events = append(events, Event{Kind: EventMedia, Media: &Media{MIME: part.InlineData.MIMEType, Data: data}, ThoughtSignature: signature})
	}
	if part.ExecutableCode != nil {
		code := *part.ExecutableCode
		events = append(events, Event{Kind: EventExecutableCode, ExecutableCode: &code, ThoughtSignature: signature})
	}
	if part.CodeExecutionResult != nil {
		result := CodeExecutionResult{Outcome: part.CodeExecutionResult.Outcome}
		if result.Outcome == "OUTCOME_OK" {
			result.Output = part.CodeExecutionResult.Output
		} else {
			result.Error = part.CodeExecutionResult.Output
		}
		events = append(events, Event{Kind: EventCodeExecutionResult, CodeExecutionResult: &result, ThoughtSignature: signature})
	}
	if part.FunctionCall != nil {
		arguments := part.FunctionCall.Args
		if len(bytes.TrimSpace(arguments)) == 0 {
			arguments = json.RawMessage(`{}`)
		}
		call := FunctionCall{ID: part.FunctionCall.ID, Name: part.FunctionCall.Name, Arguments: arguments, ThoughtSignature: signature}
		events = append(events, Event{Kind: EventToolCall, ToolCall: &call, ThoughtSignature: signature})
	}
	if len(events) == 0 && signature != "" {
		events = append(events, Event{Kind: EventThoughtSignature, ThoughtSignature: signature})
	}
	return events, nil
}

func decodeBuildGrounding(value buildGrounding) *GroundingMetadata {
	metadata := &GroundingMetadata{
		WebSearchQueries: value.WebSearchQueries, MapsWidgetContextToken: value.GoogleMapsWidgetContextToken,
	}
	if value.SearchEntryPoint != nil {
		metadata.SearchEntryPoint = &SearchEntryPoint{RenderedContent: value.SearchEntryPoint.RenderedContent, SDKBlob: value.SearchEntryPoint.SDKBlob}
	}
	for _, chunk := range value.GroundingChunks {
		for _, key := range []string{"web", "retrievedContext", "maps"} {
			raw, exists := chunk[key]
			if !exists {
				continue
			}
			var fields struct {
				URI     string `json:"uri"`
				Title   string `json:"title"`
				Text    string `json:"text"`
				PlaceID string `json:"placeId"`
			}
			if json.Unmarshal(raw, &fields) != nil {
				continue
			}
			metadata.Chunks = append(metadata.Chunks, GroundingChunk{
				Source: buildChunkSources[key], URI: fields.URI, Title: fields.Title, Text: fields.Text, PlaceID: fields.PlaceID,
			})
		}
	}
	for _, support := range value.GroundingSupports {
		metadata.Supports = append(metadata.Supports, GroundingSupport{
			Segment: GroundingSegment{
				PartIndex: support.Segment.PartIndex, StartIndex: support.Segment.StartIndex,
				EndIndex: support.Segment.EndIndex, Text: support.Segment.Text,
			},
			ChunkIndices: support.GroundingChunkIndices, ConfidenceScores: support.ConfidenceScores,
		})
	}
	return metadata
}

// DecodeBuildStream 解码 ProxyStreamedCall 响应：field 1 为 [["<JSON>"]...]，其后可带 google.rpc 状态
func DecodeBuildStream(source io.Reader, decoder *BuildStreamDecoder, emit func(Event) error) error {
	stream := json.NewDecoder(newSparseJSONReader(source))
	stream.UseNumber()
	if token, err := stream.Token(); err != nil || token != json.Delim('[') {
		return &ProtocolEvidenceError{Method: buildProxyStreamedMethod, Path: "$", Detail: "根值不是数组"}
	}
	if !stream.More() {
		return &ProtocolEvidenceError{Method: buildProxyStreamedMethod, Path: "$", Detail: "根数组缺少响应列表"}
	}
	if token, err := stream.Token(); err != nil || token != json.Delim('[') {
		return &ProtocolEvidenceError{Method: buildProxyStreamedMethod, Path: "$[0]", Detail: "响应列表不是数组"}
	}
	for index := 0; stream.More(); index++ {
		var raw json.RawMessage
		if err := stream.Decode(&raw); err != nil {
			return &ProtocolEvidenceError{Method: buildProxyStreamedMethod, Path: fmt.Sprintf("$[0][%d]", index), Detail: err.Error()}
		}
		chunk, err := buildProxyResponseBody(raw, fmt.Sprintf("$[0][%d]", index))
		if err != nil {
			return err
		}
		events, err := decoder.Decode(chunk)
		if err != nil {
			return err
		}
		for _, event := range events {
			if err := emit(event); err != nil {
				return err
			}
		}
	}
	if token, err := stream.Token(); err != nil || token != json.Delim(']') {
		return &ProtocolEvidenceError{Method: buildProxyStreamedMethod, Path: "$[0]", Detail: "响应列表没有正常结束"}
	}
	for stream.More() {
		var raw json.RawMessage
		if err := stream.Decode(&raw); err != nil {
			return &ProtocolEvidenceError{Method: buildProxyStreamedMethod, Path: "$", Detail: err.Error()}
		}
		if err := buildTrailerError(raw); err != nil {
			return err
		}
	}
	return decoder.End()
}

// DecodeBuildUnary 解码 ProxyUnaryCall 响应：field 1 为一个 Gemini API GenerateContentResponse JSON
func DecodeBuildUnary(source io.Reader, decoder *BuildStreamDecoder, emit func(Event) error) error {
	raw, err := io.ReadAll(source)
	if err != nil {
		return fmt.Errorf("读取 AI Studio %s: %w", buildProxyUnaryMethod, err)
	}
	decoded, err := decodeJSONValue(raw)
	if err != nil {
		return &ProtocolEvidenceError{Method: buildProxyUnaryMethod, Path: "$", Detail: err.Error(), Raw: cloneRaw(raw)}
	}
	content, err := buildProxyResponseBody(decoded, "$")
	if err != nil {
		return err
	}
	events, err := decoder.Decode(content)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := emit(event); err != nil {
			return err
		}
	}
	return decoder.End()
}

// buildProxyResponseBody 读取 ProxyResponse oneof：field 1 为 JSON 字符串，field 3 为 Base64 字节
func buildProxyResponseBody(raw json.RawMessage, path string) ([]byte, error) {
	values, err := rawArray(raw, path, raw)
	if err != nil {
		return nil, withMethod(err, buildProxyStreamedMethod)
	}
	if text := rawAt(values, 0); !isJSONNull(text) {
		value, err := rawString(text, path+"[0]", raw)
		if err != nil {
			return nil, withMethod(err, buildProxyStreamedMethod)
		}
		return []byte(value), nil
	}
	if encoded := rawAt(values, 2); !isJSONNull(encoded) {
		value, err := rawString(encoded, path+"[2]", raw)
		if err != nil {
			return nil, withMethod(err, buildProxyStreamedMethod)
		}
		data, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, &ProtocolEvidenceError{Method: buildProxyStreamedMethod, Path: path + "[2]", Detail: "响应字节不是 Base64", Raw: cloneRaw(raw)}
		}
		return data, nil
	}
	return nil, &ProtocolEvidenceError{Method: buildProxyStreamedMethod, Path: path, Detail: "代理响应缺少正文", Raw: cloneRaw(raw)}
}

func buildTrailerError(raw json.RawMessage) error {
	status, err := rawArray(raw, "$[1]", raw)
	if err != nil || len(status) == 0 {
		return &ProtocolEvidenceError{Method: buildProxyStreamedMethod, Path: "$[1]", Detail: "流尾状态无效", Raw: cloneRaw(raw)}
	}
	code, err := rawInt64(status[0], "$[1][0]", raw)
	if err != nil {
		return withMethod(err, buildProxyStreamedMethod)
	}
	if code == 0 {
		return nil
	}
	statusCode := http.StatusBadGateway
	if mapped, ok := interactionStatusHTTP[code]; ok {
		statusCode = mapped
	}
	return DecodeRPCError(buildProxyStreamedMethod, statusCode, raw)
}

// sendBuild 编码并发送 Build 代理请求，返回响应与含 finishReason 校验的解码
func (c *Client) sendBuild(ctx context.Context, request GenerateRequest, entry modelEntry) (*RPCResponse, func(io.Reader, func(Event) error) error, error) {
	unary := request.Unary || buildUsesUnary(entry.model)
	response, decoder, err := c.sendBuildMode(ctx, request, entry, unary, "")
	var rpcErr *RPCError
	if err != nil && request.Unary && !buildUsesUnary(entry.model) && ctx.Err() == nil && errors.As(err, &rpcErr) {
		message := strings.ToLower(rpcErr.Message)
		unsupported := rpcErr.StatusCode == http.StatusMethodNotAllowed || rpcErr.StatusCode == http.StatusNotImplemented ||
			rpcErr.Code == 12 || rpcErr.StatusCode == http.StatusBadRequest && strings.Contains(message, "support") && (strings.Contains(message, "stream") || strings.Contains(message, "unary"))
		if unsupported {
			return c.sendBuildMode(ctx, request, entry, false, "Build 原生单次调用不可用: "+rpcErr.Error())
		}
	}
	return response, decoder, err
}

// sendBuildMode 按选定模式发送 Build 请求
func (c *Client) sendBuildMode(ctx context.Context, request GenerateRequest, entry modelEntry, unary bool, reason string) (*RPCResponse, func(io.Reader, func(Event) error) error, error) {
	path, body, err := EncodeBuildGenerateRequest(request, entry.defaults, request.ImageRoute, unary)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	proxy, err := EncodeBuildProxyRequest(path, body, unary)
	if err != nil {
		return nil, nil, fmt.Errorf("编码 Build 代理请求: %w", err)
	}
	method := buildProxyStreamedMethod
	if unary {
		method = buildProxyUnaryMethod
	}
	mode := "stream"
	if unary {
		mode = "native"
	}
	reportUpstreamMode(ctx, method, mode, reason)
	rpc := newRPCRequest(method, request.AccountID, request.ID, proxy, !unary)
	c.applyBenefitTier(rpc.Method, request.AccountID, rpc.Header)
	response, err := c.protected.DoProtected(ctx, request, rpc)
	if err != nil {
		return nil, nil, fmt.Errorf("发送 AI Studio %s: %w", method, err)
	}
	response, err = validateRPCResponse(method, response)
	if err != nil {
		return nil, nil, err
	}
	decoder := &BuildStreamDecoder{}
	if unary {
		return response, func(source io.Reader, emit func(Event) error) error {
			return DecodeBuildUnary(source, decoder, emit)
		}, nil
	}
	return response, func(source io.Reader, emit func(Event) error) error {
		return DecodeBuildStream(source, decoder, emit)
	}, nil
}

// buildModelEntry 返回 Build 请求使用的目录条目：Playground 目录模型复用官网默认值，Build 独有模型按 Build 目录推导
func (c *Client) buildModelEntry(ctx context.Context, request GenerateRequest, lease *AccountLease) (modelEntry, error) {
	entry, err := c.modelEntry(ctx, request.AccountID, request.Model)
	if err == nil || !errors.Is(err, ErrModelNotFound) {
		return entry, err
	}
	model, ok := lease.buildModel(request.Model)
	if !ok {
		return modelEntry{}, err
	}
	return modelEntry{model: model, defaults: buildModelDefaults(model)}, nil
}

// buildModel 返回租约账户 Build 目录中的模型
func (l *AccountLease) buildModel(modelID string) (Model, bool) {
	if l == nil || l.pool == nil || l.account == nil {
		return Model{}, false
	}
	modelID = strings.TrimPrefix(strings.TrimSpace(modelID), "models/")
	l.pool.mu.Lock()
	defer l.pool.mu.Unlock()
	for _, model := range l.account.buildModels {
		if model.ID == modelID {
			return cloneAccountModels([]Model{model})[0], true
		}
	}
	return Model{}, false
}

// buildListedModel 表示 Gemini API models.list 的模型条目
type buildListedModel struct {
	Name                       string   `json:"name"`
	DisplayName                string   `json:"displayName"`
	Description                string   `json:"description"`
	InputTokenLimit            int64    `json:"inputTokenLimit"`
	OutputTokenLimit           int64    `json:"outputTokenLimit"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
	Thinking                   bool     `json:"thinking"`
}

// ParseBuildModels 解析 Gemini API models.list 响应
func ParseBuildModels(raw []byte) ([]Model, string, error) {
	var response struct {
		Models        []buildListedModel `json:"models"`
		NextPageToken string             `json:"nextPageToken"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, "", &ProtocolEvidenceError{Method: buildProxyUnaryMethod, Path: "$models", Detail: err.Error(), Raw: cloneRaw(raw)}
	}
	models := make([]Model, 0, len(response.Models))
	for _, listed := range response.Models {
		id := strings.TrimPrefix(listed.Name, "models/")
		if id == "" {
			continue
		}
		capabilities := map[string]bool{}
		if slices.Contains(listed.SupportedGenerationMethods, "generateContent") {
			capabilities["chat_model"] = true
		}
		if listed.Thinking {
			capabilities["thinking"] = true
		}
		models = append(models, Model{
			ID: id, Name: listed.DisplayName, Description: listed.Description,
			Methods:         append([]string(nil), listed.SupportedGenerationMethods...),
			InputTokenLimit: listed.InputTokenLimit, OutputTokenLimit: listed.OutputTokenLimit,
			Capabilities: capabilities,
		})
	}
	return models, response.NextPageToken, nil
}

// BuildModelsForAccount 经 ProxyUnaryCall 读取账户 Build 代理的 Gemini API 模型目录
func (c *Client) BuildModelsForAccount(ctx context.Context, accountID string) ([]Model, error) {
	var models []Model
	pageToken := ""
	for page := 0; page < 10; page++ {
		query := map[string]string{"pageSize": "200"}
		if pageToken != "" {
			query["pageToken"] = pageToken
		}
		encodedQuery, err := json.Marshal(query)
		if err != nil {
			return nil, err
		}
		body, err := json.Marshal([]any{"/v1beta/models", string(encodedQuery), nil, "GET"})
		if err != nil {
			return nil, err
		}
		response, err := c.do(ctx, buildProxyUnaryMethod, accountID, "", body, false)
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("读取 AI Studio %s: %w", buildProxyUnaryMethod, err)
		}
		decoded, err := decodeJSONValue(raw)
		if err != nil {
			return nil, &ProtocolEvidenceError{Method: buildProxyUnaryMethod, Path: "$", Detail: err.Error(), Raw: cloneRaw(raw)}
		}
		content, err := buildProxyResponseBody(decoded, "$")
		if err != nil {
			return nil, err
		}
		listed, next, err := ParseBuildModels(content)
		if err != nil {
			return nil, err
		}
		models = append(models, listed...)
		if next == "" {
			return models, nil
		}
		pageToken = next
	}
	return nil, fmt.Errorf("Build 模型目录分页超过 10 页")
}
