package waa

import (
	"bytes"
	"compress/gzip"
	cryptorand "crypto/rand"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/goja"
)

//go:embed dom.js
var domJavaScript string

//go:embed firefox152.json.gz
var firefoxShapeGzip []byte

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

// hostState 保存同一 agent 内各 Realm 共享的宿主数据
type hostState struct {
	registry   *hostRegistry
	rejections *rejectionQueue
	profile    Profile
	loadImage  func(string) bool
	pending    *sync.WaitGroup
	schedule   func(time.Duration, func())
	location   *time.Location
	zoneName   func(time.Time) string
	shape      goja.Value
	order      *globalOrderShape
	brands     goja.Value
	storage    *goja.Object
	topAPI     goja.Value
}

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
    const source = originalFunctionToString.call(this);
    if (!source.includes('[native code]') || source.startsWith('function() {')) return source;
    const name = typeof this.name === 'string' ? this.name : '';
    return 'function ' + name + '() {\n    [native code]\n}';
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
    intl: input.intl,
    intlHost: input.intlHost,
  });
  return domAPI;
})
`

// interfaceTable 记录一个 Realm 中每个 WebIDL 接口自身与沿父接口的全部祖先
type interfaceTable map[string]map[string]bool

// load 按宿主脚本的父接口表生成每个接口的祖先集合
func (table interfaceTable) load(parents *goja.Object) {
	for _, name := range parents.Keys() {
		ancestors := map[string]bool{name: true}
		for parent := parents.Get(name); parent != nil && parent.ToBoolean(); parent = parents.Get(parent.String()) {
			ancestors[parent.String()] = true
		}
		table[name] = ancestors
	}
}

// isA 判断 iface 是否为 target 或继承自 target
func (table interfaceTable) isA(iface, target string) bool {
	if ancestors, ok := table[iface]; ok {
		return ancestors[target]
	}
	return iface == target
}

// installRealmHost 在指定 Realm 安装宿主并返回该 Realm 的 DOM 接口
func (state *hostState) installRealmHost(vm *goja.Runtime, realm string, createIframe func() *goja.Object) (goja.Value, error) {
	if err := installBinaryEncoding(vm); err != nil {
		return nil, err
	}
	var report goja.Callable
	reportError := func(err error) {
		var exception *goja.Exception
		if report != nil && errors.As(err, &exception) {
			_, _ = report(goja.Undefined(), exception.Value())
		}
	}
	if err := installQueueMicrotask(vm, reportError); err != nil {
		return nil, err
	}
	installDOM, err := vm.RunProgram(domProgram())
	if err != nil {
		return nil, fmt.Errorf("编译 WAA DOM 宿主: %w", err)
	}
	hostFunction, err := vm.RunProgram(hostProgram())
	if err != nil {
		return nil, fmt.Errorf("编译 WAA Realm 宿主: %w", err)
	}
	install, ok := goja.AssertFunction(hostFunction)
	if !ok {
		return nil, errors.New("WAA Realm 宿主不是函数")
	}
	if state.shape == nil {
		shapeJSON, err := firefoxShapeJSON()
		if err != nil {
			return nil, fmt.Errorf("读取 Firefox 形状表: %w", err)
		}
		shapeJSON = closureListenerMapPattern.ReplaceAllLiteralString(shapeJSON, "closure_lm_"+strconv.Itoa(rand.IntN(1000000)))
		parse, _ := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("parse"))
		state.shape, err = parse(goja.Undefined(), vm.ToValue(shapeJSON))
		if err != nil {
			return nil, fmt.Errorf("解析 Firefox 形状表: %w", err)
		}
		state.order, err = parseGlobalOrderShape(shapeJSON)
		if err != nil {
			return nil, fmt.Errorf("解析 Firefox 全局键顺序: %w", err)
		}
		weakMap, _ := goja.AssertConstructor(vm.Get("WeakMap"))
		state.brands, err = weakMap(nil)
		if err != nil {
			return nil, err
		}
		state.storage = vm.NewObject()
	}
	timers := &realmTimers{vm: vm, schedule: state.schedule, active: make(map[int64]bool), registry: state.registry, trustedTypes: state.profile.TrustedTypes, report: reportError}
	timerObject := vm.NewObject()
	_ = timerObject.Set("setTimeout", func(call goja.FunctionCall) goja.Value { return timers.start(call, false) })
	_ = timerObject.Set("setInterval", func(call goja.FunctionCall) goja.Value { return timers.start(call, true) })
	_ = timerObject.Set("clear", func(call goja.FunctionCall) goja.Value {
		delete(timers.active, call.Argument(0).ToInteger())
		return goja.Undefined()
	})
	input := vm.NewObject()
	_ = input.Set("markNative", func(call goja.FunctionCall) goja.Value {
		if object, ok := call.Argument(0).(*goja.Object); ok {
			state.registry.native[object] = call.Argument(1).String()
		}
		return call.Argument(0)
	})
	_ = input.Set("nativeSource", func(call goja.FunctionCall) goja.Value {
		if object, ok := call.Argument(0).(*goja.Object); ok {
			if source, exists := state.registry.native[object]; exists {
				return vm.ToValue(source)
			}
		}
		return goja.Undefined()
	})
	_ = input.Set("trust", func(call goja.FunctionCall) goja.Value {
		if object, ok := call.Argument(0).(*goja.Object); ok {
			state.registry.trusted[object] = call.Argument(1).String()
		}
		return goja.Undefined()
	})
	intlHost, err := state.installIntlHost(vm)
	if err != nil {
		return nil, err
	}
	installIntl, err := vm.RunProgram(intlInstaller)
	if err != nil {
		return nil, fmt.Errorf("编译 WAA Intl 宿主: %w", err)
	}
	_ = input.Set("intl", installIntl)
	_ = input.Set("intlHost", intlHost)
	_ = input.Set("timers", timerObject)
	_ = input.Set("installDOM", installDOM)
	_ = input.Set("shape", state.shape)
	_ = input.Set("brands", state.brands)
	brands := state.brands.(*goja.Object)
	interfaces := interfaceTable{}
	_ = input.Set("interfaceParents", func(call goja.FunctionCall) goja.Value {
		interfaces.load(call.Argument(0).ToObject(vm))
		return goja.Undefined()
	})
	_ = input.Set("isA", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(interfaces.isA(call.Argument(0).String(), call.Argument(1).String()))
	})
	_ = input.Set("brandCheck", func(call goja.FunctionCall) goja.Value {
		self := call.Argument(0)
		if goja.IsUndefined(self) || goja.IsNull(self) {
			self = vm.GlobalObject()
		}
		if object, ok := self.(*goja.Object); ok {
			if brand, ok := brands.WeakMapGet(object).(*goja.Object); ok {
				if iface := brand.Get("iface"); iface != nil && interfaces.isA(iface.String(), call.Argument(1).String()) {
					return brand
				}
			}
		}
		return goja.Null()
	})
	_ = input.Set("storage", state.storage)
	_ = input.Set("realm", realm)
	_ = input.Set("profile", state.realmProfile(realm))
	vm.SetTimeLocation(state.location)
	if state.zoneName != nil {
		vm.SetTimeZoneName(state.zoneName)
	}
	realmStarted := time.Now()
	if realm == "top" && !state.profile.NavigationStart.IsZero() {
		realmStarted = state.profile.NavigationStart
	}
	_ = input.Set("timeOrigin", float64(realmStarted.UnixMilli()))
	_ = input.Set("performanceNow", func(goja.FunctionCall) goja.Value {
		return vm.ToValue(math.Floor(float64(time.Since(realmStarted)) / float64(time.Millisecond)))
	})
	_ = input.Set("preciseNow", func(goja.FunctionCall) goja.Value {
		return vm.ToValue(float64(time.Since(realmStarted).Microseconds()) / 1000)
	})
	_ = input.Set("queueTask", func(delay float64, callback goja.Callable) {
		state.schedule(time.Duration(max(delay, 0)*float64(time.Millisecond)), func() {
			_, err := callback(goja.Undefined())
			reportError(err)
		})
	})
	_ = input.Set("incumbent", func() goja.Value {
		return vm.IncumbentGlobal(func(name string) bool { return strings.HasPrefix(name, hostScriptName) })
	})
	_ = input.Set("kindOf", func(call goja.FunctionCall) goja.Value {
		if object, ok := call.Argument(0).(*goja.Object); ok {
			return vm.ToValue(object.InternalKind())
		}
		return vm.ToValue("")
	})
	_ = input.Set("transferBuffer", func(call goja.FunctionCall) goja.Value {
		return transferArrayBuffer(vm, call.Argument(0), call.Argument(1))
	})
	_ = input.Set("bufferDetached", func(call goja.FunctionCall) goja.Value {
		buffer, ok := call.Argument(0).Export().(goja.ArrayBuffer)
		if !ok {
			panic(vm.NewTypeError("get detached method called on incompatible " + incompatibleName(call.Argument(0))))
		}
		return vm.ToValue(buffer.Detached())
	})
	_ = input.Set("throwLocation", func() []any {
		file, line, column := vm.LastThrowPosition()
		return []any{file, line, column}
	})
	_ = input.Set("digest", func(name string, data goja.ArrayBuffer) goja.Value {
		return vm.ToValue(vm.NewArrayBuffer(digestBytes(name, data.Bytes())))
	})
	_ = input.Set("encodingName", encodingName)
	_ = input.Set("templatedObject", func(id string, keys []string, proto *goja.Object) *goja.Object {
		return vm.NewTemplatedObject(interfaceTemplate(id, keys), proto)
	})
	_ = input.Set("setTemplateResolver", func(resolve goja.Callable) {
		vm.SetTemplateResolver(func(id string, index int) goja.TemplateProperty {
			result, err := resolve(goja.Undefined(), vm.ToValue(id), vm.ToValue(index))
			if err != nil {
				panic(err)
			}
			item := result.ToObject(vm)
			property := goja.TemplateProperty{
				Accessor: item.Get("0").ToBoolean(), Value: item.Get("1"),
				Writable: item.Get("4").ToBoolean(), Enumerable: item.Get("5").ToBoolean(), Configurable: item.Get("6").ToBoolean(),
			}
			property.Getter, _ = item.Get("2").(*goja.Object)
			property.Setter, _ = item.Get("3").(*goja.Object)
			return property
		})
	})
	_ = input.Set("parseURL", func(call goja.FunctionCall) goja.Value {
		record := parseURL(call.Argument(0).String(), call.Argument(1))
		if record == nil {
			return goja.Null()
		}
		return vm.ToValue(record)
	})
	_ = input.Set("updateURL", func(href, field, value string) goja.Value {
		record := updateURL(href, field, value)
		if record == nil {
			return goja.Null()
		}
		return vm.ToValue(record)
	})
	_ = input.Set("randomUUID", randomUUID)
	_ = input.Set("randomBytes", func(size int) goja.ArrayBuffer {
		data := make([]byte, size)
		_, _ = cryptorand.Read(data)
		return vm.NewArrayBuffer(data)
	})
	_ = input.Set("decodeText", func(name string, data goja.ArrayBuffer, fatal bool) string {
		text, err := decodeText(name, data.Bytes(), fatal)
		if err != nil {
			panic(vm.NewTypeError("TextDecoder.decode: Decoding failed."))
		}
		return text
	})
	if state.topAPI != nil {
		_ = input.Set("topRealm", state.topAPI)
	}
	if createIframe != nil {
		_ = input.Set("createIframeWindow", createIframe)
	}
	if state.loadImage != nil {
		_ = input.Set("loadImage", func(address string, callback goja.Callable) {
			state.pending.Add(1)
			go func() {
				defer state.pending.Done()
				loaded := state.loadImage(address)
				state.schedule(0, func() { _, _ = callback(goja.Undefined(), vm.ToValue(loaded)) })
			}()
		})
	}
	var order *firefoxGlobalOrder
	_ = input.Set("exposed", func(call goja.FunctionCall) goja.Value {
		value := call.Argument(0)
		if object, ok := value.(*goja.Object); ok && order != nil {
			if brand, ok := brands.WeakMapGet(object).(*goja.Object); ok {
				order.Resolve(brand.Get("iface").String())
			}
		}
		return value
	})
	_ = input.Set("resolveInterface", func(call goja.FunctionCall) goja.Value {
		if order != nil {
			order.Resolve(call.Argument(0).String())
		}
		return goja.Undefined()
	})
	_ = input.Set("orderKeys", func(object *goja.Object, names []string) {
		object.OrderOwnKeys(names)
	})
	api, err := install(goja.Undefined(), input)
	if err != nil {
		return nil, fmt.Errorf("安装 WAA %s 宿主: %w", realm, err)
	}
	report, _ = goja.AssertFunction(api.ToObject(vm).Get("reportException"))
	notify, _ := goja.AssertFunction(api.ToObject(vm).Get("reportRejection"))
	vm.SetPromiseRejectionTracker(state.rejections.tracker(vm, notify))
	if realm == "top" {
		order = newTopGlobalOrder(state.order)
	} else {
		order = newFrameGlobalOrder(state.order)
	}
	vm.SetGlobalObserver(order)
	return api, nil
}

// realmProfile 生成指定 Realm 的地址、窗口、导航器、屏幕与文档现场值
func (state *hostState) realmProfile(realm string) map[string]any {
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
