package chromeauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/gorilla/websocket"
)

// bidiGoogleCookies 构造满足 aistudio.ParseCookieText 校验的 BiDi Cookie 样本，有效期相对当前时间生成
func bidiGoogleCookies() []map[string]any {
	expiry := float64(time.Now().Add(24 * time.Hour).Unix())
	return []map[string]any{
		{
			"name": "__Secure-1PSID", "value": map[string]string{"type": "string", "value": "sid-value"},
			"domain": ".google.com", "path": "/", "secure": true, "httpOnly": true, "sameSite": "none", "expiry": expiry,
		},
		{
			"name": "SAPISID", "value": map[string]string{"type": "string", "value": "sapisid-value"},
			"domain": ".google.com", "path": "/", "secure": true, "httpOnly": true, "sameSite": "lax",
		},
		{
			"name": "__Secure-1PAPISID", "value": map[string]string{"type": "string", "value": "papisid1"},
			"domain": ".google.com", "path": "/", "secure": true, "httpOnly": true, "sameSite": "none", "expiry": expiry,
		},
		{
			"name": "__Secure-3PAPISID", "value": map[string]string{"type": "string", "value": "papisid3"},
			"domain": ".google.com", "path": "/", "secure": true, "httpOnly": true, "sameSite": "none", "expiry": expiry,
		},
		{
			"name": "BiDiBase64", "value": map[string]string{"type": "base64", "value": "aGVsbG8="},
			"domain": ".google.com", "path": "/", "secure": true,
		},
		// 模拟 Firefox 按 sourceOrigin 返回时在未分区项之后追加的同站分区同名 Cookie
		{
			"name": "__Secure-1PSID", "value": map[string]string{"type": "string", "value": "same-origin-partitioned"},
			"domain": ".google.com", "path": "/", "secure": true, "httpOnly": true, "sameSite": "none", "expiry": expiry,
		},
	}
}

type fakeBiDiOptions struct {
	mode               string
	blockCookies       chan struct{}
	cookiesRequested   chan struct{}
	failCookiesMessage string
}

// newBiDiRemoteServer 启动模拟 Firefox WebDriver BiDi 服务端，按真实 Firefox 语义只允许同时存在一个活跃会话，断线不自动销毁会话
func newBiDiRemoteServer(t *testing.T, opts fakeBiDiOptions) (*httptest.Server, <-chan bool) {
	t.Helper()
	upgrader := websocket.Upgrader{}
	ended := make(chan bool, 4)
	var mu sync.Mutex
	activeSession := false
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, _ *http.Request) {
		switch opts.mode {
		case "firefox-both":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"Browser":              "Firefox/128.0.2",
				"webSocketDebuggerUrl": "ws://invalid-cdp.example.invalid/devtools/browser/firefox",
			})
		default:
			http.NotFound(w, nil)
		}
	})
	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("升级 BiDi WebSocket 失败: %v", err)
			return
		}
		defer conn.Close()
		var writeMu sync.Mutex
		write := func(payload any) error {
			writeMu.Lock()
			defer writeMu.Unlock()
			return conn.WriteJSON(payload)
		}
		for {
			var message struct {
				ID     int            `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if err := conn.ReadJSON(&message); err != nil {
				return
			}
			switch message.Method {
			case "session.new":
				mu.Lock()
				already := activeSession
				if !already {
					activeSession = true
				}
				mu.Unlock()
				if already {
					_ = write(map[string]any{
						"id": message.ID, "error": "session not created", "message": "Maximum number of active sessions",
					})
					continue
				}
				_ = write(map[string]any{
					"id": message.ID,
					"result": map[string]any{
						"sessionId": "test-session",
						"capabilities": map[string]any{
							"browserName": "firefox", "browserVersion": "128.0.2",
						},
					},
				})
			case "storage.getCookies":
				if opts.cookiesRequested != nil {
					select {
					case opts.cookiesRequested <- struct{}{}:
					default:
					}
				}
				if opts.blockCookies != nil {
					// 在独立协程中等待放行，确保同一连接上后续到达的 session.end 可被读取循环及时处理
					msgID := message.ID
					params := message.Params
					go func() {
						<-opts.blockCookies
						_ = write(map[string]any{
							"id":     msgID,
							"result": map[string]any{"cookies": fakeBiDiCookiesForParams(params)},
						})
					}()
					continue
				}
				if opts.failCookiesMessage != "" {
					_ = write(map[string]any{
						"id": message.ID, "error": "unknown error", "message": opts.failCookiesMessage,
					})
					continue
				}
				_ = write(map[string]any{
					"id":     message.ID,
					"result": map[string]any{"cookies": fakeBiDiCookiesForParams(message.Params)},
				})
			case "session.end":
				mu.Lock()
				activeSession = false
				mu.Unlock()
				ended <- len(message.Params) == 0
				if opts.mode == "stall-end" {
					var discard any
					_ = conn.ReadJSON(&discard)
					return
				}
				_ = write(map[string]any{"id": message.ID, "result": map[string]any{}})
				return
			default:
				_ = write(map[string]any{
					"id": message.ID, "error": "unknown command", "message": message.Method,
				})
				return
			}
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, ended
}

// fakeBiDiCookiesForParams 按真实 Firefox 行为校验 partition 参数，缺 default 容器或 AI Studio 来源时追加混淆 Cookie
func fakeBiDiCookiesForParams(params map[string]any) []map[string]any {
	expiry := float64(time.Now().Add(24 * time.Hour).Unix())
	cookies := bidiGoogleCookies()
	partition, _ := params["partition"].(map[string]any)
	if partition["type"] != "storageKey" || partition["sourceOrigin"] != "https://aistudio.google.com" {
		// 未限定 AI Studio 存储分区时返回第三方分区 Cookie 放在最前以模拟污染
		return append([]map[string]any{{
			"name": "__Secure-1PSID", "value": map[string]string{"type": "string", "value": "third-party-partitioned"},
			"domain": ".google.com", "path": "/", "secure": true, "httpOnly": true, "sameSite": "none", "expiry": expiry,
		}}, cookies...)
	}
	if partition["userContext"] != "default" {
		// 未限定默认容器时混入其他容器的会话 Cookie
		return append([]map[string]any{{
			"name": "__Secure-1PSID", "value": map[string]string{"type": "string", "value": "other-container-sid"},
			"domain": ".google.com", "path": "/", "secure": true, "httpOnly": true, "sameSite": "none", "expiry": expiry,
		}}, cookies...)
	}
	return cookies
}

// waitSessionEnded 在有界时间内等待服务端收到 session.end，避免漏发时测试挂死
func waitSessionEnded(t *testing.T, ended <-chan bool) {
	t.Helper()
	select {
	case emptyParams := <-ended:
		if !emptyParams {
			t.Fatal("session.end 应传入空参数对象")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("等待服务端收到 session.end 超时")
	}
}

// newCDPRemoteServer 启动只说 CDP 的模拟 Chromium 调试端口
func newCDPRemoteServer(t *testing.T, failCookies bool) *httptest.Server {
	t.Helper()
	expiry := float64(time.Now().Add(24 * time.Hour).Unix())
	upgrader := websocket.Upgrader{}
	mux := http.NewServeMux()
	mux.HandleFunc("/devtools/browser/test", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("升级 CDP WebSocket 失败: %v", err)
			return
		}
		defer conn.Close()
		for {
			var message struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if err := conn.ReadJSON(&message); err != nil {
				return
			}
			if failCookies || message.Method != "Storage.getCookies" {
				_ = conn.WriteJSON(map[string]any{"id": message.ID, "error": map[string]string{"message": "storage unavailable"}})
				return
			}
			_ = conn.WriteJSON(map[string]any{
				"id": message.ID,
				"result": map[string]any{"cookies": []map[string]any{
					{
						"name": "__Secure-1PSID", "value": "sid-value", "domain": ".google.com",
						"path": "/", "expires": expiry, "httpOnly": true, "secure": true, "sameSite": "None",
					},
					{
						"name": "SAPISID", "value": "sapisid-value", "domain": ".google.com",
						"path": "/", "session": true, "httpOnly": true, "secure": true, "sameSite": "Lax",
					},
					{
						"name": "__Secure-1PAPISID", "value": "papisid1", "domain": ".google.com",
						"path": "/", "expires": expiry, "httpOnly": true, "secure": true,
					},
					{
						"name": "__Secure-3PAPISID", "value": "papisid3", "domain": ".google.com",
						"path": "/", "expires": expiry, "httpOnly": true, "secure": true,
					},
					{
						"name": "PartitionedCookie", "value": "partitioned", "domain": ".google.com",
						"path": "/", "secure": true, "partitionKey": "https://aistudio.google.com",
					},
				}},
			})
		}
	})
	var server *httptest.Server
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/131.0.0.0",
			"webSocketDebuggerUrl": "ws://" + strings.TrimPrefix(server.URL, "http://") + "/devtools/browser/test",
		})
	})
	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestReadBrowserCookiesBiDiFallback(t *testing.T) {
	server, ended := newBiDiRemoteServer(t, fakeBiDiOptions{mode: "bidi-only"})
	address := strings.TrimPrefix(server.URL, "http://")

	browser, err := ReadBrowserCookies(context.Background(), address)
	if err != nil {
		t.Fatalf("BiDi 读取失败: %v", err)
	}
	if browser.Product != "Firefox/128.0.2" {
		t.Fatalf("产品名 = %q，期望 Firefox/128.0.2", browser.Product)
	}
	waitSessionEnded(t, ended)
	state := assertExportedCookieState(t, browser.Cookies)
	var sawBase64 bool
	for _, cookie := range state.Cookies {
		if cookie.Name == "__Secure-1PSID" && cookie.Value != "sid-value" {
			t.Fatalf("__Secure-1PSID = %q，期望未分区的 sid-value", cookie.Value)
		}
		if cookie.Name == "BiDiBase64" {
			if cookie.Value != "hello" {
				t.Fatalf("base64 Cookie 值 = %q，期望 hello", cookie.Value)
			}
			sawBase64 = true
		}
	}
	if !sawBase64 {
		t.Fatal("缺少解码后的 BiDiBase64 Cookie")
	}
}

func TestReadBrowserCookiesFirefoxWithBothProtocols(t *testing.T) {
	server, ended := newBiDiRemoteServer(t, fakeBiDiOptions{mode: "firefox-both"})
	address := strings.TrimPrefix(server.URL, "http://")

	browser, err := ReadBrowserCookies(context.Background(), address)
	if err != nil {
		t.Fatalf("双协议 Firefox 读取失败: %v", err)
	}
	if browser.Product != "Firefox/128.0.2" {
		t.Fatalf("产品名 = %q，期望 Firefox/128.0.2", browser.Product)
	}
	waitSessionEnded(t, ended)
}

func TestReadBrowserCookiesDirectBiDiURL(t *testing.T) {
	for _, suffix := range []string{"", "/", "/session", "/session/"} {
		t.Run("path="+suffix, func(t *testing.T) {
			server, ended := newBiDiRemoteServer(t, fakeBiDiOptions{mode: "bidi-only"})
			address := "ws://" + strings.TrimPrefix(server.URL, "http://") + suffix

			browser, err := ReadBrowserCookies(context.Background(), address)
			if err != nil {
				t.Fatalf("直连 BiDi WebSocket (%q) 失败: %v", address, err)
			}
			if browser.Product != "Firefox/128.0.2" {
				t.Fatalf("产品名 = %q，期望 Firefox/128.0.2", browser.Product)
			}
			waitSessionEnded(t, ended)
		})
	}
}

func TestReadBrowserCookiesBiDiCancelReleasesSession(t *testing.T) {
	block := make(chan struct{})
	requested := make(chan struct{}, 1)
	server, ended := newBiDiRemoteServer(t, fakeBiDiOptions{
		mode:             "bidi-only",
		blockCookies:     block,
		cookiesRequested: requested,
	})
	address := strings.TrimPrefix(server.URL, "http://")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := ReadBrowserCookies(ctx, address)
		done <- err
	}()
	select {
	case <-requested:
	case <-time.After(2 * time.Second):
		t.Fatal("等待 storage.getCookies 请求超时")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消后的错误 = %v，期望 context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("取消后 ReadBrowserCookies 未及时返回")
	}
	waitSessionEnded(t, ended)
	close(block)

	// 会话已正确结束，第二次扫描必须能立即新建会话并读出 Cookie
	browser, err := ReadBrowserCookies(context.Background(), address)
	if err != nil {
		t.Fatalf("取消后第二次 BiDi 读取失败: %v", err)
	}
	if browser.Product != "Firefox/128.0.2" {
		t.Fatalf("产品名 = %q，期望 Firefox/128.0.2", browser.Product)
	}
	waitSessionEnded(t, ended)
}

func TestReadBrowserCookiesBiDiSessionEndTimeout(t *testing.T) {
	previous := bidiSessionEndTimeout
	bidiSessionEndTimeout = 50 * time.Millisecond
	t.Cleanup(func() { bidiSessionEndTimeout = previous })

	server, ended := newBiDiRemoteServer(t, fakeBiDiOptions{mode: "stall-end"})
	started := time.Now()
	browser, err := ReadBrowserCookies(context.Background(), strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatalf("BiDi 读取失败: %v", err)
	}
	if browser.Product != "Firefox/128.0.2" {
		t.Fatalf("产品名 = %q，期望 Firefox/128.0.2", browser.Product)
	}
	waitSessionEnded(t, ended)
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("session.end 阻塞过久: %v", elapsed)
	}
}

func TestReadBrowserCookiesPreservesProductOnReadError(t *testing.T) {
	cdpServer := newCDPRemoteServer(t, true)
	cdpBrowser, err := ReadBrowserCookies(context.Background(), strings.TrimPrefix(cdpServer.URL, "http://"))
	if err == nil {
		t.Fatal("CDP Cookie 读取失败应当返回错误")
	}
	if cdpBrowser.Product != "Chrome/131.0.0.0" {
		t.Fatalf("CDP 失败时产品名 = %q，期望保留 Chrome/131.0.0.0", cdpBrowser.Product)
	}

	bidiServer, ended := newBiDiRemoteServer(t, fakeBiDiOptions{mode: "bidi-only", failCookiesMessage: "storage error"})
	bidiBrowser, err := ReadBrowserCookies(context.Background(), strings.TrimPrefix(bidiServer.URL, "http://"))
	if err == nil {
		t.Fatal("BiDi Cookie 读取失败应当返回错误")
	}
	if bidiBrowser.Product != "Firefox/128.0.2" {
		t.Fatalf("BiDi 失败时产品名 = %q，期望保留 Firefox/128.0.2", bidiBrowser.Product)
	}
	waitSessionEnded(t, ended)
}

func TestReadBrowserCookiesVersionReadErrorDoesNotFallbackToBiDi(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			t.Errorf("收到非预期的 BiDi 回退请求 %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"Browser":"Chrome"`))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		if hijacker, ok := w.(http.Hijacker); ok {
			conn, _, err := hijacker.Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}
	}))
	t.Cleanup(server.Close)

	_, err := ReadBrowserCookies(context.Background(), strings.TrimPrefix(server.URL, "http://"))
	if err == nil {
		t.Fatal("中断的 /json/version 响应应当返回错误")
	}
	if !strings.Contains(err.Error(), "读取浏览器调试信息") || strings.Contains(err.Error(), "WebDriver BiDi") {
		t.Fatalf("错误 = %q，期望保留读取错误而不回退 BiDi", err.Error())
	}
}

func TestReadBrowserCookiesCDP(t *testing.T) {
	server := newCDPRemoteServer(t, false)
	address := strings.TrimPrefix(server.URL, "http://")

	browser, err := ReadBrowserCookies(context.Background(), address)
	if err != nil {
		t.Fatalf("CDP 读取失败: %v", err)
	}
	if browser.Product != "Chrome/131.0.0.0" {
		t.Fatalf("产品名 = %q，期望 Chrome/131.0.0.0", browser.Product)
	}
	var cookies []map[string]any
	if err := json.Unmarshal([]byte(browser.Cookies), &cookies); err != nil {
		t.Fatalf("Cookie JSON 无效: %v", err)
	}
	for _, cookie := range cookies {
		if cookie["name"] == "PartitionedCookie" {
			t.Fatal("分区 Cookie 没有被过滤")
		}
	}
	assertExportedCookieState(t, browser.Cookies)
}

func TestReadBrowserCookiesUnreachable(t *testing.T) {
	listener := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := strings.TrimPrefix(listener.URL, "http://")
	listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := ReadBrowserCookies(ctx, address); err == nil {
		t.Fatal("不可达的调试地址应当返回错误")
	} else if !strings.Contains(err.Error(), "连接浏览器调试地址") {
		t.Fatalf("错误 = %q，期望包含连接失败说明", err.Error())
	}
}

// assertExportedCookieState 验证 Cookie 文本可被存储状态解析、登录 Cookie 齐全且分区 Cookie 被过滤，返回解析后的状态
func assertExportedCookieState(t *testing.T, text string) aistudio.StorageState {
	t.Helper()
	state, err := aistudio.ParseCookieText(text)
	if err != nil {
		t.Fatalf("Cookie 解析为存储状态失败: %v", err)
	}
	names := make(map[string]string, len(state.Cookies))
	for _, cookie := range state.Cookies {
		names[cookie.Name] = cookie.Value
	}
	for _, required := range []string{"__Secure-1PSID", "SAPISID", "__Secure-1PAPISID", "__Secure-3PAPISID"} {
		if _, ok := names[required]; !ok {
			t.Fatalf("缺少必需 Cookie %s，实际有 %v", required, names)
		}
	}
	if _, ok := names["PartitionedCookie"]; ok {
		t.Fatal("分区 Cookie 不应进入存储状态")
	}
	return state
}

func TestBiDiCookiesToExport(t *testing.T) {
	expiry := 1900000000.0
	items := []bidiCookie{
		{
			Name: "Persistent", Domain: ".google.com", Path: "/", Secure: true, SameSite: "lax", Expiry: &expiry,
			Value: bidiBytesValue{Type: "string", Value: "persisted"},
		},
		{
			// 同键后出现的项模拟同站分区副本，应保留先出现的未分区值
			Name: "Persistent", Domain: ".google.com", Path: "/", Secure: true, SameSite: "lax", Expiry: &expiry,
			Value: bidiBytesValue{Type: "string", Value: "partitioned-duplicate"},
		},
		{
			Name: "Session", Domain: ".google.com",
			Value: bidiBytesValue{Type: "string", Value: "session"},
		},
		{
			Name: "Encoded", Domain: ".google.com",
			Value: bidiBytesValue{Type: "base64", Value: "aGVsbG8="},
		},
		{Name: "", Domain: ".google.com"},
	}
	cookies, err := bidiCookiesToExport(items)
	if err != nil {
		t.Fatalf("转换 BiDi Cookie 失败: %v", err)
	}
	names := make(map[string]aistudio.StateCookie, len(cookies))
	for _, cookie := range cookies {
		names[cookie.Name] = cookie
	}
	if len(cookies) != 3 {
		t.Fatalf("保留 Cookie 数 = %d，期望 3", len(cookies))
	}
	if names["Persistent"].Expires != expiry || names["Persistent"].Value != "persisted" {
		t.Fatalf("持久 Cookie 转换错误: %+v", names["Persistent"])
	}
	if names["Session"].Expires != -1 {
		t.Fatalf("会话 Cookie Expires = %v，期望 -1", names["Session"].Expires)
	}
	if names["Session"].Path != "/" {
		t.Fatal("空路径应补为 /")
	}
	if names["Encoded"].Value != "hello" {
		t.Fatalf("base64 值 = %q，期望 hello", names["Encoded"].Value)
	}

	invalid := []bidiCookie{{
		Name:  "BadBase64",
		Domain: ".google.com",
		Value: bidiBytesValue{Type: "base64", Value: "not-base64!"},
	}}
	if _, err := bidiCookiesToExport(invalid); err == nil {
		t.Fatal("非法 base64 应当返回错误")
	}
}

func TestDebugAddress(t *testing.T) {
	userData := t.TempDir()
	if err := os.WriteFile(filepath.Join(userData, "DevToolsActivePort"), []byte("9333\n/devtools/browser/x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	extFirefox := t.TempDir()
	if err := os.WriteFile(filepath.Join(extFirefox, "WebDriverBiDiServer.json"), []byte(`{"ws_host":"127.0.0.1","ws_port":9444}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ownCamoufox := filepath.Join(t.TempDir(), "aistudio-camoufox-12345")
	if err := os.MkdirAll(ownCamoufox, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ownCamoufox, "WebDriverBiDiServer.json"), []byte(`{"ws_host":"127.0.0.1","ws_port":9555}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lockedCamoufox := t.TempDir()
	if err := os.WriteFile(filepath.Join(lockedCamoufox, ".aistudio2api-profile.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockedCamoufox, "WebDriverBiDiServer.json"), []byte(`{"ws_host":"127.0.0.1","ws_port":9556}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		args   []string
		expect string
	}{
		{name: "等号形式", args: []string{"firefox.exe", "--remote-debugging-port=9222"}, expect: "127.0.0.1:9222"},
		{name: "空格分隔", args: []string{"firefox.exe", "--remote-debugging-port", "9225", "-headless"}, expect: "127.0.0.1:9225"},
		{name: "单短横线等号", args: []string{"firefox.exe", "-remote-debugging-port=9223"}, expect: "127.0.0.1:9223"},
		{name: "单短横线空格", args: []string{"firefox.exe", "-remote-debugging-port", "9224"}, expect: "127.0.0.1:9224"},
		{name: "Firefox 省略端口默认 9222", args: []string{"firefox.exe", "--remote-debugging-port", "-headless"}, expect: "127.0.0.1:9222"},
		{name: "Firefox 末尾省略端口默认 9222", args: []string{"firefox.exe", "-remote-debugging-port"}, expect: "127.0.0.1:9222"},
		{name: "Chromium 端口 0 读 DevToolsActivePort", args: []string{"chrome.exe", "--remote-debugging-port=0", "--user-data-dir=" + userData}, expect: "127.0.0.1:9333"},
		{name: "Chromium 空格参数读 DevToolsActivePort", args: []string{"chrome.exe", "--remote-debugging-port", "0", "--user-data-dir", userData}, expect: "127.0.0.1:9333"},
		{name: "外部 Firefox 端口 0 读 WebDriverBiDiServer.json", args: []string{"firefox.exe", "--remote-debugging-port=0", "-profile", extFirefox}, expect: "127.0.0.1:9444"},
		{name: "内置 Camoufox 前缀目录不纳入扫描", args: []string{"camoufox.exe", "--remote-debugging-port=0", "-no-remote", "-headless", "-profile", ownCamoufox}, expect: ""},
		{name: "内置 Camoufox 锁文件目录不纳入扫描", args: []string{"camoufox.exe", "--remote-debugging-port=0", "-profile", lockedCamoufox}, expect: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := debugAddress(tc.args); got != tc.expect {
				t.Fatalf("debugAddress(%v) = %q，期望 %q", tc.args, got, tc.expect)
			}
		})
	}
}
