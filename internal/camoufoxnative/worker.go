package camoufoxnative

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/proxydial"
	"github.com/gofrs/flock"
	"github.com/gorilla/websocket"
)

const aiStudioOrigin = "https://aistudio.google.com"

// generateContentPath 是官网 GenerateContent 的 RPC 路径
const generateContentPath = "/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/GenerateContent"

// collectedBodyLimit 为 BiDi 数据收集器保存的单个正文上限
const collectedBodyLimit = 20_000_000

// publicHeaderNames 是页面 MakerSuite 请求共用的公共头，x-goog-ext-519733851-bin 由页面 GetLoggingContext 响应得出
var publicHeaderNames = []string{
	"x-goog-api-key",
	"x-goog-authuser",
	"x-user-agent",
	"x-aistudio-visit-id",
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
	// cookieOrder 是启动时写入浏览器的各 Cookie 的序号
	cookieOrder map[cookieKey]int
	// pageCatalog 是页面载入时目录 RPC 的响应正文
	pageCatalog PageCatalog
	// options 是启动选项，重新载入页面时沿用
	options Options
	// tabs 是有进行中受保护请求或已被替换的标签页，键为标签页的 context
	tabs map[string]*tabUse
	// chats 是可运行界面对话的标签页，第一个是当前页面所在的标签页
	chats []*chatTab
	// selected 是浏览器窗口中选中的标签页
	selected string
	// inputMu 串行化页面输入与标签页切换，同一时刻只有一个标签页接收指针与输入
	inputMu sync.Mutex
	// inputWaiters 是正在等待 inputMu 的前台操作数
	inputWaiters atomic.Int32
	// frameMu 保护 frameBoosts 与刷新帧率的切换
	frameMu sync.Mutex
	// frameBoosts 是进行中的界面操作数，大于 0 时页面以 interactiveFrameRate 刷新
	frameBoosts int
	// idle 在最后一次界面操作 idleMinimizeDelay 后发出 memory-pressure，由 frameMu 保护
	idle *time.Timer
	// settle 在 Worker 就绪 readyMinimizeDelay 后发出一次 memory-pressure
	settle *time.Timer
}

// tabUse 是一个标签页中进行中的受保护请求数，retired 表示该标签页已被重新载入的页面替换
type tabUse struct {
	requests int
	retired  bool
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
	worker := &Worker{cacheLock: cacheLock, options: options, tabs: make(map[string]*tabUse)}
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
	if options.Headless && !options.liveView {
		// 浏览器启动与首次载入按 interactiveFrameRate 刷新，Worker 就绪后降到 idleFrameDivisor 并停止标签页绘制
		if _, err := worker.chromeEvaluate(ctx, idleRenderingExpression); err != nil {
			return nil, fmt.Errorf("停止 Camoufox 标签页绘制: %w", err)
		}
		worker.idle = time.AfterFunc(idleMinimizeDelay, worker.minimizeMemory)
		worker.settle = time.AfterFunc(readyMinimizeDelay, worker.minimizeMemory)
	}
	failed = false
	return worker, nil
}

// browserStartupTimeout 为 Camoufox 完成启动并响应 session.new 的最长等待
const browserStartupTimeout = 30 * time.Second

// pageImageTimeout 为页面载入等待官方 WAA 程序图片请求结束的上限，到期后页面照常就绪
const pageImageTimeout = 30 * time.Second

// pageReleaseTimeout 为页面载入结束后移除载入期间 BiDi 资源的期限
const pageReleaseTimeout = 10 * time.Second

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
	if runtime.GOOS != "windows" && options.Headless {
		return worker.placeWindow(sessionCtx)
	}
	return nil
}

// placeWindow 移动主窗口，使页面内容区的屏幕坐标与 Windows 版一致
func (worker *Worker) placeWindow(ctx context.Context) error {
	result, err := worker.client.command(ctx, "browser.getClientWindows", map[string]any{})
	if err != nil {
		return err
	}
	window := result["clientWindows"].([]any)[0].(map[string]any)
	_, err = worker.client.command(ctx, "browser.setClientWindowState", map[string]any{
		"clientWindow": window["clientWindow"], "state": "normal", "x": windowsWindowPosition, "y": windowsWindowPosition + windowsChromeHeight - hostChromeHeight,
	})
	return err
}

// stopRendering 经浏览器界面层关闭当前标签页的 renderLayers，标签页不再光栅化与合成，页面的可见状态、焦点、动画帧与定时器不变
func (worker *Worker) stopRendering(ctx context.Context) error {
	_, err := worker.chromeEvaluate(ctx, "gBrowser.selectedBrowser.renderLayers = false")
	return err
}

// interactiveFrameRate 是无头 Worker 的软件 vsync 频率，界面操作期间页面按此频率刷新，与官网页面在 60 Hz 显示器上相同
const interactiveFrameRate = 60

// idleFrameDivisor 是无头 Worker 空闲时的 vsync 分频，页面每秒刷新 1 次
const idleFrameDivisor = interactiveFrameRate

// idleRenderingExpression 把 vsync 分频设为 idleFrameDivisor 并停止选中标签页的绘制
var idleRenderingExpression = fmt.Sprintf("Services.prefs.setIntPref('gfx.display.frame-rate-divisor', %d); gBrowser.selectedBrowser.renderLayers = false", idleFrameDivisor)

// idleMinimizeDelay 为无头 Worker 最后一次界面操作后发出 memory-pressure 的空闲时长，与 SpiderMonkey 后端的空闲收缩 GC 相同
const idleMinimizeDelay = 5 * time.Minute

// readyMinimizeDelay 为无头 Worker 就绪后发出一次 memory-pressure 的延迟，回收浏览器启动与首次载入留下的渲染目标与 GC 垃圾
const readyMinimizeDelay = 2 * time.Second

// minimizeMemory 在没有进行中的界面操作时发出 memory-pressure heap-minimize，各进程回收 GC 堆与软件 WebRender 的渲染缓存
func (worker *Worker) minimizeMemory() {
	worker.frameMu.Lock()
	defer worker.frameMu.Unlock()
	if worker.frameBoosts > 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := worker.chromeEvaluate(ctx, "Services.obs.notifyObservers(null, 'memory-pressure', 'heap-minimize')"); err != nil {
		slog.Warn("Camoufox 空闲回收内存失败", "error", err)
	}
}

// boostFrames 在无头 Worker 的界面操作期间把 vsync 分频设为 1 并恢复选中标签页的图层绘制，页面从下一次 vsync 起按 interactiveFrameRate 刷新，可信指针事件像可见标签页一样派发；返回的函数结束本次操作，全部操作结束后恢复 idleFrameDivisor 并停止选中标签页的绘制
func (worker *Worker) boostFrames(ctx context.Context) (func(), error) {
	if !worker.options.Headless || worker.options.liveView {
		return func() {}, nil
	}
	worker.frameMu.Lock()
	defer worker.frameMu.Unlock()
	if worker.frameBoosts == 0 {
		if _, err := worker.chromeEvaluate(ctx, "Services.prefs.setIntPref('gfx.display.frame-rate-divisor', 1); gBrowser.selectedBrowser.renderLayers = true"); err != nil {
			return nil, fmt.Errorf("提高 Camoufox 刷新帧率: %w", err)
		}
		worker.idle.Stop()
	}
	worker.frameBoosts++
	var once sync.Once
	return func() {
		once.Do(func() {
			worker.frameMu.Lock()
			defer worker.frameMu.Unlock()
			worker.frameBoosts--
			if worker.frameBoosts > 0 {
				return
			}
			restoreCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := worker.chromeEvaluate(restoreCtx, idleRenderingExpression); err != nil {
				slog.Warn("恢复 Camoufox 刷新帧率失败", "error", err)
			}
			worker.idle.Reset(idleMinimizeDelay)
		})
	}, nil
}

// browserWindowURL 是浏览器主窗口界面层文档的地址
const browserWindowURL = "chrome://browser/content/browser.xhtml"

// chromeEvaluate 在浏览器主窗口的界面层执行表达式
func (worker *Worker) chromeEvaluate(ctx context.Context, expression string) (map[string]any, error) {
	tree, err := worker.client.command(ctx, "browsingContext.getTree", map[string]any{"maxDepth": 0, "moz:scope": "chrome"})
	if err != nil {
		return nil, err
	}
	contexts, _ := tree["contexts"].([]any)
	for _, item := range contexts {
		window, _ := item.(map[string]any)
		if window["url"] == browserWindowURL {
			return worker.client.evaluate(ctx, window["context"].(string), expression)
		}
	}
	return nil, errors.New("Camoufox BiDi 没有浏览器主窗口")
}

// selectTabExpression 选中 WebDriver BiDi context 为 %q 的标签页
const selectTabExpression = `(() => {
  const { NavigableManager } = ChromeUtils.importESModule("chrome://remote/content/shared/NavigableManager.sys.mjs");
  gBrowser.selectedTab = gBrowser.getTabForBrowser(NavigableManager.getBrowsingContextById(%q).embedderElement);
})()`

// createTab 在后台新建标签页，返回其 context
func (worker *Worker) createTab(ctx context.Context) (string, error) {
	created, err := worker.client.command(ctx, "browsingContext.create", map[string]any{"type": "tab", "background": true})
	if err != nil {
		return "", fmt.Errorf("打开新标签页: %w", err)
	}
	contextID, _ := created["context"].(string)
	if contextID == "" {
		return "", errors.New("Camoufox BiDi 新标签页无效")
	}
	return contextID, nil
}

// lockInput 为前台操作取得 inputMu
func (worker *Worker) lockInput() {
	worker.inputWaiters.Add(1)
	worker.inputMu.Lock()
	worker.inputWaiters.Add(-1)
}

// lockInputIdle 在没有前台操作等待时取得 inputMu，后台新开对话让界面运行先接收输入
func (worker *Worker) lockInputIdle() {
	for {
		worker.inputMu.Lock()
		if worker.inputWaiters.Load() == 0 {
			return
		}
		worker.inputMu.Unlock()
		runtime.Gosched()
	}
}

// selectTab 经浏览器界面层选中标签页并记为选中；调用方持有 inputMu
func (worker *Worker) selectTab(ctx context.Context, contextID string) error {
	if _, err := worker.chromeEvaluate(ctx, fmt.Sprintf(selectTabExpression, contextID)); err != nil {
		return fmt.Errorf("选中新标签页: %w", err)
	}
	worker.mu.Lock()
	worker.selected = contextID
	worker.mu.Unlock()
	return nil
}

// openTab 在后台新建标签页并选中它，返回其 context；调用方持有 inputMu
func (worker *Worker) openTab(ctx context.Context) (string, error) {
	contextID, err := worker.createTab(ctx)
	if err != nil {
		return "", err
	}
	if err := worker.selectTab(ctx, contextID); err != nil {
		return "", errors.Join(err, worker.closeTab(contextID))
	}
	return contextID, nil
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

// PageCatalog 返回当前页面载入时 GetLoggingContext、GetAiStudioBenefitTier 与 ListModels 的响应正文
func (worker *Worker) PageCatalog() PageCatalog {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.pageCatalog
}

// Reload 像官网用户一样在当前标签页中按官网冷启动重新载入页面；当前标签页有进行中的请求时改在新标签页中载入并切换过去，原标签页在其中的请求结束后关闭
func (worker *Worker) Reload(ctx context.Context) error {
	worker.mu.Lock()
	if worker.closed {
		worker.mu.Unlock()
		return errors.New("Camoufox runtime 已关闭")
	}
	options := worker.options
	worker.mu.Unlock()
	options.StartupProgress = nil
	worker.lockInput()
	defer worker.inputMu.Unlock()
	worker.mu.Lock()
	contextID := worker.contextID
	busy := worker.tabs[contextID] != nil
	worker.mu.Unlock()
	if busy {
		opened, err := worker.openTab(ctx)
		if err != nil {
			return err
		}
		contextID = opened
	}
	loaded, err := worker.openPage(ctx, options, contextID)
	if err == nil && options.Headless && !options.liveView {
		err = worker.stopRendering(ctx)
	}
	if err != nil && busy {
		return errors.Join(err, worker.closeTab(contextID))
	}
	if err != nil {
		return err
	}
	return worker.switchTab(contextID, loaded)
}

// switchTab 切换到已载入页面的标签页；页面在原标签页中重新载入时不关闭标签页，否则原标签页没有进行中的受保护请求时立即关闭，有时在其中最后一个请求结束后关闭
func (worker *Worker) switchTab(contextID string, loaded loadedPage) error {
	worker.mu.Lock()
	previous := worker.contextID
	worker.contextID, worker.page, worker.state, worker.pageCatalog = contextID, loaded.page, loaded.state, loaded.catalog
	worker.chats[0], worker.selected = &chatTab{contextID: contextID, page: loaded.page}, contextID
	if previous == contextID {
		worker.mu.Unlock()
		return nil
	}
	use := worker.tabs[previous]
	if use != nil {
		use.retired = true
	}
	worker.mu.Unlock()
	if use == nil {
		return worker.closeTab(previous)
	}
	return nil
}

// beginRequest 记下当前标签页中开始的一个受保护请求，返回该标签页与其中的 WAA 页面对象
func (worker *Worker) beginRequest() (string, pageHandle) {
	worker.useTab(worker.contextID)
	return worker.contextID, worker.page
}

// useTab 记下 contextID 标签页中开始的一个请求，调用方持有 mu
func (worker *Worker) useTab(contextID string) {
	use := worker.tabs[contextID]
	if use == nil {
		use = &tabUse{}
		worker.tabs[contextID] = use
	}
	use.requests++
}

// finishRequest 结束标签页中的一个受保护请求，已被替换的标签页在其中最后一个请求结束后关闭
func (worker *Worker) finishRequest(contextID string) {
	worker.mu.Lock()
	use := worker.tabs[contextID]
	use.requests--
	idle := use.requests == 0
	if idle {
		delete(worker.tabs, contextID)
	}
	closing := idle && use.retired && !worker.closed
	worker.mu.Unlock()
	if closing {
		if err := worker.closeTab(contextID); err != nil {
			slog.Warn("关闭已替换的 Camoufox 标签页失败", "context", contextID, "error", err)
		}
	}
}

// closeTab 关闭标签页
func (worker *Worker) closeTab(contextID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := worker.client.command(ctx, "browsingContext.close", map[string]any{"context": contextID}); err != nil {
		return fmt.Errorf("关闭标签页: %w", err)
	}
	return nil
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
	worker.lockInput()
	defer worker.inputMu.Unlock()
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
	if worker.idle != nil {
		worker.idle.Stop()
		worker.settle.Stop()
	}
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

// restoreCookies 按 storage state 的顺序写入 Cookie 并记下各 Cookie 的序号
func (worker *Worker) restoreCookies(ctx context.Context, cookies []storageCookie) error {
	worker.cookieOrder = make(map[cookieKey]int, len(cookies))
	for index, cookie := range cookies {
		worker.cookieOrder[cookie.key()] = index
	}
	return worker.client.installCookies(ctx, cookies)
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
		func() error { return worker.restoreCookies(ctx, storage.Cookies) },
	); err != nil {
		return err
	}
	loaded, err := worker.openPage(ctx, options, contextID)
	if err != nil {
		return err
	}
	worker.page, worker.state, worker.pageCatalog = loaded.page, loaded.state, loaded.catalog
	worker.chats, worker.selected = []*chatTab{{contextID: contextID, page: loaded.page}}, contextID
	return nil
}

// loadedPage 是一次官网页面载入取得的 WAA 页面对象、页面状态与目录 RPC 响应
type loadedPage struct {
	page    pageHandle
	state   State
	catalog PageCatalog
}

// openPage 在 contextID 标签页中按官网冷启动载入新对话页：preload 在官网 WAA service 首次取请求头时交出该实例，页面载入期间记下目录 RPC 响应与第一个带 x-aistudio-visit-id 的 MakerSuite 请求的公共头，等官方 WAA 程序的图片请求结束或到达上限后返回页面对象
func (worker *Worker) openPage(ctx context.Context, options Options, contextID string) (loadedPage, error) {
	client := worker.client
	serviceChannel := rand.Text()
	services, err := client.openChannel(serviceChannel)
	if err != nil {
		return loadedPage{}, err
	}
	defer client.closeChannel(serviceChannel)
	client.startLoad(contextID)
	defer client.endLoad(contextID)
	var catalogCollector, preloadScript string
	var subscribed, released bool
	// release 移除载入期间登记的响应收集器、preload 与 network 事件订阅，原请求超时或取消后仍以独立期限执行
	release := func() error {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pageReleaseTimeout)
		defer cancel()
		var removals []func() error
		if catalogCollector != "" {
			removals = append(removals, func() error {
				if _, err := client.command(releaseCtx, "network.removeDataCollector", map[string]any{"collector": catalogCollector}); err != nil {
					return fmt.Errorf("移除页面目录响应收集: %w", err)
				}
				return nil
			})
		}
		if preloadScript != "" {
			removals = append(removals, func() error {
				if _, err := client.command(releaseCtx, "script.removePreloadScript", map[string]any{"script": preloadScript}); err != nil {
					return fmt.Errorf("移除官网 WAA service preload: %w", err)
				}
				return nil
			})
		}
		if subscribed {
			removals = append(removals, func() error {
				_, err := client.command(releaseCtx, "session.unsubscribe", map[string]any{
					"events":   []string{"network.beforeRequestSent", "network.responseCompleted", "network.fetchError"},
					"contexts": []string{contextID},
				})
				return err
			})
		}
		released = true
		return parallel(removals...)
	}
	defer func() {
		if !released {
			_ = release()
		}
	}()
	if err := parallel(
		func() error {
			_, err := client.command(ctx, "session.subscribe", map[string]any{
				"events":   []string{"browsingContext.navigationStarted", "script.message", "network.beforeRequestSent", "network.responseCompleted", "network.fetchError"},
				"contexts": []string{contextID},
			})
			subscribed = err == nil
			return err
		},
		func() error {
			collector, err := client.command(ctx, "network.addDataCollector", map[string]any{
				"dataTypes":          []string{"response"},
				"maxEncodedDataSize": collectedBodyLimit,
				"contexts":           []string{contextID},
			})
			if err != nil {
				return fmt.Errorf("安装页面目录响应收集: %w", err)
			}
			catalogCollector, _ = collector["collector"].(string)
			return nil
		},
		func() error {
			preload, err := client.command(ctx, "script.addPreloadScript", map[string]any{
				"functionDeclaration": servicePreloadFunction,
				"arguments": []map[string]any{{"type": "channel", "value": map[string]any{
					"channel": serviceChannel, "ownership": "root", "serializationOptions": map[string]any{"maxObjectDepth": 0},
				}}},
				"contexts": []string{contextID},
			})
			if err != nil {
				return fmt.Errorf("安装官网 WAA service preload: %w", err)
			}
			preloadScript, _ = preload["script"].(string)
			return nil
		},
	); err != nil {
		return loadedPage{}, err
	}
	options.reportStartup(StartupLoadingAIStudio)
	pagePath := "/prompts/new_chat"
	if options.AuthUser > 0 {
		pagePath = fmt.Sprintf("/u/%d/prompts/new_chat", options.AuthUser)
	}
	target := aiStudioOrigin + pagePath + "?model=" + url.QueryEscape(options.Model) + "&temporary=true"
	pageCtx, stopWatch := watchProxy(ctx, options.Proxy)
	defer stopWatch()
	if _, err := client.command(pageCtx, "browsingContext.navigate", map[string]any{
		"context": contextID,
		"url":     target,
		"wait":    "interactive",
	}); err != nil && !strings.Contains(err.Error(), "NS_ERROR_ABORT") {
		if cause := context.Cause(pageCtx); errors.Is(cause, ErrProxyUnreachable) {
			return loadedPage{}, cause
		}
		if strings.Contains(err.Error(), "NS_ERROR_PROXY_") {
			return loadedPage{}, fmt.Errorf("%w: 导航 AI Studio: %v", ErrProxyUnreachable, err)
		}
		return loadedPage{}, fmt.Errorf("导航 AI Studio: %w", err)
	}
	if err := client.waitFor(pageCtx, contextID, workerPageReadyExpression, 120*time.Second); err != nil {
		if cause := context.Cause(pageCtx); errors.Is(cause, ErrProxyUnreachable) {
			return loadedPage{}, cause
		}
		if errors.Is(err, ErrRegionNotSupported) {
			return loadedPage{}, err
		}
		pageURL, _ := client.evaluateString(ctx, contextID, "location.href")
		return loadedPage{}, fmt.Errorf("AI Studio 输入框未就绪 url=%s: %w", pageURL, err)
	}
	stopWatch()
	pageURL, err := client.evaluateString(ctx, contextID, "location.href")
	if err != nil {
		return loadedPage{}, err
	}
	if location, parseErr := url.Parse(pageURL); parseErr == nil && location.Hostname() == "accounts.google.com" {
		return loadedPage{}, fmt.Errorf("%w url=%s", ErrAuthenticationRequired, pageURL)
	}
	if err := client.waitFor(ctx, contextID, loggingContextReadyExpression, 30*time.Second); err != nil {
		return loadedPage{}, fmt.Errorf("等待官网 GetLoggingContext 响应: %w", err)
	}
	catalog, err := client.pageCatalog(ctx, contextID, catalogCollector, 30*time.Second)
	if err != nil {
		return loadedPage{}, err
	}
	options.reportStartup(StartupLocatingWAA)
	serviceCtx, cancelService := context.WithTimeout(ctx, 30*time.Second)
	service, err := services.next(serviceCtx)
	cancelService()
	if err != nil {
		return loadedPage{}, fmt.Errorf("等待官网 WAA service: %w", err)
	}
	if err := client.waitLoad(ctx, contextID, 30*time.Second, "发出带 x-aistudio-visit-id 的 MakerSuite 请求", func(load *pageLoad) bool { return load.headers != nil }); err != nil {
		return loadedPage{}, err
	}
	options.reportStartup(StartupBootstrappingWAA)
	if err := client.waitLoad(ctx, contextID, pageImageTimeout, "完成官方 WAA 程序的图片请求", func(load *pageLoad) bool { return load.image }); err != nil {
		if ctx.Err() != nil {
			return loadedPage{}, err
		}
		slog.Warn("官方 WAA 程序的图片请求没有结束，继续载入页面", "error", err)
	}
	client.mu.Lock()
	observed := client.loads[contextID].headers
	client.mu.Unlock()
	if err := release(); err != nil {
		return loadedPage{}, err
	}
	if _, err := dismissKnownOverlays(ctx, client, contextID); err != nil {
		return loadedPage{}, err
	}
	page, err := client.callFunctionHandle(ctx, contextID, pageObjectFunction, pageHandle(service))
	if err != nil {
		return loadedPage{}, fmt.Errorf("构造官网 WAA 页面对象: %w", err)
	}
	if page == "" {
		return loadedPage{}, errors.New("官网高层 snapshot 函数不存在")
	}
	snapshotKey, err := client.callFunctionString(ctx, contextID, `page => page.key`, page)
	if err != nil {
		return loadedPage{}, fmt.Errorf("读取官网 snapshot 函数名: %w", err)
	}
	headers := make(http.Header, len(publicHeaderNames))
	for _, name := range publicHeaderNames {
		if value := observed[name]; value != "" {
			headers.Set(name, value)
		}
	}
	for _, name := range []string{"user-agent", "x-goog-api-key", "x-goog-authuser", "x-user-agent"} {
		if headers.Get(name) == "" {
			return loadedPage{}, fmt.Errorf("官网 MakerSuite 请求缺少必要公共头 %s", name)
		}
	}
	return loadedPage{page: page, catalog: catalog, state: State{
		PID:         worker.process.command.Process.Pid,
		PageURL:     pageURL,
		UserAgent:   headers.Get("user-agent"),
		SnapshotKey: snapshotKey,
		Headers:     headers,
	}}, nil
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
