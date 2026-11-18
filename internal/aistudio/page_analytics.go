package aistudio

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// tagManagerOrigin 是 GTM 容器脚本与 gtag 脚本的来源
	tagManagerOrigin = "https://www.googletagmanager.com"
	// analyticsCollectURL 是 gtag 发出 GA4 命中的地址
	analyticsCollectURL = "https://www.google-analytics.com/g/collect"
	// analyticsFlushDelay 是 gtag 从攒下第一个非转化事件到把这一批发出的延迟
	analyticsFlushDelay = 5 * time.Second
	// analyticsConsent 是 Cookie 提示条拒绝后 gtag 命中的同意状态参数
	analyticsConsent = "gcs=G100&gcd=13q3q3l3q5l1&npa=1&dma=0"
)

var (
	// gtmContainerPattern 匹配官网文档 Cookie 提示条回调中插入的 GTM 容器 ID
	gtmContainerPattern = regexp.MustCompile(`https:\\/\\/www\.googletagmanager\.com\\/gtm\.js\?id\\x3d(GTM-[A-Z0-9]+)`)
	// tagTriggerPattern 匹配 GTM 容器脚本中以容器内部编号开头的触发器 ID
	tagTriggerPattern = regexp.MustCompile(`"vtp_uniqueTriggerId":"(\d+)_`)
	// tagHostPattern 匹配 GTM 容器脚本中把 google.com 主机名改写为带后缀主机名的宏
	tagHostPattern = regexp.MustCompile(`"\(\.\*\\\\\.google\)\(\\\\\.com\)","value","\$1([^"$]+)\$2"`)
	// tagFocusedPattern 匹配 gtag 脚本中按需记录、初始化时不分组的实验研究
	tagFocusedPattern = regexp.MustCompile(`switch\(\w+\.studyId\)\{((?:case \d+:)+)\w+=!0`)
)

// gtmContainerURL 返回官网文档 Cookie 提示条回调插入的 GTM 容器脚本地址，文档没有时为空
func gtmContainerURL(body []byte) string {
	match := gtmContainerPattern.FindSubmatch(body)
	if match == nil {
		return ""
	}
	return tagManagerOrigin + "/gtm.js?id=" + string(match[1])
}

// analyticsTag 是 GTM 容器在官网页面载入的一个 GA4 目标与 gtag 为它固定附加的命中参数
type analyticsTag struct {
	// id 是 GA4 衡量 ID
	id string
	// euid 表示命中带 ecid
	euid bool
	// usage 是命中的 _eu
	usage string
	// google 表示命中带 _ng=1
	google bool
	// redact 表示命中带 ir=1
	redact bool
	// signals 表示命中带 ngs=1
	signals bool
	// flags 是命中的 gaf，0 时不带
	flags int
	// iseea 表示事件参数带 iseea
	iseea bool
	// conversion 表示 run_prompt 为转化事件，随即连同攒下的事件发出
	conversion bool
	// rewritten 表示 user_engagement 的 dl 取 GTM 改写主机名后的页面地址
	rewritten bool
	// clickLast 表示 GTM 为该目标先触发事件标签、后触发 all_content_click
	clickLast bool
}

// analyticsTags 是官网 GTM 容器为 aistudio.google.com 依次载入的 GA4 目标
var analyticsTags = []analyticsTag{
	{id: "G-RJSPDF5Y0Q", usage: "EA", redact: true, signals: true, conversion: true, rewritten: true, clickLast: true},
	{id: "G-P1DBVKWT6V", euid: true, usage: "EAAAAGA", google: true, redact: true, flags: 2, iseea: true, conversion: true},
	{id: "G-7ZL2Y67NVH", signals: true, iseea: true},
}

// tagStudy 是 gtag 脚本中的一项实验研究，Arms 依次为实验组、对照组与第二对照组，Shared 的研究在页面上的各个 gtag 之间共用一次分组
type tagStudy struct {
	ID          int      `json:"id"`
	Probability float64  `json:"probability"`
	Arms        [3]int64 `json:"arms"`
	Shared      bool     `json:"shared,omitempty"`
}

// tagScript 是从 GTM 容器脚本或 gtag 脚本读出的生成命中用到的值
type tagScript struct {
	// Build 是脚本构建号
	Build string `json:"build"`
	// Container 是 GTM 容器内部编号
	Container string `json:"container,omitempty"`
	// EEA 是 GTM 按访问地区判定的欧洲经济区标记
	EEA bool `json:"eea,omitempty"`
	// HostSuffix 是 GTM 改写 google.com 主机名时插入的后缀
	HostSuffix string `json:"hostSuffix,omitempty"`
	// Version 是 gtag 目标或 GTM 容器的版本
	Version string `json:"version,omitempty"`
	// Experiments 是标签脚本固定上报的实验 ID
	Experiments []string `json:"experiments,omitempty"`
	// Studies 是标签脚本初始化时按概率分组的实验研究
	Studies []tagStudy `json:"studies,omitempty"`
	// Diagnostics 是标签脚本每次载入进入 tag 诊断抽样的概率（blob 42）
	Diagnostics float64 `json:"diagnostics,omitempty"`
}

// tagBlob 读出 GTM 容器脚本或 gtag 脚本的 blob 与其中的构建号（14）
func tagBlob(body []byte) (map[string]json.RawMessage, string, error) {
	index := bytes.Index(body, []byte(`"blob":`))
	if index < 0 {
		return nil, "", errors.New("标签脚本缺少 blob")
	}
	var blob map[string]json.RawMessage
	if err := json.NewDecoder(bytes.NewReader(body[index+len(`"blob":`):])).Decode(&blob); err != nil {
		return nil, "", err
	}
	var build string
	if json.Unmarshal(blob["14"], &build) != nil || build == "" {
		return nil, "", errors.New("标签脚本缺少构建号")
	}
	return blob, build, nil
}

// parseContainerScript 读出 GTM 容器脚本的构建号、容器内部编号、改写主机名的后缀、blob 22 中按访问地区判定的欧洲经济区标记，以及与 gtag 脚本同样位置的容器版本、实验、实验研究与诊断抽样概率
func parseContainerScript(body []byte) (tagScript, error) {
	blob, _, err := tagBlob(body)
	if err != nil {
		return tagScript{}, err
	}
	match := tagTriggerPattern.FindSubmatch(body)
	if match == nil {
		return tagScript{}, errors.New("GTM 容器脚本缺少内部编号")
	}
	script, err := parseGtagScript(body)
	if err != nil {
		return tagScript{}, err
	}
	script.Container = string(match[1])
	if host := tagHostPattern.FindSubmatch(body); host != nil {
		script.HostSuffix = string(host[1])
	}
	var geo string
	if json.Unmarshal(blob["22"], &geo) == nil {
		decoded, _ := base64.StdEncoding.DecodeString(geo)
		var region map[string]any
		if json.Unmarshal(decoded, &region) == nil {
			script.EEA, _ = region["2"].(bool)
		}
	}
	return script, nil
}

// parseGtagScript 读出 gtag 脚本 blob 中的构建号、目标版本（6）、固定上报的实验（44、67）与初始化时分组的实验研究（56）：按需记录的研究、第三类研究与没有对照组的低概率研究不分组
func parseGtagScript(body []byte) (tagScript, error) {
	blob, build, err := tagBlob(body)
	if err != nil {
		return tagScript{}, err
	}
	script := tagScript{Build: build}
	if json.Unmarshal(blob["6"], &script.Version) != nil || script.Version == "" {
		return tagScript{}, errors.New("gtag 脚本缺少目标版本")
	}
	var constant string
	_ = json.Unmarshal(blob["44"], &constant)
	script.Experiments = strings.FieldsFunc(constant, func(r rune) bool { return r == '~' })
	var extra []string
	_ = json.Unmarshal(blob["67"], &extra)
	script.Experiments = append(script.Experiments, extra...)
	_ = json.Unmarshal(blob["42"], &script.Diagnostics)
	var focused []int
	if match := tagFocusedPattern.FindSubmatch(body); match != nil {
		for _, item := range strings.Split(strings.TrimSuffix(string(match[1]), ":"), ":") {
			if id, err := strconv.Atoi(strings.TrimPrefix(item, "case ")); err == nil {
				focused = append(focused, id)
			}
		}
	}
	var studies []map[string]json.RawMessage
	_ = json.Unmarshal(blob["56"], &studies)
	for _, raw := range studies {
		var study tagStudy
		var kind int
		_ = json.Unmarshal(raw["1"], &study.ID)
		_ = json.Unmarshal(raw["3"], &study.Probability)
		_ = json.Unmarshal(raw["7"], &kind)
		for arm, key := range []string{"4", "5", "6"} {
			_ = json.Unmarshal(raw[key], &study.Arms[arm])
		}
		if kind == 3 || slices.Contains(focused, study.ID) || study.Arms[0] == 0 || study.Probability <= 0 || study.Probability <= 0.5 && study.Arms[1] == 0 {
			continue
		}
		if study.Probability > 0.25 {
			study.Arms[2] = 0
		}
		study.Shared = kind == 2
		script.Studies = append(script.Studies, study)
	}
	return script, nil
}

// assign 按 gtag 的规则为研究抽一次分组：概率高于 0.5 时总在实验组，否则实验组、对照组与第二对照组各以该概率抽中，都没抽中时返回 0
func (study tagStudy) assign() int64 {
	if study.Probability > 0.5 {
		return study.Arms[0]
	}
	draw := rand.Float64()
	for arm, id := range study.Arms {
		if id != 0 && draw < study.Probability*float64(arm+1) {
			return id
		}
	}
	return 0
}

// analyticsEvent 是一个 GA4 事件：gtm 表示由 GTM 标签触发，page 表示带 dp，conversion 表示转化事件，location 不为空时取代页面地址作 dl
type analyticsEvent struct {
	name       string
	location   string
	params     []string
	gtm        bool
	page       bool
	conversion bool
	at         time.Time
}

// analyticsTarget 是页面上一个已载入的 gtag 目标
type analyticsTarget struct {
	tag analyticsTag
	// build、version 与 container 拼成命中的 gtm 参数
	build     string
	version   string
	container string
	tagExp    string
	rcb       int
	ecid      uint32
	sid       int64
	seq       int
	last      time.Time
	queue     []analyticsEvent
	timer     *time.Timer
	// script 是目标的 gtag 脚本地址，cached 表示它取自页面缓存
	script string
	cached bool
	// diagnostics 表示这次载入进入 tag 诊断抽样，pid 是诊断上报的页面编号，hits 与 fallbacks 是已发出的命中与改发到 www.google.com 的命中数
	diagnostics     bool
	pid             int64
	hits, fallbacks int
}

// pageAnalytics 是官网页面上 GTM 与 gtag 的 GA4 上报
type pageAnalytics struct {
	runtime *goWAARuntime
	// container 是文档中 GTM 容器脚本的地址
	container string
	// location 与 path 是命中的 dl 与 dp
	location string
	path     string
	screen   string
	language string

	mu     sync.Mutex
	start  time.Time
	closed bool
	eea    bool
	// rewritten 是 GTM 改写主机名后的页面地址
	rewritten string
	cid       string
	shared    map[int]int64
	targets   []*analyticsTarget
	// loaded 表示 GTM 容器与各 gtag 脚本的载入已结束，此后事件只交给已载入的目标
	loaded bool
	// waiting 是载入结束前页面发生、载入结束后补记的事件
	waiting []func()
}

// newPageAnalytics 按官网文档 body 与文档地址 target 建立页面的 GA4 上报
func newPageAnalytics(runtime *goWAARuntime, body []byte, target string) *pageAnalytics {
	address, _ := url.Parse(target)
	pagePath := address.Path
	width, _ := numberValue(runtime.profile.Screen["width"])
	height, _ := numberValue(runtime.profile.Screen["height"])
	return &pageAnalytics{
		runtime: runtime, container: gtmContainerURL(body), location: aiStudioOrigin + pagePath, path: pagePath + "/",
		screen: strconv.Itoa(int(width)) + "x" + strconv.Itoa(int(height)), language: strings.ToLower(runtime.profile.Locale), shared: map[int]int64{},
	}
}

// load 在 Cookie 提示条载入回调时像官网页面一样记下 gtm.start，随后载入 GTM 容器脚本，再并发载入各 gtag 脚本，每个 gtag 载完后发出 page_view；失败不影响页面与其余目标
func (analytics *pageAnalytics) load(ctx context.Context) {
	if analytics == nil {
		return
	}
	defer analytics.finishLoad()
	if analytics.container == "" {
		return
	}
	analytics.mu.Lock()
	analytics.start = time.Now()
	analytics.mu.Unlock()
	pauseContext(ctx, 100*time.Millisecond+rand.N(100*time.Millisecond))
	runtime := analytics.runtime
	containerCached := runtime.pageCache.notes(analytics.container).Tags != nil
	if runtime.fetchCached(ctx, analytics.container, subresourceHeaders(analytics.container, "*/*", "script", "")) != nil {
		return
	}
	container := runtime.pageCache.notes(analytics.container).Tags
	analytics.mu.Lock()
	analytics.eea = container.EEA
	if container.HostSuffix != "" {
		analytics.rewritten = strings.Replace(analytics.location, ".google.com/", ".google"+container.HostSuffix+".com/", 1) + "/"
	}
	analytics.mu.Unlock()
	pauseContext(ctx, 70*time.Millisecond+rand.N(150*time.Millisecond))
	if tagSample() >= 1-container.Diagnostics {
		analytics.mu.Lock()
		experiments := analytics.experiments(container)
		analytics.mu.Unlock()
		go func() {
			pauseContext(ctx, 40*time.Millisecond+rand.N(10*time.Millisecond))
			analytics.diagnoseContainer(container, experiments, containerCached)
		}()
	}
	var group sync.WaitGroup
	for index, tag := range analyticsTags {
		group.Go(func() {
			pauseContext(ctx, time.Duration(index)*(10*time.Millisecond+rand.N(20*time.Millisecond)))
			target := tagManagerOrigin + "/gtag/js?id=" + tag.id + "&cx=c&gtm=4e" + container.Build
			cached := runtime.pageCache.notes(target).Tags != nil
			if runtime.fetchCached(ctx, target, subresourceHeaders(target, "*/*", "script", "")) != nil {
				return
			}
			script := runtime.pageCache.notes(target).Tags
			pauseContext(ctx, 90*time.Millisecond+rand.N(60*time.Millisecond))
			analytics.ready(tag, container, script, target, cached)
		})
	}
	group.Wait()
}

// finishLoad 记下载入结束并把此前发生的事件补记给已载入的目标
func (analytics *pageAnalytics) finishLoad() {
	analytics.mu.Lock()
	defer analytics.mu.Unlock()
	analytics.loaded = true
	if !analytics.closed {
		for _, replay := range analytics.waiting {
			replay()
		}
	}
	analytics.waiting = nil
}

// ready 在 gtag 目标载入后按页面共用与本目标的实验分组定下命中参数，按诊断抽样在 gtm.init 时发出 tag 诊断，再发出 page_view；address 是 gtag 脚本地址，cached 表示它取自页面缓存
func (analytics *pageAnalytics) ready(tag analyticsTag, container *tagScript, script *tagScript, address string, cached bool) {
	analytics.mu.Lock()
	defer analytics.mu.Unlock()
	if analytics.closed {
		return
	}
	now := time.Now()
	target := &analyticsTarget{
		tag: tag, build: container.Build, version: script.Version, container: container.Container, tagExp: analytics.experiments(script),
		rcb: rand.IntN(20), ecid: rand.Uint32() >> 1, sid: now.Unix(), last: now, script: address, cached: cached,
	}
	if tagSample() >= 1-script.Diagnostics {
		target.diagnostics, target.pid = true, rand.Int64N(1<<31)
		analytics.diagnose(target, false)
	}
	first := analytics.cid == ""
	if first {
		analytics.cid = strconv.FormatUint(uint64(rand.Uint32()>>1), 10) + "." + strconv.FormatInt(now.Unix(), 10)
	}
	analytics.targets = append(analytics.targets, target)
	view := analyticsEvent{name: "page_view", gtm: true, at: now, params: []string{"_fv=1"}}
	if first {
		view.params = append(view.params, "_nsi=1")
	}
	view.params = append(view.params, "_ss=1")
	view.params = append(view.params, analytics.eventParams(tag, "ep.percent_scrolled=0", "ep.scroll_increment=0", "ep.scroll_instance=1")...)
	analytics.send(target, []analyticsEvent{view}, now)
}

// experiments 按页面上各标签共用的分组与本脚本自己的分组返回标签脚本上报的实验 ID，按数值排序以 ~ 连接；调用方持有 mu
func (analytics *pageAnalytics) experiments(script *tagScript) string {
	experiments := slices.Clone(script.Experiments)
	for _, study := range script.Studies {
		id, ok := analytics.shared[study.ID]
		if !study.Shared || !ok {
			id = study.assign()
		}
		if study.Shared {
			analytics.shared[study.ID] = id
		}
		if id != 0 {
			experiments = append(experiments, strconv.FormatInt(id, 10))
		}
	}
	slices.SortFunc(experiments, func(a, b string) int {
		x, _ := strconv.ParseInt(a, 10, 64)
		y, _ := strconv.ParseInt(b, 10, 64)
		return int(x - y)
	})
	return strings.Join(slices.Compact(experiments), "~")
}

// eventParams 返回 GTM 为事件附加的页面参数，后接 extra；调用方持有 mu
func (analytics *pageAnalytics) eventParams(tag analyticsTag, extra ...string) []string {
	eea := strconv.FormatBool(analytics.eea)
	params := []string{"ep.page_locale=default", "ep.is_eea=" + eea}
	if tag.iseea {
		params = append(params, "ep.iseea="+eea)
	}
	params = append(params, "ep.ads_storage=false", "ep.analytics_storage=false", "ep.signed_in=true")
	return append(params, extra...)
}

// record 在载入结束后执行 action，载入结束前发生的事件在载入结束时补记；调用方持有 mu
func (analytics *pageAnalytics) record(action func()) {
	if analytics.closed {
		return
	}
	if !analytics.loaded {
		analytics.waiting = append(analytics.waiting, action)
		return
	}
	action()
}

// rejected 记录在 at 点击 Cookie 提示条的 No thanks：GTM 为各目标记下 user_consent 与 all_content_click，攒入这一批
func (analytics *pageAnalytics) rejected(at time.Time) {
	if analytics == nil {
		return
	}
	analytics.mu.Lock()
	defer analytics.mu.Unlock()
	analytics.record(func() {
		for _, target := range analytics.targets {
			consent := analyticsEvent{name: "user_consent", gtm: true, at: at, params: analytics.eventParams(target.tag, "ep.full_event_name=user_consent", "ep.text=No%20thanks")}
			click := analyticsEvent{name: "all_content_click", gtm: true, page: true, at: at, params: analytics.eventParams(target.tag, "ep.event=user_consent", "ep.click_text=No%20thanks")}
			events := []analyticsEvent{click, consent}
			if target.tag.clickLast {
				events = []analyticsEvent{consent, click}
			}
			analytics.fire(target, events)
		}
	})
}

// clicked 记录在 at 点击 Run：GTM 为各目标记下 run_prompt 与 all_content_click，run_prompt 为转化事件的目标随即连同攒下的事件发出
func (analytics *pageAnalytics) clicked(at time.Time) {
	if analytics == nil {
		return
	}
	analytics.mu.Lock()
	defer analytics.mu.Unlock()
	analytics.record(func() {
		for _, target := range analytics.targets {
			prompt := analyticsEvent{name: "run_prompt", gtm: true, conversion: target.tag.conversion, at: at, params: analytics.eventParams(target.tag, "ep.full_event_name=run_prompt", "ep.text=Run")}
			click := analyticsEvent{name: "all_content_click", gtm: true, page: true, at: at, params: analytics.eventParams(target.tag, "ep.event_action=Run%20Prompt", "ep.event_category=RUN", "ep.event=RUN%20-%20Run%20Prompt", "ep.click_text=Run")}
			events := []analyticsEvent{click, prompt}
			if target.tag.clickLast {
				events = []analyticsEvent{prompt, click}
			}
			analytics.fire(target, events)
		}
	})
}

// newChat 记录在 at 点 New chat 并确认后页面切换到新对话页：GTM 的页面浏览触发器为各目标记下 page_view，攒入这一批
func (analytics *pageAnalytics) newChat(at time.Time) {
	if analytics == nil {
		return
	}
	analytics.mu.Lock()
	defer analytics.mu.Unlock()
	analytics.record(func() {
		for _, target := range analytics.targets {
			analytics.queue(target, analyticsEvent{name: "page_view", gtm: true, at: at, params: analytics.eventParams(target.tag, "ep.percent_scrolled=0", "ep.scroll_increment=25", "ep.scroll_instance=1")})
		}
	})
}

// fire 依次把 GTM 为一次点击触发的事件交给 gtag，后一个标签比前一个晚 0–2 ms；调用方持有 mu
func (analytics *pageAnalytics) fire(target *analyticsTarget, events []analyticsEvent) {
	for index, event := range events {
		if index > 0 {
			event.at = events[index-1].at.Add(time.Duration(rand.IntN(3)) * time.Millisecond)
			events[index] = event
		}
		analytics.queue(target, event)
	}
}

// queue 把事件交给 gtag：转化事件先发出攒下的事件再单独发出，其余事件攒入这一批，第一个事件攒入 analyticsFlushDelay 后发出；调用方持有 mu
func (analytics *pageAnalytics) queue(target *analyticsTarget, event analyticsEvent) {
	if event.conversion {
		analytics.flush(target, event.at)
		analytics.send(target, []analyticsEvent{event}, event.at)
		return
	}
	target.queue = append(target.queue, event)
	if target.timer == nil {
		target.timer = time.AfterFunc(time.Until(event.at.Add(analyticsFlushDelay)), func() {
			analytics.mu.Lock()
			defer analytics.mu.Unlock()
			if !analytics.closed {
				analytics.flush(target, time.Now())
			}
		})
	}
}

// flush 发出目标攒下的事件；调用方持有 mu
func (analytics *pageAnalytics) flush(target *analyticsTarget, now time.Time) {
	if target.timer != nil {
		target.timer.Stop()
		target.timer = nil
	}
	if len(target.queue) == 0 {
		return
	}
	events := target.queue
	target.queue = nil
	analytics.send(target, events, now)
}

// close 像官网页面卸载一样发出各目标攒下的事件与 user_engagement 并停止上报
func (analytics *pageAnalytics) close() {
	if analytics == nil {
		return
	}
	analytics.mu.Lock()
	defer analytics.mu.Unlock()
	if analytics.closed {
		return
	}
	now := time.Now()
	for _, target := range analytics.targets {
		analytics.flush(target, now)
		engagement := analyticsEvent{name: "user_engagement", at: now, params: analytics.eventParams(target.tag)}
		if target.tag.rewritten {
			engagement.location = analytics.rewritten
		}
		analytics.send(target, []analyticsEvent{engagement}, now)
		if target.diagnostics && target.hits > 0 {
			analytics.diagnose(target, true)
		}
	}
	analytics.closed = true
	analytics.waiting = nil
}

// send 以 fetch keepalive 发出目标的一次命中：一个事件时参数全在地址中，多个事件时共用参数在地址中、各事件参数按行放在正文；请求没有得到响应时像 gtag 一样以不带凭据的请求改发到 www.google.com；调用方持有 mu
func (analytics *pageAnalytics) send(target *analyticsTarget, events []analyticsEvent, now time.Time) {
	target.seq++
	target.hits++
	tag := target.tag
	fired := slices.ContainsFunc(events, func(event analyticsEvent) bool { return event.gtm })
	query := []string{"v=2", "tid=" + tag.id, "gtm=" + target.gtmParam(fired), "_p=" + strconv.FormatInt(analytics.start.UnixMilli(), 10), analyticsConsent}
	if tag.euid {
		query = append(query, "ecid="+strconv.FormatUint(uint64(target.ecid), 10))
	}
	if tag.usage != "" {
		query = append(query, "_eu="+tag.usage)
	}
	if tag.google {
		query = append(query, "_ng=1")
	}
	query = append(query, "cid="+analytics.cid, "frm=0", "gtm_up=1")
	if tag.redact {
		query = append(query, "ir=1")
	}
	if tag.signals {
		query = append(query, "ngs=1")
	}
	query = append(query, "pscdl=denied", "rcb="+strconv.Itoa(target.rcb), "sr="+analytics.screen, "ul="+analytics.language)
	if tag.flags > 0 {
		query = append(query, "gaf="+strconv.Itoa(tag.flags))
	}
	seq := "_s=" + strconv.Itoa(target.seq)
	segment := "seg=0"
	if target.seq > 1 {
		segment = "seg=1"
	}
	location := []string{"dt=-", "dl=" + uriComponent(analytics.location)}
	if len(events) == 1 && events[0].location != "" {
		location[1] = "dl=" + uriComponent(events[0].location)
	}
	session := []string{"sid=" + strconv.FormatInt(target.sid, 10), "sct=1", segment}
	tfd := "tfd=" + strconv.FormatInt(now.Sub(analytics.runtime.profile.NavigationStart).Milliseconds(), 10)
	var body []byte
	if len(events) == 1 {
		event := events[0]
		query = append(query, seq, "tag_exp="+target.tagExp)
		query = append(query, location...)
		if event.page {
			query = append(query, "dp="+uriComponent(analytics.path))
		}
		query = append(query, session...)
		query = append(query, "en="+event.name)
		if event.conversion {
			query = append(query, "_c=1")
		}
		query = append(query, event.params...)
		if elapsed := analytics.engagement(target, event.at); elapsed != "" {
			query = append(query, elapsed)
		}
		query = append(query, tfd)
	} else {
		query = append(query, "tag_exp="+target.tagExp)
		query = append(query, location...)
		query = append(query, session...)
		query = append(query, seq, tfd)
		lines := make([]string, 0, len(events))
		for _, event := range events {
			line := append([]string{"en=" + event.name}, event.params...)
			if elapsed := analytics.engagement(target, event.at); elapsed != "" {
				line = append(line, elapsed)
			}
			if event.page {
				line = append(line, "dp="+uriComponent(analytics.path))
			}
			lines = append(lines, strings.Join(line, "&"))
		}
		body = []byte(strings.Join(lines, "\r\n"))
	}
	headers := http.Header{}
	headers.Set("Accept", "*/*")
	if body != nil {
		headers.Set("Content-Type", "text/plain;charset=UTF-8")
	}
	headers.Set("Referer", aiStudioOrigin+"/")
	headers.Set("Origin", aiStudioOrigin)
	headers.Set("Sec-Fetch-Dest", "empty")
	headers.Set("Sec-Fetch-Mode", "no-cors")
	headers.Set("Sec-Fetch-Site", fetchSite(analyticsCollectURL))
	headers.Set("Priority", "u=4")
	headers.Set("Pragma", "no-cache")
	headers.Set("Cache-Control", "no-cache")
	address := analyticsCollectURL + "?" + strings.Join(query, "&")
	fallback := analyticsFallbackURL + "?" + strings.Join(fallbackQuery(query, tag.flags), "&")
	runtime := analytics.runtime
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		response, err := runtime.do(ctx, http.MethodPost, address, body, headers)
		if err == nil {
			runtime.drain(response, nil)
			return
		}
		analytics.mu.Lock()
		target.fallbacks++
		analytics.mu.Unlock()
		headers.Set("Sec-Fetch-Site", fetchSite(analyticsFallbackURL))
		anonymous, cancel := context.WithTimeout(withRequestClass(context.Background(), requestClass{anonymous: true}), time.Minute)
		defer cancel()
		runtime.drain(runtime.do(anonymous, http.MethodPost, fallback, body, headers))
	}()
}

// tagSample 是 GTM 容器与 gtag 每次载入为 tag 诊断抽样取的随机数
var tagSample = rand.Float64

// analyticsDiagnosticsURL 是 GTM 容器与 gtag 发出 tag 诊断的地址
const analyticsDiagnosticsURL = tagManagerOrigin + "/td"

// diagnose 像抽中诊断的 gtag 一样发出 tag 诊断：gtm.init 时以图片请求发 seq=1，页面 pagehide 时（fin）以 fetch keepalive 发 seq=2 并带各端点的命中计数 mde，再以脚本地址加 is_td=1 请求一次；调用方持有 mu
func (analytics *pageAnalytics) diagnose(target *analyticsTarget, fin bool) {
	tag := target.tag
	params := []string{"v=3", "t=t", "pid=" + strconv.FormatInt(target.pid, 10), "gtm=" + target.gtmParam(false), "seq=1", "exp=" + target.tagExp,
		"dl=" + uriComponent(strings.TrimPrefix(analytics.location, "https://")), "tdp=" + tag.id + ";" + target.version + ";1;6;0"}
	headers := http.Header{}
	method := http.MethodGet
	if fin {
		params[4] = "seq=2"
		mde := "mde=" + tag.id + ";16_" + strconv.Itoa(target.hits) + ";61_1"
		if target.fallbacks > 0 {
			mde += ";67_" + strconv.Itoa(target.fallbacks)
		}
		params = append(params, mde, "fin=1", "z=0")
		method = http.MethodPost
		headers.Set("Accept", "*/*")
		headers.Set("Referer", aiStudioOrigin+"/")
		headers.Set("Origin", aiStudioOrigin)
		headers.Set("Sec-Fetch-Dest", "empty")
		headers.Set("Sec-Fetch-Mode", "no-cors")
		headers.Set("Sec-Fetch-Site", fetchSite(analyticsDiagnosticsURL))
		headers.Set("Priority", "u=4")
		headers.Set("Pragma", "no-cache")
		headers.Set("Cache-Control", "no-cache")
	} else {
		slo := "slo=4"
		if target.cached {
			slo = "slo=1"
		}
		params = append(params, "frm=0", "rtg="+target.container, slo, "hlo=1", "lst=2", "pcid="+target.container, "bt=0", "ct=3", "jsp=16~17", "z=0")
		headers = diagnosticsImageHeaders()
	}
	query := "&" + strings.Join(params, "&")
	address := analyticsDiagnosticsURL + "?id=" + tag.id + query
	runtime := analytics.runtime
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		runtime.drain(runtime.do(ctx, method, address, nil, headers))
		if fin {
			script := target.script + "&is_td=1" + query
			runtime.drain(runtime.do(ctx, http.MethodGet, script, nil, subresourceHeaders(script, "*/*", "script", "")))
		}
	}()
}

// diagnoseContainer 像抽中诊断的 GTM 容器一样在 gtm.init 时以图片请求发出 seq=1 的 tag 诊断：experiments 是容器上报的实验，cached 表示容器脚本取自页面缓存
func (analytics *pageAnalytics) diagnoseContainer(container *tagScript, experiments string, cached bool) {
	address, err := url.Parse(analytics.container)
	if err != nil {
		return
	}
	id := address.Query().Get("id")
	slo := "slo=4"
	if cached {
		slo = "slo=1"
	}
	params := []string{"id=" + id, "v=3", "t=t", "pid=" + strconv.FormatInt(rand.Int64N(1<<31), 10), "gtm=45He" + container.Build + "v9" + container.Version + "za200zd9" + container.Container + "xf1", "seq=1", "exp=" + experiments,
		"dl=" + uriComponent(strings.TrimPrefix(analytics.location, "https://")), "tdp=" + id + ";" + container.Version + ";0;0;0", "frm=0", "rtg=" + container.Container, slo, "hlo=1", "lst=2", "bt=0", "ct=3", "jsp=16~17", "z=0"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	analytics.runtime.drain(analytics.runtime.do(ctx, http.MethodGet, analyticsDiagnosticsURL+"?"+strings.Join(params, "&"), nil, diagnosticsImageHeaders()))
}

// diagnosticsImageHeaders 返回 Firefox 以图片请求发出 tag 诊断的请求头
func diagnosticsImageHeaders() http.Header {
	headers := http.Header{}
	headers.Set("Accept", "image/avif,image/jxl,image/webp,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5")
	headers.Set("Referer", aiStudioOrigin+"/")
	headers.Set("Sec-Fetch-Storage-Access", "none")
	headers.Set("Sec-Fetch-Dest", "image")
	headers.Set("Sec-Fetch-Mode", "no-cors")
	headers.Set("Sec-Fetch-Site", fetchSite(analyticsDiagnosticsURL))
	headers.Set("Priority", "u=5, i")
	return headers
}

// analyticsFallbackURL 是 gtag 发往 www.google-analytics.com 的命中请求失败后改发的地址
const analyticsFallbackURL = "https://www.google.com/g/collect"

// fallbackQuery 返回 gtag 改发命中时的参数：gaf 置上最低位，原来没有 gaf 时以 gaf=1 接在末尾
func fallbackQuery(query []string, flags int) []string {
	query = slices.Clone(query)
	if index := slices.Index(query, "gaf="+strconv.Itoa(flags)); flags > 0 && index >= 0 {
		query[index] = "gaf=" + strconv.Itoa(flags|1)
		return query
	}
	return append(query, "gaf=1")
}

// gtmParam 返回命中的 gtm 参数：GTM 构建号与目标版本，fired 为命中含 GTM 标签触发的事件时带上 GTM 容器编号，再接容器标记
func (target *analyticsTarget) gtmParam(fired bool) string {
	gtm := "45je" + target.build + "v9" + target.version
	if fired {
		gtm += "z89" + target.container
	}
	return gtm + "za20gzb9" + target.container + "zd9" + target.container + "xf1"
}

// engagement 返回事件距目标上一个事件的参与时长参数 _et，为 0 时为空，并记下事件时刻；调用方持有 mu
func (analytics *pageAnalytics) engagement(target *analyticsTarget, at time.Time) string {
	elapsed := at.Sub(target.last).Milliseconds()
	if at.After(target.last) {
		target.last = at
	}
	if elapsed <= 0 {
		return ""
	}
	return "_et=" + strconv.FormatInt(elapsed, 10)
}
