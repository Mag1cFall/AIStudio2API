package aistudio

import "context"

// RequestPhase 表示受保护请求的当前准备阶段，Camoufox 界面运行在页面发出 GenerateContent 之前以 camoufoxnative.GenerateStep 的步骤说明为阶段
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

type serviceRunContextKey struct{}

type regenerationContextKey struct{}

// ContextWithServiceRun 记下生成服务本次运行的上下文，服务停止时它被取消
func ContextWithServiceRun(ctx context.Context, run context.Context) context.Context {
	return context.WithValue(ctx, serviceRunContextKey{}, run)
}

// ServiceStopped 返回 ctx 所属的生成服务是否已停止，ctx 没有记下服务运行时为 false
func ServiceStopped(ctx context.Context) bool {
	run, ok := ctx.Value(serviceRunContextKey{}).(context.Context)
	return ok && run.Err() != nil
}

// afterServiceStop 在 ctx 所属的生成服务停止时调用 stop，服务已停止时在返回前调用，返回撤销注册的函数；ctx 没有记下服务运行时不调用
func afterServiceStop(ctx context.Context, stop func()) func() bool {
	run, ok := ctx.Value(serviceRunContextKey{}).(context.Context)
	if !ok {
		return func() bool { return false }
	}
	if run.Err() != nil {
		stop()
	}
	return context.AfterFunc(run, stop)
}

// ContextWithRegenerationObserver 在流在完成前中断、流尾为 5xx 或上游异常结束而重新生成之前调用 observer，attempt 从 1 起，reason 为中断错误或结束原因
func ContextWithRegenerationObserver(ctx context.Context, observer func(attempt int, reason string)) context.Context {
	return context.WithValue(ctx, regenerationContextKey{}, observer)
}

func reportRegeneration(ctx context.Context, attempt int, reason string) {
	if observer, ok := ctx.Value(regenerationContextKey{}).(func(int, string)); ok {
		observer(attempt, reason)
	}
}

// ContextWithUpstreamModeObserver 记录实际 RPC、传输模式、续写轮次与流式回退原因
func ContextWithUpstreamModeObserver(ctx context.Context, observer func(method, mode string, round int, reason string)) context.Context {
	return context.WithValue(ctx, upstreamModeContextKey{}, observer)
}

// reportUpstreamMode 在发送请求前报告实际采用的上游调用方式，round 为续写轮次，原请求为 0
func reportUpstreamMode(ctx context.Context, method, mode string, round int, reason string) {
	if observer, ok := ctx.Value(upstreamModeContextKey{}).(func(string, string, int, string)); ok {
		observer(method, mode, round, reason)
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
