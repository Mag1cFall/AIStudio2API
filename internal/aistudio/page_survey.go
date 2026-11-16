package aistudio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// feedbackOrigin 是页面 HaTS 问卷代理 iframe 与问卷请求的来源
	feedbackOrigin = "https://" + feedbackHost
	// gapiProxyScript 是问卷代理 iframe 载入的 gapi 代理脚本
	gapiProxyScript = "https://apis.google.com/js/googleapis.proxy.js?onload=startup"
	// surveyProductID 是前端主程序创建 HaTS 服务时的产品 ID
	surveyProductID = "5292923"
	// surveyStartupKey 是反馈服务脚本读取问卷启动配置的 API key
	surveyStartupKey = "AIzaSyCB6OnnfuitFnaYWu4BvtGKaoLFk4cm-GE"
	// surveyLibraryVersion 是反馈服务脚本的问卷库版本
	surveyLibraryVersion = 438217234
	// surveyLanguage 是前端主程序创建 HaTS 服务时的问卷语言
	surveyLanguage = "en-US"
	// surveyModelDelay 是需要模型名的问卷在回答结束后等待的时长，期间开始新的回答即取消
	surveyModelDelay = 15 * time.Second
)

// pageSurvey 是 /prompts 页面回答正常结束后参与抽取的一份 HaTS 问卷
type pageSurvey struct {
	id     string
	weight int
	// model 表示问卷只在对话只用过一个模型时参与，抽中后延迟 surveyModelDelay 请求
	model bool
}

// chatSurveys 是前端主程序 HaTS 配置中在 /prompts 页面回答结束时参与抽取的问卷：前两项不限路径，第三项限 /prompts 且需要模型名
var chatSurveys = []pageSurvey{
	{id: "aDN5do7X05EkxwRzbsZ80SRWcZYV", weight: 50},
	{id: "rtXjx6tD05EkxwRzbsZ80Q7A5Ahj", weight: 50},
	{id: "Dmtva3pY05EkxwRzbsZ80Nhsckdf", weight: 80, model: true},
}

// gapiClientModulePattern 匹配 gapi client 模块地址中的模块版本路径与 rs 参数
var gapiClientModulePattern = regexp.MustCompile(`^https://apis\.google\.com(/_/scs/abc-static/_/js/k=[^/]+)/m=client/rt=j/sv=1/d=1/ed=1/rs=([^/]+)/`)

// pageSurveys 是页面 HaTS 问卷的状态：triggered 为本页已请求过的问卷，models 为当前对话用过的模型，timer 为等待中的延迟请求，frame 载入问卷代理 iframe 后 framed 为其地址，载入失败时为空
type pageSurveys struct {
	mu        sync.Mutex
	triggered map[string]bool
	models    map[string]bool
	timer     *time.Timer
	frame     sync.Once
	framed    string
}

// stop 取消等待中的延迟请求，调用方持有 mu
func (surveys *pageSurveys) stop() {
	if surveys.timer != nil {
		surveys.timer.Stop()
		surveys.timer = nil
	}
}

// close 在页面关闭时取消等待中的延迟请求
func (surveys *pageSurveys) close() {
	surveys.mu.Lock()
	surveys.stop()
	surveys.mu.Unlock()
}

// surveyStarted 在页面为 turn 发出 GenerateContent 时取消等待中的问卷请求并记下对话用过的模型，新对话的第一次回答先清空
func (runtime *goWAARuntime) surveyStarted(turn *pageTurn) {
	surveys := &runtime.surveys
	surveys.mu.Lock()
	defer surveys.mu.Unlock()
	surveys.stop()
	if surveys.models == nil || turn.turns == 1 && !turn.rerun {
		surveys.models = map[string]bool{}
	}
	surveys.models[turn.model] = true
}

// surveyEnded 在 GenerateContent 响应正文关闭时按官网页面处理问卷：回答正常结束（completed）时从本页尚未请求过的问卷中按权重抽取一份，需要模型名的问卷只在对话只用过一个模型时参与、抽中后延迟 surveyModelDelay 请求，其余立即请求；回答被取消或出错时只取消等待中的请求
func (runtime *goWAARuntime) surveyEnded(completed bool) {
	surveys := &runtime.surveys
	surveys.mu.Lock()
	defer surveys.mu.Unlock()
	surveys.stop()
	if !completed {
		return
	}
	var candidates []pageSurvey
	total := 0
	for _, survey := range chatSurveys {
		if !surveys.triggered[survey.id] && (!survey.model || len(surveys.models) == 1) {
			candidates = append(candidates, survey)
			total += survey.weight
		}
	}
	if total == 0 {
		return
	}
	pick := rand.IntN(total)
	for _, survey := range candidates {
		if pick -= survey.weight; pick >= 0 {
			continue
		}
		if !survey.model {
			surveys.trigger(survey.id)
			go runtime.requestSurvey(survey.id)
			return
		}
		var timer *time.Timer
		timer = time.AfterFunc(surveyModelDelay, func() {
			surveys.mu.Lock()
			defer surveys.mu.Unlock()
			if surveys.timer != timer {
				return
			}
			surveys.timer = nil
			surveys.trigger(survey.id)
			go runtime.requestSurvey(survey.id)
		})
		surveys.timer = timer
		return
	}
}

// trigger 记下本页已请求过问卷 id，调用方持有 mu
func (surveys *pageSurveys) trigger(id string) {
	if surveys.triggered == nil {
		surveys.triggered = map[string]bool{}
	}
	surveys.triggered[id] = true
}

// requestSurvey 像页面的反馈服务一样请求问卷 id：约 25 ms 后开始，本页第一次请求时先载入问卷代理 iframe；读取问卷启动配置，配置开启问卷时请求问卷；失败不影响页面
func (runtime *goWAARuntime) requestSurvey(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	time.Sleep(time.Duration(20+rand.IntN(15)) * time.Millisecond)
	surveys := &runtime.surveys
	surveys.frame.Do(func() {
		frame, err := runtime.loadSurveyFrame(ctx)
		if err != nil {
			return
		}
		surveys.framed = frame
	})
	if surveys.framed == "" {
		return
	}
	config, err := runtime.surveyRPC(ctx, surveys.framed, "startup_config", surveyStartupKey, `["`+surveyProductID+`",null,1]`)
	if err != nil || !surveyEnabled(config) {
		return
	}
	_, _ = runtime.surveyRPC(ctx, surveys.framed, "trigger", runtime.headers.Get("x-goog-api-key"), surveyTriggerBody(id, time.Now().In(runtime.location())))
}

// loadSurveyFrame 以 iframe 导航载入 feedback-pa 的 gapi 代理页，再按代理页的顺序载入 googleapis.proxy.js 与其中的代理模块，返回代理页地址
func (runtime *goWAARuntime) loadSurveyFrame(ctx context.Context) (string, error) {
	match := gapiClientModulePattern.FindStringSubmatch(runtime.pageCache.notes(gapiLoaderScript).Gapi)
	if match == nil {
		return "", errors.New("页面缺少 gapi client 模块地址")
	}
	frame := feedbackOrigin + "/static/proxy.html?usegapi=1&jsh=" + url.QueryEscape("m;"+match[1]+"/d=1/rs="+match[2]+"/m=__features__")
	if !runtime.navigateFrame(ctx, frame) {
		return "", errors.New("问卷代理页载入失败")
	}
	script := func(target string) error {
		headers := subresourceHeaders(target, "*/*", "script", "")
		headers.Set("Referer", feedbackOrigin+"/")
		return runtime.fetchCached(ctx, target, headers)
	}
	if err := script(gapiProxyScript); err != nil {
		return "", err
	}
	module := "https://apis.google.com" + match[1] + "/m=googleapis_proxy/rt=j/sv=1/d=1/ed=1/rs=" + match[2] + "/cb=gapi.loaded_0?le=" + runtime.pageCache.notes(gapiProxyScript).GapiProxy
	if err := script(module); err != nil {
		return "", err
	}
	return frame, nil
}

// surveyRPC 从问卷代理页以 gapi 请求形状向 feedback-pa 发出一个问卷请求，返回 HTTP 200 的响应正文
func (runtime *goWAARuntime) surveyRPC(ctx context.Context, frame, method, key, body string) ([]byte, error) {
	authorization, err := NewSigner().AuthorizationAt(runtime.cookies(), time.Now())
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set("Accept", "*/*")
	headers.Set("X-ClientDetails", runtime.clientDetails)
	headers.Set("Authorization", authorization)
	headers.Set("X-Goog-AuthUser", strconv.Itoa(runtime.options.AuthUser))
	headers.Set("X-Requested-With", "XMLHttpRequest")
	headers.Set("X-JavaScript-User-Agent", "google-api-javascript-client/1.1.0")
	headers.Set("X-Goog-Api-Key", key)
	headers.Set("Content-Type", "application/json+protobuf")
	headers.Set("X-Origin", aiStudioOrigin)
	headers.Set("X-Referer", aiStudioOrigin)
	headers.Set("X-Goog-Encode-Response-If-Executable", "base64")
	headers.Set("Origin", feedbackOrigin)
	headers.Set("Referer", frame)
	headers.Set("Sec-Fetch-Dest", "empty")
	headers.Set("Sec-Fetch-Mode", "cors")
	headers.Set("Sec-Fetch-Site", "same-origin")
	response, err := runtime.do(ctx, http.MethodPost, feedbackOrigin+"/v1/survey/"+method+"?key="+key, []byte(body), headers)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("问卷请求 %s 返回 HTTP %d", method, response.StatusCode)
	}
	return data, nil
}

// surveyEnabled 返回问卷启动配置是否开启问卷：第 1 字段消息的第 1 字段为真
func surveyEnabled(config []byte) bool {
	var fields []json.RawMessage
	if json.Unmarshal(config, &fields) != nil || len(fields) == 0 {
		return false
	}
	var enabled []json.RawMessage
	if json.Unmarshal(fields[0], &enabled) != nil || len(enabled) == 0 {
		return false
	}
	value := string(enabled[0])
	return value == "1" || value == "true"
}

// surveyTriggerBody 返回请求问卷 id 的 JSPB 正文：触发上下文为问卷 ID 与语言，客户端上下文为 at 所在时区相对 UTC 的秒数（为 0 时不写）与问卷库的平台、支持的问卷形式和版本
func surveyTriggerBody(id string, at time.Time) string {
	_, offset := at.Zone()
	timezone := "[]"
	if offset != 0 {
		timezone = "[" + strconv.Itoa(offset) + "]"
	}
	return `[["` + id + `",["` + surveyLanguage + `"]],[[null,null,` + timezone + `],[1,null,[1,2],` + strconv.Itoa(surveyLibraryVersion) + `]]]`
}

// gapiProxyExtensions 返回 googleapis.proxy.js 配置中声明的 le 列表，以逗号相连；代理页按地址中的 jsh 载入代理模块时不带脚本末尾追加的扩展
func gapiProxyExtensions(script []byte) (string, error) {
	names, ok := gapiExtensions(script)
	if !ok {
		return "", errors.New("googleapis.proxy.js 缺少 le 配置")
	}
	return strings.Join(names, ","), nil
}
