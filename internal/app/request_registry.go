package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

// requestRegistry 保存活动请求与事件订阅
type requestRegistry struct {
	mu          sync.Mutex
	active      map[string]trackedRequest
	logs        []api.AdminLog
	subscribers map[*eventSubscriber]struct{}
	console     chan api.AdminLog
	// firstEvents 是各模型与通道近期原请求的首事件等待，生成服务重启后保留
	firstEvents *firstEventStats
}

type eventSubscriber struct {
	ctx             context.Context
	mu              sync.Mutex
	events          chan api.AdminEvent
	wake            chan struct{}
	pending         []api.AdminEvent
	pendingLogs     int
	pendingRequests int
}

const (
	adminLogRetain             = 2000
	adminLogCompactAt          = 2200
	adminLogInitialEvents      = 200
	adminRequestRetain         = 512
	adminRequestCompactAt      = 640
	adminRequestRefreshLatency = 50 * time.Millisecond
)

type trackedRequest struct {
	request api.AdminRequest
	cancel  context.CancelFunc
}

// newRequestRegistry 创建活动请求注册表
func newRequestRegistry(ctx context.Context) *requestRegistry {
	registry := &requestRegistry{
		active:      make(map[string]trackedRequest),
		logs:        make([]api.AdminLog, 0, 128),
		subscribers: make(map[*eventSubscriber]struct{}),
		console:     make(chan api.AdminLog, 256),
		firstEvents: newFirstEventStats(),
	}
	go registry.writeConsole(ctx)
	return registry
}

func (registry *requestRegistry) start(request aistudio.GenerateRequest, cancel context.CancelFunc) {
	tracked := trackedRequest{
		request: api.AdminRequest{
			ID: request.ID, Model: request.Model, AccountID: request.AccountID,
			State: "queued", StartedAt: time.Now().UTC(),
		},
		cancel: cancel,
	}
	registry.mu.Lock()
	registry.active[request.ID] = tracked
	registry.publishLocked(api.AdminEvent{Type: "request", Data: tracked.request})
	registry.mu.Unlock()
}

func (registry *requestRegistry) markRunning(id string, accountID string, accountLabel string) {
	registry.mu.Lock()
	tracked, exists := registry.active[id]
	if exists {
		tracked.request.AccountID = accountID
		tracked.request.AccountLabel = accountLabel
		tracked.request.State = "running"
		registry.active[id] = tracked
		registry.publishLocked(api.AdminEvent{Type: "request", Data: tracked.request})
	}
	registry.mu.Unlock()
}

// markChannel 记录请求本次尝试使用的上游通道
func (registry *requestRegistry) markChannel(id string, channel string) {
	registry.mu.Lock()
	if tracked, exists := registry.active[id]; exists {
		tracked.request.Channel = channel
		registry.active[id] = tracked
		registry.publishLocked(api.AdminEvent{Type: "request", Data: tracked.request})
	}
	registry.mu.Unlock()
}

func (registry *requestRegistry) finish(id string, state string) {
	registry.mu.Lock()
	tracked, exists := registry.active[id]
	if exists {
		delete(registry.active, id)
		tracked.request.State = state
		registry.publishLocked(api.AdminEvent{Type: "request", Data: tracked.request})
	}
	registry.mu.Unlock()
}

func (registry *requestRegistry) list() []api.AdminRequest {
	registry.mu.Lock()
	requests := make([]api.AdminRequest, 0, len(registry.active))
	for _, tracked := range registry.active {
		requests = append(requests, tracked.request)
	}
	registry.mu.Unlock()
	sort.Slice(requests, func(left int, right int) bool {
		return requests[left].StartedAt.Before(requests[right].StartedAt)
	})
	return requests
}

func (registry *requestRegistry) count() int {
	registry.mu.Lock()
	count := len(registry.active)
	registry.mu.Unlock()
	return count
}

func (registry *requestRegistry) cancel(id string) error {
	registry.mu.Lock()
	tracked, exists := registry.active[id]
	registry.mu.Unlock()
	if !exists {
		return &adminOperationError{
			status: http.StatusNotFound, code: "request_not_found",
			message: fmt.Sprintf("活动请求不存在: %s", id),
		}
	}
	tracked.cancel()
	return nil
}

func (registry *requestRegistry) cancelAll() {
	registry.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(registry.active))
	for _, tracked := range registry.active {
		cancels = append(cancels, tracked.cancel)
	}
	registry.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (registry *requestRegistry) log(source string, level string, message string) {
	registry.recordLog(api.AdminLog{Source: source, Level: level, Message: message, Event: "runtime.message"})
}

// recordLog 将同一结构化事件发布到管理页面与控制台
func (registry *requestRegistry) recordLog(entry api.AdminLog) {
	entry.Time = time.Now().UTC()
	registry.mu.Lock()
	registry.logs = append(registry.logs, entry)
	if len(registry.logs) >= adminLogCompactAt {
		copy(registry.logs, registry.logs[len(registry.logs)-adminLogRetain:])
		registry.logs = registry.logs[:adminLogRetain]
	}
	registry.publishLocked(api.AdminEvent{Type: "log", Data: entry})
	registry.mu.Unlock()
	select {
	case registry.console <- entry:
	default:
	}
}

func (registry *requestRegistry) writeConsole(ctx context.Context) {
	for {
		select {
		case entry := <-registry.console:
			level := slog.LevelInfo
			switch entry.Level {
			case "ERROR":
				level = slog.LevelError
			case "WARN":
				level = slog.LevelWarn
			}
			record := slog.NewRecord(entry.Time, level, entry.Message, 0)
			record.AddAttrs(slog.String("event", entry.Event), slog.String("source", entry.Source))
			if entry.Request != nil {
				record.AddAttrs(slog.Any("request", entry.Request))
			}
			_ = slog.Default().Handler().Handle(ctx, record)
		case <-ctx.Done():
			return
		}
	}
}

func (registry *requestRegistry) clearLogs() {
	registry.mu.Lock()
	registry.logs = registry.logs[:0]
	registry.mu.Unlock()
}

// newEventSubscriber 创建管理页有界事件队列
func newEventSubscriber(ctx context.Context) *eventSubscriber {
	return &eventSubscriber{
		ctx: ctx, events: make(chan api.AdminEvent, 16), wake: make(chan struct{}, 1),
		pending: make([]api.AdminEvent, 0, 256),
	}
}

func (subscriber *eventSubscriber) enqueue(event api.AdminEvent) {
	subscriber.mu.Lock()
	subscriber.enqueueLocked(event)
	subscriber.mu.Unlock()
	subscriber.notify()
}

func (subscriber *eventSubscriber) enqueueLocked(event api.AdminEvent) {
	switch event.Type {
	case "status", "models", "accounts", "cooldowns":
		for index := len(subscriber.pending) - 1; index >= 0; index-- {
			if subscriber.pending[index].Type == event.Type {
				subscriber.pending[index] = event
				return
			}
		}
	case "request":
		request := event.Data.(api.AdminRequest)
		for index := len(subscriber.pending) - 1; index >= 0; index-- {
			pending := subscriber.pending[index]
			if pending.Type == "request" && pending.Data.(api.AdminRequest).ID == request.ID {
				subscriber.pending[index] = event
				return
			}
		}
		subscriber.pendingRequests++
	case "log":
		subscriber.pendingLogs++
	}
	subscriber.pending = append(subscriber.pending, event)
	if subscriber.pendingLogs >= adminLogCompactAt {
		subscriber.trimPendingLocked("log", adminLogRetain)
	}
	if subscriber.pendingRequests >= adminRequestCompactAt {
		subscriber.trimPendingLocked("request", adminRequestRetain)
	}
}

func (subscriber *eventSubscriber) trimPendingLocked(eventType string, retain int) {
	count := subscriber.pendingLogs
	if eventType == "request" {
		count = subscriber.pendingRequests
	}
	drop := count - retain
	compacted := subscriber.pending[:0]
	for _, event := range subscriber.pending {
		if event.Type == eventType && drop > 0 {
			drop--
			continue
		}
		compacted = append(compacted, event)
	}
	subscriber.pending = compacted
	if eventType == "request" {
		subscriber.pendingRequests = retain
	} else {
		subscriber.pendingLogs = retain
	}
}

func (subscriber *eventSubscriber) activate(initial []api.AdminEvent) {
	subscriber.mu.Lock()
	buffered := append([]api.AdminEvent(nil), subscriber.pending...)
	subscriber.pending = subscriber.pending[:0]
	subscriber.pendingLogs = 0
	subscriber.pendingRequests = 0
	for _, event := range initial {
		subscriber.enqueueLocked(event)
	}
	for _, event := range buffered {
		if event.Type != "log" {
			subscriber.enqueueLocked(event)
		}
	}
	subscriber.mu.Unlock()
	subscriber.notify()
}

func (subscriber *eventSubscriber) notify() {
	select {
	case subscriber.wake <- struct{}{}:
	default:
	}
}

func (subscriber *eventSubscriber) next() (api.AdminEvent, bool) {
	subscriber.mu.Lock()
	defer subscriber.mu.Unlock()
	if len(subscriber.pending) == 0 {
		return api.AdminEvent{}, false
	}
	event := subscriber.pending[0]
	subscriber.pending[0] = api.AdminEvent{}
	subscriber.pending = subscriber.pending[1:]
	if event.Type == "log" {
		subscriber.pendingLogs--
	}
	if event.Type == "request" {
		subscriber.pendingRequests--
	}
	if len(subscriber.pending) == 0 {
		subscriber.pending = nil
	}
	return event, true
}

func (subscriber *eventSubscriber) run() {
	defer close(subscriber.events)
	for {
		if event, ok := subscriber.next(); ok {
			select {
			case subscriber.events <- event:
				continue
			case <-subscriber.ctx.Done():
				return
			}
		}
		select {
		case <-subscriber.wake:
		case <-subscriber.ctx.Done():
			return
		}
	}
}

func (registry *requestRegistry) subscribe(ctx context.Context) *eventSubscriber {
	subscriber := newEventSubscriber(ctx)
	registry.mu.Lock()
	registry.subscribers[subscriber] = struct{}{}
	registry.mu.Unlock()
	go func() {
		<-ctx.Done()
		registry.mu.Lock()
		delete(registry.subscribers, subscriber)
		registry.mu.Unlock()
	}()
	return subscriber
}

// activateSubscriber 原子衔接日志请求快照与实时事件
func (registry *requestRegistry) activateSubscriber(
	subscriber *eventSubscriber,
	prefix []api.AdminEvent,
	suffix []api.AdminEvent,
) <-chan api.AdminEvent {
	registry.mu.Lock()
	initialLogs := registry.logs
	if len(initialLogs) > adminLogInitialEvents {
		initialLogs = initialLogs[len(initialLogs)-adminLogInitialEvents:]
	}
	initial := make([]api.AdminEvent, 0, len(prefix)+len(initialLogs)+len(suffix)+len(registry.active))
	initial = append(initial, prefix...)
	for _, entry := range initialLogs {
		initial = append(initial, api.AdminEvent{Type: "log", Data: entry})
	}
	initial = append(initial, suffix...)
	requests := make([]api.AdminRequest, 0, len(registry.active))
	for _, tracked := range registry.active {
		requests = append(requests, tracked.request)
	}
	sort.Slice(requests, func(left int, right int) bool {
		return requests[left].StartedAt.Before(requests[right].StartedAt)
	})
	for _, request := range requests {
		initial = append(initial, api.AdminEvent{Type: "request", Data: request})
	}
	subscriber.activate(initial)
	registry.mu.Unlock()
	go subscriber.run()
	return subscriber.events
}

func (registry *requestRegistry) publishLocked(event api.AdminEvent) {
	for subscriber := range registry.subscribers {
		subscriber.enqueue(event)
	}
}

// publish 向管理页订阅者发布增量事件
func (registry *requestRegistry) publish(event api.AdminEvent) {
	registry.mu.Lock()
	registry.publishLocked(event)
	registry.mu.Unlock()
}
