//go:build !windows

package app

// osProxyServer 在 Windows 以外的系统上由环境变量提供系统代理
func osProxyServer() string {
	return ""
}
