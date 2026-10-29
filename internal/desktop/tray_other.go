//go:build desktop && !windows

package desktop

import (
	_ "embed"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed tray.html
var trayPage string

// attachMenu 按系统双击间隔显示主窗口，右键打开自绘菜单
func (s *App) attachMenu(tray *application.SystemTray) {
	var clicked time.Time
	click := func() {
		application.InvokeAsync(func() {
			now := time.Now()
			if now.Sub(clicked) <= doubleClickInterval() {
				clicked = time.Time{}
				s.show()
			} else {
				clicked = now
			}
		})
	}
	popup := func(x, y int) {
		application.InvokeAsync(func() {
			clicked = time.Time{}
			s.popupTray(tray, x, y)
		})
	}
	s.bindTray(tray, click, popup)
}

// popupTray 在托盘旁创建菜单，文字、主题与勾选状态取自当前设置
func (s *App) popupTray(tray *application.SystemTray, x, y int) {
	s.mu.Lock()
	previous, locale, theme := s.menu, s.locale, s.theme
	s.mu.Unlock()
	if previous != nil {
		previous.Close()
	}
	enabled, err := s.autostartEnabled()
	if err != nil {
		slog.Warn("开机自启动设置读取失败", "error", err)
	}
	data, _ := json.Marshal(struct {
		Labels  [4]string `json:"labels"`
		Locale  string    `json:"locale"`
		Theme   string    `json:"theme"`
		Checked bool      `json:"checked"`
	}{s.labels(), locale, theme, enabled})
	window := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "tray", Title: "AIStudio2API", Width: 260, Height: 148,
		Frameless: true, DisableResize: true, AlwaysOnTop: true, Hidden: true,
		HTML: strings.Replace(trayPage, "__MENU__", string(data), 1),
	})
	s.mu.Lock()
	s.menu = window
	s.mu.Unlock()
	window.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) { window.Close() })
	window.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		s.mu.Lock()
		if s.menu == window {
			s.menu = nil
		}
		s.mu.Unlock()
	})
	window.Show()
	s.positionTrayMenu(tray, window, x, y)
	window.Focus()
}

// trayMessage 只在 Windows 上拦截托盘消息
func (s *App) trayMessage(uintptr, uint32, uintptr, uintptr) (uintptr, bool) {
	return 0, false
}
