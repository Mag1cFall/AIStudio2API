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
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  *string         `json:"thinking,omitempty"`
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
	messageID := newID("msg")
	generateRequest, err := request.toGenerateRequest(messageID)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	var inputTokens int64
	if request.Stream {
		count, err := s.service.CountTokens(r.Context(), aistudio.TokenCountRequest{
			Model: generateRequest.Model, System: generateRequest.System, Contents: generateRequest.Contents,
			Tools: generateRequest.Tools,
		})
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				writeAnthropicError(w, statusFromError(err), anthropicErrorType(err), err.Error())
			}
			return
		}
		inputTokens = count.InputTokens
	}
	events, err := s.service.Generate(r.Context(), generateRequest)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			writeAnthropicError(w, statusFromError(err), anthropicErrorType(err), err.Error())
		}
		return
	}
	if request.Stream {
		s.streamAnthropic(w, r, request.Model, messageID, inputTokens, events)
		return
	}
	result, err := consumeEvents(r.Context(), events, nil)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
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
		if !errors.Is(err, context.Canceled) {
			writeAnthropicError(w, statusFromError(err), anthropicErrorType(err), err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"input_tokens": count.InputTokens})
}

func (request anthropicRequest) toGenerateRequest(id string) (aistudio.GenerateRequest, error) {
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
	for _, rawBlock := range blocks {
		var block struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
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
			pendingSignature = ""
			parts = append(parts, aistudio.Part{Text: block.Text})
		case "thinking", "redacted_thinking":
			pendingSignature = block.Signature
		case "image", "document":
			pendingSignature = ""
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
					parts = append(parts, aistudio.Part{File: &aistudio.FileRef{ID: block.Source.URL, MIME: block.Source.MediaType}})
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
			pendingSignature = ""
			content, err := normalizeFunctionResultContent(block.Content)
			if err != nil {
				return nil, fmt.Errorf("tool_result: %w", err)
			}
			parts = append(parts, aistudio.Part{FunctionResult: &aistudio.FunctionResult{
				ID: block.ToolUseID, Content: content,
			}})
		default:
			return nil, fmt.Errorf("unsupported content block type %q", block.Type)
		}
	}
	return parts, nil
}

func mapAnthropicTools(tools []anthropicTool, choice json.RawMessage) (aistudio.Tools, error) {
	var mapped aistudio.Tools
	for _, tool := range tools {
		typeName := strings.ToLower(tool.Type)
		switch {
		case strings.HasPrefix(typeName, "web_search"):
			mapped.Google = appendUnique(mapped.Google, "google_search")
		case strings.HasPrefix(typeName, "web_fetch"), strings.HasPrefix(typeName, "url_context"):
			mapped.Google = appendUnique(mapped.Google, "url_context")
		case strings.HasPrefix(typeName, "code_execution"):
			mapped.Google = appendUnique(mapped.Google, "code_execution")
		case strings.HasPrefix(typeName, "google_maps"):
			mapped.Google = appendUnique(mapped.Google, "google_maps")
		case typeName == "", typeName == "custom":
			if tool.Name == "" {
				return aistudio.Tools{}, fmt.Errorf("tool name is required")
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
	response := map[string]any{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       anthropicBlocks(result),
		"stop_reason":   anthropicStopReason(result.finishReason, len(result.toolCalls) > 0),
		"stop_sequence": nil,
	}
	if result.providerModel != "" {
		response["provider_model"] = result.providerModel
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
		case aistudio.EventToolCall:
			if event.ToolCall != nil {
				if event.ToolCall.ThoughtSignature != "" {
					if len(blocks) > 0 && blocks[len(blocks)-1].Type == "thinking" {
						blocks[len(blocks)-1].Signature = event.ToolCall.ThoughtSignature
					} else {
						thinking := ""
						blocks = append(blocks, anthropicContentBlock{
							Type: "thinking", Thinking: &thinking, Signature: event.ToolCall.ThoughtSignature,
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

func anthropicStopReason(reason string, hasTools bool) string {
	if hasTools {
		return "tool_use"
	}
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "max_tokens", "max_output_tokens", "length":
		return "max_tokens"
	case "stop_sequence":
		return "stop_sequence"
	case "pause_turn":
		return "pause_turn"
	case "refusal":
		return "refusal"
	case "safety", "content_filter", "blocked", "recitation", "blocklist", "prohibited_content", "spii", "image_safety", "image_prohibited_content", "no_image", "image_recitation":
		return "refusal"
	default:
		return "end_turn"
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

func (s *server) streamAnthropic(w http.ResponseWriter, r *http.Request, model string, id string, inputTokens int64, events <-chan aistudio.Event) {
	streamHeaders(w)
	writer := &anthropicStreamWriter{w: w, id: id, model: model, inputTokens: inputTokens}
	if err := writer.start(); err != nil {
		return
	}
	result, err := consumeStreamEvents(r.Context(), events, writer.live, func() error { return writeSSEHeartbeat(w) })
	if err != nil {
		if !errors.Is(err, context.Canceled) {
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
	case aistudio.EventToolCall:
		if event.ToolCall == nil {
			return nil
		}
		call := event.ToolCall
		if call.ThoughtSignature != "" {
			if err := writer.ensureBlock("thinking"); err != nil {
				return err
			}
			writer.thinkingSignature = call.ThoughtSignature
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
	if err := writer.emit("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason": anthropicStopReason(result.finishReason, len(result.toolCalls) > 0), "stop_sequence": nil,
		},
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
