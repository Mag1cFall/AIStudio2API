package api

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
)

// fieldOrderJSON 是各公开协议输出对象的官方字段顺序表
//
//go:embed field_order.json
var fieldOrderJSON []byte

// fieldOrder 是一个公开协议输出对象的官方字段顺序：Global 为全协议统一的先后，Kinds 按对象种类给出先后，TypeFirst 为 type 在前、其余按字母序
type fieldOrder struct {
	Global    []string            `json:"global"`
	Kinds     map[string][]string `json:"kinds"`
	TypeFirst bool                `json:"typeFirst"`
	Opaque    []string            `json:"opaque"`
	global    map[string]int
	kinds     map[string]map[string]int
}

// fieldOrders 是 gemini、chat、responses、anthropic 与 interactions 输出的字段顺序
var fieldOrders = loadFieldOrders()

// loadFieldOrders 解析内嵌的字段顺序表
func loadFieldOrders() map[string]*fieldOrder {
	var orders map[string]*fieldOrder
	if err := json.Unmarshal(fieldOrderJSON, &orders); err != nil {
		panic(err)
	}
	for _, order := range orders {
		order.global = fieldRanks(order.Global)
		order.kinds = make(map[string]map[string]int, len(order.Kinds))
		for kind, keys := range order.Kinds {
			order.kinds[kind] = fieldRanks(keys)
		}
	}
	return orders
}

// fieldRanks 返回字段名到先后位置的映射
func fieldRanks(keys []string) map[string]int {
	if len(keys) == 0 {
		return nil
	}
	ranks := make(map[string]int, len(keys))
	for index, key := range keys {
		ranks[key] = index
	}
	return ranks
}

// officialOrder 包装输出值，编码时其中的对象按 protocol 的官方字段顺序排列
func officialOrder(protocol string, value any) any {
	return orderedJSON{value: value, order: fieldOrders[protocol]}
}

// orderedJSON 是按官方字段顺序编码的输出值，parent 是它所在的字段名
type orderedJSON struct {
	value  any
	order  *fieldOrder
	parent string
}

// MarshalJSON 按字段顺序表编码对象，数组逐项编码，其余值与 encoding/json 相同
func (o orderedJSON) MarshalJSON() ([]byte, error) {
	if _, ok := o.value.(json.Marshaler); ok || slices.Contains(o.order.Opaque, o.parent) {
		return json.Marshal(o.value)
	}
	value := reflect.ValueOf(o.value)
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return []byte("null"), nil
		}
		value = value.Elem()
	}
	switch {
	case value.Kind() == reflect.Map && value.Type().Key().Kind() == reflect.String:
		if value.IsNil() {
			return []byte("null"), nil
		}
		keys := make([]string, 0, value.Len())
		for _, key := range value.MapKeys() {
			keys = append(keys, key.String())
		}
		ranks := o.order.ranksFor(value, o.parent)
		slices.SortFunc(keys, func(a, b string) int { return o.order.compare(ranks, a, b) })
		var buffer bytes.Buffer
		buffer.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				buffer.WriteByte(',')
			}
			name, _ := json.Marshal(key)
			item, err := json.Marshal(orderedJSON{value: value.MapIndex(reflect.ValueOf(key).Convert(value.Type().Key())).Interface(), order: o.order, parent: key})
			if err != nil {
				return nil, err
			}
			buffer.Write(name)
			buffer.WriteByte(':')
			buffer.Write(item)
		}
		buffer.WriteByte('}')
		return buffer.Bytes(), nil
	case (value.Kind() == reflect.Slice || value.Kind() == reflect.Array) && value.Type().Elem().Kind() != reflect.Uint8:
		if value.Kind() == reflect.Slice && value.IsNil() {
			return []byte("null"), nil
		}
		var buffer bytes.Buffer
		buffer.WriteByte('[')
		for index := range value.Len() {
			if index > 0 {
				buffer.WriteByte(',')
			}
			item, err := json.Marshal(orderedJSON{value: value.Index(index).Interface(), order: o.order, parent: o.parent})
			if err != nil {
				return nil, err
			}
			buffer.Write(item)
		}
		buffer.WriteByte(']')
		return buffer.Bytes(), nil
	}
	return json.Marshal(o.value)
}

// ranksFor 返回对象所属种类的字段先后：Global 优先，否则按 type、object 或所在字段名查 Kinds
func (order *fieldOrder) ranksFor(object reflect.Value, parent string) map[string]int {
	if order.global != nil {
		return order.global
	}
	if order.TypeFirst {
		return nil
	}
	for _, field := range []string{"type", "object"} {
		if name := mapString(object, field); name != "" {
			return order.kinds[field+":"+name]
		}
	}
	return order.kinds["key:"+parent]
}

// compare 比较同一对象中的两个字段：表中的字段按表的先后在前，其余按字母序
func (order *fieldOrder) compare(ranks map[string]int, a, b string) int {
	if order.TypeFirst && (a == "type") != (b == "type") {
		if a == "type" {
			return -1
		}
		return 1
	}
	left, leftKnown := ranks[a]
	right, rightKnown := ranks[b]
	switch {
	case leftKnown && rightKnown:
		return left - right
	case leftKnown:
		return -1
	case rightKnown:
		return 1
	}
	return strings.Compare(a, b)
}

// mapString 读取对象中字符串字段的值，原始 JSON 字段按 JSON 字符串解析
func mapString(object reflect.Value, field string) string {
	item := object.MapIndex(reflect.ValueOf(field).Convert(object.Type().Key()))
	if !item.IsValid() {
		return ""
	}
	switch value := item.Interface().(type) {
	case string:
		return value
	case json.RawMessage:
		var name string
		_ = json.Unmarshal(value, &name)
		return name
	}
	return ""
}
