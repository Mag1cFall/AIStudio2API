package waa

import (
	cryptorand "crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
)

// maxCallStackDepth 为 JavaScript 调用栈深度上限，超过时抛出 InternalError
const maxCallStackDepth = 20000

// hostScriptName 是不出现在 Error.stack 中的宿主脚本来源名
const hostScriptName = "\x00waa-host"

// interfaceTemplates 缓存进程内共享的接口原型模板
var interfaceTemplates sync.Map

// interfaceTemplate 返回 id 接口原型的成员模板，keys 为成员键的顺序
func interfaceTemplate(id string, keys []string) *goja.ObjectTemplate {
	if cached, ok := interfaceTemplates.Load(id); ok {
		return cached.(*goja.ObjectTemplate)
	}
	actual, _ := interfaceTemplates.LoadOrStore(id, goja.NewObjectTemplate(id, keys))
	return actual.(*goja.ObjectTemplate)
}

// runHostScript 把 source 按宿主脚本编译并在 vm 中执行
func runHostScript(vm *goja.Runtime, source string) (goja.Value, error) {
	program, err := goja.CompileHost(hostScriptName, source, false)
	if err != nil {
		return nil, err
	}
	return vm.RunProgram(program)
}

// hostRegistry 保存同一 agent 内各 Realm 共享的原生函数源码与 TrustedScript 源码
type hostRegistry struct {
	trusted map[*goja.Object]string
	native  map[*goja.Object]string
}

func newHostRegistry() *hostRegistry {
	return &hostRegistry{trusted: make(map[*goja.Object]string), native: make(map[*goja.Object]string)}
}

// evalTransformer 将 TrustedScript 解包为源码，blockStrings 时按页面 CSP 拒绝字符串 eval
func (registry *hostRegistry) evalTransformer(vm *goja.Runtime, blockStrings bool) func(goja.Value) goja.Value {
	return func(argument goja.Value) goja.Value {
		if object, ok := argument.(*goja.Object); ok {
			if source, trusted := registry.trusted[object]; trusted {
				return vm.ToValue(source)
			}
			return argument
		}
		if _, isString := argument.(goja.String); isString && blockStrings {
			throwError(vm, "EvalError", "call to eval() blocked by CSP")
		}
		return argument
	}
}

// functionTransformer 在 blockStrings 时按页面 CSP 拒绝以字符串创建函数，参数全为 TrustedScript 时解包为源码
func (registry *hostRegistry) functionTransformer(vm *goja.Runtime, blockStrings bool) func([]goja.Value) []goja.Value {
	return func(arguments []goja.Value) []goja.Value {
		if !blockStrings || len(arguments) == 0 {
			return arguments
		}
		sources := make([]goja.Value, len(arguments))
		for index, argument := range arguments {
			object, _ := argument.(*goja.Object)
			source, trusted := registry.trusted[object]
			if !trusted {
				throwError(vm, "EvalError", "call to Function() blocked by CSP")
			}
			sources[index] = vm.ToValue(source)
		}
		return sources
	}
}

// throwError 抛出当前 Realm 中指定名称的内建错误
func throwError(vm *goja.Runtime, name, message string) {
	constructor, _ := goja.AssertConstructor(vm.Get(name))
	value, err := constructor(nil, vm.ToValue(message))
	if err != nil {
		panic(err)
	}
	panic(value)
}

// clampedTimerNesting 是 Firefox 开始把 0 至 4 ms 定时器钳制为 4 ms 的嵌套层数
const clampedTimerNesting = 4

// realmTimers 实现单个 Realm 的 setTimeout 与 setInterval；nesting 为正在执行的定时器的嵌套层数
type realmTimers struct {
	vm           *goja.Runtime
	schedule     func(time.Duration, func())
	nextID       int64
	active       map[int64]bool
	registry     *hostRegistry
	trustedTypes bool
	report       func(error)
	nesting      int
}

func (timers *realmTimers) start(call goja.FunctionCall, repeat bool) goja.Value {
	handler := call.Argument(0)
	callback, callable := goja.AssertFunction(handler)
	source := ""
	if !callable {
		object, _ := handler.(*goja.Object)
		text, trusted := timers.registry.trusted[object]
		if !trusted && timers.trustedTypes {
			name := "setTimeout"
			if repeat {
				name = "setInterval"
			}
			panic(timers.vm.NewTypeError("Window." + name + ": Sink type mismatch violation blocked by CSP"))
		}
		if !trusted {
			text = handler.String()
		}
		source = text
	}
	timers.nextID++
	timerID := timers.nextID
	timers.active[timerID] = true
	delay := time.Duration(call.Argument(1).ToInteger()) * time.Millisecond
	if delay < 0 {
		delay = 0
	}
	if (repeat || timers.nesting >= clampedTimerNesting) && delay < 4*time.Millisecond {
		delay = 4 * time.Millisecond
	}
	level := timers.nesting + 1
	arguments := append([]goja.Value(nil), call.Arguments[min(2, len(call.Arguments)):]...)
	var fire func()
	fire = func() {
		if !timers.active[timerID] {
			return
		}
		if repeat {
			timers.schedule(delay, fire)
		} else {
			delete(timers.active, timerID)
		}
		previous := timers.nesting
		timers.nesting = level
		var err error
		if callable {
			_, err = callback(timers.vm.GlobalObject(), arguments...)
		} else {
			_, err = timers.vm.RunString(source)
		}
		timers.nesting = previous
		timers.report(err)
	}
	timers.schedule(delay, fire)
	return timers.vm.ToValue(timerID)
}

// newIframeWindow 创建与父页面共享堆和微任务队列的同源 iframe Realm
func newIframeWindow(parent *goja.Runtime, state *hostState) (*goja.Object, error) {
	child := parent.NewRealm()
	child.SetMaxCallStackSize(maxCallStackDepth)
	source, err := newFirefoxRandSource()
	if err != nil {
		return nil, err
	}
	child.SetRandSource(source)
	if _, err := state.installRealmHost(child, "frame", nil); err != nil {
		return nil, err
	}
	child.SetEvalTransformer(state.registry.evalTransformer(child, state.profile.TrustedTypes))
	child.SetFunctionTransformer(state.registry.functionTransformer(child, state.profile.TrustedTypes))
	return child.GlobalObject(), nil
}

// newFirefoxRandSource 返回 SpiderMonkey XorShift128+ 算法的 Math.random 源
func newFirefoxRandSource() (goja.RandSource, error) {
	var seed [16]byte
	if _, err := cryptorand.Read(seed[:]); err != nil {
		return nil, err
	}
	state0 := binary.LittleEndian.Uint64(seed[:8])
	state1 := binary.LittleEndian.Uint64(seed[8:])
	if state0 == 0 && state1 == 0 {
		state1 = 1
	}
	return func() float64 {
		s1 := state0
		s0 := state1
		state0 = s0
		s1 ^= s1 << 23
		state1 = s1 ^ s0 ^ (s1 >> 17) ^ (s0 >> 26)
		return float64((state1+s0)&((uint64(1)<<53)-1)) / float64(uint64(1)<<53)
	}, nil
}

// installBinaryEncoding 安装与 Firefox 一致的 atob 与 btoa
func installBinaryEncoding(vm *goja.Runtime) error {
	invalidCharacter := func() {
		constructor, _ := goja.AssertConstructor(vm.Get("DOMException"))
		value, err := constructor(nil, vm.ToValue("String contains an invalid character"), vm.ToValue("InvalidCharacterError"))
		if err != nil {
			panic(err)
		}
		panic(value)
	}
	if err := vm.Set("atob", func(value string) string {
		value = strings.Map(func(character rune) rune {
			switch character {
			case ' ', '\t', '\n', '\f', '\r':
				return -1
			default:
				return character
			}
		}, value)
		encoding := base64.StdEncoding
		if len(value)%4 == 2 || len(value)%4 == 3 {
			encoding = base64.RawStdEncoding
		}
		decoded, err := encoding.DecodeString(value)
		if err != nil {
			invalidCharacter()
		}
		characters := make([]rune, len(decoded))
		for index, current := range decoded {
			characters[index] = rune(current)
		}
		return string(characters)
	}); err != nil {
		return err
	}
	return vm.Set("btoa", func(value string) string {
		bytes := make([]byte, 0, len(value))
		for _, current := range value {
			if current > 255 {
				invalidCharacter()
			}
			bytes = append(bytes, byte(current))
		}
		return base64.StdEncoding.EncodeToString(bytes)
	})
}

// installQueueMicrotask 安装共享 agent 微任务队列的 queueMicrotask，回调抛出的异常经 report 报告
func installQueueMicrotask(vm *goja.Runtime, report func(error)) error {
	return vm.Set("queueMicrotask", func(call goja.FunctionCall) goja.Value {
		callback, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			panic(vm.NewTypeError("Window.queueMicrotask: Argument 1 is not callable."))
		}
		vm.QueueMicrotask(func() {
			_, err := callback(goja.Undefined())
			report(err)
		})
		return goja.Undefined()
	})
}

// rejectionQueue 收集任务内被拒绝且没有处理函数的 Promise，在任务结束后通知所属 Realm
type rejectionQueue struct {
	pending  []rejection
	handled  []rejection
	notified map[*goja.Promise]rejection
}

// rejection 是一个被拒绝的 Promise 与其所属 Realm 的通知函数
type rejection struct {
	promise *goja.Promise
	vm      *goja.Runtime
	notify  goja.Callable
}

// tracker 返回 Realm 的 Promise 拒绝跟踪函数
func (queue *rejectionQueue) tracker(vm *goja.Runtime, notify goja.Callable) goja.PromiseRejectionTracker {
	return func(promise *goja.Promise, operation goja.PromiseRejectionOperation) {
		if operation == goja.PromiseRejectionReject {
			queue.pending = append(queue.pending, rejection{promise: promise, vm: vm, notify: notify})
			return
		}
		if index := slices.IndexFunc(queue.pending, func(item rejection) bool { return item.promise == promise }); index >= 0 {
			queue.pending = slices.Delete(queue.pending, index, index+1)
			return
		}
		if item, ok := queue.notified[promise]; ok {
			delete(queue.notified, promise)
			queue.handled = append(queue.handled, item)
		}
	}
}

// notify 为仍未处理的拒绝派发 unhandledrejection，为通知后才处理的拒绝派发 rejectionhandled
func (queue *rejectionQueue) notify() {
	for len(queue.pending) > 0 || len(queue.handled) > 0 {
		pending, handled := queue.pending, queue.handled
		queue.pending, queue.handled = nil, nil
		for _, item := range pending {
			if queue.notified == nil {
				queue.notified = make(map[*goja.Promise]rejection)
			}
			queue.notified[item.promise] = item
			_, _ = item.notify(goja.Undefined(), item.vm.ToValue("unhandledrejection"), item.vm.ToValue(item.promise), item.promise.Result())
		}
		for _, item := range handled {
			_, _ = item.notify(goja.Undefined(), item.vm.ToValue("rejectionhandled"), item.vm.ToValue(item.promise), item.promise.Result())
		}
	}
}

// transferArrayBuffer 实现 ArrayBuffer.prototype.transfer：把内容复制到指定长度的新 ArrayBuffer 并分离原对象
func transferArrayBuffer(vm *goja.Runtime, receiver, length goja.Value) goja.Value {
	buffer, ok := receiver.Export().(goja.ArrayBuffer)
	if !ok {
		panic(vm.NewTypeError("transfer method called on incompatible " + incompatibleName(receiver)))
	}
	if buffer.Detached() {
		panic(vm.NewTypeError("attempting to access detached ArrayBuffer"))
	}
	data := buffer.Bytes()
	size := len(data)
	if !goja.IsUndefined(length) {
		requested := length.ToInteger()
		if requested < 0 {
			throwError(vm, "RangeError", "invalid or out-of-range index")
		}
		size = int(requested)
	}
	copied := make([]byte, size)
	copy(copied, data)
	buffer.Detach()
	return vm.ToValue(vm.NewArrayBuffer(copied))
}

// incompatibleName 返回 SpiderMonkey 报错文案中不兼容接收者的名称：对象为类名，其余为 typeof 结果
func incompatibleName(value goja.Value) string {
	if object, ok := value.(*goja.Object); ok {
		return object.ClassName()
	}
	if goja.IsUndefined(value) || goja.IsNull(value) {
		return value.String()
	}
	return value.ExportType().Kind().String()
}

// digestBytes 按 SubtleCrypto.digest 的算法名计算摘要
func digestBytes(name string, data []byte) []byte {
	switch name {
	case "SHA-1":
		sum := sha1.Sum(data)
		return sum[:]
	case "SHA-384":
		sum := sha512.Sum384(data)
		return sum[:]
	case "SHA-512":
		sum := sha512.Sum512(data)
		return sum[:]
	default:
		sum := sha256.Sum256(data)
		return sum[:]
	}
}

// encodingName 按 WHATWG Encoding 标准把标签解析为编码名，不支持的标签返回空串
func encodingName(label string) string {
	encoding, err := htmlindex.Get(strings.TrimSpace(label))
	if err != nil {
		return ""
	}
	name, err := htmlindex.Name(encoding)
	if err != nil {
		return ""
	}
	return name
}

// decodeText 以 WHATWG 编码解码字节，fatal 时遇到无效序列返回错误
func decodeText(name string, data []byte, fatal bool) (string, error) {
	encoder, err := htmlindex.Get(name)
	if err != nil {
		return "", err
	}
	decoded, err := encoder.NewDecoder().Bytes(data)
	if err != nil {
		return "", err
	}
	if fatal && strings.ContainsRune(string(decoded), '�') && !strings.ContainsRune(string(data), '�') {
		return "", encoding.ErrInvalidUTF8
	}
	return string(decoded), nil
}
