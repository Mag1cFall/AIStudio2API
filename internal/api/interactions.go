package api

import (
	"context"
	"fmt"
	"net/http"
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
	current := cloneResponseContents(generate.Contents)
	if request.PreviousID != "" {
		previous, _, ok := s.responseStates.Load(request.PreviousID)
		if !ok || !strings.HasPrefix(request.PreviousID, "int_") {
			writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "previous_interaction_id was not found")
			return
		}
		generate.Contents = append(previous, generate.Contents...)
	}
	if err := resolveInteractionResults(generate.Contents); err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	r = r.WithContext(ctx)
	events, err := s.service.Generate(ctx, generate)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiError(w, statusFromError(err), geminiErrorStatus(err), err.Error())
		}
		return
	}
	created := time.Now().UTC().Format(time.RFC3339)
	if request.Stream {
		s.streamInteraction(w, r, request, generate, current, created, events)
		return
	}
	result, err := consumeEvents(ctx, events, nil)
	if err == nil {
		err = validateInteractionResult(generate, result)
	}
	if err == nil {
		var steps []map[string]any
		steps, err = interactionSteps(result, request.audioFormat(), request.Generation.ThinkingSummaries != "none")
		if err == nil {
			if request.Store == nil || *request.Store {
				s.storeResponseState(generate.ID, request.PreviousID, current, nil, result)
			}
			response := interactionObject(generate, created, interactionStatus(result), result.usage)
			response["steps"] = steps
			writeJSON(w, http.StatusOK, response)
			return
		}
	}
	if shouldWriteRequestError(r, err) {
		writeGeminiError(w, statusFromError(err), geminiErrorStatus(err), err.Error())
	}
}

// validateInteractionResult 确认请求音频时已收到可用音频内容
func validateInteractionResult(request aistudio.GenerateRequest, result generationResult) error {
	for _, modality := range request.Config.ResponseModalities {
		if modality == aistudio.ResponseModalityAudio {
			_, err := joinedAudio(result.media)
			return err
		}
	}
	return nil
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
