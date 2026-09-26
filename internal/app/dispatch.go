package app

import (
	"slices"
	"strings"
	"sync"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// dispatchQueue 按账户选择条件分组，先到先唤醒等待账户槽位的请求
type dispatchQueue struct {
	mu     sync.Mutex
	queues map[string][]*dispatchWaiter
}

// dispatchWaiter 表示一个等待账户槽位的请求
type dispatchWaiter struct {
	key  string
	wake chan struct{}
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
		strings.Join(allowed, ","),
	}, "\x00")
}

// join 把请求排到同条件队列末尾，队列原本非空时唤醒队首以复查空闲槽位
func (queue *dispatchQueue) join(key string) *dispatchWaiter {
	waiter := &dispatchWaiter{key: key, wake: make(chan struct{}, 1)}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.queues[key] = append(queue.queues[key], waiter)
	queue.queues[key][0].signal()
	return waiter
}

// leave 移除请求并在其位于队首时唤醒下一个请求
func (queue *dispatchQueue) leave(waiter *dispatchWaiter) {
	if waiter == nil {
		return
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	waiters := queue.queues[waiter.key]
	index := slices.Index(waiters, waiter)
	if index < 0 {
		return
	}
	waiters = slices.Delete(waiters, index, index+1)
	if len(waiters) == 0 {
		delete(queue.queues, waiter.key)
		return
	}
	queue.queues[waiter.key] = waiters
	if index == 0 {
		waiters[0].signal()
	}
}

// wakeHeads 在账户或 Worker 状态变化后唤醒每个队列的队首
func (queue *dispatchQueue) wakeHeads() {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	for _, waiters := range queue.queues {
		waiters[0].signal()
	}
}

func (waiter *dispatchWaiter) signal() {
	select {
	case waiter.wake <- struct{}{}:
	default:
	}
}
