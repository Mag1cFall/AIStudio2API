package chromeauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/gorilla/websocket"
)

// biDiGoogleCookies 是满足 aistudio.ParseCookieText 登录 Cookie 校验的 BiDi Cookie 集合
func biDiGoogleCookies() []map[string]any {
	return []map[string]any{
		{
			"name": "__Secure-1PSID", "value": map[string]string{"type": "string", "value": "sid-value"},
			"domain": ".google.com", "path": "/", "secure": true, "httpOnly": true, "sameSite": "none", "expiry": 1900000000.0,
		},
		{
			"name": "SAPISID", "value": map[string]string{"type": "string", "value": "sapisid-value"},
			"domain": ".google.com", "path": "/", "secure": true, "httpOnly": true, "sameSite": "lax",
		},
		{
			"name": "__Secure-1PAPISID", "value": map[string]string{"type": "string", "value": "papisid1"},
			"domain": ".google.com", "path": "/", "secure": true, "httpOnly": true, "sameSite": "none", "expiry": 1900000000.0,
		},
		{
			"name": "__Secure-3PAPISID", "value": map[string]string{"type": "string", "value": "papisid3"},
			"domain": ".google.com", "path": "/", "secure": true, "httpOnly": true, "sameSite": "none", "expiry": 1900000000.0,
		},
		{
			// base64 编码的 Cookie 值应被解码
			"name": "BiDiBase64", "value": map[string]string{"type": "base64", "value": "aGVsbG8="},
			"domain": ".google.com", "path": "/", "secure": true,
		},
		{
			// 分区 Cookie 应被过滤
			"name": "PartitionedCookie", "value": map[string]string{"type": "string", "value": "partitioned"},
			"domain": ".google.com", "path": "/", "secure": true,
			"partitionKey": map[string]any{"userContext": "default", "sourceOrigin": "https://aistudio.google.com"},
		},
		{
			// 非 google.com 域的 Cookie 在 ParseCookieText 中被过滤，但 chromeauth 层仍原样返回
			"name": "OtherSite", "value": map[string]string{"type": "string", "value": "other"},
			"domain": "example.com", "path": "/",
		},
	}
}

// newBiDiRemoteServer 启动模拟远程调试浏览器：mode 为 bidi-only 时 /json/version 返回 404，
// 为 firefox-both 时返回带 Firefox 产品名的 CDP 信息，为 chrome 时返回 Chrome 的 CDP 信息
func newBiDiRemoteServer(t *testing.T, mode string) (*httptest.Server, <-chan string) {
	t.Helper()
	upgrader := websocket.Upgrader{}
	ended := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, _ *http.Request) {
		switch mode {
		case "bidi-only":
			http.NotFound(w, nil)
		case "firefox-both":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"Browser":              "Firefox/128.0.2",
				"webSocketDebuggerUrl": "ws://invalid-cdp.example.invalid/devtools/browser/firefox",
			})
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"Browser":              "Chrome/131.0.0.0",
				"webSocketDebuggerUrl": "",
			})
		}
	})
	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("升级 BiDi WebSocket 失败: %v", err)
			return
		}
		defer conn.Close()
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
				_ = conn.WriteJSON(map[string]any{
					"id": message.ID,
					"result": map[string]any{
						"sessionId": "test-session",
						"capabilities": map[string]any{
							"browserName": "firefox", "browserVersion": "128.0.2",
						},
					},
				})
			case "storage.getCookies":
				_ = conn.WriteJSON(map[string]any{
					"id":     message.ID,
					"result": map[string]any{"cookies": biDiGoogleCookies()},
				})
			case "session.end":
				ended <- message.Params["sessionId"].(string)
				_ = conn.WriteJSON(map[string]any{"id": message.ID, "result": map[string]any{}})
				return
			default:
				_ = conn.WriteJSON(map[string]any{
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

// newCDPRemoteServer 启动只说 CDP 的模拟 Chromium 调试端口
func newCDPRemoteServer(t *testing.T) *httptest.Server {
	t.Helper()
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
			if message.Method != "Storage.getCookies" {
				_ = conn.WriteJSON(map[string]any{"id": message.ID, "error": map[string]string{"message": "unknown method"}})
				return
			}
			_ = conn.WriteJSON(map[string]any{
				"id": message.ID,
				"result": map[string]any{"cookies": []map[string]any{
					{
						"name": "__Secure-1PSID", "value": "sid-value", "domain": ".google.com",
						"path": "/", "expires": 1900000000.0, "httpOnly": true, "secure": true, "sameSite": "None",
					},
					{
						"name": "SAPISID", "value": "sapisid-value", "domain": ".google.com",
						"path": "/", "session": true, "httpOnly": true, "secure": true, "sameSite": "Lax",
					},
					{
						"name": "__Secure-1PAPISID", "value": "papisid1", "domain": ".google.com",
						"path": "/", "expires": 1900000000.0, "httpOnly": true, "secure": true,
					},
					{
						"name": "__Secure-3PAPISID", "value": "papisid3", "domain": ".google.com",
						"path": "/", "expires": 1900000000.0, "httpOnly": true, "secure": true,
					},
					{
						"name": "PartitionedCookie", "value": "partitioned", "domain": ".google.com",
						"path": "/", "secure": true, "partitionKey": "https://aistudio.google.com",
					},
				}},
			})
		}
	})
	// /json/version 回报指向当前测试服务器的浏览器级 WebSocket
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
	server, ended := newBiDiRemoteServer(t, "bidi-only")
	address := strings.TrimPrefix(server.URL, "http://")

	browser, err := ReadBrowserCookies(context.Background(), address)
	if err != nil {
		t.Fatalf("BiDi 读取失败: %v", err)
	}
	if browser.Product != "Firefox/128.0.2" {
		t.Fatalf("产品名 = %q，期望 Firefox/128.0.2", browser.Product)
	}
	if sessionID := <-ended; sessionID != "test-session" {
		t.Fatalf("会话结束参数 = %q，期望 test-session", sessionID)
	}
	state := assertExportedCookieState(t, browser.Cookies)
	// base64 解码只发生在 BiDi 路径，CDP 的 Storage.getCookies 返回明文值
	for _, cookie := range state.Cookies {
		if cookie.Name == "BiDiBase64" {
			if cookie.Value != "hello" {
				t.Fatalf("base64 Cookie 值 = %q，期望 hello", cookie.Value)
			}
			return
		}
	}
	t.Fatal("缺少解码后的 BiDiBase64 Cookie")
}

func TestReadBrowserCookiesFirefoxWithBothProtocols(t *testing.T) {
	server, _ := newBiDiRemoteServer(t, "firefox-both")
	address := strings.TrimPrefix(server.URL, "http://")

	browser, err := ReadBrowserCookies(context.Background(), address)
	if err != nil {
		t.Fatalf("双协议 Firefox 读取失败: %v", err)
	}
	if browser.Product != "Firefox/128.0.2" {
		t.Fatalf("产品名 = %q，期望 Firefox/128.0.2", browser.Product)
	}
}

func TestReadBrowserCookiesDirectBiDiURL(t *testing.T) {
	server, ended := newBiDiRemoteServer(t, "bidi-only")
	address := "ws://" + strings.TrimPrefix(server.URL, "http://") + "/session"

	browser, err := ReadBrowserCookies(context.Background(), address)
	if err != nil {
		t.Fatalf("直连 BiDi WebSocket 失败: %v", err)
	}
	if browser.Product != "Firefox/128.0.2" {
		t.Fatalf("产品名 = %q，期望 Firefox/128.0.2", browser.Product)
	}
	if sessionID := <-ended; sessionID != "test-session" {
		t.Fatalf("会话结束参数 = %q，期望 test-session", sessionID)
	}
}

func TestReadBrowserCookiesCDP(t *testing.T) {
	server := newCDPRemoteServer(t)
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
			Value: struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			}{Type: "string", Value: "persisted"},
		},
		{
			Name: "Session", Domain: ".google.com",
			Value: struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			}{Type: "string", Value: "session"},
		},
		{
			Name: "Encoded", Domain: ".google.com",
			Value: struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			}{Type: "base64", Value: "aGVsbG8="},
		},
		{
			Name: "Partitioned", Domain: ".google.com",
			Value: struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			}{Type: "string", Value: "x"},
			PartitionKey: json.RawMessage(`{"userContext":"default"}`),
		},
		{
			Name: "NullPartition", Domain: ".google.com",
			Value: struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			}{Type: "string", Value: "y"},
			PartitionKey: json.RawMessage("null"),
		},
		{Name: "", Domain: ".google.com"},
	}
	cookies, err := biDiCookiesToExport(items)
	if err != nil {
		t.Fatalf("转换 BiDi Cookie 失败: %v", err)
	}
	names := make(map[string]browserCookieExport, len(cookies))
	for _, cookie := range cookies {
		names[cookie.Name] = cookie
	}
	if len(cookies) != 4 {
		t.Fatalf("保留 Cookie 数 = %d，期望 4", len(cookies))
	}
	if names["Persistent"].Expires != expiry || names["Persistent"].Session {
		t.Fatal("持久 Cookie 的过期时间转换错误")
	}
	if names["Session"].Expires != 0 || !names["Session"].Session {
		t.Fatal("会话 Cookie 应标记 session 且没有过期时间")
	}
	if names["Session"].Path != "/" {
		t.Fatal("空路径应补为 /")
	}
	if names["Encoded"].Value != "hello" {
		t.Fatalf("base64 值 = %q，期望 hello", names["Encoded"].Value)
	}
	if names["NullPartition"].SameSite != "" {
		t.Fatal("SameSite 缺省应为空字符串")
	}

	invalid := []bidiCookie{{
		Name: "BadBase64", Domain: ".google.com",
		Value: struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		}{Type: "base64", Value: "not-base64!"},
	}}
	if _, err := biDiCookiesToExport(invalid); err == nil {
		t.Fatal("非法 base64 应当返回错误")
	}
}

func TestBidiSessionEndpoint(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		expect  string
		wantErr bool
	}{
		{name: "主机端口", input: "127.0.0.1:9222", expect: "ws://127.0.0.1:9222/session"},
		{name: "HTTP 地址", input: "http://127.0.0.1:9222", expect: "ws://127.0.0.1:9222/session"},
		{name: "HTTPS 地址", input: "https://127.0.0.1:9222", expect: "wss://127.0.0.1:9222/session"},
		{name: "WS 根路径补 session", input: "ws://127.0.0.1:9222", expect: "ws://127.0.0.1:9222/session"},
		{name: "WS session 保持不变", input: "ws://127.0.0.1:9222/session", expect: "ws://127.0.0.1:9222/session"},
		{name: "无效地址", input: "bad url", wantErr: true},
		{name: "空地址", input: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			endpoint, err := bidiSessionEndpoint(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("输入 %q 应当返回错误", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("输入 %q 返回错误: %v", tc.input, err)
			}
			if endpoint != tc.expect {
				t.Fatalf("端点 = %q，期望 %q", endpoint, tc.expect)
			}
		})
	}
}

func TestBidiProductName(t *testing.T) {
	cases := []struct {
		name, version, expect string
	}{
		{name: "firefox", version: "128.0.2", expect: "Firefox/128.0.2"},
		{name: "camoufox", version: "", expect: "Camoufox"},
		{name: "", version: "128", expect: bidiProductFallback + "/128"},
		{name: "", version: "", expect: bidiProductFallback},
		{name: "Firefox", version: "1", expect: "Firefox/1"},
	}
	for _, tc := range cases {
		if got := bidiProductName(tc.name, tc.version); got != tc.expect {
			t.Fatalf("bidiProductName(%q, %q) = %q，期望 %q", tc.name, tc.version, got, tc.expect)
		}
	}
}
