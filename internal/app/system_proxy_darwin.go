package app

import "os/exec"

// osProxy 返回 scutil --proxy 列出的 macOS 系统代理
func osProxy() string {
	output, err := exec.Command("scutil", "--proxy").Output()
	if err != nil {
		return ""
	}
	return scutilProxyURL(string(output))
}
