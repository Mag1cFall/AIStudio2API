package goja

import (
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja/unistring"
)

// GlobalObserver 接收全局对象的惰性名称解析、属性定义与删除，并给出自有字符串键的枚举顺序
type GlobalObserver interface {
	Resolve(name string)
	Define(name string)
	Delete(name string)
	OrderKeys(keys []string) []string
}

// observedGlobal 包装全局对象，把脚本对全局属性的具名访问与枚举交给观察者
type observedGlobal struct {
	objectImpl
	r *Runtime
}

// SetGlobalObserver 为当前 Realm 的全局对象设置观察者
func (r *Runtime) SetGlobalObserver(observer GlobalObserver) {
	r.globalObserver = observer
	if _, wrapped := r.globalObject.self.(*observedGlobal); !wrapped {
		r.globalObject.self = &observedGlobal{objectImpl: r.globalObject.self, r: r}
	}
}

// ResolveGlobal 按脚本访问的语义解析当前 Realm 的惰性全局名称
func (r *Runtime) ResolveGlobal(name string) {
	if r.globalObserver != nil {
		r.globalObserver.Resolve(name)
	}
}

// resolveBuiltin 在非宿主脚本使用内建类时解析对应的全局名称
func (r *Runtime) resolveBuiltin(name string) {
	if r.globalObserver != nil && r.resolveMuted == 0 && !r.vm.inHostScript() {
		r.globalObserver.Resolve(name)
	}
}

// inHostScript 判断最近的脚本帧是否属于宿主脚本
func (vm *vm) inHostScript() bool {
	prg := vm.prg
	for index := len(vm.callStack) - 1; prg == nil && index >= 0; index-- {
		prg = vm.callStack[index].prg
	}
	return prg != nil && prg.src != nil && strings.HasPrefix(prg.src.Name(), hostSourcePrefix)
}

func (o *observedGlobal) touch(name unistring.String) {
	if o.r.resolveMuted == 0 && !o.r.vm.inHostScript() {
		o.r.globalObserver.Resolve(name.String())
	}
}

func (o *observedGlobal) getStr(name unistring.String, receiver Value) Value {
	o.touch(name)
	return o.objectImpl.getStr(name, receiver)
}

func (o *observedGlobal) getOwnPropStr(name unistring.String) Value {
	o.touch(name)
	return o.objectImpl.getOwnPropStr(name)
}

func (o *observedGlobal) hasPropertyStr(name unistring.String) bool {
	o.touch(name)
	return o.objectImpl.hasPropertyStr(name)
}

func (o *observedGlobal) hasOwnPropertyStr(name unistring.String) bool {
	o.touch(name)
	return o.objectImpl.hasOwnPropertyStr(name)
}

func (o *observedGlobal) setOwnStr(name unistring.String, value Value, throw bool) bool {
	o.touch(name)
	existed := o.objectImpl.hasOwnPropertyStr(name)
	ok := o.objectImpl.setOwnStr(name, value, throw)
	if ok && !existed && !o.r.vm.inHostScript() {
		o.r.globalObserver.Define(name.String())
	}
	return ok
}

func (o *observedGlobal) setForeignStr(name unistring.String, value, receiver Value, throw bool) (bool, bool) {
	o.touch(name)
	return o.objectImpl.setForeignStr(name, value, receiver, throw)
}

func (o *observedGlobal) defineOwnPropertyStr(name unistring.String, descriptor PropertyDescriptor, throw bool) bool {
	o.touch(name)
	existed := o.objectImpl.hasOwnPropertyStr(name)
	ok := o.objectImpl.defineOwnPropertyStr(name, descriptor, throw)
	if ok && !existed && !o.r.vm.inHostScript() {
		o.r.globalObserver.Define(name.String())
	}
	return ok
}

func (o *observedGlobal) deleteStr(name unistring.String, throw bool) bool {
	o.touch(name)
	ok := o.objectImpl.deleteStr(name, throw)
	if ok && !o.r.vm.inHostScript() {
		o.r.globalObserver.Delete(name.String())
	}
	return ok
}

func (o *observedGlobal) stringKeys(all bool, accum []Value) []Value {
	o.r.resolveMuted++
	keys := o.objectImpl.stringKeys(all, nil)
	o.r.resolveMuted--
	if o.r.vm.inHostScript() {
		return append(accum, keys...)
	}
	names := make([]string, 0, len(keys))
	values := make(map[string]Value, len(keys))
	for _, key := range keys {
		name := key.String()
		names = append(names, name)
		values[name] = key
	}
	for _, name := range o.r.globalObserver.OrderKeys(names) {
		if value, ok := values[name]; ok {
			accum = append(accum, value)
		}
	}
	return accum
}

func (o *observedGlobal) keys(all bool, accum []Value) []Value {
	return o.objectImpl.symbols(all, o.stringKeys(all, accum))
}

func (o *observedGlobal) iterateStringKeys() iterNextFunc {
	items := make(map[string]propIterItem)
	o.r.resolveMuted++
	defer func() { o.r.resolveMuted-- }()
	next := o.objectImpl.iterateStringKeys()
	for {
		item, following := next()
		if following == nil {
			break
		}
		items[item.name.String()] = item
		next = following
	}
	names := make([]string, 0, len(items))
	for _, key := range o.objectImpl.stringKeys(true, nil) {
		if _, ok := items[key.String()]; ok {
			names = append(names, key.String())
		}
	}
	ordered := names
	if !o.r.vm.inHostScript() {
		ordered = o.r.globalObserver.OrderKeys(names)
	}
	index := 0
	var iterate iterNextFunc
	iterate = func() (propIterItem, iterNextFunc) {
		for index < len(ordered) {
			item, ok := items[ordered[index]]
			index++
			if ok {
				return item, iterate
			}
		}
		return propIterItem{}, nil
	}
	return iterate
}
