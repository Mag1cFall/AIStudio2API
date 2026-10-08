package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	} `json:"functionResponse"`
	ExecutableCode *struct {
		Language string `json:"language"`
		Code     string `json:"code"`
	} `json:"executableCode"`
	CodeExecutionResult *struct {
		Outcome string `json:"outcome"`
		Output  string `json:"output"`
		Error   string `json:"error"`
	} `json:"codeExecutionResult"`
	SpeechMetadata      *aistudio.SpeechMetadata `json:"speechMetadata"`
	SpeechMetadataSnake *aistudio.SpeechMetadata `json:"speech_metadata"`
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
	ResponseModalities  []string                   `json:"responseModalities"`
	ImageConfig         *geminiImageConfig         `json:"imageConfig"`
	SpeechConfig        *geminiSpeechConfig        `json:"speechConfig"`
	TranscriptionConfig *geminiTranscriptionConfig `json:"transcriptionConfig"`
	Seed                *int64                     `json:"seed"`
	MediaResolution     string                     `json:"mediaResolution"`
	ThinkingConfig      *struct {
		ThinkingBudget *int64 `json:"thinkingBudget"`
		ThinkingLevel  string `json:"thinkingLevel"`
	} `json:"thinkingConfig"`
}

type geminiTranscriptionConfig struct {
	LanguageCodes      []string `json:"languageCodes"`
	CustomVocabulary   []string `json:"customVocabulary"`
	WordTimestamps     *bool    `json:"wordTimestamps"`
	SpeakerLabels      *bool    `json:"speakerLabels"`
	SmartTranscription bool     `json:"smartTranscription"`
}

type geminiImageConfig struct {
	AspectRatio string `json:"aspectRatio"`
	ImageSize   string `json:"imageSize"`
}

type geminiVoiceConfig struct {
	PrebuiltVoiceConfig *struct {
		VoiceName string `json:"voiceName"`
	} `json:"prebuiltVoiceConfig"`
}

type geminiSpeakerVoiceConfig struct {
	Speaker     string            `json:"speaker"`
	VoiceConfig geminiVoiceConfig `json:"voiceConfig"`
}

type geminiSpeechConfig struct {
	VoiceConfig             *geminiVoiceConfig `json:"voiceConfig"`
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
	} `json:"functionDeclarations"`
	GoogleSearch          json.RawMessage `json:"googleSearch"`
	GoogleSearchRetrieval json.RawMessage `json:"googleSearchRetrieval"`
	URLContext            json.RawMessage `json:"urlContext"`
	CodeExecution         json.RawMessage `json:"codeExecution"`
	GoogleMaps            json.RawMessage `json:"googleMaps"`
	ImageSearch           json.RawMessage `json:"imageSearch"`
}

type geminiToolConfig struct {
	FunctionCallingConfig struct {
		Mode                 string   `json:"mode"`
		AllowedFunctionNames []string `json:"allowedFunctionNames"`
	} `json:"functionCallingConfig"`
}

func (s *server) handleGeminiModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.service.Models(r.Context())
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiError(w, statusFromError(err), geminiErrorStatus(err), err.Error())
		}
		return
	}
	data := make([]map[string]any, 0, len(models))
	for _, model := range models {
		data = append(data, geminiModelObject(model))
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": data})
}

func (s *server) handleGeminiModel(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("model")
	models, err := s.service.Models(r.Context())
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiError(w, statusFromError(err), geminiErrorStatus(err), err.Error())
		}
		return
	}
	if model, ok := lookupPublicModel(models, modelID); ok {
		writeJSON(w, http.StatusOK, geminiModelObject(model))
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
		s.handleGeminiCountTokens(w, r, generateRequest)
		return
	}
	s.handleGeminiGenerate(w, r, generateRequest, method == "streamGenerateContent", request.GenerationConfig.candidates())
}

// geminiFilesAPIURI 返回 URI 是否指向 Gemini Files API 的文件资源
func geminiFilesAPIURI(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && strings.EqualFold(parsed.Hostname(), "generativelanguage.googleapis.com") && strings.Contains(parsed.Path, "/files/")
}

// geminiOpaqueFields 为取值是自由 JSON 的字段，其中的键名属于用户数据
var geminiOpaqueFields = map[string]bool{
	"args": true, "response": true, "parameters": true, "parametersJsonSchema": true,
	"responseSchema": true, "responseJsonSchema": true, "partMetadata": true,
}

// geminiCamelKeys 按 proto3 JSON 规则把各层消息字段名转为 lowerCamelCase，同层两种写法并存时保留驼峰字段，自由 JSON 字段的取值原样保留
func geminiCamelKeys(raw []byte) ([]byte, bool, error) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || value[0] != '{' && value[0] != '[' {
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
	var system string
	if request.SystemInstruction != nil {
		parts, _, err := mapGeminiParts(request.SystemInstruction.Parts)
		if err != nil {
			return aistudio.GenerateRequest{}, fmt.Errorf("systemInstruction: %w", err)
		}
		var text strings.Builder
		for _, part := range parts {
			if part.Text == "" && (part.InlineData != nil || part.File != nil || part.FunctionCall != nil || part.FunctionResult != nil) {
				return aistudio.GenerateRequest{}, fmt.Errorf("systemInstruction must contain text")
			}
			text.WriteString(part.Text)
		}
		system = text.String()
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
	tools, err := mapGeminiTools(request.Tools, request.ToolConfig)
	if err != nil {
		return aistudio.GenerateRequest{}, err
	}
	config := aistudio.GenerationConfig{
		Temperature:      request.GenerationConfig.Temperature,
		TopP:             request.GenerationConfig.TopP,
		TopK:             request.GenerationConfig.TopK,
		MaxOutputTokens:  request.GenerationConfig.MaxOutputTokens,
		StopSequences:    normalizeStopSequences(request.GenerationConfig.StopSequences),
		ResponseMIMEType: request.GenerationConfig.ResponseMIMEType,
		ResponseSchema:   request.GenerationConfig.ResponseSchema,
		Seed:             request.GenerationConfig.Seed,
		MediaResolution:  request.GenerationConfig.MediaResolution,
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
	config.TranscriptionConfig, err = mapGeminiTranscriptionConfig(request.GenerationConfig.TranscriptionConfig)
	if err != nil {
		return aistudio.GenerateRequest{}, err
	}
	if len(request.GenerationConfig.ResponseJSONSchema) > 0 {
		config.ResponseSchema = request.GenerationConfig.ResponseJSONSchema
	}
	if request.GenerationConfig.ThinkingConfig != nil {
		config.ThinkingBudget = request.GenerationConfig.ThinkingConfig.ThinkingBudget
		config.ReasoningEffort = request.GenerationConfig.ThinkingConfig.ThinkingLevel
	}
	safety := make([]aistudio.SafetySetting, 0, len(request.SafetySettings))
	for _, setting := range request.SafetySettings {
		safety = append(safety, aistudio.SafetySetting{Category: setting.Category, Threshold: setting.Threshold})
	}
	return aistudio.GenerateRequest{
		ID: id, Model: model, System: system, Contents: contents, Config: config, Tools: tools,
		SafetySettings: safety,
	}, nil
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

func mapGeminiTranscriptionConfig(input *geminiTranscriptionConfig) (*aistudio.TranscriptionConfig, error) {
	if input == nil {
		return nil, nil
	}
	config := &aistudio.TranscriptionConfig{
		SmartTranscription: input.SmartTranscription,
	}
	if input.WordTimestamps != nil {
		config.WordTimestamps = *input.WordTimestamps
	}
	if input.SpeakerLabels != nil {
		config.SpeakerLabels = *input.SpeakerLabels
	}
	for _, vocabulary := range input.CustomVocabulary {
		if vocabulary = strings.TrimSpace(vocabulary); vocabulary != "" {
			config.CustomVocabulary = append(config.CustomVocabulary, vocabulary)
		}
	}
	for _, language := range input.LanguageCodes {
		language = strings.TrimSpace(language)
		if language != "" && !strings.EqualFold(language, "detect") {
			config.LanguageCodes = append(config.LanguageCodes, language)
		}
	}
	if config.SmartTranscription {
		if input.WordTimestamps != nil && *input.WordTimestamps || input.SpeakerLabels != nil && *input.SpeakerLabels {
			return nil, fmt.Errorf("transcriptionConfig.smartTranscription cannot be combined with wordTimestamps or speakerLabels")
		}
		config.WordTimestamps = false
		config.SpeakerLabels = false
	}
	return config, nil
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
		case "MODALITY_UNSPECIFIED":
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
	config := &aistudio.SpeechConfig{}
	if input.VoiceConfig != nil {
		if input.VoiceConfig.PrebuiltVoiceConfig == nil || strings.TrimSpace(input.VoiceConfig.PrebuiltVoiceConfig.VoiceName) == "" {
			return nil, fmt.Errorf("speechConfig.voiceConfig requires prebuiltVoiceConfig.voiceName")
		}
		config.VoiceName = input.VoiceConfig.PrebuiltVoiceConfig.VoiceName
	}
	if input.MultiSpeakerVoiceConfig != nil {
		config.Mode = input.MultiSpeakerVoiceConfig.Mode
		for index, speaker := range input.MultiSpeakerVoiceConfig.SpeakerVoiceConfigs {
			if strings.TrimSpace(speaker.Speaker) == "" || speaker.VoiceConfig.PrebuiltVoiceConfig == nil || strings.TrimSpace(speaker.VoiceConfig.PrebuiltVoiceConfig.VoiceName) == "" {
				return nil, fmt.Errorf("speechConfig.multiSpeakerVoiceConfig.speakerVoiceConfigs[%d] requires speaker and voiceName", index)
			}
			config.Speakers = append(config.Speakers, aistudio.SpeakerVoiceConfig{
				Speaker: speaker.Speaker, VoiceName: speaker.VoiceConfig.PrebuiltVoiceConfig.VoiceName,
			})
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
				parts = append(parts, aistudio.Part{ExternalMedia: media, ThoughtSignature: part.ThoughtSignature})
			} else {
				parts = append(parts, aistudio.Part{
					File: &aistudio.FileRef{
						ID: uri, Name: file.DisplayName, MIME: mime,
					},
					ThoughtSignature: part.ThoughtSignature,
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
			parts = append(parts, aistudio.Part{
				FunctionResult: &aistudio.FunctionResult{
					ID: part.FunctionResponse.ID, Name: part.FunctionResponse.Name, Content: response,
				},
				ThoughtSignature: part.ThoughtSignature,
			})
			hasResult = true
		case part.ExecutableCode != nil:
			parts = append(parts, aistudio.Part{
				ExecutableCode: &aistudio.ExecutableCode{
					Language: part.ExecutableCode.Language, Code: part.ExecutableCode.Code,
				},
				ThoughtSignature: part.ThoughtSignature,
			})
		case part.CodeExecutionResult != nil:
			parts = append(parts, aistudio.Part{
				CodeExecutionResult: &aistudio.CodeExecutionResult{
					Outcome: part.CodeExecutionResult.Outcome,
					Output:  part.CodeExecutionResult.Output,
					Error:   part.CodeExecutionResult.Error,
				},
				ThoughtSignature: part.ThoughtSignature,
			})
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
			mapped.Functions = append(mapped.Functions, aistudio.FunctionDeclaration{
				Name: declaration.Name, Description: declaration.Description, Parameters: parameters,
			})
		}
		search, err := mapGeminiGoogleSearch(group.GoogleSearch)
		if err != nil {
			return aistudio.Tools{}, err
		}
		if search != nil {
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
		}
		if geminiRawObjectPresent(group.ImageSearch) {
			mapped.Google = appendUnique(mapped.Google, "image_search")
		}
	}
	var toolConfig aistudio.ToolConfig
	toolConfig.AllowedFunctionNames = config.FunctionCallingConfig.AllowedFunctionNames
	switch strings.ToUpper(config.FunctionCallingConfig.Mode) {
	case "", "AUTO", "MODE_UNSPECIFIED":
		toolConfig.Mode = "auto"
	case "ANY":
		toolConfig.Mode = "required"
	case "VALIDATED":
		toolConfig.Mode = "validated"
	case "NONE":
		toolConfig.Mode = "none"
	default:
		return aistudio.Tools{}, fmt.Errorf("unsupported functionCallingConfig mode %q", config.FunctionCallingConfig.Mode)
	}
	mapped.ToolConfig = toolConfig
	return mapped, nil
}

func (s *server) handleGeminiCountTokens(w http.ResponseWriter, r *http.Request, request aistudio.GenerateRequest) {
	count, err := s.service.CountTokens(r.Context(), aistudio.TokenCountRequest{
		Model: request.Model, System: request.System, Contents: request.Contents, Tools: request.Tools,
	})
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiError(w, statusFromError(err), geminiErrorStatus(err), err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"totalTokens": count.InputTokens})
}

// handleGeminiGenerate 为每个候选启动一次生成，任一候选失败时整个请求按首个错误失败
func (s *server) handleGeminiGenerate(w http.ResponseWriter, r *http.Request, request aistudio.GenerateRequest, stream bool, count int) {
	request.Unary = !stream
	candidates := s.startChatChoices(r.Context(), request, count, stream)
	defer candidates.cancel()
	if err := candidates.err; err != nil {
		candidates.settle(r.Context())
		if shouldWriteRequestError(r, err) {
			writeGeminiError(w, statusFromError(err), geminiErrorStatus(err), err.Error())
		}
		return
	}
	if stream {
		s.streamGemini(w, r, request, candidates)
		return
	}
	results, err := candidates.run(func(_ int, events <-chan aistudio.Event) (generationResult, error) {
		return consumeEvents(candidates.ctx, events, nil)
	})
	if err != nil {
		candidates.settle(r.Context())
		if shouldWriteRequestError(r, err) {
			writeGeminiError(w, statusFromError(err), geminiErrorStatus(err), err.Error())
		}
		return
	}
	candidates.record(r.Context(), results)
	writeJSON(w, http.StatusOK, buildGeminiResponse(request, results))
}

// buildGeminiResponse 按候选序号输出各候选内容与终止原因，用量为全部候选之和
func buildGeminiResponse(request aistudio.GenerateRequest, results []generationResult) map[string]any {
	candidates := make([]any, 0, len(results))
	for index, result := range results {
		candidate := map[string]any{"index": index}
		if parts := geminiOutputParts(result); len(parts) > 0 {
			candidate["content"] = map[string]any{"role": "model", "parts": parts}
		}
		setGeminiFinish(candidate, result.finishReason, result.finishMessage)
		if result.grounding != nil {
			candidate["groundingMetadata"] = geminiGroundingMetadata(*result.grounding)
		} else if len(result.citations) > 0 {
			candidate["citationMetadata"] = geminiCitationMetadata(result.citations)
		}
		candidates = append(candidates, candidate)
	}
	response := map[string]any{
		"candidates":   candidates,
		"modelVersion": request.Model,
		"responseId":   request.ID,
	}
	if results[0].providerModel != "" {
		response["modelVersion"] = results[0].providerModel
	}
	if usage := sumUsage(results); usage != nil {
		response["usageMetadata"] = geminiUsage(usage)
	}
	return response
}

func geminiOutputParts(result generationResult) []map[string]any {
	parts := make([]map[string]any, 0)
	var pendingSignature string

	appendPart := func(part map[string]any, sig string) {
		if sig != "" {
			part["thoughtSignature"] = sig
		}
		if pendingSignature != "" {
			if part["thoughtSignature"] == nil {
				part["thoughtSignature"] = pendingSignature
			} else {
				parts = append(parts, geminiSignaturePart(pendingSignature))
			}
			pendingSignature = ""
		}
		parts = append(parts, part)
	}

	for _, event := range result.events {
		switch event.Kind {
		case aistudio.EventText:
			if len(parts) > 0 {
				last := parts[len(parts)-1]
				if lastText, ok := last["text"].(string); ok && last["thought"] != true && last["transcriptionMetadata"] == nil && event.Transcript == nil && last["thoughtSignature"] == nil && event.ThoughtSignature == "" {
					last["text"] = lastText + event.Text
					continue
				}
			}
			appendPart(geminiTextPart(event), event.ThoughtSignature)
		case aistudio.EventReasoning:
			if len(parts) > 0 {
				last := parts[len(parts)-1]
				if lastText, ok := last["text"].(string); ok && last["thought"] == true && last["thoughtSignature"] == nil && event.ThoughtSignature == "" {
					last["text"] = lastText + event.Text
					continue
				}
			}
			appendPart(map[string]any{"text": event.Text, "thought": true}, event.ThoughtSignature)
		case aistudio.EventToolCall:
			if event.ToolCall != nil {
				appendPart(geminiFunctionCallPart(*event.ToolCall), event.ThoughtSignature)
			}
		case aistudio.EventExecutableCode:
			if event.ExecutableCode != nil {
				appendPart(map[string]any{"executableCode": map[string]any{
					"language": event.ExecutableCode.Language, "code": event.ExecutableCode.Code,
				}}, event.ThoughtSignature)
			}
		case aistudio.EventCodeExecutionResult:
			if event.CodeExecutionResult != nil {
				appendPart(map[string]any{
					"codeExecutionResult": geminiCodeExecutionResult(*event.CodeExecutionResult),
				}, event.ThoughtSignature)
			}
		case aistudio.EventMedia:
			if event.Media != nil {
				var part map[string]any
				if len(event.Media.Data) > 0 {
					part = map[string]any{"inlineData": map[string]any{
						"mimeType": event.Media.MIME, "data": base64.StdEncoding.EncodeToString(event.Media.Data),
					}}
				} else if event.Media.URL != "" {
					part = map[string]any{"fileData": map[string]any{
						"mimeType": event.Media.MIME, "fileUri": event.Media.URL, "displayName": event.Media.Name,
					}}
				}
				if part != nil {
					appendPart(part, event.ThoughtSignature)
				}
			}
		case aistudio.EventThoughtSignature:
			if event.ThoughtSignature == "" {
				continue
			}
			if pendingSignature != "" {
				parts = append(parts, geminiSignaturePart(pendingSignature))
				pendingSignature = ""
			}
			if len(parts) > 0 && parts[len(parts)-1]["thoughtSignature"] == nil {
				parts[len(parts)-1]["thoughtSignature"] = event.ThoughtSignature
			} else if len(parts) > 0 {
				parts = append(parts, geminiSignaturePart(event.ThoughtSignature))
			} else {
				pendingSignature = event.ThoughtSignature
			}
		}
	}
	if pendingSignature != "" {
		parts = append(parts, geminiSignaturePart(pendingSignature))
	}
	return parts
}

// geminiSignaturePart 用空思考 Part 承载没有正文的独立签名
func geminiSignaturePart(signature string) map[string]any {
	return map[string]any{"text": "", "thought": true, "thoughtSignature": signature}
}

func geminiTextPart(event aistudio.Event) map[string]any {
	part := map[string]any{"text": event.Text}
	if event.Transcript == nil {
		return part
	}
	metadata := map[string]any{}
	if event.Transcript.Speaker != "" {
		metadata["speaker"] = event.Transcript.Speaker
	}
	if len(event.Transcript.Timestamps) > 0 {
		timestamps := make([]map[string]any, 0, len(event.Transcript.Timestamps))
		for _, timestamp := range event.Transcript.Timestamps {
			timestamps = append(timestamps, map[string]any{
				"start": geminiTranscriptDuration(timestamp.Start),
				"end":   geminiTranscriptDuration(timestamp.End),
			})
		}
		metadata["timestamps"] = timestamps
	}
	part["transcriptionMetadata"] = metadata
	return part
}

func geminiTranscriptDuration(duration aistudio.TranscriptDuration) map[string]int64 {
	return map[string]int64{"seconds": duration.Seconds, "nanos": duration.Nanos}
}

func geminiFunctionCallPart(call aistudio.FunctionCall) map[string]any {
	part := map[string]any{"functionCall": map[string]any{
		"id": call.ID, "name": call.Name, "args": call.Arguments,
	}}
	if call.ThoughtSignature != "" {
		part["thoughtSignature"] = call.ThoughtSignature
	}
	return part
}

func geminiSignedPart(part map[string]any, signature string) map[string]any {
	if signature != "" {
		part["thoughtSignature"] = signature
	}
	return part
}

func geminiCodeExecutionResult(result aistudio.CodeExecutionResult) map[string]any {
	output := map[string]any{"outcome": result.Outcome}
	if result.Outcome == "OUTCOME_OK" {
		output["output"] = result.Output
	} else {
		output["error"] = result.Error
	}
	return output
}

func geminiCitationMetadata(citations []aistudio.Citation) map[string]any {
	sources := make([]map[string]any, 0, len(citations))
	for _, citation := range citations {
		sources = append(sources, map[string]any{
			"uri": citation.URL, "title": citation.Title, "startIndex": citation.Start, "endIndex": citation.End,
		})
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
			switch chunk.Source {
			case "web":
				chunks = append(chunks, map[string]any{"web": value})
			case "retrieved_context":
				value["text"] = chunk.Text
				chunks = append(chunks, map[string]any{"retrievedContext": value})
			case "maps":
				value["text"] = chunk.Text
				value["placeId"] = chunk.PlaceID
				chunks = append(chunks, map[string]any{"maps": value})
			}
		}
		output["groundingChunks"] = chunks
	}
	if len(metadata.Supports) > 0 {
		supports := make([]map[string]any, 0, len(metadata.Supports))
		for _, support := range metadata.Supports {
			value := map[string]any{
				"segment": map[string]any{
					"partIndex": support.Segment.PartIndex, "startIndex": support.Segment.StartIndex,
					"endIndex": support.Segment.EndIndex, "text": support.Segment.Text,
				},
				"groundingChunkIndices": support.ChunkIndices,
			}
			if len(support.ConfidenceScores) > 0 {
				value["confidenceScores"] = support.ConfidenceScores
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
		return "OTHER"
	default:
		return "OTHER"
	}
}

func setGeminiFinish(candidate map[string]any, reason, message string) {
	candidate["finishReason"] = geminiFinishReason(reason)
	normalized := strings.ToLower(strings.TrimSpace(reason))
	if message != "" {
		candidate["finishMessage"] = message
	} else if normalized == "missing_thought_signature" {
		candidate["finishMessage"] = "Missing thought signature"
	} else if strings.HasPrefix(normalized, "provider_") {
		candidate["finishMessage"] = "AI Studio finish reason " + strings.TrimPrefix(normalized, "provider_")
	}
}

func geminiUsage(usage *aistudio.Usage) map[string]any {
	return map[string]any{
		"promptTokenCount":        usage.InputTokens,
		"candidatesTokenCount":    usage.OutputTokens,
		"thoughtsTokenCount":      usage.ReasoningTokens,
		"toolUsePromptTokenCount": usage.ToolTokens,
		"totalTokenCount":         usage.TotalTokens,
	}
}

// streamGemini 按到达顺序输出各候选的增量块，最后一块携带全部候选的终止原因与用量之和
func (s *server) streamGemini(w http.ResponseWriter, r *http.Request, request aistudio.GenerateRequest, candidates *chatChoices) {
	if err := streamHeaders(w); err != nil {
		return
	}
	var writing sync.Mutex
	write := func(value map[string]any) error {
		writing.Lock()
		defer writing.Unlock()
		return writeSSE(w, "", value)
	}
	heartbeat := func() error {
		writing.Lock()
		defer writing.Unlock()
		return writeSSEHeartbeat(w)
	}
	results, err := candidates.run(func(index int, events <-chan aistudio.Event) (generationResult, error) {
		return consumeStreamEvents(candidates.ctx, events, func(event aistudio.Event) error {
			candidate := geminiStreamCandidate(event, index)
			if candidate == nil {
				return nil
			}
			return write(map[string]any{"responseId": request.ID, "modelVersion": request.Model, "candidates": []any{candidate}})
		}, heartbeat)
	})
	if err != nil {
		candidates.settle(r.Context())
		if shouldWriteRequestError(r, err) {
			_ = write(map[string]any{"error": map[string]any{
				"code": statusFromError(err), "message": err.Error(), "status": geminiErrorStatus(err),
			}})
		}
		return
	}
	candidates.record(r.Context(), results)
	model := request.Model
	if results[0].providerModel != "" {
		model = results[0].providerModel
	}
	finished := make([]any, 0, len(results))
	for index, result := range results {
		candidate := map[string]any{"index": index}
		setGeminiFinish(candidate, result.finishReason, result.finishMessage)
		finished = append(finished, candidate)
	}
	final := map[string]any{
		"responseId": request.ID, "modelVersion": model,
		"candidates": finished,
	}
	if usage := sumUsage(results); usage != nil {
		final["usageMetadata"] = geminiUsage(usage)
	}
	_ = write(final)
}

// geminiStreamCandidate 把规范事件投影为指定序号候选的流式内容，没有可输出内容时返回 nil
func geminiStreamCandidate(event aistudio.Event, index int) map[string]any {
	var part map[string]any
	switch event.Kind {
	case aistudio.EventText:
		part = geminiSignedPart(geminiTextPart(event), event.ThoughtSignature)
	case aistudio.EventReasoning:
		part = geminiSignedPart(map[string]any{"text": event.Text, "thought": true}, event.ThoughtSignature)
	case aistudio.EventToolCall:
		if event.ToolCall == nil {
			return nil
		}
		part = geminiSignedPart(geminiFunctionCallPart(*event.ToolCall), event.ThoughtSignature)
	case aistudio.EventExecutableCode:
		if event.ExecutableCode == nil {
			return nil
		}
		part = geminiSignedPart(map[string]any{"executableCode": map[string]any{
			"language": event.ExecutableCode.Language, "code": event.ExecutableCode.Code,
		}}, event.ThoughtSignature)
	case aistudio.EventCodeExecutionResult:
		if event.CodeExecutionResult == nil {
			return nil
		}
		part = geminiSignedPart(map[string]any{
			"codeExecutionResult": geminiCodeExecutionResult(*event.CodeExecutionResult),
		}, event.ThoughtSignature)
	case aistudio.EventGrounding:
		if event.Grounding == nil {
			return nil
		}
		return map[string]any{"index": index, "groundingMetadata": geminiGroundingMetadata(*event.Grounding)}
	case aistudio.EventCitation:
		if event.Citation == nil {
			return nil
		}
		return map[string]any{"index": index, "citationMetadata": geminiCitationMetadata([]aistudio.Citation{*event.Citation})}
	case aistudio.EventMedia:
		if event.Media == nil {
			return nil
		}
		if len(event.Media.Data) > 0 {
			part = map[string]any{"inlineData": map[string]any{
				"mimeType": event.Media.MIME, "data": base64.StdEncoding.EncodeToString(event.Media.Data),
			}}
		} else if event.Media.URL != "" {
			part = map[string]any{"fileData": map[string]any{
				"mimeType": event.Media.MIME, "fileUri": event.Media.URL, "displayName": event.Media.Name,
			}}
		} else {
			return nil
		}
		part = geminiSignedPart(part, event.ThoughtSignature)
	case aistudio.EventThoughtSignature:
		if event.ThoughtSignature == "" {
			return nil
		}
		part = geminiSignaturePart(event.ThoughtSignature)
	default:
		return nil
	}
	return map[string]any{"index": index, "content": map[string]any{"role": "model", "parts": []any{part}}}
}
