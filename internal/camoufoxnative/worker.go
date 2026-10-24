package camoufoxnative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/proxydial"
	"github.com/gofrs/flock"
	"github.com/gorilla/websocket"
)

const aiStudioOrigin = "https://aistudio.google.com"

const generateContentPath = "/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/GenerateContent"

// requestBodyLimit 为 bootstrap 期间 BiDi 数据收集器保存的单个请求正文上限
const requestBodyLimit = 20_000_000

var publicHeaderNames = []string{
	"x-goog-api-key",
	"x-goog-authuser",
	"x-user-agent",
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
	// page 为页面主世界中保存官方 WAA service 与受保护请求状态的对象
	page      pageHandle
	state     State
	closed    bool
	cacheLock *flock.Flock
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
		options.BootstrapPrompt = DefaultBootstrapPrompt
	}
	options.reportStartup(StartupPreparingBrowser)
	ffVersion := camoufoxFirefoxMajor
	fingerprint, err := loadAccountCamoufoxConfig(options.StorageStatePath, ffVersion, options.Locale, options.Timezone)
	if err != nil {
		return nil, err
	}
	cacheDirectory, cacheLock, err := lockAccountCache(options.StorageStatePath)
	if err != nil {
		return nil, err
	}
	options.CacheDirectory = cacheDirectory
	worker := &Worker{cacheLock: cacheLock}
	failed := true
	defer func() {
		if failed {
			_ = worker.abort()
		}
	}()
	for {
		err := worker.launch(ctx, options, fingerprint)
		if err == nil {
			break
		}
		if !errors.Is(err, errBrowserStartupStalled) {
			return nil, err
		}
		if err := worker.closeBrowser(); err != nil {
			return nil, err
		}
	}
	if err := worker.bootstrap(ctx, options, state); err != nil {
		return nil, err
	}
	failed = false
	return worker, nil
}

// browserStartupTimeout 为 Camoufox 完成启动并响应 session.new 的最长等待
const browserStartupTimeout = 30 * time.Second

// errBrowserStartupStalled 表示 Camoufox 未在 browserStartupTimeout 内完成启动
var errBrowserStartupStalled = errors.New("Camoufox 启动未完成")

// ErrProxyUnreachable 表示 Worker 载入 AI Studio 期间无法连接代理
var ErrProxyUnreachable = errors.New("代理不可达")

// proxyWatchInterval 为载入 AI Studio 期间检查代理连接的间隔
const proxyWatchInterval = 2 * time.Second

// watchProxy 在返回的 context 结束前定期连接代理，连接失败时以 ErrProxyUnreachable 取消该 context
func watchProxy(ctx context.Context, proxy string) (context.Context, func()) {
	watchCtx, cancel := context.WithCancelCause(ctx)
	stop := func() { cancel(nil) }
	parsed, err := url.Parse(strings.TrimSpace(proxy))
	if err != nil || parsed.Host == "" {
		return watchCtx, stop
	}
	address := proxydial.Address(parsed)
	go func() {
		ticker := time.NewTicker(proxyWatchInterval)
		defer ticker.Stop()
		dialer := net.Dialer{Timeout: proxyWatchInterval}
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
			}
			connection, err := dialer.DialContext(watchCtx, "tcp", address)
			if err != nil {
				if watchCtx.Err() == nil {
					cancel(fmt.Errorf("%w %s: %v", ErrProxyUnreachable, address, err))
				}
				return
			}
			_ = connection.Close()
		}
	}()
	return watchCtx, stop
}

// launch 启动 Camoufox、连接 BiDi 并创建 session
func (worker *Worker) launch(ctx context.Context, options Options, fingerprint map[string]any) error {
	options.reportStartup(StartupLaunchingBrowser)
	process, endpoint, err := launchBrowser(ctx, options, fingerprint)
	if err != nil {
		return err
	}
	worker.process = process
	options.reportStartup(StartupConnectingBiDi)
	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	connection, _, err := dialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return fmt.Errorf("连接 Camoufox BiDi: %w", err)
	}
	worker.connection = connection
	worker.client = newBiDiClient(connection)
	sessionCtx, cancel := context.WithTimeout(ctx, browserStartupTimeout)
	defer cancel()
	if _, err := worker.client.command(sessionCtx, "session.new", map[string]any{"capabilities": map[string]any{}}); err != nil {
		if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
			return errBrowserStartupStalled
		}
		return err
	}
	return nil
}

// closeBrowser 关闭未完成启动的 Camoufox，保留账户缓存锁
func (worker *Worker) closeBrowser() error {
	_ = worker.connection.Close()
	err := worker.process.Close()
	worker.connection, worker.client, worker.process = nil, nil, nil
	return err
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
	if err := process.Close(); err != nil {
		return err
	}
	return releaseAccountCache(worker.cacheLock)
}

// ProtocolHeaders 返回官网为 GenerateContent 构造的六个公共头
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

// Proof 在一次页面调用中写入官网 prompt 并为 SHA-256 digest 生成 fresh WAA proof，页面值未同步时最多重试 5 秒
func (worker *Worker) Proof(ctx context.Context, digest string, prompt string) (string, error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.closed {
		return "", errors.New("Camoufox runtime 已关闭")
	}
	prompt = normalizePromptNewlines(prompt)
	deadline := time.Now().Add(5 * time.Second)
	for {
		result, err := worker.client.callFunction(ctx, worker.contextID, proofFunction, worker.page, prompt, digest)
		if err != nil {
			return "", fmt.Errorf("生成 fresh WAA proof: %w", err)
		}
		if result["type"] != "null" {
			proof, _ := result["value"].(string)
			if !strings.HasPrefix(proof, "!") {
				return "", errors.New("fresh WAA proof 前缀无效")
			}
			return proof, nil
		}
		if time.Now().After(deadline) {
			return "", errors.New("官网 prompt 状态未同步")
		}
		if err := waitContext(ctx, 100*time.Millisecond); err != nil {
			return "", err
		}
	}
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
	if err := releaseAccountCache(worker.cacheLock); err != nil {
		return err
	}
	worker.mu.Lock()
	worker.closed = true
	worker.mu.Unlock()
	return nil
}

// parallel 同时执行各函数并合并它们的错误
func parallel(functions ...func() error) error {
	errs := make([]error, len(functions))
	var wait sync.WaitGroup
	for index, function := range functions {
		wait.Go(func() { errs[index] = function() })
	}
	wait.Wait()
	return errors.Join(errs...)
}

func (worker *Worker) bootstrap(ctx context.Context, options Options, storage storageState) error {
	client := worker.client
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
	if err := parallel(
		func() error { return client.installLocalStorage(ctx, contextID, storage.Origins) },
		func() error { return client.installCookies(ctx, storage.Cookies) },
		func() error {
			_, err := client.command(ctx, "session.subscribe", map[string]any{
				"events":   []string{"browsingContext.navigationStarted", "script.message"},
				"contexts": []string{contextID},
			})
			return err
		},
	); err != nil {
		return err
	}
	options.reportStartup(StartupLoadingAIStudio)
	target := aiStudioOrigin + "/prompts/new_chat?model=" + url.QueryEscape(options.Model)
	if options.TemporaryChat {
		target += "&temporary=true"
	}
	pageCtx, stopWatch := watchProxy(ctx, options.Proxy)
	defer stopWatch()
	if _, err := client.command(pageCtx, "browsingContext.navigate", map[string]any{
		"context": contextID,
		"url":     target,
		"wait":    "interactive",
	}); err != nil && !strings.Contains(err.Error(), "NS_ERROR_ABORT") {
		if cause := context.Cause(pageCtx); errors.Is(cause, ErrProxyUnreachable) {
			return cause
		}
		if strings.Contains(err.Error(), "NS_ERROR_PROXY_") {
			return fmt.Errorf("%w: 导航 AI Studio: %v", ErrProxyUnreachable, err)
		}
		return fmt.Errorf("导航 AI Studio: %w", err)
	}
	if err := client.waitFor(pageCtx, contextID, workerPageReadyExpression, 120*time.Second); err != nil {
		if cause := context.Cause(pageCtx); errors.Is(cause, ErrProxyUnreachable) {
			return cause
		}
		if errors.Is(err, ErrRegionNotSupported) {
			return err
		}
		pageURL, _ := client.evaluateString(ctx, contextID, "location.href")
		return fmt.Errorf("AI Studio 输入框未就绪 url=%s: %w", pageURL, err)
	}
	stopWatch()
	pageURL, err := client.evaluateString(ctx, contextID, "location.href")
	if err != nil {
		return err
	}
	if location, parseErr := url.Parse(pageURL); parseErr == nil && location.Hostname() == "accounts.google.com" {
		return fmt.Errorf("%w url=%s", ErrAuthenticationRequired, pageURL)
	}
	if err := dismissKnownOverlays(ctx, client, contextID); err != nil {
		return err
	}
	options.reportStartup(StartupLocatingWAA)
	page, err := client.waitSnapshotFunction(ctx, contextID, 30*time.Second)
	if err != nil {
		return err
	}
	snapshotKey, err := client.callFunctionString(ctx, contextID, `page => page.key`, page)
	if err != nil {
		return fmt.Errorf("读取官网 snapshot 函数名: %w", err)
	}
	options.reportStartup(StartupBootstrappingWAA)
	if err := fillPrompt(ctx, client, contextID, options.BootstrapPrompt); err != nil {
		return fmt.Errorf("填写 bootstrap 提示词: %w", err)
	}
	var collectorID, interceptID string
	if err := parallel(
		func() error {
			_, err := client.command(ctx, "session.subscribe", map[string]any{
				"events":   []string{"network.beforeRequestSent"},
				"contexts": []string{contextID},
			})
			return err
		},
		func() error {
			collector, err := client.command(ctx, "network.addDataCollector", map[string]any{
				"dataTypes":          []string{"request"},
				"maxEncodedDataSize": requestBodyLimit,
				"contexts":           []string{contextID},
			})
			if err != nil {
				return fmt.Errorf("安装 GenerateContent 正文收集: %w", err)
			}
			collectorID, _ = collector["collector"].(string)
			return nil
		},
		func() error {
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
			interceptID, _ = intercept["intercept"].(string)
			if interceptID == "" {
				return errors.New("GenerateContent 拦截 ID 无效")
			}
			return nil
		},
	); err != nil {
		return err
	}
	if err := client.waitFor(ctx, contextID, runButtonEnabledExpression, 15*time.Second); err != nil {
		return fmt.Errorf("等待官网 Run 按钮启用: %w", err)
	}
	if _, err := client.evaluate(ctx, contextID, submitPromptExpression); err != nil {
		return fmt.Errorf("提交官网提示词: %w", err)
	}
	blocked, err := client.waitBlockedGenerateRequest(ctx, 60*time.Second)
	if err != nil {
		return err
	}
	captured, err := client.callFunction(ctx, contextID, `page => Boolean(page.service)`, page)
	if err != nil {
		return fmt.Errorf("读取官网 WAA service: %w", err)
	}
	if captured["value"] != true {
		return errors.New("官网 WAA service 未暴露")
	}
	data, err := client.command(ctx, "network.getData", map[string]any{"request": blocked.id, "dataType": "request", "collector": collectorID})
	if err != nil {
		return fmt.Errorf("读取官网 bootstrap 请求正文: %w", err)
	}
	if _, err := client.evaluate(ctx, contextID, stopGenerationExpression); err != nil {
		return fmt.Errorf("停止官网 bootstrap 生成: %w", err)
	}
	if _, err := client.command(ctx, "network.failRequest", map[string]any{"request": blocked.id}); err != nil {
		return fmt.Errorf("释放 bootstrap GenerateContent: %w", err)
	}
	if err := parallel(
		func() error {
			_, err := client.command(ctx, "network.removeIntercept", map[string]any{"intercept": interceptID})
			if err != nil {
				return fmt.Errorf("移除 GenerateContent 拦截: %w", err)
			}
			return nil
		},
		func() error {
			_, err := client.command(ctx, "network.removeDataCollector", map[string]any{"collector": collectorID})
			if err != nil {
				return fmt.Errorf("移除 GenerateContent 正文收集: %w", err)
			}
			return nil
		},
		func() error {
			_, err := client.command(ctx, "session.unsubscribe", map[string]any{
				"events":   []string{"network.beforeRequestSent"},
				"contexts": []string{contextID},
			})
			if err != nil {
				return fmt.Errorf("停止 GenerateContent 网络事件订阅: %w", err)
			}
			return nil
		},
	); err != nil {
		return err
	}
	actualModel, err := bootstrapModel(remoteBytesValue(data["bytes"]))
	if err != nil {
		return err
	}
	if actualModel != strings.TrimPrefix(options.Model, "models/") {
		return fmt.Errorf("官网初始化页面模型不匹配 expected=%s actual=%s", options.Model, actualModel)
	}
	headers := make(http.Header, len(publicHeaderNames))
	for _, name := range publicHeaderNames {
		if value := blocked.headers[name]; value != "" {
			headers.Set(name, value)
		}
	}
	for _, name := range []string{"user-agent", "x-goog-api-key", "x-goog-authuser", "x-user-agent"} {
		if headers.Get(name) == "" {
			return fmt.Errorf("官网 GenerateContent 缺少必要公共头 %s", name)
		}
	}
	worker.page = page
	worker.state = State{
		PID:         worker.process.command.Process.Pid,
		PageURL:     pageURL,
		UserAgent:   headers.Get("user-agent"),
		SnapshotKey: snapshotKey,
		Headers:     headers,
	}
	return nil
}

// bootstrapModel 返回官网 bootstrap GenerateContent 请求正文中的模型
func bootstrapModel(body string) (string, error) {
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
