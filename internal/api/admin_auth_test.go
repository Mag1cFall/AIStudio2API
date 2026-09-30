package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// authExchange 在内存中执行管理认证请求
func authExchange(handler http.Handler, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("Origin", "http://localhost")
	r.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

// TestAdminSession 验证登录、会话 Cookie、登出与到期
func TestAdminSession(t *testing.T) {
	auth := newAdminAuth(Config{AdminAuthEnabled: true, AdminUsername: "operator", AdminPassword: "test-password"})
	handler := auth.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	if got := authExchange(handler, "GET", "/api/status", ""); got.Code != 401 {
		t.Fatalf("without session=%d", got.Code)
	}
	login := authExchange(handler, "POST", "/api/auth/login", `{"username":"operator","password":"test-password"}`)
	cookies := login.Result().Cookies()
	if login.Code != 200 || strings.Contains(login.Body.String(), "test-password") || len(cookies) != 1 {
		t.Fatalf("login=%d cookies=%d", login.Code, len(cookies))
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/api" || cookie.MaxAge != 43200 || cookie.Value == "" {
		t.Fatalf("cookie=%+v", cookie)
	}
	if got := authExchange(handler, "GET", "/api/status", "", cookie); got.Code != 204 || got.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("authenticated=%d", got.Code)
	}
	session := auth.sessions[cookie.Value]
	authExchange(handler, "POST", "/api/auth/logout", "", cookie)
	if got := authExchange(handler, "GET", "/api/status", "", cookie); got.Code != 401 || session.ctx.Err() == nil {
		t.Fatalf("logout=%d context=%v", got.Code, session.ctx.Err())
	}
	login = authExchange(handler, "POST", "/api/auth/login", `{"username":"operator","password":"test-password"}`)
	cookies = login.Result().Cookies()
	if login.Code != 200 || len(cookies) != 1 {
		t.Fatalf("second login=%d cookies=%d", login.Code, len(cookies))
	}
	cookie = cookies[0]
	auth.sessions[cookie.Value].expires = time.Unix(1, 0)
	if got := authExchange(handler, "GET", "/api/status", "", cookie); got.Code != 401 {
		t.Fatalf("expired=%d", got.Code)
	}
}

// TestAdminLoginModes 验证回环免密与远程登录页面的状态
func TestAdminLoginModes(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		auth := newAdminAuth(Config{AdminAuthEnabled: enabled, AdminUsername: "operator", AdminPassword: "test-password"})
		handler := auth.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
		r := httptest.NewRequest("GET", "https://console.example/api/auth/session", nil)
		r.RemoteAddr = "192.0.2.1:5000"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		want := 403
		if enabled {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("enabled=%v status=%d", enabled, w.Code)
		}
		if enabled {
			var value map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
			if value["authenticated"] != false || value["username"] != "" {
				t.Fatalf("session=%v", value)
			}
		} else if got := authExchange(handler, "GET", "/api/status", ""); got.Code != 204 {
			t.Fatalf("local=%d", got.Code)
		}
	}
}

// TestAdminLoginRateLimit 验证失败登录限流与到期后的恢复
func TestAdminLoginRateLimit(t *testing.T) {
	auth := newAdminAuth(Config{AdminAuthEnabled: true, AdminUsername: "operator", AdminPassword: "test-password"})
	handler := auth.handler(http.NotFoundHandler())
	for i := 0; i < 5; i++ {
		if got := authExchange(handler, "POST", "/api/auth/login", `{"username":"operator","password":"wrong"}`); got.Code != 401 {
			t.Fatal(got.Code)
		}
	}
	if got := authExchange(handler, "POST", "/api/auth/login", `{"username":"operator","password":"wrong"}`); got.Code != 429 || got.Header().Get("Retry-After") == "" {
		t.Fatal(got.Code)
	}
	auth.attempts["127.0.0.1"] = loginAttempt{count: 5, until: time.Unix(1, 0)}
	login := authExchange(handler, "POST", "/api/auth/login", `{"username":"operator","password":"test-password"}`)
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	authExchange(handler, "POST", "/api/auth/logout", "", login.Result().Cookies()[0])
}
