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

// decodeAnthropicSearchHistory 将搜索历史改写为文本块，查询与其结果合并到同一块
func (s *server) decodeAnthropicSearchHistory(request *anthropicRequest) error {
	aead := anthropicSearchCipher(s.config.APIKey)
	for index := range request.Messages {
		message := &request.Messages[index]
		if !strings.HasPrefix(strings.TrimSpace(string(message.Content)), "[") {
			continue
		}
		var blocks []json.RawMessage
		if err := json.Unmarshal(message.Content, &blocks); err != nil {
			return err
		}
		calls := make(map[string]int)
		converted := make([]any, 0, len(blocks))
		for _, raw := range blocks {
			var block anthropicContentBlock
			if err := json.Unmarshal(raw, &block); err != nil {
				return err
			}
			switch block.Type {
			case "server_tool_use":
				var input struct {
					Query string `json:"query"`
				}
				if block.Name != "web_search" || json.Unmarshal(block.Input, &input) != nil {
					converted = append(converted, raw)
					continue
				}
				calls[block.ID] = len(converted)
				converted = append(converted, anthropicContentBlock{Type: "text", Text: "Web search: " + input.Query})
			case "web_search_tool_result":
				text, err := anthropicSearchResultText(aead, block.Content)
				if err != nil {
					return err
				}
				if position, exists := calls[block.ToolUseID]; exists {
					call := converted[position].(anthropicContentBlock)
					call.Text += text
					converted[position] = call
					continue
				}
				converted = append(converted, anthropicContentBlock{Type: "text", Text: "Web search results:" + text})
			default:
				converted = append(converted, raw)
			}
		}
		message.Content, _ = json.Marshal(converted)
	}
	return nil
}

// anthropicSearchResultText 渲染搜索结果或失败原因，摘要只从本服务签发的来源中恢复
func anthropicSearchResultText(aead cipher.AEAD, content json.RawMessage) (string, error) {
	var results []anthropicSearchResult
	if err := json.Unmarshal(content, &results); err != nil {
		var failure struct {
			ErrorCode string `json:"error_code"`
		}
		if json.Unmarshal(content, &failure) != nil || failure.ErrorCode == "" {
			return "", fmt.Errorf("web_search_tool_result content: %w", err)
		}
		return "\nWeb search failed: " + failure.ErrorCode, nil
	}
	text := ""
	for _, result := range results {
		text += "\n" + result.Title + "\n" + result.URL
		if snippet := anthropicSearchSnippet(aead, result); snippet != "" {
			text += "\n" + snippet
		}
	}
	return text, nil
}

// anthropicSearchSnippet 用当前凭证解出来源摘要，来源来自其他服务或凭证时返回空
func anthropicSearchSnippet(aead cipher.AEAD, result anthropicSearchResult) string {
	sealed, err := base64.StdEncoding.DecodeString(result.EncryptedContent)
	if err != nil {
		return ""
	}
	payload, err := aead.Open(nil, nil, sealed, nil)
	if err != nil {
		return ""
	}
	var source aistudio.GroundingChunk
	if json.Unmarshal(payload, &source) != nil || source.URI != result.URL {
		return ""
	}
	return source.Text
}
