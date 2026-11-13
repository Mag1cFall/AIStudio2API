package aistudio

import (
	"math/rand/v2"
	"sync"
	"time"
)

const (
	// pageVitalsSource 是官网页面 web vitals logger 的 log source
	pageVitalsSource = 1957
	// pageCountersSource 是官网 clearcut 传输为 web vitals logger 上报发送计数的 log source
	pageCountersSource = 2584
	// pageVitalsDelay 是官网 web vitals logger 在 load 事件结束后发出首次上报的延迟
	pageVitalsDelay = 30 * time.Second
	// pageCountersDelay 是官网 clearcut 传输在 web vitals 上报后发出计数上报的延迟
	pageCountersDelay = 60 * time.Second
	// pageApplication 是官网页面应用名
	pageApplication = "alkali-makersuite"
	// pageRoute 是官网新对话页的路由
	pageRoute = "prompts/new_chat"
	// pageLCPSelector 是官网新对话页最大内容绘制元素的选择器
	pageLCPSelector = "#glue-cookie-notification-bar-1>p.glue-cookie-notification-bar__text"
)

// pageVitals 是一次页面载入的官网 web vitals 上报：load 事件结束 pageVitalsDelay 后以 1957 发出导航计时、绘制、最大内容绘制、首字节与资源统计，再过 pageCountersDelay 由传输层以 2584 发出这次上报的计数
type pageVitals struct {
	send      func(clearcutShape, []byte)
	telemetry pageTelemetry
	locale    string
	zone      *time.Location
	// origin 是页面导航开始的时刻，2584 的时间窗口以它为零点
	origin time.Time
	// payload 是 1957 载荷字段，按 JSPB 下标存放
	payload []any

	mu     sync.Mutex
	timer  *time.Timer
	closed bool
}

// navigationInt 返回导航计时中的整数毫秒值
func navigationInt(navigation map[string]any, name string) int {
	value, _ := navigation[name].(int)
	return value
}

// newPageVitals 按 BotGuard 读取的同一份导航计时建立页面载入的 web vitals 上报并安排发送；首次内容绘制取 DOMContentLoaded 到 load 之间官网现场所在的区段，最大内容绘制为同时出现的 Cookie 提示文字，资源统计按官网现场的类别与耗时范围抽样
func newPageVitals(send func(clearcutShape, []byte), telemetry pageTelemetry, locale string, zone *time.Location, origin time.Time, navigation map[string]any, path string) *pageVitals {
	nav := func(name string) int { return navigationInt(navigation, name) }
	interactive, complete := nav("domInteractive"), nav("domComplete")
	paint := interactive + int(float64(complete-interactive)*(0.15+0.35*rand.Float64()))
	firstByte := nav("responseStart")
	lookup, connect, connected := nav("domainLookupStart"), nav("connectStart"), nav("connectEnd")
	payload := make([]any, 18)
	payload[4] = []any{nil, nil, "aistudio.google.com", pageApplication + "-" + pageRoute, pageApplication, path, nil, 3, pageVitalsID(time.Now()), telemetry.bundle, 1}
	payload[7] = []any{1, nav("startTime"), nav("fetchStart"), lookup, nav("domainLookupEnd"), connect, connected, nav("requestStart"), firstByte, nav("responseEnd"), interactive, complete, nav("loadEventStart"), nav("loadEventEnd")}
	payload[8] = []any{nil, paint, nil, paint, firstByte}
	payload[9] = []any{1, 200 + rand.IntN(100), nil, nil, 9 + rand.IntN(2), 800 + rand.IntN(150), 6, 1300 + rand.IntN(1600), 8 + rand.IntN(2), 1400 + rand.IntN(250)}
	payload[10] = []any{nil, paint}
	payload[12] = []any{[]any{[]any{"", pageRoute, 1100 + rand.IntN(500)}}}
	payload[13] = []any{
		nil, []any{paint, firstByte, paint - firstByte}, nil,
		[]any{paint, pageLCPSelector, nil, firstByte, 0, 0, paint - firstByte},
		[]any{firstByte, lookup, connect - lookup, connected - connect, firstByte - connected},
	}
	payload[17] = telemetry.sampling
	vitals := &pageVitals{send: send, telemetry: telemetry, locale: locale, zone: zone, origin: origin, payload: payload}
	at := origin.Add(time.Duration(nav("loadEventEnd"))*time.Millisecond + pageVitalsDelay + time.Duration(5+rand.IntN(15))*time.Millisecond)
	vitals.timer = time.AfterFunc(time.Until(at), vitals.report)
	return vitals
}

// pageVitalsID 返回官网 web vitals logger 的页面实例编号：当前毫秒数与一个随机小数的 36 进制拼接
func pageVitalsID(now time.Time) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	var prefix []byte
	for value := uint64(now.UnixMilli()); value > 0; value /= 36 {
		prefix = append([]byte{digits[value%36]}, prefix...)
	}
	fraction := make([]byte, 10+rand.IntN(2))
	for index := range fraction {
		fraction[index] = digits[rand.IntN(36)]
	}
	return string(prefix) + string(fraction)
}

// report 发出 1957 上报并安排 2584 计数上报
func (vitals *pageVitals) report() {
	vitals.mu.Lock()
	defer vitals.mu.Unlock()
	if vitals.closed {
		return
	}
	now := time.Now()
	window := now.Sub(vitals.origin).Milliseconds()
	vitals.send(clearcutBeacon, vitals.body(now))
	vitals.timer = time.AfterFunc(pageCountersDelay, func() { vitals.count(window) })
}

// body 编码 1957 上报
func (vitals *pageVitals) body(now time.Time) []byte {
	encoded, _ := scriptJSON(vitals.payload)
	event := clearcutEvent(21, now, vitals.zone, encoded)
	event[15] = []any{nil, []any{vitals.telemetry.token}}
	event[20] = 1
	request := make([]any, 18)
	request[0] = clearcutClient(vitals.locale, vitals.telemetry.label, 3)
	request[1] = pageVitalsSource
	request[2] = []any{event}
	request[3] = now.UnixMilli()
	request[17] = []any{[]any{nil, []any{nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, vitals.telemetry.client}}, 9}
	body, _ := scriptJSON(request)
	return []byte(body)
}

// count 发出 2584 计数
func (vitals *pageVitals) count(window int64) {
	vitals.mu.Lock()
	defer vitals.mu.Unlock()
	if !vitals.closed {
		vitals.send(clearcutPlain, vitals.countersBody(window, time.Now()))
	}
}

// countersBody 编码 2584 计数：时间窗口从 1957 上报到 now，以导航开始为零点的毫秒数，窗口内有一个 1957 事件与一次请求
func (vitals *pageVitals) countersBody(window int64, now time.Time) []byte {
	payload, _ := scriptJSON([]any{nil, 0, window, now.Sub(vitals.origin).Milliseconds(), []any{pageVitalsSource, 1, 1}})
	event := clearcutEvent(16, now, vitals.zone, payload)
	event[15] = []any{nil, []any{vitals.telemetry.token}}
	body, _ := scriptJSON([]any{clearcutClient(vitals.locale, vitals.telemetry.label, 4), pageCountersSource, []any{event}, now.UnixMilli()})
	return []byte(body)
}

// close 停止尚未发出的上报
func (vitals *pageVitals) close() {
	vitals.mu.Lock()
	defer vitals.mu.Unlock()
	vitals.closed = true
	vitals.timer.Stop()
}
