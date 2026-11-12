package aistudio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
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
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// pageResourceKind 是官网文档 HTML 引用、浏览器解析文档时请求的子资源类别
type pageResourceKind int

const (
	// pageStylesheet 是 <link rel="stylesheet">
	pageStylesheet pageResourceKind = iota
	// pagePreloadScript 是 <link rel="preload" as="script">
	pagePreloadScript
	// pageScript 是没有预载的同步 <script src>
	pageScript
)

// pageResource 是官网文档 HTML 引用的一个子资源
type pageResource struct {
	url  string
	kind pageResourceKind
}

// pageIcon 是官网文档 HTML 中的一个图标链接，width 为 sizes 中第一个有效尺寸的宽度，没有时为 -1
type pageIcon struct {
	url   string
	width int
	// rich 表示 apple-touch-icon、apple-touch-icon-precomposed 或 fluid-icon
	rich bool
}

// pageDocument 是从官网文档 HTML 中解析出的子资源与图标，顺序与文档相同
type pageDocument struct {
	resources []pageResource
	icons     []pageIcon
}

// iconSizePattern 匹配 sizes 属性中的一个有效尺寸
var iconSizePattern = regexp.MustCompile(`(?i)^([1-9][0-9]*)x[1-9][0-9]*$`)

// parsePageDocument 按 Firefox 解析文档的规则取出官网文档 HTML 引用的样式表、预载脚本、同步脚本与图标，地址按文档 base 解析
func parsePageDocument(body []byte, pageURL string) pageDocument {
	base, _ := url.Parse(pageURL)
	resolve := func(raw string) string {
		target, err := base.Parse(strings.TrimSpace(raw))
		if err != nil {
			return ""
		}
		return target.String()
	}
	var document pageDocument
	preloaded := map[string]bool{}
	tokenizer := html.NewTokenizer(bytes.NewReader(body))
	noscript := false
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			return document
		}
		token := tokenizer.Token()
		if token.DataAtom == atom.Noscript {
			noscript = kind == html.StartTagToken
			continue
		}
		if noscript || kind != html.StartTagToken && kind != html.SelfClosingTagToken {
			continue
		}
		attributes := map[string]string{}
		for _, attribute := range token.Attr {
			attributes[attribute.Key] = attribute.Val
		}
		switch token.DataAtom {
		case atom.Base:
			if href, ok := attributes["href"]; ok {
				if parsed, err := base.Parse(href); err == nil {
					base = parsed
				}
			}
		case atom.Link:
			href := resolve(attributes["href"])
			if href == "" {
				continue
			}
			rels := strings.Fields(strings.ToLower(attributes["rel"]))
			switch {
			case slices.Contains(rels, "stylesheet"):
				document.resources = append(document.resources, pageResource{url: href, kind: pageStylesheet})
			case slices.Contains(rels, "preload") && strings.EqualFold(attributes["as"], "script"):
				document.resources = append(document.resources, pageResource{url: href, kind: pagePreloadScript})
				preloaded[href] = true
			default:
				rich := slices.Contains(rels, "apple-touch-icon") || slices.Contains(rels, "apple-touch-icon-precomposed") || slices.Contains(rels, "fluid-icon")
				if rich || slices.Contains(rels, "icon") {
					document.icons = append(document.icons, pageIcon{url: href, width: iconWidth(attributes["sizes"]), rich: rich})
				}
			}
		case atom.Script:
			_, async := attributes["async"]
			_, deferred := attributes["defer"]
			source := resolve(attributes["src"])
			if attributes["src"] == "" || async || deferred || attributes["type"] == "module" || preloaded[source] {
				continue
			}
			document.resources = append(document.resources, pageResource{url: source, kind: pageScript})
		}
	}
}

// iconWidth 按 Firefox 读取 sizes 中第一个有效尺寸的宽度，any 或没有有效尺寸时为 -1
func iconWidth(sizes string) int {
	for _, size := range strings.Fields(sizes) {
		if strings.EqualFold(size, "any") {
			return -1
		}
		if match := iconSizePattern.FindStringSubmatch(size); match != nil {
			width, _ := strconv.Atoi(match[1])
			return width
		}
	}
	return -1
}

// firefoxRichIconMinWidth 是 Firefox 把普通图标也算作富图标的最小宽度
const firefoxRichIconMinWidth = 96

// selectPageIcons 按 Firefox FaviconLoader 的 selectIcons 选出富图标与标签页图标，preferredWidth 为 16 乘以向上取整的 devicePixelRatio
func selectPageIcons(icons []pageIcon, preferredWidth int) (rich, tab string) {
	var largestRich, bestSized, fallback *pageIcon
	for index := range icons {
		icon := &icons[index]
		if icon.rich || icon.width >= firefoxRichIconMinWidth {
			if largestRich == nil || largestRich.width < icon.width {
				largestRich = icon
			}
		} else {
			fallback = icon
		}
		if icon.width >= preferredWidth && (bestSized == nil || bestSized.width >= icon.width) {
			bestSized = icon
		}
	}
	if largestRich != nil {
		rich = largestRich.url
	}
	switch {
	case bestSized != nil:
		tab = bestSized.url
	case fallback != nil:
		tab = fallback.url
	}
	return rich, tab
}

// fetchSite 返回从官网页面发起的请求的 Sec-Fetch-Site
func fetchSite(target string) string {
	parsed, err := url.Parse(target)
	if err != nil {
		return "cross-site"
	}
	host := parsed.Hostname()
	switch {
	case host == "aistudio.google.com":
		return "same-origin"
	case host == "google.com" || strings.HasSuffix(host, ".google.com"):
		return "same-site"
	}
	return "cross-site"
}

// subresourceHeaders 返回 Firefox 从官网页面请求无 CORS 子资源的请求头
func subresourceHeaders(target, accept, destination, priority string) http.Header {
	headers := http.Header{}
	headers.Set("Accept", accept)
	headers.Set("Referer", aiStudioOrigin+"/")
	site := fetchSite(target)
	if site == "cross-site" {
		headers.Set("Sec-Fetch-Storage-Access", "none")
	}
	headers.Set("Sec-Fetch-Dest", destination)
	headers.Set("Sec-Fetch-Mode", "no-cors")
	headers.Set("Sec-Fetch-Site", site)
	if priority != "" {
		headers.Set("Priority", priority)
	}
	return headers
}

// frontendBundle 返回文档引用的官网前端主程序地址，没有时返回空字符串
func (document pageDocument) frontendBundle() string {
	for _, resource := range document.resources {
		if address, err := url.Parse(resource.url); err == nil && camoufoxnative.FrontendBundle(address) {
			return resource.url
		}
	}
	return ""
}

// loadDocumentResources 像 Firefox 解析官网文档一样按文档顺序并发请求样式表与脚本，全部完成后返回
func (runtime *goWAARuntime) loadDocumentResources(ctx context.Context, document pageDocument) error {
	errs := make([]error, len(document.resources))
	var group sync.WaitGroup
	for index, resource := range document.resources {
		accept, destination, priority := "*/*", "script", "u=2"
		switch resource.kind {
		case pageStylesheet:
			accept, destination = "text/css,*/*;q=0.1", "style"
		case pagePreloadScript:
			priority = "u=1"
		}
		headers := subresourceHeaders(resource.url, accept, destination, priority)
		group.Go(func() {
			if err := runtime.fetchCached(ctx, resource.url, headers); err != nil {
				errs[index] = fmt.Errorf("载入官网页面资源 %s: %w", resource.url, err)
			}
		})
	}
	group.Wait()
	return errors.Join(errs...)
}

// loadPageIcons 像 Firefox 在文档 head 解析完成后一样先后请求富图标与标签页图标，失败不影响页面
func (runtime *goWAARuntime) loadPageIcons(ctx context.Context, document pageDocument) {
	ratio, ok := numberValue(runtime.profile.Window["devicePixelRatio"])
	if !ok {
		ratio = 1
	}
	rich, tab := selectPageIcons(document.icons, 16*int(math.Ceil(ratio)))
	for _, icon := range []string{rich, tab} {
		if icon == "" {
			continue
		}
		go func() {
			_ = runtime.fetchCached(context.WithoutCancel(ctx), icon, subresourceHeaders(icon, "image/avif,image/webp,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5", "image", "u=6"))
		}()
	}
}

// fetchCached 按账户页面缓存发出子资源请求：新鲜的缓存不发请求，过期且有校验值时发条件请求，读完响应后更新缓存
func (runtime *goWAARuntime) fetchCached(ctx context.Context, target string, headers http.Header) error {
	requested := time.Now()
	entry, fresh := runtime.pageCache.lookup(target, requested)
	if fresh {
		return nil
	}
	if entry.LastModified != "" {
		headers.Set("If-Modified-Since", entry.LastModified)
	}
	if entry.ETag != "" {
		headers.Set("If-None-Match", entry.ETag)
	}
	response, err := runtime.do(ctx, http.MethodGet, target, nil, headers)
	if err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNotModified {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return runtime.pageCache.store(target, response, time.Now())
}

// firefoxRedirectionLimit 是 Firefox network.http.redirection-limit 的默认值
const firefoxRedirectionLimit = 20

// navigateFrame 以 Firefox iframe 导航的请求头载入地址并跟随跳转，返回是否收到文档响应
func (runtime *goWAARuntime) navigateFrame(ctx context.Context, target string) bool {
	for redirect := 0; redirect <= firefoxRedirectionLimit; redirect++ {
		headers := http.Header{}
		headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		headers.Set("Referer", aiStudioOrigin+"/")
		headers.Set("Upgrade-Insecure-Requests", "1")
		headers.Set("Sec-Fetch-Dest", "iframe")
		headers.Set("Sec-Fetch-Mode", "navigate")
		headers.Set("Sec-Fetch-Site", fetchSite(target))
		headers.Set("Priority", "u=4")
		response, err := runtime.do(ctx, http.MethodGet, target, nil, headers)
		if err != nil {
			return false
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode < 300 || response.StatusCode >= 400 {
			return true
		}
		location, err := response.Location()
		if err != nil {
			return false
		}
		target = location.String()
	}
	return false
}

// loadFrame 以 Firefox iframe 导航形状载入 WAA 程序插入的 iframe 地址
func (runtime *goWAARuntime) loadFrame(address string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return runtime.navigateFrame(ctx, address)
}

// authWarmup 像官网页面一样以隐藏 iframe 载入 Cloud Console 的 auth_warmup，并跟随跳转载入 authcheck
func (runtime *goWAARuntime) authWarmup(ctx context.Context) {
	target := "https://console.cloud.google.com/onboarding/auth_warmup?origin=" + url.QueryEscape(aiStudioOrigin)
	if runtime.options.AuthUser > 0 {
		target += "&authuser=" + strconv.Itoa(runtime.options.AuthUser)
	}
	runtime.navigateFrame(ctx, target)
}

// pageCacheFile 是账户目录下保存 SpiderMonkey 页面子资源 HTTP 缓存元数据的文件
const pageCacheFile = "spidermonkey-cache.json"

// pageCache 按 Firefox HTTP 缓存的新鲜度规则记录账户页面子资源最近一次响应的时刻与校验值
type pageCache struct {
	mu      sync.Mutex
	path    string
	entries map[string]pageCacheEntry
}

// pageCacheEntry 是一个子资源最近一次响应的缓存元数据
type pageCacheEntry struct {
	// Received 是收到响应的时刻
	Received     time.Time `json:"received"`
	Date         string    `json:"date,omitempty"`
	Age          string    `json:"age,omitempty"`
	CacheControl string    `json:"cacheControl,omitempty"`
	LastModified string    `json:"lastModified,omitempty"`
	ETag         string    `json:"etag,omitempty"`
}

// loadPageCache 读取账户目录中的页面缓存元数据，文件不存在时为空缓存
func loadPageCache(storageStatePath string) (*pageCache, error) {
	cache := &pageCache{path: filepath.Join(filepath.Dir(storageStatePath), pageCacheFile), entries: map[string]pageCacheEntry{}}
	data, err := os.ReadFile(cache.path)
	if errors.Is(err, os.ErrNotExist) {
		return cache, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取页面缓存: %w", err)
	}
	if err := json.Unmarshal(data, &cache.entries); err != nil {
		return nil, fmt.Errorf("解析页面缓存: %w", err)
	}
	return cache, nil
}

// lookup 返回地址的缓存条目，以及它在 now 时刻按 max-age 是否仍新鲜
func (cache *pageCache) lookup(target string, now time.Time) (pageCacheEntry, bool) {
	cache.mu.Lock()
	entry, ok := cache.entries[target]
	cache.mu.Unlock()
	if !ok {
		return entry, false
	}
	maxAge, cacheable := cacheMaxAge(entry.CacheControl)
	if !cacheable {
		return entry, false
	}
	age := time.Duration(0)
	if date, err := http.ParseTime(entry.Date); err == nil && entry.Received.After(date) {
		age = entry.Received.Sub(date)
	}
	if seconds, err := strconv.Atoi(entry.Age); err == nil {
		age = max(age, time.Duration(seconds)*time.Second)
	}
	return entry, age+now.Sub(entry.Received) < maxAge
}

// store 按响应更新地址的缓存条目并写回文件：304 刷新新鲜度，200 替换条目，no-store 删除条目
func (cache *pageCache) store(target string, response *http.Response, received time.Time) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry := cache.entries[target]
	header := response.Header
	if response.StatusCode == http.StatusOK {
		entry = pageCacheEntry{LastModified: header.Get("Last-Modified"), ETag: header.Get("ETag")}
	}
	entry.Received, entry.Date, entry.Age = received, header.Get("Date"), header.Get("Age")
	if value := header.Get("Cache-Control"); value != "" || response.StatusCode == http.StatusOK {
		entry.CacheControl = value
	}
	if strings.Contains(strings.ToLower(entry.CacheControl), "no-store") {
		delete(cache.entries, target)
	} else {
		cache.entries[target] = entry
	}
	data, err := json.Marshal(cache.entries)
	if err != nil {
		return err
	}
	return atomicWriteFile(cache.path, data, 0o600)
}

// cacheMaxAge 返回 Cache-Control 的 max-age，no-cache、no-store 或没有 max-age 时不可直接使用缓存
func cacheMaxAge(value string) (time.Duration, bool) {
	maxAge, found := time.Duration(0), false
	for _, directive := range strings.Split(strings.ToLower(value), ",") {
		name, argument, _ := strings.Cut(strings.TrimSpace(directive), "=")
		switch name {
		case "no-cache", "no-store":
			return 0, false
		case "max-age":
			seconds, err := strconv.Atoi(strings.Trim(argument, `"`))
			if err != nil {
				return 0, false
			}
			maxAge, found = time.Duration(seconds)*time.Second, true
		}
	}
	return maxAge, found
}
