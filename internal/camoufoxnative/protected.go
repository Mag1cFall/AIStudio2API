package camoufoxnative

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ProtectedResponse 表示由固定指纹浏览器流式返回的 MakerSuite 响应
type ProtectedResponse struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

// protectedMessage 是受保护请求通道上以 { 开头的控制消息：先是状态码与响应头，之后是结束标记或错误；其余消息为 Base64 正文块
type protectedMessage struct {
	Status  int         `json:"status"`
	Headers [][2]string `json:"headers"`
	Done    bool        `json:"done"`
	Error   string      `json:"error"`
}

// FollowUp 是受保护请求发出 Delay 后以相同请求头发出的伴随请求，响应被丢弃
type FollowUp struct {
	URL   string
	Body  []byte
	Delay time.Duration
}

// followUpKey 是上下文中伴随请求的键
type followUpKey struct{}

// WithFollowUp 返回携带伴随请求的上下文，SendProtected 发出受保护请求时同时安排它
func WithFollowUp(ctx context.Context, followUp FollowUp) context.Context {
	return context.WithValue(ctx, followUpKey{}, followUp)
}

// FollowUpFrom 返回上下文中的伴随请求
func FollowUpFrom(ctx context.Context) (FollowUp, bool) {
	followUp, ok := ctx.Value(followUpKey{}).(FollowUp)
	return followUp, ok
}

// protectedGrantBatch 是 Go 端每消费多少个正文块向页面授予一次额度，页面的初始额度为它的两倍
const protectedGrantBatch = 4

// protectedSend 是页面内以 XMLHttpRequest 发起受保护请求并经通道依次发出响应头、正文块与结束标记的函数声明，参数依次为 Worker 页面对象、请求 ID、URL、请求头 JSON、正文、伴随请求 JSON 与通道
const protectedSend = `(page, id, url, headers, body, follow, emit) => {
  const xhr = new XMLHttpRequest();
  const request = {xhr, credit: 8, resume: null};
  (page.requests ||= new Map()).set(id, request);
  let loaded = false, failure = "", wake = null;
  const changed = () => {
    wake?.();
    wake = null;
  };
  const next = () => new Promise(resolve => wake = resolve);
  xhr.open("POST", url);
  xhr.withCredentials = true;
  for (const [name, value] of JSON.parse(headers)) xhr.setRequestHeader(name, value);
  xhr.onreadystatechange = xhr.onprogress = changed;
  xhr.onload = () => {
    loaded = true;
    changed();
  };
  xhr.onerror = () => {
    failure = "NetworkError when attempting to fetch resource.";
    changed();
  };
  xhr.onabort = () => {
    failure = "AbortError: The operation was aborted.";
    changed();
    request.resume?.();
  };
  xhr.send(body);
  if (follow) {
    const next = JSON.parse(follow);
    setTimeout(() => {
      const companion = new XMLHttpRequest();
      companion.open("POST", next.url);
      companion.withCredentials = true;
      for (const [name, value] of JSON.parse(headers)) companion.setRequestHeader(name, value);
      companion.send(next.body);
    }, next.delay);
  }
  (async () => {
    try {
      while (xhr.readyState < 2 && !failure) await next();
      if (failure) throw new Error(failure);
      const lines = xhr.getAllResponseHeaders().split(/\r\n/).filter(Boolean);
      emit(JSON.stringify({status: xhr.status, headers: lines.map(line => [line.slice(0, line.indexOf(":")), line.slice(line.indexOf(":") + 1).trim()])}));
      const encoder = new TextEncoder();
      for (let sent = 0;;) {
        const text = xhr.responseText;
        if (sent < text.length) {
          while (request.credit <= 0 && !failure) await new Promise(resolve => request.resume = resolve);
          if (failure) throw new Error(failure);
          let end = Math.min(text.length, sent + 65536);
          if (end < text.length && text.charCodeAt(end - 1) >= 0xd800 && text.charCodeAt(end - 1) <= 0xdbff) end--;
          const block = encoder.encode(text.slice(sent, end));
          let binary = "";
          for (let start = 0; start < block.length; start += 32768) {
            binary += String.fromCharCode(...block.subarray(start, start + 32768));
          }
          emit(btoa(binary));
          request.credit--;
          sent = end;
          continue;
        }
        if (failure) throw new Error(failure);
        if (loaded) break;
        await next();
      }
      emit(JSON.stringify({done: true}));
    } catch (error) {
      emit(JSON.stringify({error: String(error)}));
    } finally {
      page.requests.delete(id);
    }
  })();
}`

// protectedCancel 是中止并删除受保护请求的函数声明
const protectedCancel = `(page, id) => {
  const request = page.requests?.get(id);
  request?.xhr.abort();
  request?.resume?.();
  page.requests?.delete(id);
}`

// protectedGrant 是为受保护请求增加正文块额度并唤醒等待中的读取的函数声明
const protectedGrant = `(page, id, count) => {
  const request = page.requests?.get(id);
  if (!request) return;
  request.credit += Number(count);
  request.resume?.();
  request.resume = null;
}`

// SendProtected 通过固定指纹 Camoufox 页面发送请求，保留原生 TLS、HTTP/2、请求头、Cookie 和页面指纹
func (worker *Worker) SendProtected(ctx context.Context, rawURL string, headers http.Header, body []byte) (*ProtectedResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	worker.mu.Lock()
	if worker.closed {
		worker.mu.Unlock()
		return nil, errors.New("Camoufox runtime 已关闭")
	}
	client := worker.client
	contextID := worker.contextID
	page := worker.page
	worker.mu.Unlock()

	encodedHeaders, err := json.Marshal(browserRequestHeaders(headers))
	if err != nil {
		return nil, fmt.Errorf("编码浏览器请求头: %w", err)
	}
	follow := ""
	if followUp, ok := FollowUpFrom(ctx); ok {
		encoded, err := json.Marshal(map[string]any{"url": followUp.URL, "body": string(followUp.Body), "delay": followUp.Delay.Milliseconds()})
		if err != nil {
			return nil, fmt.Errorf("编码伴随请求: %w", err)
		}
		follow = string(encoded)
	}
	requestID := rand.Text()
	channel, err := client.openChannel(requestID)
	if err != nil {
		return nil, fmt.Errorf("浏览器发送受保护请求: %w", err)
	}
	startCtx, cancelStart := context.WithTimeout(ctx, 5*time.Second)
	_, err = client.callFunction(startCtx, contextID, protectedSend, page, requestID, rawURL, string(encodedHeaders), string(body), follow, bidiChannelArgument(requestID))
	cancelStart()
	if err != nil {
		return nil, worker.abandonProtectedRequest(ctx, requestID, fmt.Errorf("浏览器发送受保护请求: %w", err))
	}
	metadata, err := readProtectedMessage(ctx, channel)
	if err == nil && metadata.Error != "" {
		err = errors.New(metadata.Error)
	}
	if err != nil {
		return nil, worker.abandonProtectedRequest(ctx, requestID, err)
	}
	responseHeaders := make(http.Header, len(metadata.Headers)+1)
	for _, pair := range metadata.Headers {
		responseHeaders.Add(pair[0], pair[1])
	}
	if responseHeaders.Get("Content-Type") == "" {
		responseHeaders.Set("Content-Type", "application/json+protobuf")
	}
	bodyCtx, cancelBody := context.WithCancel(ctx)
	return &ProtectedResponse{
		StatusCode: metadata.Status,
		Header:     responseHeaders,
		Body: &protectedResponseBody{
			ctx: bodyCtx, cancel: cancelBody, worker: worker, requestID: requestID, channel: channel,
		},
	}, nil
}

// readProtectedMessage 等待受保护请求通道上的下一条控制消息
func readProtectedMessage(ctx context.Context, channel *bidiChannel) (protectedMessage, error) {
	value, err := channel.next(ctx)
	if err != nil {
		return protectedMessage{}, err
	}
	var message protectedMessage
	if err := json.Unmarshal([]byte(value), &message); err != nil {
		return message, fmt.Errorf("解析浏览器响应消息: %w", err)
	}
	return message, nil
}

func browserRequestHeaders(headers http.Header) [][2]string {
	order := []string{
		"X-AIStudio-G1-Tier",
		"X-AIStudio-Visit-Id",
		"X-Goog-Ext-519733851-bin",
		"X-Goog-Api-Key",
		"X-Goog-AuthUser",
		"Authorization",
		"Content-Type",
		"X-User-Agent",
	}
	result := make([][2]string, 0, len(order))
	for _, name := range order {
		if value := strings.TrimSpace(headers.Get(name)); value != "" {
			result = append(result, [2]string{name, value})
		}
	}
	return result
}

// StorageCookies 导出当前固定指纹浏览器 Cookie
func (worker *Worker) StorageCookies(ctx context.Context) ([]byte, error) {
	worker.mu.Lock()
	if worker.closed {
		worker.mu.Unlock()
		return nil, errors.New("Camoufox runtime 已关闭")
	}
	client := worker.client
	worker.mu.Unlock()
	result, err := client.command(ctx, "storage.getCookies", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("导出浏览器 Cookie: %w", err)
	}
	items, _ := result["cookies"].([]any)
	cookies := make([]storageCookie, 0, len(items))
	for _, raw := range items {
		value, _ := raw.(map[string]any)
		if cookie, ok := decodeStorageCookie(value); ok {
			cookies = append(cookies, cookie)
		}
	}
	sort.SliceStable(cookies, func(left, right int) bool {
		if cookies[left].Domain != cookies[right].Domain {
			return cookies[left].Domain < cookies[right].Domain
		}
		if cookies[left].Name != cookies[right].Name {
			return cookies[left].Name < cookies[right].Name
		}
		return cookies[left].Path < cookies[right].Path
	})
	return json.Marshal(cookies)
}

type protectedResponseBody struct {
	ctx       context.Context
	cancel    context.CancelFunc
	worker    *Worker
	requestID string
	channel   *bidiChannel

	mu       sync.Mutex
	buffer   []byte
	consumed int
	done     bool
	closed   bool
}

func (body *protectedResponseBody) Read(target []byte) (int, error) {
	body.mu.Lock()
	defer body.mu.Unlock()
	if body.closed {
		return 0, io.ErrClosedPipe
	}
	for len(body.buffer) == 0 && !body.done {
		value, err := body.channel.next(body.ctx)
		if err := body.ctx.Err(); err != nil {
			return 0, err
		}
		if err != nil {
			body.done = true
			return 0, errors.Join(err, body.worker.cancelProtectedRequest(body.requestID))
		}
		if !strings.HasPrefix(value, "{") {
			body.buffer, err = base64.StdEncoding.AppendDecode(body.buffer, []byte(value))
			if err != nil {
				return 0, fmt.Errorf("解码浏览器响应块: %w", err)
			}
			if body.consumed++; body.consumed == protectedGrantBatch {
				body.consumed = 0
				if _, err := body.worker.client.callFunction(body.ctx, body.worker.contextID, protectedGrant, body.worker.page, body.requestID, strconv.Itoa(protectedGrantBatch)); err != nil {
					return 0, fmt.Errorf("浏览器继续读取受保护响应: %w", err)
				}
			}
			continue
		}
		var message protectedMessage
		if err := json.Unmarshal([]byte(value), &message); err != nil {
			return 0, fmt.Errorf("解析浏览器响应消息: %w", err)
		}
		if message.Error != "" || message.Done {
			body.done = true
			body.worker.client.closeChannel(body.requestID)
		}
		if message.Error != "" {
			return 0, errors.New(message.Error)
		}
	}
	if len(body.buffer) == 0 {
		return 0, io.EOF
	}
	n := copy(target, body.buffer)
	body.buffer = body.buffer[n:]
	return n, nil
}

func (body *protectedResponseBody) Close() error {
	body.cancel()
	body.mu.Lock()
	if body.closed {
		body.mu.Unlock()
		return nil
	}
	body.closed = true
	done := body.done
	body.mu.Unlock()
	if done {
		return nil
	}
	return body.worker.cancelProtectedRequest(body.requestID)
}

// abandonProtectedRequest 在受保护请求出错时中止页面请求并返回 err，请求已取消时在后台中止，不等待忙碌的页面
func (worker *Worker) abandonProtectedRequest(ctx context.Context, requestID string, err error) error {
	if ctx.Err() != nil {
		go func() { _ = worker.cancelProtectedRequest(requestID) }()
		return err
	}
	return errors.Join(err, worker.cancelProtectedRequest(requestID))
}

// cancelProtectedRequest 注销通道并在页面中中止受保护请求
func (worker *Worker) cancelProtectedRequest(requestID string) error {
	worker.client.closeChannel(requestID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := worker.client.callFunction(ctx, worker.contextID, protectedCancel, worker.page, requestID)
	return err
}
