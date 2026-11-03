package proxydial

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

func TestCONNECTPreservesDataAfterResponseHeaders(t *testing.T) {
	for _, throughRelay := range []bool{false, true} {
		name := "direct"
		if throughRelay {
			name = "relay"
		}
		t.Run(name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			payload := bytes.Repeat([]byte{0, 1, 2, 0xff}, 64)
			serverDone := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					serverDone <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				if _, err = http.ReadRequest(bufio.NewReader(conn)); err == nil {
					response := append([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"), payload...)
					_, err = conn.Write(response)
				}
				serverDone <- err
			}()
			upstream := &url.URL{Scheme: "http", Host: listener.Addr().String()}
			dialer, err := Dialer(upstream)
			if err != nil {
				t.Fatal(err)
			}
			if throughRelay {
				relay, err := StartRelay(upstream)
				if err != nil {
					t.Fatal(err)
				}
				defer relay.Close()
				socks, err := proxy.SOCKS5("tcp", relay.Addr().String(), nil, &net.Dialer{})
				if err != nil {
					t.Fatal(err)
				}
				dialer = socks.(proxy.ContextDialer)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, err := dialer.DialContext(ctx, "tcp", "example.com:443")
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			got := make([]byte, len(payload))
			n, readErr := io.ReadFull(conn, got)
			if err := <-serverDone; err != nil {
				t.Fatal(err)
			}
			if readErr != nil || !bytes.Equal(got, payload) {
				t.Fatalf("tunnel read %d of %d bytes, error = %v", n, len(payload), readErr)
			}
		})
	}
}

func TestCONNECTBufferedConnectionKeepsHalfClose(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		_, err = http.ReadRequest(bufio.NewReader(conn))
		if err == nil {
			_, err = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\nhello")
		}
		if err == nil {
			var request []byte
			request, err = io.ReadAll(conn)
			if err == nil {
				_, err = conn.Write(request)
			}
		}
		serverDone <- err
	}()
	relay, err := StartRelay(&url.URL{Scheme: "http", Host: listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	dialer, err := proxy.SOCKS5("tcp", relay.Addr().String(), nil, &net.Dialer{})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dialer.Dial("tcp", "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	greeting := make([]byte, 5)
	if _, err := io.ReadFull(conn, greeting); err != nil || string(greeting) != "hello" {
		t.Fatalf("greeting = %q, error = %v", greeting, err)
	}
	if _, err := io.WriteString(conn, "request"); err != nil {
		t.Fatal(err)
	}
	if err := conn.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	reply, err := io.ReadAll(conn)
	if err != nil || string(reply) != "request" {
		t.Fatalf("reply after half-close = %q, error = %v", reply, err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

// TestRelayAuthenticatedUpstream 验证中转经带账号密码的 CONNECT 上游转发域名目标，凭据错误时返回 SOCKS5 失败
func TestRelayAuthenticatedUpstream(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	targets := make(chan string, 4)
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:secret"))
	go func() {
		for {
			conn, err := upstream.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				request, err := http.ReadRequest(bufio.NewReader(conn))
				if err != nil {
					return
				}
				if request.Header.Get("Proxy-Authorization") != expected {
					_, _ = conn.Write([]byte("HTTP/1.1 407 Proxy Authentication Required\r\n\r\n"))
					return
				}
				targets <- request.Host
				target, err := net.Dial("tcp", echo.Addr().String())
				if err != nil {
					return
				}
				defer target.Close()
				_, _ = conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
				go func() { _, _ = io.Copy(target, conn) }()
				_, _ = io.Copy(conn, target)
			}()
		}
	}()

	for _, password := range []string{"secret", "wrong"} {
		relay, err := StartRelay(&url.URL{Scheme: "http", User: url.UserPassword("user", password), Host: upstream.Addr().String()})
		if err != nil {
			t.Fatal(err)
		}
		client, err := proxy.SOCKS5("tcp", relay.Addr().String(), nil, &net.Dialer{Timeout: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		conn, err := client.Dial("tcp", "aistudio.google.com:443")
		if password == "wrong" {
			if err == nil {
				_ = conn.Close()
				t.Fatal("错误凭据仍建立了连接")
			}
			_ = relay.Close()
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if target := <-targets; target != "aistudio.google.com:443" {
			t.Fatalf("上游收到目标 %q", target)
		}
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write([]byte("ping")); err != nil {
			t.Fatal(err)
		}
		reply := make([]byte, 4)
		if _, err := io.ReadFull(conn, reply); err != nil || string(reply) != "ping" {
			t.Fatalf("回显 %q, err = %v", reply, err)
		}
		_ = conn.Close()
		_ = relay.Close()
	}
}
