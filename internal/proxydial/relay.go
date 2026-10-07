package proxydial

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// Relay 在本机回环地址提供无认证 SOCKS5 入口，并经上游代理转发连接
type Relay struct {
	listener net.Listener
	dialer   proxy.ContextDialer
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	closed   bool
}

// StartRelay 为上游代理启动本机 SOCKS5 中转
func StartRelay(proxyURL *url.URL) (*Relay, error) {
	dialer, err := Dialer(proxyURL)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("创建本机代理中转: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	relay := &Relay{listener: listener, dialer: dialer, ctx: ctx, cancel: cancel, conns: make(map[net.Conn]struct{})}
	go relay.serve()
	return relay, nil
}

// Addr 返回中转监听的本机地址
func (relay *Relay) Addr() *net.TCPAddr {
	return relay.listener.Addr().(*net.TCPAddr)
}

// Close 停止监听并断开全部转发连接，可重复调用
func (relay *Relay) Close() error {
	relay.mu.Lock()
	if relay.closed {
		relay.mu.Unlock()
		return nil
	}
	relay.closed = true
	conns := make([]net.Conn, 0, len(relay.conns))
	for conn := range relay.conns {
		conns = append(conns, conn)
	}
	relay.mu.Unlock()
	relay.cancel()
	err := relay.listener.Close()
	for _, conn := range conns {
		_ = conn.Close()
	}
	return err
}

func (relay *Relay) serve() {
	for {
		conn, err := relay.listener.Accept()
		if err != nil {
			return
		}
		if !relay.track(conn) {
			_ = conn.Close()
			continue
		}
		go relay.handle(conn)
	}
}

// track 登记活动连接，中转已关闭时返回 false
func (relay *Relay) track(conn net.Conn) bool {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	if relay.closed {
		return false
	}
	relay.conns[conn] = struct{}{}
	return true
}

func (relay *Relay) untrack(conn net.Conn) {
	relay.mu.Lock()
	delete(relay.conns, conn)
	relay.mu.Unlock()
	_ = conn.Close()
}

// handle 完成 SOCKS5 握手后经上游代理连接目标并双向转发
func (relay *Relay) handle(client net.Conn) {
	defer relay.untrack(client)
	if err := client.SetDeadline(time.Now().Add(connectTimeout)); err != nil {
		return
	}
	target, err := readSocksConnect(client)
	if err != nil {
		return
	}
	upstream, err := relay.dialer.DialContext(relay.ctx, "tcp", target)
	if err != nil {
		_, _ = client.Write([]byte{5, 4, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	if !relay.track(upstream) {
		_ = upstream.Close()
		return
	}
	defer relay.untrack(upstream)
	if _, err := client.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	if err := client.SetDeadline(time.Time{}); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go pipe(upstream, client, done)
	go pipe(client, upstream, done)
	<-done
	<-done
}

// pipe 单向转发到源端结束，正常结束时只关闭对端写方向，出错或对端不支持半关闭时关闭两端
func pipe(destination, source net.Conn, done chan<- struct{}) {
	_, err := io.Copy(destination, source)
	writer, halfClose := destination.(interface{ CloseWrite() error })
	if err != nil || !halfClose || writer.CloseWrite() != nil {
		_ = destination.Close()
		_ = source.Close()
	}
	done <- struct{}{}
}

// readSocksConnect 读取无认证 SOCKS5 握手与 CONNECT 请求，返回未解析的目标地址
func readSocksConnect(client net.Conn) (string, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(client, header); err != nil {
		return "", err
	}
	if header[0] != 5 {
		return "", fmt.Errorf("SOCKS 版本 %d 不受支持", header[0])
	}
	methods := make([]byte, header[1])
	if _, err := io.ReadFull(client, methods); err != nil {
		return "", err
	}
	noAuth := false
	for _, method := range methods {
		noAuth = noAuth || method == 0
	}
	if !noAuth {
		_, _ = client.Write([]byte{5, 0xff})
		return "", errors.New("客户端不支持无认证 SOCKS5")
	}
	if _, err := client.Write([]byte{5, 0}); err != nil {
		return "", err
	}
	request := make([]byte, 4)
	if _, err := io.ReadFull(client, request); err != nil {
		return "", err
	}
	if request[0] != 5 || request[1] != 1 {
		_, _ = client.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
		return "", fmt.Errorf("SOCKS5 命令 %d 不受支持", request[1])
	}
	var host string
	switch request[3] {
	case 1, 4:
		address := make([]byte, 4)
		if request[3] == 4 {
			address = make([]byte, 16)
		}
		if _, err := io.ReadFull(client, address); err != nil {
			return "", err
		}
		host = net.IP(address).String()
	case 3:
		length := make([]byte, 1)
		if _, err := io.ReadFull(client, length); err != nil {
			return "", err
		}
		domain := make([]byte, length[0])
		if _, err := io.ReadFull(client, domain); err != nil {
			return "", err
		}
		host = string(domain)
	default:
		_, _ = client.Write([]byte{5, 8, 0, 1, 0, 0, 0, 0, 0, 0})
		return "", fmt.Errorf("SOCKS5 地址类型 %d 不受支持", request[3])
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(client, port); err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port)))), nil
}
