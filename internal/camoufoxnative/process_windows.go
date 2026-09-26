//go:build windows

package camoufoxnative

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const windowsStillActive = 259

var (
	browserJobOnce   sync.Once
	browserJob       windows.Handle
	browserJobErr    error
	ntResumeProcess  = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")
	browserJobAccess = uint32(windows.PROCESS_SET_QUOTA | windows.PROCESS_TERMINATE | windows.PROCESS_SUSPEND_RESUME)
)

// configureBrowserProcess 将 Camoufox 隔离到独立 Windows 进程组并挂起启动
func configureBrowserProcess(command *exec.Cmd, headless bool) {
	attributes := &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_SUSPENDED}
	if headless {
		attributes.CreationFlags |= windows.CREATE_NO_WINDOW
		attributes.HideWindow = true
	}
	command.SysProcAttr = attributes
}

// serviceBrowserJob 返回服务进程退出时终止全部 Camoufox 的 Job
func serviceBrowserJob() (windows.Handle, error) {
	browserJobOnce.Do(func() {
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			browserJobErr = fmt.Errorf("创建 Camoufox Job: %w", err)
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(
			job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)),
		); err != nil {
			windows.CloseHandle(job)
			browserJobErr = fmt.Errorf("配置 Camoufox Job: %w", err)
			return
		}
		browserJob = job
	})
	return browserJob, browserJobErr
}

// attachBrowserProcess 把挂起的 Camoufox 加入服务 Job 后恢复执行
func attachBrowserProcess(command *exec.Cmd) error {
	job, err := serviceBrowserJob()
	if err != nil {
		return err
	}
	process, err := windows.OpenProcess(browserJobAccess, false, uint32(command.Process.Pid))
	if err != nil {
		return fmt.Errorf("打开 Camoufox 进程: %w", err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		return fmt.Errorf("加入 Camoufox Job: %w", err)
	}
	if status, _, _ := ntResumeProcess.Call(uintptr(process)); status != 0 {
		return fmt.Errorf("恢复 Camoufox 进程: NTSTATUS 0x%x", status)
	}
	return nil
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
