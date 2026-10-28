package api

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type geminiRequest struct {
	Contents          []geminiContent        `json:"contents"`
	SystemInstruction *geminiContent         `json:"systemInstruction"`
	GenerationConfig  geminiGenerationConfig `json:"generationConfig"`
	Tools             []geminiToolGroup      `json:"tools"`
	ToolConfig        geminiToolConfig       `json:"toolConfig"`
	SafetySettings    []geminiSafetySetting  `json:"safetySettings"`
	CachedContent     string                 `json:"cachedContent"`
	ServiceTier       string                 `json:"serviceTier"`
	Store             *bool                  `json:"store"`
	Labels            map[string]string      `json:"labels"`
	// GenerateContentRequest 为 countTokens 携带的完整生成请求
	GenerateContentRequest *geminiRequest `json:"generateContentRequest"`
}

type geminiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiBlobPart struct {
	MIMEType      string `json:"mimeType"`
	MIMETypeSnake string `json:"mime_type"`
	Data          string `json:"data"`
	DisplayName   string `json:"displayName"`
}

func (b *geminiBlobPart) MIME() string {
	if b == nil {
		return ""
	}
	if b.MIMEType != "" {
		return b.MIMEType
	}
	return b.MIMETypeSnake
}

type geminiFilePart struct {
	MIMEType      string `json:"mimeType"`
	MIMETypeSnake string `json:"mime_type"`
	FileURI       string `json:"fileUri"`
	FileURISnake  string `json:"file_uri"`
	DisplayName   string `json:"displayName"`
}

func (f *geminiFilePart) MIME() string {
	if f == nil {
		return ""
	}
	if f.MIMEType != "" {
		return f.MIMEType
	}
	return f.MIMETypeSnake
}

func (f *geminiFilePart) URI() string {
	if f == nil {
		return ""
	}
	if f.FileURI != "" {
		return f.FileURI
	}
	return f.FileURISnake
}

type geminiPart struct {
	Text             *string         `json:"text"`
	Thought          bool            `json:"thought"`
	ThoughtSignature string          `json:"thoughtSignature"`
	InlineData       *geminiBlobPart `json:"inlineData"`
	InlineDataSnake  *geminiBlobPart `json:"inline_data"`
	FileData         *geminiFilePart `json:"fileData"`
	FileDataSnake    *geminiFilePart `json:"file_data"`
	FunctionCall     *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
	FunctionResponse *struct {
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Response json.RawMessage `json:"response"`
		Parts    []geminiPart    `json:"parts"`
	} `json:"functionResponse"`
	ExecutableCode      *aistudio.ExecutableCode      `json:"executableCode"`
	CodeExecutionResult *aistudio.CodeExecutionResult `json:"codeExecutionResult"`
	SpeechMetadata      *aistudio.SpeechMetadata      `json:"speechMetadata"`
	SpeechMetadataSnake *aistudio.SpeechMetadata      `json:"speech_metadata"`
	VideoMetadata       *geminiVideoMetadata          `json:"videoMetadata"`
	MediaProcessing     string                        `json:"mediaProcessing"`
	ToolCall            *geminiToolStep               `json:"toolCall"`
	ToolResponse        *geminiToolStep               `json:"toolResponse"`
	MediaResolution     *geminiMediaResolution        `json:"mediaResolution"`
}

// geminiMediaResolution 为 Part.mediaResolution 的分辨率档位
type geminiMediaResolution struct {
	Level string `json:"level"`
}

// geminiVideoMetadata 为 Part.videoMetadata 的截取起止与采样帧率
type geminiVideoMetadata struct {
	StartOffset string   `json:"startOffset"`
	EndOffset   string   `json:"endOffset"`
	FPS         *float64 `json:"fps"`
}

// geminiToolStep 为 Part.toolCall 与 Part.toolResponse 的服务端工具步骤
type geminiToolStep struct {
	ID       string          `json:"id"`
	ToolType string          `json:"toolType"`
	ToolName string          `json:"toolName"`
	Args     json.RawMessage `json:"args"`
	Response json.RawMessage `json:"response"`
}

// step 把服务端工具步骤转为规范步骤，payload 为 args 或 response
func (step geminiToolStep) step(payload json.RawMessage) *aistudio.ServerToolStep {
	return &aistudio.ServerToolStep{ID: step.ID, Type: step.ToolType, Name: step.ToolName, Data: payload}
}

func (p geminiPart) speechMetadata() *aistudio.SpeechMetadata {
	metadata := p.SpeechMetadata
	if metadata == nil {
		metadata = p.SpeechMetadataSnake
	}
	if metadata == nil || strings.TrimSpace(metadata.Speaker) == "" && strings.TrimSpace(metadata.Style) == "" {
		return nil
	}
	return &aistudio.SpeechMetadata{Speaker: strings.TrimSpace(metadata.Speaker), Style: strings.TrimSpace(metadata.Style)}
}

func (p geminiPart) inline() *geminiBlobPart {
	if p.InlineData != nil {
		return p.InlineData
	}
	return p.InlineDataSnake
}

func (p geminiPart) file() *geminiFilePart {
	if p.FileData != nil {
		return p.FileData
	}
	return p.FileDataSnake
}

type geminiGenerationConfig struct {
	Temperature         *float64                   `json:"temperature"`
	TopP                *float64                   `json:"topP"`
	TopK                *int                       `json:"topK"`
	CandidateCount      *int64                     `json:"candidateCount"`
	MaxOutputTokens     *int64                     `json:"maxOutputTokens"`
	StopSequences       []string                   `json:"stopSequences"`
	ResponseMIMEType    string                     `json:"responseMimeType"`
	ResponseSchema      json.RawMessage            `json:"responseSchema"`
	ResponseJSONSchema  json.RawMessage            `json:"responseJsonSchema"`
	ResponseFormat      geminiResponseFormats      `json:"responseFormat"`
	ResponseModalities  []string                   `json:"responseModalities"`
	ImageConfig         *geminiImageConfig         `json:"imageConfig"`
	VideoConfig         *geminiVideoConfig         `json:"videoConfig"`
	SpeechConfig        *geminiSpeechConfig        `json:"speechConfig"`
	TranscriptionConfig *geminiTranscriptionConfig `json:"audioTranscriptionConfig"`
	EnhancedCivic       bool                       `json:"enableEnhancedCivicAnswers"`
	Seed                *int64                     `json:"seed"`
	MediaResolution     string                     `json:"mediaResolution"`
	ThinkingConfig      *struct {
		ThinkingBudget  *int64 `json:"thinkingBudget"`
		ThinkingLevel   string `json:"thinkingLevel"`
		IncludeThoughts bool   `json:"includeThoughts"`
	} `json:"thinkingConfig"`
}

// geminiTranscriptionConfig 为 generationConfig.audioTranscriptionConfig
type geminiTranscriptionConfig struct {
	LanguageCodes     []string  `json:"languageCodes"`
	CustomVocabulary  []string  `json:"customVocabulary"`
	AdaptationPhrases []string  `json:"adaptationPhrases"`
	WordTimestamp     bool      `json:"wordTimestamp"`
	Diarization       bool      `json:"diarization"`
	LanguageAuto      *struct{} `json:"languageAuto"`
	LanguageHints     *struct {
		LanguageCodes []string `json:"languageCodes"`
	} `json:"languageHints"`
	Mode string `json:"mode"`
}

type geminiImageConfig struct {
	AspectRatio string `json:"aspectRatio"`
	ImageSize   string `json:"imageSize"`
}

// geminiVideoConfig 为 generationConfig.videoConfig 扩展字段，task 取 Interactions API video_config.task 的取值
type geminiVideoConfig struct {
	Task string `json:"task"`
}

// geminiResponseFormat 为 generationConfig.responseFormat 的一项输出格式
type geminiResponseFormat struct {
	Text *struct {
		MIMEType string          `json:"mimeType"`
		Schema   json.RawMessage `json:"schema"`
	} `json:"text"`
	Image *geminiImageConfig `json:"image"`
	Audio *struct {
		MIMEType   string `json:"mimeType"`
		SampleRate int    `json:"sampleRate"`
		BitRate    int    `json:"bitRate"`
	} `json:"audio"`
	Video *struct {
		AspectRatio string `json:"aspectRatio"`
		Duration    string `json:"duration"`
		Resolution  string `json:"resolution"`
	} `json:"video"`
}

// geminiResponseFormats 为 generationConfig.responseFormat，REST 写法为单个对象，SDK 写法为数组
type geminiResponseFormats []geminiResponseFormat

// UnmarshalJSON 按单个对象或数组读取 responseFormat
func (formats *geminiResponseFormats) UnmarshalJSON(data []byte) error {
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '{' {
		var format geminiResponseFormat
		if err := json.Unmarshal(trimmed, &format); err != nil {
			return err
		}
		*formats = geminiResponseFormats{format}
		return nil
	}
	return json.Unmarshal(data, (*[]geminiResponseFormat)(formats))
}

// geminiResponseFormatValues 为 responseFormat 枚举名对应的上游取值
var geminiResponseFormatValues = map[string]string{
	"APPLICATION_JSON":                "application/json",
	"TEXT_PLAIN":                      "text/plain",
	"ASPECT_RATIO_ONE_BY_ONE":         "1:1",
	"ASPECT_RATIO_TWO_BY_THREE":       "2:3",
	"ASPECT_RATIO_THREE_BY_TWO":       "3:2",
	"ASPECT_RATIO_THREE_BY_FOUR":      "3:4",
	"ASPECT_RATIO_FOUR_BY_THREE":      "4:3",
	"ASPECT_RATIO_FOUR_BY_FIVE":       "4:5",
	"ASPECT_RATIO_FIVE_BY_FOUR":       "5:4",
	"ASPECT_RATIO_NINE_BY_SIXTEEN":    "9:16",
	"ASPECT_RATIO_SIXTEEN_BY_NINE":    "16:9",
	"ASPECT_RATIO_TWENTY_ONE_BY_NINE": "21:9",
	"ASPECT_RATIO_ONE_BY_EIGHT":       "1:8",
	"ASPECT_RATIO_EIGHT_BY_ONE":       "8:1",
	"ASPECT_RATIO_ONE_BY_FOUR":        "1:4",
	"ASPECT_RATIO_FOUR_BY_ONE":        "4:1",
	"IMAGE_SIZE_FIVE_TWELVE":          "512",
	"IMAGE_SIZE_ONE_K":                "1K",
	"IMAGE_SIZE_TWO_K":                "2K",
	"IMAGE_SIZE_FOUR_K":               "4K",
}

// geminiResponseFormatValue 把 responseFormat 枚举名换成上游取值，其他值原样返回
func geminiResponseFormatValue(value string) string {
	if mapped, ok := geminiResponseFormatValues[value]; ok {
		return mapped
	}
	return value
}

type geminiVoiceConfig struct {
	PrebuiltVoiceConfig *struct {
		VoiceName string `json:"voiceName"`
	} `json:"prebuiltVoiceConfig"`
	Voice string `json:"voice"`
}

// voices 返回预置音色名与说话人标识
func (config geminiVoiceConfig) voices() (string, string) {
	name := ""
	if config.PrebuiltVoiceConfig != nil {
		name = strings.TrimSpace(config.PrebuiltVoiceConfig.VoiceName)
	}
	return name, strings.TrimSpace(config.Voice)
}

type geminiSpeakerVoiceConfig struct {
	Speaker     string            `json:"speaker"`
	VoiceConfig geminiVoiceConfig `json:"voiceConfig"`
}

type geminiSpeechConfig struct {
	VoiceConfig             *geminiVoiceConfig `json:"voiceConfig"`
	LanguageCode            string             `json:"languageCode"`
	MultiSpeakerVoiceConfig *struct {
		Mode                string                     `json:"mode"`
		SpeakerVoiceConfigs []geminiSpeakerVoiceConfig `json:"speakerVoiceConfigs"`
	} `json:"multiSpeakerVoiceConfig"`
}

type geminiToolGroup struct {
	FunctionDeclarations []struct {
		Name                 string          `json:"name"`
		Description          string          `json:"description"`
		Parameters           json.RawMessage `json:"parameters"`
		ParametersJSONSchema json.RawMessage `json:"parametersJsonSchema"`
		Response             json.RawMessage `json:"response"`
		ResponseJSONSchema   json.RawMessage `json:"responseJsonSchema"`
	} `json:"functionDeclarations"`
	GoogleSearch          json.RawMessage `json:"googleSearch"`
	GoogleSearchRetrieval json.RawMessage `json:"googleSearchRetrieval"`
	URLContext            json.RawMessage `json:"urlContext"`
	CodeExecution         json.RawMessage `json:"codeExecution"`
	GoogleMaps            json.RawMessage `json:"googleMaps"`
	ImageSearch           json.RawMessage `json:"imageSearch"`
	ComputerUse           *struct {
		Environment                    string   `json:"environment"`
		ExcludedPredefinedFunctions    []string `json:"excludedPredefinedFunctions"`
		DisabledSafetyPolicies         []string `json:"disabledSafetyPolicies"`
		EnablePromptInjectionDetection bool     `json:"enablePromptInjectionDetection"`
	} `json:"computerUse"`
	MCPServers []struct {
		Name      string `json:"name"`
		Transport struct {
			URL              string            `json:"url"`
			Headers          map[string]string `json:"headers"`
			Timeout          string            `json:"timeout"`
			SSEReadTimeout   string            `json:"sseReadTimeout"`
			TerminateOnClose *bool             `json:"terminateOnClose"`
		} `json:"streamableHttpTransport"`
	} `json:"mcpServers"`
	FileSearch json.RawMessage `json:"fileSearch"`
}

type geminiToolConfig struct {
	FunctionCallingConfig struct {
		Mode                 string   `json:"mode"`
		AllowedFunctionNames []string `json:"allowedFunctionNames"`
	} `json:"functionCallingConfig"`
	IncludeServerSideToolInvocations bool `json:"includeServerSideToolInvocations"`
	RetrievalConfig                  *struct {
		LatLng *struct {
			Latitude  *float64 `json:"latitude"`
			Longitude *float64 `json:"longitude"`
		} `json:"latLng"`
		LanguageCode string `json:"languageCode"`
	} `json:"retrievalConfig"`
}

func (s *server) handleGeminiModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.service.Models(r.Context())
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiRequestError(w, err)
		}
		return
	}
	page, next, err := geminiPage(r.URL.Query(), models, 50, 1000)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	data := make([]map[string]any, 0, len(page))
	for _, model := range page {
		data = append(data, geminiModelObject(model))
	}
	response := map[string]any{"models": data}
	if next != "" {
		response["nextPageToken"] = next
	}
	writeJSON(w, http.StatusOK, officialOrder("gemini", response))
}

// geminiPage 按 pageSize 与 pageToken 返回列表的一页与下一页令牌，pageSize 为 0 时取 defaultSize、最多 maxSize，pageToken 为下一页的起始位置
func geminiPage[T any](query url.Values, items []T, defaultSize int, maxSize int) ([]T, string, error) {
	size, offset := defaultSize, 0
	if value := firstQueryValue(query, "pageSize", "page_size"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			return nil, "", fmt.Errorf("invalid pageSize %q", value)
		}
		if parsed > 0 {
			size = min(parsed, maxSize)
		}
	}
	if token := firstQueryValue(query, "pageToken", "page_token"); token != "" {
		parsed, err := strconv.Atoi(token)
		if err != nil || parsed < 0 || parsed > len(items) {
			return nil, "", fmt.Errorf("invalid pageToken %q", token)
		}
		offset = parsed
	}
	end := min(offset+size, len(items))
	if end < len(items) {
		return items[offset:end], strconv.Itoa(end), nil
	}
	return items[offset:end], "", nil
}

func (s *server) handleGeminiModel(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("model")
	models, err := s.service.Models(r.Context())
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiRequestError(w, err)
		}
		return
	}
	if model, ok := LookupPublicModel(models, modelID); ok {
		writeJSON(w, http.StatusOK, officialOrder("gemini", geminiModelObject(model)))
		return
	}
	writeGeminiError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("model %q is unavailable", modelID))
}

func geminiModelObject(model aistudio.Model) map[string]any {
	item := map[string]any{
		"name":                       "models/" + model.ID,
		"displayName":                model.Name,
		"description":                model.Description,
		"supportedGenerationMethods": model.Methods,
		"inputTokenLimit":            model.InputTokenLimit,
		"outputTokenLimit":           model.OutputTokenLimit,
	}
	if model.Version != "" {
		item["version"] = model.Version
	}
	if model.Stage != "" {
		item["modelStage"] = model.Stage
	}
	if model.Temperature != nil {
		item["temperature"] = *model.Temperature
	}
	if model.MaxTemperature != nil {
		item["maxTemperature"] = *model.MaxTemperature
	}
	if model.TopP != nil {
		item["topP"] = *model.TopP
	}
	if model.TopK != nil {
		item["topK"] = *model.TopK
	}
	if model.Capabilities["thinking"] {
		item["thinking"] = true
	}
	if len(model.ThinkingLevels) > 0 {
		item["thinkingLevels"] = model.ThinkingLevels
		item["defaultThinkingLevel"] = model.DefaultThinkingLevel
	}
	if len(model.Capabilities) > 0 {
		item["capabilities"] = model.Capabilities
	}
	if len(model.CapabilityOptions) > 0 {
		item["capabilityOptions"] = model.CapabilityOptions
	}
	if len(model.AccessModes) > 0 {
		item["accessModes"] = model.AccessModes
	}
	if len(model.Channels) > 0 {
		item["channels"] = model.Channels
	}
	if model.Paid {
		item["paid"] = true
	}
	return item
}

func (s *server) handleGeminiAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	separator := strings.LastIndex(action, ":")
	if separator < 1 {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "expected models/{model}:{method}")
		return
	}
	model := strings.TrimPrefix(action[:separator], "models/")
	method := action[separator+1:]
	switch method {
	case "predictLongRunning":
		s.handleGeminiVideoCreate(w, r, model)
		return
	case "embedContent", "batchEmbedContents":
		s.handleGeminiEmbed(w, r, model, method == "batchEmbedContents")
		return
	}
	var request geminiRequest
	body, err := io.ReadAll(r.Body)
	if err == nil {
		body, _, err = geminiCamelKeys(body)
	}
	if err == nil {
		err = json.Unmarshal(body, &request)
	}
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if method == "countTokens" && request.GenerateContentRequest != nil {
		request = *request.GenerateContentRequest
	}
	generateRequest, err := request.toGenerateRequest(newID("resp"), model)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	cache, ok := s.applyGeminiCache(w, request.CachedContent, &generateRequest)
	if !ok {
		return
	}
	if err := resolveGeminiFileNames(r.Context(), s.service, generateRequest.Contents); err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiRequestError(w, err)
		}
		return
	}
	switch method {
	case "countTokens", "generateContent", "streamGenerateContent":
	default:
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "unknown method: "+method)
		return
	}
	if err := inlineRemoteMedia(r.Context(), generateRequest.Contents); err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		}
		return
	}
	if method == "countTokens" {
		s.handleGeminiCountTokens(w, r, generateRequest, cache)
		return
	}
	audio, err := request.GenerationConfig.audioFormat()
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	s.handleGeminiGenerate(w, r, generateRequest, method == "streamGenerateContent", request.GenerationConfig.candidates(), audio)
}

// geminiFilesAPIURI 返回 URI 是否指向 Gemini Files API 的文件资源
func geminiFilesAPIURI(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && strings.EqualFold(parsed.Hostname(), "generativelanguage.googleapis.com") && strings.Contains(parsed.Path, "/files/")
}

// geminiOpaqueFields 为取值是自由 JSON 的字段，其中的键名属于用户数据
var geminiOpaqueFields = map[string]bool{
	"args": true, "response": true, "parameters": true, "parametersJsonSchema": true,
	"responseSchema": true, "responseJsonSchema": true, "partMetadata": true, "schema": true, "labels": true, "headers": true,
}

// geminiCamelKeys 按 proto3 JSON 规则把各层消息字段名转为 lowerCamelCase，同层两种写法并存时保留驼峰字段，自由 JSON 字段的取值原样保留
func geminiCamelKeys(raw []byte) ([]byte, bool, error) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || value[0] != '{' && value[0] != '[' {
		return raw, false, nil
	}
	if camel, err := geminiKeysCamel(value); err == nil && camel {
		return raw, false, nil
	}
	changed := false
	if value[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(value, &items); err != nil {
			return nil, false, err
		}
		for index, item := range items {
			converted, itemChanged, err := geminiCamelKeys(item)
			if err != nil {
				return nil, false, err
			}
			items[index], changed = converted, changed || itemChanged
		}
		if !changed {
			return raw, false, nil
		}
		encoded, err := json.Marshal(items)
		return encoded, true, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil {
		return nil, false, err
	}
	converted := make(map[string]json.RawMessage, len(fields))
	for key, field := range fields {
		name := geminiJSONName(key)
		if name != key {
			changed = true
			if _, exists := fields[name]; exists {
				continue
			}
		}
		if !geminiOpaqueFields[name] {
			var fieldChanged bool
			var err error
			field, fieldChanged, err = geminiCamelKeys(field)
			if err != nil {
				return nil, false, err
			}
			changed = changed || fieldChanged
		}
		converted[name] = field
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := json.Marshal(converted)
	return encoded, true, err
}

// geminiKeysCamel 逐个读取 JSON token，判断各层消息字段名是否都已是 lowerCamelCase，不读取自由 JSON 字段的取值
func geminiKeysCamel(raw []byte) (bool, error) {
	decoder := jsontext.NewDecoder(bytes.NewBuffer(raw))
	for {
		kind, length := decoder.StackIndex(decoder.StackDepth())
		token, err := decoder.ReadToken()
		if err == io.EOF {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if kind != '{' || length%2 != 0 || token.Kind() != '"' {
			continue
		}
		name := token.String()
		if geminiJSONName(name) != name {
			return false, nil
		}
		if geminiOpaqueFields[name] {
			if err := decoder.SkipValue(); err != nil {
				return false, err
			}
		}
	}
}

// geminiJSONName 返回 proto 字段名对应的 JSON 名
func geminiJSONName(name string) string {
	if !strings.Contains(name, "_") {
		return name
	}
	var builder strings.Builder
	upper := false
	for _, char := range name {
		if char == '_' {
			upper = true
			continue
		}
		if upper {
			char, upper = unicode.ToUpper(char), false
		}
		builder.WriteRune(char)
	}
	return builder.String()
}

func (request geminiRequest) toGenerateRequest(id string, model string) (aistudio.GenerateRequest, error) {
	if err := request.GenerationConfig.validate(); err != nil {
		return aistudio.GenerateRequest{}, err
	}
	var system []string
	var systemMedia []aistudio.Part
	if request.SystemInstruction != nil {
		parts, _, err := mapGeminiParts(request.SystemInstruction.Parts)
		if err != nil {
			return aistudio.GenerateRequest{}, fmt.Errorf("systemInstruction: %w", err)
		}
		for _, part := range parts {
			if part.FunctionCall != nil || part.FunctionResult != nil {
				transcript, err := aistudio.ToolPartTranscript(part)
				if err != nil {
					return aistudio.GenerateRequest{}, fmt.Errorf("systemInstruction: %w", err)
				}
				system = append(system, transcript)
				continue
			}
			if part.Text == "" && (part.InlineData != nil || part.File != nil || part.ExternalMedia != nil) {
				systemMedia = append(systemMedia, part)
			}
			if part.Text != "" {
				system = append(system, part.Text)
			}
		}
	}
	contents := make([]aistudio.Content, 0, len(request.Contents))
	for _, content := range request.Contents {
		parts, hasResult, err := mapGeminiParts(content.Parts)
		if err != nil {
			return aistudio.GenerateRequest{}, err
		}
		if len(parts) == 0 {
			continue
		}
		role, err := geminiRole(content.Role)
		if err != nil {
			return aistudio.GenerateRequest{}, err
		}
		if hasResult {
			role = aistudio.RoleTool
		}
		contents = append(contents, aistudio.Content{Role: role, Parts: parts})
	}
	contents = prependSystemMedia(contents, systemMedia)
	tools, err := mapGeminiTools(request.Tools, request.ToolConfig)
	if err != nil {
		return aistudio.GenerateRequest{}, err
	}
	config := aistudio.GenerationConfig{
		Temperature:          request.GenerationConfig.Temperature,
		TopP:                 request.GenerationConfig.TopP,
		TopK:                 request.GenerationConfig.TopK,
		MaxOutputTokens:      request.GenerationConfig.MaxOutputTokens,
		StopSequences:        normalizeStopSequences(request.GenerationConfig.StopSequences),
		ResponseMIMEType:     request.GenerationConfig.ResponseMIMEType,
		ResponseSchema:       request.GenerationConfig.ResponseSchema,
		ResponseJSONSchema:   request.GenerationConfig.ResponseJSONSchema,
		Seed:                 request.GenerationConfig.Seed,
		MediaResolution:      request.GenerationConfig.MediaResolution,
		EnhancedCivicAnswers: request.GenerationConfig.EnhancedCivic,
	}
	config.ResponseModalities, err = mapGeminiResponseModalities(request.GenerationConfig.ResponseModalities)
	if err != nil {
		return aistudio.GenerateRequest{}, err
	}
	if image := request.GenerationConfig.ImageConfig; image != nil {
		config.ImageConfig = &aistudio.ImageConfig{AspectRatio: image.AspectRatio, ImageSize: image.ImageSize}
	}
	config.SpeechConfig, err = mapGeminiSpeechConfig(request.GenerationConfig.SpeechConfig)
	if err != nil {
		return aistudio.GenerateRequest{}, err
	}
	config.TranscriptionConfig = mapGeminiTranscriptionConfig(request.GenerationConfig.TranscriptionConfig)
	for _, format := range request.GenerationConfig.ResponseFormat {
		if text := format.Text; text != nil {
			if text.MIMEType != "" {
				config.ResponseMIMEType = geminiResponseFormatValue(text.MIMEType)
			}
			if len(text.Schema) > 0 {
				config.ResponseJSONSchema = text.Schema
			}
		}
		if image := format.Image; image != nil {
			config.ImageConfig = &aistudio.ImageConfig{AspectRatio: geminiResponseFormatValue(image.AspectRatio), ImageSize: geminiResponseFormatValue(image.ImageSize)}
		}
		if video := format.Video; video != nil {
			seconds, err := videoDurationSeconds(video.Duration)
			if err != nil {
				return aistudio.GenerateRequest{}, fmt.Errorf("generationConfig.responseFormat.video: %w", err)
			}
			config.VideoConfig = &aistudio.VideoConfig{AspectRatio: geminiResponseFormatValue(video.AspectRatio), DurationSeconds: seconds, Resolution: video.Resolution}
		}
	}
	for _, name := range request.GenerationConfig.ResponseModalities {
		if strings.EqualFold(strings.TrimSpace(name), "VIDEO") && config.VideoConfig == nil {
			config.VideoConfig = &aistudio.VideoConfig{}
		}
	}
	if video := request.GenerationConfig.VideoConfig; video != nil && video.Task != "" {
		if config.VideoConfig == nil {
			config.VideoConfig = &aistudio.VideoConfig{}
		}
		config.VideoConfig.Task = video.Task
	}
	config.HideThinking = true
	if request.GenerationConfig.ThinkingConfig != nil {
		// thinkingBudget -1 为动态思考，使用模型默认思考设置
		if budget := request.GenerationConfig.ThinkingConfig.ThinkingBudget; budget == nil || *budget != -1 {
			config.ThinkingBudget = budget
		}
		config.ReasoningEffort = request.GenerationConfig.ThinkingConfig.ThinkingLevel
		config.HideThinking = !request.GenerationConfig.ThinkingConfig.IncludeThoughts
	}
	safety := make([]aistudio.SafetySetting, 0, len(request.SafetySettings))
	for _, setting := range request.SafetySettings {
		safety = append(safety, aistudio.SafetySetting{Category: setting.Category, Threshold: setting.Threshold})
	}
	return aistudio.GenerateRequest{
		ID: id, Model: model, System: system, Contents: contents, Config: config, Tools: tools,
		SafetySettings: safety, ServiceTier: request.ServiceTier, Store: request.Store, Labels: request.Labels,
	}, nil
}

// geminiAudioMIMETypes 为 responseFormat.audio.mimeType 枚举名对应的输出 MIME
var geminiAudioMIMETypes = map[string]string{
	"AUDIO_MP3": "audio/mp3", "AUDIO_OGG_OPUS": "audio/ogg_opus", "AUDIO_L16": "audio/l16",
	"AUDIO_WAV": "audio/wav", "AUDIO_ALAW": "audio/alaw", "AUDIO_MULAW": "audio/mulaw",
}

// audioFormat 返回 responseFormat.audio 指定的音频输出格式，未指定 MIME 时为 WAV，没有音频格式时为 nil
func (config geminiGenerationConfig) audioFormat() (*outputAudioFormat, error) {
	for _, format := range config.ResponseFormat {
		if audio := format.Audio; audio != nil {
			mimeType := "audio/wav"
			if name := strings.ToUpper(strings.TrimSpace(audio.MIMEType)); name != "" && name != "MIME_TYPE_UNSPECIFIED" {
				var ok bool
				if mimeType, ok = geminiAudioMIMETypes[name]; !ok {
					return nil, fmt.Errorf("generationConfig.responseFormat.audio.mimeType %q is not supported", audio.MIMEType)
				}
			}
			return &outputAudioFormat{MIME: mimeType, SampleRate: audio.SampleRate, BitRate: audio.BitRate}, nil
		}
	}
	return nil, nil
}

// geminiMaxCandidates 是 candidateCount 的上限
const geminiMaxCandidates = 8

func (config geminiGenerationConfig) validate() error {
	if config.CandidateCount != nil && (*config.CandidateCount < 0 || *config.CandidateCount > geminiMaxCandidates) {
		return fmt.Errorf("generationConfig.candidateCount must be between 1 and %d", geminiMaxCandidates)
	}
	return nil
}

// candidates 返回请求的候选数，省略或为 0 时为 1
func (config geminiGenerationConfig) candidates() int {
	if config.CandidateCount == nil || *config.CandidateCount == 0 {
		return 1
	}
	return int(*config.CandidateCount)
}

// mapGeminiTranscriptionConfig 转换 generationConfig.audioTranscriptionConfig
func mapGeminiTranscriptionConfig(input *geminiTranscriptionConfig) *aistudio.TranscriptionConfig {
	if input == nil {
		return nil
	}
	config := &aistudio.TranscriptionConfig{
		WordTimestamps: input.WordTimestamp, SpeakerLabels: input.Diarization, LanguageCodes: input.LanguageCodes,
		CustomVocabulary: input.CustomVocabulary, AdaptationPhrases: input.AdaptationPhrases,
		LanguageAuto: input.LanguageAuto != nil, SmartTranscription: strings.EqualFold(input.Mode, "SMART"),
	}
	if input.LanguageHints != nil {
		config.LanguageHints = input.LanguageHints.LanguageCodes
	}
	return config
}

func mapGeminiResponseModalities(input []string) ([]aistudio.ResponseModality, error) {
	if input == nil {
		return nil, nil
	}
	modalities := make([]aistudio.ResponseModality, 0, len(input))
	for _, raw := range input {
		modality := aistudio.ResponseModality(strings.ToUpper(strings.TrimSpace(raw)))
		switch modality {
		case aistudio.ResponseModalityText, aistudio.ResponseModalityImage, aistudio.ResponseModalityAudio:
			modalities = append(modalities, modality)
		case "MODALITY_UNSPECIFIED", "VIDEO":
		default:
			return nil, fmt.Errorf("unsupported response modality %q", raw)
		}
	}
	if len(modalities) == 0 && len(input) > 0 {
		return nil, nil
	}
	return modalities, nil
}

func mapGeminiSpeechConfig(input *geminiSpeechConfig) (*aistudio.SpeechConfig, error) {
	if input == nil {
		return nil, nil
	}
	if input.VoiceConfig != nil && input.MultiSpeakerVoiceConfig != nil {
		return nil, fmt.Errorf("speechConfig cannot contain both voiceConfig and multiSpeakerVoiceConfig")
	}
	config := &aistudio.SpeechConfig{LanguageCode: strings.TrimSpace(input.LanguageCode)}
	if input.VoiceConfig != nil {
		config.VoiceName, config.Voice = input.VoiceConfig.voices()
		if config.VoiceName == "" && config.Voice == "" {
			return nil, fmt.Errorf("speechConfig.voiceConfig requires prebuiltVoiceConfig.voiceName or voice")
		}
	}
	if input.MultiSpeakerVoiceConfig != nil {
		config.Mode = input.MultiSpeakerVoiceConfig.Mode
		for index, speaker := range input.MultiSpeakerVoiceConfig.SpeakerVoiceConfigs {
			name, voice := speaker.VoiceConfig.voices()
			if strings.TrimSpace(speaker.Speaker) == "" || name == "" && voice == "" {
				return nil, fmt.Errorf("speechConfig.multiSpeakerVoiceConfig.speakerVoiceConfigs[%d] requires speaker and voiceName or voice", index)
			}
			config.Speakers = append(config.Speakers, aistudio.SpeakerVoiceConfig{Speaker: speaker.Speaker, VoiceName: name, Voice: voice})
		}
	}
	return config, nil
}

func geminiRole(role string) (aistudio.Role, error) {
	switch role {
	case "", "user":
		return aistudio.RoleUser, nil
	case "model", "assistant":
		return aistudio.RoleAssistant, nil
	case "function", "tool":
		return aistudio.RoleTool, nil
	default:
		return "", fmt.Errorf("unsupported content role %q", role)
	}
}

func mapGeminiParts(input []geminiPart) ([]aistudio.Part, bool, error) {
	parts := make([]aistudio.Part, 0, len(input))
	hasResult := false
	for index, part := range input {
		variants := 0
		if part.Text != nil {
			variants++
		}
		inline := part.inline()
		if inline != nil {
			variants++
		}
		file := part.file()
		if file != nil {
			variants++
		}
		if part.FunctionCall != nil {
			variants++
		}
		if part.FunctionResponse != nil {
			variants++
		}
		if part.ExecutableCode != nil {
			variants++
		}
		if part.CodeExecutionResult != nil {
			variants++
		}
		if part.ToolCall != nil {
			variants++
		}
		if part.ToolResponse != nil {
			variants++
		}
		video, processing := (*aistudio.VideoMetadata)(part.VideoMetadata), part.MediaProcessing
		resolution := ""
		if part.MediaResolution != nil {
			resolution = part.MediaResolution.Level
		}
		if variants == 0 {
			if part.ThoughtSignature != "" {
				parts = append(parts, aistudio.Part{ThoughtSignature: part.ThoughtSignature})
			}
			continue
		}
		if variants > 1 {
			return nil, false, fmt.Errorf("parts[%d] must contain exactly one data field", index)
		}
		switch {
		case inline != nil:
			mime := inline.MIME()
			if mime == "" || inline.Data == "" {
				return nil, false, fmt.Errorf("inlineData requires mimeType and data")
			}
			data, err := decodeBase64Flexible(inline.Data)
			if err != nil {
				return nil, false, fmt.Errorf("inlineData.data: %w", err)
			}
			mimeType, data := normalizeImagePayload(mime, data)
			parts = append(parts, aistudio.Part{
				InlineData:       &aistudio.Blob{MIME: mimeType, Data: data},
				ThoughtSignature: part.ThoughtSignature,
				VideoMetadata:    video, MediaProcessing: processing, DisplayName: inline.DisplayName, MediaResolution: resolution,
			})
		case file != nil:
			uri := file.URI()
			mime := file.MIME()
			if uri == "" {
				return nil, false, fmt.Errorf("fileData requires fileUri")
			}
			if driveID, ok := geminiUploadedFileID(uri); ok {
				uri = driveID
			}
			if media, ok := aistudio.ExternalMediaForURL(uri); ok {
				parts = append(parts, aistudio.Part{
					ExternalMedia: media, ThoughtSignature: part.ThoughtSignature, VideoMetadata: video, MediaProcessing: processing,
					DisplayName: file.DisplayName, MediaResolution: resolution,
				})
			} else {
				parts = append(parts, aistudio.Part{
					File: &aistudio.FileRef{
						ID: uri, Name: file.DisplayName, MIME: mime,
					},
					ThoughtSignature: part.ThoughtSignature,
					VideoMetadata:    video, MediaProcessing: processing, MediaResolution: resolution,
				})
			}
		case part.FunctionCall != nil:
			if part.FunctionCall.Name == "" {
				return nil, false, fmt.Errorf("functionCall requires name")
			}
			arguments, err := geminiJSONObject(part.FunctionCall.Args, "functionCall.args")
			if err != nil {
				return nil, false, err
			}
			parts = append(parts, aistudio.Part{
				FunctionCall: &aistudio.FunctionCall{
					ID: part.FunctionCall.ID, Name: part.FunctionCall.Name, Arguments: arguments, ThoughtSignature: part.ThoughtSignature,
				},
				ThoughtSignature: part.ThoughtSignature,
			})
		case part.FunctionResponse != nil:
			if part.FunctionResponse.Name == "" {
				return nil, false, fmt.Errorf("functionResponse requires name")
			}
			response, err := geminiJSONObject(part.FunctionResponse.Response, "functionResponse.response")
			if err != nil {
				return nil, false, err
			}
			media, _, err := mapGeminiParts(part.FunctionResponse.Parts)
			if err != nil {
				return nil, false, fmt.Errorf("functionResponse.parts: %w", err)
			}
			parts = append(parts, aistudio.Part{
				FunctionResult: &aistudio.FunctionResult{
					ID: part.FunctionResponse.ID, Name: part.FunctionResponse.Name, Content: response,
				},
				ThoughtSignature: part.ThoughtSignature,
			})
			parts = append(parts, media...)
			hasResult = true
		case part.ToolCall != nil:
			parts = append(parts, aistudio.Part{ToolCall: part.ToolCall.step(part.ToolCall.Args), ThoughtSignature: part.ThoughtSignature})
		case part.ToolResponse != nil:
			parts = append(parts, aistudio.Part{ToolResponse: part.ToolResponse.step(part.ToolResponse.Response), ThoughtSignature: part.ThoughtSignature})
		case part.ExecutableCode != nil:
			parts = append(parts, aistudio.Part{ExecutableCode: part.ExecutableCode, ThoughtSignature: part.ThoughtSignature})
		case part.CodeExecutionResult != nil:
			parts = append(parts, aistudio.Part{CodeExecutionResult: part.CodeExecutionResult, ThoughtSignature: part.ThoughtSignature})
		default:
			if part.Thought || *part.Text == "" {
				if part.ThoughtSignature != "" {
					parts = append(parts, aistudio.Part{ThoughtSignature: part.ThoughtSignature})
				}
				continue
			}
			parts = append(parts, aistudio.Part{Text: *part.Text, ThoughtSignature: part.ThoughtSignature, SpeechMetadata: part.speechMetadata()})
		}
	}
	return parts, hasResult, nil
}

func geminiJSONObject(raw json.RawMessage, field string) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, fmt.Errorf("%s must be an object", field)
	}
	return raw, nil
}

func mapGeminiGoogleSearch(raw json.RawMessage) (*aistudio.GoogleSearchOptions, error) {
	if !geminiRawObjectPresent(raw) {
		return nil, nil
	}
	var config struct {
		SearchTypes *struct {
			WebSearch   json.RawMessage `json:"webSearch"`
			ImageSearch json.RawMessage `json:"imageSearch"`
		} `json:"searchTypes"`
		TimeRangeFilter *struct {
			StartTime string `json:"startTime"`
			EndTime   string `json:"endTime"`
		} `json:"timeRangeFilter"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("googleSearch must be an object")
	}
	options := &aistudio.GoogleSearchOptions{}
	if config.SearchTypes == nil {
		options.WebSearch = true
	} else {
		options.WebSearch = geminiRawObjectPresent(config.SearchTypes.WebSearch)
		options.ImageSearch = geminiRawObjectPresent(config.SearchTypes.ImageSearch)
		if !options.WebSearch && !options.ImageSearch {
			options.WebSearch = true
		}
	}
	if config.TimeRangeFilter != nil {
		timeRange := &aistudio.GoogleSearchTimeRange{}
		if config.TimeRangeFilter.StartTime != "" {
			value, err := time.Parse(time.RFC3339Nano, config.TimeRangeFilter.StartTime)
			if err != nil {
				return nil, fmt.Errorf("googleSearch.timeRangeFilter.startTime: %w", err)
			}
			timeRange.StartTime = value
		}
		if config.TimeRangeFilter.EndTime != "" {
			value, err := time.Parse(time.RFC3339Nano, config.TimeRangeFilter.EndTime)
			if err != nil {
				return nil, fmt.Errorf("googleSearch.timeRangeFilter.endTime: %w", err)
			}
			timeRange.EndTime = value
		}
		options.TimeRange = timeRange
	}
	return options, nil
}

func geminiRawObjectPresent(raw json.RawMessage) bool {
	return len(raw) > 0 && strings.TrimSpace(string(raw)) != "null"
}

func mapGeminiTools(groups []geminiToolGroup, config geminiToolConfig) (aistudio.Tools, error) {
	var mapped aistudio.Tools
	for _, group := range groups {
		for _, declaration := range group.FunctionDeclarations {
			if declaration.Name == "" {
				return aistudio.Tools{}, fmt.Errorf("function declaration name is required")
			}
			parameters := declaration.Parameters
			if len(declaration.ParametersJSONSchema) > 0 {
				parameters = declaration.ParametersJSONSchema
			}
			if len(parameters) == 0 {
				parameters = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			response := declaration.Response
			if len(declaration.ResponseJSONSchema) > 0 {
				response = declaration.ResponseJSONSchema
			}
			mapped.Functions = append(mapped.Functions, aistudio.FunctionDeclaration{
				Name: declaration.Name, Description: declaration.Description, Parameters: parameters, Response: response,
			})
		}
		search, err := mapGeminiGoogleSearch(group.GoogleSearch)
		if err != nil {
			return aistudio.Tools{}, err
		}
		if search != nil {
			if search.WebSearch {
				mapped.Google = appendUnique(mapped.Google, "google_search")
			}
			if search.ImageSearch {
				mapped.Google = appendUnique(mapped.Google, "image_search")
			}
			if mapped.GoogleSearch == nil {
				mapped.GoogleSearch = search
			} else {
				mapped.GoogleSearch.WebSearch = mapped.GoogleSearch.WebSearch || search.WebSearch
				mapped.GoogleSearch.ImageSearch = mapped.GoogleSearch.ImageSearch || search.ImageSearch
				if search.TimeRange != nil {
					mapped.GoogleSearch.TimeRange = search.TimeRange
				}
			}
		}
		if geminiRawObjectPresent(group.GoogleSearchRetrieval) {
			mapped.Google = appendUnique(mapped.Google, "google_search")
		}
		if geminiRawObjectPresent(group.URLContext) {
			mapped.Google = appendUnique(mapped.Google, "url_context")
		}
		if geminiRawObjectPresent(group.CodeExecution) {
			mapped.Google = appendUnique(mapped.Google, "code_execution")
		}
		if geminiRawObjectPresent(group.GoogleMaps) {
			mapped.Google = appendUnique(mapped.Google, "google_maps")
			var maps struct {
				EnableWidget bool `json:"enableWidget"`
			}
			if err := json.Unmarshal(group.GoogleMaps, &maps); err != nil {
				return aistudio.Tools{}, fmt.Errorf("googleMaps: %w", err)
			}
			mapped.MapsWidget = mapped.MapsWidget || maps.EnableWidget
		}
		if geminiRawObjectPresent(group.ImageSearch) {
			mapped.Google = appendUnique(mapped.Google, "image_search")
		}
		if computer := group.ComputerUse; computer != nil {
			mapped.ComputerUse = &aistudio.ComputerUse{
				Environment: computer.Environment, ExcludedPredefinedFunctions: computer.ExcludedPredefinedFunctions,
				DisabledSafetyPolicies: computer.DisabledSafetyPolicies, EnablePromptInjectionDetection: computer.EnablePromptInjectionDetection,
			}
		}
		for _, server := range group.MCPServers {
			mapped.MCPServers = append(mapped.MCPServers, aistudio.MCPServer{
				Name: server.Name, URL: server.Transport.URL, Headers: server.Transport.Headers, Timeout: server.Transport.Timeout,
				SSEReadTimeout: server.Transport.SSEReadTimeout, TerminateOnClose: server.Transport.TerminateOnClose,
			})
		}
		if geminiRawObjectPresent(group.FileSearch) {
			mapped.FileSearch = group.FileSearch
		}
	}
	var toolConfig aistudio.ToolConfig
	toolConfig.AllowedFunctionNames = config.FunctionCallingConfig.AllowedFunctionNames
	toolConfig.ServerSideInvocations = config.IncludeServerSideToolInvocations
	if retrieval := config.RetrievalConfig; retrieval != nil {
		toolConfig.Retrieval = &aistudio.RetrievalConfig{LanguageCode: retrieval.LanguageCode}
		if retrieval.LatLng != nil {
			toolConfig.Retrieval.Latitude, toolConfig.Retrieval.Longitude = retrieval.LatLng.Latitude, retrieval.LatLng.Longitude
		}
	}
	switch strings.ToUpper(config.FunctionCallingConfig.Mode) {
	case "", "AUTO", "MODE_UNSPECIFIED":
		toolConfig.Mode = "auto"
	case "ANY":
		toolConfig.Mode = "required"
	case "VALIDATED":
		toolConfig.Mode = "validated"
	case "NONE":
		mapped.Functions, mapped.ComputerUse = nil, nil
		if len(mapped.Google) == 0 && mapped.GoogleSearch == nil && len(mapped.MCPServers) == 0 && len(mapped.FileSearch) == 0 {
			toolConfig.Mode = "none"
		} else {
			toolConfig.Mode = "auto"
		}
	default:
		return aistudio.Tools{}, fmt.Errorf("unsupported functionCallingConfig mode %q", config.FunctionCallingConfig.Mode)
	}
	mapped.ToolConfig = toolConfig
	return mapped, nil
}

// handleGeminiCountTokens 统计请求的输入 token 数，引用显式缓存时另给出缓存部分的 token 数
func (s *server) handleGeminiCountTokens(w http.ResponseWriter, r *http.Request, request aistudio.GenerateRequest, cache *geminiCache) {
	count, err := s.service.CountTokens(r.Context(), aistudio.TokenCountRequest{
		Model: request.Model, System: request.System, Contents: request.Contents, Tools: request.Tools,
		MediaResolution: request.Config.MediaResolution,
	})
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiRequestError(w, err)
		}
		return
	}
	if cache != nil {
		count = cache.requestTokens(count)
	}
	response := map[string]any{}
	if count.InputTokens != 0 {
		response["totalTokens"] = count.InputTokens
	}
	if count.System != nil {
		response["systemInstructionsTokens"] = count.System
	}
	if len(count.Contents) > 0 {
		response["contentTokens"] = count.Contents
	}
	if len(count.Tools) > 0 {
		response["toolTokens"] = count.Tools
	}
	if len(count.PromptTokensDetails) > 0 {
		response["promptTokensDetails"] = count.PromptTokensDetails
	}
	if cache != nil {
		response["cachedContentTokenCount"] = cache.tokens
		if len(cache.details) > 0 {
			response["cacheTokensDetails"] = cache.details
		}
	}
	writeJSON(w, http.StatusOK, officialOrder("gemini", response))
}

// handleGeminiGenerate 为每个候选启动一次生成，任一候选失败时整个请求按首个错误失败，audio 非空时音频按 responseFormat.audio 转换
func (s *server) handleGeminiGenerate(w http.ResponseWriter, r *http.Request, request aistudio.GenerateRequest, stream bool, count int, audio *outputAudioFormat) {
	request.Unary = !stream || s.responseModes().FakeStream
	if !stream {
		var stopKeepalive func()
		w, stopKeepalive = s.nonstreamKeepalive(w, r)
		defer stopKeepalive()
	}
	candidates := s.startChatChoices(r.Context(), request, count, stream)
	defer candidates.cancel()
	if err := candidates.err; err != nil {
		candidates.settle(r.Context())
		writeGeminiFailure(w, r, request, err, stream)
		return
	}
	if stream {
		s.streamGemini(w, r, request, candidates, audio)
		return
	}
	results, err := candidates.run(func(_ int, events <-chan aistudio.Event) (generationResult, error) {
		result, err := consumeEvents(candidates.ctx, events, nil)
		if err == nil && audio != nil {
			err = result.convertAudio(*audio)
		}
		return result, err
	})
	if err != nil {
		candidates.settle(r.Context())
		writeGeminiFailure(w, r, request, err, false)
		return
	}
	candidates.record(r.Context(), results)
	writeJSON(w, http.StatusOK, officialOrder("gemini", buildGeminiResponse(request, results)))
}

// writeGeminiFailure 写出响应头之前的生成失败，上游拒绝输入时按 Gemini API 返回 200 与 promptFeedback
func writeGeminiFailure(w http.ResponseWriter, r *http.Request, request aistudio.GenerateRequest, err error, stream bool) {
	if !shouldWriteRequestError(r, err) {
		return
	}
	response, ok := geminiPromptFeedback(request, err)
	switch {
	case !ok:
		writeGeminiRequestError(w, err)
	case stream:
		if output, err := openGeminiStream(w, r); err == nil {
			_ = output.write(response)
			output.close()
		}
	default:
		writeJSON(w, http.StatusOK, officialOrder("gemini", response))
	}
}

// geminiPromptFeedback 把上游输入拒绝投影为不含候选的 Gemini 响应
func geminiPromptFeedback(request aistudio.GenerateRequest, err error) (map[string]any, bool) {
	var feedback *aistudio.PromptFeedbackError
	if !errors.As(err, &feedback) {
		return nil, false
	}
	reason := strings.ToUpper(strings.TrimSpace(feedback.Reason))
	if reason == "" || reason == "UNSPECIFIED" {
		reason = "BLOCK_REASON_UNSPECIFIED"
	}
	block := map[string]any{"blockReason": reason}
	if feedback.Message != "" {
		block["blockReasonMessage"] = feedback.Message
	}
	if len(feedback.SafetyRatings) > 0 {
		block["safetyRatings"] = feedback.SafetyRatings
	}
	return map[string]any{"promptFeedback": block, "modelVersion": request.Model, "responseId": request.ID}, true
}

// buildGeminiResponse 按候选序号输出各候选内容与终止原因，用量为全部候选之和
func buildGeminiResponse(request aistudio.GenerateRequest, results []generationResult) map[string]any {
	candidates := make([]any, 0, len(results))
	for index, result := range results {
		candidate := map[string]any{"index": index}
		if parts := geminiOutputParts(result); len(parts) > 0 {
			candidate["content"] = map[string]any{"role": "model", "parts": parts}
		}
		setGeminiCandidateEnd(candidate, result)
		candidates = append(candidates, candidate)
	}
	response := map[string]any{
		"candidates":   candidates,
		"modelVersion": request.Model,
		"responseId":   cmp.Or(results[0].responseID, request.ID),
	}
	if results[0].providerModel != "" {
		response["modelVersion"] = results[0].providerModel
	}
	if results[0].turnToken != "" {
		response["turnToken"] = results[0].turnToken
	}
	if len(results[0].debugInfo) > 0 {
		response["debugInfo"] = results[0].debugInfo
	}
	if usage := sumUsage(results); usage != nil {
		response["usageMetadata"] = geminiUsage(usage)
	}
	return response
}

// geminiOutputParts 把一次生成累积的 Part 投影为非流式响应 Part，独立签名并入前一个没有签名的 Part
func geminiOutputParts(result generationResult) []map[string]any {
	var output aistudio.Output
	for _, event := range result.events {
		output.Add(event)
	}
	parts := make([]map[string]any, 0, len(output.Parts))
	for _, part := range output.Parts {
		if part.SignatureOnly() && len(parts) > 0 && parts[len(parts)-1]["thoughtSignature"] == nil {
			parts[len(parts)-1]["thoughtSignature"] = part.ThoughtSignature
			continue
		}
		parts = append(parts, geminiOutputPart(part))
	}
	return parts
}

// geminiOutputPart 把规范 Part 投影为 Gemini 响应 Part
func geminiOutputPart(part aistudio.Part) map[string]any {
	var value map[string]any
	switch {
	case part.FunctionCall != nil:
		value = map[string]any{"functionCall": map[string]any{
			"id": part.FunctionCall.ID, "name": part.FunctionCall.Name, "args": part.FunctionCall.Arguments,
		}}
	case part.ToolCall != nil, part.ToolResponse != nil:
		value = geminiServerToolPart(part)
	case part.ExecutableCode != nil:
		value = map[string]any{"executableCode": part.ExecutableCode}
	case part.CodeExecutionResult != nil:
		value = map[string]any{"codeExecutionResult": part.CodeExecutionResult}
	case part.InlineData != nil:
		value = map[string]any{"inlineData": map[string]any{
			"mimeType": part.InlineData.MIME, "data": base64.StdEncoding.EncodeToString(part.InlineData.Data),
		}}
	case part.ExternalMedia != nil:
		value = map[string]any{"fileData": map[string]any{"mimeType": part.ExternalMedia.MIME, "fileUri": part.ExternalMedia.URL}}
	default:
		value = map[string]any{"text": part.Text}
		if part.Transcript != nil {
			value["audioTranscription"] = geminiAudioTranscription(*part.Transcript)
		}
	}
	if part.Thought {
		value["thought"] = true
	}
	signature := part.ThoughtSignature
	if part.FunctionCall != nil {
		signature = cmp.Or(signature, part.FunctionCall.ThoughtSignature)
	}
	if signature != "" {
		value["thoughtSignature"] = signature
	}
	return value
}

// geminiServerToolPart 把服务端工具步骤投影为 Gemini 的 toolCall 或 toolResponse Part
func geminiServerToolPart(part aistudio.Part) map[string]any {
	step, key, payload := part.ToolResponse, "toolResponse", "response"
	if part.ToolCall != nil {
		step, key, payload = part.ToolCall, "toolCall", "args"
	}
	value := map[string]any{"id": step.ID}
	if step.Type != "" {
		value["toolType"] = step.Type
	}
	if key == "toolCall" && step.Name != "" {
		value["toolName"] = step.Name
	}
	if len(step.Data) != 0 {
		value[payload] = step.Data
	}
	return map[string]any{key: value}
}

// geminiAudioTranscription 把转录说话人与逐词时间投影为 Part.audioTranscription
func geminiAudioTranscription(transcript aistudio.TranscriptMetadata) map[string]any {
	value := map[string]any{"text": transcript.Text}
	if transcript.Speaker != "" {
		value["speakerLabel"] = transcript.Speaker
	}
	if len(transcript.Words) > 0 {
		words := make([]map[string]any, 0, len(transcript.Words))
		for _, word := range transcript.Words {
			words = append(words, map[string]any{"word": word.Word, "startOffset": geminiDuration(word.Start), "endOffset": geminiDuration(word.End)})
		}
		value["words"] = words
	}
	return value
}

// geminiDuration 按 protobuf JSON 规则把转录时间写成 Duration 字符串，如 "0.800s"、"2s"
func geminiDuration(duration aistudio.TranscriptDuration) string {
	switch {
	case duration.Nanos == 0:
		return fmt.Sprintf("%ds", duration.Seconds)
	case duration.Nanos%1_000_000 == 0:
		return fmt.Sprintf("%d.%03ds", duration.Seconds, duration.Nanos/1_000_000)
	case duration.Nanos%1_000 == 0:
		return fmt.Sprintf("%d.%06ds", duration.Seconds, duration.Nanos/1_000)
	}
	return fmt.Sprintf("%d.%09ds", duration.Seconds, duration.Nanos)
}

func geminiCitationMetadata(citations []aistudio.Citation) map[string]any {
	sources := make([]map[string]any, 0, len(citations))
	for _, citation := range citations {
		source := map[string]any{
			"uri": citation.URL, "title": citation.Title, "startIndex": citation.Start, "endIndex": citation.End,
		}
		if citation.License != "" {
			source["license"] = citation.License
		}
		sources = append(sources, source)
	}
	return map[string]any{"citationSources": sources}
}

func geminiGroundingMetadata(metadata aistudio.GroundingMetadata) map[string]any {
	output := map[string]any{}
	if metadata.SearchEntryPoint != nil {
		entry := map[string]any{}
		if metadata.SearchEntryPoint.RenderedContent != "" {
			entry["renderedContent"] = metadata.SearchEntryPoint.RenderedContent
		}
		if metadata.SearchEntryPoint.SDKBlob != "" {
			entry["sdkBlob"] = metadata.SearchEntryPoint.SDKBlob
		}
		output["searchEntryPoint"] = entry
	}
	if len(metadata.Chunks) > 0 {
		chunks := make([]map[string]any, 0, len(metadata.Chunks))
		for _, chunk := range metadata.Chunks {
			value := map[string]any{"uri": chunk.URI, "title": chunk.Title}
			var entry map[string]any
			switch chunk.Source {
			case "web":
				for key, field := range map[string]string{"domain": chunk.Domain, "siteName": chunk.SiteName} {
					if field != "" {
						value[key] = field
					}
				}
				if len(chunk.SearchResultMapping) > 0 {
					value["searchResultMapping"] = chunk.SearchResultMapping
				}
				entry = map[string]any{"web": value}
			case "image":
				image := map[string]any{"sourceUri": chunk.URI}
				for key, field := range map[string]string{"imageUri": chunk.ImageURI, "title": chunk.Title, "domain": chunk.Domain} {
					if field != "" {
						image[key] = field
					}
				}
				entry = map[string]any{"image": image}
			case "retrieved_context":
				value["text"] = chunk.Text
				entry = map[string]any{"retrievedContext": value}
			case "maps":
				value["text"] = chunk.Text
				value["placeId"] = chunk.PlaceID
				if len(chunk.PlaceAnswerSources) > 0 {
					value["placeAnswerSources"] = chunk.PlaceAnswerSources
				}
				entry = map[string]any{"maps": value}
			default:
				continue
			}
			for key, field := range map[string]string{"origin": chunk.Origin, "referenceId": chunk.ReferenceID} {
				if field != "" {
					entry[key] = field
				}
			}
			chunks = append(chunks, entry)
		}
		output["groundingChunks"] = chunks
	}
	if len(metadata.Supports) > 0 {
		supports := make([]map[string]any, 0, len(metadata.Supports))
		for _, support := range metadata.Supports {
			segment := map[string]any{}
			for name, value := range map[string]int{"partIndex": support.Segment.PartIndex, "startIndex": support.Segment.StartIndex, "endIndex": support.Segment.EndIndex} {
				if value != 0 {
					segment[name] = value
				}
			}
			if support.Segment.Text != "" {
				segment["text"] = support.Segment.Text
			}
			value := map[string]any{"segment": segment, "groundingChunkIndices": support.ChunkIndices}
			if support.Origin != "" {
				value["origin"] = support.Origin
			}
			if len(support.ConfidenceScores) > 0 {
				value["confidenceScores"] = support.ConfidenceScores
			}
			if len(support.RenderedParts) > 0 {
				value["renderedParts"] = support.RenderedParts
			}
			supports = append(supports, value)
		}
		output["groundingSupports"] = supports
	}
	if metadata.DynamicRetrievalScore != nil {
		output["retrievalMetadata"] = map[string]any{
			"googleSearchDynamicRetrievalScore": *metadata.DynamicRetrievalScore,
		}
	}
	if len(metadata.WebSearchQueries) > 0 {
		output["webSearchQueries"] = metadata.WebSearchQueries
	}
	if len(metadata.ImageSearchQueries) > 0 {
		output["imageSearchQueries"] = metadata.ImageSearchQueries
	}
	if metadata.MapsWidgetContextToken != "" {
		output["googleMapsWidgetContextToken"] = metadata.MapsWidgetContextToken
	}
	return output
}

func geminiFinishReason(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "", "stop", "stop_sequence":
		return "STOP"
	case "unspecified":
		return "FINISH_REASON_UNSPECIFIED"
	case "max_tokens", "max_output_tokens", "length":
		return "MAX_TOKENS"
	case "safety", "content_filter", "blocked":
		return "SAFETY"
	case "recitation":
		return "RECITATION"
	case "language":
		return "LANGUAGE"
	case "other":
		return "OTHER"
	case "blocklist":
		return "BLOCKLIST"
	case "prohibited_content":
		return "PROHIBITED_CONTENT"
	case "spii":
		return "SPII"
	case "malformed_function_call":
		return "MALFORMED_FUNCTION_CALL"
	case "image_safety":
		return "IMAGE_SAFETY"
	case "unexpected_tool_call":
		return "UNEXPECTED_TOOL_CALL"
	case "too_many_tool_calls":
		return "TOO_MANY_TOOL_CALLS"
	case "image_prohibited_content":
		return "IMAGE_PROHIBITED_CONTENT"
	case "image_other":
		return "IMAGE_OTHER"
	case "no_image":
		return "NO_IMAGE"
	case "image_recitation":
		return "IMAGE_RECITATION"
	case "missing_thought_signature":
		return "MISSING_THOUGHT_SIGNATURE"
	case "malformed_response":
		return "MALFORMED_RESPONSE"
	case "continuation":
		return "CONTINUATION"
	}
	if name, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(reason)), "provider_"); ok && strings.Trim(name, "abcdefghijklmnopqrstuvwxyz_") == "" {
		return strings.ToUpper(name)
	}
	return "OTHER"
}

// setGeminiCandidateEnd 写入候选的终止原因、搜索来源与上游引用
func setGeminiCandidateEnd(candidate map[string]any, result generationResult) {
	setGeminiFinish(candidate, result.finishReason, result.finishMessage)
	if len(result.safetyRatings) > 0 {
		candidate["safetyRatings"] = result.safetyRatings
	}
	if len(result.detailedFinishReason) > 0 {
		candidate["detailedFinishReason"] = result.detailedFinishReason
	}
	if result.grounding != nil {
		if grounding := geminiGroundingMetadata(*result.grounding); len(grounding) > 0 {
			candidate["groundingMetadata"] = grounding
		}
		if len(result.grounding.URLContext) > 0 {
			urls := make([]map[string]any, 0, len(result.grounding.URLContext))
			for _, item := range result.grounding.URLContext {
				urls = append(urls, map[string]any{"retrievedUrl": item.RetrievedURL, "urlRetrievalStatus": item.Status})
			}
			candidate["urlContextMetadata"] = map[string]any{"urlMetadata": urls}
		}
	}
	citations := make([]aistudio.Citation, 0)
	for _, event := range result.events {
		if event.Kind == aistudio.EventCitation && event.Citation != nil {
			citations = append(citations, *event.Citation)
		}
	}
	if len(citations) > 0 {
		candidate["citationMetadata"] = geminiCitationMetadata(citations)
	}
}

func setGeminiFinish(candidate map[string]any, reason, message string) {
	candidate["finishReason"] = geminiFinishReason(reason)
	normalized := strings.ToLower(strings.TrimSpace(reason))
	if message != "" {
		candidate["finishMessage"] = message
	} else if strings.HasPrefix(normalized, "provider_") && candidate["finishReason"] == "OTHER" {
		candidate["finishMessage"] = "AI Studio finish reason " + strings.TrimPrefix(normalized, "provider_")
	}
}

// geminiUsage 按 Gemini API usageMetadata 输出用量：值为 0 的计数与上游没有的字段省略
func geminiUsage(usage *aistudio.Usage) map[string]any {
	metadata := map[string]any{}
	for name, value := range map[string]int64{
		"promptTokenCount": usage.InputTokens, "candidatesTokenCount": usage.OutputTokens, "totalTokenCount": usage.TotalTokens,
		"cachedContentTokenCount": usage.CachedTokens, "toolUsePromptTokenCount": usage.ToolTokens, "thoughtsTokenCount": usage.ReasoningTokens,
	} {
		if value != 0 {
			metadata[name] = value
		}
	}
	details := usage.Details
	if details == nil {
		return metadata
	}
	for name, value := range map[string]json.RawMessage{
		"billedToolCalls": details.BilledToolCalls, "toolCallStats": details.ToolCallStats, "modelInvocationTokenCounts": details.ModelInvocationTokenCounts,
		"nonGroundingModelInvocationTokenCounts": details.NonGroundingModelInvocationTokenCounts,
	} {
		if len(value) > 0 {
			metadata[name] = value
		}
	}
	if details.RawPromptTokenCount != 0 {
		metadata["rawPromptTokenCount"] = details.RawPromptTokenCount
	}
	for name, value := range map[string][]aistudio.ModalityTokenCount{
		"promptTokensDetails": details.PromptTokensDetails, "cacheTokensDetails": details.CacheTokensDetails,
		"candidatesTokensDetails": details.CandidatesTokensDetails, "toolUsePromptTokensDetails": details.ToolUsePromptTokensDetails,
	} {
		if len(value) > 0 {
			metadata[name] = value
		}
	}
	if details.ServiceTier != "" {
		metadata["serviceTier"] = details.ServiceTier
	}
	return metadata
}

// geminiStream 写出 streamGenerateContent 的响应块：alt=sse 时为 SSE，否则与官方 REST 一样为逐个对象写出的 JSON 数组
type geminiStream struct {
	w       http.ResponseWriter
	sse     bool
	mu      sync.Mutex
	written bool
}

// openGeminiStream 按 alt 参数写出流式响应头
func openGeminiStream(w http.ResponseWriter, r *http.Request) (*geminiStream, error) {
	stream := &geminiStream{w: w, sse: r.URL.Query().Get("alt") == "sse"}
	if stream.sse {
		return stream, streamHeaders(w)
	}
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	return stream, http.NewResponseController(w).Flush()
}

// write 写出一个响应块
func (stream *geminiStream) write(value map[string]any) error {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.sse {
		return writeSSE(stream.w, "", officialOrder("gemini", value))
	}
	data, err := json.Marshal(officialOrder("gemini", value))
	if err != nil {
		return err
	}
	prefix := "["
	if stream.written {
		prefix = "\r\n,\r\n"
	}
	stream.written = true
	if _, err := io.WriteString(stream.w, prefix+string(data)); err != nil {
		return err
	}
	return http.NewResponseController(stream.w).Flush()
}

// heartbeat 在 SSE 流静默时写出空行，JSON 数组不写
func (stream *geminiStream) heartbeat() error {
	if !stream.sse {
		return nil
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return writeGeminiHeartbeat(stream.w)
}

// close 结束 JSON 数组
func (stream *geminiStream) close() {
	if stream.sse {
		return
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	tail := "\r\n]"
	if !stream.written {
		tail = "[]"
	}
	_, _ = io.WriteString(stream.w, tail)
}

// streamGemini 按到达顺序输出各候选的增量块，带结束原因的上游帧中的内容与全部候选的终止原因、来源和用量之和放在最后一块
func (s *server) streamGemini(w http.ResponseWriter, r *http.Request, request aistudio.GenerateRequest, candidates *chatChoices, audio *outputAudioFormat) {
	stream, err := openGeminiStream(w, r)
	if err != nil {
		return
	}
	defer stream.close()
	write, heartbeat := stream.write, stream.heartbeat
	finalParts := make([][]any, len(candidates.events))
	results, err := candidates.run(func(index int, events <-chan aistudio.Event) (generationResult, error) {
		send := func(event aistudio.Event) error {
			if !event.HasContent() {
				return nil
			}
			part := geminiOutputPart(event.Part)
			if event.FinishFrame {
				finalParts[index] = append(finalParts[index], part)
				return nil
			}
			candidate := map[string]any{"index": index, "content": map[string]any{"role": "model", "parts": []any{part}}}
			frame := map[string]any{"responseId": cmp.Or(event.ResponseID, request.ID), "modelVersion": cmp.Or(event.ProviderModel, request.Model), "candidates": []any{candidate}}
			if event.TurnToken != "" {
				frame["turnToken"] = event.TurnToken
			}
			if event.FrameUsage != nil {
				frame["usageMetadata"] = geminiUsage(event.FrameUsage)
			}
			return write(frame)
		}
		buffered := false
		result, err := consumeStreamEvents(candidates.ctx, events, func(event aistudio.Event) error {
			if audio != nil && audioEvent(event) {
				if !audio.streams(*event.InlineData) {
					buffered = true
					return nil
				}
				converted, err := audio.encode(*event.InlineData)
				if err != nil {
					return err
				}
				event.InlineData = &converted
			}
			return send(event)
		}, heartbeat)
		if err != nil || !buffered {
			return result, err
		}
		joined, err := joinedAudio(result.media)
		if err == nil {
			joined, err = audio.output(joined)
		}
		if err == nil {
			err = send(aistudio.Event{Kind: aistudio.EventMedia, Part: aistudio.Part{InlineData: &joined}, ProviderModel: result.providerModel, TurnToken: result.turnToken})
		}
		return result, err
	})
	if err != nil {
		candidates.settle(r.Context())
		if !shouldWriteRequestError(r, err) {
			return
		}
		if response, ok := geminiPromptFeedback(request, err); ok {
			_ = write(response)
			return
		}
		_ = write(map[string]any{"error": map[string]any{
			"code": statusFromError(err), "message": err.Error(), "status": geminiErrorStatus(err),
		}})
		return
	}
	candidates.record(r.Context(), results)
	model := request.Model
	if results[0].providerModel != "" {
		model = results[0].providerModel
	}
	finished := make([]any, 0, len(results))
	for index, result := range results {
		candidate := map[string]any{"index": index, "content": map[string]any{"role": "model", "parts": append([]any{}, finalParts[index]...)}}
		setGeminiCandidateEnd(candidate, result)
		finished = append(finished, candidate)
	}
	final := map[string]any{
		"responseId": cmp.Or(results[0].responseID, request.ID), "modelVersion": model,
		"candidates": finished,
	}
	if results[0].turnToken != "" {
		final["turnToken"] = results[0].turnToken
	}
	if usage := sumUsage(results); usage != nil {
		final["usageMetadata"] = geminiUsage(usage)
	}
	_ = write(final)
}

// convertAudio 把结果中的音频合并为一段并转换为请求的输出格式，放在第一个音频事件的位置
func (result *generationResult) convertAudio(audio outputAudioFormat) error {
	events := make([]aistudio.Event, 0, len(result.events))
	var media []aistudio.Blob
	position := -1
	for _, event := range result.events {
		if !audioEvent(event) {
			events = append(events, event)
			continue
		}
		if position < 0 {
			position = len(events)
			events = append(events, event)
		}
		media = append(media, *event.InlineData)
	}
	if position < 0 {
		return nil
	}
	joined, err := joinedAudio(media)
	if err == nil {
		joined, err = audio.output(joined)
	}
	if err != nil {
		return err
	}
	events[position].InlineData = &joined
	result.events = events
	return nil
}
