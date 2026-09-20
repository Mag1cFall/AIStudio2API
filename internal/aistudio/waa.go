package aistudio

import "net/http"

// ProtectedRequest represents an AI Studio request requiring WAA protection
type ProtectedRequest struct {
	URL        string
	Headers    http.Header
	Body       []byte
	Prompt     string
	ProofField int
}

// PreparedProtectedRequest represents a request with fresh proof written
type PreparedProtectedRequest struct {
	Body    []byte
	Headers http.Header
}

// WorkerPhase represents the current phase of account runtime
type WorkerPhase string

const (
	// WorkerStarting indicates runtime process is starting
	WorkerStarting WorkerPhase = "starting"
	// WorkerBootstrapping indicates official page is bootstrapping WAA
	WorkerBootstrapping WorkerPhase = "bootstrapping"
	// WorkerReady indicates runtime can accept requests
	WorkerReady WorkerPhase = "ready"
	// WorkerBusy indicates runtime is handling a protected request
	WorkerBusy WorkerPhase = "busy"
	// WorkerClosing indicates runtime is closing
	WorkerClosing WorkerPhase = "closing"
	// WorkerClosed indicates runtime is closed
	WorkerClosed WorkerPhase = "closed"
	// WorkerFailed indicates runtime has failed
	WorkerFailed WorkerPhase = "failed"
)

// WorkerState represents observable state of account runtime
type WorkerState struct {
	AccountID    string
	Phase        WorkerPhase
	PID          int
	RuntimeID    string
	PageURL      string
	RequestCount uint64
	LastError    string
}
