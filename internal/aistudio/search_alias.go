package aistudio

import "strings"

const searchModelAliasSuffix = "-search"

// ResolveSearchModelAlias 解析模型名末尾或思考后缀之前的搜索后缀，思考后缀保留在返回的型号上
func ResolveSearchModelAlias(model string) (baseModel string, enableSearch bool) {
	name, suffix := model, ""
	if open := strings.LastIndex(model, "("); open >= 0 && strings.HasSuffix(model, ")") {
		name, suffix = strings.TrimSpace(model[:open]), model[open:]
	}
	if !strings.HasSuffix(name, searchModelAliasSuffix) {
		return model, false
	}
	baseModel = strings.TrimSuffix(name, searchModelAliasSuffix)
	if baseModel == "" || baseModel == "models/" {
		return model, false
	}
	return baseModel + suffix, true
}

// ResolveSearchGenerateRequest 将搜索型号解析为基础型号并合入 Google 搜索
func ResolveSearchGenerateRequest(request GenerateRequest, aliases, search bool) GenerateRequest {
	if aliases {
		baseModel, enableSearch := ResolveSearchModelAlias(request.Model)
		if enableSearch {
			request.Model = baseModel
			search = true
		}
	}
	if search {
		request.Tools = toolsWithWebSearch(request.Tools)
	}
	return request
}

func toolsWithWebSearch(tools Tools) Tools {
	if tools.GoogleSearch != nil {
		options := *tools.GoogleSearch
		options.WebSearch = true
		tools.GoogleSearch = &options
		return tools
	}
	for _, name := range tools.Google {
		if name == "google_search" {
			return tools
		}
	}
	tools.GoogleSearch = &GoogleSearchOptions{WebSearch: true}
	return tools
}

// ModelsWithSearchAliases 为支持生成与 Google 搜索的对话模型补充搜索型号
func ModelsWithSearchAliases(models []Model) []Model {
	result := make([]Model, 0, len(models)*2)
	knownIDs := make(map[string]struct{}, len(models))
	for _, model := range models {
		knownIDs[model.ID] = struct{}{}
	}
	for _, model := range models {
		result = append(result, model)
		if !searchAliasEligible(model) {
			continue
		}
		aliasID := model.ID + searchModelAliasSuffix
		if _, exists := knownIDs[aliasID]; exists {
			continue
		}
		alias := cloneModels([]Model{model})[0]
		alias.ID = aliasID
		alias.Name = model.Name + " (Search)"
		alias.Description = searchAliasDescription(model.Description)
		result = append(result, alias)
	}
	return result
}

func searchAliasEligible(model Model) bool {
	return !strings.HasSuffix(model.ID, searchModelAliasSuffix) &&
		model.Capabilities["chat_model"] &&
		model.Capabilities["google_search"] &&
		hasMethod(model, "generateContent")
}

func searchAliasDescription(description string) string {
	const note = "Automatically enables Google Search."
	if strings.TrimSpace(description) == "" {
		return note
	}
	return description + " " + note
}
