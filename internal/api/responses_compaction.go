package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

const (
	// compactionRecentTokens 是压缩项原样保留的最近条目 token 预算
	compactionRecentTokens = 20000
	// compactionRetainedTokens 是 V1 输出保留 user 消息的 token 预算
	compactionRetainedTokens = 64000
	// compactionImageTokens 是预算中每张图片的估算 token 数
	compactionImageTokens = 765
)

// compactionInstructions 是追加在待摘要历史末尾的 user 轮，填入摘要提示词
const compactionInstructions = "The conversation above is the complete history to summarize. The API replaces every message in this request with your summary; no message you leave out survives.\n\n%s\n\nYou MUST NOT call any tools while writing the summary; respond with the summary text only."

// compactionSummaryPrompt 是首次压缩的结构化摘要提示词
const compactionSummaryPrompt = `You MUST summarize the conversation above into a structured handoff summary for another LLM to resume the task.

IMPORTANT: If the conversation ends with an unanswered question or a request awaiting user response (e.g., "Please run command and paste output"), you MUST preserve that exact question/request.

You MUST use this format (sections can be omitted if not applicable):

## Goal
[User goals; list multiple if session covers different tasks.]

## Constraints & Preferences
- [Constraints or requirements mentioned]

## Progress

### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Current work]

### Blocked
- [Issues preventing progress]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [Ordered list of next actions]

## Critical Context
- [Important data, pending questions, references]

## Additional Notes
[Anything else important not covered above]

You MUST output only the structured summary; you NEVER include extra text.

Sections MUST be kept concise. You MUST preserve exact file paths, function names, error messages, and relevant tool outputs or command results. You MUST include repository state changes (branch, uncommitted changes) if mentioned.`

// compactionUpdatePrompt 是已有摘要时合并新条目的增量摘要提示词
const compactionUpdatePrompt = `Update existing handoff summary in <previous-summary> tags from new messages above for another LLM to resume.

MUST:
- preserve all previous-summary information; add new progress, decisions, context.
- Progress: move completed "In Progress" items to "Done".
- update "Next Steps" for completed work.
- preserve exact file paths, function names, error messages.
- MAY remove irrelevant content.
- If new messages end with an unanswered user question/request: add it to Critical Context; replace any previous pending question if answered.
- output only the structured summary; NEVER extra text.
- keep sections concise.
- preserve relevant tool outputs/command results.
- include mentioned repository state changes (branch, uncommitted changes).

Format (omit inapplicable sections):

## Goal
[Preserve existing goals; add new ones if task expanded]

## Constraints & Preferences
- [Preserve existing; add new ones discovered]

## Progress

### Done
- [x] [Include previously done and newly completed items]

### In Progress
- [ ] [Current work—update based on progress]

### Blocked
- [Current blockers—remove if resolved]

## Key Decisions
- **[Decision]**: [Brief rationale] (preserve all previous, add new)

## Next Steps
1. [Update based on current state]

## Critical Context
- [Preserve important context; add new if needed]

## Additional Notes
[Other important info not fitting above]`

// compactionSummaryContext 是摘要还原为 user 消息时的文本模板
const compactionSummaryContext = "Prior model work/tool state available.\nMUST build on prior work; NEVER duplicate prior work.\n\n<summary>\n%s\n</summary>"

// compactionUserPrefixes 是客户端注入的上下文 user 消息前缀，V1 保留 user 消息时跳过
var compactionUserPrefixes = []string{"<environment_context>", "<user_instructions>", "<additional_context>", "<skills", "<token_budget>", "<model_switch>"}

// compactionPayload 是 compaction 项 encrypted_content 中的明文 JSON：前文摘要与最近条目原文
type compactionPayload struct {
	Version int               `json:"v"`
	Summary string            `json:"summary"`
	Items   []json.RawMessage `json:"items"`
}

// compactionWindow 是按最后一个 compaction 项拆分的输入：之前保留的条目、摘要、原样条目与之后的新条目
type compactionWindow struct {
	before  []json.RawMessage
	summary string
	recent  []json.RawMessage
	after   []json.RawMessage
}

// splitCompaction 按最后一个 compaction 项拆分输入，丢弃更早的 compaction 项与之前和原样条目重复的 user 消息
func splitCompaction(items []json.RawMessage) (compactionWindow, error) {
	last := -1
	for index, item := range items {
		if responsesItemType(item) == "compaction" {
			last = index
		}
	}
	if last < 0 {
		return compactionWindow{after: items}, nil
	}
	var item responsesInputItem
	var payload compactionPayload
	if json.Unmarshal(items[last], &item) != nil || json.Unmarshal([]byte(item.EncryptedContent), &payload) != nil || payload.Version != 1 {
		return compactionWindow{}, fmt.Errorf("compaction: encrypted_content was not produced by this service")
	}
	pending := make(map[string]int)
	for _, recent := range payload.Items {
		if text, ok := userMessageText(recent); ok {
			pending[text]++
		}
	}
	window := compactionWindow{summary: payload.Summary, recent: payload.Items, after: items[last+1:]}
	for index := last - 1; index >= 0; index-- {
		if responsesItemType(items[index]) == "compaction" {
			continue
		}
		if text, ok := userMessageText(items[index]); ok && pending[text] > 0 {
			pending[text]--
			continue
		}
		window.before = append(window.before, items[index])
	}
	slices.Reverse(window.before)
	return window, nil
}

// items 返回展开后的输入条目：之前保留的条目、摘要 user 消息、原样条目与之后的新条目
func (window compactionWindow) items() []json.RawMessage {
	items := slices.Clone(window.before)
	if window.summary != "" {
		items = append(items, userTextMessage(fmt.Sprintf(compactionSummaryContext, window.summary)))
	}
	return slices.Concat(items, window.recent, window.after)
}

// userTextMessage 返回只含一段文本的 user 消息条目
func userTextMessage(text string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": text}}})
	return raw
}

// userMessageText 返回 user 消息条目的全部文本
func userMessageText(raw json.RawMessage) (string, bool) {
	var message struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &message) != nil || message.Type != "" && message.Type != "message" || message.Role != "user" {
		return "", false
	}
	var text string
	if json.Unmarshal(message.Content, &text) == nil {
		return text, true
	}
	var parts []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(message.Content, &parts)
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n"), true
}

// compactionItemTokens 按每 4 字节 1 token 估算条目大小，消息只计文本与图片，其他条目不计 encrypted_content
func compactionItemTokens(raw json.RawMessage) int {
	var message struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &message) == nil && (message.Type == "" || message.Type == "message") {
		var text string
		if json.Unmarshal(message.Content, &text) == nil {
			return (len(text) + 3) / 4
		}
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(message.Content, &parts)
		tokens := 0
		for _, part := range parts {
			if part.Type == "input_image" {
				tokens += compactionImageTokens
			} else {
				tokens += (len(part.Text) + 3) / 4
			}
		}
		return tokens
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return (len(raw) + 3) / 4
	}
	delete(object, "encrypted_content")
	encoded, _ := json.Marshal(object)
	return (len(encoded) + 3) / 4
}

// compactionItemSide 把条目分为 user 消息、工具输出、指令与模型侧条目
func compactionItemSide(raw json.RawMessage) string {
	var head struct {
		Type string `json:"type"`
		Role string `json:"role"`
	}
	_ = json.Unmarshal(raw, &head)
	switch {
	case (head.Type == "" || head.Type == "message") && head.Role == "user":
		return "user"
	case (head.Type == "" || head.Type == "message") && (head.Role == "system" || head.Role == "developer"):
		return "instruction"
	case strings.HasSuffix(head.Type, "_output") || head.Type == "mcp_approval_response":
		return "output"
	}
	return "model"
}

// compactionCut 返回原样保留部分的起点：从新到旧按完整的模型侧条目组与 user 消息累计，最新一组总是保留，切点不落在工具输出上
func compactionCut(items []json.RawMessage) int {
	cut, tokens := -1, 0
	for index := len(items) - 1; index >= 0; index-- {
		tokens += compactionItemTokens(items[index])
		side := compactionItemSide(items[index])
		if side != "user" && (side != "model" || index > 0 && compactionItemSide(items[index-1]) == "model") {
			continue
		}
		if cut >= 0 && tokens > compactionRecentTokens {
			break
		}
		cut = index
	}
	return max(cut, 0)
}

// retainedUserMessages 从新到旧保留 user 消息直到预算用完，跨预算的一条保留首尾文本并标出截去的 token 数
func retainedUserMessages(items []json.RawMessage) []json.RawMessage {
	remaining := compactionRetainedTokens
	var retained []json.RawMessage
	for index := len(items) - 1; index >= 0 && remaining > 0; index-- {
		text, ok := userMessageText(items[index])
		if !ok || slices.ContainsFunc(compactionUserPrefixes, func(prefix string) bool {
			return strings.HasPrefix(strings.ToLower(strings.TrimSpace(text)), prefix)
		}) {
			continue
		}
		tokens := max(compactionItemTokens(items[index]), 1)
		if tokens <= remaining {
			retained = append(retained, items[index])
			remaining -= tokens
			continue
		}
		retained = append(retained, userTextMessage(truncateMiddle(text, remaining)))
		remaining = 0
	}
	slices.Reverse(retained)
	return retained
}

// truncateMiddle 把文本截到约 tokens 个 token，保留首尾并在中间标出截去的 token 数
func truncateMiddle(text string, tokens int) string {
	limit := tokens * 4
	if len(text) <= limit {
		return text
	}
	marker := fmt.Sprintf("…%d tokens truncated…", max(1, (len(text)+3)/4-tokens))
	side := max(1, (limit-len(marker))/2)
	if limit <= len(marker)+2 {
		return strings.ToValidUTF8(text[:limit], "")
	}
	return strings.ToValidUTF8(text[:side], "") + marker + strings.ToValidUTF8(text[len(text)-side:], "")
}

// compactResponses 摘要原样保留部分之前的历史，返回 compaction 项、原样保留部分之前的 user 消息、其中的 developer 与 system 消息和摘要用量
func (s *server) compactResponses(ctx context.Context, request responsesRequest, previous responseHistory, id string) (map[string]any, []json.RawMessage, []json.RawMessage, *aistudio.Usage, error) {
	var items []json.RawMessage
	for _, item := range slices.Concat(previous.Items, responsesInputItems(request.Input, id)) {
		items = append(items, item.Raw)
	}
	if len(items) > 0 && responsesItemType(items[len(items)-1]) == "compaction_trigger" {
		items = items[:len(items)-1]
	}
	window, err := splitCompaction(items)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	work := slices.Concat(window.recent, window.after)
	cut := compactionCut(work)
	summary := window.summary
	var usage *aistudio.Usage
	if conversation := conversationItems(work[:cut]); len(conversation) > 0 {
		if summary, usage, err = s.compactionSummary(ctx, request, conversation, window.summary, id); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	payload, _ := json.Marshal(compactionPayload{Version: 1, Summary: summary, Items: work[cut:]})
	item := map[string]any{"id": "cmp_" + id, "type": "compaction", "encrypted_content": string(payload)}
	compacted := slices.Concat(window.before, work[:cut])
	instructions := slices.DeleteFunc(slices.Clone(compacted), func(item json.RawMessage) bool { return !instructionItem(item) })
	return item, retainedUserMessages(compacted), instructions, usage, nil
}

// instructionItem 判断条目是否为 developer 或 system 消息
func instructionItem(item json.RawMessage) bool {
	var head struct {
		Type string `json:"type"`
		Role string `json:"role"`
	}
	_ = json.Unmarshal(item, &head)
	return (head.Type == "message" || head.Type == "") && (head.Role == "developer" || head.Role == "system")
}

// conversationItems 返回去掉 developer 与 system 消息后的条目，客户端每次请求都重新发送这些指令
func conversationItems(items []json.RawMessage) []json.RawMessage {
	return slices.DeleteFunc(slices.Clone(items), instructionItem)
}

// compactionSummary 以请求的模型、指令与工具非流式生成摘要，工具调用关闭，旧摘要作为首个 user 轮，摘要提示词作为末尾 user 轮
func (s *server) compactionSummary(ctx context.Context, request responsesRequest, items []json.RawMessage, previous, id string) (string, *aistudio.Usage, error) {
	prompt := compactionSummaryPrompt
	if previous != "" {
		items = append([]json.RawMessage{userTextMessage("<previous-summary>\n" + previous + "\n</previous-summary>")}, items...)
		prompt = compactionUpdatePrompt
	}
	request.Input, _ = json.Marshal(items)
	generate, _, err := request.toGenerateRequest(id)
	if err != nil {
		return "", nil, err
	}
	if err := inlineRemoteMedia(ctx, generate.Contents); err != nil {
		return "", nil, err
	}
	generate.Contents = append(generate.Contents, aistudio.Content{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: fmt.Sprintf(compactionInstructions, prompt)}}})
	generate.Tools.ToolConfig.Mode = "none"
	generate.Unary = true
	events, err := s.service.Generate(ctx, generate)
	if err != nil {
		return "", nil, err
	}
	result, err := consumeEvents(ctx, events, nil)
	if err != nil {
		return "", nil, err
	}
	if result.finishReason != "stop" {
		return "", nil, fmt.Errorf("compaction summary finished with %s", result.finishReason)
	}
	return strings.TrimSpace(result.text.String()), result.usage, nil
}

// handleResponsesCompact 返回保留的 user 消息与一个 compaction 项
func (s *server) handleResponsesCompact(w http.ResponseWriter, r *http.Request) {
	var request responsesRequest
	if err := decodeJSON(r, &request); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.Model == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "model is required")
		return
	}
	id := newID("resp")
	prepared, err := s.prepareResponses(r.Context(), &request, id)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		}
		return
	}
	item, retained, _, usage, err := s.compactResponses(r.Context(), request, prepared.previous, id)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIRequestError(w, err)
		}
		return
	}
	output := make([]any, 0, len(retained)+1)
	for _, message := range retained {
		output = append(output, message)
	}
	response := map[string]any{"id": id, "object": "response.compaction", "created_at": time.Now().Unix(), "output": append(output, item)}
	if usage != nil {
		response["usage"] = responsesUsage(usage)
	}
	writeJSON(w, http.StatusOK, response)
}

// responsesCompactionTrigger 返回输入是否以 compaction_trigger 项结尾
func responsesCompactionTrigger(raw json.RawMessage) bool {
	var items []json.RawMessage
	return json.Unmarshal(raw, &items) == nil && len(items) > 0 && responsesItemType(items[len(items)-1]) == "compaction_trigger"
}

// handleCompactionTrigger 返回只含一个 compaction 项的响应，流式时依次发送 created、in_progress、compaction 项的 added 与 done、completed 事件
func (s *server) handleCompactionTrigger(w http.ResponseWriter, r *http.Request, request responsesRequest, prepared responsesPreparation, id string) {
	created := time.Now().Unix()
	var writer *responsesStreamWriter
	if request.Stream {
		if err := streamHeaders(w); err != nil {
			return
		}
		writer = newResponsesWriter(w, id, created, request)
		for _, eventType := range []string{"response.created", "response.in_progress"} {
			if err := writer.emit(eventType, map[string]any{"response": responseShell(id, created, "in_progress", request)}); err != nil {
				return
			}
		}
		if err := writer.emit("response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"id": "cmp_" + id, "type": "compaction", "encrypted_content": ""}}); err != nil {
			return
		}
	}
	item, retained, _, usage, err := s.compactResponses(r.Context(), request, prepared.previous, id)
	if err != nil {
		if !shouldWriteRequestError(r, err) {
			return
		}
		if writer != nil {
			_ = writer.failed(err)
		} else {
			writeOpenAIRequestError(w, err)
		}
		return
	}
	response := responseShell(id, created, "completed", request)
	response["completed_at"] = time.Now().Unix()
	response["output"] = []any{item}
	if usage != nil {
		response["usage"] = responsesUsage(usage)
	}
	if request.storesResponse() {
		s.storeCompaction(r.Context(), id, request, prepared, item, retained, response)
	}
	if writer == nil {
		writeJSON(w, http.StatusOK, response)
		return
	}
	if err := writer.emit("response.output_item.done", map[string]any{"output_index": 0, "item": item}); err != nil {
		return
	}
	_ = writer.emit("response.completed", map[string]any{"response": response})
}

// storeCompaction 保存压缩节点，续接上下文为保留的 user 消息与 compaction 项展开后的条目，不再接续前序节点
func (s *server) storeCompaction(ctx context.Context, id string, request responsesRequest, prepared responsesPreparation, item map[string]any, retained []json.RawMessage, response map[string]any) {
	encodedItem, _ := json.Marshal(item)
	window, _ := json.Marshal(append(slices.Clone(retained), encodedItem))
	contents, inlineInstructions, err := responsesContents(window)
	if err != nil || inlineRemoteMedia(ctx, contents) != nil {
		return
	}
	prepared.contents = contents
	prepared.inlineInstructions = append(slices.Clone(prepared.previous.InlineInstructions), inlineInstructions...)
	prepared.input = responsesInputItems(window, id)
	prepared.previous = responseHistory{}
	s.storeResponse(id, request, prepared, generationResult{}, response)
}
