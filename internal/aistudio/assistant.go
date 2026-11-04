package aistudio

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	// assistantModelSuffix 是 Build 写代码助手模型 ID 的后缀
	assistantModelSuffix = "-build"
	// assistantOfflineMethod 是提交写代码助手生成任务的受保护 RPC
	assistantOfflineMethod = "CodeAssistantOffline"
	// assistantStreamMethod 是读取写代码助手生成任务的流式 RPC
	assistantStreamMethod = "StreamCodeAssistantOfflineGeneration"
	// assistantUploadMethod 是写代码助手附件上传在错误中的名称
	assistantUploadMethod = "Upload"
	// assistantProofField 是 CodeAssistantOffline 的 WAA proof 字段
	assistantProofField = 2
	// assistantDefaultThinkingLevel 是 Build 对话默认的 high 思考强度
	assistantDefaultThinkingLevel = 3
	// assistantCleanupTimeout 是取消未完成生成任务的期限
	assistantCleanupTimeout = time.Minute
	// assistantFence 是 Markdown 代码块的围栏
	assistantFence = "```"
	// assistantCallFence 是写代码助手以文本输出函数调用的代码块开头
	assistantCallFence = assistantFence + "function_calls"
	// assistantResultFence 是转录函数结果的代码块开头
	assistantResultFence = assistantFence + "function_results"
	// assistantToolProtocol 是写入自定义指令的客户端函数说明，%s 为函数声明 JSON
	assistantToolProtocol = "You can call the following client functions. They run on the user's machine, not in this workspace, and are the only way to read or change the user's files, run the user's commands or reach the user's services.\n%s\n" +
		"To call functions, write one fenced block:\n" + assistantCallFence + "\n[{\"name\": \"<function name>\", \"arguments\": {<arguments>}}]\n```\n" +
		"Right after the block, call your ask_question tool once with the question \"" + assistantCallQuestion + "\" and write nothing more. The user's machine runs the calls and the results arrive as the answer in a fenced function_results block. Never write a function_results block yourself. Answer normally when no function is needed."
	// assistantCallQuestion 是模型写出调用代码块后以 ask_question 交回调用的问题
	assistantCallQuestion = "Run these function calls and send me their results."
	// assistantQuestionTool 是写代码助手向用户提问并结束本轮生成的内置工具
	assistantQuestionTool = "ask_question"
	// assistantWorkspaceRule 是写在自定义指令开头、禁止调用写代码助手内置工具的说明，%s 为禁止的工具名
	assistantWorkspaceRule = "The tools listed earlier in your instructions (%s) act on an empty remote applet workspace that has nothing to do with this conversation or the user's machine. You are strictly forbidden to call any of them: do not create, view, edit or delete workspace files, run workspace commands, compile, install, deploy or generate images there. Reply to the user directly in your answer."
	// assistantSearchRule 是请求允许网页搜索时接在禁止说明之后的说明
	assistantSearchRule = " The only exception is search_web, which you may call to search the web."
	// assistantTurnOpen 与 assistantTurnClose 包住以 user 轮发送的模型历史轮
	assistantTurnOpen  = "<assistant_turn>\n"
	assistantTurnClose = "\n</assistant_turn>"
	// assistantSearchTranscript 是模型历史轮中补回的一次 search_web 结果，依次为查询与摘要
	assistantSearchTranscript = "search_web results for %q:\n%s"
	// assistantSearchHistoryLimit 是保存 search_web 结果的模型回复数上限
	assistantSearchHistoryLimit = 1000
	// assistantHistoryRule 是对话含模型历史轮时写入自定义指令的说明
	assistantHistoryRule = "Your own earlier replies in this conversation, including your function_calls blocks, appear in the user message between <assistant_turn> and </assistant_turn>. Continue from the latest content and never write these tags yourself."
	// assistantEmptyReplyPrompt 是模型一轮结束时没有正文与函数调用后在同一应用中追加的用户轮
	assistantEmptyReplyPrompt = "Your last turn ended without any visible reply. Continue now and give your reply."
)

// assistantWorkspaceTools 是写代码助手服务端指令声明的工作区内置工具，search_web 在最后
var assistantWorkspaceTools = []string{
	"run_command", "manage_task", "schedule", "write_to_file", "replace_file_content", "multi_replace_file_content", "view_file", "delete_file",
	"list_dir", "delete_dir", "move", "compile_applet", "lint_applet", "install_applet_dependencies", "install_applet_package", "restart_dev_server",
	"ask_question", "show_aistudio_ui", "set_up_firebase", "deploy_firebase", "rpc_action", "generate_image", "set_up_oauth", "remove_oauth", "search_web",
}

// assistantCallFences 是扣下解码的代码块语言标记：function_calls 与 function_results 为调用与结果转录，其余标记只在调用的函数均已声明时解码为调用
var assistantCallFences = map[string]bool{"function_calls": true, "function_results": true, "function_call": true, "tool_calls": true, "tool_call": true, "tool_code": true}

// assistantCall 是 function_calls 代码块中的一次调用
type assistantCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// assistantResult 是 function_results 代码块中的一个结果
type assistantResult struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

// AssistantUploader 以账户身份上传写代码助手附件并返回上传路径
type AssistantUploader interface {
	UploadAssistantFile(context.Context, string, Blob) (string, error)
}

// assistantModelEntry 由可在 Build 写代码助手中选用的目录模型派生 -build 条目
func assistantModelEntry(base modelEntry) modelEntry {
	model := cloneModels([]Model{base.model})[0]
	model.ID = base.model.ID + assistantModelSuffix
	model.Name = base.model.Name + " (Build)"
	model.Methods = nil
	for _, method := range base.model.Methods {
		if method == "generateContent" || method == "countTokens" {
			model.Methods = append(model.Methods, method)
		}
	}
	// 函数调用经自定义指令与代码块转录完成，网页搜索由写代码助手内置的 search_web 完成
	model.Capabilities = map[string]bool{"assistant_route": true, "function_declarations": true, "google_search": true}
	for _, name := range []string{"chat_model", "thinking", "thinking_level"} {
		if base.model.Capabilities[name] {
			model.Capabilities[name] = true
		}
	}
	model.CapabilityOptions = nil
	if len(model.ThinkingLevels) > 0 {
		model.DefaultThinkingLevel = buildThinkingLevels[closestSupportedThinkingLevel(assistantDefaultThinkingLevel, base.defaults.ThinkingLevels)]
	}
	return modelEntry{model: model, defaults: GenerationDefaults{
		MaxOutputTokens: base.defaults.MaxOutputTokens, Thinking: true, ThinkingLevel: true,
		DefaultThinkingLevel: assistantDefaultThinkingLevel, ThinkingLevels: base.defaults.ThinkingLevels,
	}}
}

// assistantBaseModelID 返回 -build 模型对应的目录模型 ID
func assistantBaseModelID(modelID string) string {
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(modelID), "models/"), assistantModelSuffix)
}

// assistantThinkingLevel 按请求的思考强度或预算选择 Build 对话的 thinking level，未指定时为 high
func assistantThinkingLevel(config GenerationConfig, defaults GenerationDefaults) (int64, error) {
	level := int64(assistantDefaultThinkingLevel)
	switch normalizedReasoningEffort(config.ReasoningEffort) {
	case "":
		if config.ThinkingBudget != nil {
			level = thinkingLevelForBudget(*config.ThinkingBudget)
		}
	case "minimal", "none":
		level = 4
	case "low":
		level = 1
	case "medium":
		level = 2
	case "high":
		level = 3
	default:
		return 0, fmt.Errorf("reasoning effort 必须是 none、minimal、low、medium、high、xhigh 或 max")
	}
	return closestSupportedThinkingLevel(level, defaults.ThinkingLevels), nil
}

// EncodeAssistantRequest 编码 CodeAssistantOffline：field 1 对话轮、6 模式、8 模型、9 自定义指令、12 应用、14 框架、16、19 思考配置、21 继续的对话、29 请求 ID，field 2 由 WAA proof 写入；新应用的首轮模式为 1，continued 时模式为 2 并继续应用中以应用 ID 为 ID 的对话
func EncodeAssistantRequest(request GenerateRequest, defaults GenerationDefaults, applet string, continued bool, requestID string) ([]byte, error) {
	contents, err := encodeContents(request.Contents)
	if err != nil {
		return nil, err
	}
	if len(contents) == 0 {
		return nil, fmt.Errorf("CodeAssistantOffline contents 不能为空")
	}
	level, err := assistantThinkingLevel(request.Config, defaults)
	if err != nil {
		return nil, err
	}
	wire := make([]any, 29)
	wire[0] = contents
	wire[5] = int64(1)
	wire[7] = wireModelName(assistantBaseModelID(request.Model))
	if system := SystemText(request.System); system != "" {
		wire[8] = system
	}
	wire[11] = applet
	wire[13] = []any{int64(2), nil, int64(2)}
	if continued {
		wire[5] = int64(2)
		wire[13] = []any{int64(2)}
		wire[20] = applet
	}
	wire[15] = int64(1)
	wire[18] = []any{int64(1), nil, nil, level}
	wire[28] = requestID
	return json.Marshal(wire)
}

// assistantToolPrompt 把禁止调用内置工具的说明与函数声明写在自定义指令开头，声明函数时允许以 ask_question 交回调用，把对话中的函数调用与函数结果改写为 function_calls 与 function_results 代码块，只保留 start 起的对话轮；写代码助手只读取 user 轮，模型历史轮去掉思考后包上标记以 user 轮发送，transcribed 时说明这一标记
func assistantToolPrompt(request GenerateRequest, start int, transcribed bool) (GenerateRequest, error) {
	functions := len(request.Tools.Functions) > 0 && request.Tools.ToolConfig.Mode != "none"
	forbidden := assistantWorkspaceTools
	if request.Tools.GoogleSearch != nil {
		forbidden = forbidden[:len(forbidden)-1]
	}
	if functions {
		forbidden = slices.DeleteFunc(slices.Clone(forbidden), func(name string) bool { return name == assistantQuestionTool })
	}
	prompt := fmt.Sprintf(assistantWorkspaceRule, strings.Join(forbidden, ", "))
	if request.Tools.GoogleSearch != nil {
		prompt += assistantSearchRule
	}
	if functions {
		declarations, err := scriptJSON(request.Tools.Functions)
		if err != nil {
			return request, err
		}
		prompt += "\n\n" + fmt.Sprintf(assistantToolProtocol, declarations)
	}
	contents := make([]Content, 0, len(request.Contents))
	var pending []FunctionCall
	for index, content := range request.Contents {
		linked, err := linkFunctionResults(content, &pending)
		if err != nil {
			return request, fmt.Errorf("contents[%d]: %w", index, err)
		}
		parts := make([]Part, 0, len(linked.Parts))
		fence := ""
		var block []any
		flush := func() error {
			if len(block) == 0 {
				return nil
			}
			encoded, err := scriptJSON(block)
			if err != nil {
				return fmt.Errorf("contents[%d]: %w", index, err)
			}
			parts = append(parts, Part{Text: fence + "\n" + encoded + "\n```"})
			block = nil
			return nil
		}
		for _, part := range linked.Parts {
			next, entry := "", any(nil)
			switch {
			case part.FunctionCall != nil:
				next, entry = assistantCallFence, assistantCall{Name: part.FunctionCall.Name, Arguments: transcriptJSON(part.FunctionCall.Arguments)}
			case part.FunctionResult != nil:
				next, entry = assistantResultFence, assistantResult{Name: part.FunctionResult.Name, Response: transcriptJSON(part.FunctionResult.Content)}
			}
			if next != fence {
				if err := flush(); err != nil {
					return request, err
				}
				fence = next
			}
			if entry == nil {
				parts = append(parts, part)
				continue
			}
			block = append(block, entry)
		}
		if err := flush(); err != nil {
			return request, err
		}
		if linked.Role == RoleAssistant {
			parts = assistantTurn(parts)
		}
		contents = append(contents, Content{Role: RoleUser, Parts: parts})
	}
	if transcribed {
		prompt += "\n\n" + assistantHistoryRule
	}
	if len(request.System) > 0 {
		prompt += "\n\n"
	}
	request.System = append([]string{prompt}, request.System...)
	request.Contents = contents[start:]
	return request, nil
}

// assistantTurn 去掉模型历史轮的思考与空 Part，首尾加上模型轮标记
func assistantTurn(parts []Part) []Part {
	turn := []Part{{Text: assistantTurnOpen}}
	for _, part := range parts {
		if part.Thought || !part.payload() {
			continue
		}
		turn = append(turn, part)
	}
	return append(turn, Part{Text: assistantTurnClose})
}

// scriptJSON 按页面脚本 JSON.stringify 的形式编码，保留 HTML 字符原样、不带结尾换行
func scriptJSON(value any) (string, error) {
	var buffer strings.Builder
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buffer.String(), "\n"), nil
}

// sendAssistant 在对话所在的应用中只提交新增轮次，没有可继续的应用时新建应用并提交全部历史，返回任务流；本轮结束时没有正文与函数调用的，在同一应用中追加用户轮请模型继续，用量按续写合并；流关闭后取消未完成的任务，本轮回复完整结束时对话保存为可继续，否则放弃应用留待巡检删除
func (c *Client) sendAssistant(ctx context.Context, request GenerateRequest, entry modelEntry) (*RPCResponse, func(io.Reader, func(Event) error) error, error) {
	reason := ""
	if request.Unary {
		reason = "Build 写代码助手使用服务端流，收集完成后返回"
	}
	reportUpstreamMode(ctx, assistantOfflineMethod, "stream", reason)
	var functions []string
	if request.Tools.ToolConfig.Mode != "none" {
		for _, function := range request.Tools.Functions {
			functions = append(functions, function.Name)
		}
	}
	apiKey, _ := ctx.Value(apiKeyContextKey{}).(string)
	history := assistantHistoryDigest(request.Contents)
	links := assistantLinks(request.Contents)
	session, start := c.assistants.claim(apiKey, request.AccountID, links)
	cleanup := &assistantCleanup{client: c, accountID: request.AccountID, session: session}
	cleanup.transcribed = slices.ContainsFunc(request.Contents[start:], func(content Content) bool { return content.Role == RoleAssistant }) ||
		session != nil && session.transcribed
	request.Contents = assistantSearches.restore(apiKey, request.Contents)
	request, err := assistantToolPrompt(request, start, cleanup.transcribed)
	if err != nil {
		cleanup.run()
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	contents, err := c.uploadAssistantAttachments(ctx, request.AccountID, request.Contents)
	if err != nil {
		cleanup.run()
		return nil, nil, err
	}
	request.Contents = contents
	if session == nil {
		applet, err := c.createAssistantApplet(ctx, request.AccountID)
		if err != nil {
			return nil, nil, err
		}
		cleanup.session = c.assistants.open(request.AccountID, applet)
		lease, _ := AccountLeaseFromContext(ctx)
		if err := lease.BindResource(applet, assistantAppletKind); err != nil {
			cleanup.run()
			return nil, nil, err
		}
	}
	applet := cleanup.session.applet
	generation, streamBody, stream, err := c.openAssistantGeneration(ctx, request, entry, applet, session != nil)
	cleanup.generation.Store(&generation)
	if err != nil {
		cleanup.run()
		return nil, nil, err
	}
	stream.Body = &assistantStreamBody{ReadCloser: stream.Body, cleanup: cleanup}
	return stream, func(source io.Reader, emit func(Event) error) error {
		newDecoder := func(streamBody []byte) *AssistantStreamDecoder {
			return &AssistantStreamDecoder{Functions: functions, Reopen: func() (io.ReadCloser, error) {
				reopened, err := c.do(ctx, assistantStreamMethod, request.AccountID, request.ID, streamBody, true)
				if err != nil {
					return nil, err
				}
				return reopened.Body, nil
			}}
		}
		var answer strings.Builder
		var reply []Part
		var usage *Usage
		collect := func(event Event) error {
			switch event.Kind {
			case EventText:
				answer.WriteString(event.Text)
				reply = append(reply, Part{Text: event.Text})
			case EventToolCall:
				reply = append(reply, Part{FunctionCall: event.FunctionCall})
			case EventUsage:
				if usage != nil {
					merged := mergeContinuedUsage(*usage, *event.Usage)
					event.Usage = &merged
				}
				usage = event.Usage
			}
			return emit(event)
		}
		decoder := newDecoder(streamBody)
		err := DecodeAssistantStream(source, decoder, collect)
		for followed := 0; err == nil && len(reply) == 0 && !decoder.overran && !decoder.malformed && followed < regenerations; followed++ {
			followUp := request
			followUp.Contents = []Content{{Role: RoleUser, Parts: []Part{{Text: assistantEmptyReplyPrompt}}}}
			next, nextBody, follow, openErr := c.openAssistantGeneration(ctx, followUp, entry, applet, true)
			cleanup.generation.Store(&next)
			if err = openErr; err != nil {
				break
			}
			decoder = newDecoder(nextBody)
			err = errors.Join(DecodeAssistantStream(follow.Body, decoder, collect), follow.Body.Close())
		}
		if err == nil {
			cleanup.completed.Store(!decoder.overran)
			assistantSearches.store(apiKey, history, answer.String(), decoder.searches)
			if !decoder.overran && !decoder.malformed {
				cleanup.tip.Store(&assistantSessionKey{apiKey: apiKey, digest: assistantLinkDigest(links[len(links)-1].digest, true, reply)})
			}
		}
		return err
	}, nil
}

// openAssistantGeneration 在应用中提交请求的对话轮并打开任务流，返回任务 ID、任务流请求正文与任务流；continued 为 true 时在应用已有对话之后继续
func (c *Client) openAssistantGeneration(ctx context.Context, request GenerateRequest, entry modelEntry, applet string, continued bool) (string, []byte, *RPCResponse, error) {
	body, err := EncodeAssistantRequest(request, entry.defaults, applet, continued, strings.ToUpper(uuid.NewString()))
	if err != nil {
		return "", nil, nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	rpc := newRPCRequest(assistantOfflineMethod, request.AccountID, request.ID, body, false)
	c.applyBenefitTier(rpc.Method, request.AccountID, rpc.Header)
	response, err := c.protected.DoProtected(ctx, request, rpc)
	if err == nil {
		response, err = validateRPCResponse(assistantOfflineMethod, response)
	}
	if err != nil {
		return "", nil, nil, fmt.Errorf("发送 AI Studio %s: %w", assistantOfflineMethod, err)
	}
	generation, err := decodeAssistantGeneration(response.Body)
	response.Body.Close()
	if err != nil {
		return "", nil, nil, err
	}
	streamBody, _ := json.Marshal([]any{generation, nil, nil, applet})
	stream, err := c.do(ctx, assistantStreamMethod, request.AccountID, request.ID, streamBody, true)
	return generation, streamBody, stream, err
}

// assistantSearches 是本进程写代码助手回复的 search_web 结果
var assistantSearches = &assistantSearchHistory{entries: map[assistantSearchKey][]string{}}

// assistantSearchKey 是请求所用 API key、该轮之前对话的摘要与模型回复正文
type assistantSearchKey struct {
	apiKey  string
	history string
	answer  string
}

// assistantHistoryDigest 返回对话轮角色与各非思考 Part 的 SHA-256 摘要，内联数据以其 SHA-256 计入，思考签名不计入
func assistantHistoryDigest(contents []Content) string {
	digest := sha256.New()
	for _, content := range contents {
		digest.Write([]byte(content.Role))
		digest.Write([]byte{0})
		for _, part := range content.Parts {
			if part.Thought {
				continue
			}
			part.ThoughtSignature = ""
			if part.InlineData != nil {
				sum := sha256.Sum256(part.InlineData.Data)
				part.InlineData = &Blob{MIME: part.InlineData.MIME, Data: sum[:]}
			}
			encoded, _ := json.Marshal(part)
			digest.Write(encoded)
			digest.Write([]byte{0})
		}
		digest.Write([]byte{1})
	}
	return fmt.Sprintf("%x", digest.Sum(nil))
}

// assistantSearchHistory 按 API key、之前的对话与模型回复正文保存该轮 search_web 的查询与摘要，同一 key 回传同一对话与回复时补回对话历史
type assistantSearchHistory struct {
	mu      sync.Mutex
	entries map[assistantSearchKey][]string
	order   []assistantSearchKey
}

// store 保存 API key、之前的对话与回复正文对应的 search_web 结果，超过上限时丢弃最早的回复
func (history *assistantSearchHistory) store(apiKey string, digest string, answer string, searches []string) {
	key := assistantSearchKey{apiKey: apiKey, history: digest, answer: strings.TrimSpace(answer)}
	if key.answer == "" || len(searches) == 0 {
		return
	}
	history.mu.Lock()
	defer history.mu.Unlock()
	if _, exists := history.entries[key]; !exists {
		history.order = append(history.order, key)
	}
	history.entries[key] = searches
	if len(history.order) > assistantSearchHistoryLimit {
		delete(history.entries, history.order[0])
		history.order = history.order[1:]
	}
}

// restore 在同一 API key 下之前的对话与正文都和保存的回复相同的模型轮开头补回该轮的 search_web 结果
func (history *assistantSearchHistory) restore(apiKey string, contents []Content) []Content {
	history.mu.Lock()
	defer history.mu.Unlock()
	restored := slices.Clone(contents)
	for index, content := range restored {
		if content.Role != RoleAssistant {
			continue
		}
		var answer strings.Builder
		for _, part := range content.Parts {
			if !part.Thought {
				answer.WriteString(part.Text)
			}
		}
		searches := history.entries[assistantSearchKey{apiKey: apiKey, history: assistantHistoryDigest(contents[:index]), answer: strings.TrimSpace(answer.String())}]
		if len(searches) == 0 {
			continue
		}
		parts := make([]Part, 0, len(searches)+len(content.Parts))
		for _, search := range searches {
			parts = append(parts, Part{Text: search})
		}
		restored[index].Parts = append(parts, content.Parts...)
	}
	return restored
}

// uploadAssistantAttachments 把对话轮中的内联附件上传为写代码助手的附件引用
func (c *Client) uploadAssistantAttachments(ctx context.Context, accountID string, contents []Content) ([]Content, error) {
	rewritten := make([]Content, len(contents))
	count := 0
	for index, content := range contents {
		parts := slices.Clone(content.Parts)
		for partIndex := range parts {
			blob := parts[partIndex].InlineData
			if blob == nil {
				continue
			}
			uploader, ok := c.transport.(AssistantUploader)
			if !ok {
				return nil, fmt.Errorf("AI Studio transport 不支持写代码助手附件上传")
			}
			count++
			path, err := uploader.UploadAssistantFile(ctx, accountID, *blob)
			if err != nil {
				return nil, fmt.Errorf("上传写代码助手附件 %d: %w", count, err)
			}
			parts[partIndex].InlineData = nil
			parts[partIndex].Upload = &UploadRef{MIME: blob.MIME, Path: path, Name: fmt.Sprintf("attachment-%d", count), Size: int64(len(blob.Data))}
		}
		rewritten[index] = Content{Role: content.Role, Parts: parts}
	}
	return rewritten, nil
}

// createAssistantApplet 新建承载一次写代码助手对话的应用
func (c *Client) createAssistantApplet(ctx context.Context, accountID string) (string, error) {
	body, _ := json.Marshal([]any{nil, []any{"Untitled", "Generated by Gemini.", nil, nil, "default"}, nil, nil, int64(1)})
	response, err := c.do(ctx, "CreateApplet", accountID, "", body, false)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return "", fmt.Errorf("读取 AI Studio CreateApplet: %w", err)
	}
	values, err := rawArray(raw, "$", raw)
	if err != nil {
		return "", withMethod(err, "CreateApplet")
	}
	applet, err := rawString(rawAt(values, 0), "$[0]", raw)
	if err != nil || applet == "" {
		return "", &ProtocolEvidenceError{Method: "CreateApplet", Path: "$[0]", Detail: "缺少应用 ID", Raw: cloneRaw(raw)}
	}
	return applet, nil
}

// decodeAssistantGeneration 读取 CodeAssistantOffline 响应 [任务 ID, 应用 ID] 中的任务 ID
func decodeAssistantGeneration(source io.Reader) (string, error) {
	raw, err := io.ReadAll(source)
	if err != nil {
		return "", fmt.Errorf("读取 AI Studio %s: %w", assistantOfflineMethod, err)
	}
	values, err := rawArray(raw, "$", raw)
	if err != nil {
		return "", withMethod(err, assistantOfflineMethod)
	}
	generation, err := rawString(rawAt(values, 0), "$[0]", raw)
	if err != nil || generation == "" {
		return "", &ProtocolEvidenceError{Method: assistantOfflineMethod, Path: "$[0]", Detail: "缺少任务 ID", Raw: cloneRaw(raw)}
	}
	return generation, nil
}

// assistantCleanup 在请求结束后取消未完成的生成任务，并按回复是否完整结束保存或放弃会话
type assistantCleanup struct {
	client    *Client
	accountID string
	session   *assistantSession
	// transcribed 表示请求结束后应用中有以 user 轮转录的模型历史轮
	transcribed bool
	// generation 是应用中最近提交的任务 ID
	generation atomic.Pointer[string]
	completed  atomic.Bool
	// tip 是本轮回复完整结束后对话可继续的键
	tip  atomic.Pointer[assistantSessionKey]
	once sync.Once
}

// run 执行一次清理：结束会话上的请求，未完成的任务在后台以独立期限取消
func (cleanup *assistantCleanup) run() {
	cleanup.once.Do(func() {
		if cleanup.session == nil {
			return
		}
		cleanup.client.assistants.finish(cleanup.session, cleanup.tip.Load(), cleanup.transcribed)
		generation := cleanup.generation.Load()
		if generation == nil || *generation == "" || cleanup.completed.Load() {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), assistantCleanupTimeout)
			defer cancel()
			body, _ := json.Marshal([]any{*generation, cleanup.session.applet})
			response, err := cleanup.client.do(ctx, "CancelCodeAssistantOfflineGeneration", cleanup.accountID, "", body, false)
			if err == nil {
				_, err = io.Copy(io.Discard, response.Body)
				err = errors.Join(err, response.Body.Close())
			}
			if err != nil {
				slog.Warn("Build 写代码助手清理失败", "account", cleanup.accountID, "method", "CancelCodeAssistantOfflineGeneration", "applet", cleanup.session.applet, "error", err)
			}
		}()
	})
}

// assistantStreamBody 在任务流关闭时触发清理
type assistantStreamBody struct {
	io.ReadCloser
	cleanup *assistantCleanup
}

// Close 关闭任务流并清理
func (body *assistantStreamBody) Close() error {
	err := body.ReadCloser.Close()
	body.cleanup.run()
	return err
}

// AssistantStreamDecoder 把写代码助手的步骤快照解码为增量事件
type AssistantStreamDecoder struct {
	// Functions 是请求声明的函数名，不为空时正文中的调用代码块解码为函数调用
	Functions []string
	// Reopen 以同一任务重新打开任务流
	Reopen    func() (io.ReadCloser, error)
	emitted   map[int64][]string
	usage     map[int64]Usage
	searched  map[int64]bool
	grounding *GroundingMetadata
	// searches 是已完成的 search_web 的查询与摘要，供下一轮补回对话历史
	searches []string
	// held 是扣下的正文：可能的代码块围栏开头，或尚未结束的调用候选代码块
	held string
	// block 表示 held 以调用候选代码块开头，other 表示正处在其他代码块内
	block bool
	other bool
	// called 表示已输出函数调用，malformed 表示 function_calls 代码块无法解码
	called    bool
	malformed bool
	lastStep  int64
	wroteText bool
	finished  bool
	// answered 表示最近解码的步骤是已完成、含正文且没有工具动作的模型步骤，即本轮回复结束
	answered bool
	// overran 表示函数调用之后模型继续输出正文，本轮回复在调用处结束
	overran bool
}

// DecodeAssistantStream 解码 StreamCodeAssistantOfflineGeneration 响应：field 1 为消息列表，其后可带 google.rpc 状态；本轮回复结束或函数调用之后模型继续输出正文时不再读取上游，消息列表在回复完成前正常结束时经 Reopen 重新打开一次任务流继续读取
func DecodeAssistantStream(source io.Reader, decoder *AssistantStreamDecoder, emit func(Event) error) error {
	err := decodeAssistantMessages(source, decoder, emit)
	if err == nil && !decoder.finished && !decoder.overran && decoder.Reopen != nil {
		var reopened io.ReadCloser
		if reopened, err = decoder.Reopen(); err == nil {
			err = errors.Join(decodeAssistantMessages(reopened, decoder, emit), reopened.Close())
		}
	}
	if err != nil {
		return err
	}
	events, err := decoder.End()
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := emit(event); err != nil {
			return err
		}
	}
	return nil
}

// decodeAssistantMessages 读取一条任务流的消息列表与流尾状态
func decodeAssistantMessages(source io.Reader, decoder *AssistantStreamDecoder, emit func(Event) error) error {
	reader := &streamReadRecorder{source: source}
	stream := json.NewDecoder(newSparseJSONReader(reader))
	stream.UseNumber()
	index := 0
	fail := func(path string, detail string) error {
		return reader.failure(assistantStreamMethod, index, &ProtocolEvidenceError{Method: assistantStreamMethod, Path: path, Detail: detail})
	}
	if token, err := stream.Token(); err != nil || token != json.Delim('[') {
		return fail("$", "根值不是数组")
	}
	if !stream.More() {
		return fail("$", "根数组缺少消息列表")
	}
	if token, err := stream.Token(); err != nil || token != json.Delim('[') {
		return fail("$[0]", "消息列表不是数组")
	}
	for ; !decoder.answered && !decoder.overran && stream.More(); index++ {
		var raw json.RawMessage
		if err := stream.Decode(&raw); err != nil {
			return fail(fmt.Sprintf("$[0][%d]", index), err.Error())
		}
		events, err := decoder.Decode(raw, fmt.Sprintf("$[0][%d]", index))
		if err != nil {
			return err
		}
		for _, event := range events {
			if err := emit(event); err != nil {
				return err
			}
		}
	}
	if !decoder.answered && !decoder.overran {
		if token, err := stream.Token(); err != nil || token != json.Delim(']') {
			return fail("$[0]", "消息列表没有正常结束")
		}
		for stream.More() {
			var raw json.RawMessage
			if err := stream.Decode(&raw); err != nil {
				return fail("$", err.Error())
			}
			if err := trailerError(assistantStreamMethod, raw); err != nil {
				return err
			}
		}
	}
	return reader.failure(assistantStreamMethod, index, nil)
}

// Decode 解码一条消息 [null, null, [步骤...], 应用 ID]：步骤 field 1 为对话轮、6 为失败原因、10 为工具动作、12 为序号、13 为状态（1 完成、2 进行中、4 失败）、16 为用量
func (d *AssistantStreamDecoder) Decode(raw json.RawMessage, path string) ([]Event, error) {
	if d.emitted == nil {
		d.emitted = make(map[int64][]string)
		d.usage = make(map[int64]Usage)
		d.searched = make(map[int64]bool)
	}
	message, err := rawArray(raw, path, raw)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	stepsRaw := rawAt(message, 2)
	if isJSONNull(stepsRaw) {
		return nil, nil
	}
	steps, err := rawArray(stepsRaw, path+"[2]", raw)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	var events []Event
	for stepIndex, stepRaw := range steps {
		stepPath := fmt.Sprintf("%s[2][%d]", path, stepIndex)
		stepEvents, err := d.decodeStep(stepRaw, stepPath, raw)
		if err != nil {
			return nil, err
		}
		events = append(events, stepEvents...)
	}
	return events, nil
}

func (d *AssistantStreamDecoder) decodeStep(raw json.RawMessage, path string, evidence json.RawMessage) ([]Event, error) {
	d.answered = false
	step, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	number, err := rawInt64(rawAt(step, 11), path+"[11]", evidence)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	status, err := rawInt64(rawAt(step, 12), path+"[12]", evidence)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	if failure := rawAt(step, 5); !isJSONNull(failure) {
		values, err := rawArray(failure, path+"[5]", evidence)
		if err != nil {
			return nil, withMethod(err, assistantStreamMethod)
		}
		message, err := rawString(rawAt(values, 0), path+"[5][0]", evidence)
		if err != nil {
			return nil, withMethod(err, assistantStreamMethod)
		}
		return nil, &RPCError{Method: assistantStreamMethod, StatusCode: http.StatusBadGateway, Message: "生成步骤失败: " + message}
	}
	if usageRaw := rawAt(step, 15); !isJSONNull(usageRaw) && status == 1 {
		usage, err := decodeAssistantUsage(usageRaw, path+"[15]", evidence)
		if err != nil {
			return nil, err
		}
		d.usage[number] = usage
	}
	var events []Event
	if actionRaw := rawAt(step, 9); !isJSONNull(actionRaw) && status == 1 && !d.searched[number] {
		if err := d.decodeSearch(actionRaw, path+"[9]", evidence); err != nil {
			return nil, err
		}
		if events, err = d.decodeQuestion(actionRaw, path+"[9]", evidence); err != nil {
			return nil, err
		}
		d.searched[number] = true
	}
	contentsRaw := rawAt(step, 0)
	if isJSONNull(contentsRaw) {
		return events, nil
	}
	contents, err := rawArray(contentsRaw, path+"[0]", evidence)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	partOffset := 0
	for contentIndex, contentRaw := range contents {
		contentPath := fmt.Sprintf("%s[0][%d]", path, contentIndex)
		content, err := rawArray(contentRaw, contentPath, evidence)
		if err != nil {
			return nil, withMethod(err, assistantStreamMethod)
		}
		role, err := rawString(rawAt(content, 1), contentPath+"[1]", evidence)
		if err != nil {
			return nil, withMethod(err, assistantStreamMethod)
		}
		if role != "model" {
			continue
		}
		parts, err := rawArray(rawAt(content, 0), contentPath+"[0]", evidence)
		if err != nil {
			return nil, withMethod(err, assistantStreamMethod)
		}
		for partIndex, partRaw := range parts {
			partEvents, err := d.decodePart(number, partOffset+partIndex, partRaw, fmt.Sprintf("%s[0][%d]", contentPath, partIndex), evidence)
			if err != nil {
				return nil, err
			}
			events = append(events, partEvents...)
		}
		partOffset += len(parts)
		if status == 1 {
			d.finished = true
		}
	}
	d.answered = status == 1 && isJSONNull(rawAt(step, 9)) && d.wroteText && d.lastStep == number
	return events, nil
}

// decodePart 返回模型 part 文本相对上一次快照新增的部分
func (d *AssistantStreamDecoder) decodePart(step int64, index int, raw json.RawMessage, path string, evidence json.RawMessage) ([]Event, error) {
	part, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	textRaw := rawAt(part, 1)
	if isJSONNull(textRaw) {
		return nil, nil
	}
	text, err := rawString(textRaw, path+"[1]", evidence)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	thought := false
	if thoughtRaw := rawAt(part, 12); !isJSONNull(thoughtRaw) {
		thought, err = rawBool(thoughtRaw, path+"[12]", evidence)
		if err != nil {
			return nil, withMethod(err, assistantStreamMethod)
		}
	}
	emitted := d.emitted[step]
	for len(emitted) <= index {
		emitted = append(emitted, "")
	}
	previous := emitted[index]
	if !strings.HasPrefix(text, previous) {
		return nil, &ProtocolEvidenceError{Method: assistantStreamMethod, Path: path + "[1]", Detail: "步骤正文不是上一次快照的延续", Raw: cloneRaw(raw)}
	}
	emitted[index] = text
	d.emitted[step] = emitted
	delta := text[len(previous):]
	if delta == "" {
		return nil, nil
	}
	if thought {
		return []Event{{Kind: EventReasoning, Text: delta, Thought: true}}, nil
	}
	if d.wroteText && previous == "" && step != d.lastStep {
		delta = "\n\n" + delta
	}
	d.wroteText = true
	d.lastStep = step
	return d.text(delta), nil
}

// text 返回正文增量的事件；声明了函数时扣下调用候选代码块，代码块结束后解码，函数调用之后的正文与模型自写的 function_results 代码块不输出
func (d *AssistantStreamDecoder) text(delta string) []Event {
	if len(d.Functions) == 0 {
		return []Event{{Kind: EventText, Text: delta}}
	}
	if d.called {
		d.overran = d.overran || strings.TrimSpace(delta) != ""
		return nil
	}
	d.held += delta
	var events []Event
	emit := func(text string) {
		if text != "" {
			events = append(events, Event{Kind: EventText, Text: text})
		}
	}
	for !d.called {
		switch {
		case d.other:
			end := strings.Index(d.held, "\n"+assistantFence)
			if end < 0 {
				keep := min(len(d.held), len(assistantFence))
				emit(d.held[:len(d.held)-keep])
				d.held = d.held[len(d.held)-keep:]
				return events
			}
			end += 1 + len(assistantFence)
			emit(d.held[:end])
			d.held = d.held[end:]
			d.other = false
		case d.block:
			opening := strings.IndexByte(d.held, '\n')
			end := strings.Index(d.held[opening:], "\n"+assistantFence)
			if end < 0 {
				return events
			}
			end += opening + 1 + len(assistantFence)
			block := d.held[:end]
			d.held = d.held[end:]
			d.block = false
			events = append(events, d.callBlock(block)...)
		default:
			start := strings.Index(d.held, assistantFence)
			if start < 0 {
				keep := len(d.held) - len(strings.TrimRight(d.held, "`"))
				emit(d.held[:len(d.held)-keep])
				d.held = d.held[len(d.held)-keep:]
				return events
			}
			emit(d.held[:start])
			d.held = d.held[start:]
			newline := strings.IndexByte(d.held, '\n')
			if newline < 0 {
				return events
			}
			if assistantCallFences[strings.TrimSpace(d.held[len(assistantFence):newline])] {
				d.block = true
				continue
			}
			emit(d.held[:newline+1])
			d.held = d.held[newline+1:]
			d.other = true
		}
	}
	d.overran = strings.TrimSpace(d.held) != ""
	d.held = ""
	return events
}

// callBlock 解码一个调用候选代码块：调用解码为函数调用，function_results 代码块丢弃，无法解码的 function_calls 代码块记为格式错误的函数调用，其余代码块按正文输出
func (d *AssistantStreamDecoder) callBlock(block string) []Event {
	opening := strings.IndexByte(block, '\n')
	info := strings.TrimSpace(block[len(assistantFence):opening])
	body := block[opening+1 : len(block)-len(assistantFence)]
	if info == "function_results" {
		return nil
	}
	if calls, ok := assistantCalls(body, info == "function_calls", d.Functions); ok {
		d.called = true
		return calls
	}
	if info == "function_calls" {
		d.malformed = true
		return nil
	}
	return []Event{{Kind: EventText, Text: block}}
}

// assistantCalls 把代码块中的第一个 JSON 值解码为函数调用事件：无法解码时修正字符串中的不合法转义与原始控制字符后再解码，值为调用数组或单个调用对象，anyName 为假时调用的函数必须已声明，不是调用时返回 false
func assistantCalls(body string, anyName bool, functions []string) ([]Event, bool) {
	var value json.RawMessage
	if json.NewDecoder(strings.NewReader(body)).Decode(&value) != nil && json.NewDecoder(strings.NewReader(repairJSONStrings(body))).Decode(&value) != nil {
		return nil, false
	}
	var calls []assistantCall
	if json.Unmarshal(value, &calls) != nil {
		var call assistantCall
		if json.Unmarshal(value, &call) != nil {
			return nil, false
		}
		calls = []assistantCall{call}
	}
	if len(calls) == 0 {
		return nil, false
	}
	events := make([]Event, 0, len(calls))
	for _, call := range calls {
		if call.Name == "" || !anyName && !slices.Contains(functions, call.Name) {
			return nil, false
		}
		arguments := call.Arguments
		if isJSONNull(arguments) {
			arguments = json.RawMessage(`{}`)
		}
		events = append(events, Event{Kind: EventToolCall, FunctionCall: &FunctionCall{ID: uuid.NewString(), Name: call.Name, Arguments: arguments}})
	}
	return events, true
}

// repairJSONStrings 修正 JSON 字符串中模型写出的内容：不合法的转义（如按 Python 写出的 \x27、Windows 路径中 \u 之后不是十六进制的部分）改为字面反斜杠，原始控制字符（如多行代码中的换行）改为转义
func repairJSONStrings(body string) string {
	var repaired strings.Builder
	quoted := false
	for index := 0; index < len(body); index++ {
		char := body[index]
		switch {
		case quoted && char == '\\' && validJSONEscape(body[index+1:]):
			repaired.WriteString(body[index : index+2])
			index++
		case quoted && char == '\\':
			repaired.WriteString(`\\`)
		case quoted && char < 0x20:
			fmt.Fprintf(&repaired, `\u%04x`, char)
		default:
			if char == '"' {
				quoted = !quoted
			}
			repaired.WriteByte(char)
		}
	}
	return repaired.String()
}

// validJSONEscape 表示反斜杠之后的 rest 以 JSON 合法转义开头，\u 之后须为 4 位十六进制
func validJSONEscape(rest string) bool {
	if strings.HasPrefix(rest, "u") {
		return len(rest) >= 5 && strings.Trim(rest[1:5], "0123456789abcdefABCDEF") == ""
	}
	return rest != "" && strings.IndexByte(`"\/bfnrt`, rest[0]) >= 0
}

// decodeQuestion 解码 ask_question 工具动作 field 22 [标题, null, 说明, [[参数名, 值]...]]：没有输出函数调用时把各问题与选项输出为正文，交回函数调用的提问不输出
func (d *AssistantStreamDecoder) decodeQuestion(raw json.RawMessage, path string, evidence json.RawMessage) ([]Event, error) {
	action, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	toolRaw := rawAt(action, 21)
	if isJSONNull(toolRaw) || d.called {
		return nil, nil
	}
	tool, err := rawArray(toolRaw, path+"[21]", evidence)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	pairs, err := rawArray(rawAt(tool, 3), path+"[21][3]", evidence)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	arguments := map[string]string{}
	for index, pairRaw := range pairs {
		pairPath := fmt.Sprintf("%s[21][3][%d]", path, index)
		pair, err := rawArray(pairRaw, pairPath, evidence)
		if err != nil {
			return nil, withMethod(err, assistantStreamMethod)
		}
		name, err := rawString(rawAt(pair, 0), pairPath+"[0]", evidence)
		if err != nil {
			return nil, withMethod(err, assistantStreamMethod)
		}
		if arguments[name], err = rawString(rawAt(pair, 1), pairPath+"[1]", evidence); err != nil {
			return nil, withMethod(err, assistantStreamMethod)
		}
	}
	if arguments["tag"] != assistantQuestionTool {
		return nil, nil
	}
	var questions []struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	}
	if err := json.Unmarshal([]byte(arguments["questions"]), &questions); err != nil {
		return nil, &ProtocolEvidenceError{Method: assistantStreamMethod, Path: path + "[21][3]", Detail: "ask_question 的 questions 不是问题列表: " + err.Error(), Raw: cloneRaw(evidence)}
	}
	var text strings.Builder
	for _, question := range questions {
		if question.Question == assistantCallQuestion {
			continue
		}
		if d.wroteText || text.Len() > 0 {
			text.WriteString("\n\n")
		}
		text.WriteString(question.Question)
		for _, option := range question.Options {
			text.WriteString("\n- " + option)
		}
	}
	if text.Len() == 0 {
		return nil, nil
	}
	d.wroteText = true
	return []Event{{Kind: EventText, Text: text.String()}}, nil
}

// decodeSearch 把完成的 search_web 工具动作（field 19：查询、域名、摘要、[标题, 链接] 列表）并入搜索来源，查询与摘要记入 searches
func (d *AssistantStreamDecoder) decodeSearch(raw json.RawMessage, path string, evidence json.RawMessage) error {
	action, err := rawArray(raw, path, evidence)
	if err != nil {
		return withMethod(err, assistantStreamMethod)
	}
	searchRaw := rawAt(action, 18)
	if isJSONNull(searchRaw) {
		return nil
	}
	search, err := rawArray(searchRaw, path+"[18]", evidence)
	if err != nil {
		return withMethod(err, assistantStreamMethod)
	}
	query, err := rawString(rawAt(search, 0), path+"[18][0]", evidence)
	if err != nil {
		return withMethod(err, assistantStreamMethod)
	}
	if d.grounding == nil {
		d.grounding = &GroundingMetadata{}
	}
	d.grounding.WebSearchQueries = append(d.grounding.WebSearchQueries, query)
	if summaryRaw := rawAt(search, 2); !isJSONNull(summaryRaw) {
		summary, err := rawString(summaryRaw, path+"[18][2]", evidence)
		if err != nil {
			return withMethod(err, assistantStreamMethod)
		}
		d.searches = append(d.searches, fmt.Sprintf(assistantSearchTranscript, query, summary))
	}
	if resultsRaw := rawAt(search, 3); !isJSONNull(resultsRaw) {
		results, err := rawArray(resultsRaw, path+"[18][3]", evidence)
		if err != nil {
			return withMethod(err, assistantStreamMethod)
		}
		for index, resultRaw := range results {
			resultPath := fmt.Sprintf("%s[18][3][%d]", path, index)
			result, err := rawArray(resultRaw, resultPath, evidence)
			if err != nil {
				return withMethod(err, assistantStreamMethod)
			}
			title, err := rawString(rawAt(result, 0), resultPath+"[0]", evidence)
			if err != nil {
				return withMethod(err, assistantStreamMethod)
			}
			uri, err := rawString(rawAt(result, 1), resultPath+"[1]", evidence)
			if err != nil {
				return withMethod(err, assistantStreamMethod)
			}
			d.grounding.Chunks = append(d.grounding.Chunks, GroundingChunk{Source: "web", URI: uri, Title: title})
		}
	}
	return nil
}

// End 在本轮回复结束、函数调用之后模型继续输出正文或流结束时返回扣下的正文或函数调用、搜索来源、各步骤用量之和与完成事件
func (d *AssistantStreamDecoder) End() ([]Event, error) {
	if !d.finished && !d.overran {
		return nil, &ProtocolEvidenceError{Method: assistantStreamMethod, Path: "$", Detail: "流结束前没有完成的模型回复"}
	}
	var total Usage
	for _, usage := range d.usage {
		total.InputTokens += usage.InputTokens
		total.CachedTokens += usage.CachedTokens
		total.OutputTokens += usage.OutputTokens
		total.TotalTokens += usage.TotalTokens
	}
	var events []Event
	switch {
	case d.block:
		events = d.callBlock(d.held + "\n" + assistantFence)
	case !d.called && d.held != "":
		events = []Event{{Kind: EventText, Text: d.held}}
	}
	if d.grounding != nil {
		events = append(events, Event{Kind: EventGrounding, Grounding: d.grounding})
	}
	if len(d.usage) > 0 {
		events = append(events, Event{Kind: EventUsage, Usage: &total})
	}
	finish := decodeFinishReason(1)
	if d.malformed {
		finish = decodeFinishReason(10)
	}
	return append(events, Event{Kind: EventFinish, FinishReason: finish}), nil
}

// decodeAssistantUsage 解码步骤用量 [未命中缓存的输入, 输出, 缓存命中]
func decodeAssistantUsage(raw json.RawMessage, path string, evidence json.RawMessage) (Usage, error) {
	values, err := rawArray(raw, path, evidence)
	if err != nil {
		return Usage{}, withMethod(err, assistantStreamMethod)
	}
	numbers := make([]int64, 3)
	for index := range numbers {
		if value := rawAt(values, index); !isJSONNull(value) {
			numbers[index], err = rawInt64(value, fmt.Sprintf("%s[%d]", path, index), evidence)
			if err != nil {
				return Usage{}, withMethod(err, assistantStreamMethod)
			}
		}
	}
	input := numbers[0] + numbers[2]
	return Usage{InputTokens: input, CachedTokens: numbers[2], OutputTokens: numbers[1], TotalTokens: input + numbers[1]}, nil
}
