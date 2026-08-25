package aistudio

import "net/http"

// ProtectedRequest 表示需要 WAA 保护的 AI Studio 请求
type ProtectedRequest struct {
	URL        string
	Headers    http.Header
	Body       []byte
	Prompt     string
	ProofField int
}

// PreparedProtectedRequest 表示已写入 fresh proof 的请求
type PreparedProtectedRequest struct {
	Body    []byte
	Headers http.Header
}

// WorkerPhase 表示账户 runtime 当前阶段
type WorkerPhase string

const (
	// WorkerStarting 表示 runtime 进程正在启动
	WorkerStarting WorkerPhase = "starting"
	// WorkerBootstrapping 表示官方页面正在初始化 WAA
	WorkerBootstrapping WorkerPhase = "bootstrapping"
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
	AccountID    string
	Phase        WorkerPhase
	PID          int
	RuntimeID    string
	PageURL      string
	RequestCount uint64
	LastError    string
}
