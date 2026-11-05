package api

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"
)

// keepaliveWriter 在非流式响应写出前每隔心跳间隔写出一个换行，首个换行以 200 与 JSON 类型提交响应头，状态码只记入访问日志
type keepaliveWriter struct {
	http.ResponseWriter
	ctx       context.Context
	mu        sync.Mutex
	timer     *time.Timer
	committed bool
	stopped   bool
}

// responseModes 返回当前的响应方式设置
func (s *server) responseModes() ResponseModes {
	if s.config.ResponseModes == nil {
		return ResponseModes{}
	}
	return s.config.ResponseModes()
}

// nonstreamKeepalive 在开启非流式保活时返回带保活的 ResponseWriter，以及在处理函数返回前调用的停止函数
func (s *server) nonstreamKeepalive(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, func()) {
	if !s.responseModes().NonstreamKeepalive {
		return w, func() {}
	}
	writer := &keepaliveWriter{ResponseWriter: w, ctx: r.Context()}
	writer.timer = time.AfterFunc(streamHeartbeatInterval, writer.beat)
	return writer, writer.stop
}

// beat 写出一个换行并安排下一次保活
func (writer *keepaliveWriter) beat() {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.stopped {
		return
	}
	if !writer.committed {
		writer.ResponseWriter.Header().Set("Content-Type", "application/json")
		writer.ResponseWriter.Header().Set("X-Accel-Buffering", "no")
		writer.ResponseWriter.WriteHeader(http.StatusOK)
		writer.committed = true
	}
	if _, err := io.WriteString(writer.ResponseWriter, "\n"); err != nil {
		return
	}
	if http.NewResponseController(writer.ResponseWriter).Flush() != nil {
		return
	}
	writer.timer.Reset(streamHeartbeatInterval)
}

// stop 停止保活，之后的写入直接到达底层 ResponseWriter
func (writer *keepaliveWriter) stop() {
	writer.mu.Lock()
	writer.stopped = true
	writer.timer.Stop()
	writer.mu.Unlock()
}

// Header 停止保活后返回响应头，处理函数只在写出最终响应前访问它
func (writer *keepaliveWriter) Header() http.Header {
	writer.stop()
	return writer.ResponseWriter.Header()
}

// WriteHeader 在响应头未提交时写出状态码，已提交时把状态码记入访问日志
func (writer *keepaliveWriter) WriteHeader(status int) {
	writer.stop()
	if !writer.committed {
		writer.ResponseWriter.WriteHeader(status)
		return
	}
	setAccessLogFailureStatus(writer.ctx, status)
}

func (writer *keepaliveWriter) Write(data []byte) (int, error) {
	writer.stop()
	return writer.ResponseWriter.Write(data)
}

func (writer *keepaliveWriter) setError(message string) {
	setAccessLogResponseError(writer.ResponseWriter, message)
}

func (writer *keepaliveWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

// 流式响应的心跳帧：OpenAI 与 Interactions 为 SSE 注释，Anthropic 为官方 ping 事件，Gemini 为不触发事件的空行
const (
	sseHeartbeat       = ": ping\n\n"
	anthropicHeartbeat = "event: ping\ndata: {\"type\":\"ping\"}\n\n"
	geminiHeartbeat    = "\n"
)

// streamKeepalive 是流式响应的 ResponseWriter，距最近一次写出满心跳间隔时写出心跳帧，响应头由 open 或首个心跳写出
type streamKeepalive struct {
	http.ResponseWriter
	frame   string
	mu      sync.Mutex
	timer   *time.Timer
	opened  bool
	stopped bool
}

// keepStreamAlive 返回静默满心跳间隔时写出 frame 的流式 ResponseWriter，首个事件可能晚于心跳间隔的流式响应用它在等待期间写出响应头与心跳
func keepStreamAlive(w http.ResponseWriter, frame string) *streamKeepalive {
	writer := &streamKeepalive{ResponseWriter: w, frame: frame}
	writer.mu.Lock()
	writer.timer = time.AfterFunc(streamHeartbeatInterval, writer.beat)
	writer.mu.Unlock()
	return writer
}

// openStream 写出流式响应头并返回静默满心跳间隔时写出 frame 的 ResponseWriter，响应头写出失败时停止心跳
func openStream(w http.ResponseWriter, frame string) (*streamKeepalive, error) {
	writer := keepStreamAlive(w, frame)
	if err := writer.open(); err != nil {
		writer.stop()
		return writer, err
	}
	return writer, nil
}

// open 写出流式响应头，已写出时不再写
func (writer *streamKeepalive) open() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.openLocked()
}

func (writer *streamKeepalive) openLocked() error {
	if writer.opened {
		return nil
	}
	writer.opened = true
	return streamHeaders(writer.ResponseWriter)
}

// beat 写出心跳帧并安排下一次心跳
func (writer *streamKeepalive) beat() {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.stopped || writer.openLocked() != nil {
		return
	}
	if _, err := io.WriteString(writer.ResponseWriter, writer.frame); err != nil {
		return
	}
	if http.NewResponseController(writer.ResponseWriter).Flush() != nil {
		return
	}
	writer.timer.Reset(streamHeartbeatInterval)
}

// stop 停止心跳，返回响应头是否已写出
func (writer *streamKeepalive) stop() bool {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.stopped = true
	writer.timer.Stop()
	return writer.opened
}

// Write 写出数据并把下一次心跳推迟到心跳间隔之后
func (writer *streamKeepalive) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if !writer.stopped {
		writer.timer.Reset(streamHeartbeatInterval)
	}
	return writer.ResponseWriter.Write(data)
}

// FlushError 与心跳互斥地刷新底层 ResponseWriter
func (writer *streamKeepalive) FlushError() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return http.NewResponseController(writer.ResponseWriter).Flush()
}

func (writer *streamKeepalive) setError(message string) {
	setAccessLogResponseError(writer.ResponseWriter, message)
}

func (writer *streamKeepalive) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}
