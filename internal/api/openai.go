package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type chatRequest struct {
	Model               string            `json:"model"`
	Messages            []chatMessage     `json:"messages"`
	Stream              bool              `json:"stream"`
	StreamOptions       chatStreamOptions `json:"stream_options"`
	Tools               []openAITool      `json:"tools"`
	ToolChoice          json.RawMessage   `json:"tool_choice"`
	Temperature         *float64          `json:"temperature"`
	TopP                *float64          `json:"top_p"`
	MaxTokens           *int64            `json:"max_tokens"`
	MaxCompletionTokens *int64            `json:"max_completion_tokens"`
	N                   *int64            `json:"n"`
	ParallelToolCalls   *bool             `json:"parallel_tool_calls"`
	Stop                json.RawMessage   `json:"stop"`
	ResponseFormat      json.RawMessage   `json:"response_format"`
	ReasoningEffort     string            `json:"reasoning_effort"`
	Reasoning           json.RawMessage   `json:"reasoning"`
	Seed                *int64            `json:"seed"`
	WebSearchOptions    json.RawMessage   `json:"web_search_options"`
}

type chatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role       string           `json:"role"`
	Content    json.RawMessage  `json:"content"`
	Name       string           `json:"name"`
	ToolCallID string           `json:"tool_call_id"`
	ToolCalls  []openAIToolCall `json:"tool_calls"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
	Custom struct {
		Name  string `json:"name"`
		Input string `json:"input"`
	} `json:"custom"`
	ExtraContent struct {
		Google struct {
			ThoughtSignature string `json:"thought_signature"`
		} `json:"google"`
	} `json:"extra_content"`
}

type openAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
		Strict      *bool           `json:"strict"`
	} `json:"function"`
	Custom struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Format      json.RawMessage `json:"format"`
	} `json:"custom"`
}

var assistantImagePattern = regexp.MustCompile(`!\[[^\]]*\]\((data:image/[A-Za-z0-9.+-]+;base64,[A-Za-z0-9+/_=\r\n-]+)\)`)

// openAIModelObject 投影列表与单模型共用的公开字段
func openAIModelObject(model aistudio.Model) map[string]any {
	item := map[string]any{
		"id": model.ID, "object": "model", "created": 0, "owned_by": "google",
		"name": model.Name, "description": model.Description,
		"supported_generation_methods": model.Methods,
		"input_token_limit":            model.InputTokenLimit, "output_token_limit": model.OutputTokenLimit,
	}
	if len(model.Capabilities) > 0 {
		item["capabilities"] = model.Capabilities
	}
	if len(model.CapabilityOptions) > 0 {
		item["capability_options"] = model.CapabilityOptions
	}
	if len(model.AccessModes) > 0 {
		item["access_modes"] = model.AccessModes
	}
	if len(model.Channels) > 0 {
		item["channels"] = model.Channels
	}
	if model.Paid {
		item["paid"] = true
	}
	return item
}

// lookupPublicModel 按正式 ID 优先于别名解析同一实时目录
func lookupPublicModel(models []aistudio.Model, id string) (aistudio.Model, bool) {
	id = strings.TrimPrefix(strings.TrimSpace(id), "models/")
	if id == "" {
		return aistudio.Model{}, false
	}
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	for _, model := range models {
		for _, alias := range model.CapabilityOptions["aliases"] {
			if strings.TrimPrefix(strings.TrimSpace(alias), "models/") == id {
				return model, true
			}
		}
	}
	return aistudio.Model{}, false
}

func (s *server) handleOpenAIModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.service.Models(r.Context())
	if err != nil {
		if shouldWriteRequestError(r, err) {
			if r.Header.Get("Anthropic-Version") != "" {
				writeAnthropicError(w, statusFromError(err), anthropicErrorType(err), err.Error())
			} else {
				writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
			}
		}
		return
	}
	if r.Header.Get("Anthropic-Version") != "" {
		writeAnthropicModels(w, r, models)
		return
	}
	data := make([]map[string]any, 0, len(models))
	for _, model := range models {
		data = append(data, openAIModelObject(model))
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

// handleOpenAIModel 返回实时目录中的单模型或协议化错误
func (s *server) handleOpenAIModel(w http.ResponseWriter, r *http.Request) {
	models, err := s.service.Models(r.Context())
	if err != nil {
		if shouldWriteRequestError(r, err) {
			if r.Header.Get("Anthropic-Version") != "" {
				writeAnthropicError(w, statusFromError(err), anthropicErrorType(err), err.Error())
			} else {
				writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
			}
		}
		return
	}
	model, ok := lookupPublicModel(models, r.PathValue("model"))
	if !ok {
		message := fmt.Sprintf("model %q is unavailable", r.PathValue("model"))
		if r.Header.Get("Anthropic-Version") != "" {
			writeAnthropicError(w, http.StatusNotFound, "not_found_error", message)
		} else {
			writeOpenAIError(w, http.StatusNotFound, "model_not_found", message)
		}
		return
	}
	if r.Header.Get("Anthropic-Version") != "" {
		writeJSON(w, http.StatusOK, anthropicModelObject(model))
	} else {
		writeJSON(w, http.StatusOK, openAIModelObject(model))
	}
}

func (s *server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	var request chatRequest
	if err := decodeJSON(r, &request); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.Model == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "model is required")
		return
	}
	requestID := newID("chatcmpl")
	generateRequest, err := request.toGenerateRequest(requestID)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := inlineRemoteMedia(r.Context(), generateRequest.Contents); err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		}
		return
	}
	generateRequest.Unary = !request.Stream
	s.thoughtSignatures.Restore(generateRequest.Contents)
	count := 1
	if request.N != nil {
		count = int(*request.N)
	}
	choices := s.startChatChoices(r.Context(), generateRequest, count, request.Stream)
	defer choices.cancel()
	if err := choices.err; err != nil {
		choices.settle(r.Context())
		if shouldWriteRequestError(r, err) {
			writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		}
		return
	}
	created := time.Now().Unix()
	if request.Stream {
		s.streamChatCompletion(w, r, request, requestID, created, choices)
		return
	}
	results, err := choices.run(func(_ int, events <-chan aistudio.Event) (generationResult, error) {
		return consumeEvents(choices.ctx, events, nil)
	})
	if err != nil {
		choices.settle(r.Context())
		if shouldWriteRequestError(r, err) {
			writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		}
		return
	}
	for index := range results {
		s.thoughtSignatures.Remember(results[index].toolCalls)
	}
	choices.record(r.Context(), results)
	writeJSON(w, http.StatusOK, buildChatCompletion(requestID, created, request.Model, results, request.Tools))
}

func (request chatRequest) toGenerateRequest(id string) (aistudio.GenerateRequest, error) {
	var system []string
	contents := make([]aistudio.Content, 0, len(request.Messages))
	for _, message := range request.Messages {
		if message.Role == "system" || message.Role == "developer" {
			text, err := openAITextContent(message.Content)
			if err != nil {
				return aistudio.GenerateRequest{}, fmt.Errorf("%s message: %w", message.Role, err)
			}
			if text != "" {
				system = append(system, text)
			}
			continue
		}
		content, err := chatMessageContent(message)
		if err != nil {
			return aistudio.GenerateRequest{}, err
		}
		if len(content.Parts) == 0 {
			continue
		}
		contents = append(contents, content)
	}
	tools, err := mapOpenAITools(request.Tools, request.ToolChoice)
	if err != nil {
		return aistudio.GenerateRequest{}, err
	}
	if rawJSONConfigured(request.WebSearchOptions) {
		var options struct {
			SearchContextSize string          `json:"search_context_size"`
			UserLocation      json.RawMessage `json:"user_location"`
		}
		if err := json.Unmarshal(request.WebSearchOptions, &options); err != nil {
			return aistudio.GenerateRequest{}, fmt.Errorf("web_search_options must be an object")
		}
		tools.GoogleSearch, err = mapSearchOptions(options.SearchContextSize, options.UserLocation, nil)
		if err != nil {
			return aistudio.GenerateRequest{}, err
		}
		tools.Google = appendUnique(tools.Google, "google_search")
	}
	tools.ToolConfig.ParallelCalls = request.ParallelToolCalls
	config, err := request.generationConfig()
	if err != nil {
		return aistudio.GenerateRequest{}, err
	}
	return aistudio.GenerateRequest{
		ID:       id,
		Model:    request.Model,
		System:   strings.Join(system, "\n"),
		Contents: contents,
		Config:   config,
		Tools:    tools,
	}, nil
}

func chatMessageContent(message chatMessage) (aistudio.Content, error) {
	role, err := openAIRole(message.Role)
	if err != nil {
		return aistudio.Content{}, err
	}
	if role == aistudio.RoleTool {
		content, err := normalizeFunctionResultContent(message.Content)
		if err != nil {
			return aistudio.Content{}, fmt.Errorf("tool message content: %w", err)
		}
		return aistudio.Content{Role: role, Parts: []aistudio.Part{{FunctionResult: &aistudio.FunctionResult{
			ID:      message.ToolCallID,
			Name:    message.Name,
			Content: content,
		}}}}, nil
	}
	parts, err := openAIContentParts(message.Content)
	if err != nil {
		return aistudio.Content{}, fmt.Errorf("%s message content: %w", message.Role, err)
	}
	if role == aistudio.RoleAssistant {
		parts, err = openAIAssistantContentParts(message.Content, parts)
		if err != nil {
			return aistudio.Content{}, fmt.Errorf("assistant message content: %w", err)
		}
	}
	for _, call := range message.ToolCalls {
		functionCall := aistudio.FunctionCall{ID: call.ID, ThoughtSignature: call.ExtraContent.Google.ThoughtSignature}
		switch call.Type {
		case "", "function":
			functionCall.Name = call.Function.Name
			functionCall.Arguments = functionCallArguments(call.Function.Arguments)
		case "custom":
			functionCall.Name = call.Custom.Name
			functionCall.Arguments = customToolArguments(call.Custom.Input)
		default:
			return aistudio.Content{}, fmt.Errorf("unsupported tool call type %q", call.Type)
		}
		parts = append(parts, aistudio.Part{FunctionCall: &functionCall})
	}
	return aistudio.Content{Role: role, Parts: parts}, nil
}

func openAIAssistantContentParts(raw json.RawMessage, fallback []aistudio.Part) ([]aistudio.Part, error) {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return fallback, nil
	}
	matches := assistantImagePattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return fallback, nil
	}
	parts := make([]aistudio.Part, 0, len(matches)*2+1)
	position := 0
	for _, match := range matches {
		if match[0] > position {
			parts = append(parts, aistudio.Part{Text: text[position:match[0]]})
		}
		part, err := fileOrInlinePart(text[match[2]:match[3]], "")
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
		position = match[1]
	}
	if position < len(text) {
		parts = append(parts, aistudio.Part{Text: text[position:]})
	}
	return parts, nil
}

func openAIRole(role string) (aistudio.Role, error) {
	switch role {
	case "user":
		return aistudio.RoleUser, nil
	case "assistant":
		return aistudio.RoleAssistant, nil
	case "tool", "function":
		return aistudio.RoleTool, nil
	default:
		return "", fmt.Errorf("unsupported message role %q", role)
	}
}

func openAITextContent(raw json.RawMessage) (string, error) {
	parts, err := openAIContentParts(raw)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for _, part := range parts {
		if part.Text == "" && (part.InlineData != nil || part.File != nil) {
			return "", fmt.Errorf("system content must be text")
		}
		text.WriteString(part.Text)
	}
	return text.String(), nil
}

// openAIContentParts 转换文本与媒体并省略空文本占位
func openAIContentParts(raw json.RawMessage) ([]aistudio.Part, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if text == "" {
			return nil, nil
		}
		return []aistudio.Part{{Text: text}}, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("content must be a string or array")
	}
	parts := make([]aistudio.Part, 0, len(blocks))
	for _, block := range blocks {
		part, err := openAIContentPart(block)
		if err != nil {
			return nil, err
		}
		if part == (aistudio.Part{}) {
			continue
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func openAIContentPart(raw json.RawMessage) (aistudio.Part, error) {
	var block struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Refusal  string          `json:"refusal"`
		ImageURL json.RawMessage `json:"image_url"`
		VideoURL json.RawMessage `json:"video_url"`
		FileID   string          `json:"file_id"`
		Filename string          `json:"filename"`
		FileData string          `json:"file_data"`
		FileURL  string          `json:"file_url"`
		File     *struct {
			FileID   string `json:"file_id"`
			Filename string `json:"filename"`
			FileData string `json:"file_data"`
		} `json:"file"`
		InputAudio *struct {
			Data   string `json:"data"`
			Format string `json:"format"`
		} `json:"input_audio"`
	}
	if err := json.Unmarshal(raw, &block); err != nil {
		return aistudio.Part{}, err
	}
	switch block.Type {
	case "text", "input_text", "output_text":
		return aistudio.Part{Text: block.Text}, nil
	case "refusal":
		return aistudio.Part{Text: block.Refusal}, nil
	case "image_url", "input_image":
		if !rawJSONConfigured(block.ImageURL) && block.FileID != "" {
			return aistudio.Part{File: &aistudio.FileRef{ID: block.FileID}}, nil
		}
		url, err := imageURLString(block.ImageURL)
		if err != nil {
			return aistudio.Part{}, err
		}
		return fileOrInlinePart(url, "")
	case "video_url", "input_video":
		url, err := imageURLString(block.VideoURL)
		if err != nil {
			return aistudio.Part{}, err
		}
		media, ok := aistudio.ExternalMediaForURL(url)
		if !ok {
			return aistudio.Part{}, fmt.Errorf("video_url must contain a YouTube video URL")
		}
		return aistudio.Part{ExternalMedia: media}, nil
	case "file", "input_file":
		if block.File != nil {
			block.FileID, block.Filename, block.FileData = block.File.FileID, block.File.Filename, block.File.FileData
		}
		if block.FileData != "" {
			return fileDataPart(block.FileData, block.Filename)
		}
		if block.FileID == "" {
			block.FileID = block.FileURL
		}
		return aistudio.Part{File: &aistudio.FileRef{ID: block.FileID, Name: block.Filename}}, nil
	case "input_audio":
		if block.InputAudio == nil {
			return aistudio.Part{}, fmt.Errorf("input_audio is required")
		}
		data, err := decodeBase64Flexible(block.InputAudio.Data)
		if err != nil {
			return aistudio.Part{}, fmt.Errorf("input_audio.data: %w", err)
		}
		return aistudio.Part{InlineData: &aistudio.Blob{MIME: audioMIME(block.InputAudio.Format), Data: data}}, nil
	default:
		return aistudio.Part{}, fmt.Errorf("unsupported content type %q", block.Type)
	}
}

func imageURLString(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, nil
	}
	var object struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &object); err != nil || object.URL == "" {
		return "", fmt.Errorf("image_url must contain url")
	}
	return object.URL, nil
}

func fileOrInlinePart(value string, name string) (aistudio.Part, error) {
	if media, ok := aistudio.ExternalMediaForURL(value); ok {
		return aistudio.Part{ExternalMedia: media}, nil
	}
	if !strings.HasPrefix(value, "data:") {
		return aistudio.Part{File: &aistudio.FileRef{ID: value, Name: name}}, nil
	}
	metadata, encoded, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	if !ok {
		return aistudio.Part{}, fmt.Errorf("data URL must contain a comma")
	}
	var data []byte
	if mediaType, isBase64 := strings.CutSuffix(metadata, ";base64"); isBase64 {
		decoded, err := decodeBase64Flexible(encoded)
		if err != nil {
			return aistudio.Part{}, fmt.Errorf("data URL: %w", err)
		}
		metadata, data = mediaType, decoded
	} else {
		decoded, err := url.PathUnescape(encoded)
		if err != nil {
			return aistudio.Part{}, fmt.Errorf("data URL: %w", err)
		}
		data = []byte(decoded)
	}
	mimeType, _, err := mime.ParseMediaType(metadata)
	if err != nil {
		mimeType = detectMediaType(name, data)
	}
	mimeType, data = normalizeImagePayload(mimeType, data)
	return aistudio.Part{InlineData: &aistudio.Blob{MIME: mimeType, Data: data}}, nil
}

// fileDataPart 解析 data URL 或 base64 文件内容
func fileDataPart(value string, name string) (aistudio.Part, error) {
	if strings.HasPrefix(value, "data:") {
		return fileOrInlinePart(value, name)
	}
	data, err := decodeBase64Flexible(value)
	if err != nil {
		return aistudio.Part{}, fmt.Errorf("file_data: %w", err)
	}
	mimeType, data := normalizeImagePayload(detectMediaType(name, data), data)
	return aistudio.Part{InlineData: &aistudio.Blob{MIME: mimeType, Data: data}}, nil
}

// detectMediaType 按内容前缀判断不带参数的 MIME，无法识别时依次按 FLAC 头、MPEG 音频帧头与扩展名判断
func detectMediaType(name string, data []byte) string {
	detected, _, _ := mime.ParseMediaType(http.DetectContentType(data))
	switch {
	case detected == "application/ogg":
		return "audio/ogg"
	case detected == "audio/wave":
		return "audio/wav"
	case detected != "application/octet-stream":
		return detected
	case bytes.HasPrefix(data, []byte("fLaC")):
		return "audio/flac"
	case len(data) >= 2 && data[0] == 0xff && data[1]&0xe0 == 0xe0 && data[1]&0x06 != 0:
		return "audio/mpeg"
	}
	extension := strings.ToLower(path.Ext(name))
	if mediaType := audioExtensionTypes[extension]; mediaType != "" {
		return mediaType
	}
	if mediaType, _, err := mime.ParseMediaType(mime.TypeByExtension(extension)); err == nil {
		return mediaType
	}
	return detected
}

func audioMIME(format string) string {
	switch strings.ToLower(format) {
	case "mp3", "mpeg":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	default:
		return "audio/" + strings.ToLower(format)
	}
}

func mapOpenAITools(tools []openAITool, choice json.RawMessage) (aistudio.Tools, error) {
	var mapped aistudio.Tools
	for _, tool := range tools {
		switch tool.Type {
		case "function":
			if tool.Function.Name == "" {
				return aistudio.Tools{}, fmt.Errorf("function tool name is required")
			}
			parameters := tool.Function.Parameters
			if len(parameters) == 0 {
				parameters = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			mapped.Functions = append(mapped.Functions, aistudio.FunctionDeclaration{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  parameters,
				Strict:      tool.Function.Strict != nil && *tool.Function.Strict,
			})
		case "custom":
			if tool.Custom.Name == "" {
				return aistudio.Tools{}, fmt.Errorf("custom tool name is required")
			}
			mapped.Functions = append(mapped.Functions, customToolDeclaration(tool.Custom.Name, tool.Custom.Description, tool.Custom.Format))
		case "web_search", "web_search_preview":
			mapped.Google = appendUnique(mapped.Google, "google_search")
		case "code_interpreter":
			mapped.Google = appendUnique(mapped.Google, "code_execution")
		case "url_context":
			mapped.Google = appendUnique(mapped.Google, "url_context")
		case "google_maps":
			mapped.Google = appendUnique(mapped.Google, "google_maps")
		case "image_search":
			mapped.Google = appendUnique(mapped.Google, "image_search")
		default:
			return aistudio.Tools{}, fmt.Errorf("unsupported tool type %q", tool.Type)
		}
	}
	config, allowed, err := openAIToolChoice(choice)
	if err != nil {
		return aistudio.Tools{}, err
	}
	if allowed != nil {
		mapped = restrictAllowedTools(mapped, allowed)
	}
	mapped.ToolConfig = config
	return mapped, nil
}

// openAIToolChoice 转换 tool_choice，类型为 allowed_tools 时另外返回允许的工具引用
func openAIToolChoice(raw json.RawMessage) (aistudio.ToolConfig, []toolReference, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return aistudio.ToolConfig{Mode: "auto"}, nil, nil
	}
	var mode string
	if err := json.Unmarshal(raw, &mode); err == nil {
		switch mode {
		case "auto", "none", "required":
			return aistudio.ToolConfig{Mode: mode}, nil, nil
		default:
			return aistudio.ToolConfig{}, nil, fmt.Errorf("unsupported tool_choice %q", mode)
		}
	}
	var object struct {
		toolReference
		Mode         string          `json:"mode"`
		Tools        []toolReference `json:"tools"`
		AllowedTools *struct {
			Mode  string          `json:"mode"`
			Tools []toolReference `json:"tools"`
		} `json:"allowed_tools"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return aistudio.ToolConfig{}, nil, fmt.Errorf("invalid tool_choice: %w", err)
	}
	switch object.Type {
	case "allowed_tools":
		if object.AllowedTools != nil {
			object.Mode, object.Tools = object.AllowedTools.Mode, object.AllowedTools.Tools
		}
		if object.Mode != "auto" && object.Mode != "required" {
			return aistudio.ToolConfig{}, nil, fmt.Errorf("allowed_tools mode must be auto or required")
		}
		config := aistudio.ToolConfig{Mode: object.Mode}
		for _, tool := range object.Tools {
			if name := tool.functionName(); name != "" {
				config.AllowedFunctionNames = append(config.AllowedFunctionNames, name)
			}
		}
		return config, append([]toolReference{}, object.Tools...), nil
	case "web_search_preview", "web_search_preview_2025_03_11", "code_interpreter":
		return aistudio.ToolConfig{Mode: "required"}, nil, nil
	case "file_search", "computer", "computer_use", "computer_use_preview", "image_generation", "mcp", "programmatic_tool_calling", "tool_search":
		return aistudio.ToolConfig{Mode: "auto"}, nil, nil
	}
	if name := object.functionName(); name != "" {
		return aistudio.ToolConfig{Mode: "required", AllowedFunctionNames: []string{name}}, nil, nil
	}
	if object.Type == "function" || object.Type == "custom" {
		return aistudio.ToolConfig{}, nil, fmt.Errorf("named tool_choice requires a name")
	}
	return aistudio.ToolConfig{}, nil, fmt.Errorf("unsupported tool_choice type %q", object.Type)
}

// hostedGoogleTools 为托管工具类型对应的 Google 工具
var hostedGoogleTools = map[string]string{
	"web_search": "google_search", "web_search_2025_08_26": "google_search", "web_search_preview": "google_search",
	"web_search_preview_2025_03_11": "google_search", "code_interpreter": "code_execution", "url_context": "url_context",
	"google_maps": "google_maps", "image_search": "image_search",
}

// restrictAllowedTools 只保留 allowed_tools 引用的函数与托管工具对应的 Google 工具
func restrictAllowedTools(tools aistudio.Tools, allowed []toolReference) aistudio.Tools {
	names, google := make(map[string]bool), make(map[string]bool)
	for _, reference := range allowed {
		if name := reference.functionName(); name != "" {
			names[name] = true
		} else if tool := hostedGoogleTools[reference.Type]; tool != "" {
			google[tool] = true
		}
	}
	tools.Functions = slices.DeleteFunc(tools.Functions, func(declaration aistudio.FunctionDeclaration) bool { return !names[declaration.Name] })
	tools.Google = slices.DeleteFunc(tools.Google, func(tool string) bool { return !google[tool] })
	if !google["google_search"] {
		tools.GoogleSearch = nil
	}
	return tools
}

// toolReference 是 tool_choice 与 allowed_tools 中对单个工具的引用
type toolReference struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Function  *struct {
		Name string `json:"name"`
	} `json:"function"`
	Custom *struct {
		Name string `json:"name"`
	} `json:"custom"`
}

// functionName 返回引用对应的声明函数名，客户端执行工具使用固定函数名，托管工具返回空串
func (reference toolReference) functionName() string {
	switch reference.Type {
	case "function", "custom":
		name := reference.Name
		if reference.Function != nil {
			name = reference.Function.Name
		}
		if reference.Custom != nil {
			name = reference.Custom.Name
		}
		if reference.Namespace != "" && name != "" {
			name = reference.Namespace + "." + name
		}
		return name
	case "local_shell", "shell", "apply_patch":
		return reference.Type
	}
	return ""
}

func appendUnique(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func (request chatRequest) generationConfig() (aistudio.GenerationConfig, error) {
	if request.N != nil && (*request.N < 1 || *request.N > maxChatChoices) {
		return aistudio.GenerationConfig{}, fmt.Errorf("n must be between 1 and %d", maxChatChoices)
	}
	config := aistudio.GenerationConfig{
		Temperature:     request.Temperature,
		TopP:            request.TopP,
		ReasoningEffort: request.ReasoningEffort,
		Seed:            request.Seed,
	}
	if request.MaxCompletionTokens != nil {
		config.MaxOutputTokens = request.MaxCompletionTokens
	} else {
		config.MaxOutputTokens = request.MaxTokens
	}
	stop, err := decodeStopSequences(request.Stop)
	if err != nil {
		return config, err
	}
	config.StopSequences = stop
	if len(request.Reasoning) > 0 && string(request.Reasoning) != "null" {
		var reasoning struct {
			Effort string `json:"effort"`
		}
		if err := json.Unmarshal(request.Reasoning, &reasoning); err != nil {
			return config, fmt.Errorf("invalid reasoning: %w", err)
		}
		if reasoning.Effort != "" {
			config.ReasoningEffort = reasoning.Effort
		}
	}
	if len(request.ResponseFormat) > 0 && string(request.ResponseFormat) != "null" {
		var format struct {
			Type       string `json:"type"`
			JSONSchema struct {
				Schema json.RawMessage `json:"schema"`
			} `json:"json_schema"`
		}
		if err := json.Unmarshal(request.ResponseFormat, &format); err != nil {
			return config, fmt.Errorf("invalid response_format: %w", err)
		}
		switch format.Type {
		case "json_object":
			config.ResponseMIMEType = "application/json"
		case "json_schema":
			config.ResponseMIMEType = "application/json"
			config.ResponseSchema = format.JSONSchema.Schema
		case "", "text":
		default:
			return config, fmt.Errorf("unsupported response_format type %q", format.Type)
		}
	}
	return config, nil
}

func decodeStopSequences(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return normalizeStopSequences([]string{single}), nil
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err != nil {
		return nil, fmt.Errorf("stop must be a string or string array")
	}
	return normalizeStopSequences(multiple), nil
}

// normalizeStopSequences 删除不会形成停止条件的空字符串
func normalizeStopSequences(values []string) []string {
	var normalized []string
	for _, value := range values {
		if value != "" {
			normalized = append(normalized, value)
		}
	}
	return normalized
}

func buildChatCompletion(id string, created int64, model string, results []generationResult, tools []openAITool) map[string]any {
	choices := make([]any, 0, len(results))
	response := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
	}
	for index := range results {
		choices = append(choices, chatCompletionChoice(index, results[index], tools))
		if results[index].providerModel != "" && response["provider_model"] == nil {
			response["provider_model"] = results[index].providerModel
		}
	}
	response["choices"] = choices
	if usage := sumUsage(results); usage != nil {
		response["usage"] = openAIUsage(usage)
	}
	return response
}

// chatCompletionChoice 构造非流式响应中第 index 个 choice
func chatCompletionChoice(index int, result generationResult, tools []openAITool) map[string]any {
	rendered := renderedContent(result.events)
	content := any(rendered)
	if rendered == "" && len(result.toolCalls) > 0 {
		content = nil
	}
	message := map[string]any{"role": "assistant", "content": content}
	if result.reasoning.Len() > 0 {
		message["reasoning_content"] = result.reasoning.String()
	}
	if len(result.toolCalls) > 0 {
		message["tool_calls"] = openAIToolCallOutput(result.toolCalls, tools)
	}
	if len(result.citations) > 0 {
		message["annotations"] = openAICitations(result.citations)
	}
	choice := map[string]any{
		"index":         index,
		"message":       message,
		"logprobs":      nil,
		"finish_reason": openAIFinishReason(result.finishReason, len(result.toolCalls) > 0),
	}
	if providerReason := providerFinishReason(result.finishReason); providerReason != "" {
		choice["provider_finish_reason"] = providerReason
	}
	return choice
}

func (s *server) streamChatCompletion(w http.ResponseWriter, r *http.Request, request chatRequest, id string, created int64, choices *chatChoices) {
	if err := streamHeaders(w); err != nil {
		return
	}
	var writing sync.Mutex
	write := func(index int, delta map[string]any, finish *string) error {
		writing.Lock()
		defer writing.Unlock()
		return writeChatChunk(w, id, created, request.Model, index, delta, finish, request.StreamOptions.IncludeUsage)
	}
	for index := range choices.events {
		if err := write(index, map[string]any{"role": "assistant", "content": ""}, nil); err != nil {
			return
		}
	}
	heartbeat := func() error {
		writing.Lock()
		defer writing.Unlock()
		return writeSSEHeartbeat(w)
	}
	results, err := choices.run(func(index int, events <-chan aistudio.Event) (generationResult, error) {
		choice := &chatStreamChoice{server: s, tools: request.Tools, index: index, write: write}
		result, err := consumeStreamEvents(choices.ctx, events, choice.emit, heartbeat)
		if err != nil {
			return result, err
		}
		if len(result.citations) > 0 {
			_ = write(index, map[string]any{"annotations": openAICitations(result.citations)}, nil)
		}
		finish := openAIFinishReason(result.finishReason, len(result.toolCalls) > 0)
		finalDelta := map[string]any{}
		if providerReason := providerFinishReason(result.finishReason); providerReason != "" {
			finalDelta["provider_finish_reason"] = providerReason
		}
		return result, write(index, finalDelta, &finish)
	})
	if err != nil {
		choices.settle(r.Context())
		if shouldWriteRequestError(r, err) {
			status := statusFromError(err)
			code := openAIErrorCode(err)
			_ = writeSSE(w, "", map[string]any{"error": map[string]any{
				"message": err.Error(),
				"type":    openAIErrorType(status, code),
				"code":    code,
			}})
		}
		return
	}
	choices.record(r.Context(), results)
	if usage := sumUsage(results); request.StreamOptions.IncludeUsage && usage != nil {
		chunk := map[string]any{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   request.Model,
			"choices": []any{},
			"usage":   openAIUsage(usage),
		}
		if err := writeSSE(w, "", chunk); err != nil {
			return
		}
	}
	_ = writeSSEText(w, "[DONE]")
}

// chatStreamChoice 把一个 choice 的事件写成带 index 的 Chat chunk
type chatStreamChoice struct {
	server                 *server
	tools                  []openAITool
	index                  int
	write                  func(int, map[string]any, *string) error
	toolIndex              int
	hasContent             bool
	contentEndsWithNewline bool
}

// emit 写出一个上游事件对应的 delta
func (choice *chatStreamChoice) emit(event aistudio.Event) error {
	switch event.Kind {
	case aistudio.EventText:
		choice.hasContent = choice.hasContent || event.Text != ""
		choice.contentEndsWithNewline = strings.HasSuffix(event.Text, "\n")
		return choice.write(choice.index, map[string]any{"content": event.Text}, nil)
	case aistudio.EventReasoning:
		return choice.write(choice.index, map[string]any{"reasoning_content": event.Text}, nil)
	case aistudio.EventToolCall:
		if event.ToolCall == nil {
			return nil
		}
		call := event.ToolCall
		choice.server.thoughtSignatures.Remember([]aistudio.FunctionCall{*call})
		toolCall := openAIToolCallItem(*call, choice.tools)
		toolCall["index"] = choice.toolIndex
		choice.toolIndex++
		return choice.write(choice.index, map[string]any{"tool_calls": []any{toolCall}}, nil)
	case aistudio.EventMedia:
		if event.Media == nil {
			return nil
		}
		content := renderMediaMarkdown(*event.Media)
		if choice.hasContent && !choice.contentEndsWithNewline {
			content = "\n" + content
		}
		choice.hasContent = true
		choice.contentEndsWithNewline = false
		return choice.write(choice.index, map[string]any{"content": content}, nil)
	case aistudio.EventExecutableCode, aistudio.EventCodeExecutionResult:
		content := renderCodeExecution(event)
		if content == "" {
			return nil
		}
		if choice.hasContent && !choice.contentEndsWithNewline {
			content = "\n" + content
		}
		content += "\n"
		choice.hasContent = true
		choice.contentEndsWithNewline = true
		return choice.write(choice.index, map[string]any{"content": content}, nil)
	}
	return nil
}

func writeChatChunk(w http.ResponseWriter, id string, created int64, model string, index int, delta map[string]any, finish *string, includeUsage bool) error {
	choice := map[string]any{
		"index":         index,
		"delta":         delta,
		"finish_reason": finish,
	}
	if providerReason, ok := delta["provider_finish_reason"].(string); ok && providerReason != "" {
		delete(delta, "provider_finish_reason")
		choice["provider_finish_reason"] = providerReason
	}
	chunk := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []any{choice},
	}
	if includeUsage {
		chunk["usage"] = nil
	}
	return writeSSE(w, "", chunk)
}

func openAIFinishReason(reason string, hasTools bool) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "max_tokens", "max_output_tokens", "length":
		return "length"
	case "stop_sequence":
		return "stop"
	case "", "stop":
		if hasTools {
			return "tool_calls"
		}
		return "stop"
	default:
		return "content_filter"
	}
}

func openAIToolCallOutput(calls []aistudio.FunctionCall, tools []openAITool) []map[string]any {
	output := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		output = append(output, openAIToolCallItem(call, tools))
	}
	return output
}

// openAIToolCallItem 把函数调用投影为 Chat tool_calls 项，custom 工具使用 custom 形状
func openAIToolCallItem(call aistudio.FunctionCall, tools []openAITool) map[string]any {
	item := map[string]any{"id": call.ID, "type": "function"}
	if chatCustomTool(tools, call.Name) {
		item["type"] = "custom"
		item["custom"] = map[string]any{"name": call.Name, "input": customToolInput(call.Arguments)}
	} else {
		item["function"] = map[string]any{"name": call.Name, "arguments": string(call.Arguments)}
	}
	if call.ThoughtSignature != "" {
		item["extra_content"] = openAIGoogleThoughtSignature(call.ThoughtSignature)
	}
	return item
}

func openAIGoogleThoughtSignature(signature string) map[string]any {
	return map[string]any{"google": map[string]string{"thought_signature": signature}}
}

func openAICitations(citations []aistudio.Citation) []map[string]any {
	output := make([]map[string]any, 0, len(citations))
	for _, citation := range citations {
		output = append(output, map[string]any{
			"type": "url_citation",
			"url_citation": map[string]any{
				"start_index": citation.Start,
				"end_index":   citation.End,
				"title":       citation.Title,
				"url":         citation.URL,
			},
		})
	}
	return output
}

func openAIUsage(usage *aistudio.Usage) map[string]any {
	return map[string]any{
		"prompt_tokens":     inputTokens(usage),
		"completion_tokens": outputTokens(usage),
		"total_tokens":      usage.TotalTokens,
		"completion_tokens_details": map[string]any{
			"reasoning_tokens": usage.ReasoningTokens,
		},
	}
}

func renderedContent(events []aistudio.Event) string {
	var content strings.Builder
	for _, event := range events {
		switch event.Kind {
		case aistudio.EventText:
			content.WriteString(event.Text)
		case aistudio.EventMedia:
			if event.Media == nil {
				continue
			}
			if content.Len() > 0 && !strings.HasSuffix(content.String(), "\n") {
				content.WriteByte('\n')
			}
			content.WriteString(renderMediaMarkdown(*event.Media))
		case aistudio.EventExecutableCode, aistudio.EventCodeExecutionResult:
			rendered := renderCodeExecution(event)
			if rendered == "" {
				continue
			}
			if content.Len() > 0 && !strings.HasSuffix(content.String(), "\n") {
				content.WriteByte('\n')
			}
			content.WriteString(rendered)
			content.WriteByte('\n')
		}
	}
	return content.String()
}
