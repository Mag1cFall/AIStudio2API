package api

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// anthropicClearedToolResult 是清理后的工具结果与参数占位文本
const anthropicClearedToolResult = "[cleared]"

// anthropicCompactionPrompt 是 compact_20260112 没有 instructions 时追加在历史末尾的摘要提示词
const anthropicCompactionPrompt = "You have written a partial transcript for the initial task above. Please write a summary of the transcript. The purpose of this summary is to provide continuity so you can continue to make progress towards solving the task in a future context, where the raw history above may not be accessible and will be replaced with this summary. Write down anything that would be helpful, including the state, next steps, learnings etc. You must wrap your summary in a <summary></summary> block."

// anthropicContextManagement 是 context_management 的上下文编辑策略
type anthropicContextManagement struct {
	Edits []anthropicContextEdit `json:"edits"`
}

// anthropicContextEdit 是一条上下文编辑策略，字段按 type 取用
type anthropicContextEdit struct {
	Type                 string               `json:"type"`
	Trigger              *anthropicEditAmount `json:"trigger"`
	Keep                 json.RawMessage      `json:"keep"`
	ClearAtLeast         *anthropicEditAmount `json:"clear_at_least"`
	ExcludeTools         []string             `json:"exclude_tools"`
	ClearToolInputs      json.RawMessage      `json:"clear_tool_inputs"`
	PauseAfterCompaction bool                 `json:"pause_after_compaction"`
	Instructions         string               `json:"instructions"`
}

// anthropicEditAmount 是触发条件、保留数量与最少清理量的类型与数值
type anthropicEditAmount struct {
	Type  string `json:"type"`
	Value int64  `json:"value"`
}

// anthropicCompaction 是本次请求生成的压缩摘要与其用量
type anthropicCompaction struct {
	summary string
	usage   *aistudio.Usage
	pause   bool
}

// anthropicContextResult 是上下文管理写入响应的内容：生效的编辑与本次压缩
type anthropicContextResult struct {
	requested  bool
	compacting bool
	applied    []map[string]any
	compaction *anthropicCompaction
}

// anthropicBlockHead 是定位思考块、工具调用与工具结果所需的块字段
type anthropicBlockHead struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolUseID string `json:"tool_use_id"`
}

// anthropicCompactedMessages 丢弃最后一个 compaction 块之前的全部内容，摘要成为首个 user 消息
func anthropicCompactedMessages(messages []anthropicMessage) []anthropicMessage {
	for index := len(messages) - 1; index >= 0; index-- {
		var blocks []json.RawMessage
		if json.Unmarshal(messages[index].Content, &blocks) != nil {
			continue
		}
		for position := len(blocks) - 1; position >= 0; position-- {
			var block struct {
				Type    string  `json:"type"`
				Content *string `json:"content"`
			}
			if json.Unmarshal(blocks[position], &block) != nil || block.Type != "compaction" {
				continue
			}
			var compacted []anthropicMessage
			if block.Content != nil && *block.Content != "" {
				summary, _ := json.Marshal(fmt.Sprintf(compactionSummaryContext, *block.Content))
				compacted = append(compacted, anthropicMessage{Role: "user", Content: summary})
			}
			if position+1 < len(blocks) {
				rest, _ := json.Marshal(blocks[position+1:])
				compacted = append(compacted, anthropicMessage{Role: messages[index].Role, Content: rest})
			}
			return append(compacted, messages[index+1:]...)
		}
	}
	return messages
}

// anthropicHistoryBlocks 把各消息的块数组拆为块，字符串内容为 nil
func anthropicHistoryBlocks(messages []anthropicMessage) [][]json.RawMessage {
	history := make([][]json.RawMessage, len(messages))
	for index, message := range messages {
		_ = json.Unmarshal(message.Content, &history[index])
	}
	return history
}

// withHistoryBlocks 返回把改写后的块写回后的消息副本
func withHistoryBlocks(messages []anthropicMessage, history [][]json.RawMessage) []anthropicMessage {
	edited := slices.Clone(messages)
	for index, blocks := range history {
		if blocks != nil {
			edited[index].Content, _ = json.Marshal(blocks)
		}
	}
	return edited
}

// anthropicInputTokens 用本地分词估算请求的输入 token 数
func (s *server) anthropicInputTokens(request anthropicRequest) (int64, error) {
	request.Messages = slices.Clone(request.Messages)
	if err := s.decodeAnthropicSearchHistory(&request); err != nil {
		return 0, err
	}
	generate, err := request.toGenerateRequest("")
	if err != nil {
		return 0, err
	}
	return <-aistudio.StartInputTokenEstimate(generate), nil
}

// applyAnthropicContextEdits 按顺序执行思考与工具结果清理，改写 request.Messages，返回请求的上下文管理结果
func (s *server) applyAnthropicContextEdits(request *anthropicRequest) (anthropicContextResult, error) {
	result := anthropicContextResult{requested: request.ContextManagement != nil, applied: []map[string]any{}}
	if request.ContextManagement == nil {
		return result, nil
	}
	for _, edit := range request.ContextManagement.Edits {
		var applied map[string]any
		var err error
		switch edit.Type {
		case "clear_thinking_20251015":
			applied, err = s.clearAnthropicThinking(request, edit)
		case "clear_tool_uses_20250919":
			applied, err = s.clearAnthropicToolUses(request, edit)
		case "compact_20260112":
			if edit.Trigger != nil && edit.Trigger.Type != "input_tokens" {
				return result, fmt.Errorf("context_management: compact_20260112 trigger type must be input_tokens")
			}
			result.compacting = true
		default:
			return result, fmt.Errorf("context_management: unsupported edit type %q", edit.Type)
		}
		if err != nil {
			return result, err
		}
		if applied != nil {
			result.applied = append(result.applied, applied)
		}
	}
	return result, nil
}

// clearAnthropicThinking 去掉保留轮数之前的 assistant 轮中的 thinking 与 redacted_thinking 块，省略 keep 时保留全部
func (s *server) clearAnthropicThinking(request *anthropicRequest, edit anthropicContextEdit) (map[string]any, error) {
	var keep struct {
		Type  string `json:"type"`
		Value int    `json:"value"`
	}
	if len(edit.Keep) == 0 || string(edit.Keep) == `"all"` {
		return nil, nil
	}
	if err := json.Unmarshal(edit.Keep, &keep); err != nil || keep.Type != "all" && keep.Type != "thinking_turns" || keep.Value < 0 {
		return nil, fmt.Errorf(`context_management: clear_thinking_20251015 keep must be "all" or {"type":"thinking_turns","value":N} with N >= 0`)
	}
	if keep.Type == "all" {
		return nil, nil
	}
	history := anthropicHistoryBlocks(request.Messages)
	var turns []int
	for index, blocks := range history {
		if request.Messages[index].Role == "assistant" && slices.ContainsFunc(blocks, anthropicThinkingBlock) {
			turns = append(turns, index)
		}
	}
	if len(turns) <= keep.Value {
		return nil, nil
	}
	cleared := turns[:len(turns)-keep.Value]
	for _, index := range cleared {
		history[index] = slices.DeleteFunc(history[index], anthropicThinkingBlock)
	}
	return s.commitAnthropicEdit(request, history, edit, map[string]any{"type": edit.Type, "cleared_thinking_turns": len(cleared)})
}

// anthropicThinkingBlock 判断块是否为 thinking 或 redacted_thinking
func anthropicThinkingBlock(raw json.RawMessage) bool {
	var head anthropicBlockHead
	_ = json.Unmarshal(raw, &head)
	return head.Type == "thinking" || head.Type == "redacted_thinking"
}

// clearAnthropicToolUses 在触发条件满足时把保留数量之前的工具结果替换为占位文本，按 clear_tool_inputs 清空调用参数
func (s *server) clearAnthropicToolUses(request *anthropicRequest, edit anthropicContextEdit) (map[string]any, error) {
	trigger := anthropicEditAmount{Type: "input_tokens", Value: 100000}
	if edit.Trigger != nil {
		trigger = *edit.Trigger
	}
	keep := anthropicEditAmount{Type: "tool_uses", Value: 3}
	if len(edit.Keep) > 0 {
		if err := json.Unmarshal(edit.Keep, &keep); err != nil || keep.Type != "tool_uses" || keep.Value < 0 {
			return nil, fmt.Errorf(`context_management: clear_tool_uses_20250919 keep must be {"type":"tool_uses","value":N} with N >= 0`)
		}
	}
	clearInputs := func(string) bool { return false }
	if rawJSONConfigured(edit.ClearToolInputs) {
		var all bool
		var names []string
		switch {
		case json.Unmarshal(edit.ClearToolInputs, &all) == nil:
			clearInputs = func(string) bool { return all }
		case json.Unmarshal(edit.ClearToolInputs, &names) == nil:
			clearInputs = func(name string) bool { return slices.Contains(names, name) }
		default:
			return nil, fmt.Errorf("context_management: clear_tool_inputs must be a boolean or an array of tool names")
		}
	}
	history := anthropicHistoryBlocks(request.Messages)
	type toolUse struct {
		name         string
		call, result [2]int
		hasResult    bool
	}
	var uses []*toolUse
	byID := map[string]*toolUse{}
	for index, blocks := range history {
		for position, raw := range blocks {
			var head anthropicBlockHead
			_ = json.Unmarshal(raw, &head)
			switch {
			case (head.Type == "tool_use" || head.Type == "server_tool_use" || head.Type == "mcp_tool_use") && head.ID != "":
				use := &toolUse{name: head.Name, call: [2]int{index, position}}
				uses = append(uses, use)
				byID[head.ID] = use
			case head.ToolUseID != "" && byID[head.ToolUseID] != nil:
				use := byID[head.ToolUseID]
				use.result, use.hasResult = [2]int{index, position}, true
			}
		}
	}
	switch trigger.Type {
	case "tool_uses":
		if int64(len(uses)) <= trigger.Value {
			return nil, nil
		}
	case "input_tokens":
		tokens, err := s.anthropicInputTokens(*request)
		if err != nil || tokens <= trigger.Value {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("context_management: clear_tool_uses_20250919 trigger type must be input_tokens or tool_uses")
	}
	if int64(len(uses)) <= keep.Value {
		return nil, nil
	}
	cleared := 0
	for _, use := range uses[:int64(len(uses))-keep.Value] {
		if !use.hasResult || slices.Contains(edit.ExcludeTools, use.name) {
			continue
		}
		block := history[use.result[0]][use.result[1]]
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(block, &fields)
		if head := string(fields["type"]); head == `"tool_result"` || head == `"mcp_tool_result"` {
			fields["content"], _ = json.Marshal(anthropicClearedToolResult)
			block, _ = json.Marshal(fields)
		} else {
			block, _ = json.Marshal(map[string]string{"type": "text", "text": anthropicClearedToolResult})
		}
		history[use.result[0]][use.result[1]] = block
		if clearInputs(use.name) {
			var call map[string]json.RawMessage
			_ = json.Unmarshal(history[use.call[0]][use.call[1]], &call)
			call["input"] = json.RawMessage(`{}`)
			history[use.call[0]][use.call[1]], _ = json.Marshal(call)
		}
		cleared++
	}
	if cleared == 0 {
		return nil, nil
	}
	return s.commitAnthropicEdit(request, history, edit, map[string]any{"type": edit.Type, "cleared_tool_uses": cleared})
}

// commitAnthropicEdit 估算编辑清理的输入 token，未达到 clear_at_least 时不改写请求
func (s *server) commitAnthropicEdit(request *anthropicRequest, history [][]json.RawMessage, edit anthropicContextEdit, applied map[string]any) (map[string]any, error) {
	edited := *request
	edited.Messages = withHistoryBlocks(request.Messages, history)
	before, err := s.anthropicInputTokens(*request)
	if err != nil {
		return nil, err
	}
	after, err := s.anthropicInputTokens(edited)
	if err != nil {
		return nil, err
	}
	cleared := max(before-after, 0)
	if edit.ClearAtLeast != nil && cleared < edit.ClearAtLeast.Value {
		return nil, nil
	}
	request.Messages = edited.Messages
	applied["cleared_input_tokens"] = cleared
	return applied, nil
}

// compactAnthropic 在输入超过 compact_20260112 的触发值时生成摘要，摘要替换生成请求的全部历史
func (s *server) compactAnthropic(ctx context.Context, request anthropicRequest, generate *aistudio.GenerateRequest) (*anthropicCompaction, error) {
	index := slices.IndexFunc(request.ContextManagement.Edits, func(edit anthropicContextEdit) bool { return edit.Type == "compact_20260112" })
	edit := request.ContextManagement.Edits[index]
	trigger := int64(150000)
	if edit.Trigger != nil {
		trigger = edit.Trigger.Value
	}
	if tokens := <-aistudio.StartInputTokenEstimate(*generate); tokens <= trigger {
		return nil, nil
	}
	instructions := edit.Instructions
	if instructions == "" {
		instructions = anthropicCompactionPrompt
	}
	summarize := *generate
	summarize.Contents = append(slices.Clone(generate.Contents), aistudio.Content{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: instructions}}})
	summarize.Tools.ToolConfig.Mode = "none"
	summarize.Unary = true
	events, err := s.service.Generate(ctx, summarize)
	if err != nil {
		return nil, err
	}
	result, err := consumeEvents(ctx, events)
	if err != nil {
		return nil, err
	}
	if result.finishReason != "stop" {
		return nil, fmt.Errorf("compaction summary finished with %s", result.finishReason)
	}
	summary := strings.TrimSpace(result.text.String())
	if start, end := strings.Index(summary, "<summary>"), strings.LastIndex(summary, "</summary>"); start >= 0 && end > start {
		summary = strings.TrimSpace(summary[start+len("<summary>") : end])
	}
	generate.Contents = []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: fmt.Sprintf(compactionSummaryContext, summary)}}}}
	return &anthropicCompaction{summary: summary, usage: result.usage, pause: edit.PauseAfterCompaction}, nil
}

// block 返回压缩摘要的 compaction 内容块
func (compaction anthropicCompaction) block() map[string]any {
	return map[string]any{"type": "compaction", "content": compaction.summary, "encrypted_content": nil}
}

// usage 在请求带 compact_20260112 时给用量加上各次采样的 iterations，暂停时顶层用量为 0
func (result anthropicContextResult) usage(usage map[string]any, message *aistudio.Usage) map[string]any {
	if !result.compacting {
		return usage
	}
	iteration := func(kind string, value *aistudio.Usage) map[string]any {
		entry := map[string]any{"type": kind, "input_tokens": int64(0), "output_tokens": int64(0), "cache_creation_input_tokens": 0, "cache_read_input_tokens": int64(0)}
		if value != nil {
			entry["input_tokens"], entry["output_tokens"], entry["cache_read_input_tokens"] = inputTokens(value)-value.CachedTokens, outputTokens(value), value.CachedTokens
		}
		return entry
	}
	var iterations []map[string]any
	if result.compaction != nil {
		iterations = append(iterations, iteration("compaction", result.compaction.usage))
	}
	if result.compaction == nil || !result.compaction.pause {
		iterations = append(iterations, iteration("message", message))
	}
	usage["iterations"] = iterations
	return usage
}

// response 把压缩块、iterations 与生效的编辑写入非流式 Message
func (result anthropicContextResult) response(response map[string]any, message *aistudio.Usage) {
	if result.compaction != nil {
		response["content"] = append([]map[string]any{result.compaction.block()}, response["content"].([]map[string]any)...)
	}
	if usage, ok := response["usage"].(map[string]any); ok {
		response["usage"] = result.usage(usage, message)
	}
	if result.requested {
		response["context_management"] = map[string]any{"applied_edits": result.applied}
	}
}
