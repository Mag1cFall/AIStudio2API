package camoufoxnative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const aiStudioOrigin = "https://aistudio.google.com"

const generateContentPath = "/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/GenerateContent"

var publicHeaderNames = []string{
	"x-goog-api-key",
	"x-goog-authuser",
	"x-user-agent",
	"x-aistudio-g1-tier",
	"x-aistudio-visit-id",
	"x-goog-ext-519733851-bin",
	"user-agent",
}

// Worker 保存单个账户的长驻 Camoufox 与 WAA service
type Worker struct {
	mu         sync.Mutex
	process    *browserProcess
	connection *websocket.Conn
	client     *bidiClient
	contextID  string
	state      State
	closed     bool
}

// Start 启动隔离 Camoufox 并完成一次官网 WAA bootstrap
func Start(ctx context.Context, options Options) (*Worker, error) {
	state, err := loadStorageState(options.StorageStatePath)
	if err != nil {
		return nil, err
	}
	if options.Model == "" {
		return nil, errors.New("WAA bootstrap 缺少实时目录聊天模型")
	}
	if options.BootstrapPrompt == "" {
		options.BootstrapPrompt = fmt.Sprintf("AIStudio2API bootstrap %d", time.Now().UnixNano())
	}
	options.reportStartup(StartupPreparingBrowser)
	ffVersion, err := browserMajor(options.ExecutablePath)
	if err != nil {
		return nil, err
	}
	fingerprint, err := loadAccountCamoufoxConfig(options.StorageStatePath, ffVersion, options.Locale, options.Timezone)
	if err != nil {
		return nil, err
	}
	options.reportStartup(StartupLaunchingBrowser)
	process, endpoint, err := launchBrowser(ctx, options, fingerprint)
	if err != nil {
		return nil, err
	}
	worker := &Worker{process: process}
	failed := true
	defer func() {
		if failed {
			_ = worker.abort()
		}
	}()
	options.reportStartup(StartupConnectingBiDi)
	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	connection, _, err := dialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("连接 Camoufox BiDi: %w", err)
	}
	worker.connection = connection
	worker.client = newBiDiClient(connection)
	if err := worker.bootstrap(ctx, options, state); err != nil {
		return nil, err
	}
	failed = false
	return worker, nil
}

func (worker *Worker) abort() error {
	if worker == nil {
		return nil
	}
	worker.mu.Lock()
	if worker.closed {
		worker.mu.Unlock()
		return nil
	}
	worker.closed = true
	connection := worker.connection
	process := worker.process
	worker.mu.Unlock()
	if connection != nil {
		_ = connection.Close()
	}
	return process.Close()
}

// ProtocolHeaders 返回官网为 GenerateContent 构造的七个公共头
func (worker *Worker) ProtocolHeaders(ctx context.Context) (http.Header, error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if worker.closed {
		return nil, errors.New("Camoufox runtime 已关闭")
	}
	return worker.state.Headers.Clone(), nil
}

// Proof 同步官网 prompt 状态后为 SHA-256 digest 生成 fresh WAA proof
func (worker *Worker) Proof(ctx context.Context, digest string, prompt string) (string, error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.closed {
		return "", errors.New("Camoufox runtime 已关闭")
	}
	value, err := worker.client.evaluateString(ctx, worker.contextID, fillPromptExpression(prompt))
	if err != nil {
		return "", fmt.Errorf("同步官网 prompt: %w", err)
	}
	if value != prompt {
		return "", errors.New("官网 prompt 状态未同步")
	}
	proof, err := worker.client.evaluateString(ctx, worker.contextID, takeProofExpression(digest))
	if err != nil {
		return "", fmt.Errorf("生成 fresh WAA proof: %w", err)
	}
	if !strings.HasPrefix(proof, "!") {
		return "", errors.New("fresh WAA proof 前缀无效")
	}
	return proof, nil
}

// State 返回 runtime 的不可变状态副本
func (worker *Worker) State() State {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	state := worker.state
	state.Headers = state.Headers.Clone()
	return state
}

// Close 结束 BiDi session 并清理 Camoufox profile
func (worker *Worker) Close() error {
	if worker == nil {
		return nil
	}
	worker.mu.Lock()
	if worker.closed {
		worker.mu.Unlock()
		return nil
	}
	client := worker.client
	connection := worker.connection
	process := worker.process
	worker.mu.Unlock()
	if client != nil && connection != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = client.command(closeCtx, "session.end", map[string]any{})
		cancel()
		_ = connection.Close()
	}
	if err := process.Close(); err != nil {
		return err
	}
	worker.mu.Lock()
	worker.closed = true
	worker.mu.Unlock()
	return nil
}

func (worker *Worker) bootstrap(ctx context.Context, options Options, storage storageState) error {
	client := worker.client
	if _, err := client.command(ctx, "session.new", map[string]any{"capabilities": map[string]any{}}); err != nil {
		return err
	}
	tree, err := client.command(ctx, "browsingContext.getTree", map[string]any{"maxDepth": 0})
	if err != nil {
		return err
	}
	contexts, _ := tree["contexts"].([]any)
	if len(contexts) == 0 {
		return errors.New("Camoufox BiDi 未返回初始 tab")
	}
	root, _ := contexts[0].(map[string]any)
	contextID, _ := root["context"].(string)
	if contextID == "" {
		return errors.New("Camoufox BiDi 初始 tab 无效")
	}
	worker.contextID = contextID
	if err := client.installLocalStorage(ctx, contextID, storage.Origins); err != nil {
		return err
	}
	if err := client.installCookies(ctx, storage.Cookies); err != nil {
		return err
	}
	options.reportStartup(StartupLoadingAIStudio)
	target := aiStudioOrigin + "/prompts/new_chat?model=" + url.QueryEscape(options.Model)
	if options.TemporaryChat {
		target += "&temporary=true"
	}
	if _, err := client.command(ctx, "browsingContext.navigate", map[string]any{
		"context": contextID,
		"url":     target,
		"wait":    "interactive",
	}); err != nil && !strings.Contains(err.Error(), "NS_ERROR_ABORT") {
		return fmt.Errorf("导航 AI Studio: %w", err)
	}
	if err := client.waitFor(ctx, contextID, `(() => {
	  if (location.hostname === 'accounts.google.com') return true;
  const item = document.querySelector('ms-prompt-box textarea:last-of-type') || [...document.querySelectorAll('ms-prompt-box textarea')].at(-1);
  return Boolean(item && item.offsetParent !== null);
})()`, 120*time.Second); err != nil {
		pageURL, _ := client.evaluateString(ctx, contextID, "location.href")
		return fmt.Errorf("AI Studio 输入框未就绪 url=%s: %w", pageURL, err)
	}
	pageURL, err := client.evaluateString(ctx, contextID, "location.href")
	if err != nil {
		return err
	}
	if strings.Contains(pageURL, "accounts.google.com") {
		return fmt.Errorf("隔离登录态失效 url=%s", pageURL)
	}
	if err := dismissKnownOverlays(ctx, client, contextID); err != nil {
		return err
	}
	options.reportStartup(StartupLocatingWAA)
	snapshotKey, err := client.waitSnapshotFunction(ctx, contextID, 30*time.Second)
	if err != nil {
		return err
	}
	if err := installBootstrapRequestCapture(ctx, client, contextID); err != nil {
		return err
	}
	options.reportStartup(StartupBootstrappingWAA)
	filled, err := client.evaluateString(ctx, contextID, fillPromptExpression(options.BootstrapPrompt))
	if err != nil || filled != options.BootstrapPrompt {
		return fmt.Errorf("填写 bootstrap 提示词失败 value=%q err=%v", filled, err)
	}
	if _, err := client.command(ctx, "session.subscribe", map[string]any{
		"events":   []string{"network.beforeRequestSent"},
		"contexts": []string{contextID},
	}); err != nil {
		return err
	}
	intercept, err := client.command(ctx, "network.addIntercept", map[string]any{
		"phases":   []string{"beforeRequestSent"},
		"contexts": []string{contextID},
		"urlPatterns": []map[string]any{{
			"type":     "pattern",
			"protocol": "https",
			"hostname": "alkalimakersuite-pa.clients6.google.com",
			"pathname": generateContentPath,
		}},
	})
	if err != nil {
		return fmt.Errorf("安装 GenerateContent 拦截: %w", err)
	}
	interceptID, _ := intercept["intercept"].(string)
	if interceptID == "" {
		return errors.New("GenerateContent 拦截 ID 无效")
	}
	clicked, err := client.evaluateBool(ctx, contextID, `(() => {
  const button = [...document.querySelectorAll('ms-run-button button[type="submit"]')].at(-1);
  if (!button || button.disabled) return false;
  button.click();
  return true;
})()`)
	if err != nil || !clicked {
		return fmt.Errorf("官网 Run 按钮不可用 clicked=%t err=%v", clicked, err)
	}
	if err := client.waitFor(ctx, contextID, "Boolean(window.__aistudioWaaService)", 60*time.Second); err != nil {
		return fmt.Errorf("官网 WAA service 未暴露: %w", err)
	}
	requestID, err := client.waitBlockedGenerateRequest(ctx, contextID, 60*time.Second)
	if err != nil {
		return err
	}
	if _, err := client.command(ctx, "network.failRequest", map[string]any{"request": requestID}); err != nil {
		return fmt.Errorf("终止 bootstrap GenerateContent: %w", err)
	}
	if _, err := client.command(ctx, "network.removeIntercept", map[string]any{"intercept": interceptID}); err != nil {
		return fmt.Errorf("移除 GenerateContent 拦截: %w", err)
	}
	actualModel, err := capturedBootstrapModel(ctx, client, contextID)
	if err != nil {
		return err
	}
	if actualModel != strings.TrimPrefix(options.Model, "models/") {
		return fmt.Errorf("官网初始化页面模型不匹配 expected=%s actual=%s", options.Model, actualModel)
	}
	restored, err := client.evaluateBool(ctx, contextID, `(() => {
  if (typeof window.__aistudioRestoreBootstrapCapture !== 'function') return false;
  window.__aistudioRestoreBootstrapCapture();
  delete window.__aistudioRestoreBootstrapCapture;
  return true;
})()`)
	if err != nil {
		return fmt.Errorf("移除 bootstrap 请求捕获: %w", err)
	}
	if !restored {
		return errors.New("bootstrap 请求捕获未安装")
	}
	headers := make(http.Header, len(publicHeaderNames))
	for _, name := range publicHeaderNames {
		value := client.generateHeaders[name]
		if value != "" {
			headers.Set(name, value)
		}
	}
	for _, name := range []string{"user-agent", "x-goog-api-key", "x-goog-authuser", "x-user-agent"} {
		if headers.Get(name) == "" {
			return fmt.Errorf("官网 GenerateContent 缺少必要公共头 %s", name)
		}
	}
	if _, err := client.command(ctx, "session.unsubscribe", map[string]any{
		"events":   []string{"network.beforeRequestSent"},
		"contexts": []string{contextID},
	}); err != nil {
		return fmt.Errorf("停止 GenerateContent 网络事件订阅: %w", err)
	}
	userAgent, _ := client.evaluateString(ctx, contextID, "navigator.userAgent")
	platform, _ := client.evaluateString(ctx, contextID, "navigator.platform")
	timezone, _ := client.evaluateString(ctx, contextID, "Intl.DateTimeFormat().resolvedOptions().timeZone")
	worker.state = State{
		PID:         worker.process.command.Process.Pid,
		PageURL:     pageURL,
		UserAgent:   userAgent,
		Platform:    platform,
		Timezone:    timezone,
		SnapshotKey: snapshotKey,
		Headers:     headers,
	}
	return nil
}

func dismissKnownOverlays(ctx context.Context, client *bidiClient, contextID string) error {
	_, err := client.evaluate(ctx, contextID, `(() => {
  const selectors = [
    'ms-g1-welcome-dialog button[aria-label="Close dialog"]',
    'button[aria-label="Close guided tour"]',
    '#glue-cookie-notification-bar-1 .glue-cookie-notification-bar__reject'
  ];
  let clicked = 0;
  for (const selector of selectors) {
    const button = document.querySelector(selector);
    if (button instanceof HTMLElement && button.offsetParent !== null && !button.disabled) {
      button.click();
      clicked++;
    }
  }
  return clicked;
})()`)
	if err != nil {
		return fmt.Errorf("处理 AI Studio 启动覆盖层: %w", err)
	}
	return nil
}

func installBootstrapRequestCapture(ctx context.Context, client *bidiClient, contextID string) error {
	encodedPath, err := json.Marshal(generateContentPath)
	if err != nil {
		return err
	}
	expression := fmt.Sprintf(`(() => {
  const targetPath = %s;
  const matches = (input) => {
    const raw = typeof input === 'string' ? input : input?.url;
    if (!raw) return false;
    try { return new URL(raw, location.href).pathname === targetPath; } catch { return false; }
  };
  const record = (body) => {
    if (typeof body === 'string') {
      window.__aistudioBootstrapRequestBody = body;
      return;
    }
    if (body instanceof Blob) {
      body.text().then(record);
      return;
    }
    if (body instanceof ArrayBuffer) {
      record(new TextDecoder().decode(body));
      return;
    }
    if (ArrayBuffer.isView(body)) record(new TextDecoder().decode(body));
  };
  const originalFetch = window.fetch;
  const fetchWrapper = function(input, init) {
    if (matches(input)) {
      const body = init?.body;
      if (body !== undefined) record(body);
      else if (input instanceof Request) input.clone().text().then(record);
    }
    return originalFetch.apply(this, arguments);
  };
  const originalOpen = XMLHttpRequest.prototype.open;
  const originalSend = XMLHttpRequest.prototype.send;
  const requestURLs = new WeakMap();
  const openWrapper = function(method, url) {
    requestURLs.set(this, String(url));
    return originalOpen.apply(this, arguments);
  };
  const sendWrapper = function(body) {
    if (matches(requestURLs.get(this))) record(body);
    return originalSend.apply(this, arguments);
  };
  window.fetch = fetchWrapper;
  XMLHttpRequest.prototype.open = openWrapper;
  XMLHttpRequest.prototype.send = sendWrapper;
  window.__aistudioRestoreBootstrapCapture = () => {
    if (window.fetch === fetchWrapper) window.fetch = originalFetch;
    if (XMLHttpRequest.prototype.open === openWrapper) XMLHttpRequest.prototype.open = originalOpen;
    if (XMLHttpRequest.prototype.send === sendWrapper) XMLHttpRequest.prototype.send = originalSend;
  };
  return true;
})()`, encodedPath)
	installed, err := client.evaluateBool(ctx, contextID, expression)
	if err != nil {
		return fmt.Errorf("安装 bootstrap 请求捕获: %w", err)
	}
	if !installed {
		return errors.New("安装 bootstrap 请求捕获失败")
	}
	return nil
}

func capturedBootstrapModel(ctx context.Context, client *bidiClient, contextID string) (string, error) {
	if err := client.waitFor(ctx, contextID, "typeof window.__aistudioBootstrapRequestBody === 'string'", 5*time.Second); err != nil {
		return "", fmt.Errorf("官网 bootstrap 请求正文未捕获: %w", err)
	}
	body, err := client.evaluateString(ctx, contextID, `(() => {
  window.__aistudioRestoreBootstrapCapture?.();
  return window.__aistudioBootstrapRequestBody;
})()`)
	if err != nil {
		return "", err
	}
	var wire []any
	if err := json.Unmarshal([]byte(body), &wire); err != nil {
		return "", fmt.Errorf("解析官网 bootstrap 请求正文: %w", err)
	}
	if len(wire) == 0 {
		return "", errors.New("官网 bootstrap 请求缺少模型")
	}
	model, _ := wire[0].(string)
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if model == "" {
		return "", errors.New("官网 bootstrap 请求模型无效")
	}
	return model, nil
}

func loadStorageState(path string) (storageState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return storageState{}, fmt.Errorf("读取 storage state: %w", err)
	}
	var state storageState
	if err := json.Unmarshal(data, &state); err != nil {
		return storageState{}, fmt.Errorf("解析 storage state: %w", err)
	}
	if len(state.Cookies) == 0 {
		return storageState{}, errors.New("storage state 没有 Cookie")
	}
	return state, nil
}
