package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// ErrPromptNotFound 表示对话不存在
var ErrPromptNotFound = errors.New("prompt not found")

// ErrInvalidPageToken 表示对话列表的翻页令牌无法解析
var ErrInvalidPageToken = errors.New("invalid page_token")

// promptIDPattern 是对话 ID 的取值范围
var promptIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// PromptSummary 是对话列表中的一项
type PromptSummary struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Model       string    `json:"model"`
	Description string    `json:"description"`
	Created     time.Time `json:"created_at"`
	Updated     time.Time `json:"updated_at"`
}

// Prompt 是一份保存的对话，Data 为管理端写入的对话内容
type Prompt struct {
	PromptSummary
	Data json.RawMessage `json:"data"`
}

// PromptStore 保存 Playground 对话
type PromptStore interface {
	Prompts(ctx context.Context, pageSize int, pageToken string) ([]PromptSummary, string, error)
	Prompt(context.Context, string) (Prompt, error)
	SavePrompt(context.Context, Prompt) (PromptSummary, error)
	RenamePrompt(ctx context.Context, id, title, description string) (PromptSummary, error)
	CopyPrompt(ctx context.Context, source, id, title string) (PromptSummary, error)
	DeletePrompt(context.Context, string) error
}

func (s *server) registerPrompts(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/prompts", s.handlePrompts)
	mux.HandleFunc("GET /api/prompts/{id}", s.handlePrompt)
	mux.HandleFunc("PUT /api/prompts/{id}", s.handleSavePrompt)
	mux.HandleFunc("PATCH /api/prompts/{id}", s.handleRenamePrompt)
	mux.HandleFunc("POST /api/prompts/{id}/copy", s.handleCopyPrompt)
	mux.HandleFunc("DELETE /api/prompts/{id}", s.handleDeletePrompt)
	mux.HandleFunc("POST /api/prompts/title", s.handleGenerateTitle)
}

// handleGenerateTitle 按本次运行的模型、提示词与回答经 GenerateTitle 生成对话标题
func (s *server) handleGenerateTitle(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.TitleService)
	if !ok {
		writeAdminError(w, http.StatusNotImplemented, "title_unavailable", "生成服务不支持生成标题")
		return
	}
	var request struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
		Answer string `json:"answer"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	title, err := service.GenerateTitle(r.Context(), aistudio.TitleRequest{Model: request.Model, Prompt: request.Prompt, Answer: request.Answer})
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"title": title})
}

// handlePrompts 按更新时间倒序返回一页对话，page_size 缺省时返回 page_token 之后的全部
func (s *server) handlePrompts(w http.ResponseWriter, r *http.Request) {
	pageSize := 0
	if value := r.URL.Query().Get("page_size"); value != "" {
		size, err := strconv.Atoi(value)
		if err != nil || size < 1 {
			writeAdminError(w, http.StatusBadRequest, "invalid_request", "page_size 必须是正整数")
			return
		}
		pageSize = size
	}
	prompts, next, err := s.config.Prompts.Prompts(r.Context(), pageSize, r.URL.Query().Get("page_token"))
	if errors.Is(err, ErrInvalidPageToken) {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "prompt_store_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Prompts       []PromptSummary `json:"prompts"`
		NextPageToken string          `json:"next_page_token,omitempty"`
	}{prompts, next})
}

func (s *server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	prompt, err := s.config.Prompts.Prompt(r.Context(), r.PathValue("id"))
	if err != nil {
		writePromptError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, prompt)
}

func (s *server) handleSavePrompt(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !promptIDPattern.MatchString(id) {
		writeAdminError(w, http.StatusBadRequest, "invalid_prompt_id", "Prompt ID must be 1 to 64 letters, digits, '-' or '_'")
		return
	}
	var body struct {
		Title       string          `json:"title"`
		Model       string          `json:"model"`
		Description string          `json:"description"`
		Data        json.RawMessage `json:"data"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(body.Data) == 0 || body.Data[0] != '{' {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "data must be a JSON object")
		return
	}
	saved, err := s.config.Prompts.SavePrompt(r.Context(), Prompt{
		PromptSummary: PromptSummary{ID: id, Title: body.Title, Model: body.Model, Description: body.Description}, Data: body.Data,
	})
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "prompt_store_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

// handleRenamePrompt 只改写对话的标题与描述
func (s *server) handleRenamePrompt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	saved, err := s.config.Prompts.RenamePrompt(r.Context(), r.PathValue("id"), body.Title, body.Description)
	if err != nil {
		writePromptError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

// handleCopyPrompt 在对话库中把对话复制为 ID 与标题由请求给出的新对话
func (s *server) handleCopyPrompt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !promptIDPattern.MatchString(body.ID) {
		writeAdminError(w, http.StatusBadRequest, "invalid_prompt_id", "Prompt ID must be 1 to 64 letters, digits, '-' or '_'")
		return
	}
	saved, err := s.config.Prompts.CopyPrompt(r.Context(), r.PathValue("id"), body.ID, body.Title)
	if err != nil {
		writePromptError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *server) handleDeletePrompt(w http.ResponseWriter, r *http.Request) {
	if err := s.config.Prompts.DeletePrompt(r.Context(), r.PathValue("id")); err != nil {
		writePromptError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writePromptError 把对话库错误写为管理端错误
func writePromptError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrPromptNotFound) {
		writeAdminError(w, http.StatusNotFound, "prompt_not_found", "Prompt not found")
		return
	}
	writeAdminError(w, http.StatusInternalServerError, "prompt_store_failed", err.Error())
}
