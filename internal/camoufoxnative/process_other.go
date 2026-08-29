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

// terminateBrowserProcess 结束 Camoufox 进程组
func terminateBrowserProcess(ctx context.Context, command *exec.Cmd) error {
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
