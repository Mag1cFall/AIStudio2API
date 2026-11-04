//go:build !windows

package camoufoxnative

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// foreignUserPath 表示本进程以 root 运行而 path 属于其他用户，Firefox 的会话变量指向这种路径时拒绝启动
func foreignUserPath(path string) bool {
	var info syscall.Stat_t
	return os.Geteuid() == 0 && syscall.Stat(path, &info) == nil && info.Uid != 0
}

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
func terminateBrowserProcess(_ context.Context, process *browserProcess) error {
	err := syscall.Kill(-process.command.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
