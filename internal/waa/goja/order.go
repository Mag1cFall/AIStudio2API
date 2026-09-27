package goja

import "github.com/Mag1cFall/AIStudio2API/internal/waa/goja/unistring"

// orderOwnKeys 按 SpiderMonkey 的定义顺序重排对象的字符串键，未列出的键按原相对顺序排在其后
func (o *baseObject) orderOwnKeys(names ...string) {
	listed := make(map[unistring.String]bool, len(names))
	ordered := make([]unistring.String, 0, len(o.propNames))
	for _, name := range names {
		key := unistring.NewFromString(name)
		if _, exists := o.values[key]; exists && !listed[key] {
			listed[key] = true
			ordered = append(ordered, key)
		}
	}
	for _, key := range o.propNames {
		if !listed[key] {
			ordered = append(ordered, key)
		}
	}
	o.propNames = ordered
	o.lastSortedPropLen, o.idxPropCount = 0, 0
}

// orderOwnKeys 物化模板属性后按给定顺序重排字符串键
func (o *templatedObject) orderOwnKeys(names ...string) {
	o.materialiseProps()
	o.baseObject.orderOwnKeys(names...)
}

// OrderOwnKeys 按给定顺序重排对象的自有字符串键，未列出的键保持原相对顺序排在其后
func (o *Object) OrderOwnKeys(names []string) {
	if target, ok := o.self.(interface{ orderOwnKeys(...string) }); ok {
		target.orderOwnKeys(names...)
	}
}
