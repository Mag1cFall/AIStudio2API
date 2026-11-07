//go:build desktop

package desktop

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/godbus/dbus/v5"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// statusNotifierWatcher 为托盘宿主在会话总线上的名称
const statusNotifierWatcher = "org.kde.StatusNotifierWatcher"

// startTray 在会话总线上有托盘宿主时创建托盘；没有时（如未装托盘扩展的 GNOME）关闭窗口即退出，托盘宿主出现后再创建托盘
func (s *App) startTray() {
	conn, err := dbus.SessionBus()
	if err == nil {
		err = conn.AddMatchSignal(
			dbus.WithMatchSender("org.freedesktop.DBus"),
			dbus.WithMatchMember("NameOwnerChanged"),
			dbus.WithMatchArg(0, statusNotifierWatcher),
		)
	}
	var owned bool
	if err == nil {
		err = conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, statusNotifierWatcher).Store(&owned)
	}
	if err != nil {
		slog.Error("托盘连接会话总线失败", "error", err)
		return
	}
	if owned {
		s.createTray()
		return
	}
	slog.Info("桌面环境没有系统托盘，关闭窗口即退出")
	signals := make(chan *dbus.Signal, 10)
	conn.Signal(signals)
	go func() {
		defer conn.RemoveSignal(signals)
		for signal := range signals {
			if signal.Name == "org.freedesktop.DBus.NameOwnerChanged" && len(signal.Body) == 3 && signal.Body[0] == statusNotifierWatcher && signal.Body[2] != "" {
				application.InvokeSync(s.createTray)
				return
			}
		}
	}()
}

// bindTray 在 Wails 注册的托盘项上接收 StatusNotifierItem 的点击与上下文菜单事件
func (s *App) bindTray(tray *application.SystemTray, click func(), popup func(int, int)) {
	tray.OnClick(click)
	conn, err := dbus.SessionBus()
	if err != nil {
		slog.Error("托盘连接会话总线失败", "error", err)
		return
	}
	signals := make(chan *dbus.Signal, 10)
	conn.Signal(signals)
	go func() {
		defer conn.RemoveSignal(signals)
		name := fmt.Sprintf("org.kde.StatusNotifierItem-%d-1", os.Getpid())
		for signal := range signals {
			if signal.Name != "org.freedesktop.DBus.NameAcquired" || signal.Body[0] != name {
				continue
			}
			err := conn.ExportMethodTable(map[string]interface{}{
				"Activate":          func(int32, int32) *dbus.Error { click(); return nil },
				"ContextMenu":       func(x, y int32) *dbus.Error { popup(int(x), int(y)); return nil },
				"SecondaryActivate": func(int32, int32) *dbus.Error { return nil },
				"Scroll":            func(int32, string) *dbus.Error { return nil },
			}, "/StatusNotifierItem", "org.kde.StatusNotifierItem")
			if err != nil {
				slog.Error("托盘事件接口注册失败", "error", err)
			}
			return
		}
	}()
}

// positionTrayMenu 将菜单放在点击坐标旁，并限制在显示器的工作区内
func (s *App) positionTrayMenu(_ *application.SystemTray, window *application.WebviewWindow, x, y int) {
	for _, screen := range s.app.Screen.GetAll() {
		area := screen.WorkArea
		if x >= screen.X && x < screen.X+screen.Size.Width && y >= screen.Y && y < screen.Y+screen.Size.Height {
			left, top := x, y+4
			if top+window.Height() > area.Y+area.Height {
				top = y - window.Height() - 4
			}
			window.SetPosition(max(area.X, min(left, area.X+area.Width-window.Width())), max(area.Y, top))
			return
		}
	}
}
