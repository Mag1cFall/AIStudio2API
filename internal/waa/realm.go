package waa

import (
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja"
)

// maxCallStackDepth 为 JavaScript 调用栈深度上限，超过时抛出 InternalError
const maxCallStackDepth = 20000

// hostScriptName 是不出现在 Error.stack 中的宿主脚本来源名
const hostScriptName = "\x00waa-host"

// hostRegistry 保存同一 agent 内各 Realm 共享的原生函数源码与可信类型标记
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
			constructor, _ := goja.AssertConstructor(vm.Get("EvalError"))
			value, err := constructor(nil, vm.ToValue("call to eval() blocked by CSP"))
			if err != nil {
				panic(err)
			}
			panic(value)
		}
		return argument
	}
}

// realmTimers 实现单个 Realm 的 setTimeout 与 setInterval
type realmTimers struct {
	vm       *goja.Runtime
	schedule func(time.Duration, func())
	nextID   int64
	active   map[int64]bool
}

func (timers *realmTimers) start(call goja.FunctionCall, repeat bool) goja.Value {
	callback, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		return timers.vm.ToValue(0)
	}
	timers.nextID++
	timerID := timers.nextID
	timers.active[timerID] = true
	delay := time.Duration(call.Argument(1).ToInteger()) * time.Millisecond
	if delay < 0 {
		delay = 0
	}
	if repeat && delay < 4*time.Millisecond {
		delay = 4 * time.Millisecond
	}
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
		_, _ = callback(timers.vm.GlobalObject(), arguments...)
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
	child.SetEvalTransformer(state.registry.evalTransformer(child, false))
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

// installQueueMicrotask 安装共享 agent 微任务队列的 queueMicrotask
func installQueueMicrotask(vm *goja.Runtime) error {
	return vm.Set("queueMicrotask", func(call goja.FunctionCall) goja.Value {
		callback, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			panic(vm.NewTypeError("Window.queueMicrotask: Argument 1 is not callable."))
		}
		vm.QueueMicrotask(func() {
			_, _ = callback(goja.Undefined())
		})
		return goja.Undefined()
	})
}
