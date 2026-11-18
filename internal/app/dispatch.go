package app

import (
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// dispatchQueue 按到达先后记录正在取号的请求，可用账户重叠的同条件请求按到达先后取用共同的账户
type dispatchQueue struct {
	mu      sync.Mutex
	waiters []*dispatchWaiter
}

// dispatchWaiter 表示一个正在取号的请求
type dispatchWaiter struct {
	// key 是请求的模型、方法、能力、资源与是否只用 Playground，相同时请求可以互相替代
	key string
	// accounts 是请求按指定账户、API key 与模型绑定收窄后可用的账户，nil 表示全部账户
	accounts []string
	wake     chan struct{}
	// starting 表示请求已取得账户、正在等它的 Worker 启动，排在后面的请求不必等它
	starting bool
}

func newDispatchQueue() *dispatchQueue {
	return &dispatchQueue{}
}

// dispatchKey 返回可互相替代的请求共用的条件键
func dispatchKey(selection aistudio.AccountSelection) string {
	return strings.Join([]string{
		selection.ModelID, selection.Method, selection.Capability, selection.ResourceID, strconv.FormatBool(selection.PlaygroundOnly),
	}, "\x00")
}

// join 把已收窄账户范围的请求排到末尾
func (queue *dispatchQueue) join(selection aistudio.AccountSelection) *dispatchWaiter {
	waiter := &dispatchWaiter{key: dispatchKey(selection), accounts: selection.AllowedAccountIDs, wake: make(chan struct{}, 1)}
	if selection.AccountID != "" {
		waiter.accounts = []string{selection.AccountID}
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.waiters = append(queue.waiters, waiter)
	return waiter
}

// turn 返回请求这一轮要让给前面请求的账户；ok 为 false 表示它可用的账户都要先让给前面的请求
func (queue *dispatchQueue) turn(waiter *dispatchWaiter) (reserved map[string]struct{}, ok bool) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return queue.turnLocked(waiter)
}

// turnLocked 收集排在前面、条件相同、不在等 Worker 启动且可用账户与请求重叠的请求的账户
func (queue *dispatchQueue) turnLocked(waiter *dispatchWaiter) (map[string]struct{}, bool) {
	var reserved map[string]struct{}
	for _, ahead := range queue.waiters {
		if ahead == waiter {
			break
		}
		if ahead.key != waiter.key || ahead.starting || !accountsOverlap(ahead.accounts, waiter.accounts) {
			continue
		}
		if ahead.accounts == nil {
			return nil, false
		}
		if reserved == nil {
			reserved = make(map[string]struct{})
		}
		for _, accountID := range ahead.accounts {
			reserved[accountID] = struct{}{}
		}
	}
	if len(reserved) > 0 && waiter.accounts != nil && !slices.ContainsFunc(waiter.accounts, func(accountID string) bool {
		_, taken := reserved[accountID]
		return !taken
	}) {
		return nil, false
	}
	return reserved, true
}

// accountsOverlap 判断两组可用账户是否有共同的账户，nil 表示全部账户
func accountsOverlap(left, right []string) bool {
	switch {
	case left == nil:
		return right == nil || len(right) > 0
	case right == nil:
		return len(left) > 0
	}
	return slices.ContainsFunc(left, func(accountID string) bool { return slices.Contains(right, accountID) })
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
	queue.waiters = slices.DeleteFunc(queue.waiters, func(other *dispatchWaiter) bool { return other == waiter })
	queue.wakeTurnLocked(waiter.key)
}

// wakeHeads 在账户或 Worker 状态变化后唤醒全部轮到取号的请求
func (queue *dispatchQueue) wakeHeads() {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.wakeTurnLocked("")
}

// wakeTurnLocked 唤醒条件为 key 的请求中轮到取号的请求，key 为空时唤醒全部条件
func (queue *dispatchQueue) wakeTurnLocked(key string) {
	for _, waiter := range queue.waiters {
		if waiter.starting || key != "" && waiter.key != key {
			continue
		}
		if _, ok := queue.turnLocked(waiter); ok {
			waiter.signal()
		}
	}
}

func (waiter *dispatchWaiter) signal() {
	select {
	case waiter.wake <- struct{}{}:
	default:
	}
}
