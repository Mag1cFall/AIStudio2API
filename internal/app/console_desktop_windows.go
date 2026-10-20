//go:build desktop

package app

import (
	"os"

	"golang.org/x/sys/windows"
)

// kernel32 为控制台函数所在的系统库
var kernel32 = windows.NewLazySystemDLL("kernel32.dll")

// attachConsoleProc 为 kernel32 的 AttachConsole
var attachConsoleProc = kernel32.NewProc("AttachConsole")

// allocConsoleProc 为 kernel32 的 AllocConsole
var allocConsoleProc = kernel32.NewProc("AllocConsole")

// attachParentProcess 为 AttachConsole 附加到父进程控制台的参数
const attachParentProcess = ^uintptr(0)

// attachConsole 让 GUI 子系统的桌面版附加到启动它的控制台，没有控制台时新建一个，并把标准输入输出指向该控制台
func attachConsole() {
	if attached, _, _ := attachConsoleProc.Call(attachParentProcess); attached == 0 {
		if allocated, _, _ := allocConsoleProc.Call(); allocated == 0 {
			return
		}
	}
	if input, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err == nil {
		os.Stdin = input
	}
	if output, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		os.Stdout, os.Stderr = output, output
	}
}
