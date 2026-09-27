package goja

import (
	"strconv"
	"strings"
)

// hostSourcePrefix 标记不出现在 Error.stack 中的宿主脚本来源
const hostSourcePrefix = "\x00"

const propNameStack = "stack"

type errorObject struct {
	baseObject
	stack []StackFrame
}

func (e *errorObject) formatStack() String {
	var b StringBuilder
	for _, frame := range e.stack {
		if frame.prg == nil || frame.prg.src != nil && strings.HasPrefix(frame.prg.src.Name(), hostSourcePrefix) {
			continue
		}
		name := frame.funcName
		if name == "" {
			name = frame.prg.displayName
		}
		b.WriteUTF8String(name.String())
		b.WriteRune('@')
		position := frame.Position()
		if position.Filename == "" {
			b.writeASCII("<eval>")
		} else {
			b.WriteUTF8String(position.Filename)
		}
		b.WriteRune(':')
		b.writeASCII(strconv.Itoa(position.Line))
		b.WriteRune(':')
		b.writeASCII(strconv.Itoa(position.Column))
		b.WriteRune('\n')
	}
	stack := b.String()
	return stack
}

func (e *errorObject) init() {
	e.baseObject.init()
	vm := e.val.runtime.vm
	e.stack = vm.captureStack(make([]StackFrame, 0, len(vm.callStack)+1), 0)
	if frame := firstScriptFrame(e.stack); frame != nil {
		position := frame.Position()
		e._putProp("fileName", e.val.runtime.ToValue(position.Filename), true, false, true)
		e._putProp("lineNumber", e.val.runtime.ToValue(position.Line), true, false, true)
		e._putProp("columnNumber", e.val.runtime.ToValue(position.Column), true, false, true)
	}
}

func (r *Runtime) newErrorObject(proto *Object, class string) *errorObject {
	obj := &Object{runtime: r}
	o := &errorObject{
		baseObject: baseObject{
			class:      class,
			val:        obj,
			extensible: true,
			prototype:  proto,
		},
	}
	obj.self = o
	o.init()
	return o
}

func (r *Runtime) builtin_Error(args []Value, proto *Object) *Object {
	obj := r.newErrorObject(proto, classError)
	if len(args) > 0 && args[0] != _undefined {
		obj._putProp("message", args[0].toString(), true, false, true)
	}
	if len(args) > 1 && args[1] != _undefined {
		if options, ok := args[1].(*Object); ok {
			if options.hasProperty(asciiString("cause")) {
				obj.defineOwnPropertyStr("cause", PropertyDescriptor{
					Writable:     FLAG_TRUE,
					Enumerable:   FLAG_FALSE,
					Configurable: FLAG_TRUE,
					Value:        options.Get("cause"),
				}, true)
			}
		}
	}
	return obj.val
}

func (r *Runtime) builtin_AggregateError(args []Value, proto *Object) *Object {
	obj := r.newErrorObject(proto, classError)
	if len(args) > 1 && args[1] != nil && args[1] != _undefined {
		obj._putProp("message", args[1].toString(), true, false, true)
	}
	items := _undefined
	if len(args) > 0 {
		items = args[0]
	}
	errors := r.iterableToList(items, nil)
	obj._putProp("errors", r.newArrayValues(errors), true, false, true)

	if len(args) > 2 && args[2] != _undefined {
		if options, ok := args[2].(*Object); ok {
			if options.hasProperty(asciiString("cause")) {
				obj.defineOwnPropertyStr("cause", PropertyDescriptor{
					Writable:     FLAG_TRUE,
					Enumerable:   FLAG_FALSE,
					Configurable: FLAG_TRUE,
					Value:        options.Get("cause"),
				}, true)
			}
		}
	}

	return obj.val
}

func (r *Runtime) newAggregateErrorErrors(errors []Value) *Object {
	proto := r.getPrototypeFromCtor(r.getAggregateError(), nil, nil)
	obj := r.newErrorObject(proto, classError)
	obj._putProp("errors", r.newArrayValues(errors), true, false, true)
	return obj.val
}

func (r *Runtime) error_isError(call FunctionCall) Value {
	if o, ok := call.Argument(0).(*Object); ok {
		if o.self.className() == classError {
			return valueTrue
		}
	}
	return valueFalse
}

func writeErrorString(sb *StringBuilder, obj *Object) String {
	var nameStr, msgStr String
	name := obj.self.getStr("name", nil)
	if name == nil || name == _undefined {
		nameStr = asciiString("Error")
	} else {
		nameStr = name.toString()
	}
	msg := obj.self.getStr("message", nil)
	if msg == nil || msg == _undefined {
		msgStr = stringEmpty
	} else {
		msgStr = msg.toString()
	}
	if nameStr.Length() == 0 {
		return msgStr
	}
	if msgStr.Length() == 0 {
		return nameStr
	}
	sb.WriteString(nameStr)
	sb.WriteString(asciiString(": "))
	sb.WriteString(msgStr)
	return nil
}

func (r *Runtime) error_toString(call FunctionCall) Value {
	var sb StringBuilder
	val := writeErrorString(&sb, r.toObject(call.This))
	if val != nil {
		return val
	}
	return sb.String()
}

func (r *Runtime) error_stack(call FunctionCall) Value {
	if obj, ok := call.This.(*Object); ok {
		if err, ok := obj.self.(*errorObject); ok {
			return err.formatStack()
		}
	}
	return _undefined
}

func (r *Runtime) error_setStack(call FunctionCall) Value {
	obj := r.toObject(call.This)
	obj.self.defineOwnPropertyStr(propNameStack, PropertyDescriptor{
		Value:        call.Argument(0),
		Writable:     FLAG_TRUE,
		Configurable: FLAG_TRUE,
		Enumerable:   FLAG_TRUE,
	}, true)
	return _undefined
}

func (r *Runtime) createErrorPrototype(name String, ctor *Object) *Object {
	o := r.newBaseObject(r.getErrorPrototype(), classObject)
	o._putProp("message", stringEmpty, true, false, true)
	o._putProp("name", name, true, false, true)
	o._putProp("constructor", ctor, true, false, true)
	return o.val
}

func (r *Runtime) getErrorPrototype() *Object {
	ret := r.global.ErrorPrototype
	if ret == nil {
		ret = r.NewObject()
		r.global.ErrorPrototype = ret
		o := ret.self
		o._putProp("message", stringEmpty, true, false, true)
		o._putProp("name", stringError, true, false, true)
		o.defineOwnPropertyStr(propNameStack, PropertyDescriptor{
			Getter:       r.newNativeFunc(r.error_stack, "get stack", 0),
			Setter:       r.newNativeFunc(r.error_setStack, "set stack", 1),
			Configurable: FLAG_TRUE,
			Enumerable:   FLAG_FALSE,
		}, true)
		o._putProp("toString", r.newNativeFunc(r.error_toString, "toString", 0), true, false, true)
		o._putProp("constructor", r.getError(), true, false, true)
	}
	return ret
}

func (r *Runtime) getError() *Object {
	r.resolveBuiltin("Error")
	ret := r.global.Error
	if ret == nil {
		ret = &Object{runtime: r}
		r.global.Error = ret
		r.newNativeFuncConstruct(ret, r.builtin_Error, "Error", r.getErrorPrototype(), 1)
		o := ret.self
		o._putProp("isError", r.newNativeFunc(r.error_isError, "isError", 1), true, false, true)
	}
	return ret
}

func (r *Runtime) getAggregateError() *Object {
	r.resolveBuiltin("AggregateError")
	ret := r.global.AggregateError
	if ret == nil {
		ret = &Object{runtime: r}
		r.global.AggregateError = ret
		r.newNativeFuncConstructProto(ret, r.builtin_AggregateError, "AggregateError", r.createErrorPrototype(stringAggregateError, ret), r.getError(), 2)
	}
	return ret
}

func (r *Runtime) getTypeError() *Object {
	r.resolveBuiltin("TypeError")
	ret := r.global.TypeError
	if ret == nil {
		ret = &Object{runtime: r}
		r.global.TypeError = ret
		r.newNativeFuncConstructProto(ret, r.builtin_Error, "TypeError", r.createErrorPrototype(stringTypeError, ret), r.getError(), 1)
	}
	return ret
}

func (r *Runtime) getReferenceError() *Object {
	r.resolveBuiltin("ReferenceError")
	ret := r.global.ReferenceError
	if ret == nil {
		ret = &Object{runtime: r}
		r.global.ReferenceError = ret
		r.newNativeFuncConstructProto(ret, r.builtin_Error, "ReferenceError", r.createErrorPrototype(stringReferenceError, ret), r.getError(), 1)
	}
	return ret
}

func (r *Runtime) getSyntaxError() *Object {
	r.resolveBuiltin("SyntaxError")
	ret := r.global.SyntaxError
	if ret == nil {
		ret = &Object{runtime: r}
		r.global.SyntaxError = ret
		r.newNativeFuncConstructProto(ret, r.builtin_Error, "SyntaxError", r.createErrorPrototype(stringSyntaxError, ret), r.getError(), 1)
	}
	return ret
}

func (r *Runtime) getRangeError() *Object {
	r.resolveBuiltin("RangeError")
	ret := r.global.RangeError
	if ret == nil {
		ret = &Object{runtime: r}
		r.global.RangeError = ret
		r.newNativeFuncConstructProto(ret, r.builtin_Error, "RangeError", r.createErrorPrototype(stringRangeError, ret), r.getError(), 1)
	}
	return ret
}

func (r *Runtime) getInternalError() *Object {
	r.resolveBuiltin("InternalError")
	ret := r.global.InternalError
	if ret == nil {
		ret = &Object{runtime: r}
		r.global.InternalError = ret
		r.newNativeFuncConstructProto(ret, r.builtin_Error, "InternalError", r.createErrorPrototype(asciiString("InternalError"), ret), r.getError(), 1)
	}
	return ret
}

func (r *Runtime) getEvalError() *Object {
	r.resolveBuiltin("EvalError")
	ret := r.global.EvalError
	if ret == nil {
		ret = &Object{runtime: r}
		r.global.EvalError = ret
		r.newNativeFuncConstructProto(ret, r.builtin_Error, "EvalError", r.createErrorPrototype(stringEvalError, ret), r.getError(), 1)
	}
	return ret
}

func (r *Runtime) getURIError() *Object {
	r.resolveBuiltin("URIError")
	ret := r.global.URIError
	if ret == nil {
		ret = &Object{runtime: r}
		r.global.URIError = ret
		r.newNativeFuncConstructProto(ret, r.builtin_Error, "URIError", r.createErrorPrototype(stringURIError, ret), r.getError(), 1)
	}
	return ret
}

func (r *Runtime) getGoError() *Object {
	ret := r.global.GoError
	if ret == nil {
		ret = &Object{runtime: r}
		r.global.GoError = ret
		r.newNativeFuncConstructProto(ret, r.builtin_Error, "GoError", r.createErrorPrototype(stringGoError, ret), r.getError(), 1)
	}
	return ret
}

// firstScriptFrame 返回第一个出现在 Error.stack 中的脚本帧
func firstScriptFrame(stack []StackFrame) *StackFrame {
	for index := range stack {
		frame := &stack[index]
		if frame.prg != nil && (frame.prg.src == nil || !strings.HasPrefix(frame.prg.src.Name(), hostSourcePrefix)) {
			return frame
		}
	}
	return nil
}
