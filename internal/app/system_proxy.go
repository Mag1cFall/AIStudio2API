package app

import (
	"net/url"
	"strings"

	"golang.org/x/net/http/httpproxy"
)

// systemProxyTarget 是判断系统代理是否作用于上游时使用的地址
var systemProxyTarget = &url.URL{Scheme: "https", Host: "aistudio.google.com"}

// systemProxy 返回环境变量或操作系统设置中作用于上游的代理 URL，没有设置时返回空
func systemProxy() string {
	if proxy, err := httpproxy.FromEnvironment().ProxyFunc()(systemProxyTarget); err == nil && proxy != nil {
		return proxy.String()
	}
	return windowsProxyURL(osProxyServer())
}

// windowsProxyURL 把 Windows ProxyServer 的 host:port 或按协议分列的写法转换为代理 URL，依次取 https、http 与 socks
func windowsProxyURL(server string) string {
	server = strings.TrimSpace(server)
	if server == "" {
		return ""
	}
	if !strings.Contains(server, "=") {
		if strings.Contains(server, "://") {
			return server
		}
		return "http://" + server
	}
	entries := make(map[string]string)
	for _, item := range strings.Split(server, ";") {
		if name, address, ok := strings.Cut(strings.TrimSpace(item), "="); ok {
			entries[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(address)
		}
	}
	for _, name := range []string{"https", "http"} {
		if address := entries[name]; address != "" {
			return "http://" + address
		}
	}
	if address := entries["socks"]; address != "" {
		return "socks5://" + address
	}
	return ""
}

// redactedProxy 返回隐去密码的代理 URL
func redactedProxy(proxy string) string {
	parsed, err := url.Parse(proxy)
	if err != nil {
		return proxy
	}
	return parsed.Redacted()
}
