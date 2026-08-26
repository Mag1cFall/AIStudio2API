package aistudio

import "context"

// RequestPhase 表示受保护请求的当前准备阶段
type RequestPhase string

const (
	// RequestPhasePreparingWAA 表示正在生成 fresh WAA proof
	RequestPhasePreparingWAA RequestPhase = "preparing_waa"
	// RequestPhaseSendingUpstream 表示正在等待 AI Studio 响应头
	RequestPhaseSendingUpstream RequestPhase = "sending_upstream"
	// RequestPhaseStreaming 表示 AI Studio 已经返回流式响应
	RequestPhaseStreaming RequestPhase = "streaming"
)

type requestPhaseContextKey struct{}

// ContextWithRequestPhaseObserver 记录受保护请求阶段
func ContextWithRequestPhaseObserver(ctx context.Context, observer func(RequestPhase)) context.Context {
	return context.WithValue(ctx, requestPhaseContextKey{}, observer)
}

func reportRequestPhase(ctx context.Context, phase RequestPhase) {
	observer, _ := ctx.Value(requestPhaseContextKey{}).(func(RequestPhase))
	if observer != nil {
		observer(phase)
	}
}
