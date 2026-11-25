package chromeauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
)

// BrowserCookies 是经远程调试地址读取的浏览器产品名与 Cookie JSON 数组文本
type BrowserCookies struct {
	Product string
	Cookies string
}

// ReadBrowserCookies 经远程调试地址读取浏览器 Cookie：Chromium 走 CDP，Firefox 内核浏览器走 WebDriver BiDi
func ReadBrowserCookies(ctx context.Context, address string) (BrowserCookies, error) {
	address = strings.TrimSpace(address)
	if strings.HasPrefix(address, "ws://") || strings.HasPrefix(address, "wss://") {
		if parsed, err := url.Parse(address); err == nil {
			if path := strings.TrimSuffix(parsed.Path, "/"); path == "" || path == "/session" {
				return readBiDiBrowser(ctx, address)
			}
		}
		return readCDPBrowser(ctx, address, "")
	}
	version, status, tryBiDi, err := probeCDPVersion(ctx, address)
	if err == nil {
		if strings.Contains(strings.ToLower(version.Browser), "firefox") {
			return readBiDiBrowser(ctx, address)
		}
		return readCDPBrowser(ctx, version.WebSocketDebuggerURL, version.Browser)
	}
	if !tryBiDi {
		return BrowserCookies{}, err
	}
	bidi, bidiErr := readBiDiBrowser(ctx, address)
	if bidiErr != nil {
		return BrowserCookies{Product: bidi.Product}, fmt.Errorf("浏览器调试地址没有返回调试信息: HTTP %d；WebDriver BiDi 读取失败: %w", status, bidiErr)
	}
	return bidi, nil
}

// readCDPBrowser 经浏览器级 CDP WebSocket 读取浏览器全部未分区 Cookie
func readCDPBrowser(ctx context.Context, endpoint, product string) (BrowserCookies, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return BrowserCookies{Product: product}, fmt.Errorf("连接浏览器调试地址: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	session := &cdpSession{conn: conn}
	var stored struct {
		Cookies []map[string]any `json:"cookies"`
	}
	if err := session.call(ctx, "Storage.getCookies", nil, &stored); err != nil {
		return BrowserCookies{Product: product}, fmt.Errorf("读取浏览器 Cookie: %w", err)
	}
	cookies := make([]map[string]any, 0, len(stored.Cookies))
	for _, cookie := range stored.Cookies {
		if cookie["partitionKey"] == nil {
			cookies = append(cookies, cookie)
		}
	}
	data, err := json.Marshal(cookies)
	if err != nil {
		return BrowserCookies{Product: product}, fmt.Errorf("编码浏览器 Cookie: %w", err)
	}
	return BrowserCookies{Product: product, Cookies: string(data)}, nil
}

// cdpSession 在一条浏览器级 CDP 连接上依次发送命令
type cdpSession struct {
	conn *websocket.Conn
	id   int
}

// call 发送一条命令并把同一 id 的结果解码到 result
func (session *cdpSession) call(ctx context.Context, method string, params any, result any) error {
	session.id++
	message := map[string]any{"id": session.id, "method": method}
	if params != nil {
		message["params"] = params
	}
	if err := session.conn.WriteJSON(message); err != nil {
		return err
	}
	for {
		var response struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := session.conn.ReadJSON(&response); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if response.ID != session.id {
			continue
		}
		if response.Error != nil {
			return errors.New(response.Error.Message)
		}
		return json.Unmarshal(response.Result, result)
	}
}

// cdpVersion 是浏览器调试接口 /json/version 的产品名与浏览器级 WebSocket 地址
type cdpVersion struct {
	Browser              string `json:"Browser"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// parseDebugHTTPURL 解析 host:port 或 http(s):// 远程调试地址
func parseDebugHTTPURL(address string) (*url.URL, error) {
	address = strings.TrimSpace(address)
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("浏览器调试地址无效，例如 127.0.0.1:9222")
	}
	return parsed, nil
}

// probeCDPVersion 读取 host:port 或 HTTP 调试地址的 /json/version，仅在完整收到非 CDP 响应时置 tryBiDi 为真
func probeCDPVersion(ctx context.Context, address string) (cdpVersion, int, bool, error) {
	parsed, err := parseDebugHTTPURL(address)
	if err != nil {
		return cdpVersion{}, 0, false, err
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, parsed.Scheme+"://"+parsed.Host+"/json/version", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return cdpVersion{}, 0, false, fmt.Errorf("连接浏览器调试地址: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return cdpVersion{}, response.StatusCode, false, fmt.Errorf("读取浏览器调试信息: %w", err)
	}
	var version cdpVersion
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &version) != nil || version.WebSocketDebuggerURL == "" {
		return cdpVersion{}, response.StatusCode, true, fmt.Errorf("浏览器调试地址没有返回调试信息: HTTP %d", response.StatusCode)
	}
	return version, response.StatusCode, false, nil
}
