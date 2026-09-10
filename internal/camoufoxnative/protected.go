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

type protectedResponseMetadata struct {
	Status  int         `json:"status"`
	Headers [][2]string `json:"headers"`
	Error   string      `json:"error"`
}

type protectedChunk struct {
	Data  []string `json:"data"`
	Done  bool     `json:"done"`
	Error string   `json:"error"`
}

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
	worker.mu.Unlock()

	encodedURL, _ := json.Marshal(rawURL)
	encodedHeaders, err := json.Marshal(browserRequestHeaders(headers))
	if err != nil {
		return nil, fmt.Errorf("编码浏览器请求头: %w", err)
	}
	encodedBody, _ := json.Marshal(string(body))
	requestID := rand.Text()
	encodedID, _ := json.Marshal(requestID)
	expression := fmt.Sprintf(`(() => {
  const id = %s;
  const requests = window.__aistudioProtectedRequests ||= new Map();
  const state = {controller: new AbortController(), status: 0, headers: [], chunks: [], done: false, error: ""};
  requests.set(id, state);
  (async () => {
    try {
      const response = await fetch(%s, {
        method: "POST",
        headers: %s,
        body: %s,
        credentials: "include",
        signal: state.controller.signal
      });
      state.status = response.status;
      state.headers = [...response.headers.entries()];
      if (!response.body) return;
      const reader = response.body.getReader();
      for (;;) {
        const {value, done} = await reader.read();
        while (state.chunks.length >= 8) {
          if (state.controller.signal.aborted) throw new DOMException("Aborted", "AbortError");
          await new Promise(resolve => setTimeout(resolve, 5));
        }
        if (done) break;
        let binary = "";
        for (let offset = 0; offset < value.length; offset += 32768) {
          binary += String.fromCharCode(...value.subarray(offset, offset + 32768));
        }
        state.chunks.push(btoa(binary));
      }
    } catch (error) {
      state.error = String(error);
    } finally {
      state.done = true;
    }
  })();
  return true;
})()`, encodedID, encodedURL, encodedHeaders, encodedBody)
	startCtx, cancelStart := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = client.evaluateBool(startCtx, contextID, expression)
	cancelStart()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("浏览器发送受保护请求: %w", err), worker.cancelProtectedRequest(requestID))
	}
	metadata, err := worker.waitProtectedHeaders(ctx, requestID)
	if err != nil {
		return nil, errors.Join(err, worker.cancelProtectedRequest(requestID))
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
			ctx: bodyCtx, cancel: cancelBody, worker: worker, requestID: requestID,
		},
	}, nil
}

// waitProtectedHeaders 通过短命令读取异步请求的响应头
func (worker *Worker) waitProtectedHeaders(ctx context.Context, requestID string) (protectedResponseMetadata, error) {
	encodedID, _ := json.Marshal(requestID)
	expression := fmt.Sprintf(`(() => {
  const state = window.__aistudioProtectedRequests.get(%s);
  return JSON.stringify({status: state.status, headers: state.headers, error: state.error});
})()`, encodedID)
	for {
		if err := ctx.Err(); err != nil {
			return protectedResponseMetadata{}, err
		}
		pollCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		value, err := worker.client.evaluateString(pollCtx, worker.contextID, expression)
		cancel()
		if err != nil {
			return protectedResponseMetadata{}, err
		}
		var metadata protectedResponseMetadata
		if err := json.Unmarshal([]byte(value), &metadata); err != nil {
			return metadata, fmt.Errorf("解析浏览器响应元数据: %w", err)
		}
		if metadata.Status > 0 {
			return metadata, nil
		}
		if metadata.Error != "" {
			return metadata, errors.New(metadata.Error)
		}
		if err := waitContext(ctx, 10*time.Millisecond); err != nil {
			return metadata, err
		}
	}
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

// StorageCookies 导出当前固定指纹浏览器 Cookie，供账户状态签名和持久化
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

	mu     sync.Mutex
	buffer []byte
	done   bool
	closed bool
}

func (body *protectedResponseBody) Read(target []byte) (int, error) {
	body.mu.Lock()
	defer body.mu.Unlock()
	if body.closed {
		return 0, io.ErrClosedPipe
	}
	for len(body.buffer) == 0 && !body.done {
		if err := body.ctx.Err(); err != nil {
			return 0, err
		}
		chunk, err := body.worker.readProtectedChunk(body.requestID)
		if err != nil {
			body.done = true
			return 0, errors.Join(err, body.worker.cancelProtectedRequest(body.requestID))
		}
		if chunk.Error != "" {
			body.done = true
			return 0, errors.Join(errors.New(chunk.Error), body.worker.cancelProtectedRequest(body.requestID))
		}
		for _, data := range chunk.Data {
			body.buffer, err = base64.StdEncoding.AppendDecode(body.buffer, []byte(data))
			if err != nil {
				return 0, fmt.Errorf("解码浏览器响应块: %w", err)
			}
		}
		body.done = chunk.Done
		if len(body.buffer) == 0 && !body.done {
			timer := time.NewTimer(10 * time.Millisecond)
			select {
			case <-body.ctx.Done():
				timer.Stop()
				return 0, body.ctx.Err()
			case <-timer.C:
			}
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

func (worker *Worker) readProtectedChunk(requestID string) (protectedChunk, error) {
	encodedID, _ := json.Marshal(requestID)
	expression := fmt.Sprintf(`(() => {
  const state = window.__aistudioProtectedRequests?.get(%s);
  if (!state) return JSON.stringify({done: true});
  const data = state.chunks.splice(0);
  const done = state.done && state.chunks.length === 0;
  const error = state.error;
  if (done) window.__aistudioProtectedRequests.delete(%s);
  return JSON.stringify({data, done, error});
})()`, encodedID, encodedID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	value, err := worker.client.evaluateString(ctx, worker.contextID, expression)
	if err != nil {
		return protectedChunk{}, err
	}
	var chunk protectedChunk
	if err := json.Unmarshal([]byte(value), &chunk); err != nil {
		return protectedChunk{}, fmt.Errorf("解析浏览器响应块: %w", err)
	}
	return chunk, nil
}

func (worker *Worker) cancelProtectedRequest(requestID string) error {
	encodedID, _ := json.Marshal(requestID)
	expression := fmt.Sprintf(`(() => {
  const state = window.__aistudioProtectedRequests?.get(%s);
  if (!state) return true;
  state.controller.abort();
  window.__aistudioProtectedRequests.delete(%s);
  return true;
})()`, encodedID, encodedID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := worker.client.evaluateBool(ctx, worker.contextID, expression)
	return err
}
