package camoufoxnative

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

// liveViewFrameInterval 为登录窗口截取画面的最短间隔，约每秒 20 帧
const liveViewFrameInterval = 50 * time.Millisecond

// liveViewWriteTimeout 为向登录窗口写出一条消息的最长等待
const liveViewWriteTimeout = 10 * time.Second

// liveViewReadLimit 为登录窗口单条消息的大小上限，可容纳一次粘贴的文本
const liveViewReadLimit = 1 << 20

// liveViewFormat 为登录窗口画面的截图格式
var liveViewFormat = map[string]any{"type": "image/jpeg", "quality": 0.8}

// liveViewCapabilities 让 alert、confirm 与 prompt 保持打开，由登录窗口中的用户处理
var liveViewCapabilities = map[string]any{"alwaysMatch": map[string]any{"unhandledPromptBehavior": map[string]any{
	"alert": "ignore", "confirm": "ignore", "prompt": "ignore",
}}}

// liveViewEvents 为登录窗口跟踪的顶层页面与页面对话框事件
var liveViewEvents = []string{
	"browsingContext.contextCreated", "browsingContext.contextDestroyed",
	"browsingContext.userPromptOpened", "browsingContext.userPromptClosed",
}

// liveViewMetaExpression 读取画面所属页面的地址、标题、CSS 视口尺寸与 devicePixelRatio
const liveViewMetaExpression = `JSON.stringify([location.href, document.title, innerWidth, innerHeight, devicePixelRatio])`

const (
	// liveViewStarting 表示正在启动登录浏览器
	liveViewStarting = "starting"
	// liveViewWaiting 表示等待用户在登录窗口中登录
	liveViewWaiting = "waiting"
	// liveViewSaving 表示登录完成，正在授权 Drive 并保存认证状态
	liveViewSaving = "saving"
	// liveViewChecking 表示账户已保存，正在发一次生成请求核对账户状态
	liveViewChecking = "checking"
	// liveViewVerifying 表示官网要求验证，窗口停在验证页等待完成验证
	liveViewVerifying = "verifying"
)

// errLiveViewReplaced 表示同一 LiveView 连接了新的登录窗口
var errLiveViewReplaced = errors.New("登录窗口已在其他连接中打开")

// LiveView 把无头隔离登录浏览器的画面推给管理页登录窗口，并把窗口中的输入转发给浏览器
type LiveView struct {
	mu        sync.Mutex
	status    string
	frame     *liveFrame
	frameSeq  int
	dialog    *liveDialog
	dialogSeq int
	// contexts 为顶层页面，按打开顺序排列，画面取最后一个
	contexts []string
	viewer   *liveViewer
	inputs   []liveInput
	client   *bidiClient
	// closed 表示浏览器连接已断开，closedReason 是断开原因，由 mu 保护
	closed       bool
	closedReason string
	// controlError 是最近一次导航或标签页控制命令的失败原因，controlErrorSeq 随其递增，由 mu 保护
	controlError    string
	controlErrorSeq int
	// inputReady 在有待转发的输入时唤醒输入协程
	inputReady chan struct{}
	// capture 在登录窗口连接或页面对话框关闭时唤醒截图协程
	capture chan struct{}
}

// liveViewer 是一个登录窗口连接
type liveViewer struct {
	wake chan struct{}
	done chan struct{}
}

// liveFrame 是一帧画面及其所属页面，宽高为 CSS 像素
type liveFrame struct {
	Type    string  `json:"type"`
	Context string  `json:"context"`
	URL     string  `json:"url"`
	Title   string  `json:"title"`
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
	Scale   float64 `json:"scale"`
	data    []byte
}

// liveDialog 是页面打开且等待用户处理的 alert、confirm 或 prompt
type liveDialog struct {
	Context string `json:"-"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Value   string `json:"value"`
}

// liveInput 是登录窗口发来的一条消息，坐标为画面中的 CSS 像素
type liveInput struct {
	Type    string  `json:"type"`
	Action  string  `json:"action"`
	Context string  `json:"context"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Button  int     `json:"button"`
	DeltaX  float64 `json:"dx"`
	DeltaY  float64 `json:"dy"`
	Delta   int64   `json:"delta"`
	Key     string  `json:"key"`
	Text    string  `json:"text"`
	URL     string  `json:"url"`
	Accept  bool    `json:"accept"`
}

// liveControlTimeout 是地址栏导航、开关标签页等控制命令的最长时间
const liveControlTimeout = 30 * time.Second

// liveBatch 是对同一页面执行的一次 input.performActions
type liveBatch struct {
	context string
	source  map[string]any
}

// NewLiveView 创建尚未连接登录浏览器与登录窗口的 LiveView
func NewLiveView() *LiveView {
	return &LiveView{inputReady: make(chan struct{}, 1), capture: make(chan struct{}, 1)}
}

// signal 非阻塞地唤醒等待者
func signal(channel chan struct{}) {
	select {
	case channel <- struct{}{}:
	default:
	}
}

// notify 在持锁期间唤醒当前登录窗口的推送协程
func (view *LiveView) notify() {
	if view.viewer != nil {
		signal(view.viewer.wake)
	}
}

// setStatus 更新登录阶段，view 为 nil 时不做任何事
func (view *LiveView) setStatus(status string) {
	if view == nil {
		return
	}
	view.mu.Lock()
	defer view.mu.Unlock()
	view.status = status
	view.notify()
}

// Serve 把登录阶段、画面与页面对话框推给登录窗口连接并接收其输入，后连接的窗口替换先前的窗口；连接断开或 ctx 结束时关闭连接并返回
func (view *LiveView) Serve(ctx context.Context, connection *websocket.Conn) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	viewer := &liveViewer{wake: make(chan struct{}, 1), done: make(chan struct{})}
	view.mu.Lock()
	if view.viewer != nil {
		close(view.viewer.done)
	}
	view.viewer = viewer
	view.mu.Unlock()
	defer func() {
		view.mu.Lock()
		if view.viewer == viewer {
			view.viewer = nil
		}
		view.mu.Unlock()
	}()
	signal(viewer.wake)
	signal(view.capture)
	received := make(chan error, 1)
	go func() {
		received <- view.receive(connection)
		cancel()
	}()
	err := view.push(ctx, connection, viewer)
	_ = connection.Close()
	return errors.Join(err, <-received)
}

// receive 读取登录窗口的输入、导航控制与对话框选择，直到连接出错
func (view *LiveView) receive(connection *websocket.Conn) error {
	connection.SetReadLimit(liveViewReadLimit)
	for {
		var input liveInput
		if err := connection.ReadJSON(&input); err != nil {
			return err
		}
		switch input.Type {
		case "dialog":
			view.answerDialog(input.Accept, input.Text)
			continue
		case "navigate", "reload", "history", "newTab", "closeTab":
			if err := view.control(input); err != nil {
				view.reportControlError(err)
			}
			continue
		}
		view.enqueue(input)
	}
}

// reportControlError 记下控制命令错误并唤醒窗口连接推送
func (view *LiveView) reportControlError(err error) {
	if err == nil {
		return
	}
	view.mu.Lock()
	view.controlError = err.Error()
	view.controlErrorSeq++
	view.mu.Unlock()
	view.notify()
}

// control 执行地址栏导航、历史进退、刷新与标签页开关命令，失败时返回错误
func (view *LiveView) control(input liveInput) error {
	view.mu.Lock()
	client := view.client
	contextID := input.Context
	if contextID == "" {
		contextID = view.activeContextLocked()
	}
	view.mu.Unlock()
	if client == nil {
		return errors.New("浏览器连接不可用")
	}
	if contextID == "" && input.Type != "newTab" {
		return errors.New("没有可操作的标签页")
	}
	ctx, cancel := context.WithTimeout(context.Background(), liveControlTimeout)
	defer cancel()
	switch input.Type {
	case "navigate":
		if strings.TrimSpace(input.URL) == "" {
			return nil
		}
		if _, err := client.command(ctx, "browsingContext.navigate", map[string]any{
			"context": contextID, "url": input.URL, "wait": "none",
		}); err != nil {
			return fmt.Errorf("打开网址失败: %w", err)
		}
	case "reload":
		if _, err := client.command(ctx, "browsingContext.reload", map[string]any{
			"context": contextID, "ignoreCache": false,
		}); err != nil {
			return fmt.Errorf("刷新失败: %w", err)
		}
	case "history":
		if _, err := client.command(ctx, "browsingContext.traverseHistory", map[string]any{
			"context": contextID, "delta": input.Delta,
		}); err != nil {
			return fmt.Errorf("历史导航失败: %w", err)
		}
	case "newTab":
		result, err := client.command(ctx, "browsingContext.create", map[string]any{"type": "tab"})
		if err != nil {
			return fmt.Errorf("新建标签页失败: %w", err)
		}
		newContext, _ := result["context"].(string)
		if newContext == "" {
			return errors.New("新建标签页失败：浏览器未返回标签页")
		}
		if url := strings.TrimSpace(input.URL); url != "" {
			if _, err := client.command(ctx, "browsingContext.navigate", map[string]any{
				"context": newContext, "url": url, "wait": "none",
			}); err != nil {
				return fmt.Errorf("新标签页打开网址失败: %w", err)
			}
		}
	case "closeTab":
		if _, err := client.command(ctx, "browsingContext.close", map[string]any{"context": contextID}); err != nil {
			return fmt.Errorf("关闭标签页失败: %w", err)
		}
	}
	return nil
}

// enqueue 把输入排入转发队列，连续的指针移动只保留最后一次，同一位置连续的滚动合并
func (view *LiveView) enqueue(input liveInput) {
	view.mu.Lock()
	defer view.mu.Unlock()
	if count := len(view.inputs); count > 0 {
		last := &view.inputs[count-1]
		if last.Context == input.Context && last.Type == input.Type {
			switch {
			case input.Type == "pointer" && input.Action == "move" && last.Action == "move":
				*last = input
				return
			case input.Type == "wheel" && last.X == input.X && last.Y == input.Y:
				last.DeltaX += input.DeltaX
				last.DeltaY += input.DeltaY
				return
			}
		}
	}
	view.inputs = append(view.inputs, input)
	signal(view.inputReady)
}

// push 在状态变化时向登录窗口写出登录阶段、页面对话框与最新一帧画面
func (view *LiveView) push(ctx context.Context, connection *websocket.Conn, viewer *liveViewer) error {
	status, frameSeq, dialogSeq, controlErrorSeq := "", 0, 0, 0
	write := func(messageType int, data []byte) error {
		if err := connection.SetWriteDeadline(time.Now().Add(liveViewWriteTimeout)); err != nil {
			return err
		}
		return connection.WriteMessage(messageType, data)
	}
	writeJSON := func(value any) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return write(websocket.TextMessage, encoded)
	}
	closedSent := false
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-viewer.done:
			return errLiveViewReplaced
		case <-viewer.wake:
		}
		view.mu.Lock()
		currentStatus, frame, currentFrame, dialog, currentDialog := view.status, view.frame, view.frameSeq, view.dialog, view.dialogSeq
		closed, closedReason := view.closed, view.closedReason
		controlError, currentControlErrorSeq := view.controlError, view.controlErrorSeq
		view.mu.Unlock()
		if closed && !closedSent {
			if err := writeJSON(map[string]any{"type": "closed", "error": closedReason}); err != nil {
				return err
			}
			closedSent = true
		}
		if currentStatus != status {
			if err := writeJSON(map[string]any{"type": "status", "status": currentStatus}); err != nil {
				return err
			}
			status = currentStatus
		}
		if currentDialog != dialogSeq {
			if err := writeJSON(map[string]any{"type": "dialog", "dialog": dialog}); err != nil {
				return err
			}
			dialogSeq = currentDialog
		}
		if currentControlErrorSeq != controlErrorSeq {
			if err := writeJSON(map[string]any{"type": "control-error", "message": controlError}); err != nil {
				return err
			}
			controlErrorSeq = currentControlErrorSeq
		}
		if currentFrame != frameSeq && frame != nil {
			if err := writeJSON(frame); err != nil {
				return err
			}
			if err := write(websocket.BinaryMessage, frame.data); err != nil {
				return err
			}
			frameSeq = currentFrame
		}
	}
}

// answerDialog 按登录窗口的选择关闭页面对话框，打开对话框的命令仍在等待响应
func (view *LiveView) answerDialog(accept bool, text string) {
	view.mu.Lock()
	dialog, client := view.dialog, view.client
	view.mu.Unlock()
	if dialog == nil || client == nil {
		return
	}
	params := map[string]any{"context": dialog.Context, "accept": accept}
	if dialog.Kind == "prompt" && accept {
		params["userText"] = text
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = client.command(ctx, "browsingContext.handleUserPrompt", params)
}

// observe 跟踪顶层页面的打开与关闭和等待用户处理的页面对话框
func (view *LiveView) observe(message map[string]any) {
	method, _ := message["method"].(string)
	params, _ := message["params"].(map[string]any)
	contextID, _ := params["context"].(string)
	view.mu.Lock()
	defer view.mu.Unlock()
	switch method {
	case "browsingContext.contextCreated":
		if params["parent"] == nil && contextID != "" && !slices.Contains(view.contexts, contextID) {
			view.contexts = append(view.contexts, contextID)
		}
	case "browsingContext.contextDestroyed":
		view.contexts = slices.DeleteFunc(view.contexts, func(item string) bool { return item == contextID })
		if view.dialog != nil && view.dialog.Context == contextID {
			view.closeDialog()
		}
	case "browsingContext.userPromptOpened":
		if handler, _ := params["handler"].(string); handler != "ignore" {
			return
		}
		kind, _ := params["type"].(string)
		text, _ := params["message"].(string)
		value, _ := params["defaultValue"].(string)
		view.dialog = &liveDialog{Context: contextID, Kind: kind, Message: text, Value: value}
		view.dialogSeq++
		view.notify()
	case "browsingContext.userPromptClosed":
		if view.dialog != nil && view.dialog.Context == contextID {
			view.closeDialog()
		}
	}
}

// closeDialog 在持锁期间清除页面对话框并恢复截图
func (view *LiveView) closeDialog() {
	view.dialog = nil
	view.dialogSeq++
	view.notify()
	signal(view.capture)
}

// attach 把新启动的登录浏览器接到 LiveView，root 为它的初始顶层页面
func (view *LiveView) attach(ctx context.Context, client *bidiClient, root string) (func(), error) {
	return view.attachWith(ctx, client, []string{root})
}

// attachTo 把运行中的浏览器接到 LiveView：读取现有顶层页面后开始截图与输入转发，返回的函数停止转发
func (view *LiveView) attachTo(ctx context.Context, client *bidiClient) (func(), error) {
	tree, err := client.command(ctx, "browsingContext.getTree", map[string]any{"maxDepth": 0})
	if err != nil {
		return nil, fmt.Errorf("读取浏览器标签页: %w", err)
	}
	rawContexts, _ := tree["contexts"].([]any)
	roots := make([]string, 0, len(rawContexts))
	for _, raw := range rawContexts {
		if item, ok := raw.(map[string]any); ok {
			if id, _ := item["context"].(string); id != "" {
				roots = append(roots, id)
			}
		}
	}
	if len(roots) == 0 {
		return nil, errors.New("浏览器没有打开的标签页")
	}
	return view.attachWith(ctx, client, roots)
}

// attachWith 订阅顶层页面与对话框事件，开始截图与输入转发；返回的函数停止并等待这些协程
func (view *LiveView) attachWith(ctx context.Context, client *bidiClient, roots []string) (func(), error) {
	view.mu.Lock()
	view.contexts = append([]string(nil), roots...)
	view.client = client
	view.closed = false
	view.closedReason = ""
	view.mu.Unlock()
	unsubscribe := client.addListener(view.observe)
	subscription := map[string]any{"events": liveViewEvents}
	if _, err := client.command(ctx, "session.subscribe", subscription); err != nil {
		unsubscribe()
		return nil, err
	}
	loopCtx, cancel := context.WithCancel(ctx)
	var group sync.WaitGroup
	group.Go(func() { view.runFrames(loopCtx, client) })
	group.Go(func() { view.runInputs(loopCtx, client) })
	return func() {
		cancel()
		group.Wait()
		unsubscribe()
		// 连接仍正常时取消订阅，连接已断开则命令失败可忽略
		if client.Failure() == nil {
			unsubCtx, unsubCancel := context.WithTimeout(context.Background(), liveControlTimeout)
			_, _ = client.command(unsubCtx, "session.unsubscribe", subscription)
			unsubCancel()
		}
		view.mu.Lock()
		view.client = nil
		view.inputs = nil
		view.mu.Unlock()
	}, nil
}

// watched 判断是否需要截图：登录窗口已连接且页面没有等待处理的对话框
func (view *LiveView) watched() bool {
	view.mu.Lock()
	defer view.mu.Unlock()
	return view.viewer != nil && view.dialog == nil
}

// activeContext 返回画面所属的顶层页面，即最后打开且仍存在的页面
func (view *LiveView) activeContext() string {
	view.mu.Lock()
	defer view.mu.Unlock()
	return view.activeContextLocked()
}

// activeContextLocked 返回当前顶层页面，调用方需持有 view.mu
func (view *LiveView) activeContextLocked() string {
	if len(view.contexts) == 0 {
		return ""
	}
	return view.contexts[len(view.contexts)-1]
}

// markClosed 记下浏览器连接断开的原因并唤醒窗口连接
func (view *LiveView) markClosed(reason error) {
	view.mu.Lock()
	if view.closed {
		view.mu.Unlock()
		return
	}
	view.closed = true
	if reason != nil {
		view.closedReason = reason.Error()
	} else {
		view.closedReason = "浏览器连接已断开"
	}
	view.mu.Unlock()
	view.notify()
}

// runFrames 在登录窗口连接期间按间隔截取当前顶层页面，画面变化时推给登录窗口
func (view *LiveView) runFrames(ctx context.Context, client *bidiClient) {
	for {
		if !view.watched() {
			select {
			case <-ctx.Done():
				return
			case <-client.Done():
				view.markClosed(client.Failure())
				return
			case <-view.capture:
			}
			continue
		}
		started := time.Now()
		view.captureFrame(ctx, client)
		wait := liveViewFrameInterval - time.Since(started)
		timer := time.NewTimer(max(wait, 0))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-client.Done():
			timer.Stop()
			view.markClosed(client.Failure())
			return
		case <-timer.C:
		}
	}
}

// captureFrame 截取当前顶层页面，与上一帧相同时跳过
func (view *LiveView) captureFrame(ctx context.Context, client *bidiClient) {
	contextID := view.activeContext()
	if contextID == "" {
		return
	}
	shot, err := client.command(ctx, "browsingContext.captureScreenshot", map[string]any{"context": contextID, "format": liveViewFormat})
	if err != nil {
		return
	}
	encoded, _ := shot["data"].(string)
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return
	}
	view.mu.Lock()
	previous := view.frame
	view.mu.Unlock()
	if previous != nil && previous.Context == contextID && bytes.Equal(previous.data, data) {
		return
	}
	frame := &liveFrame{Type: "frame", Context: contextID, data: data}
	if !frame.readMeta(ctx, client) {
		if previous == nil || previous.Context != contextID {
			return
		}
		frame.URL, frame.Title, frame.Width, frame.Height, frame.Scale = previous.URL, previous.Title, previous.Width, previous.Height, previous.Scale
	}
	view.mu.Lock()
	view.frame = frame
	view.frameSeq++
	view.notify()
	view.mu.Unlock()
}

// readMeta 读取画面所属页面的地址、标题与视口
func (frame *liveFrame) readMeta(ctx context.Context, client *bidiClient) bool {
	encoded, err := client.evaluateString(ctx, frame.Context, liveViewMetaExpression)
	if err != nil {
		return false
	}
	var meta []any
	if json.Unmarshal([]byte(encoded), &meta) != nil || len(meta) != 5 {
		return false
	}
	frame.URL, _ = meta[0].(string)
	frame.Title, _ = meta[1].(string)
	frame.Width, _ = meta[2].(float64)
	frame.Height, _ = meta[3].(float64)
	frame.Scale, _ = meta[4].(float64)
	return frame.Width > 0 && frame.Height > 0
}

// runInputs 按到达顺序把登录窗口的输入转成 input.performActions 发给其所属页面
func (view *LiveView) runInputs(ctx context.Context, client *bidiClient) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-client.Done():
			return
		case <-view.inputReady:
		}
		view.mu.Lock()
		inputs, frame, contexts := view.inputs, view.frame, slices.Clone(view.contexts)
		view.inputs = nil
		view.mu.Unlock()
		active := ""
		if len(contexts) > 0 {
			active = contexts[len(contexts)-1]
		}
		for _, batch := range liveBatches(inputs, frame, active) {
			if !slices.Contains(contexts, batch.context) {
				continue
			}
			_, _ = client.command(ctx, "input.performActions", map[string]any{
				"context": batch.context, "actions": []map[string]any{batch.source},
			})
			if ctx.Err() != nil {
				return
			}
		}
	}
}

// liveBatches 把连续的同类输入合成对同一页面的一次 input.performActions，坐标按该页面最新一帧的视口取整并限制在视口内
func liveBatches(inputs []liveInput, frame *liveFrame, active string) []liveBatch {
	var batches []liveBatch
	for _, input := range inputs {
		target := input.Context
		if target == "" {
			target = active
		}
		x, y := livePoint(input, frame, target)
		var actions []map[string]any
		kind := input.Type
		switch input.Type {
		case "pointer":
			move := map[string]any{"type": "pointerMove", "x": x, "y": y}
			switch input.Action {
			case "move":
				actions = []map[string]any{move}
			case "down":
				actions = []map[string]any{move, {"type": "pointerDown", "button": input.Button}}
			case "up":
				actions = []map[string]any{move, {"type": "pointerUp", "button": input.Button}}
			}
		case "wheel":
			actions = []map[string]any{{
				"type": "scroll", "x": x, "y": y,
				"deltaX": int(math.Round(input.DeltaX)), "deltaY": int(math.Round(input.DeltaY)),
			}}
		case "key":
			if value, ok := webDriverKey(input.Key); ok && (input.Action == "down" || input.Action == "up") {
				action := "keyDown"
				if input.Action == "up" {
					action = "keyUp"
				}
				actions = []map[string]any{{"type": action, "value": value}}
			}
		case "text":
			kind = "key"
			actions = textActions(input.Text)
		}
		if len(actions) == 0 {
			continue
		}
		if count := len(batches); count > 0 && batches[count-1].context == target && batches[count-1].source["type"] == kind {
			source := batches[count-1].source
			source["actions"] = append(source["actions"].([]map[string]any), actions...)
			continue
		}
		batches = append(batches, liveBatch{context: target, source: liveSource(kind, actions)})
	}
	return batches
}

// livePoint 返回输入在页面视口内的整数坐标
func livePoint(input liveInput, frame *liveFrame, target string) (int, int) {
	x, y := int(math.Round(input.X)), int(math.Round(input.Y))
	if frame != nil && frame.Context == target {
		x = min(max(x, 0), max(int(frame.Width)-1, 0))
		y = min(max(y, 0), max(int(frame.Height)-1, 0))
	}
	return x, y
}

// liveSource 返回 input.performActions 的输入源
func liveSource(kind string, actions []map[string]any) map[string]any {
	switch kind {
	case "pointer":
		return map[string]any{"type": "pointer", "id": "mouse", "parameters": map[string]any{"pointerType": "mouse"}, "actions": actions}
	case "wheel":
		return map[string]any{"type": "wheel", "id": "wheel", "actions": actions}
	default:
		return map[string]any{"type": "key", "id": "keyboard", "actions": actions}
	}
}

// textActions 把粘贴或输入法提交的文本转成逐字符的按下与抬起，换行与制表符按 Enter 与 Tab
func textActions(text string) []map[string]any {
	var actions []map[string]any
	for _, char := range text {
		value := string(char)
		switch {
		case char == '\n':
			value = webDriverKeys["Enter"]
		case char == '\t':
			value = webDriverKeys["Tab"]
		case char < 0x20 || char == 0x7f:
			continue
		}
		actions = append(actions, map[string]any{"type": "keyDown", "value": value}, map[string]any{"type": "keyUp", "value": value})
	}
	return actions
}

// webDriverKeys 把 KeyboardEvent.key 的命名键映射到 WebDriver 键码点
var webDriverKeys = map[string]string{
	"Cancel": "\uE001", "Help": "\uE002", "Backspace": "\uE003", "Tab": "\uE004", "Clear": "\uE005",
	"Enter": "\uE007", "Shift": "\uE008", "Control": "\uE009", "Alt": "\uE00A", "Pause": "\uE00B",
	"Escape": "\uE00C", "PageUp": "\uE00E", "PageDown": "\uE00F", "End": "\uE010", "Home": "\uE011",
	"ArrowLeft": "\uE012", "ArrowUp": "\uE013", "ArrowRight": "\uE014", "ArrowDown": "\uE015",
	"Insert": "\uE016", "Delete": "\uE017",
	"F1": "\uE031", "F2": "\uE032", "F3": "\uE033", "F4": "\uE034", "F5": "\uE035", "F6": "\uE036",
	"F7": "\uE037", "F8": "\uE038", "F9": "\uE039", "F10": "\uE03A", "F11": "\uE03B", "F12": "\uE03C",
	"Meta": "\uE03D", "ZenkakuHankaku": "\uE040",
}

// webDriverKey 返回 KeyboardEvent.key 对应的 WebDriver 键值：命名键取键码点，单个字符原样发送，其他键不转发
func webDriverKey(key string) (string, bool) {
	if value, ok := webDriverKeys[key]; ok {
		return value, true
	}
	if utf8.RuneCountInString(key) == 1 {
		return key, true
	}
	return "", false
}
