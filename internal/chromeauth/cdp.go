package chromeauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/gorilla/websocket"
)

// CDPBrowser 是经远程调试地址读取的浏览器 Cookie 与页面语言、时区
type CDPBrowser struct {
	Cookies  string
	Locale   string
	Timezone string
}

// ReadCDPBrowser 经 Chromium 远程调试地址读取浏览器全部未分区 Cookie，并在第一个页面中读取 navigator.language 与 Intl 时区，没有页面时语言与时区为空
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
	browser := CDPBrowser{Cookies: string(data)}
	if browser.Locale, browser.Timezone, err = session.pageEnvironment(ctx); err != nil {
		return CDPBrowser{}, fmt.Errorf("读取浏览器语言与时区: %w", err)
	}
	return browser, nil
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

// cdpTarget 是 Target.getTargets 列出的一个调试目标
type cdpTarget struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
}

// pageEnvironment 附加到浏览器的第一个页面，返回该页面的 navigator.language 与 Intl 时区
func (session *cdpSession) pageEnvironment(ctx context.Context) (string, string, error) {
	var targets struct {
		TargetInfos []cdpTarget `json:"targetInfos"`
	}
	if err := session.call(ctx, "Target.getTargets", nil, "", &targets); err != nil {
		return "", "", err
	}
	index := slices.IndexFunc(targets.TargetInfos, func(target cdpTarget) bool { return target.Type == "page" })
	if index < 0 {
		return "", "", nil
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := session.call(ctx, "Target.attachToTarget", map[string]any{"targetId": targets.TargetInfos[index].TargetID, "flatten": true}, "", &attached); err != nil {
		return "", "", err
	}
	var evaluated struct {
		Result struct {
			Value [2]string `json:"value"`
		} `json:"result"`
	}
	expression := "[navigator.language, Intl.DateTimeFormat().resolvedOptions().timeZone]"
	if err := session.call(ctx, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true}, attached.SessionID, &evaluated); err != nil {
		return "", "", err
	}
	return evaluated.Result.Value[0], evaluated.Result.Value[1], nil
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
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.Scheme+"://"+parsed.Host+"/json/version", nil)
	if err != nil {
		return cdpVersion{}, err
	}
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
