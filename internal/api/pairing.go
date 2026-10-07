package api

import (
	"crypto/rand"
	"crypto/sha256"
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

// AccountStateInput 是远程配对上传的账户认证状态与 Camoufox 指纹
type AccountStateInput struct {
	Label        string                `json:"label"`
	Locale       string                `json:"locale"`
	Timezone     string                `json:"timezone"`
	StorageState aistudio.StorageState `json:"storage_state"`
	Fingerprint  json.RawMessage       `json:"fingerprint,omitempty"`
}

// pairingTokens 保存当前唯一有效配对令牌的摘要
type pairingTokens struct {
	mu      sync.Mutex
	digest  [sha256.Size]byte
	expires time.Time
}

// issue 生成新令牌并使旧令牌失效
func (tokens *pairingTokens) issue(now time.Time) PairingToken {
	token := rand.Text()
	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	tokens.digest = sha256.Sum256([]byte(token))
	tokens.expires = now.Add(pairingTokenLifetime)
	return PairingToken{Token: token, ExpiresAt: tokens.expires}
}

// valid 判断令牌是否为当前未过期的配对令牌
func (tokens *pairingTokens) valid(token string, now time.Time) bool {
	digest := sha256.Sum256([]byte(token))
	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	return now.Before(tokens.expires) && subtle.ConstantTimeCompare(digest[:], tokens.digest[:]) == 1
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
	account, err := s.config.Admin.ImportAccountState(r.Context(), input)
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]AdminAccount{"account": account})
}
