package aistudio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// clearcutURL 是官网页面 clearcut 上报的地址与固定参数
const clearcutURL = "https://play.google.com/log?format=json&hasfast=true"

// clearcutShape 是官网页面 clearcut 上报的请求形状
type clearcutShape int

const (
	// clearcutBeacon 是 sendBeacon 发出的 no-cors 上报，正文为 text/plain，凭据摘要放在 auth 参数
	clearcutBeacon clearcutShape = iota
	// clearcutPlain 是带 x-goog-authuser 的 cors 上报，正文为 text/plain
	clearcutPlain
	// clearcutGzip 是凭据摘要放在 Authorization 头、带 x-goog-authuser 的 cors 上报，正文为 gzip 压缩的 application/binary
	clearcutGzip
)

// sendClearcut 按官网页面的请求形状在后台发出一次 clearcut 上报
func (runtime *goWAARuntime) sendClearcut(shape clearcutShape, body []byte) {
	hashes, err := NewSigner().OriginHashes(runtime.cookies())
	if err != nil {
		return
	}
	authUser := strconv.Itoa(runtime.options.AuthUser)
	target := clearcutURL + "&authuser=" + authUser
	headers := http.Header{}
	headers.Set("Accept", "*/*")
	headers.Set("Referer", aiStudioOrigin+"/")
	headers.Set("Content-Type", "text/plain;charset=UTF-8")
	headers.Set("Origin", aiStudioOrigin)
	headers.Set("Sec-Fetch-Dest", "empty")
	headers.Set("Sec-Fetch-Mode", "cors")
	headers.Set("Sec-Fetch-Site", "same-site")
	headers.Set("Priority", "u=4")
	switch shape {
	case clearcutBeacon:
		target += "&auth=" + url.QueryEscape(hashes)
		headers.Set("Sec-Fetch-Mode", "no-cors")
		headers.Set("Priority", "u=6")
	case clearcutPlain:
		headers.Set("X-Goog-Authuser", authUser)
	case clearcutGzip:
		headers.Set("Authorization", hashes)
		headers.Set("X-Goog-Authuser", authUser)
		headers.Set("Content-Encoding", "gzip")
		headers.Set("Content-Type", "application/binary")
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		runtime.drain(runtime.do(ctx, http.MethodPost, target, body, headers))
	}()
}

// clearcutClient 返回官网页面上报正文的客户端信息，label 为前端构建标签或客户端版本，kind 为日志客户端类别
func clearcutClient(locale string, label any, kind int) []any {
	return []any{1, nil, nil, nil, nil, nil, nil, nil, nil, nil, []any{nil, nil, nil, nil, locale, nil, label, nil, nil, []any{kind, 0}}}
}

// clearcutEvent 返回官网页面上报中一个事件：0 为毫秒时间，7 为载荷，14 为本地时区相对 UTC 秒数的相反数，其余位置由调用方填写
func clearcutEvent(size int, now time.Time, zone *time.Location, payload string) []any {
	event := make([]any, size)
	event[0] = now.UnixMilli()
	event[7] = payload
	_, offset := now.In(zone).Zone()
	event[14] = -offset
	return event
}

// scriptLoaderCall 是官网前端主程序 Closure 脚本加载函数把 script 元素追加到文档的调用
const scriptLoaderCall = `(document.getElementsByTagName("HEAD")[0]||document.documentElement).appendChild(`

// sourcePosition 是脚本中一处代码的行号与列号，均从 1 开始
type sourcePosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// scriptLoaderPosition 返回官网前端主程序中第一个脚本加载调用 appendChild 的位置，Firefox 以它作为 BotGuard 解释器脚本 CSP 报告的行列号
func scriptLoaderPosition(bundle []byte) (sourcePosition, error) {
	index := bytes.Index(bundle, []byte(scriptLoaderCall))
	if index < 0 {
		return sourcePosition{}, fmt.Errorf("前端主程序缺少脚本加载调用")
	}
	lineStart := bytes.LastIndexByte(bundle[:index], '\n') + 1
	column := index - lineStart + strings.Index(scriptLoaderCall, "appendChild") + 1
	return sourcePosition{Line: bytes.Count(bundle[:index], []byte("\n")) + 1, Column: column}, nil
}

// cspReport 是官网文档 report-only CSP 下页面载入 BotGuard 解释器脚本时 Firefox 发出的违规报告
type cspReport struct {
	// policy 是文档响应的 Content-Security-Policy-Report-Only
	policy string
	// document 是文档地址
	document string
	// bundle 是前端主程序地址
	bundle string
	// loader 是前端主程序中脚本加载调用的位置
	loader sourcePosition
}

// body 返回解释器脚本 script 违规报告的正文与报告地址：策略按 Firefox 序列化为以 "; " 分隔的指令，report-uri 按文档地址解析为绝对地址
func (report cspReport) body(script string) (string, []byte, error) {
	base, err := url.Parse(report.document)
	if err != nil {
		return "", nil, err
	}
	var directives []string
	target := ""
	for _, directive := range strings.Split(report.policy, ";") {
		directive = strings.TrimSpace(directive)
		if uri, ok := strings.CutPrefix(directive, "report-uri "); ok {
			resolved, err := base.Parse(strings.TrimSpace(uri))
			if err != nil {
				return "", nil, err
			}
			target = resolved.String()
			directive = "report-uri " + target
		}
		if directive != "" {
			directives = append(directives, directive)
		}
	}
	if target == "" {
		return "", nil, fmt.Errorf("report-only CSP 缺少 report-uri")
	}
	encoded, err := scriptJSON(map[string]any{"csp-report": map[string]any{
		"blocked-uri": script, "column-number": report.loader.Column, "disposition": "report", "document-uri": report.document,
		"effective-directive": "script-src-elem", "line-number": report.loader.Line, "original-policy": strings.Join(directives, "; "),
		"referrer": "", "source-file": report.bundle, "status-code": 200, "violated-directive": "script-src-elem",
	}})
	return target, []byte(encoded), err
}

// sendScriptReport 像 Firefox 一样以不带凭据的匿名请求发出解释器脚本的 CSP 违规报告，报告的策略与地址已在页面载入时校验
func (runtime *goWAARuntime) sendScriptReport(script string) {
	target, body, _ := runtime.scriptReport.body(script)
	headers := http.Header{}
	headers.Set("Accept", "*/*")
	headers.Set("Content-Type", "application/csp-report")
	headers.Set("Origin", aiStudioOrigin)
	headers.Set("Sec-Fetch-Dest", "report")
	headers.Set("Sec-Fetch-Mode", "no-cors")
	headers.Set("Sec-Fetch-Site", "same-origin")
	headers.Set("Priority", "u=4")
	go func() {
		ctx, cancel := context.WithTimeout(withRequestClass(context.Background(), requestClass{anonymous: true}), time.Minute)
		defer cancel()
		runtime.drain(runtime.do(ctx, http.MethodPost, target, body, headers))
	}()
}

// pageTelemetry 是官网页面 WIZ 数据中 clearcut 上报用到的值
type pageTelemetry struct {
	// label 是前端构建标签
	label string
	// experiments 是页面生效的实验 ID
	experiments []int64
	// token 是 1957 与 2584 事件携带的实验标记串
	token string
	// sampling 是 1957 载荷末项的采样配置
	sampling json.RawMessage
	// client 是 1957 请求末项的客户端编号
	client int64
	// bundle 是前端主程序地址中 k 参数的值
	bundle string
}

// wizDataPattern 匹配官网页面以 application/json 脚本内联的 WIZ 数据
var wizDataPattern = regexp.MustCompile(`(?s)<script type="application/json"[^>]*>(\{.*?\})</script>`)

// wizMessage 解析 WIZ 数据中以 %.@. 代替开头方括号的 JSPB 数组
func wizMessage(value string) (json.RawMessage, error) {
	if !strings.HasPrefix(value, "%.@.") {
		return nil, fmt.Errorf("不是 JSPB 数组: %.20q", value)
	}
	raw := json.RawMessage("[" + value[len("%.@."):])
	if !json.Valid(raw) {
		return nil, fmt.Errorf("JSPB 数组无效: %.20q", value)
	}
	return raw, nil
}

// parsePageTelemetry 读取官网页面 WIZ 数据中的构建标签（cfb2h）、实验 ID（goX6ee）、实验标记串（TSDtV）、1957 采样配置（UUFaWc）与客户端编号（S6lZl）
func parsePageTelemetry(body []byte, bundle string) (pageTelemetry, error) {
	for _, match := range wizDataPattern.FindAllSubmatch(body, -1) {
		var data struct {
			Label       string  `json:"cfb2h"`
			Experiments []int64 `json:"goX6ee"`
			Token       string  `json:"TSDtV"`
			Sampling    string  `json:"UUFaWc"`
			Client      int64   `json:"S6lZl"`
		}
		if json.Unmarshal(match[1], &data) != nil || data.Label == "" {
			continue
		}
		flags, err := wizMessage(data.Token)
		if err != nil {
			return pageTelemetry{}, fmt.Errorf("AI Studio 页面 TSDtV %w", err)
		}
		var nested [][][]json.RawMessage
		if err := json.Unmarshal(flags, &nested); err != nil || len(nested) == 0 || len(nested[0]) == 0 || len(nested[0][0]) < 3 {
			return pageTelemetry{}, fmt.Errorf("AI Studio 页面 TSDtV 缺少实验标记串")
		}
		telemetry := pageTelemetry{label: data.Label, experiments: data.Experiments, client: data.Client}
		if err := json.Unmarshal(nested[0][0][2], &telemetry.token); err != nil {
			return pageTelemetry{}, fmt.Errorf("AI Studio 页面 TSDtV 实验标记串: %w", err)
		}
		if telemetry.sampling, err = wizMessage(data.Sampling); err != nil {
			return pageTelemetry{}, fmt.Errorf("AI Studio 页面 UUFaWc %w", err)
		}
		if len(telemetry.experiments) == 0 || telemetry.client == 0 {
			return pageTelemetry{}, fmt.Errorf("AI Studio 页面缺少 goX6ee 或 S6lZl")
		}
		if parsed, err := url.Parse(bundle); err == nil {
			for _, segment := range strings.Split(parsed.Path, "/") {
				if key, ok := strings.CutPrefix(segment, "k="); ok {
					telemetry.bundle = key
				}
			}
		}
		if telemetry.bundle == "" {
			return pageTelemetry{}, fmt.Errorf("AI Studio 前端主程序地址缺少 k 参数: %s", bundle)
		}
		return telemetry, nil
	}
	return pageTelemetry{}, fmt.Errorf("AI Studio 页面缺少 WIZ 数据")
}
