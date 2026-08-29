package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
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
}

func (s *server) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	var request anthropicRequest
	if err := decodeJSON(r, &request); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if request.Model == "" || len(request.Messages) == 0 || request.MaxTokens == nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "model, messages and max_tokens are required")
		return
	}
	if request.Messages[len(request.Messages)-1].Role == "assistant" {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "assistant prefill is not supported by the AI Studio upstream")
		return
	}
	messageID := newID("msg")
	generateRequest, err := request.toGenerateRequest(messageID)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if request.Stream {
		streamHeaders(w)
		writer := &anthropicStreamWriter{
			w: w, id: messageID, model: request.Model,
			inputTokens: aistudio.EstimatedInputTokens(generateRequest),
		}
		if err := writer.start(); err != nil {
			return
		}
		events, err := s.service.Generate(r.Context(), generateRequest)
		if err != nil {
			if shouldWriteRequestError(r, err) {
				_ = writer.error(err)
			}
			return
		}
		s.streamAnthropic(r, writer, events)
		return
	}
	events, err := s.service.Generate(r.Context(), generateRequest)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeAnthropicError(w, statusFromError(err), anthropicErrorType(err), err.Error())
		}
		return
	}
	result, err := consumeEvents(r.Context(), events, nil)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeAnthropicError(w, statusFromError(err), anthropicErrorType(err), err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, buildAnthropicResponse(messageID, request.Model, result))
}

func (s *server) handleAnthropicCountTokens(w http.ResponseWriter, r *http.Request) {
	var request anthropicRequest
	if err := decodeJSON(r, &request); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if request.Model == "" || len(request.Messages) == 0 {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "model and messages are required")
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
		case "disabled":
			return aistudio.GenerateRequest{}, fmt.Errorf("thinking.type disabled is not supported by the AI Studio upstream")
		default:
			return aistudio.GenerateRequest{}, fmt.Errorf("thinking.type must be enabled")
		}
	}
	system, err := anthropicSystemText(request.System)
	if err != nil {
		return aistudio.GenerateRequest{}, err
	}
	contents := make([]aistudio.Content, 0, len(request.Messages))
	for _, message := range request.Messages {
		role, err := anthropicRole(message.Role)
		if err != nil {
			return aistudio.GenerateRequest{}, err
		}
		parts, err := anthropicParts(message.Content)
		if err != nil {
			return aistudio.GenerateRequest{}, fmt.Errorf("%s message: %w", message.Role, err)
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

func anthropicParts(raw json.RawMessage) ([]aistudio.Part, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
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
			Source    *struct {
				Type      string `json:"type"`
				MediaType string `json:"media_type"`
				Data      string `json:"data"`
				URL       string `json:"url"`
			} `json:"source"`
		}
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			return nil, err
		}
		switch block.Type {
		case "text":
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
			if block.Source == nil {
				return nil, fmt.Errorf("%s source is required", block.Type)
			}
			switch block.Source.Type {
			case "base64":
				data, err := base64.StdEncoding.DecodeString(block.Source.Data)
				if err != nil {
					return nil, fmt.Errorf("%s source data: %w", block.Type, err)
				}
				parts = append(parts, aistudio.Part{InlineData: &aistudio.Blob{MIME: block.Source.MediaType, Data: data}})
			case "url":
				if media, ok := aistudio.ExternalMediaForURL(block.Source.URL); ok {
					parts = append(parts, aistudio.Part{ExternalMedia: media})
				} else {
					mime := block.Source.MediaType
					if mime == "" && block.Type == "image" {
						mime = "image/*"
					}
					if mime == "" {
						mime = "application/pdf"
					}
					if strings.TrimSpace(block.Source.URL) == "" {
						return nil, fmt.Errorf("%s source URL is required", block.Type)
					}
					parts = append(parts, aistudio.Part{ExternalMedia: &aistudio.ExternalMedia{MIME: mime, URL: block.Source.URL}})
				}
			default:
				return nil, fmt.Errorf("unsupported source type %q", block.Source.Type)
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
		case typeName == "web_search_20250305":
			if err := validateAnthropicServerTool(tool, "web_search"); err != nil {
				return aistudio.Tools{}, err
			}
			mapped.Google = appendUnique(mapped.Google, "google_search")
		case typeName == "image_search":
			if err := validateAnthropicServerTool(tool, "image_search"); err != nil {
				return aistudio.Tools{}, err
			}
			mapped.Google = appendUnique(mapped.Google, "image_search")
		case typeName == "web_fetch_20250910":
			if err := validateAnthropicServerTool(tool, "web_fetch"); err != nil {
				return aistudio.Tools{}, err
			}
			mapped.Google = appendUnique(mapped.Google, "url_context")
		case typeName == "code_execution_20250522", typeName == "code_execution_20250825":
			if err := validateAnthropicServerTool(tool, "code_execution"); err != nil {
				return aistudio.Tools{}, err
			}
			mapped.Google = appendUnique(mapped.Google, "code_execution")
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
			if len(tool.Options) > 0 {
				fields := make([]string, 0, len(tool.Options))
				for field := range tool.Options {
					fields = append(fields, field)
				}
				sort.Strings(fields)
				return aistudio.Tools{}, fmt.Errorf("custom tool %q has unsupported option %q", tool.Name, fields[0])
			}
			parameters := tool.InputSchema
			if len(parameters) == 0 {
				parameters = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			mapped.Functions = append(mapped.Functions, aistudio.FunctionDeclaration{
				Name: tool.Name, Description: tool.Description, Parameters: parameters,
			})
		default:
			return aistudio.Tools{}, fmt.Errorf("unsupported tool type %q", tool.Type)
		}
	}
	config, err := anthropicToolChoice(choice)
	if err != nil {
		return aistudio.Tools{}, err
	}
	if len(mapped.Functions) == 0 && len(mapped.Google) == 0 {
		return mapped, nil
	}
	mapped.ToolConfig = config
	return mapped, nil
}

func validateAnthropicServerTool(tool anthropicTool, name string) error {
	if tool.Name != name {
		return fmt.Errorf("tool type %q requires name %q", tool.Type, name)
	}
	if tool.Description != "" || rawJSONConfigured(tool.InputSchema) {
		return fmt.Errorf("tool type %q does not accept description or input_schema", tool.Type)
	}
	if len(tool.Options) == 0 {
		return nil
	}
	fields := make([]string, 0, len(tool.Options))
	for field := range tool.Options {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fmt.Errorf("tool type %q has unsupported option %q", tool.Type, fields[0])
}

func anthropicToolChoice(raw json.RawMessage) (aistudio.ToolConfig, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return aistudio.ToolConfig{Mode: "auto"}, nil
	}
	var choice struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &choice); err != nil {
		return aistudio.ToolConfig{}, fmt.Errorf("invalid tool_choice: %w", err)
	}
	switch choice.Type {
	case "auto":
		return aistudio.ToolConfig{Mode: "auto"}, nil
	case "none":
		return aistudio.ToolConfig{Mode: "none"}, nil
	case "any":
		return aistudio.ToolConfig{}, fmt.Errorf("tool_choice any is not supported by AI Studio Web")
	case "tool":
		return aistudio.ToolConfig{}, fmt.Errorf("named tool_choice is not supported by AI Studio Web")
	default:
		return aistudio.ToolConfig{}, fmt.Errorf("unsupported tool_choice type %q", choice.Type)
	}
}

func buildAnthropicResponse(id string, model string, result generationResult) map[string]any {
	stopReason, stopSequence := anthropicStop(result.finishReason, len(result.toolCalls) > 0, result.stopSequence)
	response := map[string]any{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       anthropicBlocks(result),
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

func writeAnthropicModels(w http.ResponseWriter, models []aistudio.Model) {
	data := make([]map[string]any, 0, len(models))
	for _, model := range models {
		data = append(data, map[string]any{
			"id": model.ID, "type": "model", "display_name": model.Name, "created_at": "1970-01-01T00:00:00Z",
		})
	}
	response := map[string]any{"data": data, "has_more": false, "first_id": nil, "last_id": nil}
	if len(models) > 0 {
		response["first_id"] = models[0].ID
		response["last_id"] = models[len(models)-1].ID
	}
	writeJSON(w, http.StatusOK, response)
}

type anthropicStreamWriter struct {
	w                 http.ResponseWriter
	id                string
	model             string
	inputTokens       int64
	blockIndex        int
	currentBlock      string
	thinkingSignature string
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
		if event.ThoughtSignature == "" {
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
	usage := map[string]int64{"output_tokens": 0}
	if result.usage != nil {
		usage["input_tokens"] = inputTokens(result.usage)
		usage["output_tokens"] = outputTokens(result.usage)
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
