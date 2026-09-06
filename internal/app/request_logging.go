package app

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

// requestLogData 投影请求身份、参数与端到端用量
func requestLogData(entry api.AccessLog) *api.RequestLog {
	data := &api.RequestLog{
		ID: entry.RequestID, Model: entry.Model, Method: entry.Method, Path: entry.Path,
		Status: entry.Status, DurationMS: float64(entry.Latency) / float64(time.Millisecond),
		ToolCalls: entry.ToolCalls, FinishReason: entry.FinishReason, Error: entry.Error,
		InputMessages: entry.InputMessages, InputTextChars: entry.InputTextChars,
		InputMedia: entry.InputMedia, InputMediaBytes: entry.InputMediaBytes, InputFiles: entry.InputFiles,
		FirstEventMS: float64(entry.FirstEvent) / float64(time.Millisecond), UpstreamBytes: entry.UpstreamBytes,
	}
	if entry.Generation {
		data.Parameters = map[string]string{
			"temperature": entry.Temperature, "top_p": entry.TopP,
			"thinking": strings.ToLower(entry.Thinking), "max_output_tokens": entry.MaxOutputTokens,
		}
	}
	if usage := entry.Usage; usage != nil {
		output := usage.OutputTokens + usage.ReasoningTokens
		data.Usage = &api.RequestLogUsage{
			InputTokens: usage.InputTokens + usage.ToolTokens, ReasoningTokens: usage.ReasoningTokens,
			ReplyTokens: usage.OutputTokens, OutputTokens: output, TotalTokens: usage.TotalTokens,
		}
		if entry.Latency > 0 {
			data.Usage.AverageTokensPerSecond = float64(output) / entry.Latency.Seconds()
		}
	}
	return data
}

// RecordAccessStart 保存可与后续事件关联的请求开始记录
func (admin *runtimeAdmin) RecordAccessStart(entry api.AccessLog) {
	data := requestLogData(entry)
	data.State = "running"
	admin.recordRequestLog(entry.Account, "INFO", "request.started", "请求开始", data)
}

// RecordAccessLog 保存请求结果并区分工具调用、限制与失败
func (admin *runtimeAdmin) RecordAccessLog(entry api.AccessLog) {
	data := requestLogData(entry)
	data.State = "completed"
	level, message := "INFO", "请求完成"
	switch {
	case entry.Canceled || entry.Status == 499:
		data.State, level, message = "cancelled", "WARN", "请求已取消"
	case entry.Status >= http.StatusBadRequest || entry.Error != "":
		data.State, level, message = "failed", "ERROR", "请求失败"
		if data.Error == "" {
			data.Error = fmt.Sprintf("HTTP %d", entry.Status)
		}
	case entry.FinishReason == "max_tokens" || entry.FinishReason == "max_output_tokens" || entry.FinishReason == "length":
		data.State, level, message = "limited", "WARN", "达到输出上限"
	case entry.FinishReason != "" && entry.FinishReason != "stop" && entry.FinishReason != "stop_sequence":
		data.State, level, message = "blocked", "WARN", "上游终止生成"
	case entry.ToolCalls > 0:
		data.State, message = "tool_calls", "工具调用完成"
	}
	admin.recordRequestLog(entry.Account, level, "request.finished", message, data)
}

// recordRequestLog 统一请求日志的账户来源与事件载荷
func (admin *runtimeAdmin) recordRequestLog(source, level, event, message string, data *api.RequestLog) {
	if source == "" {
		source = "request"
	}
	admin.requests.recordLog(api.AdminLog{Source: source, Level: level, Event: event, Message: message, Request: data})
}

// logRequestProgress 将等待与恢复事件关联到所属请求
func (registry *requestRegistry) logRequestProgress(id, source, level, message string) {
	registry.recordLog(api.AdminLog{Source: source, Level: level, Event: "request.progress", Message: message,
		Request: &api.RequestLog{ID: id, State: "running"}})
}
