package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

// TestAdminConfig 验证凭据保存、密码保留与管理重启标记
func TestAdminConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	cfg := config.Default()
	cfg.AdminAuthEnabled = true
	cfg.AdminUsername = "operator"
	cfg.AdminPassword = "test-password"
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	admin := &runtimeAdmin{configPath: path, requests: newRequestRegistry(ctx)}
	value, err := admin.RuntimeConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "test-password") || !value.AdminPasswordSet || !value.BuildNativeNonstream {
		t.Fatalf("config=%s", raw)
	}
	value.Proxy = "http://127.0.0.1:1234"
	if _, err := admin.UpdateRuntimeConfig(ctx, value); err != nil {
		t.Fatal(err)
	}
	saved, err := config.Load(path)
	if err != nil || saved.AdminPassword != cfg.AdminPassword {
		t.Fatalf("password changed: %v", err)
	}
	manager := &runtimeManager{activeManagement: cfg}
	password := "new-test-password"
	value.AdminPassword = &password
	value, err = admin.UpdateRuntimeConfig(ctx, value)
	if err != nil {
		t.Fatal(err)
	}
	decorated := manager.decorateRuntimeConfig(value, cfg)
	if !decorated.ManagementRestartRequired || decorated.AdminPassword != nil {
		t.Fatalf("restart=%+v", decorated)
	}
	value.AdminPassword = new(string)
	if _, err := admin.UpdateRuntimeConfig(ctx, value); err == nil {
		t.Fatal("empty enabled password accepted")
	}
	value.AdminAuthEnabled = false
	if _, err := admin.UpdateRuntimeConfig(ctx, value); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaFallbackRuntimeConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	active := config.Default()
	if err := active.Save(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	admin := &runtimeAdmin{configPath: path, requests: newRequestRegistry(ctx)}
	value := runtimeConfigDTO(active)
	if !sameDataConfig(value, active, dataConfigOverrides{}) {
		t.Fatal("unchanged config requires restart")
	}
	value.SchemaFallback = true
	saved, err := admin.UpdateRuntimeConfig(ctx, value)
	if err != nil || !saved.SchemaFallback {
		t.Fatalf("fallback not saved: %v", err)
	}
	if sameDataConfig(saved, active, dataConfigOverrides{}) {
		t.Fatal("fallback change must require a data-plane restart")
	}
	loaded, err := admin.RuntimeConfig(ctx)
	if err != nil || !loaded.SchemaFallback {
		t.Fatalf("fallback not read back: %v", err)
	}
	loaded.TemporaryChat = !loaded.TemporaryChat
	if saved, err = admin.UpdateRuntimeConfig(ctx, loaded); err != nil || !saved.SchemaFallback {
		t.Fatalf("unrelated settings save lost fallback: %v", err)
	}
}
