package api

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// interactionMedia 投影音频容器、采样参数及其他媒体内容
func interactionMedia(media aistudio.Media, audioFormat string) (map[string]any, error) {
	baseType, parameters, err := mime.ParseMediaType(media.MIME)
	if err != nil {
		return nil, err
	}
	if media.URL == "" && (baseType == "audio/wav" || baseType == "audio/x-wav") {
		media, err = wavPCM(media)
		if err != nil {
			return nil, err
		}
		baseType, parameters, err = mime.ParseMediaType(media.MIME)
		if err != nil {
			return nil, err
		}
	}
	typeName := strings.SplitN(baseType, "/", 2)[0]
	if typeName != "audio" && typeName != "video" && typeName != "image" {
		typeName = "document"
	}
	content := map[string]any{"type": typeName, "mime_type": baseType}
	if media.URL != "" {
		content["uri"] = media.URL
		return content, nil
	}
	data := media.Data
	if typeName == "audio" {
		data, _, err = encodeSpeechResponse(media, audioFormat)
		if err != nil {
			return nil, err
		}
		if audioFormat == "wav" {
			content["mime_type"] = "audio/wav"
		}
		if rate, parseErr := strconv.Atoi(parameters["rate"]); parseErr == nil {
			content["sample_rate"] = rate
		}
		channels := 1
		if value, parseErr := strconv.Atoi(parameters["channels"]); parseErr == nil {
			channels = value
		}
		content["channels"] = channels
	}
	content["data"] = base64.StdEncoding.EncodeToString(data)
	return content, nil
}

// interactionStep 将单个规范事件投影为步骤内容与对应增量
func interactionStep(event aistudio.Event, audioFormat string) (map[string]any, map[string]any, error) {
	var content map[string]any
	switch event.Kind {
	case aistudio.EventText:
		content = map[string]any{"type": "text", "text": event.Text}
	case aistudio.EventReasoning:
		content = map[string]any{"type": "text", "text": event.Text}
		step := map[string]any{"type": "thought", "summary": []map[string]any{content}}
		if event.ThoughtSignature != "" {
			step["signature"] = event.ThoughtSignature
		}
		return step, map[string]any{"type": "thought_summary", "content": content}, nil
	case aistudio.EventThoughtSignature:
		if event.ThoughtSignature == "" {
			return nil, nil, nil
		}
		return map[string]any{"type": "thought", "signature": event.ThoughtSignature}, map[string]any{"type": "thought_signature", "signature": event.ThoughtSignature}, nil
	case aistudio.EventToolCall:
		if event.ToolCall == nil {
			return nil, nil, nil
		}
		call := event.ToolCall
		return map[string]any{"type": "function_call", "id": call.ID, "name": call.Name, "arguments": call.Arguments}, nil, nil
	case aistudio.EventMedia:
		if event.Media == nil {
			return nil, nil, nil
		}
		var err error
		content, err = interactionMedia(*event.Media, audioFormat)
		if err != nil {
			return nil, nil, err
		}
	case aistudio.EventExecutableCode, aistudio.EventCodeExecutionResult:
		if text := renderCodeExecution(event); text != "" {
			content = map[string]any{"type": "text", "text": text}
		}
	}
	if content == nil {
		return nil, nil, nil
	}
	return map[string]any{"type": "model_output", "content": []map[string]any{content}}, content, nil
}

// interactionSteps 汇总完整内容并将所有 PCM 分块封装为一段音频
func interactionSteps(result generationResult, audioFormat string, summaries bool) ([]map[string]any, error) {
	steps := make([]map[string]any, 0)
	audioWritten := false
	for _, event := range result.events {
		if event.Kind == aistudio.EventToolCall && event.ToolCall != nil {
			if signature := interactionCallSignature(event); signature != "" {
				steps = append(steps, map[string]any{"type": "thought", "signature": signature})
			}
		}
		if event.Kind == aistudio.EventReasoning && !summaries {
			if event.ThoughtSignature == "" {
				continue
			}
			event.Kind = aistudio.EventThoughtSignature
		}
		if event.Kind == aistudio.EventMedia && event.Media != nil && strings.HasPrefix(event.Media.MIME, "audio/") {
			if audioWritten {
				continue
			}
			audio, err := joinedAudio(result.media)
			if err != nil {
				return nil, err
			}
			event.Media = &audio
			audioWritten = true
		}
		step, _, err := interactionStep(event, audioFormat)
		if err != nil {
			return nil, err
		}
		if step == nil {
			continue
		}
		if len(steps) > 0 && step["type"] == steps[len(steps)-1]["type"] && (step["type"] == "model_output" || step["type"] == "thought") {
			previous := steps[len(steps)-1]
			field := "content"
			if step["type"] == "thought" {
				field = "summary"
			}
			items, _ := step[field].([]map[string]any)
			prior, _ := previous[field].([]map[string]any)
			for _, item := range items {
				if len(prior) > 0 && item["type"] == "text" && prior[len(prior)-1]["type"] == "text" {
					prior[len(prior)-1]["text"] = prior[len(prior)-1]["text"].(string) + item["text"].(string)
				} else {
					prior = append(prior, item)
				}
			}
			if len(prior) > 0 {
				previous[field] = prior
			}
			if signature, ok := step["signature"]; ok {
				previous["signature"] = signature
			}
		} else {
			steps = append(steps, step)
		}
	}
	return steps, nil
}

// streamInteraction 输出创建、步骤增量、步骤结束与交互终态
func (s *server) streamInteraction(w http.ResponseWriter, r *http.Request, request interactionRequest, generate aistudio.GenerateRequest, current []aistudio.Content, created string, events <-chan aistudio.Event) {
	if err := streamHeaders(w); err != nil {
		return
	}
	send := func(kind string, value map[string]any) error {
		value["event_type"] = kind
		return writeSSE(w, kind, value)
	}
	if err := send("interaction.created", map[string]any{"interaction": interactionObject(generate, created, "in_progress", nil)}); err != nil {
		return
	}
	index := -1
	active := ""
	closeStep := func() error {
		if active == "" {
			return nil
		}
		active = ""
		return send("step.stop", map[string]any{"index": index})
	}
	var emit func(aistudio.Event) error
	emit = func(event aistudio.Event) error {
		if event.Kind == aistudio.EventReasoning && request.Generation.ThinkingSummaries == "none" {
			if event.ThoughtSignature == "" {
				return nil
			}
			event.Kind = aistudio.EventThoughtSignature
		}
		if signature := interactionCallSignature(event); signature != "" {
			if err := emit(aistudio.Event{Kind: aistudio.EventThoughtSignature, ThoughtSignature: signature}); err != nil {
				return err
			}
		}
		step, delta, err := interactionStep(event, request.audioFormat())
		if err != nil || step == nil {
			return err
		}
		kind := step["type"].(string)
		if active != kind || kind == "function_call" {
			if err := closeStep(); err != nil {
				return err
			}
			index++
			active = kind
			start := map[string]any{"type": kind}
			if kind == "model_output" {
				start["content"] = []any{}
			}
			if kind == "thought" {
				start["summary"] = []any{}
			}
			if kind == "function_call" {
				start = step
			}
			if err := send("step.start", map[string]any{"index": index, "step": start}); err != nil {
				return err
			}
		}
		if delta != nil {
			return send("step.delta", map[string]any{"index": index, "delta": delta})
		}
		return nil
	}
	bufferWAV := request.audioFormat() == "wav"
	result, err := consumeStreamEvents(r.Context(), events, func(event aistudio.Event) error {
		if bufferWAV && event.Kind == aistudio.EventMedia && event.Media != nil && strings.HasPrefix(event.Media.MIME, "audio/") {
			return nil
		}
		return emit(event)
	}, func() error { return writeSSEHeartbeat(w) })
	if err == nil && bufferWAV {
		for _, media := range result.media {
			if !strings.HasPrefix(media.MIME, "audio/") {
				continue
			}
			var audio aistudio.Media
			audio, err = joinedAudio(result.media)
			if err == nil {
				err = emit(aistudio.Event{Kind: aistudio.EventMedia, Media: &audio})
			}
			break
		}
	}
	if err == nil {
		err = validateInteractionResult(generate, result)
	}
	if err != nil {
		SetAccessLogError(r.Context(), err)
		if shouldWriteRequestError(r, err) {
			_ = send("error", map[string]any{"error": map[string]any{"code": geminiErrorStatus(err), "message": err.Error()}})
		}
		return
	}
	if err := closeStep(); err != nil {
		return
	}
	if request.Store == nil || *request.Store {
		s.storeResponseState(generate.ID, request.PreviousID, current, nil, result)
	}
	if err := send("interaction.completed", map[string]any{"interaction": interactionObject(generate, created, interactionStatus(result), result.usage)}); err != nil {
		SetAccessLogError(r.Context(), fmt.Errorf("interaction completion: %w", err))
	}
}

// interactionCallSignature 提取函数调用携带的可回传思考签名
func interactionCallSignature(event aistudio.Event) string {
	if event.Kind != aistudio.EventToolCall || event.ToolCall == nil {
		return ""
	}
	if event.ToolCall.ThoughtSignature != "" {
		return event.ToolCall.ThoughtSignature
	}
	return event.ThoughtSignature
}
