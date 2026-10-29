//go:build desktop

package desktop

// #cgo pkg-config: gtk4
// #include <gtk/gtk.h>
// static int trayDoubleClickTime(void) { int value; g_object_get(gtk_settings_get_default(), "gtk-double-click-time", &value, NULL); return value; }
import "C"

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// doubleClickInterval 返回 GTK 的双击间隔
func doubleClickInterval() time.Duration {
	return time.Duration(C.trayDoubleClickTime()) * time.Millisecond
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
