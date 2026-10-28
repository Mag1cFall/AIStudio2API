package api

import (
	"cmp"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// geminiCacheTTL 是未指定 ttl 与 expireTime 时显式缓存的存活时间
const geminiCacheTTL = time.Hour

// geminiCache 是一个显式缓存资源：缓存的系统指令、内容与工具，以及资源元数据
type geminiCache struct {
	name        string
	model       string
	displayName string
	created     time.Time
	updated     time.Time
	expires     time.Time
	tokens      int64
	// details 是缓存内容按模态细分的 token 数
	details []aistudio.ModalityTokenCount
	// tools 是缓存工具的逐项 token 数
	tools   []aistudio.ToolTokens
	request aistudio.GenerateRequest
}

// geminiCaches 按资源名保存显式缓存，过期缓存在访问与创建时删除
type geminiCaches struct {
	mu    sync.Mutex
	items map[string]*geminiCache
}

func newGeminiCaches() *geminiCaches {
	return &geminiCaches{items: make(map[string]*geminiCache)}
}

// geminiCacheRequest 表示 cachedContents 创建与更新请求
type geminiCacheRequest struct {
	Model             string            `json:"model"`
	DisplayName       string            `json:"displayName"`
	Contents          []geminiContent   `json:"contents"`
	SystemInstruction *geminiContent    `json:"systemInstruction"`
	Tools             []geminiToolGroup `json:"tools"`
	ToolConfig        geminiToolConfig  `json:"toolConfig"`
	TTL               string            `json:"ttl"`
	ExpireTime        string            `json:"expireTime"`
}

// expiration 返回 expireTime 或 now 加 ttl，两者都未设置时 ok 为 false，同时设置时返回错误
func (request geminiCacheRequest) expiration(now time.Time) (time.Time, bool, error) {
	if request.ExpireTime != "" && request.TTL != "" {
		return time.Time{}, false, fmt.Errorf("Invalid value at 'cached_content' (oneof), oneof field 'expiration' is already set. Cannot set both 'ttl' and 'expireTime'")
	}
	if request.ExpireTime != "" {
		value, err := time.Parse(time.RFC3339Nano, request.ExpireTime)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("expireTime: %w", err)
		}
		return value, true, nil
	}
	if request.TTL != "" {
		ttl, err := time.ParseDuration(request.TTL)
		if err != nil || ttl <= 0 {
			return time.Time{}, false, fmt.Errorf("ttl %q must be a positive duration such as \"300s\"", request.TTL)
		}
		return now.Add(ttl), true, nil
	}
	return time.Time{}, false, nil
}

// geminiCacheName 返回缓存资源名，接受 cachedContents/{id} 与 {id}
func geminiCacheName(value string) string {
	return "cachedContents/" + strings.TrimPrefix(strings.TrimSpace(value), "cachedContents/")
}

// get 返回未过期的缓存
func (caches *geminiCaches) get(name string) (*geminiCache, bool) {
	caches.mu.Lock()
	defer caches.mu.Unlock()
	name = geminiCacheName(name)
	cache, ok := caches.items[name]
	if ok && !time.Now().Before(cache.expires) {
		delete(caches.items, name)
		return nil, false
	}
	return cache, ok
}

// add 保存新缓存并删除已过期的缓存
func (caches *geminiCaches) add(cache *geminiCache) {
	caches.mu.Lock()
	defer caches.mu.Unlock()
	caches.removeExpiredLocked(time.Now())
	caches.items[cache.name] = cache
}

// list 返回未过期缓存的元数据，按创建时间从新到旧排列
func (caches *geminiCaches) list() []map[string]any {
	caches.mu.Lock()
	defer caches.mu.Unlock()
	caches.removeExpiredLocked(time.Now())
	items := slices.Collect(maps.Values(caches.items))
	slices.SortFunc(items, func(a, b *geminiCache) int { return b.created.Compare(a.created) })
	objects := make([]map[string]any, len(items))
	for index, cache := range items {
		objects[index] = cache.object()
	}
	return objects
}

// removeExpiredLocked 删除在 now 之前过期的缓存
func (caches *geminiCaches) removeExpiredLocked(now time.Time) {
	maps.DeleteFunc(caches.items, func(_ string, cache *geminiCache) bool { return !now.Before(cache.expires) })
}

// object 返回 CachedContent 资源的元数据
func (cache *geminiCache) object() map[string]any {
	object := map[string]any{
		"name": cache.name, "model": "models/" + cache.model,
		"createTime": cache.created.Format(time.RFC3339Nano), "updateTime": cache.updated.Format(time.RFC3339Nano),
		"expireTime": cache.expires.Format(time.RFC3339Nano), "usageMetadata": map[string]any{"totalTokenCount": cache.tokens},
	}
	if cache.displayName != "" {
		object["displayName"] = cache.displayName
	}
	return object
}

// apply 把缓存的系统指令、内容与工具放在请求之前，请求未设置工具调用方式时沿用缓存的设置
func (cache *geminiCache) apply(request *aistudio.GenerateRequest) {
	cached := cache.request
	request.System = append(slices.Clone(cached.System), request.System...)
	request.Contents = append(cloneResponseContents(cached.Contents), request.Contents...)
	tools := &request.Tools
	tools.Functions = append(slices.Clone(cached.Tools.Functions), tools.Functions...)
	google := slices.Clone(cached.Tools.Google)
	for _, name := range tools.Google {
		google = appendUnique(google, name)
	}
	tools.Google = google
	tools.GoogleSearch = cmp.Or(tools.GoogleSearch, cached.Tools.GoogleSearch)
	tools.MapsWidget = tools.MapsWidget || cached.Tools.MapsWidget
	tools.ComputerUse = cmp.Or(tools.ComputerUse, cached.Tools.ComputerUse)
	tools.MCPServers = append(slices.Clone(cached.Tools.MCPServers), tools.MCPServers...)
	if len(tools.FileSearch) == 0 {
		tools.FileSearch = cached.Tools.FileSearch
	}
	config := tools.ToolConfig
	if (config.Mode == "" || config.Mode == "auto") && len(config.AllowedFunctionNames) == 0 && config.Retrieval == nil {
		invocations := config.ServerSideInvocations
		tools.ToolConfig = cached.Tools.ToolConfig
		tools.ToolConfig.ServerSideInvocations = tools.ToolConfig.ServerSideInvocations || invocations
	}
}

// requestTokens 从展开缓存后的计数中去掉缓存的系统指令、内容与工具，只留请求自身的逐项 token 数
func (cache *geminiCache) requestTokens(count aistudio.TokenCount) aistudio.TokenCount {
	if count.System != nil {
		parts := count.System.PartTokens
		count.System = &aistudio.ContentTokens{PartTokens: parts[min(len(cache.request.System), len(parts)):]}
	}
	cached := 0
	for _, content := range cache.request.Contents {
		if len(content.Parts) > 0 {
			cached++
		}
	}
	count.Contents = count.Contents[min(cached, len(count.Contents)):]
	functions, others := len(cache.request.Tools.Functions), 0
	for _, tool := range cache.tools {
		if len(tool.FunctionDeclarationTokens) == 0 {
			others++
		}
	}
	var tools []aistudio.ToolTokens
	for _, tool := range count.Tools {
		if declarations := tool.FunctionDeclarationTokens; len(declarations) > 0 {
			skip := min(functions, len(declarations))
			functions -= skip
			if skip < len(declarations) {
				tools = append(tools, aistudio.ToolTokens{FunctionDeclarationTokens: declarations[skip:]})
			}
		} else if others > 0 {
			others--
		} else {
			tools = append(tools, tool)
		}
	}
	count.Tools = tools
	return count
}

// applyGeminiCache 展开请求引用的显式缓存，缓存不存在或已过期时写出 404 并返回 false
func (s *server) applyGeminiCache(w http.ResponseWriter, name string, request *aistudio.GenerateRequest) (*geminiCache, bool) {
	if name == "" {
		return nil, true
	}
	cache, ok := s.caches.get(name)
	if !ok {
		writeGeminiCacheNotFound(w, name)
		return nil, false
	}
	cache.apply(request)
	return cache, true
}

// readGeminiCacheRequest 读取 cachedContents 请求体
func readGeminiCacheRequest(r *http.Request) (geminiCacheRequest, error) {
	var request geminiCacheRequest
	body, err := io.ReadAll(r.Body)
	if err == nil {
		body, _, err = geminiCamelKeys(body)
	}
	if err == nil {
		err = json.Unmarshal(body, &request)
	}
	return request, err
}

// handleGeminiCacheCreate 保存缓存内容并以 countTokens 统计缓存的 token 数
func (s *server) handleGeminiCacheCreate(w http.ResponseWriter, r *http.Request) {
	request, err := readGeminiCacheRequest(r)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	model := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	if model == "" {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "cachedContent model is required")
		return
	}
	now := time.Now().UTC()
	expires, ok, err := request.expiration(now)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if !ok {
		expires = now.Add(geminiCacheTTL)
	}
	cached, err := geminiRequest{
		Contents: request.Contents, SystemInstruction: request.SystemInstruction, Tools: request.Tools, ToolConfig: request.ToolConfig,
	}.toGenerateRequest(newID("cache"), model)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if err := resolveGeminiFileNames(r.Context(), s.service, cached.Contents); err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiRequestError(w, err)
		}
		return
	}
	if err := inlineRemoteMedia(r.Context(), cached.Contents); err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		}
		return
	}
	count, err := s.service.CountTokens(r.Context(), aistudio.TokenCountRequest{
		Model: model, System: cached.System, Contents: cached.Contents, Tools: cached.Tools,
	})
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiRequestError(w, err)
		}
		return
	}
	cache := &geminiCache{
		name: "cachedContents/" + strings.ToLower(rand.Text()), model: model, displayName: request.DisplayName,
		created: now, updated: now, expires: expires.UTC(), tokens: count.InputTokens, details: count.PromptTokensDetails, tools: count.Tools, request: cached,
	}
	s.caches.add(cache)
	writeJSON(w, http.StatusOK, officialOrder("gemini", cache.object()))
}

// handleGeminiCacheList 按创建时间从新到旧分页列出未过期的缓存
func (s *server) handleGeminiCacheList(w http.ResponseWriter, r *http.Request) {
	page, next, err := geminiPage(r.URL.Query(), s.caches.list(), 10, 1000)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	body := map[string]any{"cachedContents": page}
	if next != "" {
		body["nextPageToken"] = next
	}
	writeJSON(w, http.StatusOK, officialOrder("gemini", body))
}

// handleGeminiCacheGet 返回缓存资源的元数据
func (s *server) handleGeminiCacheGet(w http.ResponseWriter, r *http.Request) {
	cache, ok := s.caches.get(r.PathValue("cache"))
	if !ok {
		writeGeminiCacheNotFound(w, r.PathValue("cache"))
		return
	}
	s.caches.mu.Lock()
	object := cache.object()
	s.caches.mu.Unlock()
	writeJSON(w, http.StatusOK, officialOrder("gemini", object))
}

// handleGeminiCacheUpdate 按请求中的 ttl 或 expireTime 更新缓存的过期时间
func (s *server) handleGeminiCacheUpdate(w http.ResponseWriter, r *http.Request) {
	request, err := readGeminiCacheRequest(r)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	now := time.Now().UTC()
	expires, ok, err := request.expiration(now)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if !ok {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "cachedContent update requires ttl or expireTime")
		return
	}
	cache, found := s.caches.get(r.PathValue("cache"))
	if !found {
		writeGeminiCacheNotFound(w, r.PathValue("cache"))
		return
	}
	s.caches.mu.Lock()
	cache.expires, cache.updated = expires.UTC(), now
	object := cache.object()
	s.caches.mu.Unlock()
	writeJSON(w, http.StatusOK, officialOrder("gemini", object))
}

// handleGeminiCacheDelete 删除缓存
func (s *server) handleGeminiCacheDelete(w http.ResponseWriter, r *http.Request) {
	name := geminiCacheName(r.PathValue("cache"))
	if _, ok := s.caches.get(name); !ok {
		writeGeminiCacheNotFound(w, name)
		return
	}
	s.caches.mu.Lock()
	delete(s.caches.items, name)
	s.caches.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{})
}

// writeGeminiCacheNotFound 写出缓存不存在的 404
func writeGeminiCacheNotFound(w http.ResponseWriter, name string) {
	writeGeminiError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("cachedContent %q was not found or has expired", geminiCacheName(name)))
}
