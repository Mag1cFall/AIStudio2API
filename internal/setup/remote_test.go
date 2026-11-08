package setup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

// TestSetupRemoteOrigin 验证远程地址只接受 HTTPS origin 与本机 HTTP
func TestSetupRemoteOrigin(t *testing.T) {
	for raw, want := range map[string]string{
		"https://example.com":       "https://example.com",
		"https://example.com:8443/": "https://example.com:8443",
		"http://127.0.0.1:2048":     "http://127.0.0.1:2048",
		"http://localhost:2048":     "http://localhost:2048",
		"http://[::1]:2048":         "http://[::1]:2048",
		"http://192.168.1.2:2048":   "",
		"http://example.com":        "",
		"https://example.com/api":   "",
		"https://u:p@example.com":   "",
		"https://example.com?a=1":   "",
		"example.com":               "",
	} {
		got, err := remoteOrigin(raw)
		if want == "" && err == nil || want != "" && (err != nil || got != want) {
			t.Errorf("remoteOrigin(%q) = %q, %v", raw, got, err)
		}
	}
	if _, err := parseSetupFlags([]string{"--token", "abc"}, config.Config{}); err == nil {
		t.Fatal("没有 --remote 时接受了 --token")
	}
}

// TestPairingRenewalUploadWithoutBoundSession 验证远程上传保留 Chrome 来源与 OAuth 续签材料，DBSC 会话登记留在持有设备绑定密钥的本机
func TestPairingRenewalUploadWithoutBoundSession(t *testing.T) {
	var uploaded aistudio.AuthExtension
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input api.AccountStateInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		uploaded, _, _ = input.StorageState.AuthExtension()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(api.PairingAccountResult{Account: api.AdminAccount{ID: "user@example.com", Label: "user@example.com"}, RenewalToken: "renewal"})
	}))
	defer server.Close()
	state := aistudio.StorageState{Cookies: []aistudio.StateCookie{}, Origins: []aistudio.StorageOrigin{}}
	state.SetAuthExtension(aistudio.AuthExtension{
		Source: aistudio.AuthSource{Browser: "chrome", Profile: "Default", Email: "user@example.com"},
		OAuth:  &aistudio.ChromeOAuthMaterial{GaiaID: "gaia", RefreshToken: "token", WrappedBindingKey: []byte("key")},
		Bound:  &aistudio.BoundSession{ID: "sidts_session", RefreshURL: "https://accounts.google.com/RotateBoundCookies", Credentials: []string{"__Secure-1PSIDRTS"}},
	})
	target := &remoteTarget{client: server.Client(), origin: server.URL, token: "pairing"}
	if _, err := target.Save(t.Context(), aistudio.DefaultAccountConfig("user@example.com"), state, ""); err != nil {
		t.Fatal(err)
	}
	if uploaded.OAuth == nil || uploaded.OAuth.RefreshToken != "token" || uploaded.Bound != nil {
		t.Fatalf("上传的认证扩展 %+v", uploaded)
	}
	if len(target.renewals) != 1 || target.renewals[0].OAuth.RefreshToken != "token" {
		t.Fatalf("本机续签材料 %+v", target.renewals)
	}
}
