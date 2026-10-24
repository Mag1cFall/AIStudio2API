package aistudio

import (
	"reflect"
	"testing"
	"time"

	"github.com/Mag1cFall/fhttp/http2"
)

// TestBrowserRoundTripperFirefoxHTTP2 验证浏览器传输使用 Firefox 152 的 HTTP/2 帧参数与 PING 保活
func TestBrowserRoundTripperFirefoxHTTP2(t *testing.T) {
	roundTripper, err := newBrowserRoundTripper("")
	if err != nil {
		t.Fatal(err)
	}
	transport := roundTripper.(*browserRoundTripper).transport
	if transport.ReadIdleTimeout != 58*time.Second || transport.PingTimeout != 8*time.Second {
		t.Fatalf("PING 阈值=%s 超时=%s，应为 Firefox 默认的 58s 与 8s", transport.ReadIdleTimeout, transport.PingTimeout)
	}
	settings := map[http2.SettingID]uint32{http2.SettingHeaderTableSize: 65536, http2.SettingEnablePush: 0, http2.SettingInitialWindowSize: 131072, http2.SettingMaxFrameSize: 16384}
	order := []http2.SettingID{http2.SettingHeaderTableSize, http2.SettingEnablePush, http2.SettingInitialWindowSize, http2.SettingMaxFrameSize}
	if !reflect.DeepEqual(transport.Settings, settings) || !reflect.DeepEqual(transport.SettingsOrder, order) {
		t.Fatalf("SETTINGS=%v 顺序=%v，应为 Firefox 152 的 %v 顺序 %v", transport.Settings, transport.SettingsOrder, settings, order)
	}
	if transport.ConnectionFlow != 12517377 || transport.StreamWindowUpdate != 12451840 || transport.WindowUpdateThreshold != 4<<20 || transport.InitialStreamID != 3 {
		t.Fatalf("连接窗口=%d 流窗口增量=%d 补窗阈值=%d 首个流=%d，应为 Firefox 152 的 12517377、12451840、4 MiB 与 3", transport.ConnectionFlow, transport.StreamWindowUpdate, transport.WindowUpdateThreshold, transport.InitialStreamID)
	}
	if !reflect.DeepEqual(transport.PseudoHeaderOrder, []string{":method", ":path", ":authority", ":scheme"}) || *transport.HeaderPriority != (http2.PriorityParam{Weight: 41}) {
		t.Fatalf("伪头顺序=%v 头部优先级=%+v，应为 Firefox 152 的 m,p,a,s 与权重 42", transport.PseudoHeaderOrder, transport.HeaderPriority)
	}
}
