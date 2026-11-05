package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeTestEnv 在临时目录写入 .env 并返回路径
func writeTestEnv(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// readTestEnv 读取 .env 全文
func readTestEnv(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestSaveWritesOnlyChangedKeys 核对保存写入 WAA 后端与其余和默认值不同的键，未写的键取当前默认值
func TestSaveWritesOnlyChangedKeys(t *testing.T) {
	path := writeTestEnv(t, "")
	cfg := Default()
	cfg.RoutingStrategy = "fill-first"
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	if got := readTestEnv(t, path); got != "ROUTING_STRATEGY=fill-first\nWAA_BACKEND=spidermonkey\n" {
		t.Fatalf(".env = %q", got)
	}
	loaded, err := Load(path)
	if err != nil || loaded.RoutingStrategy != "fill-first" || loaded.WAABackend != WAABackendSpiderMonkey {
		t.Fatalf("loaded = %+v, err = %v", loaded, err)
	}
}

// TestLoadInvalidValueUsesDefault 核对取值无效的项按默认值加载并记录键与原值
func TestLoadInvalidValueUsesDefault(t *testing.T) {
	path := writeTestEnv(t, "ROUTING_STRATEGY=fill-first\nMEDIA_OUTPUT=file\nPER_ACCOUNT_CONCURRENCY=abc\nWARM_WORKER_LIMIT=30\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MediaOutput != MediaOutputInline || cfg.PerAccountConcurrency != defaultAccountConcurrency ||
		cfg.WarmWorkerLimit != defaultWarmWorkerLimit || cfg.MaxActiveWorkers != defaultMaxActiveWorkers || cfg.RoutingStrategy != "fill-first" {
		t.Fatalf("cfg = %+v", cfg)
	}
	var keys []string
	for _, item := range cfg.Invalid {
		keys = append(keys, item.Key+"="+item.Value)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"MEDIA_OUTPUT=file", "PER_ACCOUNT_CONCURRENCY=abc", "WARM_WORKER_LIMIT=30"}) {
		t.Fatalf("invalid = %v", keys)
	}
}

// TestEnvironmentKeepsSavedFile 核对环境变量决定的项按环境变量生效，保存时保留 .env 中原有的值
func TestEnvironmentKeepsSavedFile(t *testing.T) {
	path := writeTestEnv(t, "ROUTING_STRATEGY=fill-first\n")
	t.Setenv("ROUTING_STRATEGY", "round-robin")
	saved, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.RoutingStrategy != "round-robin" || !slices.Equal(saved.Environment, []string{"ROUTING_STRATEGY"}) {
		t.Fatalf("saved = %+v", saved)
	}
	update := Default()
	update.RoutingStrategy = "fill-first"
	update.RequestBodyLog = true
	update = update.KeepEnvironment(saved)
	if update.RoutingStrategy != "round-robin" {
		t.Fatalf("update = %+v", update)
	}
	if err := update.Save(path); err != nil {
		t.Fatal(err)
	}
	if got := readTestEnv(t, path); got != "ROUTING_STRATEGY=fill-first\nWAA_BACKEND=spidermonkey\nREQUEST_BODY_LOG=true\n" {
		t.Fatalf(".env = %q", got)
	}
}

// TestAPIKeyAccounts 核对 API_KEY_ACCOUNTS 按最后一个冒号分出 key 与以竖线分隔的账户，跳过不在 PROXY_API_KEY 中的 key 而保留其余绑定，取值无效时按默认值加载且原因不含 key
func TestAPIKeyAccounts(t *testing.T) {
	accounts, err := ParseAPIKeyAccounts(" sk-a : a@example.com | b@example.com , sk:b:c@example.com", "sk-a, sk:b")
	if err != nil || len(accounts) != 2 || len(accounts["sk-a"]) != 2 || accounts["sk-a"][1] != "b@example.com" || accounts["sk:b"][0] != "c@example.com" {
		t.Fatalf("accounts=%v err=%v", accounts, err)
	}
	if accounts, err := ParseAPIKeyAccounts("sk-a:a@example.com,sk-gone:b@example.com", "sk-a"); err != nil || len(accounts) != 1 || accounts["sk-a"][0] != "a@example.com" {
		t.Fatalf("已删除 key 的绑定 accounts=%v err=%v", accounts, err)
	}
	for _, value := range []string{"sk-a", "sk-a:", "sk-a:a@example.com,sk-a:b@example.com"} {
		if _, err := ParseAPIKeyAccounts(value, "sk-a"); err == nil {
			t.Fatalf("%q accepted", value)
		}
	}
	cfg, err := Load(writeTestEnv(t, "PROXY_API_KEY=sk-secret\nAPI_KEY_ACCOUNTS=sk-secret:a@example.com,sk-gone:b@example.com\n"))
	if err != nil || cfg.APIKeyAccounts != "sk-secret:a@example.com,sk-gone:b@example.com" || len(cfg.Invalid) != 0 {
		t.Fatalf("含已删除 key 的绑定 value=%q invalid=%+v err=%v", cfg.APIKeyAccounts, cfg.Invalid, err)
	}
	cfg, err = Load(writeTestEnv(t, "PROXY_API_KEY=sk-secret\nAPI_KEY_ACCOUNTS=sk-secret:\n"))
	if err != nil || cfg.APIKeyAccounts != "" || len(cfg.Invalid) != 1 || cfg.Invalid[0].Key != "API_KEY_ACCOUNTS" || strings.Contains(cfg.Invalid[0].Reason, "sk-") {
		t.Fatalf("invalid=%+v err=%v", cfg.Invalid, err)
	}
}

// TestEnvInvalidSecretOmitsValue 核对取值无效的凭据配置项只记录键名与原因，不记录原值
func TestEnvInvalidSecretOmitsValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("PROXY=http://alice:s3cret@127.0.0.1:notaport\nMEDIA_OUTPUT=file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, invalid := range cfg.Invalid {
		values[invalid.Key] = invalid.Value
	}
	if value, ok := values["PROXY"]; !ok || value != "" {
		t.Fatalf("PROXY 无效记录=%q 存在=%v", value, ok)
	}
	if values["MEDIA_OUTPUT"] != "file" {
		t.Fatalf("MEDIA_OUTPUT 无效记录=%q", values["MEDIA_OUTPUT"])
	}
}

// TestEnvKeepEnvironmentKeepsInvalid 核对保存设置时环境变量决定的无效项仍列在无效记录中
func TestEnvKeepEnvironmentKeepsInvalid(t *testing.T) {
	t.Setenv("MEDIA_OUTPUT", "file")
	path := filepath.Join(t.TempDir(), ".env")
	saved, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	kept := Default().KeepEnvironment(saved)
	if len(kept.Invalid) != 1 || kept.Invalid[0].Key != "MEDIA_OUTPUT" || kept.MediaOutput != MediaOutputInline {
		t.Fatalf("保存后无效记录=%v 媒体输出=%q", kept.Invalid, kept.MediaOutput)
	}
}
