package config

import (
	"testing"
	"time"
)

// TestValidateProxyCredentials 验证代理 URL 接受账号密码并拒绝空用户名
func TestValidateProxyCredentials(t *testing.T) {
	for _, value := range []string{"http://user:secret@127.0.0.1:8080", "socks5://user:secret@127.0.0.1:1080"} {
		if err := ValidateProxy(value); err != nil {
			t.Errorf("ValidateProxy(%q) = %v", value, err)
		}
	}
	if err := ValidateProxy("http://:secret@127.0.0.1:8080"); err == nil {
		t.Error("空用户名被接受")
	}
}

// TestBuildStallTimeoutAndAssistantFallbackDefaults 验证 BUILD_STALL_TIMEOUT 默认 2 分钟且 ASSISTANT_FALLBACK 默认关闭
func TestBuildStallTimeoutAndAssistantFallbackDefaults(t *testing.T) {
	cfg := Default()
	if cfg.BuildStallTimeout != 2*time.Minute {
		t.Fatalf("BuildStallTimeout = %v, want 2m", cfg.BuildStallTimeout)
	}
	if cfg.AssistantFallback {
		t.Fatalf("AssistantFallback = true, want false")
	}
	path := writeTestEnv(t, "BUILD_STALL_TIMEOUT=90s\nASSISTANT_FALLBACK=true\n")
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.BuildStallTimeout != 90*time.Second || !loaded.AssistantFallback {
		t.Fatalf("loaded = %+v", loaded)
	}
}
