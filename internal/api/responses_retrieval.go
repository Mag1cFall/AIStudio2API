package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// handleResponsesInputTokens 用与生成相同的转换计算 Responses 请求的输入 token
func (s *server) handleResponsesInputTokens(w http.ResponseWriter, r *http.Request) {
	var request responsesRequest
	if err := decodeJSON(r, &request); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	prepared, err := s.prepareResponses(r.Context(), &request, newID("count"))
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		}
		return
	}
	if prepared.generate.Model == "" {
		prepared.generate.Model = prepared.previous.Model
	}
	if prepared.generate.Model == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "model is required")
		return
	}
	count, err := s.service.CountTokens(r.Context(), aistudio.TokenCountRequest{
		Model: prepared.generate.Model, System: prepared.generate.System, Contents: prepared.generate.Contents,
		Tools: prepared.generate.Tools,
	})
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "response.input_tokens", "input_tokens": count.InputTokens})
}

// handleResponseGet 返回已保存的响应对象
func (s *server) handleResponseGet(w http.ResponseWriter, r *http.Request) {
	if stream, _ := strconv.ParseBool(r.URL.Query().Get("stream")); stream {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "stored responses are complete; retrieve them without stream=true")
		return
	}
	response, _, ok := s.responseStates.Response(r.PathValue("response"))
	if !ok {
		writeResponseNotFound(w, r.PathValue("response"))
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// handleResponseDelete 删除已保存的响应
func (s *server) handleResponseDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("response")
	if !s.responseStates.Delete(id, false) {
		writeResponseNotFound(w, id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "object": "response", "deleted": true})
}

// handleResponseCancel 对后台模式创建的已完成响应返回最终响应对象，其他响应不可取消
func (s *server) handleResponseCancel(w http.ResponseWriter, r *http.Request) {
	response, background, ok := s.responseStates.Response(r.PathValue("response"))
	if !ok {
		writeResponseNotFound(w, r.PathValue("response"))
		return
	}
	if !background {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "Only responses created with background=true can be cancelled.")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// handleResponseInputItems 按 after、before、limit 与 order 分页返回生成该响应时的输入项
func (s *server) handleResponseInputItems(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, err := listLimit(query, listLimits{fallback: 20, min: 1, max: 100})
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	order := query.Get("order")
	if order == "" {
		order = "desc"
	}
	if order != "asc" && order != "desc" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "order must be asc or desc")
		return
	}
	items, ok := s.responseStates.InputItems(r.PathValue("response"))
	if !ok {
		writeResponseNotFound(w, r.PathValue("response"))
		return
	}
	if order == "desc" {
		slices.Reverse(items)
	}
	start, end := 0, len(items)
	for _, cursor := range []string{"after", "before"} {
		value := query.Get(cursor)
		if value == "" {
			continue
		}
		index := slices.IndexFunc(items, func(item storedItem) bool { return item.ID == value })
		if index < 0 {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("%s item %q is not an input item of this response", cursor, value))
			return
		}
		if cursor == "after" {
			start = index + 1
		} else {
			end = index
		}
	}
	page := items[min(start, end):end]
	hasMore := len(page) > limit
	if hasMore && query.Get("before") != "" && query.Get("after") == "" {
		page = page[len(page)-limit:]
	} else if hasMore {
		page = page[:limit]
	}
	data := make([]json.RawMessage, 0, len(page))
	for _, item := range page {
		data = append(data, item.Raw)
	}
	list := map[string]any{"object": "list", "data": data, "first_id": nil, "last_id": nil, "has_more": hasMore}
	if len(page) > 0 {
		list["first_id"], list["last_id"] = page[0].ID, page[len(page)-1].ID
	}
	writeJSON(w, http.StatusOK, list)
}

// writeResponseNotFound 返回响应不存在的 404
func writeResponseNotFound(w http.ResponseWriter, id string) {
	writeOpenAIError(w, http.StatusNotFound, "not_found", fmt.Sprintf("Response with id '%s' not found.", id))
}
