package api

import (
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
