//go:build desktop && !darwin

package desktop

// setDockIcon 在 macOS 以外的系统上没有 Dock 图标
func setDockIcon(bool) {}

// activateApp 在 macOS 以外的系统上由窗口聚焦完成
func activateApp() {}

// localizeMenus 在 macOS 以外的系统上没有原生应用菜单，托盘菜单在弹出时按界面语言取文字
func (s *App) localizeMenus(string) {}
