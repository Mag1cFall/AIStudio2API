package chromeauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/gorilla/websocket"
)

// bidiSessionEndTimeout 是关闭 BiDi 会话的最长时间
var bidiSessionEndTimeout = 3 * time.Second

const (
	// bidiDefaultUserContext 是 Firefox 普通窗口使用的默认用户上下文
	bidiDefaultUserContext = "default"
	// bidiCookiePartitionOrigin 是读取 AI Studio 相关 Cookie 时传给 storage.getCookies 的存储分区来源
	bidiCookiePartitionOrigin = "https://aistudio.google.com"
	// bidiProductFallback 是 BiDi 会话没有回报产品名时显示的浏览器名
	bidiProductFallback = "Firefox (WebDriver BiDi)"
)

// bidiBytesValue 是 WebDriver BiDi 协议的 BytesValue，type 为 string 时 Value 为明文，为 base64 时是标准 base64
type bidiBytesValue struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// bidiCookie 是 WebDriver BiDi storage.getCookies 返回的 Cookie
type bidiCookie struct {
	Name     string         `json:"name"`
	Value    bidiBytesValue `json:"value"`
	Domain   string         `json:"domain"`
	Path     string         `json:"path"`
	HTTPOnly bool           `json:"httpOnly"`
	Secure   bool           `json:"secure"`
	SameSite string         `json:"sameSite"`
	Expiry   *float64       `json:"expiry"`
}

// readBiDiBrowser 经 Firefox WebDriver BiDi 读取默认上下文下 AI Studio 存储分区的 Cookie，返回产品名与 storage state Cookie 数组 JSON
func readBiDiBrowser(ctx context.Context, address string) (BrowserCookies, error) {
	endpoint, err := bidiSessionEndpoint(address)
	if err != nil {
		return BrowserCookies{}, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return BrowserCookies{}, fmt.Errorf("连接 Firefox WebDriver BiDi 调试地址: %w", err)
	}
	client := newBiDiSession(conn)
	defer client.close()

	session, err := client.call(ctx, "session.new", map[string]any{"capabilities": map[string]any{}})
	if err != nil {
		if ctx.Err() != nil {
			endCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bidiSessionEndTimeout)
			_, _ = client.call(endCtx, "session.end", map[string]any{})
			cancel()
		}
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
	product := bidiProductName(info.Capabilities.BrowserName, info.Capabilities.BrowserVersion)
	defer func() {
		endCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bidiSessionEndTimeout)
		defer cancel()
		_, _ = client.call(endCtx, "session.end", map[string]any{})
	}()

	result, err := client.call(ctx, "storage.getCookies", map[string]any{
		"partition": map[string]any{
			"type":         "storageKey",
			"userContext":  bidiDefaultUserContext,
			"sourceOrigin": bidiCookiePartitionOrigin,
		},
	})
	if err != nil {
		return BrowserCookies{Product: product}, fmt.Errorf("经 WebDriver BiDi 读取浏览器 Cookie: %w", err)
	}
	var stored struct {
		Cookies []bidiCookie `json:"cookies"`
	}
	if err := json.Unmarshal(result, &stored); err != nil {
		return BrowserCookies{Product: product}, fmt.Errorf("解析 WebDriver BiDi Cookie: %w", err)
	}
	cookies, err := bidiCookiesToExport(stored.Cookies)
	if err != nil {
		return BrowserCookies{Product: product}, err
	}
	data, err := json.Marshal(cookies)
	if err != nil {
		return BrowserCookies{Product: product}, fmt.Errorf("编码浏览器 Cookie: %w", err)
	}
	return BrowserCookies{
		Product: product,
		Cookies: string(data),
	}, nil
}

// bidiReply 是读取协程交付的一条命令响应或读取错误
type bidiReply struct {
	id      int
	result  json.RawMessage
	errCode string
	errMsg  string
	readErr error
}

// bidiSession 在一条 WebDriver BiDi WebSocket 上依次发送命令，独立读取协程保证取消上下文后连接仍可发送 session.end
type bidiSession struct {
	conn    *websocket.Conn
	id      int
	replies chan bidiReply
	done    chan struct{}
}

// newBiDiSession 创建 BiDi 会话并启动响应读取协程
func newBiDiSession(conn *websocket.Conn) *bidiSession {
	session := &bidiSession{
		conn:    conn,
		replies: make(chan bidiReply, 8),
		done:    make(chan struct{}),
	}
	go session.readLoop()
	return session
}

// readLoop 持续读取连接响应并投递到 replies，连接关闭或出错时退出
func (session *bidiSession) readLoop() {
	defer close(session.done)
	defer close(session.replies)
	for {
		var response struct {
			ID      int             `json:"id"`
			Result  json.RawMessage `json:"result"`
			Error   string          `json:"error"`
			Message string          `json:"message"`
		}
		if err := session.conn.ReadJSON(&response); err != nil {
			session.replies <- bidiReply{readErr: err}
			return
		}
		if response.ID == 0 {
			continue
		}
		session.replies <- bidiReply{
			id:      response.ID,
			result:  response.Result,
			errCode: response.Error,
			errMsg:  response.Message,
		}
	}
}

// close 关闭底层连接并等待读取协程退出
func (session *bidiSession) close() {
	_ = session.conn.Close()
	for range session.replies {
	}
	<-session.done
}

// call 发送一条 BiDi 命令并等待同一 id 的响应，非匹配 id 的消息忽略，ctx 取消时立即返回而不关闭连接
func (session *bidiSession) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	session.id++
	if deadline, ok := ctx.Deadline(); ok {
		_ = session.conn.SetWriteDeadline(deadline)
	} else {
		_ = session.conn.SetWriteDeadline(time.Time{})
	}
	if err := session.conn.WriteJSON(map[string]any{"id": session.id, "method": method, "params": params}); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case reply, ok := <-session.replies:
			if !ok {
				return nil, io.EOF
			}
			if reply.readErr != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, reply.readErr
			}
			if reply.id != session.id {
				continue
			}
			if reply.errCode != "" {
				if reply.errMsg == "" {
					return nil, errors.New(reply.errCode)
				}
				return nil, fmt.Errorf("%s: %s", reply.errCode, reply.errMsg)
			}
			return reply.result, nil
		}
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
		if path := strings.TrimSuffix(parsed.Path, "/"); path == "" || path == "/session" {
			parsed.Path = "/session"
		}
		return parsed.String(), nil
	}
	parsed, err := parseDebugHTTPURL(address)
	if err != nil {
		return "", err
	}
	scheme := "ws"
	if parsed.Scheme == "https" {
		scheme = "wss"
	}
	return scheme + "://" + parsed.Host + "/session", nil
}

// bidiCookiesToExport 把 BiDi Cookie 转为 StateCookie 列表，跳过缺少名称或域的条目，同键保留先出现的未分区项
func bidiCookiesToExport(items []bidiCookie) ([]aistudio.StateCookie, error) {
	cookies := make([]aistudio.StateCookie, 0, len(items))
	seen := make(map[[3]string]struct{}, len(items))
	for _, item := range items {
		if item.Name == "" || item.Domain == "" {
			continue
		}
		path := item.Path
		if path == "" {
			path = "/"
		}
		key := [3]string{strings.ToLower(item.Domain), path, item.Name}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		value := item.Value.Value
		if item.Value.Type == "base64" {
			decoded, err := base64.StdEncoding.DecodeString(value)
			if err != nil {
				return nil, fmt.Errorf("解码 WebDriver BiDi Cookie %s 的 base64 值: %w", item.Name, err)
			}
			value = string(decoded)
		}
		expires := float64(-1)
		if item.Expiry != nil {
			expires = *item.Expiry
		}
		cookies = append(cookies, aistudio.StateCookie{
			Name: item.Name, Value: value, Domain: item.Domain, Path: path,
			Expires: expires, HTTPOnly: item.HTTPOnly, Secure: item.Secure, SameSite: item.SameSite,
		})
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
