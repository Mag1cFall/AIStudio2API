package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sort"
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
	Store              *bool             `json:"store"`
	Background         bool              `json:"background"`
}

type responsesTool struct {
	Type              string          `json:"type"`
	Name              string          `json:"name,omitempty"`
	Description       string          `json:"description,omitempty"`
	Parameters        json.RawMessage `json:"parameters,omitempty"`
	SearchContextSize string          `json:"search_context_size,omitempty"`
	UserLocation      json.RawMessage `json:"user_location,omitempty"`
	Filters           json.RawMessage `json:"filters,omitempty"`
	Container         json.RawMessage `json:"container,omitempty"`
	Format            json.RawMessage `json:"format,omitempty"`
	Strict            *bool           `json:"strict,omitempty"`
	Tools             []responsesTool `json:"tools,omitempty"`
}

type responsesInputItem struct {
	Type             string          `json:"type"`
	ID               string          `json:"id"`
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content"`
	CallID           string          `json:"call_id"`
	Name             string          `json:"name"`
	Namespace        string          `json:"namespace"`
	Arguments        string          `json:"arguments"`
	Input            string          `json:"input"`
	Action           json.RawMessage `json:"action"`
	Operation        json.RawMessage `json:"operation"`
	Status           string          `json:"status"`
	Output           json.RawMessage `json:"output"`
	EncryptedContent string          `json:"encrypted_content"`
}

type responseState struct {
	ParentID           string
	Contents           []aistudio.Content
	InlineInstructions []string
	Record             *responseRecord
	Interaction        *interactionRecord
}

// responseRecord 是 Responses 接口保存的响应对象、输入项与输出项
type responseRecord struct {
	Model      string
	Shell      json.RawMessage
	Input      []storedItem
	Output     []storedItem
	Background bool
}

// storedItem 是带 ID 的 Responses 输入或输出项
type storedItem struct {
	ID  string
	Raw json.RawMessage
}

const responseStateCapacity = 256

// responseHistory 是续接起点的响应 ID 及其按顺序展开的完整上下文
type responseHistory struct {
	ID                 string
	Model              string
	Interaction        bool
	Contents           []aistudio.Content
	InlineInstructions []string
	Items              []storedItem
}

type responseStateStore struct {
	mu     sync.Mutex
	states map[string]responseState
	order  []string
}

func newResponseStateStore() *responseStateStore {
	return &responseStateStore{states: make(map[string]responseState, responseStateCapacity)}
}

func (store *responseStateStore) Load(id string) (responseHistory, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	history := responseHistory{ID: id, Contents: make([]aistudio.Content, 0), InlineInstructions: make([]string, 0)}
	if state, exists := store.states[id]; exists {
		history.Interaction = state.Interaction != nil
		if state.Record != nil {
			history.Model = state.Record.Model
		}
	}
	chain := make([]responseState, 0)
	for id != "" {
		state, exists := store.states[id]
		if !exists {
			return responseHistory{}, false
		}
		chain = append(chain, state)
		id = state.ParentID
	}
	for index := len(chain) - 1; index >= 0; index-- {
		history.Contents = append(history.Contents, cloneResponseContents(chain[index].Contents)...)
		history.InlineInstructions = append(history.InlineInstructions, chain[index].InlineInstructions...)
		if record := chain[index].Record; record != nil {
			history.Items = append(append(history.Items, record.Input...), record.Output...)
		}
	}
	return history, true
}

// Store 保存本次输入与生成输出组成的续接节点；续接起点在生成期间已被淘汰时，把请求开始时读取的完整上下文并入该节点
func (store *responseStateStore) Store(id string, previous responseHistory, result generationResult, state responseState) {
	state.ParentID = previous.ID
	state.Contents = cloneResponseContents(state.Contents)
	if output := responseHistoryOutput(result); len(output.Parts) > 0 {
		state.Contents = append(state.Contents, output)
	}
	state.InlineInstructions = append([]string(nil), state.InlineInstructions...)
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.states[previous.ID]; previous.ID != "" && !exists {
		state.ParentID = ""
		state.Contents = append(cloneResponseContents(previous.Contents), state.Contents...)
		state.InlineInstructions = append(append([]string(nil), previous.InlineInstructions...), state.InlineInstructions...)
		if state.Record != nil {
			state.Record.Input = append(append([]storedItem(nil), previous.Items...), state.Record.Input...)
		}
	}
	store.states[id] = state
	store.order = append(store.order, id)
	for len(store.order) > responseStateCapacity {
		id := store.order[0]
		store.order = store.order[1:]
		store.removeLocked(id)
	}
}

// removeLocked 删除一个节点，并把它的上下文并入以它为续接起点的子节点
func (store *responseStateStore) removeLocked(id string) {
	parent := store.states[id]
	for childID, child := range store.states {
		if child.ParentID != id {
			continue
		}
		child.ParentID = parent.ParentID
		child.Contents = append(cloneResponseContents(parent.Contents), child.Contents...)
		child.InlineInstructions = append(append([]string(nil), parent.InlineInstructions...), child.InlineInstructions...)
		if child.Record != nil && parent.Record != nil {
			merged := *child.Record
			merged.Input = append(append(append([]storedItem(nil), parent.Record.Input...), parent.Record.Output...), child.Record.Input...)
			child.Record = &merged
		}
		store.states[childID] = child
	}
	delete(store.states, id)
}

// Response 返回 Responses 接口保存的完整响应对象及其是否以后台模式创建
func (store *responseStateStore) Response(id string) (json.RawMessage, bool, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	state, exists := store.states[id]
	if !exists || state.Record == nil {
		return nil, false, false
	}
	var shell map[string]json.RawMessage
	_ = json.Unmarshal(state.Record.Shell, &shell)
	output := make([]json.RawMessage, 0, len(state.Record.Output))
	for _, item := range state.Record.Output {
		output = append(output, item.Raw)
	}
	shell["output"], _ = json.Marshal(output)
	encoded, _ := json.Marshal(shell)
	return encoded, state.Record.Background, true
}

// Interaction 返回 Interactions 接口保存的交互资源
func (store *responseStateStore) Interaction(id string) (*interactionRecord, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	interaction := store.states[id].Interaction
	return interaction, interaction != nil
}

// InputItems 返回生成该响应时的全部输入项：续接链上各前序响应的输入与输出，以及本次输入
func (store *responseStateStore) InputItems(id string) ([]storedItem, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	state, exists := store.states[id]
	if !exists || state.Record == nil {
		return nil, false
	}
	items := append([]storedItem(nil), state.Record.Input...)
	for parentID := state.ParentID; parentID != ""; {
		parent, exists := store.states[parentID]
		if !exists {
			break
		}
		if parent.Record != nil {
			items = append(append(append([]storedItem(nil), parent.Record.Input...), parent.Record.Output...), items...)
		}
		parentID = parent.ParentID
	}
	return items, true
}

// Delete 删除 Responses 接口保存的响应，interaction 为 true 时删除 Interactions 接口保存的交互，以它为起点的续接保留完整上下文
func (store *responseStateStore) Delete(id string, interaction bool) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	if state, exists := store.states[id]; !exists || (state.Interaction != nil) != interaction {
		return false
	}
	store.removeLocked(id)
	store.order = slices.DeleteFunc(store.order, func(value string) bool { return value == id })
	return true
}

// ResolveItems 将输入中的 item_reference 替换为已保存的同 ID 输入或输出项
func (store *responseStateStore) ResolveItems(raw json.RawMessage) (json.RawMessage, error) {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return raw, nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	resolved := false
	for index, item := range items {
		var head struct {
			Type string `json:"type"`
			Role string `json:"role"`
			ID   string `json:"id"`
		}
		if json.Unmarshal(item, &head) != nil || head.Type != "item_reference" && (head.Type != "" || head.Role != "" || head.ID == "") {
			continue
		}
		found := false
		for _, state := range store.states {
			if state.Record == nil {
				continue
			}
			for _, stored := range slices.Concat(state.Record.Input, state.Record.Output) {
				if stored.ID == head.ID {
					items[index], found = stored.Raw, true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("item %q was not found", head.ID)
		}
		resolved = true
	}
	if !resolved {
		return raw, nil
	}
	return json.Marshal(items)
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
	if request.Model == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "model is required")
		return
	}
	responseID := newID("resp")
	prepared, err := s.prepareResponses(r.Context(), &request, responseID)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		}
		return
	}
	generateRequest := prepared.generate
	generateRequest.Unary = !request.Stream
	events, err := s.service.Generate(r.Context(), generateRequest)
	if err == nil && request.Stream {
		events, err = awaitStreamStart(r.Context(), events)
	}
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		}
		return
	}
	created := time.Now().Unix()
	if request.Stream {
		s.streamResponses(w, r, request, prepared, responseID, created, events)
		return
	}
	result, err := consumeEvents(r.Context(), events, nil)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		}
		return
	}
	response, err := buildResponsesObject(responseID, created, request, result)
	if err != nil {
		writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		return
	}
	if request.storesResponse() {
		s.storeResponse(responseID, request, prepared, result, response)
	}
	writeJSON(w, http.StatusOK, response)
}

// responsesPreparation 是转换后的生成请求，以及保存续接节点所需的前序上下文与本次输入
type responsesPreparation struct {
	generate           aistudio.GenerateRequest
	previous           responseHistory
	contents           []aistudio.Content
	inlineInstructions []string
	input              []storedItem
}

// prepareResponses 展开 item_reference、转换请求、下载外部媒体并接上 previous_response_id 的上下文
func (s *server) prepareResponses(ctx context.Context, request *responsesRequest, id string) (responsesPreparation, error) {
	input, err := s.responseStates.ResolveItems(request.Input)
	if err != nil {
		return responsesPreparation{}, err
	}
	request.Input = input
	generateRequest, inlineInstructions, err := request.toGenerateRequest(id)
	if err != nil {
		return responsesPreparation{}, err
	}
	if err := inlineRemoteMedia(ctx, generateRequest.Contents); err != nil {
		return responsesPreparation{}, err
	}
	prepared := responsesPreparation{
		generate: generateRequest, contents: cloneResponseContents(generateRequest.Contents),
		inlineInstructions: append([]string(nil), inlineInstructions...), input: responsesInputItems(request.Input, id),
	}
	if request.PreviousResponseID == "" {
		return prepared, nil
	}
	previous, ok := s.responseStates.Load(request.PreviousResponseID)
	if !ok {
		return responsesPreparation{}, fmt.Errorf("previous response %q was not found", request.PreviousResponseID)
	}
	prepared.previous = previous
	prepared.generate.Contents = append(cloneResponseContents(previous.Contents), generateRequest.Contents...)
	inlineInstructions = append(append([]string(nil), previous.InlineInstructions...), inlineInstructions...)
	instructions := make([]string, 0, 1+len(inlineInstructions))
	if request.Instructions != "" {
		instructions = append(instructions, request.Instructions)
	}
	instructions = append(instructions, inlineInstructions...)
	prepared.generate.System = strings.Join(instructions, "\n")
	return prepared, nil
}

// storesResponse 返回响应是否保存为可续接与查询的节点，后台请求同样保存以供轮询
func (request responsesRequest) storesResponse() bool {
	return request.Store == nil || *request.Store || request.Background
}

// storeResponse 保存续接上下文与可查询的响应对象、输入项和输出项
func (s *server) storeResponse(id string, request responsesRequest, prepared responsesPreparation, result generationResult, response map[string]any) {
	shell := maps.Clone(response)
	delete(shell, "output")
	encoded, _ := json.Marshal(shell)
	record := &responseRecord{Model: request.Model, Shell: encoded, Input: prepared.input, Background: request.Background}
	for _, item := range response["output"].([]any) {
		object, _ := item.(map[string]any)
		if itemID, _ := object["id"].(string); itemID != "" {
			raw, _ := json.Marshal(object)
			record.Output = append(record.Output, storedItem{ID: itemID, Raw: raw})
		}
	}
	s.responseStates.Store(id, prepared.previous, result, responseState{Contents: prepared.contents, InlineInstructions: prepared.inlineInstructions, Record: record})
}

// responsesInputItems 把请求输入整理为带 ID 的输入项：字符串输入与字符串内容转为内容数组，缺少 ID 的项按位置编号
func responsesInputItems(raw json.RawMessage, responseID string) []storedItem {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		encoded, _ := json.Marshal([]map[string]string{{"role": "user", "content": text}})
		raw = encoded
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	stored := make([]storedItem, 0, len(items))
	for index, rawItem := range items {
		var item map[string]json.RawMessage
		var head struct {
			Type    string          `json:"type"`
			ID      string          `json:"id"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(rawItem, &item) != nil || json.Unmarshal(rawItem, &head) != nil {
			continue
		}
		if head.Type == "" || head.Type == "message" {
			item["type"] = json.RawMessage(`"message"`)
			var content string
			if json.Unmarshal(head.Content, &content) == nil {
				part := map[string]any{"type": "input_text", "text": content}
				if head.Role == "assistant" {
					part = map[string]any{"type": "output_text", "text": content, "annotations": []any{}}
				}
				item["content"], _ = json.Marshal([]any{part})
			}
			if _, exists := item["status"]; !exists {
				item["status"] = json.RawMessage(`"completed"`)
			}
		}
		if head.ID == "" {
			prefix := "item"
			if head.Type == "" || head.Type == "message" {
				prefix = "msg"
			}
			head.ID = fmt.Sprintf("%s_%s_%d", prefix, responseID, index)
			item["id"], _ = json.Marshal(head.ID)
		}
		encoded, _ := json.Marshal(item)
		stored = append(stored, storedItem{ID: head.ID, Raw: encoded})
	}
	return stored
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
	switch request.Truncation {
	case "", "disabled", "auto":
	default:
		return aistudio.GenerateRequest{}, nil, fmt.Errorf("unsupported truncation %q", request.Truncation)
	}
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
	var files []aistudio.Part
	for _, tool := range request.Tools {
		if tool.Type != "code_interpreter" {
			continue
		}
		ids, err := responsesContainerFiles(tool.Container)
		if err != nil {
			return aistudio.GenerateRequest{}, nil, err
		}
		for _, id := range ids {
			files = append(files, aistudio.Part{File: &aistudio.FileRef{ID: id}})
		}
	}
	if len(files) > 0 {
		index := len(contents) - 1
		for index >= 0 && contents[index].Role != aistudio.RoleUser {
			index--
		}
		if index < 0 {
			contents = append(contents, aistudio.Content{Role: aistudio.RoleUser, Parts: files})
		} else {
			contents[index].Parts = append(contents[index].Parts, files...)
		}
	}
	tools.ToolConfig.ParallelCalls = request.ParallelToolCalls
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
		Truncate: request.Truncation == "auto",
	}, inlineInstructions, nil
}

func responsesContents(raw json.RawMessage) ([]aistudio.Content, []string, error) {
	if value := strings.TrimSpace(string(raw)); value == "" || value == "null" {
		return nil, nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: text}}}}, nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, nil, fmt.Errorf("input must be a string or item array")
	}
	contents := make([]aistudio.Content, 0, len(items))
	var instructions []string
	var systemMedia []aistudio.Part
	pendingSignature := ""
	for _, rawItem := range items {
		var item responsesInputItem
		switch kind := responsesItemType(rawItem); kind {
		case "compaction", "compaction_trigger":
			continue
		case "web_search_call", "code_interpreter_call", "image_generation_call", "file_search_call", "computer_call", "computer_call_output",
			"mcp_call", "mcp_list_tools", "mcp_approval_request", "mcp_approval_response", "tool_search_call", "tool_search_output":
			pendingSignature = ""
			content, err := responsesHostedItem(kind, rawItem)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", kind, err)
			}
			contents = append(contents, content)
			continue
		default:
			if err := json.Unmarshal(rawItem, &item); err != nil {
				return nil, nil, fmt.Errorf("input item: %w", err)
			}
		}
		if item.Namespace != "" {
			item.Name = item.Namespace + "." + item.Name
		}
		call := aistudio.FunctionCall{ID: item.CallID, Name: item.Name, ThoughtSignature: pendingSignature}
		switch item.Type {
		case "", "message":
			pendingSignature = ""
			if item.Role == "system" || item.Role == "developer" {
				parts, err := openAIContentParts(item.Content)
				if err != nil {
					return nil, nil, fmt.Errorf("%s message: %w", item.Role, err)
				}
				var text strings.Builder
				for _, part := range parts {
					if part.Text == "" {
						systemMedia = append(systemMedia, part)
					}
					text.WriteString(part.Text)
				}
				if text.Len() > 0 {
					instructions = append(instructions, text.String())
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
			continue
		case "function_call":
			call.Arguments = functionCallArguments(item.Arguments)
		case "custom_tool_call":
			call.Arguments = customToolArguments(item.Input)
		case "local_shell_call":
			call.Name, call.Arguments = "local_shell", localShellArguments(item.Action)
		case "shell_call":
			call.Name, call.Arguments = "shell", rawObjectArguments(item.Action)
		case "apply_patch_call":
			call.Name, call.Arguments = "apply_patch", rawObjectArguments(item.Operation)
		case "function_call_output", "custom_tool_call_output", "local_shell_call_output", "shell_call_output", "apply_patch_call_output":
			pendingSignature = ""
			if item.CallID == "" {
				item.CallID = item.ID
			}
			if item.Type == "apply_patch_call_output" {
				item.Output, _ = json.Marshal(map[string]any{"status": item.Status, "output": rawJSONValue(item.Output, nil)})
			}
			output, err := normalizeFunctionResultContent(item.Output)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", item.Type, err)
			}
			contents = append(contents, aistudio.Content{Role: aistudio.RoleTool, Parts: []aistudio.Part{{FunctionResult: &aistudio.FunctionResult{
				ID: item.CallID, Content: output,
			}}}})
			continue
		case "reasoning":
			pendingSignature = item.EncryptedContent
			continue
		default:
			return nil, nil, fmt.Errorf("unsupported input item type %q", item.Type)
		}
		contents = append(contents, aistudio.Content{Role: aistudio.RoleAssistant, Parts: []aistudio.Part{{FunctionCall: &call}}})
		pendingSignature = ""
	}
	if len(systemMedia) > 0 {
		index := slices.IndexFunc(contents, func(content aistudio.Content) bool { return content.Role == aistudio.RoleUser })
		if index < 0 {
			contents = append([]aistudio.Content{{Role: aistudio.RoleUser, Parts: systemMedia}}, contents...)
		} else {
			contents[index].Parts = append(systemMedia, contents[index].Parts...)
		}
	}
	return contents, instructions, nil
}

// responsesItemType 读取输入项的 type 字段
func responsesItemType(raw json.RawMessage) string {
	var head struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &head)
	return head.Type
}

// responsesHostedItem 把托管工具与 computer、MCP 历史项转为文本摘要，截图与生成图片保留为图片
func responsesHostedItem(kind string, raw json.RawMessage) (aistudio.Content, error) {
	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return aistudio.Content{}, err
	}
	content := aistudio.Content{Role: aistudio.RoleAssistant}
	var media []aistudio.Part
	switch kind {
	case "image_generation_call":
		var result string
		if json.Unmarshal(item["result"], &result) == nil && result != "" {
			data, err := decodeBase64Flexible(result)
			if err != nil {
				return aistudio.Content{}, fmt.Errorf("result: %w", err)
			}
			media = append(media, aistudio.Part{InlineData: &aistudio.Blob{MIME: detectMediaType("", data), Data: data}})
			delete(item, "result")
		}
	case "computer_call_output":
		content.Role = aistudio.RoleUser
		var output struct {
			ImageURL string `json:"image_url"`
			FileID   string `json:"file_id"`
		}
		if json.Unmarshal(item["output"], &output) == nil && (output.ImageURL != "" || output.FileID != "") {
			part := aistudio.Part{File: &aistudio.FileRef{ID: output.FileID}}
			if output.ImageURL != "" {
				var err error
				if part, err = fileOrInlinePart(output.ImageURL, ""); err != nil {
					return aistudio.Content{}, fmt.Errorf("output: %w", err)
				}
			}
			media = append(media, part)
			delete(item, "output")
		}
	case "mcp_approval_response":
		content.Role = aistudio.RoleUser
	}
	delete(item, "id")
	delete(item, "type")
	delete(item, "status")
	summary, _ := json.Marshal(item)
	content.Parts = append([]aistudio.Part{{Text: kind + " " + string(summary)}}, media...)
	return content, nil
}

func mapResponsesTools(tools []responsesTool, choice json.RawMessage) (aistudio.Tools, error) {
	var mapped aistudio.Tools
	names := make(map[string]bool)
	addDeclaration := func(declaration aistudio.FunctionDeclaration) error {
		if declaration.Name == "" {
			return fmt.Errorf("function tool name is required")
		}
		if names[declaration.Name] {
			return fmt.Errorf("function tool name %q is duplicated", declaration.Name)
		}
		names[declaration.Name] = true
		mapped.Functions = append(mapped.Functions, declaration)
		return nil
	}
	addTool := func(tool responsesTool) error {
		if tool.Type == "custom" {
			return addDeclaration(customToolDeclaration(tool.Name, tool.Description, tool.Format))
		}
		parameters := tool.Parameters
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		return addDeclaration(aistudio.FunctionDeclaration{
			Name: tool.Name, Description: tool.Description, Parameters: parameters, Strict: tool.Strict != nil && *tool.Strict,
		})
	}
	for _, tool := range tools {
		switch tool.Type {
		case "function", "custom":
			if err := addTool(tool); err != nil {
				return aistudio.Tools{}, err
			}
		case "local_shell", "shell", "apply_patch":
			if err := addDeclaration(clientToolDeclarations[tool.Type]); err != nil {
				return aistudio.Tools{}, err
			}
		case "namespace":
			if tool.Name == "" {
				return aistudio.Tools{}, fmt.Errorf("namespace name is required")
			}
			for _, inner := range tool.Tools {
				if inner.Type != "function" && inner.Type != "custom" {
					return aistudio.Tools{}, fmt.Errorf("namespace %q tool type %q is not supported", tool.Name, inner.Type)
				}
				inner.Name = tool.Name + "." + inner.Name
				if err := addTool(inner); err != nil {
					return aistudio.Tools{}, err
				}
			}
		case "web_search", "web_search_2025_08_26", "web_search_preview", "web_search_preview_2025_03_11":
			search, err := mapSearchOptions(tool.SearchContextSize, tool.UserLocation, tool.Filters)
			if err != nil {
				return aistudio.Tools{}, err
			}
			mapped.GoogleSearch = search
			mapped.Google = appendUnique(mapped.Google, "google_search")
		case "code_interpreter":
			mapped.Google = appendUnique(mapped.Google, "code_execution")
		case "url_context":
			mapped.Google = appendUnique(mapped.Google, "url_context")
		case "google_maps":
			mapped.Google = appendUnique(mapped.Google, "google_maps")
		case "image_search":
			mapped.Google = appendUnique(mapped.Google, "image_search")
		case "image_generation", "file_search", "mcp", "computer", "computer_use_preview", "tool_search", "programmatic_tool_calling":
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
	if responsesUsesWebSearch(request.Tools) {
		output = append(output, responseWebSearchItems(id, result.events)...)
	}
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
		output = append(output, responseFunctionCall(call, request.Tools))
	}
	for index, media := range result.media {
		item, err := responseImageGenerationItem(id, index, media)
		if err != nil {
			return nil, err
		}
		output = append(output, item)
	}
	status := "completed"
	incompleteReason := ""
	switch openAIFinishReason(result.finishReason, false) {
	case "length":
		status = "incomplete"
		incompleteReason = "max_output_tokens"
	case "content_filter":
		status = "incomplete"
		incompleteReason = "content_filter"
	}
	response := responseShell(id, created, status, request)
	response["completed_at"] = time.Now().Unix()
	if status == "incomplete" {
		response["incomplete_details"] = map[string]any{"reason": incompleteReason}
	}
	response["output"] = output
	response["output_text"] = result.text.String()
	if result.providerModel != "" {
		response["provider_model"] = result.providerModel
	}
	if providerReason := providerFinishReason(result.finishReason); providerReason != "" {
		response["provider_finish_reason"] = providerReason
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

func rawJSONConfigured(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null"
}

// responseFunctionCall 按声明的工具类型把函数调用投影为 Responses 输出项
func responseFunctionCall(call aistudio.FunctionCall, tools []responsesTool) map[string]any {
	item := map[string]any{"id": "fc_" + call.ID, "status": "completed", "call_id": call.ID}
	switch responsesToolKind(tools, call.Name) {
	case "local_shell":
		item["type"], item["action"] = "local_shell_call", localShellAction(call.Arguments)
		return item
	case "shell":
		item["type"], item["action"], item["environment"] = "shell_call", shellAction(call.Arguments), nil
		return item
	case "apply_patch":
		item["type"], item["operation"] = "apply_patch_call", rawJSONValue(call.Arguments, map[string]any{})
		return item
	case "custom":
		item["type"], item["name"], item["input"] = "custom_tool_call", call.Name, customToolInput(call.Arguments)
	default:
		item["type"], item["name"], item["arguments"] = "function_call", call.Name, string(call.Arguments)
	}
	if namespace := responsesNamespace(tools, call.Name); namespace != "" {
		item["namespace"] = namespace
		item["name"] = strings.TrimPrefix(call.Name, namespace+".")
	}
	return item
}

// responsesNamespace 返回函数所属的命名空间工具名
func responsesNamespace(tools []responsesTool, name string) string {
	for _, tool := range tools {
		if tool.Type != "namespace" {
			continue
		}
		for _, inner := range tool.Tools {
			if tool.Name+"."+inner.Name == name {
				return tool.Name
			}
		}
	}
	return ""
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
				logs = call.result.Error
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
		"input_tokens_details": map[string]any{
			"cached_tokens": 0,
		},
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
	searchCount   int
	searchQueries map[string]struct{}
	searchProbe   bool
	pendingText   []string
	pendingCode   *responsesPendingCode
}

type responsesPendingCode struct {
	id    string
	index int
	code  string
}

func (s *server) streamResponses(w http.ResponseWriter, r *http.Request, request responsesRequest, prepared responsesPreparation, id string, created int64, events <-chan aistudio.Event) {
	if err := streamHeaders(w); err != nil {
		return
	}
	writer := &responsesStreamWriter{
		w: w, id: id, created: created, request: request,
		indexes: make(map[string]int), searchProbe: responsesUsesWebSearch(request.Tools),
	}
	if err := writer.emit("response.created", map[string]any{"response": responseShell(id, created, "in_progress", request)}); err != nil {
		return
	}
	if err := writer.emit("response.in_progress", map[string]any{"response": responseShell(id, created, "in_progress", request)}); err != nil {
		return
	}
	result, err := consumeStreamEvents(r.Context(), events, writer.live, func() error { return writeSSEHeartbeat(w) })
	if err != nil {
		if shouldWriteRequestError(r, err) {
			_ = writer.flushPendingText()
			_ = writer.failed(err)
		}
		return
	}
	response, err := buildResponsesObject(id, created, request, result)
	if err != nil {
		_ = writer.failed(err)
		return
	}
	completeErr := writer.complete(result, response)
	if request.storesResponse() {
		s.storeResponse(id, request, prepared, result, response)
	}
	if completeErr != nil {
		_ = writer.failed(completeErr)
		return
	}
	if err := writer.terminal(response); err != nil {
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
		if writer.searchProbe {
			writer.pendingText = append(writer.pendingText, event.Text)
			return nil
		}
		return writer.emitText(event.Text)
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
	case aistudio.EventGrounding:
		if event.Grounding != nil {
			emitted, err := writer.emitGrounding(*event.Grounding)
			if err != nil {
				return err
			}
			if writer.searchProbe && emitted {
				if err := writer.flushPendingText(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (writer *responsesStreamWriter) emitText(text string) error {
	index, err := writer.ensureText()
	if err != nil {
		return err
	}
	return writer.emit("response.output_text.delta", map[string]any{
		"item_id": "msg_" + writer.id, "output_index": index, "content_index": 0, "delta": text, "logprobs": []any{},
	})
}

func (writer *responsesStreamWriter) flushPendingText() error {
	writer.searchProbe = false
	for _, text := range writer.pendingText {
		if err := writer.emitText(text); err != nil {
			return err
		}
	}
	writer.pendingText = nil
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
	completed := responseFunctionCall(call, writer.request.Tools)
	id := "fc_" + call.ID
	index := len(writer.indexes)
	writer.indexes[id] = index
	item := maps.Clone(completed)
	item["status"] = "in_progress"
	switch completed["type"] {
	case "function_call":
		item["arguments"] = ""
	case "custom_tool_call":
		item["input"] = ""
	}
	if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": item}); err != nil {
		return err
	}
	switch completed["type"] {
	case "function_call":
		arguments := string(call.Arguments)
		if err := writer.emit("response.function_call_arguments.delta", map[string]any{
			"item_id": id, "output_index": index, "delta": arguments,
		}); err != nil {
			return err
		}
		if err := writer.emit("response.function_call_arguments.done", map[string]any{
			"item_id": id, "output_index": index, "arguments": arguments, "name": item["name"],
		}); err != nil {
			return err
		}
	case "custom_tool_call":
		if err := writer.emit("response.custom_tool_call_input.delta", map[string]any{
			"item_id": id, "output_index": index, "delta": completed["input"],
		}); err != nil {
			return err
		}
		if err := writer.emit("response.custom_tool_call_input.done", map[string]any{
			"item_id": id, "output_index": index, "input": completed["input"],
		}); err != nil {
			return err
		}
	}
	return writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": completed})
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
		logs = result.Error
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

// complete 结束仍打开的输出项，并把响应对象的输出按流中顺序排列
func (writer *responsesStreamWriter) complete(result generationResult, response map[string]any) error {
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
	if err := writer.flushPendingText(); err != nil {
		return err
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
	orderResponsesOutput(response, writer.indexes)
	return nil
}

// terminal 发送携带完整响应对象的终止事件
func (writer *responsesStreamWriter) terminal(response map[string]any) error {
	eventType := "response.completed"
	if response["status"] == "incomplete" {
		eventType = "response.incomplete"
	}
	return writer.emit(eventType, map[string]any{"response": response})
}

func orderResponsesOutput(response map[string]any, indexes map[string]int) {
	output, ok := response["output"].([]any)
	if !ok {
		return
	}
	sort.SliceStable(output, func(left int, right int) bool {
		leftIndex, leftExists := responseOutputIndex(output[left], indexes)
		rightIndex, rightExists := responseOutputIndex(output[right], indexes)
		if !leftExists {
			return false
		}
		if !rightExists {
			return true
		}
		return leftIndex < rightIndex
	})
	response["output"] = output
}

func responseOutputIndex(item any, indexes map[string]int) (int, bool) {
	object, ok := item.(map[string]any)
	if !ok {
		return 0, false
	}
	id, ok := object["id"].(string)
	if !ok {
		return 0, false
	}
	index, exists := indexes[id]
	return index, exists
}

func (writer *responsesStreamWriter) failed(err error) error {
	response := responseShell(writer.id, writer.created, "failed", writer.request)
	response["error"] = map[string]any{"code": openAIErrorCode(err), "message": err.Error()}
	return writer.emit("response.failed", map[string]any{"response": response})
}
