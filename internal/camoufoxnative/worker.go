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
	ffVersion, err := browserMajor(options.ExecutablePath)
	if err != nil {
		return nil, err
	}
	fingerprint, err := loadAccountCamoufoxConfig(options.StorageStatePath, ffVersion, options.Locale, options.Timezone)
	if err != nil {
		return nil, err
	}
	process, endpoint, err := launchBrowser(ctx, options, fingerprint)
	if err != nil {
		return nil, err
	}
	worker := &Worker{process: process}
	failed := true
	defer func() {
		if failed {
			_ = worker.Close()
		}
	}()
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

// ProtocolHeaders 返回官网 bootstrap 实际发送的七个公共头
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

// Proof 为给定 SHA-256 digest 生成一枚 fresh WAA proof
func (worker *Worker) Proof(ctx context.Context, digest string) (string, error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.closed {
		return "", errors.New("Camoufox runtime 已关闭")
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
	worker.closed = true
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
	return process.Close()
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
	_, _ = client.evaluate(ctx, contextID, `(() => {
  const bar = document.querySelector('#glue-cookie-notification-bar-1');
  const button = bar?.querySelector('.glue-cookie-notification-bar__reject');
  if (button && button.offsetParent !== null) button.click();
  return Boolean(button);
})()`)
	if err := dismissVisibleDialogs(ctx, client, contextID); err != nil {
		return err
	}
	snapshotKey, err := client.waitSnapshotFunction(ctx, contextID, 30*time.Second)
	if err != nil {
		return err
	}
	filled, err := client.evaluateString(ctx, contextID, fillPromptExpression(options.BootstrapPrompt))
	if err != nil || filled != options.BootstrapPrompt {
		return fmt.Errorf("填写 bootstrap 提示词失败 value=%q err=%v", filled, err)
	}
	if _, err := client.command(ctx, "session.subscribe", map[string]any{
		"events":   []string{"network.beforeRequestSent", "network.responseStarted", "network.responseCompleted"},
		"contexts": []string{contextID},
	}); err != nil {
		return err
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
	if err := client.waitGenerateCompleted(ctx, contextID, 60*time.Second); err != nil {
		return err
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
	userAgent, _ := client.evaluateString(ctx, contextID, "navigator.userAgent")
	platform, _ := client.evaluateString(ctx, contextID, "navigator.platform")
	timezone, _ := client.evaluateString(ctx, contextID, "Intl.DateTimeFormat().resolvedOptions().timeZone")
	worker.state = State{
		PID:                    worker.process.command.Process.Pid,
		PageURL:                pageURL,
		UserAgent:              userAgent,
		Platform:               platform,
		Timezone:               timezone,
		SnapshotKey:            snapshotKey,
		OfficialGenerateStatus: client.generateStatus,
		Headers:                headers,
	}
	return nil
}

// dismissVisibleDialogs 关闭页面启动时出现的可见公告模态
func dismissVisibleDialogs(ctx context.Context, client *bidiClient, contextID string) error {
	for range 8 {
		clicked, err := client.evaluateBool(ctx, contextID, `(() => {
  const visible = (item) => item instanceof HTMLElement && item.offsetParent !== null;
  const dialogs = [...document.querySelectorAll('dialog[open], [role="dialog"]')].filter(visible);
  for (const dialog of dialogs.reverse()) {
    const buttons = [...dialog.querySelectorAll('button, [role="button"]')]
      .filter((button) => visible(button) && !button.disabled && button.getAttribute('aria-disabled') !== 'true');
    if (buttons.length === 0) continue;
    buttons.at(-1).click();
    return true;
  }
  return false;
})()`)
		if err != nil {
			return fmt.Errorf("处理 AI Studio 公告模态: %w", err)
		}
		if !clicked {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil
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
