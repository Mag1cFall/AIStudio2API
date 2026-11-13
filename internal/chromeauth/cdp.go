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

// CDPBrowser 是经远程调试地址读取的浏览器 Cookie
type CDPBrowser struct {
	Cookies string
}

// ReadCDPBrowser 经 Chromium 远程调试地址读取浏览器全部未分区 Cookie
func ReadCDPBrowser(ctx context.Context, address string) (CDPBrowser, error) {
	endpoint, err := cdpBrowserEndpoint(ctx, address)
	if err != nil {
		return CDPBrowser{}, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return CDPBrowser{}, fmt.Errorf("连接浏览器调试地址: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	session := &cdpSession{conn: conn}
	var stored struct {
		Cookies []map[string]any `json:"cookies"`
	}
	if err := session.call(ctx, "Storage.getCookies", nil, "", &stored); err != nil {
		return CDPBrowser{}, fmt.Errorf("读取浏览器 Cookie: %w", err)
	}
	cookies := make([]map[string]any, 0, len(stored.Cookies))
	for _, cookie := range stored.Cookies {
		if cookie["partitionKey"] == nil {
			cookies = append(cookies, cookie)
		}
	}
	data, err := json.Marshal(cookies)
	if err != nil {
		return CDPBrowser{}, fmt.Errorf("编码浏览器 Cookie: %w", err)
	}
	return CDPBrowser{Cookies: string(data)}, nil
}

// cdpSession 在一条浏览器级 CDP 连接上依次发送命令
type cdpSession struct {
	conn *websocket.Conn
	id   int
}

// call 发送一条命令并把同一 id 的结果解码到 result，sessionID 非空时发往已附加的页面
func (session *cdpSession) call(ctx context.Context, method string, params any, sessionID string, result any) error {
	session.id++
	message := map[string]any{"id": session.id, "method": method}
	if params != nil {
		message["params"] = params
	}
	if sessionID != "" {
		message["sessionId"] = sessionID
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

// cdpBrowserEndpoint 把 host:port、HTTP 调试地址或浏览器 WebSocket 地址解析为浏览器级 WebSocket 地址
func cdpBrowserEndpoint(ctx context.Context, address string) (string, error) {
	address = strings.TrimSpace(address)
	if strings.HasPrefix(address, "ws://") || strings.HasPrefix(address, "wss://") {
		return address, nil
	}
	version, err := debugVersion(ctx, address)
	if err != nil {
		return "", err
	}
	return version.WebSocketDebuggerURL, nil
}

// debugVersion 读取 host:port 或 HTTP 调试地址的 /json/version
func debugVersion(ctx context.Context, address string) (cdpVersion, error) {
	address = strings.TrimSpace(address)
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return cdpVersion{}, fmt.Errorf("浏览器调试地址无效，例如 127.0.0.1:9222")
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, parsed.Scheme+"://"+parsed.Host+"/json/version", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return cdpVersion{}, fmt.Errorf("连接浏览器调试地址: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return cdpVersion{}, fmt.Errorf("读取浏览器调试信息: %w", err)
	}
	var version cdpVersion
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &version) != nil || version.WebSocketDebuggerURL == "" {
		return cdpVersion{}, fmt.Errorf("浏览器调试地址没有返回调试信息: HTTP %d", response.StatusCode)
	}
	return version, nil
}
