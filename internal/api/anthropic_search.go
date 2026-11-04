package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
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

// anthropicSearchBlocks 将上游搜索调用步骤与 grounding 中的查询和来源投影为服务端工具块，返回各查询按首次出现顺序对应的一组 server_tool_use 与 web_search_tool_result
func anthropicSearchBlocks(events []aistudio.Event, apiKey string) ([]string, map[string][]anthropicContentBlock) {
	queries := make([]string, 0)
	sources := make([]aistudio.GroundingChunk, 0)
	seenQueries := make(map[string]bool)
	seenSources := make(map[string]bool)
	addQueries := func(found []string) {
		for _, query := range found {
			if query != "" && !seenQueries[query] {
				seenQueries[query] = true
				queries = append(queries, query)
			}
		}
	}
	for _, event := range events {
		if step := event.ToolCall; step != nil && groundingToolTypes[step.Type] == "google_search" {
			addQueries(searchStepQueries(*step))
		}
		if event.Grounding == nil {
			continue
		}
		addQueries(event.Grounding.WebSearchQueries)
		for _, source := range event.Grounding.Chunks {
			if source.URI != "" && !seenSources[source.URI] {
				seenSources[source.URI] = true
				sources = append(sources, source)
			}
		}
	}
	if len(queries) == 0 {
		return nil, nil
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
	blocks := make(map[string][]anthropicContentBlock, len(queries))
	for _, query := range queries {
		id := newID("srvtoolu")
		input, _ := json.Marshal(map[string]string{"query": query})
		blocks[query] = []anthropicContentBlock{
			{Type: "server_tool_use", ID: id, Name: "web_search", Input: input},
			{Type: "web_search_tool_result", ToolUseID: id, Content: content},
		}
	}
	return queries, blocks
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

// anthropicCitedBlock 是写出器中带搜索引用的 text 块的块类型
const anthropicCitedBlock = "cited_text"

// anthropicCitationGroup 是上游正文中被同一组来源支撑的字节区间 [start,end) 与对应的 web_search_result_location 引用
type anthropicCitationGroup struct {
	start, end int
	citations  []any
}

// anthropicCitationGroups 把 grounding 中带区间的来源按区间合并并按起点排序，cited_text 为该区间的回答正文，encrypted_index 为加密的来源
func anthropicCitationGroups(events []aistudio.Event, apiKey string) []anthropicCitationGroup {
	var text strings.Builder
	var citations []aistudio.Citation
	for _, event := range events {
		switch {
		case event.Kind == aistudio.EventText:
			text.WriteString(event.Text)
		case event.Grounding != nil:
			citations = append(citations, groundingCitations(*event.Grounding)...)
		}
	}
	answer := text.String()
	aead := anthropicSearchCipher(apiKey)
	groups := make([]anthropicCitationGroup, 0)
	for _, citation := range citations {
		if citation.End <= citation.Start || citation.End > len(answer) {
			continue
		}
		index := slices.IndexFunc(groups, func(group anthropicCitationGroup) bool {
			return group.start == citation.Start && group.end == citation.End
		})
		if index < 0 {
			groups = append(groups, anthropicCitationGroup{start: citation.Start, end: citation.End})
			index = len(groups) - 1
		}
		if slices.ContainsFunc(groups[index].citations, func(item any) bool { return item.(map[string]any)["url"] == citation.URL }) {
			continue
		}
		payload, _ := json.Marshal(aistudio.GroundingChunk{URI: citation.URL, Title: citation.Title})
		groups[index].citations = append(groups[index].citations, map[string]any{
			"type": "web_search_result_location", "cited_text": answer[citation.Start:citation.End], "url": citation.URL, "title": citation.Title,
			"encrypted_index": base64.StdEncoding.EncodeToString(aead.Seal(nil, nil, payload, nil)),
		})
	}
	slices.SortStableFunc(groups, func(a, b anthropicCitationGroup) int { return a.start - b.start })
	return groups
}

// citedText 写出搜索回答的一段正文，被来源支撑的区间写成带 citations 的独立 text 块
func (writer *anthropicStreamWriter) citedText(text string) error {
	for text != "" {
		if writer.cited == nil && len(writer.citations) > 0 && writer.citations[0].start <= writer.source {
			writer.cited = &writer.citations[0]
			writer.citations = writer.citations[1:]
			if writer.cited.end <= writer.source {
				writer.cited = nil
				continue
			}
		}
		length := len(text)
		if writer.cited != nil {
			length = min(length, writer.cited.end-writer.source)
			if writer.currentBlock != anthropicCitedBlock {
				if err := writer.startCited(writer.cited.citations); err != nil {
					return err
				}
			}
		} else {
			if len(writer.citations) > 0 {
				length = min(length, writer.citations[0].start-writer.source)
			}
			if err := writer.ensureBlock("text"); err != nil {
				return err
			}
		}
		if err := writer.send("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": writer.blockIndex,
			"delta": map[string]any{"type": "text_delta", "text": text[:length]},
		}); err != nil {
			return err
		}
		writer.source += length
		text = text[length:]
		if writer.cited != nil && writer.source >= writer.cited.end {
			writer.cited = nil
			if err := writer.closeBlock(); err != nil {
				return err
			}
		}
	}
	return nil
}

// startCited 打开带搜索引用的 text 块并逐条写出 citations_delta
func (writer *anthropicStreamWriter) startCited(citations []any) error {
	if err := writer.closeBlock(); err != nil {
		return err
	}
	if err := writer.send("content_block_start", map[string]any{
		"type": "content_block_start", "index": writer.blockIndex,
		"content_block": map[string]any{"type": "text", "text": "", "citations": nil},
	}); err != nil {
		return err
	}
	writer.currentBlock = anthropicCitedBlock
	for _, citation := range citations {
		if err := writer.send("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": writer.blockIndex,
			"delta": map[string]any{"type": "citations_delta", "citation": citation},
		}); err != nil {
			return err
		}
	}
	return nil
}
