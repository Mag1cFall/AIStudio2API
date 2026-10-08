package api

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// openAIEmbeddingRequest 表示 OpenAI Embeddings 请求
type openAIEmbeddingRequest struct {
	Model          string          `json:"model"`
	Input          json.RawMessage `json:"input"`
	Dimensions     *int64          `json:"dimensions"`
	EncodingFormat string          `json:"encoding_format"`
}

// geminiEmbedContentRequest 表示 Gemini EmbedContentRequest
type geminiEmbedContentRequest struct {
	Model                string         `json:"model"`
	Content              *geminiContent `json:"content"`
	TaskType             string         `json:"taskType"`
	Title                string         `json:"title"`
	OutputDimensionality *int64         `json:"outputDimensionality"`
}

// openAIEmbeddingInputs 把 input 的字符串或字符串数组转换为 embedding 输入
func openAIEmbeddingInputs(raw json.RawMessage) ([]string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, fmt.Errorf("input is required")
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return []string{single}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("input must be a string or an array of strings")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("input must not be empty")
	}
	inputs := make([]string, 0, len(items))
	for index, item := range items {
		var text string
		if err := json.Unmarshal(item, &text); err != nil {
			return nil, fmt.Errorf("input[%d]: token arrays are not supported because Gemini embedding models use a different tokenizer; send text instead", index)
		}
		inputs = append(inputs, text)
	}
	return inputs, nil
}

// handleOpenAIEmbeddings 处理 OpenAI Embeddings，经 Build 通道调用 batchEmbedContents
func (s *server) handleOpenAIEmbeddings(w http.ResponseWriter, r *http.Request) {
	var request openAIEmbeddingRequest
	if err := decodeJSON(r, &request); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	model := strings.TrimSpace(request.Model)
	if model == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "model is required")
		return
	}
	inputs, err := openAIEmbeddingInputs(request.Input)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	format := strings.TrimSpace(request.EncodingFormat)
	if format != "" && format != "float" && format != "base64" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "encoding_format must be float or base64")
		return
	}
	if request.Dimensions != nil && *request.Dimensions <= 0 {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "dimensions must be a positive integer")
		return
	}
	requests := make([]aistudio.EmbedContentRequest, 0, len(inputs))
	for _, input := range inputs {
		requests = append(requests, aistudio.EmbedContentRequest{
			Content:              aistudio.Content{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: input}}},
			OutputDimensionality: request.Dimensions,
		})
	}
	result, err := s.embed(r, model, requests)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		}
		return
	}
	data := make([]map[string]any, 0, len(result.Embeddings))
	for index, values := range result.Embeddings {
		item := map[string]any{"object": "embedding", "index": index, "embedding": values}
		if format == "base64" {
			item["embedding"] = embeddingBase64(values)
		}
		data = append(data, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list", "data": data, "model": strings.TrimPrefix(model, "models/"),
		"usage": map[string]int64{"prompt_tokens": result.TokenCount, "total_tokens": result.TokenCount},
	})
}

// embeddingBase64 按 OpenAI base64 编码把向量写成小端 float32 字节
func embeddingBase64(values []float64) string {
	encoded := make([]byte, 4*len(values))
	for index, value := range values {
		binary.LittleEndian.PutUint32(encoded[4*index:], math.Float32bits(float32(value)))
	}
	return base64.StdEncoding.EncodeToString(encoded)
}

// handleGeminiEmbed 处理 Gemini embedContent 与 batchEmbedContents，两者都以 batchEmbedContents 经 Build 通道发送
func (s *server) handleGeminiEmbed(w http.ResponseWriter, r *http.Request, model string, batch bool) {
	body, err := io.ReadAll(r.Body)
	if err == nil {
		body, _, err = geminiCamelKeys(body)
	}
	var single geminiEmbedContentRequest
	var multiple struct {
		Requests []geminiEmbedContentRequest `json:"requests"`
	}
	if err == nil {
		if batch {
			err = json.Unmarshal(body, &multiple)
		} else {
			err = json.Unmarshal(body, &single)
			multiple.Requests = []geminiEmbedContentRequest{single}
		}
	}
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if len(multiple.Requests) == 0 {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "requests must not be empty")
		return
	}
	requests := make([]aistudio.EmbedContentRequest, 0, len(multiple.Requests))
	for index, item := range multiple.Requests {
		if name := strings.TrimPrefix(strings.TrimSpace(item.Model), "models/"); name != "" && name != model {
			writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("requests[%d].model %q does not match models/%s", index, item.Model, model))
			return
		}
		if item.Content == nil {
			writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("requests[%d].content is required", index))
			return
		}
		parts, _, err := mapGeminiParts(item.Content.Parts)
		if err != nil {
			writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("requests[%d].content: %v", index, err))
			return
		}
		requests = append(requests, aistudio.EmbedContentRequest{
			Content: aistudio.Content{Role: aistudio.RoleUser, Parts: parts}, TaskType: item.TaskType, Title: item.Title,
			OutputDimensionality: item.OutputDimensionality,
		})
	}
	result, err := s.embed(r, model, requests)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiError(w, statusFromError(err), geminiErrorStatus(err), err.Error())
		}
		return
	}
	embeddings := make([]map[string]any, 0, len(result.Embeddings))
	for _, values := range result.Embeddings {
		embeddings = append(embeddings, map[string]any{"values": values})
	}
	if !batch {
		writeJSON(w, http.StatusOK, map[string]any{"embedding": embeddings[0]})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"embeddings": embeddings})
}

// embed 调用支持 embedding 的服务
func (s *server) embed(r *http.Request, model string, requests []aistudio.EmbedContentRequest) (aistudio.EmbeddingResult, error) {
	service, ok := s.service.(aistudio.EmbeddingService)
	if !ok {
		return aistudio.EmbeddingResult{}, fmt.Errorf("%w: embedding service is unavailable", aistudio.ErrModelNotFound)
	}
	return service.Embed(r.Context(), aistudio.EmbeddingRequest{ID: newID("embed"), Model: model, Requests: requests})
}
