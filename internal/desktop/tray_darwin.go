//go:build desktop

package desktop

// #cgo CFLAGS: -x objective-c
// #include <Cocoa/Cocoa.h>
// static double trayDoubleClickTime(void) { return [NSEvent doubleClickInterval]; }
import "C"

import (
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// doubleClickInterval 返回 macOS 的双击间隔
func doubleClickInterval() time.Duration {
	return time.Duration(float64(C.trayDoubleClickTime()) * float64(time.Second))
}

// bindTray 以菜单栏图标的鼠标事件驱动双击与自绘菜单
func (s *App) bindTray(tray *application.SystemTray, click func(), popup func(int, int)) {
	tray.OnClick(click)
	tray.OnRightClick(func() { popup(0, 0) })
}

// positionTrayMenu 将菜单定位到菜单栏图标下方
func (s *App) positionTrayMenu(tray *application.SystemTray, window *application.WebviewWindow, _, _ int) {
	_ = tray.PositionWindow(window, 4)
}
