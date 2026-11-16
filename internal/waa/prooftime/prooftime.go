// Package prooftime 累计一次请求取 WAA proof 的分项耗时
package prooftime

import (
	"context"
	"sync/atomic"
	"time"
)

// Phase 是取 proof 耗时的分项
type Phase int

const (
	// Lock 是等待页面、VM 与界面操作锁的时间，含按需新开页面
	Lock Phase = iota
	// Interaction 是在页面上点击、粘贴、提交与页面应用发出请求的时间
	Interaction
	// Refresh 是等待 VM 到期重建完成的时间
	Refresh
	// Compute 是 VM 或页面应用生成 proof 的时间
	Compute
	phases
)

// Timing 累计一次请求各次取 proof 的分项耗时与空闲不够而没有做的运行设置步数
type Timing struct {
	phases  [phases]atomic.Int64
	skipped atomic.Int64
}

// Duration 返回 phase 分项累计的耗时
func (timing *Timing) Duration(phase Phase) time.Duration {
	return time.Duration(timing.phases[phase].Load())
}

// Skipped 返回空闲不够而没有做的运行设置步数
func (timing *Timing) Skipped() int {
	return int(timing.skipped.Load())
}

type timingKey struct{}

// With 返回带 timing 的 ctx，取 proof 的各层把分项耗时累计进 timing
func With(ctx context.Context, timing *Timing) context.Context {
	return context.WithValue(ctx, timingKey{}, timing)
}

// Observe 把 duration 累计进 ctx 所带 Timing 的 phase 分项，ctx 没有 Timing 时不记录
func Observe(ctx context.Context, phase Phase, duration time.Duration) {
	if timing, ok := ctx.Value(timingKey{}).(*Timing); ok {
		timing.phases[phase].Add(int64(duration))
	}
}

// Skip 把 steps 累计进 ctx 所带 Timing 的跳过运行设置步数，ctx 没有 Timing 时不记录
func Skip(ctx context.Context, steps int) {
	if timing, ok := ctx.Value(timingKey{}).(*Timing); ok {
		timing.skipped.Add(int64(steps))
	}
}
