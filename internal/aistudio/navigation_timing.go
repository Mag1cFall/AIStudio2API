package aistudio

import (
	"math"
	"math/rand/v2"
	"time"
)

// pageLoad 是一次以浏览器导航形状请求官网页面的实测结果
type pageLoad struct {
	url      string
	protocol string
	// started 是导航开始的时刻
	started time.Time
	// connection 是文档请求建立连接的实测时刻，复用连接时 dialed 为 false
	connection *connectionTiming
	// end 是读完文档正文时距导航开始的时长
	end     time.Duration
	decoded int
}

// firefoxEncodedRatio 是 Firefox 158 经同一代理载入官网新对话页时压缩正文与解压正文的字节比
const firefoxEncodedRatio = 0.1943

// navigationTiming 按一次页面载入的实测时刻生成 Firefox 158 的 PerformanceNavigationTiming 网络阶段（以导航开始为零点的毫秒数）并返回旧版 timing 的 domLoading：新建连接的 DNS、TCP 与 HTTP 代理 CONNECT 应答取拨号实测，隧道内 TLS 与服务端耗时落在 requestStart 之后，响应各时刻取读完正文的实测时刻，Firefox 内部派发间隔与压缩正文大小取 Firefox 158 现场取值
func navigationTiming(load pageLoad) (map[string]any, int) {
	at := func(moment time.Time) int { return int(moment.Sub(load.started).Milliseconds()) }
	fetch := rand.IntN(2)
	lookup, lookupEnd, connect, connectEnd := fetch, fetch, fetch, fetch
	request := fetch + 2 + rand.IntN(12)
	response := max(request+1, int(load.end.Milliseconds()))
	headers := response
	if timing := load.connection; timing != nil && timing.dialed {
		lookup = fetch + rand.IntN(14)
		lookupEnd = lookup
		if !timing.dnsStart.IsZero() && !timing.dnsDone.IsZero() {
			lookupEnd += int(timing.dnsDone.Sub(timing.dnsStart).Milliseconds())
		}
		connect = lookupEnd + rand.IntN(2)
		connectEnd = connect
		if !timing.connectStart.IsZero() && !timing.connectDone.IsZero() {
			connectEnd += int(timing.connectDone.Sub(timing.connectStart).Milliseconds())
		}
		request = connectEnd + 1 + rand.IntN(4)
		headers = request
		if !timing.tunneled.IsZero() {
			headers = max(request, connectEnd+at(timing.tunneled)-at(timing.connectDone))
		}
		response = max(response, headers)
	}
	encoded := int(math.Round(float64(load.decoded)*firefoxEncodedRatio)) + rand.IntN(13) - 6
	entry := map[string]any{
		"name": load.url, "startTime": 0, "nextHopProtocol": load.protocol,
		"fetchStart": fetch, "domainLookupStart": lookup, "domainLookupEnd": lookupEnd,
		"connectStart": connect, "connectEnd": connectEnd, "secureConnectionStart": connectEnd,
		"requestStart": request, "finalResponseHeadersStart": headers, "responseStart": response, "responseEnd": response,
		"transferSize": encoded + 300, "encodedBodySize": encoded, "decodedBodySize": load.decoded,
	}
	return entry, response + pick(0, 0, 1, 1, 1, 1, 1, 1, 2, 2, 2, 3)
}

// documentTiming 补全导航条目的文档解析与 load 阶段：domInteractive 不早于读完正文与文档子资源实测载入完成（resources，距导航开始）的较晚者，其后执行脚本与页面应用的时长按 Firefox 158 现场分布抽样
func documentTiming(entry map[string]any, resources time.Duration) {
	ready := max(entry["responseEnd"].(int), int(resources.Milliseconds()))
	interactive := ready + max(200, lognormal(6.13, 0.62))
	complete := interactive + lognormal(6.96, 0.48)
	loadEnd := complete + pick(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1)
	entry["domInteractive"] = interactive
	entry["domContentLoadedEventStart"] = interactive
	entry["domContentLoadedEventEnd"] = interactive + pick(0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 4)
	entry["domComplete"] = complete
	entry["loadEventStart"] = complete
	entry["loadEventEnd"] = loadEnd
	entry["duration"] = loadEnd
}

// nextHopProtocol 返回响应协议对应的 ALPN 名称
func nextHopProtocol(proto string) string {
	switch proto {
	case "HTTP/3.0":
		return "h3"
	case "HTTP/2.0":
		return "h2"
	default:
		return "http/1.1"
	}
}

// lognormal 返回对数均值 mu、对数标准差 sigma 的对数正态分布取整样本
func lognormal(mu, sigma float64) int {
	return int(math.Round(math.Exp(mu + sigma*rand.NormFloat64())))
}

// pick 返回等概率取出的一个现场值
func pick(values ...int) int {
	return values[rand.IntN(len(values))]
}
