package chromeauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

const (
	aiStudioChatURL    = "https://aistudio.google.com/prompts/new_chat"
	maxCookieAuthUsers = 10
)

// ErrSignedOut 表示 Cookie 打开 AI Studio 时被转到登录页
var ErrSignedOut = errors.New("Cookie 未登录 AI Studio 或已失效")

// accountEmailPattern 匹配 AI Studio 页面全局数据中当前登录账户的邮箱
var accountEmailPattern = regexp.MustCompile(`"oPEP7c":("(?:[^"\\]|\\.)*")`)

// Verification 保存 AI Studio 登录页与模型目录验收结果
type Verification struct {
	Email      string
	ModelCount int
}

// CookieAccount 描述同一份 Cookie 中单个 Google 会话槽位 (u/0, u/1, ...) 的账户
type CookieAccount struct {
	AuthUser   int
	Email      string
	ModelCount int
}

// Verify 验证默认账号 (u/0) 可访问 WAA 页面并读取实时模型目录
func Verify(ctx context.Context, state *aistudio.StorageState, proxy string) (Verification, error) {
	return VerifyAuthUser(ctx, state, proxy, 0)
}

// VerifyAuthUser 验证指定会话槽位 (u/0, u/1, ...) 的账号可访问 WAA 页面并读取实时模型目录
func VerifyAuthUser(ctx context.Context, state *aistudio.StorageState, proxy string, authUser int) (Verification, error) {
	if authUser < 0 || authUser >= maxCookieAuthUsers {
		return Verification{}, fmt.Errorf("auth_user 无效: %d", authUser)
	}
	client, headers, err := newVerifyClient(ctx, state, proxy)
	if err != nil {
		return Verification{}, err
	}
	defer client.CloseIdleConnections()
	email, err := verifyChatPage(ctx, client, state, headers.Get("User-Agent"), authUser)
	if err != nil {
		return Verification{}, err
	}
	models, err := verifyModels(ctx, client, state, headers, authUser)
	if err != nil {
		return Verification{}, err
	}
	return Verification{Email: email, ModelCount: models}, nil
}

// VerifyNextAuthUser 按 u/0..u/9 依次查找首个不在 existing 中的登录账户并验证其模型目录
func VerifyNextAuthUser(
	ctx context.Context,
	state *aistudio.StorageState,
	proxy string,
	existing map[string]struct{},
) (Verification, int, string, error) {
	client, headers, err := newVerifyClient(ctx, state, proxy)
	if err != nil {
		return Verification{}, 0, "", err
	}
	defer client.CloseIdleConnections()
	seen := make(map[string]struct{}, maxCookieAuthUsers)
	firstExisting := ""
	for authUser := 0; authUser < maxCookieAuthUsers; authUser++ {
		candidate := cloneStorageState(*state)
		email, err := verifyChatPage(ctx, client, &candidate, headers.Get("User-Agent"), authUser)
		if err != nil {
			if authUser == 0 {
				return Verification{}, 0, "", err
			}
			break
		}
		normalized := strings.ToLower(strings.TrimSpace(email))
		if _, duplicated := seen[normalized]; duplicated {
			break
		}
		seen[normalized] = struct{}{}
		if _, exists := existing[normalized]; exists {
			if firstExisting == "" {
				firstExisting = normalized
			}
			continue
		}
		models, err := verifyModels(ctx, client, &candidate, headers, authUser)
		if err != nil {
			return Verification{}, authUser, "", err
		}
		*state = candidate
		return Verification{Email: email, ModelCount: models}, authUser, "", nil
	}
	if firstExisting != "" {
		return Verification{Email: firstExisting}, 0, firstExisting, nil
	}
	return Verification{}, 0, "", ErrSignedOut
}

// FindCookieAccount 在同一份 Cookie 的 u/0..u/9 会话槽位中定位目标邮箱对应的槽位并验证其模型目录
func FindCookieAccount(
	ctx context.Context,
	state *aistudio.StorageState,
	proxy string,
	targetEmail string,
) (Verification, int, error) {
	targetEmail = strings.ToLower(strings.TrimSpace(targetEmail))
	client, headers, err := newVerifyClient(ctx, state, proxy)
	if err != nil {
		return Verification{}, 0, err
	}
	defer client.CloseIdleConnections()
	seen := make(map[string]struct{}, maxCookieAuthUsers)
	for authUser := 0; authUser < maxCookieAuthUsers; authUser++ {
		candidate := cloneStorageState(*state)
		email, err := verifyChatPage(ctx, client, &candidate, headers.Get("User-Agent"), authUser)
		if err != nil {
			if authUser == 0 {
				return Verification{}, 0, err
			}
			break
		}
		normalized := strings.ToLower(strings.TrimSpace(email))
		if _, duplicated := seen[normalized]; duplicated {
			break
		}
		seen[normalized] = struct{}{}
		if normalized != targetEmail {
			continue
		}
		models, err := verifyModels(ctx, client, &candidate, headers, authUser)
		if err != nil {
			return Verification{}, authUser, err
		}
		*state = candidate
		return Verification{Email: email, ModelCount: models}, authUser, nil
	}
	return Verification{}, 0, fmt.Errorf("Cookie 中未找到账户: %s", targetEmail)
}

// DiscoverCookieAccounts 列出同一份 Cookie 中登录 AI Studio 的全部会话槽位账户 (u/0, u/1, ...)
func DiscoverCookieAccounts(ctx context.Context, state *aistudio.StorageState, proxy string) ([]CookieAccount, error) {
	client, headers, err := newVerifyClient(ctx, state, proxy)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	seen := make(map[string]struct{}, maxCookieAuthUsers)
	accounts := make([]CookieAccount, 0, 2)
	for authUser := 0; authUser < maxCookieAuthUsers; authUser++ {
		candidate := cloneStorageState(*state)
		email, err := verifyChatPage(ctx, client, &candidate, headers.Get("User-Agent"), authUser)
		if err != nil {
			if authUser == 0 {
				return nil, err
			}
			break
		}
		normalized := strings.ToLower(strings.TrimSpace(email))
		if _, duplicated := seen[normalized]; duplicated {
			break
		}
		seen[normalized] = struct{}{}
		models, err := verifyModels(ctx, client, &candidate, headers, authUser)
		if err != nil {
			if authUser == 0 {
				return nil, err
			}
			break
		}
		accounts = append(accounts, CookieAccount{
			AuthUser:   authUser,
			Email:      normalized,
			ModelCount: models,
		})
	}
	return accounts, nil
}

func newVerifyClient(ctx context.Context, state *aistudio.StorageState, proxy string) (*http.Client, http.Header, error) {
	if state == nil {
		return nil, nil, fmt.Errorf("storage state 为空")
	}
	if _, err := aistudio.NewSigner().Sign(*state); err != nil {
		return nil, nil, err
	}
	client, err := aistudio.NewProxyHTTPClient(proxy)
	if err != nil {
		return nil, nil, err
	}
	headers, err := aistudio.DiscoverPublicHeaders(ctx, client)
	if err != nil {
		client.CloseIdleConnections()
		return nil, nil, err
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client, headers, nil
}

func cloneStorageState(state aistudio.StorageState) aistudio.StorageState {
	cloned := state
	cloned.Cookies = slices.Clone(state.Cookies)
	cloned.Origins = slices.Clone(state.Origins)
	return cloned
}

func chatURLForAuthUser(authUser int) string {
	if authUser > 0 {
		return fmt.Sprintf("https://aistudio.google.com/u/%d/prompts/new_chat", authUser)
	}
	return aiStudioChatURL
}

// verifyChatPage 以账户 Cookie 打开对话页并返回页面中登录账户的邮箱
func verifyChatPage(ctx context.Context, client *http.Client, state *aistudio.StorageState, userAgent string, authUser int) (string, error) {
	targetURL := chatURLForAuthUser(authUser)
	cookie, err := state.CookieHeader(targetURL, time.Now())
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	request.Header.Set("Cookie", cookie)
	request.Header.Set("User-Agent", userAgent)
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("访问 AI Studio 登录页: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", fmt.Errorf("读取 AI Studio 登录页: %w", err)
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return "", ErrSignedOut
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("AI Studio 登录页返回 HTTP %d", response.StatusCode)
	}
	var email string
	if match := accountEmailPattern.FindSubmatch(body); match == nil || json.Unmarshal(match[1], &email) != nil || !strings.Contains(email, "@") {
		return "", fmt.Errorf("AI Studio 页面缺少登录账户")
	}
	return email, mergeResponseCookies(state, response, targetURL)
}

func verifyModels(ctx context.Context, client *http.Client, state *aistudio.StorageState, headers http.Header, authUser int) (int, error) {
	url := aistudio.MakerSuiteRPCBase + "ListModels"
	cookie, err := state.CookieHeader(url, time.Now())
	if err != nil {
		return 0, err
	}
	authorization, err := aistudio.NewSigner().Authorization(*state)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte("[]")))
	if err != nil {
		return 0, err
	}
	request.Header = headers.Clone()
	request.Header.Set("Accept", "*/*")
	request.Header.Set("Authorization", authorization)
	request.Header.Set("Content-Type", aistudio.JSONProtobufContentType)
	request.Header.Set("Cookie", cookie)
	request.Header.Set("Origin", "https://aistudio.google.com")
	request.Header.Set("Referer", "https://aistudio.google.com/")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Site", "same-site")
	request.Header.Set("X-Goog-Authuser", strconv.Itoa(authUser))
	response, err := client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("读取 AI Studio 模型目录: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, response.Body)
		return 0, fmt.Errorf("AI Studio ListModels 返回 HTTP %d", response.StatusCode)
	}
	if !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), aistudio.JSONProtobufContentType) {
		return 0, fmt.Errorf("AI Studio ListModels 返回未识别的 Content-Type %q", response.Header.Get("Content-Type"))
	}
	models, err := aistudio.ParseModels(response.Body)
	if err != nil {
		return 0, err
	}
	if len(models) == 0 {
		return 0, fmt.Errorf("AI Studio ListModels 返回空目录")
	}
	if err := mergeResponseCookies(state, response, url); err != nil {
		return 0, err
	}
	return len(models), nil
}

func mergeResponseCookies(state *aistudio.StorageState, response *http.Response, sourceURL string) error {
	setCookies := response.Header.Values("Set-Cookie")
	if len(setCookies) == 0 {
		return nil
	}
	return state.MergeSetCookieHeaders(setCookies, sourceURL, time.Now())
}
