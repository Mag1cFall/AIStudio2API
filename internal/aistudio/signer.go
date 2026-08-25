package aistudio

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
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
type Signer struct {
	origin string
	now    func() time.Time
}

// NewSigner 创建 AI Studio 官方来源的签名器
func NewSigner() *Signer {
	return &Signer{origin: aiStudioOrigin, now: time.Now}
}

// NewSignerForOrigin 创建指定来源的签名器
func NewSignerForOrigin(origin string) (*Signer, error) {
	normalized, err := normalizeOrigin(origin)
	if err != nil {
		return nil, err
	}
	return &Signer{origin: normalized, now: time.Now}, nil
}

// Sign 使用当前时间生成授权头
func (s *Signer) Sign(state StorageState) (string, error) {
	return s.Authorization(state)
}

// Authorization 使用当前时间生成授权头
func (s *Signer) Authorization(state StorageState) (string, error) {
	if s == nil || s.now == nil {
		return "", fmt.Errorf("签名器未初始化")
	}
	return s.AuthorizationAt(state, s.now())
}

// AuthorizationAt 使用指定时间生成授权头
func (s *Signer) AuthorizationAt(state StorageState, now time.Time) (string, error) {
	if s == nil || s.origin == "" {
		return "", fmt.Errorf("签名器未初始化")
	}
	return SignAuthorization(state.Cookies, s.origin, now.Unix())
}

// SignAuthorization 为指定来源和时间生成三段授权头
func SignAuthorization(cookies []StateCookie, origin string, timestamp int64) (string, error) {
	normalized, err := normalizeOrigin(origin)
	if err != nil {
		return "", err
	}
	state := StorageState{Cookies: cookies}
	now := time.Unix(timestamp, 0)
	parts := make([]string, 0, len(signatureCookies))
	for _, item := range signatureCookies {
		value, ok := state.CookieValue(item.name, normalized+"/", now)
		if !ok {
			return "", fmt.Errorf("storage state 缺少有效 Cookie: %s", item.name)
		}
		source := fmt.Sprintf("%d %s %s", timestamp, value, normalized)
		digest := sha1.Sum([]byte(source))
		parts = append(parts, fmt.Sprintf("%s %d_%s", item.label, timestamp, hex.EncodeToString(digest[:])))
	}
	return strings.Join(parts, " "), nil
}

func normalizeOrigin(origin string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return "", fmt.Errorf("签名来源必须是 HTTPS origin")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("签名来源必须是 HTTPS origin")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}
