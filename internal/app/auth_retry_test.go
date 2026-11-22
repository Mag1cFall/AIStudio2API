package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// TestAuthRefreshKeepsAccountSource 验证没有 OAuth 续签材料的账户不可续签且认证状态原样保留，重新登录以新登录结果为准
func TestAuthRefreshKeepsAccountSource(t *testing.T) {
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
		pool: pool, requests: newRequestRegistry(),
		importCurrent: func(_ context.Context, source aistudio.AuthSource, _ aistudio.ChromeOAuthMaterial, _ string) (aistudio.StorageState, error) {
			updated := aistudio.StorageState{Cookies: cookies}
			updated.SetAuthExtension(aistudio.AuthExtension{
				Source: source, OAuth: &aistudio.ChromeOAuthMaterial{GaiaID: "gaia", RefreshToken: "token"},
			})
			return updated, nil
		},
		reset: func(string) error { return nil },
	}
	ctx := aistudio.ContextWithAccountLease(t.Context(), lease)
	if refresher.Available(ctx) {
		t.Fatal("没有 OAuth 续签材料的账户被判定为可续签")
	}
	if err := refresher.Refresh(ctx, lease.AuthGeneration()); err == nil {
		t.Fatal("没有 OAuth 续签材料的账户续签成功")
	}
	if stored, err := os.ReadFile(storage); err != nil || string(stored) != state {
		t.Fatalf("续签失败后认证状态被改写: %s err=%v", stored, err)
	}

	chrome := aistudio.StorageState{Cookies: cookies}
	chrome.SetAuthExtension(aistudio.AuthExtension{
		Source: aistudio.AuthSource{Browser: "chrome", Email: account.ID},
		OAuth:  &aistudio.ChromeOAuthMaterial{GaiaID: "gaia", RefreshToken: "token"},
	})
	if err := lease.SaveStorageState(chrome); err != nil {
		t.Fatal(err)
	}
	login := aistudio.StorageState{Cookies: cookies}
	login.SetAuthExtension(aistudio.AuthExtension{Source: aistudio.AuthSource{Browser: "camoufox", Email: account.ID}})
	if err := replaceAccountState(lease, login, ""); err != nil {
		t.Fatal(err)
	}
	stored, err := aistudio.LoadStorageState(storage)
	if err != nil {
		t.Fatal(err)
	}
	if extension, _, err := stored.AuthExtension(); err != nil || extension.OAuth != nil || extension.Source.Browser != "camoufox" {
		t.Fatalf("重新登录后的认证扩展 %+v err=%v", extension, err)
	}
}
