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

func validateResponsesCodeContainer(raw json.RawMessage) error {
	if !rawJSONConfigured(raw) {
		return nil
	}
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		if name == "auto" {
			return nil
		}
		return fmt.Errorf("AI Studio Web 的 code_interpreter container 只支持 auto")
	}
	var container struct {
		Type    string   `json:"type"`
		FileIDs []string `json:"file_ids"`
	}
	if err := json.Unmarshal(raw, &container); err != nil || container.Type != "auto" {
		return fmt.Errorf("AI Studio Web 的 code_interpreter container 只支持 auto")
	}
	if len(container.FileIDs) > 0 {
		return fmt.Errorf("AI Studio Web 的 code_interpreter 不支持 container file_ids")
	}
	return nil
}

func responseWebSearchItems(responseID string, events []aistudio.Event) []any {
	items := make([]any, 0)
	seen := make(map[string]struct{})
	index := 0
	for _, event := range events {
		if event.Kind != aistudio.EventGrounding || event.Grounding == nil {
			continue
		}
		for _, query := range event.Grounding.WebSearchQueries {
			if _, exists := seen[query]; exists {
				continue
			}
			seen[query] = struct{}{}
			items = append(items, responseWebSearchItem(responseID, index, query, *event.Grounding, "completed"))
			index++
		}
	}
	return items
}

func responseWebSearchItem(responseID string, index int, query string, metadata aistudio.GroundingMetadata, status string) map[string]any {
	return map[string]any{
		"id":     fmt.Sprintf("ws_%s_%d", responseID, index),
		"type":   "web_search_call",
		"status": status,
		"action": map[string]any{
			"type": "search", "query": query, "sources": responseWebSearchSources(metadata),
		},
	}
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

func (writer *responsesStreamWriter) emitGrounding(metadata aistudio.GroundingMetadata) (bool, error) {
	if !responsesUsesWebSearch(writer.request.Tools) {
		return false, nil
	}
	if writer.searchQueries == nil {
		writer.searchQueries = make(map[string]struct{})
	}
	emitted := false
	for _, query := range metadata.WebSearchQueries {
		if _, exists := writer.searchQueries[query]; exists {
			continue
		}
		writer.searchQueries[query] = struct{}{}
		completed := responseWebSearchItem(writer.id, writer.searchCount, query, metadata, "completed")
		writer.searchCount++
		id := completed["id"].(string)
		index := len(writer.indexes)
		writer.indexes[id] = index
		inProgress := responseWebSearchItem(writer.id, writer.searchCount-1, query, metadata, "in_progress")
		if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": inProgress}); err != nil {
			return false, err
		}
		if err := writer.emit("response.web_search_call.in_progress", map[string]any{"item_id": id, "output_index": index}); err != nil {
			return false, err
		}
		if err := writer.emit("response.web_search_call.searching", map[string]any{"item_id": id, "output_index": index}); err != nil {
			return false, err
		}
		if err := writer.emit("response.web_search_call.completed", map[string]any{"item_id": id, "output_index": index}); err != nil {
			return false, err
		}
		if err := writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": completed}); err != nil {
			return false, err
		}
		emitted = true
	}
	return emitted, nil
}
