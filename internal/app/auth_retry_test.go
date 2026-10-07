package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
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
			err := updated.SetAuthExtension(aistudio.AuthExtension{
				Source: source, OAuth: &aistudio.ChromeOAuthMaterial{GaiaID: "gaia", RefreshToken: "token"},
			})
			return updated, err
		},
		reset:          func(string) error { return nil },
		prepareHeaders: func(string) (func(bool), error) { return func(bool) {}, nil },
	}
	ctx := aistudio.ContextWithAccountLease(t.Context(), lease)
	if !refresher.Available(ctx) {
		t.Fatal("账户在 Chrome 中登录但被判定为不可续签")
	}
	if err := refresher.Refresh(ctx); err != nil {
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
	if err := keepAuthExtension(&login, stored); err != nil {
		t.Fatal(err)
	}
	if kept, exists, _ := login.AuthExtension(); !exists || kept.OAuth == nil || kept.OAuth.RefreshToken != "token" {
		t.Fatalf("重新登录丢失续签扩展: %+v", kept)
	}
}
