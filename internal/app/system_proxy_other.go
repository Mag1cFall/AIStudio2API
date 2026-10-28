//go:build !windows && !darwin

package app

// osProxy 在 Windows 与 macOS 以外的系统上由环境变量提供系统代理
func osProxy() string {
	return ""
}
