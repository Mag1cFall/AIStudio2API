package api

import "github.com/Mag1cFall/AIStudio2API/internal/aistudio"

// searchSettings 返回生成服务当前生效的搜索设置
func (s *server) searchSettings() (modelAliases, googleSearch bool) {
	if s.config.SearchSettings == nil {
		return true, false
	}
	return s.config.SearchSettings()
}

// searchModels 按当前设置生成搜索模型目录
func (s *server) searchModels(models []aistudio.Model) []aistudio.Model {
	aliases, _ := s.searchSettings()
	if aliases {
		return aistudio.ModelsWithSearchAliases(models)
	}
	return models
}

// prepareSearch 按当前设置解析搜索型号并启用搜索工具
func (s *server) prepareSearch(request aistudio.GenerateRequest) aistudio.GenerateRequest {
	aliases, search := s.searchSettings()
	return aistudio.ResolveSearchGenerateRequest(request, aliases, search)
}
