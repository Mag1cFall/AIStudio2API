//go:build !windows

package camoufoxnative

import "os/exec"

// configureBrowserProcess 使用当前平台的默认进程属性
func configureBrowserProcess(command *exec.Cmd, _ bool) {}
