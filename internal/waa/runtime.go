package waa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja"
)

// Options 定义一份 BotGuard VM 的解释器、宿主现场与网络依赖
type Options struct {
	Challenge   Challenge
	Interpreter string
	Profile     Profile
	LoadImage   func(string) bool
}

// Runtime 在纯 Go JavaScript VM 中持有一份与官网页面同形态的 BotGuard 生命周期
type Runtime struct {
	loop        *eventLoop
	vm          *goja.Runtime
	snapshot    goja.Callable
	shutdown    goja.Callable
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

// NewRuntime 在 Firefox 宿主中加载解释器并初始化 challenge program
func NewRuntime(ctx context.Context, options Options) (*Runtime, error) {
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
	location, zoneName, err := timeZoneOf(options.Profile)
	if err != nil {
		return nil, err
	}
	loop := newEventLoop()
	runtime := &Runtime{loop: loop}
	ready := make(chan error, 1)
	var readyOnce sync.Once
	complete := func(err error) { readyOnce.Do(func() { ready <- err }) }
	if !loop.post(func() {
		vm := goja.New()
		runtime.vm = vm
		source, err := newFirefoxRandSource()
		if err != nil {
			complete(fmt.Errorf("初始化 WAA 随机源: %w", err))
			return
		}
		vm.SetRandSource(source)
		vm.SetMaxCallStackSize(maxCallStackDepth)
		state := &hostState{registry: newHostRegistry(), profile: options.Profile, loadImage: options.LoadImage, schedule: loop.schedule, location: location, zoneName: zoneName, pending: &runtime.pending}
		createIframe := func() *goja.Object {
			window, err := newIframeWindow(vm, state)
			if err != nil {
				panic(vm.NewGoError(err))
			}
			return window
		}
		api, err := state.installRealmHost(vm, "top", createIframe)
		if err != nil {
			complete(err)
			return
		}
		state.topAPI = api
		vm.SetEvalTransformer(state.registry.evalTransformer(vm, false))
		if _, err := vm.RunScript(sourceName, options.Interpreter); err != nil {
			complete(fmt.Errorf("执行 WAA 解释器: %w", err))
			return
		}
		global := vm.Get(options.Challenge.GlobalName)
		if global == nil || goja.IsUndefined(global) {
			complete(errors.New("WAA 解释器未定义全局对象"))
			return
		}
		object := global.ToObject(vm)
		initialize, ok := goja.AssertFunction(object.Get("a"))
		if !ok {
			complete(errors.New("WAA 解释器缺少初始化函数"))
			return
		}
		signals, state0, err := experimentSignals(vm, options.Challenge.ClientExperimentsStateBlob)
		if err != nil {
			complete(err)
			return
		}
		callback := func(call goja.FunctionCall) goja.Value {
			snapshot, snapshotOK := goja.AssertFunction(call.Argument(0))
			if !snapshotOK {
				complete(errors.New("WAA 解释器未返回 snapshot 函数"))
				return goja.Undefined()
			}
			runtime.snapshot = snapshot
			runtime.shutdown, _ = goja.AssertFunction(call.Argument(1))
			complete(nil)
			return goja.Undefined()
		}
		noop, err := vm.RunString("[((v, x, C, G) => {}), ((v, x) => {}), (v => {}), (v => {}), ((v, x) => {})]")
		if err != nil {
			complete(err)
			return
		}
		callbacks := noop.ToObject(vm)
		loggers := vm.NewArray(callbacks.Get("1"), callbacks.Get("2"), callbacks.Get("3"), callbacks.Get("4"))
		if _, err := initialize(object, vm.ToValue(options.Challenge.Program), vm.ToValue(callback), vm.ToValue(true), goja.Undefined(), callbacks.Get("0"), signals, state0, vm.ToValue(false), loggers); err != nil {
			complete(fmt.Errorf("初始化 WAA program: %w", err))
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

// experimentSignals 按官网规则从 client experiments blob 派生 signal lists 与持久状态
func experimentSignals(vm *goja.Runtime, blob string) (goja.Value, goja.Value, error) {
	values := vm.NewArray()
	kinds := vm.NewArray()
	state := goja.Undefined()
	if blob == "" {
		return vm.NewArray(values, kinds), state, nil
	}
	var fields []json.RawMessage
	if err := json.Unmarshal([]byte(blob), &fields); err != nil {
		return nil, nil, fmt.Errorf("解析 client experiments blob: %w", err)
	}
	var low, high []any
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
			state = vm.ToValue(stored)
		}
	}
	list := make([]any, 0, len(low)+len(high))
	types := make([]any, 0, len(low)+len(high))
	for _, value := range low {
		list = append(list, value)
		types = append(types, 1)
	}
	for _, value := range high {
		list = append(list, value)
		types = append(types, 2)
	}
	return vm.NewArray(vm.NewArray(list...), vm.NewArray(types...)), state, nil
}

// Settle 等待 VM 发出的页面资源请求完成，再保持 quiet 时长让异步回调落定
func (runtime *Runtime) Settle(ctx context.Context, quiet time.Duration) error {
	done := make(chan struct{})
	go func() {
		runtime.pending.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	timer := time.NewTimer(quiet)
	defer timer.Stop()
	select {
	case <-timer.C:
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
	result := make(chan error, 1)
	if !runtime.loop.post(func() {
		function, err := runtime.vm.RunScript(hostScriptName, promptScript)
		if err != nil {
			result <- err
			return
		}
		fill, _ := goja.AssertFunction(function)
		value, err := fill(goja.Undefined(), runtime.vm.ToValue(prompt), runtime.vm.ToValue(submit))
		if err != nil {
			result <- fmt.Errorf("写入页面提示词: %w", err)
			return
		}
		if value.String() != textareaValue(prompt) {
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

// textareaValue 返回 Firefox textarea 写入后的取值，CRLF 与 CR 规范为 LF
func textareaValue(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}

// Proof 同步页面提示词后为 SHA-256 digest 生成 snapshot proof
func (runtime *Runtime) Proof(ctx context.Context, digest string, prompt string) (string, error) {
	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
	if err := runtime.fillPrompt(ctx, prompt, false); err != nil {
		return "", err
	}
	return runtime.snapshotWith(ctx, func(vm *goja.Runtime) goja.Value {
		content := vm.NewObject()
		_ = content.Set("content", digest)
		return vm.NewArray(content, goja.Undefined(), goja.Undefined(), goja.Undefined())
	})
}

// RefreshSnapshot 生成官网刷新 Waa/Create 时携带的无绑定 snapshot
func (runtime *Runtime) RefreshSnapshot(ctx context.Context) (string, error) {
	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
	return runtime.snapshotWith(ctx, func(vm *goja.Runtime) goja.Value {
		return vm.NewArray(goja.Undefined(), goja.Undefined(), goja.Undefined(), goja.Undefined())
	})
}

func (runtime *Runtime) snapshotWith(ctx context.Context, options func(*goja.Runtime) goja.Value) (string, error) {
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
		callback := func(call goja.FunctionCall) goja.Value {
			finish(call.Argument(0).String(), nil)
			return goja.Undefined()
		}
		if _, err := runtime.snapshot(goja.Undefined(), runtime.vm.ToValue(callback), options(runtime.vm)); err != nil {
			finish("", fmt.Errorf("执行 WAA snapshot: %w", err))
		}
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

// Close 结束 VM 生命周期并停止事件循环
func (runtime *Runtime) Close() {
	runtime.closeOnce.Do(func() {
		if runtime.shutdown != nil {
			done := make(chan struct{})
			if runtime.loop.post(func() {
				_, _ = runtime.shutdown(goja.Undefined())
				close(done)
			}) {
				select {
				case <-done:
				case <-time.After(time.Second):
				}
			}
		}
		runtime.loop.close()
	})
}
