package camoufoxnative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const loginEmailExpression = `(() => {
  const values = [document.body?.innerText || ''];
  for (const element of document.querySelectorAll('[aria-label]')) {
    values.push(element.getAttribute('aria-label') || '');
  }
  return values.join('\n').match(/[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}/i)?.[0] || '';
})()`

// LoginOptions 定义纯 Go 隔离登录环境，View 不为空时浏览器以无头方式运行，画面与输入经管理页登录窗口传递
type LoginOptions struct {
	ExecutablePath string
	Directory      string
	Locale         string
	Timezone       string
	Proxy          string
	ProxyBypass    string
	Timeout        time.Duration
	Log            io.Writer
	View           *LiveView
}

// LoginResult 返回隔离浏览器导出的 Playwright storage state
type LoginResult struct {
	StorageStateJSON []byte
	Email            string
	PageURL          string
	VerifiedAt       time.Time
	DriveError       string
	CloseError       string
}

type loginSession struct {
	process    *browserProcess
	connection *websocket.Conn
	client     *bidiClient
	contextID  string
	// stopView 停止登录窗口的截图与输入转发
	stopView func()
}

// ErrNoDisplay 表示本机没有图形界面，无法打开可见的登录窗口
var ErrNoDisplay = errors.New("当前环境没有图形界面，无法打开 Camoufox 登录窗口：启动服务后在管理页面“添加账户”中选择“浏览器登录”，登录窗口显示在管理页面中")

// HasDisplay 判断本机能否显示可见的浏览器窗口，Linux 上需要 DISPLAY 或 WAYLAND_DISPLAY
func HasDisplay() bool {
	return runtime.GOOS != "linux" || os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

// Login 启动隔离 Camoufox 供用户登录 Google，并在 AI Studio 可用后导出认证状态
func Login(ctx context.Context, options LoginOptions) (result LoginResult, err error) {
	options, err = validateLoginOptions(options)
	if err != nil {
		return LoginResult{}, err
	}
	if options.View == nil && !HasDisplay() {
		return LoginResult{}, ErrNoDisplay
	}
	options.View.setStatus(liveViewStarting)
	loginCtx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	session, err := startLoginSession(loginCtx, options, storageState{})
	if err != nil {
		return LoginResult{}, err
	}
	defer func() {
		closeErr := session.Close()
		if err == nil && closeErr != nil {
			result.CloseError = closeErr.Error()
			return
		}
		err = errors.Join(err, closeErr)
	}()
	options.View.setStatus(liveViewWaiting)
	origins := make(map[string]storageOrigin)
	pageURL, err := session.waitLogin(loginCtx, origins)
	if err != nil {
		return LoginResult{}, err
	}
	options.View.setStatus(liveViewSaving)
	email, err := session.client.evaluateString(loginCtx, session.contextID, loginEmailExpression)
	if err != nil {
		return LoginResult{}, fmt.Errorf("读取 AI Studio 登录邮箱: %w", err)
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return LoginResult{}, errors.New("AI Studio 页面没有登录邮箱")
	}
	driveErr := session.authorizeDrive(loginCtx, email)
	if err := loginCtx.Err(); err != nil {
		return LoginResult{}, err
	}
	state, err := session.exportStorageState(loginCtx, origins)
	if err != nil {
		return LoginResult{}, err
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return LoginResult{}, fmt.Errorf("编码 storage state: %w", err)
	}
	return LoginResult{
		StorageStateJSON: encoded,
		Email:            email,
		PageURL:          pageURL,
		VerifiedAt:       time.Now().UTC(),
		DriveError:       errorText(driveErr),
	}, nil
}

// errorText 返回错误文本，nil 返回空字符串
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func validateLoginOptions(options LoginOptions) (LoginOptions, error) {
	options.ExecutablePath = strings.TrimSpace(options.ExecutablePath)
	options.Directory = strings.TrimSpace(options.Directory)
	if options.ExecutablePath == "" {
		return LoginOptions{}, errors.New("缺少 Camoufox 路径")
	}
	if options.Directory == "" {
		return LoginOptions{}, errors.New("缺少隔离登录目录")
	}
	directory, err := filepath.Abs(options.Directory)
	if err != nil {
		return LoginOptions{}, fmt.Errorf("解析隔离登录目录: %w", err)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return LoginOptions{}, fmt.Errorf("创建隔离登录目录: %w", err)
	}
	if options.Timeout <= 0 {
		return LoginOptions{}, errors.New("隔离登录超时必须为正数")
	}
	options.Directory = directory
	return options, nil
}

func startLoginSession(ctx context.Context, options LoginOptions, state storageState) (*loginSession, error) {
	ffVersion := camoufoxFirefoxMajor
	fingerprintPath := filepath.Join(options.Directory, "storage-state.json")
	fingerprint, err := loadAccountCamoufoxConfig(fingerprintPath, ffVersion, options.Locale, options.Timezone)
	if err != nil {
		return nil, err
	}
	process, endpoint, err := launchBrowser(ctx, Options{
		ExecutablePath: options.ExecutablePath,
		Locale:         options.Locale,
		Timezone:       options.Timezone,
		Proxy:          options.Proxy,
		ProxyBypass:    options.ProxyBypass,
		Headless:       options.View != nil,
		ReadyTimeout:   options.Timeout,
		Log:            options.Log,
		liveView:       options.View != nil,
	}, fingerprint)
	if err != nil {
		return nil, err
	}
	session := &loginSession{process: process}
	failed := true
	defer func() {
		if failed {
			_ = session.Close()
		}
	}()
	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	connection, _, err := dialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("连接 Camoufox BiDi: %w", err)
	}
	session.connection = connection
	session.client = newBiDiClient(connection)
	capabilities := map[string]any{}
	if options.View != nil {
		capabilities = liveViewCapabilities
	}
	if _, err := session.client.command(ctx, "session.new", map[string]any{"capabilities": capabilities}); err != nil {
		return nil, err
	}
	tree, err := session.client.command(ctx, "browsingContext.getTree", map[string]any{"maxDepth": 0})
	if err != nil {
		return nil, err
	}
	contexts, _ := tree["contexts"].([]any)
	if len(contexts) == 0 {
		return nil, errors.New("Camoufox BiDi 未返回初始 tab")
	}
	root, _ := contexts[0].(map[string]any)
	session.contextID, _ = root["context"].(string)
	if session.contextID == "" {
		return nil, errors.New("Camoufox BiDi 初始 tab 无效")
	}
	if len(state.Origins) != 0 {
		if err := session.client.installLocalStorage(ctx, session.contextID, state.Origins); err != nil {
			return nil, err
		}
	}
	if len(state.Cookies) != 0 {
		if err := session.client.installCookies(ctx, state.Cookies); err != nil {
			return nil, err
		}
	}
	if _, err := session.client.command(ctx, "session.subscribe", map[string]any{
		"events":   []string{"network.responseCompleted"},
		"contexts": []string{session.contextID},
	}); err != nil {
		return nil, err
	}
	wait := "interactive"
	if options.View != nil {
		session.stopView, err = options.View.attach(ctx, session.client, session.contextID)
		if err != nil {
			return nil, err
		}
		wait = "none"
	}
	if _, err := session.client.command(ctx, "browsingContext.navigate", map[string]any{
		"context": session.contextID,
		"url":     aiStudioOrigin + "/prompts/new_chat",
		"wait":    wait,
	}); err != nil && !strings.Contains(err.Error(), "NS_ERROR_ABORT") {
		return nil, fmt.Errorf("导航 AI Studio: %w", err)
	}
	failed = false
	return session, nil
}

func (session *loginSession) waitLogin(ctx context.Context, origins map[string]storageOrigin) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		pageURL, err := session.client.evaluateString(ctx, session.contextID, "location.href")
		if err != nil {
			if !retryablePageEvaluation(err) {
				return "", fmt.Errorf("读取隔离登录页面: %w", err)
			}
			if err := waitContext(ctx, 300*time.Millisecond); err != nil {
				return "", err
			}
			continue
		}
		session.captureCurrentOrigin(ctx, pageURL, origins)
		ready, readyErr := session.client.evaluateBool(ctx, session.contextID, promptReadyExpression)
		if readyErr != nil && !retryablePageEvaluation(readyErr) {
			return "", fmt.Errorf("检查隔离登录页面: %w", readyErr)
		}
		if readyErr == nil && ready && strings.HasPrefix(pageURL, aiStudioOrigin+"/") {
			return pageURL, nil
		}
		if err := waitContext(ctx, 300*time.Millisecond); err != nil {
			return "", err
		}
	}
}

func (session *loginSession) captureCurrentOrigin(ctx context.Context, pageURL string, origins map[string]storageOrigin) {
	parsed, err := url.Parse(pageURL)
	if err != nil || parsed.Scheme != "https" || !isGoogleHost(parsed.Hostname()) {
		return
	}
	expression := `JSON.stringify(Object.keys(localStorage).sort().map(name => ({name, value: localStorage.getItem(name)})))`
	encoded, err := session.client.evaluateString(ctx, session.contextID, expression)
	if err != nil {
		return
	}
	var items []localStorageItem
	if err := json.Unmarshal([]byte(encoded), &items); err != nil {
		return
	}
	origin := parsed.Scheme + "://" + parsed.Host
	origins[origin] = storageOrigin{Origin: origin, LocalStorage: items}
}

func (session *loginSession) exportStorageState(ctx context.Context, origins map[string]storageOrigin) (storageState, error) {
	result, err := session.client.command(ctx, "storage.getCookies", map[string]any{})
	if err != nil {
		return storageState{}, fmt.Errorf("导出 Cookie: %w", err)
	}
	items, _ := result["cookies"].([]any)
	cookies := make([]storageCookie, 0, len(items))
	for _, raw := range items {
		value, _ := raw.(map[string]any)
		cookie, ok := decodeStorageCookie(value)
		if ok {
			cookies = append(cookies, cookie)
		}
	}
	if len(cookies) == 0 {
		return storageState{}, errors.New("隔离浏览器没有可导出的 Cookie")
	}
	sort.SliceStable(cookies, func(left, right int) bool {
		if cookies[left].Domain != cookies[right].Domain {
			return cookies[left].Domain < cookies[right].Domain
		}
		if cookies[left].Name != cookies[right].Name {
			return cookies[left].Name < cookies[right].Name
		}
		return cookies[left].Path < cookies[right].Path
	})
	storageOrigins := make([]storageOrigin, 0, len(origins))
	for _, origin := range origins {
		storageOrigins = append(storageOrigins, origin)
	}
	sort.Slice(storageOrigins, func(left, right int) bool {
		return storageOrigins[left].Origin < storageOrigins[right].Origin
	})
	return storageState{Cookies: cookies, Origins: storageOrigins}, nil
}

func decodeStorageCookie(value map[string]any) (storageCookie, bool) {
	name, _ := value["name"].(string)
	domain, _ := value["domain"].(string)
	path, _ := value["path"].(string)
	if name == "" || domain == "" || path == "" {
		return storageCookie{}, false
	}
	expires := float64(-1)
	if value["expiry"] != nil {
		if parsed, ok := number(value["expiry"]); ok {
			expires = parsed
		}
	}
	sameSite, _ := value["sameSite"].(string)
	sameSite = strings.ToLower(strings.TrimSpace(sameSite))
	if sameSite != "none" && sameSite != "lax" && sameSite != "strict" {
		sameSite = ""
	} else {
		sameSite = strings.ToUpper(sameSite[:1]) + sameSite[1:]
	}
	partitionKey, _ := value["partitionKey"].(string)
	httpOnly, _ := value["httpOnly"].(bool)
	secure, _ := value["secure"].(bool)
	return storageCookie{
		Name:         name,
		Value:        remoteBytesValue(value["value"]),
		Domain:       domain,
		Path:         path,
		Expires:      expires,
		HTTPOnly:     httpOnly,
		Secure:       secure,
		SameSite:     sameSite,
		PartitionKey: partitionKey,
	}, true
}

// Close 结束登录 session 并清理隔离 profile
func (session *loginSession) Close() error {
	if session == nil {
		return nil
	}
	if session.stopView != nil {
		session.stopView()
	}
	if session.client != nil && session.connection != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = session.client.command(closeCtx, "session.end", map[string]any{})
		cancel()
		_ = session.connection.Close()
	}
	if session.process == nil {
		return nil
	}
	return session.process.Close()
}

func isGoogleLoginURL(pageURL string) bool {
	parsed, err := url.Parse(pageURL)
	return err == nil && strings.EqualFold(parsed.Hostname(), "accounts.google.com")
}

func isGoogleHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "google.com" || strings.HasSuffix(host, ".google.com")
}
