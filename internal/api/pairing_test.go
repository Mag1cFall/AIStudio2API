package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// pairingAdmin 记录配对上传的账户
type pairingAdmin struct {
	AdminService
	imported []AccountStateInput
}

// ImportAccountState 保存上传内容并返回同名账户
func (admin *pairingAdmin) ImportAccountState(_ context.Context, input AccountStateInput) (AdminAccount, error) {
	admin.imported = append(admin.imported, input)
	return AdminAccount{ID: input.Label, Label: input.Label}, nil
}

// TestPairingTokenAccount 验证令牌由管理会话签发、上传不需要管理会话、新令牌使旧令牌失效
func TestPairingTokenAccount(t *testing.T) {
	admin := &pairingAdmin{}
	handler := NewHandler(nil, Config{Admin: admin, AdminAuthEnabled: true, AdminUsername: "admin", AdminPassword: "secret"})
	serve := func(method, path, body string, header http.Header) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		for key, values := range header {
			request.Header[key] = values
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	if response := serve("POST", "/api/pairing", "", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("无会话签发令牌 = %d", response.Code)
	}
	login := serve("POST", "/api/auth/login", `{"username":"admin","password":"secret"}`, nil)
	cookies := login.Result().Cookies()
	if login.Code != http.StatusOK || len(cookies) == 0 {
		t.Fatalf("登录 = %d %s", login.Code, login.Body)
	}
	session := http.Header{"Cookie": {cookies[0].String()}}
	issue := func() string {
		response := serve("POST", "/api/pairing", "", session)
		var token PairingToken
		if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &token) != nil || token.Token == "" {
			t.Fatalf("签发令牌 = %d %s", response.Code, response.Body)
		}
		if remaining := time.Until(token.ExpiresAt); remaining <= 9*time.Minute || remaining > 10*time.Minute {
			t.Fatalf("令牌有效期 = %s", remaining)
		}
		return token.Token
	}
	upload := func(token string) int {
		header := http.Header{"Content-Type": {"application/json"}}
		if token != "" {
			header.Set("Authorization", "Bearer "+token)
		}
		body := `{"label":"a@example.com","locale":"en-US","timezone":"UTC","storage_state":{"cookies":[],"origins":[]},"fingerprint":{"config":{"a":1}}}`
		return serve("POST", "/api/pairing/accounts", body, header).Code
	}
	first := issue()
	if code := upload(""); code != http.StatusUnauthorized {
		t.Fatalf("无令牌上传 = %d", code)
	}
	if code := upload(first + "x"); code != http.StatusUnauthorized {
		t.Fatalf("错误令牌上传 = %d", code)
	}
	if code := upload(first); code != http.StatusCreated {
		t.Fatalf("令牌上传 = %d", code)
	}
	if code := upload(first); code != http.StatusCreated {
		t.Fatalf("有效期内第二个账户 = %d", code)
	}
	second := issue()
	if code := upload(first); code != http.StatusUnauthorized {
		t.Fatalf("旧令牌上传 = %d", code)
	}
	if code := upload(second); code != http.StatusCreated {
		t.Fatalf("新令牌上传 = %d", code)
	}
	if len(admin.imported) != 3 || admin.imported[0].Label != "a@example.com" || string(admin.imported[0].Fingerprint) != `{"config":{"a":1}}` {
		t.Fatalf("导入内容 = %+v", admin.imported)
	}
}

// TestPairingTokenExpiry 验证令牌到期后失效
func TestPairingTokenExpiry(t *testing.T) {
	tokens := &pairingTokens{}
	now := time.Unix(1_700_000_000, 0)
	token := tokens.issue(now)
	if !tokens.valid(token.Token, now.Add(pairingTokenLifetime-time.Second)) {
		t.Fatal("到期前令牌无效")
	}
	if tokens.valid(token.Token, now.Add(pairingTokenLifetime)) {
		t.Fatal("到期后令牌仍有效")
	}
	if (&pairingTokens{}).valid("", now) {
		t.Fatal("未签发时空令牌有效")
	}
}
