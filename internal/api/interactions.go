package api

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// handleInteraction 将公开 Interactions 请求接入规范生成链路
func (s *server) handleInteraction(w http.ResponseWriter, r *http.Request) {
	var request interactionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	generate, err := request.toGenerateRequest(newID("int"))
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if err := inlineRemoteMedia(r.Context(), generate.Contents); err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		}
		return
	}
	generate.Unary = !request.Stream
	current := cloneResponseContents(generate.Contents)
	var previous responseHistory
	if request.PreviousID != "" {
		var ok bool
		previous, ok = s.responseStates.Load(request.PreviousID)
		if !ok || !previous.Interaction {
			writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "previous_interaction_id was not found")
			return
		}
		generate.Contents = append(cloneResponseContents(previous.Contents), generate.Contents...)
	}
	if err := resolveInteractionResults(generate.Contents); err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	audioExpected := false
	if slices.Contains(generate.Config.ResponseModalities, aistudio.ResponseModalityAudio) {
		models, err := s.service.Models(r.Context())
		if err != nil {
			if shouldWriteRequestError(r, err) {
				writeGeminiError(w, statusFromError(err), geminiErrorStatus(err), err.Error())
			}
			return
		}
		model, _ := lookupPublicModel(models, generate.Model)
		audioExpected = interactionAudioExpected(generate.Config.ResponseModalities, model)
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	r = r.WithContext(ctx)
	events, err := s.service.Generate(ctx, generate)
	if err == nil && request.Stream {
		events, err = awaitStreamStart(ctx, events)
	}
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiError(w, statusFromError(err), geminiErrorStatus(err), err.Error())
		}
		return
	}
	if request.imageMIME() == "image/jpeg" {
		events = jpegImageEvents(ctx, events)
	}
	created := time.Now().UTC().Format(time.RFC3339)
	if request.Stream {
		s.streamInteraction(w, r, request, generate, audioExpected, previous, current, created, events)
		return
	}
	result, err := consumeEvents(ctx, events, nil)
	if err == nil {
		err = validateInteractionResult(audioExpected, result)
	}
	if err == nil {
		var steps []map[string]any
		steps, err = interactionSteps(result, request.audioFormat(), request.Generation.ThinkingSummaries != "none")
		if err == nil {
			response := interactionObject(generate, created, interactionStatus(result), result.usage)
			response["steps"] = steps
			if request.Store == nil || *request.Store {
				s.storeInteraction(request, previous, current, result, response)
			}
			writeJSON(w, http.StatusOK, response)
			return
		}
	}
	if shouldWriteRequestError(r, err) {
		writeGeminiError(w, statusFromError(err), geminiErrorStatus(err), err.Error())
	}
}

// jpegImageEvents 将非 JPEG 的图片输出转码为 JPEG
func jpegImageEvents(ctx context.Context, events <-chan aistudio.Event) <-chan aistudio.Event {
	converted := make(chan aistudio.Event)
	go func() {
		defer close(converted)
		for event := range events {
			if media := event.Media; event.Kind == aistudio.EventMedia && media != nil && len(media.Data) > 0 && strings.HasPrefix(media.MIME, "image/") && !strings.HasPrefix(media.MIME, "image/jpeg") {
				decoded, _, err := image.Decode(bytes.NewReader(media.Data))
				var encoded bytes.Buffer
				if err == nil {
					err = jpeg.Encode(&encoded, decoded, nil)
				}
				if err != nil {
					event = aistudio.Event{Kind: aistudio.EventError, Err: fmt.Errorf("convert %s output to image/jpeg: %w", media.MIME, err)}
				} else {
					jpegMedia := *media
					jpegMedia.MIME, jpegMedia.Data = "image/jpeg", encoded.Bytes()
					event.Media = &jpegMedia
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

// interactionAudioExpected 判断请求的 AUDIO 是否对该模型生效
func interactionAudioExpected(modalities []aistudio.ResponseModality, model aistudio.Model) bool {
	return slices.Contains(aistudio.EffectiveResponseModalities(modalities, model), aistudio.ResponseModalityAudio)
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

// interactionObject 构造 SDK 使用的资源标识、状态与用量
func interactionObject(request aistudio.GenerateRequest, created, status string, usage *aistudio.Usage) map[string]any {
	object := map[string]any{
		"id": request.ID, "object": "interaction", "model": request.Model,
		"created": created, "updated": time.Now().UTC().Format(time.RFC3339), "status": status,
	}
	if usage != nil {
		object["usage"] = map[string]any{
			"total_input_tokens": usage.InputTokens, "total_output_tokens": usage.OutputTokens,
			"total_thought_tokens": usage.ReasoningTokens, "total_tool_use_tokens": usage.ToolTokens,
			"total_tokens": usage.TotalTokens,
		}
	}
	return object
}

// interactionStatus 将生成终态转换为交互资源状态
func interactionStatus(result generationResult) string {
	if result.finishReason != "stop" && result.finishReason != "tool_calls" {
		return "incomplete"
	}
	if len(result.toolCalls) > 0 {
		return "requires_action"
	}
	return "completed"
}
