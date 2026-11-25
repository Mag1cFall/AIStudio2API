package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestAuthMiddlewarePriorityAndMatching(t *testing.T) {
	requiredKey := "secret-123"
	handler := authMiddleware(func() string { return requiredKey }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// TestDynamicAPIKeyHotReload 验证 PROXY_API_KEY 热替换后对下一次请求立即生效，并同步切换无密钥时的跨源拦截与上下文密钥绑定
func TestDynamicAPIKeyHotReload(t *testing.T) {
	currentKey := ""
	keySource := func() string { return currentKey }
	pool := aistudio.NewAccountPool([]*aistudio.Account{
		{ID: "acct-b", State: aistudio.AccountReady, Config: aistudio.AccountConfig{Enabled: true}},
		{ID: "acct-c", State: aistudio.AccountReady, Config: aistudio.AccountConfig{Enabled: true}},
	}, 1)
	pool.SetAPIKeyAccounts(map[string][]string{"key-b": {"acct-b"}, "key-c": {"acct-c"}})
	var allowed []string
	handler := browserOriginMiddleware(keySource, authMiddleware(keySource, apiKeyContextMiddleware(keySource, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed = pool.ScopedSelection(r.Context(), aistudio.AccountSelection{}).AllowedAccountIDs
		w.WriteHeader(http.StatusOK)
	}))))

	send := func(headers map[string]string) int {
		allowed = nil
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	// 1. 初始无密钥：免密放行，跨源浏览器请求被拒 401
	if code := send(nil); code != http.StatusOK || len(allowed) != 0 {
		t.Fatalf("empty key no-auth: code=%d allowed=%v", code, allowed)
	}
	if code := send(map[string]string{"Origin": "https://evil.example"}); code != http.StatusUnauthorized {
		t.Fatalf("empty key cross-origin: code=%d want 401", code)
	}

	// 2. 热切为多密钥：无密钥 401，匹配 key-b 放行且上下文按 key-b 限定账户，跨源 Origin 放行
	currentKey = "key-a, key-b"
	if code := send(nil); code != http.StatusUnauthorized {
		t.Fatalf("configured key without auth: code=%d want 401", code)
	}
	if code := send(map[string]string{"Authorization": "Bearer key-b", "Origin": "https://app.example"}); code != http.StatusOK || len(allowed) != 1 || allowed[0] != "acct-b" {
		t.Fatalf("configured key-b: code=%d allowed=%v", code, allowed)
	}

	// 3. 热切为新密钥：旧 key-b 立即失效 401，新 key-c 立即放行并绑定 acct-c
	currentKey = "key-c"
	if code := send(map[string]string{"Authorization": "Bearer key-b"}); code != http.StatusUnauthorized {
		t.Fatalf("rotated old key-b: code=%d want 401", code)
	}
	if code := send(map[string]string{"Authorization": "Bearer key-c"}); code != http.StatusOK || len(allowed) != 1 || allowed[0] != "acct-c" {
		t.Fatalf("rotated new key-c: code=%d allowed=%v", code, allowed)
	}

	// 4. 热切回空密钥：免密恢复，跨源保护恢复
	currentKey = ""
	if code := send(nil); code != http.StatusOK || len(allowed) != 0 {
		t.Fatalf("cleared key: code=%d allowed=%v", code, allowed)
	}
	if code := send(map[string]string{"Origin": "https://evil.example"}); code != http.StatusUnauthorized {
		t.Fatalf("cleared key cross-origin: code=%d want 401", code)
	}
}
