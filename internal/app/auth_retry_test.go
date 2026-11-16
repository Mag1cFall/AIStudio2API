package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

// TestAuthRefreshWithoutExtension 验证缺少续签扩展的账户从当前 Chrome 导入同一邮箱并写回扩展，重新登录保留扩展
func TestAuthRefreshWithoutExtension(t *testing.T) {
	directory := t.TempDir()
	storage := filepath.Join(directory, "storage-state.json")
	state := `{"cookies":[{"name":"SAPISID","value":"old","domain":".google.com","path":"/","secure":true,"sameSite":"Lax"}],"origins":[]}`
	if err := os.WriteFile(storage, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := aistudio.LoadStorageState(storage)
	if err != nil {
		t.Fatal(err)
	}
	account := &aistudio.Account{
		ID: "user@example.com", Directory: directory, StoragePath: storage,
		RuntimePath: filepath.Join(directory, "runtime-state.json"), StorageState: loaded,
		Config: aistudio.DefaultAccountConfig("user@example.com"),
	}
	pool := aistudio.NewAccountPool([]*aistudio.Account{account}, 1)
	lease, err := pool.AcquireAccount(t.Context(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	var cookies []aistudio.StateCookie
	for _, name := range []string{"SAPISID", "__Secure-1PAPISID", "__Secure-3PAPISID"} {
		cookies = append(cookies, aistudio.StateCookie{Name: name, Value: "chrome", Domain: ".google.com", Path: "/", Expires: -1, Secure: true, SameSite: "Lax"})
	}
	refresher := &authRuntimeRefresher{
		pool: pool, requests: newRequestRegistry(t.Context()),
		chromeHas: func(email string) bool { return email == account.ID },
		importCurrent: func(_ context.Context, source aistudio.AuthSource, _ *aistudio.ChromeOAuthMaterial, _ string) (aistudio.StorageState, error) {
			updated := aistudio.StorageState{Cookies: cookies}
			updated.SetAuthExtension(aistudio.AuthExtension{
				Source: source, OAuth: &aistudio.ChromeOAuthMaterial{GaiaID: "gaia", RefreshToken: "token"},
			})
			return updated, nil
		},
		reset: func(string) error { return nil },
	}
	ctx := aistudio.ContextWithAccountLease(t.Context(), lease)
	if !refresher.Available(ctx) {
		t.Fatal("账户在 Chrome 中登录但被判定为不可续签")
	}
	if err := refresher.Refresh(ctx, lease.AuthGeneration()); err != nil {
		t.Fatal(err)
	}
	stored, err := aistudio.LoadStorageState(storage)
	if err != nil {
		t.Fatal(err)
	}
	extension, exists, err := stored.AuthExtension()
	if err != nil || !exists || extension.OAuth == nil || extension.Source.Email != account.ID || stored.Cookies[0].Value != "chrome" {
		t.Fatalf("续签结果 exists=%t extension=%+v cookies=%+v err=%v", exists, extension, stored.Cookies, err)
	}

	login := aistudio.StorageState{Cookies: cookies}
	login.SetAuthExtension(aistudio.AuthExtension{Source: aistudio.AuthSource{Browser: "camoufox", Email: account.ID}})
	if err := keepAuthExtension(&login, stored); err != nil {
		t.Fatal(err)
	}
	if kept, exists, _ := login.AuthExtension(); !exists || kept.OAuth == nil || kept.OAuth.RefreshToken != "token" {
		t.Fatalf("重新登录丢失续签扩展: %+v", kept)
	}
}

// newAuthRecoveryFixture 创建有一个账户、已注入账户租约上下文的续签测试环境
func newAuthRecoveryFixture(t *testing.T) (*aistudio.Account, *aistudio.AccountLease, *aistudio.AccountPool, context.Context) {
	t.Helper()
	directory := t.TempDir()
	storage := filepath.Join(directory, "storage-state.json")
	state := `{"cookies":[{"name":"SAPISID","value":"old","domain":".google.com","path":"/","secure":true,"sameSite":"Lax"}],"origins":[]}`
	if err := os.WriteFile(storage, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := aistudio.LoadStorageState(storage)
	if err != nil {
		t.Fatal(err)
	}
	account := &aistudio.Account{
		ID: "user@example.com", Directory: directory, StoragePath: storage,
		RuntimePath: filepath.Join(directory, "runtime-state.json"), StorageState: loaded,
		Config: aistudio.DefaultAccountConfig("user@example.com"),
	}
	pool := aistudio.NewAccountPool([]*aistudio.Account{account}, 1)
	lease, err := pool.AcquireAccount(t.Context(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lease.Release() })
	return account, lease, pool, aistudio.ContextWithAccountLease(t.Context(), lease)
}

// TestAuthRecoveryDisabledMarksRequired 验证关闭续签时认证失败不读取续签材料，账户仍按官网确认的结果标记为需要重新登录
func TestAuthRecoveryDisabledMarksRequired(t *testing.T) {
	account, lease, pool, ctx := newAuthRecoveryFixture(t)
	used := false
	refresher := &authRuntimeRefresher{
		pool: pool, requests: newRequestRegistry(t.Context()), authRecovery: false,
		refresh: func(context.Context, aistudio.AuthSource, aistudio.ChromeOAuthMaterial, string) ([]aistudio.StateCookie, *aistudio.BoundSession, error) {
			used = true
			return nil, nil, nil
		},
		importCurrent: func(context.Context, aistudio.AuthSource, *aistudio.ChromeOAuthMaterial, string) (aistudio.StorageState, error) {
			used = true
			return aistudio.StorageState{}, nil
		},
		chromeHas: func(email string) bool { return email == account.ID },
		reset:     func(string) error { return nil },
	}
	if !refresher.Available(ctx) {
		t.Fatal("账户在 Chrome 中登录但被判定为不可续签")
	}
	cause := fmt.Errorf("%w: test", camoufoxnative.ErrAuthenticationRequired)
	err := refresher.Recover(ctx, cause, lease.AuthGeneration())
	if err == nil || !errors.Is(err, camoufoxnative.ErrAuthenticationRequired) {
		t.Fatalf("Recover err = %v", err)
	}
	if used {
		t.Fatal("关闭续签后仍读取了续签材料")
	}
	if account.State != aistudio.AccountAuthRequired {
		t.Fatalf("account.State = %v", account.State)
	}
}

// TestAuthRecoveryEnabledRenews 验证默认开启续签时认证失败按账户保存的材料原地续签
func TestAuthRecoveryEnabledRenews(t *testing.T) {
	account, lease, pool, ctx := newAuthRecoveryFixture(t)
	var cookies []aistudio.StateCookie
	for _, name := range []string{"SAPISID", "__Secure-1PAPISID", "__Secure-3PAPISID"} {
		cookies = append(cookies, aistudio.StateCookie{Name: name, Value: "renewed", Domain: ".google.com", Path: "/", Expires: -1, Secure: true, SameSite: "Lax"})
	}
	imported := false
	refresher := &authRuntimeRefresher{
		pool: pool, requests: newRequestRegistry(t.Context()), authRecovery: true,
		refresh: func(context.Context, aistudio.AuthSource, aistudio.ChromeOAuthMaterial, string) ([]aistudio.StateCookie, *aistudio.BoundSession, error) {
			return nil, nil, fmt.Errorf("没有 OAuth 材料时不应调用 refresh")
		},
		importCurrent: func(_ context.Context, source aistudio.AuthSource, _ *aistudio.ChromeOAuthMaterial, _ string) (aistudio.StorageState, error) {
			imported = true
			updated := aistudio.StorageState{Cookies: cookies}
			updated.SetAuthExtension(aistudio.AuthExtension{
				Source: source, OAuth: &aistudio.ChromeOAuthMaterial{GaiaID: "gaia", RefreshToken: "token"},
			})
			return updated, nil
		},
		chromeHas: func(email string) bool { return email == account.ID },
		reset:     func(string) error { return nil },
	}
	cause := fmt.Errorf("%w: test", camoufoxnative.ErrAuthenticationRequired)
	if err := refresher.Recover(ctx, cause, lease.AuthGeneration()); err != nil {
		t.Fatal(err)
	}
	if !imported {
		t.Fatal("开启续签时认证失败没有读取续签材料")
	}
	stored, err := aistudio.LoadStorageState(account.StoragePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Cookies) == 0 || stored.Cookies[0].Value != "renewed" {
		t.Fatalf("续签结果 cookies = %+v", stored.Cookies)
	}
}
