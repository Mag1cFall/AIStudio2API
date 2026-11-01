package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddlewarePriorityAndMatching(t *testing.T) {
	requiredKey := "secret-123"
	handler := authMiddleware(requiredKey, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name       string
		url        string
		headers    map[string]string
		wantStatus int
	}{
		{
			name:       "query key matches",
			url:        "/v1beta/models/gemini?key=secret-123",
			headers:    nil,
			wantStatus: http.StatusOK,
		},
		{
			name:       "x-goog-api-key matches while query has bogus token (SillyTavern + New API)",
			url:        "/v1beta/models/gemini?key=sk-user-bogus-token&alt=sse",
			headers:    map[string]string{"X-Goog-API-Key": "secret-123"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "bearer token matches while query has bogus token",
			url:        "/v1beta/models/gemini?key=sk-user-bogus-token",
			headers:    map[string]string{"Authorization": "Bearer secret-123"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "x-api-key matches",
			url:        "/v1/messages",
			headers:    map[string]string{"X-API-Key": "secret-123"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "all mismatched",
			url:        "/v1beta/models/gemini?key=wrong-key",
			headers:    map[string]string{"X-Goog-API-Key": "wrong-header"},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.url, nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

// TestAuthMiddlewareAPIKeyAffinitySeed 验证命中的 API key 以 SHA-256 种子进入请求上下文
func TestAuthMiddlewareAPIKeyAffinitySeed(t *testing.T) {
	seedOf := func(key string) string {
		sum := sha256.Sum256([]byte(key))
		return hex.EncodeToString(sum[:])
	}
	tests := []struct {
		name     string
		keys     string
		headers  map[string]string
		wantSeed string
	}{
		{name: "bearer k2", keys: "k1,k2", headers: map[string]string{"Authorization": "Bearer k2"}, wantSeed: seedOf("k2")},
		{name: "x-goog k1", keys: "k1,k2", headers: map[string]string{"X-Goog-API-Key": "k1"}, wantSeed: seedOf("k1")},
		{name: "no keys configured", keys: "", wantSeed: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSeed := "unset"
			handler := authMiddleware(tt.keys, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotSeed = APIKeyAffinitySeed(r.Context())
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			if gotSeed != tt.wantSeed {
				t.Errorf("seed = %q, want %q", gotSeed, tt.wantSeed)
			}
		})
	}
}
