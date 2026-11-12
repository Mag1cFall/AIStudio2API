package aistudio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
)

// StaticFetcher 经账户出口读取官网静态资源
type StaticFetcher interface {
	FetchStatic(ctx context.Context, accountID string, address string) ([]byte, error)
}

// frontendModels 是官网前端主程序与对话页分块里推荐模型页的模型族与这些页面指向的模型 ID
type frontendModels struct {
	// families 是推荐模型页的模型族名与页面类型，模型族名为页面按 models/ 名称筛选目录时的部分
	families map[string]string
	// models 是推荐模型页路由与模型文档链接中属于某个模型族的模型 ID，按出现顺序排列
	models []string
}

var (
	// frontendFamilyPattern 匹配推荐模型页配置开头的模型族名与页面类型
	frontendFamilyPattern = regexp.MustCompile(`\{id:"([a-z0-9.-]+)",type:"([a-z]+)"`)
	// frontendFamilyNamePattern 匹配推荐模型页按模型名筛选目录的 models/ 名称
	frontendFamilyNamePattern = regexp.MustCompile(`EA:\["models/([a-z0-9.-]+)"`)
	// frontendChatModulePattern 匹配主程序路由表中对话页按需载入的模块 ID
	frontendChatModulePattern = regexp.MustCompile(`path:"prompts/new_chat",[^{}]*?\("([A-Za-z0-9_]+)"\)`)
	// frontendModelPatterns 匹配推荐模型页路由的 model 查询参数与模型文档链接
	frontendModelPatterns = []*regexp.Regexp{
		regexp.MustCompile(`queryParams:\{model:"([a-z0-9.-]+)"`),
		regexp.MustCompile(`aistudio\.google\.com/docs/models/([a-z0-9.-]+)`),
	}
)

// parseFrontendModels 从官网前端主程序读取推荐模型页的模型族与指向的模型 ID
func parseFrontendModels(source []byte) frontendModels {
	named := make(map[string]bool)
	for _, match := range frontendFamilyNamePattern.FindAllSubmatch(source, -1) {
		named[string(match[1])] = true
	}
	parsed := frontendModels{families: make(map[string]string)}
	for _, match := range frontendFamilyPattern.FindAllSubmatch(source, -1) {
		if named[string(match[1])] {
			parsed.families[string(match[1])] = string(match[2])
		}
	}
	for _, pattern := range frontendModelPatterns {
		for _, match := range pattern.FindAllSubmatch(source, -1) {
			if modelID := string(match[1]); parsed.family(modelID) != "" && !slices.Contains(parsed.models, modelID) {
				parsed.models = append(parsed.models, modelID)
			}
		}
	}
	return parsed
}

// family 返回模型 ID 所属的模型族，与官网推荐模型页按 models/.*<模型族> 筛选目录一致，不属于任何模型族时返回空字符串
func (frontend frontendModels) family(modelID string) string {
	family := ""
	for name := range frontend.families {
		if strings.Contains(modelID, name) && len(name) > len(family) {
			family = name
		}
	}
	return family
}

// template 返回 catalog 中属于其他模型族、页面类型与 modelID 相同的推荐模型条目，作为 modelID 目录条目的依据
func (frontend frontendModels) template(catalog modelCatalog, modelID string) (modelEntry, bool) {
	family := frontend.family(modelID)
	for _, candidate := range frontend.models {
		other := frontend.family(candidate)
		if other == family || frontend.families[other] != frontend.families[family] {
			continue
		}
		if entry, ok := catalog.lookup(candidate); ok {
			return entry, true
		}
	}
	return modelEntry{}, false
}

// DiscoverHiddenModels 从账户页面载入的官网前端主程序与对话页分块找出 ListModels 没有列出的推荐模型，经 CountTokens 确认可用后加入账户目录，返回隐藏模型 ID 到依据模型 ID 的映射
func (c *Client) DiscoverHiddenModels(ctx context.Context, accountID string) (map[string]string, error) {
	c.catalogMu.RLock()
	catalog := c.catalogs[accountID]
	c.catalogMu.RUnlock()
	if catalog.bundle == "" {
		return nil, errors.New("账户页面载入时没有取得官网前端主程序")
	}
	frontend, err := c.frontendModels(ctx, accountID, catalog.bundle)
	if err != nil {
		return nil, err
	}
	hidden := make(map[string]string)
	for _, modelID := range frontend.models {
		if _, listed := catalog.lookup(modelID); listed {
			continue
		}
		template, ok := frontend.template(catalog, modelID)
		if !ok {
			slog.Warn("官网隐藏模型没有同类的已列出模型可作目录依据", "account", accountID, "model", modelID)
			continue
		}
		_, err := c.CountTokensForAccount(ctx, accountID, TokenCountRequest{Model: modelID, Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "ok"}}}}})
		var rpcErr *RPCError
		if errors.As(err, &rpcErr) && rpcErr.StatusCode == http.StatusNotFound {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("探测官网隐藏模型 %s: %w", modelID, err)
		}
		hidden[modelID] = template.model.ID
	}
	c.catalogMu.Lock()
	c.catalogs[accountID] = c.catalogs[accountID].withHidden(hidden)
	c.catalogMu.Unlock()
	return hidden, nil
}

// frontendChatChunk 返回官网前端主程序地址对应的对话页分块地址，与页面按需载入时一样排除主程序模块
func frontendChatChunk(bundle string, module string) string {
	return strings.Replace(strings.TrimSuffix(bundle, "/m=_b"), "/excm=_b/", "/exm=_b/excm=_b/", 1) + "/m=" + module
}

// frontendModels 返回官网前端主程序与对话页分块中的推荐模型，同一主程序地址只经账户出口下载与解析一次
func (c *Client) frontendModels(ctx context.Context, accountID string, bundle string) (frontendModels, error) {
	c.frontendMu.Lock()
	parsed, ok := c.frontend[bundle]
	c.frontendMu.Unlock()
	if ok {
		return parsed, nil
	}
	source, err := c.static.FetchStatic(ctx, accountID, bundle)
	if err != nil {
		return frontendModels{}, fmt.Errorf("下载官网前端主程序: %w", err)
	}
	module := frontendChatModulePattern.FindSubmatch(source)
	if module == nil {
		return frontendModels{}, errors.New("官网前端主程序没有对话页模块")
	}
	chunk, err := c.static.FetchStatic(ctx, accountID, frontendChatChunk(bundle, string(module[1])))
	if err != nil {
		return frontendModels{}, fmt.Errorf("下载官网对话页分块: %w", err)
	}
	parsed = parseFrontendModels(append(source, chunk...))
	c.frontendMu.Lock()
	c.frontend[bundle] = parsed
	c.frontendMu.Unlock()
	return parsed, nil
}

// withHidden 返回加入隐藏模型条目的目录副本：条目复制自依据模型的参数与能力，不带依据模型的官网资料、权益限制与付费标记，ID 与名称为隐藏模型 ID 并标为隐藏；目录已列出或依据模型不在目录中的隐藏模型不加入
func (catalog modelCatalog) withHidden(hidden map[string]string) modelCatalog {
	if len(hidden) == 0 {
		return catalog
	}
	models := cloneModels(catalog.models)
	entries := maps.Clone(catalog.entries)
	for _, modelID := range slices.Sorted(maps.Keys(hidden)) {
		template, ok := catalog.entries[hidden[modelID]]
		if _, listed := catalog.lookup(modelID); listed || !ok {
			continue
		}
		model := cloneModels([]Model{template.model})[0]
		model.ID, model.Name, model.Description, model.Hidden = modelID, modelID, "", true
		model.Details, model.AccessModes, model.Paid = nil, nil, false
		delete(model.CapabilityOptions, "aliases")
		models = append(models, model)
		entries[modelID] = modelEntry{model: model, defaults: template.defaults}
	}
	catalog.models, catalog.entries = models, entries
	return catalog
}
