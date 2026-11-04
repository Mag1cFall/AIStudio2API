package app

import (
	"context"
	"sync"
)

// startupLimiter 限制同时启动的 Worker 数，上限可在运行中调整
type startupLimiter struct {
	mu       sync.Mutex
	limit    int
	active   int
	waiting  int
	reserved map[string]int
	changed  chan struct{}
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

// acquire 等待一个启动名额，temporary 为临时页面载入的账户，非空时只在除该账户的预约外没有普通等待者时取得，ctx 结束时返回其错误
func (limiter *startupLimiter) acquire(ctx context.Context, temporary string) error {
	low := temporary != ""
	counted := false
	for {
		limiter.mu.Lock()
		if limiter.active < limiter.limit && (!low || limiter.waiting == limiter.reserved[temporary]) {
			limiter.active++
			if counted {
				limiter.waiting--
				limiter.notifyLocked()
			}
			limiter.mu.Unlock()
			return nil
		}
		if !low && !counted {
			limiter.waiting++
			counted = true
		}
		if limiter.changed == nil {
			limiter.changed = make(chan struct{})
		}
		changed := limiter.changed
		limiter.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			if counted {
				limiter.mu.Lock()
				limiter.waiting--
				limiter.notifyLocked()
				limiter.mu.Unlock()
			}
			return ctx.Err()
		}
	}
}

// startupReservation 是常驻或按需启动登记后、取得启动名额前对名额的预约
type startupReservation struct {
	limiter   *startupLimiter
	accountID string
	once      sync.Once
}

// reserve 为账户预约一个启动名额，预约结束前其他账户的临时页面载入不取得名额
func (limiter *startupLimiter) reserve(accountID string) *startupReservation {
	limiter.mu.Lock()
	limiter.waiting++
	if limiter.reserved == nil {
		limiter.reserved = make(map[string]int)
	}
	limiter.reserved[accountID]++
	limiter.mu.Unlock()
	return &startupReservation{limiter: limiter, accountID: accountID}
}

// release 结束预约并唤醒等待者，重复调用无效
func (reservation *startupReservation) release() {
	reservation.once.Do(func() {
		limiter := reservation.limiter
		limiter.mu.Lock()
		limiter.waiting--
		if limiter.reserved[reservation.accountID]--; limiter.reserved[reservation.accountID] == 0 {
			delete(limiter.reserved, reservation.accountID)
		}
		limiter.notifyLocked()
		limiter.mu.Unlock()
	})
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
