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
	// pageVitalsInterval 是官网 web vitals logger 从 load 事件结束起发出积攒记录的周期
	pageVitalsInterval = 30 * time.Second
	// pageCountersDelay 是官网 clearcut 传输从计数窗口内第一次记录到发出计数上报的延迟
	pageCountersDelay = 60 * time.Second
	// pageApplication 是官网页面应用名
	pageApplication = "alkali-makersuite"
	// pageRoute 是官网新对话页的路由
	pageRoute = "prompts/new_chat"
	// pageLCPSelector 是官网新对话页最大内容绘制元素的选择器
	pageLCPSelector = "#glue-cookie-notification-bar-1>p.glue-cookie-notification-bar__text"
)

// pageRouteTimes 是官网会话现场中点 New chat 后路由切换的耗时（毫秒）
var pageRouteTimes = []int{10, 19, 36, 210}

// pageVitals 是一次页面载入的官网 web vitals 上报：load 事件结束后每 pageVitalsInterval 发出一次积攒的记录，第一次为导航计时、绘制、最大内容绘制、首字节与资源统计，之后为页面内路由切换的耗时；clearcut 传输为每次记录计数，计数窗口内第一次记录 pageCountersDelay 后以 2584 发出
type pageVitals struct {
	send      func(clearcutShape, []byte)
	telemetry pageTelemetry
	locale    string
	zone      *time.Location
	// origin 是页面导航开始的时刻，2584 的时间窗口以它为零点
	origin time.Time
	// payload 是载入记录的 1957 载荷字段，按 JSPB 下标存放
	payload []any

	mu     sync.Mutex
	timer  *time.Timer
	closed bool
	// loaded 表示载入记录还没有发出
	loaded bool
	// routes 是积攒的路由切换记录
	routes []any
	// seq 是已记录的 1957 事件数
	seq int
	// counter 是计数上报的定时器，计数窗口没有记录时为 nil，window 是计数窗口的起点（以导航开始为零点的毫秒数），logged 是窗口内的记录数
	counter *time.Timer
	window  int64
	logged  int
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
	vitals := &pageVitals{send: send, telemetry: telemetry, locale: locale, zone: zone, origin: origin, payload: payload, loaded: true}
	at := origin.Add(time.Duration(nav("loadEventEnd"))*time.Millisecond + pageVitalsInterval + time.Duration(5+rand.IntN(15))*time.Millisecond)
	vitals.timer = time.AfterFunc(time.Until(at), vitals.tick)
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

// tick 发出积攒的记录并安排下一次，周期由上一次发出后起算
func (vitals *pageVitals) tick() {
	vitals.mu.Lock()
	defer vitals.mu.Unlock()
	if vitals.closed {
		return
	}
	vitals.flush(time.Now())
	vitals.timer = time.AfterFunc(pageVitalsInterval+time.Duration(rand.IntN(7))*time.Millisecond, vitals.tick)
}

// route 记录页面内路由切换到新对话页：官网点 New chat 后 Angular 路由从开始到结束的耗时，下一次周期发出
func (vitals *pageVitals) route() {
	vitals.mu.Lock()
	defer vitals.mu.Unlock()
	if !vitals.closed {
		took := pageRouteTimes[rand.IntN(len(pageRouteTimes))]
		vitals.routes = append(vitals.routes, []any{pageRoute, pageRoute, took + rand.IntN(took/5+1)})
	}
}

// flush 以 sendBeacon 发出积攒的记录并为它计数，没有记录时不发；调用方持有 mu
func (vitals *pageVitals) flush(now time.Time) {
	var payload []any
	switch {
	case vitals.loaded:
		payload = vitals.payload
		vitals.loaded = false
	case len(vitals.routes) > 0:
		payload = make([]any, 18)
		payload[4] = vitals.payload[4]
		payload[12] = []any{vitals.routes}
		payload[17] = vitals.telemetry.sampling
		vitals.routes = nil
	default:
		return
	}
	vitals.seq++
	vitals.send(clearcutBeacon, vitals.body(payload, vitals.seq, now))
	if vitals.counter == nil {
		if vitals.seq == 1 {
			vitals.window = now.Sub(vitals.origin).Milliseconds()
		}
		vitals.counter = time.AfterFunc(pageCountersDelay, vitals.count)
	}
	vitals.logged++
}

// body 编码第 seq 个 1957 事件为 payload 的上报
func (vitals *pageVitals) body(payload []any, seq int, now time.Time) []byte {
	encoded, _ := scriptJSON(payload)
	event := clearcutEvent(21, now, vitals.zone, encoded)
	event[15] = []any{nil, []any{vitals.telemetry.token}}
	event[20] = seq
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
func (vitals *pageVitals) count() {
	vitals.mu.Lock()
	defer vitals.mu.Unlock()
	if !vitals.closed {
		vitals.sendCounters(time.Now())
	}
}

// sendCounters 发出计数窗口到 now 的 2584 计数并开始下一个窗口；调用方持有 mu
func (vitals *pageVitals) sendCounters(now time.Time) {
	end := now.Sub(vitals.origin).Milliseconds()
	vitals.send(clearcutPlain, vitals.countersBody(vitals.window, vitals.logged, now))
	vitals.window, vitals.logged = end, 0
	vitals.counter.Stop()
	vitals.counter = nil
}

// countersBody 编码 2584 计数：时间窗口从 window 到 now，以导航开始为零点的毫秒数，窗口内记录与发出的 1957 事件各 logged 个
func (vitals *pageVitals) countersBody(window int64, logged int, now time.Time) []byte {
	payload, _ := scriptJSON([]any{nil, 0, window, now.Sub(vitals.origin).Milliseconds(), []any{pageVitalsSource, logged, logged}})
	event := clearcutEvent(16, now, vitals.zone, payload)
	event[15] = []any{nil, []any{vitals.telemetry.token}}
	body, _ := scriptJSON([]any{clearcutClient(vitals.locale, vitals.telemetry.label, 4), pageCountersSource, []any{event}, now.UnixMilli()})
	return []byte(body)
}

// close 像官网页面卸载时一样立即发出积攒的记录与计数窗口内的 2584 计数，再停止上报
func (vitals *pageVitals) close() {
	vitals.mu.Lock()
	defer vitals.mu.Unlock()
	if vitals.closed {
		return
	}
	vitals.closed = true
	vitals.timer.Stop()
	now := time.Now()
	if !vitals.loaded {
		vitals.flush(now)
	}
	if vitals.counter != nil {
		vitals.sendCounters(now)
	}
}
