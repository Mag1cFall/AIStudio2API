package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type geminiRequest struct {
	Contents          []geminiContent        `json:"contents"`
	SystemInstruction *geminiContent         `json:"systemInstruction"`
	GenerationConfig  geminiGenerationConfig `json:"generationConfig"`
	Tools             []geminiToolGroup      `json:"tools"`
	ToolConfig        geminiToolConfig       `json:"toolConfig"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             *string `json:"text"`
	Thought          bool    `json:"thought"`
	ThoughtSignature string  `json:"thoughtSignature"`
	InlineData       *struct {
		MIMEType string `json:"mimeType"`
		Data     string `json:"data"`
	} `json:"inlineData"`
	FileData *struct {
		MIMEType    string `json:"mimeType"`
		FileURI     string `json:"fileUri"`
		DisplayName string `json:"displayName"`
	} `json:"fileData"`
	FunctionCall *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
	FunctionResponse *struct {
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Response json.RawMessage `json:"response"`
	} `json:"functionResponse"`
}

type geminiGenerationConfig struct {
	Temperature        *float64            `json:"temperature"`
	TopP               *float64            `json:"topP"`
	TopK               *int                `json:"topK"`
	MaxOutputTokens    *int64              `json:"maxOutputTokens"`
	StopSequences      []string            `json:"stopSequences"`
	ResponseMIMEType   string              `json:"responseMimeType"`
	ResponseSchema     json.RawMessage     `json:"responseSchema"`
	ResponseJSONSchema json.RawMessage     `json:"responseJsonSchema"`
	ResponseModalities []string            `json:"responseModalities"`
	ImageConfig        *geminiImageConfig  `json:"imageConfig"`
	SpeechConfig       *geminiSpeechConfig `json:"speechConfig"`
	Seed               *int64              `json:"seed"`
	ThinkingConfig     *struct {
		ThinkingBudget *int64 `json:"thinkingBudget"`
		ThinkingLevel  string `json:"thinkingLevel"`
	} `json:"thinkingConfig"`
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
		writeGeminiError(w, statusFromError(err), geminiErrorStatus(err), err.Error())
		return
	}
	models = publicModels(models)
	data := make([]map[string]any, 0, len(models))
	for _, model := range models {
		data = append(data, geminiModelObject(model))
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": data})
}

func (s *server) handleGeminiModel(w http.ResponseWriter, r *http.Request) {
	modelID := strings.TrimPrefix(r.PathValue("model"), "models/")
	models, err := s.service.Models(r.Context())
	if err != nil {
		writeGeminiError(w, statusFromError(err), geminiErrorStatus(err), err.Error())
		return
	}
	for _, model := range publicModels(models) {
		if model.ID == modelID {
			writeJSON(w, http.StatusOK, geminiModelObject(model))
			return
		}
	}
	writeGeminiError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("model %q is unavailable", modelID))
}

func geminiModelObject(model aistudio.Model) map[string]any {
	return map[string]any{
		"name":                       "models/" + model.ID,
		"displayName":                model.Name,
		"description":                model.Description,
		"supportedGenerationMethods": model.Methods,
		"inputTokenLimit":            model.InputTokenLimit,
		"outputTokenLimit":           model.OutputTokenLimit,
	}
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
	if method == "predictLongRunning" {
		s.handleGeminiVideoCreate(w, r, model)
		return
	}
	var request geminiRequest
	if err := decodeJSON(r, &request); err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if len(request.Contents) == 0 {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "contents is required")
		return
	}
	generateRequest, err := request.toGenerateRequest(newID("resp"), model)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	switch method {
	case "countTokens":
		s.handleGeminiCountTokens(w, r, generateRequest)
	case "generateContent":
		s.handleGeminiGenerate(w, r, generateRequest, false)
	case "streamGenerateContent":
		s.handleGeminiGenerate(w, r, generateRequest, true)
	default:
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "unknown method: "+method)
	}
}

func (request geminiRequest) toGenerateRequest(id string, model string) (aistudio.GenerateRequest, error) {
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
	if len(request.GenerationConfig.ResponseJSONSchema) > 0 {
		config.ResponseSchema = request.GenerationConfig.ResponseJSONSchema
	}
	if request.GenerationConfig.ThinkingConfig != nil {
		config.ThinkingBudget = request.GenerationConfig.ThinkingConfig.ThinkingBudget
		config.ReasoningEffort = request.GenerationConfig.ThinkingConfig.ThinkingLevel
	}
	return aistudio.GenerateRequest{
		ID: id, Model: model, System: system, Contents: contents, Config: config, Tools: tools,
	}, nil
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
		default:
			return nil, fmt.Errorf("unsupported response modality %q", raw)
		}
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
		if part.InlineData != nil {
			variants++
		}
		if part.FileData != nil {
			variants++
		}
		if part.FunctionCall != nil {
			variants++
		}
		if part.FunctionResponse != nil {
			variants++
		}
		if variants != 1 {
			return nil, false, fmt.Errorf("parts[%d] must contain exactly one data field", index)
		}
		switch {
		case part.InlineData != nil:
			if part.InlineData.MIMEType == "" || part.InlineData.Data == "" {
				return nil, false, fmt.Errorf("inlineData requires mimeType and data")
			}
			data, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
			if err != nil {
				return nil, false, fmt.Errorf("inlineData.data: %w", err)
			}
			parts = append(parts, aistudio.Part{
				InlineData:       &aistudio.Blob{MIME: part.InlineData.MIMEType, Data: data},
				ThoughtSignature: part.ThoughtSignature,
			})
		case part.FileData != nil:
			if part.FileData.FileURI == "" || part.FileData.MIMEType == "" {
				return nil, false, fmt.Errorf("fileData requires fileUri and mimeType")
			}
			parts = append(parts, aistudio.Part{
				File: &aistudio.FileRef{
					ID: part.FileData.FileURI, Name: part.FileData.DisplayName, MIME: part.FileData.MIMEType,
				},
				ThoughtSignature: part.ThoughtSignature,
			})
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
		default:
			if part.Thought {
				if part.ThoughtSignature != "" {
					parts = append(parts, aistudio.Part{ThoughtSignature: part.ThoughtSignature})
				}
				continue
			}
			parts = append(parts, aistudio.Part{Text: *part.Text, ThoughtSignature: part.ThoughtSignature})
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
		if len(group.GoogleSearch) > 0 || len(group.GoogleSearchRetrieval) > 0 {
			mapped.Google = appendUnique(mapped.Google, "google_search")
		}
		if len(group.URLContext) > 0 {
			mapped.Google = appendUnique(mapped.Google, "url_context")
		}
		if len(group.CodeExecution) > 0 {
			mapped.Google = appendUnique(mapped.Google, "code_execution")
		}
		if len(group.GoogleMaps) > 0 {
			mapped.Google = appendUnique(mapped.Google, "google_maps")
		}
		if len(group.ImageSearch) > 0 {
			mapped.Google = appendUnique(mapped.Google, "image_search")
		}
	}
	var toolConfig aistudio.ToolConfig
	if len(config.FunctionCallingConfig.AllowedFunctionNames) > 0 {
		return aistudio.Tools{}, fmt.Errorf("allowedFunctionNames is not supported by AI Studio Web")
	}
	switch strings.ToUpper(config.FunctionCallingConfig.Mode) {
	case "", "AUTO":
		toolConfig.Mode = "auto"
	case "ANY":
		return aistudio.Tools{}, fmt.Errorf("functionCallingConfig mode ANY is not supported by AI Studio Web")
	case "NONE":
		toolConfig.Mode = "none"
	default:
		return aistudio.Tools{}, fmt.Errorf("unsupported functionCallingConfig mode %q", config.FunctionCallingConfig.Mode)
	}
	if len(mapped.Functions) == 0 && len(mapped.Google) == 0 {
		return mapped, nil
	}
	mapped.ToolConfig = toolConfig
	return mapped, nil
}

func (s *server) handleGeminiCountTokens(w http.ResponseWriter, r *http.Request, request aistudio.GenerateRequest) {
	count, err := s.service.CountTokens(r.Context(), aistudio.TokenCountRequest{
		Model: request.Model, System: request.System, Contents: request.Contents,
	})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			writeGeminiError(w, statusFromError(err), geminiErrorStatus(err), err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"totalTokens": count.InputTokens})
}

func (s *server) handleGeminiGenerate(w http.ResponseWriter, r *http.Request, request aistudio.GenerateRequest, stream bool) {
	events, err := s.service.Generate(r.Context(), request)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			writeGeminiError(w, statusFromError(err), geminiErrorStatus(err), err.Error())
		}
		return
	}
	if stream {
		s.streamGemini(w, r, request, events)
		return
	}
	result, err := consumeEvents(r.Context(), events, nil)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			writeGeminiError(w, statusFromError(err), geminiErrorStatus(err), err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, buildGeminiResponse(request, result))
}

func buildGeminiResponse(request aistudio.GenerateRequest, result generationResult) map[string]any {
	candidate := map[string]any{
		"content":      map[string]any{"role": "model", "parts": geminiOutputParts(result)},
		"finishReason": geminiFinishReason(result.finishReason),
		"index":        0,
	}
	if result.grounding != nil {
		candidate["groundingMetadata"] = geminiGroundingMetadata(*result.grounding)
	} else if len(result.citations) > 0 {
		candidate["citationMetadata"] = geminiCitationMetadata(result.citations)
	}
	response := map[string]any{
		"candidates":   []any{candidate},
		"modelVersion": request.Model,
		"responseId":   request.ID,
	}
	if result.providerModel != "" {
		response["modelVersion"] = result.providerModel
	}
	if result.usage != nil {
		response["usageMetadata"] = geminiUsage(result.usage)
	}
	return response
}

func geminiOutputParts(result generationResult) []map[string]any {
	parts := make([]map[string]any, 0)
	for _, event := range result.events {
		switch event.Kind {
		case aistudio.EventText:
			parts = append(parts, geminiSignedPart(map[string]any{"text": event.Text}, event.ThoughtSignature))
		case aistudio.EventReasoning:
			parts = append(parts, geminiSignedPart(map[string]any{"text": event.Text, "thought": true}, event.ThoughtSignature))
		case aistudio.EventToolCall:
			if event.ToolCall != nil {
				parts = append(parts, geminiSignedPart(geminiFunctionCallPart(*event.ToolCall), event.ThoughtSignature))
			}
		case aistudio.EventExecutableCode:
			if event.ExecutableCode != nil {
				parts = append(parts, geminiSignedPart(map[string]any{"executableCode": map[string]any{
					"language": event.ExecutableCode.Language, "code": event.ExecutableCode.Code,
				}}, event.ThoughtSignature))
			}
		case aistudio.EventCodeExecutionResult:
			if event.CodeExecutionResult != nil {
				parts = append(parts, geminiSignedPart(map[string]any{"codeExecutionResult": map[string]any{
					"outcome": event.CodeExecutionResult.Outcome, "output": event.CodeExecutionResult.Output,
				}}, event.ThoughtSignature))
			}
		case aistudio.EventMedia:
			if event.Media != nil {
				if len(event.Media.Data) > 0 {
					parts = append(parts, geminiSignedPart(map[string]any{"inlineData": map[string]any{
						"mimeType": event.Media.MIME, "data": base64.StdEncoding.EncodeToString(event.Media.Data),
					}}, event.ThoughtSignature))
				} else if event.Media.URL != "" {
					parts = append(parts, geminiSignedPart(map[string]any{"fileData": map[string]any{
						"mimeType": event.Media.MIME, "fileUri": event.Media.URL, "displayName": event.Media.Name,
					}}, event.ThoughtSignature))
				}
			}
		case aistudio.EventThoughtSignature:
			if event.ThoughtSignature != "" {
				parts = append(parts, map[string]any{"thoughtSignature": event.ThoughtSignature})
			}
		}
	}
	return parts
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
	default:
		return "STOP"
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

func (s *server) streamGemini(w http.ResponseWriter, r *http.Request, request aistudio.GenerateRequest, events <-chan aistudio.Event) {
	streamHeaders(w)
	result, err := consumeEvents(r.Context(), events, func(event aistudio.Event) error {
		response := map[string]any{"responseId": request.ID, "modelVersion": request.Model}
		switch event.Kind {
		case aistudio.EventText:
			response["candidates"] = []any{geminiStreamCandidate(geminiSignedPart(map[string]any{"text": event.Text}, event.ThoughtSignature))}
		case aistudio.EventReasoning:
			response["candidates"] = []any{geminiStreamCandidate(geminiSignedPart(map[string]any{"text": event.Text, "thought": true}, event.ThoughtSignature))}
		case aistudio.EventToolCall:
			if event.ToolCall == nil {
				return nil
			}
			response["candidates"] = []any{geminiStreamCandidate(geminiSignedPart(geminiFunctionCallPart(*event.ToolCall), event.ThoughtSignature))}
		case aistudio.EventExecutableCode:
			if event.ExecutableCode == nil {
				return nil
			}
			part := map[string]any{"executableCode": map[string]any{
				"language": event.ExecutableCode.Language, "code": event.ExecutableCode.Code,
			}}
			response["candidates"] = []any{geminiStreamCandidate(geminiSignedPart(part, event.ThoughtSignature))}
		case aistudio.EventCodeExecutionResult:
			if event.CodeExecutionResult == nil {
				return nil
			}
			part := map[string]any{"codeExecutionResult": map[string]any{
				"outcome": event.CodeExecutionResult.Outcome, "output": event.CodeExecutionResult.Output,
			}}
			response["candidates"] = []any{geminiStreamCandidate(geminiSignedPart(part, event.ThoughtSignature))}
		case aistudio.EventGrounding:
			if event.Grounding == nil {
				return nil
			}
			response["candidates"] = []any{map[string]any{
				"index": 0, "groundingMetadata": geminiGroundingMetadata(*event.Grounding),
			}}
		case aistudio.EventCitation:
			if event.Citation == nil {
				return nil
			}
			response["candidates"] = []any{map[string]any{
				"index": 0, "citationMetadata": geminiCitationMetadata([]aistudio.Citation{*event.Citation}),
			}}
		case aistudio.EventMedia:
			if event.Media == nil {
				return nil
			}
			var part map[string]any
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
			response["candidates"] = []any{geminiStreamCandidate(geminiSignedPart(part, event.ThoughtSignature))}
		case aistudio.EventThoughtSignature:
			if event.ThoughtSignature == "" {
				return nil
			}
			response["candidates"] = []any{geminiStreamCandidate(map[string]any{"thoughtSignature": event.ThoughtSignature})}
		default:
			return nil
		}
		return writeSSE(w, "", response)
	})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			_ = writeSSE(w, "", map[string]any{"error": map[string]any{
				"code": statusFromError(err), "message": err.Error(), "status": geminiErrorStatus(err),
			}})
		}
		return
	}
	model := request.Model
	if result.providerModel != "" {
		model = result.providerModel
	}
	final := map[string]any{
		"responseId": request.ID, "modelVersion": model,
		"candidates": []any{map[string]any{"index": 0, "finishReason": geminiFinishReason(result.finishReason)}},
	}
	if result.usage != nil {
		final["usageMetadata"] = geminiUsage(result.usage)
	}
	_ = writeSSE(w, "", final)
}

func geminiStreamCandidate(part map[string]any) map[string]any {
	return map[string]any{"index": 0, "content": map[string]any{"role": "model", "parts": []any{part}}}
}
