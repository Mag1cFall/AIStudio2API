package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
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
	pool := aistudio.NewAccountPool(nil, 1)
	admin := &runtimeAdmin{configPath: path, requests: newRequestRegistry(ctx), pool: pool, service: &trackedService{pool: pool}}
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
	value.AutoStart = true
	if _, err := admin.UpdateRuntimeConfig(ctx, value); err != nil {
		t.Fatal(err)
	}
	saved, err := config.Load(path)
	if err != nil || saved.AdminPassword != cfg.AdminPassword {
		t.Fatalf("password changed: %v", err)
	}
	if !saved.AutoStart {
		t.Fatal("auto_start was not saved")
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
	_, err = admin.UpdateRuntimeConfig(ctx, value)
	var status interface{ HTTPStatus() int }
	if !errors.As(err, &status) || status.HTTPStatus() != http.StatusBadRequest {
		t.Fatalf("empty enabled password error = %v", err)
	}
	value.AdminAuthEnabled = false
	if _, err := admin.UpdateRuntimeConfig(ctx, value); err != nil {
		t.Fatal(err)
	}
}

// TestAuthRecoveryLifecycle 核对保存与运行的 AUTH_RECOVERY 不一致的两个方向都标为需要重启，一致则不标，无关保存保留 .env 与环境变量取值
func TestAuthRecoveryLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	cfg := config.Default()
	cfg.AuthRecovery = false
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := aistudio.NewAccountPool(nil, 1)
	admin := &runtimeAdmin{configPath: path, requests: newRequestRegistry(ctx), pool: pool, service: &trackedService{pool: pool}}
	manager := &runtimeManager{}

	value, err := admin.RuntimeConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if value.SavedAuthRecovery {
		t.Fatal("SavedAuthRecovery 未载入保存值")
	}
	raw, err := json.Marshal(value)
	if err != nil || strings.Contains(string(raw), "saved_auth_recovery") {
		t.Fatalf("保存值泄漏到前端配置: %s", raw)
	}

	active := config.Default()
	if decorated := manager.decorateRuntimeConfig(value, active); !decorated.ServiceRestartRequired {
		t.Fatal("保存 false、运行 true 未标为需要重启")
	}
	active.AuthRecovery = false
	if decorated := manager.decorateRuntimeConfig(value, active); decorated.ServiceRestartRequired {
		t.Fatal("保存与运行一致仍标为需要重启")
	}

	t.Setenv("AUTH_RECOVERY", "true")
	value, err = admin.RuntimeConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !value.SavedAuthRecovery {
		t.Fatal("环境变量决定的 AUTH_RECOVERY 未进入重启判定")
	}
	if decorated := manager.decorateRuntimeConfig(value, active); !decorated.ServiceRestartRequired {
		t.Fatal("环境变量 true、运行 false 未标为需要重启")
	}
	value.AutoStart = true
	updated, err := admin.UpdateRuntimeConfig(ctx, value)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.SavedAuthRecovery {
		t.Fatal("无关保存丢失了环境变量决定的 AUTH_RECOVERY")
	}
	saved, err := config.Load(path)
	if err != nil || !saved.AuthRecovery {
		t.Fatalf("保存值被改写: %+v err=%v", saved, err)
	}
	content, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(content), "AUTH_RECOVERY=false") {
		t.Fatalf(".env 原值被改写: %s err=%v", content, err)
	}
}
