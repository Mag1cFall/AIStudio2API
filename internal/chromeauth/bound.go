package chromeauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// ErrBoundSessionRejected 表示 Google 拒绝刷新保存的 DBSC 会话
var ErrBoundSessionRejected = errors.New("Google DBSC 会话刷新被拒绝")

// boundChallengePattern 匹配 Secure-Session-Challenge 结构化列表中的一项挑战与会话 id
var boundChallengePattern = regexp.MustCompile(`"([^"]+)"(?:;id="([^"]*)")?`)

// RotateBoundCookies 按 Chromium RegistrationFetcher 的刷新流程以设备绑定密钥刷新 google.com 的 DBSC 会话，返回各次响应的 Set-Cookie
func RotateBoundCookies(ctx context.Context, state aistudio.StorageState, extension aistudio.AuthExtension, proxy string) ([]string, error) {
	identity, proxyURL, err := sourceIdentity(extension.Source, proxy)
	if err != nil {
		return nil, err
	}
	client, err := newOAuthClient(proxyURL)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	session := *extension.Bound
	status, header, err := requestBoundRefresh(ctx, client, identity, &state, session, "")
	setCookies := header.Values("Set-Cookie")
	if err != nil || status == http.StatusOK {
		return setCookies, err
	}
	challenge := boundChallenge(header.Get("Secure-Session-Challenge"), session.ID)
	if status != http.StatusForbidden || challenge == "" {
		return nil, boundRefreshStatusError(status)
	}
	key, err := openDeviceBindingKey(extension.OAuth.WrappedBindingKey)
	if err != nil {
		return nil, err
	}
	defer key.Close()
	signed, err := signBoundRefresh(key, challenge)
	if err != nil {
		return nil, err
	}
	status, header, err = requestBoundRefresh(ctx, client, identity, &state, session, signed)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, boundRefreshStatusError(status)
	}
	return append(setCookies, header.Values("Set-Cookie")...), nil
}

// requestBoundRefresh 以 state 中的 Cookie 向刷新地址发出一次 DBSC 刷新请求，并把响应 Cookie 合并回 state
func requestBoundRefresh(ctx context.Context, client tls_client.HttpClient, identity chromeIdentity, state *aistudio.StorageState, session aistudio.BoundSession, signed string) (int, http.Header, error) {
	cookie, err := state.CookieHeader(session.RefreshURL, time.Now())
	if err != nil {
		return 0, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, session.RefreshURL, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("创建 DBSC 刷新请求: %w", err)
	}
	request.Header.Set("Sec-Secure-Session-Id", session.ID)
	if signed != "" {
		request.Header.Set("Secure-Session-Response", signed)
	}
	request.Header.Set("Origin", "https://accounts.google.com")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "no-cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	request.Header.Set("User-Agent", identity.userAgent)
	request.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	request.Header.Set("Accept-Language", identity.acceptLanguage)
	request.Header.Set("Cookie", cookie)
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("DBSC 刷新请求失败: %w", err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		return 0, nil, fmt.Errorf("读取 DBSC 刷新响应: %w", err)
	}
	if err := state.MergeSetCookieHeaders(response.Header.Values("Set-Cookie"), session.RefreshURL, time.Now()); err != nil {
		return 0, nil, err
	}
	return response.StatusCode, response.Header, nil
}

// boundChallenge 返回 Secure-Session-Challenge 中属于 sessionID 或未指明会话的挑战
func boundChallenge(header string, sessionID string) string {
	for _, match := range boundChallengePattern.FindAllStringSubmatch(header, -1) {
		if match[2] == "" || match[2] == sessionID {
			return match[1]
		}
	}
	return ""
}

// signBoundRefresh 按 Chromium CreateKeyRefreshHeaderAndPayload 以设备绑定密钥签发 dbsc+jwt 刷新令牌
func signBoundRefresh(key deviceBindingKey, challenge string) (string, error) {
	header, err := json.Marshal(struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}{"ES256", "dbsc+jwt"})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(struct {
		JWTID string `json:"jti"`
	}{challenge})
	if err != nil {
		return "", err
	}
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	signature, err := key.SignSHA256([]byte(input))
	if err != nil {
		return "", err
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// boundRefreshStatusError 按 Chromium 把 DBSC 刷新的 3xx 与 4xx 归为会话被拒绝，429 与 5xx 保留为普通错误
func boundRefreshStatusError(status int) error {
	err := fmt.Errorf("RotateBoundCookies 返回 HTTP %d", status)
	if status >= 300 && status < 500 && status != http.StatusTooManyRequests {
		return errors.Join(ErrBoundSessionRejected, err)
	}
	return err
}
