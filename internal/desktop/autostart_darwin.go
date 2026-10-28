//go:build desktop

package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

// autostartEnabled 返回是否已注册为登录项
func (s *App) autostartEnabled() (bool, error) {
	return s.app.Autostart.IsEnabled()
}

// setAutostart 注册或移除登录项：macOS 13 以上的应用包使用 SMAppService，否则写入 LaunchAgent 并以 -open-ui=false 启动
func (s *App) setAutostart(enabled bool) error {
	if !enabled {
		return s.app.Autostart.Disable()
	}
	return s.app.Autostart.EnableWithOptions(application.AutostartOptions{Arguments: []string{"-open-ui=false"}})
}
