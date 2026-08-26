package aistudio

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
)

const (
	// MakerSuiteRPCBase 是现场确认的 AI Studio RPC 根地址
	MakerSuiteRPCBase = "https://alkalimakersuite-pa.clients6.google.com/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/"
	// JSONProtobufContentType 是 MakerSuite 使用的数组协议媒体类型
	JSONProtobufContentType = "application/json+protobuf"
)

// RPCRequest 描述交给认证传输层的单次 MakerSuite 请求
type RPCRequest struct {
	Method    string
	URL       string
	AccountID string
	RequestID string
	Header    http.Header
	Body      []byte
	Streaming bool
}

// RPCResponse 描述认证传输层返回的响应头和实时正文
type RPCResponse struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

// RPCTransport 负责认证、账户租约、Cookie 写回和真实网络发送
type RPCTransport interface {
	Do(context.Context, RPCRequest) (*RPCResponse, error)
}

// ProtectedTransport 原子完成 fresh WAA proof、field 5 写入和同 context 发送
type ProtectedTransport interface {
	DoProtected(context.Context, GenerateRequest, RPCRequest) (*RPCResponse, error)
}

// VideoProtectedTransport 原子完成 Veo fresh WAA proof、field 8 写入和同 context 发送
type VideoProtectedTransport interface {
	DoProtectedVideo(context.Context, VideoRequest, RPCRequest) (*RPCResponse, error)
}

// ProtectedTransportFunc 将函数适配为 ProtectedTransport
type ProtectedTransportFunc func(context.Context, GenerateRequest, RPCRequest) (*RPCResponse, error)

// DoProtected 调用受保护传输函数
func (f ProtectedTransportFunc) DoProtected(ctx context.Context, request GenerateRequest, rpc RPCRequest) (*RPCResponse, error) {
	return f(ctx, request, rpc)
}

// RequestContext 保存账户运行时提供的协议上下文
type RequestContext struct {
	Timezone string
}

// RequestContextProvider 按账户返回当前协议上下文
type RequestContextProvider interface {
	RequestContext(context.Context, string) (RequestContext, error)
}

// RequestContextProviderFunc 将函数适配为 RequestContextProvider
type RequestContextProviderFunc func(context.Context, string) (RequestContext, error)

// RequestContext 调用上下文函数
func (f RequestContextProviderFunc) RequestContext(ctx context.Context, accountID string) (RequestContext, error) {
	return f(ctx, accountID)
}

// ClientOptions 定义协议客户端的窄依赖
type ClientOptions struct {
	Transport       RPCTransport
	Protected       ProtectedTransport
	ContextProvider RequestContextProvider
}

// Client 实现 AI Studio 私有协议核心
type Client struct {
	transport       RPCTransport
	protected       ProtectedTransport
	contextProvider RequestContextProvider
	catalogMu       sync.RWMutex
	catalogs        map[string]modelCatalog
	tierMu          sync.RWMutex
	tiers           map[string]BenefitTier
}

var _ Service = (*Client)(nil)

// NewClient 创建协议客户端
func NewClient(options ClientOptions) (*Client, error) {
	if options.Transport == nil {
		return nil, fmt.Errorf("AI Studio transport 不能为空")
	}
	if options.Protected == nil {
		return nil, fmt.Errorf("AI Studio protected transport 不能为空")
	}
	return &Client{
		transport:       options.Transport,
		protected:       options.Protected,
		contextProvider: options.ContextProvider,
		catalogs:        make(map[string]modelCatalog),
		tiers:           make(map[string]BenefitTier),
	}, nil
}

// RPCError 保存上游状态和协议错误码
type RPCError struct {
	Method     string
	StatusCode int
	Code       int64
	Message    string
}

// Error 返回结构化上游错误
func (e *RPCError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("AI Studio %s 返回 HTTP %d、协议错误码 %d: %s", e.Method, e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("AI Studio %s 返回 HTTP %d: %s", e.Method, e.StatusCode, e.Message)
}

// HTTPStatus 返回上游 HTTP 状态
func (e *RPCError) HTTPStatus() int {
	return e.StatusCode
}

func (c *Client) do(ctx context.Context, method string, accountID string, requestID string, body []byte, streaming bool) (*RPCResponse, error) {
	rpc := newRPCRequest(method, accountID, requestID, body, streaming)
	c.applyBenefitTier(method, accountID, rpc.Header)
	response, err := c.transport.Do(ctx, rpc)
	if err != nil {
		return nil, fmt.Errorf("发送 AI Studio %s: %w", method, err)
	}
	return validateRPCResponse(method, response)
}

func (c *Client) doProtected(ctx context.Context, request GenerateRequest, body []byte) (*RPCResponse, error) {
	rpc := newRPCRequest("GenerateContent", request.AccountID, request.ID, body, true)
	c.applyBenefitTier(rpc.Method, request.AccountID, rpc.Header)
	response, err := c.protected.DoProtected(ctx, request, rpc)
	if err != nil {
		return nil, fmt.Errorf("发送 AI Studio GenerateContent: %w", err)
	}
	return validateRPCResponse("GenerateContent", response)
}

func (c *Client) doProtectedVideo(ctx context.Context, request VideoRequest, body []byte) (*RPCResponse, error) {
	transport, ok := c.protected.(VideoProtectedTransport)
	if !ok {
		return nil, fmt.Errorf("AI Studio protected transport 不支持 GenerateVideo")
	}
	rpc := newRPCRequest("GenerateVideo", request.AccountID, "", body, false)
	c.applyBenefitTier(rpc.Method, request.AccountID, rpc.Header)
	response, err := transport.DoProtectedVideo(ctx, request, rpc)
	if err != nil {
		return nil, fmt.Errorf("发送 AI Studio GenerateVideo: %w", err)
	}
	return validateRPCResponse("GenerateVideo", response)
}

func newRPCRequest(method string, accountID string, requestID string, body []byte, streaming bool) RPCRequest {
	return RPCRequest{
		Method:    method,
		URL:       MakerSuiteRPCBase + method,
		AccountID: accountID,
		RequestID: requestID,
		Header: http.Header{
			"Content-Type": []string{JSONProtobufContentType},
		},
		Body:      append([]byte(nil), body...),
		Streaming: streaming,
	}
}

func validateRPCResponse(method string, response *RPCResponse) (*RPCResponse, error) {
	if response == nil || response.Body == nil {
		return nil, fmt.Errorf("AI Studio %s transport 返回空响应", method)
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		raw, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			return nil, fmt.Errorf("读取 AI Studio %s 错误响应: %w", method, readErr)
		}
		return nil, decodeRPCError(method, response.StatusCode, raw)
	}
	contentType := response.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, JSONProtobufContentType) {
		response.Body.Close()
		return nil, fmt.Errorf("AI Studio %s 返回未识别的 Content-Type %q", method, contentType)
	}
	return response, nil
}

func decodeRPCError(method string, statusCode int, raw []byte) error {
	rpcError := &RPCError{
		Method:     method,
		StatusCode: statusCode,
		Message:    http.StatusText(statusCode),
	}
	value, err := decodeJSONValue(raw)
	if err != nil {
		return rpcError
	}
	root, err := rawArray(value, "$", value)
	if err != nil || len(root) < 2 || isJSONNull(root[1]) {
		return rpcError
	}
	provider, err := rawArray(root[1], "$[1]", value)
	if err != nil || len(provider) < 2 {
		return rpcError
	}
	if code, err := rawInt64(provider[0], "$[1][0]", value); err == nil {
		rpcError.Code = code
	}
	if message, err := rawString(provider[1], "$[1][1]", value); err == nil && message != "" {
		rpcError.Message = message
	}
	return rpcError
}
