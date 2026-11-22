package aistudio

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	appconfig "github.com/Mag1cFall/AIStudio2API/internal/config"
	"github.com/Mag1cFall/AIStudio2API/internal/waa"
	"github.com/Mag1cFall/AIStudio2API/internal/waa/prooftime"
	"github.com/Mag1cFall/AIStudio2API/internal/waa/rhythm"
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
	SendProtected(context.Context, string, http.Header, protectedBody) (*camoufoxnative.ProtectedResponse, error)
	StorageCookies(context.Context) ([]byte, error)
	// MergeSetCookieHeaders 把账户在页面之外取得的响应 Cookie 写入页面
	MergeSetCookieHeaders(context.Context, []string, string) error
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
	// rhythm 是账户点击与粘贴的事件节奏
	rhythm rhythm.Rhythm
	// settings 是页面运行设置中上次写入的系统指令与函数声明，以及 Grounding with Google Search 与 Function calling 开关的状态
	settings pageSettings
	// previous 是重新载入前的页面，新页面的文档响应到达时它像官网页面卸载一样发出并停止上报
	previous *goWAARuntime
	// running 是最近一次点击 Run 或 Rerun 取 proof 的 VM，随后的 GenerateContent 响应开始与结束时在其中派发指针下元素被替换的事件
	running *waa.Runtime
	// chat 是本页点击后发出的上一个 GenerateContent 各对话轮次的 SHA-256 摘要，页面还没有对话、点过 New chat 或上一次响应不是 200 时为 nil；与 running 同由 vmMu 保护
	chat [][sha256.Size]byte

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
	// elementsSent 在本页上一批 2712 上报的请求结束时关闭
	elementsMu   sync.Mutex
	elementsSent chan struct{}
	// analytics 是页面载入的 GTM 与 GA4 上报
	analytics *pageAnalytics
	// windowID 是页面 sessionStorage 中的浏览器窗口 ID，重新载入后的页面沿用
	windowID string
	// peopleKey 是页面 WIZ 数据中经 gapi 读取账户资料的 API key
	peopleKey string
	// clientDetails 是页面 gapi 请求的 X-ClientDetails，取账户指纹的 appVersion、platform 与 User-Agent
	clientDetails string
	// cookieBarLanguage 是 Cookie 提示条按文档 lang 选出的文案语言
	cookieBarLanguage string
	// storage 是页面写入、随账户保存的 localStorage 项
	storage *pageStorage
	// startupDone 在页面启动请求全部返回后关闭；preferencesReady 在其中 GetUserPreferences 返回、preferences 记下响应正文后关闭；tokenReady 在第二个 GenerateAccessToken 返回、accessToken 记下 Drive access token 与取得时刻 tokenAt 后关闭；cookieBarDone 在本次启动的 2a.json 请求结束后关闭
	startupDone      chan struct{}
	preferencesReady chan struct{}
	tokenReady       chan struct{}
	cookieBarOnce    sync.Once
	cookieBarDone    chan struct{}
	preferences      []byte
	accessToken      string
	tokenAt          time.Time
	// mediaAcknowledged 表示页面已确认首次上传素材提示，skippedInserts 是空闲不够而没有做、尚未计入取 proof 跳过步数的插入附件操作步数
	mediaAcknowledged atomic.Bool
	skippedInserts    atomic.Int64
	// attachedFiles 是页面上传后显示在输入框中、尚未随 GenerateContent 发出的附件，键为 Drive 文件 ID，值为读取它的 pageDrive
	attachedFiles sync.Map
	// surveys 是页面在回答结束后请求 HaTS 问卷的状态
	surveys pageSurveys
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

// pageStartupRPCs 是官网页面启动时与 GetLoggingContext、Waa/Create、权益和模型目录请求先后发出的其余请求，按发出先后分为三组：GetUserPreferences 与第一个 GenerateAccessToken、第二个 GenerateAccessToken、ListIncidentsHistory 与 ListPromos
var pageStartupRPCs = [][]pageRPC{
	{{"GetUserPreferences", "[]"}, {"GenerateAccessToken", `["users/me"]`}},
	{{"GenerateAccessToken", `["users/me"]`}},
	{incidentsRPC, {"ListPromos", "[]"}},
}

// pageStartupTimes 是官网页面载入中各组启动请求在 GetLoggingContext 之后的发出时刻
// 单位为毫秒；每行是一次预检已缓存的官网载入，共四次
// 各列依次为 GetUserPreferences 与第一个 GenerateAccessToken、Waa/Create、第二个 GenerateAccessToken、
// ListIncidentsHistory 与 ListPromos、GetAiStudioBenefitTier 与 ListModels
var pageStartupTimes = [][5]int{{49, 119, 124, 125, 150}, {25, 36, 40, 61, 85}, {40, 53, 71, 95, 120}, {26, 38, 53, 74, 95}}

// pageStartupOffsets 返回这次载入各组启动请求在 GetLoggingContext 之后的发出时刻：随机取官网的一次载入，整体按 0.9–1.1 倍缩放
func pageStartupOffsets() [5]time.Duration {
	sample := pageStartupTimes[rand.IntN(len(pageStartupTimes))]
	scale := 0.9 + 0.2*rand.Float64()
	var offsets [5]time.Duration
	for index, milliseconds := range sample {
		offsets[index] = time.Duration(float64(milliseconds) * scale * float64(time.Millisecond))
	}
	return offsets
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
	// gdpClaim 为开关 45854328：导航栏为未加入 Google Developer Program、可领取额度的账户显示领取入口
	gdpClaim bool
	// billingTargeting 为开关 45822819：读取结算账号后以 EvaluateTargeting 查询各账号
	billingTargeting bool
	// loggingContextMaxAge 为页面数据 NHyHAe：localStorage 中上一次 GetLoggingContext 结果的有效毫秒数，0 时不使用
	loggingContextMaxAge int64
}

// parsePageFlags 读取官网页面内联数据中的构建开关
func parsePageFlags(body []byte) pageFlags {
	enabled := func(id string) bool { return bytes.Contains(body, []byte("["+id+",null,true,")) }
	flags := pageFlags{gdpProfile: enabled("45806340"), plusBenefit: enabled("45777963"), gdpClaim: enabled("45854328"), billingTargeting: enabled("45822819")}
	if match := loggingContextMaxAgePattern.FindSubmatch(body); match != nil {
		flags.loggingContextMaxAge, _ = strconv.ParseInt(string(match[1]), 10, 64)
	}
	return flags
}

// loggingContextMaxAgePattern 匹配页面数据中的 NHyHAe
var loggingContextMaxAgePattern = regexp.MustCompile(`"NHyHAe":(\d+)`)

// loggingContextStorageKey 是官网页面记录上一次 GetLoggingContext 结果的 localStorage 项
const loggingContextStorageKey = "ais_glcr"

// loggingContextRequest 像官网页面一样返回 GetLoggingContext 的请求正文：ais_glcr 记录的结果在 maxAge 毫秒内时带上它，否则为空消息
func loggingContextRequest(storage map[string]string, maxAge int64, now time.Time) []byte {
	var record struct {
		Timestamp json.Number `json:"timestamp"`
		Result    string      `json:"result"`
	}
	if maxAge <= 0 || json.Unmarshal([]byte(storage[loggingContextStorageKey]), &record) != nil || record.Timestamp == "" || record.Result == "" {
		return []byte("[]")
	}
	stamp, err := record.Timestamp.Int64()
	if err != nil || now.UnixMilli()-stamp > maxAge {
		return []byte("[]")
	}
	if record.Result == "true" {
		return []byte("[1]")
	}
	return []byte("[0]")
}

// loggingContextRecord 返回官网页面在 GetLoggingContext 响应后写入 ais_glcr 的值：响应第 2 项为 B 时结果为 true
func loggingContextRecord(body []byte, now time.Time) string {
	var response []json.RawMessage
	result := false
	if json.Unmarshal(body, &response) == nil && len(response) > 1 {
		var value string
		result = json.Unmarshal(response[1], &value) == nil && value == "B"
	}
	return fmt.Sprintf(`{"timestamp":%d,"result":"%t"}`, now.UnixMilli(), result)
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
	worker := &NativeWorker{
		accountID: accountID,
		runtime:   runtime,
		state: WorkerState{
			AccountID: accountID,
			Phase:     WorkerReady,
			PID:       os.Getpid(),
			RuntimeID: "spidermonkey-waa",
			PageURL:   runtime.pageURL,
		},
	}
	pageWorkers.Store(accountID, worker)
	return worker, nil
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
	next.previous = runtime
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
	runtime.profile.LocalStorage = map[string]string{}
	for _, origin := range runtime.cookies().Origins {
		if origin.Origin != aiStudioOrigin {
			continue
		}
		for _, item := range origin.LocalStorage {
			runtime.profile.LocalStorage[item.Name] = item.Value
		}
	}
	if runtime.storage, err = loadPageStorage(options.StorageStatePath); err != nil {
		return err
	}
	maps.Copy(runtime.profile.LocalStorage, runtime.storage.items)
	appVersion, _ := fingerprint["navigator.appVersion"].(string)
	platform, _ := fingerprint["navigator.platform"].(string)
	runtime.clientDetails = clientDetails(appVersion, platform, runtime.userAgent)
	runtime.cacheDirectory = filepath.Join(filepath.Dir(filepath.Dir(options.StorageStatePath)), appconfig.WAAInterpreterCacheName)
	reportGoStartup(options, camoufoxnative.StartupLoadingAIStudio)
	apiKey, document, err := runtime.loadPage(ctx)
	if err != nil {
		return err
	}
	if runtime.previous != nil {
		runtime.previous.closeTelemetry()
		runtime.previous = nil
	}
	runtime.uiLocale = uiLocale(document.frontendBundle())
	runtime.cookieBarLanguage = cookieBarLanguage(document.lang)
	runtime.serviceStreamz = runtime.newStreamz(nil)
	page, err := url.Parse(runtime.pageURL)
	if err != nil {
		return err
	}
	if err := runtime.loadDocumentResources(ctx, document); err != nil {
		return err
	}
	documentTiming(runtime.profile.Navigation, time.Since(runtime.profile.NavigationStart))
	go runtime.loadPageScripts(context.WithoutCancel(ctx))
	runtime.vitals = newPageVitals(runtime.sendClearcut, runtime.telemetry, runtime.uiLocale, runtime.location(), runtime.profile.NavigationStart, runtime.profile.Navigation, page.Path)
	if runtime.windowID == "" {
		runtime.windowID = pageWindowID()
	}
	runtime.elements = newPageElements(runtime.sendClearcut, runtime.uiLocale, runtime.location(), runtime.telemetry.experiments, runtime.windowID, runtime.profile.Locale, "models/"+strings.TrimPrefix(options.Model, "models/"))
	runtime.elements.viewport, _ = numberValue(runtime.profile.Window["innerHeight"])
	runtime.elements.width, _ = numberValue(runtime.profile.Window["innerWidth"])
	runtime.elements.render = func(part pageRenderPart, images []string) {
		runtime.loadRenderedResources(context.WithoutCancel(ctx), document, part, images)
	}
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
	loggingReady := make(chan struct{})
	var logged loggingContext
	go func() {
		extension, body, err := runtime.loggingContextHeader(ctx, apiKey, visitID)
		logged = loggingContext{extension, body, err}
		close(loggingReady)
		loggingDone <- logged
	}()
	loggedExtension := func() string {
		select {
		case <-loggingReady:
			return logged.extension
		default:
			return ""
		}
	}
	catalogDone := make(chan [][]byte, 1)
	pageStarted := time.Now()
	offsets := pageStartupOffsets()
	var catalogAt time.Time
	go func() {
		pauseContext(ctx, time.Until(pageStarted.Add(offsets[4])))
		tierDone := make(chan []byte, 1)
		go func() {
			tier := runtime.sendPageRPCs(ctx, apiKey, visitID, "", pageCatalogStartupRPCs[:1])[0]
			go runtime.requestGDPProfile(context.WithoutCancel(ctx), apiKey, visitID, tier, loggedExtension)
			tierDone <- tier
		}()
		models := runtime.sendPageRPCs(ctx, apiKey, visitID, "", pageCatalogStartupRPCs[1:])[0]
		bodies := [][]byte{<-tierDone, models}
		catalogAt = time.Now()
		catalogDone <- bodies
	}()
	runtime.requestStartup(context.WithoutCancel(ctx), apiKey, visitID, pageStarted, offsets)
	type startedVM struct {
		vm  *waa.Runtime
		err error
	}
	vmDone := make(chan startedVM, 1)
	pauseContext(ctx, time.Until(pageStarted.Add(offsets[1])))
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
	runtime.elements.catalog(catalogAt, tier.HeaderValue(), parseCatalogModels(catalog[1]))
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
		<-runtime.startupDone
		runtime.sendFollowupRPCs(context.WithoutCancel(ctx), apiKey, visitID, extension)
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
	go runtime.dismissOverlays(context.WithoutCancel(ctx))
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
	for _, device := range []struct{ count, kind string }{{"mediaDevices:micros", "audioinput"}, {"mediaDevices:webcams", "videoinput"}} {
		if count, _ := numberValue(config[device.count]); count > 0 {
			profile.MediaInputs = append(profile.MediaInputs, device.kind)
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
	if body == nil {
		return runtime.send(ctx, method, target, nil, headers)
	}
	return runtime.send(ctx, method, target, &protectedBody{data: body}, headers)
}

// send 与 do 相同，正文为 body，为 nil 时不带正文；写入 proof 的正文分段读出，不复制
func (runtime *goWAARuntime) send(ctx context.Context, method string, target string, body *protectedBody, headers http.Header) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = body.Reader()
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	if body != nil && body.proof != nil {
		request.ContentLength = int64(body.Len())
		request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(body.Reader()), nil }
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
	account, err := camoufoxnative.AccountRhythm(options)
	if err != nil {
		return nil, nil, err
	}
	runtime := &goWAARuntime{options: options, client: client, session: session, pageCache: cache, pointer: waa.NewPointer(), rhythm: account}
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
		runtime.peopleKey = peopleAPIKey(body)
		runtime.analytics = newPageAnalytics(runtime, body, target)
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

// loggingContextHeader 以官网页面的 visit ID 调用 GetLoggingContext，像官网页面一样把结果记入 localStorage 的 ais_glcr，返回编码为官网 X-Goog-Ext-519733851-Bin 的值与响应正文
func (runtime *goWAARuntime) loggingContextHeader(ctx context.Context, apiKey string, visitID string) (string, []byte, error) {
	headers, err := runtime.rpcHeaders(apiKey)
	if err != nil {
		return "", nil, err
	}
	headers.Set("X-AIStudio-Visit-Id", visitID)
	response, err := runtime.do(ctx, http.MethodPost, loggingContextURL, loggingContextRequest(runtime.storage.items, runtime.pageFlags.loggingContextMaxAge, time.Now()), headers)
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
	if err := runtime.storage.set(loggingContextStorageKey, loggingContextRecord(body, time.Now())); err != nil {
		return "", nil, err
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

// sendFollowupRPCs 发出官网页面在启动请求完成后接着发出的请求，带已返回的日志上下文 extension：最近应用与提示词列表存放在 Drive，与官网导航栏一样只在启动时的 GenerateAccessToken 取得 Drive access token 后读取
func (runtime *goWAARuntime) sendFollowupRPCs(ctx context.Context, apiKey string, visitID string, extension string) {
	if runtime.accessToken != "" {
		runtime.sendPageRPCs(ctx, apiKey, visitID, extension, pageFollowupRPCs)
	}
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
	sentSession := hasSessionCookie(runtime.cookies(), waaCreateURL, started)
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
	if sentSession && !responseConfirmedSession(response.Header.Values("Set-Cookie")) {
		return waa.Challenge{}, fmt.Errorf("%w: Waa/Create 未确认账户会话", camoufoxnative.ErrAuthenticationRequired)
	}
	status = 0
	decoded := time.Now()
	challenge, err := waa.ParseChallenge(raw)
	if err == nil && challenge.Packed {
		runtime.serviceStreamz.record(streamzRecord{metric: "el", labels: []any{"U", streamzMachineKey}, value: float64(time.Since(decoded).Milliseconds())})
	}
	return challenge, err
}

// hasSessionCookie 判断状态中包含用于目标地址的 Google 登录会话 Cookie
func hasSessionCookie(state StorageState, targetURL string, now time.Time) bool {
	for _, name := range []string{"SID", "__Secure-1PSID", "__Secure-3PSID"} {
		if _, ok := state.CookieValue(name, targetURL, now); ok {
			return true
		}
	}
	return false
}

// responseConfirmedSession 判断响应通过 SIDCC 系列 Set-Cookie 确认了账户会话
func responseConfirmedSession(headers []string) bool {
	for _, header := range headers {
		for _, cookie := range (&http.Response{Header: http.Header{"Set-Cookie": []string{header}}}).Cookies() {
			if rotatingCookieNames[cookie.Name] && cookie.Value != "" && cookie.MaxAge >= 0 {
				return true
			}
		}
	}
	return false
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
	if err := runtime.pageCache.store(challenge.InterpreterURL, response, time.Now(), pageCacheNotes{}); err != nil {
		return "", err
	}
	if response.StatusCode == http.StatusOK {
		if err := os.MkdirAll(runtime.cacheDirectory, 0o755); err == nil {
			_ = os.WriteFile(path, data, 0o644)
		}
	}
	return string(data), nil
}

// startVM 加载 challenge 指定的解释器并初始化新 VM，VM 从导航计时的 loadEventEnd 开始
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
	var loaded time.Time
	if loadEnd, ok := profile.Navigation["loadEventEnd"].(int); ok {
		loaded = profile.NavigationStart.Add(time.Duration(loadEnd) * time.Millisecond)
	}
	vm, err := waa.NewRuntime(ctx, waa.Options{Challenge: challenge, Interpreter: interpreter, Profile: profile, LoadImage: runtime.loadImage, LoadFrame: runtime.loadFrame, Pointer: runtime.pointer, Rhythm: runtime.rhythm, Start: loaded})
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

// closeTelemetry 像官网页面卸载一样发出尚未发出的界面元素事件、2584 计数与 GA4 事件，停止页面全部 1828 定时上报与尚未发出的 1957 上报
func (runtime *goWAARuntime) closeTelemetry() {
	if runtime.vitals != nil {
		runtime.vitals.close()
	}
	if runtime.elements != nil {
		runtime.elements.close()
	}
	runtime.analytics.close()
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
	headers.Set("Accept", imageAccept)
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
	if runtime.options.RefreshCookies != nil {
		if err := runtime.options.RefreshCookies(ctx); err != nil {
			return nil, err
		}
	}
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
	return runtime.proof(ctx, digest, prompt, proofSync, 0, nil, nil)
}

// SubmitProof 像官网用户一样在 rounds 轮对话最后一轮的函数调用块中点击函数响应输入框、粘贴 result 并点击提交后为 SHA-256 digest 生成 fresh WAA proof
func (runtime *goWAARuntime) SubmitProof(ctx context.Context, digest string, result string, rounds int) (string, error) {
	return runtime.proof(ctx, digest, result, proofSubmit, rounds, nil, nil)
}

// RunProof 为 SHA-256 digest 生成 GenerateContent 的 fresh WAA proof，返回 proof 与粘贴进页面的提示词
// ctx 带 pageRerunKey 且本页面点过 Run 时点击最后一轮的 Rerun，其余粘贴 prompt 后点击 Run
// prompt 为空时取 body 最后一个 user 轮次的文本；body 的对话不接着本页上一次的对话时先点 New chat
// 因空闲不够没有做的插入附件操作步数计入这次取 proof 的跳过步数
func (runtime *goWAARuntime) RunProof(ctx context.Context, digest string, prompt string, body []byte) (string, string, error) {
	prooftime.Skip(ctx, int(runtime.skippedInserts.Swap(0)))
	if _, rerun := ctx.Value(pageRerunKey{}).(int); rerun && !runtime.pointer.Clicked().IsZero() {
		proof, err := runtime.proof(ctx, digest, "", proofSync, 0, nil, nil)
		return proof, "", err
	}
	if prompt == "" {
		prompt, _ = camoufoxnative.GeneratePrompt(body)
	}
	proof, err := runtime.proof(ctx, digest, prompt, proofRun, 0, runtime.settingSteps(body), generateContents(body))
	return proof, prompt, err
}

// generateContents 返回 GenerateContent 正文中的对话轮次
func generateContents(body []byte) []json.RawMessage {
	var contents []json.RawMessage
	if fields, ok := wireArray(body); ok && len(fields) > 1 {
		contents, _ = wireArray(fields[1])
	}
	return contents
}

// chatDigests 返回 GenerateContent 正文各对话轮次的 SHA-256 摘要，不持有正文底层数组
func chatDigests(body []byte) [][sha256.Size]byte {
	contents := generateContents(body)
	if contents == nil {
		return nil
	}
	digests := make([][sha256.Size]byte, len(contents))
	for index, content := range contents {
		digests[index] = sha256.Sum256(content)
	}
	return digests
}

// startsNewChat 返回页面上一次发出的对话轮次摘要 previous 之后要发出 contents 时是否先开新对话
func startsNewChat(previous [][sha256.Size]byte, contents []json.RawMessage) bool {
	if previous == nil {
		return false
	}
	samePrefix := func() bool {
		for index, digest := range previous {
			if sha256.Sum256(contents[index]) != digest {
				return false
			}
		}
		return true
	}
	if len(contents) == len(previous) && samePrefix() {
		return false
	}
	if len(contents) == len(previous)+2 && samePrefix() && wireContentRole(contents[len(previous)]) == "model" {
		return false
	}
	return true
}

// wireContentRole 返回 GenerateContent 对话轮次 [parts, role] 的角色
func wireContentRole(content json.RawMessage) string {
	fields, ok := wireArray(content)
	if !ok || len(fields) <= 1 {
		return ""
	}
	var role string
	_ = json.Unmarshal(fields[1], &role)
	return role
}

// userGestureKey 标记 proof 所属请求在官网由页面上的点击或按键触发
type userGestureKey struct{}

// pageRerunKey 标记 GenerateContent 是账户页面再次运行同一对话，与官网点击最后一轮的 Rerun 相同，取值为对话的轮数
type pageRerunKey struct{}

// proofInput 是取 proof 前在页面上的界面操作
type proofInput int

const (
	// proofSync 只同步页面提示词，或在本页点过 Run 后点击最后一轮的 Rerun
	proofSync proofInput = iota
	// proofRun 粘贴提示词并点击 Run
	proofRun
	// proofSubmit 在函数调用块中粘贴函数结果并点击提交
	proofSubmit
)

// proof 以当前 VM 生成 fresh WAA proof
// input 为 proofRun 时依次点 New chat（页面上一次的对话不接到 contents 时，空闲放不下时不做）、做 settings 中的各组运行设置、
// 写入提示词并以可信鼠标点击 Run；为 proofSubmit 时在 submitted 轮对话最后一轮的函数调用块中粘贴函数结果并点击提交
// ctx 带 pageRerunKey 且本页面点过 Run 时点击最后一轮的 Rerun，其余 ctx 带 userGestureKey 的请求先记录一次用户操作
// VM 到期时等待后台重建完成，等待随 ctx 结束
func (runtime *goWAARuntime) proof(ctx context.Context, digest string, prompt string, input proofInput, submitted int, settings [][]waa.SettingStep, contents []json.RawMessage) (string, error) {
	lockStarted := time.Now()
	runtime.vmMu.Lock()
	defer runtime.vmMu.Unlock()
	prooftime.Observe(ctx, prooftime.Lock, time.Since(lockStarted))
	for !runtime.closed && (runtime.vm == nil || !time.Now().Before(runtime.vmExpires)) {
		refresh := runtime.refreshVM()
		runtime.vmMu.Unlock()
		refreshStarted := time.Now()
		select {
		case <-refresh.done:
		case <-ctx.Done():
		}
		prooftime.Observe(ctx, prooftime.Refresh, time.Since(refreshStarted))
		lockStarted = time.Now()
		runtime.vmMu.Lock()
		prooftime.Observe(ctx, prooftime.Lock, time.Since(lockStarted))
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
	run := input == proofRun
	if gesture, _ := ctx.Value(userGestureKey{}).(bool); gesture && input == proofSync && !rerun {
		activateStarted := time.Now()
		if err := vm.Activate(ctx); err != nil {
			return "", err
		}
		prooftime.Observe(ctx, prooftime.Interaction, time.Since(activateStarted))
	}
	var proof string
	var err error
	switch {
	case run:
		var newChat func(at time.Time)
		if startsNewChat(runtime.chat, contents) {
			newChat = runtime.newChat
		}
		proof, err = vm.RunProofAfter(ctx, digest, prompt, newChat, settings, pickedPause)
	case input == proofSubmit:
		proof, err = vm.SubmitProof(ctx, digest, prompt, submitted)
	case rerun:
		proof, err = vm.RerunProof(ctx, digest, rounds)
	default:
		proof, err = vm.Proof(ctx, digest, prompt)
	}
	go runtime.flushVMStreamz(context.Background(), vm)
	if err != nil {
		return "", fmt.Errorf("生成 fresh WAA proof: %w", err)
	}
	switch {
	case run || rerun:
		runtime.running = vm
		runtime.elements.press(runtime.pointer.Clicked(), rerun)
		if run {
			runtime.analytics.clicked(runtime.pointer.Clicked())
		}
	case input == proofSubmit:
		runtime.running = vm
		runtime.elements.submit()
	}
	if !strings.HasPrefix(proof, "!") {
		return "", errors.New("fresh WAA proof 前缀无效")
	}
	return proof, nil
}

// newChatRoute 是官网点确认框的 Discard and continue 到页面渲染新对话页、提示词输入框取得焦点的间隔
const newChatRoute = 383 * time.Millisecond

// newChat 记录在 at 点确认框的 Discard and continue 后页面切换到新对话页：界面元素日志、web vitals 与 GA4 记下 newChatRoute 后的路由切换，本页对话清空；在取 proof 的界面操作中调用，调用方持有 vmMu
func (runtime *goWAARuntime) newChat(at time.Time) {
	route := at.Add(newChatRoute)
	runtime.chat = nil
	if runtime.elements != nil {
		runtime.elements.newChat(route)
	}
	if runtime.vitals != nil {
		runtime.vitals.route()
	}
	runtime.analytics.newChat(route)
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
func (runtime *goWAARuntime) SendProtected(ctx context.Context, rawURL string, headers http.Header, protected protectedBody) (*camoufoxnative.ProtectedResponse, error) {
	body := protected.data
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
		if running != nil {
			runtime.chat = chatDigests(body)
		}
		runtime.vmMu.Unlock()
		turn = runtime.elements.turn(body, counting)
		if turn != nil {
			turn.started()
			runtime.surveyStarted(turn)
		}
		runtime.readAttached(ctx, body)
	}
	if counting {
		companion := request.Clone()
		companion.Del("Priority")
		time.AfterFunc(followUp.Delay, func() {
			counted := runtime.drain(runtime.do(context.WithoutCancel(ctx), http.MethodPost, followUp.URL, followUp.Body, companion))
			if turn != nil {
				count, _ := ParseTokenCount(bytes.NewReader(counted))
				turn.count(time.Now(), int(count.InputTokens))
			}
		})
	}
	response, err := runtime.send(ctx, http.MethodPost, rawURL, &protected, request)
	if running != nil && (err != nil || response.StatusCode != http.StatusOK) {
		runtime.vmMu.Lock()
		runtime.chat = nil
		runtime.vmMu.Unlock()
	}
	if err != nil {
		return nil, err
	}
	if running == nil {
		return &camoufoxnative.ProtectedResponse{StatusCode: response.StatusCode, Header: response.Header, Body: response.Body}, nil
	}
	go running.Replaced(context.Background())
	return &camoufoxnative.ProtectedResponse{StatusCode: response.StatusCode, Header: response.Header, Body: &runningBody{ReadCloser: response.Body, vm: running, turn: turn, runtime: runtime}}, nil
}

// runningBody 是点击 Run、Rerun 或提交函数结果后 GenerateContent 的响应正文，读到第一个思考 Part 时记录思考面板、第一个函数调用 Part 时记录函数调用块，关闭时在 VM 中派发指针下元素被替换的事件，并以响应末尾的 usage 记录回答结束，再按正文是否读到结尾处理页面问卷
type runningBody struct {
	io.ReadCloser
	vm *waa.Runtime
	// turn 是这次回答的界面元素日志，页面日志已关闭时为 nil
	turn *pageTurn
	once sync.Once
	// tail 是读到的最后 runningTailSize 个字节，用于查找跨读取边界的思考 Part 与最后一帧的 usage
	tail []byte
	// thought 表示已读到思考 Part，call 表示已读到函数调用 Part，text 表示已读到思考之后的回答正文 Part
	thought, call, text bool
	// reads 是读到数据的次数，textFrom 是 tail 中查找回答正文 Part 的起点，answerFrom 是 tail 中取回答正文增量的起点
	reads, textFrom, answerFrom int
	// runtime 是发出这次 GenerateContent 的页面，ended 表示正文已读到结尾
	runtime *goWAARuntime
	ended   bool
}

// runningTailSize 是响应正文保留的末尾字节数，覆盖最后一帧 usage 之后的字段
const runningTailSize = 8 << 10

var (
	// thoughtPartMarker 是响应流中思考文本 Part 在文本之后的字段：十个空字段后 thought 为 1
	thoughtPartMarker = []byte(`",null,null,null,null,null,null,null,null,null,null,1`)
	// frameUsagePattern 匹配响应帧 candidates 之后 usage 的输入、可见输出与总 token 数
	frameUsagePattern = regexp.MustCompile(`,null,\[(\d+),(\d*|null),(\d+)`)
)

// Read 读取响应正文，第一次读到思考文本 Part 时在界面元素日志中记录思考面板，第一次读到函数调用 Part 时记录函数调用块，Rerun 在思考之后第一次读到回答正文 Part 时记录正文段，其余后续数据记为对话区滚动，思考之外的滚动在 VM 中派发静止指针下元素变化的事件
func (body *runningBody) Read(buffer []byte) (int, error) {
	read, err := body.ReadCloser.Read(buffer)
	body.ended = body.ended || err == io.EOF
	if read > 0 && body.turn != nil {
		start := len(body.tail)
		body.tail = append(body.tail, buffer[:read]...)
		scrolled := body.reads > 0
		body.reads++
		if !body.thought && bytes.Contains(body.tail[max(start-len(thoughtPartMarker)+1, 0):], thoughtPartMarker) {
			body.thought, scrolled = true, false
			body.turn.thought(time.Now())
		}
		if !body.call && bytes.Contains(body.tail[max(start-len(camoufoxnative.FunctionCallMarker)+1, 0):], camoufoxnative.FunctionCallMarker) {
			body.call, scrolled = true, false
			body.turn.functionCall(time.Now())
		}
		if body.thought && !body.text {
			if body.text, body.textFrom = answerTextPart(body.tail, body.textFrom); body.text && body.turn.rerun {
				scrolled = false
				body.turn.answerText(time.Now())
			}
		}
		var grown string
		grown, body.answerFrom = answerTexts(body.tail, body.answerFrom)
		if grown != "" {
			body.turn.grew(time.Now(), grown)
		}
		if scrolled {
			body.turn.streamed(time.Now())
			if body.text || !body.thought {
				body.vm.Streamed()
			}
		}
		if len(body.tail) > runningTailSize {
			trimmed := len(body.tail) - runningTailSize
			body.tail = append(body.tail[:0], body.tail[trimmed:]...)
			body.textFrom = max(body.textFrom-trimmed, 0)
			body.answerFrom = max(body.answerFrom-trimmed, 0)
		}
	}
	return read, err
}

// framePartPrefix 是响应帧中第一个 Part 以文本开头时文本之前的字段
var framePartPrefix = []byte(`[[[[[[null,"`)

// answerTextPart 从 data[from:] 起查找文本非空且文本后没有其他字段的 Part（回答正文，思考与思考签名在文本后还有字段），返回是否找到与下次查找的起点
func answerTextPart(data []byte, from int) (bool, int) {
	for {
		index := bytes.Index(data[from:], framePartPrefix)
		if index < 0 {
			return false, max(from, len(data)-len(framePartPrefix)+1)
		}
		start := from + index + len(framePartPrefix)
		end := start
		for end < len(data) && data[end] != '"' {
			if data[end] == '\\' {
				end++
			}
			end++
		}
		if end+1 >= len(data) {
			return false, from + index
		}
		if end > start && data[end+1] == ']' {
			return true, end
		}
		from = end + 1
	}
}

// answerTexts 返回 data[from:] 中完整到达的各回答正文 Part 的文本之和与下次查找的起点
func answerTexts(data []byte, from int) (string, int) {
	var text strings.Builder
	for {
		index := bytes.Index(data[from:], framePartPrefix)
		if index < 0 {
			return text.String(), max(from, len(data)-len(framePartPrefix)+1)
		}
		start := from + index + len(framePartPrefix)
		end := start
		for end < len(data) && data[end] != '"' {
			if data[end] == '\\' {
				end++
			}
			end++
		}
		if end+1 >= len(data) {
			return text.String(), from + index
		}
		if data[end+1] == ']' {
			var value string
			if json.Unmarshal(data[start-1:end+1], &value) == nil {
				text.WriteString(value)
			}
		}
		from = end + 1
	}
}

// Close 关闭响应正文，派发一次指针下元素被替换的事件，记录回答结束并处理页面问卷
func (body *runningBody) Close() error {
	body.once.Do(func() {
		go body.vm.Replaced(context.Background())
		if body.turn != nil {
			if matches := frameUsagePattern.FindAllSubmatch(body.tail, -1); matches != nil {
				usage := matches[len(matches)-1]
				input, _ := strconv.Atoi(string(usage[1]))
				output, _ := strconv.Atoi(string(usage[2]))
				total, _ := strconv.Atoi(string(usage[3]))
				body.turn.usage(input, output, total)
			}
			body.turn.done(time.Now())
			body.runtime.surveyEnded(body.ended && frameUsagePattern.Match(body.tail))
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
	runtime.surveys.close()
	runtime.closeTelemetry()
	return nil
}
