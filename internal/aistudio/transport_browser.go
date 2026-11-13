package aistudio

import (
	"context"
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"net/http/httptrace"
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
	"github.com/bogdanfinn/utls/dicttls"
	"golang.org/x/net/proxy"
)

const (
	// firefoxPingThreshold 是 Firefox network.http.http2.ping-threshold，连接持续未收到帧达到该时长时发送 PING
	firefoxPingThreshold = 58 * time.Second
	// firefoxPingTimeout 是 Firefox network.http.http2.ping-timeout，PING 超过该时长未应答时关闭连接
	firefoxPingTimeout = 8 * time.Second
	// firefoxTLSHandshakeTimeout 是 Firefox network.http.tls-handshake-timeout
	firefoxTLSHandshakeTimeout = 30 * time.Second
	// browserIdleConnTimeout 是 Firefox network.http.http2.timeout，空闲 HTTP/2 连接保留该时长后关闭
	browserIdleConnTimeout = 170 * time.Second
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

// firefoxHTTP2Settings 是 Firefox 158 在每条 HTTP/2 连接开头发送的 SETTINGS
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

// firefoxStreamPriority 返回 Firefox 为请求的 HEADERS 帧写入的优先级：页面文档、用户输入中发起的请求（Priority: u=0）与预载脚本（Priority: u=1）为 41，页面图片为 11，其余（包括 Priority: u=6 的网站图标）为 21，线上权重各加 1
func firefoxStreamPriority(header fhttp.Header) http2.PriorityParam {
	priority := header.Get("Priority")
	switch {
	case header.Get("Sec-Fetch-Dest") == "document" || priority == "u=0" || priority == "u=1":
		return http2.PriorityParam{Weight: 41}
	case header.Get("Sec-Fetch-Dest") == "image" && priority != "u=6":
		return http2.PriorityParam{Weight: 11}
	}
	return http2.PriorityParam{Weight: 21}
}

// firefoxRequestHeaderOrder 是 Firefox 158 发出官网页面请求的请求头顺序
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
	"content-encoding",
	"content-type",
	"x-user-agent",
	"content-length",
	"origin",
	"alt-used",
	"sec-fetch-storage-access",
	"cookie",
	"upgrade-insecure-requests",
	"sec-fetch-dest",
	"sec-fetch-mode",
	"sec-fetch-site",
	"sec-fetch-user",
	"if-modified-since",
	"if-none-match",
	"priority",
	"te",
}

// waaHost 是官网 Waa/Create 的主机，其页面脚本先写 authorization 再写 x-goog-authuser
const waaHost = "waa-pa.clients6.google.com"

// clearcutHost 是官网 clearcut 上报的主机，2712 上报脚本同样先写 authorization 再写 x-goog-authuser
const clearcutHost = "play.google.com"

// gapiRequestHeaderOrder 是 Firefox 158 发出官网页面 gapi 请求（Drive discovery 与上传）的请求头顺序
var gapiRequestHeaderOrder = []string{
	"user-agent",
	"accept",
	"accept-language",
	"accept-encoding",
	"access-control-request-method",
	"access-control-request-headers",
	"referer",
	"content-type",
	"x-javascript-user-agent",
	"x-requested-with",
	"x-goog-encode-response-if-executable",
	"x-goog-authuser",
	"authorization",
	"x-clientdetails",
	"content-length",
	"origin",
	"sec-fetch-storage-access",
	"cookie",
	"sec-fetch-dest",
	"sec-fetch-mode",
	"sec-fetch-site",
	"priority",
	"te",
}

// gapiHosts 是官网页面 gapi 请求的主机
var gapiHosts = map[string]bool{"clients6.google.com": true, "content.googleapis.com": true}

// browserManagedHeaders 是浏览器自行附加、不进入 CORS 预检 access-control-request-headers 的请求头
var browserManagedHeaders = map[string]struct{}{
	"user-agent": {}, "accept": {}, "accept-language": {}, "accept-encoding": {}, "referer": {}, "origin": {}, "cookie": {},
	"content-length": {}, "host": {}, "upgrade-insecure-requests": {}, "sec-fetch-dest": {}, "sec-fetch-mode": {}, "sec-fetch-site": {},
	"sec-fetch-user": {}, "sec-fetch-storage-access": {}, "priority": {}, "te": {},
}

type browserRoundTripper struct {
	transport *http2.Transport
	pool      *firefoxConnPool
	// http3 在直连时按 Alt-Svc 把请求切换到 HTTP/3，经代理时为 nil
	http3 *firefoxHTTP3
	mu    sync.Mutex
	// preflights 按 URL 记录跨域请求（方法与脚本设置的头集合）的 CORS 预检缓存到期时刻
	preflights map[string]map[string]time.Time
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
	sessions := &firefoxSessionCache{sessions: map[firefoxSessionPeer][]*tls.ClientSessionState{}}
	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil || parsed.Hostname() == "" {
			return nil, fmt.Errorf("代理 URL 无效")
		}
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https":
			sessions.tunnel = true
		case "socks5":
		default:
			return nil, fmt.Errorf("代理协议必须是 http、https 或 socks5")
		}
		proxyDialer, err = proxydial.Dialer(parsed)
		if err != nil {
			return nil, err
		}
	}
	pool := &firefoxConnPool{
		dial: func(address string, anonymous bool, timing *connectionTiming) (net.Conn, error) {
			proxyDialer := timing.dialer(proxyDialer, sessions.tunnel)
			return dialBrowserTLS(proxyDialer, sessions, address, anonymous)
		},
		conns:       map[string][]*http2.ClientConn{},
		experienced: map[*http2.ClientConn]bool{},
		dialing:     map[string]int{},
		added:       make(chan struct{}),
	}
	pool.transport = &http2.Transport{
		ConnPool:                   pool,
		ConnectionFlow:             firefoxConnectionWindowUpdate,
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
	roundTripper := &browserRoundTripper{preflights: map[string]map[string]time.Time{}, transport: pool.transport, pool: pool}
	if proxyURL == "" {
		roundTripper.http3 = newFirefoxHTTP3(pool)
	}
	return roundTripper, nil
}

// connectionTimingKey 是请求 context 中记录该请求自己建立连接各阶段时刻的 *connectionTiming
type connectionTimingKey struct{}

// connectionTiming 是一次请求自己建立连接时各阶段的时刻，请求复用已有连接时 dialed 为 false
type connectionTiming struct {
	mu     sync.Mutex
	dialed bool
	// dnsStart 与 dnsDone 是解析目标主机或代理主机的起止时刻，地址为 IP 时为零值
	dnsStart, dnsDone time.Time
	// connectStart 与 connectDone 是与目标主机或代理建立 TCP 连接的起止时刻
	connectStart, connectDone time.Time
	// tunneled 是 HTTP 代理返回 CONNECT 应答的时刻，直连与 SOCKS5 代理时为零值
	tunneled time.Time
}

// dialer 返回记录各阶段时刻的拨号器，tunnel 表示经 HTTP 代理的 CONNECT 隧道，timing 为 nil 时原样返回
func (timing *connectionTiming) dialer(dialer proxy.ContextDialer, tunnel bool) proxy.ContextDialer {
	if timing == nil {
		return dialer
	}
	return tracedDialer{ContextDialer: dialer, timing: timing, tunnel: tunnel}
}

// mark 在 timing 的锁内记下一个时刻
func (timing *connectionTiming) mark(at *time.Time) {
	timing.mu.Lock()
	*at = time.Now()
	timing.mu.Unlock()
}

// tracedDialer 以 httptrace 记下拨号的 DNS 与 TCP 起止时刻，经 HTTP 代理时记下 CONNECT 应答时刻
type tracedDialer struct {
	proxy.ContextDialer
	timing *connectionTiming
	tunnel bool
}

func (dialer tracedDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	timing := dialer.timing
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		DNSStart:     func(httptrace.DNSStartInfo) { timing.mark(&timing.dnsStart) },
		DNSDone:      func(httptrace.DNSDoneInfo) { timing.mark(&timing.dnsDone) },
		ConnectStart: func(string, string) { timing.mark(&timing.connectStart) },
		ConnectDone:  func(string, string, error) { timing.mark(&timing.connectDone) },
	})
	conn, err := dialer.ContextDialer.DialContext(ctx, network, address)
	if err == nil && dialer.tunnel {
		timing.mark(&timing.tunneled)
	}
	return conn, err
}

// firefoxConnPool 按 Firefox 选择 HTTP/2 连接，匿名请求、携带凭据的请求与 HTTP/3 备用连接分开建连
type firefoxConnPool struct {
	transport *http2.Transport
	dial      func(address string, anonymous bool, timing *connectionTiming) (net.Conn, error)
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

// get 返回 context 所标类别下能接受新请求的连接，没有时取另一类已复用过的连接，仍没有时拨号并使用自己拨出的连接；dispatch 表示返回的连接将承载请求
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
	pool.mu.Unlock()
	timing, _ := ctx.Value(connectionTimingKey{}).(*connectionTiming)
	if timing != nil {
		timing.dialed = true
	}
	raw, err := pool.dial(address, class.anonymous, timing)
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
	pool.notifyLocked()
	pool.mu.Unlock()
	return conn, err
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

// firefoxSessionsPerPeer 是 Firefox network.ssl_tokens_cache_records_per_entry 的默认值，即每个 peer 最多保存的会话票据张数
const firefoxSessionsPerPeer = 10

// firefoxSessionPeer 是 Firefox 区分 TLS 会话票据的 peer：主机与连接是否匿名
type firefoxSessionPeer struct {
	host      string
	anonymous bool
}

// firefoxSessionCache 按 peer 保存服务器下发的 TLS 会话票据，与 Firefox 一样先用最早收到的票据，每张只用于一次握手
type firefoxSessionCache struct {
	mu       sync.Mutex
	sessions map[firefoxSessionPeer][]*tls.ClientSessionState
	// tunnel 表示连接经 HTTP 代理的 CONNECT 隧道建立，Firefox 此时建 socket 时取出的票据在隧道建立后的握手重置中丢弃，再取下一张
	tunnel bool
}

// take 取出 peer 最早收到且仍能用于 TLS 1.3 恢复的票据与恢复时 pre_shared_key 扩展的长度，途经的不可用票据一并丢弃
func (cache *firefoxSessionCache) take(peer firefoxSessionPeer) (*tls.ClientSessionState, int) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for len(cache.sessions[peer]) > 0 {
		session := cache.sessions[peer][0]
		cache.sessions[peer] = cache.sessions[peer][1:]
		if ticket, state, _ := session.ResumptionState(); state != nil {
			if encoded, err := state.Bytes(); err == nil {
				if psk := resumptionPSKLength(encoded, len(ticket)); psk > 0 {
					return session, psk
				}
			}
		}
	}
	return nil, 0
}

// put 把服务器下发的票据存到 peer 名下，已满时丢弃最早收到的一张
func (cache *firefoxSessionCache) put(peer firefoxSessionPeer, session *tls.ClientSessionState) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	sessions := append(cache.sessions[peer], session)
	if len(sessions) > firefoxSessionsPerPeer {
		sessions = sessions[1:]
	}
	cache.sessions[peer] = sessions
}

// handshakeSessions 是一次握手所用的会话缓存：Get 返回为这次握手取出的票据，服务器下发的新票据存到这条连接的 peer 名下
type handshakeSessions struct {
	cache   *firefoxSessionCache
	peer    firefoxSessionPeer
	session *tls.ClientSessionState
}

func (sessions handshakeSessions) Get(string) (*tls.ClientSessionState, bool) {
	return sessions.session, sessions.session != nil
}

func (sessions handshakeSessions) Put(_ string, session *tls.ClientSessionState) {
	if session != nil {
		sessions.cache.put(sessions.peer, session)
	}
}

// dialBrowserTLS 以 Firefox ClientHello 建立 TLS 连接并要求协商 HTTP/2，按 Firefox 取票据的时机在建 socket 时与 CONNECT 隧道建立后取用会话票据
func dialBrowserTLS(dialer proxy.ContextDialer, sessions *firefoxSessionCache, address string, anonymous bool) (net.Conn, error) {
	host, _, _ := net.SplitHostPort(address)
	peer := firefoxSessionPeer{host: host, anonymous: anonymous}
	session, psk := sessions.take(peer)
	raw, err := dialer.DialContext(context.Background(), "tcp", address)
	if err != nil {
		return nil, err
	}
	if sessions.tunnel {
		session, psk = sessions.take(peer)
	}
	connection := tls.UClient(raw, &tls.Config{ServerName: host, OmitEmptyPsk: true, ClientSessionCache: handshakeSessions{sessions, peer, session}}, firefox158ClientHello(psk), false, false, false)
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

// RoundTrip 按缓存先发 CORS 预检再发送请求，context 标为匿名类别的请求经不带凭据的连接发送，请求在收到响应头前失败时清除该 URL 的预检缓存
func (transport *browserRoundTripper) RoundTrip(request *stdhttp.Request) (*stdhttp.Response, error) {
	err := transport.ensurePreflight(request)
	var upstream *fhttp.Request
	if err == nil {
		upstream, err = fhttp.NewRequestWithContext(request.Context(), request.Method, request.URL.String(), request.Body)
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
	response, err := transport.roundTrip(upstream, requestClassOf(request.Context()).anonymous)
	if err != nil {
		transport.mu.Lock()
		delete(transport.preflights, request.URL.String())
		transport.mu.Unlock()
		return nil, err
	}
	responseTrailer := standardHeader(response.Trailer)
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
		Body:             &browserResponseBody{body: response.Body, source: response, trailer: responseTrailer},
		ContentLength:    response.ContentLength,
		TransferEncoding: append([]string(nil), response.TransferEncoding...),
		Close:            response.Close,
		Uncompressed:     response.Uncompressed,
		Trailer:          responseTrailer,
		Request:          responseRequest,
	}, nil
}

// roundTrip 执行上游请求，anonymous 表示不携带凭据的请求（CORS 预检与 CSP 报告）：直连时先按 Alt-Svc 选择 HTTP/3，其余经 HTTP/2 发送并记录响应的 Alt-Svc
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

// roundTripHTTP2 经 HTTP/2 以 Firefox 的 HEADERS 帧优先级执行上游请求，请求 context 结束时立即返回，未完成的拨号在后台结束
func (transport *browserRoundTripper) roundTripHTTP2(request *fhttp.Request) (*fhttp.Response, error) {
	request = request.WithContext(http2.WithHeaderPriority(request.Context(), firefoxStreamPriority(request.Header)))
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

// ensurePreflight 为带 Origin 的跨域 cors 请求按 Firefox 的流程先发 CORS 预检，结果按 Access-Control-Max-Age（上限 24 小时）缓存；no-cors 请求不预检
func (transport *browserRoundTripper) ensurePreflight(request *stdhttp.Request) error {
	origin, err := url.Parse(request.Header.Get("Origin"))
	if request.Method == stdhttp.MethodOptions || request.Header.Get("Sec-Fetch-Mode") == "no-cors" || err != nil || origin.Host == "" || origin.Host == request.URL.Host {
		return nil
	}
	target := request.URL.String()
	requestHeaders := strings.Join(corsRequestHeaders(request.Header), ",")
	key := request.Method + " " + requestHeaders
	transport.mu.Lock()
	expiry, cached := transport.preflights[target][key]
	transport.mu.Unlock()
	if cached && time.Now().Before(expiry) {
		return nil
	}
	preflight, err := fhttp.NewRequestWithContext(request.Context(), stdhttp.MethodOptions, target, nil)
	if err != nil {
		return err
	}
	preflight.Header = make(fhttp.Header, 14)
	for _, name := range []string{"User-Agent", "Accept-Language", "Accept-Encoding", "Referer", "Origin", "Sec-Fetch-Storage-Access", "Sec-Fetch-Dest", "Sec-Fetch-Mode", "Sec-Fetch-Site"} {
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
	if transport.preflights[target] == nil {
		transport.preflights[target] = map[string]time.Time{}
	}
	transport.preflights[target][key] = time.Now().Add(maxAge)
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
	if gapiHosts[host] {
		return gapiRequestHeaderOrder
	}
	if host != waaHost && host != clearcutHost {
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

// firefox158ClientHello 返回以 tls-client 的 Firefox 148 档案为底、密码套件与支持的组按 Firefox 158 排列、GREASE ECH 按 NSS 选套件与长度的 ClientHello；psk 为 pre_shared_key 扩展长度，非 0 时与 Firefox 的 TLS 1.3 会话恢复一样去掉 session_ticket、末尾加 pre_shared_key
func firefox158ClientHello(psk int) tls.ClientHelloID {
	base := profiles.Firefox_148
	hello := base.GetClientHelloId()
	hello.Version = "158"
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
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_RSA_WITH_AES_256_CBC_SHA,
		}
		for _, extension := range spec.Extensions {
			switch typed := extension.(type) {
			case *tls.SupportedCurvesExtension:
				typed.Curves = []tls.CurveID{tls.X25519MLKEM768, tls.X25519, tls.CurveP256, tls.CurveP384, tls.CurveP521}
			case *tls.GREASEEncryptedClientHelloExtension:
				typed.CandidateCipherSuites = []tls.HPKESymmetricCipherSuite{{KdfId: dicttls.HKDF_SHA256, AeadId: dicttls.AEAD_AES_128_GCM}, {KdfId: dicttls.HKDF_SHA256, AeadId: dicttls.AEAD_CHACHA20_POLY1305}}
				typed.CandidatePayloadLens = []uint16{nssGREASEECHPayload(nssTLSGREASEECHInner, psk, false)}
			}
		}
		if psk > 0 {
			spec.Extensions = slices.DeleteFunc(spec.Extensions, func(extension tls.TLSExtension) bool {
				_, ticket := extension.(*tls.SessionTicketExtension)
				return ticket
			})
			spec.Extensions = append(spec.Extensions, &tls.UtlsPreSharedKeyExtension{})
		}
		return spec, nil
	}
	return hello
}
