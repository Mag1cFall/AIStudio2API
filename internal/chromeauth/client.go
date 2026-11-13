package chromeauth

import (
	"encoding/hex"
	"fmt"
	"math/rand/v2"

	"github.com/bogdanfinn/fhttp/http2"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	tls "github.com/bogdanfinn/utls"
)

// chromePriority 是 Chrome 网络栈的请求优先级在 HTTP/2 HEADERS 帧中的权重与 priority 请求头
type chromePriority struct {
	weight uint8
	header string
}

var (
	// chromeIdle 是 GaiaAuthFetcher 与 DBSC 主动刷新使用的 IDLE 优先级
	chromeIdle = chromePriority{weight: 109, header: "u=4, i"}
	// chromeHighest 是请求因缺少绑定 Cookie 被挂起时 DBSC 刷新使用的 HIGHEST 优先级
	chromeHighest = chromePriority{weight: 255, header: "u=0, i"}
)

// chromeTrustAnchors 是本机 Chrome 157 trust_anchors 扩展的内容，即 Chrome 根证书库的信任锚 ID 列表
var chromeTrustAnchors, _ = hex.DecodeString("00b80582df1302010582df1302060582df13020d0582df13020e0582df13020f0582df1302120582df1302130582df13021408839a648c9b2d010708839a648c9b2d010808839a648c9b2d010908839a648c9b2d010a08839a648c9b2d010b08839a648c9b2d010c08839a648c9b2d010d08839a648c9b2d011208839a648c9b2d011304d679090104d679090404d679090504d679090604d679090704d679090804d679090a04d679090b04d679090c04d679090d04d679090f")

// chrome157Profile 返回以 tls-client 的 Chrome 146 档案为底、签名算法与 trust_anchors 按本机 Chrome 157、HEADERS 帧权重按 priority 的客户端档案
func chrome157Profile(priority chromePriority) profiles.ClientProfile {
	base := profiles.Chrome_146
	hello := base.GetClientHelloId()
	hello.Version = "157"
	hello.SpecFactory = func() (tls.ClientHelloSpec, error) {
		spec, err := base.GetClientHelloSpec()
		if err != nil {
			return tls.ClientHelloSpec{}, err
		}
		for _, extension := range spec.Extensions {
			switch typed := extension.(type) {
			case *tls.SignatureAlgorithmsExtension:
				grease := tls.SignatureScheme(0x0a0a + 0x1010*rand.N(16))
				typed.SupportedSignatureAlgorithms = append([]tls.SignatureScheme{grease, 0x0904, 0x0905, 0x0906}, typed.SupportedSignatureAlgorithms...)
			case *tls.GenericExtension:
				if typed.Id == 0xca34 {
					typed.Data = chromeTrustAnchors
				}
			}
		}
		return spec, nil
	}
	return profiles.NewClientProfile(hello, base.GetSettings(), base.GetSettingsOrder(), base.GetPseudoHeaderOrder(), base.GetConnectionFlow(),
		base.GetPriorities(), &http2.PriorityParam{Exclusive: true, Weight: priority.weight}, base.GetStreamID(), base.GetAllowHTTP(),
		base.GetHttp3Settings(), base.GetHttp3SettingsOrder(), base.GetHttp3PriorityParam(), base.GetHttp3PseudoHeaderOrder(), base.GetHttp3SendGreaseFrames())
}

// newOAuthClient 返回按本机 Chrome 157 的 TLS 与 HTTP/2 形状、以 priority 发出请求的客户端，扩展顺序与 Chrome 一样每条连接随机排列
func newOAuthClient(proxyURL string, priority chromePriority) (tls_client.HttpClient, error) {
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(chrome157Profile(priority)),
		tls_client.WithRandomTLSExtensionOrder(),
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
