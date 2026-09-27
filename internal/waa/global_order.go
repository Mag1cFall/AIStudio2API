package waa

import (
	"encoding/json"
	"strconv"
)

// numberGlobals 是 Number 类解析时按 SpiderMonkey 顺序一并定义的全局名称
var numberGlobals = []string{"isNaN", "isFinite", "parseInt", "parseFloat", "NaN", "Infinity", "Number"}

// stringGlobals 是 String 类解析时按 SpiderMonkey 顺序一并定义的全局名称
var stringGlobals = []string{"escape", "unescape", "decodeURI", "encodeURI", "decodeURIComponent", "encodeURIComponent", "String"}

// errorSubclasses 是解析前需要先解析 Error 的内建错误类
var errorSubclasses = map[string]bool{"InternalError": true, "AggregateError": true, "EvalError": true, "RangeError": true, "ReferenceError": true, "SuppressedError": true, "SyntaxError": true, "TypeError": true, "URIError": true, "WebAssembly": true}

// legacyFactories 是接口对象解析后紧随定义的旧式工厂函数
var legacyFactories = map[string]string{"HTMLImageElement": "Image", "HTMLAudioElement": "Audio", "HTMLOptionElement": "Option"}

// globalOrderShape 是形状表中决定全局键顺序的字段
type globalOrderShape struct {
	Interfaces []struct {
		N  string `json:"n"`
		CN string `json:"cn"`
		P  string `json:"p"`
	} `json:"interfaces"`
	Frame struct {
		FreshKeys []string `json:"freshKeys"`
	} `json:"frame"`
	Top struct {
		Global []struct {
			N string `json:"n"`
		} `json:"global"`
	} `json:"top"`
}

// firefoxGlobalOrder 按 Gecko 与 SpiderMonkey 的惰性全局解析规则维护一个 Realm 全局对象的自有键顺序
type firefoxGlobalOrder struct {
	lazy      []string
	isLazy    map[string]bool
	resolved  map[string]bool
	native    []string
	inNative  map[string]bool
	parents   map[string]string
	factories map[string]string
}

// parseGlobalOrderShape 读取形状表中的全局键顺序字段
func parseGlobalOrderShape(source string) (*globalOrderShape, error) {
	var shape globalOrderShape
	if err := json.Unmarshal([]byte(source), &shape); err != nil {
		return nil, err
	}
	return &shape, nil
}

// newFrameGlobalOrder 以新建同源 iframe 的首次枚举结果初始化惰性名称表与已定义属性顺序
func newFrameGlobalOrder(shape *globalOrderShape) *firefoxGlobalOrder {
	order := newGlobalOrder(shape)
	split := len(shape.Frame.FreshKeys)
	for index, name := range shape.Frame.FreshKeys {
		if name == "Function" {
			split = index
			break
		}
	}
	for _, name := range shape.Frame.FreshKeys[:split] {
		order.lazy = append(order.lazy, name)
		order.isLazy[name] = true
	}
	for _, name := range shape.Frame.FreshKeys[split:] {
		order.appendNative(name)
	}
	return order
}

// newTopGlobalOrder 以采集时顶层页面的全局键顺序初始化已解析顺序
func newTopGlobalOrder(shape *globalOrderShape) *firefoxGlobalOrder {
	order := newGlobalOrder(shape)
	order.lazy = []string{"undefined"}
	order.isLazy["undefined"] = true
	order.resolved["undefined"] = true
	for _, entry := range shape.Top.Global {
		order.appendNative(entry.N)
	}
	return order
}

func newGlobalOrder(shape *globalOrderShape) *firefoxGlobalOrder {
	order := &firefoxGlobalOrder{isLazy: map[string]bool{}, resolved: map[string]bool{}, inNative: map[string]bool{}, parents: map[string]string{}, factories: legacyFactories}
	for _, info := range shape.Interfaces {
		if info.N == info.CN && info.P != "" {
			order.parents[info.N] = info.P
		}
	}
	return order
}

func (order *firefoxGlobalOrder) appendNative(name string) {
	if !order.inNative[name] {
		order.inNative[name] = true
		order.native = append(order.native, name)
	}
}

// group 返回解析 name 时按顺序一并定义的名称
func (order *firefoxGlobalOrder) group(name string) []string {
	for factory, target := range map[string]string{"Image": "HTMLImageElement", "Audio": "HTMLAudioElement", "Option": "HTMLOptionElement"} {
		if name == factory {
			name = target
		}
	}
	for _, list := range [][]string{numberGlobals, stringGlobals} {
		for _, member := range list {
			if member == name {
				return list
			}
		}
	}
	if errorSubclasses[name] {
		return []string{"Error", name}
	}
	chain := []string{name}
	for parent := order.parents[name]; parent != ""; parent = order.parents[parent] {
		chain = append([]string{parent}, chain...)
	}
	if factory := order.factories[name]; factory != "" {
		chain = append(chain, factory)
	}
	return chain
}

// Resolve 在脚本首次按名称访问或引擎内部使用惰性全局时定义它
func (order *firefoxGlobalOrder) Resolve(name string) {
	if !order.isLazy[name] || order.resolved[name] {
		return
	}
	for _, member := range order.group(name) {
		if order.isLazy[member] && !order.resolved[member] {
			order.resolved[member] = true
			order.appendNative(member)
		}
	}
}

// Define 记录脚本新定义的全局属性
func (order *firefoxGlobalOrder) Define(name string) {
	order.appendNative(name)
}

// Delete 移除被删除的全局属性
func (order *firefoxGlobalOrder) Delete(name string) {
	if !order.inNative[name] {
		return
	}
	delete(order.inNative, name)
	for index, current := range order.native {
		if current == name {
			order.native = append(order.native[:index:index], order.native[index+1:]...)
			break
		}
	}
}

// OrderKeys 按枚举钩子名称、已定义属性、其余属性的顺序排列自有字符串键
func (order *firefoxGlobalOrder) OrderKeys(keys []string) []string {
	present := make(map[string]bool, len(keys))
	ordered := make([]string, 0, len(keys))
	added := make(map[string]bool, len(keys))
	add := func(name string) {
		if present[name] && !added[name] {
			added[name] = true
			ordered = append(ordered, name)
		}
	}
	for _, key := range keys {
		present[key] = true
		if _, err := strconv.ParseUint(key, 10, 32); err == nil {
			add(key)
		}
	}
	add("undefined")
	if order.isLazy["globalThis"] && !order.resolved["globalThis"] && present["globalThis"] {
		add("globalThis")
		order.resolved["globalThis"] = true
		order.appendNative("globalThis")
	}
	for _, name := range order.lazy {
		if !order.resolved[name] {
			add(name)
		}
	}
	for _, name := range order.native {
		add(name)
	}
	for _, key := range keys {
		add(key)
	}
	return ordered
}
