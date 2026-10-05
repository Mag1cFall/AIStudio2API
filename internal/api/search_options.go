package api

import (
	"encoding/json"
	"fmt"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// mapSearchOptions 将公开搜索偏好保留到共同工具配置
func mapSearchOptions(size string, location json.RawMessage, filters json.RawMessage) (*aistudio.GoogleSearchOptions, error) {
	switch size {
	case "", "low", "medium", "high":
	default:
		return nil, fmt.Errorf("search_context_size must be low, medium or high")
	}
	options := &aistudio.GoogleSearchOptions{WebSearch: true, ContextSize: size}
	if rawJSONConfigured(location) {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(location, &object); err != nil || object == nil {
			return nil, fmt.Errorf("user_location must be an object")
		}
		options.UserLocation = location
	}
	if rawJSONConfigured(filters) {
		var object struct {
			AllowedDomains []string `json:"allowed_domains"`
		}
		if err := json.Unmarshal(filters, &object); err != nil {
			return nil, fmt.Errorf("web_search filters must be an object")
		}
		options.AllowedDomains = object.AllowedDomains
	}
	return options, nil
}
