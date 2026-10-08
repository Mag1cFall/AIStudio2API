package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type anthropicRequest struct {
	Model         string             `json:"model"`
	Messages      []anthropicMessage `json:"messages"`
	System        json.RawMessage    `json:"system"`
	MaxTokens     *int64             `json:"max_tokens"`
	StopSequences []string           `json:"stop_sequences"`
	Stream        bool               `json:"stream"`
	Temperature   *float64           `json:"temperature"`
	TopP          *float64           `json:"top_p"`
	TopK          *int               `json:"top_k"`
	Tools         []anthropicTool    `json:"tools"`
	ToolChoice    json.RawMessage    `json:"tool_choice"`
	Thinking      *struct {
		Type         string `json:"type"`
		BudgetTokens *int64 `json:"budget_tokens"`
	} `json:"thinking"`
	OutputConfig *struct {
		Effort string `json:"effort"`
	} `json:"output_config"`
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicTool struct {
	Type        string                     `json:"type"`
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	InputSchema json.RawMessage            `json:"input_schema"`
	Options     map[string]json.RawMessage `json:"-"`
}

func (tool *anthropicTool) UnmarshalJSON(data []byte) error {
	type knownTool anthropicTool
	var known knownTool
	if err := json.Unmarshal(data, &known); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	delete(fields, "type")
	delete(fields, "name")
	delete(fields, "description")
	delete(fields, "input_schema")
	*tool = anthropicTool(known)
	tool.Options = fields
	return nil
}

type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  *string         `json:"thinking,omitempty"`
	Data      string          `json:"data,omitempty"`
	Signature string          `json:"signature,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

func (s *server) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	var request anthropicRequest
	if err := decodeJSON(r, &request); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if request.Model == "" {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	messageID := newID("msg")
	if err := s.decodeAnthropicSearchHistory(&request); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	generateRequest, err := request.toGenerateRequest(messageID)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	generateRequest.Unary = !request.Stream
	events, err := s.service.Generate(r.Context(), generateRequest)
	if err == nil && request.Stream {
		events, err = awaitStreamStart(r.Context(), events)
	}
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeAnthropicError(w, statusFromError(err), anthropicErrorType(err), err.Error())
		}
		return
	}
	if request.Stream {
		if err := streamHeaders(w); err != nil {
			return
		}
		writer := &anthropicStreamWriter{
			w: w, id: messageID, model: request.Model,
			inputTokens: aistudio.EstimatedInputTokens(generateRequest),
			searchKey:   s.config.APIKey,
		}
		if err := writer.start(); err != nil {
			return
		}
		s.streamAnthropic(r, writer, events)
		return
	}
	result, err := consumeEvents(r.Context(), events, nil)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeAnthropicError(w, statusFromError(err), anthropicErrorType(err), err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, buildAnthropicResponse(messageID, request.Model, result, s.config.APIKey))
}

func (s *server) handleAnthropicCountTokens(w http.ResponseWriter, r *http.Request) {
	var request anthropicRequest
	if err := decodeJSON(r, &request); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if request.Model == "" {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	if err := s.decodeAnthropicSearchHistory(&request); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	generateRequest, err := request.toGenerateRequest(newID("count"))
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	count, err := s.service.CountTokens(r.Context(), aistudio.TokenCountRequest{
		Model: generateRequest.Model, System: generateRequest.System, Contents: generateRequest.Contents,
		Tools: generateRequest.Tools,
	})
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeAnthropicError(w, statusFromError(err), anthropicErrorType(err), err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"input_tokens": count.InputTokens})
}

func (request anthropicRequest) toGenerateRequest(id string) (aistudio.GenerateRequest, error) {
	if request.Thinking != nil {
		switch request.Thinking.Type {
		case "enabled":
			if request.Thinking.BudgetTokens == nil {
				return aistudio.GenerateRequest{}, fmt.Errorf("thinking.budget_tokens is required when thinking.type is enabled")
			}
		case "adaptive":
		case "disabled":
		default:
			return aistudio.GenerateRequest{}, fmt.Errorf("thinking.type must be enabled, adaptive or disabled")
		}
	}
	system, err := anthropicSystemText(request.System)
	if err != nil {
		return aistudio.GenerateRequest{}, err
	}
	contents := make([]aistudio.Content, 0, len(request.Messages))
	for _, message := range request.Messages {
		if message.Role == "system" {
			text, err := anthropicSystemText(message.Content)
			if err != nil {
				return aistudio.GenerateRequest{}, fmt.Errorf("system message: %w", err)
			}
			if strings.TrimSpace(text) != "" {
				contents = append(contents, aistudio.Content{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "<system-reminder>\n" + text + "\n</system-reminder>"}}})
			}
			continue
		}
		role, err := anthropicRole(message.Role)
		if err != nil {
			return aistudio.GenerateRequest{}, err
		}
		parts, err := anthropicParts(message.Content)
		if err != nil {
			return aistudio.GenerateRequest{}, fmt.Errorf("%s message: %w", message.Role, err)
		}
		if len(parts) == 0 {
			continue
		}
		contents = append(contents, aistudio.Content{Role: role, Parts: parts})
	}
	tools, err := mapAnthropicTools(request.Tools, request.ToolChoice)
	if err != nil {
		return aistudio.GenerateRequest{}, err
	}
	config := aistudio.GenerationConfig{
		Temperature:     request.Temperature,
		TopP:            request.TopP,
		TopK:            request.TopK,
		MaxOutputTokens: request.MaxTokens,
		StopSequences:   normalizeStopSequences(request.StopSequences),
	}
	if request.Thinking != nil && request.Thinking.Type == "enabled" {
		config.ThinkingBudget = request.Thinking.BudgetTokens
	}
	if request.Thinking != nil && request.Thinking.Type == "disabled" {
		config.ReasoningEffort = "none"
		config.HideThinking = true
	}
	if request.OutputConfig != nil {
		config.ReasoningEffort = request.OutputConfig.Effort
	}
	return aistudio.GenerateRequest{
		ID: id, Model: request.Model, System: system, Contents: contents, Config: config, Tools: tools,
	}, nil
}

func anthropicSystemText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", fmt.Errorf("system must be a string or text block array")
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" {
			return "", fmt.Errorf("unsupported system block type %q", block.Type)
		}
		if block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	return strings.Join(texts, "\n"), nil
}

func anthropicRole(role string) (aistudio.Role, error) {
	switch role {
	case "user":
		return aistudio.RoleUser, nil
	case "assistant":
		return aistudio.RoleAssistant, nil
	default:
		return "", fmt.Errorf("unsupported message role %q", role)
	}
}

// anthropicParts 转换消息块并保留工具调用的思考签名
func anthropicParts(raw json.RawMessage) ([]aistudio.Part, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if text == "" {
			return nil, nil
		}
		return []aistudio.Part{{Text: text}}, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("content must be a string or block array")
	}
	parts := make([]aistudio.Part, 0, len(blocks))
	pendingSignature := ""
	flushPendingSignature := func() {
		if pendingSignature == "" {
			return
		}
		parts = append(parts, aistudio.Part{ThoughtSignature: pendingSignature})
		pendingSignature = ""
	}
	for _, rawBlock := range blocks {
		var block struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			Data      string          `json:"data"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
			Signature string          `json:"signature"`
			Source    json.RawMessage `json:"source"`
			Title     string          `json:"title"`
			FileID    string          `json:"file_id"`
		}
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			return nil, err
		}
		switch block.Type {
		case "text":
			if block.Text == "" {
				continue
			}
			flushPendingSignature()
			parts = append(parts, aistudio.Part{Text: block.Text})
		case "thinking":
			flushPendingSignature()
			pendingSignature = block.Signature
		case "redacted_thinking":
			flushPendingSignature()
			if block.Data == "" {
				return nil, fmt.Errorf("redacted_thinking data is required")
			}
			pendingSignature = block.Data
		case "image", "document":
			flushPendingSignature()
			if !rawJSONConfigured(block.Source) {
				return nil, fmt.Errorf("%s source is required", block.Type)
			}
			var source struct {
				Type      string          `json:"type"`
				MediaType string          `json:"media_type"`
				Data      string          `json:"data"`
				URL       string          `json:"url"`
				FileID    string          `json:"file_id"`
				Content   json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(block.Source, &source); err != nil {
				return nil, fmt.Errorf("%s source: %w", block.Type, err)
			}
			switch source.Type {
			case "base64":
				data, err := decodeBase64Flexible(source.Data)
				if err != nil {
					return nil, fmt.Errorf("%s source data: %w", block.Type, err)
				}
				mediaType, data := normalizeImagePayload(source.MediaType, data)
				parts = append(parts, aistudio.Part{InlineData: &aistudio.Blob{MIME: mediaType, Data: data}})
			case "url":
				if media, ok := aistudio.ExternalMediaForURL(source.URL); ok {
					parts = append(parts, aistudio.Part{ExternalMedia: media})
				} else {
					mime := source.MediaType
					if mime == "" && block.Type == "image" {
						mime = "image/*"
					}
					if mime == "" {
						mime = "application/pdf"
					}
					if strings.TrimSpace(source.URL) == "" {
						return nil, fmt.Errorf("%s source URL is required", block.Type)
					}
					parts = append(parts, aistudio.Part{ExternalMedia: &aistudio.ExternalMedia{MIME: mime, URL: source.URL}})
				}
			case "text":
				if source.Data != "" {
					parts = append(parts, aistudio.Part{Text: source.Data})
				}
			case "content":
				nested, err := anthropicParts(source.Content)
				if err != nil {
					return nil, fmt.Errorf("%s source content: %w", block.Type, err)
				}
				parts = append(parts, nested...)
			case "file":
				parts = append(parts, aistudio.Part{File: &aistudio.FileRef{ID: source.FileID}})
			default:
				return nil, fmt.Errorf("unsupported source type %q", source.Type)
			}
		case "tool_use":
			input := block.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			parts = append(parts, aistudio.Part{FunctionCall: &aistudio.FunctionCall{
				ID: block.ID, Name: block.Name, Arguments: input, ThoughtSignature: pendingSignature,
			}})
			pendingSignature = ""
		case "tool_result":
			flushPendingSignature()
			content := block.Content
			if block.IsError {
				if len(content) == 0 {
					content = json.RawMessage("null")
				}
				if !json.Valid(content) {
					return nil, fmt.Errorf("tool_result: function result must be JSON")
				}
				content = json.RawMessage(`{"error":` + strings.TrimSpace(string(content)) + `}`)
			} else {
				var err error
				content, err = normalizeFunctionResultContent(content)
				if err != nil {
					return nil, fmt.Errorf("tool_result: %w", err)
				}
			}
			parts = append(parts, aistudio.Part{FunctionResult: &aistudio.FunctionResult{
				ID: block.ToolUseID, Content: content,
			}})
		case "search_result":
			flushPendingSignature()
			var source string
			_ = json.Unmarshal(block.Source, &source)
			nested, err := anthropicParts(block.Content)
			if err != nil {
				return nil, fmt.Errorf("search_result content: %w", err)
			}
			parts = append(parts, aistudio.Part{Text: "Search result: " + block.Title + "\n" + source})
			parts = append(parts, nested...)
		case "server_tool_use":
			flushPendingSignature()
			parts = append(parts, aistudio.Part{Text: "Server tool call " + block.Name + ": " + string(block.Input)})
		case "web_fetch_tool_result":
			flushPendingSignature()
			var result struct {
				URL       string          `json:"url"`
				ErrorCode string          `json:"error_code"`
				Content   json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(block.Content, &result); err != nil {
				return nil, fmt.Errorf("web_fetch_tool_result content: %w", err)
			}
			if result.ErrorCode != "" {
				parts = append(parts, aistudio.Part{Text: "Web fetch failed: " + result.ErrorCode})
			} else {
				document, err := anthropicParts(json.RawMessage("[" + string(result.Content) + "]"))
				if err != nil {
					return nil, fmt.Errorf("web_fetch_tool_result content: %w", err)
				}
				parts = append(parts, aistudio.Part{Text: "Web fetch: " + result.URL})
				parts = append(parts, document...)
			}
		case "code_execution_tool_result", "bash_code_execution_tool_result", "text_editor_code_execution_tool_result", "tool_search_tool_result":
			flushPendingSignature()
			parts = append(parts, aistudio.Part{Text: "Server tool result " + block.Type + ": " + string(block.Content)})
		case "container_upload":
			flushPendingSignature()
			parts = append(parts, aistudio.Part{File: &aistudio.FileRef{ID: block.FileID}})
		default:
			return nil, fmt.Errorf("unsupported content block type %q", block.Type)
		}
	}
	flushPendingSignature()
	return parts, nil
}

func mapAnthropicTools(tools []anthropicTool, choice json.RawMessage) (aistudio.Tools, error) {
	var mapped aistudio.Tools
	for _, tool := range tools {
		typeName := strings.ToLower(tool.Type)
		switch {
		case strings.HasPrefix(typeName, "web_search_"):
			if err := validateAnthropicServerTool(tool, "web_search"); err != nil {
				return aistudio.Tools{}, err
			}
			var filters json.RawMessage
			if domains := tool.Options["allowed_domains"]; rawJSONConfigured(domains) {
				filters = json.RawMessage(`{"allowed_domains":` + string(domains) + `}`)
			}
			search, err := mapSearchOptions("", tool.Options["user_location"], filters)
			if err != nil {
				return aistudio.Tools{}, err
			}
			mapped.GoogleSearch = search
			mapped.Google = appendUnique(mapped.Google, "google_search")
		case typeName == "image_search":
			if err := validateAnthropicServerTool(tool, "image_search"); err != nil {
				return aistudio.Tools{}, err
			}
			mapped.Google = appendUnique(mapped.Google, "image_search")
		case strings.HasPrefix(typeName, "web_fetch_"):
			if err := validateAnthropicServerTool(tool, "web_fetch"); err != nil {
				return aistudio.Tools{}, err
			}
			mapped.Google = appendUnique(mapped.Google, "url_context")
		case strings.HasPrefix(typeName, "code_execution_"):
			if err := validateAnthropicServerTool(tool, "code_execution"); err != nil {
				return aistudio.Tools{}, err
			}
			mapped.Google = appendUnique(mapped.Google, "code_execution")
		case strings.HasPrefix(typeName, "bash_"), strings.HasPrefix(typeName, "text_editor_"), strings.HasPrefix(typeName, "memory_"):
			declaration, err := anthropicClientTool(tool, typeName)
			if err != nil {
				return aistudio.Tools{}, err
			}
			mapped.Functions = append(mapped.Functions, declaration)
		case strings.HasPrefix(typeName, "tool_search_tool_"), strings.Contains(typeName, "toolset"):
		case typeName == "url_context":
			if err := validateAnthropicServerTool(tool, "url_context"); err != nil {
				return aistudio.Tools{}, err
			}
			mapped.Google = appendUnique(mapped.Google, "url_context")
		case typeName == "google_maps":
			if err := validateAnthropicServerTool(tool, "google_maps"); err != nil {
				return aistudio.Tools{}, err
			}
			mapped.Google = appendUnique(mapped.Google, "google_maps")
		case typeName == "", typeName == "custom":
			if tool.Name == "" {
				return aistudio.Tools{}, fmt.Errorf("tool name is required")
			}
			var strict bool
			if raw := tool.Options["strict"]; rawJSONConfigured(raw) {
				if err := json.Unmarshal(raw, &strict); err != nil {
					return aistudio.Tools{}, fmt.Errorf("tool strict must be a boolean")
				}
			}
			parameters := tool.InputSchema
			if len(parameters) == 0 {
				parameters = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			mapped.Functions = append(mapped.Functions, aistudio.FunctionDeclaration{
				Name: tool.Name, Description: tool.Description, Parameters: parameters, Strict: strict,
			})
		default:
			return aistudio.Tools{}, fmt.Errorf("unsupported tool type %q", tool.Type)
		}
	}
	config, err := anthropicToolChoice(choice)
	if err != nil {
		return aistudio.Tools{}, err
	}
	mapped.ToolConfig = config
	return mapped, nil
}

// anthropicServerToolOptions 为 Anthropic 服务端工具定义接受的选项
var anthropicServerToolOptions = []string{
	"allowed_callers", "allowed_domains", "blocked_domains", "cache_control", "citations", "defer_loading",
	"max_content_tokens", "max_uses", "response_inclusion", "strict", "url_sources", "use_cache", "user_location",
}

func validateAnthropicServerTool(tool anthropicTool, name string) error {
	if tool.Name != name {
		return fmt.Errorf("tool type %q requires name %q", tool.Type, name)
	}
	if tool.Description != "" || rawJSONConfigured(tool.InputSchema) {
		return fmt.Errorf("tool type %q does not accept description or input_schema", tool.Type)
	}
	fields := make([]string, 0, len(tool.Options))
	for field := range tool.Options {
		if !slices.Contains(anthropicServerToolOptions, field) {
			fields = append(fields, field)
		}
	}
	if len(fields) == 0 {
		return nil
	}
	sort.Strings(fields)
	return fmt.Errorf("tool type %q has unsupported option %q", tool.Type, fields[0])
}

// anthropicClientTool 将 Anthropic 定义的客户端工具映射为固定参数的函数声明
func anthropicClientTool(tool anthropicTool, typeName string) (aistudio.FunctionDeclaration, error) {
	if tool.Name == "" {
		return aistudio.FunctionDeclaration{}, fmt.Errorf("tool name is required")
	}
	declaration := aistudio.FunctionDeclaration{Name: tool.Name}
	switch {
	case strings.HasPrefix(typeName, "bash_"):
		declaration.Description = "Run a shell command in a persistent bash session. Set restart to true to restart the session."
		declaration.Parameters = json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"restart":{"type":"boolean"}}}`)
	case strings.HasPrefix(typeName, "text_editor_"):
		commands := `"view","create","str_replace","insert"`
		if typeName == "text_editor_20241022" || typeName == "text_editor_20250124" {
			commands += `,"undo_edit"`
		}
		declaration.Description = "View, create and edit text files. view reads a file or lists a directory, create writes file_text, str_replace replaces old_str with new_str, insert adds insert_text after insert_line."
		declaration.Parameters = json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","enum":[` + commands + `]},"path":{"type":"string"},"file_text":{"type":"string"},"old_str":{"type":"string"},"new_str":{"type":"string"},"insert_line":{"type":"integer"},"insert_text":{"type":"string"},"view_range":{"type":"array","items":{"type":"integer"}}},"required":["command","path"]}`)
	default:
		declaration.Description = "Read and write persistent memory files in the /memories directory. view reads a file or lists a directory, create writes file_text, str_replace replaces old_str with new_str, insert adds insert_text after insert_line, delete removes path, rename moves old_path to new_path."
		declaration.Parameters = json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","enum":["view","create","str_replace","insert","delete","rename"]},"path":{"type":"string"},"file_text":{"type":"string"},"old_str":{"type":"string"},"new_str":{"type":"string"},"insert_line":{"type":"integer"},"insert_text":{"type":"string"},"view_range":{"type":"array","items":{"type":"integer"}},"old_path":{"type":"string"},"new_path":{"type":"string"}},"required":["command"]}`)
	}
	return declaration, nil
}

func anthropicToolChoice(raw json.RawMessage) (aistudio.ToolConfig, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return aistudio.ToolConfig{Mode: "auto"}, nil
	}
	var choice struct {
		Type            string `json:"type"`
		Name            string `json:"name"`
		DisableParallel *bool  `json:"disable_parallel_tool_use"`
	}
	if err := json.Unmarshal(raw, &choice); err != nil {
		return aistudio.ToolConfig{}, fmt.Errorf("invalid tool_choice: %w", err)
	}
	config := aistudio.ToolConfig{}
	if choice.DisableParallel != nil {
		parallel := !*choice.DisableParallel
		config.ParallelCalls = &parallel
	}
	switch choice.Type {
	case "auto":
		config.Mode = "auto"
	case "none":
		config.Mode = "none"
	case "any":
		config.Mode = "required"
	case "tool":
		if choice.Name == "" {
			return aistudio.ToolConfig{}, fmt.Errorf("tool_choice name is required")
		}
		config.Mode, config.AllowedFunctionNames = "required", []string{choice.Name}
	default:
		return aistudio.ToolConfig{}, fmt.Errorf("unsupported tool_choice type %q", choice.Type)
	}
	return config, nil
}

func buildAnthropicResponse(id string, model string, result generationResult, searchKey string) map[string]any {
	stopReason, stopSequence := anthropicStop(result.finishReason, len(result.toolCalls) > 0, result.stopSequence)
	searchBlocks, searchCount := anthropicSearchBlocks(result.events, searchKey)
	if searchCount > 0 {
		result.citations = nil
	}
	response := map[string]any{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       append(anthropicBlocks(result), searchBlocks...),
		"stop_reason":   stopReason,
		"stop_sequence": stopSequence,
	}
	if result.providerModel != "" {
		response["provider_model"] = result.providerModel
	}
	if providerReason := providerFinishReason(result.finishReason); providerReason != "" {
		response["provider_finish_reason"] = providerReason
	}
	if result.usage != nil {
		response["usage"] = anthropicUsage(result.usage)
	}
	if searchCount > 0 {
		usage, ok := response["usage"].(map[string]any)
		if !ok {
			usage = map[string]any{"input_tokens": 0, "output_tokens": 0}
			response["usage"] = usage
		}
		usage["server_tool_use"] = map[string]int{"web_search_requests": searchCount, "web_fetch_requests": 0}
	}
	return response
}

func anthropicBlocks(result generationResult) []anthropicContentBlock {
	blocks := make([]anthropicContentBlock, 0)
	for _, event := range result.events {
		switch event.Kind {
		case aistudio.EventText:
			if len(blocks) > 0 && blocks[len(blocks)-1].Type == "text" {
				blocks[len(blocks)-1].Text += event.Text
			} else {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: event.Text})
			}
		case aistudio.EventReasoning:
			if len(blocks) > 0 && blocks[len(blocks)-1].Type == "thinking" {
				*blocks[len(blocks)-1].Thinking += event.Text
				if event.ThoughtSignature != "" {
					blocks[len(blocks)-1].Signature = event.ThoughtSignature
				}
			} else {
				thinking := event.Text
				blocks = append(blocks, anthropicContentBlock{Type: "thinking", Thinking: &thinking, Signature: event.ThoughtSignature})
			}
		case aistudio.EventThoughtSignature:
			if event.ThoughtSignature == "" {
				continue
			}
			blocks = append(blocks, anthropicContentBlock{Type: "redacted_thinking", Data: event.ThoughtSignature})
		case aistudio.EventToolCall:
			if event.ToolCall != nil {
				if event.ToolCall.ThoughtSignature != "" {
					if len(blocks) > 0 && blocks[len(blocks)-1].Type == "thinking" {
						blocks[len(blocks)-1].Signature = event.ToolCall.ThoughtSignature
					} else {
						blocks = append(blocks, anthropicContentBlock{
							Type: "redacted_thinking", Data: event.ToolCall.ThoughtSignature,
						})
					}
				}
				blocks = append(blocks, anthropicContentBlock{
					Type: "tool_use", ID: event.ToolCall.ID, Name: event.ToolCall.Name, Input: event.ToolCall.Arguments,
				})
			}
		case aistudio.EventMedia:
			if event.Media != nil {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: renderMediaMarkdown(*event.Media)})
			}
		case aistudio.EventExecutableCode, aistudio.EventCodeExecutionResult:
			rendered := renderCodeExecution(event)
			if rendered == "" {
				continue
			}
			if len(blocks) > 0 && blocks[len(blocks)-1].Type == "text" {
				blocks[len(blocks)-1].Text += "\n" + rendered + "\n"
			} else {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: rendered + "\n"})
			}
		}
	}
	if sources := renderCitationsMarkdown(result.citations); sources != "" {
		if len(blocks) > 0 && blocks[len(blocks)-1].Type == "text" {
			blocks[len(blocks)-1].Text += "\n\n" + sources
		} else {
			blocks = append(blocks, anthropicContentBlock{Type: "text", Text: sources})
		}
	}
	return blocks
}

func anthropicStop(reason string, hasTools bool, stopSequence string) (string, *string) {
	normalized := strings.ToLower(strings.TrimSpace(reason))
	switch normalized {
	case "max_tokens", "max_output_tokens", "length":
		return "max_tokens", nil
	case "stop_sequence":
		if stopSequence != "" {
			return "stop_sequence", &stopSequence
		}
		return "stop_sequence", nil
	case "pause_turn":
		return "pause_turn", nil
	case "", "stop":
		if hasTools {
			return "tool_use", nil
		}
		return "end_turn", nil
	default:
		return "refusal", nil
	}
}

func anthropicUsage(usage *aistudio.Usage) map[string]any {
	return map[string]any{
		"input_tokens":  inputTokens(usage),
		"output_tokens": outputTokens(usage),
	}
}

// writeAnthropicModels 按 Anthropic Models API 的生命周期、游标与条数参数返回一页模型，省略 limit 时返回全部
func writeAnthropicModels(w http.ResponseWriter, r *http.Request, models []aistudio.Model) {
	query := r.URL.Query()
	if lifecycles := append(query["lifecycle"], query["lifecycle[]"]...); len(lifecycles) > 0 && !slices.Contains(strings.Split(strings.Join(lifecycles, ","), ","), "active") {
		models = nil
	}
	page, hasMore, err := anthropicListPage(query, models, func(model aistudio.Model) string { return model.ID }, listLimits{fallback: len(models), min: 1, max: 1000})
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	data := make([]map[string]any, 0, len(page))
	for _, model := range page {
		data = append(data, anthropicModelObject(model))
	}
	response := map[string]any{"data": data, "has_more": hasMore, "first_id": nil, "last_id": nil}
	if len(page) > 0 {
		response["first_id"] = page[0].ID
		response["last_id"] = page[len(page)-1].ID
	}
	writeJSON(w, http.StatusOK, response)
}

// anthropicModelObject 投影列表与单模型共用的 Anthropic ModelInfo 字段，输入与输出上限取实时目录
func anthropicModelObject(model aistudio.Model) map[string]any {
	return map[string]any{
		"type": "model", "id": model.ID, "display_name": model.Name, "created_at": "1970-01-01T00:00:00Z",
		"max_input_tokens": anthropicTokenLimit(model.InputTokenLimit), "max_tokens": anthropicTokenLimit(model.OutputTokenLimit),
		"capabilities": nil, "lifecycle": "active", "line": nil, "deprecated_at": nil, "retires_at": nil,
	}
}

// anthropicTokenLimit 将目录缺失的 token 上限投影为 null
func anthropicTokenLimit(limit int64) any {
	if limit <= 0 {
		return nil
	}
	return limit
}

type anthropicStreamWriter struct {
	w                 http.ResponseWriter
	id                string
	model             string
	inputTokens       int64
	blockIndex        int
	currentBlock      string
	thinkingSignature string
	searchKey         string
}

func (s *server) streamAnthropic(r *http.Request, writer *anthropicStreamWriter, events <-chan aistudio.Event) {
	result, err := consumeStreamEvents(r.Context(), events, writer.live, func() error { return writeSSEHeartbeat(writer.w) })
	if err != nil {
		if shouldWriteRequestError(r, err) {
			_ = writer.error(err)
		}
		return
	}
	_ = writer.finish(result)
}

func (writer *anthropicStreamWriter) emit(eventType string, payload any) error {
	return writeSSE(writer.w, eventType, payload)
}

func (writer *anthropicStreamWriter) start() error {
	return writer.emit("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": writer.id, "type": "message", "role": "assistant", "model": writer.model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]int64{"input_tokens": writer.inputTokens, "output_tokens": 0},
		},
	})
}

func (writer *anthropicStreamWriter) live(event aistudio.Event) error {
	switch event.Kind {
	case aistudio.EventText:
		if err := writer.ensureBlock("text"); err != nil {
			return err
		}
		return writer.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": writer.blockIndex,
			"delta": map[string]any{"type": "text_delta", "text": event.Text},
		})
	case aistudio.EventReasoning:
		if err := writer.ensureBlock("thinking"); err != nil {
			return err
		}
		if event.ThoughtSignature != "" {
			writer.thinkingSignature = event.ThoughtSignature
		}
		return writer.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": writer.blockIndex,
			"delta": map[string]any{"type": "thinking_delta", "thinking": event.Text},
		})
	case aistudio.EventThoughtSignature:
		if event.ThoughtSignature == "" || writer.currentBlock == "text" {
			return nil
		}
		return writer.redactedThinking(event.ThoughtSignature)
	case aistudio.EventToolCall:
		if event.ToolCall == nil {
			return nil
		}
		call := event.ToolCall
		if call.ThoughtSignature != "" {
			if writer.currentBlock == "thinking" {
				writer.thinkingSignature = call.ThoughtSignature
			} else if err := writer.redactedThinking(call.ThoughtSignature); err != nil {
				return err
			}
		}
		if err := writer.closeBlock(); err != nil {
			return err
		}
		if err := writer.emit("content_block_start", map[string]any{
			"type": "content_block_start", "index": writer.blockIndex,
			"content_block": map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": map[string]any{}},
		}); err != nil {
			return err
		}
		writer.currentBlock = "tool_use"
		if err := writer.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": writer.blockIndex,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": string(call.Arguments)},
		}); err != nil {
			return err
		}
		return writer.closeBlock()
	case aistudio.EventMedia:
		if event.Media == nil {
			return nil
		}
		if err := writer.closeBlock(); err != nil {
			return err
		}
		if err := writer.ensureBlock("text"); err != nil {
			return err
		}
		return writer.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": writer.blockIndex,
			"delta": map[string]any{"type": "text_delta", "text": renderMediaMarkdown(*event.Media)},
		})
	case aistudio.EventExecutableCode, aistudio.EventCodeExecutionResult:
		rendered := renderCodeExecution(event)
		if rendered == "" {
			return nil
		}
		if err := writer.ensureBlock("text"); err != nil {
			return err
		}
		return writer.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": writer.blockIndex,
			"delta": map[string]any{"type": "text_delta", "text": "\n" + rendered + "\n"},
		})
	}
	return nil
}

func (writer *anthropicStreamWriter) ensureBlock(blockType string) error {
	if writer.currentBlock == blockType {
		return nil
	}
	if err := writer.closeBlock(); err != nil {
		return err
	}
	block := map[string]any{"type": blockType}
	if blockType == "thinking" {
		block["thinking"] = ""
	} else {
		block["text"] = ""
	}
	if err := writer.emit("content_block_start", map[string]any{
		"type": "content_block_start", "index": writer.blockIndex, "content_block": block,
	}); err != nil {
		return err
	}
	writer.currentBlock = blockType
	return nil
}

func (writer *anthropicStreamWriter) redactedThinking(data string) error {
	if err := writer.closeBlock(); err != nil {
		return err
	}
	if err := writer.emit("content_block_start", map[string]any{
		"type": "content_block_start", "index": writer.blockIndex,
		"content_block": map[string]any{"type": "redacted_thinking", "data": data},
	}); err != nil {
		return err
	}
	if err := writer.emit("content_block_stop", map[string]any{
		"type": "content_block_stop", "index": writer.blockIndex,
	}); err != nil {
		return err
	}
	writer.blockIndex++
	return nil
}

func (writer *anthropicStreamWriter) closeBlock() error {
	if writer.currentBlock == "" {
		return nil
	}
	if writer.currentBlock == "thinking" && writer.thinkingSignature != "" {
		if err := writer.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": writer.blockIndex,
			"delta": map[string]any{"type": "signature_delta", "signature": writer.thinkingSignature},
		}); err != nil {
			return err
		}
	}
	if err := writer.emit("content_block_stop", map[string]any{
		"type": "content_block_stop", "index": writer.blockIndex,
	}); err != nil {
		return err
	}
	writer.blockIndex++
	writer.currentBlock = ""
	writer.thinkingSignature = ""
	return nil
}

func (writer *anthropicStreamWriter) finish(result generationResult) error {
	searchBlocks, searchCount := anthropicSearchBlocks(result.events, writer.searchKey)
	if searchCount > 0 {
		result.citations = nil
	}
	if sources := renderCitationsMarkdown(result.citations); sources != "" {
		prefix := ""
		if writer.currentBlock == "text" {
			prefix = "\n\n"
		}
		if err := writer.ensureBlock("text"); err != nil {
			return err
		}
		if err := writer.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": writer.blockIndex,
			"delta": map[string]any{"type": "text_delta", "text": prefix + sources},
		}); err != nil {
			return err
		}
	}
	if err := writer.closeBlock(); err != nil {
		return err
	}
	for _, block := range searchBlocks {
		if err := writer.emit("content_block_start", map[string]any{
			"type": "content_block_start", "index": writer.blockIndex, "content_block": block,
		}); err != nil {
			return err
		}
		writer.currentBlock = block.Type
		if err := writer.closeBlock(); err != nil {
			return err
		}
	}
	usage := map[string]any{"output_tokens": 0}
	if result.usage != nil {
		usage["input_tokens"] = inputTokens(result.usage)
		usage["output_tokens"] = outputTokens(result.usage)
	}
	if searchCount > 0 {
		usage["server_tool_use"] = map[string]int{"web_search_requests": searchCount, "web_fetch_requests": 0}
	}
	stopReason, stopSequence := anthropicStop(result.finishReason, len(result.toolCalls) > 0, result.stopSequence)
	delta := map[string]any{
		"stop_reason": stopReason, "stop_sequence": stopSequence,
	}
	if providerReason := providerFinishReason(result.finishReason); providerReason != "" {
		delta["provider_finish_reason"] = providerReason
	}
	if err := writer.emit("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": delta,
		"usage": usage,
	}); err != nil {
		return err
	}
	return writer.emit("message_stop", map[string]string{"type": "message_stop"})
}

func (writer *anthropicStreamWriter) error(err error) error {
	return writer.emit("error", map[string]any{
		"type":  "error",
		"error": map[string]string{"type": anthropicErrorType(err), "message": err.Error()},
	})
}
