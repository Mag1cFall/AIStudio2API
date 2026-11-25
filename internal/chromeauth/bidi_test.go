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

// newBiDiRemoteServer 启动模拟 Firefox WebDriver BiDi 服务端，核对按浏览器选择 BiDi 协议的流程
func newBiDiRemoteServer(t *testing.T, mode string) (*httptest.Server, <-chan bool) {
	t.Helper()
	upgrader := websocket.Upgrader{}
	ended := make(chan bool, 4)
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, _ *http.Request) {
		switch mode {
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
					"result": map[string]any{"cookies": fakeBiDiCookiesForParams(message.Params)},
				})
			case "session.end":
				ended <- len(message.Params) == 0
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

// assertExportedCookieState 验证 Cookie 文本可被存储状态解析且登录 Cookie 齐全
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
	return state
}

// TestReadBrowserCookiesProtocolSelection 按浏览器响应与地址形态核对 WebDriver BiDi 协议选择：404 回退、双协议 Firefox 优先 BiDi 与直连 BiDi WebSocket
func TestReadBrowserCookiesProtocolSelection(t *testing.T) {
	cases := []struct {
		name         string
		mode         string
		address      func(hostPort string) string
		checkCookies bool
	}{
		{name: "bidi-fallback", mode: "bidi-only", address: func(hp string) string { return hp }, checkCookies: true},
		{name: "firefox-both-protocols", mode: "firefox-both", address: func(hp string) string { return hp }},
		{name: "direct-bidi-empty", mode: "bidi-only", address: func(hp string) string { return "ws://" + hp }},
		{name: "direct-bidi-root", mode: "bidi-only", address: func(hp string) string { return "ws://" + hp + "/" }},
		{name: "direct-bidi-session", mode: "bidi-only", address: func(hp string) string { return "ws://" + hp + "/session" }},
		{name: "direct-bidi-session-slash", mode: "bidi-only", address: func(hp string) string { return "ws://" + hp + "/session/" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, ended := newBiDiRemoteServer(t, tc.mode)
			address := tc.address(strings.TrimPrefix(server.URL, "http://"))
			browser, err := ReadBrowserCookies(context.Background(), address)
			if err != nil {
				t.Fatalf("读取失败 (%q): %v", address, err)
			}
			if browser.Product != "Firefox/128.0.2" {
				t.Fatalf("产品名 = %q，期望 Firefox/128.0.2", browser.Product)
			}
			waitSessionEnded(t, ended)
			if tc.checkCookies {
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
		})
	}
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
		Name:   "BadBase64",
		Domain: ".google.com",
		Value:  bidiBytesValue{Type: "base64", Value: "not-base64!"},
	}}
	if _, err := bidiCookiesToExport(invalid); err == nil {
		t.Fatal("非法 base64 应当返回错误")
	}
}
