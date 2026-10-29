package aistudio

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

// NativeWorker 将单个账户的 Camoufox 或 SpiderMonkey WAA runtime 适配为受保护请求 preparer
type NativeWorker struct {
	accountID   string
	runtime     workerRuntime
	operationMu sync.Mutex
	stateMu     sync.RWMutex
	state       WorkerState
}

var _ ProtectedPreparer = (*NativeWorker)(nil)
var _ ProtocolHeaderProvider = (*NativeWorker)(nil)

// NewNativeWorker 启动单个账户的 Camoufox WAA runtime
func NewNativeWorker(ctx context.Context, accountID string, options camoufoxnative.Options) (*NativeWorker, error) {
	runtime, err := camoufoxnative.Start(ctx, options)
	if err != nil {
		return nil, err
	}
	runtimeState := runtime.State()
	return &NativeWorker{
		accountID: accountID,
		runtime:   runtime,
		state: WorkerState{
			AccountID: accountID,
			Phase:     WorkerReady,
			PID:       runtimeState.PID,
			RuntimeID: "native-webdriver-bidi",
			PageURL:   runtimeState.PageURL,
		},
	}, nil
}

// Prepare 生成 fresh proof 并写入请求指定的 WAA field
func (worker *NativeWorker) Prepare(ctx context.Context, request ProtectedRequest) (PreparedProtectedRequest, error) {
	start, end, err := arrayElementSpan(request.Body, request.ProofField-1)
	if err != nil {
		return PreparedProtectedRequest{}, fmt.Errorf("受保护请求缺少 WAA field %d: %w", request.ProofField, err)
	}
	proof, headers, err := worker.proofAndHeaders(ctx, request.Prompt, request.PagePrompt)
	if err != nil {
		return PreparedProtectedRequest{}, err
	}
	encoded, _ := json.Marshal(proof)
	body := make([]byte, 0, len(request.Body)-(end-start)+len(encoded))
	body = append(append(append(body, request.Body[:start]...), encoded...), request.Body[end:]...)
	return PreparedProtectedRequest{Body: body, Headers: headers}, nil
}

// proofAndHeaders 把 page 写入页面提示词后为 binding 的 SHA-256 生成 fresh proof，再取公共协议头
func (worker *NativeWorker) proofAndHeaders(ctx context.Context, binding, page string) (string, http.Header, error) {
	worker.operationMu.Lock()
	defer worker.operationMu.Unlock()
	worker.setPhase(WorkerBusy)
	digest := sha256.Sum256([]byte(binding))
	proof, err := worker.runtime.Proof(ctx, fmt.Sprintf("%x", digest), page)
	var headers http.Header
	if err == nil {
		headers, err = worker.runtime.ProtocolHeaders(ctx)
	}
	if err != nil {
		if ctx.Err() != nil {
			worker.setPhase(WorkerReady)
		} else {
			worker.setPhase(WorkerFailed)
		}
		return "", nil, err
	}
	worker.setPhase(WorkerReady)
	return proof, headers, nil
}

// arrayElementSpan 返回紧凑 JSON 顶层数组第 index 个元素的字节范围
func arrayElementSpan(body []byte, index int) (int, int, error) {
	depth, element, start := 0, 0, -1
	inString, escaped := false, false
	for position, char := range body {
		if inString {
			switch {
			case escaped:
				escaped = false
			case char == '\\':
				escaped = true
			case char == '"':
				inString = false
			}
			continue
		}
		switch char {
		case '"':
			inString = true
		case '[', '{':
			depth++
			if depth == 1 {
				continue
			}
		case ']', '}':
			depth--
			if depth == 0 {
				if element == index && start >= 0 {
					return start, position, nil
				}
				return 0, 0, fmt.Errorf("顶层数组只有 %d 个元素", element+1)
			}
		case ',':
			if depth == 1 {
				if element == index {
					return start, position, nil
				}
				element++
				start = -1
				continue
			}
		}
		if depth >= 1 && element == index && start < 0 {
			start = position
		}
	}
	return 0, 0, fmt.Errorf("JSON 顶层数组不完整")
}

// pageCountTokensDelay 是官网页面在 GenerateContent 发出后统计用户轮次 token 的时延
const pageCountTokensDelay = 2200 * time.Millisecond

// SendProtected 经账户 WAA runtime 流式发送已准备的请求，GenerateContent 发出 pageCountTokensDelay 后由 runtime 以相同请求头统计页面提示词的 token
func (worker *NativeWorker) SendProtected(ctx context.Context, request ProtectedRequest) (*RPCResponse, error) {
	if body, ok := countTokensBody(request); ok {
		ctx = camoufoxnative.WithFollowUp(ctx, camoufoxnative.FollowUp{URL: makerSuiteRPCURL + "CountTokens", Body: body, Delay: pageCountTokensDelay})
	}
	response, err := worker.runtime.SendProtected(ctx, request.URL, request.Headers, request.Body)
	if err != nil {
		if ctx.Err() == nil {
			worker.setPhase(WorkerFailed)
		}
		return nil, err
	}
	return &RPCResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header,
		Body:       response.Body,
	}, nil
}

// countTokensBody 按官网页面对用户轮次的统计，以 GenerateContent 的模型构造页面提示词的 CountTokens 正文；不是带页面提示词的 GenerateContent 时返回 false
func countTokensBody(generate ProtectedRequest) ([]byte, bool) {
	if !strings.HasSuffix(generate.URL, "/GenerateContent") || generate.PagePrompt == "" {
		return nil, false
	}
	var fields []json.RawMessage
	var model string
	if json.Unmarshal(generate.Body, &fields) != nil || len(fields) == 0 || json.Unmarshal(fields[0], &model) != nil {
		return nil, false
	}
	body, err := json.Marshal([]any{model, []any{[]any{[]any{[]any{nil, generate.PagePrompt}}, "user"}}})
	return body, err == nil
}

// BrowserStorageState 返回账户 WAA runtime 当前 Cookie 状态
func (worker *NativeWorker) BrowserStorageState(ctx context.Context) (StorageState, error) {
	encoded, err := worker.runtime.StorageCookies(ctx)
	if err != nil {
		return StorageState{}, err
	}
	var cookies []StateCookie
	if err := json.Unmarshal(encoded, &cookies); err != nil {
		return StorageState{}, fmt.Errorf("解析浏览器 Cookie: %w", err)
	}
	state := StorageState{Cookies: cookies}
	if err := state.Validate(); err != nil {
		return StorageState{}, err
	}
	return state, nil
}

// ProtocolHeaders 返回当前账户官网请求的动态公共头
func (worker *NativeWorker) ProtocolHeaders(ctx context.Context, _ string) (http.Header, error) {
	return worker.runtime.ProtocolHeaders(ctx)
}

// State 返回账户 WAA runtime 状态
func (worker *NativeWorker) State() WorkerState {
	worker.stateMu.RLock()
	defer worker.stateMu.RUnlock()
	return worker.state
}

// Close 关闭账户 WAA runtime
func (worker *NativeWorker) Close() error {
	worker.operationMu.Lock()
	defer worker.operationMu.Unlock()
	worker.setPhase(WorkerClosing)
	err := worker.runtime.Close()
	if err == nil {
		worker.setPhase(WorkerClosed)
	}
	return err
}

// setPhase 更新账户 WAA runtime 阶段
func (worker *NativeWorker) setPhase(phase WorkerPhase) {
	worker.stateMu.Lock()
	defer worker.stateMu.Unlock()
	worker.state.Phase = phase
}
