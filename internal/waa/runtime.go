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
}

// Runtime 在嵌入的 SpiderMonkey 中持有一份与官网页面同形态的 BotGuard 生命周期
type Runtime struct {
	loop        *eventLoop
	agent       *agent
	snapshot    mozjs.Handle
	shutdown    mozjs.Handle
	prompt      mozjs.Handle
	clear       mozjs.Handle
	options     mozjs.Handle
	bind        mozjs.Handle
	deliver     mozjs.Handle
	results     map[float64]func(string)
	nextResult  float64
	operationMu sync.Mutex
	closeOnce   sync.Once
	pending     sync.WaitGroup
}

// promptScript 按官网 Worker 的方式向提示词输入框写值并派发 input 与 change，submit 时点击 Run
const promptScript = `((prompt, submit) => {
  let box = document.querySelector('ms-prompt-box');
  if (!box) {
    box = document.createElement('ms-prompt-box');
    document.body.appendChild(box);
    box.appendChild(document.createElement('textarea'));
    const run = document.createElement('ms-run-button');
    document.body.appendChild(run);
    run.appendChild(document.createElement('button'));
  }
  const textarea = box.querySelector('textarea');
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
  setter.call(textarea, prompt);
  textarea.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: prompt }));
  textarea.dispatchEvent(new Event('change', { bubbles: true }));
  if (submit) document.querySelector('ms-run-button').querySelector('button').click();
  return textarea.value;
})`

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

// noopScript 是官网页面传给 BotGuard 初始化函数的回调
const noopScript = "[((v, x, C, G) => {}), ((v, x) => {}), (v => {}), (v => {}), ((v, x) => {})]"

// NewRuntime 在 Firefox 宿主中加载解释器并初始化 challenge program
func NewRuntime(ctx context.Context, options Options) (*Runtime, error) {
	return newRuntime(ctx, options, "")
}

// newRuntime 创建 VM，prelude 在顶层宿主安装后、解释器执行前运行
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
	runtime := &Runtime{loop: loop, results: make(map[float64]func(string))}
	ready := make(chan error, 1)
	var readyOnce sync.Once
	complete := func(err error) { readyOnce.Do(func() { ready <- err }) }
	if !loop.post(func() {
		camou := camouLocale(options.Profile.Locale)
		mozjs.SetCamouLocale(camou[0], camou[1], camou[2])
		cx, err := mozjs.NewContext(mozjs.Options{HeapBytes: 1 << 30, Locale: options.Profile.Locale})
		if err != nil {
			complete(err)
			return
		}
		state := &agent{cx: cx, loop: loop, profile: options.Profile, loadImage: options.LoadImage, pending: &runtime.pending, timezone: location.String(), navigationStart: options.Profile.NavigationStart}
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
	callback, err := cx.NewFunction(top.global, "", 2, func(call *mozjs.Call) error {
		snapshot := call.Arg(0)
		if snapshot.Kind != mozjs.KindHandle {
			complete(errors.New("WAA 解释器未返回 snapshot 函数"))
			return nil
		}
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
	noop, err := handleOf(state.evaluate(top.global, noopScript, ""))
	if err != nil {
		return err
	}
	defer cx.Release(noop)
	callbacks := make([]mozjs.Value, 5)
	for index := range callbacks {
		callbacks[index], err = cx.Get(noop, fmt.Sprint(index))
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

// Settle 等待 VM 发出的页面资源请求完成
func (runtime *Runtime) Settle(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		runtime.pending.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// FillPrompt 向页面提示词输入框写入文本并派发官网 Worker 相同的事件，submit 时点击 Run
func (runtime *Runtime) FillPrompt(ctx context.Context, prompt string, submit bool) error {
	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
	return runtime.fillPrompt(ctx, prompt, submit)
}

func (runtime *Runtime) fillPrompt(ctx context.Context, prompt string, submit bool) error {
	prompt = textareaValue(prompt)
	result := make(chan error, 1)
	if !runtime.loop.post(func() {
		if err := ctx.Err(); err != nil {
			result <- err
			return
		}
		value, err := runtime.agent.invoke(runtime.prompt, 0, mozjs.String(prompt), mozjs.Bool(submit))
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
	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
	defer runtime.loop.post(func() {
		if value, err := runtime.agent.invoke(runtime.clear, 0); err == nil {
			runtime.agent.cx.ReleaseValue(value)
		} else {
			runtime.agent.release(err)
		}
	})
	if err := runtime.fillPrompt(ctx, prompt, false); err != nil {
		return "", err
	}
	return runtime.snapshotWith(ctx, mozjs.String(digest))
}

// RefreshSnapshot 生成官网刷新 Waa/Create 时携带的无绑定 snapshot
func (runtime *Runtime) RefreshSnapshot(ctx context.Context) (string, error) {
	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
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
		runtime.results[id] = func(value string) { finish(value, nil) }
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

// Close 结束 VM 生命周期并停止事件循环，关闭超时时中断仍在执行的脚本
func (runtime *Runtime) Close() {
	runtime.closeOnce.Do(func() {
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
