//go:build desktop

package desktop

// #cgo CFLAGS: -x objective-c
// #include <Cocoa/Cocoa.h>
// static void dockSetVisible(int visible) {
//   [NSApp setActivationPolicy:visible ? NSApplicationActivationPolicyRegular : NSApplicationActivationPolicyAccessory];
//   [NSApp activateIgnoringOtherApps:YES];
// }
// static void dockActivate(void) { [NSApp activateIgnoringOtherApps:YES]; }
import "C"

import "github.com/wailsapp/wails/v3/pkg/application"

// setDockIcon 在主线程切换 Dock 图标的显示，隐藏后应用只保留菜单栏图标，当前窗口留在前台
func setDockIcon(visible bool) {
	application.InvokeSync(func() {
		flag := C.int(0)
		if visible {
			flag = 1
		}
		C.dockSetVisible(flag)
	})
}

// activateApp 把应用带到前台，不显示 Dock 图标时窗口也能获得焦点
func activateApp() {
	application.InvokeSync(func() { C.dockActivate() })
}
