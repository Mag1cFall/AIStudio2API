package aistudio

import (
	"bufio"
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	fhttp "github.com/Mag1cFall/fhttp"
	quic "github.com/Mag1cFall/uquic"
	utls "github.com/Mag1cFall/utls"
)

// TestHTTP3SlowResponse 验证上游超过空闲超时才返回响应头或响应体时，请求流未完成期间的保活使连接不断开，POST 只发一次
func TestHTTP3SlowResponse(t *testing.T) {
	for _, flushHeaders := range []bool{false, true} {
		name := "headers"
		if flushHeaders {
			name = "body"
		}
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			conn := newTestHTTP3Conn(t, func(stream *quic.Stream) {
				requests.Add(1)
				block := newQPACKEncoder().encode([]qpackField{{":status", "200"}}, int64(stream.StreamID()))
				headers := appendH3Frame(nil, h3FrameHeaders, block)
				if flushHeaders {
					_, _ = stream.Write(headers)
				}
				time.Sleep(1500 * time.Millisecond)
				if !flushHeaders {
					_, _ = stream.Write(headers)
				}
				_, _ = stream.Write(appendH3Frame(nil, h3FrameData, []byte("ok")))
				_ = stream.Close()
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			request, err := fhttp.NewRequestWithContext(ctx, "POST", "https://localhost/", strings.NewReader("test"))
			if err != nil {
				t.Fatal(err)
			}
			response, opened, err := conn.roundTrip(request, h3RequestFields(request, false))
			if err != nil || !opened {
				t.Fatalf("等待慢响应失败: opened=%v err=%v", opened, err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || string(body) != "ok" || response.StatusCode != 200 {
				t.Fatalf("响应=%q 状态=%d err=%v", body, response.StatusCode, err)
			}
			if requests.Load() != 1 {
				t.Fatalf("POST 执行了 %d 次，应为 1 次", requests.Load())
			}
		})
	}
}

// newTestHTTP3Conn 返回连到本地 QUIC 服务的 HTTP/3 连接，连接使用生产 QUIC 配置并把空闲超时改为 500 ms，handler 处理正文为 test 的请求流
func newTestHTTP3Conn(t *testing.T, handler func(*quic.Stream)) *http3Conn {
	t.Helper()
	certServer := httptest.NewTLSServer(nil)
	t.Cleanup(certServer.Close)
	cert := certServer.TLS.Certificates[0]
	tlsConfig := &utls.Config{
		Certificates: []utls.Certificate{{Certificate: cert.Certificate, PrivateKey: cert.PrivateKey}},
		NextProtos:   []string{"h3"},
	}
	listener, err := quic.ListenAddr("127.0.0.1:0", tlsConfig, &quic.Config{MaxIdleTimeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, err := listener.Accept(ctx)
		if err != nil {
			return
		}
		defer connection.CloseWithError(0, "")
		var handlers sync.WaitGroup
		defer handlers.Wait()
		for {
			stream, err := connection.AcceptStream(ctx)
			if err != nil {
				return
			}
			handlers.Add(1)
			go func() {
				defer handlers.Done()
				reader := bufio.NewReader(stream)
				if kind, _, err := readH3Frame(reader); err != nil || kind != h3FrameHeaders {
					return
				}
				if kind, body, err := readH3Frame(reader); err == nil && kind == h3FrameData && string(body) == "test" {
					handler(stream)
				}
			}()
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		<-done
	})
	config := firefoxQUICConfig(neqoIPv4Datagram, nil)
	config.MaxIdleTimeout = 500 * time.Millisecond
	connection, err := quic.DialAddr(ctx, listener.Addr().String(), &utls.Config{InsecureSkipVerify: true, NextProtos: []string{"h3"}}, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.CloseWithError(0, "") })
	conn, err := newHTTP3Conn(connection, nil, func(uint64, uint64) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.close)
	return conn
}
