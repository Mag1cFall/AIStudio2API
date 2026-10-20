package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"
)

// ErrPromptNotFound 表示对话不存在
var ErrPromptNotFound = errors.New("prompt not found")

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
	Prompts(context.Context) ([]PromptSummary, error)
	Prompt(context.Context, string) (Prompt, error)
	SavePrompt(context.Context, Prompt) (PromptSummary, error)
	DeletePrompt(context.Context, string) error
}

func (s *server) registerPrompts(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/prompts", s.handlePrompts)
	mux.HandleFunc("GET /api/prompts/{id}", s.handlePrompt)
	mux.HandleFunc("PUT /api/prompts/{id}", s.handleSavePrompt)
	mux.HandleFunc("DELETE /api/prompts/{id}", s.handleDeletePrompt)
}

func (s *server) handlePrompts(w http.ResponseWriter, r *http.Request) {
	prompts, err := s.config.Prompts.Prompts(r.Context())
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "prompt_store_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string][]PromptSummary{"prompts": prompts})
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
