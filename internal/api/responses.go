package api

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	Conversation       json.RawMessage   `json:"conversation"`
	Prompt             json.RawMessage   `json:"prompt"`
	Include            []string          `json:"include"`
	ServiceTier        string            `json:"service_tier"`
	// MaxToolCalls 是本响应内置工具调用总次数的上限
	MaxToolCalls      *int64 `json:"max_tool_calls"`
	ContextManagement []struct {
		Type             string `json:"type"`
		CompactThreshold *int64 `json:"compact_threshold"`
	} `json:"context_management"`
	// split 是 prepareResponses 对 Input 的切分结果
	split splitInput
}

// inputItems 返回 Input 的切分结果，prepareResponses 已切分同一输入时直接沿用
func (request responsesRequest) inputItems() splitInput {
	if bytes.Equal(request.split.raw, request.Input) {
		return request.split
	}
	return splitResponsesInput(request.Input)
}

// compactThreshold 返回 context_management 中 compaction 的触发 token 数，没有时为 0
func (request responsesRequest) compactThreshold() int64 {
	for _, entry := range request.ContextManagement {
		if entry.Type == "compaction" && entry.CompactThreshold != nil {
			return *entry.CompactThreshold
		}
	}
	return 0
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
	// Size、Quality、OutputFormat、OutputCompression 与 InputImageMask 是 image_generation 工具的图片尺寸、质量、输出格式、JPEG 压缩级别与编辑遮罩
	Size              string              `json:"size,omitempty"`
	Quality           string              `json:"quality,omitempty"`
	OutputFormat      string              `json:"output_format,omitempty"`
	OutputCompression *int                `json:"output_compression,omitempty"`
	InputImageMask    *responsesImageMask `json:"input_image_mask,omitempty"`
	// DisplayWidth、DisplayHeight 与 Environment 是 computer_use_preview 工具的屏幕尺寸与运行环境，shell 工具的 environment 为对象
	DisplayWidth  int64           `json:"display_width,omitempty"`
	DisplayHeight int64           `json:"display_height,omitempty"`
	Environment   json.RawMessage `json:"environment,omitempty"`
	// SearchContentTypes 是 web_search 工具检索的内容类型 text 与 image
	SearchContentTypes []string `json:"search_content_types,omitempty"`
	// ServerLabel 至 ServerDescription 是 mcp 工具的远程 MCP 服务器设置
	ServerLabel       string            `json:"server_label,omitempty"`
	ServerURL         string            `json:"server_url,omitempty"`
	ConnectorID       string            `json:"connector_id,omitempty"`
	Authorization     string            `json:"authorization,omitempty"`
	Headers           map[string]string `json:"headers,omitempty"`
	AllowedTools      json.RawMessage   `json:"allowed_tools,omitempty"`
	RequireApproval   json.RawMessage   `json:"require_approval,omitempty"`
	ServerDescription string            `json:"server_description,omitempty"`
}

// imageGenerationTool 返回请求声明的 image_generation 工具
func imageGenerationTool(tools []responsesTool) *responsesTool {
	index := slices.IndexFunc(tools, func(tool responsesTool) bool { return tool.Type == "image_generation" })
	if index < 0 {
		return nil
	}
	return &tools[index]
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
	Actions          json.RawMessage `json:"actions"`
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
	// Stream 为后台流式响应的事件记录
	Stream *streamLog `json:"-"`
	// Include 是创建请求的 include，查询时与查询参数的 include 一起决定输出项的可选字段
	Include []string
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
	// Upstream、AccountID 与 Agent 是续接起点为 agent 后台任务时的上游 interaction ID、所属账户与 agent
	Upstream  string
	AccountID string
	Agent     string
}

// StateStore 持久保存续接节点，服务重启后由 Load 按保存顺序读回
type StateStore interface {
	Load() []SavedState
	Save(saved []SavedState, removed []string)
}

// SavedState 是写入 StateStore 的一个续接节点
type SavedState struct {
	ID   string
	Data []byte
}

type responseStateStore struct {
	mu      sync.Mutex
	states  map[string]responseState
	order   []string
	persist StateStore
}

// newResponseStateStore 创建续接节点存储，persist 非空时读回其中保存的节点并把之后的变化写入它
func newResponseStateStore(persist StateStore) *responseStateStore {
	store := &responseStateStore{states: make(map[string]responseState, responseStateCapacity), persist: persist}
	if persist == nil {
		return store
	}
	for _, saved := range persist.Load() {
		var state responseState
		if json.Unmarshal(saved.Data, &state) == nil {
			store.states[saved.ID] = state
			store.order = append(store.order, saved.ID)
		}
	}
	return store
}

// persistLocked 把仍存在且已结束的节点写入 persist，并删除已移除的节点；进行中的后台响应与交互在服务重启后不能继续，只保存终态
func (store *responseStateStore) persistLocked(saved, removed []string) {
	if store.persist == nil {
		return
	}
	states := make([]SavedState, 0, len(saved))
	for _, id := range saved {
		if state, exists := store.states[id]; exists && state.finished() {
			data, _ := json.Marshal(state)
			states = append(states, SavedState{ID: id, Data: data})
		}
	}
	store.persist.Save(states, removed)
}

// finished 返回节点的响应或交互是否已不在进行中
func (state responseState) finished() bool {
	var object json.RawMessage
	switch {
	case state.Record != nil:
		object = state.Record.Shell
	case state.Interaction != nil:
		object = state.Interaction.object
	}
	var status struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(object, &status)
	return status.Status != "queued" && status.Status != "in_progress"
}

func (store *responseStateStore) Load(id string) (responseHistory, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	history := responseHistory{ID: id, Contents: make([]aistudio.Content, 0), InlineInstructions: make([]string, 0)}
	if state, exists := store.states[id]; exists {
		history.Interaction = state.Interaction != nil
		if state.Interaction != nil {
			history.Upstream, history.AccountID, history.Agent = state.Interaction.upstream, state.Interaction.account, state.Interaction.agent
		}
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
	if _, exists := store.states[id]; !exists {
		store.order = append(store.order, id)
	}
	store.states[id] = state
	saved, removed := []string{id}, []string(nil)
	for len(store.order) > responseStateCapacity {
		evicted := store.order[0]
		store.order = store.order[1:]
		saved = append(saved, store.removeLocked(evicted)...)
		removed = append(removed, evicted)
	}
	store.persistLocked(saved, removed)
}

// removeLocked 删除一个节点，并把它的上下文并入以它为续接起点的子节点，返回这些子节点的 ID
func (store *responseStateStore) removeLocked(id string) []string {
	parent := store.states[id]
	var children []string
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
		children = append(children, childID)
	}
	delete(store.states, id)
	return children
}

// Response 返回 Responses 接口保存的完整响应对象及其是否以后台模式创建
func (store *responseStateStore) Response(id string, include []string) (json.RawMessage, bool, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	state, exists := store.states[id]
	if !exists || state.Record == nil {
		return nil, false, false
	}
	var shell map[string]json.RawMessage
	_ = json.Unmarshal(state.Record.Shell, &shell)
	include = slices.Concat(state.Record.Include, include)
	output := make([]any, 0, len(state.Record.Output))
	for _, item := range state.Record.Output {
		var object map[string]any
		_ = json.Unmarshal(item.Raw, &object)
		output = append(output, responseIncludeView(object, include))
	}
	shell["output"], _ = json.Marshal(output)
	encoded, _ := json.Marshal(officialOrder("responses", shell))
	return encoded, state.Record.Background, true
}

// Stream 返回以 background 与 stream 创建的响应的事件记录
func (store *responseStateStore) Stream(id string) (*streamLog, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	state, exists := store.states[id]
	if !exists || state.Record == nil || state.Record.Stream == nil {
		return nil, false
	}
	return state.Record.Stream, true
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
		parent := store.states[parentID]
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
	store.persistLocked(store.removeLocked(id), []string{id})
	store.order = slices.DeleteFunc(store.order, func(value string) bool { return value == id })
	return true
}

// ResolveItems 将输入中的 item_reference 替换为已保存的同 ID 输入或输出项
func (store *responseStateStore) ResolveItems(input splitInput) (splitInput, error) {
	if !slices.ContainsFunc(input.heads, responsesItemHead.reference) {
		return input, nil
	}
	items := slices.Clone(input.items)
	store.mu.Lock()
	defer store.mu.Unlock()
	for index, head := range input.heads {
		if !head.reference() {
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
			return splitInput{}, fmt.Errorf("item %q was not found", head.ID)
		}
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return splitInput{}, err
	}
	return splitResponsesInput(encoded), nil
}

// responsesItemHead 是输入项的类型、角色与 ID
type responsesItemHead struct {
	Type string `json:"type"`
	Role string `json:"role"`
	ID   string `json:"id"`
	// invalid 表示该项不能完整解析为类型、角色与 ID
	invalid bool
}

// reference 返回输入项是否引用已保存的项：item_reference 或只带 ID 的项
func (head responsesItemHead) reference() bool {
	return !head.invalid && (head.Type == "item_reference" || head.Type == "" && head.Role == "" && head.ID != "")
}

// splitInput 是切分一次的 Responses 输入：array 表示原文是数组，items 与 heads 按位置对应
type splitInput struct {
	raw   json.RawMessage
	array bool
	items []json.RawMessage
	heads []responsesItemHead
}

// splitResponsesInput 把 Responses 输入切分为各输入项原文与类型、角色和 ID
func splitResponsesInput(raw json.RawMessage) splitInput {
	input := splitInput{raw: raw}
	if json.Unmarshal(raw, &input.items) != nil {
		input.items = nil
		return input
	}
	input.array = true
	input.heads = responsesItemHeads(input.items)
	return input
}

// responsesItemHeads 返回各输入项的类型、角色与 ID
func responsesItemHeads(items []json.RawMessage) []responsesItemHead {
	heads := make([]responsesItemHead, len(items))
	for index, item := range items {
		heads[index].invalid = json.Unmarshal(item, &heads[index]) != nil
	}
	return heads
}

// hasType 返回输入中是否有 kinds 中任一类型的项
func (input splitInput) hasType(kinds ...string) bool {
	return slices.ContainsFunc(input.heads, func(head responsesItemHead) bool { return slices.Contains(kinds, head.Type) })
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
	if rawJSONConfigured(request.Conversation) || rawJSONConfigured(request.Prompt) {
		writeOpenAIError(w, http.StatusNotFound, "not_found", "conversation and prompt objects are not stored by this service; send the full input or previous_response_id")
		return
	}
	responseID := newID("resp")
	prepared, err := s.prepareResponses(r.Context(), &request, responseID, true)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		}
		return
	}
	if responsesCompactionTrigger(request.inputItems()) {
		s.handleCompactionTrigger(w, r, request, prepared, responseID)
		return
	}
	if request.Background && !request.Stream {
		s.respondBackground(w, r, request, prepared, responseID)
		return
	}
	if request.Background {
		s.streamBackground(w, r, request, prepared, responseID)
		return
	}
	if !request.Stream {
		var stopKeepalive func()
		w, stopKeepalive = s.nonstreamKeepalive(w, r)
		defer stopKeepalive()
	}
	compaction, err := s.compactOverThreshold(r.Context(), &request, &prepared, responseID)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIRequestError(w, err)
		}
		return
	}
	generateRequest := prepared.generate
	generateRequest.Unary = !request.Stream || s.responseModes().FakeStream
	events, err := s.service.Generate(r.Context(), generateRequest)
	if err == nil && request.Stream {
		events, _, err = awaitStreamStart(r.Context(), events)
	}
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIRequestError(w, err)
		}
		return
	}
	created := time.Now().Unix()
	if request.Stream {
		s.streamResponses(w, r, request, prepared, responseID, created, events, compaction)
		return
	}
	result, err := consumeEvents(r.Context(), events)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIRequestError(w, err)
		}
		return
	}
	response, err := buildResponsesObject(responseID, created, request, result, outputPrefix(compaction, prepared))
	if err != nil {
		writeOpenAIRequestError(w, err)
		return
	}
	if request.storesResponse() {
		s.storeResponse(responseID, request, prepared, result, response)
	}
	writeJSON(w, http.StatusOK, officialOrder("responses", responseView(response, request.Include)))
}

// responseView 返回按 include 裁剪输出项可选字段后的响应对象
func responseView(response map[string]any, include []string) map[string]any {
	output, ok := response["output"].([]any)
	if !ok {
		return response
	}
	view := maps.Clone(response)
	items := make([]any, 0, len(output))
	for _, item := range output {
		object, _ := item.(map[string]any)
		items = append(items, responseIncludeView(object, include))
	}
	view["output"] = items
	return view
}

// responseIncludeView 返回按 include 输出的项：没有 code_interpreter_call.outputs 时代码执行项的 outputs 为 null，没有 web_search_call.action.sources 时搜索项的 action 不带 sources
func responseIncludeView(item map[string]any, include []string) map[string]any {
	switch item["type"] {
	case "code_interpreter_call":
		if item["outputs"] != nil && !slices.Contains(include, "code_interpreter_call.outputs") {
			view := maps.Clone(item)
			view["outputs"] = nil
			return view
		}
	case "web_search_call":
		action, _ := item["action"].(map[string]any)
		if _, ok := action["sources"]; ok && !slices.Contains(include, "web_search_call.action.sources") {
			view, trimmed := maps.Clone(item), maps.Clone(action)
			delete(trimmed, "sources")
			view["action"] = trimmed
			return view
		}
	}
	return item
}

// responsesPreparation 是转换后的生成请求，以及保存续接节点所需的前序上下文与本次输入
type responsesPreparation struct {
	generate           aistudio.GenerateRequest
	previous           responseHistory
	contents           []aistudio.Content
	inlineInstructions []string
	input              []storedItem
	// approved 是批准后由服务执行的 mcp_call 项，排在输出首位
	approved []map[string]any
	// stream 为后台流式响应的事件记录，随响应对象保存
	stream *streamLog
}

// compactOverThreshold 在 context_management 的 compaction 阈值被超过时先压缩输入，返回生成前插入输出的 compaction 项
func (s *server) compactOverThreshold(ctx context.Context, request *responsesRequest, prepared *responsesPreparation, id string) (map[string]any, error) {
	threshold := request.compactThreshold()
	if threshold <= 0 {
		return nil, nil
	}
	count, err := s.service.CountTokens(ctx, aistudio.TokenCountRequest{
		Model: prepared.generate.Model, System: prepared.generate.System, Contents: prepared.generate.Contents, Tools: prepared.generate.Tools,
	})
	if err != nil || count.InputTokens <= threshold {
		return nil, err
	}
	item, retained, instructions, _, err := s.compactResponses(ctx, *request, prepared.previous, id)
	if err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(item)
	request.Input, _ = json.Marshal(slices.Concat(instructions, retained, []json.RawMessage{encoded}))
	request.PreviousResponseID = ""
	compacted, err := s.prepareResponses(ctx, request, id, false)
	if err != nil {
		return nil, err
	}
	compacted.approved = prepared.approved
	*prepared = compacted
	return item, nil
}

// prepareResponses 展开 item_reference、转换请求、下载外部媒体并接上 previous_response_id 的上下文；execute 为真时执行输入中批准的 MCP 调用
func (s *server) prepareResponses(ctx context.Context, request *responsesRequest, id string, execute bool) (responsesPreparation, error) {
	input, err := s.responseStates.ResolveItems(splitResponsesInput(request.Input))
	if err != nil {
		return responsesPreparation{}, err
	}
	for index, tool := range request.Tools {
		if tool.Type == "function" && tool.Strict == nil {
			strict := tool.strict()
			request.Tools[index].Strict = &strict
		}
	}
	additionalTools, _ := responsesInputConfig(input)
	tools := slices.Concat(request.Tools, additionalTools)
	rewritten, approved, err := s.applyMCPApprovals(ctx, request.PreviousResponseID, tools, input.raw, execute)
	if err != nil {
		return responsesPreparation{}, err
	}
	request.Input, request.split = rewritten, input
	if !bytes.Equal(rewritten, input.raw) {
		request.split = splitResponsesInput(rewritten)
	}
	generateRequest, inlineInstructions, err := request.toGenerateRequest(id)
	if err == nil {
		generateRequest, err = s.resolveMCPTools(ctx, generateRequest)
	}
	if err == nil {
		generateRequest, err = s.mcpApprovalTools(ctx, tools, request.ToolChoice, generateRequest)
	}
	if err != nil {
		return responsesPreparation{}, err
	}
	generateRequest = s.prepareSearch(generateRequest)
	if err := inlineRemoteMedia(ctx, generateRequest.Contents); err != nil {
		return responsesPreparation{}, err
	}
	if tool := imageGenerationTool(request.Tools); tool != nil {
		if _, _, err := openAIImageEncoding(tool.OutputFormat, tool.OutputCompression); err != nil {
			return responsesPreparation{}, fmt.Errorf("image_generation: %w", err)
		}
	}
	if tool := imageGenerationTool(request.Tools); tool != nil && (tool.Size != "" || tool.Quality != "") {
		models, err := s.service.Models(ctx)
		if err != nil {
			return responsesPreparation{}, err
		}
		if model, _ := LookupPublicModel(models, request.Model); model.Capabilities["image_route"] {
			if generateRequest.Config.ImageConfig, err = openAIImageConfig(tool.Size, tool.Quality, model.CapabilityOptions); err != nil {
				return responsesPreparation{}, fmt.Errorf("image_generation: %w", err)
			}
		}
	}
	prepared := responsesPreparation{
		generate: generateRequest, contents: cloneResponseContents(generateRequest.Contents),
		inlineInstructions: append([]string(nil), inlineInstructions...), approved: approved,
	}
	if request.storesResponse() {
		prepared.input = responsesInputItems(input, id)
	}
	if tool := imageGenerationTool(request.Tools); tool != nil && tool.InputImageMask != nil {
		mask, err := s.imageGenerationMask(ctx, *tool.InputImageMask)
		if err != nil {
			return responsesPreparation{}, fmt.Errorf("image_generation.input_image_mask: %w", err)
		}
		generateRequest.Contents = withImageMask(generateRequest.Contents, mask)
		prepared.generate.Contents = generateRequest.Contents
	}
	if request.PreviousResponseID == "" {
		return prepared, nil
	}
	previous, ok := s.responseStates.Load(request.PreviousResponseID)
	if !ok || previous.Interaction {
		return responsesPreparation{}, fmt.Errorf("previous response %q was not found", request.PreviousResponseID)
	}
	prepared.previous = previous
	prepared.generate.Contents = append(cloneResponseContents(previous.Contents), generateRequest.Contents...)
	inlineInstructions = append(append([]string(nil), previous.InlineInstructions...), inlineInstructions...)
	prepared.generate.System = systemSegments(strings.Join(request.instructions(inlineInstructions), "\n"))
	return prepared, nil
}

// instructions 按顺序返回 instructions、输入中的系统消息与 mcp 工具的 server_description
func (request responsesRequest) instructions(inline []string) []string {
	instructions := make([]string, 0, 1+len(inline))
	if request.Instructions != "" {
		instructions = append(instructions, request.Instructions)
	}
	instructions = append(instructions, inline...)
	for _, tool := range request.Tools {
		if tool.Type == "mcp" && tool.ServerDescription != "" {
			instructions = append(instructions, "MCP server "+tool.ServerLabel+": "+tool.ServerDescription)
		}
	}
	return instructions
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
	record := &responseRecord{Model: request.Model, Shell: encoded, Input: prepared.input, Background: request.Background, Stream: prepared.stream, Include: request.Include}
	for _, item := range response["output"].([]any) {
		object, _ := item.(map[string]any)
		if itemID, _ := object["id"].(string); itemID != "" {
			raw, _ := json.Marshal(officialOrder("responses", object))
			record.Output = append(record.Output, storedItem{ID: itemID, Raw: raw})
		}
	}
	s.responseStates.Store(id, prepared.previous, result, responseState{Contents: prepared.contents, InlineInstructions: prepared.inlineInstructions, Record: record})
}

// responsesInputItems 把请求输入整理为带 ID 的输入项：字符串输入与字符串内容转为内容数组，缺少 ID 的项按位置编号
func responsesInputItems(input splitInput, responseID string) []storedItem {
	items := input.items
	if !input.array {
		var text string
		if json.Unmarshal(input.raw, &text) != nil {
			return nil
		}
		encoded, _ := json.Marshal(map[string]string{"role": "user", "content": text})
		items = []json.RawMessage{encoded}
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
	var output aistudio.Output
	for _, event := range result.events {
		output.Add(event)
	}
	return output.History()
}

func (request responsesRequest) toGenerateRequest(id string) (aistudio.GenerateRequest, []string, error) {
	switch request.Truncation {
	case "", "disabled", "auto":
	default:
		return aistudio.GenerateRequest{}, nil, fmt.Errorf("unsupported truncation %q", request.Truncation)
	}
	input := request.inputItems()
	contents, inlineInstructions, err := responsesContents(input)
	if err != nil {
		return aistudio.GenerateRequest{}, nil, err
	}
	instructions := request.instructions(inlineInstructions)
	additionalTools, configuredEffort := responsesInputConfig(input)
	tools, err := mapResponsesTools(slices.Concat(request.Tools, additionalTools), request.ToolChoice)
	if err != nil {
		return aistudio.GenerateRequest{}, nil, err
	}
	if request.MaxToolCalls != nil && (len(tools.Google) > 0 || len(tools.MCPServers) > 0) {
		instructions = append(instructions, fmt.Sprintf("Make at most %d built-in tool calls in this response.", *request.MaxToolCalls))
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
		HideThinking:    true,
	}
	if rawJSONConfigured(request.Reasoning) {
		var reasoning struct {
			Effort          string `json:"effort"`
			Summary         string `json:"summary"`
			GenerateSummary string `json:"generate_summary"`
		}
		if err := json.Unmarshal(request.Reasoning, &reasoning); err != nil {
			return aistudio.GenerateRequest{}, nil, fmt.Errorf("invalid reasoning: %w", err)
		}
		config.ReasoningEffort = reasoning.Effort
		summary := cmp.Or(reasoning.Summary, reasoning.GenerateSummary)
		config.HideThinking = summary == "" || summary == "none"
	}
	if configuredEffort != "" {
		config.ReasoningEffort = configuredEffort
	}
	if rawJSONConfigured(request.Text) {
		var text struct {
			Format struct {
				Type        string          `json:"type"`
				Description string          `json:"description"`
				Schema      json.RawMessage `json:"schema"`
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
			schema, err := describedSchema(text.Format.Schema, text.Format.Description)
			if err != nil {
				return aistudio.GenerateRequest{}, nil, fmt.Errorf("invalid text format json_schema: %w", err)
			}
			config.ResponseJSONSchema = schema
		case "", "text":
		default:
			return aistudio.GenerateRequest{}, nil, fmt.Errorf("unsupported text format %q", text.Format.Type)
		}
	}
	return aistudio.GenerateRequest{
		ID:          id,
		Model:       request.Model,
		System:      systemSegments(strings.Join(instructions, "\n")),
		Contents:    contents,
		Config:      config,
		Tools:       tools,
		Truncate:    request.Truncation == "auto",
		ServiceTier: openAIServiceTier(request.ServiceTier),
	}, inlineInstructions, nil
}

func responsesContents(input splitInput) ([]aistudio.Content, []string, error) {
	if !rawJSONConfigured(input.raw) {
		return nil, nil, nil
	}
	if !input.array {
		var text string
		if err := json.Unmarshal(input.raw, &text); err != nil {
			return nil, nil, fmt.Errorf("input must be a string or item array")
		}
		return []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: text}}}}, nil, nil
	}
	items, heads := input.items, input.heads
	if input.hasType("compaction") {
		window, err := splitCompaction(items)
		if err != nil {
			return nil, nil, err
		}
		items = window.items()
		heads = responsesItemHeads(items)
	}
	contents := make([]aistudio.Content, 0, len(items))
	var instructions []string
	var systemMedia []aistudio.Part
	pendingSignature := ""
	for index, rawItem := range items {
		var item responsesInputItem
		switch kind := heads[index].Type; kind {
		case "compaction_trigger":
			if index != len(items)-1 {
				return nil, nil, fmt.Errorf("compaction_trigger must be the final input item")
			}
			continue
		case "additional_tools", "configuration_update":
			continue
		case "web_search_call", "code_interpreter_call", "image_generation_call", "file_search_call",
			"mcp_call", "mcp_list_tools", "mcp_approval_request", "mcp_approval_response", "tool_search_call", "tool_search_output", "program", "program_output":
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
		item.Name = namespacedName(item.Namespace, item.Name)
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
		case "agent_message":
			pendingSignature = ""
			parts, err := agentMessageParts(item.Content)
			if err != nil {
				return nil, nil, fmt.Errorf("agent_message: %w", err)
			}
			contents = append(contents, aistudio.Content{Role: aistudio.RoleUser, Parts: parts})
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
		case "computer_call":
			call.Name, call.Arguments = "computer", rawObjectArguments(item.Action)
			if rawJSONConfigured(item.Actions) {
				call.Arguments, _ = json.Marshal(map[string]json.RawMessage{"actions": item.Actions})
			}
		case "computer_call_output":
			pendingSignature = ""
			screenshot, err := computerScreenshot(item.Output)
			if err != nil {
				return nil, nil, fmt.Errorf("computer_call_output: %w", err)
			}
			contents = append(contents, aistudio.Content{Role: aistudio.RoleTool, Parts: append([]aistudio.Part{{FunctionResult: &aistudio.FunctionResult{
				ID: item.CallID, Name: "computer", Content: json.RawMessage(`{"type":"computer_screenshot"}`),
			}}}, screenshot...)})
			continue
		case "function_call_output", "custom_tool_call_output", "local_shell_call_output", "shell_call_output", "apply_patch_call_output":
			pendingSignature = ""
			if item.CallID == "" {
				item.CallID = item.ID
			}
			if item.Type == "apply_patch_call_output" {
				item.Output, _ = json.Marshal(map[string]any{"status": item.Status, "output": rawJSONValue(item.Output, nil)})
			}
			output, media, err := functionResultParts(item.Output, false, openAIContentParts)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", item.Type, err)
			}
			contents = append(contents, aistudio.Content{Role: aistudio.RoleTool, Parts: append([]aistudio.Part{{FunctionResult: &aistudio.FunctionResult{
				ID: item.CallID, Content: output,
			}}}, media...)})
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
	return prependSystemMedia(contents, systemMedia), instructions, nil
}

// agentMessageParts 把 Codex 智能体间消息的内容转为文本片段，encrypted_content 中是本服务模型写出的消息原文
func agentMessageParts(raw json.RawMessage) ([]aistudio.Part, error) {
	var content []struct {
		Type             string `json:"type"`
		Text             string `json:"text"`
		EncryptedContent string `json:"encrypted_content"`
	}
	if err := json.Unmarshal(raw, &content); err != nil {
		return nil, err
	}
	parts := make([]aistudio.Part, 0, len(content))
	for _, part := range content {
		switch part.Type {
		case "input_text":
			parts = append(parts, aistudio.Part{Text: part.Text})
		case "encrypted_content":
			parts = append(parts, aistudio.Part{Text: part.EncryptedContent})
		default:
			return nil, fmt.Errorf("unsupported content type %q", part.Type)
		}
	}
	return parts, nil
}

// responsesInputConfig 返回输入项中 additional_tools 提供的工具与最后一个 configuration_update 的推理强度
func responsesInputConfig(input splitInput) ([]responsesTool, string) {
	if !input.hasType("additional_tools", "configuration_update") {
		return nil, ""
	}
	var items []struct {
		Type      string          `json:"type"`
		Tools     []responsesTool `json:"tools"`
		Reasoning *struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if json.Unmarshal(input.raw, &items) != nil {
		return nil, ""
	}
	var tools []responsesTool
	effort := ""
	for _, item := range items {
		switch item.Type {
		case "additional_tools":
			tools = append(tools, item.Tools...)
		case "configuration_update":
			if item.Reasoning != nil && item.Reasoning.Effort != "" {
				effort = item.Reasoning.Effort
			}
		}
	}
	return tools, effort
}

// responsesItemType 读取输入项的 type 字段
func responsesItemType(raw json.RawMessage) string {
	var head struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &head)
	return head.Type
}

// responsesHostedItem 把托管工具与 MCP 历史项转为文本摘要，生成图片保留为图片
func responsesHostedItem(kind string, raw json.RawMessage) (aistudio.Content, error) {
	var item map[string]json.RawMessage
	_ = json.Unmarshal(raw, &item)
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

// mcpOutputText 把上游 MCP 工具结果中的文本内容拼成 mcp_call 的 output，无法解析时返回结果原文
func mcpOutputText(raw json.RawMessage) string {
	switch result := mcpToolResult(raw).(type) {
	case string:
		return result
	case []map[string]any:
		var text strings.Builder
		for _, item := range result {
			if value, ok := item["text"].(string); ok {
				text.WriteString(value)
			}
		}
		return text.String()
	}
	return string(raw)
}

// mcpCallError 在上游 MCP 结果的 isError 为 true 时返回 mcp_call 的 mcp_tool_execution_error，content 为结果的内容数组，否则返回 nil
func mcpCallError(raw json.RawMessage) map[string]any {
	var response struct {
		Result string `json:"call_tool_result_json"`
	}
	var result struct {
		Content json.RawMessage `json:"content"`
		IsError bool            `json:"isError"`
	}
	if json.Unmarshal(raw, &response) != nil || json.Unmarshal([]byte(response.Result), &result) != nil || !result.IsError {
		return nil
	}
	return map[string]any{"type": "mcp_tool_execution_error", "content": result.Content}
}

// computerScreenshot 返回 computer_call_output 中 computer_screenshot 的图片 Part
func computerScreenshot(raw json.RawMessage) ([]aistudio.Part, error) {
	var output struct {
		ImageURL string `json:"image_url"`
		FileID   string `json:"file_id"`
	}
	if err := json.Unmarshal(raw, &output); err != nil {
		return nil, fmt.Errorf("output: %w", err)
	}
	switch {
	case output.ImageURL != "":
		part, err := fileOrInlinePart(output.ImageURL, "")
		if err != nil {
			return nil, fmt.Errorf("output: %w", err)
		}
		return []aistudio.Part{part}, nil
	case output.FileID != "":
		return []aistudio.Part{{File: &aistudio.FileRef{ID: output.FileID}}}, nil
	}
	return nil, nil
}

// strict 返回函数工具是否按 strict 校验：省略 strict 时参数 Schema 已满足 strict 要求即按 strict 处理
func (tool responsesTool) strict() bool {
	if tool.Strict != nil {
		return *tool.Strict
	}
	var schema any
	return json.Unmarshal(tool.Parameters, &schema) == nil && strictCompatible(schema)
}

// strictCompatible 表示 Schema 满足 strict 要求：每个对象设 additionalProperties 为 false 并把全部属性列为必填，且不含 strict 不支持的组合关键字
func strictCompatible(schema any) bool {
	value, ok := schema.(map[string]any)
	if !ok {
		return false
	}
	for _, keyword := range []string{"allOf", "oneOf", "not", "if", "then", "else", "patternProperties", "dependentRequired", "dependentSchemas"} {
		if _, ok := value[keyword]; ok {
			return false
		}
	}
	properties, _ := value["properties"].(map[string]any)
	if value["type"] == "object" || properties != nil {
		required, _ := value["required"].([]any)
		if value["additionalProperties"] != false || len(required) != len(properties) {
			return false
		}
		for _, name := range required {
			if key, _ := name.(string); properties[key] == nil {
				return false
			}
		}
	}
	children := slices.Collect(maps.Values(properties))
	for _, key := range []string{"$defs", "definitions"} {
		if definitions, ok := value[key].(map[string]any); ok {
			children = slices.AppendSeq(children, maps.Values(definitions))
		}
	}
	if anyOf, ok := value["anyOf"].([]any); ok {
		children = append(children, anyOf...)
	}
	if items, ok := value["items"]; ok {
		children = append(children, items)
	}
	return !slices.ContainsFunc(children, func(child any) bool { return !strictCompatible(child) })
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
			Name: tool.Name, Description: tool.Description, Parameters: parameters, Strict: tool.strict(),
		})
	}
	for _, tool := range tools {
		switch tool.Type {
		case "function", "custom":
			if err := addTool(tool); err != nil {
				return aistudio.Tools{}, err
			}
		case "local_shell", "shell", "apply_patch":
			if err := addDeclaration(shellSkills(clientToolDeclarations[tool.Type], tool.Environment)); err != nil {
				return aistudio.Tools{}, err
			}
		case "computer_use_preview", "computer":
			if err := addDeclaration(computerToolDeclaration(tool)); err != nil {
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
				inner.Name = namespacedName(tool.Name, inner.Name)
				if tool.Description != "" {
					inner.Description = strings.TrimSpace(tool.Description + "\n\n" + inner.Description)
				}
				if err := addTool(inner); err != nil {
					return aistudio.Tools{}, err
				}
			}
		case "web_search", "web_search_2025_08_26", "web_search_preview", "web_search_preview_2025_03_11":
			search, err := mapSearchOptions(tool.SearchContextSize, tool.UserLocation, tool.Filters)
			if err != nil {
				return aistudio.Tools{}, err
			}
			if len(tool.SearchContentTypes) > 0 {
				search.WebSearch, search.ImageSearch = slices.Contains(tool.SearchContentTypes, "text"), slices.Contains(tool.SearchContentTypes, "image")
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
		case "mcp":
			server, err := responsesMCPServer(tool)
			if err != nil {
				return aistudio.Tools{}, err
			}
			approval, err := parseMCPApproval(tool.RequireApproval)
			if err != nil {
				return aistudio.Tools{}, err
			}
			if approval.never {
				mapped.MCPServers = append(mapped.MCPServers, server)
			}
		case "image_generation", "file_search", "tool_search", "programmatic_tool_calling":
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
	mapped.ToolConfig.ServerSideInvocations = len(mapped.MCPServers) > 0 || slices.Contains(mapped.Google, "google_search")
	return mapped, nil
}

// responsesMCPServer 把 mcp 工具映射为 MCP 服务器连接：authorization 写为 Bearer 授权头，allowed_tools 按工具名与只读要求限定
func responsesMCPServer(tool responsesTool) (aistudio.MCPServer, error) {
	if tool.ConnectorID != "" {
		return aistudio.MCPServer{}, fmt.Errorf("mcp connector_id %q is an OpenAI hosted connector; upstream connects only to a server_url", tool.ConnectorID)
	}
	if tool.ServerLabel == "" || tool.ServerURL == "" {
		return aistudio.MCPServer{}, fmt.Errorf("mcp server_label and server_url are required")
	}
	server := aistudio.MCPServer{Name: tool.ServerLabel, URL: tool.ServerURL, Headers: maps.Clone(tool.Headers)}
	if tool.Authorization != "" {
		if server.Headers == nil {
			server.Headers = map[string]string{}
		}
		server.Headers["Authorization"] = "Bearer " + tool.Authorization
	}
	if rawJSONConfigured(tool.AllowedTools) {
		var filter struct {
			ToolNames []string `json:"tool_names"`
			ReadOnly  *bool    `json:"read_only"`
		}
		if json.Unmarshal(tool.AllowedTools, &server.AllowedTools) != nil {
			if err := json.Unmarshal(tool.AllowedTools, &filter); err != nil {
				return aistudio.MCPServer{}, fmt.Errorf("mcp allowed_tools: %w", err)
			}
			server.AllowedTools = filter.ToolNames
			server.ReadOnly = filter.ReadOnly != nil && *filter.ReadOnly
		}
	}
	return server, nil
}

// buildResponsesObject 用与流式相同的输出累积构造非流式响应对象，prefix 排在输出首位
func buildResponsesObject(id string, created int64, request responsesRequest, result generationResult, prefix []map[string]any) (map[string]any, error) {
	writer := newResponsesWriter(nil, id, created, request)
	writer.prefix = prefix
	for _, event := range result.events {
		if err := writer.live(event); err != nil {
			return nil, err
		}
	}
	if err := writer.complete(true); err != nil {
		return nil, err
	}
	return writer.response(result), nil
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
		"max_tool_calls":       request.MaxToolCalls,
		"service_tier":         cmp.Or(request.ServiceTier, "auto"),
		"background":           request.Background,
		"usage":                nil,
	}
}

func rawJSONValue(raw json.RawMessage, defaultValue any) any {
	if !rawJSONConfigured(raw) {
		return defaultValue
	}
	var value any
	_ = json.Unmarshal(raw, &value)
	return value
}

// rawJSONConfigured 判断 JSON 字段是否给出了 null 以外的值
func rawJSONConfigured(raw json.RawMessage) bool {
	value := bytes.TrimSpace(raw)
	return len(value) > 0 && string(value) != "null"
}

// responseFunctionCall 按声明的工具类型把函数调用投影为 Responses 输出项，需要批准的 MCP 工具调用投影为 mcp_approval_request 项
func responseFunctionCall(call aistudio.FunctionCall, tools []responsesTool) map[string]any {
	if label, name, ok := mcpApprovalTarget(tools, call.Name); ok {
		return map[string]any{"id": "mcpr_" + call.ID, "type": "mcp_approval_request", "server_label": label, "name": name, "arguments": string(call.Arguments)}
	}
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
	case "computer_use_preview":
		item["type"], item["action"], item["pending_safety_checks"] = "computer_call", rawJSONValue(call.Arguments, map[string]any{}), []any{}
		return item
	case "computer":
		var arguments struct {
			Actions []any `json:"actions"`
		}
		_ = json.Unmarshal(call.Arguments, &arguments)
		item["type"], item["actions"], item["pending_safety_checks"] = "computer_call", arguments.Actions, []any{}
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

// namespacedName 返回命名空间中函数的声明名：默认命名空间 functions 中的函数与顶层函数相同，用原名，其余为 <命名空间>.<名>
func namespacedName(namespace, name string) string {
	if namespace == "" || namespace == "functions" {
		return name
	}
	return namespace + "." + name
}

// responsesNamespace 返回函数所属的命名空间工具名
func responsesNamespace(tools []responsesTool, name string) string {
	for _, tool := range tools {
		if tool.Type != "namespace" {
			continue
		}
		for _, inner := range tool.Tools {
			if namespacedName(tool.Name, inner.Name) == name {
				return tool.Name
			}
		}
	}
	return ""
}

func responseImageGenerationItem(responseID string, index int, media aistudio.Blob) (map[string]any, error) {
	if !strings.HasPrefix(media.MIME, "image/") {
		return nil, fmt.Errorf("Responses API cannot represent media %q without inline image data", media.MIME)
	}
	return map[string]any{
		"id":     fmt.Sprintf("ig_%s_%d", responseID, index),
		"type":   "image_generation_call",
		"status": "completed",
		"result": base64.StdEncoding.EncodeToString(media.Data),
	}, nil
}

func responsesUsage(usage *aistudio.Usage) map[string]any {
	return map[string]any{
		"input_tokens":  inputTokens(usage),
		"output_tokens": outputTokens(usage),
		"total_tokens":  usage.TotalTokens,
		"input_tokens_details": map[string]any{
			"cached_tokens": usage.CachedTokens, "cache_write_tokens": 0,
		},
		"output_tokens_details": map[string]any{
			"reasoning_tokens": usage.ReasoningTokens,
		},
	}
}

// responsesOutput 按事件生成顺序累积 Responses 输出项：思考为 reasoning 项，正文为 message 项，代码执行、搜索、图片与函数调用为各自的调用项
type responsesOutput struct {
	id        string
	request   responsesRequest
	tools     []responsesTool
	items     []map[string]any
	reasoning map[string]any
	message   map[string]any
	code      map[string]any
	texts     map[string]*strings.Builder
	starts    map[string]int
	queries   map[string]struct{}
	// search 是最后一个 search 动作，请求 include 来源时写入其后到达的搜索来源
	search map[string]any
	// pages 是已写出 open_page 动作的网址
	pages     map[string]struct{}
	text      int
	codes     int
	images    int
	searches  int
	citations []aistudio.Citation
	// mcpCalls 是等待结果的上游 MCP 工具调用
	mcpCalls map[string]aistudio.ServerToolStep
}

// itemID 返回新输出项的 ID：同类首项为前缀加响应 ID，之后的项再加序号
func (o *responsesOutput) itemID(prefix string) string {
	if !slices.ContainsFunc(o.items, func(item map[string]any) bool { return item["id"] == prefix+o.id }) {
		return prefix + o.id
	}
	return fmt.Sprintf("%s%s_%d", prefix, o.id, len(o.items))
}

// add 处理一个事件，返回新增的输出项；reasoning 与正文接在同类项之后时并入该项，否则开始新项；搜索调用或搜索结果中出现新查询、读取了新网址时新增一个 web_search_call 项
func (o *responsesOutput) add(event aistudio.Event) ([]map[string]any, error) {
	var added []map[string]any
	push := func(item map[string]any) {
		o.items = append(o.items, item)
		added = append(added, item)
	}
	isLast := func(item map[string]any) bool {
		return item != nil && o.items[len(o.items)-1]["id"] == item["id"]
	}
	write := func(item map[string]any, part map[string]any, text string) {
		builder := o.texts[item["id"].(string)]
		builder.WriteString(text)
		part["text"] = builder.String()
	}
	switch event.Kind {
	case aistudio.EventReasoning:
		if event.Text == "" {
			break
		}
		if !isLast(o.reasoning) {
			o.reasoning = map[string]any{"id": o.itemID("rs_"), "type": "reasoning", "status": "completed", "summary": []any{map[string]any{"type": "summary_text", "text": ""}}}
			o.texts[o.reasoning["id"].(string)] = &strings.Builder{}
			push(o.reasoning)
		}
		write(o.reasoning, o.reasoning["summary"].([]any)[0].(map[string]any), event.Text)
	case aistudio.EventText:
		if !isLast(o.message) {
			o.message = map[string]any{"id": o.itemID("msg_"), "type": "message", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "", "annotations": []any{}}}}
			o.texts[o.message["id"].(string)] = &strings.Builder{}
			o.starts[o.message["id"].(string)] = o.text
			push(o.message)
		}
		write(o.message, o.message["content"].([]any)[0].(map[string]any), event.Text)
		o.text += len(event.Text)
	case aistudio.EventToolCall:
		call := *event.FunctionCall
		if len(call.Arguments) == 0 {
			call.Arguments = json.RawMessage(`{}`)
		}
		if call.ThoughtSignature != "" && slices.Contains(o.request.Include, "reasoning.encrypted_content") {
			if len(o.items) > 0 && o.items[len(o.items)-1]["type"] == "reasoning" && o.items[len(o.items)-1]["encrypted_content"] == nil {
				o.items[len(o.items)-1]["encrypted_content"] = call.ThoughtSignature
			} else {
				push(map[string]any{"id": "rs_" + call.ID, "type": "reasoning", "status": "completed", "summary": []any{}, "encrypted_content": call.ThoughtSignature})
			}
		}
		push(responseFunctionCall(call, o.tools))
	case aistudio.EventMedia:
		if event.InlineData == nil {
			break
		}
		media := *event.InlineData
		if tool := imageGenerationTool(o.request.Tools); tool != nil && strings.HasPrefix(media.MIME, "image/") {
			size, _ := openAIImageSize(tool.Size)
			format, compression, _ := openAIImageEncoding(tool.OutputFormat, tool.OutputCompression)
			converted, _, err := fitImage(media, size, format, compression)
			if err != nil {
				return nil, err
			}
			media = converted
		}
		item, err := responseImageGenerationItem(o.id, o.images, media)
		if err != nil {
			return nil, err
		}
		o.images++
		push(item)
	case aistudio.EventExecutableCode:
		o.code = map[string]any{
			"id": fmt.Sprintf("ci_%s_%d", o.id, o.codes), "type": "code_interpreter_call",
			"status": "incomplete", "code": event.ExecutableCode.Code, "container_id": "aistudio", "outputs": nil,
		}
		o.codes++
		push(o.code)
	case aistudio.EventCodeExecutionResult:
		if o.code == nil {
			break
		}
		result := event.CodeExecutionResult
		status, logs := "completed", result.Output
		if result.Outcome != "OUTCOME_OK" {
			status = "failed"
			if logs != "" {
				logs = "stderr:\n" + logs
			}
		}
		o.code["status"] = status
		if logs != "" {
			o.code["outputs"] = []any{map[string]any{"type": "logs", "logs": logs}}
		}
		o.code = nil
	case aistudio.EventServerToolCall:
		step := event.ToolCall
		switch {
		case step.Type == mcpToolType:
			o.mcpCalls[step.ID] = *step
		case groundingToolTypes[step.Type] == "google_search" && responsesUsesWebSearch(o.request.Tools):
			o.addSearch(searchStepQueries(*step), nil, push)
		}
	case aistudio.EventServerToolResponse:
		step := event.ToolResponse
		call, ok := o.mcpCalls[step.ID]
		if !ok {
			break
		}
		delete(o.mcpCalls, step.ID)
		server, name := mcpToolName(call.Name)
		arguments := "{}"
		if len(call.Data) > 0 {
			arguments = string(call.Data)
		}
		item := map[string]any{
			"id": o.itemID("mcp_"), "type": "mcp_call", "status": "completed", "server_label": server, "name": name,
			"arguments": arguments, "output": mcpOutputText(step.Data), "error": nil, "approval_request_id": nil,
		}
		if failure := mcpCallError(step.Data); failure != nil {
			item["status"], item["output"], item["error"] = "failed", nil, failure
		}
		push(item)
	case aistudio.EventCitation:
		o.citations = append(o.citations, *event.Citation)
	case aistudio.EventGrounding:
		o.citations = append(o.citations, groundingCitations(*event.Grounding)...)
		for _, page := range event.Grounding.URLContext {
			if _, exists := o.pages[page.RetrievedURL]; exists {
				continue
			}
			o.pages[page.RetrievedURL] = struct{}{}
			status := "completed"
			if page.Status != "URL_RETRIEVAL_STATUS_SUCCESS" {
				status = "failed"
			}
			push(map[string]any{"id": fmt.Sprintf("ws_%s_%d", o.id, o.searches), "type": "web_search_call", "status": status, "action": map[string]any{"type": "open_page", "url": page.RetrievedURL}})
			o.searches++
		}
		if responsesUsesWebSearch(o.request.Tools) {
			o.addSearch(slices.Concat(event.Grounding.WebSearchQueries, event.Grounding.ImageSearchQueries), event.Grounding, push)
		}
	}
	return added, nil
}

// addSearch 为未出现过的查询新增一个 search 动作的 web_search_call 项，grounding 的来源按 URL 去重并入最后一个 search 动作
func (o *responsesOutput) addSearch(queries []string, grounding *aistudio.GroundingMetadata, push func(map[string]any)) {
	var fresh []any
	for _, query := range queries {
		if _, exists := o.queries[query]; !exists {
			o.queries[query] = struct{}{}
			fresh = append(fresh, query)
		}
	}
	if len(fresh) > 0 {
		o.search = map[string]any{"type": "search", "query": fresh[0], "queries": fresh}
		push(map[string]any{"id": fmt.Sprintf("ws_%s_%d", o.id, o.searches), "type": "web_search_call", "status": "completed", "action": o.search})
		o.searches++
	}
	if grounding == nil || o.search == nil {
		return
	}
	sources, _ := o.search["sources"].([]map[string]any)
	for _, source := range responseWebSearchSources(*grounding) {
		if !slices.ContainsFunc(sources, func(known map[string]any) bool { return known["url"] == source["url"] }) {
			sources = append(sources, source)
		}
	}
	o.search["sources"] = sources
}

// searchStepQueries 返回网页或图片搜索调用步骤参数中的查询
func searchStepQueries(step aistudio.ServerToolStep) []string {
	var args struct {
		Queries []string `json:"queries"`
	}
	json.Unmarshal(step.Data, &args)
	return args.Queries
}

// annotate 写入 message 项正文的 url_citation 注释并返回：有区间的引用按全文字节区间归入所在项并换算为字符下标，没有区间的引用只写入最后结束的 message 项
func (o *responsesOutput) annotate(message map[string]any, final bool) []any {
	part := message["content"].([]any)[0].(map[string]any)
	text := part["text"].(string)
	spans := []textSpan{{source: o.starts[message["id"].(string)], length: len(text)}}
	annotations := []any{}
	for _, citation := range o.citations {
		start, end, ok := 0, 0, final && citation.End == 0
		if citation.End > 0 {
			start, end, ok = spanCitation(text, spans, citation)
		}
		if ok {
			annotations = append(annotations, map[string]any{
				"type": "url_citation", "start_index": start, "end_index": end, "title": citation.Title, "url": citation.URL,
			})
		}
	}
	part["annotations"] = annotations
	return annotations
}

// responsesStreamWriter 把规范事件写成 Responses 流；w 为空时只累积输出项，非流式响应由它生成与流式相同的 output
type responsesStreamWriter struct {
	w        http.ResponseWriter
	sequence atomic.Int64
	id       string
	created  int64
	request  responsesRequest
	output   *responsesOutput
	open     []map[string]any
	code     map[string]any
	// searches 是请求 include 来源、来源尚未到达的 search 动作 web_search_call 项，收到 grounding 或生成结束时完成
	searches []map[string]any
	// prefix 是生成前得到的 compaction 项与批准后执行的 mcp_call 项，排在输出首位
	prefix []map[string]any
}

// outputPrefix 返回排在输出首位的 compaction 项与批准后执行的 mcp_call 项
func outputPrefix(compaction map[string]any, prepared responsesPreparation) []map[string]any {
	if compaction == nil {
		return prepared.approved
	}
	return append([]map[string]any{compaction}, prepared.approved...)
}

// newResponsesWriter 返回 Responses 输出写出器
func newResponsesWriter(w http.ResponseWriter, id string, created int64, request responsesRequest) *responsesStreamWriter {
	additional, _ := responsesInputConfig(request.inputItems())
	return &responsesStreamWriter{
		w: w, id: id, created: created, request: request,
		output: &responsesOutput{
			id: id, request: request, tools: slices.Concat(request.Tools, additional), texts: make(map[string]*strings.Builder),
			starts: make(map[string]int), queries: make(map[string]struct{}), pages: make(map[string]struct{}), mcpCalls: make(map[string]aistudio.ServerToolStep),
		},
	}
}

func (s *server) streamResponses(w http.ResponseWriter, r *http.Request, request responsesRequest, prepared responsesPreparation, id string, created int64, events <-chan aistudio.Event, compaction map[string]any) {
	stream, err := openStream(w, sseHeartbeat)
	defer stream.stop()
	if err != nil {
		return
	}
	writer := newResponsesWriter(stream, id, created, request)
	if err := writer.emit("response.created", map[string]any{"response": responseShell(id, created, "in_progress", request)}); err != nil {
		return
	}
	if err := writer.emit("response.in_progress", map[string]any{"response": responseShell(id, created, "in_progress", request)}); err != nil {
		return
	}
	stream.setFrame(writer.progressFrame)
	_ = s.streamOutput(r.Context(), writer, prepared, events, compaction, func(err error) bool { return shouldWriteRequestError(r, err) })
}

// streamOutput 写出输出项事件与终止事件并保存最终响应，report 决定生成出错时是否写出 response.failed；返回生成或写出的错误
func (s *server) streamOutput(ctx context.Context, writer *responsesStreamWriter, prepared responsesPreparation, events <-chan aistudio.Event, compaction map[string]any, report func(error) bool) error {
	writer.prefix = outputPrefix(compaction, prepared)
	for index, item := range writer.prefix {
		if item["type"] == "mcp_call" {
			if err := writer.mcpCall(item, index); err != nil {
				return err
			}
			continue
		}
		if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": item}); err != nil {
			return err
		}
		if err := writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": item}); err != nil {
			return err
		}
	}
	result, err := consumeStreamEvents(ctx, events, writer.live)
	if err != nil {
		if report(err) {
			_ = writer.failed(err)
		}
		return err
	}
	completeErr := writer.complete(true)
	response := writer.response(result)
	if writer.request.storesResponse() {
		s.storeResponse(writer.id, writer.request, prepared, result, response)
	}
	if completeErr != nil {
		_ = writer.failed(completeErr)
		return completeErr
	}
	if err := writer.terminal(response); err != nil {
		_ = writer.failed(err)
		return err
	}
	return nil
}

// progressFrame 返回流静默时重发的 response.in_progress 帧，序号与其他事件连续递增，外壳不回显请求字段
func (writer *responsesStreamWriter) progressFrame() string {
	shell := responseShell(writer.id, writer.created, "in_progress", responsesRequest{Model: writer.request.Model})
	data, _ := json.Marshal(officialOrder("responses", map[string]any{"type": "response.in_progress", "sequence_number": writer.sequence.Add(1) - 1, "response": shell}))
	return "event: response.in_progress\ndata: " + string(data) + "\n\n"
}

func (writer *responsesStreamWriter) emit(eventType string, payload map[string]any) error {
	if item, ok := payload["item"].(map[string]any); ok {
		payload["item"] = responseIncludeView(item, writer.request.Include)
	}
	if response, ok := payload["response"].(map[string]any); ok {
		payload["response"] = responseView(response, writer.request.Include)
	}
	payload["type"] = eventType
	payload["sequence_number"] = writer.sequence.Add(1) - 1
	if writer.w == nil {
		return nil
	}
	return writeSSE(writer.w, eventType, officialOrder("responses", payload))
}

// index 返回输出项在响应 output 中的位置
func (writer *responsesStreamWriter) index(item map[string]any) int {
	return len(writer.prefix) + slices.IndexFunc(writer.output.items, func(candidate map[string]any) bool { return candidate["id"] == item["id"] })
}

// items 返回已累积的输出项，prefix 排在首位
func (writer *responsesStreamWriter) items() []any {
	output := make([]any, 0, len(writer.prefix)+len(writer.output.items))
	for _, item := range writer.prefix {
		output = append(output, item)
	}
	for _, item := range writer.output.items {
		output = append(output, item)
	}
	return output
}

// live 写出一个事件，新输出项开始前先完成仍打开的项，reasoning 与 message 项保持打开到下一个输出项或结束
func (writer *responsesStreamWriter) live(event aistudio.Event) error {
	added, err := writer.output.add(event)
	if err != nil {
		return err
	}
	for _, item := range added {
		if err := writer.added(item); err != nil {
			return err
		}
	}
	switch event.Kind {
	case aistudio.EventReasoning:
		if event.Text == "" {
			return nil
		}
		return writer.emit("response.reasoning_summary_text.delta", map[string]any{
			"item_id": writer.output.reasoning["id"], "output_index": writer.index(writer.output.reasoning), "summary_index": 0, "delta": event.Text,
		})
	case aistudio.EventText:
		return writer.emit("response.output_text.delta", map[string]any{
			"item_id": writer.output.message["id"], "output_index": writer.index(writer.output.message), "content_index": 0, "delta": event.Text, "logprobs": []any{},
		})
	case aistudio.EventGrounding:
		return writer.finishSearches()
	case aistudio.EventCodeExecutionResult:
		if writer.code == nil || writer.output.code != nil {
			return nil
		}
		code := writer.code
		writer.code = nil
		index := writer.index(code)
		if err := writer.emit("response.code_interpreter_call.completed", map[string]any{"item_id": code["id"], "output_index": index}); err != nil {
			return err
		}
		return writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": code})
	}
	return nil
}

// added 完成仍打开的项后写出新输出项的 added 事件；调用项与只带签名的 reasoning 项随即写出内容与 done 事件
func (writer *responsesStreamWriter) added(item map[string]any) error {
	if err := writer.complete(false); err != nil {
		return err
	}
	index := writer.index(item)
	switch {
	case item["type"] == "reasoning" && len(item["summary"].([]any)) > 0:
		pending := map[string]any{"id": item["id"], "type": "reasoning", "status": "in_progress", "summary": []any{}}
		if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": pending}); err != nil {
			return err
		}
		writer.open = append(writer.open, item)
		return writer.emit("response.reasoning_summary_part.added", map[string]any{
			"item_id": item["id"], "output_index": index, "summary_index": 0,
			"part": map[string]any{"type": "summary_text", "text": ""},
		})
	case item["type"] == "message":
		pending := map[string]any{"id": item["id"], "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}
		if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": pending}); err != nil {
			return err
		}
		writer.open = append(writer.open, item)
		return writer.emit("response.content_part.added", map[string]any{
			"item_id": item["id"], "output_index": index, "content_index": 0,
			"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
		})
	case item["type"] == "reasoning":
		pending := maps.Clone(item)
		pending["status"] = "in_progress"
		if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": pending}); err != nil {
			return err
		}
		return writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": item})
	case item["type"] == "code_interpreter_call":
		pending := map[string]any{"id": item["id"], "type": "code_interpreter_call", "status": "in_progress", "code": "", "container_id": "aistudio", "outputs": nil}
		if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": pending}); err != nil {
			return err
		}
		if err := writer.emit("response.code_interpreter_call.in_progress", map[string]any{"item_id": item["id"], "output_index": index}); err != nil {
			return err
		}
		if code := item["code"].(string); code != "" {
			if err := writer.emit("response.code_interpreter_call_code.delta", map[string]any{"item_id": item["id"], "output_index": index, "delta": code}); err != nil {
				return err
			}
			if err := writer.emit("response.code_interpreter_call_code.done", map[string]any{"item_id": item["id"], "output_index": index, "code": code}); err != nil {
				return err
			}
		}
		writer.code = item
		return writer.emit("response.code_interpreter_call.interpreting", map[string]any{"item_id": item["id"], "output_index": index})
	case item["type"] == "web_search_call":
		pending := map[string]any{"id": item["id"], "type": "web_search_call", "status": "in_progress", "action": item["action"]}
		if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": pending}); err != nil {
			return err
		}
		for _, stage := range []string{"in_progress", "searching"} {
			if err := writer.emit("response.web_search_call."+stage, map[string]any{"item_id": item["id"], "output_index": index}); err != nil {
				return err
			}
		}
		action, _ := item["action"].(map[string]any)
		if _, sourced := action["sources"]; action["type"] == "search" && !sourced && slices.Contains(writer.request.Include, "web_search_call.action.sources") {
			writer.searches = append(writer.searches, item)
			return nil
		}
		return writer.finishSearch(item, index)
	case item["type"] == "mcp_call":
		return writer.mcpCall(item, index)
	case item["type"] == "mcp_approval_request":
		if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": item}); err != nil {
			return err
		}
		return writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": item})
	case item["type"] == "image_generation_call":
		pending := map[string]any{"id": item["id"], "type": "image_generation_call", "status": "in_progress", "result": nil}
		if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": pending}); err != nil {
			return err
		}
		if err := writer.emit("response.image_generation_call.in_progress", map[string]any{"item_id": item["id"], "output_index": index}); err != nil {
			return err
		}
		if err := writer.emit("response.image_generation_call.completed", map[string]any{"item_id": item["id"], "output_index": index}); err != nil {
			return err
		}
		return writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": item})
	}
	return writer.toolCall(item, index)
}

// mcpCall 写出 mcp_call 项的 added、参数、进行中与结束状态和 done 事件
func (writer *responsesStreamWriter) mcpCall(item map[string]any, index int) error {
	pending := maps.Clone(item)
	pending["status"], pending["arguments"], pending["output"], pending["error"] = "in_progress", "", nil, nil
	if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": pending}); err != nil {
		return err
	}
	for _, event := range []map[string]any{
		{"type": "response.mcp_call_arguments.delta", "delta": item["arguments"]},
		{"type": "response.mcp_call_arguments.done", "arguments": item["arguments"]},
		{"type": "response.mcp_call.in_progress"},
		{"type": "response.mcp_call." + item["status"].(string)},
	} {
		eventType := event["type"].(string)
		delete(event, "type")
		event["item_id"], event["output_index"] = item["id"], index
		if err := writer.emit(eventType, event); err != nil {
			return err
		}
	}
	return writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": item})
}

// toolCall 写出函数调用项的 added、参数增量与 done 事件
func (writer *responsesStreamWriter) toolCall(completed map[string]any, index int) error {
	id := completed["id"]
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
		if err := writer.emit("response.function_call_arguments.delta", map[string]any{
			"item_id": id, "output_index": index, "delta": completed["arguments"],
		}); err != nil {
			return err
		}
		if err := writer.emit("response.function_call_arguments.done", map[string]any{
			"item_id": id, "output_index": index, "arguments": completed["arguments"], "name": completed["name"],
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

// complete 结束仍打开的 reasoning 与 message 项；final 为真时表示响应结束，同时结束没有执行结果的代码执行项
func (writer *responsesStreamWriter) complete(final bool) error {
	open := writer.open
	writer.open = nil
	for _, item := range open {
		index := writer.index(item)
		if item["type"] == "reasoning" {
			part := item["summary"].([]any)[0]
			if err := writer.emit("response.reasoning_summary_text.done", map[string]any{
				"item_id": item["id"], "output_index": index, "summary_index": 0, "text": part.(map[string]any)["text"],
			}); err != nil {
				return err
			}
			if err := writer.emit("response.reasoning_summary_part.done", map[string]any{
				"item_id": item["id"], "output_index": index, "summary_index": 0, "part": part,
			}); err != nil {
				return err
			}
		} else {
			for position, annotation := range writer.output.annotate(item, final) {
				if err := writer.emit("response.output_text.annotation.added", map[string]any{
					"item_id": item["id"], "output_index": index, "content_index": 0, "annotation_index": position, "annotation": annotation,
				}); err != nil {
					return err
				}
			}
			part := item["content"].([]any)[0]
			if err := writer.emit("response.output_text.done", map[string]any{
				"item_id": item["id"], "output_index": index, "content_index": 0, "text": part.(map[string]any)["text"], "logprobs": []any{},
			}); err != nil {
				return err
			}
			if err := writer.emit("response.content_part.done", map[string]any{
				"item_id": item["id"], "output_index": index, "content_index": 0, "part": part,
			}); err != nil {
				return err
			}
		}
		if err := writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": item}); err != nil {
			return err
		}
	}
	if !final {
		return nil
	}
	if err := writer.finishSearches(); err != nil {
		return err
	}
	if writer.code != nil {
		code := writer.code
		writer.code = nil
		return writer.emit("response.output_item.done", map[string]any{"output_index": writer.index(code), "item": code})
	}
	return nil
}

// finishSearches 完成等待来源的搜索项
func (writer *responsesStreamWriter) finishSearches() error {
	searches := writer.searches
	writer.searches = nil
	for _, item := range searches {
		if err := writer.finishSearch(item, writer.index(item)); err != nil {
			return err
		}
	}
	return nil
}

// finishSearch 写出搜索项的 completed 与 done 事件，失败的搜索项只写 done
func (writer *responsesStreamWriter) finishSearch(item map[string]any, index int) error {
	if item["status"] != "failed" {
		if err := writer.emit("response.web_search_call.completed", map[string]any{"item_id": item["id"], "output_index": index}); err != nil {
			return err
		}
	}
	return writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": item})
}

// response 返回携带已累积输出项的完整响应对象
func (writer *responsesStreamWriter) response(result generationResult) map[string]any {
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
	response := responseShell(writer.id, writer.created, status, writer.request)
	response["service_tier"] = openAIResponseServiceTier(result.usage)
	if status == "completed" {
		response["completed_at"] = time.Now().Unix()
	}
	if status == "incomplete" {
		response["incomplete_details"] = map[string]any{"reason": incompleteReason}
	}
	response["output"] = writer.items()
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
	return response
}

// terminal 发送携带完整响应对象的终止事件
func (writer *responsesStreamWriter) terminal(response map[string]any) error {
	eventType := "response.completed"
	if response["status"] == "incomplete" {
		eventType = "response.incomplete"
	}
	return writer.emit(eventType, map[string]any{"response": response})
}

// failed 发送 response.failed：output 为已写出的输出项，error.code 取 Responses 错误枚举
func (writer *responsesStreamWriter) failed(err error) error {
	response := responseShell(writer.id, writer.created, "failed", writer.request)
	response["output"] = writer.items()
	response["error"] = map[string]any{"code": responsesErrorCode(err), "message": publicErrorMessage(statusFromError(err), err.Error())}
	return writer.emit("response.failed", map[string]any{"response": response})
}

// responsesErrorCode 返回 Responses 错误对象的官方枚举 code
func responsesErrorCode(err error) string {
	switch statusFromError(err) {
	case http.StatusTooManyRequests:
		return "rate_limit_exceeded"
	case http.StatusBadRequest:
		return "invalid_prompt"
	}
	return "server_error"
}
