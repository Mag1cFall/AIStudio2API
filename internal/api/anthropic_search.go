package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// anthropicSearchResult 保存可回传的搜索来源
type anthropicSearchResult struct {
	Type             string  `json:"type"`
	URL              string  `json:"url"`
	Title            string  `json:"title"`
	EncryptedContent string  `json:"encrypted_content"`
	PageAge          *string `json:"page_age"`
}

// anthropicSearchCipher 将来源上下文绑定到当前服务凭证
func anthropicSearchCipher(apiKey string) cipher.AEAD {
	key := sha256.Sum256([]byte("aistudio-anthropic-search\x00" + apiKey))
	block, _ := aes.NewCipher(key[:])
	aead, _ := cipher.NewGCMWithRandomNonce(block)
	return aead
}

// anthropicSearchBlocks 将上游实际查询和来源投影为服务端工具块
func anthropicSearchBlocks(events []aistudio.Event, apiKey string) ([]anthropicContentBlock, int) {
	queries := make([]string, 0)
	sources := make([]aistudio.GroundingChunk, 0)
	seenQueries := make(map[string]bool)
	seenSources := make(map[string]bool)
	for _, event := range events {
		if event.Grounding == nil {
			continue
		}
		for _, query := range event.Grounding.WebSearchQueries {
			if query != "" && !seenQueries[query] {
				seenQueries[query] = true
				queries = append(queries, query)
			}
		}
		for _, source := range event.Grounding.Chunks {
			if source.URI != "" && !seenSources[source.URI] {
				seenSources[source.URI] = true
				sources = append(sources, source)
			}
		}
	}
	if len(queries) == 0 {
		return nil, 0
	}
	aead := anthropicSearchCipher(apiKey)
	results := make([]anthropicSearchResult, 0, len(sources))
	for _, source := range sources {
		payload, _ := json.Marshal(source)
		sealed := aead.Seal(nil, nil, payload, nil)
		results = append(results, anthropicSearchResult{
			Type: "web_search_result", URL: source.URI, Title: source.Title,
			EncryptedContent: base64.StdEncoding.EncodeToString(sealed),
		})
	}
	content, _ := json.Marshal(results)
	blocks := make([]anthropicContentBlock, 0, len(queries)*2)
	for _, query := range queries {
		id := newID("srvtoolu")
		input, _ := json.Marshal(map[string]string{"query": query})
		blocks = append(blocks,
			anthropicContentBlock{Type: "server_tool_use", ID: id, Name: "web_search", Input: input},
			anthropicContentBlock{Type: "web_search_tool_result", ToolUseID: id, Content: content},
		)
	}
	return blocks, len(queries)
}

// decodeAnthropicSearchHistory 还原搜索历史中的来源文本并保持普通消息块
func (s *server) decodeAnthropicSearchHistory(request *anthropicRequest) error {
	for index := range request.Messages {
		message := &request.Messages[index]
		if !strings.HasPrefix(strings.TrimSpace(string(message.Content)), "[") {
			continue
		}
		var blocks []json.RawMessage
		if err := json.Unmarshal(message.Content, &blocks); err != nil {
			return err
		}
		calls := make(map[string]string)
		converted := make([]json.RawMessage, 0, len(blocks))
		for _, raw := range blocks {
			var block anthropicContentBlock
			if err := json.Unmarshal(raw, &block); err != nil {
				return err
			}
			switch block.Type {
			case "server_tool_use":
				if message.Role != "assistant" || block.Name != "web_search" || block.ID == "" {
					return fmt.Errorf("invalid web_search server_tool_use")
				}
				var input struct {
					Query string `json:"query"`
				}
				if err := json.Unmarshal(block.Input, &input); err != nil {
					return fmt.Errorf("web_search input: %w", err)
				}
				calls[block.ID] = input.Query
			case "web_search_tool_result":
				query, exists := calls[block.ToolUseID]
				if message.Role != "assistant" || !exists {
					return fmt.Errorf("web_search_tool_result requires its server_tool_use")
				}
				delete(calls, block.ToolUseID)
				var results []anthropicSearchResult
				if err := json.Unmarshal(block.Content, &results); err != nil {
					return fmt.Errorf("web_search_tool_result content: %w", err)
				}
				aead := anthropicSearchCipher(s.config.APIKey)
				text := "Web search: " + query
				for _, result := range results {
					sealed, err := base64.StdEncoding.DecodeString(result.EncryptedContent)
					if err != nil {
						return fmt.Errorf("invalid web_search encrypted_content")
					}
					payload, err := aead.Open(nil, nil, sealed, nil)
					if err != nil {
						return fmt.Errorf("invalid web_search encrypted_content")
					}
					var source aistudio.GroundingChunk
					if err := json.Unmarshal(payload, &source); err != nil {
						return fmt.Errorf("invalid web_search source")
					}
					if result.Type != "web_search_result" || result.URL != source.URI || result.Title != source.Title {
						return fmt.Errorf("web_search source does not match encrypted_content")
					}
					text += "\n" + source.Title + "\n" + source.URI + "\n" + source.Text
				}
				encoded, _ := json.Marshal(anthropicContentBlock{Type: "text", Text: text})
				converted = append(converted, encoded)
			default:
				converted = append(converted, raw)
			}
		}
		if len(calls) > 0 {
			return fmt.Errorf("web_search server_tool_use requires its result")
		}
		message.Content, _ = json.Marshal(converted)
	}
	return nil
}
