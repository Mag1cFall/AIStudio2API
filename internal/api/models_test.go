package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type mockModelService struct {
	models []aistudio.Model
	err    error
}

func (m *mockModelService) Models(ctx context.Context) ([]aistudio.Model, error) {
	return m.models, m.err
}

func (m *mockModelService) CountTokens(ctx context.Context, req aistudio.TokenCountRequest) (aistudio.TokenCount, error) {
	return aistudio.TokenCount{}, nil
}

func (m *mockModelService) Generate(ctx context.Context, req aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	ch := make(chan aistudio.Event)
	close(ch)
	return ch, nil
}

func TestOpenAIModelsEndpoint(t *testing.T) {
	service := &mockModelService{
		models: []aistudio.Model{
			{
				ID:               "gemini-2.5-flash",
				Name:             "Gemini 2.5 Flash",
				Description:      "Fast and versatile multimodal model",
				Methods:          []string{"generateContent", "countTokens"},
				InputTokenLimit:  1048576,
				OutputTokenLimit: 8192,
				Capabilities:     map[string]bool{"multimodal": true},
				CapabilityOptions: map[string][]string{
					"aliases": {"gemini-flash"},
				},
				Channels: []string{"webchannel"},
				Paid:     false,
			},
			{
				ID:               "gemini-2.5-pro",
				Name:             "Gemini 2.5 Pro",
				Description:      "Complex reasoning model",
				Methods:          []string{"generateContent"},
				InputTokenLimit:  2097152,
				OutputTokenLimit: 8192,
				Paid:             true,
			},
		},
	}
	handler := NewHandler(service, Config{})

	t.Run("list models openai", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp struct {
			Object string           `json:"object"`
			Data   []map[string]any `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if resp.Object != "list" || len(resp.Data) != 2 {
			t.Fatalf("unexpected response: %+v", resp)
		}
		if resp.Data[0]["id"] != "gemini-2.5-flash" || resp.Data[0]["object"] != "model" {
			t.Fatalf("unexpected model data: %+v", resp.Data[0])
		}
	})

	t.Run("list models anthropic", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		req.Header.Set("Anthropic-Version", "2023-06-01")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if len(resp.Data) != 2 || resp.Data[0]["type"] != "model" {
			t.Fatalf("unexpected anthropic response: %+v", resp)
		}
	})

	t.Run("retrieve model by id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/models/gemini-2.5-flash", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var model map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &model); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if model["id"] != "gemini-2.5-flash" {
			t.Fatalf("expected id gemini-2.5-flash, got %v", model["id"])
		}
		if model["object"] != "model" {
			t.Fatalf("expected object model, got %v", model["object"])
		}
		if model["owned_by"] != "google" {
			t.Fatalf("expected owned_by google, got %v", model["owned_by"])
		}
		if model["name"] != "Gemini 2.5 Flash" {
			t.Fatalf("expected name Gemini 2.5 Flash, got %v", model["name"])
		}
	})

	t.Run("retrieve model with prefix models/", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/models/models/gemini-2.5-flash", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var model map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &model); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if model["id"] != "gemini-2.5-flash" {
			t.Fatalf("expected id gemini-2.5-flash, got %v", model["id"])
		}
	})

	t.Run("retrieve model by alias", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/models/gemini-flash", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var model map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &model); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if model["id"] != "gemini-2.5-flash" {
			t.Fatalf("expected canonical id gemini-2.5-flash, got %v", model["id"])
		}
	})

	t.Run("retrieve model not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/models/non-existent-model", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
		}

		var errResp struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if errResp.Error.Code != "model_not_found" {
			t.Fatalf("expected code model_not_found, got %s", errResp.Error.Code)
		}
		if errResp.Error.Type != "invalid_request_error" {
			t.Fatalf("expected type invalid_request_error, got %s", errResp.Error.Type)
		}
	})

	t.Run("retrieve model anthropic found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/models/gemini-2.5-flash", nil)
		req.Header.Set("Anthropic-Version", "2023-06-01")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var model map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &model); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if model["type"] != "model" || model["id"] != "gemini-2.5-flash" || model["display_name"] != "Gemini 2.5 Flash" {
			t.Fatalf("unexpected anthropic model: %+v", model)
		}
	})

	t.Run("retrieve model anthropic not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/models/non-existent-model", nil)
		req.Header.Set("Anthropic-Version", "2023-06-01")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
		}

		var errResp struct {
			Type  string `json:"type"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if errResp.Type != "error" || errResp.Error.Type != "not_found_error" {
			t.Fatalf("unexpected anthropic error: %+v", errResp)
		}
	})
}
