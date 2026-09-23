package aistudio

import "strings"

const searchModelAliasSuffix = "-search"

// ResolveSearchModelAlias resolves the strict -search model suffix.
func ResolveSearchModelAlias(model string) (baseModel string, enableSearch bool) {
	if !strings.HasSuffix(model, searchModelAliasSuffix) {
		return model, false
	}
	baseModel = strings.TrimSuffix(model, searchModelAliasSuffix)
	if baseModel == "" || baseModel == "models/" {
		return model, false
	}
	return baseModel, true
}

// ResolveSearchGenerateRequest maps a Search alias to its real model and merges
// native Google Search into the request without replacing any existing tools.
func ResolveSearchGenerateRequest(request GenerateRequest) (GenerateRequest, bool) {
	baseModel, enableSearch := ResolveSearchModelAlias(request.Model)
	if !enableSearch {
		return request, false
	}
	request.Model = baseModel
	request.Tools = toolsWithWebSearch(request.Tools)
	return request, true
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

// ModelsWithSearchAliases adds derived catalog entries for chat models that
// support both GenerateContent and native Google Search.
func ModelsWithSearchAliases(models []Model) []Model {
	result := make([]Model, 0, len(models)*2)
	knownIDs := make(map[string]struct{}, len(models)*2)
	for _, model := range models {
		knownIDs[model.ID] = struct{}{}
	}
	for _, model := range cloneModels(models) {
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
		knownIDs[aliasID] = struct{}{}
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
