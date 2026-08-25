package chromeauth

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"strings"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

const (
	multiloginURL     = "https://accounts.google.com/oauth/multilogin?source=ChromiumAccountReconcilorDice&reuseCookies=0"
	oauthClientID     = "77185425430.apps.googleusercontent.com"
	assertionAudience = "https://accounts.google.com/accountmanager"
	assertionSentinel = "DBSC_CHALLENGE_IF_REQUIRED"
)

type multiloginCookie struct {
	Name       string   `json:"name"`
	Value      string   `json:"value"`
	Domain     string   `json:"domain"`
	Host       string   `json:"host"`
	Path       string   `json:"path"`
	MaxAge     *float64 `json:"maxAge"`
	IsHTTPOnly bool     `json:"isHttpOnly"`
	IsSecure   bool     `json:"isSecure"`
	SameSite   string   `json:"sameSite"`
}

type multiloginResponse struct {
	Status         string             `json:"status"`
	Cookies        []multiloginCookie `json:"cookies"`
	Directed       json.RawMessage    `json:"token_binding_directed_response"`
	FailedAccounts []struct {
		Retry struct {
			Challenge string `json:"challenge"`
		} `json:"token_binding_retry_response"`
	} `json:"failed_accounts"`
}

type deviceBindingKey interface {
	PublicKey() (*ecdsa.PublicKey, []byte, error)
	SignSHA256([]byte) ([]byte, error)
	Close()
}

func fetchGoogleCookies(ctx context.Context, gaiaID string, token string, wrappedKey []byte, proxyURL string) ([]multiloginCookie, error) {
	bindingKey, err := openDeviceBindingKey(wrappedKey)
	if err != nil {
		return nil, err
	}
	defer bindingKey.Close()
	publicKey, spki, err := bindingKey.PublicKey()
	if err != nil {
		return nil, err
	}
	client, err := newOAuthClient(proxyURL)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()

	firstStatus, first, err := requestMultilogin(ctx, client, gaiaID, token, assertionSentinel)
	if err != nil {
		return nil, err
	}
	challenge := findChallenge(first)
	if first.Status != "RETRY" || challenge == "" {
		return nil, fmt.Errorf("OAuthMultilogin challenge 阶段失败 HTTP %d status %s", firstStatus, first.Status)
	}

	ephemeralPrivateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成 HPKE 临时密钥: %w", err)
	}
	assertion, err := createAssertion(bindingKey, publicKey, spki, challenge, ephemeralPrivateKey.PublicKey().Bytes())
	if err != nil {
		return nil, err
	}
	secondStatus, second, err := requestMultilogin(ctx, client, gaiaID, token, assertion)
	if err != nil {
		return nil, err
	}
	if secondStatus != http.StatusOK || second.Status != "OK" {
		return nil, fmt.Errorf("OAuthMultilogin assertion 阶段失败 HTTP %d status %s", secondStatus, second.Status)
	}
	if len(second.Directed) == 0 || bytes.Equal(second.Directed, []byte("null")) {
		return nil, fmt.Errorf("OAuthMultilogin 响应缺少 token_binding_directed_response")
	}
	if len(second.Cookies) == 0 {
		return nil, fmt.Errorf("OAuthMultilogin 响应缺少 Cookie")
	}

	names := make(map[string]struct{}, len(second.Cookies))
	for index := range second.Cookies {
		cookie := &second.Cookies[index]
		if cookie.Name == "" || cookie.Value == "" {
			return nil, fmt.Errorf("OAuthMultilogin Cookie 格式异常")
		}
		cookie.Value, err = hpkeOpen(ephemeralPrivateKey, cookie.Value)
		if err != nil {
			return nil, err
		}
		if cookie.Domain == "" && (cookie.Host == "" || strings.HasPrefix(cookie.Host, ".")) {
			return nil, fmt.Errorf("OAuthMultilogin Cookie 域格式异常")
		}
		names[cookie.Name] = struct{}{}
	}
	for _, required := range []string{"SAPISID", "__Secure-1PSID"} {
		if _, ok := names[required]; !ok {
			return nil, fmt.Errorf("OAuthMultilogin 缺少核心 Cookie: %s", required)
		}
	}
	return second.Cookies, nil
}

func newOAuthClient(proxyURL string) (tls_client.HttpClient, error) {
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(profiles.Chrome_146),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithCookieJar(tls_client.NewCookieJar()),
	}
	if proxyURL != "" {
		options = append(options, tls_client.WithProxyUrl(proxyURL))
	}
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		return nil, fmt.Errorf("创建 OAuth HTTP 客户端: %w", err)
	}
	return client, nil
}

func requestMultilogin(ctx context.Context, client tls_client.HttpClient, gaiaID string, token string, assertion string) (int, multiloginResponse, error) {
	authorization := encodeMultiOAuthHeader(gaiaID, token, assertion)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, multiloginURL, strings.NewReader(" "))
	if err != nil {
		return 0, multiloginResponse{}, fmt.Errorf("创建 OAuthMultilogin 请求: %w", err)
	}
	request.Header.Set("Authorization", "MultiOAuth "+authorization)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", userAgent)
	response, err := client.Do(request)
	if err != nil {
		return 0, multiloginResponse{}, fmt.Errorf("OAuthMultilogin 请求失败: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return response.StatusCode, multiloginResponse{}, fmt.Errorf("读取 OAuthMultilogin 响应: %w", err)
	}
	body = bytes.TrimSpace(body)
	if bytes.HasPrefix(body, []byte(")]}'")) {
		body = bytes.TrimSpace(body[4:])
	}
	var result multiloginResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return response.StatusCode, multiloginResponse{}, fmt.Errorf("OAuthMultilogin 返回无法解析的响应 HTTP %d", response.StatusCode)
	}
	return response.StatusCode, result, nil
}

func findChallenge(response multiloginResponse) string {
	for _, account := range response.FailedAccounts {
		if account.Retry.Challenge != "" {
			return account.Retry.Challenge
		}
	}
	return ""
}

func encodeMultiOAuthHeader(gaiaID string, token string, assertion string) string {
	account := appendBytesField(nil, 1, []byte(gaiaID))
	account = appendBytesField(account, 2, []byte(token))
	account = appendBytesField(account, 3, []byte(assertion))
	return base64.RawURLEncoding.EncodeToString(appendBytesField(nil, 1, account))
}

func createTinkHPKEKeyset(publicKey []byte) []byte {
	params := appendVarintField(nil, 1, 1)
	params = appendVarintField(params, 2, 1)
	params = appendVarintField(params, 3, 1)
	hpkePublicKey := appendVarintField(nil, 1, 0)
	hpkePublicKey = appendBytesField(hpkePublicKey, 2, params)
	hpkePublicKey = appendBytesField(hpkePublicKey, 3, publicKey)
	keyData := appendBytesField(nil, 1, []byte("type.googleapis.com/google.crypto.tink.HpkePublicKey"))
	keyData = appendBytesField(keyData, 2, hpkePublicKey)
	keyData = appendVarintField(keyData, 3, 3)
	key := appendBytesField(nil, 1, keyData)
	key = appendVarintField(key, 2, 1)
	key = appendVarintField(key, 3, 1)
	key = appendVarintField(key, 4, 3)
	keyset := appendVarintField(nil, 1, 1)
	return appendBytesField(keyset, 2, key)
}

func createAssertion(bindingKey deviceBindingKey, publicKey *ecdsa.PublicKey, spki []byte, challenge string, ephemeralPublicKey []byte) (string, error) {
	header := struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
		Schema    string `json:"schema"`
	}{"ES256", "jwt", "DEVICE_BOUND_SESSION_CREDENTIALS_ASSERTION"}
	payload := struct {
		Subject      string `json:"sub"`
		Audience     string `json:"aud"`
		JWTID        string `json:"jti"`
		Issuer       string `json:"iss"`
		Namespace    string `json:"namespace"`
		EphemeralKey struct {
			KeyType string `json:"kty"`
			KeyInfo string `json:"TinkKeysetPublicKeyInfo"`
		} `json:"ephemeral_key"`
	}{Subject: oauthClientID, Audience: assertionAudience, JWTID: challenge, Namespace: "TokenBinding"}
	issuer := sha256.Sum256(spki)
	payload.Issuer = base64.RawURLEncoding.EncodeToString(issuer[:])
	payload.EphemeralKey.KeyType = "type.googleapis.com/google.crypto.tink.EciesAeadHkdfPublicKey"
	payload.EphemeralKey.KeyInfo = base64.RawURLEncoding.EncodeToString(createTinkHPKEKeyset(ephemeralPublicKey))
	encodedHeader, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("编码 token binding header: %w", err)
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("编码 token binding payload: %w", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(encodedHeader) + "." + base64.RawURLEncoding.EncodeToString(encodedPayload)
	signature, err := bindingKey.SignSHA256([]byte(signingInput))
	if err != nil {
		return "", err
	}
	if len(signature) != 64 {
		return "", fmt.Errorf("NCrypt ECDSA 签名长度异常")
	}
	digest := sha256.Sum256([]byte(signingInput))
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	if !ecdsa.Verify(publicKey, digest[:], r, s) {
		return "", fmt.Errorf("NCrypt ECDSA 签名校验失败")
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func hpkeOpen(privateKey *ecdh.PrivateKey, encodedValue string) (string, error) {
	encrypted, err := base64.RawURLEncoding.DecodeString(encodedValue)
	if err != nil || len(encrypted) <= 48 {
		return "", fmt.Errorf("OAuthMultilogin Cookie 密文格式异常")
	}
	encapsulatedKey := encrypted[:32]
	senderPublicKey, err := ecdh.X25519().NewPublicKey(encapsulatedKey)
	if err != nil {
		return "", fmt.Errorf("解析 HPKE 封装密钥: %w", err)
	}
	sharedDH, err := privateKey.ECDH(senderPublicKey)
	if err != nil {
		return "", fmt.Errorf("计算 HPKE 共享密钥: %w", err)
	}
	kemSuite := append([]byte("KEM"), 0, 0x20)
	hpkeSuite := append([]byte("HPKE"), 0, 0x20, 0, 1, 0, 1)
	eaePRK := labeledExtract(nil, kemSuite, []byte("eae_prk"), sharedDH)
	kemContext := append(append([]byte{}, encapsulatedKey...), privateKey.PublicKey().Bytes()...)
	sharedSecret := labeledExpand(eaePRK, kemSuite, []byte("shared_secret"), kemContext, 32)
	pskIDHash := labeledExtract(nil, hpkeSuite, []byte("psk_id_hash"), nil)
	infoHash := labeledExtract(nil, hpkeSuite, []byte("info_hash"), nil)
	keyScheduleContext := append(append([]byte{0}, pskIDHash...), infoHash...)
	secret := labeledExtract(sharedSecret, hpkeSuite, []byte("secret"), nil)
	key := labeledExpand(secret, hpkeSuite, []byte("key"), keyScheduleContext, 16)
	nonce := labeledExpand(secret, hpkeSuite, []byte("base_nonce"), keyScheduleContext, 12)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("创建 HPKE AES 解密器: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("创建 HPKE GCM 解密器: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, encrypted[32:], []byte{})
	if err != nil {
		return "", fmt.Errorf("OAuthMultilogin Cookie 解密失败")
	}
	return string(plaintext), nil
}

func labeledExtract(salt []byte, suite []byte, label []byte, input []byte) []byte {
	material := append([]byte("HPKE-v1"), suite...)
	material = append(material, label...)
	material = append(material, input...)
	return hkdfExtract(salt, material)
}

func labeledExpand(key []byte, suite []byte, label []byte, info []byte, length int) []byte {
	labeledInfo := make([]byte, 2)
	binary.BigEndian.PutUint16(labeledInfo, uint16(length))
	labeledInfo = append(labeledInfo, []byte("HPKE-v1")...)
	labeledInfo = append(labeledInfo, suite...)
	labeledInfo = append(labeledInfo, label...)
	labeledInfo = append(labeledInfo, info...)
	return hkdfExpand(key, labeledInfo, length)
}

func hkdfExtract(salt []byte, input []byte) []byte {
	if len(salt) == 0 {
		salt = make([]byte, sha256.Size)
	}
	hash := hmac.New(sha256.New, salt)
	hash.Write(input)
	return hash.Sum(nil)
}

func hkdfExpand(key []byte, info []byte, length int) []byte {
	result := make([]byte, 0, length)
	block := []byte(nil)
	for counter := byte(1); len(result) < length; counter++ {
		hash := hmac.New(sha256.New, key)
		hash.Write(block)
		hash.Write(info)
		hash.Write([]byte{counter})
		block = hash.Sum(nil)
		result = append(result, block...)
	}
	return result[:length]
}

func appendBytesField(output []byte, number int, value []byte) []byte {
	output = appendVarint(output, uint64(number<<3|2))
	output = appendVarint(output, uint64(len(value)))
	return append(output, value...)
}

func appendVarintField(output []byte, number int, value uint64) []byte {
	output = appendVarint(output, uint64(number<<3))
	return appendVarint(output, value)
}

func appendVarint(output []byte, value uint64) []byte {
	for value >= 0x80 {
		output = append(output, byte(value)|0x80)
		value >>= 7
	}
	return append(output, byte(value))
}

func parsePublicKeyBlob(blob []byte) (*ecdsa.PublicKey, []byte, error) {
	if len(blob) != 72 || binary.LittleEndian.Uint32(blob[4:8]) != 32 {
		return nil, nil, fmt.Errorf("NCrypt ECDSA 公钥格式异常")
	}
	x := new(big.Int).SetBytes(blob[8:40])
	y := new(big.Int).SetBytes(blob[40:72])
	if !elliptic.P256().IsOnCurve(x, y) {
		return nil, nil, fmt.Errorf("NCrypt ECDSA 公钥曲线异常")
	}
	publicKey := &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
	spki, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("编码 NCrypt ECDSA 公钥: %w", err)
	}
	return publicKey, spki, nil
}
