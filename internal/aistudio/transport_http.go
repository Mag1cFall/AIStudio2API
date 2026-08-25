package aistudio

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const publicDiscoveryUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:152.0) Gecko/20100101 Firefox/152.0"

var makerSuiteAPIKeyPattern = regexp.MustCompile(`"WIu0Nc":"([^"]+)"`)

// ProtocolHeaderProvider 按账户提供官方运行时发现的动态公共头
type ProtocolHeaderProvider interface {
	ProtocolHeaders(context.Context, string) (http.Header, error)
}

// ProtocolHeaderProviderFunc 将函数适配为 ProtocolHeaderProvider
type ProtocolHeaderProviderFunc func(context.Context, string) (http.Header, error)

// ProtocolHeaders 调用动态公共头函数
func (f ProtocolHeaderProviderFunc) ProtocolHeaders(ctx context.Context, accountID string) (http.Header, error) {
	return f(ctx, accountID)
}

// HTTPTransportOptions 定义普通 MakerSuite RPC 的账户与网络依赖
type HTTPTransportOptions struct {
	Pool        *AccountPool
	Signer      *Signer
	Headers     ProtocolHeaderProvider
	GlobalProxy string
}

// MakerSuiteHTTPTransport 使用账户固定出口发送普通 RPC
type MakerSuiteHTTPTransport struct {
	pool        *AccountPool
	signer      *Signer
	headers     ProtocolHeaderProvider
	globalProxy string
	now         func() time.Time
	clientsMu   sync.Mutex
	clients     map[string]*http.Client
}

type accountLeaseContextKey struct{}
type accountSelectionObserverContextKey struct{}

// ContextWithAccountLease 将上层已持有的租约传给协议传输
func ContextWithAccountLease(ctx context.Context, lease *AccountLease) context.Context {
	return context.WithValue(ctx, accountLeaseContextKey{}, lease)
}

// AccountLeaseFromContext 返回当前请求唯一的账户租约
func AccountLeaseFromContext(ctx context.Context) (*AccountLease, bool) {
	lease, ok := ctx.Value(accountLeaseContextKey{}).(*AccountLease)
	return lease, ok && lease != nil && lease.Account() != nil
}

// ContextWithAccountSelectionObserver 观察请求最终选择的账户
func ContextWithAccountSelectionObserver(ctx context.Context, observer func(*Account)) context.Context {
	return context.WithValue(ctx, accountSelectionObserverContextKey{}, observer)
}

func observeAccountSelection(ctx context.Context, account *Account) {
	observer, ok := ctx.Value(accountSelectionObserverContextKey{}).(func(*Account))
	if ok && observer != nil {
		observer(account)
	}
}

// NewMakerSuiteHTTPTransport 创建普通 MakerSuite RPC 传输
func NewMakerSuiteHTTPTransport(options HTTPTransportOptions) (*MakerSuiteHTTPTransport, error) {
	if options.Pool == nil {
		return nil, fmt.Errorf("AI Studio account pool 不能为空")
	}
	if options.Headers == nil {
		return nil, fmt.Errorf("AI Studio protocol header provider 不能为空")
	}
	signer := options.Signer
	if signer == nil {
		signer = NewSigner()
	}
	transport := &MakerSuiteHTTPTransport{
		pool:        options.Pool,
		signer:      signer,
		headers:     options.Headers,
		globalProxy: strings.TrimSpace(options.GlobalProxy),
		now:         time.Now,
		clients:     make(map[string]*http.Client),
	}
	if _, err := transport.clientForProxy(transport.globalProxy); err != nil {
		return nil, err
	}
	return transport, nil
}

// DiscoverPublicHeaders 从 AI Studio 首页读取普通 RPC 所需的公开头
func DiscoverPublicHeaders(ctx context.Context, client *http.Client) (http.Header, error) {
	if client == nil {
		return nil, fmt.Errorf("HTTP client 不能为空")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, aiStudioOrigin+"/", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	request.Header.Set("User-Agent", publicDiscoveryUserAgent)
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("读取 AI Studio 首页: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("AI Studio 首页返回 HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 AI Studio 首页正文: %w", err)
	}
	match := makerSuiteAPIKeyPattern.FindSubmatch(body)
	if len(match) != 2 || len(match[1]) == 0 {
		return nil, fmt.Errorf("AI Studio 首页缺少 WIu0Nc")
	}
	visitID, err := newVisitID()
	if err != nil {
		return nil, err
	}
	return http.Header{
		"User-Agent":          []string{publicDiscoveryUserAgent},
		"X-Aistudio-Visit-Id": []string{visitID},
		"X-Goog-Api-Key":      []string{string(match[1])},
		"X-Goog-Authuser":     []string{"0"},
		"X-User-Agent":        []string{"grpc-web-javascript/0.1"},
	}, nil
}

func newVisitID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("生成 AI Studio visit ID: %w", err)
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	uuid := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16],
	)
	return "v1_" + base64.StdEncoding.EncodeToString([]byte(uuid)), nil
}

// NewProxyHTTPClient 创建普通 HTTP、HTTPS 或 SOCKS5 固定出口客户端
func NewProxyHTTPClient(proxyURL string) (*http.Client, error) {
	roundTripper, err := newBrowserRoundTripper(proxyURL)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: roundTripper}, nil
}

// Do 发送普通 MakerSuite RPC 并让响应 body 持有租约
func (t *MakerSuiteHTTPTransport) Do(ctx context.Context, rpc RPCRequest) (*RPCResponse, error) {
	lease, owned, err := resolveAccountLease(ctx, t.pool, AccountSelection{AccountID: rpc.AccountID})
	if err != nil {
		return nil, err
	}
	releaseOnError := func(requestErr error) error {
		if !owned {
			return requestErr
		}
		return errors.Join(requestErr, lease.Release())
	}
	account := lease.Account()
	state, headers, err := prepareProtocolHeaders(ctx, lease, rpc, t.signer, t.headers, t.now(), true)
	if err != nil {
		return nil, releaseOnError(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, rpc.URL, bytes.NewReader(rpc.Body))
	if err != nil {
		return nil, releaseOnError(fmt.Errorf("创建 MakerSuite %s 请求: %w", rpc.Method, err))
	}
	request.Header = headers
	client, err := t.clientForProxy(account.EffectiveProxy(t.globalProxy))
	if err != nil {
		return nil, releaseOnError(err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, releaseOnError(fmt.Errorf("执行 MakerSuite %s 请求: %w", rpc.Method, err))
	}
	setCookies := append([]string(nil), response.Header.Values("Set-Cookie")...)
	body := &leaseResponseBody{
		body: response.Body,
		finish: func() error {
			var finishErr error
			if len(setCookies) > 0 {
				if err := state.MergeSetCookieHeaders(setCookies, rpc.URL, t.now()); err != nil {
					finishErr = err
				} else if err := lease.SaveStorageState(state); err != nil {
					finishErr = err
				}
			}
			if owned {
				finishErr = errors.Join(finishErr, lease.Release())
			}
			return finishErr
		},
	}
	return &RPCResponse{StatusCode: response.StatusCode, Header: response.Header.Clone(), Body: body}, nil
}

// CloseIdleConnections 关闭全部固定出口的空闲连接
func (t *MakerSuiteHTTPTransport) CloseIdleConnections() {
	t.clientsMu.Lock()
	defer t.clientsMu.Unlock()
	for _, client := range t.clients {
		client.CloseIdleConnections()
	}
}

func (t *MakerSuiteHTTPTransport) clientForProxy(proxyURL string) (*http.Client, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	t.clientsMu.Lock()
	defer t.clientsMu.Unlock()
	if client := t.clients[proxyURL]; client != nil {
		return client, nil
	}
	client, err := NewProxyHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	t.clients[proxyURL] = client
	return client, nil
}

func prepareProtocolHeaders(
	ctx context.Context,
	lease *AccountLease,
	rpc RPCRequest,
	signer *Signer,
	provider ProtocolHeaderProvider,
	now time.Time,
	includeCookie bool,
) (StorageState, http.Header, error) {
	state, err := lease.ReloadStorageState()
	if err != nil {
		return StorageState{}, nil, fmt.Errorf("读取账户 storage state: %w", err)
	}
	account := lease.Account()
	publicHeaders, err := provider.ProtocolHeaders(ctx, account.ID)
	if err != nil {
		return StorageState{}, nil, fmt.Errorf("读取账户动态公共头: %w", err)
	}
	if publicHeaders == nil {
		return StorageState{}, nil, fmt.Errorf("账户动态公共头为空")
	}
	headers := publicHeaders.Clone()
	for name, values := range rpc.Header {
		headers.Del(name)
		for _, value := range values {
			headers.Add(name, value)
		}
	}
	for _, name := range []string{"User-Agent", "X-Goog-Api-Key", "X-Goog-Authuser", "X-User-Agent"} {
		if strings.TrimSpace(headers.Get(name)) == "" {
			return StorageState{}, nil, fmt.Errorf("账户动态公共头缺少 %s", name)
		}
	}
	authorization, err := signer.Authorization(state)
	if err != nil {
		return StorageState{}, nil, err
	}
	headers.Set("Authorization", authorization)
	headers.Set("Accept", "*/*")
	headers.Set("Origin", aiStudioOrigin)
	headers.Set("Referer", aiStudioOrigin+"/")
	headers.Set("Sec-Fetch-Dest", "empty")
	headers.Set("Sec-Fetch-Mode", "cors")
	headers.Set("Sec-Fetch-Site", "same-site")
	if language := account.AcceptLanguage(); language != "" {
		headers.Set("Accept-Language", language)
	}
	if includeCookie {
		cookie, err := state.CookieHeader(rpc.URL, now)
		if err != nil {
			return StorageState{}, nil, err
		}
		headers.Set("Cookie", cookie)
	}
	return state, headers, nil
}

func resolveAccountLease(ctx context.Context, pool *AccountPool, selection AccountSelection) (*AccountLease, bool, error) {
	if lease, ok := AccountLeaseFromContext(ctx); ok {
		if err := validateLeaseSelection(lease, selection); err != nil {
			return nil, false, err
		}
		observeAccountSelection(ctx, lease.Account())
		return lease, false, nil
	}
	lease, err := pool.AcquireFor(ctx, selection)
	if err != nil {
		return nil, false, err
	}
	observeAccountSelection(ctx, lease.Account())
	return lease, true, nil
}

func validateLeaseSelection(lease *AccountLease, selection AccountSelection) error {
	account := lease.Account()
	if selection.AccountID != "" && account.ID != selection.AccountID {
		return fmt.Errorf("context 租约账户 %s 与请求账户 %s 不一致", account.ID, selection.AccountID)
	}
	modelID := strings.TrimPrefix(selection.ModelID, "models/")
	if modelID != "" && !account.SupportsModel(modelID) {
		return fmt.Errorf("context 租约账户 %s 不支持模型 %s", account.ID, modelID)
	}
	if selection.Method != "" && !account.SupportsMethod(modelID, selection.Method) {
		return fmt.Errorf("context 租约账户 %s 不支持方法 %s", account.ID, selection.Method)
	}
	return nil
}

type leaseResponseBody struct {
	body      io.ReadCloser
	finish    func() error
	once      sync.Once
	finishErr error
}

func (b *leaseResponseBody) Read(destination []byte) (int, error) {
	count, err := b.body.Read(destination)
	if err == io.EOF {
		b.finalize()
		if b.finishErr != nil {
			return count, errors.Join(err, b.finishErr)
		}
	}
	return count, err
}

func (b *leaseResponseBody) Close() error {
	closeErr := b.body.Close()
	b.finalize()
	return errors.Join(closeErr, b.finishErr)
}

func (b *leaseResponseBody) finalize() {
	b.once.Do(func() {
		b.finishErr = b.finish()
	})
}
