package aistudio

import (
	"reflect"
	"testing"
	"time"
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
	profile := firefox152Profile()
	if !reflect.DeepEqual(transport.Settings, profile.GetSettings()) || !reflect.DeepEqual(transport.SettingsOrder, profile.GetSettingsOrder()) {
		t.Fatalf("SETTINGS=%v 顺序=%v，应为 Firefox 配置 %v 顺序 %v", transport.Settings, transport.SettingsOrder, profile.GetSettings(), profile.GetSettingsOrder())
	}
	if transport.ConnectionFlow != profile.GetConnectionFlow() {
		t.Fatalf("连接窗口=%d，应为 %d", transport.ConnectionFlow, profile.GetConnectionFlow())
	}
	if !reflect.DeepEqual(transport.PseudoHeaderOrder, profile.GetPseudoHeaderOrder()) || !reflect.DeepEqual(transport.HeaderPriority, profile.GetHeaderPriority()) {
		t.Fatalf("伪头顺序=%v 头部优先级=%+v 与 Firefox 配置不一致", transport.PseudoHeaderOrder, transport.HeaderPriority)
	}
}
