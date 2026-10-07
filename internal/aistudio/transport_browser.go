package aistudio

import (
	"context"
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/proxydial"
	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/fhttp/http2"
	"github.com/bogdanfinn/tls-client/profiles"
	tls "github.com/bogdanfinn/utls"
	"golang.org/x/net/proxy"
)

const (
	// firefoxPingThreshold 是 Firefox network.http.http2.ping-threshold，连接持续未收到帧达到该时长时发送 PING
	firefoxPingThreshold = 58 * time.Second
	// firefoxPingTimeout 是 Firefox network.http.http2.ping-timeout，PING 超过该时长未应答时关闭连接
	firefoxPingTimeout = 8 * time.Second
	// firefoxTLSHandshakeTimeout 是 Firefox network.http.tls-handshake-timeout
	firefoxTLSHandshakeTimeout = 30 * time.Second
	// browserIdleConnTimeout 是空闲 HTTP/2 连接的保留时长
	browserIdleConnTimeout = 90 * time.Second
)

var firefoxRequestHeaderOrder = []string{
	"user-agent",
	"accept",
	"accept-language",
	"accept-encoding",
	"referer",
	"content-type",
	"x-goog-api-key",
	"x-goog-authuser",
	"x-user-agent",
	"x-aistudio-g1-tier",
	"x-aistudio-visit-id",
	"x-goog-ext-519733851-bin",
	"authorization",
	"cookie",
	"origin",
	"sec-fetch-dest",
	"sec-fetch-mode",
	"sec-fetch-site",
	"priority",
	"te",
}

type browserRoundTripper struct {
	transport *http2.Transport
}

type browserResponseBody struct {
	body    io.ReadCloser
	source  *fhttp.Response
	trailer stdhttp.Header
}

// newBrowserRoundTripper 创建与当前 Camoufox 网络形状一致的 HTTP/2 传输
func newBrowserRoundTripper(proxyURL string) (stdhttp.RoundTripper, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	var proxyDialer proxy.ContextDialer = &net.Dialer{}
	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil || parsed.Hostname() == "" {
			return nil, fmt.Errorf("代理 URL 无效")
		}
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https", "socks5":
		default:
			return nil, fmt.Errorf("代理协议必须是 http、https 或 socks5")
		}
		proxyDialer, err = proxydial.Dialer(parsed)
		if err != nil {
			return nil, err
		}
	}
	profile := firefox152Profile()
	hello := profile.GetClientHelloId()
	return &browserRoundTripper{transport: &http2.Transport{
		DialTLS: func(network, address string, _ *tls.Config) (net.Conn, error) {
			return dialBrowserTLS(proxyDialer, hello, network, address)
		},
		ConnectionFlow:    profile.GetConnectionFlow(),
		HeaderPriority:    profile.GetHeaderPriority(),
		IdleConnTimeout:   browserIdleConnTimeout,
		InitialStreamID:   profile.GetStreamID(),
		PingTimeout:       firefoxPingTimeout,
		Priorities:        profile.GetPriorities(),
		PseudoHeaderOrder: profile.GetPseudoHeaderOrder(),
		PushHandler:       &http2.DefaultPushHandler{},
		ReadIdleTimeout:   firefoxPingThreshold,
		Settings:          profile.GetSettings(),
		SettingsOrder:     profile.GetSettingsOrder(),
	}}, nil
}

// dialBrowserTLS 以 Firefox ClientHello 建立 TLS 连接并要求协商 HTTP/2
func dialBrowserTLS(dialer proxy.ContextDialer, hello tls.ClientHelloID, network string, address string) (net.Conn, error) {
	raw, err := dialer.DialContext(context.Background(), network, address)
	if err != nil {
		return nil, err
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	connection := tls.UClient(raw, &tls.Config{ServerName: host, OmitEmptyPsk: true}, hello, false, false, false)
	ctx, cancel := context.WithTimeout(context.Background(), firefoxTLSHandshakeTimeout)
	defer cancel()
	if err := connection.HandshakeContext(ctx); err != nil {
		_ = connection.Close()
		return nil, err
	}
	if protocol := connection.ConnectionState().NegotiatedProtocol; protocol != http2.NextProtoTLS {
		_ = connection.Close()
		return nil, fmt.Errorf("%s 未协商 HTTP/2，实际协议为 %q", address, protocol)
	}
	return connection, nil
}

func (transport *browserRoundTripper) RoundTrip(request *stdhttp.Request) (*stdhttp.Response, error) {
	var body io.Reader
	if request.Body != nil {
		body = request.Body
	}
	upstream, err := fhttp.NewRequestWithContext(request.Context(), request.Method, request.URL.String(), body)
	if err != nil {
		if request.Body != nil {
			_ = request.Body.Close()
		}
		return nil, err
	}
	upstream.Host = request.Host
	upstream.ContentLength = request.ContentLength
	upstream.Header = make(fhttp.Header, len(request.Header)+1)
	for name, values := range request.Header {
		upstream.Header[name] = append([]string(nil), values...)
	}
	upstream.Header[fhttp.HeaderOrderKey] = orderedHeaderNames(upstream.Header)
	response, err := transport.roundTrip(upstream)
	if err != nil {
		return nil, err
	}
	responseTrailer := standardHeader(response.Trailer)
	responseBody := io.ReadCloser(stdhttp.NoBody)
	if response.Body != nil {
		responseBody = &browserResponseBody{
			body:    response.Body,
			source:  response,
			trailer: responseTrailer,
		}
	}
	responseRequest := new(stdhttp.Request)
	*responseRequest = *request
	responseRequest.Body = nil
	return &stdhttp.Response{
		Status:           response.Status,
		StatusCode:       response.StatusCode,
		Proto:            response.Proto,
		ProtoMajor:       response.ProtoMajor,
		ProtoMinor:       response.ProtoMinor,
		Header:           standardHeader(response.Header),
		Body:             responseBody,
		ContentLength:    response.ContentLength,
		TransferEncoding: append([]string(nil), response.TransferEncoding...),
		Close:            response.Close,
		Uncompressed:     response.Uncompressed,
		Trailer:          responseTrailer,
		Request:          responseRequest,
	}, nil
}

// roundTrip 执行上游请求，请求 context 结束时立即返回，未完成的拨号在后台结束
func (transport *browserRoundTripper) roundTrip(request *fhttp.Request) (*fhttp.Response, error) {
	type result struct {
		response *fhttp.Response
		err      error
	}
	done := make(chan result, 1)
	go func() {
		response, err := transport.transport.RoundTrip(request)
		done <- result{response, err}
	}()
	select {
	case value := <-done:
		return value.response, value.err
	case <-request.Context().Done():
		go func() {
			if value := <-done; value.response != nil {
				_ = value.response.Body.Close()
			}
		}()
		return nil, request.Context().Err()
	}
}

func (body *browserResponseBody) Read(buffer []byte) (int, error) {
	read, err := body.body.Read(buffer)
	if err == io.EOF {
		body.syncTrailer()
	}
	return read, err
}

func (body *browserResponseBody) Close() error {
	err := body.body.Close()
	body.syncTrailer()
	return err
}

func (body *browserResponseBody) syncTrailer() {
	clear(body.trailer)
	for name, values := range body.source.Trailer {
		body.trailer[name] = append([]string(nil), values...)
	}
}

func (transport *browserRoundTripper) CloseIdleConnections() {
	transport.transport.CloseIdleConnections()
}

func standardHeader(source fhttp.Header) stdhttp.Header {
	result := make(stdhttp.Header, len(source))
	for name, values := range source {
		result[name] = append([]string(nil), values...)
	}
	return result
}

func orderedHeaderNames(headers fhttp.Header) []string {
	result := make([]string, 0, len(headers))
	seen := make(map[string]struct{}, len(headers))
	for _, name := range firefoxRequestHeaderOrder {
		if _, ok := headers[stdhttp.CanonicalHeaderKey(name)]; ok {
			result = append(result, name)
			seen[name] = struct{}{}
		}
	}
	remaining := make([]string, 0, len(headers))
	for name := range headers {
		name = strings.ToLower(name)
		if name == strings.ToLower(fhttp.HeaderOrderKey) {
			continue
		}
		if _, ok := seen[name]; !ok {
			remaining = append(remaining, name)
		}
	}
	sort.Strings(remaining)
	return append(result, remaining...)
}

func firefox152Profile() profiles.ClientProfile {
	base := profiles.Firefox_148
	hello := base.GetClientHelloId()
	hello.Version = "152"
	hello.SpecFactory = func() (tls.ClientHelloSpec, error) {
		spec, err := base.GetClientHelloSpec()
		if err != nil {
			return tls.ClientHelloSpec{}, err
		}
		spec.CipherSuites = []uint16{
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_RSA_WITH_AES_256_CBC_SHA,
		}
		return spec, nil
	}
	return profiles.NewClientProfile(
		hello,
		base.GetSettings(),
		base.GetSettingsOrder(),
		base.GetPseudoHeaderOrder(),
		base.GetConnectionFlow(),
		base.GetPriorities(),
		base.GetHeaderPriority(),
		base.GetStreamID(),
		base.GetAllowHTTP(),
		base.GetHttp3Settings(),
		base.GetHttp3SettingsOrder(),
		base.GetHttp3PriorityParam(),
		base.GetHttp3PseudoHeaderOrder(),
		base.GetHttp3SendGreaseFrames(),
	)
}

var _ stdhttp.RoundTripper = (*browserRoundTripper)(nil)
