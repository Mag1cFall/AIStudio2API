package waa

import (
	"bytes"
	"compress/gzip"
	cryptorand "crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/mozjs"
)

//go:embed dom.js
var domJavaScript string

//go:embed firefox152.json.gz
var firefoxShapeGzip []byte

//go:embed smhost.js
var smHostJavaScript string

// hostScriptName 是宿主脚本的来源名，SpiderMonkey 按自托管脚本把它们排除在调用栈与调用方定位之外
const hostScriptName = "self-hosted"

// gunzip 解压内嵌的 gzip 数据
func gunzip(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(reader)
}

// firefoxShapeJSON 解压内嵌的 Firefox 形状表
var firefoxShapeJSON = sync.OnceValues(func() (string, error) {
	data, err := gunzip(firefoxShapeGzip)
	return string(data), err
})

// Profile 保存账户指纹与页面状态决定的宿主现场值
type Profile struct {
	PageURL      string
	Locale       string
	TimeZone     string
	Window       map[string]any
	Navigator    map[string]any
	Screen       map[string]any
	Document     map[string]any
	Globals      map[string]any
	LocalStorage map[string]string
	HasFocus     bool
	// TrustedTypes 表示页面 CSP 要求脚本注入点使用 Trusted Types
	TrustedTypes bool
	// NavigationStart 是页面导航开始的时间，为零时页面时间起点取宿主安装时刻
	NavigationStart time.Time
}

// closureListenerMapPattern 匹配 Closure 事件库在页面载入时以随机数命名的监听表全局名
var closureListenerMapPattern = regexp.MustCompile(`closure_lm_\d+`)

// frameWindowNames 是 iframe 与顶层页面共享取值的 Window 属性
var frameWindowNames = []string{"outerWidth", "outerHeight", "screenX", "screenY", "screenLeft", "screenTop", "devicePixelRatio", "mozInnerScreenX"}

// hostJavaScript 为一个 Realm 安装 Function.prototype.toString、计时器与 Firefox DOM
const hostJavaScript = `
(input => {
  const markNative = input.markNative;
  const nativeSourceOf = input.nativeSource;
  const originalFunctionToString = Function.prototype.toString;
  const nativeFunction = (name, implementation) => {
    Object.defineProperty(implementation, 'name', { configurable: true, value: name });
    markNative(implementation, 'function ' + name + '() {\n    [native code]\n}');
    return implementation;
  };
  const functionToString = function toString() {
    'use strict';
    const marked = nativeSourceOf(this);
    if (marked !== undefined) return marked;
    return originalFunctionToString.call(this);
  };
  markNative(functionToString, 'function toString() {\n    [native code]\n}');
  Function.prototype.toString = functionToString;
  const timers = input.timers;
  globalThis.setTimeout = nativeFunction('setTimeout', function setTimeout(callback, delay, ...args) { return timers.setTimeout(callback, delay, ...args); });
  globalThis.setInterval = nativeFunction('setInterval', function setInterval(callback, delay, ...args) { return timers.setInterval(callback, delay, ...args); });
  globalThis.clearTimeout = nativeFunction('clearTimeout', function clearTimeout(id) { timers.clear(id); });
  globalThis.clearInterval = nativeFunction('clearInterval', function clearInterval(id) { timers.clear(id); });
  let domAPI;
  let idleCallbackCount = 0;
  let idleHandle = 0;
  const idleCallbacks = new Set();
  globalThis.requestIdleCallback = nativeFunction('requestIdleCallback', function requestIdleCallback(callback, options = {}) {
    const timeout = options.timeout > 0 ? Math.min(options.timeout, 50) : 0;
    const requested = input.preciseNow();
    const timedOut = timeout > 0 && idleCallbackCount++ % 2 === 1;
    const handle = ++idleHandle;
    idleCallbacks.add(handle);
    input.queueTask(timedOut ? timeout : 1, () => {
      if (!idleCallbacks.delete(handle)) return;
      const deadline = timedOut ? 0 : timeout > 0 ? requested + timeout : input.preciseNow() + 50;
      callback(domAPI.idleDeadline(timedOut, () => Math.max(0, Math.min(50, deadline - input.performanceNow()))));
    });
    return handle;
  });
  globalThis.cancelIdleCallback = nativeFunction('cancelIdleCallback', function cancelIdleCallback(handle) { idleCallbacks.delete(handle); });
  domAPI = input.installDOM(input.shape, input.profile, {
    markNative,
    brands: input.brands,
    interfaceParents: input.interfaceParents,
    isA: input.isA,
    brandCheck: input.brandCheck,
    exposed: input.exposed,
    storage: input.storage,
    realm: input.realm,
    topRealm: input.topRealm,
    trust: input.trust,
    setTimeout: globalThis.setTimeout,
    clearTimeout: globalThis.clearTimeout,
    queueTask: input.queueTask,
    incumbent: input.incumbent,
    kindOf: input.kindOf,
    transferBuffer: input.transferBuffer,
    bufferDetached: input.bufferDetached,
    throwLocation: input.throwLocation,
    callListener: input.callListener,
    digest: input.digest,
    encodingName: input.encodingName,
    decodeText: input.decodeText,
    parseURL: input.parseURL,
    updateURL: input.updateURL,
    randomUUID: input.randomUUID,
    randomBytes: input.randomBytes,
    templatedObject: input.templatedObject,
    setTemplateResolver: input.setTemplateResolver,
    performanceNow: input.performanceNow,
    timeOrigin: input.timeOrigin,
    createIframeWindow: input.createIframeWindow,
    loadImage: input.loadImage,
    resolveInterface: input.resolveInterface,
    orderKeys: input.orderKeys,
  });
  return domAPI;
})
`

// realmHost 是一个 Realm 的全局对象与宿主的任务派发、异常报告、全局键排序函数
type realmHost struct {
	name     string
	global   mozjs.Handle
	dispatch mozjs.Handle
	report   mozjs.Handle
	order    mozjs.Handle
}

// agent 保存一个 SpiderMonkey 上下文内各 Realm 共享的宿主数据
type agent struct {
	cx              *mozjs.Context
	loop            *eventLoop
	profile         Profile
	loadImage       func(string) bool
	pending         *sync.WaitGroup
	timezone        string
	navigationStart time.Time
	depth           int
	shared          mozjs.Handle
	track           mozjs.Handle
	notify          mozjs.Handle
	array           mozjs.Handle
	top             *realmHost
	realms          []*realmHost
}

// invoke 调用 JS 函数，最外层调用返回后执行微任务检查点
func (state *agent) invoke(fn, this mozjs.Handle, args ...mozjs.Value) (mozjs.Value, error) {
	state.depth++
	value, err := state.cx.Call(fn, this, args...)
	state.depth--
	if state.depth == 0 {
		state.cx.RunJobs()
	}
	return value, err
}

// evaluate 在 Realm 中执行脚本，最外层执行返回后执行微任务检查点
func (state *agent) evaluate(global mozjs.Handle, source, name string) (mozjs.Value, error) {
	state.depth++
	value, err := state.cx.Evaluate(global, source, name, 1)
	state.depth--
	if state.depth == 0 {
		state.cx.RunJobs()
	}
	return value, err
}

// report 把回调抛出的 JS 异常交给所属 Realm 的 reportException
func (state *agent) report(realm *realmHost, err error) {
	var thrown *mozjs.Error
	if !errors.As(err, &thrown) || thrown.Value == 0 {
		return
	}
	defer state.cx.Release(thrown.Value)
	state.cx.SetThrowLocation(thrown.Filename, thrown.Line, thrown.Column)
	if _, err := state.invoke(realm.report, 0, mozjs.Object(thrown.Value)); err != nil {
		state.release(err)
	}
}

// release 释放错误持有的异常值句柄
func (state *agent) release(err error) {
	var thrown *mozjs.Error
	if errors.As(err, &thrown) {
		state.cx.Release(thrown.Value)
	}
}

// dispatch 执行 Realm 中到期的宿主任务并报告其异常
func (state *agent) dispatch(realm *realmHost, id float64, argument mozjs.Value) {
	value, err := state.invoke(realm.dispatch, 0, mozjs.Number(id), argument)
	if err != nil {
		state.report(realm, err)
		return
	}
	state.cx.ReleaseValue(value)
}

// afterTask 在每个任务结束后为 Promise 拒绝派发 unhandledrejection 与 rejectionhandled
func (state *agent) afterTask() {
	defer state.cx.ClearKeptObjects()
	if state.track == 0 {
		return
	}
	state.depth++
	err := state.cx.FlushRejections(state.track)
	state.depth--
	if err != nil {
		state.release(err)
	}
	if value, err := state.invoke(state.notify, 0); err != nil {
		state.release(err)
	} else {
		state.cx.ReleaseValue(value)
	}
}

// newArray 在顶层 Realm 中以给定元素新建数组
func (state *agent) newArray(items ...mozjs.Value) (mozjs.Value, error) {
	state.depth++
	defer func() { state.depth-- }()
	return state.cx.Call(state.array, 0, items...)
}

// handleOf 取出值的句柄，非对象值返回错误
func handleOf(value mozjs.Value, err error) (mozjs.Handle, error) {
	if err != nil {
		return 0, err
	}
	if value.Kind != mozjs.KindHandle {
		return 0, errors.New("结果不是对象")
	}
	return value.Handle, nil
}

// installRealm 新建 Realm 全局对象并安装 Firefox 宿主，same 非 0 时与其共用 compartment
func (state *agent) installRealm(name string, same mozjs.Handle) (*realmHost, error) {
	cx := state.cx
	global, err := cx.NewGlobal(same, state.timezone)
	if err != nil {
		return nil, fmt.Errorf("新建 WAA %s Realm: %w", name, err)
	}
	realm := &realmHost{name: name, global: global}
	if err := cx.InstallWindowNatives(global); err != nil {
		return nil, err
	}
	installDOM, err := handleOf(state.evaluate(global, domJavaScript, hostScriptName))
	if err != nil {
		return nil, fmt.Errorf("编译 WAA DOM 宿主: %w", err)
	}
	defer cx.Release(installDOM)
	install, err := handleOf(state.evaluate(global, hostJavaScript, hostScriptName))
	if err != nil {
		return nil, fmt.Errorf("编译 WAA Realm 宿主: %w", err)
	}
	defer cx.Release(install)
	build, err := handleOf(state.evaluate(global, smHostJavaScript, hostScriptName))
	if err != nil {
		return nil, fmt.Errorf("编译 WAA 宿主接口: %w", err)
	}
	defer cx.Release(build)
	natives, err := cx.NewObject(global)
	if err != nil {
		return nil, err
	}
	defer cx.Release(natives)
	if err := cx.InstallHostNatives(natives); err != nil {
		return nil, err
	}
	for nativeName, native := range state.natives(realm) {
		fn, err := cx.NewFunction(global, nativeName, native.arity, native.call)
		if err != nil {
			return nil, err
		}
		err = cx.Set(natives, nativeName, mozjs.Object(fn))
		cx.Release(fn)
		if err != nil {
			return nil, err
		}
	}
	config, err := cx.NewObject(global)
	if err != nil {
		return nil, err
	}
	defer cx.Release(config)
	profileJSON, err := json.Marshal(state.realmProfile(name))
	if err != nil {
		return nil, err
	}
	realmStarted := time.Now()
	if name == "top" && !state.navigationStart.IsZero() {
		realmStarted = state.navigationStart
	}
	fields := map[string]mozjs.Value{
		"realm": mozjs.String(name), "trustedTypes": mozjs.Bool(state.profile.TrustedTypes), "profileJSON": mozjs.String(string(profileJSON)),
		"timeOrigin": mozjs.Number(float64(realmStarted.UnixMilli())), "started": mozjs.Number(mozjs.Now() - float64(time.Since(realmStarted).Microseconds())/1000),
	}
	if state.shared == 0 {
		shapeJSON, err := firefoxShapeJSON()
		if err != nil {
			return nil, fmt.Errorf("读取 Firefox 形状表: %w", err)
		}
		fields["shapeJSON"] = mozjs.String(closureListenerMapPattern.ReplaceAllLiteralString(shapeJSON, "closure_lm_"+strconv.Itoa(rand.IntN(1000000))))
	}
	for key, value := range fields {
		if err := cx.Set(config, key, value); err != nil {
			return nil, err
		}
	}
	shared := mozjs.Undefined()
	if state.shared != 0 {
		shared = mozjs.Object(state.shared)
	}
	result, err := handleOf(state.invoke(build, 0, mozjs.Object(natives), mozjs.Object(installDOM), mozjs.Object(install), mozjs.Object(config), shared))
	if err != nil {
		return nil, fmt.Errorf("安装 WAA %s 宿主: %w", name, err)
	}
	defer cx.Release(result)
	if realm.dispatch, err = handleOf(cx.Get(result, "dispatch")); err != nil {
		return nil, err
	}
	if realm.report, err = handleOf(cx.Get(result, "report")); err != nil {
		return nil, err
	}
	if realm.order, err = handleOf(cx.Get(result, "orderGlobal")); err != nil {
		return nil, err
	}
	if state.shared == 0 {
		if err := state.installAgent(result); err != nil {
			return nil, err
		}
	}
	state.realms = append(state.realms, realm)
	return realm, nil
}

// installAgent 保存顶层宿主返回的 agent 共享状态、Promise 拒绝回调、微任务异常报告函数与 TrustedScript 登记表
func (state *agent) installAgent(result mozjs.Handle) error {
	cx := state.cx
	var err error
	if state.shared, err = handleOf(cx.Get(result, "shared")); err != nil {
		return err
	}
	if state.track, err = handleOf(cx.Get(result, "track")); err != nil {
		return err
	}
	if state.notify, err = handleOf(cx.Get(result, "notify")); err != nil {
		return err
	}
	reporter, err := handleOf(cx.Get(result, "reportMicrotask"))
	if err != nil {
		return err
	}
	cx.SetMicrotaskReporter(reporter)
	registry, err := handleOf(cx.Get(state.shared, "registry"))
	if err != nil {
		return err
	}
	trusted, err := handleOf(cx.Get(registry, "trusted"))
	cx.Release(registry)
	if err != nil {
		return err
	}
	cx.SetTrustedTypes(trusted, state.profile.TrustedTypes)
	return nil
}

// hostNative 是一个 Go 宿主原生函数与其形参个数
type hostNative struct {
	arity int
	call  mozjs.NativeFunc
}

// urlJSON 把 URL 记录编码为 JSON，记录为空时返回 null
func urlJSON(record map[string]any) mozjs.Value {
	if record == nil {
		return mozjs.Null()
	}
	data, _ := json.Marshal(record)
	return mozjs.String(string(data))
}

// natives 返回 Realm 宿主使用的 Go 原生函数
func (state *agent) natives(realm *realmHost) map[string]hostNative {
	natives := map[string]hostNative{
		"schedule": {2, func(call *mozjs.Call) error {
			delay, id := call.ArgNumber(0), call.ArgNumber(1)
			wait := time.Duration(delay * float64(time.Millisecond))
			if delay != delay || wait < 0 {
				wait = 0
			}
			state.loop.schedule(wait, func() { state.dispatch(realm, id, mozjs.Undefined()) })
			return nil
		}},
		"digest": {2, func(call *mozjs.Call) error {
			data, _ := call.ArgBytes(1)
			call.ReturnBytes(digestBytes(call.ArgString(0), data))
			return nil
		}},
		"encodingName": {1, func(call *mozjs.Call) error {
			call.Return(mozjs.String(encodingName(call.ArgString(0))))
			return nil
		}},
		"decodeText": {3, func(call *mozjs.Call) error {
			data, _ := call.ArgBytes(1)
			text, err := decodeText(call.ArgString(0), data, call.Arg(2).Bool)
			if err != nil {
				return &mozjs.ThrowError{Type: "TypeError", Message: "TextDecoder.decode: Decoding failed."}
			}
			call.Return(mozjs.String(text))
			return nil
		}},
		"parseURL": {2, func(call *mozjs.Call) error {
			var base *string
			if value := call.Arg(1); value.Kind == mozjs.KindString {
				base = &value.String
			}
			call.Return(urlJSON(parseURL(call.ArgString(0), base)))
			return nil
		}},
		"updateURL": {3, func(call *mozjs.Call) error {
			call.Return(urlJSON(updateURL(call.ArgString(0), call.ArgString(1), call.ArgString(2))))
			return nil
		}},
		"randomUUID": {0, func(call *mozjs.Call) error {
			call.Return(mozjs.String(randomUUID()))
			return nil
		}},
		"randomBytes": {1, func(call *mozjs.Call) error {
			data := make([]byte, max(0, int(call.ArgNumber(0))))
			_, _ = cryptorand.Read(data)
			call.ReturnBytes(data)
			return nil
		}},
	}
	if state.loadImage != nil {
		natives["loadImage"] = hostNative{2, func(call *mozjs.Call) error {
			address, id := call.ArgString(0), call.ArgNumber(1)
			state.pending.Add(1)
			go func() {
				defer state.pending.Done()
				loaded := state.loadImage(address)
				state.loop.schedule(0, func() { state.dispatch(realm, id, mozjs.Bool(loaded)) })
			}()
			return nil
		}}
	}
	if realm.name == "top" {
		natives["createIframeWindow"] = hostNative{0, func(call *mozjs.Call) error {
			frame, err := state.installRealm("frame", state.top.global)
			if err != nil {
				return err
			}
			call.Return(mozjs.Object(frame.global))
			return nil
		}}
	}
	return natives
}

// realmProfile 生成指定 Realm 的地址、窗口、导航器、屏幕与文档现场值
func (state *agent) realmProfile(realm string) map[string]any {
	profile := state.profile
	values := map[string]any{}
	if len(profile.Navigator) > 0 {
		values["navigator"] = profile.Navigator
	}
	if len(profile.Screen) > 0 {
		values["screen"] = profile.Screen
	}
	window := map[string]any{}
	if realm == "top" {
		for name, value := range profile.Window {
			window[name] = value
		}
		if len(profile.Document) > 0 {
			values["document"] = profile.Document
		}
		if width, ok := profile.Window["innerWidth"]; ok {
			values["visualViewport"] = map[string]any{"width": width, "height": profile.Window["innerHeight"]}
		}
	} else {
		for _, name := range frameWindowNames {
			if value, ok := profile.Window[name]; ok {
				window[name] = value
			}
		}
		document := map[string]any{}
		for _, name := range []string{"cookie", "lastModified"} {
			if value, ok := profile.Document[name]; ok {
				document[name] = value
			}
		}
		if len(document) > 0 {
			values["document"] = document
		}
	}
	result := map[string]any{"hasFocus": profile.HasFocus, "window": window, "values": values}
	if realm == "top" && len(profile.Globals) > 0 {
		result["globals"] = profile.Globals
	}
	if len(profile.LocalStorage) > 0 {
		localStorage := make(map[string]any, len(profile.LocalStorage))
		for key, value := range profile.LocalStorage {
			localStorage[key] = value
		}
		result["localStorage"] = localStorage
	}
	if realm == "top" && profile.PageURL != "" {
		result["location"] = locationProfile(profile.PageURL)
	}
	return result
}

// locationProfile 拆分页面地址为 Location 各字段
func locationProfile(raw string) map[string]any {
	protocol, rest, _ := strings.Cut(raw, "//")
	host, path := rest, "/"
	if index := strings.IndexByte(rest, '/'); index >= 0 {
		host, path = rest[:index], rest[index:]
	}
	search, hash := "", ""
	if index := strings.IndexByte(path, '#'); index >= 0 {
		path, hash = path[:index], path[index:]
	}
	if index := strings.IndexByte(path, '?'); index >= 0 {
		path, search = path[:index], path[index:]
	}
	return map[string]any{
		"href": raw, "origin": protocol + "//" + host, "protocol": protocol, "host": host, "hostname": host,
		"port": "", "pathname": path, "search": search, "hash": hash,
	}
}
