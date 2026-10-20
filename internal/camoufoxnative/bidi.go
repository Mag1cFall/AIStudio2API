package camoufoxnative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type bidiClient struct {
	connection  *websocket.Conn
	commandLock chan struct{}
	// writeLock 串行化连接写入，dispatch 可在其他命令等待响应期间写出命令
	writeLock                sync.Mutex
	nextID                   int
	generateHeaders          map[string]string
	blockedGenerateRequestID string
	// accessTokenStatus 为最近一次 GenerateAccessToken 的响应状态
	accessTokenStatus atomic.Int64
	regionURL         string
	// listener 接收命令响应之间穿插的 BiDi 事件，在读取连接的协程中调用
	listener func(map[string]any)
}

type bidiCommandError struct {
	method  string
	code    string
	message string
	payload string
}

func (err *bidiCommandError) Error() string {
	return fmt.Sprintf("BiDi %s 失败: %s", err.method, err.payload)
}

// newBiDiClient 创建串行 WebDriver BiDi 客户端
func newBiDiClient(connection *websocket.Conn) *bidiClient {
	return &bidiClient{
		connection:      connection,
		commandLock:     make(chan struct{}, 1),
		generateHeaders: make(map[string]string),
	}
}

type bidiResponse struct {
	result map[string]any
	err    error
}

// command 发送一条 BiDi 命令，调用方取消时立即返回，命令锁在读到该命令的响应后释放
func (client *bidiClient) command(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	select {
	case client.commandLock <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-client.commandLock
		return nil, err
	}
	if client.regionURL != "" {
		<-client.commandLock
		return nil, fmt.Errorf("%w，官网跳转 %s", ErrRegionNotSupported, client.regionURL)
	}
	done := make(chan bidiResponse, 1)
	go func() {
		defer func() { <-client.commandLock }()
		result, err := client.exchange(method, params)
		done <- bidiResponse{result: result, err: err}
	}()
	select {
	case response := <-done:
		return response.result, response.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// send 分配命令 ID 并写出一条命令
func (client *bidiClient) send(method string, params map[string]any, deadline time.Time) (int, error) {
	client.writeLock.Lock()
	defer client.writeLock.Unlock()
	client.nextID++
	id := client.nextID
	if err := client.connection.SetWriteDeadline(deadline); err != nil {
		return 0, err
	}
	return id, client.connection.WriteJSON(map[string]any{"id": id, "method": method, "params": params})
}

// dispatch 不经命令锁写出一条命令且不等待响应，响应由正在等待的命令读取后丢弃
func (client *bidiClient) dispatch(method string, params map[string]any) error {
	_, err := client.send(method, params, time.Now().Add(10*time.Second))
	return err
}

// exchange 写出命令并消费穿插的网络事件，直到读到同 ID 的响应
func (client *bidiClient) exchange(method string, params map[string]any) (map[string]any, error) {
	deadline := time.Now().Add(150 * time.Second)
	id, err := client.send(method, params, deadline)
	if err != nil {
		return nil, err
	}
	if err := client.connection.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	for {
		var message map[string]any
		if err := client.connection.ReadJSON(&message); err != nil {
			return nil, err
		}
		client.observe(message)
		messageID, _ := number(message["id"])
		if int(messageID) != id {
			continue
		}
		if message["type"] != "success" {
			encoded, _ := json.Marshal(message)
			code, _ := message["error"].(string)
			detail, _ := message["message"].(string)
			return nil, &bidiCommandError{method: method, code: code, message: detail, payload: string(encoded)}
		}
		result, _ := message["result"].(map[string]any)
		return result, nil
	}
}

// observe 把事件交给 listener，并捕获官网 GenerateContent 的公共头、GenerateAccessToken 的响应状态和跳转到地区不可用页的导航
func (client *bidiClient) observe(message map[string]any) {
	if client.listener != nil && message["type"] == "event" {
		client.listener(message)
	}
	method, _ := message["method"].(string)
	if method == "browsingContext.navigationStarted" {
		params, _ := message["params"].(map[string]any)
		rawURL, _ := params["url"].(string)
		if parsed, err := url.Parse(rawURL); err == nil && strings.HasSuffix(parsed.Path, "/available-regions") {
			client.regionURL = rawURL
		}
		return
	}
	if !strings.HasPrefix(method, "network.") {
		return
	}
	params, _ := message["params"].(map[string]any)
	request, _ := params["request"].(map[string]any)
	rawURL, _ := request["url"].(string)
	requestMethod, _ := request["method"].(string)
	parsed, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(parsed.Hostname(), "alkalimakersuite-pa.clients6.google.com") || requestMethod != http.MethodPost {
		return
	}
	if parsed.Path == accessTokenPath {
		response, _ := params["response"].(map[string]any)
		if status, ok := number(response["status"]); ok && method == "network.responseCompleted" {
			client.accessTokenStatus.Store(int64(status))
		}
		return
	}
	if parsed.Path != generateContentPath {
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

// installCookies 按 storage state 的分区恢复 Cookie
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
		params := map[string]any{"cookie": cookie}
		if item.PartitionKey != "" {
			params["partition"] = map[string]any{"type": "storageKey", "sourceOrigin": item.PartitionKey}
		}
		if _, err := client.command(ctx, "storage.setCookie", params); err != nil {
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

// callFunction 在页面默认主世界以字符串参数调用函数声明
func (client *bidiClient) callFunction(ctx context.Context, contextID, declaration string, arguments ...string) error {
	values := make([]map[string]any, len(arguments))
	for index, argument := range arguments {
		values[index] = map[string]any{"type": "string", "value": argument}
	}
	result, err := client.command(ctx, "script.callFunction", map[string]any{
		"functionDeclaration": declaration,
		"arguments":           values,
		"target":              map[string]any{"context": contextID},
		"awaitPromise":        true,
	})
	if err != nil {
		return err
	}
	if result["type"] == "exception" {
		encoded, _ := json.Marshal(result)
		return fmt.Errorf("页面函数异常: %s", encoded)
	}
	return nil
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

// waitFor 将页面条件的等待期限传递给 BiDi 命令
func (client *bidiClient) waitFor(ctx context.Context, contextID, expression string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		ready, err := client.evaluateBool(ctx, contextID, expression)
		if err != nil && !retryablePageEvaluation(err) {
			return err
		}
		if err == nil && ready {
			return nil
		}
		if err := waitContext(ctx, 200*time.Millisecond); err != nil {
			return err
		}
	}
	return fmt.Errorf("等待页面条件超时: %s", expression)
}

// waitSnapshotFunction 在阶段期限内定位页面函数
func (client *bidiClient) waitSnapshotFunction(ctx context.Context, contextID string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	for time.Now().Before(deadline) {
		key, err := client.evaluateString(ctx, contextID, snapshotHookExpression())
		if err != nil && !retryablePageEvaluation(err) {
			return "", err
		}
		if err == nil && key != "" && key != "no_default_MakerSuite" && key != "no_snapshot_fn" {
			return key, nil
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := waitContext(ctx, 500*time.Millisecond); err != nil {
			return "", err
		}
	}
	return "", errors.New("官网高层 snapshot 函数定位超时")
}

// waitBlockedGenerateRequest 在阶段期限内接收网络拦截事件
func (client *bidiClient) waitBlockedGenerateRequest(ctx context.Context, contextID string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	for time.Now().Before(deadline) {
		if client.blockedGenerateRequestID != "" {
			return client.blockedGenerateRequestID, nil
		}
		if _, err := client.evaluateBool(ctx, contextID, "true"); err != nil && !retryablePageEvaluation(err) {
			return "", err
		}
		if err := waitContext(ctx, 100*time.Millisecond); err != nil {
			return "", err
		}
	}
	return "", errors.New("官网 GenerateContent 拦截事件超时")
}

func retryablePageEvaluation(err error) bool {
	var commandError *bidiCommandError
	if !errors.As(err, &commandError) || commandError.method != "script.evaluate" {
		return false
	}
	code := strings.ToLower(strings.TrimSpace(commandError.code))
	if code == "no such frame" || code == "no such browsing context" {
		return true
	}
	message := strings.ToLower(commandError.message)
	contextLost := strings.Contains(message, "browsing context") || strings.Contains(message, "realm")
	destroyed := strings.Contains(message, "discarded") || strings.Contains(message, "destroyed") || strings.Contains(message, "navigation")
	return (code == "no such handle" || code == "unknown error") && contextLost && destroyed
}

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
    const service = args[0];
    if (service && (typeof service === 'object' || typeof service === 'function')) {
      window.__aistudioWaaService = service;
    }
    return original.apply(this, args);
  };
  wrapped.__aistudioWrapped = true;
  makerSuite[snapshotKey] = wrapped;
  window.__aistudioWaaSnapshotKey = snapshotKey;
  return snapshotKey;
})()`
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
