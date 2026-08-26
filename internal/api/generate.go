package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

var errIncompleteStream = errors.New("upstream stream closed before finish")
var errUpstreamStream = errors.New("upstream stream error")

const streamHeartbeatInterval = 10 * time.Second

type generationResult struct {
	events        []aistudio.Event
	text          strings.Builder
	reasoning     strings.Builder
	toolCalls     []aistudio.FunctionCall
	citations     []aistudio.Citation
	grounding     *aistudio.GroundingMetadata
	media         []aistudio.Media
	usage         *aistudio.Usage
	finishReason  string
	providerModel string
	finished      bool
}

func (result *generationResult) apply(event aistudio.Event) error {
	if event.ProviderModel != "" {
		result.providerModel = event.ProviderModel
	}
	if event.Usage != nil {
		usage := *event.Usage
		result.usage = &usage
	}
	if event.Kind == aistudio.EventError {
		if event.Err != nil {
			return event.Err
		}
		return errUpstreamStream
	}
	switch event.Kind {
	case aistudio.EventText:
		result.text.WriteString(event.Text)
	case aistudio.EventReasoning:
		result.reasoning.WriteString(event.Text)
	case aistudio.EventToolCall:
		if event.ToolCall != nil {
			call := *event.ToolCall
			if len(call.Arguments) == 0 {
				call.Arguments = json.RawMessage(`{}`)
			}
			if !json.Valid(call.Arguments) {
				return fmt.Errorf("upstream tool call %q arguments are not JSON", call.Name)
			}
			event.ToolCall = &call
			result.toolCalls = append(result.toolCalls, call)
		}
	case aistudio.EventGrounding:
		if event.Grounding != nil {
			grounding := *event.Grounding
			result.grounding = &grounding
			result.citations = append(result.citations, groundingCitations(grounding)...)
		}
	case aistudio.EventCitation:
		if event.Citation != nil {
			result.citations = append(result.citations, *event.Citation)
		}
	case aistudio.EventMedia:
		if event.Media != nil {
			if event.Media.MIME == "" || (len(event.Media.Data) == 0 && event.Media.URL == "") {
				return fmt.Errorf("upstream media is missing MIME or content")
			}
			media := *event.Media
			media.Data = append([]byte(nil), media.Data...)
			if strings.HasPrefix(media.MIME, "audio/") && len(media.Data) > 0 && media.URL == "" && len(result.events) > 0 {
				previous := &result.events[len(result.events)-1]
				if previous.Kind == aistudio.EventMedia && previous.Media != nil && previous.Media.MIME == media.MIME && previous.Media.URL == "" {
					previous.Media.Data = append(previous.Media.Data, media.Data...)
					result.media[len(result.media)-1].Data = append(result.media[len(result.media)-1].Data, media.Data...)
					return nil
				}
			}
			event.Media = &media
			result.media = append(result.media, media)
		}
	case aistudio.EventFinish:
		result.finishReason = event.FinishReason
		result.finished = true
	}
	result.events = append(result.events, event)
	return nil
}

func consumeEvents(ctx context.Context, events <-chan aistudio.Event, emit func(aistudio.Event) error) (result generationResult, resultErr error) {
	return consumeEventsWithHeartbeat(ctx, events, emit, nil)
}

func consumeStreamEvents(
	ctx context.Context,
	events <-chan aistudio.Event,
	emit func(aistudio.Event) error,
	heartbeat func() error,
) (generationResult, error) {
	return consumeEventsWithHeartbeat(ctx, events, emit, heartbeat)
}

func consumeEventsWithHeartbeat(
	ctx context.Context,
	events <-chan aistudio.Event,
	emit func(aistudio.Event) error,
	heartbeat func() error,
) (result generationResult, resultErr error) {
	defer func() {
		SetAccessLogError(ctx, resultErr)
	}()
	var heartbeatTimer *time.Timer
	var heartbeatTick <-chan time.Time
	if heartbeat != nil {
		heartbeatTimer = time.NewTimer(streamHeartbeatInterval)
		heartbeatTick = heartbeatTimer.C
		defer heartbeatTimer.Stop()
	}
	resetHeartbeat := func() {
		if heartbeatTimer == nil {
			return
		}
		if !heartbeatTimer.Stop() {
			select {
			case <-heartbeatTimer.C:
			default:
			}
		}
		heartbeatTimer.Reset(streamHeartbeatInterval)
	}
	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-heartbeatTick:
			if err := heartbeat(); err != nil {
				return result, err
			}
			resetHeartbeat()
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
			resetHeartbeat()
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

func normalizedFinish(reason string, hasTools bool) string {
	if hasTools {
		return "tool_calls"
	}
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "max_tokens", "max_output_tokens", "length":
		return "length"
	case "content_filter", "safety", "blocked", "recitation", "blocklist", "prohibited_content", "spii", "image_safety", "image_prohibited_content", "no_image", "image_recitation":
		return "content_filter"
	case "stop_sequence":
		return "stop"
	default:
		return "stop"
	}
}

func normalizeFunctionResultContent(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("function result must be JSON")
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") {
		return raw, nil
	}
	return json.RawMessage(`{"result":` + trimmed + `}`), nil
}

func renderMediaMarkdown(media aistudio.Media) string {
	url := media.URL
	if len(media.Data) > 0 {
		url = "data:" + media.MIME + ";base64," + base64.StdEncoding.EncodeToString(media.Data)
	}
	label := media.Name
	if label == "" {
		label = "media"
	}
	if strings.HasPrefix(media.MIME, "image/") {
		return fmt.Sprintf("![%s](%s)", label, url)
	}
	return fmt.Sprintf("[%s](%s)", label, url)
}

func groundingCitations(metadata aistudio.GroundingMetadata) []aistudio.Citation {
	citations := make([]aistudio.Citation, 0)
	if len(metadata.Supports) == 0 {
		for _, chunk := range metadata.Chunks {
			if chunk.URI != "" {
				citations = append(citations, aistudio.Citation{URL: chunk.URI, Title: chunk.Title, Publisher: chunk.Source})
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
				URL: chunk.URI, Title: chunk.Title, Publisher: chunk.Source,
				Start: support.Segment.StartIndex, End: support.Segment.EndIndex,
			})
		}
	}
	return citations
}

func renderCodeExecution(event aistudio.Event) string {
	switch event.Kind {
	case aistudio.EventExecutableCode:
		if event.ExecutableCode == nil {
			return ""
		}
		language := strings.ToLower(event.ExecutableCode.Language)
		if language == "language_unspecified" {
			language = "text"
		}
		return "```" + language + "\n" + strings.TrimSuffix(event.ExecutableCode.Code, "\n") + "\n```"
	case aistudio.EventCodeExecutionResult:
		if event.CodeExecutionResult == nil || event.CodeExecutionResult.Output == "" {
			return ""
		}
		return "```text\n" + strings.TrimSuffix(event.CodeExecutionResult.Output, "\n") + "\n```"
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
