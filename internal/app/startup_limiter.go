package app

import (
	"context"
	"sync"
)

// startupLimiter 限制同时启动的 Worker 数，上限可在运行中调整
type startupLimiter struct {
	mu      sync.Mutex
	limit   int
	active  int
	changed chan struct{}
}

// setLimit 设置同时启动的上限并唤醒等待者
func (limiter *startupLimiter) setLimit(limit int) {
	limiter.mu.Lock()
	limiter.limit = limit
	limiter.notifyLocked()
	limiter.mu.Unlock()
}

// size 返回同时启动的上限
func (limiter *startupLimiter) size() int {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	return limiter.limit
}

// acquire 等待一个启动名额，ctx 结束时返回其错误
func (limiter *startupLimiter) acquire(ctx context.Context) error {
	for {
		limiter.mu.Lock()
		if limiter.active < limiter.limit {
			limiter.active++
			limiter.mu.Unlock()
			return nil
		}
		if limiter.changed == nil {
			limiter.changed = make(chan struct{})
		}
		changed := limiter.changed
		limiter.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// release 归还一个启动名额并唤醒等待者
func (limiter *startupLimiter) release() {
	limiter.mu.Lock()
	limiter.active--
	limiter.notifyLocked()
	limiter.mu.Unlock()
}

// notifyLocked 唤醒全部等待者
func (limiter *startupLimiter) notifyLocked() {
	if limiter.changed != nil {
		close(limiter.changed)
		limiter.changed = nil
	}
}
