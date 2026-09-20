package aistudio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
)

const (
	// MakerSuiteRPCBase is the live-confirmed AI Studio RPC base URL
	MakerSuiteRPCBase = "https://alkalimakersuite-pa.clients6.google.com/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/"
	// JSONProtobufContentType is the array protocol media type used by MakerSuite
	JSONProtobufContentType = "application/json+protobuf"
)

// RPCRequest describes a single MakerSuite request passed to the authenticated transport layer
type RPCRequest struct {
	Method    string
	URL       string
	AccountID string
	RequestID string
	Header    http.Header
	Body      []byte
	Streaming bool
}

// RPCResponse describes response headers and live body returned by the authenticated transport layer
type RPCResponse struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

// RPCTransport is responsible for authentication, account lease, cookie write-back, and physical network dispatch
type RPCTransport interface {
	Do(context.Context, RPCRequest) (*RPCResponse, error)
}

// ProtectedTransport atomically generates fresh WAA proof, writes field 5, and dispatches in the same context
type ProtectedTransport interface {
	DoProtected(context.Context, GenerateRequest, RPCRequest) (*RPCResponse, error)
}

// VideoProtectedTransport atomically generates Veo fresh WAA proof, writes field 8, and dispatches in the same context
type VideoProtectedTransport interface {
	DoProtectedVideo(context.Context, VideoRequest, RPCRequest) (*RPCResponse, error)
}

// ProtectedTransportFunc adapts a function to ProtectedTransport
type ProtectedTransportFunc func(context.Context, GenerateRequest, RPCRequest) (*RPCResponse, error)

// DoProtected invokes the protected transport function
func (f ProtectedTransportFunc) DoProtected(ctx context.Context, request GenerateRequest, rpc RPCRequest) (*RPCResponse, error) {
	return f(ctx, request, rpc)
}

// RequestContext stores protocol context provided by account runtime
type RequestContext struct {
	Timezone string
}

// RequestContextProvider returns the current protocol context for an account
type RequestContextProvider interface {
	RequestContext(context.Context, string) (RequestContext, error)
}

// RequestContextProviderFunc adapts a function to RequestContextProvider
type RequestContextProviderFunc func(context.Context, string) (RequestContext, error)

// RequestContext invokes the context function
func (f RequestContextProviderFunc) RequestContext(ctx context.Context, accountID string) (RequestContext, error) {
	return f(ctx, accountID)
}

// ClientOptions defines narrow dependencies for the protocol client
type ClientOptions struct {
	Transport       RPCTransport
	Protected       ProtectedTransport
	ContextProvider RequestContextProvider
}

// Client implements the AI Studio private protocol core
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

// NewClient creates a protocol client
func NewClient(options ClientOptions) (*Client, error) {
	if options.Transport == nil {
		return nil, fmt.Errorf("AI Studio transport cannot be nil")
	}
	if options.Protected == nil {
		return nil, fmt.Errorf("AI Studio protected transport cannot be nil")
	}
	return &Client{
		transport:       options.Transport,
		protected:       options.Protected,
		contextProvider: options.ContextProvider,
		catalogs:        make(map[string]modelCatalog),
		tiers:           make(map[string]BenefitTier),
	}, nil
}

// RPCError stores upstream status and protocol error code
type RPCError struct {
	Method     string
	StatusCode int
	Code       int64
	Message    string
	Metadata   map[string]string
}

// Error returns a structured upstream error
func (e *RPCError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("AI Studio %s returned HTTP %d, protocol error code %d: %s", e.Method, e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("AI Studio %s returned HTTP %d: %s", e.Method, e.StatusCode, e.Message)
}

// HTTPStatus returns the upstream HTTP status
func (e *RPCError) HTTPStatus() int {
	return e.StatusCode
}

func (c *Client) do(ctx context.Context, method string, accountID string, requestID string, body []byte, streaming bool) (*RPCResponse, error) {
	rpc := newRPCRequest(method, accountID, requestID, body, streaming)
	c.applyBenefitTier(method, accountID, rpc.Header)
	response, err := c.transport.Do(ctx, rpc)
	if err != nil {
		return nil, fmt.Errorf("send AI Studio %s: %w", method, err)
	}
	return validateRPCResponse(method, response)
}

func (c *Client) doProtected(ctx context.Context, request GenerateRequest, body []byte) (*RPCResponse, error) {
	rpc := newRPCRequest("GenerateContent", request.AccountID, request.ID, body, true)
	c.applyBenefitTier(rpc.Method, request.AccountID, rpc.Header)
	response, err := c.protected.DoProtected(ctx, request, rpc)
	if err != nil {
		return nil, fmt.Errorf("send AI Studio GenerateContent: %w", err)
	}
	return validateRPCResponse("GenerateContent", response)
}

func (c *Client) doProtectedVideo(ctx context.Context, request VideoRequest, body []byte) (*RPCResponse, error) {
	transport, ok := c.protected.(VideoProtectedTransport)
	if !ok {
		return nil, fmt.Errorf("AI Studio protected transport does not support GenerateVideo")
	}
	rpc := newRPCRequest("GenerateVideo", request.AccountID, "", body, false)
	c.applyBenefitTier(rpc.Method, request.AccountID, rpc.Header)
	response, err := transport.DoProtectedVideo(ctx, request, rpc)
	if err != nil {
		return nil, fmt.Errorf("send AI Studio GenerateVideo: %w", err)
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
		return nil, fmt.Errorf("AI Studio %s transport returned empty response", method)
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		raw, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			return nil, fmt.Errorf("read AI Studio %s error response: %w", method, readErr)
		}
		return nil, DecodeRPCError(method, response.StatusCode, raw)
	}
	contentType := response.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, JSONProtobufContentType) {
		response.Body.Close()
		return nil, fmt.Errorf("AI Studio %s returned unrecognized Content-Type %q", method, contentType)
	}
	return response, nil
}

// DecodeRPCError parses upstream errors in standalone status and streaming envelopes
func DecodeRPCError(method string, statusCode int, raw []byte) *RPCError {
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
	provider, providerPath := root, "$"
	if bytes.HasPrefix(bytes.TrimSpace(root[1]), []byte("[")) {
		provider, err = rawArray(root[1], "$[1]", value)
		if err != nil || len(provider) < 2 {
			return rpcError
		}
		providerPath = "$[1]"
	}
	if code, err := rawInt64(provider[0], providerPath+"[0]", value); err == nil {
		rpcError.Code = code
	}
	if message, err := rawString(provider[1], providerPath+"[1]", value); err == nil && message != "" {
		rpcError.Message = message
	}
	if len(provider) > 2 && !isJSONNull(provider[2]) {
		decodeRPCErrorMetadata(rpcError, provider[2])
	}
	return rpcError
}

func decodeRPCErrorMetadata(rpcError *RPCError, raw json.RawMessage) {
	var details [][]json.RawMessage
	if err := json.Unmarshal(raw, &details); err != nil {
		return
	}
	for _, detail := range details {
		if len(detail) < 2 {
			continue
		}
		var typeURL string
		if err := json.Unmarshal(detail[0], &typeURL); err != nil || typeURL != "type.googleapis.com/google.rpc.ErrorInfo" {
			continue
		}
		var info []json.RawMessage
		if err := json.Unmarshal(detail[1], &info); err != nil || len(info) < 3 {
			continue
		}
		var metadata [][]string
		if err := json.Unmarshal(info[2], &metadata); err != nil {
			continue
		}
		for _, pair := range metadata {
			if len(pair) < 2 || pair[0] == "" {
				continue
			}
			if rpcError.Metadata == nil {
				rpcError.Metadata = make(map[string]string)
			}
			rpcError.Metadata[pair[0]] = pair[1]
		}
	}
}
