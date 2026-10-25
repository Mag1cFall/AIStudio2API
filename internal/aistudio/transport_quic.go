package aistudio

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	mrand "math/rand/v2"
	"net"
	"net/netip"
	"sync"
	"time"

	quic "github.com/Mag1cFall/uquic"
	"github.com/Mag1cFall/uquic/quicvarint"
	utls "github.com/Mag1cFall/utls"
	"github.com/Mag1cFall/utls/dicttls"
)

const (
	// firefoxQUICIdleTimeout 是 Firefox network.http.http3.idle_timeout
	firefoxQUICIdleTimeout = 30 * time.Second
	// firefoxQUICMaxData 是 Firefox network.http.http3.max_data
	firefoxQUICMaxData = 25165824
	// firefoxQUICMaxStreamData 是 Firefox network.http.http3.max_stream_data
	firefoxQUICMaxStreamData = 12582912
	// neqoStreamWindow 是 neqo 0.28 对端发起流与单向流的初始接收窗口
	neqoStreamWindow = 1048576
	// neqoMaxStreams 是 neqo 0.28 允许对端打开的双向流与单向流数
	neqoMaxStreams = 100
	// neqoMinAckDelayParameter 是 neqo 0.28 发送的 min_ack_delay 草案传输参数 ID
	neqoMinAckDelayParameter = 0xff02de1a
	// neqoMinAckDelay 是 neqo 0.28 声明的 min_ack_delay，单位微秒
	neqoMinAckDelay = 1000
	// neqoSourceConnectionIDLength 是 Firefox 为 neqo 配置的本端连接 ID 长度
	neqoSourceConnectionIDLength = 3
	// neqoIPv4Datagram 是 neqo 0.28 关闭 PMTUD 时 IPv4 的 UDP 载荷上限，Initial 数据报补零到该长度
	neqoIPv4Datagram = 1252
	// neqoIPv6Datagram 是 neqo 0.28 关闭 PMTUD 时 IPv6 的 UDP 载荷上限
	neqoIPv6Datagram = 1232
	// quicAEADOverhead 是 QUIC 包保护的 AEAD 标签长度
	quicAEADOverhead = 16
	// h3NoError 是 HTTP/3 H3_NO_ERROR
	h3NoError = 0x100
	// nssGREASEECHInner 是 NSS 为 GREASE ECH 估算的内层 ClientHello 基础长度：前导 47、内层 ECH 5、补到 100 字节主机名的 SNI 109、supported_versions 7 与引用 10 个外层扩展的 outer_extensions 25
	nssGREASEECHInner = 193
)

// quicResumption 是一类请求的 QUIC 连接共用的会话票据、NEW_TOKEN 令牌与各主机上次收到的服务端 QPACK 设置
type quicResumption struct {
	sessions utls.ClientSessionCache
	tokens   quic.TokenStore
	mu       sync.Mutex
	settings map[string][2]uint64
}

func newQUICResumption() *quicResumption {
	return &quicResumption{sessions: utls.NewLRUClientSessionCache(0), tokens: quic.NewLRUTokenStore(100, 1), settings: map[string][2]uint64{}}
}

// lookup 按 utls loadSession 的判定返回 host 会话恢复时 pre_shared_key 扩展的长度（0 表示不恢复），票据允许 0-RTT 且记得服务端设置时 settings 非 nil
func (resumption *quicResumption) lookup(host string) (int, *[2]uint64) {
	session, _ := resumption.sessions.Get(host)
	ticket, state, err := session.ResumptionState()
	if err != nil || state == nil {
		return 0, nil
	}
	raw, err := state.Bytes()
	if err != nil || time.Now().After(time.Unix(int64(binary.BigEndian.Uint64(raw[len(raw)-12:])), 0)) {
		return 0, nil
	}
	binder := 32
	if binary.BigEndian.Uint16(raw[3:]) == utls.TLS_AES_256_GCM_SHA384 {
		binder = 48
	}
	psk := 2 + 2 + len(ticket) + 4 + 2 + 1 + binder
	settings, ok := resumption.rememberedSettings(host)
	if !state.EarlyData || !ok {
		return psk, nil
	}
	return psk, &settings
}

// rememberSettings 记录 host 服务端 SETTINGS 中的 QPACK 动态表容量与阻塞流上限
func (resumption *quicResumption) rememberSettings(host string, capacity uint64, blocked uint64) {
	resumption.mu.Lock()
	defer resumption.mu.Unlock()
	resumption.settings[host] = [2]uint64{capacity, blocked}
}

// rememberedSettings 返回 host 上次连接记录的服务端 QPACK 设置
func (resumption *quicResumption) rememberedSettings(host string) ([2]uint64, bool) {
	resumption.mu.Lock()
	defer resumption.mu.Unlock()
	settings, ok := resumption.settings[host]
	return settings, ok
}

// dialFirefoxQUIC 以 Firefox 152 的 QUIC 首航直连 host:443，每条连接使用独立 UDP 套接字；有会话票据时带 NEW_TOKEN 令牌与 PSK 恢复，票据允许 0-RTT 时不等握手完成即返回，并返回 0-RTT 期间沿用的服务端设置
func dialFirefoxQUIC(ctx context.Context, host string, resumption *quicResumption) (*quic.Conn, *[2]uint64, error) {
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, nil, err
	}
	if len(addresses) == 0 {
		return nil, nil, fmt.Errorf("%s 没有可用地址", host)
	}
	address := addresses[0].Unmap()
	network, datagram := "udp4", neqoIPv4Datagram
	if address.Is6() {
		network, datagram = "udp6", neqoIPv6Datagram
	}
	socket, err := net.ListenUDP(network, nil)
	if err != nil {
		return nil, nil, err
	}
	psk, settings := resumption.lookup(host)
	transport := &quic.UTransport{Transport: &quic.Transport{Conn: socket}, QUICSpec: firefoxQUICSpec(datagram, psk, settings != nil)}
	dial := transport.Dial
	if settings != nil {
		dial = transport.DialEarly
	}
	connection, err := dial(ctx, net.UDPAddrFromAddrPort(netip.AddrPortFrom(address, 443)), &utls.Config{
		ServerName:         host,
		NextProtos:         []string{"h3"},
		MinVersion:         utls.VersionTLS13,
		ClientSessionCache: resumption.sessions,
		OmitEmptyPsk:       true,
	}, firefoxQUICConfig(datagram, resumption.tokens))
	if err != nil {
		_ = transport.Close()
		_ = socket.Close()
		return nil, nil, err
	}
	go func() {
		<-connection.Context().Done()
		_ = transport.Close()
		_ = socket.Close()
	}()
	return connection, settings, nil
}

// firefoxQUICConfig 返回 Firefox 152 的 QUIC 连接配置：空闲超时 30 秒，与 neqo 一样只在请求流尚未完成时每半个空闲超时发送保活 PING
func firefoxQUICConfig(datagram int, tokens quic.TokenStore) *quic.Config {
	return &quic.Config{
		Versions:                       []quic.Version{quic.Version1},
		HandshakeIdleTimeout:           firefoxQUICIdleTimeout,
		MaxIdleTimeout:                 firefoxQUICIdleTimeout,
		KeepAlivePeriod:                firefoxQUICIdleTimeout / 2,
		KeepAliveStreamsOnly:           true,
		InitialStreamReceiveWindow:     firefoxQUICMaxStreamData,
		MaxStreamReceiveWindow:         firefoxQUICMaxStreamData,
		InitialConnectionReceiveWindow: firefoxQUICMaxData,
		MaxConnectionReceiveWindow:     firefoxQUICMaxData,
		MaxIncomingStreams:             neqoMaxStreams,
		MaxIncomingUniStreams:          neqoMaxStreams,
		InitialPacketSize:              uint16(datagram),
		DisablePathMTUDiscovery:        true,
		TokenStore:                     tokens,
	}
}

// firefoxQUICSpec 返回 Firefox 152（neqo 0.28）一次直连的 QUIC 首航：随机 DCID 长度与首个包号、SNI 切分的 CRYPTO 帧、Initial 数据报补零到 datagram 字节，psk 与 early 决定 ClientHello 是否带 pre_shared_key 与 early_data
func firefoxQUICSpec(datagram int, psk int, early bool) *quic.QUICSpec {
	var random [3]byte
	_, _ = rand.Read(random[:])
	destination := max(8, 5+int(random[0]&(random[0]>>4)))
	firstPN := uint64(random[1]&0x1f) + uint64(max(int(random[2])-224, 0))<<5 + 1
	lengths := []quic.PacketNumberLen{neqoPacketNumberLength(firstPN + 1), neqoPacketNumberLength(firstPN + 2), quic.PacketNumberLen1}
	return &quic.QUICSpec{
		InitialPacketSpec: quic.InitialPacketSpec{
			SrcConnIDLength:         neqoSourceConnectionIDLength,
			DestConnIDLength:        destination,
			InitPacketNumber:        firstPN,
			InitPacketNumberLengths: lengths,
			FrameBuilder:            &neqoInitialFlight{limit: datagram - quicAEADOverhead},
		},
		ClientHelloSpec:    firefoxQUICClientHello(psk, early),
		UDPDatagramMinSize: datagram,
	}
}

// neqoPacketNumberLength 是 neqo 在尚无确认时为包号 pn 选择的编码长度，即 pn 的非零字节数
func neqoPacketNumberLength(unacked uint64) quic.PacketNumberLen {
	switch {
	case unacked < 1<<8:
		return quic.PacketNumberLen1
	case unacked < 1<<16:
		return quic.PacketNumberLen2
	case unacked < 1<<24:
		return quic.PacketNumberLen3
	default:
		return quic.PacketNumberLen4
	}
}

// firefoxQUICClientHello 返回 Firefox 152 经 NSS 发出的 QUIC ClientHello：13 个扩展（0-RTT 时加 early_data）每次随机排列，其后依次为传输参数、ECH GREASE，psk 为 pre_shared_key 扩展长度，非 0 时该扩展在最后
func firefoxQUICClientHello(psk int, early bool) *utls.ClientHelloSpec {
	extensions := []utls.TLSExtension{
		&utls.UtlsCompressCertExtension{Algorithms: []utls.CertCompressionAlgo{utls.CertCompressionZlib, utls.CertCompressionZstd, utls.CertCompressionBrotli}},
		&utls.ExtendedMasterSecretExtension{},
		&utls.KeyShareExtension{KeyShares: append(utls.ReuseHybridAndClassicalKeyShares(utls.KeyShare{Group: utls.X25519MLKEM768}, utls.KeyShare{Group: utls.X25519}), utls.KeyShare{Group: utls.CurveP256})},
		&utls.StatusRequestExtension{},
		&utls.ALPNExtension{AlpnProtocols: []string{"h3"}},
		&utls.PSKKeyExchangeModesExtension{Modes: []uint8{utls.PskModeDHE}},
		&utls.SupportedCurvesExtension{Curves: []utls.CurveID{utls.X25519MLKEM768, utls.X25519, utls.CurveP256, utls.CurveP384, utls.CurveP521}},
		&utls.RenegotiationInfoExtension{Renegotiation: utls.RenegotiateOnceAsClient},
		&utls.SupportedVersionsExtension{Versions: []uint16{utls.VersionTLS13}},
		&utls.FakeRecordSizeLimitExtension{Limit: 0x4001},
		&utls.FakeDelegatedCredentialsExtension{SupportedSignatureAlgorithms: []utls.SignatureScheme{
			utls.ECDSAWithP256AndSHA256, utls.ECDSAWithP384AndSHA384, utls.ECDSAWithP521AndSHA512, utls.ECDSAWithSHA1,
		}},
		&utls.SNIExtension{},
		&utls.SignatureAlgorithmsExtension{SupportedSignatureAlgorithms: []utls.SignatureScheme{
			utls.ECDSAWithP256AndSHA256, utls.ECDSAWithP384AndSHA384, utls.ECDSAWithP521AndSHA512, utls.ECDSAWithSHA1,
			utls.PSSWithSHA256, utls.PSSWithSHA384, utls.PSSWithSHA512,
			utls.PKCS1WithSHA256, utls.PKCS1WithSHA384, utls.PKCS1WithSHA512, utls.PKCS1WithSHA1,
		}},
	}
	if early {
		extensions = append(extensions, &utls.GenericExtension{Id: 42})
	}
	mrand.Shuffle(len(extensions), func(i, j int) { extensions[i], extensions[j] = extensions[j], extensions[i] })
	extensions = append(extensions,
		&utls.QUICTransportParametersExtension{TransportParameters: utls.TransportParameters{
			utls.MaxIdleTimeout(firefoxQUICIdleTimeout.Milliseconds()),
			utls.InitialMaxData(firefoxQUICMaxData),
			utls.InitialMaxStreamDataBidiLocal(firefoxQUICMaxStreamData),
			utls.InitialMaxStreamDataBidiRemote(neqoStreamWindow),
			utls.InitialMaxStreamDataUni(neqoStreamWindow),
			utls.InitialMaxStreamsBidi(neqoMaxStreams),
			utls.InitialMaxStreamsUni(neqoMaxStreams),
			utls.MaxAckDelay(20),
			utls.ActiveConnectionIDLimit(8),
			utls.InitialSourceConnectionID(nil),
			&utls.VersionInformation{ChoosenVersion: utls.VERSION_1, AvailableVersions: []uint32{utls.VERSION_GREASE, utls.VERSION_1}},
			&utls.FakeQUICTransportParameter{Id: neqoMinAckDelayParameter, Val: quicvarint.Append(nil, neqoMinAckDelay)},
			utls.MaxDatagramFrameSize(65535),
		}},
		&utls.GREASEEncryptedClientHelloExtension{
			CandidateCipherSuites: []utls.HPKESymmetricCipherSuite{{KdfId: dicttls.HKDF_SHA256, AeadId: dicttls.AEAD_AES_128_GCM}},
			CandidatePayloadLens:  []uint16{nssGREASEECHPayload(psk, early)},
		},
	)
	if psk > 0 {
		extensions = append(extensions, &utls.UtlsPreSharedKeyExtension{})
	}
	return &utls.ClientHelloSpec{
		TLSVersMin:         utls.VersionTLS13,
		TLSVersMax:         utls.VersionTLS13,
		CipherSuites:       []uint16{utls.TLS_AES_128_GCM_SHA256, utls.TLS_CHACHA20_POLY1305_SHA256, utls.TLS_AES_256_GCM_SHA384},
		CompressionMethods: []uint8{0},
		Extensions:         extensions,
	}
}

// nssGREASEECHPayload 是 NSS GREASE ECH 的密文长度（不含 AEAD 标签）：内层 ClientHello 带上完整的 pre_shared_key 与压缩引用的 early_data，补零到 32 字节对齐减 1
func nssGREASEECHPayload(psk int, early bool) uint16 {
	inner := nssGREASEECHInner
	if psk > 0 {
		inner += 4 + psk
	}
	if early {
		inner += 2
	}
	return uint16(inner + 31 - inner%32)
}

// neqoInitialFlight 按 neqo 0.28 的 SNI 切分把 ClientHello 排进 Initial 包：首包在 SNI 中点切开并先写后半段，两段截到 ClientHello 按包数均分的长度以内，其余字节依次写入后续包
type neqoInitialFlight struct {
	// limit 是一个 Initial 包去掉 AEAD 标签后的长度上限
	limit int
}

// cryptoRange 是 CRYPTO 流中的一段
type cryptoRange struct {
	offset int
	data   []byte
}

func (flight *neqoInitialFlight) Build(crypto []byte) ([]byte, error) {
	payloads, err := flight.BuildFlight(crypto, []quic.InitialDatagramBudget{{MaxFrameBytes: flight.limit}})
	if err != nil {
		return nil, err
	}
	return payloads[0], nil
}

// BuildFlight 按 budgets 给出的各包帧空间排布 CRYPTO 帧，包数超出 budgets 时沿用最后一项
func (flight *neqoInitialFlight) BuildFlight(crypto []byte, budgets []quic.InitialDatagramBudget) ([][]byte, error) {
	room := func(index int) int { return budgets[min(index, len(budgets)-1)].MaxFrameBytes }
	var payloads [][]byte
	pending := []cryptoRange{{0, crypto}}
	if start, end, ok := findClientHelloSNI(crypto); ok {
		middle := start + (end-start)/2
		packets := (len(crypto) + flight.limit - 1) / flight.limit
		left, right := neqoLimitChunks(cryptoRange{0, crypto[:middle]}, cryptoRange{middle, crypto[middle:]}, len(crypto)/packets)
		remaining := room(0)
		var payload []byte
		sent := make([]cryptoRange, 0, 2)
		for _, chunk := range []cryptoRange{right, left} {
			var written int
			var ok bool
			if payload, written, ok = appendCryptoChunk(payload, chunk, remaining-len(payload)); ok {
				sent = append(sent, cryptoRange{chunk.offset, chunk.data[:written]})
			}
		}
		if len(sent) == 0 {
			return nil, fmt.Errorf("Initial 包放不下 CRYPTO 帧")
		}
		payloads = append(payloads, payload)
		pending = subtractCryptoRanges(crypto, sent)
	}
	for len(pending) > 0 {
		remaining := room(len(payloads))
		var payload []byte
		for len(pending) > 0 {
			chunk := pending[0]
			var written int
			var ok bool
			if payload, written, ok = appendCryptoChunk(payload, chunk, remaining-len(payload)); !ok {
				break
			}
			if written < len(chunk.data) {
				pending[0] = cryptoRange{chunk.offset + written, chunk.data[written:]}
				break
			}
			pending = pending[1:]
		}
		if payload == nil {
			return nil, fmt.Errorf("Initial 包放不下 CRYPTO 帧")
		}
		payloads = append(payloads, payload)
	}
	return payloads, nil
}

// neqoLimitChunks 是 neqo limit_chunks：把 SNI 两侧的片段截到 limit 以内，使 SNI 分落在不同包
func neqoLimitChunks(left cryptoRange, right cryptoRange, limit int) (cryptoRange, cryptoRange) {
	switch {
	case len(left.data)+len(right.data) <= limit:
	case len(left.data) <= limit:
		skip := len(right.data) + len(left.data) - limit
		right = cryptoRange{right.offset + skip, right.data[skip:]}
	case len(right.data) <= limit:
		left.data = left.data[:limit-len(right.data)]
	default:
		left.data = left.data[:limit/2]
		right.data = right.data[:limit/2]
	}
	return left, right
}

// appendCryptoChunk 是 neqo write_chunk：在 room 字节内写入尽量多的 chunk 并返回写入的数据长度，放不下帧头与 1 字节数据时返回 false
func appendCryptoChunk(payload []byte, chunk cryptoRange, room int) ([]byte, int, bool) {
	header := 1 + quicvarint.Len(uint64(chunk.offset)) + 1
	if room < header+1 {
		return payload, 0, false
	}
	length := min(len(chunk.data), room-header)
	header += quicvarint.Len(uint64(length)) - 1
	length = min(len(chunk.data), room-header)
	payload = append(payload, 0x06)
	payload = quicvarint.Append(payload, uint64(chunk.offset))
	payload = quicvarint.Append(payload, uint64(length))
	return append(payload, chunk.data[:length]...), length, true
}

// subtractCryptoRanges 返回 crypto 中未被 sent 覆盖的各段，按偏移排列
func subtractCryptoRanges(crypto []byte, sent []cryptoRange) []cryptoRange {
	covered := make([]bool, len(crypto))
	for _, chunk := range sent {
		for i := range chunk.data {
			covered[chunk.offset+i] = true
		}
	}
	var pending []cryptoRange
	for start := 0; start < len(crypto); {
		if covered[start] {
			start++
			continue
		}
		end := start
		for end < len(crypto) && !covered[end] {
			end++
		}
		pending = append(pending, cryptoRange{start, crypto[start:end]})
		start = end
	}
	return pending
}

// findClientHelloSNI 是 neqo find_sni：返回 ClientHello 中 SNI 主机名的字节区间
func findClientHelloSNI(hello []byte) (int, int, bool) {
	if len(hello) < 1+3+2+32+1 || hello[0] != 1 {
		return 0, 0, false
	}
	offset := 1 + 3 + 2 + 32
	skip := func(width int) bool {
		if offset+width > len(hello) {
			return false
		}
		length := 0
		for _, b := range hello[offset : offset+width] {
			length = length<<8 | int(b)
		}
		offset += width + length
		return offset <= len(hello)
	}
	if !skip(1) || !skip(2) || !skip(1) {
		return 0, 0, false
	}
	offset += 2
	for offset+4 <= len(hello) {
		kind := binary.BigEndian.Uint16(hello[offset:])
		length := int(binary.BigEndian.Uint16(hello[offset+2:]))
		offset += 4
		if kind == 0 {
			if offset+2 > len(hello) {
				return 0, 0, false
			}
			list := int(binary.BigEndian.Uint16(hello[offset:]))
			if list < 3 {
				return 0, 0, false
			}
			start := offset + 2 + 3
			end := start + list - 3
			if end > len(hello) {
				return 0, 0, false
			}
			return start, end, true
		}
		offset += length
	}
	return 0, 0, false
}

var _ quic.QUICFlightFrameBuilder = (*neqoInitialFlight)(nil)
