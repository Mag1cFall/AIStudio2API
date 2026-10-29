package api

import (
	"cmp"
	"crypto/rand"
	"fmt"
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

// get 返回未过期缓存的副本，内容与工具在创建后保持不变
func (caches *geminiCaches) get(name string) (*geminiCache, bool) {
	caches.mu.Lock()
	defer caches.mu.Unlock()
	cache, ok := caches.getLocked(name)
	if !ok {
		return nil, false
	}
	snapshot := *cache
	return &snapshot, true
}

// getLocked 在持锁时查找未过期的缓存
func (caches *geminiCaches) getLocked(name string) (*geminiCache, bool) {
	name = geminiCacheName(name)
	cache, ok := caches.items[name]
	if ok && !time.Now().Before(cache.expires) {
		delete(caches.items, name)
		return nil, false
	}
	return cache, ok
}

// add 保存新缓存并删除已过期的缓存
func (caches *geminiCaches) add(cache *geminiCache) geminiCacheMetadata {
	caches.mu.Lock()
	defer caches.mu.Unlock()
	caches.removeExpiredLocked(time.Now())
	caches.items[cache.name] = cache
	return cache.object()
}

// list 返回未过期缓存的元数据，按创建时间从新到旧排列
func (caches *geminiCaches) list() []geminiCacheMetadata {
	caches.mu.Lock()
	defer caches.mu.Unlock()
	caches.removeExpiredLocked(time.Now())
	items := slices.Collect(maps.Values(caches.items))
	slices.SortFunc(items, func(a, b *geminiCache) int { return b.created.Compare(a.created) })
	objects := make([]geminiCacheMetadata, len(items))
	for index, cache := range items {
		objects[index] = cache.object()
	}
	return objects
}

// metadata 返回未过期缓存的元数据快照
func (caches *geminiCaches) metadata(name string) (geminiCacheMetadata, bool) {
	caches.mu.Lock()
	defer caches.mu.Unlock()
	cache, ok := caches.getLocked(name)
	if !ok {
		return geminiCacheMetadata{}, false
	}
	return cache.object(), true
}

// update 更新未过期缓存的过期时间并返回元数据快照
func (caches *geminiCaches) update(name string, expires, now time.Time) (geminiCacheMetadata, bool) {
	caches.mu.Lock()
	defer caches.mu.Unlock()
	cache, ok := caches.getLocked(name)
	if !ok {
		return geminiCacheMetadata{}, false
	}
	cache.expires, cache.updated = expires.UTC(), now
	return cache.object(), true
}

// delete 删除未过期缓存并返回是否存在
func (caches *geminiCaches) delete(name string) bool {
	caches.mu.Lock()
	defer caches.mu.Unlock()
	cache, ok := caches.getLocked(name)
	if ok {
		delete(caches.items, cache.name)
	}
	return ok
}

// removeExpiredLocked 删除在 now 之前过期的缓存
func (caches *geminiCaches) removeExpiredLocked(now time.Time) {
	maps.DeleteFunc(caches.items, func(_ string, cache *geminiCache) bool { return !now.Before(cache.expires) })
}

// geminiCacheMetadata 是 CachedContent 资源的响应快照
type geminiCacheMetadata struct {
	Name          string           `json:"name"`
	Model         string           `json:"model"`
	CreateTime    string           `json:"createTime"`
	UpdateTime    string           `json:"updateTime"`
	ExpireTime    string           `json:"expireTime"`
	DisplayName   string           `json:"displayName,omitempty"`
	UsageMetadata geminiCacheUsage `json:"usageMetadata"`
}

// geminiCacheUsage 是缓存的 token 总数
type geminiCacheUsage struct {
	TotalTokenCount int64 `json:"totalTokenCount"`
}

// geminiCacheList 是 CachedContent 分页响应
type geminiCacheList struct {
	CachedContents []geminiCacheMetadata `json:"cachedContents"`
	NextPageToken  string                `json:"nextPageToken,omitempty"`
}

// object 返回 CachedContent 资源的元数据
func (cache *geminiCache) object() geminiCacheMetadata {
	return geminiCacheMetadata{
		Name: cache.name, Model: "models/" + cache.model,
		CreateTime: cache.created.Format(time.RFC3339Nano), UpdateTime: cache.updated.Format(time.RFC3339Nano),
		ExpireTime: cache.expires.Format(time.RFC3339Nano), UsageMetadata: geminiCacheUsage{TotalTokenCount: cache.tokens},
		DisplayName: cache.displayName,
	}
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
		writeGeminiError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("cachedContent %q was not found or has expired", geminiCacheName(name)))
		return nil, false
	}
	cache.apply(request)
	return cache, true
}

// handleGeminiCacheCreate 保存缓存内容并以 countTokens 统计缓存的 token 数
func (s *server) handleGeminiCacheCreate(w http.ResponseWriter, r *http.Request) {
	var request geminiCacheRequest
	if err := decodeGeminiJSON(r, &request); err != nil {
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
	counted := s.prepareSearch(cached)
	count, err := s.service.CountTokens(r.Context(), aistudio.TokenCountRequest{
		Model: counted.Model, System: counted.System, Contents: counted.Contents, Tools: counted.Tools,
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
	writeJSON(w, http.StatusOK, s.caches.add(cache))
}

// handleGeminiCacheList 按创建时间从新到旧分页列出未过期的缓存
func (s *server) handleGeminiCacheList(w http.ResponseWriter, r *http.Request) {
	page, next, err := geminiPage(r.URL.Query(), s.caches.list(), 10, 1000)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, geminiCacheList{CachedContents: page, NextPageToken: next})
}

// handleGeminiCacheGet 返回缓存资源的元数据
func (s *server) handleGeminiCacheGet(w http.ResponseWriter, r *http.Request) {
	object, ok := s.caches.metadata(r.PathValue("cache"))
	if !ok {
		writeGeminiCacheNotFound(w)
		return
	}
	writeJSON(w, http.StatusOK, object)
}

// handleGeminiCacheUpdate 按请求中的 ttl 或 expireTime 更新缓存的过期时间
func (s *server) handleGeminiCacheUpdate(w http.ResponseWriter, r *http.Request) {
	var request geminiCacheRequest
	if err := decodeGeminiJSON(r, &request); err != nil {
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
	object, found := s.caches.update(r.PathValue("cache"), expires, now)
	if !found {
		writeGeminiCacheNotFound(w)
		return
	}
	writeJSON(w, http.StatusOK, object)
}

// handleGeminiCacheDelete 删除缓存
func (s *server) handleGeminiCacheDelete(w http.ResponseWriter, r *http.Request) {
	if !s.caches.delete(r.PathValue("cache")) {
		writeGeminiCacheNotFound(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// writeGeminiCacheNotFound 写出缓存资源不存在或已过期时官方的 403
func writeGeminiCacheNotFound(w http.ResponseWriter) {
	writeGeminiError(w, http.StatusForbidden, "PERMISSION_DENIED", "CachedContent not found (or permission denied)")
}
