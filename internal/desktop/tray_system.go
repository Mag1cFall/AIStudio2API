//go:build desktop && !linux

package desktop

// startTray 创建托盘，Windows 与 macOS 总有系统托盘
func (s *App) startTray() {
	s.createTray()
}
