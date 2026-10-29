// Package mozjs 经 cgo 嵌入 SpiderMonkey，提供线程绑定的上下文、根表句柄、求值、Go 原生函数、微任务与中断
package mozjs

/*
#cgo CXXFLAGS: -std=c++20 -fno-rtti -fno-exceptions -DSTATIC_JS_API -DJS_STANDALONE -Wno-invalid-offsetof -Wno-attributes
#cgo windows,amd64 CXXFLAGS: -DXP_WIN
#cgo windows,amd64 LDFLAGS: -ljs_static -ljsrust -Wl,-Bstatic -lc++ -lunwind -Wl,-Bdynamic -lws2_32 -lwinmm -lpsapi -ldbghelp -lntdll -luserenv -lbcrypt -ladvapi32 -lshell32 -lole32 -loleaut32 -luuid -lsynchronization
#cgo linux,amd64 CXXFLAGS: -DXP_UNIX -DXP_LINUX
#cgo linux,amd64 LDFLAGS: -ljs_static -ljsrust -Wl,-Bstatic -lstdc++ -Wl,-Bdynamic -lm -ldl -lpthread
#cgo linux,arm64 CXXFLAGS: -DXP_UNIX -DXP_LINUX
#cgo linux,arm64 LDFLAGS: -ljs_static -ljsrust -Wl,-Bstatic -lstdc++ -Wl,-Bdynamic -lm -ldl -lpthread
#cgo darwin,amd64 CXXFLAGS: -DXP_UNIX -DXP_DARWIN -DXP_MACOSX
#cgo darwin,amd64 LDFLAGS: -ljs_static -ljsrust -lc++ -framework CoreFoundation -framework Security
#cgo darwin,arm64 CXXFLAGS: -DXP_UNIX -DXP_DARWIN -DXP_MACOSX
#cgo darwin,arm64 LDFLAGS: -ljs_static -ljsrust -lc++ -framework CoreFoundation -framework Security
#include <stdlib.h>
#include "mozjs.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"
)

// Handle 是上下文根表中一个 JS 值的句柄，0 表示 undefined
type Handle uint32

// Kind 是 Value 的种类
type Kind int32

// Kind 的取值与 mozjs.h 的 MZ_* 一致
const (
	KindUndefined Kind = C.MZ_UNDEFINED
	KindNull      Kind = C.MZ_NULL
	KindBool      Kind = C.MZ_BOOL
	KindNumber    Kind = C.MZ_NUMBER
	KindString    Kind = C.MZ_STRING
	KindHandle    Kind = C.MZ_HANDLE
)

// Value 是跨边界的 JS 值：原始值按种类保存，其余值为句柄
type Value struct {
	Kind   Kind
	Bool   bool
	Number float64
	String string
	Handle Handle
}

// Undefined 返回 undefined
func Undefined() Value { return Value{} }

// Null 返回 null
func Null() Value { return Value{Kind: KindNull} }

// Bool 返回布尔值
func Bool(value bool) Value { return Value{Kind: KindBool, Bool: value} }

// Number 返回数值
func Number(value float64) Value { return Value{Kind: KindNumber, Number: value} }

// String 返回字符串
func String(value string) Value { return Value{Kind: KindString, String: value} }

// Object 返回句柄引用的值
func Object(handle Handle) Value { return Value{Kind: KindHandle, Handle: handle} }

// NativeFunc 是由 JS 调用的 Go 函数，返回错误时在 JS 中抛出
type NativeFunc func(call *Call) error

// ThrowError 让原生函数以当前全局对象中名为 Type 的错误构造器抛出 Message
type ThrowError struct {
	Type    string
	Message string
}

func (err *ThrowError) Error() string { return err.Type + ": " + err.Message }

// Error 是 JS 抛出的异常，Value 为异常值的句柄
type Error struct {
	Message  string
	Stack    string
	Filename string
	Line     int
	Column   int
	Value    Handle
}

func (err *Error) Error() string {
	if err.Stack != "" {
		return err.Message + "\n" + err.Stack
	}
	return err.Message
}

// Options 是新建上下文的参数
type Options struct {
	HeapBytes uint32
	Locale    string
}

// Context 是绑定到创建它的 OS 线程的 JSContext，只能在该线程上使用
type Context struct {
	c       *C.mz_context
	id      uintptr
	natives []NativeFunc
	closed  bool
}

var (
	initOnce   sync.Once
	initErr    error
	contexts   sync.Map
	nextID     atomic.Uintptr
	stackSlack = 256 << 10
)

// Init 初始化进程内的 SpiderMonkey，只执行一次
func Init() error {
	initOnce.Do(func() {
		if reason := C.mz_init(); reason != nil {
			initErr = fmt.Errorf("初始化 SpiderMonkey: %s", C.GoString(reason))
		}
	})
	return initErr
}

// NewContext 在当前 OS 线程创建上下文，调用方需已锁定线程
func NewContext(options Options) (*Context, error) {
	if err := Init(); err != nil {
		return nil, err
	}
	quota := int(C.mz_thread_stack_size()) - stackSlack
	if quota < 512<<10 {
		quota = 512 << 10
	}
	id := nextID.Add(1)
	locale := C.CString(options.Locale)
	defer C.free(unsafe.Pointer(locale))
	handle := C.mz_context_new(C.uintptr_t(id), C.uint32_t(options.HeapBytes), C.size_t(quota), locale)
	if handle == nil {
		return nil, errors.New("创建 SpiderMonkey 上下文失败")
	}
	context := &Context{c: handle, id: id}
	contexts.Store(id, context)
	return context, nil
}

// SetCamouLocale 设置当前线程 mozilla::intl::Locale 的 Camoufox 语言、地区与 navigator.language
func SetCamouLocale(language, region, locale string) {
	cLanguage, cRegion, cLocale := C.CString(language), C.CString(region), C.CString(locale)
	defer C.free(unsafe.Pointer(cLanguage))
	defer C.free(unsafe.Pointer(cRegion))
	defer C.free(unsafe.Pointer(cLocale))
	C.mz_set_camou_locale(cLanguage, cRegion, cLocale)
}

// Now 返回单调时钟的毫秒数，精度为微秒
func Now() float64 { return float64(C.mz_now()) }

// Close 销毁上下文
func (cx *Context) Close() {
	if cx.closed {
		return
	}
	cx.closed = true
	contexts.Delete(cx.id)
	C.mz_context_free(cx.c)
}

// takeError 取出失败调用留下的异常
func (cx *Context) takeError() error {
	var exception C.mz_exception
	if !C.mz_take_exception(cx.c, &exception) {
		return errors.New("SpiderMonkey 调用失败")
	}
	err := &Error{
		Message: C.GoString(exception.message), Stack: C.GoString(exception.stack), Filename: C.GoString(exception.filename),
		Line: int(exception.line), Column: int(exception.column), Value: Handle(exception.value),
	}
	C.mz_free(unsafe.Pointer(exception.message))
	C.mz_free(unsafe.Pointer(exception.stack))
	C.mz_free(unsafe.Pointer(exception.filename))
	return err
}

// fromC 把 mz_value 转为 Value 并释放其字符串
func fromC(value *C.mz_value) Value {
	result := Value{Kind: Kind(value.kind), Bool: bool(value.boolean), Number: float64(value.number), Handle: Handle(value.handle)}
	if result.Kind == KindString {
		result.String = C.GoStringN(value.str, C.int(value.len))
		C.mz_free(unsafe.Pointer(value.str))
	}
	return result
}

// cValues 把参数转为 mz_value，返回释放字符串的函数
func cValues(values []Value) ([]C.mz_value, func()) {
	if len(values) == 0 {
		return nil, func() {}
	}
	converted := make([]C.mz_value, len(values))
	var strings []*C.char
	for index, value := range values {
		item := &converted[index]
		item.kind = C.int32_t(value.Kind)
		item.boolean = C.bool(value.Bool)
		item.number = C.double(value.Number)
		item.handle = C.uint32_t(value.Handle)
		if value.Kind == KindString {
			item.str = C.CString(value.String)
			item.len = C.size_t(len(value.String))
			strings = append(strings, item.str)
		}
	}
	return converted, func() {
		for _, pointer := range strings {
			C.free(unsafe.Pointer(pointer))
		}
	}
}

// Release 释放句柄
func (cx *Context) Release(handle Handle) {
	if handle != 0 {
		C.mz_release(cx.c, C.uint32_t(handle))
	}
}

// ReleaseValue 释放值持有的句柄
func (cx *Context) ReleaseValue(value Value) {
	if value.Kind == KindHandle {
		cx.Release(value.Handle)
	}
}

// NewGlobal 新建 Window 全局对象，same 非 0 时与其共用 compartment，timezone 为该 Realm 的时区
func (cx *Context) NewGlobal(same Handle, timezone string) (Handle, error) {
	zone := C.CString(timezone)
	defer C.free(unsafe.Pointer(zone))
	handle := Handle(C.mz_global_new(cx.c, C.uint32_t(same), zone))
	if handle == 0 {
		return 0, cx.takeError()
	}
	return handle, nil
}

// Evaluate 在 global 的 Realm 中执行脚本
func (cx *Context) Evaluate(global Handle, source, filename string, line int) (Value, error) {
	cSource := C.CString(source)
	defer C.free(unsafe.Pointer(cSource))
	cName := C.CString(filename)
	defer C.free(unsafe.Pointer(cName))
	var out C.mz_value
	if !C.mz_evaluate(cx.c, C.uint32_t(global), cSource, C.size_t(len(source)), cName, C.uint32_t(line), &out) {
		return Value{}, cx.takeError()
	}
	return fromC(&out), nil
}

// Call 以 this 调用函数 fn，this 为 0 时为 undefined
func (cx *Context) Call(fn Handle, this Handle, args ...Value) (Value, error) {
	converted, free := cValues(args)
	defer free()
	var pointer *C.mz_value
	if len(converted) > 0 {
		pointer = &converted[0]
	}
	var out C.mz_value
	if !C.mz_call_function(cx.c, C.uint32_t(fn), C.uint32_t(this), pointer, C.int(len(converted)), &out) {
		return Value{}, cx.takeError()
	}
	return fromC(&out), nil
}

// Get 读取对象属性
func (cx *Context) Get(object Handle, name string) (Value, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	var out C.mz_value
	if !C.mz_get_property(cx.c, C.uint32_t(object), cName, &out) {
		return Value{}, cx.takeError()
	}
	return fromC(&out), nil
}

// Set 写入对象属性
func (cx *Context) Set(object Handle, name string, value Value) error {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	converted, free := cValues([]Value{value})
	defer free()
	if !C.mz_set_property(cx.c, C.uint32_t(object), cName, &converted[0]) {
		return cx.takeError()
	}
	return nil
}

// NewObject 在 global 的 Realm 中新建普通对象
func (cx *Context) NewObject(global Handle) (Handle, error) {
	handle := Handle(C.mz_new_object(cx.c, C.uint32_t(global)))
	if handle == 0 {
		return 0, cx.takeError()
	}
	return handle, nil
}

// ParseJSON 在 global 的 Realm 中解析 JSON
func (cx *Context) ParseJSON(global Handle, text string) (Value, error) {
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))
	var out C.mz_value
	if !C.mz_parse_json(cx.c, C.uint32_t(global), cText, C.size_t(len(text)), &out) {
		return Value{}, cx.takeError()
	}
	return fromC(&out), nil
}

// NewFunction 在 global 的 Realm 中新建调用 fn 的函数
func (cx *Context) NewFunction(global Handle, name string, nargs int, fn NativeFunc) (Handle, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	cx.natives = append(cx.natives, fn)
	handle := Handle(C.mz_new_function(cx.c, C.uint32_t(global), cName, C.uint32_t(nargs), C.int32_t(len(cx.natives)-1)))
	if handle == 0 {
		return 0, cx.takeError()
	}
	return handle, nil
}

// InstallHostNatives 在 target 上定义 now、kindOf、throwLocation 与 incumbent
func (cx *Context) InstallHostNatives(target Handle) error {
	if !C.mz_install_host_natives(cx.c, C.uint32_t(target)) {
		return cx.takeError()
	}
	return nil
}

// InstallWindowNatives 在全局对象上定义 queueMicrotask、atob 与 btoa
func (cx *Context) InstallWindowNatives(global Handle) error {
	if !C.mz_install_window_natives(cx.c, C.uint32_t(global)) {
		return cx.takeError()
	}
	return nil
}

// RunJobs 执行微任务检查点
func (cx *Context) RunJobs() { C.mz_run_jobs(cx.c) }

// ClearKeptObjects 结束当前任务的 WeakRef 存活期
func (cx *Context) ClearKeptObjects() { C.mz_clear_kept_objects(cx.c) }

// SetMicrotaskReporter 设置以 (global, exception) 报告微任务回调异常的函数
func (cx *Context) SetMicrotaskReporter(fn Handle) { C.mz_set_microtask_reporter(cx.c, C.uint32_t(fn)) }

// FlushRejections 按发生顺序以 (promise, handled, global) 调用 fn 并清空 Promise 拒绝记录
func (cx *Context) FlushRejections(fn Handle) error {
	if !C.mz_flush_rejections(cx.c, C.uint32_t(fn)) {
		return cx.takeError()
	}
	return nil
}

// SetTrustedTypes 设置 TrustedScript 源码登记表与是否拒绝以字符串编译代码
func (cx *Context) SetTrustedTypes(registry Handle, block bool) {
	C.mz_set_trusted_types(cx.c, C.uint32_t(registry), C.bool(block))
}

// SetThrowLocation 设置宿主 throwLocation 返回的抛出位置
func (cx *Context) SetThrowLocation(filename string, line, column int) {
	cName := C.CString(filename)
	defer C.free(unsafe.Pointer(cName))
	C.mz_set_throw_location(cx.c, cName, C.uint32_t(line), C.uint32_t(column))
}

// Interrupt 请求终止正在执行的脚本，可在任意 goroutine 调用
func (cx *Context) Interrupt() { C.mz_request_interrupt(cx.c) }

// GC 执行一次完整 GC
func (cx *Context) GC() { C.mz_gc(cx.c) }

// Call 是一次原生函数调用
type Call struct {
	c       *C.mz_call
	context *Context
}

// Arg 返回第 index 个参数，对象等值的句柄由调用方释放
func (call *Call) Arg(index int) Value {
	var out C.mz_value
	if !C.mz_call_arg(call.c, C.uint32_t(index), &out) {
		return Value{}
	}
	return fromC(&out)
}

// ArgString 返回第 index 个参数的字符串，不是字符串时为空串
func (call *Call) ArgString(index int) string {
	value := call.Arg(index)
	call.context.ReleaseValue(value)
	return value.String
}

// ArgNumber 返回第 index 个参数的数值，不是数值时为 0
func (call *Call) ArgNumber(index int) float64 {
	value := call.Arg(index)
	call.context.ReleaseValue(value)
	return value.Number
}

// ArgBytes 返回第 index 个参数（ArrayBuffer 或视图）的字节副本
func (call *Call) ArgBytes(index int) ([]byte, bool) {
	var data *C.uint8_t
	var size C.size_t
	if !C.mz_call_arg_bytes(call.c, C.uint32_t(index), &data, &size) {
		return nil, false
	}
	defer C.mz_free(unsafe.Pointer(data))
	return C.GoBytes(unsafe.Pointer(data), C.int(size)), true
}

// Return 设置返回值
func (call *Call) Return(value Value) {
	converted, free := cValues([]Value{value})
	defer free()
	C.mz_call_return(call.c, &converted[0])
}

// ReturnBytes 以字节新建 ArrayBuffer 作为返回值
func (call *Call) ReturnBytes(data []byte) {
	var pointer *C.uint8_t
	if len(data) > 0 {
		pointer = (*C.uint8_t)(unsafe.Pointer(&data[0]))
	}
	C.mz_call_return_bytes(call.c, pointer, C.size_t(len(data)))
}

// throw 在 JS 中抛出错误
func (call *Call) throw(kind, message string) {
	cKind, cMessage := C.CString(kind), C.CString(message)
	defer C.free(unsafe.Pointer(cKind))
	defer C.free(unsafe.Pointer(cMessage))
	C.mz_call_throw(call.c, cKind, cMessage)
}

//export mzGoNative
func mzGoNative(id C.uintptr_t, native C.int32_t, raw *C.mz_call) C.bool {
	value, _ := contexts.Load(uintptr(id))
	context := value.(*Context)
	call := &Call{c: raw, context: context}
	if err := context.natives[native](call); err != nil {
		var thrown *ThrowError
		if errors.As(err, &thrown) {
			call.throw(thrown.Type, thrown.Message)
		} else {
			call.throw("Error", err.Error())
		}
		return false
	}
	return true
}
