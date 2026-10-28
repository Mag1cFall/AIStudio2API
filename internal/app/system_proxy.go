package app

import (
	"net"
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
	return osProxy()
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

// scutilProxyURL 把 macOS scutil --proxy 输出中启用的代理转换为代理 URL，依次取 HTTPS、HTTP 与 SOCKS
func scutilProxyURL(output string) string {
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		if name, value, ok := strings.Cut(line, " : "); ok {
			values[strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
	}
	for _, item := range []struct{ name, scheme string }{{"HTTPS", "http"}, {"HTTP", "http"}, {"SOCKS", "socks5"}} {
		host, port := values[item.name+"Proxy"], values[item.name+"Port"]
		if values[item.name+"Enable"] == "1" && host != "" && port != "" {
			return item.scheme + "://" + net.JoinHostPort(host, port)
		}
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
