package config

import (
	"path/filepath"
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

// TestValidateRoutingStrategyKeyAffinity 验证路由策略接受 key-affinity 并拒绝未知值，且支持配置亲和有效时长
func TestValidateRoutingStrategyKeyAffinity(t *testing.T) {
	cfg := Default()
	if cfg.KeyAffinityTTL != 60*time.Minute {
		t.Fatalf("默认 KeyAffinityTTL = %v, want 60m", cfg.KeyAffinityTTL)
	}
	cfg.RoutingStrategy = "key-affinity"
	if err := cfg.Validate(); err != nil {
		t.Errorf("key-affinity 被拒绝: %v", err)
	}
	cfg.RoutingStrategy = "bogus"
	if err := cfg.Validate(); err == nil {
		t.Error("未知策略被接受")
	}
	cfg.RoutingStrategy = "key-affinity"
	cfg.KeyAffinityTTL = 0
	if err := cfg.Validate(); err == nil {
		t.Error("零值 KeyAffinityTTL 被接受")
	}
	cfg.KeyAffinityTTL = 45 * time.Minute
	path := filepath.Join(t.TempDir(), ".env")
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.KeyAffinityTTL != 45*time.Minute {
		t.Errorf("加载后 KeyAffinityTTL = %v, want 45m", loaded.KeyAffinityTTL)
	}
}
