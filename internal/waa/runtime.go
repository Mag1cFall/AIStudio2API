package waa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/mozjs"
)

// Options 定义一份 BotGuard VM 的解释器、宿主现场与网络依赖
type Options struct {
	Challenge   Challenge
	Interpreter string
	Profile     Profile
	LoadImage   func(string) bool
	// LoadFrame 以浏览器 iframe 导航形状载入地址，返回是否收到文档响应
	LoadFrame func(string) bool
	// Pointer 是页面上可信指针与焦点所在的位置，同一页面先后新建的 VM 共用；为 nil 时按刚载入的页面
	Pointer *Pointer
}

// Pointer 是页面上可信指针与焦点所在的位置
type Pointer struct {
	mu sync.Mutex
	// at 是指针所在的位置（run、turn、rerun、content），指针还没进入页面时为空
	at string
	// focus 是焦点所在的位置（prompt、run、rerun），焦点不在页面元素上时为空
	focus string
	// rounds 是最近一次 Rerun 时对话的轮数，决定最后一轮与其 Rerun 按钮的位置
	rounds int
	// clicked 是最近一次派发可信 click 的时刻
	clicked time.Time
}

// Clicked 返回最近一次点击 Run 或 Rerun 时派发可信 click 的时刻
func (pointer *Pointer) Clicked() time.Time {
	pointer.mu.Lock()
	defer pointer.mu.Unlock()
	return pointer.clicked
}

// NewPointer 返回刚载入的页面上的位置：指针还没进入页面，焦点在提示词输入框
func NewPointer() *Pointer {
	return &Pointer{focus: "prompt"}
}

// Runtime 在嵌入的 SpiderMonkey 中持有一份与官网页面同形态的 BotGuard 生命周期
type Runtime struct {
	loop       *eventLoop
	agent      *agent
	snapshot   mozjs.Handle
	shutdown   mozjs.Handle
	prompt     mozjs.Handle
	mouse      mozjs.Handle
	clear      mozjs.Handle
	options    mozjs.Handle
	bind       mozjs.Handle
	deliver    mozjs.Handle
	results    map[float64]func(string)
	nextResult float64
	// metrics 是上次取出以来按官网包装层记录的 BotGuard 指标，只在事件循环中读写
	metrics     []BotGuardMetric
	operationMu sync.Mutex
	pointer     *Pointer
	closeOnce   sync.Once
	// idle 在最后一次操作 idleShrinkDelay 后执行收缩 GC
	idle *time.Timer
}

// idleShrinkDelay 为 VM 最后一次操作后执行收缩 GC 的空闲时长，与 Firefox 正式版 javascript.options.compact_on_user_inactive_delay 相同
const idleShrinkDelay = 5 * time.Minute

// promptElements 在页面还没有提示词输入框时建出输入框与 Run 按钮
const promptElements = `
  if (!document.querySelector('ms-prompt-box')) {
    document.body.appendChild(document.createElement('ms-prompt-box')).appendChild(document.createElement('textarea'));
    document.body.appendChild(document.createElement('ms-run-button')).appendChild(document.createElement('button')).appendChild(document.createElement('span'));
  }`

// promptScript 按官网 Worker 的方式向提示词输入框写值并派发 input 与 change
const promptScript = `(prompt => {` + promptElements + `
  const textarea = document.querySelector('ms-prompt-box').querySelector('textarea');
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
  setter.call(textarea, prompt);
  textarea.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: prompt }));
  textarea.dispatchEvent(new Event('change', { bubbles: true }));
  return textarea.value;
})`

// pointerScript 以宿主的 mouseInput 派发一组可信鼠标与焦点事件：from 与 to 为指针移动前后的位置，blurred 与 focused 为焦点移出与移入的位置，空串表示不在页面元素上，rounds 为对话轮数。位置 run 为 Run 按钮中心处的文字，turn 为最后一轮中心处的正文，rerun 为 Rerun 按钮中心处的图标路径，content 为 Rerun 后最后一轮被移除时指针下的对话区；焦点位置 prompt 为提示词输入框，run 与 rerun 为对应按钮。布局按官网新对话页：左侧导航宽 220、右侧运行设置宽 300，对话列宽至多 1000 并在两者之间居中；Run 按钮宽 101.2、高 32、右缘距对话列右缘 25、上缘距视口底 59，中心处文字距按钮左缘 45、上缘 6，宽 24.2、高 20；每轮为一行短文本时最后一轮上缘一轮为 197.5、每多一轮下移 212，高 125.5，对话区超出时滚到底部，Rerun 按钮距对话列左缘 911、在最后一轮上缘之上 19、边长 32，图标与路径的矩形按官网取值；取值按 Firefox 以单精度浮点返回
const pointerScript = `((input, from, to, blurred, focused, rounds, types) => {` + promptElements + `
  const section = innerWidth - 520;
  const box = Math.min(section, 1000);
  const left = 220 + (section - box) / 2;
  let turn = document.querySelector('ms-chat-turn');
  if (!turn) {
    turn = document.body.appendChild(document.createElement('div')).appendChild(document.createElement('ms-chat-turn'));
    turn.appendChild(document.createElement('p'));
    const svg = 'http://www.w3.org/2000/svg';
    turn.appendChild(document.createElement('div')).appendChild(document.createElement('button')).appendChild(document.createElementNS(svg, 'svg')).appendChild(document.createElementNS(svg, 'g')).appendChild(document.createElementNS(svg, 'path'));
  }
  const run = document.querySelector('ms-run-button').querySelector('button');
  const rerun = turn.querySelector('button');
  const x = Math.fround(left + box - 126.2);
  const y = innerHeight - 59;
  const button = [x, y, Math.fround(101.2), 32];
  const top = Math.min(197.5 + 212 * (rounds - 1), innerHeight - 307.5);
  const icon = [Math.fround(left + 919 + 25 / 60), Math.fround(top - 10.5 - 5 / 60), Math.fround(15 + 11 / 60), Math.fround(15 + 11 / 60)];
  const rerunPoint = [Math.round(left + 927), Math.round(top - 3)];
  const spots = {
    run: { target: run.firstElementChild, point: [Math.round(x + Math.fround(101.2) / 2), y + 16], nodes: [run.firstElementChild, run, run.parentElement], rects: [[Math.fround(x + 45), y + 6, Math.fround(24.2), 20], button, button] },
    turn: { target: turn.firstElementChild, point: [Math.round(left + box / 2), Math.round(top + 62.75)], nodes: [turn], rects: [[left, top, box, 125.5]] },
    rerun: { target: rerun.querySelector('path'), point: rerunPoint, nodes: [rerun.querySelector('path'), rerun.querySelector('g'), rerun.querySelector('svg'), rerun, rerun.parentElement], rects: [icon, icon, [left + 917, top - 13, 20, 20], [left + 911, top - 19, 32, 32], [left + 867, top - 23, 120, 40]] },
    content: { target: turn.parentElement, point: rerunPoint, nodes: [turn.parentElement], rects: [[left, 88, box, top + 81.5]] },
  };
  const focusable = { prompt: document.querySelector('ms-prompt-box').querySelector('textarea'), run, rerun };
  const spot = spots[to];
  const movement = from ? [spot.point[0] - spots[from].point[0], spot.point[1] - spots[from].point[1]] : [0, 0];
  input(spot.nodes, spot.rects, spot.target, from ? spots[from].target : null, spot.point, movement, focusable[blurred] ?? null, focusable[focused] ?? null, types);
})`

// pointerStep 是一次可信指针操作与它之前的间隔：move 把指针移到 to，press 按下并把焦点移到指针下的按钮，release 抬起并点击，replace 为页面替换了指针下的元素
type pointerStep struct {
	delay  time.Duration
	action string
	to     string
}

var (
	// runClick 是 Camoufox 后端以可信指针用 120 ms 移到 Run 按钮中心、按下、停顿 60 ms 再抬起时 Firefox 派发各组事件的间隔
	runClick = []pointerStep{{0, "move", "run"}, {5 * time.Millisecond, "press", ""}, {65 * time.Millisecond, "release", ""}}
	// rerunClick 是 Camoufox 后端把指针移到最后一轮中心、等 Rerun 按钮出现后以同样方式点击它时 Firefox 派发各组事件的间隔
	rerunClick = []pointerStep{{0, "move", "turn"}, {time.Second, "move", "rerun"}, {5 * time.Millisecond, "press", ""}, {65 * time.Millisecond, "release", ""}}
)

// events 返回 step 在当前位置上 Firefox 依次派发的事件与事件涉及的位置，并把位置更新为操作之后：指针换到另一元素时先派发离开与进入事件，按下时焦点移到指针下的按钮，原焦点不在页面元素上时只有 focusin；Run 按钮切换为 Stop 或切换回 Run 时指针下为新按钮，Rerun 后最后一轮被移除时指针下为对话区，焦点随按钮移除而不在页面元素上，不派发 focusout
func (pointer *Pointer) events(step pointerStep) (from, to, blurred, focused string, rounds int, types []string) {
	pointer.mu.Lock()
	defer pointer.mu.Unlock()
	from, to, blurred, focused, rounds = pointer.at, pointer.at, pointer.focus, pointer.focus, pointer.rounds
	switch step.action {
	case "move":
		to = step.to
		switch from {
		case to:
			types = []string{"pointermove", "mousemove"}
		case "":
			types = []string{"pointerover", "pointermove", "mouseover", "mousemove"}
		default:
			types = []string{"pointerout", "pointerover", "pointermove", "mouseout", "mouseover", "mousemove"}
		}
	case "press":
		types = []string{"pointerdown", "mousedown"}
		if focused = from; focused != blurred {
			if blurred != "" {
				types = append(types, "focusout")
			}
			types = append(types, "focusin")
		}
	case "release":
		types = []string{"pointerup", "mouseup", "click"}
		pointer.clicked = time.Now()
	case "replace":
		switch from {
		case "run":
		case "rerun":
			to = "content"
			if focused == "rerun" {
				focused = ""
			}
		default:
			return
		}
		types = []string{"pointerover", "mouseover"}
	}
	pointer.at, pointer.focus = to, focused
	return
}

// runProofDelay 是官网页面应用从 Run 按钮的 click 到为 GenerateContent 取 proof 的间隔
const runProofDelay = 50 * time.Millisecond

// clearPromptScript 以原生 setter 清空提示词输入框，与 Camoufox 页面 proof 调用结束时相同，不派发事件
const clearPromptScript = `(() => {
  const box = document.querySelector('ms-prompt-box');
  if (box) Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(box.querySelector('textarea'), '');
})`

// helperScript 返回宿主在顶层 Realm 中新建数组、snapshot 选项与按序号回传结果的函数
const helperScript = `({
  array: (...items) => items,
  options: digest => [digest === undefined ? undefined : { content: digest }, undefined, undefined, undefined],
  bind: (deliver, id) => value => deliver(id, value),
})`

// recorderScript 返回官网 BotGuard 包装层传给初始化函数的计时回调与四个记录函数：h、u 事件逐次计数并记录耗时，k 事件按随机计数约每 200 次记录一次
const recorderScript = `(record => {
  let skip = Math.floor(Math.random() * 200);
  return [
    (u, w, B, F) => {
      const name = w ? 'h' : B ? 'u' : 'k';
      if (name !== 'k') {
        if (F !== 0) record('ec', name, 1), record('el', name, u);
      } else if (skip <= 0) {
        record('ec', name, 1), record('el', name, u);
        skip = Math.floor(Math.random() * 200);
      } else skip--;
    },
    (u, w) => record('el', u, w),
    u => record('cec', String(u), 1),
    u => record('od/p', '', u),
    (u, w) => record('od/n', u, w),
  ];
})`

// BotGuardMetric 是官网 BotGuard 包装层记录的一条 client streamz 指标，Metric 为 ec 或 cec 时 Value 是计数增量，其余为分布取值
type BotGuardMetric struct {
	Metric string
	Label  string
	Value  float64
}

// NewRuntime 在 Firefox 宿主中加载解释器并初始化 challenge program
func NewRuntime(ctx context.Context, options Options) (*Runtime, error) {
	return newRuntime(ctx, options, "")
}

// newRuntime 创建 VM，prelude 在顶层宿主安装后、解释器执行前运行，VM 就绪后以保留 JIT 代码的完整 GC 回收初始化留下的对象
func newRuntime(ctx context.Context, options Options, prelude string) (*Runtime, error) {
	if options.Interpreter == "" {
		return nil, errors.New("WAA 解释器为空")
	}
	if options.Challenge.Program == "" || options.Challenge.GlobalName == "" {
		return nil, errors.New("WAA challenge 缺少 program")
	}
	sourceName := options.Challenge.InterpreterURL
	if sourceName == "" {
		sourceName = "https://www.google.com/js/bg/" + options.Challenge.InterpreterHash + ".js"
	}
	zone := options.Profile.TimeZone
	if zone == "" {
		zone = "UTC"
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, err
	}
	loop := newEventLoop()
	pointer := options.Pointer
	if pointer == nil {
		pointer = NewPointer()
	}
	runtime := &Runtime{loop: loop, results: make(map[float64]func(string)), pointer: pointer}
	ready := make(chan error, 1)
	var readyOnce sync.Once
	complete := func(err error) { readyOnce.Do(func() { ready <- err }) }
	if !loop.post(func() {
		mozjs.SetCamouLocale(options.Profile.Locale)
		cx, err := mozjs.NewContext(mozjs.Options{HeapBytes: 1 << 30, Locale: options.Profile.Locale})
		if err != nil {
			complete(err)
			return
		}
		state := &agent{cx: cx, loop: loop, profile: options.Profile, loadImage: options.LoadImage, loadFrame: options.LoadFrame, timezone: location.String(), navigationStart: options.Profile.NavigationStart}
		runtime.agent = state
		loop.cleanup = cx.Close
		loop.afterTask = state.afterTask
		if err := runtime.start(options, sourceName, prelude, complete); err != nil {
			complete(err)
		}
	}) {
		loop.close()
		return nil, errors.New("WAA 事件循环未启动")
	}
	select {
	case err := <-ready:
		if err != nil {
			loop.close()
			return nil, err
		}
		loop.post(runtime.agent.cx.GC)
		runtime.idle = time.AfterFunc(idleShrinkDelay, func() { loop.post(runtime.agent.cx.ShrinkGC) })
		return runtime, nil
	case <-ctx.Done():
		loop.close()
		return nil, ctx.Err()
	}
}

// start 安装顶层 Realm、执行解释器并调用 BotGuard 初始化函数，snapshot 函数就绪时调用 complete
func (runtime *Runtime) start(options Options, sourceName, prelude string, complete func(error)) error {
	state := runtime.agent
	cx := state.cx
	top, err := state.installRealm("top", 0)
	if err != nil {
		return err
	}
	state.top = top
	helpers, err := handleOf(state.evaluate(top.global, helperScript, hostScriptName))
	if err != nil {
		return fmt.Errorf("编译 WAA 宿主辅助函数: %w", err)
	}
	defer cx.Release(helpers)
	if state.array, err = handleOf(cx.Get(helpers, "array")); err != nil {
		return err
	}
	if runtime.options, err = handleOf(cx.Get(helpers, "options")); err != nil {
		return err
	}
	if runtime.bind, err = handleOf(cx.Get(helpers, "bind")); err != nil {
		return err
	}
	if runtime.prompt, err = handleOf(state.evaluate(top.global, promptScript, hostScriptName)); err != nil {
		return err
	}
	if runtime.mouse, err = handleOf(state.evaluate(top.global, pointerScript, hostScriptName)); err != nil {
		return err
	}
	if runtime.clear, err = handleOf(state.evaluate(top.global, clearPromptScript, hostScriptName)); err != nil {
		return err
	}
	if prelude != "" {
		value, err := state.evaluate(top.global, prelude, "")
		if err != nil {
			return fmt.Errorf("执行 prelude: %w", err)
		}
		cx.ReleaseValue(value)
	}
	value, err := state.evaluate(top.global, options.Interpreter, sourceName)
	if err != nil {
		return fmt.Errorf("执行 WAA 解释器: %w", err)
	}
	cx.ReleaseValue(value)
	if value, err = state.invoke(top.order, 0); err != nil {
		return fmt.Errorf("排列 WAA 顶层全局键: %w", err)
	}
	cx.ReleaseValue(value)
	object, err := handleOf(cx.Get(top.global, options.Challenge.GlobalName))
	if err != nil {
		return errors.New("WAA 解释器未定义全局对象")
	}
	defer cx.Release(object)
	initialize, err := handleOf(cx.Get(object, "a"))
	if err != nil {
		return errors.New("WAA 解释器缺少初始化函数")
	}
	defer cx.Release(initialize)
	signals, state0, err := experimentSignals(cx, top.global, options.Challenge.ClientExperimentsStateBlob)
	if err != nil {
		return err
	}
	defer cx.ReleaseValue(signals)
	initStarted := time.Now()
	runtime.metrics = append(runtime.metrics, BotGuardMetric{Metric: "ec", Label: "t", Value: 1})
	callback, err := cx.NewFunction(top.global, "", 2, func(call *mozjs.Call) error {
		snapshot := call.Arg(0)
		if snapshot.Kind != mozjs.KindHandle {
			complete(errors.New("WAA 解释器未返回 snapshot 函数"))
			return nil
		}
		runtime.metrics = append(runtime.metrics, BotGuardMetric{Metric: "el", Label: "t", Value: float64(time.Since(initStarted).Milliseconds())})
		runtime.snapshot = snapshot.Handle
		if shutdown := call.Arg(1); shutdown.Kind == mozjs.KindHandle {
			runtime.shutdown = shutdown.Handle
		}
		complete(nil)
		return nil
	})
	if err != nil {
		return err
	}
	defer cx.Release(callback)
	if runtime.deliver, err = runtime.newDeliver(); err != nil {
		return err
	}
	record, err := cx.NewFunction(top.global, "", 3, func(call *mozjs.Call) error {
		label := call.Arg(1)
		cx.ReleaseValue(label)
		text := label.String
		if label.Kind == mozjs.KindNumber {
			text = fmt.Sprint(label.Number)
		}
		runtime.metrics = append(runtime.metrics, BotGuardMetric{Metric: call.ArgString(0), Label: text, Value: call.ArgNumber(2)})
		return nil
	})
	if err != nil {
		return err
	}
	defer cx.Release(record)
	recorder, err := handleOf(state.evaluate(top.global, recorderScript, ""))
	if err != nil {
		return err
	}
	defer cx.Release(recorder)
	recorded, err := handleOf(state.invoke(recorder, 0, mozjs.Object(record)))
	if err != nil {
		return err
	}
	defer cx.Release(recorded)
	callbacks := make([]mozjs.Value, 5)
	for index := range callbacks {
		callbacks[index], err = cx.Get(recorded, fmt.Sprint(index))
		if err != nil {
			return err
		}
		defer cx.ReleaseValue(callbacks[index])
	}
	loggers, err := state.newArray(callbacks[1], callbacks[2], callbacks[3], callbacks[4])
	if err != nil {
		return err
	}
	defer cx.ReleaseValue(loggers)
	result, err := state.invoke(initialize, object, mozjs.String(options.Challenge.Program), mozjs.Object(callback), mozjs.Bool(true), mozjs.Undefined(), callbacks[0], signals, state0, mozjs.Bool(false), loggers)
	if err != nil {
		return fmt.Errorf("初始化 WAA program: %w", err)
	}
	cx.ReleaseValue(result)
	return nil
}

// newDeliver 新建把 (序号, 结果) 交给等待中的请求的原生函数
func (runtime *Runtime) newDeliver() (mozjs.Handle, error) {
	state := runtime.agent
	deliver, err := state.cx.NewFunction(state.top.global, "deliver", 2, func(call *mozjs.Call) error {
		id := call.ArgNumber(0)
		value := call.Arg(1)
		state.cx.ReleaseValue(value)
		text := value.String
		if value.Kind != mozjs.KindString {
			text = fmt.Sprint(value.Number)
		}
		if finish, ok := runtime.results[id]; ok {
			delete(runtime.results, id)
			finish(text)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deliver, nil
}

// experimentSignals 按官网规则从 client experiments blob 派生 signal lists 与持久状态
func experimentSignals(cx *mozjs.Context, global mozjs.Handle, blob string) (mozjs.Value, mozjs.Value, error) {
	state := mozjs.Undefined()
	if blob == "" {
		signals, err := cx.ParseJSON(global, "[[],[]]")
		return signals, state, err
	}
	var fields []json.RawMessage
	if err := json.Unmarshal([]byte(blob), &fields); err != nil {
		return mozjs.Value{}, state, fmt.Errorf("解析 client experiments blob: %w", err)
	}
	var low, high []float64
	if len(fields) > 5 {
		var entries [][]json.RawMessage
		_ = json.Unmarshal(fields[5], &entries)
		for _, entry := range entries {
			if len(entry) < 2 {
				continue
			}
			var value, key float64
			if json.Unmarshal(entry[0], &value) != nil || json.Unmarshal(entry[1], &key) != nil {
				continue
			}
			if key <= 53 {
				low = append(low, value)
			} else {
				high = append(high, value)
			}
		}
	}
	if len(fields) > 4 {
		var stored string
		if json.Unmarshal(fields[4], &stored) == nil && stored != "" {
			state = mozjs.String(stored)
		}
	}
	list := make([]float64, 0, len(low)+len(high))
	types := make([]int, 0, len(low)+len(high))
	for _, value := range low {
		list = append(list, value)
		types = append(types, 1)
	}
	for _, value := range high {
		list = append(list, value)
		types = append(types, 2)
	}
	encoded, err := json.Marshal([]any{list, types})
	if err != nil {
		return mozjs.Value{}, state, err
	}
	signals, err := cx.ParseJSON(global, string(encoded))
	return signals, state, err
}

// Settle 等待 VM 发出的页面资源请求完成，请求完成回调中发出的新请求一并等待
func (runtime *Runtime) Settle(ctx context.Context) error {
	done := make(chan struct{})
	if !runtime.loop.post(func() {
		if runtime.agent.pending == 0 {
			close(done)
			return
		}
		runtime.agent.settled = append(runtime.agent.settled, done)
	}) {
		return errors.New("WAA runtime 已关闭")
	}
	select {
	case <-done:
		return nil
	case <-runtime.loop.done:
		return errors.New("WAA runtime 已关闭")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// fillPrompt 向页面提示词输入框写入文本并派发官网 Worker 相同的事件
func (runtime *Runtime) fillPrompt(ctx context.Context, prompt string) error {
	prompt = textareaValue(prompt)
	result := make(chan error, 1)
	if !runtime.loop.post(func() {
		if err := ctx.Err(); err != nil {
			result <- err
			return
		}
		value, err := runtime.agent.invoke(runtime.prompt, 0, mozjs.String(prompt))
		if err != nil {
			runtime.agent.release(err)
			result <- fmt.Errorf("写入页面提示词: %w", err)
			return
		}
		runtime.agent.cx.ReleaseValue(value)
		if value.Kind != mozjs.KindString || value.String != prompt {
			result <- errors.New("页面提示词状态未同步")
			return
		}
		result <- nil
	}) {
		return errors.New("WAA runtime 已关闭")
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// textareaValue 返回按 Firefox textarea 取值规则把 CRLF 与 CR 规范为 LF 的文本
func textareaValue(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}

// Proof 同步页面提示词后为 SHA-256 digest 生成 snapshot proof，结束后清空输入框
func (runtime *Runtime) Proof(ctx context.Context, digest string, prompt string) (string, error) {
	return runtime.proof(ctx, digest, prompt, true, nil)
}

// RunProof 向页面提示词输入框写入文本并点击 Run 后为 SHA-256 digest 生成 fresh proof，与官网用户点击 Run 后页面为 GenerateContent 取 proof 的顺序相同，返回前清空输入框
func (runtime *Runtime) RunProof(ctx context.Context, digest string, prompt string) (string, error) {
	return runtime.proof(ctx, digest, prompt, true, runClick)
}

// RerunProof 在 rounds 轮的对话上点击最后一轮的 Rerun 后为 SHA-256 digest 生成 fresh proof，与官网 Rerun 一样不写入提示词
func (runtime *Runtime) RerunProof(ctx context.Context, digest string, rounds int) (string, error) {
	runtime.pointer.mu.Lock()
	runtime.pointer.rounds = rounds
	runtime.pointer.mu.Unlock()
	return runtime.proof(ctx, digest, "", false, rerunClick)
}

// proof 在 fill 时写入提示词，按 click 以可信鼠标点击，再为 digest 生成 proof，返回前清空输入框
func (runtime *Runtime) proof(ctx context.Context, digest string, prompt string, fill bool, click []pointerStep) (string, error) {
	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
	defer runtime.idle.Reset(idleShrinkDelay)
	defer runtime.loop.post(func() {
		if value, err := runtime.agent.invoke(runtime.clear, 0); err == nil {
			runtime.agent.cx.ReleaseValue(value)
		} else {
			runtime.agent.release(err)
		}
	})
	if fill {
		if err := runtime.fillPrompt(ctx, prompt); err != nil {
			return "", err
		}
	}
	if click != nil {
		if err := runtime.click(ctx, click); err != nil {
			return "", err
		}
	}
	return runtime.snapshotWith(ctx, mozjs.String(digest))
}

// click 按 steps 的顺序与间隔以可信鼠标操作，再等到官网页面应用取 proof 的时刻
func (runtime *Runtime) click(ctx context.Context, steps []pointerStep) error {
	for _, step := range steps {
		select {
		case <-time.After(step.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := runtime.pointerInput(ctx, step); err != nil {
			return err
		}
	}
	select {
	case <-time.After(runProofDelay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Replaced 在页面替换指针下的元素时派发 Firefox 的 pointerover 与 mouseover：点击 Run 后按钮切换为 Stop 与切换回 Run，点击 Rerun 后最后一轮被移除；指针不在这两处时不派发
func (runtime *Runtime) Replaced(ctx context.Context) error {
	return runtime.pointerInput(ctx, pointerStep{action: "replace"})
}

// pointerInput 在事件循环中按 step 与页面当前的指针和焦点位置派发可信鼠标与焦点事件
func (runtime *Runtime) pointerInput(ctx context.Context, step pointerStep) error {
	result := make(chan error, 1)
	if !runtime.loop.post(func() {
		from, to, blurred, focused, rounds, types := runtime.pointer.events(step)
		if len(types) == 0 {
			result <- nil
			return
		}
		state := runtime.agent
		names := make([]mozjs.Value, len(types))
		for index, name := range types {
			names[index] = mozjs.String(name)
		}
		list, err := state.newArray(names...)
		if err != nil {
			result <- err
			return
		}
		defer state.cx.ReleaseValue(list)
		value, err := state.invoke(runtime.mouse, 0, mozjs.Object(state.mouseInput), mozjs.String(from), mozjs.String(to), mozjs.String(blurred), mozjs.String(focused), mozjs.Number(float64(rounds)), list)
		if err != nil {
			state.release(err)
			result <- fmt.Errorf("派发鼠标事件: %w", err)
			return
		}
		state.cx.ReleaseValue(value)
		result <- nil
	}) {
		return errors.New("WAA runtime 已关闭")
	}
	select {
	case err := <-result:
		return err
	case <-runtime.loop.done:
		return errors.New("WAA runtime 已关闭")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Activate 记录一次用户点击或按键：此后 navigator.userActivation 的 hasBeenActive 为 true，5 秒内 isActive 为 true
func (runtime *Runtime) Activate(ctx context.Context) error {
	result := make(chan error, 1)
	if !runtime.loop.post(func() {
		value, err := runtime.agent.invoke(runtime.agent.activate, 0)
		if err != nil {
			runtime.agent.release(err)
			result <- fmt.Errorf("记录用户操作: %w", err)
			return
		}
		runtime.agent.cx.ReleaseValue(value)
		result <- nil
	}) {
		return errors.New("WAA runtime 已关闭")
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RefreshSnapshot 生成官网刷新 Waa/Create 时携带的无绑定 snapshot
func (runtime *Runtime) RefreshSnapshot(ctx context.Context) (string, error) {
	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
	defer runtime.idle.Reset(idleShrinkDelay)
	return runtime.snapshotWith(ctx, mozjs.Undefined())
}

func (runtime *Runtime) snapshotWith(ctx context.Context, digest mozjs.Value) (string, error) {
	result := make(chan struct {
		value string
		err   error
	}, 1)
	var once sync.Once
	finish := func(value string, err error) {
		once.Do(func() {
			result <- struct {
				value string
				err   error
			}{value, err}
		})
	}
	if !runtime.loop.post(func() {
		if err := ctx.Err(); err != nil {
			finish("", err)
			return
		}
		state := runtime.agent
		runtime.nextResult++
		id := runtime.nextResult
		runtime.metrics = append(runtime.metrics, BotGuardMetric{Metric: "ec", Label: "n", Value: 1})
		started := time.Now()
		runtime.results[id] = func(value string) {
			runtime.metrics = append(runtime.metrics,
				BotGuardMetric{Metric: "el", Label: "n", Value: float64(time.Since(started).Milliseconds())},
				BotGuardMetric{Metric: "frs", Value: float64(len(value))})
			finish(value, nil)
		}
		callback, err := handleOf(state.invoke(runtime.bind, 0, mozjs.Object(runtime.deliver), mozjs.Number(id)))
		if err != nil {
			delete(runtime.results, id)
			finish("", err)
			return
		}
		defer state.cx.Release(callback)
		options, err := handleOf(state.invoke(runtime.options, 0, digest))
		if err != nil {
			delete(runtime.results, id)
			finish("", err)
			return
		}
		defer state.cx.Release(options)
		value, err := state.invoke(runtime.snapshot, 0, mozjs.Object(callback), mozjs.Object(options))
		if err != nil {
			delete(runtime.results, id)
			state.release(err)
			finish("", fmt.Errorf("执行 WAA snapshot: %w", err))
			return
		}
		state.cx.ReleaseValue(value)
	}) {
		return "", errors.New("WAA runtime 已关闭")
	}
	select {
	case output := <-result:
		return output.value, output.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// TakeMetrics 在事件循环中取出上次以来记录的 BotGuard 指标，排在已排队的任务之后执行，与官网包装层以 setTimeout(0) 上报的时机相同
func (runtime *Runtime) TakeMetrics(ctx context.Context) ([]BotGuardMetric, error) {
	result := make(chan []BotGuardMetric, 1)
	if !runtime.loop.post(func() {
		taken := runtime.metrics
		runtime.metrics = nil
		result <- taken
	}) {
		return nil, errors.New("WAA runtime 已关闭")
	}
	select {
	case metrics := <-result:
		return metrics, nil
	case <-runtime.loop.done:
		return nil, errors.New("WAA runtime 已关闭")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close 结束 VM 生命周期并停止事件循环，关闭超时时中断仍在执行的脚本
func (runtime *Runtime) Close() {
	runtime.closeOnce.Do(func() {
		runtime.idle.Stop()
		if runtime.shutdown != 0 {
			done := make(chan struct{})
			if runtime.loop.post(func() {
				if value, err := runtime.agent.invoke(runtime.shutdown, 0); err == nil {
					runtime.agent.cx.ReleaseValue(value)
				} else {
					runtime.agent.release(err)
				}
				close(done)
			}) {
				select {
				case <-done:
				case <-time.After(time.Second):
				}
			}
		}
		runtime.loop.close()
		select {
		case <-runtime.loop.exited:
		case <-time.After(time.Second):
			if runtime.agent != nil {
				runtime.agent.cx.Interrupt()
			}
		}
	})
}
