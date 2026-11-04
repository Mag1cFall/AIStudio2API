//go:build !windows

package camoufoxnative

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

// recordBrowserProcess 把 Camoufox 进程组号写入 profile，供服务异常退出后的下次启动回收
func recordBrowserProcess(profile string, pid int) error {
	return os.WriteFile(filepath.Join(profile, profilePIDName), []byte(strconv.Itoa(pid)), 0o600)
}

// terminateStaleBrowser 结束 profile 记录的遗留 Camoufox 进程组，进程已不存在或不属于该 profile 时不做任何事
func terminateStaleBrowser(profile string) error {
	data, err := os.ReadFile(filepath.Join(profile, profilePIDName))
	if err != nil {
		return nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		return nil
	}
	output, err := exec.Command("ps", "-ww", "-o", "pgid=,args=", "-p", strconv.Itoa(pid)).Output()
	if err != nil || !isProfileBrowser(string(output), pid, profile) {
		return nil
	}
	err = syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("结束遗留 Camoufox 进程组 %d: %w", pid, err)
	}
	return nil
}

// isProfileBrowser 判断 ps 输出的进程是否为以 profile 启动且自成进程组的 Camoufox
func isProfileBrowser(line string, pid int, profile string) bool {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != strconv.Itoa(pid) {
		return false
	}
	args := strings.TrimSpace(strings.TrimSpace(line)[len(fields[0]):])
	return strings.HasSuffix(args, " -profile "+profile)
}
