package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const adminCookieName = "aistudio_admin"
const adminSessionLifetime = 12 * time.Hour

// adminSession 保存管理会话及其关联请求的取消信号
type adminSession struct {
	ctx     context.Context
	cancel  context.CancelFunc
	expires time.Time
}

// loginAttempt 保存来源地址在一分钟内的失败次数
type loginAttempt struct {
	count int
	until time.Time
}

// adminAuth 管理独立于生成 API 密钥的登录会话
type adminAuth struct {
	mu       sync.Mutex
	config   Config
	sessions map[string]*adminSession
	attempts map[string]loginAttempt
}

// newAdminAuth 创建进程级管理认证
func newAdminAuth(config Config) *adminAuth {
	return &adminAuth{config: config, sessions: make(map[string]*adminSession), attempts: make(map[string]loginAttempt)}
}

// handler 统一管理认证、同源检查与会话接口
func (auth *adminAuth) handler(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/auth/session", auth.status)
	mux.HandleFunc("POST /api/auth/login", auth.login)
	mux.HandleFunc("POST /api/auth/logout", auth.logout)
	mux.Handle("/", auth.requireSession(next))
	var handler http.Handler = sameOriginMiddleware(mux)
	if !auth.config.AdminAuthEnabled {
		handler = loopbackMiddleware(handler)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		handler.ServeHTTP(w, r)
	})
}

// session 查询有效 Cookie 会话
func (auth *adminAuth) session(r *http.Request) *adminSession {
	cookie, err := r.Cookie(adminCookieName)
	if err != nil {
		return nil
	}
	auth.mu.Lock()
	defer auth.mu.Unlock()
	session := auth.sessions[cookie.Value]
	if session != nil && !time.Now().Before(session.expires) {
		session.cancel()
		delete(auth.sessions, cookie.Value)
		return nil
	}
	return session
}

// requireSession 在管理请求期间同时监听退出登录和会话到期
func (auth *adminAuth) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.config.AdminAuthEnabled {
			next.ServeHTTP(w, r)
			return
		}
		session := auth.session(r)
		if session == nil {
			writeAdminError(w, http.StatusUnauthorized, "admin_login_required", "Sign in to the console")
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		stop := context.AfterFunc(session.ctx, cancel)
		defer stop()
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// status 返回登录页面需要的会话状态
func (auth *adminAuth) status(w http.ResponseWriter, r *http.Request) {
	loggedIn := auth.config.AdminAuthEnabled && auth.session(r) != nil
	username := ""
	if loggedIn {
		username = auth.config.AdminUsername
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": auth.config.AdminAuthEnabled, "authenticated": loggedIn || !auth.config.AdminAuthEnabled, "username": username,
	})
}

// login 验证管理员凭据并签发限时会话
func (auth *adminAuth) login(w http.ResponseWriter, r *http.Request) {
	if !auth.config.AdminAuthEnabled {
		auth.status(w, r)
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := decodeJSON(r, &input); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "Invalid login request")
		return
	}
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	now := time.Now()
	auth.mu.Lock()
	defer auth.mu.Unlock()
	for address, attempt := range auth.attempts {
		if !now.Before(attempt.until) {
			delete(auth.attempts, address)
		}
	}
	if auth.attempts[peer].count >= 5 || len(auth.attempts) >= 1024 {
		w.Header().Set("Retry-After", "60")
		writeAdminError(w, http.StatusTooManyRequests, "login_rate_limited", "Too many attempts; try again in a minute")
		return
	}
	userOK := subtle.ConstantTimeCompare([]byte(strings.TrimSpace(input.Username)), []byte(auth.config.AdminUsername))
	passwordOK := subtle.ConstantTimeCompare([]byte(input.Password), []byte(auth.config.AdminPassword))
	if userOK&passwordOK != 1 || auth.config.AdminPassword == "" {
		attempt := auth.attempts[peer]
		if attempt.count == 0 {
			attempt.until = now.Add(time.Minute)
		}
		attempt.count++
		auth.attempts[peer] = attempt
		writeAdminError(w, http.StatusUnauthorized, "invalid_credentials", "Incorrect username or password")
		return
	}
	delete(auth.attempts, peer)
	if old, err := r.Cookie(adminCookieName); err == nil {
		auth.removeSession(old.Value)
	}
	oldest := ""
	for token, session := range auth.sessions {
		if !now.Before(session.expires) {
			auth.removeSession(token)
		} else if oldest == "" || session.expires.Before(auth.sessions[oldest].expires) {
			oldest = token
		}
	}
	if len(auth.sessions) >= 128 {
		auth.removeSession(oldest)
	}
	token := rand.Text()
	expires := now.Add(adminSessionLifetime)
	ctx, cancel := context.WithDeadline(context.Background(), expires)
	auth.sessions[token] = &adminSession{ctx: ctx, cancel: cancel, expires: expires}
	auth.setCookie(w, r, token, int(adminSessionLifetime.Seconds()))
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "authenticated": true, "username": auth.config.AdminUsername})
}

// removeSession 在持锁期间撤销会话
func (auth *adminAuth) removeSession(token string) {
	if session := auth.sessions[token]; session != nil {
		session.cancel()
		delete(auth.sessions, token)
	}
}

// logout 撤销当前浏览器的管理会话
func (auth *adminAuth) logout(w http.ResponseWriter, r *http.Request) {
	auth.mu.Lock()
	if cookie, err := r.Cookie(adminCookieName); err == nil {
		auth.removeSession(cookie.Value)
	}
	auth.mu.Unlock()
	auth.setCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

// setCookie 写入浏览器专用的管理会话 Cookie
func (auth *adminAuth) setCookie(w http.ResponseWriter, r *http.Request, token string, age int) {
	http.SetCookie(w, &http.Cookie{
		Name: adminCookieName, Value: token, Path: "/api", MaxAge: age,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
	})
}
