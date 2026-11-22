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
	// OnLoggedIn 在导出认证状态后、关闭浏览器之前保存账户，为空时直接关闭浏览器并返回
	OnLoggedIn func(context.Context, LoginResult) error
	// CheckVerification 核对已保存账户是否需要验证，传入最新导出的 storage state JSON
	CheckVerification func(context.Context, []byte) (bool, error)
	// OnVerifyStep 报告核对与验证页步骤（verifying 或 verify）
	OnVerifyStep func(string)
	// VerifyPollInterval 是停在验证页时定时自动再核对的间隔，不大于 0 时默认 10 秒
	VerifyPollInterval time.Duration
}

// LoginResult 返回隔离浏览器导出的 Playwright storage state
type LoginResult struct {
	StorageStateJSON    []byte
	Email               string
	PageURL             string
	VerifiedAt          time.Time
	DriveError          string
	CloseError          string
	Saved               bool
	VerificationPending bool
	VerificationError   string
}

// VerifyOptions 定义在隔离 Camoufox 中打开已有账户验证页的环境与核对回调
type VerifyOptions struct {
	ExecutablePath    string
	Directory         string
	StorageStateJSON  []byte
	AuthUser          int
	Locale            string
	Timezone          string
	Proxy             string
	ProxyBypass       string
	Timeout           time.Duration
	Log               io.Writer
	View              *LiveView
	CheckVerification func(context.Context, []byte) (bool, error)
	OnVerifyStep      func(string)
	PollInterval      time.Duration
}

// defaultVerifyPollInterval 是验证页自动再核对的默认间隔
const defaultVerifyPollInterval = 10 * time.Second

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

// Login 启动隔离 Camoufox 供用户登录 Google，并在 AI Studio 可用后导出认证状态；配置 OnLoggedIn 时先保存账户再核对，官网要求验证时窗口停在验证页并自动再核对
func Login(ctx context.Context, options LoginOptions) (result LoginResult, err error) {
	if options.View == nil && !HasDisplay() {
		return LoginResult{}, ErrNoDisplay
	}
	options.View.setStatus(liveViewStarting)
	loginCtx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	session, err := startLoginSession(loginCtx, options, storageState{}, aiStudioOrigin+"/prompts/new_chat")
	if err != nil {
		if exited := loginBrowserExit(err); exited != nil {
			return LoginResult{}, exited
		}
		return LoginResult{}, err
	}
	defer func() {
		closeErr := session.Close()
		if result.Saved {
			if err == nil && closeErr != nil {
				result.CloseError = closeErr.Error()
			}
			return
		}
		if exited := loginBrowserExit(err); exited != nil {
			err = errors.Join(exited, closeErr)
			return
		}
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
	result = LoginResult{
		StorageStateJSON: encoded,
		Email:            email,
		PageURL:          pageURL,
		VerifiedAt:       time.Now().UTC(),
		DriveError:       errorText(driveErr),
	}
	if options.OnLoggedIn == nil {
		return result, nil
	}
	if err := options.OnLoggedIn(loginCtx, result); err != nil {
		return LoginResult{}, err
	}
	result.Saved = true
	if options.CheckVerification == nil {
		return result, nil
	}
	options.View.setStatus(liveViewChecking)
	reportVerifyStep(options.OnVerifyStep, "verifying")
	needVerify, checkErr := options.CheckVerification(loginCtx, result.StorageStateJSON)
	if checkErr != nil {
		result.VerificationPending = needVerify
		result.VerificationError = checkErr.Error()
		return result, nil
	}
	if !needVerify {
		return result, nil
	}
	wait := "interactive"
	if options.View != nil {
		wait = "none"
	}
	if _, navErr := session.client.command(loginCtx, "browsingContext.navigate", map[string]any{
		"context": session.contextID,
		"url":     AccountVerificationURL,
		"wait":    wait,
	}); navErr != nil && !commandErrorContains(navErr, "NS_ERROR_ABORT") {
		result.VerificationPending = true
		if loginBrowserExit(navErr) == nil && loginCtx.Err() == nil {
			result.VerificationError = navErr.Error()
		}
		return result, nil
	}
	pending, latest, verifyErr := session.waitVerification(loginCtx, options.View, origins, options.CheckVerification, options.OnVerifyStep, options.VerifyPollInterval)
	if len(latest) > 0 {
		result.StorageStateJSON = latest
	}
	result.VerificationPending = pending
	result.VerificationError = errorText(verifyErr)
	return result, nil
}

// Verify 启动隔离 Camoufox 载入已有账户认证状态并停在官网验证页，扫码后自动再核对，返回结束时账户是否仍需要验证
func Verify(ctx context.Context, options VerifyOptions) (bool, error) {
	if options.View == nil && !HasDisplay() {
		return true, ErrNoDisplay
	}
	var state storageState
	if err := json.Unmarshal(options.StorageStateJSON, &state); err != nil {
		return true, fmt.Errorf("解析账户 storage state: %w", err)
	}
	if len(state.Cookies) == 0 {
		return true, errors.New("账户没有可用于验证的 Cookie")
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	options.View.setStatus(liveViewStarting)
	verifyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	loginOptions := LoginOptions{
		ExecutablePath: options.ExecutablePath,
		Directory:      options.Directory,
		Locale:         options.Locale,
		Timezone:       options.Timezone,
		Proxy:          options.Proxy,
		ProxyBypass:    options.ProxyBypass,
		Timeout:        timeout,
		Log:            options.Log,
		View:           options.View,
	}
	session, err := startLoginSession(verifyCtx, loginOptions, state, verificationURLForAuthUser(options.AuthUser))
	if err != nil {
		if loginBrowserExit(err) != nil || verifyCtx.Err() != nil {
			return true, nil
		}
		return true, err
	}
	defer func() { _ = session.Close() }()
	origins := make(map[string]storageOrigin, len(state.Origins))
	for _, origin := range state.Origins {
		if origin.Origin != "" {
			origins[origin.Origin] = origin
		}
	}
	pending, _, verifyErr := session.waitVerification(verifyCtx, options.View, origins, options.CheckVerification, options.OnVerifyStep, options.PollInterval)
	if verifyErr != nil && (loginBrowserExit(verifyErr) != nil || verifyCtx.Err() != nil) {
		return true, nil
	}
	return pending, verifyErr
}

// reportVerifyStep 回调验证阶段步骤，report 为空时不做任何事
func reportVerifyStep(report func(string), step string) {
	if report != nil {
		report(step)
	}
}

// verificationURLForAuthUser 返回第 authUser 个会话的 AI Studio 验证页地址
func verificationURLForAuthUser(authUser int) string {
	if authUser > 0 {
		return fmt.Sprintf("%s/u/%d/verify-account", aiStudioOrigin, authUser)
	}
	return AccountVerificationURL
}

// isVerificationPageURL 判断页面地址是否为 AI Studio 验证页
func isVerificationPageURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() != "aistudio.google.com" {
		return false
	}
	cleanPath := strings.TrimSuffix(parsed.Path, "/")
	return cleanPath == "/verify-account" || strings.HasSuffix(cleanPath, "/verify-account")
}

// isAIStudioAppURL 判断页面地址是否位于非验证页的 AI Studio 页面
func isAIStudioAppURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() != "aistudio.google.com" {
		return false
	}
	return !isVerificationPageURL(raw)
}

// waitVerification 保持窗口在验证页并监听页面跳转与定时自动再核对，验证通过返回 pending=false，用户关闭窗口或取消任务返回 pending=true 且不报错
func (session *loginSession) waitVerification(
	ctx context.Context,
	view *LiveView,
	origins map[string]storageOrigin,
	check func(context.Context, []byte) (bool, error),
	onStep func(string),
	pollInterval time.Duration,
) (bool, []byte, error) {
	if pollInterval <= 0 {
		pollInterval = defaultVerifyPollInterval
	}
	view.setStatus(liveViewVerifying)
	reportVerifyStep(onStep, "verify")
	var latest []byte
	sawVerifyPage := false
	checkedAppPage := false
	lastCheck := time.Now()
	for {
		if ctx.Err() != nil {
			return true, latest, nil
		}
		pageURL, evalErr := session.client.evaluateString(ctx, session.contextID, "location.href")
		if evalErr != nil {
			if loginBrowserExit(evalErr) != nil || ctx.Err() != nil {
				return true, latest, nil
			}
			if !retryablePageEvaluation(evalErr) {
				return true, latest, fmt.Errorf("读取账户验证页面: %w", evalErr)
			}
			if waitContext(ctx, 300*time.Millisecond) != nil {
				return true, latest, nil
			}
			continue
		}
		session.captureCurrentOrigin(ctx, pageURL, origins)
		if isVerificationPageURL(pageURL) {
			sawVerifyPage = true
			checkedAppPage = false
		}
		leftVerify := isAIStudioAppURL(pageURL) && (sawVerifyPage || !checkedAppPage)
		due := time.Since(lastCheck) >= pollInterval
		if check != nil && (leftVerify || due) {
			if exported, exportErr := session.exportStorageState(ctx, origins); exportErr == nil {
				if raw, marshalErr := json.Marshal(exported); marshalErr == nil {
					latest = raw
				}
			} else if loginBrowserExit(exportErr) != nil || ctx.Err() != nil {
				return true, latest, nil
			}
			view.setStatus(liveViewChecking)
			reportVerifyStep(onStep, "verifying")
			needVerify, checkErr := check(ctx, latest)
			lastCheck = time.Now()
			if checkErr == nil && !needVerify {
				return false, latest, nil
			}
			if ctx.Err() != nil {
				return true, latest, nil
			}
			if leftVerify {
				sawVerifyPage = false
				checkedAppPage = true
			}
			view.setStatus(liveViewVerifying)
			reportVerifyStep(onStep, "verify")
		}
		if waitContext(ctx, 300*time.Millisecond) != nil {
			return true, latest, nil
		}
	}
}

// errorText 返回错误文本，nil 返回空字符串
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func startLoginSession(ctx context.Context, options LoginOptions, state storageState, startURL string) (*loginSession, error) {
	if startURL == "" {
		startURL = aiStudioOrigin + "/prompts/new_chat"
	}
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
	session.client = newBiDiClient(connection, process)
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
		"url":     startURL,
		"wait":    wait,
	}); err != nil && !commandErrorContains(err, "NS_ERROR_ABORT") {
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

// loginBrowserExit 在 err 由登录窗口关闭（页面被丢弃或浏览器以退出码 0 退出）或登录浏览器意外退出引起时返回对应的错误，其余返回 nil
func loginBrowserExit(err error) error {
	if commandErrorContains(err, "Browsing context got discarded") {
		return errors.New("登录窗口已关闭，登录没有完成")
	}
	var exited *browserExited
	if !errors.As(err, &exited) {
		return nil
	}
	if exited.clean {
		return errors.New("登录窗口已关闭，登录没有完成")
	}
	return fmt.Errorf("登录浏览器意外退出: %w", err)
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

func isGoogleHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "google.com" || strings.HasSuffix(host, ".google.com")
}
