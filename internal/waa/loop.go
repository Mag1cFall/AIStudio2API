package waa

import (
	"sync"
	"time"
)

// eventLoop 在单个 goroutine 中按到达顺序串行执行 VM 任务与计时器回调
type eventLoop struct {
	mu     sync.Mutex
	queue  []func()
	wake   chan struct{}
	done   chan struct{}
	closed bool
	timers map[*time.Timer]struct{}
}

func newEventLoop() *eventLoop {
	loop := &eventLoop{wake: make(chan struct{}, 1), done: make(chan struct{}), timers: make(map[*time.Timer]struct{})}
	go loop.run()
	return loop
}

func (loop *eventLoop) run() {
	for {
		loop.mu.Lock()
		if loop.closed {
			loop.mu.Unlock()
			return
		}
		if len(loop.queue) == 0 {
			loop.mu.Unlock()
			select {
			case <-loop.wake:
			case <-loop.done:
				return
			}
			continue
		}
		job := loop.queue[0]
		loop.queue[0] = nil
		loop.queue = loop.queue[1:]
		loop.mu.Unlock()
		job()
	}
}

// post 把任务追加到事件循环队列，循环已关闭时返回 false
func (loop *eventLoop) post(job func()) bool {
	loop.mu.Lock()
	if loop.closed {
		loop.mu.Unlock()
		return false
	}
	loop.queue = append(loop.queue, job)
	loop.mu.Unlock()
	select {
	case loop.wake <- struct{}{}:
	default:
	}
	return true
}

// schedule 在延迟后把任务追加到事件循环队列
func (loop *eventLoop) schedule(delay time.Duration, job func()) {
	if delay <= 0 {
		loop.post(job)
		return
	}
	var timer *time.Timer
	timer = time.AfterFunc(delay, func() {
		loop.mu.Lock()
		delete(loop.timers, timer)
		loop.mu.Unlock()
		loop.post(job)
	})
	loop.mu.Lock()
	if loop.closed {
		loop.mu.Unlock()
		timer.Stop()
		return
	}
	loop.timers[timer] = struct{}{}
	loop.mu.Unlock()
}

func (loop *eventLoop) close() {
	loop.mu.Lock()
	if loop.closed {
		loop.mu.Unlock()
		return
	}
	loop.closed = true
	for timer := range loop.timers {
		timer.Stop()
	}
	clear(loop.timers)
	loop.queue = nil
	loop.mu.Unlock()
	close(loop.done)
}
