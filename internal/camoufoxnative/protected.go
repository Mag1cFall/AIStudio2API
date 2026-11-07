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

// protectedMessage 是受保护请求通道上以 { 开头的控制消息：界面运行时先是发出标记，之后是状态码与响应头，最后是结束标记或错误；其余消息为 Base64 正文块
type protectedMessage struct {
	Sent    bool        `json:"sent"`
	Status  int         `json:"status"`
	Headers [][2]string `json:"headers"`
	Done    bool        `json:"done"`
	Error   string      `json:"error"`
}

// protectedSendKey 是让页面在用户输入的处理中发出受保护请求的一次 Pause 按键
var protectedSendKey = []any{map[string]any{"type": "key", "id": "keyboard", "actions": []any{
	map[string]any{"type": "keyDown", "value": ""},
	map[string]any{"type": "keyUp", "value": ""},
}}}

// protectedGrantBatch 是 Go 端每消费多少个正文块向页面授予一次额度，页面的初始额度为它的两倍
const protectedGrantBatch = 4

// protectedPump 是页面内等 request.xhr 收到响应头后经通道依次发出状态码与响应头、按 65536 个 UTF-16 码元分块并以 UTF-8 编码的 Base64 正文块与结束标记的语句，正文块受 request.credit 额度限制；page、id、request、failure、loaded、next 与 emit 由外层函数定义
const protectedPump = `
  (async () => {
    try {
      while ((!request.xhr || request.xhr.readyState < 2) && !failure) await next();
      if (failure) throw new Error(failure);
      const xhr = request.xhr;
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
`

// protectedSend 是页面内以 XMLHttpRequest 发起受保护请求并经通道发出响应的函数声明，activate 非空时在下一次可信按键的处理中发起并拦下该按键；参数依次为 Worker 页面对象、请求 ID、URL、请求头 JSON、正文、activate 与通道
const protectedSend = `(page, id, url, headers, body, activate, emit) => {
  const xhr = new XMLHttpRequest();
  const request = {xhr, credit: 8, resume: null};
  (page.requests ||= new Map()).set(id, request);
  let loaded = false, failure = "", wake = null;
  const changed = () => {
    wake?.();
    wake = null;
  };
  const next = () => new Promise(resolve => wake = resolve);
  const send = () => {
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
  };
  const keydown = event => {
    if (!event.isTrusted) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    removeEventListener("keydown", keydown, true);
    send();
  };
  const keyup = event => {
    if (!event.isTrusted) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    removeEventListener("keyup", keyup, true);
  };
  request.release = () => {
    removeEventListener("keydown", keydown, true);
    removeEventListener("keyup", keyup, true);
    if (xhr.readyState === 0) {
      failure = "AbortError: The operation was aborted.";
      changed();
    }
  };
  if (activate) {
    addEventListener("keydown", keydown, true);
    addEventListener("keyup", keyup, true);
  } else {
    send();
  }
` + protectedPump + `}`

// protectedCancel 是中止并删除受保护请求的函数声明
const protectedCancel = `(page, id) => {
  const request = page.requests?.get(id);
  request?.release();
  request?.xhr?.abort();
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

// SendProtected 通过固定指纹 Camoufox 页面发送请求，保留原生 TLS、HTTP/2、请求头、Cookie 和页面指纹；请求头带 Priority: u=0 时在一次 Pause 按键的处理中发出
func (worker *Worker) SendProtected(ctx context.Context, rawURL string, headers http.Header, body []byte) (*ProtectedResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	worker.lockInput()
	worker.mu.Lock()
	if worker.closed {
		worker.mu.Unlock()
		worker.inputMu.Unlock()
		return nil, errors.New("Camoufox runtime 已关闭")
	}
	client := worker.client
	contextID, page := worker.beginRequest()
	worker.mu.Unlock()
	worker.inputMu.Unlock()
	response, err := worker.sendProtected(ctx, client, contextID, page, rawURL, headers, body)
	if err != nil {
		worker.finishRequest(contextID)
	}
	return response, err
}

// sendProtected 在 contextID 标签页的 WAA 页面对象中发出受保护请求并等待响应头
func (worker *Worker) sendProtected(ctx context.Context, client *bidiClient, contextID string, page pageHandle, rawURL string, headers http.Header, body []byte) (*ProtectedResponse, error) {
	encodedHeaders, err := json.Marshal(browserRequestHeaders(headers))
	if err != nil {
		return nil, fmt.Errorf("编码浏览器请求头: %w", err)
	}
	requestID := rand.Text()
	channel, err := client.openChannel(requestID)
	if err != nil {
		return nil, fmt.Errorf("浏览器发送受保护请求: %w", err)
	}
	activate := ""
	if headers.Get("Priority") == "u=0" {
		activate = "keydown"
	}
	startCtx, cancelStart := context.WithTimeout(ctx, 5*time.Second)
	_, err = client.callFunction(startCtx, contextID, protectedSend, page, requestID, rawURL, string(encodedHeaders), string(body), activate, bidiChannelArgument(requestID))
	if err == nil && activate != "" {
		_, err = client.command(startCtx, "input.performActions", map[string]any{"context": contextID, "actions": protectedSendKey})
	}
	cancelStart()
	if err != nil {
		return nil, worker.abandonProtectedRequest(ctx, contextID, page, requestID, fmt.Errorf("浏览器发送受保护请求: %w", err))
	}
	status, header, err := readResponseHead(ctx, channel)
	if err != nil {
		return nil, worker.abandonProtectedRequest(ctx, contextID, page, requestID, err)
	}
	bodyCtx, cancelBody := context.WithCancel(ctx)
	return &ProtectedResponse{
		StatusCode: status,
		Header:     header,
		Body: &protectedResponseBody{
			ctx: bodyCtx, cancel: cancelBody, worker: worker, contextID: contextID, page: page, requestID: requestID, channel: channel,
		},
	}, nil
}

// readResponseHead 等待受保护请求通道上的状态码与响应头
func readResponseHead(ctx context.Context, channel *bidiChannel) (int, http.Header, error) {
	metadata, err := readProtectedMessage(ctx, channel)
	if err == nil && metadata.Error != "" {
		err = errors.New(metadata.Error)
	}
	if err != nil {
		return 0, nil, err
	}
	header := make(http.Header, len(metadata.Headers)+1)
	for _, pair := range metadata.Headers {
		header.Add(pair[0], pair[1])
	}
	if header.Get("Content-Type") == "" {
		header.Set("Content-Type", "application/json+protobuf")
	}
	return metadata.Status, header, nil
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

// StorageCookies 导出当前固定指纹浏览器 Cookie，启动时写入的 Cookie 保持写入顺序，之后出现的 Cookie 排在其后
func (worker *Worker) StorageCookies(ctx context.Context) ([]byte, error) {
	worker.mu.Lock()
	if worker.closed {
		worker.mu.Unlock()
		return nil, errors.New("Camoufox runtime 已关闭")
	}
	client := worker.client
	order := worker.cookieOrder
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
	rank := func(cookie storageCookie) int {
		if index, installed := order[cookie.key()]; installed {
			return index
		}
		return len(order)
	}
	sort.SliceStable(cookies, func(left, right int) bool {
		if leftRank, rightRank := rank(cookies[left]), rank(cookies[right]); leftRank != rightRank {
			return leftRank < rightRank
		}
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
	ctx    context.Context
	cancel context.CancelFunc
	worker *Worker
	// contextID 与 page 是发出请求的标签页与其中的 WAA 页面对象
	contextID string
	page      pageHandle
	requestID string
	channel   *bidiChannel
	// stop 在正文读完前关闭时中止请求，为空时在页面中止 XHR
	stop func() error
	// release 在关闭时结束请求所在的标签页使用，参数表示正文已读完；为空时按受保护请求结束
	release func(done bool)

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
			return 0, errors.Join(err, body.worker.cancelProtectedRequest(body.contextID, body.page, body.requestID))
		}
		if !strings.HasPrefix(value, "{") {
			body.buffer, err = base64.StdEncoding.AppendDecode(body.buffer, []byte(value))
			if err != nil {
				return 0, fmt.Errorf("解码浏览器响应块: %w", err)
			}
			if body.consumed++; body.consumed == protectedGrantBatch {
				body.consumed = 0
				if _, err := body.worker.client.callFunction(body.ctx, body.contextID, protectedGrant, body.page, body.requestID, strconv.Itoa(protectedGrantBatch)); err != nil {
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
	if body.release != nil {
		defer body.release(done)
	} else {
		defer body.worker.finishRequest(body.contextID)
	}
	if done {
		return nil
	}
	if body.stop != nil {
		body.worker.client.closeChannel(body.requestID)
		return body.stop()
	}
	return body.worker.cancelProtectedRequest(body.contextID, body.page, body.requestID)
}

// abandonProtectedRequest 在受保护请求出错时中止页面请求并返回 err，请求已取消时在后台中止，不等待忙碌的页面
func (worker *Worker) abandonProtectedRequest(ctx context.Context, contextID string, page pageHandle, requestID string, err error) error {
	if ctx.Err() != nil {
		go func() { _ = worker.cancelProtectedRequest(contextID, page, requestID) }()
		return err
	}
	return errors.Join(err, worker.cancelProtectedRequest(contextID, page, requestID))
}

// cancelProtectedRequest 注销通道并在发出请求的标签页中中止受保护请求
func (worker *Worker) cancelProtectedRequest(contextID string, page pageHandle, requestID string) error {
	worker.client.closeChannel(requestID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := worker.client.callFunction(ctx, contextID, protectedCancel, page, requestID)
	return err
}
