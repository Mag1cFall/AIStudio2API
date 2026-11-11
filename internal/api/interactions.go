package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// handleInteraction 将公开 Interactions 请求接入规范生成流程
func (s *server) handleInteraction(w http.ResponseWriter, r *http.Request) {
	var request interactionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	generate, err := request.toGenerateRequest(newID("int"))
	if err == nil && request.Background && request.Store != nil && !*request.Store {
		err = errors.New("store=false is incompatible with background=true")
	}
	if err == nil && request.WebhookConfig != nil {
		err = request.WebhookConfig.validate(request.Background)
	}
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if err := resolveGeminiFileNames(r.Context(), s.service, generate.Contents); err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiRequestError(w, err)
		}
		return
	}
	if err := inlineRemoteMedia(r.Context(), generate.Contents); err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		}
		return
	}
	generate = s.prepareSearch(generate)
	generate.Unary = !request.Stream || s.responseModes().FakeStream
	generate.Background = request.Background
	if !request.Stream && !request.Background {
		var stopKeepalive func()
		w, stopKeepalive = s.nonstreamKeepalive(w, r)
		defer stopKeepalive()
	}
	current := cloneResponseContents(generate.Contents)
	var previous responseHistory
	if request.PreviousID != "" {
		var ok bool
		previous, ok = s.responseStates.Load(request.PreviousID)
		if !ok || !previous.Interaction {
			writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "previous_interaction_id was not found")
			return
		}
		if previous.Upstream != "" && previous.Agent == generate.Model {
			if requested := requestedAccount(r); requested != "" && requested != previous.AccountID {
				writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "previous_interaction_id was created by a different account than X-AIStudio-Account")
				return
			}
			// 同一 agent 续接自己的后台任务时与官网一样只发送新输入并以其上游 interaction 续接，使用创建它的账户
			generate.PreviousInteractionID, generate.AccountID = previous.Upstream, previous.AccountID
		} else {
			generate.Contents = append(cloneResponseContents(previous.Contents), generate.Contents...)
		}
	}
	cache, ok := s.applyGeminiCache(w, request.CachedContent, &generate)
	if !ok {
		return
	}
	request.cache = cache
	generate.Tools.ToolConfig.ServerSideInvocations = interactionServerSteps(generate.Tools)
	if err := resolveInteractionResults(generate.Contents); err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	audioExpected := false
	if slices.Contains(generate.Config.ResponseModalities, aistudio.ResponseModalityAudio) {
		models, err := s.service.Models(r.Context())
		if err != nil {
			if shouldWriteRequestError(r, err) {
				writeGeminiRequestError(w, err)
			}
			return
		}
		model, _ := LookupPublicModel(models, generate.Model)
		audioExpected = slices.Contains(aistudio.EffectiveResponseModalities(generate.Config.ResponseModalities, model), aistudio.ResponseModalityAudio)
	}
	created := time.Now().UTC().Format(time.RFC3339)
	client := r.Context()
	ctx, cancel := context.WithCancel(client)
	defer cancel()
	failure := &bytes.Buffer{}
	var following sync.WaitGroup
	if request.Background {
		var finish func()
		ctx, finish = s.startBackground(r, generate.ID)
		defer finish()
		request.stream = newStreamLog()
		defer following.Wait()
		defer request.stream.finish()
		defer s.endBackgroundInteraction(ctx, request, generate, previous, current, created, failure)
		started := interactionObject(generate, request.PreviousID, created, "in_progress", nil)
		s.storeInteraction(request, previous, current, generationResult{}, started)
		if !request.Stream {
			body, _ := json.Marshal(started)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.Header().Set("Connection", "close")
			_, _ = w.Write(body)
			_ = http.NewResponseController(w).Flush()
			w = discardResponseWriter{header: http.Header{}, body: failure}
		}
	}
	r = r.WithContext(ctx)
	events, err := s.service.Generate(ctx, generate)
	if err == nil && request.Stream {
		events, _, err = awaitStreamStart(ctx, events)
	}
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiRequestError(w, err)
		}
		if request.Background && request.Stream {
			writeGeminiRequestError(discardResponseWriter{header: http.Header{}, body: failure}, err)
		}
		if request.Background && shouldWriteRequestError(r, err) {
			_ = writeSSE(request.stream, "error", officialOrder("interactions", interactionErrorEvent(err, "1")))
		}
		return
	}
	if request.imageMIME() == "image/jpeg" {
		events = jpegImageEvents(ctx, events)
	}
	if request.Background {
		if request.Stream {
			following.Add(1)
			go func() {
				defer following.Done()
				out, err := openStream(w, sseHeartbeat)
				defer out.stop()
				if err == nil {
					_ = request.stream.follow(client, out, -1)
				}
			}()
		}
		if err := s.streamInteraction(request.stream, r, request, generate, audioExpected, previous, current, created, events); err != nil {
			writeGeminiRequestError(discardResponseWriter{header: http.Header{}, body: failure}, err)
		}
		return
	}
	if request.Stream {
		out, err := openStream(w, sseHeartbeat)
		defer out.stop()
		if err == nil {
			_ = s.streamInteraction(out, r, request, generate, audioExpected, previous, current, created, events)
		}
		return
	}
	result, err := consumeEvents(ctx, events)
	if err == nil {
		err = validateInteractionResult(audioExpected, result)
	}
	if err == nil {
		result.usage = request.cache.usage(result.usage)
		var steps []map[string]any
		steps, err = interactionSteps(result, request.audioFormat(), request.Generation.ThinkingSummaries != "none")
		if err == nil {
			response := interactionObject(generate, request.PreviousID, created, interactionStatus(result), &result)
			response["steps"] = steps
			if request.Store == nil || *request.Store {
				s.storeInteraction(request, previous, current, result, response)
			}
			writeJSON(w, http.StatusOK, officialOrder("interactions", response))
			return
		}
	}
	if shouldWriteRequestError(r, err) {
		writeGeminiRequestError(w, err)
	}
}

// endBackgroundInteraction 在后台交互结束时把仍为 in_progress 的记录保存为终态：被取消时为 cancelled，其他情况为 failed；带 webhook_config 时发送终态事件，failure 为后台请求失败时写出的 Gemini 错误
func (s *server) endBackgroundInteraction(ctx context.Context, request interactionRequest, generate aistudio.GenerateRequest, previous responseHistory, current []aistudio.Content, created string, failure *bytes.Buffer) {
	record, ok := s.responseStates.Interaction(generate.ID)
	if !ok {
		return
	}
	var stored struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(record.object, &stored)
	if stored.Status == "in_progress" {
		stored.Status = "failed"
		if errors.Is(ctx.Err(), context.Canceled) {
			stored.Status = "cancelled"
		}
		s.storeInteraction(request, previous, current, generationResult{}, interactionObject(generate, request.PreviousID, created, stored.Status, nil))
	}
	if request.WebhookConfig != nil {
		sendInteractionWebhook(*request.WebhookConfig, generate.ID, stored.Status, failure.Bytes())
	}
}

// discardResponseWriter 把后台交互在立即返回之后的写出记入 body
type discardResponseWriter struct {
	header http.Header
	body   *bytes.Buffer
}

func (w discardResponseWriter) Header() http.Header            { return w.header }
func (w discardResponseWriter) Write(data []byte) (int, error) { return w.body.Write(data) }
func (w discardResponseWriter) WriteHeader(int)                {}

// jpegImageEvents 将非 JPEG 的图片输出转码为 JPEG
func jpegImageEvents(ctx context.Context, events <-chan aistudio.Event) <-chan aistudio.Event {
	converted := make(chan aistudio.Event)
	go func() {
		defer close(converted)
		for event := range events {
			if media := event.InlineData; event.Kind == aistudio.EventMedia && media != nil && strings.HasPrefix(media.MIME, "image/") {
				converted, err := convertImage(*media)
				if err != nil {
					event = aistudio.Event{Kind: aistudio.EventError, Err: err}
				} else {
					event.InlineData = &converted
				}
			}
			select {
			case converted <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return converted
}

// validateInteractionResult 确认模型实际生成音频时已收到可用音频内容
func validateInteractionResult(audioExpected bool, result generationResult) error {
	if !audioExpected {
		return nil
	}
	_, err := joinedAudio(result.media)
	return err
}

// resolveInteractionResults 从完整调用历史补齐函数结果名称
func resolveInteractionResults(contents []aistudio.Content) error {
	calls := make(map[string]string)
	for _, content := range contents {
		for _, part := range content.Parts {
			if part.FunctionCall != nil {
				calls[part.FunctionCall.ID] = part.FunctionCall.Name
			}
			if result := part.FunctionResult; result != nil {
				name := calls[result.ID]
				if name == "" {
					name = result.Name
				}
				if name == "" || result.Name != "" && result.Name != name {
					return fmt.Errorf("function result %q must match a preceding function call", result.ID)
				}
				if result.Name == "" {
					result.Name = name
				}
			}
		}
	}
	return nil
}

// interactionObject 构造 SDK 使用的资源标识、前序交互、状态与用量，result 为空时不带用量
func interactionObject(request aistudio.GenerateRequest, previousID string, created, status string, result *generationResult) map[string]any {
	object := map[string]any{
		"id": request.ID, "object": "interaction", "model": request.Model,
		"created": created, "updated": time.Now().UTC().Format(time.RFC3339), "status": status,
	}
	if previousID != "" {
		object["previous_interaction_id"] = previousID
	}
	if result != nil && result.environmentID != "" {
		object["environment_id"] = result.environmentID
	}
	if result != nil && result.usage != nil {
		usage := result.usage
		fields := map[string]any{
			"total_input_tokens": usage.InputTokens, "total_cached_tokens": usage.CachedTokens, "total_output_tokens": usage.OutputTokens,
			"total_thought_tokens": usage.ReasoningTokens, "total_tool_use_tokens": usage.ToolTokens,
			"total_tokens": usage.TotalTokens,
		}
		if details := usage.Details; details != nil {
			for name, counts := range map[string][]aistudio.ModalityTokenCount{
				"input_tokens_by_modality": details.PromptTokensDetails, "cached_tokens_by_modality": details.CacheTokensDetails,
				"output_tokens_by_modality": details.CandidatesTokensDetails, "tool_use_tokens_by_modality": details.ToolUsePromptTokensDetails,
			} {
				if len(counts) > 0 {
					fields[name] = interactionModalityTokens(counts)
				}
			}
		}
		if counts := interactionGroundingToolCount(result.events); len(counts) > 0 {
			fields["grounding_tool_count"] = counts
		}
		object["usage"] = fields
	}
	return object
}

// groundingToolTypes 是服务端工具类型对应的 Interactions grounding_tool_count 类型
var groundingToolTypes = map[string]string{"GOOGLE_SEARCH_WEB": "google_search", "GOOGLE_SEARCH_IMAGE": "google_search", "GOOGLE_MAPS": "google_maps"}

// interactionGroundingToolCount 按工具类型统计搜索与地图的调用步骤数
func interactionGroundingToolCount(events []aistudio.Event) []map[string]any {
	var counts []map[string]any
	for _, event := range events {
		if event.ToolCall == nil {
			continue
		}
		kind, ok := groundingToolTypes[event.ToolCall.Type]
		if !ok {
			continue
		}
		index := slices.IndexFunc(counts, func(item map[string]any) bool { return item["type"] == kind })
		if index < 0 {
			counts = append(counts, map[string]any{"type": kind, "count": 0})
			index = len(counts) - 1
		}
		counts[index]["count"] = counts[index]["count"].(int) + 1
	}
	return counts
}

// interactionModalityTokens 把 Gemini 模态细分转换为 Interactions 的 {modality,tokens}
func interactionModalityTokens(counts []aistudio.ModalityTokenCount) []map[string]any {
	items := make([]map[string]any, 0, len(counts))
	for _, count := range counts {
		items = append(items, map[string]any{"modality": strings.ToLower(count.Modality), "tokens": count.TokenCount})
	}
	return items
}

// interactionStatus 将生成终态转换为交互资源状态，输出上限与内容拦截为 incomplete
func interactionStatus(result generationResult) string {
	switch openAIFinishReason(result.finishReason, len(result.toolCalls) > 0) {
	case "length", "content_filter":
		return "incomplete"
	case "tool_calls":
		return "requires_action"
	}
	return "completed"
}
