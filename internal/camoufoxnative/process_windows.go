//go:build windows

package camoufoxnative

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// configureBrowserProcess 将 Camoufox 隔离到独立 Windows 进程组
func configureBrowserProcess(command *exec.Cmd, headless bool) {
	attributes := &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	if headless {
		attributes.CreationFlags |= windows.CREATE_NO_WINDOW
		attributes.HideWindow = true
	}
	command.SysProcAttr = attributes
}
