//go:build !windows

package camoufoxnative

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureBrowserProcess 将 Camoufox 隔离到独立进程组
func configureBrowserProcess(command *exec.Cmd, _ bool) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// browserTree 在非 Windows 平台由进程组表示，不需要额外句柄
type browserTree struct{}

// attachBrowserProcess 在非 Windows 平台由进程组负责回收
func attachBrowserProcess(*exec.Cmd) (browserTree, error) {
	return browserTree{}, nil
}

// releaseBrowserTree 在非 Windows 平台无需释放
func releaseBrowserTree(browserTree) {}

// terminateBrowserProcess 结束 Camoufox 进程组
func terminateBrowserProcess(ctx context.Context, process *browserProcess) error {
	command := process.command
	if command == nil || command.Process == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
