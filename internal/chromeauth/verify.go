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

const aiStudioChatURL = "https://aistudio.google.com/prompts/new_chat"

// maxCookieAuthUsers 是识别同一份 Cookie 中会话的序号上限
const maxCookieAuthUsers = 10

// ErrSignedOut 表示 Cookie 打开 AI Studio 时被转到登录页
var ErrSignedOut = errors.New("Cookie 未登录 AI Studio 或已失效")

// accountEmailPattern 匹配 AI Studio 页面全局数据中当前登录账户的邮箱
var accountEmailPattern = regexp.MustCompile(`"oPEP7c":("(?:[^"\\]|\\.)*")`)

// Verification 保存 AI Studio 登录页与模型目录验收结果
type Verification struct {
	Email      string
	ModelCount int
}

// CookieAccount 是同一份 Cookie 中一个 Google 会话序号上登录 AI Studio 的账户
type CookieAccount struct {
	AuthUser int
	Email    string
}

// Verify 验证 Cookie 中第一个会话的账号可访问 WAA 页面并读取实时模型目录
func Verify(ctx context.Context, state *aistudio.StorageState, proxy string) (Verification, error) {
	return VerifyAuthUser(ctx, state, proxy, 0)
}

// VerifyAuthUser 验证 Cookie 中第 authUser 个会话的账号可访问 WAA 页面并读取实时模型目录
func VerifyAuthUser(ctx context.Context, state *aistudio.StorageState, proxy string, authUser int) (Verification, error) {
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

// DiscoverCookieAccounts 从 u/0 起依次打开 Cookie 中各会话的对话页，返回各序号登录的账户，序号不存在或账户与前面重复时结束
func DiscoverCookieAccounts(ctx context.Context, state *aistudio.StorageState, proxy string) ([]CookieAccount, error) {
	client, headers, err := newVerifyClient(ctx, state, proxy)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	var accounts []CookieAccount
	for authUser := range maxCookieAuthUsers {
		email, err := verifyChatPage(ctx, client, state, headers.Get("User-Agent"), authUser)
		if authUser > 0 && errors.Is(err, ErrSignedOut) {
			break
		}
		if err != nil {
			return nil, err
		}
		email = strings.ToLower(email)
		if slices.ContainsFunc(accounts, func(account CookieAccount) bool { return account.Email == email }) {
			break
		}
		accounts = append(accounts, CookieAccount{AuthUser: authUser, Email: email})
	}
	return accounts, nil
}

// newVerifyClient 创建验证用的 HTTP 客户端并读取官网公共请求头，客户端不跟随重定向
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

// chatURLForAuthUser 返回 Cookie 中第 authUser 个会话的对话页地址
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
