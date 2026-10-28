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

// bidiCommandTimeout 为单条 BiDi 命令等待响应的最长时间
const bidiCommandTimeout = 150 * time.Second

type bidiClient struct {
	connection *websocket.Conn
	// writeLock 串行化连接写入
	writeLock sync.Mutex
	mu        sync.Mutex
	nextID    int
	pending   map[int]bidiPending
	channels  map[string]*bidiChannel
	// failure 为读取连接失败的原因，之后的命令立即以它返回
	failure error
	// done 在读取连接失败时关闭
	done chan struct{}
	// blocked 接收被拦截的官网 GenerateContent 请求
	blocked chan blockedRequest
	// accessTokenStatus 为最近一次 GenerateAccessToken 的响应状态
	accessTokenStatus atomic.Int64
	regionURL         string
	// listener 接收 BiDi 事件，在读取连接的协程中调用
	listener func(map[string]any)
}

// bidiPending 是等待响应的一条命令
type bidiPending struct {
	method   string
	response chan bidiResponse
}

// blockedRequest 是在发送前被拦截的官网 GenerateContent 请求 ID 与请求头
type blockedRequest struct {
	id      string
	headers map[string]string
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

// newBiDiClient 创建 WebDriver BiDi 客户端，读取协程按命令 ID 交付响应并分发事件，多条命令可同时等待响应
func newBiDiClient(connection *websocket.Conn) *bidiClient {
	client := &bidiClient{
		connection: connection,
		pending:    make(map[int]bidiPending),
		channels:   make(map[string]*bidiChannel),
		done:       make(chan struct{}),
		blocked:    make(chan blockedRequest, 1),
	}
	go client.read()
	return client
}

type bidiResponse struct {
	result map[string]any
	err    error
}

// command 发送一条 BiDi 命令并等待其响应，调用方取消或超过 bidiCommandTimeout 时立即返回，迟到的响应被丢弃
func (client *bidiClient) command(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, bidiCommandTimeout)
	defer cancel()
	response := make(chan bidiResponse, 1)
	client.mu.Lock()
	if client.regionURL != "" {
		region := client.regionURL
		client.mu.Unlock()
		return nil, fmt.Errorf("%w，官网跳转 %s", ErrRegionNotSupported, region)
	}
	if client.failure != nil {
		failure := client.failure
		client.mu.Unlock()
		return nil, failure
	}
	client.nextID++
	id := client.nextID
	client.pending[id] = bidiPending{method: method, response: response}
	client.mu.Unlock()
	if err := client.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		client.forget(id)
		return nil, err
	}
	select {
	case result := <-response:
		return result.result, result.err
	case <-ctx.Done():
		client.forget(id)
		return nil, ctx.Err()
	}
}

// write 写出一条消息
func (client *bidiClient) write(message map[string]any) error {
	client.writeLock.Lock()
	defer client.writeLock.Unlock()
	if err := client.connection.SetWriteDeadline(time.Now().Add(bidiCommandTimeout)); err != nil {
		return err
	}
	return client.connection.WriteJSON(message)
}

// forget 移除不再等待响应的命令
func (client *bidiClient) forget(id int) {
	client.mu.Lock()
	delete(client.pending, id)
	client.mu.Unlock()
}

// read 读取连接上的全部消息：带 ID 的交付给等待的命令，其余作为事件处理；连接出错时结束全部等待中的命令与通道
func (client *bidiClient) read() {
	for {
		var message map[string]any
		if err := client.connection.ReadJSON(&message); err != nil {
			client.fail(err)
			return
		}
		if id, ok := number(message["id"]); ok {
			client.respond(int(id), message)
			continue
		}
		client.observe(message)
	}
}

// respond 把响应交给等待该 ID 的命令
func (client *bidiClient) respond(id int, message map[string]any) {
	client.mu.Lock()
	pending, ok := client.pending[id]
	delete(client.pending, id)
	client.mu.Unlock()
	if !ok {
		return
	}
	if message["type"] != "success" {
		encoded, _ := json.Marshal(message)
		code, _ := message["error"].(string)
		detail, _ := message["message"].(string)
		pending.response <- bidiResponse{err: &bidiCommandError{method: pending.method, code: code, message: detail, payload: string(encoded)}}
		return
	}
	result, _ := message["result"].(map[string]any)
	pending.response <- bidiResponse{result: result}
}

// fail 记录连接失败并结束全部等待中的命令与通道
func (client *bidiClient) fail(err error) {
	failure := fmt.Errorf("BiDi 连接已断开: %w", err)
	client.mu.Lock()
	client.failure = failure
	pending, channels := client.pending, client.channels
	client.pending, client.channels = map[int]bidiPending{}, map[string]*bidiChannel{}
	client.mu.Unlock()
	close(client.done)
	for _, item := range pending {
		item.response <- bidiResponse{err: failure}
	}
	for _, channel := range channels {
		channel.finish(failure)
	}
}

// setListener 设置接收 BiDi 事件的函数
func (client *bidiClient) setListener(listener func(map[string]any)) {
	client.mu.Lock()
	client.listener = listener
	client.mu.Unlock()
}

// observe 把事件交给 listener 与 script.message 通道，并捕获官网 GenerateContent 的拦截请求、GenerateAccessToken 的响应状态和跳转到地区不可用页的导航
func (client *bidiClient) observe(message map[string]any) {
	client.mu.Lock()
	listener := client.listener
	client.mu.Unlock()
	if listener != nil && message["type"] == "event" {
		listener(message)
	}
	method, _ := message["method"].(string)
	params, _ := message["params"].(map[string]any)
	switch {
	case method == "script.message":
		name, _ := params["channel"].(string)
		client.mu.Lock()
		channel := client.channels[name]
		client.mu.Unlock()
		if channel != nil {
			channel.push(remoteBytesValue(params["data"]))
		}
		return
	case method == "browsingContext.navigationStarted":
		rawURL, _ := params["url"].(string)
		if parsed, err := url.Parse(rawURL); err == nil && strings.HasSuffix(parsed.Path, "/available-regions") {
			client.mu.Lock()
			client.regionURL = rawURL
			client.mu.Unlock()
		}
		return
	case !strings.HasPrefix(method, "network."):
		return
	}
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
	blocked, _ := params["isBlocked"].(bool)
	requestID, _ := request["request"].(string)
	if parsed.Path != generateContentPath || method != "network.beforeRequestSent" || !blocked || requestID == "" {
		return
	}
	headers := make(map[string]string)
	items, _ := request["headers"].([]any)
	for _, item := range items {
		header, _ := item.(map[string]any)
		name, _ := header["name"].(string)
		if name != "" {
			headers[strings.ToLower(name)] = remoteBytesValue(header["value"])
		}
	}
	select {
	case client.blocked <- blockedRequest{id: requestID, headers: headers}:
	default:
	}
}

// bidiChannel 按到达顺序缓存一个 script.message 通道收到的字符串，连接断开时以连接错误结束
type bidiChannel struct {
	mu     sync.Mutex
	queue  []string
	err    error
	notify chan struct{}
}

func (channel *bidiChannel) push(value string) {
	channel.mu.Lock()
	channel.queue = append(channel.queue, value)
	channel.mu.Unlock()
	select {
	case channel.notify <- struct{}{}:
	default:
	}
}

func (channel *bidiChannel) finish(err error) {
	channel.mu.Lock()
	channel.err = err
	channel.mu.Unlock()
	select {
	case channel.notify <- struct{}{}:
	default:
	}
}

// next 取出下一条消息，没有消息时等待到达或 ctx 结束
func (channel *bidiChannel) next(ctx context.Context) (string, error) {
	for {
		channel.mu.Lock()
		if len(channel.queue) > 0 {
			value := channel.queue[0]
			channel.queue = channel.queue[1:]
			channel.mu.Unlock()
			return value, nil
		}
		err := channel.err
		channel.mu.Unlock()
		if err != nil {
			return "", err
		}
		select {
		case <-channel.notify:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

// openChannel 注册一个 script.message 通道，作为 bidiChannelArgument 传入页面函数后接收该函数发出的消息
func (client *bidiClient) openChannel(name string) (*bidiChannel, error) {
	channel := &bidiChannel{notify: make(chan struct{}, 1)}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.failure != nil {
		return nil, client.failure
	}
	client.channels[name] = channel
	return channel, nil
}

// closeChannel 注销 script.message 通道，之后到达的消息被丢弃
func (client *bidiClient) closeChannel(name string) {
	client.mu.Lock()
	delete(client.channels, name)
	client.mu.Unlock()
}

// installCookies 按 storage state 的分区恢复 Cookie，各 Cookie 的命令同时发出
func (client *bidiClient) installCookies(ctx context.Context, cookies []storageCookie) error {
	errs := make([]error, len(cookies))
	var wait sync.WaitGroup
	for index, item := range cookies {
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
		wait.Go(func() {
			if _, err := client.command(ctx, "storage.setCookie", params); err != nil {
				errs[index] = fmt.Errorf("写入 Cookie %s: %w", item.Name, err)
			}
		})
	}
	wait.Wait()
	return errors.Join(errs...)
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

// pageHandle 是 BiDi 为 Worker 持有的页面主世界对象句柄，页面脚本无法从全局对象访问该对象
type pageHandle string

// bidiChannelArgument 作为页面函数参数时成为向同名 script.message 通道发送消息的函数
type bidiChannelArgument string

// callFunction 在页面默认主世界调用函数声明，参数为字符串、pageHandle 或 bidiChannelArgument，返回结果的 RemoteValue
func (client *bidiClient) callFunction(ctx context.Context, contextID, declaration string, arguments ...any) (map[string]any, error) {
	return client.invoke(ctx, contextID, declaration, arguments, map[string]any{})
}

// callFunctionString 调用函数声明并返回字符串结果
func (client *bidiClient) callFunctionString(ctx context.Context, contextID, declaration string, arguments ...any) (string, error) {
	result, err := client.callFunction(ctx, contextID, declaration, arguments...)
	if err != nil {
		return "", err
	}
	value, _ := result["value"].(string)
	return value, nil
}

// callFunctionHandle 调用函数声明并由 BiDi 持有返回的对象，返回其句柄，函数返回非对象值时句柄为空
func (client *bidiClient) callFunctionHandle(ctx context.Context, contextID, declaration string, arguments ...any) (pageHandle, error) {
	result, err := client.invoke(ctx, contextID, declaration, arguments, map[string]any{
		"resultOwnership":      "root",
		"serializationOptions": map[string]any{"maxObjectDepth": 0},
	})
	if err != nil {
		return "", err
	}
	handle, _ := result["handle"].(string)
	return pageHandle(handle), nil
}

// invoke 发送 script.callFunction，pageHandle 参数以句柄引用传入，bidiChannelArgument 以通道传入，其余参数作为字符串传入
func (client *bidiClient) invoke(ctx context.Context, contextID, declaration string, arguments []any, params map[string]any) (map[string]any, error) {
	values := make([]map[string]any, len(arguments))
	for index, argument := range arguments {
		switch item := argument.(type) {
		case pageHandle:
			values[index] = map[string]any{"handle": string(item)}
		case bidiChannelArgument:
			values[index] = map[string]any{"type": "channel", "value": map[string]any{"channel": string(item)}}
		default:
			values[index] = map[string]any{"type": "string", "value": argument}
		}
	}
	params["functionDeclaration"] = declaration
	params["arguments"] = values
	params["target"] = map[string]any{"context": contextID}
	params["awaitPromise"] = true
	result, err := client.command(ctx, "script.callFunction", params)
	if err != nil {
		return nil, err
	}
	if result["type"] == "exception" {
		encoded, _ := json.Marshal(result)
		return nil, fmt.Errorf("页面函数异常: %s", encoded)
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

// pagePollInterval 为等待页面条件时两次检查的间隔
const pagePollInterval = 50 * time.Millisecond

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
		if err := waitContext(ctx, pagePollInterval); err != nil {
			return err
		}
	}
	return fmt.Errorf("等待页面条件超时: %s", expression)
}

// waitSnapshotFunction 在阶段期限内定位官网 snapshot 函数并安装 service 捕获，返回保存 service 的页面对象句柄
func (client *bidiClient) waitSnapshotFunction(ctx context.Context, contextID string, timeout time.Duration) (pageHandle, error) {
	deadline := time.Now().Add(timeout)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	for time.Now().Before(deadline) {
		page, err := client.callFunctionHandle(ctx, contextID, snapshotHookFunction)
		if err != nil && !retryablePageEvaluation(err) {
			return "", err
		}
		if err == nil && page != "" {
			return page, nil
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := waitContext(ctx, pagePollInterval); err != nil {
			return "", err
		}
	}
	return "", errors.New("官网高层 snapshot 函数定位超时")
}

// waitBlockedGenerateRequest 在阶段期限内等待网络拦截事件
func (client *bidiClient) waitBlockedGenerateRequest(ctx context.Context, timeout time.Duration) (blockedRequest, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case request := <-client.blocked:
		return request, nil
	case <-client.done:
		return blockedRequest{}, client.failure
	case <-ctx.Done():
		return blockedRequest{}, ctx.Err()
	case <-timer.C:
		return blockedRequest{}, errors.New("官网 GenerateContent 拦截事件超时")
	}
}

func retryablePageEvaluation(err error) bool {
	var commandError *bidiCommandError
	if !errors.As(err, &commandError) || commandError.method != "script.evaluate" && commandError.method != "script.callFunction" {
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

// snapshotHookFunction 是在页面中临时替换调用 .snapshot({ 的高层函数并保存官网 service 的函数声明，未定位到该函数时返回 null
const snapshotHookFunction = `() => {
  const makerSuite = window.default_MakerSuite;
  if (!makerSuite) return null;
  for (const key of Object.keys(makerSuite)) {
    let original;
    try {
      original = makerSuite[key];
      if (typeof original !== 'function') continue;
      const source = original.toString();
      if (!source.includes('.snapshot({') || !source.includes('content') || !source.includes('yield')) continue;
    } catch (_) {
      continue;
    }
    const page = {key, service: null, snapshot: digest => original.call(makerSuite, page.service, digest)};
    makerSuite[key] = function(...args) {
      const service = args[0];
      if (service && (typeof service === 'object' || typeof service === 'function')) {
        page.service = service;
        makerSuite[key] = original;
      }
      return original.apply(this, args);
    };
    return page;
  }
  return null;
}`

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
