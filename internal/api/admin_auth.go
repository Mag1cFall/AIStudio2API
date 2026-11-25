package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const adminCookieName = "aistudio_admin"
const adminSessionLifetime = 12 * time.Hour
const adminRememberLifetime = 30 * 24 * time.Hour

// adminSession 保存管理会话及其关联请求的取消信号，remembered 为登录时勾选记住我、写入会话文件的会话
type adminSession struct {
	ctx        context.Context
	cancel     context.CancelFunc
	expires    time.Time
	remembered bool
}

// rememberedSessions 是会话文件内容，credential 随管理员账号或密码变化
type rememberedSessions struct {
	Credential string                 `json:"credential"`
	Sessions   []rememberedSessionRow `json:"sessions"`
}

// rememberedSessionRow 是会话文件中的一条会话
type rememberedSessionRow struct {
	Token   string    `json:"token"`
	Expires time.Time `json:"expires"`
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

// newAdminAuth 创建进程级管理认证，载入会话文件中账号密码未变且未到期的会话
func newAdminAuth(config Config) *adminAuth {
	auth := &adminAuth{config: config, sessions: make(map[string]*adminSession), attempts: make(map[string]loginAttempt)}
	if !config.AdminAuthEnabled {
		return auth
	}
	data, err := os.ReadFile(config.AdminSessionPath)
	if errors.Is(err, fs.ErrNotExist) {
		return auth
	}
	var saved rememberedSessions
	if err == nil {
		err = json.Unmarshal(data, &saved)
	}
	if err != nil {
		slog.Warn("读取管理会话文件失败", "path", config.AdminSessionPath, "error", err)
		return auth
	}
	if saved.Credential != auth.credential() {
		return auth
	}
	now := time.Now()
	for _, row := range saved.Sessions {
		if row.Expires.After(now) {
			auth.addSession(row.Token, row.Expires, true)
		}
	}
	return auth
}

// credential 是管理员账号与密码的摘要，二者变化时已保存的会话失效
func (auth *adminAuth) credential() string {
	sum := sha256.Sum256([]byte(auth.config.AdminUsername + "\x00" + auth.config.AdminPassword))
	return hex.EncodeToString(sum[:])
}

// addSession 在持锁期间登记会话
func (auth *adminAuth) addSession(token string, expires time.Time, remembered bool) {
	ctx, cancel := context.WithDeadline(context.Background(), expires)
	auth.sessions[token] = &adminSession{ctx: ctx, cancel: cancel, expires: expires, remembered: remembered}
}

// saveRemembered 在持锁期间把记住我的会话写入会话文件
func (auth *adminAuth) saveRemembered() error {
	if auth.config.AdminSessionPath == "" {
		return nil
	}
	saved := rememberedSessions{Credential: auth.credential(), Sessions: []rememberedSessionRow{}}
	for token, session := range auth.sessions {
		if session.remembered {
			saved.Sessions = append(saved.Sessions, rememberedSessionRow{Token: token, Expires: session.expires})
		}
	}
	data, _ := json.Marshal(saved)
	if err := os.MkdirAll(filepath.Dir(auth.config.AdminSessionPath), 0o755); err != nil {
		return err
	}
	temporary := auth.config.AdminSessionPath + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, auth.config.AdminSessionPath)
}

// isEnabled 返回当前是否开启了管理登录
func (auth *adminAuth) isEnabled() bool {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	return auth.config.AdminAuthEnabled
}

// ValidateConfigUpdate 校验远程请求不可关闭管理登录，防止关闭后立刻被回环限制拒绝访问
func (auth *adminAuth) ValidateConfigUpdate(r *http.Request, nextEnabled bool) error {
	if auth == nil || nextEnabled || !auth.isEnabled() {
		return nil
	}
	if !isLoopbackRequest(r) {
		return errors.New("只能在本机访问时关闭管理登录")
	}
	return nil
}

// ApplyConfig 热更新管理登录开关与凭据：开启或改密时撤销旧会话并为当前请求签发新会话 Cookie，关闭时清除全部会话
func (auth *adminAuth) ApplyConfig(enabled bool, username, password string, w http.ResponseWriter, r *http.Request) {
	if auth == nil {
		return
	}
	auth.mu.Lock()
	defer auth.mu.Unlock()
	if auth.config.AdminAuthEnabled == enabled && (!enabled || (auth.config.AdminUsername == username && auth.config.AdminPassword == password)) {
		return
	}
	currentToken := ""
	remembered := false
	if r != nil {
		if cookie, err := r.Cookie(adminCookieName); err == nil {
			currentToken = cookie.Value
			if current := auth.sessions[currentToken]; current != nil {
				remembered = current.remembered
			}
		}
	}
	for token, session := range auth.sessions {
		// 当前保存请求的上下文绑定在 currentToken 上，该会话在保存请求结束、响应写回之后撤销
		if token == currentToken {
			context.AfterFunc(r.Context(), session.cancel)
		} else {
			session.cancel()
		}
		delete(auth.sessions, token)
	}
	clear(auth.attempts)
	auth.config.AdminAuthEnabled = enabled
	auth.config.AdminUsername = username
	auth.config.AdminPassword = password
	if !enabled {
		if err := auth.saveRemembered(); err != nil {
			slog.Warn("写入管理会话文件失败", "path", auth.config.AdminSessionPath, "error", err)
		}
		if w != nil && r != nil {
			auth.setCookie(w, r, "", -1)
		}
		return
	}
	now := time.Now()
	lifetime := adminSessionLifetime
	if remembered {
		lifetime = adminRememberLifetime
	}
	if w != nil && r != nil {
		token := rand.Text()
		auth.addSession(token, now.Add(lifetime), remembered)
		auth.setCookie(w, r, token, int(lifetime.Seconds()))
	}
	if err := auth.saveRemembered(); err != nil {
		slog.Warn("写入管理会话文件失败", "path", auth.config.AdminSessionPath, "error", err)
	}
}

// handler 统一管理认证、同源检查与会话接口
func (auth *adminAuth) handler(next http.Handler) http.Handler {
	routes := func(root http.Handler) http.Handler {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/auth/session", auth.status)
		mux.HandleFunc("POST /api/auth/login", auth.login)
		mux.HandleFunc("POST /api/auth/logout", auth.logout)
		mux.Handle("/", root)
		return sameOriginMiddleware(mux)
	}
	// loopback 是关闭管理登录时只放行本机来源的链，guarded 是开启时要求有效会话的链；每个请求只按进入时读取的开关走其中一条
	loopback := loopbackMiddleware(routes(next))
	guarded := routes(auth.requireSession(next))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		limit := int64(1 << 20)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/prompts/media":
			limit = openAIFileMaxBytes
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/prompts/"):
			limit = maxPublicBodyBytes
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		if !auth.isEnabled() {
			loopback.ServeHTTP(w, r)
			return
		}
		guarded.ServeHTTP(w, r)
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
		auth.removeSession(cookie.Value)
		return nil
	}
	return session
}

// requireSession 要求有效会话，并在管理请求期间同时监听退出登录和会话到期
func (auth *adminAuth) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	auth.mu.Lock()
	enabled := auth.config.AdminAuthEnabled
	configuredUser := auth.config.AdminUsername
	auth.mu.Unlock()
	loggedIn := enabled && auth.session(r) != nil
	username := ""
	if loggedIn {
		username = configuredUser
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": enabled, "authenticated": loggedIn || !enabled, "username": username,
	})
}

// login 验证管理员凭据并签发限时会话
func (auth *adminAuth) login(w http.ResponseWriter, r *http.Request) {
	if !auth.isEnabled() {
		auth.status(w, r)
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Remember bool   `json:"remember"`
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
	if !auth.config.AdminAuthEnabled {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "authenticated": true, "username": ""})
		return
	}
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
	if userOK&passwordOK != 1 {
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
	lifetime := adminSessionLifetime
	if input.Remember {
		lifetime = adminRememberLifetime
	}
	auth.addSession(token, now.Add(lifetime), input.Remember)
	if input.Remember {
		if err := auth.saveRemembered(); err != nil {
			auth.removeSession(token)
			writeAdminError(w, http.StatusInternalServerError, "session_save_failed", "Failed to save the session: "+err.Error())
			return
		}
	}
	auth.setCookie(w, r, token, int(lifetime.Seconds()))
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "authenticated": true, "username": auth.config.AdminUsername})
}

// removeSession 在持锁期间撤销会话，记住我的会话同时从会话文件删除
func (auth *adminAuth) removeSession(token string) {
	session := auth.sessions[token]
	if session == nil {
		return
	}
	session.cancel()
	delete(auth.sessions, token)
	if session.remembered {
		if err := auth.saveRemembered(); err != nil {
			slog.Warn("写入管理会话文件失败", "path", auth.config.AdminSessionPath, "error", err)
		}
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
