package aistudio

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"time"
)

// ProtectedRequest 表示需要 WAA 保护的 AI Studio 请求，Proof 非空时写入该 proof 并直接发送；ProofAt 是编码 Body 时给出的 WAA proof 字段占位 null 的起始字节
type ProtectedRequest struct {
	URL        string
	Headers    http.Header
	Body       []byte
	ProofAt    int
	Prompt     string
	PagePrompt string
	Proof      string
	// Digest 是直接作为 proof 摘要的 binding，为空时取 Prompt 的 SHA-256
	Digest string
}

// proofDigest 返回请求 proof 绑定的摘要
func proofDigest(request ProtectedRequest) string {
	if request.Digest != "" {
		return request.Digest
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(request.Prompt)))
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

// PreparedProtectedRequest 表示已取得 proof 的请求：Body 为原正文，发送时在 ProofAt 处写入 Proof；PagePrompt 为页面输入框中的提示词，由页面界面运行的请求 proof 为空
type PreparedProtectedRequest struct {
	Body       []byte
	ProofAt    int
	Headers    http.Header
	Proof      string
	PagePrompt string
}

// Payload 返回写入 proof 后的完整正文
func (prepared PreparedProtectedRequest) Payload() []byte {
	return newProtectedBody(prepared.Body, prepared.ProofAt, prepared.Proof).Bytes()
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
