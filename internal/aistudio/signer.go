package aistudio

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

const aiStudioOrigin = "https://aistudio.google.com"

var signatureCookies = [...]struct {
	label string
	name  string
}{
	{label: "SAPISIDHASH", name: "SAPISID"},
	{label: "SAPISID1PHASH", name: "__Secure-1PAPISID"},
	{label: "SAPISID3PHASH", name: "__Secure-3PAPISID"},
}

// Signer 为 AI Studio 请求生成三段 SAPISID 授权头
type Signer struct{}

// NewSigner 创建 AI Studio 官方来源的签名器
func NewSigner() *Signer {
	return &Signer{}
}

// Authorization 使用当前时间生成授权头
func (s *Signer) Authorization(state StorageState) (string, error) {
	return s.AuthorizationAt(state, time.Now())
}

// AuthorizationAt 使用指定时间生成授权头
func (s *Signer) AuthorizationAt(state StorageState, now time.Time) (string, error) {
	timestamp := now.Unix()
	now = time.Unix(timestamp, 0)
	parts := make([]string, 0, len(signatureCookies))
	for _, item := range signatureCookies {
		value, ok := state.CookieValue(item.name, aiStudioOrigin+"/", now)
		if !ok {
			return "", fmt.Errorf("%w: storage state 缺少有效 Cookie: %s", camoufoxnative.ErrAuthenticationRequired, item.name)
		}
		source := fmt.Sprintf("%d %s %s", timestamp, value, aiStudioOrigin)
		digest := sha1.Sum([]byte(source))
		parts = append(parts, fmt.Sprintf("%s %d_%s", item.label, timestamp, hex.EncodeToString(digest[:])))
	}
	return strings.Join(parts, " "), nil
}
