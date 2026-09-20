package app

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

// requestLogData projects request identity, parameters, and end-to-end usage metrics.
func requestLogData(entry api.AccessLog) *api.RequestLog {
	data := &api.RequestLog{
		ID:              entry.RequestID,
		Model:           entry.Model,
		Method:          entry.Method,
		Path:            entry.Path,
		Status:          entry.Status,
		DurationMS:      float64(entry.Latency) / float64(time.Millisecond),
		ToolCalls:       entry.ToolCalls,
		FinishReason:    entry.FinishReason,
		Error:           entry.Error,
		InputMessages:   entry.InputMessages,
		InputTextChars:  entry.InputTextChars,
		InputMedia:      entry.InputMedia,
		InputMediaBytes: entry.InputMediaBytes,
		InputFiles:      entry.InputFiles,
		FirstEventMS:    float64(entry.FirstEvent) / float64(time.Millisecond),
		UpstreamBytes:   entry.UpstreamBytes,
	}

	if entry.Generation {
		data.Parameters = map[string]string{
			"temperature":       entry.Temperature,
			"top_p":             entry.TopP,
			"thinking":          strings.ToLower(entry.Thinking),
			"max_output_tokens": entry.MaxOutputTokens,
		}
	}

	if usage := entry.Usage; usage != nil {
		output := usage.OutputTokens + usage.ReasoningTokens
		data.Usage = &api.RequestLogUsage{
			InputTokens:     usage.InputTokens + usage.ToolTokens,
			ReasoningTokens: usage.ReasoningTokens,
			ReplyTokens:     usage.OutputTokens,
			OutputTokens:    output,
			TotalTokens:     usage.TotalTokens,
		}

		if entry.Latency > 0 {
			data.Usage.AverageTokensPerSecond = float64(output) / entry.Latency.Seconds()
		}
	}

	return data
}

// RecordAccessStart records the start of a request that can be correlated with subsequent events.
func (admin *runtimeAdmin) RecordAccessStart(entry api.AccessLog) {
	data := requestLogData(entry)
	data.State = "running"

	admin.recordRequestLog(entry.Account, "INFO", "request.started", "Request started", data)
}

// RecordAccessLog records the outcome of a request, categorizing tool calls, limits, and failures.
func (admin *runtimeAdmin) RecordAccessLog(entry api.AccessLog) {
	data := requestLogData(entry)
	data.State = "completed"

	level, message := "INFO", "Request completed"

	switch {
	case entry.Canceled || entry.Status == 499:
		data.State, level, message = "cancelled", "WARN", "Request canceled"

	case entry.Status >= http.StatusBadRequest || entry.Error != "":
		data.State, level, message = "failed", "ERROR", "Request failed"
		if data.Error == "" {
			data.Error = fmt.Sprintf("HTTP %d", entry.Status)
		}

	case entry.FinishReason == "max_tokens" || entry.FinishReason == "max_output_tokens" || entry.FinishReason == "length":
		data.State, level, message = "limited", "WARN", "Output token limit reached"

	case entry.FinishReason != "" && entry.FinishReason != "stop" && entry.FinishReason != "stop_sequence":
		data.State, level, message = "blocked", "WARN", "Upstream terminated generation"

	case entry.ToolCalls > 0:
		data.State, message = "tool_calls", "Tool calls completed"
	}

	admin.recordRequestLog(entry.Account, level, "request.finished", message, data)
}

// recordRequestLog standardizes the account source and event payload for request logs.
func (admin *runtimeAdmin) recordRequestLog(source, level, event, message string, data *api.RequestLog) {
	if source == "" {
		source = "request"
	}

	admin.requests.recordLog(api.AdminLog{
		Source:  source,
		Level:   level,
		Event:   event,
		Message: message,
		Request: data,
	})
}

// logRequestProgress associates waiting and recovery events with the corresponding request.
func (registry *requestRegistry) logRequestProgress(id, source, level, message string) {
	registry.recordLog(api.AdminLog{
		Source:  source,
		Level:   level,
		Event:   "request.progress",
		Message: message,
		Request: &api.RequestLog{
			ID:    id,
			State: "running",
		},
	})
}
