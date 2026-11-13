package aistudio

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	appconfig "github.com/Mag1cFall/AIStudio2API/internal/config"
	"github.com/Mag1cFall/AIStudio2API/internal/waa"
)

const (
	waaCreateURL           = "https://waa-pa.clients6.google.com/$rpc/google.internal.waa.v1.Waa/Create"
	waaRequestKey          = "lmnUSbltwc5ULv48iKLX"
	waaAPIKey              = "AIzaSyBGb5fGAyC-pRcRU6MUHb__b_vKha71HRE"
	waaLifetime            = 12 * time.Hour
	makerSuiteRPCURL       = "https://alkalimakersuite-pa.clients6.google.com/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/"
	loggingContextURL      = makerSuiteRPCURL + "GetLoggingContext"
	firefoxInnerHeightDiff = 85
)

// workerRuntime 是 Camoufox 页面与 SpiderMonkey VM 两种 WAA 后端共同提供的 Worker 能力
type workerRuntime interface {
	Proof(context.Context, string, string) (string, error)
	ProtocolHeaders(context.Context) (http.Header, error)
	PageCatalog() camoufoxnative.PageCatalog
	SendProtected(context.Context, string, http.Header, []byte) (*camoufoxnative.ProtectedResponse, error)
	StorageCookies(context.Context) ([]byte, error)
	// MergeSetCookieHeaders 把账户在页面之外取得的响应 Cookie 写入页面
	MergeSetCookieHeaders(context.Context, []string, string) error
	State() camoufoxnative.State
	// Reload 按官网冷启动重新载入页面，返回新页面就绪后使用的 runtime
	Reload(context.Context) (workerRuntime, error)
	Close() error
}

// waaInterpreterHashes 记录本进程已执行过的解释器版本
var waaInterpreterHashes sync.Map

// goWAARuntime 以 SpiderMonkey BotGuard VM 与账户固定出口 HTTP 承担官网页面的 WAA 职责
type goWAARuntime struct {
	options        camoufoxnative.Options
	client         *http.Client
	profile        waa.Profile
	userAgent      string
	acceptLanguage string
	pageURL        string
	pageLoaded     time.Time
	pageFlags      pageFlags
	headers        http.Header
	// pageCatalog 是页面载入时目录 RPC 的响应正文
	pageCatalog    camoufoxnative.PageCatalog
	cacheDirectory string
	// session 是账户 Cookie 状态，重新载入后的页面与原页面共用
	session *sessionCookies
	// pageCache 是账户页面子资源的 HTTP 缓存元数据，重新载入后的页面与原页面共用
	pageCache *pageCache

	vmMu            sync.Mutex
	vm              *waa.Runtime
	vmExpires       time.Time
	refreshing      *vmRefresh
	interpreterHash string
	interpreter     string
	closed          bool
	refreshTimer    *time.Timer
	incidentsTimer  *time.Timer
	// pointer 是本页面上可信指针与焦点所在的位置，本页面新建的 VM 共用
	pointer *waa.Pointer
	// running 是最近一次点击 Run 或 Rerun 取 proof 的 VM，随后的 GenerateContent 响应开始与结束时在其中派发指针下元素被替换的事件
	running *waa.Runtime

	// uiLocale 是官网前端主程序地址声明的界面语言
	uiLocale string
	// serviceStreamz 是页面 WAA 服务 logger 的 1828 上报通道，记录 Waa/Create 耗时与 challenge 解码耗时
	serviceStreamz *streamzReporter
	streamzMu      sync.Mutex
	// vmStreamz 是各 VM 包装层 logger 的 1828 上报通道
	vmStreamz map[*waa.Runtime]*streamzReporter
	// telemetry 是页面 WIZ 数据中 clearcut 上报用到的值
	telemetry pageTelemetry
	// scriptReport 是页面载入 BotGuard 解释器脚本时的 CSP 违规报告，文档没有 report-only CSP 时 policy 为空
	scriptReport cspReport
	// vitals 是页面载入的 1957 与 2584 上报
	vitals *pageVitals
	// elements 是页面载入的 2712 界面元素日志
	elements *pageElements
	// windowID 是页面 sessionStorage 中的浏览器窗口 ID，重新载入后的页面沿用
	windowID string
}

// vmRefreshTimeout 是后台重建 VM 的期限
const vmRefreshTimeout = 2 * time.Minute

// vmRefresh 是一次后台 VM 重建，done 关闭后 err 为重建结果
type vmRefresh struct {
	done   chan struct{}
	cancel context.CancelFunc
	err    error
}

// pageRPC 是官网页面自身发出的 MakerSuite 请求方法与正文
type pageRPC struct {
	method string
	body   string
}

// pageCatalogStartupRPCs 是官网页面启动请求中的权益与模型目录请求，Worker 就绪前需要它们的正文
var pageCatalogStartupRPCs = []pageRPC{
	{"GetAiStudioBenefitTier", "[]"},
	{"ListModels", "[]"},
}

// pageStartupRPCs 是官网页面启动时与权益和模型目录请求、GetLoggingContext、Waa/Create 并发发出的其余请求
var pageStartupRPCs = []pageRPC{
	{"GetUserPreferences", "[]"},
	{"GenerateAccessToken", `["users/me"]`},
	incidentsRPC,
	{"ListPromos", "[]"},
	{"GenerateAccessToken", `["users/me"]`},
}

// pageFollowupRPCs 是官网页面在启动请求完成后接着发出的请求
var pageFollowupRPCs = []pageRPC{
	{"ListRecentApplets", "[]"},
	{"ListPrompts", "[100]"},
}

// gdpProfileRPC 是官网导航栏在账户有订阅权益时读取 Google Developer Program 权益的请求
var gdpProfileRPC = pageRPC{"GetGdpProfileAndBenefits", "[]"}

// incidentsRPC 是官网页面打开期间按 incidentsPollInterval 轮询的服务事件请求
var incidentsRPC = pageRPC{"ListIncidentsHistory", "[]"}

// incidentsPollInterval 是官网页面从启动时的第一次 ListIncidentsHistory 算起的固定轮询周期
const incidentsPollInterval = 10 * time.Minute

// pageFlags 是官网页面内联数据中决定页面自身请求的构建开关
type pageFlags struct {
	// gdpProfile 为开关 45806340：导航栏读取 Google Developer Program 权益
	gdpProfile bool
	// plusBenefit 为开关 45777963：Plus 权益计为订阅权益
	plusBenefit bool
}

// parsePageFlags 读取官网页面内联数据中的构建开关
func parsePageFlags(body []byte) pageFlags {
	enabled := func(id string) bool { return bytes.Contains(body, []byte("["+id+",null,true,")) }
	return pageFlags{gdpProfile: enabled("45806340"), plusBenefit: enabled("45777963")}
}

// requestsGDPProfile 按官网导航栏的条件判断页面是否读取 Google Developer Program 权益：开关开启且 GetAiStudioBenefitTier 返回订阅权益
func (flags pageFlags) requestsGDPProfile(benefitTier []byte) bool {
	if !flags.gdpProfile {
		return false
	}
	tier, err := decodeBenefitTier(benefitTier)
	if err != nil || tier == BenefitTierPlus && !flags.plusBenefit {
		return false
	}
	return tier != BenefitTierFree
}

// pageTogglesPattern 匹配官网页面内联脚本写入的构建开关数组
var pageTogglesPattern = regexp.MustCompile(`window\["_F_toggles_default_MakerSuite"\] = \[([^\]]*)\]`)

// pageStringGlobalPattern 匹配官网页面内联脚本以 var 声明的构建字符串
var pageStringGlobalPattern = regexp.MustCompile(`var (_F_cssRowKey|_F_combinedSignature) = '([^']*)'`)

// pageGlobals 返回官网页面内联脚本声明的当前构建全局值
func pageGlobals(body []byte) map[string]any {
	globals := map[string]any{}
	if match := pageTogglesPattern.FindSubmatch(body); match != nil {
		toggles := []any{}
		for _, item := range strings.Split(string(match[1]), ",") {
			if value, err := strconv.ParseInt(strings.TrimSpace(item), 0, 64); err == nil {
				toggles = append(toggles, float64(value))
			}
		}
		globals["_F_toggles_default_MakerSuite"] = toggles
	}
	for _, match := range pageStringGlobalPattern.FindAllSubmatch(body, -1) {
		globals[string(match[1])] = string(match[2])
	}
	return globals
}

// newGoWAAHTTPClient 创建由 runtime 手动处理重定向的固定出口客户端
func newGoWAAHTTPClient(proxyURL string) (*http.Client, error) {
	client, err := NewProxyHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client, nil
}

// NewSpiderMonkeyWorker 启动单个账户的 SpiderMonkey WAA runtime
func NewSpiderMonkeyWorker(ctx context.Context, accountID string, options camoufoxnative.Options) (*NativeWorker, error) {
	if accountID == "" {
		return nil, fmt.Errorf("缺少账户 ID")
	}
	runtime, err := newGoWAARuntime(ctx, options)
	if err != nil {
		return nil, err
	}
	return &NativeWorker{
		accountID: accountID,
		runtime:   runtime,
		state: WorkerState{
			AccountID: accountID,
			Phase:     WorkerReady,
			PID:       os.Getpid(),
			RuntimeID: "spidermonkey-waa",
			PageURL:   runtime.pageURL,
		},
	}, nil
}

func newGoWAARuntime(ctx context.Context, options camoufoxnative.Options) (*goWAARuntime, error) {
	if options.Model == "" {
		return nil, errors.New("WAA bootstrap 缺少实时目录聊天模型")
	}
	reportGoStartup(options, camoufoxnative.StartupPreparingBrowser)
	runtime, fingerprint, err := newGoPageRuntime(options)
	if err != nil {
		return nil, err
	}
	if err := runtime.open(ctx, fingerprint); err != nil {
		return nil, err
	}
	return runtime, nil
}

// Reload 以同一 HTTP 客户端与账户 Cookie 按官网冷启动重新载入页面，返回新页面的 runtime
func (runtime *goWAARuntime) Reload(ctx context.Context) (workerRuntime, error) {
	options := runtime.options
	options.StartupProgress = nil
	next, fingerprint, err := pageRuntime(options, runtime.client, runtime.session, runtime.pageCache)
	if err != nil {
		return nil, err
	}
	next.windowID = runtime.windowID
	if err := next.open(ctx, fingerprint); err != nil {
		return nil, err
	}
	return next, nil
}

// open 按官网冷启动载入页面：请求页面与文档引用的样式表和脚本，脚本就绪后像页面应用一样预热认证并发出页面启动 RPC，再创建 WAA VM
func (runtime *goWAARuntime) open(ctx context.Context, fingerprint map[string]any) (err error) {
	defer func() {
		if err != nil {
			runtime.closeTelemetry()
		}
	}()
	options := runtime.options
	runtime.pageURL += "?model=" + url.QueryEscape(options.Model) + "&temporary=true"
	runtime.profile.PageURL = runtime.pageURL
	runtime.profile.HasFocus = true
	runtime.profile.Locale, _ = fingerprint["navigator.language"].(string)
	runtime.profile.TimeZone, _ = fingerprint["timezone"].(string)
	for _, origin := range runtime.cookies().Origins {
		if origin.Origin != aiStudioOrigin {
			continue
		}
		runtime.profile.LocalStorage = make(map[string]string, len(origin.LocalStorage))
		for _, item := range origin.LocalStorage {
			runtime.profile.LocalStorage[item.Name] = item.Value
		}
	}
	runtime.cacheDirectory = filepath.Join(filepath.Dir(filepath.Dir(options.StorageStatePath)), appconfig.WAAInterpreterCacheName)
	reportGoStartup(options, camoufoxnative.StartupLoadingAIStudio)
	apiKey, document, err := runtime.loadPage(ctx)
	if err != nil {
		return err
	}
	runtime.uiLocale = uiLocale(document.frontendBundle())
	runtime.serviceStreamz = runtime.newStreamz(nil)
	page, err := url.Parse(runtime.pageURL)
	if err != nil {
		return err
	}
	if err := runtime.loadDocumentResources(ctx, document); err != nil {
		return err
	}
	documentTiming(runtime.profile.Navigation, time.Since(runtime.profile.NavigationStart))
	runtime.vitals = newPageVitals(runtime.sendClearcut, runtime.telemetry, runtime.uiLocale, runtime.location(), runtime.profile.NavigationStart, runtime.profile.Navigation, page.Path)
	if runtime.windowID == "" {
		runtime.windowID = pageWindowID()
	}
	runtime.elements = newPageElements(func(body []byte) { runtime.sendClearcut(clearcutGzip, body) }, runtime.uiLocale, runtime.location(), runtime.telemetry.experiments, runtime.windowID, runtime.profile.Locale, "models/"+strings.TrimPrefix(options.Model, "models/"))
	runtime.elements.load(runtime.profile.NavigationStart.Add(time.Duration(navigationInt(runtime.profile.Navigation, "domInteractive")) * time.Millisecond))
	dependency, module, err := starterModuleURLs(document)
	if err != nil {
		return err
	}
	go runtime.loadStarters(context.WithoutCancel(ctx), dependency, module)
	if runtime.scriptReport.policy != "" {
		if runtime.scriptReport.loader, err = runtime.pageCache.loader(runtime.scriptReport.bundle); err != nil {
			return err
		}
	}
	runtime.loadPageIcons(ctx, document)
	go runtime.authWarmup(context.WithoutCancel(ctx))
	visitID, err := newVisitID()
	if err != nil {
		return err
	}
	reportGoStartup(options, camoufoxnative.StartupLocatingWAA)
	type loggingContext struct {
		extension string
		body      []byte
		err       error
	}
	loggingDone := make(chan loggingContext, 1)
	go func() {
		extension, body, err := runtime.loggingContextHeader(ctx, apiKey, visitID)
		loggingDone <- loggingContext{extension, body, err}
	}()
	catalogDone := make(chan [][]byte, 1)
	pageStarted := time.Now()
	var catalogAt time.Time
	go func() {
		bodies := runtime.sendPageRPCs(ctx, apiKey, visitID, "", pageCatalogStartupRPCs)
		catalogAt = time.Now()
		catalogDone <- bodies
	}()
	startupDone := make(chan struct{})
	go func() {
		runtime.sendPageRPCs(context.WithoutCancel(ctx), apiKey, visitID, "", pageStartupRPCs)
		close(startupDone)
	}()
	type startedVM struct {
		vm  *waa.Runtime
		err error
	}
	vmDone := make(chan startedVM, 1)
	challenge, err := runtime.createChallenge(ctx, nil)
	if err == nil {
		go func() {
			vm, err := runtime.startVM(ctx, challenge)
			vmDone <- startedVM{vm, err}
		}()
	}
	logging := <-loggingDone
	catalog := <-catalogDone
	if err != nil {
		return err
	}
	if logging.err != nil {
		if started := <-vmDone; started.vm != nil {
			runtime.closeVM(started.vm)
		}
		return logging.err
	}
	extension := logging.extension
	runtime.pageCatalog = camoufoxnative.PageCatalog{
		Logging: logging.body,
		Tier:    catalog[0],
		Models:  catalog[1],
		Bundle:  document.frontendBundle(),
	}
	tier, _ := decodeBenefitTier(catalog[0])
	runtime.elements.catalog(catalogAt, tier.HeaderValue())
	runtime.headers = http.Header{}
	runtime.headers.Set("user-agent", runtime.userAgent)
	runtime.headers.Set("x-goog-api-key", apiKey)
	runtime.headers.Set("x-goog-authuser", strconv.Itoa(options.AuthUser))
	runtime.headers.Set("x-user-agent", "grpc-web-javascript/0.1")
	runtime.headers.Set("x-aistudio-visit-id", visitID)
	if extension != "" {
		runtime.headers.Set("x-goog-ext-519733851-bin", extension)
	}
	reportGoStartup(options, camoufoxnative.StartupBootstrappingWAA)
	go func() {
		<-startupDone
		runtime.sendFollowupRPCs(context.WithoutCancel(ctx), apiKey, visitID, extension, catalog[0])
	}()
	started := <-vmDone
	if started.err != nil {
		return started.err
	}
	vm := started.vm
	if err := vm.Settle(ctx); err != nil {
		runtime.closeVM(vm)
		return err
	}
	runtime.vm = vm
	runtime.scheduleRefresh()
	runtime.scheduleIncidents(pageStarted.Add(incidentsPollInterval), apiKey, visitID, extension)
	return nil
}

// scheduleIncidents 在 at 时刻以官网页面的请求头发出 ListIncidentsHistory，并按 incidentsPollInterval 安排下一次，直到 runtime 关闭
func (runtime *goWAARuntime) scheduleIncidents(at time.Time, apiKey string, visitID string, extension string) {
	runtime.incidentsTimer = time.AfterFunc(time.Until(at), func() {
		runtime.vmMu.Lock()
		if runtime.closed {
			runtime.vmMu.Unlock()
			return
		}
		runtime.scheduleIncidents(at.Add(incidentsPollInterval), apiKey, visitID, extension)
		runtime.vmMu.Unlock()
		runtime.sendPageRPCs(context.Background(), apiKey, visitID, extension, []pageRPC{incidentsRPC})
	})
}

// scheduleRefresh 在 VM 生命周期到期时按官网流程后台重建 VM
func (runtime *goWAARuntime) scheduleRefresh() {
	runtime.vmExpires = time.Now().Add(waaLifetime)
	if runtime.refreshTimer != nil {
		runtime.refreshTimer.Stop()
	}
	runtime.refreshTimer = time.AfterFunc(waaLifetime, func() {
		runtime.vmMu.Lock()
		defer runtime.vmMu.Unlock()
		if !runtime.closed && !time.Now().Before(runtime.vmExpires) {
			runtime.refreshVM()
		}
	})
}

func reportGoStartup(options camoufoxnative.Options, stage camoufoxnative.StartupStage) {
	if options.StartupProgress != nil {
		options.StartupProgress(stage)
	}
}

// profileFromFingerprint 按 Camoufox 页面实际呈现的规则把账户指纹映射为宿主现场值
func profileFromFingerprint(config map[string]any) (waa.Profile, string, string) {
	profile := waa.Profile{Window: map[string]any{}, Navigator: map[string]any{}, Screen: map[string]any{}, Document: map[string]any{}}
	for key, value := range config {
		scope, name, found := strings.Cut(key, ".")
		if !found {
			continue
		}
		switch scope {
		case "navigator":
			profile.Navigator[name] = value
		case "screen":
			profile.Screen[name] = value
		case "window":
			profile.Window[name] = value
		}
	}
	for _, name := range []string{"availLeft", "availTop"} {
		if _, ok := profile.Screen[name]; !ok {
			profile.Screen[name] = 0
		}
	}
	profile.Navigator["doNotTrack"] = "unspecified"
	profile.Navigator["globalPrivacyControl"] = false
	if _, ok := profile.Window["innerWidth"]; !ok {
		if width, ok := numberValue(profile.Window["outerWidth"]); ok {
			profile.Window["innerWidth"] = width
		}
	}
	if _, ok := profile.Window["innerHeight"]; !ok {
		if height, ok := numberValue(profile.Window["outerHeight"]); ok {
			profile.Window["innerHeight"] = height - firefoxInnerHeightDiff
		}
	}
	userAgent, _ := config["navigator.userAgent"].(string)
	if userAgent == "" {
		userAgent = publicDiscoveryUserAgent
	}
	acceptLanguage, _ := config["headers.Accept-Language"].(string)
	if acceptLanguage == "" {
		acceptLanguage = "en-US,en;q=0.5"
	}
	return profile, userAgent, acceptLanguage
}

func numberValue(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case int:
		return float64(number), true
	case json.Number:
		parsed, err := number.Float64()
		return parsed, err == nil
	}
	return 0, false
}

// sessionCookies 是 SpiderMonkey runtime 的账户 Cookie 状态
type sessionCookies struct {
	mu    sync.Mutex
	state StorageState
}

// cookies 返回当前账户 Cookie 状态副本
func (runtime *goWAARuntime) cookies() StorageState {
	runtime.session.mu.Lock()
	defer runtime.session.mu.Unlock()
	state := runtime.session.state
	state.Cookies = append([]StateCookie(nil), runtime.session.state.Cookies...)
	return state
}

// mergeResponseCookies 把响应 Set-Cookie 写回账户 Cookie 状态
func (runtime *goWAARuntime) mergeResponseCookies(response *http.Response, requestURL string) {
	headers := response.Header.Values("Set-Cookie")
	if len(headers) == 0 {
		return
	}
	runtime.session.mu.Lock()
	defer runtime.session.mu.Unlock()
	if err := runtime.session.state.MergeSetCookieHeaders(headers, requestURL, time.Now()); err != nil {
		slog.Warn("WAA SpiderMonkey Cookie 写回失败", "url", requestURL, "error", err)
	}
}

// do 以 Firefox 请求头形状发送请求并写回响应 Cookie，context 标为匿名类别的请求不带 Cookie 也不写回
func (runtime *goWAARuntime) do(ctx context.Context, method string, target string, body []byte, headers http.Header) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	request.Header = headers.Clone()
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", runtime.userAgent)
	}
	if request.Header.Get("Accept-Language") == "" {
		request.Header.Set("Accept-Language", runtime.acceptLanguage)
	}
	if request.Header.Get("Accept-Encoding") == "" {
		request.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	}
	anonymous := requestClassOf(ctx).anonymous
	if cookieHeader, err := runtime.cookies().CookieHeader(target, time.Now()); err == nil && cookieHeader != "" && !anonymous {
		request.Header.Set("Cookie", cookieHeader)
	}
	response, err := sendHTTP(runtime.client, request)
	if err != nil {
		return nil, err
	}
	if !anonymous {
		runtime.mergeResponseCookies(response, target)
	}
	return response, nil
}

// newGoPageRuntime 以账户的指纹、固定出口与保存的 Cookie 准备打开官网新对话页面
func newGoPageRuntime(options camoufoxnative.Options) (*goWAARuntime, map[string]any, error) {
	storage, err := LoadStorageState(options.StorageStatePath)
	if err != nil {
		return nil, nil, err
	}
	client, err := newGoWAAHTTPClient(options.Proxy)
	if err != nil {
		return nil, nil, err
	}
	cache, err := loadPageCache(options.StorageStatePath)
	if err != nil {
		return nil, nil, err
	}
	return pageRuntime(options, client, &sessionCookies{state: storage}, cache)
}

// pageRuntime 以账户的指纹、HTTP 客户端、Cookie 状态与页面缓存准备打开官网新对话页面
func pageRuntime(options camoufoxnative.Options, client *http.Client, session *sessionCookies, cache *pageCache) (*goWAARuntime, map[string]any, error) {
	fingerprint, err := camoufoxnative.AccountFingerprint(options)
	if err != nil {
		return nil, nil, err
	}
	runtime := &goWAARuntime{options: options, client: client, session: session, pageCache: cache, pointer: waa.NewPointer()}
	runtime.profile, runtime.userAgent, runtime.acceptLanguage = profileFromFingerprint(fingerprint)
	pagePath := "/prompts/new_chat"
	if options.AuthUser > 0 {
		pagePath = fmt.Sprintf("/u/%d/prompts/new_chat", options.AuthUser)
	}
	runtime.pageURL = aiStudioOrigin + pagePath
	return runtime, fingerprint, nil
}

// CheckSession 像官网页面一样打开账户的 AI Studio 新对话页，被转到 Google 登录页时返回 camoufoxnative.ErrAuthenticationRequired，被转到拒绝页时返回对应的 camoufoxnative.AccessDenial
func CheckSession(ctx context.Context, options camoufoxnative.Options) error {
	runtime, _, err := newGoPageRuntime(options)
	if err != nil {
		return err
	}
	_, _, err = runtime.loadPage(ctx)
	return err
}

// loadPage 以浏览器导航形状请求官网页面，返回页面公开 API Key 与文档引用的子资源
func (runtime *goWAARuntime) loadPage(ctx context.Context) (string, pageDocument, error) {
	headers := http.Header{}
	headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	headers.Set("Upgrade-Insecure-Requests", "1")
	headers.Set("Sec-Fetch-Dest", "document")
	headers.Set("Sec-Fetch-Mode", "navigate")
	headers.Set("Sec-Fetch-Site", "none")
	headers.Set("Sec-Fetch-User", "?1")
	headers.Set("Priority", "u=0, i")
	target := runtime.pageURL
	navigationStart := time.Now()
	for redirect := 0; redirect < 5; redirect++ {
		timing := &connectionTiming{}
		response, err := runtime.do(context.WithValue(ctx, connectionTimingKey{}, timing), http.MethodGet, target, nil, headers)
		if err != nil {
			return "", pageDocument{}, fmt.Errorf("载入 AI Studio 页面: %w", err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			return "", pageDocument{}, fmt.Errorf("读取 AI Studio 页面: %w", readErr)
		}
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			location, err := response.Location()
			if err != nil {
				return "", pageDocument{}, fmt.Errorf("AI Studio 页面跳转无效: %w", err)
			}
			if denial := camoufoxnative.PageDenial(location); denial != nil {
				return "", pageDocument{}, denial
			}
			if location.Hostname() == "accounts.google.com" {
				return "", pageDocument{}, fmt.Errorf("%w url=%s", camoufoxnative.ErrAuthenticationRequired, location.String())
			}
			target = location.String()
			continue
		}
		if response.StatusCode != http.StatusOK {
			return "", pageDocument{}, fmt.Errorf("AI Studio 页面返回 HTTP %d", response.StatusCode)
		}
		match := makerSuiteAPIKeyPattern.FindSubmatch(body)
		if len(match) != 2 || len(match[1]) == 0 {
			return "", pageDocument{}, fmt.Errorf("AI Studio 页面缺少 WIu0Nc")
		}
		runtime.pageLoaded = time.Now()
		runtime.profile.Globals = pageGlobals(body)
		runtime.pageFlags = parsePageFlags(body)
		runtime.profile.NavigationStart = navigationStart
		runtime.profile.Navigation, runtime.profile.DOMLoading = navigationTiming(pageLoad{
			url: target, protocol: nextHopProtocol(response.Proto), started: navigationStart, connection: timing, end: runtime.pageLoaded.Sub(navigationStart), decoded: len(body),
		})
		runtime.profile.TrustedTypes = requiresTrustedScripts(response.Header.Values("Content-Security-Policy"))
		document := parsePageDocument(body, target)
		if runtime.telemetry, err = parsePageTelemetry(body, document.frontendBundle()); err != nil {
			return "", pageDocument{}, err
		}
		runtime.scriptReport = cspReport{policy: response.Header.Get("Content-Security-Policy-Report-Only"), document: target, bundle: document.frontendBundle()}
		if runtime.scriptReport.policy != "" {
			if _, _, err := runtime.scriptReport.body(""); err != nil {
				return "", pageDocument{}, fmt.Errorf("AI Studio 页面 report-only CSP: %w", err)
			}
		}
		return string(match[1]), document, nil
	}
	return "", pageDocument{}, errors.New("AI Studio 页面跳转次数过多")
}

// requiresTrustedScripts 判断页面 CSP 是否含 require-trusted-types-for 'script'
func requiresTrustedScripts(policies []string) bool {
	for _, policy := range policies {
		for _, directive := range strings.FieldsFunc(policy, func(character rune) bool { return character == ';' || character == ',' }) {
			fields := strings.Fields(directive)
			if len(fields) > 1 && strings.EqualFold(fields[0], "require-trusted-types-for") && slices.Contains(fields[1:], "'script'") {
				return true
			}
		}
	}
	return false
}

// rpcHeaders 返回官网页面 grpc-web 请求的公共头
func (runtime *goWAARuntime) rpcHeaders(apiKey string) (http.Header, error) {
	authorization, err := NewSigner().Authorization(runtime.cookies())
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set("Accept", "*/*")
	headers.Set("Referer", aiStudioOrigin+"/")
	headers.Set("Content-Type", "application/json+protobuf")
	headers.Set("X-Goog-Api-Key", apiKey)
	headers.Set("X-Goog-AuthUser", strconv.Itoa(runtime.options.AuthUser))
	headers.Set("X-User-Agent", "grpc-web-javascript/0.1")
	headers.Set("Authorization", authorization)
	headers.Set("Origin", aiStudioOrigin)
	headers.Set("Sec-Fetch-Dest", "empty")
	headers.Set("Sec-Fetch-Mode", "cors")
	headers.Set("Sec-Fetch-Site", "same-site")
	return headers, nil
}

// loggingContextHeader 以官网页面的 visit ID 调用 GetLoggingContext，返回编码为官网 X-Goog-Ext-519733851-Bin 的值与响应正文
func (runtime *goWAARuntime) loggingContextHeader(ctx context.Context, apiKey string, visitID string) (string, []byte, error) {
	headers, err := runtime.rpcHeaders(apiKey)
	if err != nil {
		return "", nil, err
	}
	headers.Set("X-AIStudio-Visit-Id", visitID)
	response, err := runtime.do(ctx, http.MethodPost, loggingContextURL, []byte("[]"), headers)
	if err != nil {
		return "", nil, fmt.Errorf("读取官网日志上下文: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", nil, err
	}
	if response.StatusCode != http.StatusOK {
		return "", nil, DecodeRPCError("GetLoggingContext", response.StatusCode, body)
	}
	encoded, err := encodeJSPBFields(body)
	if err != nil {
		return "", nil, fmt.Errorf("编码官网日志上下文: %w", err)
	}
	return base64.StdEncoding.EncodeToString(encoded), body, nil
}

// sendPageRPCs 以官网页面的请求头并发发出页面自身的 MakerSuite 请求，extension 非空时带 X-Goog-Ext-519733851-Bin，按 rpcs 顺序返回各 HTTP 200 响应的正文
func (runtime *goWAARuntime) sendPageRPCs(ctx context.Context, apiKey string, visitID string, extension string, rpcs []pageRPC) [][]byte {
	bodies := make([][]byte, len(rpcs))
	var group sync.WaitGroup
	for index, rpc := range rpcs {
		group.Go(func() {
			headers, err := runtime.rpcHeaders(apiKey)
			if err != nil {
				return
			}
			headers.Set("X-AIStudio-Visit-Id", visitID)
			if extension != "" {
				headers.Set("X-Goog-Ext-519733851-Bin", extension)
			}
			bodies[index] = runtime.drain(runtime.do(ctx, http.MethodPost, makerSuiteRPCURL+rpc.method, []byte(rpc.body), headers))
		})
	}
	group.Wait()
	return bodies
}

// sendFollowupRPCs 发出官网页面在启动请求完成后接着发出的请求，带已返回的日志上下文 extension，权益响应 benefitTier 满足导航栏条件时同时读取 Google Developer Program 权益
func (runtime *goWAARuntime) sendFollowupRPCs(ctx context.Context, apiKey string, visitID string, extension string, benefitTier []byte) {
	rpcs := pageFollowupRPCs
	if runtime.pageFlags.requestsGDPProfile(benefitTier) {
		rpcs = append(slices.Clone(rpcs), gdpProfileRPC)
	}
	runtime.sendPageRPCs(ctx, apiKey, visitID, extension, rpcs)
}

// drain 读完并关闭页面自身请求的响应，返回 HTTP 200 响应的正文
func (runtime *goWAARuntime) drain(response *http.Response, err error) []byte {
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK {
		return nil
	}
	return body
}

// encodeJSPBFields 按字段顺序把 JSPB 数组中的数字与字符串编码为 protobuf 二进制
func encodeJSPBFields(raw []byte) ([]byte, error) {
	var fields []json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	var output []byte
	for index, field := range fields {
		number := uint64(index + 1)
		trimmed := bytes.TrimSpace(field)
		if len(trimmed) == 0 || string(trimmed) == "null" {
			continue
		}
		switch trimmed[0] {
		case '"':
			var text string
			if err := json.Unmarshal(trimmed, &text); err != nil {
				return nil, err
			}
			output = appendVarint(output, number<<3|2)
			output = appendVarint(output, uint64(len(text)))
			output = append(output, text...)
		case 't', 'f':
			output = appendVarint(output, number<<3)
			if trimmed[0] == 't' {
				output = appendVarint(output, 1)
			} else {
				output = appendVarint(output, 0)
			}
		default:
			var value int64
			if err := json.Unmarshal(trimmed, &value); err != nil {
				return nil, fmt.Errorf("字段 %d 不是整数: %s", number, trimmed)
			}
			output = appendVarint(output, number<<3)
			output = appendVarint(output, uint64(value))
		}
	}
	return output, nil
}

func appendVarint(output []byte, value uint64) []byte {
	for value >= 0x80 {
		output = append(output, byte(value)|0x80)
		value >>= 7
	}
	return append(output, byte(value))
}

// createChallenge 按官网请求体调用 Waa/Create，刷新时携带已加载解释器 hash 与上一 VM 的 snapshot
func (runtime *goWAARuntime) createChallenge(ctx context.Context, refresh []string) (waa.Challenge, error) {
	requestBody := []string{waaRequestKey}
	requestBody = append(requestBody, refresh...)
	body, _ := json.Marshal(requestBody)
	headers, err := runtime.rpcHeaders(waaAPIKey)
	if err != nil {
		return waa.Challenge{}, err
	}
	started := time.Now()
	status := -1
	defer func() {
		runtime.serviceStreamz.record(streamzRecord{metric: "wrl", labels: []any{"c", 1, status, waaRequestKey, streamzMachineKey}, value: float64(time.Since(started).Milliseconds())})
	}()
	response, err := runtime.do(ctx, http.MethodPost, waaCreateURL, body, headers)
	if err != nil {
		return waa.Challenge{}, fmt.Errorf("调用 Waa/Create: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return waa.Challenge{}, err
	}
	if response.StatusCode != http.StatusOK {
		return waa.Challenge{}, fmt.Errorf("Waa/Create 返回 HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(raw)))
	}
	status = 0
	decoded := time.Now()
	challenge, err := waa.ParseChallenge(raw)
	if err == nil && challenge.Packed {
		runtime.serviceStreamz.record(streamzRecord{metric: "el", labels: []any{"U", streamzMachineKey}, value: float64(time.Since(decoded).Milliseconds())})
	}
	return challenge, err
}

// loadInterpreter 按 challenge 指定的 hash 取得解释器并校验摘要：challenge 不带解释器地址或账户页面缓存中新鲜时沿用同 hash 的已有解释器，否则像页面脚本一样请求（有本地副本时带条件头）
func (runtime *goWAARuntime) loadInterpreter(ctx context.Context, challenge waa.Challenge) (string, error) {
	if challenge.InterpreterJavaScript != "" {
		if hash := waa.InterpreterHash([]byte(challenge.InterpreterJavaScript)); hash != challenge.InterpreterHash {
			return "", fmt.Errorf("WAA 解释器摘要不一致 expected=%s actual=%s", challenge.InterpreterHash, hash)
		}
		return challenge.InterpreterJavaScript, nil
	}
	if runtime.scriptReport.policy != "" {
		runtime.sendScriptReport(challenge.InterpreterURL)
	}
	path := filepath.Join(runtime.cacheDirectory, challenge.InterpreterHash+".js")
	cached := ""
	if runtime.interpreterHash == challenge.InterpreterHash && runtime.interpreter != "" {
		cached = runtime.interpreter
	} else if data, err := os.ReadFile(path); err == nil && waa.InterpreterHash(data) == challenge.InterpreterHash {
		cached = string(data)
	}
	entry, fresh := runtime.pageCache.lookup(challenge.InterpreterURL, time.Now())
	if cached != "" && (fresh || challenge.InterpreterURL == "") {
		return cached, nil
	}
	headers := http.Header{}
	headers.Set("Accept", "*/*")
	headers.Set("Referer", aiStudioOrigin+"/")
	headers.Set("Sec-Fetch-Dest", "script")
	headers.Set("Sec-Fetch-Mode", "no-cors")
	headers.Set("Sec-Fetch-Site", "same-site")
	if cached != "" && entry.LastModified != "" {
		headers.Set("If-Modified-Since", entry.LastModified)
	}
	if cached != "" && entry.ETag != "" {
		headers.Set("If-None-Match", entry.ETag)
	}
	response, err := runtime.do(ctx, http.MethodGet, challenge.InterpreterURL, nil, headers)
	if err != nil {
		return "", fmt.Errorf("下载 WAA 解释器: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	switch {
	case response.StatusCode == http.StatusNotModified && cached != "":
		data = []byte(cached)
	case response.StatusCode != http.StatusOK:
		return "", fmt.Errorf("下载 WAA 解释器返回 HTTP %d", response.StatusCode)
	}
	if hash := waa.InterpreterHash(data); hash != challenge.InterpreterHash {
		return "", fmt.Errorf("WAA 解释器摘要不一致 expected=%s actual=%s", challenge.InterpreterHash, hash)
	}
	if err := runtime.pageCache.store(challenge.InterpreterURL, response, time.Now(), nil, nil); err != nil {
		return "", err
	}
	if response.StatusCode == http.StatusOK {
		if err := os.MkdirAll(runtime.cacheDirectory, 0o755); err == nil {
			_ = os.WriteFile(path, data, 0o644)
		}
	}
	return string(data), nil
}

// startVM 加载 challenge 指定的解释器并初始化新 VM，导航计时的 loadEventEnd 之前等到页面 load 事件结束
func (runtime *goWAARuntime) startVM(ctx context.Context, challenge waa.Challenge) (*waa.Runtime, error) {
	interpreter, err := runtime.loadInterpreter(ctx, challenge)
	if err != nil {
		return nil, err
	}
	if _, seen := waaInterpreterHashes.LoadOrStore(challenge.InterpreterHash, true); !seen {
		slog.Info("WAA 解释器版本", "hash", challenge.InterpreterHash, "url", challenge.InterpreterURL)
	}
	if runtime.interpreterHash != "" && runtime.interpreterHash != challenge.InterpreterHash {
		slog.Warn("WAA 解释器版本变化", "previous", runtime.interpreterHash, "current", challenge.InterpreterHash)
	}
	runtime.interpreterHash, runtime.interpreter = challenge.InterpreterHash, interpreter
	cookie, _ := documentCookie(runtime.cookies())
	profile := runtime.profile
	profile.Document = map[string]any{"cookie": cookie, "lastModified": runtime.pageLoaded.In(runtime.location()).Format("01/02/2006 15:04:05")}
	if loadEnd, ok := profile.Navigation["loadEventEnd"].(int); ok {
		select {
		case <-time.After(time.Until(profile.NavigationStart.Add(time.Duration(loadEnd) * time.Millisecond))):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	vm, err := waa.NewRuntime(ctx, waa.Options{Challenge: challenge, Interpreter: interpreter, Profile: profile, LoadImage: runtime.loadImage, LoadFrame: runtime.loadFrame, Pointer: runtime.pointer})
	if err != nil {
		return nil, err
	}
	reporter := runtime.newStreamz(vm.TakeMetrics)
	runtime.streamzMu.Lock()
	if runtime.vmStreamz == nil {
		runtime.vmStreamz = map[*waa.Runtime]*streamzReporter{}
	}
	runtime.vmStreamz[vm] = reporter
	runtime.streamzMu.Unlock()
	reporter.flush(ctx)
	runtime.serviceStreamz.flush(ctx)
	return vm, nil
}

// location 返回账户指纹的时区，本机时区数据中没有该时区时为本机时区
func (runtime *goWAARuntime) location() *time.Location {
	location, err := time.LoadLocation(runtime.profile.TimeZone)
	if err != nil {
		return time.Local
	}
	return location
}

// newStreamz 创建一个以账户时区与页面界面语言上报的 1828 通道
func (runtime *goWAARuntime) newStreamz(take func(context.Context) ([]waa.BotGuardMetric, error)) *streamzReporter {
	return newStreamzReporter(runtime.uiLocale, runtime.location(), func(body []byte) { runtime.sendClearcut(clearcutBeacon, body) }, take)
}

// flushVMStreamz 上报 VM 包装层 logger 积攒的记录
func (runtime *goWAARuntime) flushVMStreamz(ctx context.Context, vm *waa.Runtime) {
	runtime.streamzMu.Lock()
	reporter := runtime.vmStreamz[vm]
	runtime.streamzMu.Unlock()
	if reporter != nil {
		reporter.flush(ctx)
	}
}

// closeVM 停止 VM 的 1828 定时上报并关闭 VM
func (runtime *goWAARuntime) closeVM(vm *waa.Runtime) {
	runtime.streamzMu.Lock()
	if reporter := runtime.vmStreamz[vm]; reporter != nil {
		reporter.close()
		delete(runtime.vmStreamz, vm)
	}
	runtime.streamzMu.Unlock()
	vm.Close()
}

// closeTelemetry 停止页面全部 1828 定时上报与尚未发出的 1957、2584 上报
func (runtime *goWAARuntime) closeTelemetry() {
	if runtime.vitals != nil {
		runtime.vitals.close()
	}
	if runtime.elements != nil {
		runtime.elements.close()
	}
	runtime.streamzMu.Lock()
	defer runtime.streamzMu.Unlock()
	if runtime.serviceStreamz != nil {
		runtime.serviceStreamz.close()
	}
	for vm, reporter := range runtime.vmStreamz {
		reporter.close()
		delete(runtime.vmStreamz, vm)
	}
}

// documentCookie 返回页面脚本可见的非 HttpOnly Cookie 串
func documentCookie(state StorageState) (string, error) {
	parts := make([]string, 0, len(state.Cookies))
	for _, cookie := range state.Cookies {
		if cookie.HTTPOnly || !cookieDomainMatches(cookie.Domain, "aistudio.google.com") {
			continue
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(parts, "; "), nil
}

// loadImage 以 Firefox 图片请求形状发出页面 img 请求，按解码结果返回是否加载成功
func (runtime *goWAARuntime) loadImage(address string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	headers := http.Header{}
	headers.Set("Accept", "image/avif,image/webp,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5")
	headers.Set("Referer", aiStudioOrigin+"/")
	headers.Set("Sec-Fetch-Dest", "image")
	headers.Set("Sec-Fetch-Mode", "no-cors")
	headers.Set("Sec-Fetch-Site", "same-origin")
	headers.Set("Priority", "u=5, i")
	response, err := runtime.do(ctx, http.MethodGet, address, nil, headers)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	return response.StatusCode == http.StatusOK && strings.HasPrefix(response.Header.Get("Content-Type"), "image/") && len(body) > 0
}

// refreshVM 在后台按官网刷新流程重建 VM 并返回这次重建，已有重建进行中时返回它；调用方持有 vmMu
func (runtime *goWAARuntime) refreshVM() *vmRefresh {
	if runtime.refreshing != nil {
		return runtime.refreshing
	}
	ctx, cancel := context.WithTimeout(context.Background(), vmRefreshTimeout)
	refresh := &vmRefresh{done: make(chan struct{}), cancel: cancel}
	runtime.refreshing = refresh
	previous := runtime.vm
	go func() {
		defer cancel()
		vm, err := runtime.nextVM(ctx, previous)
		runtime.vmMu.Lock()
		defer runtime.vmMu.Unlock()
		switch {
		case err != nil:
			slog.Warn("WAA SpiderMonkey VM 刷新失败", "error", err)
		case runtime.closed:
			runtime.closeVM(vm)
			err = errors.New("SpiderMonkey WAA runtime 已关闭")
		default:
			if previous != nil {
				runtime.closeVM(previous)
			}
			runtime.vm = vm
			runtime.scheduleRefresh()
		}
		refresh.err = err
		runtime.refreshing = nil
		close(refresh.done)
	}()
	return refresh
}

// nextVM 以旧 VM 的无绑定 snapshot 调用 Waa/Create 并返回静置后的新 VM，没有旧 VM 时按首次载入创建
func (runtime *goWAARuntime) nextVM(ctx context.Context, previous *waa.Runtime) (*waa.Runtime, error) {
	var refresh []string
	if previous != nil {
		snapshot, err := previous.RefreshSnapshot(ctx)
		if err != nil {
			snapshot = "E:UCE"
		}
		runtime.flushVMStreamz(ctx, previous)
		refresh = []string{runtime.interpreterHash, snapshot}
	}
	challenge, err := runtime.createChallenge(ctx, refresh)
	if err != nil {
		return nil, err
	}
	vm, err := runtime.startVM(ctx, challenge)
	if err != nil {
		return nil, err
	}
	if err := vm.Settle(ctx); err != nil {
		runtime.closeVM(vm)
		return nil, err
	}
	return vm, nil
}

// Proof 同步页面提示词后为 SHA-256 digest 生成 fresh WAA proof
func (runtime *goWAARuntime) Proof(ctx context.Context, digest string, prompt string) (string, error) {
	return runtime.proof(ctx, digest, prompt, false)
}

// RunProof 同步页面提示词并点击 Run 后为 SHA-256 digest 生成 GenerateContent 的 fresh WAA proof
func (runtime *goWAARuntime) RunProof(ctx context.Context, digest string, prompt string) (string, error) {
	return runtime.proof(ctx, digest, prompt, true)
}

// userGestureKey 标记 proof 所属请求在官网由页面上的点击或按键触发
type userGestureKey struct{}

// pageRerunKey 标记 GenerateContent 是账户页面再次运行同一对话，与官网点击最后一轮的 Rerun 相同，取值为对话的轮数
type pageRerunKey struct{}

// proof 以当前 VM 生成 fresh WAA proof：run 时写入提示词后以可信鼠标点击 Run，ctx 带 pageRerunKey 且本页面点过 Run 时点击最后一轮的 Rerun，其余 ctx 带 userGestureKey 的请求先记录一次用户操作；VM 到期时等待后台重建完成，等待随 ctx 结束
func (runtime *goWAARuntime) proof(ctx context.Context, digest string, prompt string, run bool) (string, error) {
	runtime.vmMu.Lock()
	defer runtime.vmMu.Unlock()
	for !runtime.closed && (runtime.vm == nil || !time.Now().Before(runtime.vmExpires)) {
		refresh := runtime.refreshVM()
		runtime.vmMu.Unlock()
		select {
		case <-refresh.done:
		case <-ctx.Done():
		}
		runtime.vmMu.Lock()
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if refresh.err != nil {
			return "", refresh.err
		}
	}
	if runtime.closed {
		return "", errors.New("SpiderMonkey WAA runtime 已关闭")
	}
	vm := runtime.vm
	rounds, rerun := ctx.Value(pageRerunKey{}).(int)
	rerun = rerun && !runtime.pointer.Clicked().IsZero()
	if gesture, _ := ctx.Value(userGestureKey{}).(bool); gesture && !run && !rerun {
		if err := vm.Activate(ctx); err != nil {
			return "", err
		}
	}
	var proof string
	var err error
	switch {
	case run:
		proof, err = vm.RunProof(ctx, digest, prompt)
	case rerun:
		proof, err = vm.RerunProof(ctx, digest, rounds)
	default:
		proof, err = vm.Proof(ctx, digest, prompt)
	}
	go runtime.flushVMStreamz(context.Background(), vm)
	if err != nil {
		return "", fmt.Errorf("生成 fresh WAA proof: %w", err)
	}
	if run || rerun {
		runtime.running = vm
		runtime.elements.press(runtime.pointer.Clicked(), rerun)
	}
	if !strings.HasPrefix(proof, "!") {
		return "", errors.New("fresh WAA proof 前缀无效")
	}
	return proof, nil
}

// PageCatalog 返回页面载入时 GetLoggingContext、GetAiStudioBenefitTier 与 ListModels 的响应正文
func (runtime *goWAARuntime) PageCatalog() camoufoxnative.PageCatalog {
	return runtime.pageCatalog
}

// ProtocolHeaders 返回官网 GenerateContent 的六个公共头
func (runtime *goWAARuntime) ProtocolHeaders(ctx context.Context) (http.Header, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return runtime.headers.Clone(), nil
}

// SendProtected 以官网页面 XHR 的 Firefox 请求形状流式发送受保护请求；点击 Run 或 Rerun 后的 GenerateContent 在响应头到达与正文关闭时各派发一次指针下元素被替换的事件，并在界面元素日志中记录回答占位、回答结束与随后曝光的按钮
func (runtime *goWAARuntime) SendProtected(ctx context.Context, rawURL string, headers http.Header, body []byte) (*camoufoxnative.ProtectedResponse, error) {
	request := headers.Clone()
	request.Set("Accept", "*/*")
	request.Set("Referer", aiStudioOrigin+"/")
	request.Set("Origin", aiStudioOrigin)
	request.Set("Sec-Fetch-Dest", "empty")
	request.Set("Sec-Fetch-Mode", "cors")
	request.Set("Sec-Fetch-Site", "same-site")
	request.Del("Cookie")
	followUp, counting := followUpFrom(ctx)
	var running *waa.Runtime
	var turn *pageTurn
	if strings.HasSuffix(rawURL, "/GenerateContent") {
		runtime.vmMu.Lock()
		running, runtime.running = runtime.running, nil
		runtime.vmMu.Unlock()
		turn = runtime.elements.turn(body, counting)
	}
	if counting {
		companion := request.Clone()
		companion.Del("Priority")
		time.AfterFunc(followUp.Delay, func() {
			runtime.drain(runtime.do(context.WithoutCancel(ctx), http.MethodPost, followUp.URL, followUp.Body, companion))
			if turn != nil {
				turn.count(time.Now())
			}
		})
	}
	response, err := runtime.do(ctx, http.MethodPost, rawURL, body, request)
	if err != nil {
		return nil, err
	}
	if turn != nil {
		turn.started(time.Now())
	}
	if running == nil {
		return &camoufoxnative.ProtectedResponse{StatusCode: response.StatusCode, Header: response.Header, Body: response.Body}, nil
	}
	go running.Replaced(context.Background())
	return &camoufoxnative.ProtectedResponse{StatusCode: response.StatusCode, Header: response.Header, Body: &runningBody{ReadCloser: response.Body, vm: running, turn: turn}}, nil
}

// runningBody 是点击 Run 或 Rerun 后 GenerateContent 的响应正文，关闭时在 VM 中派发指针下元素被替换的事件并记录回答结束
type runningBody struct {
	io.ReadCloser
	vm *waa.Runtime
	// turn 是这次回答的界面元素日志，页面日志已关闭时为 nil
	turn *pageTurn
	once sync.Once
}

// Close 关闭响应正文，派发一次指针下元素被替换的事件并记录回答结束
func (body *runningBody) Close() error {
	body.once.Do(func() {
		go body.vm.Replaced(context.Background())
		if body.turn != nil {
			body.turn.done(time.Now())
		}
	})
	return body.ReadCloser.Close()
}

// StorageCookies 返回账户当前 Cookie，格式与 Camoufox storage.getCookies 导出一致
func (runtime *goWAARuntime) StorageCookies(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(runtime.cookies().Cookies)
}

// MergeSetCookieHeaders 把账户在页面之外取得的响应 Cookie 写入 runtime 的 Cookie 状态
func (runtime *goWAARuntime) MergeSetCookieHeaders(_ context.Context, headers []string, requestURL string) error {
	runtime.session.mu.Lock()
	defer runtime.session.mu.Unlock()
	return runtime.session.state.MergeSetCookieHeaders(headers, requestURL, time.Now())
}

// State 返回 SpiderMonkey runtime 的页面与协议状态
func (runtime *goWAARuntime) State() camoufoxnative.State {
	return camoufoxnative.State{
		PID:       os.Getpid(),
		PageURL:   runtime.pageURL,
		UserAgent: runtime.userAgent,
		Headers:   runtime.headers.Clone(),
	}
}

// Close 结束 VM 生命周期
func (runtime *goWAARuntime) Close() error {
	runtime.vmMu.Lock()
	defer runtime.vmMu.Unlock()
	runtime.closed = true
	if runtime.refreshTimer != nil {
		runtime.refreshTimer.Stop()
	}
	if runtime.refreshing != nil {
		runtime.refreshing.cancel()
	}
	if runtime.incidentsTimer != nil {
		runtime.incidentsTimer.Stop()
	}
	if runtime.vm != nil {
		runtime.closeVM(runtime.vm)
		runtime.vm = nil
	}
	runtime.closeTelemetry()
	return nil
}
