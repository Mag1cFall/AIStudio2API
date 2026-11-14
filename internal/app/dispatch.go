package app

import (
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// dispatchQueue 按账户选择条件分组，同条件的请求按到达先后取号
type dispatchQueue struct {
	mu     sync.Mutex
	queues map[string][]*dispatchWaiter
}

// dispatchWaiter 表示一个正在取号的请求
type dispatchWaiter struct {
	key  string
	wake chan struct{}
	// starting 表示请求已取得账户、正在等它的 Worker 启动，排在后面的请求不必等它
	starting bool
}

func newDispatchQueue() *dispatchQueue {
	return &dispatchQueue{queues: make(map[string][]*dispatchWaiter)}
}

// dispatchKey 返回可互相替代的请求共用的队列键
func dispatchKey(selection aistudio.AccountSelection) string {
	allowed := slices.Clone(selection.AllowedAccountIDs)
	slices.Sort(allowed)
	return strings.Join([]string{
		selection.ModelID, selection.Method, selection.Capability, selection.AccountID, selection.ResourceID,
		strings.Join(allowed, ","), strconv.FormatBool(selection.PlaygroundOnly),
	}, "\x00")
}

// join 把请求排到同条件队列末尾
func (queue *dispatchQueue) join(key string) *dispatchWaiter {
	waiter := &dispatchWaiter{key: key, wake: make(chan struct{}, 1)}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.queues[key] = append(queue.queues[key], waiter)
	return waiter
}

// turn 判断请求是否轮到取号：排在它前面的请求都在等 Worker 启动
func (queue *dispatchQueue) turn(waiter *dispatchWaiter) bool {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	for _, ahead := range queue.queues[waiter.key] {
		if ahead == waiter || !ahead.starting {
			return ahead == waiter
		}
	}
	return false
}

// setStarting 记录请求是否在等 Worker 启动并唤醒随之轮到取号的请求
func (queue *dispatchQueue) setStarting(waiter *dispatchWaiter, starting bool) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	waiter.starting = starting
	queue.wakeTurnLocked(waiter.key)
}

// leave 移除请求并唤醒随之轮到取号的请求
func (queue *dispatchQueue) leave(waiter *dispatchWaiter) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	waiters := queue.queues[waiter.key]
	index := slices.Index(waiters, waiter)
	waiters = slices.Delete(waiters, index, index+1)
	if len(waiters) == 0 {
		delete(queue.queues, waiter.key)
		return
	}
	queue.queues[waiter.key] = waiters
	queue.wakeTurnLocked(waiter.key)
}

// wakeHeads 在账户或 Worker 状态变化后唤醒每个队列中轮到取号的请求
func (queue *dispatchQueue) wakeHeads() {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	for key := range queue.queues {
		queue.wakeTurnLocked(key)
	}
}

// wakeTurnLocked 唤醒队列中第一个不在等 Worker 启动的请求
func (queue *dispatchQueue) wakeTurnLocked(key string) {
	for _, waiter := range queue.queues[key] {
		if !waiter.starting {
			waiter.signal()
			return
		}
	}
}

func (waiter *dispatchWaiter) signal() {
	select {
	case waiter.wake <- struct{}{}:
	default:
	}
}
