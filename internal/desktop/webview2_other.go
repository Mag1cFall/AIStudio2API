//go:build desktop && !windows

package desktop

// installWebView2 在 Windows 以外的系统上不安装任何运行时
func installWebView2() bool {
	return false
}
