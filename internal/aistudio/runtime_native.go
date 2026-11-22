package aistudio

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/Mag1cFall/AIStudio2API/internal/waa/prooftime"
)

// NativeWorker 将单个账户的 Camoufox 或 SpiderMonkey WAA runtime 适配为受保护请求 preparer
type NativeWorker struct {
	accountID string
	// runtimeMu 保护 runtime，重新载入页面后切换为新页面的 runtime
	runtimeMu   sync.RWMutex
	runtime     workerRuntime
	reloadMu    sync.Mutex
	operationMu sync.Mutex
	stateMu     sync.RWMutex
	state       WorkerState
	// tabsMu 保护 tabs 与各页面的 holder、used、retired；tabs 是账户的各页面，第一个是主页面，其余是账户并发时按需打开的页面与重新载入前仍有请求在用、等其租约归还后关闭的页面
	tabsMu sync.Mutex
	tabs   []*pageTab
	// openingMu 串行化按需新开页面，关闭页面时先等进行中的新开结束；spare 表示后台正在打开一个页面，openErr 是最近一次后台新开失败的原因，tabsChanged 在有页面空出、新开结束或 Worker 关闭时关闭并换新，都由 tabsMu 保护
	openingMu   sync.Mutex
	spare       bool
	openErr     error
	tabsChanged chan struct{}
}

// ErrProtectedTransport 表示 SpiderMonkey 后端受保护请求在出口连接上失败，VM 与页面状态不受影响
var ErrProtectedTransport = errors.New("受保护请求网络错误")

var _ ProtectedPreparer = (*NativeWorker)(nil)
var _ ProtocolHeaderProvider = (*NativeWorker)(nil)

// camoufoxRuntime 是 Camoufox 后端的 workerRuntime，重新载入在同一浏览器中进行
type camoufoxRuntime struct {
	*camoufoxnative.Worker
}

// Reload 按官网冷启动重新载入页面：当前标签页空闲时在其中重新载入，有进行中的请求时在新标签页中载入并切换过去，原标签页在请求结束后关闭；返回同一 runtime
func (runtime camoufoxRuntime) Reload(ctx context.Context) (workerRuntime, error) {
	return runtime, runtime.Worker.Reload(ctx)
}

// SendProtected 在页面中以 fetch 发出写入 proof 后的完整正文
func (runtime camoufoxRuntime) SendProtected(ctx context.Context, rawURL string, headers http.Header, body protectedBody) (*camoufoxnative.ProtectedResponse, error) {
	return runtime.Worker.SendProtected(ctx, rawURL, headers, body.Bytes())
}

// MergeSetCookieHeaders 把账户在页面之外取得的响应 Cookie 写入浏览器
func (runtime camoufoxRuntime) MergeSetCookieHeaders(ctx context.Context, headers []string, requestURL string) error {
	var state StorageState
	if err := state.MergeSetCookieHeaders(headers, requestURL, time.Now()); err != nil {
		return err
	}
	encoded, err := json.Marshal(state.Cookies)
	if err != nil {
		return err
	}
	return runtime.Worker.SetCookies(ctx, encoded)
}

// ProtocolHeaders 返回页面的公共协议头，X-Goog-Ext-519733851-Bin 与官网页面一样由页面载入时 GetLoggingContext 的响应得出
func (runtime camoufoxRuntime) ProtocolHeaders(ctx context.Context) (http.Header, error) {
	headers, err := runtime.Worker.ProtocolHeaders(ctx)
	if err != nil {
		return nil, err
	}
	encoded, err := encodeJSPBFields(runtime.PageCatalog().Logging)
	if err != nil {
		return nil, fmt.Errorf("编码官网日志上下文: %w", err)
	}
	headers.Set("X-Goog-Ext-519733851-Bin", base64.StdEncoding.EncodeToString(encoded))
	return headers, nil
}

// NewNativeWorker 启动单个账户的 Camoufox WAA runtime
func NewNativeWorker(ctx context.Context, accountID string, options camoufoxnative.Options) (*NativeWorker, error) {
	runtime, err := camoufoxnative.Start(ctx, options)
	if err != nil {
		return nil, err
	}
	runtimeState := runtime.State()
	worker := &NativeWorker{
		accountID: accountID,
		runtime:   camoufoxRuntime{runtime},
		state: WorkerState{
			AccountID: accountID,
			Phase:     WorkerReady,
			PID:       runtimeState.PID,
			RuntimeID: "native-webdriver-bidi",
			PageURL:   runtimeState.PageURL,
		},
	}
	pageWorkers.Store(accountID, worker)
	return worker, nil
}

// pageGenerator 是在页面界面运行 GenerateContent 的 workerRuntime，页面应用点击 Run 后自己取 proof 并发出请求
type pageGenerator interface {
	Generate(context.Context, camoufoxnative.GenerateTurn) (*camoufoxnative.ProtectedResponse, error)
}

// functionContinuer 是能判断页面最后一轮是否为请求所回应的函数调用的 workerRuntime
type functionContinuer interface {
	ContinuesFunctionCall([]byte) bool
}

// skipFunctionTabKey 标记请求在函数调用标签页不可用后改走非界面取 proof 路径
type skipFunctionTabKey struct{}

// generator 返回在页面界面运行该请求的 runtime：Camoufox 后端、未带 proof 的 GenerateContent，最后一个轮次是带文本的 user 轮次，或是回应页面最后一轮函数调用的函数结果
func (worker *NativeWorker) generator(ctx context.Context, request ProtectedRequest) (pageGenerator, bool) {
	runtime := worker.current()
	generator, ok := runtime.(pageGenerator)
	if !ok || request.Proof != "" || !strings.HasSuffix(request.URL, "/GenerateContent") {
		return nil, false
	}
	if _, typed := camoufoxnative.GeneratePrompt(request.Body); typed {
		return generator, true
	}
	if ctx.Value(skipFunctionTabKey{}) != nil {
		return nil, false
	}
	continuer, ok := runtime.(functionContinuer)
	_, result := wireFunctionResult(request.Body)
	return generator, ok && result && continuer.ContinuesFunctionCall(request.Body)
}

// missingFunctionTab 判断请求本应在 Camoufox 函数调用标签页由页面取 proof，但该标签页已不可用
func (worker *NativeWorker) missingFunctionTab(request ProtectedRequest) bool {
	if !strings.HasSuffix(request.URL, "/GenerateContent") {
		return false
	}
	runtime := worker.current()
	if _, ok := runtime.(pageGenerator); !ok {
		return false
	}
	if _, ok := runtime.(functionContinuer); !ok {
		return false
	}
	_, result := wireFunctionResult(request.Body)
	return result
}

// resultSubmitter 是像官网用户一样在函数调用块中粘贴函数结果并点击提交后取 proof 的 workerRuntime
type resultSubmitter interface {
	SubmitProof(ctx context.Context, digest, result string, rounds int) (string, error)
}

// submitRounds 返回提交函数结果时页面对话的轮数：GenerateContent 正文中除最后一个函数结果外的 user 轮次数
func submitRounds(body []byte) int {
	fields, ok := wireArray(body)
	if !ok || len(fields) < 2 {
		return 1
	}
	contents, ok := wireArray(fields[1])
	if !ok {
		return 1
	}
	rounds := 0
	for _, raw := range contents {
		content, _ := wireArray(raw)
		var role string
		if len(content) > 1 && json.Unmarshal(content[1], &role) == nil && role == "user" {
			rounds++
		}
	}
	return max(rounds-1, 1)
}

// Prepare 生成 fresh proof，请求带 Proof 时沿用该 proof，正文原样返回，发送时在 ProofAt 处写入；在页面界面运行的 GenerateContent 由页面应用取 proof，只返回公共协议头
func (worker *NativeWorker) Prepare(ctx context.Context, request ProtectedRequest) (PreparedProtectedRequest, error) {
	if _, ok := worker.generator(ctx, request); ok {
		headers, err := worker.current().ProtocolHeaders(ctx)
		if err != nil {
			return PreparedProtectedRequest{}, err
		}
		return PreparedProtectedRequest{Body: request.Body, Headers: headers, PagePrompt: request.PagePrompt}, nil
	}
	proof, page := request.Proof, request.PagePrompt
	var headers http.Header
	var err error
	if proof == "" {
		gesture := ctx
		if !strings.HasSuffix(request.URL, "/GenerateTitle") {
			gesture = context.WithValue(ctx, userGestureKey{}, true)
		}
		_, rerun := ctx.Value(pageRerunKey{}).(int)
		generate := strings.HasSuffix(request.URL, "/GenerateContent")
		result, submit := "", false
		if generate && request.PagePrompt == "" && !rerun {
			result, submit = wireFunctionResult(request.Body)
		}
		proof, page, headers, err = worker.proofAndHeaders(gesture, proofDigest(request), request.PagePrompt, request.Body, generate && (request.PagePrompt != "" || rerun), result, submit)
	} else {
		headers, err = worker.current().ProtocolHeaders(ctx)
	}
	if err != nil {
		return PreparedProtectedRequest{}, err
	}
	return PreparedProtectedRequest{Body: request.Body, ProofAt: request.ProofAt, Headers: headers, Proof: proof, PagePrompt: page}, nil
}

// pageRunner 是按 GenerateContent 正文点击 Run 或 Rerun 后取 proof 的 workerRuntime，返回 proof 与粘贴进页面的提示词
type pageRunner interface {
	RunProof(ctx context.Context, digest, prompt string, body []byte) (string, string, error)
}

// proofAndHeaders 把 page 写入页面提示词后为 digest 生成 fresh proof，run 时写入后点击 Run，submit 时在函数调用块中粘贴函数结果 result 后点击提交，再取公共协议头；返回 proof、页面输入框中的提示词（run 时为页面实际粘贴的文本）与协议头
func (worker *NativeWorker) proofAndHeaders(ctx context.Context, digest, page string, body []byte, run bool, result string, submit bool) (string, string, http.Header, error) {
	lockStarted := time.Now()
	tab, err := worker.usePage(ctx)
	if err != nil {
		return "", "", nil, err
	}
	runtime, unlock := worker.lockPage(tab)
	defer unlock()
	prooftime.Observe(ctx, prooftime.Lock, time.Since(lockStarted))
	worker.setPhase(WorkerBusy)
	var proof string
	submitter, submits := runtime.(resultSubmitter)
	if runner, ok := runtime.(pageRunner); ok && run {
		proof, page, err = runner.RunProof(ctx, digest, page, body)
	} else if submits && submit {
		proof, err = submitter.SubmitProof(ctx, digest, result, submitRounds(body))
	} else {
		proof, err = runtime.Proof(ctx, digest, page)
	}
	var headers http.Header
	if err == nil {
		headers, err = runtime.ProtocolHeaders(ctx)
	}
	if err != nil {
		if ctx.Err() != nil {
			worker.setPhase(WorkerReady)
		} else {
			worker.setPhase(WorkerFailed)
		}
		return "", "", nil, err
	}
	worker.setPhase(WorkerReady)
	return proof, page, headers, nil
}

// marshalWAAArray 把 wire 编码为与 json.Marshal 相同的 JSON 数组，返回正文与第 field 个元素（WAA proof 字段，取值为 nil）在正文中的起始字节
func marshalWAAArray(wire []any, field int) ([]byte, int, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	buffer.WriteByte('[')
	at := 0
	for index, item := range wire {
		if index > 0 {
			buffer.WriteByte(',')
		}
		if index == field-1 {
			at = buffer.Len()
		}
		if err := encoder.Encode(item); err != nil {
			return nil, 0, err
		}
		buffer.Truncate(buffer.Len() - 1)
	}
	buffer.WriteByte(']')
	return buffer.Bytes(), at, nil
}

// protectedBody 是发往上游的受保护请求正文：data 中第 at 字节起的 null 发送时换成 proof 的 JSON 字符串，proof 为空时原样发送
type protectedBody struct {
	data  []byte
	at    int
	proof []byte
}

// newProtectedBody 返回在 data 第 at 字节起的 null 处写入 proof 的正文，proof 为空时原样发送
func newProtectedBody(data []byte, at int, proof string) protectedBody {
	if proof == "" {
		return protectedBody{data: data}
	}
	encoded, _ := json.Marshal(proof)
	return protectedBody{data: data, at: at, proof: encoded}
}

// Len 返回发送的正文字节数
func (body protectedBody) Len() int {
	if body.proof == nil {
		return len(body.data)
	}
	return len(body.data) - len("null") + len(body.proof)
}

// Reader 依次读出 proof 之前的正文、proof 与其后的正文，不复制正文
func (body protectedBody) Reader() io.Reader {
	if body.proof == nil {
		return bytes.NewReader(body.data)
	}
	return io.MultiReader(bytes.NewReader(body.data[:body.at]), bytes.NewReader(body.proof), bytes.NewReader(body.data[body.at+len("null"):]))
}

// Bytes 返回写入 proof 后的完整正文
func (body protectedBody) Bytes() []byte {
	if body.proof == nil {
		return body.data
	}
	return slices.Concat(body.data[:body.at], body.proof, body.data[body.at+len("null"):])
}

// stringEnd 返回从 from 开始的 JSON 字符串内容之后结束引号的位置：前面连续反斜杠为偶数个的引号
func stringEnd(body []byte, from int) (int, bool) {
	for from <= len(body) {
		quote := bytes.IndexByte(body[from:], '"')
		if quote < 0 {
			return 0, false
		}
		quote += from
		escapes := 0
		for escapes < quote-from && body[quote-1-escapes] == '\\' {
			escapes++
		}
		if escapes%2 == 0 {
			return quote, true
		}
		from = quote + 1
	}
	return 0, false
}

// pageCountTokensDelay 是官网页面在 GenerateContent 发出后统计用户轮次 token 的时延
const pageCountTokensDelay = 2200 * time.Millisecond

// pageSubmitCountDelay 是官网页面提交函数结果、发出 GenerateContent 后统计函数调用与函数结果两轮 token 的最短时延，实际另加不超过 150 ms 的随机值
const pageSubmitCountDelay = 250 * time.Millisecond

// SendProtected 经账户 WAA runtime 流式发送已准备的请求，响应带实际写入的 proof
// Camoufox 后端未带 proof 的 GenerateContent 在页面界面运行，请求头、proof 与随后的 CountTokens 由页面应用产生
// 其余 GenerateContent 发出 pageCountTokensDelay 后由 runtime 以相同请求头统计页面提示词的 token
func (worker *NativeWorker) SendProtected(ctx context.Context, request ProtectedRequest) (*RPCResponse, error) {
	if generator, ok := worker.generator(ctx, request); ok {
		return worker.generate(ctx, generator, request)
	}
	if request.Proof == "" && worker.missingFunctionTab(request) {
		return nil, camoufoxnative.ErrFunctionTabUnavailable
	}
	if body, delay, ok := countTokensBody(request); ok {
		ctx = withFollowUp(ctx, followUp{URL: makerSuiteRPCURL + "CountTokens", Body: body, Delay: delay})
	}
	headers := request.Headers
	if strings.HasSuffix(request.URL, "/GenerateContent") {
		headers = headers.Clone()
		headers.Set("Priority", "u=0")
	}
	tab, err := worker.usePage(ctx)
	if err != nil {
		return nil, err
	}
	response, err := worker.tabPage(tab).SendProtected(ctx, request.URL, headers, newProtectedBody(request.Body, request.ProofAt, request.Proof))
	if err != nil {
		var transport *url.Error
		if errors.As(err, &transport) {
			return nil, fmt.Errorf("%w: %w", ErrProtectedTransport, err)
		}
		if ctx.Err() == nil {
			worker.setPhase(WorkerFailed)
		}
		return nil, err
	}
	return &RPCResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header,
		Body:       response.Body,
		Proof:      request.Proof,
	}, nil
}

// generate 在页面界面运行 GenerateContent，proof 绑定 request.Prompt 的 SHA-256；页面发出之前以当前界面步骤报告请求阶段，发出后报告等待响应头
func (worker *NativeWorker) generate(ctx context.Context, generator pageGenerator, request ProtectedRequest) (*RPCResponse, error) {
	digest := sha256.Sum256([]byte(request.Prompt))
	result, _ := wireFunctionResult(request.Body)
	worker.setPhase(WorkerBusy)
	response, err := generator.Generate(ctx, camoufoxnative.GenerateTurn{
		Body: request.Body, ProofStart: request.ProofAt, ProofEnd: request.ProofAt + len("null"), Digest: fmt.Sprintf("%x", digest), Result: result,
		Step: func(step camoufoxnative.GenerateStep) {
			if step == camoufoxnative.StepSent {
				reportRequestPhase(ctx, RequestPhaseSendingUpstream)
				return
			}
			reportRequestPhase(ctx, RequestPhase(step))
		},
	})
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, camoufoxnative.ErrFunctionTabUnavailable) {
			worker.setPhase(WorkerReady)
		} else {
			worker.setPhase(WorkerFailed)
		}
		return nil, err
	}
	worker.setPhase(WorkerReady)
	return &RPCResponse{StatusCode: response.StatusCode, Header: response.Header, Body: response.Body, Proof: response.Proof}, nil
}

// countTokensBody 按官网页面对用户轮次的统计，以 GenerateContent 的模型构造 CountTokens 正文并返回发出时延：带页面提示词时统计页面提示词，提交函数结果时统计最后的函数调用与函数结果两轮；都不是时返回 false
func countTokensBody(generate ProtectedRequest) ([]byte, time.Duration, bool) {
	if !strings.HasSuffix(generate.URL, "/GenerateContent") {
		return nil, 0, false
	}
	fields, ok := wireArray(generate.Body)
	var model string
	if !ok || len(fields) < 2 || json.Unmarshal(fields[0], &model) != nil {
		return nil, 0, false
	}
	if generate.PagePrompt != "" {
		body, err := json.Marshal([]any{model, []any{[]any{[]any{[]any{nil, generate.PagePrompt}}, "user"}}})
		return body, pageCountTokensDelay, err == nil
	}
	if _, result := wireFunctionResult(generate.Body); !result {
		return nil, 0, false
	}
	contents, ok := wireArray(fields[1])
	if !ok || len(contents) < 2 {
		return nil, 0, false
	}
	body, err := json.Marshal([]any{model, contents[len(contents)-2:]})
	return body, pageSubmitCountDelay + rand.N(150*time.Millisecond), err == nil
}

// BrowserStorageState 返回账户 WAA runtime 当前 Cookie 状态
func (worker *NativeWorker) BrowserStorageState(ctx context.Context) (StorageState, error) {
	encoded, err := worker.current().StorageCookies(ctx)
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

// SendPageRPC 像官网页面一样在当前页面中发出一次 MakerSuite RPC：带页面的公共协议头与 rpc 的请求头，以页面当前的 Cookie 签名，Cookie 由页面自己附带
func (worker *NativeWorker) SendPageRPC(ctx context.Context, signer *Signer, rpc RPCRequest) (*RPCResponse, error) {
	state, err := worker.BrowserStorageState(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取浏览器 Cookie: %w", err)
	}
	authorization, err := signer.Authorization(state)
	if err != nil {
		return nil, err
	}
	tab, err := worker.usePage(ctx)
	if err != nil {
		return nil, err
	}
	runtime := worker.tabPage(tab)
	headers, err := runtime.ProtocolHeaders(ctx)
	if err != nil {
		return nil, err
	}
	for name, values := range rpc.Header {
		headers.Del(name)
		for _, value := range values {
			headers.Add(name, value)
		}
	}
	headers.Set("Authorization", authorization)
	response, err := runtime.SendProtected(ctx, rpc.URL, headers, protectedBody{data: rpc.Body})
	if err != nil {
		return nil, err
	}
	return &RPCResponse{StatusCode: response.StatusCode, Header: response.Header, Body: response.Body}, nil
}

// MergeSetCookieHeaders 把账户在页面之外取得的响应 Cookie 写入当前页面
func (worker *NativeWorker) MergeSetCookieHeaders(ctx context.Context, headers []string, requestURL string) error {
	return worker.current().MergeSetCookieHeaders(ctx, headers, requestURL)
}

// PageCatalog 返回账户当前页面载入时目录 RPC 的响应正文
func (worker *NativeWorker) PageCatalog() camoufoxnative.PageCatalog {
	return worker.current().PageCatalog()
}

// ProtocolHeaders 返回当前账户官网请求的动态公共头
func (worker *NativeWorker) ProtocolHeaders(ctx context.Context, _ string) (http.Header, error) {
	return worker.current().ProtocolHeaders(ctx)
}

// current 返回账户当前页面的 runtime
func (worker *NativeWorker) current() workerRuntime {
	worker.runtimeMu.RLock()
	defer worker.runtimeMu.RUnlock()
	return worker.runtime
}

// Reload 按官网冷启动重新载入账户页面，新页面就绪后切换：还没在旧页面上取 proof、上传附件或发送的请求在新页面上取 proof，已在旧页面或新开页面上取过 proof、上传附件或发送的请求在原页面上完成，原页面在其租约归还后关闭，新页面同时分给其他请求
func (worker *NativeWorker) Reload(ctx context.Context) error {
	worker.reloadMu.Lock()
	defer worker.reloadMu.Unlock()
	previous := worker.current()
	next, err := previous.Reload(ctx)
	if err != nil || next == previous {
		return err
	}
	worker.operationMu.Lock()
	if phase := worker.State().Phase; phase == WorkerClosing || phase == WorkerClosed {
		worker.operationMu.Unlock()
		return errors.Join(errors.New("WAA runtime 已关闭"), next.Close())
	}
	worker.runtimeMu.Lock()
	worker.runtime = next
	worker.runtimeMu.Unlock()
	worker.operationMu.Unlock()
	return worker.retirePages(previous)
}

// State 返回账户 WAA runtime 状态
func (worker *NativeWorker) State() WorkerState {
	worker.stateMu.RLock()
	defer worker.stateMu.RUnlock()
	return worker.state
}

// Retire 让 Camoufox 后端的页面在后台离开并关闭浏览器，随后的 Close 不再等待；SpiderMonkey 后端不做操作
func (worker *NativeWorker) Retire() {
	if runtime, ok := worker.current().(camoufoxRuntime); ok {
		runtime.Worker.Retire()
	}
}

// Close 关闭账户 WAA runtime
func (worker *NativeWorker) Close() error {
	worker.operationMu.Lock()
	defer worker.operationMu.Unlock()
	pageWorkers.CompareAndDelete(worker.accountID, worker)
	worker.setPhase(WorkerClosing)
	err := errors.Join(worker.closeExtraPages(), worker.current().Close())
	if err == nil {
		worker.setPhase(WorkerClosed)
	}
	return err
}

// setPhase 更新账户 WAA runtime 阶段，进入关闭后只接受 WorkerClosed
func (worker *NativeWorker) setPhase(phase WorkerPhase) {
	worker.stateMu.Lock()
	defer worker.stateMu.Unlock()
	if current := worker.state.Phase; (current == WorkerClosing || current == WorkerClosed) && phase != WorkerClosed {
		return
	}
	worker.state.Phase = phase
}
