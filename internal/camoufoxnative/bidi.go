package camoufoxnative

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"net/url"
	"path"
	"slices"
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
	// accessTokenStatus 为最近一次 GenerateAccessToken 的响应状态
	accessTokenStatus atomic.Int64
	// denial 是页面导航到官网拒绝页时的原因，之后的命令立即以它返回
	denial *AccessDenial
	// listener 接收 BiDi 事件，在读取连接的协程中调用
	listener func(map[string]any)
	// loads 是正在载入官网页面的标签页记下的网络状态，键为标签页的 context
	loads map[string]*pageLoad
}

// pageLoad 是一个标签页载入官网页面期间从网络事件记下的状态
type pageLoad struct {
	// responses 是目录 RPC 响应完成时的请求 ID，键为 RPC 方法名
	responses map[string]string
	// headers 是页面第一个带 x-aistudio-visit-id 的 MakerSuite 请求的请求头，键为小写名称
	headers map[string]string
	// image 表示官方 WAA 程序的 /generate_204 图片请求已结束，网络错误与 204 一样在页面中以 error 事件结束
	image bool
	// bundle 是页面载入的官网前端主程序（boq-makersuite 的 m=_b 模块）地址
	bundle string
}

// bidiPending 是等待响应的一条命令
type bidiPending struct {
	method   string
	response chan bidiResponse
}

type bidiCommandError struct {
	method  string
	code    string
	message string
}

func (err *bidiCommandError) Error() string {
	return fmt.Sprintf("BiDi %s 失败: %s", err.method, err.code+": "+err.message)
}

// newBiDiClient 创建 WebDriver BiDi 客户端，读取协程按命令 ID 交付响应并分发事件，多条命令可同时等待响应
func newBiDiClient(connection *websocket.Conn) *bidiClient {
	client := &bidiClient{
		connection: connection,
		pending:    make(map[int]bidiPending),
		channels:   make(map[string]*bidiChannel),
		done:       make(chan struct{}),
		loads:      make(map[string]*pageLoad),
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
	if client.denial != nil {
		denial := client.denial
		client.mu.Unlock()
		return nil, denial
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
		code, _ := message["error"].(string)
		detail, _ := message["message"].(string)
		pending.response <- bidiResponse{err: &bidiCommandError{method: pending.method, code: code, message: detail}}
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

// observe 把事件交给 listener 与 script.message 通道，记下载入中标签页的目录 RPC、公共请求头与 WAA 图片请求，以及 GenerateAccessToken 的响应状态和跳转到地区不可用页的导航
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
		if channel == nil {
			return
		}
		data, _ := params["data"].(map[string]any)
		if handle, ok := data["handle"].(string); ok {
			channel.push(handle)
		} else {
			channel.push(remoteBytesValue(data))
		}
		return
	case method == "browsingContext.navigationStarted":
		rawURL, _ := params["url"].(string)
		if parsed, err := url.Parse(rawURL); err == nil {
			if denial := PageDenial(parsed); denial != nil {
				client.mu.Lock()
				client.denial = denial
				client.mu.Unlock()
			}
		}
		return
	case !strings.HasPrefix(method, "network."):
		return
	}
	request, _ := params["request"].(map[string]any)
	rawURL, _ := request["url"].(string)
	requestMethod, _ := request["method"].(string)
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	contextID, _ := params["context"].(string)
	client.mu.Lock()
	defer client.mu.Unlock()
	load := client.loads[contextID]
	if load != nil && (method == "network.responseCompleted" || method == "network.fetchError") && strings.EqualFold(parsed.Hostname(), "aistudio.google.com") && path.Base(parsed.Path) == "generate_204" {
		load.image = true
		return
	}
	if load != nil && method == "network.responseCompleted" && load.bundle == "" && FrontendBundle(parsed) {
		load.bundle = rawURL
		return
	}
	if !strings.EqualFold(parsed.Hostname(), "alkalimakersuite-pa.clients6.google.com") || requestMethod != http.MethodPost {
		return
	}
	if parsed.Path == accessTokenPath {
		response, _ := params["response"].(map[string]any)
		if status, ok := number(response["status"]); ok && method == "network.responseCompleted" {
			client.accessTokenStatus.Store(int64(status))
		}
	}
	if load == nil {
		return
	}
	if name := path.Base(parsed.Path); method == "network.responseCompleted" && slices.Contains(pageCatalogRPCs, name) {
		load.responses[name], _ = request["request"].(string)
	}
	if method != "network.beforeRequestSent" || load.headers != nil {
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
	if headers["x-aistudio-visit-id"] != "" {
		load.headers = headers
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

// installCookies 按 storage state 的顺序与分区逐个恢复 Cookie
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

// servicePreloadFunction 是先于官网页面脚本执行的 preload，只在仍在解析的顶层官网文档中生效：第一个 bundle 写入 default_MakerSuite 时把它恢复为普通数据属性，并在随后的微任务中定位官网 WAA service 类（原型带 initialize，getMetadata 返回 X-Goog-Api-Key），该类实例首次调用 getMetadata 时换回原方法并经通道交出实例
const servicePreloadFunction = `emit => {
  if (window !== window.top || location.origin !== 'https://aistudio.google.com' || document.readyState !== 'loading') return;
  Object.defineProperty(window, 'default_MakerSuite', {configurable: true, enumerable: true, get: () => undefined, set: namespace => {
    Object.defineProperty(window, 'default_MakerSuite', {value: namespace, writable: true, enumerable: true, configurable: true});
    queueMicrotask(() => {
      for (const key of Object.keys(namespace)) {
        let prototype, original;
        try {
          prototype = typeof namespace[key] === 'function' ? namespace[key].prototype : null;
          original = prototype && Object.getOwnPropertyDescriptor(prototype, 'getMetadata')?.value;
          if (typeof original !== 'function' || typeof prototype.initialize !== 'function' || !String(original).includes('X-Goog-Api-Key')) continue;
        } catch (_) {
          continue;
        }
        prototype.getMetadata = function() {
          prototype.getMetadata = original;
          emit(this);
          return original.apply(this, arguments);
        };
        return;
      }
    });
  }});
}`

// pageObjectFunction 以官网 WAA service 构造 Worker 持有的页面对象：key 与 original 是 default_MakerSuite 中调用 .snapshot({ 的高层 proof 函数，snapshot 以该 service 为 digest 取 proof；没有该函数时返回 null
const pageObjectFunction = `service => {
  const makerSuite = window.default_MakerSuite;
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
    return {key, original, service, snapshot: digest => original.call(makerSuite, service, digest)};
  }
  return null;
}`

// pageCatalogRPCs 是官网页面载入时读取条款状态、权益与模型目录的 RPC
var pageCatalogRPCs = []string{"GetLoggingContext", "GetAiStudioBenefitTier", "ListModels"}

// startLoad 开始记下 contextID 标签页载入页面期间的网络状态
func (client *bidiClient) startLoad(contextID string) {
	client.mu.Lock()
	client.loads[contextID] = &pageLoad{responses: make(map[string]string)}
	client.mu.Unlock()
}

// endLoad 停止记下 contextID 标签页的网络状态
func (client *bidiClient) endLoad(contextID string) {
	client.mu.Lock()
	delete(client.loads, contextID)
	client.mu.Unlock()
}

// waitLoad 在 timeout 内等待 contextID 标签页的载入状态满足 ready，what 为超时错误中页面没有做到的事
func (client *bidiClient) waitLoad(ctx context.Context, contextID string, timeout time.Duration, what string, ready func(*pageLoad) bool) error {
	deadline := time.Now().Add(timeout)
	for {
		client.mu.Lock()
		done := ready(client.loads[contextID])
		client.mu.Unlock()
		if done {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("页面载入 %s 内没有%s", timeout, what)
		}
		if err := waitContext(ctx, 100*time.Millisecond); err != nil {
			return fmt.Errorf("等待页面载入%s: %w", what, err)
		}
	}
}

// pageCatalog 等页面目录 RPC 的响应都完成后经 collector 读取它们的正文
func (client *bidiClient) pageCatalog(ctx context.Context, contextID, collector string, timeout time.Duration) (PageCatalog, error) {
	if err := client.waitLoad(ctx, contextID, timeout, "完成目录 RPC", func(load *pageLoad) bool { return len(load.responses) == len(pageCatalogRPCs) }); err != nil {
		client.mu.Lock()
		completed := slices.Sorted(maps.Keys(client.loads[contextID].responses))
		client.mu.Unlock()
		return PageCatalog{}, fmt.Errorf("%w，已完成 %v", err, completed)
	}
	client.mu.Lock()
	requests := maps.Clone(client.loads[contextID].responses)
	bundle := client.loads[contextID].bundle
	client.mu.Unlock()
	bodies := make(map[string][]byte, len(requests))
	for name, requestID := range requests {
		data, err := client.command(ctx, "network.getData", map[string]any{"request": requestID, "dataType": "response", "collector": collector})
		if err != nil {
			return PageCatalog{}, fmt.Errorf("读取页面 %s 响应: %w", name, err)
		}
		bodies[name], err = remoteBytes(data["bytes"])
		if err != nil {
			return PageCatalog{}, fmt.Errorf("解码页面 %s 响应: %w", name, err)
		}
	}
	return PageCatalog{Logging: bodies["GetLoggingContext"], Tier: bodies["GetAiStudioBenefitTier"], Models: bodies["ListModels"], Bundle: bundle}, nil
}

// remoteBytes 按 Firefox network.getData 的编码还原响应正文：正文先按 UTF-8 解码为文本，base64 值是文本逐字符取 Latin-1 字节的编码，文本含 U+00FF 以上字符时值为文本本身
func remoteBytes(value any) ([]byte, error) {
	item, _ := value.(map[string]any)
	raw, _ := item["value"].(string)
	if item["type"] != "base64" || strings.ContainsFunc(raw, func(code rune) bool { return code > 0xFF }) {
		return []byte(raw), nil
	}
	latin1, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	text := make([]rune, len(latin1))
	for index, code := range latin1 {
		text[index] = rune(code)
	}
	return []byte(string(text)), nil
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
