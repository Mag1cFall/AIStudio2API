//go:build desktop && linux

package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
)

// platformUpdateMode 返回 Linux 桌面版的更新方式：AppImage 与 deb 都原地更新
func platformUpdateMode() string {
	return "binary"
}

// install 下载并校验当前架构的新版本：AppImage 运行时替换 APPIMAGE 指向的文件，deb 安装时由 pkexec 取得管理员权限后以 apt-get 安装
func (u *selfUpdater) install(ctx context.Context, release *githubRelease) error {
	image := os.Getenv("APPIMAGE")
	format := "deb"
	if image != "" {
		format = "AppImage"
	}
	payload, err := u.download(ctx, release, fmt.Sprintf("aistudio2api-%s-linux-%s.%s", release.TagName, runtime.GOARCH, format))
	if err != nil {
		return err
	}
	if image != "" {
		u.log("INFO", "在线更新 | 2/2 | 替换 AppImage | 版本="+release.TagName)
		return replaceAppImage(image, payload)
	}
	u.log("INFO", "在线更新 | 2/2 | 安装 deb | 版本="+release.TagName)
	return installDeb(payload)
}

// replaceAppImage 把新版本写到 image 所在目录的临时文件，沿用原文件权限后改名覆盖 image
func replaceAppImage(image string, payload []byte) error {
	info, err := os.Stat(image)
	if err != nil {
		return fmt.Errorf("替换 AppImage: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(image), ".aistudio2api-*.AppImage")
	if err != nil {
		return fmt.Errorf("替换 AppImage: %w", err)
	}
	defer os.Remove(file.Name())
	_, err = file.Write(payload)
	if err == nil {
		err = file.Chmod(info.Mode().Perm())
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), image)
	}
	if err != nil {
		return fmt.Errorf("替换 AppImage: %w", err)
	}
	return nil
}

// installDeb 把 deb 写入临时文件，由 pkexec 经桌面认证对话框取得管理员权限后以 apt-get 安装
func installDeb(payload []byte) error {
	file, err := os.CreateTemp("", "aistudio2api-*.deb")
	if err != nil {
		return fmt.Errorf("安装 deb: %w", err)
	}
	defer os.Remove(file.Name())
	_, err = file.Write(payload)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("安装 deb: %w", err)
	}
	if output, err := exec.Command("pkexec", "apt-get", "install", "-y", file.Name()).CombinedOutput(); err != nil {
		return fmt.Errorf("安装 deb: %w：%s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// updateRestart 返回在线更新完成后的重启动作：启动等待当前进程退出后运行新版本的进程，再结束服务与桌面应用
func updateRestart(stop func(), _ *atomic.Bool, log func(level, message string)) func() {
	return func() {
		if err := startSuccessor(); err != nil {
			log("ERROR", "在线更新重启失败 | 错误="+err.Error())
			return
		}
		stop()
	}
}

// startSuccessor 在新会话中启动 sh，等当前进程退出后运行 AppImage 文件或 deb 安装的可执行文件；继承的文件描述符先标记为执行时关闭，AppImage 运行时保持挂载的管道不传给新版本，旧版本的挂载随当前进程退出卸载
func startSuccessor() error {
	target := os.Getenv("APPIMAGE")
	if target == "" {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		target = executable
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if fd, err := strconv.Atoi(entry.Name()); err == nil && fd > 2 {
			syscall.CloseOnExec(fd)
		}
	}
	command := exec.Command("/bin/sh", "-c", `while kill -0 "$1" 2>/dev/null; do sleep 0.2; done; exec "$2"`, "sh", strconv.Itoa(os.Getpid()), target)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
