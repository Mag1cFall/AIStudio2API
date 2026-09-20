package aistudio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// LoginMethodIsolatedBrowser indicates isolated browser login
	LoginMethodIsolatedBrowser = "isolated_browser"
)

// StateCookie represents a cookie in Playwright storage state
type StateCookie struct {
	Name         string  `json:"name"`
	Value        string  `json:"value"`
	Domain       string  `json:"domain"`
	Path         string  `json:"path"`
	Expires      float64 `json:"expires"`
	HTTPOnly     bool    `json:"httpOnly"`
	Secure       bool    `json:"secure"`
	SameSite     string  `json:"sameSite"`
	PartitionKey string  `json:"partitionKey,omitempty"`
}

// StorageItem represents a browser local storage item
type StorageItem struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// StorageOrigin represents origin data in Playwright storage state
type StorageOrigin struct {
	Origin       string        `json:"origin"`
	LocalStorage []StorageItem `json:"localStorage"`
}

// StorageState represents Playwright storage state that can be written back as-is
type StorageState struct {
	Cookies []StateCookie   `json:"cookies"`
	Origins []StorageOrigin `json:"origins"`
	extra   map[string]json.RawMessage
}

// AuthSource records the source of authentication state
type AuthSource struct {
	Browser string `json:"browser"`
	Profile string `json:"profile,omitempty"`
	Email   string `json:"email,omitempty"`
}

// ChromeOAuthMaterial stores Chrome DBSC renewal material
type ChromeOAuthMaterial struct {
	GaiaID            string `json:"gaia_id"`
	RefreshToken      string `json:"refresh_token"`
	WrappedBindingKey []byte `json:"wrapped_binding_key"`
}

// AuthExtension stores aistudio2api auth extensions
type AuthExtension struct {
	Source AuthSource           `json:"source"`
	OAuth  *ChromeOAuthMaterial `json:"oauth,omitempty"`
}

const authExtensionKey = "aistudio2api"

// SetAuthExtension writes an aistudio2api auth extension
func (s *StorageState) SetAuthExtension(extension AuthExtension) error {
	if s == nil {
		return fmt.Errorf("storage state is empty")
	}
	raw, err := json.Marshal(extension)
	if err != nil {
		return fmt.Errorf("encode auth extension: %w", err)
	}
	if s.extra == nil {
		s.extra = make(map[string]json.RawMessage)
	}
	s.extra[authExtensionKey] = raw
	return nil
}

// AuthExtension returns the aistudio2api auth extension
func (s StorageState) AuthExtension() (AuthExtension, bool, error) {
	raw, exists := s.extra[authExtensionKey]
	if !exists {
		return AuthExtension{}, false, nil
	}
	var extension AuthExtension
	if err := json.Unmarshal(raw, &extension); err != nil {
		return AuthExtension{}, true, fmt.Errorf("parse auth extension: %w", err)
	}
	return extension, true, nil
}

// LoginMethod describes a publishable account login method
type LoginMethod struct {
	ID          string `json:"id"`
	Interactive bool   `json:"interactive"`
}

// IsolatedLoginRequest describes the stable environment required for isolated browser login
type IsolatedLoginRequest struct {
	AccountID string
	Directory string
	Proxy     string
	Locale    string
	Timezone  string
}

// IsolatedLoginResult returns authentication state exported from an isolated browser
type IsolatedLoginResult struct {
	StorageState StorageState
	Email        string
	VerifiedAt   time.Time
}

// LoginVerification represents verification result of login state by an isolated runtime
type LoginVerification struct {
	Authenticated bool      `json:"authenticated"`
	VerifiedAt    time.Time `json:"verified_at"`
	Reason        string    `json:"reason,omitempty"`
}

// IsolatedLoginDriver defines the contract for isolated browser login and verification
type IsolatedLoginDriver interface {
	Login(context.Context, IsolatedLoginRequest) (IsolatedLoginResult, error)
	Verify(context.Context, IsolatedLoginRequest, StorageState) (LoginVerification, error)
}

// SupportedLoginMethods returns the login methods currently available for publishing
func SupportedLoginMethods() []LoginMethod {
	return []LoginMethod{{ID: LoginMethodIsolatedBrowser, Interactive: true}}
}

// LoadStorageState reads and validates Playwright storage state
func LoadStorageState(filePath string) (StorageState, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return StorageState{}, fmt.Errorf("read storage state: %w", err)
	}
	var state StorageState
	if err := json.Unmarshal(data, &state); err != nil {
		return StorageState{}, fmt.Errorf("parse storage state: %w", err)
	}
	if err := state.Validate(); err != nil {
		return StorageState{}, err
	}
	return state, nil
}

// WriteStorageState atomically writes back Playwright storage state
func WriteStorageState(filePath string, state StorageState) error {
	if err := state.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode storage state: %w", err)
	}
	data = append(data, '\n')
	return atomicWriteFile(filePath, data, 0o600)
}

// Validate validates browser fields of storage state
func (s StorageState) Validate() error {
	for index, cookie := range s.Cookies {
		if strings.TrimSpace(cookie.Name) == "" || strings.TrimSpace(cookie.Domain) == "" {
			return fmt.Errorf("storage state cookie %d missing name or domain", index)
		}
		if cookie.Path == "" || cookie.Path[0] != '/' {
			return fmt.Errorf("storage state cookie %s has invalid path", cookie.Name)
		}
		switch cookie.SameSite {
		case "", "Lax", "Strict", "None":
		default:
			return fmt.Errorf("storage state cookie %s has invalid SameSite", cookie.Name)
		}
	}
	for index, origin := range s.Origins {
		parsed, err := url.Parse(origin.Origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("storage state origin %d is invalid", index)
		}
	}
	return nil
}

// CookieHeader constructs currently valid Cookie request header for the target URL
func (s StorageState) CookieHeader(targetURL string, now time.Time) (string, error) {
	target, err := url.Parse(targetURL)
	if err != nil || target.Scheme == "" || target.Hostname() == "" {
		return "", fmt.Errorf("target URL is invalid")
	}
	type candidate struct {
		cookie StateCookie
		index  int
	}
	candidates := make([]candidate, 0, len(s.Cookies))
	for index, cookie := range s.Cookies {
		if cookie.Value == "" || cookie.Expires >= 0 && cookie.Expires <= float64(now.UnixNano())/1e9 {
			continue
		}
		if cookie.Secure && !strings.EqualFold(target.Scheme, "https") {
			continue
		}
		if !cookieDomainMatches(cookie.Domain, target.Hostname()) || !cookiePathMatches(cookie.Path, target.EscapedPath()) {
			continue
		}
		candidates = append(candidates, candidate{cookie: cookie, index: index})
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		if len(candidates[left].cookie.Path) == len(candidates[right].cookie.Path) {
			return candidates[left].index < candidates[right].index
		}
		return len(candidates[left].cookie.Path) > len(candidates[right].cookie.Path)
	})
	parts := make([]string, 0, len(candidates))
	for _, item := range candidates {
		parts = append(parts, item.cookie.Name+"="+item.cookie.Value)
	}
	return strings.Join(parts, "; "), nil
}

// CookieValue returns the most specific cookie with the given name for the target URL
func (s StorageState) CookieValue(name string, targetURL string, now time.Time) (string, bool) {
	target, err := url.Parse(targetURL)
	if err != nil {
		return "", false
	}
	selectedPath := -1
	selected := ""
	for _, cookie := range s.Cookies {
		if cookie.Name != name || cookie.Value == "" {
			continue
		}
		if cookie.Expires >= 0 && cookie.Expires <= float64(now.UnixNano())/1e9 {
			continue
		}
		if cookie.Secure && !strings.EqualFold(target.Scheme, "https") {
			continue
		}
		if !cookieDomainMatches(cookie.Domain, target.Hostname()) || !cookiePathMatches(cookie.Path, target.EscapedPath()) {
			continue
		}
		if len(cookie.Path) > selectedPath {
			selected = cookie.Value
			selectedPath = len(cookie.Path)
		}
	}
	return selected, selectedPath >= 0
}

// MergeSetCookieHeaders merges response rotated cookies into storage state
func (s *StorageState) MergeSetCookieHeaders(headers []string, sourceURL string, now time.Time) error {
	source, err := url.Parse(sourceURL)
	if err != nil || source.Scheme == "" || source.Hostname() == "" {
		return fmt.Errorf("source URL is invalid")
	}
	for _, header := range headers {
		response := http.Response{Header: http.Header{"Set-Cookie": []string{header}}}
		cookies := response.Cookies()
		if len(cookies) != 1 {
			return fmt.Errorf("invalid Set-Cookie format")
		}
		incoming := cookies[0]
		domain := strings.ToLower(incoming.Domain)
		if domain == "" {
			domain = strings.ToLower(source.Hostname())
		}
		cookiePath := incoming.Path
		if cookiePath == "" {
			cookiePath = defaultCookiePath(source.EscapedPath())
		}
		index := -1
		for existingIndex, existing := range s.Cookies {
			if existing.Name == incoming.Name && strings.EqualFold(existing.Domain, domain) && existing.Path == cookiePath && existing.PartitionKey == "" {
				index = existingIndex
				break
			}
		}
		if index >= 0 {
			s.Cookies = append(s.Cookies[:index], s.Cookies[index+1:]...)
		}
		if incoming.MaxAge < 0 || !incoming.Expires.IsZero() && !incoming.Expires.After(now) {
			continue
		}
		expires := float64(-1)
		if incoming.MaxAge > 0 {
			expires = float64(now.Add(time.Duration(incoming.MaxAge)*time.Second).UnixNano()) / 1e9
		} else if !incoming.Expires.IsZero() {
			expires = float64(incoming.Expires.UnixNano()) / 1e9
		}
		s.Cookies = append(s.Cookies, StateCookie{
			Name:     incoming.Name,
			Value:    incoming.Value,
			Domain:   domain,
			Path:     cookiePath,
			Expires:  expires,
			HTTPOnly: incoming.HttpOnly,
			Secure:   incoming.Secure,
			SameSite: sameSiteText(incoming.SameSite),
		})
	}
	return nil
}

// MarshalJSON preserves extension root fields of storage state
func (s StorageState) MarshalJSON() ([]byte, error) {
	value := make(map[string]json.RawMessage, len(s.extra)+2)
	for key, raw := range s.extra {
		value[key] = append(json.RawMessage(nil), raw...)
	}
	cookieValues := s.Cookies
	if cookieValues == nil {
		cookieValues = []StateCookie{}
	}
	cookies, err := json.Marshal(cookieValues)
	if err != nil {
		return nil, err
	}
	originValues := s.Origins
	if originValues == nil {
		originValues = []StorageOrigin{}
	}
	origins, err := json.Marshal(originValues)
	if err != nil {
		return nil, err
	}
	value["cookies"] = cookies
	value["origins"] = origins
	return json.Marshal(value)
}

// UnmarshalJSON parses storage state and preserves extension root fields
func (s *StorageState) UnmarshalJSON(data []byte) error {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var cookies []StateCookie
	if raw, ok := value["cookies"]; ok {
		if err := json.Unmarshal(raw, &cookies); err != nil {
			return err
		}
	}
	var origins []StorageOrigin
	if raw, ok := value["origins"]; ok {
		if err := json.Unmarshal(raw, &origins); err != nil {
			return err
		}
	}
	delete(value, "cookies")
	delete(value, "origins")
	s.Cookies = cookies
	s.Origins = origins
	s.extra = value
	return nil
}

func cookieDomainMatches(cookieDomain string, hostname string) bool {
	domain := strings.TrimPrefix(strings.ToLower(cookieDomain), ".")
	host := strings.ToLower(hostname)
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func cookiePathMatches(cookiePath string, requestPath string) bool {
	if requestPath == "" {
		requestPath = "/"
	}
	if cookiePath == "/" {
		return true
	}
	if requestPath == cookiePath {
		return true
	}
	return strings.HasPrefix(requestPath, cookiePath) && (strings.HasSuffix(cookiePath, "/") || len(requestPath) > len(cookiePath) && requestPath[len(cookiePath)] == '/')
}

func defaultCookiePath(requestPath string) string {
	if requestPath == "" || requestPath[0] != '/' || requestPath == "/" {
		return "/"
	}
	directory := path.Dir(requestPath)
	if directory == "." {
		return "/"
	}
	return directory
}

func sameSiteText(value http.SameSite) string {
	switch value {
	case http.SameSiteStrictMode:
		return "Strict"
	case http.SameSiteNoneMode:
		return "None"
	default:
		return "Lax"
	}
}

func atomicWriteFile(filePath string, data []byte, mode os.FileMode) error {
	target, err := filepath.Abs(filePath)
	if err != nil {
		return fmt.Errorf("resolve file path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create file directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("set file permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	return nil
}
