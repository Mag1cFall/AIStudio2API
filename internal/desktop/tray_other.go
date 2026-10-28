//go:build desktop && !windows

package desktop

import (
	"log/slog"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// attachMenu 设置托盘的系统菜单：显示窗口、在浏览器中打开、开机自启动、退出；macOS 单击与右键都打开菜单，Linux 单击显示窗口、右键由托盘宿主打开菜单
func (s *App) attachMenu(tray *application.SystemTray) {
	enabled, err := s.autostartEnabled()
	if err != nil {
		slog.Warn("开机自启动设置读取失败", "error", err)
	}
	menu := s.app.NewMenu()
	s.items[0] = menu.Add("").OnClick(func(*application.Context) { s.show() })
	s.items[1] = menu.Add("").OnClick(func(*application.Context) { s.openPage() })
	menu.AddSeparator()
	s.items[2] = menu.AddCheckbox("", enabled).OnClick(func(*application.Context) {
		s.toggleAutostart()
		enabled, _ := s.autostartEnabled()
		s.items[2].SetChecked(enabled)
	})
	menu.AddSeparator()
	s.items[3] = menu.Add("").OnClick(func(*application.Context) { s.app.Quit() })
	tray.SetMenu(menu)
	if runtime.GOOS == "linux" {
		tray.OnClick(s.show)
	}
}

// trayMessage 只在 Windows 上拦截托盘消息
func (s *App) trayMessage(uintptr, uint32, uintptr, uintptr) (uintptr, bool) {
	return 0, false
}
