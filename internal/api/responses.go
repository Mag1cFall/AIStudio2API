package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type responsesRequest struct {
	Model              string            `json:"model"`
	Input              json.RawMessage   `json:"input"`
	Instructions       string            `json:"instructions"`
	Stream             bool              `json:"stream"`
	Tools              []responsesTool   `json:"tools"`
	ToolChoice         json.RawMessage   `json:"tool_choice"`
	Temperature        *float64          `json:"temperature"`
	TopP               *float64          `json:"top_p"`
	MaxOutputTokens    *int64            `json:"max_output_tokens"`
	Reasoning          json.RawMessage   `json:"reasoning"`
	Text               json.RawMessage   `json:"text"`
	PreviousResponseID string            `json:"previous_response_id"`
	ParallelToolCalls  *bool             `json:"parallel_tool_calls"`
	Truncation         string            `json:"truncation"`
	Metadata           map[string]string `json:"metadata"`
}

type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type responsesInputItem struct {
	Type             string          `json:"type"`
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content"`
	CallID           string          `json:"call_id"`
	Name             string          `json:"name"`
	Arguments        string          `json:"arguments"`
	Output           json.RawMessage `json:"output"`
	EncryptedContent string          `json:"encrypted_content"`
}

type responseState struct {
	ParentID           string
	Contents           []aistudio.Content
	InlineInstructions []string
}

const responseStateCapacity = 256

type responseStateStore struct {
	mu     sync.Mutex
	states map[string]responseState
	order  []string
}

func newResponseStateStore() *responseStateStore {
	return &responseStateStore{states: make(map[string]responseState, responseStateCapacity)}
}

func (store *responseStateStore) Load(id string) ([]aistudio.Content, []string, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	chain := make([]responseState, 0)
	for id != "" {
		state, exists := store.states[id]
		if !exists {
			return nil, nil, false
		}
		chain = append(chain, state)
		id = state.ParentID
	}
	contents := make([]aistudio.Content, 0)
	instructions := make([]string, 0)
	for index := len(chain) - 1; index >= 0; index-- {
		contents = append(contents, cloneResponseContents(chain[index].Contents)...)
		instructions = append(instructions, chain[index].InlineInstructions...)
	}
	return contents, instructions, true
}

func (store *responseStateStore) Store(id string, state responseState) {
	store.mu.Lock()
	defer store.mu.Unlock()
	state.Contents = cloneResponseContents(state.Contents)
	state.InlineInstructions = append([]string(nil), state.InlineInstructions...)
	store.states[id] = state
	store.order = append(store.order, id)
	for len(store.order) > responseStateCapacity {
		store.evictOldest()
	}
}

func (store *responseStateStore) evictOldest() {
	id := store.order[0]
	store.order = store.order[1:]
	parent := store.states[id]
	for childID, child := range store.states {
		if child.ParentID != id {
			continue
		}
		child.ParentID = parent.ParentID
		child.Contents = append(cloneResponseContents(parent.Contents), child.Contents...)
		child.InlineInstructions = append(append([]string(nil), parent.InlineInstructions...), child.InlineInstructions...)
		store.states[childID] = child
	}
	delete(store.states, id)
}

func cloneResponseContents(contents []aistudio.Content) []aistudio.Content {
	cloned := append([]aistudio.Content(nil), contents...)
	for index := range cloned {
		cloned[index].Parts = append([]aistudio.Part(nil), contents[index].Parts...)
	}
	return cloned
}

func (s *server) handleResponses(w http.ResponseWriter, r *http.Request) {
	var request responsesRequest
	if err := decodeJSON(r, &request); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.Model == "" || len(request.Input) == 0 {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "model and input are required")
		return
	}
	responseID := newID("resp")
	generateRequest, inlineInstructions, err := request.toGenerateRequest(responseID)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	currentContents := cloneResponseContents(generateRequest.Contents)
	currentInlineInstructions := append([]string(nil), inlineInstructions...)
	if request.PreviousResponseID != "" {
		previousContents, previousInstructions, ok := s.responseStates.Load(request.PreviousResponseID)
		if !ok {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("previous response %q was not found", request.PreviousResponseID))
			return
		}
		generateRequest.Contents = append(previousContents, generateRequest.Contents...)
		inlineInstructions = append(previousInstructions, inlineInstructions...)
		instructions := make([]string, 0, 1+len(inlineInstructions))
		if request.Instructions != "" {
			instructions = append(instructions, request.Instructions)
		}
		instructions = append(instructions, inlineInstructions...)
		generateRequest.System = strings.Join(instructions, "\n")
	}
	events, err := s.service.Generate(r.Context(), generateRequest)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		}
		return
	}
	created := time.Now().Unix()
	if request.Stream {
		s.streamResponses(w, r, request, currentContents, currentInlineInstructions, responseID, created, events)
		return
	}
	result, err := consumeEvents(r.Context(), events, nil)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		}
		return
	}
	response, err := buildResponsesObject(responseID, created, request, result)
	if err != nil {
		writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		return
	}
	s.storeResponseState(responseID, request.PreviousResponseID, currentContents, currentInlineInstructions, result)
	writeJSON(w, http.StatusOK, response)
}

func (s *server) storeResponseState(id string, parentID string, contents []aistudio.Content, inlineInstructions []string, result generationResult) {
	if output := responseHistoryOutput(result); len(output.Parts) > 0 {
		contents = append(contents, output)
	}
	s.responseStates.Store(id, responseState{
		ParentID:           parentID,
		Contents:           contents,
		InlineInstructions: append([]string(nil), inlineInstructions...),
	})
}

func responseHistoryOutput(result generationResult) aistudio.Content {
	output := aistudio.Content{Role: aistudio.RoleAssistant}
	for _, event := range result.events {
		switch event.Kind {
		case aistudio.EventText:
			if event.Text != "" {
				output.Parts = append(output.Parts, aistudio.Part{Text: event.Text, ThoughtSignature: event.ThoughtSignature})
			}
		case aistudio.EventToolCall:
			if event.ToolCall != nil {
				call := *event.ToolCall
				call.Arguments = append(json.RawMessage(nil), call.Arguments...)
				output.Parts = append(output.Parts, aistudio.Part{FunctionCall: &call, ThoughtSignature: event.ThoughtSignature})
			}
		case aistudio.EventMedia:
			if event.Media != nil && len(event.Media.Data) > 0 {
				blob := &aistudio.Blob{MIME: event.Media.MIME, Data: append([]byte(nil), event.Media.Data...)}
				output.Parts = append(output.Parts, aistudio.Part{InlineData: blob, ThoughtSignature: event.ThoughtSignature})
			}
		case aistudio.EventReasoning, aistudio.EventThoughtSignature:
			if event.ThoughtSignature != "" {
				output.Parts = append(output.Parts, aistudio.Part{ThoughtSignature: event.ThoughtSignature})
			}
		}
	}
	return output
}

func (request responsesRequest) toGenerateRequest(id string) (aistudio.GenerateRequest, []string, error) {
	contents, inlineInstructions, err := responsesContents(request.Input)
	if err != nil {
		return aistudio.GenerateRequest{}, nil, err
	}
	instructions := make([]string, 0, 1+len(inlineInstructions))
	if request.Instructions != "" {
		instructions = append(instructions, request.Instructions)
	}
	instructions = append(instructions, inlineInstructions...)
	tools, err := mapResponsesTools(request.Tools, request.ToolChoice)
	if err != nil {
		return aistudio.GenerateRequest{}, nil, err
	}
	config := aistudio.GenerationConfig{
		Temperature:     request.Temperature,
		TopP:            request.TopP,
		MaxOutputTokens: request.MaxOutputTokens,
	}
	if len(request.Reasoning) > 0 && string(request.Reasoning) != "null" {
		var reasoning struct {
			Effort string `json:"effort"`
		}
		if err := json.Unmarshal(request.Reasoning, &reasoning); err != nil {
			return aistudio.GenerateRequest{}, nil, fmt.Errorf("invalid reasoning: %w", err)
		}
		config.ReasoningEffort = reasoning.Effort
	}
	if len(request.Text) > 0 && string(request.Text) != "null" {
		var text struct {
			Format struct {
				Type   string          `json:"type"`
				Schema json.RawMessage `json:"schema"`
			} `json:"format"`
		}
		if err := json.Unmarshal(request.Text, &text); err != nil {
			return aistudio.GenerateRequest{}, nil, fmt.Errorf("invalid text config: %w", err)
		}
		switch text.Format.Type {
		case "json_object":
			config.ResponseMIMEType = "application/json"
		case "json_schema":
			config.ResponseMIMEType = "application/json"
			config.ResponseSchema = text.Format.Schema
		case "", "text":
		default:
			return aistudio.GenerateRequest{}, nil, fmt.Errorf("unsupported text format %q", text.Format.Type)
		}
	}
	return aistudio.GenerateRequest{
		ID:       id,
		Model:    request.Model,
		System:   strings.Join(instructions, "\n"),
		Contents: contents,
		Config:   config,
		Tools:    tools,
	}, inlineInstructions, nil
}

func responsesContents(raw json.RawMessage) ([]aistudio.Content, []string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: text}}}}, nil, nil
	}
	var items []responsesInputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, nil, fmt.Errorf("input must be a string or item array")
	}
	contents := make([]aistudio.Content, 0, len(items))
	var instructions []string
	pendingSignature := ""
	for _, item := range items {
		switch item.Type {
		case "", "message":
			pendingSignature = ""
			if item.Role == "system" || item.Role == "developer" {
				text, err := openAITextContent(item.Content)
				if err != nil {
					return nil, nil, fmt.Errorf("%s message: %w", item.Role, err)
				}
				if text != "" {
					instructions = append(instructions, text)
				}
				continue
			}
			role, err := openAIRole(item.Role)
			if err != nil {
				return nil, nil, err
			}
			parts, err := openAIContentParts(item.Content)
			if err != nil {
				return nil, nil, err
			}
			contents = append(contents, aistudio.Content{Role: role, Parts: parts})
		case "function_call":
			arguments := json.RawMessage(item.Arguments)
			if len(arguments) == 0 {
				arguments = json.RawMessage(`{}`)
			}
			if !json.Valid(arguments) {
				return nil, nil, fmt.Errorf("function_call arguments must be JSON")
			}
			contents = append(contents, aistudio.Content{Role: aistudio.RoleAssistant, Parts: []aistudio.Part{{FunctionCall: &aistudio.FunctionCall{
				ID: item.CallID, Name: item.Name, Arguments: arguments, ThoughtSignature: pendingSignature,
			}}}})
			pendingSignature = ""
		case "function_call_output":
			pendingSignature = ""
			output, err := normalizeFunctionResultContent(item.Output)
			if err != nil {
				return nil, nil, fmt.Errorf("function_call_output: %w", err)
			}
			contents = append(contents, aistudio.Content{Role: aistudio.RoleTool, Parts: []aistudio.Part{{FunctionResult: &aistudio.FunctionResult{
				ID: item.CallID, Content: output,
			}}}})
		case "reasoning":
			pendingSignature = item.EncryptedContent
		default:
			return nil, nil, fmt.Errorf("unsupported input item type %q", item.Type)
		}
	}
	return contents, instructions, nil
}

func mapResponsesTools(tools []responsesTool, choice json.RawMessage) (aistudio.Tools, error) {
	var mapped aistudio.Tools
	for _, tool := range tools {
		switch tool.Type {
		case "function":
			if tool.Name == "" {
				return aistudio.Tools{}, fmt.Errorf("function tool name is required")
			}
			parameters := tool.Parameters
			if len(parameters) == 0 {
				parameters = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			mapped.Functions = append(mapped.Functions, aistudio.FunctionDeclaration{
				Name: tool.Name, Description: tool.Description, Parameters: parameters,
			})
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
	config, err := openAIToolChoice(choice)
	if err != nil {
		return aistudio.Tools{}, err
	}
	if len(mapped.Functions) == 0 && len(mapped.Google) == 0 {
		return mapped, nil
	}
	mapped.ToolConfig = config
	return mapped, nil
}

func buildResponsesObject(id string, created int64, request responsesRequest, result generationResult) (map[string]any, error) {
	output := make([]any, 0, 2+len(result.toolCalls)+len(result.media))
	if result.reasoning.Len() > 0 {
		output = append(output, map[string]any{
			"id":     "rs_" + id,
			"type":   "reasoning",
			"status": "completed",
			"summary": []any{map[string]any{
				"type": "summary_text",
				"text": result.reasoning.String(),
			}},
		})
	}
	output = append(output, responseCodeInterpreterItems(id, result.events)...)
	if result.text.Len() > 0 {
		output = append(output, map[string]any{
			"id":     "msg_" + id,
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []any{map[string]any{
				"type":        "output_text",
				"text":        result.text.String(),
				"annotations": responsesCitations(result.citations),
			}},
		})
	}
	for _, call := range result.toolCalls {
		if call.ThoughtSignature != "" {
			output = append(output, responseReasoningSignature(call))
		}
		output = append(output, responseFunctionCall(call))
	}
	for index, media := range result.media {
		item, err := responseImageGenerationItem(id, index, media)
		if err != nil {
			return nil, err
		}
		output = append(output, item)
	}
	status := "completed"
	if result.finishReason == "max_tokens" {
		status = "incomplete"
	}
	response := responseShell(id, created, status, request)
	response["completed_at"] = time.Now().Unix()
	if status == "incomplete" {
		response["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	}
	response["output"] = output
	response["output_text"] = result.text.String()
	if result.providerModel != "" {
		response["provider_model"] = result.providerModel
	}
	if result.usage != nil {
		response["usage"] = responsesUsage(result.usage)
	}
	return response, nil
}

func responseShell(id string, created int64, status string, request responsesRequest) map[string]any {
	parallelToolCalls := true
	if request.ParallelToolCalls != nil {
		parallelToolCalls = *request.ParallelToolCalls
	}
	truncation := request.Truncation
	if truncation == "" {
		truncation = "disabled"
	}
	var instructions any
	if request.Instructions != "" {
		instructions = request.Instructions
	}
	text := rawJSONValue(request.Text, map[string]any{"format": map[string]string{"type": "text"}})
	reasoning := rawJSONValue(request.Reasoning, nil)
	toolChoice := rawJSONValue(request.ToolChoice, "auto")
	tools := request.Tools
	if tools == nil {
		tools = []responsesTool{}
	}
	var previousResponseID any
	if request.PreviousResponseID != "" {
		previousResponseID = request.PreviousResponseID
	}
	return map[string]any{
		"id":                   id,
		"object":               "response",
		"created_at":           created,
		"completed_at":         nil,
		"status":               status,
		"error":                nil,
		"incomplete_details":   nil,
		"instructions":         instructions,
		"metadata":             request.Metadata,
		"model":                request.Model,
		"output":               []any{},
		"output_text":          "",
		"parallel_tool_calls":  parallelToolCalls,
		"previous_response_id": previousResponseID,
		"reasoning":            reasoning,
		"temperature":          request.Temperature,
		"text":                 text,
		"tool_choice":          toolChoice,
		"tools":                tools,
		"top_p":                request.TopP,
		"truncation":           truncation,
		"max_output_tokens":    request.MaxOutputTokens,
		"usage":                nil,
	}
}

func rawJSONValue(raw json.RawMessage, defaultValue any) any {
	if len(raw) == 0 || string(raw) == "null" {
		return defaultValue
	}
	var value any
	_ = json.Unmarshal(raw, &value)
	return value
}

func responseFunctionCall(call aistudio.FunctionCall) map[string]any {
	return map[string]any{
		"id":        "fc_" + call.ID,
		"type":      "function_call",
		"status":    "completed",
		"call_id":   call.ID,
		"name":      call.Name,
		"arguments": string(call.Arguments),
	}
}

func responseReasoningSignature(call aistudio.FunctionCall) map[string]any {
	return map[string]any{
		"id": "rs_" + call.ID, "type": "reasoning", "status": "completed",
		"summary": []any{}, "encrypted_content": call.ThoughtSignature,
	}
}

func responseCodeInterpreterItems(responseID string, events []aistudio.Event) []any {
	type codeCall struct {
		code   string
		result *aistudio.CodeExecutionResult
	}
	calls := make([]codeCall, 0)
	for _, event := range events {
		switch event.Kind {
		case aistudio.EventExecutableCode:
			if event.ExecutableCode != nil {
				calls = append(calls, codeCall{code: event.ExecutableCode.Code})
			}
		case aistudio.EventCodeExecutionResult:
			if event.CodeExecutionResult != nil && len(calls) > 0 {
				result := *event.CodeExecutionResult
				calls[len(calls)-1].result = &result
			}
		}
	}
	items := make([]any, 0, len(calls))
	for index, call := range calls {
		status := "incomplete"
		var outputs any
		if call.result != nil {
			status = "completed"
			logs := call.result.Output
			if call.result.Outcome != "OUTCOME_OK" {
				status = "failed"
				if logs != "" {
					logs = "stderr:\n" + logs
				}
			}
			if logs != "" {
				outputs = []any{map[string]any{"type": "logs", "logs": logs}}
			}
		}
		items = append(items, map[string]any{
			"id": fmt.Sprintf("ci_%s_%d", responseID, index), "type": "code_interpreter_call",
			"status": status, "code": call.code, "container_id": "aistudio", "outputs": outputs,
		})
	}
	return items
}

func responseImageGenerationItem(responseID string, index int, media aistudio.Media) (map[string]any, error) {
	if !strings.HasPrefix(media.MIME, "image/") || len(media.Data) == 0 {
		return nil, fmt.Errorf("Responses API cannot represent media %q without inline image data", media.MIME)
	}
	return map[string]any{
		"id":     fmt.Sprintf("ig_%s_%d", responseID, index),
		"type":   "image_generation_call",
		"status": "completed",
		"result": base64.StdEncoding.EncodeToString(media.Data),
	}, nil
}

func responsesCitations(citations []aistudio.Citation) []map[string]any {
	output := make([]map[string]any, 0, len(citations))
	for _, citation := range citations {
		output = append(output, map[string]any{
			"type":        "url_citation",
			"start_index": citation.Start,
			"end_index":   citation.End,
			"title":       citation.Title,
			"url":         citation.URL,
		})
	}
	return output
}

func responsesUsage(usage *aistudio.Usage) map[string]any {
	return map[string]any{
		"input_tokens":  inputTokens(usage),
		"output_tokens": outputTokens(usage),
		"total_tokens":  usage.TotalTokens,
		"output_tokens_details": map[string]any{
			"reasoning_tokens": usage.ReasoningTokens,
		},
	}
}

type responsesStreamWriter struct {
	w             http.ResponseWriter
	sequence      int
	id            string
	created       int64
	request       responsesRequest
	indexes       map[string]int
	reasoningOpen bool
	textOpen      bool
	mediaCount    int
	codeCount     int
	pendingCode   *responsesPendingCode
}

type responsesPendingCode struct {
	id    string
	index int
	code  string
}

func (s *server) streamResponses(w http.ResponseWriter, r *http.Request, request responsesRequest, contents []aistudio.Content, inlineInstructions []string, id string, created int64, events <-chan aistudio.Event) {
	streamHeaders(w)
	writer := &responsesStreamWriter{w: w, id: id, created: created, request: request, indexes: make(map[string]int)}
	if err := writer.emit("response.created", map[string]any{"response": responseShell(id, created, "in_progress", request)}); err != nil {
		return
	}
	if err := writer.emit("response.in_progress", map[string]any{"response": responseShell(id, created, "in_progress", request)}); err != nil {
		return
	}
	result, err := consumeEvents(r.Context(), events, writer.live)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			_ = writer.failed(err)
		}
		return
	}
	response, err := buildResponsesObject(id, created, request, result)
	if err != nil {
		_ = writer.failed(err)
		return
	}
	s.storeResponseState(id, request.PreviousResponseID, contents, inlineInstructions, result)
	if err := writer.finish(result, response); err != nil {
		_ = writer.failed(err)
	}
}

func (writer *responsesStreamWriter) emit(eventType string, payload map[string]any) error {
	payload["type"] = eventType
	payload["sequence_number"] = writer.sequence
	writer.sequence++
	return writeSSE(writer.w, eventType, payload)
}

func (writer *responsesStreamWriter) live(event aistudio.Event) error {
	switch event.Kind {
	case aistudio.EventReasoning:
		index, err := writer.ensureReasoning()
		if err != nil {
			return err
		}
		return writer.emit("response.reasoning_summary_text.delta", map[string]any{
			"item_id": "rs_" + writer.id, "output_index": index, "summary_index": 0, "delta": event.Text,
		})
	case aistudio.EventText:
		index, err := writer.ensureText()
		if err != nil {
			return err
		}
		return writer.emit("response.output_text.delta", map[string]any{
			"item_id": "msg_" + writer.id, "output_index": index, "content_index": 0, "delta": event.Text, "logprobs": []any{},
		})
	case aistudio.EventToolCall:
		if event.ToolCall != nil {
			return writer.emitToolCall(*event.ToolCall)
		}
	case aistudio.EventMedia:
		if event.Media != nil {
			return writer.emitMedia(*event.Media)
		}
	case aistudio.EventExecutableCode:
		if event.ExecutableCode != nil {
			return writer.emitExecutableCode(*event.ExecutableCode)
		}
	case aistudio.EventCodeExecutionResult:
		if event.CodeExecutionResult != nil {
			return writer.emitCodeExecutionResult(*event.CodeExecutionResult)
		}
	}
	return nil
}

func (writer *responsesStreamWriter) ensureReasoning() (int, error) {
	id := "rs_" + writer.id
	if index, ok := writer.indexes[id]; ok {
		return index, nil
	}
	index := len(writer.indexes)
	writer.indexes[id] = index
	item := map[string]any{"id": id, "type": "reasoning", "status": "in_progress", "summary": []any{}}
	if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": item}); err != nil {
		return 0, err
	}
	if err := writer.emit("response.reasoning_summary_part.added", map[string]any{
		"item_id": id, "output_index": index, "summary_index": 0,
		"part": map[string]any{"type": "summary_text", "text": ""},
	}); err != nil {
		return 0, err
	}
	writer.reasoningOpen = true
	return index, nil
}

func (writer *responsesStreamWriter) ensureText() (int, error) {
	id := "msg_" + writer.id
	if index, ok := writer.indexes[id]; ok {
		return index, nil
	}
	index := len(writer.indexes)
	writer.indexes[id] = index
	item := map[string]any{"id": id, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}
	if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": item}); err != nil {
		return 0, err
	}
	if err := writer.emit("response.content_part.added", map[string]any{
		"item_id": id, "output_index": index, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
	}); err != nil {
		return 0, err
	}
	writer.textOpen = true
	return index, nil
}

func (writer *responsesStreamWriter) emitToolCall(call aistudio.FunctionCall) error {
	if call.ThoughtSignature != "" {
		id := "rs_" + call.ID
		index := len(writer.indexes)
		writer.indexes[id] = index
		item := map[string]any{"id": id, "type": "reasoning", "status": "in_progress", "summary": []any{}}
		if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": item}); err != nil {
			return err
		}
		if err := writer.emit("response.output_item.done", map[string]any{
			"output_index": index, "item": responseReasoningSignature(call),
		}); err != nil {
			return err
		}
	}
	id := "fc_" + call.ID
	index := len(writer.indexes)
	writer.indexes[id] = index
	item := map[string]any{
		"id": id, "type": "function_call", "status": "in_progress",
		"call_id": call.ID, "name": call.Name, "arguments": "",
	}
	if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": item}); err != nil {
		return err
	}
	arguments := string(call.Arguments)
	if err := writer.emit("response.function_call_arguments.delta", map[string]any{
		"item_id": id, "output_index": index, "delta": arguments,
	}); err != nil {
		return err
	}
	if err := writer.emit("response.function_call_arguments.done", map[string]any{
		"item_id": id, "output_index": index, "arguments": arguments, "name": call.Name,
	}); err != nil {
		return err
	}
	return writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": responseFunctionCall(call)})
}

func (writer *responsesStreamWriter) emitMedia(media aistudio.Media) error {
	completed, err := responseImageGenerationItem(writer.id, writer.mediaCount, media)
	if err != nil {
		return err
	}
	writer.mediaCount++
	id := completed["id"].(string)
	index := len(writer.indexes)
	writer.indexes[id] = index
	inProgress := map[string]any{"id": id, "type": "image_generation_call", "status": "in_progress", "result": nil}
	if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": inProgress}); err != nil {
		return err
	}
	if err := writer.emit("response.image_generation_call.in_progress", map[string]any{"item_id": id, "output_index": index}); err != nil {
		return err
	}
	if err := writer.emit("response.image_generation_call.completed", map[string]any{"item_id": id, "output_index": index}); err != nil {
		return err
	}
	return writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": completed})
}

func (writer *responsesStreamWriter) emitExecutableCode(code aistudio.ExecutableCode) error {
	id := fmt.Sprintf("ci_%s_%d", writer.id, writer.codeCount)
	writer.codeCount++
	index := len(writer.indexes)
	writer.indexes[id] = index
	writer.pendingCode = &responsesPendingCode{id: id, index: index, code: code.Code}
	item := map[string]any{
		"id": id, "type": "code_interpreter_call", "status": "in_progress",
		"code": "", "container_id": "aistudio", "outputs": nil,
	}
	if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": item}); err != nil {
		return err
	}
	if err := writer.emit("response.code_interpreter_call.in_progress", map[string]any{"item_id": id, "output_index": index}); err != nil {
		return err
	}
	if code.Code != "" {
		if err := writer.emit("response.code_interpreter_call_code.delta", map[string]any{"item_id": id, "output_index": index, "delta": code.Code}); err != nil {
			return err
		}
		if err := writer.emit("response.code_interpreter_call_code.done", map[string]any{"item_id": id, "output_index": index, "code": code.Code}); err != nil {
			return err
		}
	}
	return writer.emit("response.code_interpreter_call.interpreting", map[string]any{"item_id": id, "output_index": index})
}

func (writer *responsesStreamWriter) emitCodeExecutionResult(result aistudio.CodeExecutionResult) error {
	if writer.pendingCode == nil {
		return fmt.Errorf("code execution result arrived before executable code")
	}
	call := writer.pendingCode
	status := "completed"
	logs := result.Output
	if result.Outcome != "OUTCOME_OK" {
		status = "failed"
		if logs != "" {
			logs = "stderr:\n" + logs
		}
	}
	var outputs any
	if logs != "" {
		outputs = []any{map[string]any{"type": "logs", "logs": logs}}
	}
	item := map[string]any{
		"id": call.id, "type": "code_interpreter_call", "status": status,
		"code": call.code, "container_id": "aistudio", "outputs": outputs,
	}
	if err := writer.emit("response.code_interpreter_call.completed", map[string]any{"item_id": call.id, "output_index": call.index}); err != nil {
		return err
	}
	if err := writer.emit("response.output_item.done", map[string]any{"output_index": call.index, "item": item}); err != nil {
		return err
	}
	writer.pendingCode = nil
	return nil
}

func (writer *responsesStreamWriter) finish(result generationResult, response map[string]any) error {
	if writer.reasoningOpen {
		id := "rs_" + writer.id
		index := writer.indexes[id]
		part := map[string]any{"type": "summary_text", "text": result.reasoning.String()}
		if err := writer.emit("response.reasoning_summary_text.done", map[string]any{
			"item_id": id, "output_index": index, "summary_index": 0, "text": result.reasoning.String(),
		}); err != nil {
			return err
		}
		if err := writer.emit("response.reasoning_summary_part.done", map[string]any{
			"item_id": id, "output_index": index, "summary_index": 0, "part": part,
		}); err != nil {
			return err
		}
		item := map[string]any{"id": id, "type": "reasoning", "status": "completed", "summary": []any{part}}
		if err := writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": item}); err != nil {
			return err
		}
	}
	if writer.textOpen {
		id := "msg_" + writer.id
		index := writer.indexes[id]
		for annotationIndex, annotation := range responsesCitations(result.citations) {
			if err := writer.emit("response.output_text.annotation.added", map[string]any{
				"item_id": id, "output_index": index, "content_index": 0,
				"annotation_index": annotationIndex, "annotation": annotation,
			}); err != nil {
				return err
			}
		}
		part := map[string]any{"type": "output_text", "text": result.text.String(), "annotations": responsesCitations(result.citations)}
		if err := writer.emit("response.output_text.done", map[string]any{
			"item_id": id, "output_index": index, "content_index": 0, "text": result.text.String(), "logprobs": []any{},
		}); err != nil {
			return err
		}
		if err := writer.emit("response.content_part.done", map[string]any{
			"item_id": id, "output_index": index, "content_index": 0, "part": part,
		}); err != nil {
			return err
		}
		item := map[string]any{"id": id, "type": "message", "status": "completed", "role": "assistant", "content": []any{part}}
		if err := writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": item}); err != nil {
			return err
		}
	}
	eventType := "response.completed"
	if response["status"] == "incomplete" {
		eventType = "response.incomplete"
	}
	return writer.emit(eventType, map[string]any{"response": response})
}

func (writer *responsesStreamWriter) failed(err error) error {
	response := responseShell(writer.id, writer.created, "failed", writer.request)
	response["error"] = map[string]any{"code": openAIErrorCode(err), "message": err.Error()}
	return writer.emit("response.failed", map[string]any{"response": response})
}
