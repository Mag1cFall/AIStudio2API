package aistudio

import (
	"context"
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
	// assistantCleanupTimeout 是取消生成与删除临时应用的期限
	assistantCleanupTimeout = time.Minute
)

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
	model.Capabilities = map[string]bool{"assistant_route": true}
	for _, name := range []string{"chat_model", "thinking", "thinking_level"} {
		if base.model.Capabilities[name] {
			model.Capabilities[name] = true
		}
	}
	model.CapabilityOptions = nil
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

// EncodeAssistantRequest 编码 CodeAssistantOffline：field 1 对话轮、6 模式、8 模型、9 自定义指令、12 应用、14 框架、16、19 思考配置、29 请求 ID，field 2 由 WAA proof 写入
func EncodeAssistantRequest(request GenerateRequest, defaults GenerationDefaults, applet string, requestID string) ([]byte, error) {
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
	if request.System != "" {
		wire[8] = request.System
	}
	wire[11] = applet
	wire[13] = []any{int64(2), nil, int64(2)}
	wire[15] = int64(1)
	wire[18] = []any{int64(1), nil, nil, level}
	wire[28] = requestID
	return json.Marshal(wire)
}

// sendAssistant 新建临时应用并提交 Build 写代码助手生成任务，返回任务流；流关闭后取消未完成的任务并删除应用
func (c *Client) sendAssistant(ctx context.Context, request GenerateRequest, entry modelEntry) (*RPCResponse, func(io.Reader, func(Event) error) error, error) {
	reason := ""
	if request.Unary {
		reason = "Build 写代码助手使用服务端流，收集完成后返回"
	}
	reportUpstreamMode(ctx, assistantOfflineMethod, "stream", reason)
	contents, err := c.uploadAssistantAttachments(ctx, request.AccountID, request.Contents)
	if err != nil {
		return nil, nil, err
	}
	request.Contents = contents
	applet, err := c.createAssistantApplet(ctx, request.AccountID)
	if err != nil {
		return nil, nil, err
	}
	cleanup := &assistantCleanup{client: c, accountID: request.AccountID, applet: applet}
	body, err := EncodeAssistantRequest(request, entry.defaults, applet, strings.ToUpper(uuid.NewString()))
	if err != nil {
		cleanup.run()
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	rpc := newRPCRequest(assistantOfflineMethod, request.AccountID, request.ID, body, false)
	c.applyBenefitTier(rpc.Method, request.AccountID, rpc.Header)
	response, err := c.protected.DoProtected(ctx, request, rpc)
	if err == nil {
		response, err = validateRPCResponse(assistantOfflineMethod, response)
	}
	if err != nil {
		cleanup.run()
		return nil, nil, fmt.Errorf("发送 AI Studio %s: %w", assistantOfflineMethod, err)
	}
	cleanup.generation, err = decodeAssistantGeneration(response.Body)
	response.Body.Close()
	if err != nil {
		cleanup.run()
		return nil, nil, err
	}
	streamBody, err := json.Marshal([]any{cleanup.generation, nil, nil, applet})
	if err != nil {
		cleanup.run()
		return nil, nil, err
	}
	stream, err := c.do(ctx, assistantStreamMethod, request.AccountID, request.ID, streamBody, true)
	if err != nil {
		cleanup.run()
		return nil, nil, err
	}
	stream.Body = &assistantStreamBody{ReadCloser: stream.Body, cleanup: cleanup}
	return stream, func(source io.Reader, emit func(Event) error) error {
		err := DecodeAssistantStream(source, &AssistantStreamDecoder{}, emit)
		if err == nil {
			cleanup.completed.Store(true)
		}
		return err
	}, nil
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
	body, err := json.Marshal([]any{nil, []any{"Untitled", "Generated by Gemini.", nil, nil, "default"}, nil, nil, int64(1)})
	if err != nil {
		return "", err
	}
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

// assistantCleanup 在请求结束后取消未完成的生成任务并删除临时应用
type assistantCleanup struct {
	client     *Client
	accountID  string
	applet     string
	generation string
	completed  atomic.Bool
	once       sync.Once
}

// run 在后台以独立期限执行一次清理
func (cleanup *assistantCleanup) run() {
	cleanup.once.Do(func() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), assistantCleanupTimeout)
			defer cancel()
			if cleanup.generation != "" && !cleanup.completed.Load() {
				cleanup.call(ctx, "CancelCodeAssistantOfflineGeneration", []any{cleanup.generation, cleanup.applet})
			}
			cleanup.call(ctx, "DeleteApplet", []any{cleanup.applet})
		}()
	})
}

func (cleanup *assistantCleanup) call(ctx context.Context, method string, value []any) {
	body, err := json.Marshal(value)
	if err == nil {
		var response *RPCResponse
		response, err = cleanup.client.do(ctx, method, cleanup.accountID, "", body, false)
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			err = errors.Join(err, response.Body.Close())
		}
	}
	if err != nil {
		slog.Warn("Build 写代码助手清理失败", "account", cleanup.accountID, "method", method, "applet", cleanup.applet, "error", err)
	}
}

// assistantStreamBody 在任务流关闭时触发清理
type assistantStreamBody struct {
	io.ReadCloser
	cleanup *assistantCleanup
}

// Close 关闭任务流并清理临时应用
func (body *assistantStreamBody) Close() error {
	err := body.ReadCloser.Close()
	body.cleanup.run()
	return err
}

// AssistantStreamDecoder 把写代码助手的步骤快照解码为增量事件
type AssistantStreamDecoder struct {
	emitted   map[int64][]string
	usage     map[int64]Usage
	lastStep  int64
	wroteText bool
	finished  bool
}

// DecodeAssistantStream 解码 StreamCodeAssistantOfflineGeneration 响应：field 1 为消息列表，其后可带 google.rpc 状态
func DecodeAssistantStream(source io.Reader, decoder *AssistantStreamDecoder, emit func(Event) error) error {
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
	for ; stream.More(); index++ {
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
	if token, err := stream.Token(); err != nil || token != json.Delim(']') {
		return fail("$[0]", "消息列表没有正常结束")
	}
	for stream.More() {
		var raw json.RawMessage
		if err := stream.Decode(&raw); err != nil {
			return fail("$", err.Error())
		}
		if err := assistantTrailerError(raw); err != nil {
			return err
		}
	}
	if err := reader.failure(assistantStreamMethod, index, nil); err != nil {
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

// Decode 解码一条消息 [null, null, [步骤...], 应用 ID]：步骤 field 1 为对话轮、6 为失败原因、12 为序号、13 为状态（1 完成、2 进行中、4 失败）、16 为用量
func (d *AssistantStreamDecoder) Decode(raw json.RawMessage, path string) ([]Event, error) {
	if d.emitted == nil {
		d.emitted = make(map[int64][]string)
		d.usage = make(map[int64]Usage)
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
	contentsRaw := rawAt(step, 0)
	if isJSONNull(contentsRaw) {
		return nil, nil
	}
	contents, err := rawArray(contentsRaw, path+"[0]", evidence)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	var events []Event
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
		return []Event{{Kind: EventReasoning, Text: delta}}, nil
	}
	if d.wroteText && previous == "" && step != d.lastStep {
		delta = "\n\n" + delta
	}
	d.wroteText = true
	d.lastStep = step
	return []Event{{Kind: EventText, Text: delta}}, nil
}

// End 在流结束时返回各步骤用量之和与完成事件
func (d *AssistantStreamDecoder) End() ([]Event, error) {
	if !d.finished {
		return nil, &ProtocolEvidenceError{Method: assistantStreamMethod, Path: "$", Detail: "流结束前没有完成的模型回复"}
	}
	var total Usage
	for _, usage := range d.usage {
		total.InputTokens += usage.InputTokens
		total.CachedTokens += usage.CachedTokens
		total.OutputTokens += usage.OutputTokens
		total.TotalTokens += usage.TotalTokens
	}
	events := make([]Event, 0, 2)
	if len(d.usage) > 0 {
		events = append(events, Event{Kind: EventUsage, Usage: &total})
	}
	return append(events, Event{Kind: EventFinish, FinishReason: decodeFinishReason(1)}), nil
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

// assistantTrailerError 解码消息列表之后的 google.rpc 状态
func assistantTrailerError(raw json.RawMessage) error {
	err := buildTrailerError(raw)
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		rpcErr.Method = assistantStreamMethod
	}
	var evidence *ProtocolEvidenceError
	if errors.As(err, &evidence) {
		evidence.Method = assistantStreamMethod
	}
	return err
}
