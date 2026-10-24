package aistudio

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	fhttp "github.com/Mag1cFall/fhttp"
)

const (
	h3FrameData     = 0x00
	h3FrameHeaders  = 0x01
	h3FrameSettings = 0x04
	h3FrameGoaway   = 0x07
	// h3ControlStreamType 是 HTTP/3 控制流的单向流类型
	h3ControlStreamType = 0x00
	// h3StreamCreationError 是 H3_STREAM_CREATION_ERROR，neqo 以它拒绝推送流与未知类型的单向流
	h3StreamCreationError = 0x103
	// h3RequestCancelled 是 H3_REQUEST_CANCELLED，Firefox 取消请求时以它重置请求流
	h3RequestCancelled = 0x10c
	// h3SettingQPACKCapacity 是 SETTINGS_QPACK_MAX_TABLE_CAPACITY
	h3SettingQPACKCapacity = 0x01
	// h3SettingQPACKBlocked 是 SETTINGS_QPACK_BLOCKED_STREAMS
	h3SettingQPACKBlocked = 0x07
	// firefoxHTTP3BackupDelay 是 Firefox network.http.http3.backup_timer_delay：HTTP/3 连接未就绪时开始备用 HTTP/2 连接的延迟
	firefoxHTTP3BackupDelay = 100 * time.Millisecond
	// altSvcDefaultMaxAge 是 Alt-Svc 未带 ma 参数时的映射有效期
	altSvcDefaultMaxAge = 24 * time.Hour
	// h3RequestBodyChunk 是请求体写成 DATA 帧时每帧的最大长度
	h3RequestBodyChunk = 32 << 10
)

// firefoxHTTP3Settings 是 Firefox 152 控制流 SETTINGS 的参数与顺序：QPACK 容量、阻塞流、WebTransport、两个 H3 DATAGRAM 与扩展 CONNECT
var firefoxHTTP3Settings = [][2]uint64{
	{h3SettingQPACKCapacity, firefoxQPACKTableSize},
	{h3SettingQPACKBlocked, firefoxQPACKBlockedStreams},
	{0x2b603742, 0},
	{0xffd277, 1},
	{0x33, 1},
	{0x08, 1},
}

// firefoxSafeMethods 是 Firefox nsHttpRequestHead::IsSafeMethod 认定的方法，0-RTT 连接只在握手完成前发送这些方法的请求
var firefoxSafeMethods = map[string]bool{"GET": true, "HEAD": true, "OPTIONS": true, "TRACE": true, "PROPFIND": true, "REPORT": true, "SEARCH": true}

// h3ExcludedRequestHeaders 是 Firefox 为 HTTP/3 请求丢弃的逐跳与连接头
var h3ExcludedRequestHeaders = map[string]struct{}{
	"connection": {}, "host": {}, "keep-alive": {}, "proxy-connection": {}, "te": {}, "transfer-encoding": {}, "upgrade": {},
}

// requestClass 标记请求在连接池中的类别：匿名请求（CORS 预检）与携带凭据的请求使用不同连接，备用连接只供 HTTP/3 未就绪时回退
type requestClass struct {
	anonymous bool
	fallback  bool
}

type requestClassKey struct{}

func withRequestClass(ctx context.Context, class requestClass) context.Context {
	return context.WithValue(ctx, requestClassKey{}, class)
}

func requestClassOf(ctx context.Context) requestClass {
	class, _ := ctx.Value(requestClassKey{}).(requestClass)
	return class
}

// firefoxHTTP3 按 Firefox 152 的 Alt-Svc 流程在直连时把请求切换到 HTTP/3：响应通告 h3 后以携带凭据的连接验证，验证成功后该主机的请求改走 HTTP/3
type firefoxHTTP3 struct {
	pool    *firefoxConnPool
	mu      sync.Mutex
	origins map[string]*altSvcOrigin
	// resumption 按是否匿名分别保存会话票据、令牌与服务端设置
	resumption [2]*quicResumption
}

// altSvcOrigin 是一个主机的 Alt-Svc h3 映射，classes 按是否匿名分别保存 HTTP/3 连接
type altSvcOrigin struct {
	validated  bool
	validation *http3Dial
	expires    time.Time
	classes    [2]http3Class
}

// http3Class 是主机上一类请求的 HTTP/3 状态，excluded 表示备用 HTTP/2 连接抢先就绪后不再尝试 HTTP/3
type http3Class struct {
	conn     *http3Conn
	dial     *http3Dial
	excluded bool
}

// http3Dial 是进行中的 QUIC 拨号
type http3Dial struct {
	done   chan struct{}
	conn   *http3Conn
	err    error
	cancel context.CancelFunc
}

func newFirefoxHTTP3(pool *firefoxConnPool) *firefoxHTTP3 {
	return &firefoxHTTP3{pool: pool, origins: map[string]*altSvcOrigin{}, resumption: [2]*quicResumption{newQUICResumption(), newQUICResumption()}}
}

func classIndex(anonymous bool) int {
	if anonymous {
		return 1
	}
	return 0
}

// observe 处理响应的 Alt-Svc：主机首次通告 h3 时开始验证连接，已验证的映射按 ma 续期，clear 删除映射
func (h3 *firefoxHTTP3) observe(host string, header fhttp.Header) {
	maxAge, clear, ok := parseAltSvcH3(header.Values("Alt-Svc"), host)
	if !ok && !clear {
		return
	}
	h3.mu.Lock()
	defer h3.mu.Unlock()
	origin := h3.origins[host]
	if clear {
		delete(h3.origins, host)
		return
	}
	if origin != nil && origin.validated && time.Now().Before(origin.expires) {
		origin.expires = time.Now().Add(maxAge)
		return
	}
	if origin != nil && origin.validation != nil {
		return
	}
	if origin == nil {
		origin = &altSvcOrigin{}
		h3.origins[host] = origin
	}
	origin.validated = false
	validation := h3.startDial(host, false)
	origin.validation = validation
	go func() {
		<-validation.done
		h3.mu.Lock()
		defer h3.mu.Unlock()
		if h3.origins[host] != origin {
			if validation.conn != nil {
				validation.conn.close()
			}
			return
		}
		origin.validation = nil
		if validation.err != nil {
			delete(h3.origins, host)
			return
		}
		origin.validated = true
		origin.expires = time.Now().Add(maxAge)
		h3.adopt(host, origin, false, validation.conn)
		h3.pool.retire(net.JoinHostPort(host, "443"), requestClass{})
	}()
}

// parseAltSvcH3 返回 Alt-Svc 中第一个指向同主机 443 端口的 h3 条目的有效期，clear 表示清除映射
func parseAltSvcH3(values []string, host string) (time.Duration, bool, bool) {
	for _, value := range values {
		if strings.TrimSpace(value) == "clear" {
			return 0, true, false
		}
		for _, entry := range strings.Split(value, ",") {
			parameters := strings.Split(entry, ";")
			protocol, authority, found := strings.Cut(strings.TrimSpace(parameters[0]), "=")
			if !found || protocol != "h3" {
				continue
			}
			alternate, port, err := net.SplitHostPort(strings.Trim(authority, `"`))
			if err != nil || port != "443" || (alternate != "" && alternate != host) {
				continue
			}
			maxAge := altSvcDefaultMaxAge
			for _, parameter := range parameters[1:] {
				name, raw, _ := strings.Cut(strings.TrimSpace(parameter), "=")
				if seconds, err := strconv.ParseUint(strings.Trim(raw, `"`), 10, 32); err == nil && strings.EqualFold(name, "ma") {
					maxAge = time.Duration(seconds) * time.Second
				}
			}
			return maxAge, false, true
		}
	}
	return 0, false, false
}

// startDial 在后台以 Firefox 的 QUIC 首航拨号并建立 HTTP/3 会话，该类别有会话票据时恢复会话
func (h3 *firefoxHTTP3) startDial(host string, anonymous bool) *http3Dial {
	ctx, cancel := context.WithCancel(context.Background())
	dial := &http3Dial{done: make(chan struct{}), cancel: cancel}
	resumption := h3.resumption[classIndex(anonymous)]
	go func() {
		defer close(dial.done)
		connection, settings, err := dialFirefoxQUIC(ctx, host, resumption)
		if err != nil {
			dial.err = err
			return
		}
		dial.conn, dial.err = newHTTP3Conn(connection, settings, func(capacity uint64, blocked uint64) {
			resumption.rememberSettings(host, capacity, blocked)
		})
	}()
	return dial
}

// adopt 把建立好的连接放进主机对应类别，连接关闭或收到 GOAWAY 后自动移出
func (h3 *firefoxHTTP3) adopt(host string, origin *altSvcOrigin, anonymous bool, conn *http3Conn) {
	origin.classes[classIndex(anonymous)].conn = conn
	go func() {
		select {
		case <-conn.quic.Context().Done():
		case <-conn.goaway:
		}
		h3.mu.Lock()
		defer h3.mu.Unlock()
		if class := &origin.classes[classIndex(anonymous)]; class.conn == conn {
			class.conn = nil
		}
	}()
}

// send 按 Firefox 的连接选择发送请求：返回 HTTP/3 响应，或返回改走 HTTP/2 的请求（备用连接已在请求 context 中标记）
func (h3 *firefoxHTTP3) send(request *fhttp.Request, anonymous bool) (*fhttp.Response, *fhttp.Request, error) {
	if request.URL.Scheme != "https" || (request.URL.Port() != "" && request.URL.Port() != "443") {
		return nil, request, nil
	}
	host := request.URL.Hostname()
	for attempt := 0; ; attempt++ {
		conn, mapped, fallback, err := h3.route(request, host, anonymous)
		if err != nil {
			return nil, nil, err
		}
		if conn == nil {
			if fallback {
				request = request.WithContext(withRequestClass(request.Context(), requestClass{anonymous: anonymous, fallback: true}))
			}
			return nil, request, nil
		}
		response, opened, err := conn.roundTrip(request, h3RequestFields(request, mapped && !anonymous))
		if err != nil && !opened && attempt == 0 && request.Context().Err() == nil {
			conn.close()
			continue
		}
		if err == nil {
			h3.observe(host, response.Header)
		}
		return response, nil, err
	}
}

// route 选择请求的承载：返回可用的 HTTP/3 连接与是否经 Alt-Svc 映射发送，conn 为 nil 时经 HTTP/2，fallback 表示使用备用连接
func (h3 *firefoxHTTP3) route(request *fhttp.Request, host string, anonymous bool) (conn *http3Conn, mapped bool, fallback bool, err error) {
	ctx := request.Context()
	address := net.JoinHostPort(host, "443")
	h3.mu.Lock()
	origin := h3.origins[host]
	if origin == nil {
		h3.mu.Unlock()
		return nil, false, false, nil
	}
	if !origin.validated {
		validation := origin.validation
		h3.mu.Unlock()
		if anonymous || validation == nil {
			return nil, false, false, nil
		}
		ready := h3.http2Ready(ctx, address, requestClass{})
		for {
			select {
			case <-validation.done:
				if validation.err == nil {
					return validation.conn, false, false, nil
				}
				return nil, false, false, nil
			case err := <-ready:
				if err == nil {
					return nil, false, false, nil
				}
				ready = nil
			case <-ctx.Done():
				return nil, false, false, ctx.Err()
			}
		}
	}
	if time.Now().After(origin.expires) {
		delete(h3.origins, host)
		h3.mu.Unlock()
		return nil, false, false, nil
	}
	class := &origin.classes[classIndex(anonymous)]
	if class.excluded {
		h3.mu.Unlock()
		return nil, false, false, nil
	}
	if class.conn != nil {
		conn = class.conn
		h3.mu.Unlock()
		return conn, true, false, nil
	}
	dial := class.dial
	if dial == nil {
		dial = h3.startDial(host, anonymous)
		class.dial = dial
		go h3.finishDial(host, origin, anonymous, dial)
	}
	idle := h3.pool.idle(address, requestClass{anonymous: anonymous})
	h3.mu.Unlock()
	if idle {
		return nil, false, false, nil
	}
	timer := time.NewTimer(firefoxHTTP3BackupDelay)
	defer timer.Stop()
	var backup <-chan error
	var backupErr error
	for {
		select {
		case <-dial.done:
			if dial.err == nil {
				return dial.conn, true, false, nil
			}
			if backupErr != nil {
				return nil, false, false, errors.Join(dial.err, backupErr)
			}
			if backup == nil {
				return nil, false, false, nil
			}
			select {
			case err := <-backup:
				if err != nil {
					return nil, false, false, errors.Join(dial.err, err)
				}
			case <-ctx.Done():
				return nil, false, false, ctx.Err()
			}
			return nil, false, true, nil
		case <-timer.C:
			backup = h3.http2Ready(ctx, address, requestClass{anonymous: anonymous, fallback: true})
		case backupErr = <-backup:
			if backupErr != nil {
				backup = nil
				continue
			}
			select {
			case <-dial.done:
				if dial.err == nil {
					return dial.conn, true, false, nil
				}
			default:
				h3.mu.Lock()
				class.excluded = true
				h3.mu.Unlock()
				dial.cancel()
			}
			return nil, false, true, nil
		case <-ctx.Done():
			return nil, false, false, ctx.Err()
		}
	}
}

// finishDial 在拨号结束后把连接交给主机类别，拨号失败时该类别不再尝试 HTTP/3，类别已改走 HTTP/2 时以 H3_NO_ERROR 关闭连接
func (h3 *firefoxHTTP3) finishDial(host string, origin *altSvcOrigin, anonymous bool, dial *http3Dial) {
	<-dial.done
	h3.mu.Lock()
	defer h3.mu.Unlock()
	class := &origin.classes[classIndex(anonymous)]
	if class.dial == dial {
		class.dial = nil
	}
	if dial.conn == nil {
		class.excluded = true
		return
	}
	if class.excluded || h3.origins[host] != origin {
		dial.conn.close()
		return
	}
	h3.adopt(host, origin, anonymous, dial.conn)
}

// http2Ready 在后台取得一条该类别的 HTTP/2 连接，返回的 channel 交付取得结果
func (h3 *firefoxHTTP3) http2Ready(ctx context.Context, address string, class requestClass) <-chan error {
	ready := make(chan error, 1)
	go func() {
		_, err := h3.pool.get(withRequestClass(context.WithoutCancel(ctx), class), address, false)
		ready <- err
	}()
	return ready
}

// shutdown 关闭全部 HTTP/3 连接与进行中的拨号，Alt-Svc 映射保留
func (h3 *firefoxHTTP3) shutdown() {
	h3.mu.Lock()
	defer h3.mu.Unlock()
	for _, origin := range h3.origins {
		if origin.validation != nil {
			origin.validation.cancel()
		}
		for i := range origin.classes {
			class := &origin.classes[i]
			if class.dial != nil {
				class.dial.cancel()
			}
			if class.conn != nil {
				class.conn.close()
				class.conn = nil
			}
		}
	}
}
