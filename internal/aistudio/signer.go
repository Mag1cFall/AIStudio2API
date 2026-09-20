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

// Signer generates 3-part SAPISID authorization headers for AI Studio requests
type Signer struct {
	origin string
	now    func() time.Time
}

// NewSigner creates a signer with the official AI Studio origin
func NewSigner() *Signer {
	return &Signer{origin: aiStudioOrigin, now: time.Now}
}

// NewSignerForOrigin creates a signer for the specified origin
func NewSignerForOrigin(origin string) (*Signer, error) {
	normalized, err := normalizeOrigin(origin)
	if err != nil {
		return nil, err
	}
	return &Signer{origin: normalized, now: time.Now}, nil
}

// Sign generates an authorization header using current time
func (s *Signer) Sign(state StorageState) (string, error) {
	return s.Authorization(state)
}

// Authorization generates an authorization header using current time
func (s *Signer) Authorization(state StorageState) (string, error) {
	if s == nil || s.now == nil {
		return "", fmt.Errorf("signer is not initialized")
	}
	return s.AuthorizationAt(state, s.now())
}

// AuthorizationAt generates an authorization header at the specified time
func (s *Signer) AuthorizationAt(state StorageState, now time.Time) (string, error) {
	if s == nil || s.origin == "" {
		return "", fmt.Errorf("signer is not initialized")
	}
	return SignAuthorization(state.Cookies, s.origin, now.Unix())
}

// SignAuthorization generates a 3-part authorization header for the specified origin and time
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
			return "", fmt.Errorf("storage state missing valid cookie: %s", item.name)
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
		return "", fmt.Errorf("signing origin must be an HTTPS origin")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("signing origin must be an HTTPS origin")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}
