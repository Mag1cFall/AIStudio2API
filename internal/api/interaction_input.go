package api

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"mime"
	"slices"
	"strconv"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// interactionRequest 表示公开 Interactions 创建请求
type interactionRequest struct {
	Model      string                     `json:"model"`
	Agent      string                     `json:"agent"`
	Input      json.RawMessage            `json:"input"`
	System     string                     `json:"system_instruction"`
	Stream     bool                       `json:"stream"`
	Background bool                       `json:"background"`
	Store      *bool                      `json:"store"`
	PreviousID string                     `json:"previous_interaction_id"`
	Formats    json.RawMessage            `json:"response_format"`
	MIME       string                     `json:"response_mime_type"`
	Modalities []string                   `json:"response_modalities"`
	Safety     []interactionSafetySetting `json:"safety_settings"`
	Tools      []interactionTool          `json:"tools"`
	// CachedContent 是显式缓存的资源名
	CachedContent string            `json:"cached_content"`
	ServiceTier   string            `json:"service_tier"`
	Labels        map[string]string `json:"labels"`
	Generation    struct {
		Temperature       *float64        `json:"temperature"`
		TopP              *float64        `json:"top_p"`
		TopK              *int            `json:"top_k"`
		MaxOutputTokens   *int64          `json:"max_output_tokens"`
		Seed              *int64          `json:"seed"`
		StopSequences     []string        `json:"stop_sequences"`
		ThinkingLevel     string          `json:"thinking_level"`
		ThinkingSummaries string          `json:"thinking_summaries"`
		Speech            json.RawMessage `json:"speech_config"`
		ToolChoice        json.RawMessage `json:"tool_choice"`
		Image             *struct {
			AspectRatio string `json:"aspect_ratio"`
			ImageSize   string `json:"image_size"`
		} `json:"image_config"`
		Video *struct {
			Task string `json:"task"`
		} `json:"video_config"`
		Transcription *interactionTranscriptionConfig `json:"transcription_config"`
	} `json:"generation_config"`
}

// interactionTool 表示 Interactions 工具声明，在 Responses 工具字段之外带有内置工具的设置
type interactionTool struct {
	responsesTool
	SearchTypes                    []string          `json:"search_types"`
	EnableWidget                   bool              `json:"enable_widget"`
	Latitude                       *float64          `json:"latitude"`
	Longitude                      *float64          `json:"longitude"`
	Environment                    string            `json:"environment"`
	ExcludedPredefinedFunctions    []string          `json:"excluded_predefined_functions"`
	DisabledSafetyPolicies         []string          `json:"disabled_safety_policies"`
	EnablePromptInjectionDetection bool              `json:"enable_prompt_injection_detection"`
	URL                            string            `json:"url"`
	Headers                        map[string]string `json:"headers"`
	AllowedTools                   []struct {
		Tools []string `json:"tools"`
	} `json:"allowed_tools"`
	FileSearchStoreNames []string `json:"file_search_store_names"`
	MetadataFilter       string   `json:"metadata_filter"`
	TopK                 *int     `json:"top_k"`
}

// interactionTranscriptionConfig 表示 generation_config.transcription_config
type interactionTranscriptionConfig struct {
	AdaptationPhrases      []string        `json:"adaptation_phrases"`
	CustomVocabulary       []string        `json:"custom_vocabulary"`
	DiarizationMode        string          `json:"diarization_mode"`
	LanguageCodes          []string        `json:"language_codes"`
	LanguageHints          []string        `json:"language_hints"`
	Mode                   json.RawMessage `json:"mode"`
	TimestampGranularities []string        `json:"timestamp_granularities"`
}

// config 返回转录配置，mode 接受 "smart" 或 {"type":"smart"}
func (config interactionTranscriptionConfig) config() (*aistudio.TranscriptionConfig, error) {
	mode := ""
	if rawJSONConfigured(config.Mode) && json.Unmarshal(config.Mode, &mode) != nil {
		var object struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(config.Mode, &object); err != nil {
			return nil, fmt.Errorf("transcription_config.mode: %w", err)
		}
		mode = object.Type
	}
	return &aistudio.TranscriptionConfig{
		WordTimestamps: slices.Contains(config.TimestampGranularities, "word"), SpeakerLabels: config.DiarizationMode == "speaker",
		CustomVocabulary: config.CustomVocabulary, LanguageCodes: config.LanguageCodes, SmartTranscription: mode == "smart",
		AdaptationPhrases: config.AdaptationPhrases, LanguageHints: config.LanguageHints,
	}, nil
}

// interactionSafetySetting 表示 Interactions 的安全类别与拦截阈值，取值为小写枚举名
type interactionSafetySetting struct {
	Type      string `json:"type"`
	Threshold string `json:"threshold"`
}

// interactionFormat 表示文本、图片、音频与视频的返回配置
type interactionFormat struct {
	Type        string          `json:"type"`
	MIME        string          `json:"mime_type"`
	Schema      json.RawMessage `json:"schema"`
	SampleRate  int             `json:"sample_rate"`
	BitRate     int             `json:"bit_rate"`
	AspectRatio string          `json:"aspect_ratio"`
	ImageSize   string          `json:"image_size"`
	Duration    string          `json:"duration"`
	Resolution  string          `json:"resolution"`
}

// interactionInput 表示输入内容块或执行步骤
type interactionInput struct {
	Type        string             `json:"type"`
	Text        *string            `json:"text"`
	Data        string             `json:"data"`
	URI         string             `json:"uri"`
	MIME        string             `json:"mime_type"`
	Processing  json.RawMessage    `json:"processing"`
	Content     []interactionInput `json:"content"`
	Summary     []interactionInput `json:"summary"`
	ID          string             `json:"id"`
	CallID      string             `json:"call_id"`
	Name        string             `json:"name"`
	SampleRate  int                `json:"sample_rate"`
	Channels    int                `json:"channels"`
	Resolution  string             `json:"resolution"`
	Arguments   json.RawMessage    `json:"arguments"`
	Result      json.RawMessage    `json:"result"`
	IsError     bool               `json:"is_error"`
	Signature   string             `json:"signature"`
	SearchType  string             `json:"search_type"`
	ServerName  string             `json:"server_name"`
	Annotations []struct {
		Type    string `json:"type"`
		Speaker string `json:"speaker"`
		Style   string `json:"style"`
	} `json:"annotations"`
}

// interactionContentParts 把 Interactions 内容块数组转成 part
func interactionContentParts(raw json.RawMessage) ([]aistudio.Part, error) {
	var contents []interactionInput
	if err := json.Unmarshal(raw, &contents); err != nil {
		return nil, err
	}
	var parts []aistudio.Part
	for _, content := range contents {
		converted, err := content.parts()
		if err != nil {
			return nil, err
		}
		parts = append(parts, converted...)
	}
	return parts, nil
}

// interactionList 解析协议中的单对象或数组联合字段
func interactionList[T any](raw json.RawMessage) ([]T, error) {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return nil, nil
	}
	var values []T
	if strings.HasPrefix(value, "[") {
		err := json.Unmarshal(raw, &values)
		return values, err
	}
	var item T
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, err
	}
	return []T{item}, nil
}

// toGenerateRequest 映射模型、结构化输入、生成参数与函数声明
func (request interactionRequest) toGenerateRequest(id string) (aistudio.GenerateRequest, error) {
	model := request.Model
	if strings.TrimSpace(model) == "" {
		model = request.Agent
	}
	generate := aistudio.GenerateRequest{
		ID: id, Model: strings.TrimPrefix(strings.TrimSpace(model), "models/"), System: systemSegments(request.System),
		ServiceTier: request.ServiceTier, Labels: request.Labels,
	}
	if generate.Model == "" {
		return generate, fmt.Errorf("model or agent is required")
	}
	var err error
	generate.Contents, err = interactionContents(request.Input)
	if err != nil {
		return generate, err
	}
	generation := request.Generation
	generate.Config = aistudio.GenerationConfig{
		Temperature: generation.Temperature, TopP: generation.TopP, TopK: generation.TopK,
		MaxOutputTokens: generation.MaxOutputTokens, Seed: generation.Seed,
		StopSequences: normalizeStopSequences(generation.StopSequences), ReasoningEffort: generation.ThinkingLevel,
	}
	if generation.ThinkingSummaries != "" && generation.ThinkingSummaries != "auto" && generation.ThinkingSummaries != "none" {
		return generate, fmt.Errorf("thinking_summaries must be auto or none")
	}
	if generation.Transcription != nil {
		if generate.Config.TranscriptionConfig, err = generation.Transcription.config(); err != nil {
			return generate, err
		}
	}
	generate.Config.SpeechConfig, err = interactionSpeech(generation.Speech)
	if err != nil {
		return generate, err
	}
	formats, err := interactionList[json.RawMessage](request.Formats)
	if err != nil {
		return generate, fmt.Errorf("response_format: %w", err)
	}
	for _, raw := range formats {
		var format interactionFormat
		if err := json.Unmarshal(raw, &format); err != nil {
			return generate, fmt.Errorf("response_format: %w", err)
		}
		switch format.Type {
		case "text":
			generate.Config.ResponseModalities = append(generate.Config.ResponseModalities, aistudio.ResponseModalityText)
			if format.MIME == "" && len(format.Schema) > 0 {
				format.MIME = "application/json"
			}
			if format.MIME != "" && format.MIME != "text/plain" && format.MIME != "application/json" {
				return generate, fmt.Errorf("text mime_type must be text/plain or application/json")
			}
			generate.Config.ResponseMIMEType = format.MIME
			generate.Config.ResponseJSONSchema = format.Schema
			if len(format.Schema) > 0 && format.MIME != "application/json" {
				return generate, fmt.Errorf("response_format.schema requires application/json")
			}
		case "", "object", "array", "string", "number", "integer", "boolean", "null":
			generate.Config.ResponseModalities = append(generate.Config.ResponseModalities, aistudio.ResponseModalityText)
			generate.Config.ResponseMIMEType = "application/json"
			generate.Config.ResponseJSONSchema = raw
		case "audio":
			if _, ok := outputAudioEncodings[format.MIME]; format.MIME != "" && !ok {
				return generate, fmt.Errorf("audio mime_type must be audio/wav, audio/l16, audio/mp3, audio/ogg_opus, audio/alaw or audio/mulaw")
			}
			if format.SampleRate < 0 || format.BitRate < 0 {
				return generate, fmt.Errorf("audio sample_rate and bit_rate must not be negative")
			}
			generate.Config.ResponseModalities = append(generate.Config.ResponseModalities, aistudio.ResponseModalityAudio)
		case "image":
			if format.MIME != "" && format.MIME != "image/jpeg" {
				return generate, fmt.Errorf("image mime_type must be image/jpeg")
			}
			generate.Config.ResponseModalities = append(generate.Config.ResponseModalities, aistudio.ResponseModalityImage)
			generate.Config.ImageConfig = &aistudio.ImageConfig{AspectRatio: format.AspectRatio, ImageSize: format.ImageSize}
		case "video":
			seconds, err := videoDurationSeconds(format.Duration)
			if err != nil {
				return generate, fmt.Errorf("response_format: %w", err)
			}
			generate.Config.VideoConfig = &aistudio.VideoConfig{AspectRatio: format.AspectRatio, DurationSeconds: seconds, Resolution: format.Resolution}
		default:
			return generate, fmt.Errorf("unsupported response_format type %q", format.Type)
		}
	}
	if video := generation.Video; video != nil && video.Task != "" {
		if generate.Config.VideoConfig == nil {
			generate.Config.VideoConfig = &aistudio.VideoConfig{}
		}
		generate.Config.VideoConfig.Task = video.Task
	}
	if err := request.applyLegacyOutput(&generate); err != nil {
		return generate, err
	}
	tools := make([]responsesTool, 0, len(request.Tools))
	var builtin aistudio.Tools
	for _, tool := range request.Tools {
		switch tool.Type {
		case "function", "url_context":
		case "google_maps":
			builtin.MapsWidget = builtin.MapsWidget || tool.EnableWidget
			if tool.Latitude != nil && tool.Longitude != nil {
				builtin.ToolConfig.Retrieval = &aistudio.RetrievalConfig{Latitude: tool.Latitude, Longitude: tool.Longitude}
			}
		case "google_search":
			builtin.GoogleSearch = &aistudio.GoogleSearchOptions{
				WebSearch:   len(tool.SearchTypes) == 0 || slices.Contains(tool.SearchTypes, "web_search") || slices.Contains(tool.SearchTypes, "enterprise_web_search"),
				ImageSearch: slices.Contains(tool.SearchTypes, "image_search"),
			}
			continue
		case "code_execution":
			tool.Type = "code_interpreter"
		case "computer_use":
			builtin.ComputerUse = &aistudio.ComputerUse{
				Environment: "ENVIRONMENT_" + strings.ToUpper(cmp.Or(tool.Environment, "browser")), ExcludedPredefinedFunctions: tool.ExcludedPredefinedFunctions,
				DisabledSafetyPolicies: tool.DisabledSafetyPolicies, EnablePromptInjectionDetection: tool.EnablePromptInjectionDetection,
			}
			continue
		case "mcp_server":
			server := aistudio.MCPServer{Name: tool.Name, URL: tool.URL, Headers: tool.Headers}
			for _, allowed := range tool.AllowedTools {
				server.AllowedTools = append(server.AllowedTools, allowed.Tools...)
			}
			builtin.MCPServers = append(builtin.MCPServers, server)
			continue
		case "file_search":
			options := map[string]any{"fileSearchStoreNames": tool.FileSearchStoreNames}
			if tool.MetadataFilter != "" {
				options["metadataFilter"] = tool.MetadataFilter
			}
			if tool.TopK != nil {
				options["topK"] = *tool.TopK
			}
			if builtin.FileSearch, err = json.Marshal(options); err != nil {
				return generate, err
			}
			continue
		case "retrieval":
			continue
		default:
			return generate, fmt.Errorf("unsupported interaction tool %q", tool.Type)
		}
		tools = append(tools, tool.responsesTool)
	}
	generate.Tools, err = mapResponsesTools(tools, nil)
	if err != nil {
		return generate, err
	}
	if search := builtin.GoogleSearch; search != nil {
		if search.WebSearch {
			generate.Tools.Google = appendUnique(generate.Tools.Google, "google_search")
		}
		if search.ImageSearch {
			generate.Tools.Google = appendUnique(generate.Tools.Google, "image_search")
		}
		generate.Tools.GoogleSearch = search
	}
	generate.Tools.MapsWidget, generate.Tools.ComputerUse = builtin.MapsWidget, builtin.ComputerUse
	generate.Tools.MCPServers, generate.Tools.FileSearch = builtin.MCPServers, builtin.FileSearch
	generate.Tools.ToolConfig, err = interactionToolChoice(generation.ToolChoice)
	generate.Tools.ToolConfig.Retrieval = builtin.ToolConfig.Retrieval
	generate.Tools.ToolConfig.ServerSideInvocations = interactionServerSteps(generate.Tools)
	return generate, err
}

// interactionServerSteps 表示请求含服务端执行的工具，Interactions 以步骤返回这些工具的调用与结果
func interactionServerSteps(tools aistudio.Tools) bool {
	return len(tools.Google) > 0 || len(tools.MCPServers) > 0 || len(tools.FileSearch) > 0
}

// interactionModalities 是 response_modalities 取值对应的输出类型
var interactionModalities = map[string]aistudio.ResponseModality{
	"text":  aistudio.ResponseModalityText,
	"image": aistudio.ResponseModalityImage,
	"audio": aistudio.ResponseModalityAudio,
}

// applyLegacyOutput 映射 response_modalities、response_mime_type、safety_settings 与 generation_config.image_config，response_format 中的同项优先，给出 response_format 时不使用 response_mime_type
func (request interactionRequest) applyLegacyOutput(generate *aistudio.GenerateRequest) error {
	for _, name := range request.Modalities {
		if strings.EqualFold(name, "video") {
			if generate.Config.VideoConfig == nil {
				generate.Config.VideoConfig = &aistudio.VideoConfig{}
			}
			continue
		}
		modality, ok := interactionModalities[strings.ToLower(name)]
		if !ok {
			return fmt.Errorf("unsupported response_modalities value %q", name)
		}
		if !slices.Contains(generate.Config.ResponseModalities, modality) {
			generate.Config.ResponseModalities = append(generate.Config.ResponseModalities, modality)
		}
	}
	if request.MIME != "" && !rawJSONConfigured(request.Formats) {
		if request.MIME != "text/plain" && request.MIME != "application/json" {
			return fmt.Errorf("response_mime_type must be text/plain or application/json")
		}
		generate.Config.ResponseMIMEType = request.MIME
	}
	if image := request.Generation.Image; image != nil {
		config := aistudio.ImageConfig{AspectRatio: image.AspectRatio, ImageSize: image.ImageSize}
		if format := generate.Config.ImageConfig; format != nil {
			config.AspectRatio = cmp.Or(format.AspectRatio, config.AspectRatio)
			config.ImageSize = cmp.Or(format.ImageSize, config.ImageSize)
		}
		generate.Config.ImageConfig = &config
	}
	for _, setting := range request.Safety {
		generate.SafetySettings = append(generate.SafetySettings, aistudio.SafetySetting{
			Category:  "HARM_CATEGORY_" + strings.ToUpper(setting.Type),
			Threshold: strings.ToUpper(setting.Threshold),
		})
	}
	return nil
}

// interactionToolChoice 将调用模式或 allowed_tools 配置映射为统一工具选择
func interactionToolChoice(raw json.RawMessage) (aistudio.ToolConfig, error) {
	if !rawJSONConfigured(raw) {
		return aistudio.ToolConfig{Mode: "auto"}, nil
	}
	var mode string
	var names []string
	if json.Unmarshal(raw, &mode) != nil {
		var config struct {
			AllowedTools struct {
				Mode  string   `json:"mode"`
				Tools []string `json:"tools"`
			} `json:"allowed_tools"`
		}
		if err := json.Unmarshal(raw, &config); err != nil {
			return aistudio.ToolConfig{}, fmt.Errorf("generation_config.tool_choice: %w", err)
		}
		mode, names = config.AllowedTools.Mode, config.AllowedTools.Tools
	}
	switch mode {
	case "", "auto":
		mode = "auto"
	case "any":
		mode = "required"
	case "none", "validated":
	default:
		return aistudio.ToolConfig{}, fmt.Errorf("unsupported generation_config.tool_choice %q", mode)
	}
	return aistudio.ToolConfig{Mode: mode, AllowedFunctionNames: names}, nil
}

// interactionContents 保留输入步骤顺序并合并同一角色的内容块
func interactionContents(raw json.RawMessage) ([]aistudio.Content, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if strings.TrimSpace(text) == "" {
			return nil, nil
		}
		return []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: text}}}}, nil
	}
	items, err := interactionList[interactionInput](raw)
	if err != nil {
		return nil, fmt.Errorf("input: %w", err)
	}
	var contents []aistudio.Content
	pendingSignature := ""
	// mcpFunctions 记录按函数调用发送的 MCP 调用 ID 与函数名
	mcpFunctions := map[string]string{}
	for _, item := range items {
		role := aistudio.RoleUser
		var parts []aistudio.Part
		switch item.Type {
		case "user_input", "model_output":
			if item.Type == "model_output" {
				role = aistudio.RoleAssistant
			}
			for _, content := range item.Content {
				mapped, mapErr := content.parts()
				if mapErr != nil {
					return nil, mapErr
				}
				parts = append(parts, mapped...)
			}
		case "function_call":
			if item.ID == "" || item.Name == "" || len(item.Arguments) == 0 {
				return nil, fmt.Errorf("function_call requires id, name and arguments")
			}
			role = aistudio.RoleAssistant
			parts = []aistudio.Part{{FunctionCall: &aistudio.FunctionCall{ID: item.ID, Name: item.Name, Arguments: item.Arguments}, ThoughtSignature: cmp.Or(item.Signature, pendingSignature)}}
			pendingSignature = ""
		case "function_result":
			if item.CallID == "" || len(item.Result) == 0 {
				return nil, fmt.Errorf("function_result requires call_id and result")
			}
			result, media, mapErr := functionResultParts(item.Result, item.IsError, interactionContentParts)
			if mapErr != nil {
				return nil, mapErr
			}
			role = aistudio.RoleTool
			parts = append([]aistudio.Part{{FunctionResult: &aistudio.FunctionResult{ID: item.CallID, Name: item.Name, Content: result}}}, media...)
		case "thought":
			role = aistudio.RoleAssistant
			if item.Signature != "" {
				pendingSignature = item.Signature
			}
		case "retrieval_call", "retrieval_result":
			continue
		default:
			if item.Type == "mcp_server_tool_call" && item.Signature == "" && pendingSignature == "" || item.Type == "mcp_server_tool_result" && mcpFunctions[item.CallID] != "" {
				role, parts, err = mcpFunctionPart(item, mcpFunctions)
				if err != nil {
					return nil, err
				}
				break
			}
			if interactionToolStepKind(item.Type) {
				role = aistudio.RoleAssistant
				mapped, mapErr := interactionToolPart(item)
				if mapErr != nil {
					return nil, mapErr
				}
				if pendingSignature != "" {
					parts = append(parts, aistudio.Part{ThoughtSignature: pendingSignature})
					pendingSignature = ""
				}
				parts = append(parts, mapped)
				break
			}
			parts, err = item.parts()
			if err != nil {
				return nil, err
			}
		}
		if len(parts) == 0 {
			continue
		}
		if pendingSignature != "" {
			if role == aistudio.RoleAssistant {
				parts[0].ThoughtSignature = pendingSignature
			} else if len(contents) > 0 && contents[len(contents)-1].Role == aistudio.RoleAssistant {
				contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, aistudio.Part{ThoughtSignature: pendingSignature})
			}
			pendingSignature = ""
		}
		if len(contents) > 0 && contents[len(contents)-1].Role == role {
			contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, parts...)
		} else {
			contents = append(contents, aistudio.Content{Role: role, Parts: parts})
		}
	}
	if pendingSignature != "" && len(contents) > 0 && contents[len(contents)-1].Role == aistudio.RoleAssistant {
		contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, aistudio.Part{ThoughtSignature: pendingSignature})
	}
	return contents, nil
}

// parts 复用 Gemini 的文本、媒体与文件输入映射
func (content interactionInput) parts() ([]aistudio.Part, error) {
	part := geminiPart{}
	if content.Resolution != "" {
		part.MediaResolution = &geminiMediaResolution{Level: "MEDIA_RESOLUTION_" + strings.ToUpper(content.Resolution)}
	}
	if rawJSONConfigured(content.Processing) {
		var err error
		if part.VideoMetadata, part.MediaProcessing, err = interactionVideoProcessing(content.Processing); err != nil {
			return nil, err
		}
	}
	switch content.Type {
	case "text":
		if content.Text == nil {
			return nil, fmt.Errorf("text content requires text")
		}
		part.Text = content.Text
		for _, annotation := range content.Annotations {
			if annotation.Type == "speech_metadata" {
				part.SpeechMetadata = &aistudio.SpeechMetadata{Speaker: annotation.Speaker, Style: annotation.Style}
			}
		}
	case "image", "audio", "video", "document":
		if (content.Data == "") == (content.URI == "") {
			return nil, fmt.Errorf("%s content requires exactly one of data or uri", content.Type)
		}
		if content.Data != "" {
			mimeType := content.MIME
			if mimeType == "" {
				data, err := decodeBase64Flexible(content.Data)
				if err != nil {
					return nil, fmt.Errorf("%s data: %w", content.Type, err)
				}
				mimeType = detectMediaType("", data)
				if mimeType == "application/octet-stream" {
					return nil, fmt.Errorf("%s content requires mime_type", content.Type)
				}
			}
			part.InlineData = &geminiBlobPart{MIMEType: content.audioMIME(mimeType), Data: content.Data, DisplayName: content.Name}
		} else {
			part.FileData = &geminiFilePart{MIMEType: content.audioMIME(content.MIME), FileURI: content.URI, DisplayName: content.Name}
		}
	default:
		return nil, fmt.Errorf("unsupported input type %q", content.Type)
	}
	parts, _, err := mapGeminiParts([]geminiPart{part})
	return parts, err
}

// audioMIME 把音频内容块的 sample_rate 与 channels 写入 MIME 参数，MIME 已带同名参数时保留原值
func (content interactionInput) audioMIME(mimeType string) string {
	if content.Type != "audio" || mimeType == "" || content.SampleRate == 0 && content.Channels == 0 {
		return mimeType
	}
	base, params, err := mime.ParseMediaType(mimeType)
	if err != nil {
		return mimeType
	}
	if _, ok := params["rate"]; !ok && content.SampleRate > 0 {
		params["rate"] = strconv.Itoa(content.SampleRate)
	}
	if _, ok := params["channels"]; !ok && content.Channels > 0 {
		params["channels"] = strconv.Itoa(content.Channels)
	}
	return mime.FormatMediaType(base, params)
}

// interactionSpeech 转换单人声音列表与多说话人配置
func interactionSpeech(raw json.RawMessage) (*aistudio.SpeechConfig, error) {
	if !rawJSONConfigured(raw) {
		return nil, nil
	}
	type voice struct {
		Voice    string `json:"voice"`
		Speaker  string `json:"speaker"`
		Language string `json:"language"`
	}
	var config struct {
		Mode     string  `json:"mode"`
		Speakers []voice `json:"speakers"`
	}
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		if err := json.Unmarshal(raw, &config.Speakers); err != nil {
			return nil, err
		}
	} else if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	if len(config.Speakers) == 0 || len(config.Speakers) > 2 {
		return nil, fmt.Errorf("speech_config requires one or two voices")
	}
	speech := &aistudio.SpeechConfig{Mode: strings.ToUpper(config.Mode)}
	for _, speaker := range config.Speakers {
		if strings.TrimSpace(speaker.Voice) == "" {
			return nil, fmt.Errorf("speech_config voice is required")
		}
		if speaker.Language != "" {
			speech.LanguageCode = speaker.Language
		}
		if len(config.Speakers) == 1 && speaker.Speaker == "" {
			speech.VoiceName = speaker.Voice
		} else {
			if strings.TrimSpace(speaker.Speaker) == "" {
				return nil, fmt.Errorf("speech_config speaker is required for named voices")
			}
			speech.Speakers = append(speech.Speakers, aistudio.SpeakerVoiceConfig{Speaker: speaker.Speaker, VoiceName: speaker.Voice})
		}
	}
	return speech, nil
}

// imageMIME 返回图片返回配置要求的 MIME 类型
func (request interactionRequest) imageMIME() string {
	formats, _ := interactionList[interactionFormat](request.Formats)
	for _, format := range formats {
		if format.Type == "image" {
			return format.MIME
		}
	}
	return ""
}

// outputAudioFormat 表示 Interactions response_format 与 Gemini responseFormat.audio 的音频输出 MIME、采样率与码率
type outputAudioFormat struct {
	MIME       string
	SampleRate int
	BitRate    int
}

// outputAudioEncodings 为音频输出 MIME 对应的编码格式与返回内容的 MIME
var outputAudioEncodings = map[string]struct{ format, content string }{
	"audio/wav": {"pcm", "audio/l16"}, "audio/l16": {"pcm", "audio/l16"}, "audio/mp3": {"mp3", "audio/mp3"},
	"audio/ogg_opus": {"opus", "audio/ogg"}, "audio/alaw": {"alaw", "audio/alaw"}, "audio/mulaw": {"mulaw", "audio/mulaw"},
}

// audioFormat 返回音频输出配置，未指定 MIME 时非流式为 WAV、流式为 L16
func (request interactionRequest) audioFormat() outputAudioFormat {
	audio := outputAudioFormat{MIME: "audio/wav"}
	if request.Stream {
		audio.MIME = "audio/l16"
	}
	formats, _ := interactionList[interactionFormat](request.Formats)
	for _, format := range formats {
		if format.Type == "audio" {
			audio.SampleRate, audio.BitRate = format.SampleRate, format.BitRate
			if format.MIME != "" {
				audio.MIME = format.MIME
			}
		}
	}
	return audio
}

// container 返回 interactionMedia 的封装方式，WAV 封装 PCM，其余格式原样输出编码结果
func (audio outputAudioFormat) container() string {
	if audio.MIME == "audio/wav" {
		return "wav"
	}
	return "pcm"
}

// encode 把上游 PCM 或 MP3 音频转换为请求的编码与采样率，编码与采样率已经一致时保留原始数据，其他编码原样返回
func (audio outputAudioFormat) encode(media aistudio.Blob) (aistudio.Blob, error) {
	baseType, _, _ := mime.ParseMediaType(media.MIME)
	if baseType != "audio/l16" && baseType != "audio/wav" && baseType != "audio/x-wav" && baseType != "audio/mpeg" {
		return media, nil
	}
	source, err := mediaPCM(media)
	if err != nil {
		return media, err
	}
	encoding := outputAudioEncodings[audio.MIME]
	sameRate := audio.SampleRate == 0 || audio.SampleRate == source.SampleRate
	switch {
	case baseType == "audio/mpeg" && encoding.format == "mp3" && sameRate && audio.BitRate == 0:
		return aistudio.Blob{MIME: fmt.Sprintf("%s;rate=%d;channels=%d", encoding.content, source.SampleRate, source.Channels), Data: media.Data}, nil
	case baseType != "audio/mpeg" && encoding.format == "pcm" && sameRate:
		return media, nil
	}
	data, encoded, err := encodeAudio(source, audioOutput{Format: encoding.format, SampleRate: audio.SampleRate, BitRate: audio.BitRate})
	return aistudio.Blob{MIME: fmt.Sprintf("%s;rate=%d;channels=%d", encoding.content, encoded.SampleRate, encoded.Channels), Data: data}, err
}

// output 把上游音频转换为请求的编码与采样率，WAV 输出封装为 RIFF WAVE
func (audio outputAudioFormat) output(media aistudio.Blob) (aistudio.Blob, error) {
	encoded, err := audio.encode(media)
	if err != nil || audio.container() != "wav" || strings.HasPrefix(encoded.MIME, "audio/wav") {
		return encoded, err
	}
	pcm, err := mediaPCM(encoded)
	if err != nil {
		return encoded, err
	}
	return aistudio.Blob{MIME: "audio/wav", Data: pcmWAV(pcm.Data, pcm.SampleRate, pcm.Channels)}, nil
}

// streams 判断音频块能否逐块输出，容器格式、重采样与 MP3 解码需要完整音频
func (audio outputAudioFormat) streams(media aistudio.Blob) bool {
	switch audio.MIME {
	case "audio/l16", "audio/alaw", "audio/mulaw":
		if baseType, _, _ := mime.ParseMediaType(media.MIME); baseType == "audio/mpeg" {
			return false
		}
		source, err := mediaPCM(media)
		return err != nil || audio.SampleRate == 0 || audio.SampleRate == source.SampleRate
	}
	return false
}

// videoDurationSeconds 把 "4s" 或 "4" 形式的视频时长四舍五入为秒数，空值为 0
func videoDurationSeconds(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	seconds, err := strconv.ParseFloat(strings.TrimSuffix(value, "s"), 64)
	if err != nil || seconds < 0 {
		return 0, fmt.Errorf("duration %q must be a non-negative number of seconds such as \"4s\"", value)
	}
	return int64(math.Round(seconds)), nil
}
