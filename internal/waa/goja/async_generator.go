package goja

import (
	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja/unistring"
)

const (
	classAsyncGenerator         = "AsyncGenerator"
	classAsyncGeneratorFunction = "AsyncGeneratorFunction"
)

// asyncGeneratorState 是异步生成器的 [[AsyncGeneratorState]]，执行中与清空队列包含其间的等待
type asyncGeneratorState uint8

const (
	asyncGenSuspendedStart asyncGeneratorState = iota
	asyncGenSuspendedYield
	asyncGenExecuting
	asyncGenDrainingQueue
	asyncGenCompleted
)

// completionType 是异步生成器请求与恢复所带的完成类型
type completionType uint8

const (
	completionNormal completionType = iota
	completionThrow
	completionReturn
)

// asyncGeneratorRequest 是 [[AsyncGeneratorQueue]] 中的请求，r 为所调用方法所在的 Realm
type asyncGeneratorRequest struct {
	completion completionType
	value      Value
	capability *promiseCapability
	r          *Runtime
}

// asyncGeneratorObject 是异步生成器对象
type asyncGeneratorObject struct {
	baseObject
	gen   generator
	state asyncGeneratorState
	queue []asyncGeneratorRequest
	// suspended 是生成器挂起处 yield 的结果类型
	suspended resultType
	// delegated 是 yield* 正在委托的内层异步迭代器
	delegated *iteratorRecord
}

// asyncGeneratorFuncObject 是 async function* 声明与表达式创建的函数
type asyncGeneratorFuncObject struct {
	baseJsFuncObject
}

// asyncGeneratorMethodFuncObject 是对象字面量与类中的 async * 方法
type asyncGeneratorMethodFuncObject struct {
	methodFuncObject
}

// asyncDelegateIter 是 yield* 取得的内层异步迭代器，经栈交给生成器对象
type asyncDelegateIter struct {
	valueNull
	iter *iteratorRecord
}

func (f *baseJsFuncObject) asyncGeneratorCall(vmCall func(*vm, int), nArgs int) Value {
	o := &Object{runtime: f.val.runtime}
	genObj := &asyncGeneratorObject{
		baseObject: baseObject{
			class:      classObject,
			val:        o,
			extensible: true,
		},
	}
	o.self = genObj
	genObj.init(vmCall, nArgs)
	genObj.prototype = o.runtime.getPrototypeFromCtor(f.val, nil, o.runtime.getAsyncGeneratorPrototype())
	return o
}

func (f *baseJsFuncObject) asyncGeneratorVmCall(vmCall func(*vm, int), nArgs int) {
	vm := f.val.runtime.vm
	vm.push(f.asyncGeneratorCall(vmCall, nArgs))
	vm.pc++
}

func (f *asyncGeneratorFuncObject) vmCall(_ *vm, nArgs int) {
	f.asyncGeneratorVmCall(f.baseJsFuncObject.vmCall, nArgs)
}

func (f *asyncGeneratorFuncObject) Call(call FunctionCall) Value {
	f.prepareForVmCall(call)
	return f.asyncGeneratorCall(f.baseJsFuncObject.vmCall, len(call.Arguments))
}

func (f *asyncGeneratorFuncObject) assertCallable() (func(FunctionCall) Value, bool) {
	return f.Call, true
}

func (f *asyncGeneratorFuncObject) export(*objectExportCtx) interface{} {
	return f.Call
}

func (f *asyncGeneratorFuncObject) assertConstructor() func(args []Value, newTarget *Object) *Object {
	return nil
}

func (f *asyncGeneratorMethodFuncObject) vmCall(_ *vm, nArgs int) {
	f.asyncGeneratorVmCall(f.methodFuncObject.vmCall, nArgs)
}

func (f *asyncGeneratorMethodFuncObject) Call(call FunctionCall) Value {
	f.prepareForVmCall(call)
	return f.asyncGeneratorCall(f.methodFuncObject.vmCall, len(call.Arguments))
}

func (f *asyncGeneratorMethodFuncObject) assertCallable() (func(FunctionCall) Value, bool) {
	return f.Call, true
}

func (f *asyncGeneratorMethodFuncObject) export(*objectExportCtx) interface{} {
	return f.Call
}

// newAsyncGeneratorFunc 创建异步生成器函数及其 prototype 对象
func (r *Runtime) newAsyncGeneratorFunc(name unistring.String, length int, strict bool) (f *asyncGeneratorFuncObject) {
	f = &asyncGeneratorFuncObject{}
	r.initBaseJsFunction(&f.baseJsFuncObject, strict)
	f.prototype = r.getAsyncGeneratorFunctionPrototype()
	f.val.self = f
	f.init(name, intToValue(int64(length)))
	f._putProp("prototype", r.newBaseObject(r.getAsyncGeneratorPrototype(), classObject).val, true, false, false)
	f.orderOwnKeys("prototype")
	return
}

// newAsyncGeneratorMethod 创建异步生成器方法及其 prototype 对象
func (r *Runtime) newAsyncGeneratorMethod(name unistring.String, length int, strict bool) (f *asyncGeneratorMethodFuncObject) {
	f = &asyncGeneratorMethodFuncObject{}
	r.initBaseJsFunction(&f.baseJsFuncObject, strict)
	f.prototype = r.getAsyncGeneratorFunctionPrototype()
	f.val.self = f
	f.init(name, intToValue(int64(length)))
	f._putProp("prototype", r.newBaseObject(r.getAsyncGeneratorPrototype(), classObject).val, true, false, false)
	f.orderOwnKeys("prototype")
	return
}

// newAsyncGeneratorFunc 是创建异步生成器函数的指令
type newAsyncGeneratorFunc struct {
	newFunc
}

func (n *newAsyncGeneratorFunc) exec(vm *vm) {
	obj := vm.r.newAsyncGeneratorFunc(n.name, n.length, n.strict)
	obj.prg = n.prg
	obj.stash = vm.stash
	obj.privEnv = vm.privEnv
	obj.src = n.source
	vm.push(obj.val)
	vm.pc++
}

// newAsyncGeneratorMethod 是创建异步生成器方法的指令
type newAsyncGeneratorMethod struct {
	newMethod
}

func (n *newAsyncGeneratorMethod) exec(vm *vm) {
	obj := vm.r.newAsyncGeneratorMethod(n.name, n.length, n.strict)
	n._exec(vm, &obj.methodFuncObject)
}

// genClosing 是 finally 块开头的指令，生成器因 return 关闭而进入时继续，否则跳转 jmp
type genClosing int32

func (jmp genClosing) exec(vm *vm) {
	if vm.tryStack[len(vm.tryStack)-1].finallyRet == -2 {
		vm.pc++
		return
	}
	vm.pc += int(jmp)
}

// getAsyncDelegate 为 yield* 取得栈顶操作数的异步迭代器
type getAsyncDelegate struct{}

func (getAsyncDelegate) exec(vm *vm) {
	value := vm.stack[vm.sp-1]
	vm.stack[vm.sp-1] = &asyncDelegateIter{iter: vm.r.getAsyncIteratorFor(value, vm.calleeText(value))}
	vm.pc++
}

// getAsyncIteratorFor 是 SpiderMonkey 的 GetIterator(obj, async)，text 为报错时操作数的反编译文本
func (r *Runtime) getAsyncIteratorFor(obj Value, text string) *iteratorRecord {
	if obj == _undefined || obj == _null {
		panic(r.propertyAccessError("Symbol.asyncIterator", text, obj))
	}
	if method := r.getV(obj, SymAsyncIterator); method != nil && method != _undefined && method != _null {
		call, ok := assertCallable(method)
		if !ok {
			panic(r.NewTypeError("%s is not iterable", text))
		}
		iter, ok := call(FunctionCall{This: obj}).(*Object)
		if !ok {
			panic(r.NewTypeError("[Symbol.asyncIterator]() returned a non-object value"))
		}
		return r.newIteratorRecord(iter)
	}
	call, ok := assertCallable(r.getV(obj, SymIterator))
	if !ok {
		panic(r.NewTypeError("%s is not iterable", text))
	}
	iter, ok := call(FunctionCall{This: obj}).(*Object)
	if !ok {
		panic(r.NewTypeError("[Symbol.iterator]() returned a non-object value"))
	}
	return r.createAsyncFromSyncIterator(r.newIteratorRecord(iter))
}

// newIteratorRecord 读取迭代器的 next 方法组成 Iterator Record，next 不可调用时留空
func (r *Runtime) newIteratorRecord(iter *Object) *iteratorRecord {
	record := &iteratorRecord{iterator: iter}
	if next, ok := assertCallable(iter.self.getStr("next", nil)); ok {
		record.next = next
	}
	return record
}

func (g *asyncGeneratorObject) init(vmCall func(*vm, int), nArgs int) {
	g.baseObject.init()
	vm := g.val.runtime.vm
	g.gen.vm = vm

	g.gen.enter()
	vmCall(vm, nArgs)

	_, _, ex := g.gen.step()

	vm.popTryFrame()
	if ex != nil {
		panic(ex)
	}

	g.suspended = resultYield
	vm.popCtx()
}

// asyncGeneratorMethod 返回 %AsyncGeneratorPrototype% 的 next、throw 或 return
func (r *Runtime) asyncGeneratorMethod(completion completionType) func(FunctionCall) Value {
	return func(call FunctionCall) Value {
		pcap := r.newPromiseCapability(r.getPromise())
		var g *asyncGeneratorObject
		if obj, ok := call.This.(*Object); ok {
			g, _ = obj.self.(*asyncGeneratorObject)
		}
		if g == nil {
			pcap.reject(r.NewTypeError("Not an async generator"))
			return pcap.promise
		}
		value := call.Argument(0)
		request := asyncGeneratorRequest{completion: completion, value: value, capability: pcap, r: r}
		switch completion {
		case completionNormal:
			if g.state == asyncGenCompleted {
				pcap.resolve(r.createIterResultObject(_undefined, true))
				break
			}
			g.queue = append(g.queue, request)
			if g.state == asyncGenSuspendedStart || g.state == asyncGenSuspendedYield {
				g.resume(completion, value)
			}
		case completionReturn:
			g.queue = append(g.queue, request)
			switch g.state {
			case asyncGenSuspendedStart, asyncGenCompleted:
				g.state = asyncGenDrainingQueue
				g.awaitReturn(value)
			case asyncGenSuspendedYield:
				g.resume(completion, value)
			}
		default:
			if g.state == asyncGenSuspendedStart {
				g.state = asyncGenCompleted
			}
			if g.state == asyncGenCompleted {
				pcap.reject(value)
				break
			}
			g.queue = append(g.queue, request)
			if g.state == asyncGenSuspendedYield {
				g.resume(completion, value)
			}
		}
		return pcap.promise
	}
}

// resume 是 AsyncGeneratorResume，return 完成先按 AsyncGeneratorUnwrapYieldResumption 等待其值
func (g *asyncGeneratorObject) resume(completion completionType, value Value) {
	g.state = asyncGenExecuting
	if completion == completionReturn {
		g.await(value, func(v Value) {
			g.continueWith(completionReturn, v)
		}, func(reason Value) {
			g.continueWith(completionThrow, reason)
		})
		return
	}
	g.continueWith(completion, value)
}

// continueWith 在挂起处以 completion 继续执行生成器体或 yield* 委托
func (g *asyncGeneratorObject) continueWith(completion completionType, value Value) {
	if g.delegated != nil {
		g.delegateStep(completion, value)
		return
	}
	switch completion {
	case completionNormal:
		g.run(g.gen.next(g.resumeValue(value)))
	case completionThrow:
		g.run(g.gen.nextThrow(value))
	default:
		g.run(g.forceReturn(value))
	}
}

// resumeValue 返回恢复时压栈的值，挂起处的 yield 不需要结果时为 nil
func (g *asyncGeneratorObject) resumeValue(value Value) Value {
	if g.suspended == resultYieldRes || g.suspended == resultYieldDelegateRes {
		return value
	}
	return nil
}

// run 处理生成器体运行到 await、yield、yield* 或结束时的结果
func (g *asyncGeneratorObject) run(res Value, resType resultType, ex *Exception) {
	if ex != nil {
		g.finish()
		g.completeStep(completionThrow, ex.val, true)
		g.drainQueue()
		return
	}
	switch resType {
	case resultAwait:
		g.await(res, func(v Value) {
			g.run(g.gen.next(v))
		}, func(reason Value) {
			g.run(g.gen.nextThrow(reason))
		})
	case resultYield, resultYieldRes:
		g.suspended = resType
		g.yield(res)
	case resultYieldDelegate, resultYieldDelegateRes:
		g.suspended = resType
		g.delegated = res.(*asyncDelegateIter).iter
		g.delegateStep(completionNormal, _undefined)
	default:
		g.finish()
		g.completeStep(completionNormal, res, true)
		g.drainQueue()
	}
}

// yield 是 AsyncGeneratorYield：兑现队首请求，队列不空时立即以下一个请求恢复
func (g *asyncGeneratorObject) yield(value Value) {
	g.completeStep(completionNormal, value, false)
	if len(g.queue) == 0 {
		g.state = asyncGenSuspendedYield
		return
	}
	next := g.queue[0]
	g.resume(next.completion, next.value)
}

// finish 在生成器体结束时进入清空队列状态
func (g *asyncGeneratorObject) finish() {
	g.state = asyncGenDrainingQueue
	g.delegated = nil
	g.gen.returning = nil
}

// completeStep 是 AsyncGeneratorCompleteStep：以完成值结算并移除队首请求
func (g *asyncGeneratorObject) completeStep(completion completionType, value Value, done bool) {
	request := g.queue[0]
	g.queue[0] = asyncGeneratorRequest{}
	g.queue = g.queue[1:]
	if completion == completionThrow {
		request.capability.reject(value)
		return
	}
	request.capability.resolve(request.r.createIterResultObject(value, done))
}

// drainQueue 是 AsyncGeneratorDrainQueue
func (g *asyncGeneratorObject) drainQueue() {
	for len(g.queue) > 0 {
		next := g.queue[0]
		switch next.completion {
		case completionReturn:
			g.awaitReturn(next.value)
			return
		case completionThrow:
			g.completeStep(completionThrow, next.value, true)
		default:
			g.completeStep(completionNormal, _undefined, true)
		}
	}
	g.state = asyncGenCompleted
}

// awaitReturn 是 AsyncGeneratorAwaitReturn：等待 return 请求的值后结算并继续清空队列
func (g *asyncGeneratorObject) awaitReturn(value Value) {
	g.await(value, func(v Value) {
		g.completeStep(completionNormal, v, true)
		g.drainQueue()
	}, func(reason Value) {
		g.completeStep(completionThrow, reason, true)
		g.drainQueue()
	})
}

// await 按 Await 等待 value，PromiseResolve 抛出时直接以拒绝处理
func (g *asyncGeneratorObject) await(value Value, onFulfilled, onRejected func(Value)) {
	r := g.val.runtime
	var promise *Object
	if ex := r.vm.try(func() {
		promise = r.promiseResolve(r.getPromise(), value)
	}); ex != nil {
		onRejected(ex.val)
		return
	}
	promise.self.(*Promise).addReactions(&promiseReaction{
		typ: promiseReactionFulfill,
		handler: &jobCallback{callback: func(call FunctionCall) Value {
			onFulfilled(call.Argument(0))
			return _undefined
		}},
	}, &promiseReaction{
		typ: promiseReactionReject,
		handler: &jobCallback{callback: func(call FunctionCall) Value {
			onRejected(call.Argument(0))
			return _undefined
		}},
	})
}

// forceReturn 在挂起处以 return 完成恢复，依次执行外层 finally 块
func (g *asyncGeneratorObject) forceReturn(value Value) (Value, resultType, *Exception) {
	vm := g.gen.vm
	g.gen.returning = value
	g.gen.enterNext()
	if !g.gen.enterNextFinallyFrame() {
		vm.popTryFrame()
		ex := vm.restoreStacks(g.gen.iterStackLen, g.gen.refStackLen)
		vm.callStack = vm.callStack[:len(vm.callStack)-1]
		vm.sp = vm.sb - 1
		vm.popCtx()
		g.gen.returning = nil
		return value, resultNormal, ex
	}
	res, resType, ex := g.gen.step()
	vm.popTryFrame()
	vm.popCtx()
	return res, resType, ex
}

// delegateStep 执行 yield* 循环的一轮：以 completion 调用内层迭代器的 next、throw 或 return 并等待结果
func (g *asyncGeneratorObject) delegateStep(completion completionType, value Value) {
	r := g.val.runtime
	iter := g.delegated
	var result Value
	closing, returning := false, false
	if ex := g.inContext(func() {
		switch completion {
		case completionNormal:
			if iter.next == nil {
				panic(r.NewTypeError("(intermediate value).next is not a function"))
			}
			result = iter.next(FunctionCall{This: iter.iterator, Arguments: []Value{value}})
		case completionThrow:
			method := nilSafe(iter.iterator.self.getStr("throw", nil))
			if method == _undefined || method == _null {
				method = nilSafe(iter.iterator.self.getStr("return", nil))
				if method == _undefined || method == _null {
					panic(r.NewTypeError("iterator does not have a 'throw' method"))
				}
				closing = true
				result = r.toCallable(method)(FunctionCall{This: iter.iterator})
				return
			}
			result = r.toCallable(method)(FunctionCall{This: iter.iterator, Arguments: []Value{value}})
		default:
			method := nilSafe(iter.iterator.self.getStr("return", nil))
			if method == _undefined || method == _null {
				returning = true
				return
			}
			result = r.toCallable(method)(FunctionCall{This: iter.iterator, Arguments: []Value{value}})
		}
	}); ex != nil {
		g.delegateThrow(ex.val)
		return
	}
	if returning {
		g.await(value, func(v Value) {
			g.delegated = nil
			g.run(g.forceReturn(v))
		}, g.delegateThrow)
		return
	}
	g.await(result, func(res Value) {
		if closing {
			if _, ok := res.(*Object); !ok {
				g.delegateThrow(r.NewTypeError("iterator.return() returned a non-object value"))
				return
			}
			g.delegateThrow(r.NewTypeError("iterator does not have a 'throw' method"))
			return
		}
		g.delegateResult(completion, res)
	}, g.delegateThrow)
}

// inContext 在生成器挂起处的执行上下文中调用 fn，使 fn 中的调用栈包含生成器帧
func (g *asyncGeneratorObject) inContext(fn func()) *Exception {
	vm := g.gen.vm
	g.gen.enterNext()
	ex := vm.try(fn)
	vm.suspend(&g.gen.ctx, g.gen.tryStackLen, g.gen.iterStackLen, g.gen.refStackLen)
	vm.sp = vm.sb - 1
	vm.callStack = vm.callStack[:len(vm.callStack)-1]
	vm.popTryFrame()
	vm.popCtx()
	return ex
}

// delegateThrow 结束 yield* 委托，并在 yield* 处抛出 reason
func (g *asyncGeneratorObject) delegateThrow(reason Value) {
	g.delegated = nil
	g.run(g.gen.nextThrow(reason))
}

// delegateResult 处理内层迭代器方法结果：完成时结束 yield* 或以其值返回，未完成时以其值 yield
func (g *asyncGeneratorObject) delegateResult(completion completionType, res Value) {
	r := g.val.runtime
	var done bool
	var value Value
	if ex := g.inContext(func() {
		obj, ok := res.(*Object)
		if !ok {
			panic(r.NewTypeError("iterator.%s() returned a non-object value", completionMethodName(completion)))
		}
		done = iteratorComplete(obj)
		value = iteratorValue(obj)
	}); ex != nil {
		g.delegateThrow(ex.val)
		return
	}
	if !done {
		g.yield(value)
		return
	}
	g.delegated = nil
	if completion == completionReturn {
		g.run(g.forceReturn(value))
		return
	}
	g.run(g.gen.next(g.resumeValue(value)))
}

// completionMethodName 返回 completion 对应的迭代器方法名
func completionMethodName(completion completionType) string {
	switch completion {
	case completionThrow:
		return "throw"
	case completionReturn:
		return "return"
	}
	return "next"
}

func (r *Runtime) builtin_asyncGeneratorFunction(args []Value, proto *Object) *Object {
	return r.functionCtor(args, proto, true, true)
}

func (r *Runtime) createAsyncGeneratorFunctionProto(val *Object) objectImpl {
	o := newBaseObjectObj(val, r.getFunctionPrototype(), classObject)

	o._putProp("constructor", r.getAsyncGeneratorFunction(), false, false, true)
	o._putProp("prototype", r.getAsyncGeneratorPrototype(), false, false, true)
	o._putSym(SymToStringTag, valueProp(asciiString(classAsyncGeneratorFunction), false, false, true))

	return o
}

func (r *Runtime) getAsyncGeneratorFunctionPrototype() *Object {
	var o *Object
	if o = r.global.AsyncGeneratorFunctionPrototype; o == nil {
		o = &Object{runtime: r}
		r.global.AsyncGeneratorFunctionPrototype = o
		o.self = r.createAsyncGeneratorFunctionProto(o)
	}
	return o
}

func (r *Runtime) getAsyncGeneratorFunction() *Object {
	var o *Object
	if o = r.global.AsyncGeneratorFunction; o == nil {
		o = &Object{runtime: r}
		r.global.AsyncGeneratorFunction = o
		f := r.newNativeFuncConstructObj(o, r.builtin_asyncGeneratorFunction, "AsyncGeneratorFunction", r.getAsyncGeneratorFunctionPrototype(), 1)
		f.prototype = r.getFunction()
		f.orderOwnKeys("prototype", "name")
		o.self = f
	}
	return o
}

func (r *Runtime) createAsyncGeneratorProto(val *Object) objectImpl {
	o := newBaseObjectObj(val, r.getAsyncIteratorPrototype(), classObject)

	o._putProp("next", r.newNameFirstNativeFunc(r.asyncGeneratorMethod(completionNormal), "next", 1), true, false, true)
	o._putProp("throw", r.newNameFirstNativeFunc(r.asyncGeneratorMethod(completionThrow), "throw", 1), true, false, true)
	o._putProp("return", r.newNameFirstNativeFunc(r.asyncGeneratorMethod(completionReturn), "return", 1), true, false, true)
	o._putProp("constructor", r.getAsyncGeneratorFunctionPrototype(), false, false, true)
	o._putSym(SymToStringTag, valueProp(asciiString(classAsyncGenerator), false, false, true))

	return o
}

func (r *Runtime) getAsyncGeneratorPrototype() *Object {
	var o *Object
	if o = r.global.AsyncGeneratorPrototype; o == nil {
		o = &Object{runtime: r}
		r.global.AsyncGeneratorPrototype = o
		o.self = r.createAsyncGeneratorProto(o)
	}
	return o
}

func (r *Runtime) createAsyncIteratorProto(val *Object) objectImpl {
	o := newBaseObjectObj(val, r.global.ObjectPrototype, classObject)

	o._putSym(SymAsyncIterator, valueProp(r.newNameFirstNativeFunc(r.returnThis, "[Symbol.asyncIterator]", 0), true, false, true))
	o._putSym(SymAsyncDispose, valueProp(r.newNameFirstNativeFunc(r.asyncIteratorProto_asyncDispose, "[Symbol.asyncDispose]", 0), true, false, true))
	return o
}

func (r *Runtime) getAsyncIteratorPrototype() *Object {
	var o *Object
	if o = r.global.AsyncIteratorPrototype; o == nil {
		o = &Object{runtime: r}
		r.global.AsyncIteratorPrototype = o
		o.self = r.createAsyncIteratorProto(o)
	}
	return o
}

// newNameFirstNativeFunc 创建自有键顺序为 name、length 的内置函数
func (r *Runtime) newNameFirstNativeFunc(call func(FunctionCall) Value, name unistring.String, length int) *Object {
	f := r.newNativeFunc(call, name, length)
	f.self.(*nativeFuncObject).orderOwnKeys("name")
	return f
}

// asyncIteratorProto_asyncDispose 是 %AsyncIteratorPrototype%[@@asyncDispose]：调用 return 并等待后以 undefined 兑现
func (r *Runtime) asyncIteratorProto_asyncDispose(call FunctionCall) Value {
	pcap := r.newPromiseCapability(r.getPromise())
	pcap.try(func() {
		method := nilSafe(r.getV(call.This, asciiString("return")))
		if method == _undefined || method == _null {
			pcap.resolve(_undefined)
			return
		}
		result := r.toCallable(method)(FunctionCall{This: call.This, Arguments: []Value{_undefined}})
		wrapper := r.promiseResolve(r.getPromise(), result)
		unwrap := r.newNativeFunc(func(FunctionCall) Value {
			return _undefined
		}, "", 1)
		r.performPromiseThen(wrapper.self.(*Promise), unwrap, _undefined, pcap)
	})
	return pcap.promise
}
