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
func interactionStep(event aistudio.Event, audio interactionAudioFormat) (map[string]any, map[string]any, error) {
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
		media := *event.Media
		var err error
		if strings.HasPrefix(media.MIME, "audio/") {
			if media, err = audio.encode(media); err != nil {
				return nil, nil, err
			}
		}
		content, err = interactionMedia(media, audio.container())
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

// interactionText 表示一段 text 内容及其文字在上游全文中的位置
type interactionText struct {
	content map[string]any
	spans   []textSpan
}

// interactionCitations 返回引用事件与 grounding 事件携带的引用
func interactionCitations(event aistudio.Event) []aistudio.Citation {
	switch {
	case event.Kind == aistudio.EventCitation && event.Citation != nil:
		return []aistudio.Citation{*event.Citation}
	case event.Kind == aistudio.EventGrounding && event.Grounding != nil:
		return groundingCitations(*event.Grounding)
	}
	return nil
}

// interactionAnnotation 返回 url_citation 注释：有区间的引用把上游全文字节区间换算为该段文本中的字节下标，区间不在该段文本内时返回 false
func interactionAnnotation(citation aistudio.Citation, spans []textSpan) (map[string]any, bool) {
	annotation := map[string]any{"type": "url_citation", "url": citation.URL}
	if citation.Title != "" {
		annotation["title"] = citation.Title
	}
	if citation.End == 0 {
		return annotation, true
	}
	for _, span := range spans {
		start, end := citation.Start-span.source, citation.End-span.source
		if start >= 0 && start <= end && end <= span.length {
			annotation["start_index"], annotation["end_index"] = span.output+start, span.output+end
			return annotation, true
		}
	}
	return nil, false
}

// appendTextSpan 追加正文区间，与上一区间在原文与输出中都相接时合并为一段
func appendTextSpan(spans []textSpan, span textSpan) []textSpan {
	if last := len(spans) - 1; last >= 0 && spans[last].source+spans[last].length == span.source && spans[last].output+spans[last].length == span.output {
		spans[last].length += span.length
		return spans
	}
	return append(spans, span)
}

// annotateInteractionTexts 把引用写入所在 text 内容的 annotations，没有区间的引用写入最后一段 text 内容
func annotateInteractionTexts(texts []*interactionText, citations []aistudio.Citation) {
	if len(texts) == 0 {
		return
	}
	for _, citation := range citations {
		targets := texts
		if citation.End == 0 {
			targets = texts[len(texts)-1:]
		}
		for _, text := range targets {
			if annotation, ok := interactionAnnotation(citation, text.spans); ok {
				annotations, _ := text.content["annotations"].([]map[string]any)
				text.content["annotations"] = append(annotations, annotation)
				break
			}
		}
	}
}

// interactionSteps 汇总完整内容并将所有 PCM 分块封装为一段音频，引用写入所在 text 内容
func interactionSteps(result generationResult, audio interactionAudioFormat, summaries bool) ([]map[string]any, error) {
	steps := make([]map[string]any, 0)
	audioWritten := false
	texts := make([]*interactionText, 0)
	var current *interactionText
	source := 0
	for _, event := range result.events {
		if signature := interactionOutputSignature(event); signature != "" {
			steps = append(steps, map[string]any{"type": "thought", "signature": signature})
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
		step, _, err := interactionStep(event, audio)
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
					current = nil
					if field == "content" && item["type"] == "text" {
						current = &interactionText{content: item}
						texts = append(texts, current)
					}
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
			current = nil
			if content, _ := step["content"].([]map[string]any); step["type"] == "model_output" && len(content) == 1 && content[0]["type"] == "text" {
				current = &interactionText{content: content[0]}
				texts = append(texts, current)
			}
		}
		if event.Kind == aistudio.EventText && current != nil {
			text := current.content["text"].(string)
			current.spans = appendTextSpan(current.spans, textSpan{source: source, output: len(text) - len(event.Text), length: len(event.Text)})
			source += len(event.Text)
		}
	}
	annotateInteractionTexts(texts, result.citations)
	return steps, nil
}

// streamInteraction 输出创建、步骤增量、步骤结束与交互终态
func (s *server) streamInteraction(w http.ResponseWriter, r *http.Request, request interactionRequest, generate aistudio.GenerateRequest, audioExpected bool, previous responseHistory, current []aistudio.Content, created string, events <-chan aistudio.Event) {
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
	audio := request.audioFormat()
	index := -1
	active := ""
	var text *interactionText
	textLength, source := 0, 0
	pendingSignature := ""
	closeStep := func() error {
		if active == "" {
			return nil
		}
		active = ""
		return send("step.stop", map[string]any{"index": index})
	}
	var emit func(aistudio.Event) error
	// flushSignature 输出正文步骤打开期间暂存的独立思考签名
	flushSignature := func() error {
		if pendingSignature == "" {
			return nil
		}
		signature := pendingSignature
		pendingSignature, text = "", nil
		return emit(aistudio.Event{Kind: aistudio.EventThoughtSignature, ThoughtSignature: signature})
	}
	emit = func(event aistudio.Event) error {
		if event.Kind == aistudio.EventReasoning && request.Generation.ThinkingSummaries == "none" {
			if event.ThoughtSignature == "" {
				return nil
			}
			event.Kind = aistudio.EventThoughtSignature
		}
		citations := interactionCitations(event)
		if signature := interactionOutputSignature(event); signature != "" {
			if err := emit(aistudio.Event{Kind: aistudio.EventThoughtSignature, ThoughtSignature: signature}); err != nil {
				return err
			}
		}
		if len(citations) > 0 {
			if text == nil {
				return nil
			}
			annotations := make([]map[string]any, 0, len(citations))
			for _, citation := range citations {
				if annotation, ok := interactionAnnotation(citation, text.spans); ok {
					annotations = append(annotations, annotation)
				}
			}
			if len(annotations) == 0 {
				return nil
			}
			return send("step.delta", map[string]any{"index": index, "delta": map[string]any{"type": "text_annotation_delta", "annotations": annotations}})
		}
		step, delta, err := interactionStep(event, audio)
		if err != nil || step == nil {
			return err
		}
		kind := step["type"].(string)
		if delta == nil || delta["type"] != "text" || kind != "model_output" {
			text = nil
		} else if text == nil {
			text, textLength = &interactionText{}, 0
		}
		if text != nil {
			length := len(delta["text"].(string))
			if event.Kind == aistudio.EventText {
				text.spans = appendTextSpan(text.spans, textSpan{source: source, output: textLength, length: length})
				source += length
			}
			textLength += length
		}
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
	result, err := consumeStreamEvents(r.Context(), events, func(event aistudio.Event) error {
		if event.Kind == aistudio.EventMedia && event.Media != nil && strings.HasPrefix(event.Media.MIME, "audio/") && !audio.streams(*event.Media) {
			return nil
		}
		if len(interactionCitations(event)) == 0 {
			if err := flushSignature(); err != nil {
				return err
			}
		}
		if text != nil && (event.Kind == aistudio.EventThoughtSignature || event.Kind == aistudio.EventText && event.Text == "" && event.ThoughtSignature != "") {
			pendingSignature = event.ThoughtSignature
			return nil
		}
		return emit(event)
	}, func() error { return writeSSEHeartbeat(w) })
	if err == nil {
		for _, media := range result.media {
			if !strings.HasPrefix(media.MIME, "audio/") || audio.streams(media) {
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
		err = validateInteractionResult(audioExpected, result)
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
	completed := interactionObject(generate, created, interactionStatus(result), result.usage)
	if request.Store == nil || *request.Store {
		s.storeInteraction(request, previous, current, result, completed)
	}
	if err := send("interaction.completed", map[string]any{"interaction": completed}); err != nil {
		SetAccessLogError(r.Context(), fmt.Errorf("interaction completion: %w", err))
	}
}

// interactionOutputSignature 提取函数调用、正文与媒体携带的可回传思考签名，输出为其前的 thought 步骤
func interactionOutputSignature(event aistudio.Event) string {
	switch event.Kind {
	case aistudio.EventToolCall:
		if event.ToolCall != nil && event.ToolCall.ThoughtSignature != "" {
			return event.ToolCall.ThoughtSignature
		}
		return event.ThoughtSignature
	case aistudio.EventText, aistudio.EventMedia:
		return event.ThoughtSignature
	}
	return ""
}
