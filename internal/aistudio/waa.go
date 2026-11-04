package aistudio

import (
	"context"
	"net/http"
	"time"
)

// ProtectedRequest 表示需要 WAA 保护的 AI Studio 请求
type ProtectedRequest struct {
	URL        string
	Headers    http.Header
	Body       []byte
	Prompt     string
	PagePrompt string
	ProofField int
}

// followUp 是受保护请求发出 Delay 后以相同请求头发出的伴随请求，响应被丢弃
type followUp struct {
	URL   string
	Body  []byte
	Delay time.Duration
}

// followUpKey 是上下文中伴随请求的键
type followUpKey struct{}

// withFollowUp 返回携带伴随请求的上下文，SpiderMonkey runtime 发出受保护请求时同时安排它
func withFollowUp(ctx context.Context, item followUp) context.Context {
	return context.WithValue(ctx, followUpKey{}, item)
}

// followUpFrom 返回上下文中的伴随请求
func followUpFrom(ctx context.Context) (followUp, bool) {
	item, ok := ctx.Value(followUpKey{}).(followUp)
	return item, ok
}

// PreparedProtectedRequest 表示已写入 fresh proof 的请求
type PreparedProtectedRequest struct {
	Body    []byte
	Headers http.Header
}

// WorkerPhase 表示账户 runtime 当前阶段
type WorkerPhase string

const (
	// WorkerReady 表示 runtime 可以接受请求
	WorkerReady WorkerPhase = "ready"
	// WorkerBusy 表示 runtime 正在处理受保护请求
	WorkerBusy WorkerPhase = "busy"
	// WorkerClosing 表示 runtime 正在关闭
	WorkerClosing WorkerPhase = "closing"
	// WorkerClosed 表示 runtime 已关闭
	WorkerClosed WorkerPhase = "closed"
	// WorkerFailed 表示 runtime 已失效
	WorkerFailed WorkerPhase = "failed"
)

// WorkerState 表示账户 runtime 的可观察状态
type WorkerState struct {
	AccountID string
	Phase     WorkerPhase
	PID       int
	RuntimeID string
	PageURL   string
}
