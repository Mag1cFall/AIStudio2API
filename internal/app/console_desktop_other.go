//go:build desktop && !windows

package app

// attachConsole 在 Windows 以外的系统上不需要处理，桌面版直接使用启动它的终端
func attachConsole() {}
