package aistudio

import (
	"context"
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/proxydial"
	fhttp "github.com/Mag1cFall/fhttp"
	"github.com/Mag1cFall/fhttp/http2"
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
	// firefoxInitialStreamID 是 Firefox 在每条 HTTP/2 连接上首个请求流的 ID
	firefoxInitialStreamID = 3
	// firefoxConnectionWindowUpdate 是 Firefox 在 SETTINGS 后把连接接收窗口开到 12 MiB 的 WINDOW_UPDATE 增量
	firefoxConnectionWindowUpdate = 12517377
	// firefoxStreamWindowUpdate 是 Firefox 在每个请求流 HEADERS 后把流接收窗口开到 12 MiB 的 WINDOW_UPDATE 增量
	firefoxStreamWindowUpdate = 12451840
	// firefoxWindowUpdateThreshold 是 Firefox 累计未确认字节达到后才归还接收窗口的阈值
	firefoxWindowUpdateThreshold = 4 << 20
	// firefoxMaxPersistentConns 是 Firefox network.http.max-persistent-connections-per-server：一类请求对一个主机的已建立与建立中连接数上限
	firefoxMaxPersistentConns = 6
)

// firefoxHTTP2Settings 是 Firefox 152 在每条 HTTP/2 连接开头发送的 SETTINGS
var firefoxHTTP2Settings = map[http2.SettingID]uint32{
	http2.SettingHeaderTableSize:   65536,
	http2.SettingEnablePush:        0,
	http2.SettingInitialWindowSize: 131072,
	http2.SettingMaxFrameSize:      16384,
}

// firefoxHTTP2SettingsOrder 是 firefoxHTTP2Settings 在 SETTINGS 帧中的顺序
var firefoxHTTP2SettingsOrder = []http2.SettingID{
	http2.SettingHeaderTableSize,
	http2.SettingEnablePush,
	http2.SettingInitialWindowSize,
	http2.SettingMaxFrameSize,
}

// firefoxPseudoHeaderOrder 是 Firefox 请求 HEADERS 帧的伪头顺序
var firefoxPseudoHeaderOrder = []string{":method", ":path", ":authority", ":scheme"}

// firefoxHeaderPriority 是 Firefox 请求 HEADERS 帧携带的优先级，线上权重为 42
var firefoxHeaderPriority = http2.PriorityParam{Weight: 41}

// firefoxRequestHeaderOrder 是 Firefox 152 发出官网页面请求的请求头顺序：浏览器默认头、页面脚本按写入顺序设置的头、content-length、origin、alt-used、Cookie、导航与 fetch 元数据头、priority 与 te
var firefoxRequestHeaderOrder = []string{
	"user-agent",
	"accept",
	"accept-language",
	"accept-encoding",
	"access-control-request-method",
	"access-control-request-headers",
	"referer",
	"x-aistudio-g1-tier",
	"x-aistudio-visit-id",
	"x-goog-ext-519733851-bin",
	"x-goog-api-key",
	"x-goog-authuser",
	"authorization",
	"content-type",
	"x-user-agent",
	"content-length",
	"origin",
	"alt-used",
	"cookie",
	"upgrade-insecure-requests",
	"sec-fetch-dest",
	"sec-fetch-mode",
	"sec-fetch-site",
	"sec-fetch-user",
	"priority",
	"te",
}

// waaHost 是官网 Waa/Create 的主机，其页面脚本先写 authorization 再写 x-goog-authuser
const waaHost = "waa-pa.clients6.google.com"

// browserManagedHeaders 是浏览器自行附加、不进入 CORS 预检 access-control-request-headers 的请求头
var browserManagedHeaders = map[string]struct{}{
	"user-agent": {}, "accept": {}, "accept-language": {}, "accept-encoding": {}, "referer": {}, "origin": {}, "cookie": {},
	"content-length": {}, "host": {}, "upgrade-insecure-requests": {}, "sec-fetch-dest": {}, "sec-fetch-mode": {}, "sec-fetch-site": {},
	"sec-fetch-user": {}, "priority": {}, "te": {},
}

type browserRoundTripper struct {
	transport *http2.Transport
	pool      *firefoxConnPool
	// http3 在直连时按 Alt-Svc 把请求切换到 HTTP/3，经代理时为 nil
	http3 *firefoxHTTP3
	mu    sync.Mutex
	// preflights 记录每个跨域请求（方法、URL 与脚本设置的头集合）的 CORS 预检缓存到期时刻
	preflights map[string]time.Time
}

type browserResponseBody struct {
	body    io.ReadCloser
	source  *fhttp.Response
	trailer stdhttp.Header
}

// newBrowserRoundTripper 创建与当前 Camoufox 网络形状一致的传输：经代理时只用 HTTP/2，直连时按 Alt-Svc 改用 HTTP/3
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
	hello := firefox152ClientHello()
	priority := firefoxHeaderPriority
	pool := &firefoxConnPool{
		dial:        func(address string) (net.Conn, error) { return dialBrowserTLS(proxyDialer, hello, "tcp", address) },
		conns:       map[string][]*http2.ClientConn{},
		experienced: map[*http2.ClientConn]bool{},
		dialing:     map[string]int{},
		added:       make(chan struct{}),
	}
	pool.transport = &http2.Transport{
		ConnPool:                   pool,
		ConnectionFlow:             firefoxConnectionWindowUpdate,
		HeaderPriority:             &priority,
		IdleConnTimeout:            browserIdleConnTimeout,
		InitialStreamID:            firefoxInitialStreamID,
		PingTimeout:                firefoxPingTimeout,
		PseudoHeaderOrder:          firefoxPseudoHeaderOrder,
		PushHandler:                &http2.DefaultPushHandler{},
		ReadIdleTimeout:            firefoxPingThreshold,
		Settings:                   firefoxHTTP2Settings,
		SettingsOrder:              firefoxHTTP2SettingsOrder,
		StrictMaxConcurrentStreams: true,
		StreamWindowUpdate:         firefoxStreamWindowUpdate,
		WindowUpdateThreshold:      firefoxWindowUpdateThreshold,
	}
	roundTripper := &browserRoundTripper{preflights: map[string]time.Time{}, transport: pool.transport, pool: pool}
	if proxyURL == "" {
		roundTripper.http3 = newFirefoxHTTP3(pool)
	}
	return roundTripper, nil
}

// firefoxConnPool 按 Firefox 选择 HTTP/2 连接：匿名请求、携带凭据的请求与 HTTP/3 备用连接分开建连，主机还没有建立好的连接时每个请求各自拨号并改用最先建好的连接，已有连接时复用最早建立且能接受新请求的连接；本类没有可用连接时合并到另一类已复用过的连接
type firefoxConnPool struct {
	transport *http2.Transport
	dial      func(address string) (net.Conn, error)
	mu        sync.Mutex
	conns     map[string][]*http2.ClientConn
	// experienced 记录握手完成后又分派过请求的连接，即 Firefox 的 IsExperienced
	experienced map[*http2.ClientConn]bool
	// dialing 记录各连接池键正在建立的连接数
	dialing map[string]int
	// added 在池中加入或移出连接、一次拨号结束时关闭并换新，等待的请求据此改用可用的连接或补上拨号
	added chan struct{}
}

// poolKey 是地址与请求类别组成的连接池键
func poolKey(address string, class requestClass) string {
	if class.anonymous {
		address += "|anonymous"
	}
	if class.fallback {
		address += "|fallback"
	}
	return address
}

func (pool *firefoxConnPool) GetClientConn(request *fhttp.Request, address string) (*http2.ClientConn, error) {
	return pool.get(request.Context(), address, true)
}

// get 返回 context 所标类别下能接受新请求的连接，本类没有时取另一类已复用过的连接，都没有时拨号；本类已建立与建立中的连接达到 Firefox 上限时等先建好的连接；dispatch 表示返回的连接将承载请求
func (pool *firefoxConnPool) get(ctx context.Context, address string, dispatch bool) (*http2.ClientConn, error) {
	class := requestClassOf(ctx)
	key := poolKey(address, class)
	inverted := poolKey(address, requestClass{anonymous: !class.anonymous, fallback: class.fallback})
	pool.mu.Lock()
	conn := pool.availableLocked(key, inverted, dispatch)
	for conn == nil && len(pool.conns[key])+pool.dialing[key] >= firefoxMaxPersistentConns {
		added := pool.added
		pool.mu.Unlock()
		select {
		case <-added:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		pool.mu.Lock()
		conn = pool.availableLocked(key, inverted, dispatch)
	}
	if conn != nil {
		pool.mu.Unlock()
		return conn, nil
	}
	pool.dialing[key]++
	added := pool.added
	pool.mu.Unlock()
	type dialed struct {
		conn *http2.ClientConn
		err  error
	}
	result := make(chan dialed, 1)
	go func() {
		var conn *http2.ClientConn
		raw, err := pool.dial(address)
		if err == nil {
			conn, err = pool.transport.NewClientConn(raw)
			if err != nil {
				_ = raw.Close()
			}
		}
		pool.mu.Lock()
		pool.dialing[key]--
		if err == nil {
			pool.conns[key] = append(pool.conns[key], conn)
		}
		result <- dialed{conn: conn, err: err}
		pool.notifyLocked()
		pool.mu.Unlock()
	}()
	for {
		select {
		case own := <-result:
			if own.err != nil {
				return nil, own.err
			}
			pool.mu.Lock()
			conn := pool.firstLocked(key, own.conn, dispatch)
			pool.mu.Unlock()
			return conn, nil
		case <-added:
			pool.mu.Lock()
			select {
			case own := <-result:
				if own.err != nil {
					pool.mu.Unlock()
					return nil, own.err
				}
				conn := pool.firstLocked(key, own.conn, dispatch)
				pool.mu.Unlock()
				return conn, nil
			default:
			}
			conn := pool.availableLocked(key, inverted, dispatch)
			added = pool.added
			pool.mu.Unlock()
			if conn != nil {
				return conn, nil
			}
		}
	}
}

// availableLocked 返回本类能接受新请求的最早连接，没有时返回另一类已复用过的连接；dispatch 时把取到的本类连接记为已复用
func (pool *firefoxConnPool) availableLocked(key string, inverted string, dispatch bool) *http2.ClientConn {
	for _, conn := range pool.conns[key] {
		if conn.CanTakeNewRequest() {
			pool.experienced[conn] = pool.experienced[conn] || dispatch
			return conn
		}
	}
	for _, conn := range pool.conns[inverted] {
		if pool.experienced[conn] && conn.CanTakeNewRequest() {
			return conn
		}
	}
	return nil
}

// firstLocked 返回本类最先建好且能接受新请求的连接，即 Firefox 让等待中的请求改用最先建好的连接；取到的不是本请求拨出的连接时记为已复用
func (pool *firefoxConnPool) firstLocked(key string, own *http2.ClientConn, dispatch bool) *http2.ClientConn {
	for _, conn := range pool.conns[key] {
		if conn == own {
			return own
		}
		if conn.CanTakeNewRequest() {
			pool.experienced[conn] = pool.experienced[conn] || dispatch
			return conn
		}
	}
	return own
}

// idle 报告该类别是否已有能接受新请求的连接
func (pool *firefoxConnPool) idle(address string, class requestClass) bool {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return slices.ContainsFunc(pool.conns[poolKey(address, class)], (*http2.ClientConn).CanTakeNewRequest)
}

// retire 让该类别的连接在已有请求结束后关闭，之后的请求不再复用它们
func (pool *firefoxConnPool) retire(address string, class requestClass) {
	pool.mu.Lock()
	conns := pool.conns[poolKey(address, class)]
	delete(pool.conns, poolKey(address, class))
	for _, conn := range conns {
		delete(pool.experienced, conn)
	}
	pool.notifyLocked()
	pool.mu.Unlock()
	for _, conn := range conns {
		go func() { _ = conn.Shutdown(context.Background()) }()
	}
}

func (pool *firefoxConnPool) MarkDead(dead *http2.ClientConn) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	for address, conns := range pool.conns {
		pool.conns[address] = slices.DeleteFunc(conns, func(conn *http2.ClientConn) bool { return conn == dead })
	}
	delete(pool.experienced, dead)
	pool.notifyLocked()
}

// notifyLocked 唤醒等待连接的请求
func (pool *firefoxConnPool) notifyLocked() {
	close(pool.added)
	pool.added = make(chan struct{})
}

// shutdown 让池中全部连接在已有请求结束后关闭，之后的请求重新拨号
func (pool *firefoxConnPool) shutdown() {
	pool.mu.Lock()
	conns := pool.conns
	pool.conns = map[string][]*http2.ClientConn{}
	pool.experienced = map[*http2.ClientConn]bool{}
	pool.notifyLocked()
	pool.mu.Unlock()
	for _, list := range conns {
		for _, conn := range list {
			go func() { _ = conn.Shutdown(context.Background()) }()
		}
	}
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
	err := transport.ensurePreflight(request)
	var upstream *fhttp.Request
	if err == nil {
		upstream, err = fhttp.NewRequestWithContext(request.Context(), request.Method, request.URL.String(), body)
	}
	if err != nil {
		if request.Body != nil {
			_ = request.Body.Close()
		}
		return nil, err
	}
	upstream.Host = request.Host
	upstream.ContentLength = request.ContentLength
	upstream.Header = make(fhttp.Header, len(request.Header)+2)
	for name, values := range request.Header {
		upstream.Header[name] = append([]string(nil), values...)
	}
	upstream.Header.Set("Te", "trailers")
	upstream.Header[fhttp.HeaderOrderKey] = orderedHeaderNames(upstream.Header, request.URL.Host)
	response, err := transport.roundTrip(upstream, false)
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

// roundTrip 执行上游请求，anonymous 表示不携带凭据的 CORS 预检：直连时先按 Alt-Svc 选择 HTTP/3，其余经 HTTP/2 发送并记录响应的 Alt-Svc
func (transport *browserRoundTripper) roundTrip(request *fhttp.Request, anonymous bool) (*fhttp.Response, error) {
	request = request.WithContext(withRequestClass(request.Context(), requestClass{anonymous: anonymous}))
	if transport.http3 == nil {
		return transport.roundTripHTTP2(request)
	}
	response, http2Request, err := transport.http3.send(request, anonymous)
	if http2Request == nil {
		return response, err
	}
	response, err = transport.roundTripHTTP2(http2Request)
	if err == nil {
		transport.http3.observe(request.URL.Hostname(), response.Header)
	}
	return response, err
}

// roundTripHTTP2 经 HTTP/2 执行上游请求，请求 context 结束时立即返回，未完成的拨号在后台结束
func (transport *browserRoundTripper) roundTripHTTP2(request *fhttp.Request) (*fhttp.Response, error) {
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
	transport.pool.shutdown()
	if transport.http3 != nil {
		transport.http3.shutdown()
	}
}

func standardHeader(source fhttp.Header) stdhttp.Header {
	result := make(stdhttp.Header, len(source))
	for name, values := range source {
		result[name] = append([]string(nil), values...)
	}
	return result
}

// ensurePreflight 为带 Origin 的跨域请求按 Firefox 的流程先发 CORS 预检，结果按 Access-Control-Max-Age（上限 24 小时）缓存
func (transport *browserRoundTripper) ensurePreflight(request *stdhttp.Request) error {
	origin, err := url.Parse(request.Header.Get("Origin"))
	if request.Method == stdhttp.MethodOptions || err != nil || origin.Host == "" || origin.Host == request.URL.Host {
		return nil
	}
	target := request.URL.String()
	requestHeaders := strings.Join(corsRequestHeaders(request.Header), ",")
	key := request.Method + " " + target + " " + requestHeaders
	transport.mu.Lock()
	expiry, cached := transport.preflights[key]
	transport.mu.Unlock()
	if cached && time.Now().Before(expiry) {
		return nil
	}
	preflight, err := fhttp.NewRequestWithContext(request.Context(), stdhttp.MethodOptions, target, nil)
	if err != nil {
		return err
	}
	preflight.Header = make(fhttp.Header, 14)
	for _, name := range []string{"User-Agent", "Accept-Language", "Accept-Encoding", "Referer", "Origin", "Sec-Fetch-Dest", "Sec-Fetch-Mode", "Sec-Fetch-Site"} {
		if value := request.Header.Get(name); value != "" {
			preflight.Header.Set(name, value)
		}
	}
	preflight.Header.Set("Accept", "*/*")
	preflight.Header.Set("Access-Control-Request-Method", request.Method)
	preflight.Header.Set("Access-Control-Request-Headers", requestHeaders)
	preflight.Header.Set("Priority", "u=4")
	preflight.Header.Set("Te", "trailers")
	preflight.Header[fhttp.HeaderOrderKey] = orderedHeaderNames(preflight.Header, request.URL.Host)
	response, err := transport.roundTrip(preflight, true)
	if err != nil {
		return fmt.Errorf("CORS 预检: %w", err)
	}
	_, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("CORS 预检返回 HTTP %d", response.StatusCode)
	}
	maxAge := 5 * time.Second
	if seconds, err := strconv.Atoi(response.Header.Get("Access-Control-Max-Age")); err == nil {
		maxAge = min(time.Duration(seconds)*time.Second, 24*time.Hour)
	}
	transport.mu.Lock()
	transport.preflights[key] = time.Now().Add(maxAge)
	transport.mu.Unlock()
	return nil
}

// corsRequestHeaders 返回页面脚本设置、需要写入预检 access-control-request-headers 的小写请求头名，按字母排序
func corsRequestHeaders(headers stdhttp.Header) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		name = strings.ToLower(name)
		if _, managed := browserManagedHeaders[name]; !managed {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// requestHeaderOrder 返回目标主机的请求头顺序
func requestHeaderOrder(host string) []string {
	if host != waaHost {
		return firefoxRequestHeaderOrder
	}
	order := slices.Clone(firefoxRequestHeaderOrder)
	authUser, authorization := slices.Index(order, "x-goog-authuser"), slices.Index(order, "authorization")
	order[authUser], order[authorization] = order[authorization], order[authUser]
	return order
}

// orderedHeaderNames 返回按 Firefox 顺序排列的全部请求头名，顺序表之外的头按字母序排在最后
func orderedHeaderNames(headers fhttp.Header, host string) []string {
	order := requestHeaderOrder(host)
	seen := make(map[string]struct{}, len(order))
	for _, name := range order {
		seen[name] = struct{}{}
	}
	remaining := make([]string, 0, len(headers))
	for name := range headers {
		name = strings.ToLower(name)
		if _, ok := seen[name]; ok || name == strings.ToLower(fhttp.HeaderOrderKey) {
			continue
		}
		remaining = append(remaining, name)
	}
	sort.Strings(remaining)
	return append(slices.Clone(order), remaining...)
}

// firefox152ClientHello 返回以 tls-client 的 Firefox 148 档案为底、密码套件按 Firefox 152 排列的 ClientHello
func firefox152ClientHello() tls.ClientHelloID {
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
	return hello
}

var _ stdhttp.RoundTripper = (*browserRoundTripper)(nil)
