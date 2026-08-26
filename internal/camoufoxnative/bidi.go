package camoufoxnative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type bidiClient struct {
	connection               *websocket.Conn
	commandLock              chan struct{}
	nextID                   int
	generateHeaders          map[string]string
	blockedGenerateRequestID string
}

// newBiDiClient 创建串行 WebDriver BiDi 客户端
func newBiDiClient(connection *websocket.Conn) *bidiClient {
	return &bidiClient{
		connection:      connection,
		commandLock:     make(chan struct{}, 1),
		generateHeaders: make(map[string]string),
	}
}

// command 发送一条 BiDi 命令并消费穿插的网络事件
func (client *bidiClient) command(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	select {
	case client.commandLock <- struct{}{}:
		defer func() { <-client.commandLock }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client.nextID++
	id := client.nextID
	deadline := time.Now().Add(150 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := client.connection.SetWriteDeadline(deadline); err != nil {
		return nil, err
	}
	if err := client.connection.WriteJSON(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	if err := client.connection.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	stopCancellation := context.AfterFunc(ctx, func() {
		_ = client.connection.Close()
	})
	defer stopCancellation()
	for {
		var message map[string]any
		if err := client.connection.ReadJSON(&message); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		client.observe(message)
		messageID, _ := number(message["id"])
		if int(messageID) != id {
			continue
		}
		if message["type"] != "success" {
			encoded, _ := json.Marshal(message)
			return nil, fmt.Errorf("BiDi %s 失败: %s", method, encoded)
		}
		result, _ := message["result"].(map[string]any)
		return result, nil
	}
}

// observe 捕获官网 GenerateContent 的公共头和响应状态
func (client *bidiClient) observe(message map[string]any) {
	method, _ := message["method"].(string)
	if !strings.HasPrefix(method, "network.") {
		return
	}
	params, _ := message["params"].(map[string]any)
	request, _ := params["request"].(map[string]any)
	rawURL, _ := request["url"].(string)
	requestMethod, _ := request["method"].(string)
	if !strings.HasSuffix(rawURL, "/GenerateContent") || requestMethod != http.MethodPost {
		return
	}
	if headers, ok := request["headers"].([]any); ok {
		for _, item := range headers {
			header, _ := item.(map[string]any)
			name, _ := header["name"].(string)
			value := remoteBytesValue(header["value"])
			if name != "" {
				client.generateHeaders[strings.ToLower(name)] = value
			}
		}
	}
	blocked, _ := params["isBlocked"].(bool)
	requestID, _ := request["request"].(string)
	if method == "network.beforeRequestSent" && blocked && requestID != "" {
		client.blockedGenerateRequestID = requestID
	}
}

// installCookies 将 Playwright storage state Cookie 写入默认分区
func (client *bidiClient) installCookies(ctx context.Context, cookies []storageCookie) error {
	for _, item := range cookies {
		cookie := map[string]any{
			"name":     item.Name,
			"value":    map[string]any{"type": "string", "value": item.Value},
			"domain":   item.Domain,
			"path":     item.Path,
			"httpOnly": item.HTTPOnly,
			"secure":   item.Secure,
		}
		if item.Expires > 0 {
			cookie["expiry"] = int64(math.Floor(item.Expires))
		}
		sameSite := strings.ToLower(item.SameSite)
		if sameSite == "strict" || sameSite == "lax" || sameSite == "none" && item.Secure {
			cookie["sameSite"] = sameSite
		}
		if _, err := client.command(ctx, "storage.setCookie", map[string]any{"cookie": cookie}); err != nil {
			return fmt.Errorf("写入 Cookie %s: %w", item.Name, err)
		}
	}
	return nil
}

// installLocalStorage 在站点脚本前恢复各 origin 的 localStorage
func (client *bidiClient) installLocalStorage(ctx context.Context, contextID string, origins []storageOrigin) error {
	values := make(map[string]map[string]string, len(origins))
	for _, origin := range origins {
		items := make(map[string]string, len(origin.LocalStorage))
		for _, item := range origin.LocalStorage {
			items[item.Name] = item.Value
		}
		values[origin.Origin] = items
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	function := fmt.Sprintf(`() => {
  const all = %s;
  const current = all[location.origin];
  if (!current) return;
  for (const [name, value] of Object.entries(current)) localStorage.setItem(name, value);
}`, encoded)
	_, err = client.command(ctx, "script.addPreloadScript", map[string]any{
		"functionDeclaration": function,
		"contexts":            []string{contextID},
	})
	if err != nil {
		return fmt.Errorf("安装 localStorage preload: %w", err)
	}
	return nil
}

// evaluate 在页面默认主世界执行表达式
func (client *bidiClient) evaluate(ctx context.Context, contextID, expression string) (map[string]any, error) {
	result, err := client.command(ctx, "script.evaluate", map[string]any{
		"expression":   expression,
		"target":       map[string]any{"context": contextID},
		"awaitPromise": true,
	})
	if err != nil {
		return nil, err
	}
	if result["type"] == "exception" {
		encoded, _ := json.Marshal(result)
		return nil, fmt.Errorf("页面表达式异常: %s", encoded)
	}
	remote, _ := result["result"].(map[string]any)
	return remote, nil
}

func (client *bidiClient) evaluateString(ctx context.Context, contextID, expression string) (string, error) {
	result, err := client.evaluate(ctx, contextID, expression)
	if err != nil {
		return "", err
	}
	value, _ := result["value"].(string)
	return value, nil
}

func (client *bidiClient) evaluateBool(ctx context.Context, contextID, expression string) (bool, error) {
	result, err := client.evaluate(ctx, contextID, expression)
	if err != nil {
		return false, err
	}
	value, _ := result["value"].(bool)
	return value, nil
}

func (client *bidiClient) waitFor(ctx context.Context, contextID, expression string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		ready, err := client.evaluateBool(ctx, contextID, expression)
		if err == nil && ready {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("等待页面条件超时: %s", expression)
}

func (client *bidiClient) waitSnapshotFunction(ctx context.Context, contextID string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		key, err := client.evaluateString(ctx, contextID, snapshotHookExpression())
		if err == nil && key != "" && key != "no_default_MakerSuite" && key != "no_snapshot_fn" {
			return key, nil
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		time.Sleep(500 * time.Millisecond)
	}
	return "", errors.New("官网高层 snapshot 函数定位超时")
}

func (client *bidiClient) waitBlockedGenerateRequest(ctx context.Context, contextID string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if client.blockedGenerateRequestID != "" {
			return client.blockedGenerateRequestID, nil
		}
		if _, err := client.evaluateBool(ctx, contextID, "true"); err != nil {
			return "", err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "", errors.New("官网 GenerateContent 拦截事件超时")
}

func snapshotHookExpression() string {
	return `(() => {
  const makerSuite = window.default_MakerSuite;
  if (!makerSuite) return 'no_default_MakerSuite';
  const currentKey = window.__aistudioWaaSnapshotKey;
  if (currentKey && makerSuite[currentKey]?.__aistudioWrapped) return currentKey;
  let snapshotKey = null;
  for (const key of Object.keys(makerSuite)) {
    try {
      if (typeof makerSuite[key] !== 'function') continue;
      const source = makerSuite[key].toString();
      if (source.includes('.snapshot({') && source.includes('content') && source.includes('yield')) {
        snapshotKey = key;
        break;
      }
    } catch (_) {}
  }
  if (!snapshotKey) return 'no_snapshot_fn';
  const original = makerSuite[snapshotKey];
  const wrapped = function(...args) {
    window.__aistudioWaaService = args[0];
    return original.apply(this, args);
  };
  wrapped.__aistudioWrapped = true;
  makerSuite[snapshotKey] = wrapped;
  window.__aistudioWaaSnapshotKey = snapshotKey;
  return snapshotKey;
})()`
}

func fillPromptExpression(prompt string) string {
	encoded, _ := json.Marshal(prompt)
	return fmt.Sprintf(`(() => {
  const items = [...document.querySelectorAll('ms-prompt-box textarea')];
  const textarea = items.at(-1);
  if (!textarea) return '';
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
  setter.call(textarea, %s);
  textarea.dispatchEvent(new InputEvent('input', {bubbles: true, inputType: 'insertText', data: %s}));
  textarea.dispatchEvent(new Event('change', {bubbles: true}));
  return textarea.value;
})()`, encoded, encoded)
}

func takeProofExpression(digest string) string {
	encoded, _ := json.Marshal(digest)
	return fmt.Sprintf(`(async () => {
  const makerSuite = window.default_MakerSuite;
  const service = window.__aistudioWaaService;
  const snapshotKey = window.__aistudioWaaSnapshotKey;
  if (!makerSuite || !service || !snapshotKey || typeof makerSuite[snapshotKey] !== 'function') {
    throw new Error('官方 WAA service 尚未就绪');
  }
  return await makerSuite[snapshotKey](service, %s);
})()`, encoded)
}

func remoteBytesValue(value any) string {
	item, _ := value.(map[string]any)
	raw, _ := item["value"].(string)
	return raw
}

func number(value any) (float64, bool) {
	switch item := value.(type) {
	case float64:
		return item, true
	case int:
		return float64(item), true
	default:
		return 0, false
	}
}
