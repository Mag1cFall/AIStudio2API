package chromeauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// bidiHandshakeTimeout 是 BiDi WebSocket 握手的最长时间
	bidiHandshakeTimeout = 15 * time.Second
	// bidiSessionEndTimeout 是关闭 BiDi 会话的最长时间
	bidiSessionEndTimeout = 3 * time.Second
)

// bidiProductFallback 是 BiDi 会话没有回报产品名时显示的浏览器名
const bidiProductFallback = "Firefox (WebDriver BiDi)"

// bidiCookie 是 WebDriver BiDi storage.getCookies 返回的 Cookie
type bidiCookie struct {
	Name string `json:"name"`
	// Value 是 BytesRemoteValue，type 为 string 时 Value 为明文，为 base64 时是标准 base64
	Value struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"value"`
	Domain       string          `json:"domain"`
	Path         string          `json:"path"`
	HTTPOnly     bool            `json:"httpOnly"`
	Secure       bool            `json:"secure"`
	SameSite     string          `json:"sameSite"`
	Expiry       *float64        `json:"expiry"`
	PartitionKey json.RawMessage `json:"partitionKey"`
}

// browserCookieExport 是 aistudio.ParseCookieText 接受的浏览器扩展导出 Cookie 形状
type browserCookieExport struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires,omitempty"`
	Session  bool    `json:"session,omitempty"`
	HTTPOnly bool    `json:"httpOnly"`
	Secure   bool    `json:"secure"`
	SameSite string  `json:"sameSite,omitempty"`
}

// readBiDiBrowser 经 Firefox WebDriver BiDi 读取浏览器全部未分区 Cookie，返回产品名与扩展导出形状的 Cookie JSON
func readBiDiBrowser(ctx context.Context, address string) (BrowserCookies, error) {
	endpoint, err := bidiSessionEndpoint(address)
	if err != nil {
		return BrowserCookies{}, err
	}
	dialer := websocket.Dialer{HandshakeTimeout: bidiHandshakeTimeout}
	conn, _, err := dialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return BrowserCookies{}, fmt.Errorf("连接 Firefox WebDriver BiDi 调试地址: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	client := &bidiSession{conn: conn}

	session, err := client.call(ctx, "session.new", map[string]any{"capabilities": map[string]any{}})
	if err != nil {
		return BrowserCookies{}, fmt.Errorf("创建 WebDriver BiDi 会话: %w", err)
	}
	var info struct {
		SessionID    string `json:"sessionId"`
		Capabilities struct {
			BrowserName    string `json:"browserName"`
			BrowserVersion string `json:"browserVersion"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(session, &info); err != nil || info.SessionID == "" {
		return BrowserCookies{}, errors.New("WebDriver BiDi 会话没有返回 sessionId")
	}
	defer func() {
		// 用脱离调用方取消的上下文结束会话，避免浏览器侧残留会话；失败时连接关闭也会让浏览器自行回收
		endCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bidiSessionEndTimeout)
		defer cancel()
		_, _ = client.call(endCtx, "session.end", map[string]any{"sessionId": info.SessionID})
	}()

	result, err := client.call(ctx, "storage.getCookies", map[string]any{})
	if err != nil {
		return BrowserCookies{}, fmt.Errorf("经 WebDriver BiDi 读取浏览器 Cookie: %w", err)
	}
	var stored struct {
		Cookies []bidiCookie `json:"cookies"`
	}
	if err := json.Unmarshal(result, &stored); err != nil {
		return BrowserCookies{}, fmt.Errorf("解析 WebDriver BiDi Cookie: %w", err)
	}
	cookies, err := biDiCookiesToExport(stored.Cookies)
	if err != nil {
		return BrowserCookies{}, err
	}
	data, err := json.Marshal(cookies)
	if err != nil {
		return BrowserCookies{}, fmt.Errorf("编码浏览器 Cookie: %w", err)
	}
	return BrowserCookies{
		Product: bidiProductName(info.Capabilities.BrowserName, info.Capabilities.BrowserVersion),
		Cookies: string(data),
	}, nil
}

// bidiSession 在一条 WebDriver BiDi WebSocket 上依次发送命令
type bidiSession struct {
	conn *websocket.Conn
	id   int
}

// call 发送一条 BiDi 命令并等待同一 id 的响应，事件消息没有 id，读取后跳过
func (session *bidiSession) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	session.id++
	if err := session.conn.WriteJSON(map[string]any{"id": session.id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		var response struct {
			ID      int             `json:"id"`
			Result  json.RawMessage `json:"result"`
			Error   string          `json:"error"`
			Message string          `json:"message"`
		}
		if err := session.conn.ReadJSON(&response); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		if response.ID != session.id {
			continue
		}
		if response.Error != "" {
			return nil, fmt.Errorf("%s: %s", response.Error, response.Message)
		}
		return response.Result, nil
	}
}

// bidiSessionEndpoint 把 host:port、HTTP 调试地址或 BiDi WebSocket 地址解析为 /session 的 WebSocket 地址
func bidiSessionEndpoint(address string) (string, error) {
	address = strings.TrimSpace(address)
	if strings.HasPrefix(address, "ws://") || strings.HasPrefix(address, "wss://") {
		parsed, err := url.Parse(address)
		if err != nil || parsed.Host == "" {
			return "", fmt.Errorf("浏览器调试地址无效，例如 127.0.0.1:9222")
		}
		if parsed.Path == "" || parsed.Path == "/" {
			parsed.Path = "/session"
		}
		return parsed.String(), nil
	}
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("浏览器调试地址无效，例如 127.0.0.1:9222")
	}
	scheme := "ws"
	if parsed.Scheme == "https" {
		scheme = "wss"
	}
	return scheme + "://" + parsed.Host + "/session", nil
}

// biDiCookiesToExport 把 BiDi Cookie 转为扩展导出形状，跳过分区 Cookie 与缺少名称或域的条目
func biDiCookiesToExport(items []bidiCookie) ([]browserCookieExport, error) {
	cookies := make([]browserCookieExport, 0, len(items))
	for _, item := range items {
		if len(item.PartitionKey) > 0 && string(item.PartitionKey) != "null" {
			continue
		}
		if item.Name == "" || item.Domain == "" {
			continue
		}
		value := item.Value.Value
		if item.Value.Type == "base64" {
			decoded, err := base64.StdEncoding.DecodeString(value)
			if err != nil {
				return nil, fmt.Errorf("解码 WebDriver BiDi Cookie %s 的 base64 值: %w", item.Name, err)
			}
			value = string(decoded)
		}
		export := browserCookieExport{
			Name: item.Name, Value: value, Domain: item.Domain, Path: item.Path,
			HTTPOnly: item.HTTPOnly, Secure: item.Secure, SameSite: item.SameSite,
		}
		if export.Path == "" {
			export.Path = "/"
		}
		if item.Expiry == nil {
			export.Session = true
		} else {
			export.Expires = *item.Expiry
		}
		cookies = append(cookies, export)
	}
	return cookies, nil
}

// bidiProductName 把会话回报的浏览器名与版本拼成产品名，缺省时返回固定的 Firefox BiDi 名称
func bidiProductName(name, version string) string {
	name = strings.TrimSpace(name)
	version = strings.TrimSpace(version)
	if name != "" {
		if first := name[0]; first >= 'a' && first <= 'z' {
			name = string(first-32) + name[1:]
		}
		if version != "" {
			return name + "/" + version
		}
		return name
	}
	if version != "" {
		return bidiProductFallback + "/" + version
	}
	return bidiProductFallback
}
