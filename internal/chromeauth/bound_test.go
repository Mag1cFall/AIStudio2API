package chromeauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// softwareBindingKey 是以内存 P-256 私钥签发 IEEE P1363 签名的设备绑定密钥
type softwareBindingKey struct {
	key *ecdsa.PrivateKey
}

func (key softwareBindingKey) PublicKey() ([]byte, error) {
	return x509.MarshalPKIXPublicKey(&key.key.PublicKey)
}

func (key softwareBindingKey) SignSHA256(value []byte) ([]byte, error) {
	digest := sha256.Sum256(value)
	r, s, err := ecdsa.Sign(rand.Reader, key.key, digest[:])
	if err != nil {
		return nil, err
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return signature, nil
}

func (softwareBindingKey) Close() {}

// TestGoogleBoundSession 验证 OAuthMultilogin 响应中 google.com 的 DBSC 会话登记按 Chromium OAuthMultiloginResult 的字段解析，相对刷新地址按 Gaia 地址补全
func TestGoogleBoundSession(t *testing.T) {
	body := `{"status":"OK","device_bound_session_info":[
		{"is_device_bound":false,"domain":"GOOGLE_COM","register_session_payload":{"session_identifier":"unbound","refresh_url":"/Unbound","credentials":[{"type":"cookie","name":"SID"}]}},
		{"is_device_bound":true,"domain":"YOUTUBE_COM","register_session_payload":{"session_identifier":"yt","refresh_url":"/RotateBoundCookies","credentials":[{"type":"cookie","name":"YT"}]}},
		{"is_device_bound":true,"domain":"GOOGLE_COM","register_session_payload":{"session_identifier":"sidts_session","credentials":[
			{"type":"cookie","name":"__Secure-1PSIDTS","scope":{"domain":".google.com","path":"/"},"attributes":"Secure; HttpOnly; Domain=.google.com; Path=/"},
			{"type":"cookie","name":"__Secure-3PSIDTS","scope":{"domain":".google.com","path":"/"},"attributes":"Secure; HttpOnly; Domain=.google.com; Path=/; SameSite=None"},
			{"type":"cookie","name":"__Secure-1PSIDRTS","scope":{"domain":".google.com","path":"/"},"attributes":"Secure; HttpOnly; Domain=.google.com; Path=/"},
			{"type":"cookie","name":"__Secure-3PSIDRTS","scope":{"domain":".google.com","path":"/"},"attributes":"Secure; HttpOnly; Domain=.google.com; Path=/; SameSite=None"}
		],"refresh_url":"/RotateBoundCookies","scope":{"origin":"https://google.com","include_site":true},"allowed_refresh_initiators":["*"]}}]}`
	var response multiloginResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatal(err)
	}
	want := &aistudio.BoundSession{
		ID: "sidts_session", RefreshURL: "https://accounts.google.com/RotateBoundCookies",
		Credentials: []string{"__Secure-1PSIDTS", "__Secure-3PSIDTS", "__Secure-1PSIDRTS", "__Secure-3PSIDRTS"},
	}
	if got := response.googleBoundSession(); !reflect.DeepEqual(got, want) {
		t.Fatalf("DBSC 会话登记 = %+v，期望 %+v", got, want)
	}
	if got := (multiloginResponse{Status: "OK"}).googleBoundSession(); got != nil {
		t.Fatalf("没有 device_bound_session_info 时返回 %+v", got)
	}
}

// TestBoundChallenge 验证按 DBSC Secure-Session-Challenge 结构化列表取出属于本会话或未指明会话的挑战
func TestBoundChallenge(t *testing.T) {
	for _, item := range []struct{ header, want string }{
		{`"abc";id="sidts_session"`, "abc"},
		{`"other";id="yt", "mine";id="sidts_session"`, "mine"},
		{`"bare"`, "bare"},
		{`"other";id="yt"`, ""},
		{``, ""},
	} {
		if got := boundChallenge(item.header, "sidts_session"); got != item.want {
			t.Fatalf("boundChallenge(%q) = %q，期望 %q", item.header, got, item.want)
		}
	}
}

// TestSignBoundRefresh 验证 DBSC 刷新令牌与 Chromium CreateKeyRefreshHeaderAndPayload 一致：header 只有 ES256 与 dbsc+jwt，payload 只有 jti，签名为设备密钥的 ES256
func TestSignBoundRefresh(t *testing.T) {
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signBoundRefresh(softwareBindingKey{private}, "challenge-value")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(signed, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT 段数 %d", len(parts))
	}
	header, _ := base64.RawURLEncoding.DecodeString(parts[0])
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if string(header) != `{"alg":"ES256","typ":"dbsc+jwt"}` || string(payload) != `{"jti":"challenge-value"}` {
		t.Fatalf("header=%s payload=%s", header, payload)
	}
	signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(signature) != 64 || !ecdsa.Verify(&private.PublicKey, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
		t.Fatal("签名无法以设备公钥验证")
	}
}

// TestBoundRefreshStatusError 验证按 Chromium RegistrationFetcher 把 3xx 与 4xx（429 除外）归为会话被拒绝
func TestBoundRefreshStatusError(t *testing.T) {
	for status, rejected := range map[int]bool{302: true, 400: true, 401: true, 403: true, 429: false, 500: false, 503: false} {
		if got := errors.Is(boundRefreshStatusError(status), ErrBoundSessionRejected); got != rejected {
			t.Fatalf("HTTP %d 被拒绝=%t，期望 %t", status, got, rejected)
		}
	}
}
