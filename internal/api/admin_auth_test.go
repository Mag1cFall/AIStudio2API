package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

// TestAdminAuthHotReload 验证管理登录开关与账号密码热切换：开启或改密时自动签发新会话防踢出并撤销其余旧会话，远程请求禁止关闭登录，本机关闭后恢复免密与回环限制
func TestAdminAuthHotReload(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "admin-sessions.json")
	auth := newAdminAuth(Config{AdminAuthEnabled: false, AdminSessionPath: sessionPath})
	handler := auth.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/test/enable":
			auth.ApplyConfig(true, "admin", "pass-1", w, r)
			w.WriteHeader(http.StatusOK)
		case "/api/test/rotate":
			auth.ApplyConfig(true, "admin2", "pass-2", w, r)
			if r.Context().Err() != nil {
				t.Errorf("current save request context was canceled during ApplyConfig: %v", r.Context().Err())
			}
			w.WriteHeader(http.StatusOK)
		case "/api/test/disable":
			if err := auth.ValidateConfigUpdate(r, false); err != nil {
				writeAdminError(w, http.StatusBadRequest, "invalid_config", err.Error())
				return
			}
			auth.ApplyConfig(false, "admin2", "pass-2", w, r)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))

	// 1. 初始关闭：本机免密，远程 403
	if got := authExchange(handler, "GET", "/api/status", ""); got.Code != http.StatusNoContent {
		t.Fatalf("disabled local status=%d", got.Code)
	}
	remoteGet := func(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "https://console.example"+path, nil)
		r.RemoteAddr = "192.0.2.10:5000"
		r.Header.Set("Origin", "https://console.example")
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := remoteGet("/api/auth/session"); got.Code != http.StatusForbidden {
		t.Fatalf("disabled remote session=%d want 403", got.Code)
	}

	// 2. 热开启管理登录：当前请求自动收到新 Cookie，无 Cookie 被拒 401，远程会话接口开放 200
	enabledResp := authExchange(handler, "POST", "/api/test/enable", "")
	if enabledResp.Code != http.StatusOK || len(enabledResp.Result().Cookies()) != 1 {
		t.Fatalf("enable resp=%d cookies=%d", enabledResp.Code, len(enabledResp.Result().Cookies()))
	}
	currentCookie := enabledResp.Result().Cookies()[0]
	if got := authExchange(handler, "GET", "/api/status", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("enabled unauthenticated=%d want 401", got.Code)
	}
	if got := authExchange(handler, "GET", "/api/status", "", currentCookie); got.Code != http.StatusNoContent {
		t.Fatalf("enabled current cookie=%d want 204", got.Code)
	}
	if got := remoteGet("/api/auth/session"); got.Code != http.StatusOK {
		t.Fatalf("enabled remote session=%d want 200", got.Code)
	}

	// 另一个记住我的客户端登录
	otherLogin := authExchange(handler, "POST", "/api/auth/login", `{"username":"admin","password":"pass-1","remember":true}`)
	if otherLogin.Code != http.StatusOK || len(otherLogin.Result().Cookies()) != 1 {
		t.Fatalf("other login=%d", otherLogin.Code)
	}
	otherCookie := otherLogin.Result().Cookies()[0]
	otherSession := auth.sessions[otherCookie.Value]

	// 3. 热改账号密码：撤销其余会话，当前请求上下文不被取消并签发新 Cookie，旧密码登录失败、新密码登录成功
	rotateResp := authExchange(handler, "POST", "/api/test/rotate", "", currentCookie)
	if rotateResp.Code != http.StatusOK || len(rotateResp.Result().Cookies()) != 1 {
		t.Fatalf("rotate resp=%d cookies=%d", rotateResp.Code, len(rotateResp.Result().Cookies()))
	}
	rotatedCookie := rotateResp.Result().Cookies()[0]
	if otherSession.ctx.Err() == nil {
		t.Fatal("other session was not canceled on password rotation")
	}
	if got := authExchange(handler, "GET", "/api/status", "", otherCookie); got.Code != http.StatusUnauthorized {
		t.Fatalf("revoked other cookie=%d want 401", got.Code)
	}
	if got := authExchange(handler, "GET", "/api/status", "", currentCookie); got.Code != http.StatusUnauthorized {
		t.Fatalf("old current cookie=%d want 401", got.Code)
	}
	if got := authExchange(handler, "GET", "/api/status", "", rotatedCookie); got.Code != http.StatusNoContent {
		t.Fatalf("rotated cookie=%d want 204", got.Code)
	}
	if got := authExchange(handler, "POST", "/api/auth/login", `{"username":"admin","password":"pass-1"}`); got.Code != http.StatusUnauthorized {
		t.Fatalf("old credentials login=%d want 401", got.Code)
	}
	if got := authExchange(handler, "POST", "/api/auth/login", `{"username":"admin2","password":"pass-2"}`); got.Code != http.StatusOK {
		t.Fatalf("new credentials login=%d want 200", got.Code)
	}

	// 4. 远程关闭管理登录被拒绝 400
	remoteDisable := httptest.NewRequest("POST", "https://console.example/api/test/disable", nil)
	remoteDisable.RemoteAddr = "192.0.2.10:5000"
	remoteDisable.Header.Set("Origin", "https://console.example")
	remoteDisable.AddCookie(rotatedCookie)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, remoteDisable)
	if rw.Code != http.StatusBadRequest || !strings.Contains(rw.Body.String(), "invalid_config") {
		t.Fatalf("remote disable=%d body=%s want 400 invalid_config", rw.Code, rw.Body.String())
	}

	// 5. 本机关闭管理登录：清除会话与 Cookie，本机免密恢复，远程恢复 403
	disableResp := authExchange(handler, "POST", "/api/test/disable", "", rotatedCookie)
	if disableResp.Code != http.StatusOK || len(disableResp.Result().Cookies()) != 1 || disableResp.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("local disable=%d cookies=%+v", disableResp.Code, disableResp.Result().Cookies())
	}
	if got := authExchange(handler, "GET", "/api/status", ""); got.Code != http.StatusNoContent {
		t.Fatalf("disabled again local=%d want 204", got.Code)
	}
	if got := remoteGet("/api/auth/session"); got.Code != http.StatusForbidden {
		t.Fatalf("disabled again remote=%d want 403", got.Code)
	}
}
