//go:build windows

package camoufoxnative

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

const windowsStillActive = 259

// configureBrowserProcess 将 Camoufox 隔离到独立 Windows 进程组
func configureBrowserProcess(command *exec.Cmd, headless bool) {
	attributes := &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	if headless {
		attributes.CreationFlags |= windows.CREATE_NO_WINDOW
		attributes.HideWindow = true
	}
	command.SysProcAttr = attributes
}

// terminateBrowserProcess 结束 Camoufox 及其全部子进程
func terminateBrowserProcess(ctx context.Context, command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	pid := command.Process.Pid
	if !browserProcessActive(pid) {
		return nil
	}
	kill := exec.CommandContext(ctx, "taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if output, err := kill.CombinedOutput(); err != nil {
		if !browserProcessActive(pid) {
			return nil
		}
		taskkillErr := fmt.Errorf("taskkill 结束 Camoufox 进程树 PID=%d: %w: %s", pid, err, output)
		directKillErr := command.Process.Kill()
		if directKillErr == nil || errors.Is(directKillErr, os.ErrProcessDone) || !browserProcessActive(pid) {
			return nil
		}
		terminateErr := errors.Join(taskkillErr, fmt.Errorf("Process.Kill 结束 Camoufox PID=%d: %w", pid, directKillErr))
		if ctxErr := ctx.Err(); ctxErr != nil {
			return errors.Join(terminateErr, ctxErr)
		}
		return terminateErr
	}
	return nil
}

func browserProcessActive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(handle)
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return true
	}
	return exitCode == windowsStillActive
}
