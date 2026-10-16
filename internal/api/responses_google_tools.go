package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func responsesUsesWebSearch(tools []responsesTool) bool {
	for _, tool := range tools {
		if strings.HasPrefix(tool.Type, "web_search") {
			return true
		}
	}
	return false
}

// responsesContainerFiles 返回 code_interpreter 自动容器携带的文件 ID，容器 ID 字符串不携带文件
func responsesContainerFiles(raw json.RawMessage) ([]string, error) {
	if !rawJSONConfigured(raw) {
		return nil, nil
	}
	var id string
	if json.Unmarshal(raw, &id) == nil {
		return nil, nil
	}
	var container struct {
		FileIDs []string `json:"file_ids"`
	}
	if err := json.Unmarshal(raw, &container); err != nil {
		return nil, fmt.Errorf("code_interpreter container must be a container ID or object")
	}
	return container.FileIDs, nil
}

func responseWebSearchSources(metadata aistudio.GroundingMetadata) []map[string]any {
	sources := make([]map[string]any, 0, len(metadata.Chunks))
	seen := make(map[string]struct{})
	for _, chunk := range metadata.Chunks {
		if chunk.URI == "" {
			continue
		}
		if _, exists := seen[chunk.URI]; exists {
			continue
		}
		seen[chunk.URI] = struct{}{}
		sources = append(sources, map[string]any{"type": "url", "url": chunk.URI})
	}
	return sources
}
