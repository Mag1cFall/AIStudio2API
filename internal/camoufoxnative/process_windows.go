//go:build windows

package camoufoxnative

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

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

// browserTree 是只含一个 Camoufox 及其全部子孙进程的嵌套 Job
type browserTree windows.Handle

// jobAccountingInformation 是 JOBOBJECT_BASIC_ACCOUNTING_INFORMATION
type jobAccountingInformation struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

// attachBrowserProcess 把挂起的 Camoufox 加入服务 Job 与它自己的嵌套 Job 后恢复执行，返回该嵌套 Job
func attachBrowserProcess(command *exec.Cmd) (browserTree, error) {
	job, err := serviceBrowserJob()
	if err != nil {
		return 0, err
	}
	process, err := windows.OpenProcess(browserJobAccess, false, uint32(command.Process.Pid))
	if err != nil {
		return 0, fmt.Errorf("打开 Camoufox 进程: %w", err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		return 0, fmt.Errorf("加入 Camoufox Job: %w", err)
	}
	tree, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("创建 Camoufox 进程树 Job: %w", err)
	}
	if err := windows.AssignProcessToJobObject(tree, process); err != nil {
		windows.CloseHandle(tree)
		return 0, fmt.Errorf("加入 Camoufox 进程树 Job: %w", err)
	}
	if status, _, _ := ntResumeProcess.Call(uintptr(process)); status != 0 {
		windows.CloseHandle(tree)
		return 0, fmt.Errorf("恢复 Camoufox 进程: NTSTATUS 0x%x", status)
	}
	return browserTree(tree), nil
}

// terminateBrowserProcess 结束 Camoufox 嵌套 Job 中的全部进程并等待它们退出
func terminateBrowserProcess(ctx context.Context, process *browserProcess) error {
	tree := windows.Handle(process.tree)
	if err := windows.TerminateJobObject(tree, 1); err != nil {
		return fmt.Errorf("结束 Camoufox 进程树: %w", err)
	}
	for {
		var info jobAccountingInformation
		if err := windows.QueryInformationJobObject(
			tree, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil,
		); err != nil {
			return fmt.Errorf("查询 Camoufox 进程树: %w", err)
		}
		if info.ActiveProcesses == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待 Camoufox 进程树退出: %d 个进程仍在运行: %w", info.ActiveProcesses, ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// releaseBrowserTree 关闭嵌套 Job 句柄
func releaseBrowserTree(tree browserTree) {
	windows.CloseHandle(windows.Handle(tree))
}
