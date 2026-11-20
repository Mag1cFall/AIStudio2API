package aistudio

import (
	"testing"
	"time"
)

// TestLoggingContextRecentResult 按官网页面脚本：GetLoggingContext 响应后 localStorage 的 ais_glcr 记下时间与第 2 项是否为 B；之后载入的页面（同一窗口新开的标签页、重新载入）在页面数据 NHyHAe 毫秒内以 [1] 或 [0] 带上该结果，超时、没有记录或 NHyHAe 为 0 时正文为 []
func TestLoggingContextRecentResult(t *testing.T) {
	if maxAge := parsePageFlags([]byte(`window.WIZ_global_data = {"MsAAre":false,"NHyHAe":60000,"rU9UAf":86400000};`)).loggingContextMaxAge; maxAge != 60000 {
		t.Fatalf("NHyHAe=%d", maxAge)
	}
	now := time.UnixMilli(1_800_000_000_000)
	storage := map[string]string{loggingContextStorageKey: loggingContextRecord([]byte(`[null,"B"]`), now)}
	if got := storage[loggingContextStorageKey]; got != `{"timestamp":1800000000000,"result":"true"}` {
		t.Fatalf("ais_glcr=%s", got)
	}
	cases := []struct {
		storage map[string]string
		maxAge  int64
		at      time.Time
		want    string
	}{
		{storage, 60000, now.Add(19 * time.Second), "[1]"},
		{storage, 60000, now.Add(61 * time.Second), "[]"},
		{storage, 0, now.Add(time.Second), "[]"},
		{map[string]string{}, 60000, now, "[]"},
		{map[string]string{loggingContextStorageKey: loggingContextRecord([]byte(`[null,"A"]`), now)}, 60000, now.Add(time.Second), "[0]"},
	}
	for _, item := range cases {
		if got := string(loggingContextRequest(item.storage, item.maxAge, item.at)); got != item.want {
			t.Fatalf("storage=%v maxAge=%d 正文 %s，期望 %s", item.storage, item.maxAge, got, item.want)
		}
	}
}
