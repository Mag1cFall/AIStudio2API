package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
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
		Display      string `json:"display"`
	} `json:"thinking"`
	OutputConfig *struct {
		Effort string                 `json:"effort"`
		Format *anthropicOutputFormat `json:"format"`
	} `json:"output_config"`
	OutputFormat *anthropicOutputFormat `json:"output_format"`
}

// anthropicOutputFormat 为 output_config.format 与 beta output_format 的结构化输出格式
type anthropicOutputFormat struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema"`
}

// thinkingOmitted 判断请求是否要求思考块不带正文
func (request anthropicRequest) thinkingOmitted() bool {
	return request.Thinking != nil && request.Thinking.Display == "omitted"
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
	Signature *string         `json:"signature,omitempty"`
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
	if err == nil {
		err = inlineRemoteMedia(r.Context(), generateRequest.Contents)
	}
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	generateRequest.Unary = !request.Stream || s.responseModes().FakeStream
	if !request.Stream {
		var stopKeepalive func()
		w, stopKeepalive = s.nonstreamKeepalive(w, r)
		defer stopKeepalive()
	}
	var estimate <-chan int64
	if request.Stream {
		estimate = aistudio.StartInputTokenEstimate(generateRequest)
	}
	events, err := s.service.Generate(r.Context(), generateRequest)
	if err == nil && request.Stream {
		events, err = awaitStreamStart(r.Context(), events)
	}
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeAnthropicRequestError(w, err)
		}
		return
	}
	if request.Stream {
		if err := streamHeaders(w); err != nil {
			return
		}
		writer := &anthropicStreamWriter{
			send: sseSender(w), id: messageID, model: request.Model,
			inputTokens: <-estimate,
			searchKey:   s.config.APIKey, omitted: request.thinkingOmitted(),
			media: s.mediaOutputFor(r),
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
			writeAnthropicRequestError(w, err)
		}
		return
	}
	media := s.mediaOutputFor(r)
	if err := media.saveEvents(result.events); err != nil {
		writeAnthropicRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, officialOrder("anthropic", buildAnthropicResponse(messageID, request.Model, result, s.config.APIKey, request.thinkingOmitted(), media)))
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
	if err == nil {
		err = inlineRemoteMedia(r.Context(), generateRequest.Contents)
	}
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
			writeAnthropicRequestError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, officialOrder("anthropic", map[string]int64{"input_tokens": count.InputTokens}))
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
	var reminders []aistudio.Part
	declared := request.Tools
	for _, message := range request.Messages {
		if message.Role == "system" {
			var text string
			text, declared, err = anthropicSystemMessage(message.Content, declared)
			if err != nil {
				return aistudio.GenerateRequest{}, fmt.Errorf("system message: %w", err)
			}
			if strings.TrimSpace(text) != "" {
				reminders = addSystemReminder(contents, reminders, text)
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
		contents, reminders = appendWithReminders(contents, aistudio.Content{Role: role, Parts: parts}, reminders)
	}
	if len(reminders) > 0 {
		contents = append(contents, aistudio.Content{Role: aistudio.RoleUser, Parts: reminders})
	}
	tools, err := mapAnthropicTools(declared, request.ToolChoice)
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
	if request.Thinking == nil || request.Thinking.Type == "disabled" {
		config.HideThinking = true
	}
	if request.Thinking != nil && request.Thinking.Type == "disabled" {
		config.ReasoningEffort = "none"
	}
	format := request.OutputFormat
	if request.OutputConfig != nil {
		config.ReasoningEffort = request.OutputConfig.Effort
		if request.OutputConfig.Format != nil {
			format = request.OutputConfig.Format
		}
	}
	if format != nil {
		if format.Type != "json_schema" {
			return aistudio.GenerateRequest{}, fmt.Errorf("unsupported output format type %q", format.Type)
		}
		config.ResponseMIMEType, config.ResponseJSONSchema = "application/json", format.Schema
	}
	return aistudio.GenerateRequest{
		ID: id, Model: request.Model, System: systemSegments(system), Contents: contents, Config: config, Tools: tools,
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

// anthropicSystemMessage 读取对话中途 system 消息的文本，并按其中的 tool_addition 与 tool_removal 更新声明的工具
func anthropicSystemMessage(raw json.RawMessage, tools []anthropicTool) (string, []anthropicTool, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, tools, nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
		Tool struct {
			Type       string        `json:"type"`
			Name       string        `json:"name"`
			Definition anthropicTool `json:"definition"`
		} `json:"tool"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", nil, fmt.Errorf("content must be a string or block array")
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				texts = append(texts, block.Text)
			}
		case "tool_addition":
			if block.Tool.Type == "tool_definition" {
				tools = append(slices.Clip(tools), block.Tool.Definition)
			}
		case "tool_removal":
			tools = slices.DeleteFunc(slices.Clone(tools), func(tool anthropicTool) bool { return tool.Name == block.Tool.Name })
		default:
			return "", nil, fmt.Errorf("unsupported system block type %q", block.Type)
		}
	}
	return strings.Join(texts, "\n"), tools, nil
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
				parts = append(parts, aistudio.Part{InlineData: &aistudio.Blob{MIME: mediaType, Data: data}, DisplayName: block.Title})
			case "url":
				if media, ok := aistudio.ExternalMediaForURL(source.URL); ok {
					parts = append(parts, aistudio.Part{ExternalMedia: media, DisplayName: block.Title})
				} else {
					if strings.TrimSpace(source.URL) == "" {
						return nil, fmt.Errorf("%s source URL is required", block.Type)
					}
					parts = append(parts, aistudio.Part{File: &aistudio.FileRef{ID: strings.TrimSpace(source.URL), MIME: source.MediaType}})
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
			content, media, err := functionResultParts(block.Content, block.IsError, anthropicParts)
			if err != nil {
				return nil, fmt.Errorf("tool_result: %w", err)
			}
			parts = append(parts, aistudio.Part{FunctionResult: &aistudio.FunctionResult{
				ID: block.ToolUseID, Content: content,
			}})
			parts = append(parts, media...)
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
		case "server_tool_use", "mcp_tool_use":
			if block.Type == "server_tool_use" && block.Name == "code_execution" {
				var input struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal(block.Input, &input); err != nil {
					return nil, fmt.Errorf("server_tool_use input: %w", err)
				}
				parts = append(parts, aistudio.Part{ExecutableCode: &aistudio.ExecutableCode{
					ID: strings.TrimPrefix(block.ID, "srvtoolu_"), Language: "PYTHON", Code: input.Code,
				}, ThoughtSignature: pendingSignature})
				pendingSignature = ""
				continue
			}
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
		case "code_execution_tool_result":
			var content struct {
				Type       string `json:"type"`
				Stdout     string `json:"stdout"`
				Stderr     string `json:"stderr"`
				ReturnCode int    `json:"return_code"`
			}
			if err := json.Unmarshal(block.Content, &content); err != nil {
				return nil, fmt.Errorf("code_execution_tool_result content: %w", err)
			}
			result := &aistudio.CodeExecutionResult{ID: strings.TrimPrefix(block.ToolUseID, "srvtoolu_"), Outcome: "OUTCOME_OK", Output: content.Stdout}
			switch {
			case content.Type == "code_execution_tool_result_error":
				result.Outcome, result.Output = "OUTCOME_DEADLINE_EXCEEDED", ""
			case content.ReturnCode != 0:
				result.Outcome, result.Output = "OUTCOME_FAILED", content.Stderr+content.Stdout
			}
			parts = append(parts, aistudio.Part{CodeExecutionResult: result, ThoughtSignature: pendingSignature})
			pendingSignature = ""
		case "bash_code_execution_tool_result", "text_editor_code_execution_tool_result", "tool_search_tool_result", "advisor_tool_result", "mcp_tool_result":
			flushPendingSignature()
			parts = append(parts, aistudio.Part{Text: "Server tool result " + block.Type + ": " + string(block.Content)})
		case "compaction":
			flushPendingSignature()
			var summary string
			_ = json.Unmarshal(block.Content, &summary)
			if summary != "" {
				parts = append(parts, aistudio.Part{Text: summary})
			}
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
			search, err := mapSearchOptions("", tool.Options["user_location"], nil)
			if err != nil {
				return aistudio.Tools{}, err
			}
			if search.AllowedDomains, err = anthropicDomains(tool.Options["allowed_domains"]); err != nil {
				return aistudio.Tools{}, fmt.Errorf("web_search allowed_domains: %w", err)
			}
			if search.BlockedDomains, err = anthropicDomains(tool.Options["blocked_domains"]); err != nil {
				return aistudio.Tools{}, fmt.Errorf("web_search blocked_domains: %w", err)
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
		case strings.HasPrefix(typeName, "tool_search_tool_"), strings.HasPrefix(typeName, "advisor_"), strings.Contains(typeName, "toolset"):
		case strings.HasPrefix(typeName, "bash_"), strings.HasPrefix(typeName, "text_editor_"), strings.HasPrefix(typeName, "memory_"), strings.HasPrefix(typeName, "computer_"):
			declaration, err := anthropicClientTool(tool, typeName)
			if err != nil {
				return aistudio.Tools{}, err
			}
			mapped.Functions = append(mapped.Functions, declaration)
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

func validateAnthropicServerTool(tool anthropicTool, name string) error {
	if tool.Name != name {
		return fmt.Errorf("tool type %q requires name %q", tool.Type, name)
	}
	if tool.Description != "" || rawJSONConfigured(tool.InputSchema) {
		return fmt.Errorf("tool type %q does not accept description or input_schema", tool.Type)
	}
	return nil
}

// anthropicDomains 读取服务端工具的域名列表选项，省略或 null 时为空
func anthropicDomains(raw json.RawMessage) ([]string, error) {
	if !rawJSONConfigured(raw) {
		return nil, nil
	}
	var domains []string
	if err := json.Unmarshal(raw, &domains); err != nil {
		return nil, fmt.Errorf("must be an array of strings")
	}
	return domains, nil
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
	case strings.HasPrefix(typeName, "computer_"):
		var width, height int64
		if json.Unmarshal(tool.Options["display_width_px"], &width) != nil || json.Unmarshal(tool.Options["display_height_px"], &height) != nil {
			return aistudio.FunctionDeclaration{}, fmt.Errorf("tool type %q requires integer display_width_px and display_height_px", tool.Type)
		}
		var zoom bool
		_ = json.Unmarshal(tool.Options["enable_zoom"], &zoom)
		declaration.Description, declaration.Parameters = anthropicComputerTool(typeName, width, height, zoom)
	default:
		declaration.Description = "Read and write persistent memory files in the /memories directory. view reads a file or lists a directory, create writes file_text, str_replace replaces old_str with new_str, insert adds insert_text after insert_line, delete removes path, rename moves old_path to new_path."
		declaration.Parameters = json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","enum":["view","create","str_replace","insert","delete","rename"]},"path":{"type":"string"},"file_text":{"type":"string"},"old_str":{"type":"string"},"new_str":{"type":"string"},"insert_line":{"type":"integer"},"insert_text":{"type":"string"},"view_range":{"type":"array","items":{"type":"integer"}},"old_path":{"type":"string"},"new_path":{"type":"string"}},"required":["command"]}`)
	}
	return declaration, nil
}

// anthropicComputerTool 返回 computer 工具的函数说明与参数 Schema：20241022 为十个基础动作，之后的版本加点击坐标与修饰键、滚动、按住与等待，enable_zoom 时加 zoom
func anthropicComputerTool(typeName string, width, height int64, zoom bool) (string, json.RawMessage) {
	actions := []string{"key", "type", "mouse_move", "left_click", "left_click_drag", "right_click", "middle_click", "double_click", "screenshot", "cursor_position"}
	point := map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "minItems": 2, "maxItems": 2}
	properties := map[string]any{"coordinate": point, "start_coordinate": point, "text": map[string]any{"type": "string"}}
	description := fmt.Sprintf("Use a mouse and keyboard to interact with a computer and take screenshots. The screen is %dx%d pixels; coordinate and start_coordinate are [x, y] pixel positions from the top-left corner. screenshot captures the screen, cursor_position reports the cursor position, mouse_move moves the cursor to coordinate, left_click_drag drags from start_coordinate to coordinate, type types text, key presses the key or key combination in text such as Return or ctrl+s.", width, height)
	if typeName == "computer_20241022" {
		description += " left_click, right_click, middle_click and double_click click at the current cursor position."
	} else {
		actions = append(actions, "triple_click", "left_mouse_down", "left_mouse_up", "scroll", "hold_key", "wait")
		properties["scroll_direction"] = map[string]any{"type": "string", "enum": []string{"up", "down", "left", "right"}}
		properties["scroll_amount"] = map[string]any{"type": "integer"}
		properties["duration"] = map[string]any{"type": "number"}
		properties["key"] = map[string]any{"type": "string"}
		description += " left_click, right_click, middle_click, double_click and triple_click click at coordinate or at the current cursor position while holding the modifier keys in key, left_mouse_down and left_mouse_up press and release the left button at the cursor, scroll turns the wheel scroll_amount clicks in scroll_direction at coordinate while holding the modifier keys in text, hold_key holds the key in text for duration seconds, wait pauses for duration seconds."
	}
	if zoom {
		actions = append(actions, "zoom")
		properties["region"] = map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "minItems": 4, "maxItems": 4}
		description += " zoom shows region [x0, y0, x1, y1] of the screen at full resolution."
	}
	properties["action"] = map[string]any{"type": "string", "enum": actions}
	parameters, _ := json.Marshal(map[string]any{"type": "object", "properties": properties, "required": []string{"action"}})
	return description, parameters
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

// buildAnthropicResponse 构造非流式 Message，内容块由流式状态机生成，omitted 时思考块不带正文
func buildAnthropicResponse(id string, model string, result generationResult, searchKey string, omitted bool, media mediaOutput) map[string]any {
	stopReason, stopSequence := anthropicStop(result.finishReason, len(result.toolCalls) > 0, result.stopSequence)
	blocks, searchCount := anthropicBlocks(result, omitted, media, searchKey)
	response := map[string]any{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       blocks,
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

// anthropicBlocks 用流式状态机生成非流式 Message 的内容块，返回内容块与搜索次数
func anthropicBlocks(result generationResult, omitted bool, media mediaOutput, searchKey string) ([]map[string]any, int) {
	var collected anthropicBlockCollector
	writer := &anthropicStreamWriter{send: collected.collect, searchKey: searchKey, omitted: omitted, media: media}
	for _, event := range result.events {
		_ = writer.live(event)
	}
	searchCount, _ := writer.finishContent(&result)
	return collected.blocks, searchCount
}

// sseSender 返回把流式事件写成 SSE 的发送函数
func sseSender(w http.ResponseWriter) func(string, any) error {
	return func(eventType string, payload any) error {
		return writeSSE(w, eventType, officialOrder("anthropic", payload))
	}
}

// anthropicBlockCollector 把流式内容块事件累积为非流式 Message 的内容块
type anthropicBlockCollector struct {
	blocks []map[string]any
	inputs map[int]string
}

// collect 按 content_block_start、content_block_delta 与 content_block_stop 更新内容块
func (collector *anthropicBlockCollector) collect(eventType string, payload any) error {
	event, _ := payload.(map[string]any)
	index, _ := event["index"].(int)
	switch eventType {
	case "content_block_start":
		encoded, err := json.Marshal(event["content_block"])
		if err != nil {
			return err
		}
		var block map[string]any
		if err := json.Unmarshal(encoded, &block); err != nil {
			return err
		}
		collector.blocks = append(collector.blocks, block)
	case "content_block_delta":
		block := collector.blocks[index]
		delta, _ := event["delta"].(map[string]any)
		switch delta["type"] {
		case "text_delta":
			block["text"] = block["text"].(string) + delta["text"].(string)
		case "thinking_delta":
			block["thinking"] = block["thinking"].(string) + delta["thinking"].(string)
		case "signature_delta":
			block["signature"] = delta["signature"]
		case "input_json_delta":
			if collector.inputs == nil {
				collector.inputs = map[int]string{}
			}
			collector.inputs[index] += delta["partial_json"].(string)
		}
	case "content_block_stop":
		if input, ok := collector.inputs[index]; ok && input != "" {
			collector.blocks[index]["input"] = json.RawMessage(input)
		}
	}
	return nil
}

func anthropicStop(reason string, hasTools bool, stopSequence string) (string, *string) {
	normalized := strings.ToLower(strings.TrimSpace(reason))
	switch normalized {
	case "max_tokens", "max_output_tokens", "length", "continuation":
		return "max_tokens", nil
	case "stop_sequence":
		if stopSequence != "" {
			return "stop_sequence", &stopSequence
		}
		return "stop_sequence", nil
	case "pause_turn":
		return "pause_turn", nil
	default:
		if contentFilterFinishReason(normalized) {
			return "refusal", nil
		}
		if hasTools {
			return "tool_use", nil
		}
		return "end_turn", nil
	}
}

func anthropicUsage(usage *aistudio.Usage) map[string]any {
	return map[string]any{
		"input_tokens":                inputTokens(usage) - usage.CachedTokens,
		"cache_creation_input_tokens": 0,
		"cache_read_input_tokens":     usage.CachedTokens,
		"output_tokens":               outputTokens(usage),
		"output_tokens_details":       map[string]any{"thinking_tokens": usage.ReasoningTokens},
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
	writeJSON(w, http.StatusOK, officialOrder("anthropic", response))
}

// anthropicModelObject 投影列表与单模型共用的 Anthropic ModelInfo 字段，输入与输出上限取实时目录
func anthropicModelObject(model aistudio.Model) map[string]any {
	return map[string]any{
		"type": "model", "id": model.ID, "display_name": model.Name, "created_at": "1970-01-01T00:00:00Z",
		"max_input_tokens": anthropicTokenLimit(model.InputTokenLimit), "max_tokens": anthropicTokenLimit(model.OutputTokenLimit),
		"capabilities": anthropicCapabilities(model), "lifecycle": "active", "line": nil, "deprecated_at": nil, "retires_at": nil,
	}
}

// anthropicCapabilities 按实时目录投影 Anthropic ModelCapabilities，effort 只列出模型实际执行的档位
func anthropicCapabilities(model aistudio.Model) map[string]any {
	support := func(supported bool) map[string]bool { return map[string]bool{"supported": supported} }
	chat := model.Capabilities["chat_model"]
	thinking := model.Capabilities["thinking"]
	return map[string]any{
		"batch":          support(false),
		"citations":      support(false),
		"code_execution": support(model.Capabilities["code_execution"]),
		"context_management": map[string]any{
			"supported": false, "clear_thinking_20251015": support(false), "clear_tool_uses_20250919": support(false), "compact_20260112": support(false),
		},
		"effort": map[string]any{
			"supported": len(model.ThinkingLevels) > 0,
			"low":       support(slices.Contains(model.ThinkingLevels, "LOW")),
			"medium":    support(slices.Contains(model.ThinkingLevels, "MEDIUM")),
			"high":      support(slices.Contains(model.ThinkingLevels, "HIGH")),
			"xhigh":     support(false),
			"max":       support(false),
		},
		"image_input":        support(chat),
		"pdf_input":          support(chat),
		"structured_outputs": support(model.Capabilities["capability_code_6"]),
		"thinking": map[string]any{
			"supported": thinking,
			"types":     map[string]any{"enabled": support(thinking), "adaptive": support(thinking)},
		},
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
	send              func(eventType string, payload any) error
	id                string
	model             string
	inputTokens       int64
	blockIndex        int
	currentBlock      string
	thinkingSignature string
	searchKey         string
	omitted           bool
	media             mediaOutput
	codeIDs           codeExecutionIDs
}

func (s *server) streamAnthropic(r *http.Request, writer *anthropicStreamWriter, events <-chan aistudio.Event) {
	result, err := consumeStreamEvents(r.Context(), events, writer.live, func() error {
		return writer.emit("ping", map[string]string{"type": "ping"})
	})
	if err != nil {
		if shouldWriteRequestError(r, err) {
			_ = writer.error(err)
		}
		return
	}
	_ = writer.finish(result)
}

func (writer *anthropicStreamWriter) emit(eventType string, payload any) error {
	return writer.send(eventType, payload)
}

func (writer *anthropicStreamWriter) start() error {
	return writer.emit("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": writer.id, "type": "message", "role": "assistant", "model": writer.model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]int64{
				"input_tokens": writer.inputTokens, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0, "output_tokens": 0,
			},
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
		if writer.omitted || event.Text == "" {
			return nil
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
		if event.FunctionCall == nil {
			return nil
		}
		call := event.FunctionCall
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
		rendered := writer.media.markdown(event.Part)
		if rendered == "" {
			return nil
		}
		if event.InlineData != nil {
			if err := writer.media.save(*event.InlineData); err != nil {
				return err
			}
		}
		if err := writer.closeBlock(); err != nil {
			return err
		}
		if err := writer.ensureBlock("text"); err != nil {
			return err
		}
		return writer.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": writer.blockIndex,
			"delta": map[string]any{"type": "text_delta", "text": rendered},
		})
	case aistudio.EventExecutableCode, aistudio.EventCodeExecutionResult:
		if event.ThoughtSignature != "" {
			if writer.currentBlock == "thinking" {
				writer.thinkingSignature = event.ThoughtSignature
			} else if err := writer.redactedThinking(event.ThoughtSignature); err != nil {
				return err
			}
		}
		part := writer.codeIDs.assign(event.Part)
		if code := part.ExecutableCode; code != nil {
			input, err := json.Marshal(map[string]string{"code": code.Code})
			if err != nil {
				return err
			}
			return writer.toolBlock(map[string]any{"type": "server_tool_use", "id": "srvtoolu_" + code.ID, "name": "code_execution", "input": map[string]any{}}, string(input))
		}
		result := part.CodeExecutionResult
		return writer.toolBlock(map[string]any{"type": "code_execution_tool_result", "tool_use_id": "srvtoolu_" + result.ID, "content": anthropicCodeExecutionContent(*result)}, "")
	}
	return nil
}

// toolBlock 写出一个完整的工具块，input 非空时以 input_json_delta 写出参数
func (writer *anthropicStreamWriter) toolBlock(block map[string]any, input string) error {
	if err := writer.closeBlock(); err != nil {
		return err
	}
	if err := writer.emit("content_block_start", map[string]any{
		"type": "content_block_start", "index": writer.blockIndex, "content_block": block,
	}); err != nil {
		return err
	}
	writer.currentBlock = block["type"].(string)
	if input != "" {
		if err := writer.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": writer.blockIndex,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": input},
		}); err != nil {
			return err
		}
	}
	return writer.closeBlock()
}

// anthropicCodeExecutionContent 把代码执行结果投影为 code_execution_tool_result 的内容，失败输出写入 stderr，超时为执行超时错误
func anthropicCodeExecutionContent(result aistudio.CodeExecutionResult) map[string]any {
	switch result.Outcome {
	case "OUTCOME_OK":
		return map[string]any{"type": "code_execution_result", "stdout": result.Output, "stderr": "", "return_code": 0, "content": []any{}}
	case "OUTCOME_DEADLINE_EXCEEDED":
		return map[string]any{"type": "code_execution_tool_result_error", "error_code": "execution_time_exceeded"}
	}
	return map[string]any{"type": "code_execution_result", "stdout": "", "stderr": result.Output, "return_code": 1, "content": []any{}}
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
		block["signature"] = ""
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

// finishContent 追加来源与搜索工具块并关闭当前块，返回搜索次数
func (writer *anthropicStreamWriter) finishContent(result *generationResult) (int, error) {
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
			return 0, err
		}
		if err := writer.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": writer.blockIndex,
			"delta": map[string]any{"type": "text_delta", "text": prefix + sources},
		}); err != nil {
			return 0, err
		}
	}
	if err := writer.closeBlock(); err != nil {
		return 0, err
	}
	for _, block := range searchBlocks {
		if err := writer.emit("content_block_start", map[string]any{
			"type": "content_block_start", "index": writer.blockIndex, "content_block": block,
		}); err != nil {
			return 0, err
		}
		writer.currentBlock = block.Type
		if err := writer.closeBlock(); err != nil {
			return 0, err
		}
	}
	return searchCount, nil
}

func (writer *anthropicStreamWriter) finish(result generationResult) error {
	searchCount, err := writer.finishContent(&result)
	if err != nil {
		return err
	}
	usage := map[string]any{"output_tokens": 0}
	if result.usage != nil {
		usage = anthropicUsage(result.usage)
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
		"error": map[string]string{"type": anthropicErrorType(err), "message": publicErrorMessage(statusFromError(err), anthropicErrorMessage(err))},
	})
}
