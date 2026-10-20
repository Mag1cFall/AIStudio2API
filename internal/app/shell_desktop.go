//go:build desktop

package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Mag1cFall/AIStudio2API/internal/desktop"
)

// prepareShell 让更新器辅助进程完成替换，切换到桌面版数据目录，setup 时使用启动它的控制台
func prepareShell(args []string) error {
	desktop.HandleUpdateHelper()
	directory, err := desktopDataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	if err := os.Chdir(directory); err != nil {
		return err
	}
	if len(args) != 0 && args[0] == "setup" {
		attachConsole()
	}
	return nil
}

// desktopDataDir 返回桌面版数据目录：Windows 为可执行文件所在目录，其他系统为用户配置目录下的 AIStudio2API
func desktopDataDir() (string, error) {
	if runtime.GOOS == "windows" {
		executable, err := os.Executable()
		if err != nil {
			return "", err
		}
		return filepath.Dir(executable), nil
	}
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "AIStudio2API"), nil
}

// uiShell 为桌面版的管理界面宿主：窗口与托盘
type uiShell struct {
	app *desktop.App
}

// newShell 创建桌面应用，open 为 false 时窗口保持隐藏、只显示托盘
func newShell(open bool) *uiShell {
	webview, _ := filepath.Abs(filepath.Join("runtime", "webview"))
	return &uiShell{app: desktop.New(open, webview)}
}

// run 运行窗口与托盘：服务就绪前显示启动画面，就绪后显示管理页面，启动失败时显示错误对话框后退出
func (s *uiShell) run(ctx context.Context, start func() (serviceReady, error)) error {
	return s.app.Run(ctx, func() (string, error) {
		service, err := start()
		return service.url, err
	})
}
