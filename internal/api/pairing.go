package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// pairingTokenLifetime 是远程配对令牌的有效期
const pairingTokenLifetime = 10 * time.Minute

// PairingToken 是远程配对令牌与到期时间
type PairingToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AccountStateInput 是远程配对上传或管理端 Cookie 导入的账户认证状态与 Camoufox 指纹
type AccountStateInput struct {
	Label        string                `json:"label"`
	Proxy        string                `json:"-"`
	Locale       string                `json:"locale"`
	Timezone     string                `json:"timezone"`
	AuthUser     int                   `json:"auth_user"`
	StorageState aistudio.StorageState `json:"storage_state"`
	Fingerprint  json.RawMessage       `json:"fingerprint,omitempty"`
	// RenewalToken 是配对上传为该账户签发的续签凭据，只由服务端设置
	RenewalToken string `json:"-"`
}

// PairingAccountResult 是配对上传的结果：导入的账户与原电脑推送新会话时使用的该账户续签凭据
type PairingAccountResult struct {
	Account      AdminAccount `json:"account"`
	RenewalToken string       `json:"renewal_token"`
}

// PairingRenewalInput 是原电脑为配对账户推送的新会话 Cookie
type PairingRenewalInput struct {
	Cookies []aistudio.StateCookie `json:"cookies"`
}

// pairingTokens 保存当前唯一有效的配对令牌
type pairingTokens struct {
	mu      sync.Mutex
	current PairingToken
}

// issue 生成新令牌并使旧令牌失效
func (tokens *pairingTokens) issue(now time.Time) PairingToken {
	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	tokens.current = PairingToken{Token: rand.Text(), ExpiresAt: now.Add(pairingTokenLifetime)}
	return tokens.current
}

// active 返回当前令牌及其是否仍在有效期内
func (tokens *pairingTokens) active(now time.Time) (PairingToken, bool) {
	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	return tokens.current, now.Before(tokens.current.ExpiresAt)
}

// valid 判断令牌是否为当前未过期的配对令牌
func (tokens *pairingTokens) valid(token string, now time.Time) bool {
	current, ok := tokens.active(now)
	return ok && subtle.ConstantTimeCompare([]byte(token), []byte(current.Token)) == 1
}

// handleActivePairing 返回仍在有效期内的远程配对令牌，没有时返回 404
func (s *server) handleActivePairing(w http.ResponseWriter, _ *http.Request) {
	token, ok := s.pairing.active(time.Now())
	if !ok {
		writeAdminError(w, http.StatusNotFound, "pairing_token_missing", "No active pairing token")
		return
	}
	writeJSON(w, http.StatusOK, token)
}

// handleCreatePairing 生成远程配对令牌
func (s *server) handleCreatePairing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusCreated, s.pairing.issue(time.Now()))
}

// handlePairingAccount 凭配对令牌导入一个账户，不经过管理会话
func (s *server) handlePairingAccount(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !s.pairing.valid(strings.TrimSpace(token), time.Now()) {
		writeAdminError(w, http.StatusUnauthorized, "pairing_token_invalid", "Pairing token is invalid or expired")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var input AccountStateInput
	if err := decodeJSON(r, &input); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "Invalid account state")
		return
	}
	input.RenewalToken = rand.Text()
	account, err := s.config.Admin.ImportAccountState(r.Context(), input)
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, PairingAccountResult{Account: account, RenewalToken: input.RenewalToken})
}

// handlePairingRenewal 凭配对上传时签发的续签凭据替换该账户的会话 Cookie，不经过管理会话
func (s *server) handlePairingRenewal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var input PairingRenewalInput
	if err := decodeJSON(r, &input); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "Invalid cookies")
		return
	}
	account, err := s.config.Admin.RenewAccountCookies(r.Context(), r.PathValue("account"), strings.TrimSpace(token), input.Cookies)
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]AdminAccount{"account": account})
}
