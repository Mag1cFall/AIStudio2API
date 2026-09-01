package aistudio

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

// NativeWorker 将纯 Go Camoufox runtime 适配为 WAA preparer
type NativeWorker struct {
	accountID   string
	runtime     *camoufoxnative.Worker
	operationMu sync.Mutex
	stateMu     sync.RWMutex
	state       WorkerState
}

var _ ProtectedPreparer = (*NativeWorker)(nil)
var _ ProtocolHeaderProvider = (*NativeWorker)(nil)

// NewNativeWorker 启动单个账户的纯 Go Camoufox runtime
func NewNativeWorker(ctx context.Context, accountID string, options camoufoxnative.Options) (*NativeWorker, error) {
	if accountID == "" {
		return nil, fmt.Errorf("缺少账户 ID")
	}
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

// Prepare 生成 fresh proof 并写入 GenerateContent 第五槽
func (worker *NativeWorker) Prepare(ctx context.Context, request ProtectedRequest) (PreparedProtectedRequest, error) {
	worker.operationMu.Lock()
	defer worker.operationMu.Unlock()
	worker.updateState(func(state *WorkerState) {
		state.Phase = WorkerBusy
		state.RequestCount++
		state.LastError = ""
	})
	digest := sha256.Sum256([]byte(request.Prompt))
	proof, err := worker.runtime.Proof(ctx, fmt.Sprintf("%x", digest), request.Prompt)
	if err != nil {
		worker.fail(err)
		return PreparedProtectedRequest{}, err
	}
	var payload []any
	if err := json.Unmarshal(request.Body, &payload); err != nil {
		worker.fail(err)
		return PreparedProtectedRequest{}, fmt.Errorf("解析受保护请求: %w", err)
	}
	if request.ProofField < 1 || len(payload) < request.ProofField {
		err := fmt.Errorf("受保护请求缺少 WAA field %d", request.ProofField)
		worker.fail(err)
		return PreparedProtectedRequest{}, err
	}
	payload[request.ProofField-1] = proof
	body, err := json.Marshal(payload)
	if err != nil {
		worker.fail(err)
		return PreparedProtectedRequest{}, fmt.Errorf("编码受保护请求: %w", err)
	}
	headers, err := worker.runtime.ProtocolHeaders(ctx)
	if err != nil {
		worker.fail(err)
		return PreparedProtectedRequest{}, err
	}
	worker.updateState(func(state *WorkerState) {
		state.Phase = WorkerReady
	})
	return PreparedProtectedRequest{
		Body:    body,
		Headers: headers,
	}, nil
}

// SendProtected 通过账户固定指纹 Camoufox 流式发送已准备的请求
func (worker *NativeWorker) SendProtected(ctx context.Context, request ProtectedRequest) (*RPCResponse, error) {
	response, err := worker.runtime.SendProtected(ctx, request.URL, request.Headers, request.Body)
	if err != nil {
		worker.fail(err)
		return nil, err
	}
	return &RPCResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header,
		Body:       response.Body,
	}, nil
}

// BrowserStorageState 返回固定指纹浏览器当前 Cookie 状态
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
func (worker *NativeWorker) ProtocolHeaders(ctx context.Context, accountID string) (http.Header, error) {
	if accountID != "" && accountID != worker.accountID {
		return nil, fmt.Errorf("runtime 账户不匹配")
	}
	return worker.runtime.ProtocolHeaders(ctx)
}

// State 返回纯 Go runtime 状态
func (worker *NativeWorker) State() WorkerState {
	worker.stateMu.RLock()
	defer worker.stateMu.RUnlock()
	return worker.state
}

// Close 关闭纯 Go runtime
func (worker *NativeWorker) Close() error {
	worker.operationMu.Lock()
	defer worker.operationMu.Unlock()
	worker.updateState(func(state *WorkerState) {
		state.Phase = WorkerClosing
		state.LastError = ""
	})
	err := worker.runtime.Close()
	worker.updateState(func(state *WorkerState) {
		if err != nil {
			state.LastError = err.Error()
			return
		}
		state.Phase = WorkerClosed
	})
	return err
}

func (worker *NativeWorker) updateState(update func(*WorkerState)) {
	worker.stateMu.Lock()
	defer worker.stateMu.Unlock()
	update(&worker.state)
}

func (worker *NativeWorker) fail(err error) {
	worker.updateState(func(state *WorkerState) {
		state.Phase = WorkerFailed
		state.LastError = err.Error()
	})
}
