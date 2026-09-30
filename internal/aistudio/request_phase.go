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

type upstreamModeContextKey struct{}

// ContextWithUpstreamModeObserver 记录实际 RPC、传输模式与流式回退原因
func ContextWithUpstreamModeObserver(ctx context.Context, observer func(string, string, string)) context.Context {
	return context.WithValue(ctx, upstreamModeContextKey{}, observer)
}

// reportUpstreamMode 在发送请求前报告实际采用的上游调用方式
func reportUpstreamMode(ctx context.Context, method, mode, reason string) {
	if observer, ok := ctx.Value(upstreamModeContextKey{}).(func(string, string, string)); ok {
		observer(method, mode, reason)
	}
}

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
