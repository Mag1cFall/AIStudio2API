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
