package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

var errIncompleteStream = errors.New("upstream stream closed before finish")

const streamHeartbeatInterval = 10 * time.Second

type generationResult struct {
	events        []aistudio.Event
	text          strings.Builder
	reasoning     strings.Builder
	toolCalls     []aistudio.FunctionCall
	citations     []aistudio.Citation
	grounding     *aistudio.GroundingMetadata
	media         []aistudio.Blob
	usage         *aistudio.Usage
	finishReason  string
	finishMessage string
	stopSequence  string
	providerModel string
	finished      bool
	// safetyRatings 是候选的安全评级
	safetyRatings []aistudio.SafetyRating
	// responseID、turnToken、debugInfo 与 detailedFinishReason 是上游最后给出的 responseId、轮次令牌、调试信息与细分结束原因
	responseID           string
	turnToken            string
	debugInfo            json.RawMessage
	detailedFinishReason json.RawMessage
}

func (result *generationResult) apply(event aistudio.Event) error {
	if event.ProviderModel != "" {
		result.providerModel = event.ProviderModel
	}
	if event.ResponseID != "" {
		result.responseID = event.ResponseID
	}
	if event.TurnToken != "" {
		result.turnToken = event.TurnToken
	}
	if len(event.DebugInfo) > 0 {
		result.debugInfo = event.DebugInfo
	}
	if event.Usage != nil {
		usage := *event.Usage
		result.usage = &usage
	}
	switch event.Kind {
	case aistudio.EventError:
		return event.Err
	case aistudio.EventText:
		result.text.WriteString(event.Text)
	case aistudio.EventReasoning:
		result.reasoning.WriteString(event.Text)
	case aistudio.EventToolCall:
		result.toolCalls = append(result.toolCalls, *event.FunctionCall)
	case aistudio.EventGrounding:
		grounding := *event.Grounding
		result.grounding = &grounding
		result.citations = append(result.citations, groundingCitations(grounding)...)
	case aistudio.EventCitation:
		result.citations = append(result.citations, *event.Citation)
	case aistudio.EventMedia:
		if event.ExternalMedia != nil && (event.ExternalMedia.MIME == "" || event.ExternalMedia.URL == "") {
			return fmt.Errorf("upstream media is missing MIME or content")
		}
		if event.InlineData != nil {
			if event.InlineData.MIME == "" || len(event.InlineData.Data) == 0 {
				return fmt.Errorf("upstream media is missing MIME or content")
			}
			if count := len(result.events); count > 0 && result.events[count-1].Kind == aistudio.EventMedia && aistudio.MergePCM(&result.events[count-1].Part, event.Part) {
				result.media[len(result.media)-1].Data = result.events[count-1].InlineData.Data
				return nil
			}
			media := *event.InlineData
			media.Data = append([]byte(nil), media.Data...)
			event.InlineData = &media
			result.media = append(result.media, media)
		}
	case aistudio.EventFinish:
		result.finishReason = event.FinishReason
		result.finishMessage = event.FinishMessage
		result.stopSequence = event.StopSequence
		result.safetyRatings = event.SafetyRatings
		result.detailedFinishReason = event.DetailedFinishReason
		result.finished = true
	}
	result.events = append(result.events, event)
	return nil
}

// awaitStreamStart 在写出流式响应头前等待首个事件，首个事件为错误时返回该错误，心跳间隔内没有事件时直接返回原事件流
func awaitStreamStart(ctx context.Context, events <-chan aistudio.Event) (<-chan aistudio.Event, error) {
	timer := time.NewTimer(streamHeartbeatInterval)
	defer timer.Stop()
	var first aistudio.Event
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return events, nil
	case event, ok := <-events:
		if !ok {
			return nil, errIncompleteStream
		}
		if event.Kind == aistudio.EventError {
			return nil, event.Err
		}
		first = event
	}
	replay := make(chan aistudio.Event)
	go func() {
		defer close(replay)
		for event, ok := first, true; ok; event, ok = <-events {
			select {
			case replay <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return replay, nil
}

// consumeEvents 收集全部事件，不逐个转发也不写心跳
func consumeEvents(ctx context.Context, events <-chan aistudio.Event) (generationResult, error) {
	return consumeStreamEvents(ctx, events, nil, nil)
}

// consumeStreamEvents 收集事件并逐个交给 emit，heartbeat 不为空时在事件间隔达到心跳间隔时调用
func consumeStreamEvents(
	ctx context.Context,
	events <-chan aistudio.Event,
	emit func(aistudio.Event) error,
	heartbeat func() error,
) (result generationResult, resultErr error) {
	defer func() {
		SetAccessLogError(ctx, resultErr)
	}()
	heartbeatTimer := time.NewTimer(streamHeartbeatInterval)
	defer heartbeatTimer.Stop()
	var heartbeatTick <-chan time.Time
	if heartbeat != nil {
		heartbeatTick = heartbeatTimer.C
	}
	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-heartbeatTick:
			if err := heartbeat(); err != nil {
				return result, err
			}
			heartbeatTimer.Reset(streamHeartbeatInterval)
		case event, ok := <-events:
			if !ok {
				if !result.finished {
					return result, errIncompleteStream
				}
				return result, nil
			}
			if err := result.apply(event); err != nil {
				return result, err
			}
			if emit != nil {
				if err := emit(event); err != nil {
					return result, err
				}
			}
			heartbeatTimer.Reset(streamHeartbeatInterval)
		}
	}
}

func outputTokens(usage *aistudio.Usage) int64 {
	if usage == nil {
		return 0
	}
	return usage.OutputTokens + usage.ReasoningTokens
}

func inputTokens(usage *aistudio.Usage) int64 {
	if usage == nil {
		return 0
	}
	return usage.InputTokens + usage.ToolTokens
}

func providerFinishReason(reason string) string {
	if strings.HasPrefix(reason, "provider_") {
		return reason
	}
	return ""
}

// addSystemReminder 把对话中途的系统消息以 system-reminder 追加到上一个 user 轮，上一轮不是 user 时返回待并入下一个 user 轮的 reminder
func addSystemReminder(contents []aistudio.Content, pending []aistudio.Part, text string) []aistudio.Part {
	reminder := aistudio.Part{Text: "<system-reminder>\n" + text + "\n</system-reminder>"}
	if last := len(contents) - 1; last >= 0 && contents[last].Role == aistudio.RoleUser {
		contents[last].Parts = append(contents[last].Parts, reminder)
		return pending
	}
	return append(pending, reminder)
}

// appendWithReminders 追加一条消息，待并入的 reminder 排在下一个 user 消息的函数结果之后
func appendWithReminders(contents []aistudio.Content, content aistudio.Content, reminders []aistudio.Part) ([]aistudio.Content, []aistudio.Part) {
	if content.Role == aistudio.RoleUser && len(reminders) > 0 {
		results := 0
		for index, part := range content.Parts {
			if part.FunctionResult != nil {
				results = index + 1
			}
		}
		content.Parts = slices.Concat(content.Parts[:results], reminders, content.Parts[results:])
		reminders = nil
	}
	return append(contents, content), reminders
}

// prependSystemMedia 把系统指令中的媒体放到首个 user 轮开头，没有 user 轮时作为第一轮
func prependSystemMedia(contents []aistudio.Content, media []aistudio.Part) []aistudio.Content {
	if len(media) == 0 {
		return contents
	}
	index := slices.IndexFunc(contents, func(content aistudio.Content) bool { return content.Role == aistudio.RoleUser })
	if index < 0 {
		return append([]aistudio.Content{{Role: aistudio.RoleUser, Parts: media}}, contents...)
	}
	contents[index].Parts = append(slices.Clone(media), contents[index].Parts...)
	return contents
}

// functionResultMediaTypes 是工具结果内容块中按媒体发送的块类型
var functionResultMediaTypes = map[string]bool{
	"image": true, "document": true, "audio": true, "video": true, "file": true,
	"image_url": true, "input_image": true, "input_file": true, "input_audio": true, "video_url": true, "input_video": true,
}

// functionResultParts 把工具结果转成函数结果内容与紧随其后的媒体 part，文本块以换行相连作为结果文本，isError 时结果写在 error 键下
func functionResultParts(raw json.RawMessage, isError bool, parse func(json.RawMessage) ([]aistudio.Part, error)) (json.RawMessage, []aistudio.Part, error) {
	key := "result"
	if isError {
		key = "error"
	}
	var blocks []json.RawMessage
	if json.Unmarshal(raw, &blocks) == nil && len(blocks) > 0 {
		var texts []string
		var media []aistudio.Part
		contentBlocks := true
		for _, block := range blocks {
			var head struct {
				Type string  `json:"type"`
				Text *string `json:"text"`
			}
			if json.Unmarshal(block, &head) != nil || head.Type == "" {
				contentBlocks = false
				break
			}
			switch {
			case head.Text != nil:
				texts = append(texts, *head.Text)
			case functionResultMediaTypes[head.Type]:
				parts, err := parse(json.RawMessage("[" + string(block) + "]"))
				if err != nil {
					return nil, nil, err
				}
				media = append(media, parts...)
			default:
				texts = append(texts, string(block))
			}
		}
		if contentBlocks {
			content, _ := json.Marshal(map[string]string{key: strings.Join(texts, "\n")})
			return content, media, nil
		}
	}
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}
	if !json.Valid(raw) {
		return nil, nil, fmt.Errorf("function result must be JSON")
	}
	trimmed := strings.TrimSpace(string(raw))
	if !isError && strings.HasPrefix(trimmed, "{") {
		return raw, nil, nil
	}
	return json.RawMessage(`{"` + key + `":` + trimmed + `}`), nil, nil
}

func groundingCitations(metadata aistudio.GroundingMetadata) []aistudio.Citation {
	citations := make([]aistudio.Citation, 0)
	if len(metadata.Supports) == 0 {
		for _, chunk := range metadata.Chunks {
			if chunk.URI != "" {
				citations = append(citations, aistudio.Citation{URL: chunk.URI, Title: chunk.Title})
			}
		}
		return citations
	}
	for _, support := range metadata.Supports {
		for _, index := range support.ChunkIndices {
			if index < 0 || index >= len(metadata.Chunks) {
				continue
			}
			chunk := metadata.Chunks[index]
			if chunk.URI == "" {
				continue
			}
			citations = append(citations, aistudio.Citation{
				URL: chunk.URI, Title: chunk.Title,
				Start: support.Segment.StartIndex, End: support.Segment.EndIndex,
			})
		}
	}
	return citations
}

// textSpan 表示上游正文中一段连续文字在输出文本中的位置：source 与 output 分别为它在上游全文与输出文本中的字节偏移
type textSpan struct {
	source int
	output int
	length int
}

// spanCitation 把上游按全文字节计的引用区间换算为 output 文本中的字符下标，区间不在任何一段文字内时返回 false
func spanCitation(output string, spans []textSpan, citation aistudio.Citation) (int, int, bool) {
	for _, span := range spans {
		start, end := citation.Start-span.source, citation.End-span.source
		if start >= 0 && start <= end && end <= span.length {
			return utf8.RuneCountInString(output[:span.output+start]), utf8.RuneCountInString(output[:span.output+end]), true
		}
	}
	return 0, 0, false
}

func renderCodeExecution(event aistudio.Event) string {
	switch event.Kind {
	case aistudio.EventExecutableCode:
		language := strings.ToLower(event.ExecutableCode.Language)
		if language == "language_unspecified" {
			language = "text"
		}
		return "```" + language + "\n" + strings.TrimSuffix(event.ExecutableCode.Code, "\n") + "\n```"
	case aistudio.EventCodeExecutionResult:
		value := event.CodeExecutionResult.Output
		if value == "" {
			return ""
		}
		return "```text\n" + strings.TrimSuffix(value, "\n") + "\n```"
	default:
		return ""
	}
}

func renderCitationsMarkdown(citations []aistudio.Citation) string {
	if len(citations) == 0 {
		return ""
	}
	var output strings.Builder
	output.WriteString("Sources:\n")
	for _, citation := range citations {
		label := citation.Title
		if label == "" {
			label = citation.URL
		}
		fmt.Fprintf(&output, "- [%s](%s)\n", label, citation.URL)
	}
	return strings.TrimSuffix(output.String(), "\n")
}
