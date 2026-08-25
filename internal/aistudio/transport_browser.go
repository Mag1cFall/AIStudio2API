package aistudio

import (
	"bufio"
	"context"
	cryptotls "crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	tls "github.com/bogdanfinn/utls"
	"golang.org/x/net/proxy"
)

const browserProxyConnectTimeout = 30 * time.Second

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
	client tlsclient.HttpClient
}

type browserConnectDialer struct {
	proxyURL    *url.URL
	proxyTarget string
	direct      *net.Dialer
}

type browserResponseBody struct {
	body    io.ReadCloser
	source  *fhttp.Response
	trailer stdhttp.Header
}

// newBrowserRoundTripper 创建与当前 Camoufox 网络形状一致的传输
func newBrowserRoundTripper(proxyURL string) (stdhttp.RoundTripper, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	var proxyDialer proxy.ContextDialer
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
		proxyDialer, err = newBrowserProxyDialer(parsed)
		if err != nil {
			return nil, err
		}
	}
	options := []tlsclient.HttpClientOption{
		tlsclient.WithClientProfile(firefox152Profile()),
		tlsclient.WithTimeoutMilliseconds(0),
		tlsclient.WithNotFollowRedirects(),
	}
	if proxyDialer != nil {
		options = append(options, tlsclient.WithDialContext(proxyDialer.DialContext))
	}
	client, err := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(), options...)
	if err != nil {
		return nil, fmt.Errorf("创建浏览器网络传输: %w", err)
	}
	return &browserRoundTripper{client: client}, nil
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
	response, err := transport.client.Do(upstream)
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

func newBrowserProxyDialer(proxyURL *url.URL) (proxy.ContextDialer, error) {
	direct := &net.Dialer{Timeout: browserProxyConnectTimeout, KeepAlive: 30 * time.Second}
	proxyTarget := proxyURL.Host
	if proxyURL.Port() == "" {
		switch strings.ToLower(proxyURL.Scheme) {
		case "http":
			proxyTarget = net.JoinHostPort(proxyURL.Hostname(), "80")
		case "https":
			proxyTarget = net.JoinHostPort(proxyURL.Hostname(), "443")
		case "socks5":
			return nil, fmt.Errorf("SOCKS5 代理 URL 缺少端口")
		}
	}
	if strings.EqualFold(proxyURL.Scheme, "socks5") {
		var auth *proxy.Auth
		if proxyURL.User != nil {
			password, _ := proxyURL.User.Password()
			auth = &proxy.Auth{User: proxyURL.User.Username(), Password: password}
		}
		dialer, err := proxy.SOCKS5("tcp", proxyTarget, auth, direct)
		if err != nil {
			return nil, fmt.Errorf("创建 SOCKS5 代理连接: %w", err)
		}
		contextDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("SOCKS5 代理不支持上下文取消")
		}
		return contextDialer, nil
	}
	return &browserConnectDialer{
		proxyURL:    proxyURL,
		proxyTarget: proxyTarget,
		direct:      direct,
	}, nil
}

func (dialer *browserConnectDialer) Dial(network, address string) (net.Conn, error) {
	return dialer.DialContext(context.Background(), network, address)
}

func (dialer *browserConnectDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	var connection net.Conn
	var err error
	if strings.EqualFold(dialer.proxyURL.Scheme, "https") {
		tlsDialer := cryptotls.Dialer{
			NetDialer: dialer.direct,
			Config: &cryptotls.Config{
				ServerName: dialer.proxyURL.Hostname(),
				NextProtos: []string{"http/1.1"},
			},
		}
		connection, err = tlsDialer.DialContext(ctx, network, dialer.proxyTarget)
	} else {
		connection, err = dialer.direct.DialContext(ctx, network, dialer.proxyTarget)
	}
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = connection.Close()
		}
	}()
	deadline := time.Now().Add(browserProxyConnectTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return nil, err
	}
	stopCancel := context.AfterFunc(ctx, func() {
		_ = connection.Close()
	})
	defer stopCancel()
	request := (&stdhttp.Request{
		Method: stdhttp.MethodConnect,
		URL:    &url.URL{Host: address},
		Host:   address,
		Header: make(stdhttp.Header),
	}).WithContext(ctx)
	if dialer.proxyURL.User != nil && dialer.proxyURL.User.Username() != "" {
		password, _ := dialer.proxyURL.User.Password()
		credentials := dialer.proxyURL.User.Username() + ":" + password
		request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(credentials)))
	}
	if err := request.Write(connection); err != nil {
		return nil, err
	}
	response, err := stdhttp.ReadResponse(bufio.NewReader(connection), request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != stdhttp.StatusOK {
		if response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, fmt.Errorf("代理 CONNECT 返回 %s", response.Status)
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	success = true
	return connection, nil
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
	transport.client.CloseIdleConnections()
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
