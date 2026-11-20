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
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/google/uuid"
)

const (
	// googleFontsHost 是官网文档引用的字体样式表主机
	googleFontsHost = "fonts.googleapis.com"
	// cookieBarScript 是官网文档引用的 Cookie 提示条脚本
	cookieBarScript = "https://www.gstatic.com/glue/cookienotificationbar/cookienotificationbar.min.js"
	// cookieBarConfig 是 Cookie 提示条脚本读取的文案配置
	cookieBarConfig = "https://www.gstatic.com/glue/cookienotificationbar/config/2a.json"
	// cookieBarSite 是官网页面交给 Cookie 提示条的站点名
	cookieBarSite = "Google AI Studio"
	// cookieBarStorageKey 是 Cookie 提示条记录用户选择的 localStorage 项
	cookieBarStorageKey = "glue.CookieNotificationBar"
	// auditRecordURL 是 Cookie 提示条记录用户选择的 CreateAuditRecord 地址
	auditRecordURL = "https://auditrecording-pa.googleapis.com/$rpc/google.internal.api.auditrecording.v1.AuditRecordingMobileService/CreateAuditRecord"
	// gapiLoaderScript 是官网文档预取、页面随后载入的 gapi 加载脚本
	gapiLoaderScript = "https://apis.google.com/js/api.js"
	// feedbackScript 是官网页面载入的反馈服务脚本
	feedbackScript = "https://www.gstatic.com/feedback/js/help/prod/service/lazy.min.js"
	// peopleBatchURL 是页面经 gapi 读取账户头像的批量请求地址，后接 boundary
	peopleBatchURL = "https://people-pa.clients6.google.com/batch?%24ct=multipart%2Fmixed%3B%20boundary%3D"
	// lockupImage 是页面框架左上角的产品标志
	lockupImage = "https://www.gstatic.com/images/branding/productlogos/makersuite/lockup_dark.svg"
	// googleLogoImage 是新对话页主体中的 Google 标志
	googleLogoImage = "https://www.gstatic.com/images/branding/productlogos/googleg/v6/24px.svg"
	// thinkingImage 是回答中思考面板的图标
	thinkingImage = "https://www.gstatic.com/aistudio/watermark/watermark.png"
	// imageAccept 是 Firefox 158 图片请求的 Accept
	imageAccept = "image/avif,image/jxl,image/webp,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"
	// fontAccept 是 Firefox 158 字体请求的 Accept
	fontAccept = "application/font-woff2;q=1.0,application/font-woff;q=0.9,*/*;q=0.8"
	// pageStorageFile 是账户目录下保存 SpiderMonkey 页面写入的 localStorage 项的文件
	pageStorageFile = "spidermonkey-storage.json"
)

var (
	// cookieBarKeyPattern 匹配 Cookie 提示条脚本中 CreateAuditRecord 的 API key
	cookieBarKeyPattern = regexp.MustCompile(`"X-Goog-Api-Key":"(AIza[0-9A-Za-z_-]+)"`)
	// gapiBasePattern 匹配 api.js 配置中 gapi 模块的路径与 rs 参数
	gapiBasePattern = regexp.MustCompile(`h:"m;(/_/scs/abc-static/_/js/k=[^/"]+)/d=1/rs=([^/"]+)/m=__features__"`)
	// gapiExtensionsPattern 匹配 api.js 配置中的 le 列表
	gapiExtensionsPattern = regexp.MustCompile(`\ble:\[([^\]]*)\]`)
	// gapiPushedExtensionPattern 匹配 api.js 末尾追加到 le 的扩展
	gapiPushedExtensionPattern = regexp.MustCompile(`"le",\[\]\)\.push\("([^"]+)"\)`)
	// fontFacePattern 匹配样式表中的一个 @font-face 块
	fontFacePattern = regexp.MustCompile(`@font-face\s*\{([^}]*)\}`)
	// fontFamilyPattern 匹配 @font-face 中的字体族名
	fontFamilyPattern = regexp.MustCompile(`font-family:\s*'([^']+)'`)
	// fontSourcePattern 匹配 @font-face 中的字体地址
	fontSourcePattern = regexp.MustCompile(`src:\s*url\(([^)]+)\)`)
	// peoplePhotoPattern 匹配 people-pa 响应中账户头像的地址
	peoplePhotoPattern = regexp.MustCompile(`"url":\s*"(https://lh3\.googleusercontent\.com/[^"]+)"`)
)

// latinFontFaces 返回 Google Fonts 样式表中拉丁字符子集（unicode-range 以 U+0000-00FF 开头）的字体地址，按字体族名索引
func latinFontFaces(css []byte) map[string]string {
	faces := map[string]string{}
	for _, block := range fontFacePattern.FindAllSubmatch(css, -1) {
		family, source := fontFamilyPattern.FindSubmatch(block[1]), fontSourcePattern.FindSubmatch(block[1])
		if family == nil || source == nil || !bytes.Contains(block[1], []byte("unicode-range: U+0000-00FF")) {
			continue
		}
		if _, ok := faces[string(family[1])]; !ok {
			faces[string(family[1])] = string(source[1])
		}
	}
	return faces
}

// gapiClientURL 按 api.js 中的配置推出 gapi.load("client") 载入的模块地址
func gapiClientURL(script []byte) (string, error) {
	base := gapiBasePattern.FindSubmatch(script)
	names, ok := gapiExtensions(script)
	if base == nil || !ok {
		return "", errors.New("api.js 缺少 gapi 模块配置")
	}
	for _, match := range gapiPushedExtensionPattern.FindAllSubmatch(script, -1) {
		names = append(names, string(match[1]))
	}
	return "https://apis.google.com" + string(base[1]) + "/m=client/rt=j/sv=1/d=1/ed=1/rs=" + string(base[2]) + "/cb=gapi.loaded_0?le=" + strings.Join(names, ","), nil
}

// gapiExtensions 返回 gapi 加载脚本配置中声明的 le 列表，配置中没有 le 时返回 false
func gapiExtensions(script []byte) ([]string, bool) {
	extensions := gapiExtensionsPattern.FindSubmatch(script)
	if extensions == nil {
		return nil, false
	}
	var names []string
	for _, item := range strings.Split(string(extensions[1]), ",") {
		if name := strings.Trim(strings.TrimSpace(item), `"`); name != "" {
			names = append(names, name)
		}
	}
	return names, true
}

// cookieBarLanguages 是 Cookie 提示条有文案的语言
var cookieBarLanguages = strings.Fields("ar bg ca cs da de el en en-GB es es-419 et eu fi fr fr-CA gl hr hu is it iw ja ko lt lv mt nl no pl pt-BR pt-PT ro ru sk sl sr sv tr uk")

// cookieBarLanguage 按 Cookie 提示条脚本的规则从文档 lang 选出文案语言：先取“语言-地区”，再取语言，都没有文案时为空
func cookieBarLanguage(lang string) string {
	parts := strings.FieldsFunc(lang, func(r rune) bool { return r == '-' || r == '_' })
	if len(parts) == 0 {
		return ""
	}
	region := parts[0]
	if len(parts) > 1 {
		region = parts[1]
	}
	if tag := parts[0] + "-" + strings.ToUpper(region); slices.Contains(cookieBarLanguages, tag) {
		return tag
	}
	if slices.Contains(cookieBarLanguages, parts[0]) {
		return parts[0]
	}
	return ""
}

// cookieBarConfigURL 返回 Cookie 提示条按文案语言读取的配置地址
func cookieBarConfigURL(language string) string {
	if language == "" {
		return cookieBarConfig
	}
	return cookieBarConfig + "?hl=" + language
}

// loadPageScripts 在文档子资源载入后按官网页面的时序请求前端主程序随后载入的脚本与配置：预取 api.js、反馈服务脚本与 Cookie 提示条文案（载入后提示条回调载入 GTM），再载入 gapi client 模块，最后经 gapi 读取账户资料并载入头像；失败不影响页面
func (runtime *goWAARuntime) loadPageScripts(ctx context.Context) {
	start := time.Now()
	wait := func(offset time.Duration) bool {
		timer := time.NewTimer(time.Until(start.Add(offset + time.Duration(rand.IntN(40))*time.Millisecond)))
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}
	// 同账户的第二个标签页从缓存载入 gapi 的加载脚本与 client 模块，页面应用开始请求后 180–720 ms 读取账户资料
	client := runtime.pageCache.notes(gapiLoaderScript).Gapi
	_, loaderCached := runtime.pageCache.lookup(gapiLoaderScript, start)
	_, clientCached := runtime.pageCache.lookup(client, start)
	cached := client != "" && loaderCached && clientCached
	if cached {
		go func() {
			if wait(time.Duration(180+rand.IntN(500)) * time.Millisecond) {
				runtime.loadAvatar(ctx)
			}
		}()
	}
	if !wait(770 * time.Millisecond) {
		return
	}
	prefetch := http.Header{}
	prefetch.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	prefetch.Set("Referer", aiStudioOrigin+"/")
	prefetch.Set("Sec-Purpose", "prefetch")
	prefetch.Set("Sec-Fetch-Dest", "empty")
	prefetch.Set("Sec-Fetch-Mode", "no-cors")
	prefetch.Set("Sec-Fetch-Site", fetchSite(gapiLoaderScript))
	prefetch.Set("Priority", "u=6")
	loaded := make(chan error, 1)
	go func() { loaded <- runtime.fetchCached(ctx, gapiLoaderScript, prefetch) }()
	go func() {
		_ = runtime.fetchCached(ctx, feedbackScript, subresourceHeaders(feedbackScript, "*/*", "script", ""))
	}()
	go func() {
		target := cookieBarConfigURL(runtime.cookieBarLanguage)
		if runtime.fetchCached(ctx, target, corsHeaders(target)) == nil {
			runtime.analytics.load(ctx)
		}
	}()
	if <-loaded != nil || cached || !wait(1410*time.Millisecond) {
		return
	}
	client = runtime.pageCache.notes(gapiLoaderScript).Gapi
	if client == "" || runtime.fetchCached(ctx, client, subresourceHeaders(client, "*/*", "script", "")) != nil || !wait(2320*time.Millisecond) {
		return
	}
	runtime.loadAvatar(ctx)
}

// loadAvatar 经 gapi 读取账户资料并载入页面右上角的头像，失败不影响页面
func (runtime *goWAARuntime) loadAvatar(ctx context.Context) {
	photo, err := runtime.loadProfile(ctx)
	if err != nil || photo == "" {
		return
	}
	_ = runtime.fetchCached(ctx, photo, subresourceHeaders(photo, imageAccept, "image", "u=5, i"))
}

// corsHeaders 返回 Firefox 从官网页面以 fetch 跨域读取资源的请求头
func corsHeaders(target string) http.Header {
	headers := http.Header{}
	headers.Set("Accept", "*/*")
	headers.Set("Referer", aiStudioOrigin+"/")
	headers.Set("Origin", aiStudioOrigin)
	headers.Set("Sec-Fetch-Dest", "empty")
	headers.Set("Sec-Fetch-Mode", "cors")
	headers.Set("Sec-Fetch-Site", fetchSite(target))
	headers.Set("Priority", "u=4")
	return headers
}

// peopleAPIKey 返回官网页面 WIZ 数据中经 gapi 读取账户资料的 API key（PeqOqb）
func peopleAPIKey(body []byte) string {
	for _, match := range wizDataPattern.FindAllSubmatch(body, -1) {
		var data struct {
			Key string `json:"PeqOqb"`
		}
		if json.Unmarshal(match[1], &data) == nil && data.Key != "" {
			return data.Key
		}
	}
	return ""
}

// loadProfile 像页面经 gapi client 一样以 multipart 批量请求读取账户的姓名、邮箱与头像，返回页面右上角头像的 64 像素地址
func (runtime *goWAARuntime) loadProfile(ctx context.Context) (string, error) {
	if runtime.peopleKey == "" {
		return "", errors.New("页面缺少 PeqOqb")
	}
	authorization, err := NewSigner().AuthorizationAt(runtime.cookies(), time.Now())
	if err != nil {
		return "", err
	}
	boundary := "batch" + strconv.FormatUint(rand.Uint64()>>1, 10)
	lines := []string{
		"--" + boundary,
		"Content-Type: application/http",
		"Content-Transfer-Encoding: binary",
		"Content-ID: <" + boundary + "+gapiRequest@googleapis.com>",
		"",
		"GET /v2/people?personId=me&requestMask.includeContainer=ACCOUNT&requestMask.includeContainer=PROFILE&requestMask.includeContainer=DOMAIN_PROFILE&requestMask.includeField=person.name%2Cperson.email%2Cperson.photo&key=" + runtime.peopleKey,
		"X-JavaScript-User-Agent: google-api-javascript-client/1.1.0",
		"X-Requested-With: XMLHttpRequest",
		"X-Goog-Encode-Response-If-Executable: base64",
		"X-Goog-AuthUser: " + strconv.Itoa(runtime.options.AuthUser),
		"Authorization: " + authorization,
		"X-ClientDetails: " + runtime.clientDetails,
		"",
		"--" + boundary + "--",
		"",
	}
	headers := corsHeaders(peopleBatchURL)
	headers.Set("Content-Type", "text/plain; charset=UTF-8")
	headers.Del("Priority")
	response, err := runtime.do(ctx, http.MethodPost, peopleBatchURL+boundary, []byte(strings.Join(lines, "\r\n")), headers)
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		return "", err
	}
	match := peoplePhotoPattern.FindSubmatch(body)
	if match == nil {
		return "", nil
	}
	photo := string(match[1])
	base, options, found := strings.Cut(photo, "=")
	if !found {
		return photo + "=s64-cc", nil
	}
	return base + "=s64-cc-" + options, nil
}

// loadRenderedResources 在页面框架、主体、入门卡片与思考面板渲染时请求其中的图片与字体：框架为产品标志、Inter 与 Google Symbols，主体为 Inter Tight 与 Google 标志，入门卡片为示例应用的预览图，思考面板为思考图标
func (runtime *goWAARuntime) loadRenderedResources(ctx context.Context, document pageDocument, part pageRenderPart, images []string) {
	var fonts [][2]string
	switch part {
	case pageRenderShell:
		images = append(images, lockupImage)
		fonts = append(fonts, [2]string{runtime.fontURL(document, "Inter"), "https://" + googleFontsHost + "/"}, [2]string{document.symbolsFont, aiStudioOrigin + "/"})
	case pageRenderMain:
		images = append(images, googleLogoImage)
		fonts = append(fonts, [2]string{runtime.fontURL(document, "Inter Tight"), "https://" + googleFontsHost + "/"})
	case pageRenderThoughts:
		images = append(images, thinkingImage)
	}
	priority := "u=4, i"
	if part == pageRenderCards || part == pageRenderThoughts {
		priority = "u=5, i"
	}
	for _, image := range images {
		go func() { _ = runtime.fetchCached(ctx, image, subresourceHeaders(image, imageAccept, "image", priority)) }()
	}
	for _, font := range fonts {
		if font[0] == "" {
			continue
		}
		headers := http.Header{}
		headers.Set("Accept", fontAccept)
		headers.Set("Accept-Encoding", "identity")
		headers.Set("Origin", aiStudioOrigin)
		headers.Set("Referer", font[1])
		headers.Set("Sec-Fetch-Dest", "font")
		headers.Set("Sec-Fetch-Mode", "cors")
		headers.Set("Sec-Fetch-Site", fetchSite(font[0]))
		go func() { _ = runtime.fetchCached(ctx, font[0], headers) }()
	}
}

// fontURL 返回文档引用的 Google Fonts 样式表中该字体族拉丁字符子集的地址
func (runtime *goWAARuntime) fontURL(document pageDocument, family string) string {
	for _, resource := range document.resources {
		if strings.HasPrefix(resource.url, "https://"+googleFontsHost+"/") {
			if address := runtime.pageCache.notes(resource.url).Fonts[family]; address != "" {
				return address
			}
		}
	}
	return ""
}

// pageStorage 是 SpiderMonkey 页面写入、随账户保存的 localStorage 项
type pageStorage struct {
	path  string
	items map[string]string
}

// loadPageStorage 读取账户目录中页面写入的 localStorage 项，文件不存在时为空
func loadPageStorage(storageStatePath string) (*pageStorage, error) {
	storage := &pageStorage{path: filepath.Join(filepath.Dir(storageStatePath), pageStorageFile), items: map[string]string{}}
	data, err := os.ReadFile(storage.path)
	if errors.Is(err, os.ErrNotExist) {
		return storage, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取页面 localStorage: %w", err)
	}
	if err := json.Unmarshal(data, &storage.items); err != nil {
		return nil, fmt.Errorf("解析页面 localStorage: %w", err)
	}
	return storage, nil
}

// set 写入一项并保存到账户目录
func (storage *pageStorage) set(name, value string) error {
	storage.items[name] = value
	data, err := json.Marshal(storage.items)
	if err != nil {
		return err
	}
	return atomicWriteFile(storage.path, data, 0o600)
}

// cookieBarRecord 是 Cookie 提示条在 localStorage 中记录的一次选择
type cookieBarRecord struct {
	Category string `json:"category"`
	Date     string `json:"date"`
	SiteID   string `json:"siteId"`
	Status   string `json:"status"`
	UUID     string `json:"uuid"`
}

// dismissOverlays 在页面启动完成后与 Camoufox 后端一样关闭启动弹层：Google AI 方案账户尚未看过欢迎对话框时点击 Continue，Cookie 提示条尚未记录选择时点击 No thanks；失败不影响页面
func (runtime *goWAARuntime) dismissOverlays(ctx context.Context) {
	<-runtime.startupDone
	tier, _ := decodeBenefitTier(runtime.pageCatalog.Tier)
	if tier.HeaderValue() != "" && !welcomeSeen(runtime.preferences) {
		body := `[[null,null,null,null,null,null,null,null,null,null,null,1],[["has_seen_g1_welcome_dialog"]]]`
		runtime.sendPageRPCs(ctx, runtime.headers.Get("x-goog-api-key"), runtime.headers.Get("x-aistudio-visit-id"), runtime.headers.Get("x-goog-ext-519733851-bin"), []pageRPC{{"UpdateUserPreferences", body}})
	}
	if err := runtime.rejectCookieBar(ctx); err != nil {
		slog.Warn("关闭 Cookie 提示条失败", "error", err)
	}
}

// requestGDPProfile 在权益 benefitTier 满足导航栏条件时读取 Google Developer Program 权益并处理额度入口
// 各请求在发出时带 extension 返回的日志上下文，GetLoggingContext 尚未返回时为空
func (runtime *goWAARuntime) requestGDPProfile(ctx context.Context, apiKey string, visitID string, benefitTier []byte, extension func() string) {
	if !runtime.pageFlags.requestsGDPProfile(benefitTier) {
		return
	}
	profile := runtime.sendPageRPCs(ctx, apiKey, visitID, extension(), []pageRPC{gdpProfileRPC})[0]
	runtime.loadGDPCredits(ctx, apiKey, visitID, extension(), profile)
}

// gdpProfile 是 GetGdpProfileAndBenefits 响应中决定导航栏额度入口的字段
type gdpProfile struct {
	// member 为第 1 字段：账户已加入 Google Developer Program
	member bool
	// offers 为第 2 字段的条数：可兑换的额度
	offers int
	// claimable 为第 4 字段有值：可领取的每月额度
	claimable bool
}

// parseGDPProfile 解析 GetGdpProfileAndBenefits 的 JSPB 响应
func parseGDPProfile(body []byte) (gdpProfile, error) {
	var fields []json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return gdpProfile{}, err
	}
	field := func(index int) string {
		if index < len(fields) {
			return string(fields[index])
		}
		return "null"
	}
	var offers []json.RawMessage
	if err := json.Unmarshal([]byte(field(1)), &offers); err != nil {
		return gdpProfile{}, err
	}
	return gdpProfile{member: field(0) == "1" || field(0) == "true", offers: len(offers), claimable: field(3) != "null"}, nil
}

// gdpTourDelay 是导航栏额度入口出现后额度引导显示并记录展示的延迟
const gdpTourDelay = 500 * time.Millisecond

// billingTargetingFeature 是官网以 EvaluateTargeting 查询结算账号时的固定功能字段
const billingTargetingFeature = `[2,[2,3,4]]`

// loadGDPCredits 按官网导航栏处理 Google Developer Program 权益响应：已加入且有可兑换额度、或未加入但可领取额度时读取结算账号，再以 EvaluateTargeting 查询各结算账号；额度入口出现时记录入口展示，gdpTourDelay 后记录额度引导展示
func (runtime *goWAARuntime) loadGDPCredits(ctx context.Context, apiKey string, visitID string, extension string, body []byte) {
	profile, err := parseGDPProfile(body)
	if err != nil {
		return
	}
	flags := runtime.pageFlags
	claim := flags.gdpClaim && !profile.member && profile.claimable
	var group sync.WaitGroup
	if claim || profile.member && profile.offers > 0 {
		group.Go(func() {
			accounts := runtime.sendPageRPCs(ctx, apiKey, visitID, extension, []pageRPC{{"ListBillingAccounts", "[]"}})[0]
			if targeting := billingTargetingBody(accounts); flags.billingTargeting && targeting != "" {
				runtime.sendPageRPCs(ctx, apiKey, visitID, extension, []pageRPC{{"EvaluateTargeting", targeting}})
			}
		})
	}
	if claim || flags.gdpClaim && profile.offers > 0 {
		group.Go(func() {
			runtime.sendPageRPCs(ctx, apiKey, visitID, extension, []pageRPC{gdpImpression("AccountSwitcherRedeemCreditsButton")})
		})
		group.Go(func() {
			time.Sleep(gdpTourDelay)
			runtime.sendPageRPCs(ctx, apiKey, visitID, extension, []pageRPC{gdpImpression("RedeemCreditsTourActionButton")})
		})
	}
	group.Wait()
}

// gdpImpression 返回记录额度入口展示的 LogGdpInteractionEvents 请求
func gdpImpression(element string) pageRPC {
	return pageRPC{"LogGdpInteractionEvents", `[["ENTRY_POINT","impression","` + element + `"]]`}
}

// billingTargetingBody 按 ListBillingAccounts 响应中结算账号的顺序返回 EvaluateTargeting 正文，账号名去掉 billingAccounts/ 前缀；没有结算账号时为空
func billingTargetingBody(accounts []byte) string {
	var response []json.RawMessage
	if json.Unmarshal(accounts, &response) != nil || len(response) == 0 {
		return ""
	}
	var list [][]json.RawMessage
	if json.Unmarshal(response[0], &list) != nil {
		return ""
	}
	ids := make([]string, 0, len(list))
	for _, account := range list {
		var name string
		if len(account) > 0 && json.Unmarshal(account[0], &name) == nil {
			ids = append(ids, strings.TrimPrefix(name, "billingAccounts/"))
		}
	}
	if len(ids) == 0 {
		return ""
	}
	encoded, _ := json.Marshal(ids)
	return `[null,` + string(encoded) + `,` + billingTargetingFeature + `]`
}

// mediaDisclaimer 是 UserPreferences 中首次上传素材确认的字段序号与 field mask 名称
var mediaDisclaimer = bidiDisclaimer{index: 1, mask: "uploading_media_disclaimer_ack"}

// pageTokenLifetime 是页面载入时取得的 Drive access token 在上传附件时仍沿用的时长
const pageTokenLifetime = 50 * time.Minute

// insertPause 返回插入文件时相邻两步之间的停顿：点开插入菜单后点上传文件、选好文件、阅读确认框后点确认
func insertPause() time.Duration {
	return 800*time.Millisecond + rand.N(600*time.Millisecond)
}

// pauseContext 等待 duration，ctx 先结束时提前返回
func pauseContext(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

// requestStartup 按 offsets 在 pageStarted 之后分组发出 pageStartupRPCs，同组请求同时发出；GetUserPreferences 返回后关闭 preferencesReady，第二个 GenerateAccessToken 返回后关闭 tokenReady，全部返回后关闭 startupDone
func (runtime *goWAARuntime) requestStartup(ctx context.Context, apiKey string, visitID string, pageStarted time.Time, offsets [5]time.Duration) {
	runtime.startupDone, runtime.preferencesReady, runtime.tokenReady = make(chan struct{}), make(chan struct{}), make(chan struct{})
	var group sync.WaitGroup
	for index, slot := range []int{0, 2, 3} {
		for position := range pageStartupRPCs[index] {
			group.Go(func() {
				time.Sleep(time.Until(pageStarted.Add(offsets[slot])))
				body := runtime.sendPageRPCs(ctx, apiKey, visitID, "", pageStartupRPCs[index][position:position+1])[0]
				switch {
				case index == 0 && position == 0:
					runtime.preferences = body
					close(runtime.preferencesReady)
				case index == 1:
					runtime.accessToken, _ = parseFirstString(bytes.NewReader(body), "GenerateAccessToken", "bearer token")
					runtime.tokenAt = time.Now()
					close(runtime.tokenReady)
				}
			})
		}
	}
	go func() {
		group.Wait()
		close(runtime.startupDone)
	}()
}

// closedBefore 等待 ready 关闭，ctx 先结束时返回 false
func closedBefore(ctx context.Context, ready chan struct{}) bool {
	select {
	case <-ready:
		return true
	case <-ctx.Done():
		return false
	}
}

// driveDenied 判断页面载入时的 GenerateAccessToken 没有取得 Drive access token（账户未授权 Drive）
func (runtime *goWAARuntime) driveDenied(ctx context.Context) bool {
	return closedBefore(ctx, runtime.tokenReady) && runtime.accessToken == ""
}

// acknowledgedMedia 判断 GetUserPreferences 响应中账户已确认首次上传素材提示（第 2 字段不为空）
func acknowledgedMedia(preferences []byte) bool {
	var fields []json.RawMessage
	return json.Unmarshal(preferences, &fields) == nil && len(fields) > mediaDisclaimer.index && !isJSONNull(fields[mediaDisclaimer.index])
}

// beforeUpload 在服务上传附件前记录官网插入文件的界面操作，返回页面载入时取得的 access token
// 这组操作整组排进页面此前的空闲区间，放不下时这次不做也不等待，步数计入下一次取 proof 的跳过步数
// rerun 为真且本页面点过 Run 时不记录这组操作
// 先等 GenerateAccessToken 与（记录这组操作时）GetUserPreferences 返回，ctx 先结束时返回空
// access token 没有或超过 pageTokenLifetime 时为空
func (runtime *goWAARuntime) beforeUpload(ctx context.Context, rerun bool) string {
	if !closedBefore(ctx, runtime.tokenReady) {
		return ""
	}
	if rerun && !runtime.pointer.Clicked().IsZero() {
		return runtime.pageToken()
	}
	if !closedBefore(ctx, runtime.preferencesReady) {
		return ""
	}
	dialog := !runtime.mediaAcknowledged.Load() && !acknowledgedMedia(runtime.preferences)
	menu, picker := insertPause(), insertPause()
	var reading time.Duration
	steps := 2
	if dialog {
		reading, steps = insertPause(), 3
	}
	finished := time.Now()
	opened := finished.Add(-menu - picker - reading)
	if opened.Before(runtime.elements.lastRecorded()) {
		runtime.skippedInserts.Add(int64(steps))
	} else {
		runtime.elements.insertClicked(opened)
		runtime.elements.insertClicked(opened.Add(menu))
		if dialog {
			runtime.mediaAcknowledged.Store(true)
			runtime.elements.disclaimer(opened.Add(menu+picker), finished)
			runtime.acknowledgeMedia(ctx, finished)
		}
	}
	return runtime.pageToken()
}

// pageToken 返回页面载入时取得、仍在 pageTokenLifetime 内的 access token，没有时为空
func (runtime *goWAARuntime) pageToken() string {
	if time.Since(runtime.tokenAt) > pageTokenLifetime {
		return ""
	}
	return runtime.accessToken
}

// acknowledgeMedia 像官网点击首次上传素材提示的确认一样以 Priority u=0 发出确认时刻为 confirmed 的 UpdateUserPreferences，不等响应
func (runtime *goWAARuntime) acknowledgeMedia(ctx context.Context, confirmed time.Time) {
	headers, err := runtime.rpcHeaders(runtime.headers.Get("x-goog-api-key"))
	if err != nil {
		return
	}
	headers.Set("X-AIStudio-Visit-Id", runtime.headers.Get("x-aistudio-visit-id"))
	if extension := runtime.headers.Get("x-goog-ext-519733851-bin"); extension != "" {
		headers.Set("X-Goog-Ext-519733851-Bin", extension)
	}
	headers.Set("Priority", "u=0")
	body, _ := json.Marshal(disclaimerBody(mediaDisclaimer, confirmed))
	go func() {
		runtime.drain(runtime.do(context.WithoutCancel(ctx), http.MethodPost, makerSuiteRPCURL+"UpdateUserPreferences", body, headers))
	}()
}

// uploaded 在服务上传完附件后像官网输入框显示附件一样立即经 drive.count 由页面为每个附件同时发出只含该文件的 CountTokens，不等响应，记下读取附件的 drive
func (runtime *goWAARuntime) uploaded(ctx context.Context, files []FileRef, drive pageDrive) {
	model := runtime.elements.pageModel()
	for _, file := range files {
		body, _ := json.Marshal([]any{model, nil, []any{model, []any{[]any{[]any{[]any{nil, nil, nil, nil, nil, []string{file.ID}}}, "user"}}}})
		runtime.countChip(ctx, body, drive.count)
		runtime.attachedFiles.Store(file.ID, drive)
	}
}

// inlined 在页面没有 Drive 令牌、附件内联时像官网输入框显示附件一样立即经 count 由页面为每个附件同时发出只含该文件内联数据的 CountTokens，不等响应；rerun 为真且本页面点过 Run 时附件已在对话中，不发
func (runtime *goWAARuntime) inlined(ctx context.Context, rerun bool, files []inlineFile, count func(context.Context, []byte) ([]byte, error)) {
	if rerun && !runtime.pointer.Clicked().IsZero() {
		return
	}
	model := runtime.elements.pageModel()
	for _, file := range files {
		data := []any{file.blob.MIME, base64.StdEncoding.EncodeToString(file.blob.Data), nil, nil, nil, file.name}
		body, _ := json.Marshal([]any{model, nil, []any{model, []any{[]any{[]any{[]any{nil, nil, data}}, "user"}}}})
		runtime.countChip(ctx, body, count)
	}
}

// countChip 在后台经 count 发出输入框中一个附件的 CountTokens，官网附件统计 token 期间 Run 照常可点，请求结束不取消它，统计出的 token 数计入带这个附件的回答结束
func (runtime *goWAARuntime) countChip(ctx context.Context, body []byte, count func(context.Context, []byte) ([]byte, error)) {
	counted := runtime.elements.chipCounting()
	go func() {
		tokens := 0
		if response, err := count(context.WithoutCancel(ctx), body); err == nil {
			usage, _ := ParseTokenCount(bytes.NewReader(response))
			tokens = int(usage.InputTokens)
		}
		counted(tokens)
	}()
}

// readAttached 在页面发出 GenerateContent 后像对话中新一轮显示附件一样，0.7–0.9 秒后经上传时记下的 pageDrive 读取最后一个 user 轮次中输入框里的各附件
func (runtime *goWAARuntime) readAttached(ctx context.Context, body []byte) {
	for _, id := range camoufoxnative.GenerateFiles(body) {
		if drive, ok := runtime.attachedFiles.LoadAndDelete(id); ok {
			time.AfterFunc(700*time.Millisecond+rand.N(200*time.Millisecond), func() { drive.(pageDrive).read(context.WithoutCancel(ctx), id) })
		}
	}
}

// welcomeSeen 判断 GetUserPreferences 响应的第 12 字段（已看过 Google AI 方案欢迎对话框）为真
func welcomeSeen(preferences []byte) bool {
	var fields []json.RawMessage
	if json.Unmarshal(preferences, &fields) != nil || len(fields) < 12 {
		return false
	}
	value := string(fields[11])
	return value == "1" || value == "true"
}

// rejectCookieBar 在账户页面尚未记录 Cookie 提示条选择时发出 CreateAuditRecord 并把拒绝记录写入账户目录，下次载入的页面读到该记录，每个账户一次
func (runtime *goWAARuntime) rejectCookieBar(ctx context.Context) error {
	if _, ok := runtime.profile.LocalStorage[cookieBarStorageKey]; ok {
		return nil
	}
	text := runtime.pageCache.notes(cookieBarConfigURL(runtime.cookieBarLanguage)).Consent
	key := runtime.pageCache.notes(cookieBarScript).AuditKey
	if text == "" || key == "" {
		return errors.New("Cookie 提示条文案或 API key 未载入")
	}
	now := time.Now()
	runtime.analytics.rejected(now)
	record := cookieBarRecord{Category: "2A", Date: now.UTC().Format("2006-01-02T15:04:05.000Z"), SiteID: cookieBarSite, Status: "REJECTED", UUID: uuid.NewString()}
	body, err := json.Marshal([]any{auditRecordBody(text, record), 264, []any{nil, nil, nil, nil, []any{runtime.userAgent, runtime.cookieBarLanguage}}, []any{now.UnixMilli(), now.UnixMilli()}})
	if err != nil {
		return err
	}
	headers := corsHeaders(auditRecordURL)
	headers.Del("Priority")
	headers.Set("X-Goog-Api-Key", key)
	headers.Set("Content-Type", "application/json+protobuf")
	headers.Set("X-User-Agent", "grpc-web-javascript/0.1")
	runtime.drain(runtime.do(ctx, http.MethodPost, auditRecordURL, body, headers))
	encoded, err := json.Marshal([]cookieBarRecord{record})
	if err != nil {
		return err
	}
	return runtime.storage.set(cookieBarStorageKey, string(encoded))
}

// auditRecordBody 返回 Cookie 提示条拒绝选择的审计记录：base64 编码的 protobuf，含选择、提示条文案与站点名、记录 UUID
func auditRecordBody(text string, record cookieBarRecord) string {
	field := func(number int, value []byte) []byte {
		out := appendVarint(nil, uint64(number<<3|2))
		out = appendVarint(out, uint64(len(value)))
		return append(out, value...)
	}
	varint := func(number int, value uint64) []byte {
		return appendVarint(appendVarint(nil, uint64(number<<3)), value)
	}
	join := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	identifier := field(19, field(1, []byte(record.UUID)))
	message := join(
		field(2, join(varint(1, 535), field(2, field(87, field(1, varint(1, 2)))))),
		field(3, join(varint(1, 969), field(3, field(60, join(field(1, []byte(text)), field(2, []byte(record.SiteID))))))),
		field(4, identifier),
		field(5, identifier),
	)
	return base64.StdEncoding.EncodeToString(message)
}

// cookieBarText 按 2a.json 的文案推出提示条正文：站点名替换 [This site]，末尾接“了解详情”链接
func cookieBarText(config []byte) (string, error) {
	var parsed struct {
		Content struct {
			MainText string `json:"mainText"`
			MoreText string `json:"moreText"`
			MoreURL  string `json:"moreUrl"`
		} `json:"content"`
	}
	if err := json.Unmarshal(config, &parsed); err != nil || parsed.Content.MainText == "" {
		return "", errors.New("Cookie 提示条文案无效")
	}
	return strings.Replace(parsed.Content.MainText, "[This site]", cookieBarSite, 1) + " <a href='" + parsed.Content.MoreURL + "'>" + parsed.Content.MoreText + "</a>", nil
}
