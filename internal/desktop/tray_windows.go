//go:build desktop

package desktop

import (
	"log/slog"
	"sync"
	"syscall"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
)

// menuClass 为自绘托盘菜单的窗口类名
const menuClass = "AIStudio2APITrayMenu"

// dwmwaWindowCornerPreference 为 DWM 的窗口圆角属性，dwmwcpRoundSmall 为 Windows 11 菜单使用的小圆角
const (
	dwmwaWindowCornerPreference w32.DWMWINDOWATTRIBUTE = 33
	dwmwcpRoundSmall            uint32                 = 3
)

var (
	// createCompatibleBitmap 为 gdi32 的 CreateCompatibleBitmap，用于双缓冲绘制
	createCompatibleBitmap = windows.NewLazySystemDLL("gdi32.dll").NewProc("CreateCompatibleBitmap")
	// notifyIconGetRect 为 shell32 的 Shell_NotifyIconGetRect，返回托盘图标的屏幕位置
	notifyIconGetRect = windows.NewLazySystemDLL("shell32.dll").NewProc("Shell_NotifyIconGetRect")
)

// menuPalette 为自绘菜单一种主题的颜色，与管理端下拉菜单相同
type menuPalette struct {
	background, border, text, hover, hoverText, check w32.COLORREF
}

var (
	// darkPalette 为深色主题的菜单颜色
	darkPalette = menuPalette{w32.RGB(0x16, 0x1b, 0x22), w32.RGB(0x30, 0x36, 0x3d), w32.RGB(0xd1, 0xd9, 0xe0), w32.RGB(0x21, 0x26, 0x2d), w32.RGB(0xff, 0xff, 0xff), w32.RGB(0x60, 0xa5, 0xfa)}
	// lightPalette 为浅色主题的菜单颜色
	lightPalette = menuPalette{w32.RGB(0xff, 0xff, 0xff), w32.RGB(0xd0, 0xd7, 0xde), w32.RGB(0x33, 0x3b, 0x45), w32.RGB(0xee, 0xf1, 0xf5), w32.RGB(0x1f, 0x23, 0x28), w32.RGB(0x25, 0x63, 0xeb)}
)

// menuFonts 为各界面语言的菜单字体，与管理端各语言的中日韩字体相同，其他语言为 Segoe UI
var menuFonts = map[string]string{"zh-CN": "Microsoft YaHei UI", "zh-TW": "Microsoft JhengHei UI", "ja": "Yu Gothic UI", "ko": "Malgun Gothic"}

// menuEntry 为自绘菜单的一项，label 为空时为分隔线，checked 为 true 时右侧显示勾选
type menuEntry struct {
	label   string
	checked bool
	run     func()
}

// trayMenu 为弹出中的自绘托盘菜单，只在主线程上访问；tops 为各项在客户区的上边缘，最后一个元素为最后一项的下边缘
type trayMenu struct {
	hwnd     w32.HWND
	entries  []menuEntry
	tops     []int32
	hot      int
	tracking bool
	dpi      int
	colors   menuPalette
	font     w32.HFONT
	glyph    w32.HFONT
}

var (
	// activeMenu 为当前弹出的自绘菜单，没有时为 nil
	activeMenu *trayMenu
	// registerMenu 保证自绘菜单的窗口类只注册一次
	registerMenu sync.Once
)

// attachMenu 设置托盘图标：单击不做任何事，双击显示窗口，右键或键盘打开托盘图标的上下文菜单时由 trayMessage 弹出自绘菜单
func (s *App) attachMenu(tray *application.SystemTray) {
	tray.OnDoubleClick(s.show)
}

// trayMessage 拦截托盘图标的 WM_CONTEXTMENU，在托盘图标处弹出自绘菜单；NOTIFYICON_VERSION_4 下 lParam 高字为图标编号，wParam 为右键或键盘打开菜单时的屏幕坐标
func (s *App) trayMessage(hwnd uintptr, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	if msg != w32.WM_USER+1 || lParam&0xffff != w32.WM_CONTEXTMENU {
		return 0, false
	}
	x, y := int32(int16(wParam)), int32(int16(wParam>>16))
	anchor := w32.RECT{Left: x, Top: y, Right: x, Bottom: y}
	identifier := struct {
		Size   uint32
		Window uintptr
		ID     uint32
		GUID   windows.GUID
	}{Window: hwnd, ID: uint32(lParam >> 16)}
	identifier.Size = uint32(unsafe.Sizeof(identifier))
	var icon w32.RECT
	if result, _, _ := notifyIconGetRect.Call(uintptr(unsafe.Pointer(&identifier)), uintptr(unsafe.Pointer(&icon))); result == 0 {
		anchor = icon
	}
	s.popup(anchor)
	return 0, true
}

// menuEntries 返回自绘菜单的各项：显示窗口（没有 WebView2 时省略）、在浏览器中打开、开机自启动、退出，开机自启动的勾选取自系统设置
func (s *App) menuEntries() []menuEntry {
	labels := s.labels()
	enabled, err := s.autostartEnabled()
	if err != nil {
		slog.Warn("开机自启动设置读取失败", "error", err)
	}
	var entries []menuEntry
	if s.webview {
		entries = append(entries, menuEntry{label: labels[0], run: s.show})
	}
	return append(entries,
		menuEntry{label: labels[1], run: s.openPage},
		menuEntry{},
		menuEntry{label: labels[2], checked: enabled, run: s.toggleAutostart},
		menuEntry{},
		menuEntry{label: labels[3], run: s.app.Quit},
	)
}

// popup 在托盘图标 anchor 旁弹出自绘菜单并让它获得焦点，已有弹出的菜单时先关闭它
func (s *App) popup(anchor w32.RECT) {
	if activeMenu != nil {
		w32.DestroyWindow(activeMenu.hwnd)
	}
	registerMenu.Do(func() {
		class := w32.WNDCLASSEX{
			Style:     w32.CS_DROPSHADOW,
			WndProc:   syscall.NewCallback(menuProc),
			Instance:  w32.GetModuleHandle(""),
			Cursor:    w32.LoadCursorWithResourceID(0, w32.IDC_ARROW),
			ClassName: w32.MustStringToUTF16Ptr(menuClass),
		}
		class.Size = uint32(unsafe.Sizeof(class))
		w32.RegisterClassEx(&class)
	})
	monitor := w32.MonitorFromRect(&anchor, w32.MONITOR_DEFAULTTONEAREST)
	var dpiX, dpiY w32.UINT
	w32.GetDPIForMonitor(monitor, w32.MDT_EFFECTIVE_DPI, &dpiX, &dpiY)
	s.mu.Lock()
	face, theme := menuFonts[s.locale], s.theme
	s.mu.Unlock()
	if face == "" {
		face = "Segoe UI"
	}
	menu := &trayMenu{entries: s.menuEntries(), hot: -1, dpi: int(dpiX), colors: lightPalette}
	if theme == "dark" || theme == "" && w32.IsCurrentlyDarkMode() {
		menu.colors = darkPalette
	}
	menu.font = createFont(face, menu.px(13))
	menu.glyph = createFont("Segoe MDL2 Assets", menu.px(12))
	width, height := menu.layout()
	info := w32.MONITORINFO{}
	info.CbSize = uint32(unsafe.Sizeof(info))
	w32.GetMonitorInfo(monitor, &info)
	left, top := placeMenu(anchor, width, height, info.RcWork)
	activeMenu = menu
	menu.hwnd = w32.CreateWindowEx(w32.WS_EX_TOOLWINDOW|w32.WS_EX_TOPMOST, w32.MustStringToUTF16Ptr(menuClass), w32.MustStringToUTF16Ptr("AIStudio2API"), w32.WS_POPUP, left, top, width, height, 0, 0, w32.GetModuleHandle(""), nil)
	corner, border := dwmwcpRoundSmall, menu.colors.border
	w32.DwmSetWindowAttribute(menu.hwnd, dwmwaWindowCornerPreference, unsafe.Pointer(&corner), unsafe.Sizeof(corner))
	w32.DwmSetWindowAttribute(menu.hwnd, w32.DwmwaBorderColor, unsafe.Pointer(&border), unsafe.Sizeof(border))
	w32.ShowWindow(menu.hwnd, w32.SW_SHOW)
	w32.SetForegroundWindow(menu.hwnd)
}

// placeMenu 返回菜单左上角：左边对齐 anchor、底边贴着 anchor 上方，右侧放不下时右边对齐 anchor，上方放不下时贴着 anchor 下方，再限制在工作区内
func placeMenu(anchor w32.RECT, width, height int, work w32.RECT) (int, int) {
	left, top := int(anchor.Left), int(anchor.Top)-height
	if left+width > int(work.Right) {
		left = int(anchor.Right) - width
	}
	if top < int(work.Top) {
		top = int(anchor.Bottom)
	}
	return max(int(work.Left), min(left, int(work.Right)-width)), max(int(work.Top), min(top, int(work.Bottom)-height))
}

// createFont 创建字号为 size 像素的 ClearType 字体
func createFont(face string, size int) w32.HFONT {
	font := w32.LOGFONT{Height: -int32(size), Weight: w32.FW_NORMAL, CharSet: w32.DEFAULT_CHARSET, Quality: w32.CLEARTYPE_QUALITY}
	copy(font.FaceName[:w32.LF_FACESIZE-1], windows.StringToUTF16(face))
	return w32.CreateFontIndirect(&font)
}

// px 把 96 DPI 下的像素换算为菜单所在显示器的像素
func (m *trayMenu) px(value int) int {
	return w32.MulDiv(value, m.dpi, 96)
}

// layout 计算各项位置并返回菜单大小，尺寸与管理端下拉菜单相同
func (m *trayMenu) layout() (int, int) {
	dc := w32.GetDC(0)
	defer w32.ReleaseDC(0, dc)
	previous := w32.SelectObject(dc, m.font)
	defer w32.SelectObject(dc, previous)
	widest, y := 0, 1+m.px(4)
	m.tops = m.tops[:0]
	for _, entry := range m.entries {
		m.tops = append(m.tops, int32(y))
		if entry.label == "" {
			y += m.px(9)
			continue
		}
		text := windows.StringToUTF16(entry.label)
		var size w32.SIZE
		w32.GetTextExtentPoint32(dc, &text[0], len(text)-1, &size)
		widest = max(widest, int(size.CX))
		y += m.px(30)
	}
	m.tops = append(m.tops, int32(y))
	return max(widest+m.px(48), m.px(160)) + 2, y + m.px(4) + 1
}

// paint 在 dc 上绘制菜单：边框、背景、高亮项、文字、分隔线与勾选
func (m *trayMenu) paint(dc w32.HDC, width, height int32) {
	fill(dc, w32.RECT{Right: width, Bottom: height}, m.colors.border)
	fill(dc, w32.RECT{Left: 1, Top: 1, Right: width - 1, Bottom: height - 1}, m.colors.background)
	w32.SetBkMode(dc, w32.TRANSPARENT)
	padding := int32(m.px(12))
	for index, entry := range m.entries {
		row := w32.RECT{Left: 1, Top: m.tops[index], Right: width - 1, Bottom: m.tops[index+1]}
		if entry.label == "" {
			middle := (row.Top + row.Bottom) / 2
			fill(dc, w32.RECT{Left: row.Left, Top: middle, Right: row.Right, Bottom: middle + 1}, m.colors.border)
			continue
		}
		color := m.colors.text
		if index == m.hot {
			fill(dc, row, m.colors.hover)
			color = m.colors.hoverText
		}
		w32.SelectObject(dc, m.font)
		w32.SetTextColor(dc, color)
		label := w32.RECT{Left: row.Left + padding, Top: row.Top, Right: row.Right - 3*padding, Bottom: row.Bottom}
		drawText(dc, entry.label, &label, w32.DT_LEFT)
		if entry.checked {
			w32.SelectObject(dc, m.glyph)
			w32.SetTextColor(dc, m.colors.check)
			check := w32.RECT{Left: row.Right - 2*padding, Top: row.Top, Right: row.Right - padding, Bottom: row.Bottom}
			drawText(dc, "", &check, w32.DT_CENTER)
		}
	}
}

// fill 用 color 填充矩形
func fill(dc w32.HDC, rect w32.RECT, color w32.COLORREF) {
	brush := w32.CreateSolidBrush(color)
	w32.FillRect(dc, &rect, brush)
	w32.DeleteObject(brush)
}

// drawText 在矩形内垂直居中绘制单行文字，align 为水平对齐方式
func drawText(dc w32.HDC, text string, rect *w32.RECT, align uint32) {
	value := windows.StringToUTF16(text)
	w32.DrawText(dc, value, len(value)-1, rect, align|w32.DT_SINGLELINE|w32.DT_VCENTER|w32.DT_NOPREFIX)
}

// hit 返回客户区纵坐标 y 处的菜单项，分隔线与菜单以外返回 -1
func (m *trayMenu) hit(y int32) int {
	for index, entry := range m.entries {
		if entry.label != "" && y >= m.tops[index] && y < m.tops[index+1] {
			return index
		}
	}
	return -1
}

// next 返回从 from 起沿 direction 方向的下一个可选项，首尾相接
func (m *trayMenu) next(from, direction int) int {
	count := len(m.entries)
	for range count {
		from = (from + direction + count) % count
		if m.entries[from].label != "" {
			return from
		}
	}
	return m.hot
}

// highlight 高亮第 index 项并以它的文字作为窗口标题，供屏幕阅读器朗读，index 为 -1 时取消高亮
func (m *trayMenu) highlight(index int) {
	if index == m.hot {
		return
	}
	m.hot = index
	title := "AIStudio2API"
	if index >= 0 {
		title = m.entries[index].label
	}
	w32.SetWindowText(m.hwnd, title)
	w32.InvalidateRect(m.hwnd, nil, false)
}

// choose 关闭菜单并执行第 index 项
func (m *trayMenu) choose(index int) {
	run := m.entries[index].run
	w32.DestroyWindow(m.hwnd)
	go run()
}

// menuProc 为自绘菜单的窗口过程：鼠标悬停高亮、点击执行，方向键、Home、End 移动高亮，回车或空格执行，Esc 或失去焦点时关闭
func menuProc(hwnd w32.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	menu := activeMenu
	if menu == nil || menu.hwnd != hwnd {
		return w32.DefWindowProc(hwnd, msg, wParam, lParam)
	}
	switch msg {
	case w32.WM_PAINT:
		var paint w32.PAINTSTRUCT
		dc := w32.BeginPaint(hwnd, &paint)
		client := w32.GetClientRect(hwnd)
		memory := w32.CreateCompatibleDC(dc)
		bitmap, _, _ := createCompatibleBitmap.Call(dc, uintptr(client.Right), uintptr(client.Bottom))
		previous := w32.SelectObject(memory, bitmap)
		menu.paint(memory, client.Right, client.Bottom)
		w32.BitBlt(dc, 0, 0, int(client.Right), int(client.Bottom), memory, 0, 0, w32.SRCCOPY)
		w32.SelectObject(memory, previous)
		w32.DeleteObject(bitmap)
		w32.DeleteDC(memory)
		w32.EndPaint(hwnd, &paint)
		return 0
	case w32.WM_PRINTCLIENT:
		client := w32.GetClientRect(hwnd)
		menu.paint(wParam, client.Right, client.Bottom)
		return 0
	case w32.WM_ERASEBKGND:
		return 1
	case w32.WM_MOUSEMOVE:
		if !menu.tracking {
			track := w32.TRACKMOUSEEVENT{DwFlags: w32.TME_LEAVE, HwndTrack: hwnd}
			track.CbSize = uint32(unsafe.Sizeof(track))
			menu.tracking = w32.TrackMouseEvent(&track)
		}
		menu.highlight(menu.hit(int32(int16(lParam >> 16))))
		return 0
	case w32.WM_MOUSELEAVE:
		menu.tracking = false
		menu.highlight(-1)
		return 0
	case w32.WM_LBUTTONUP, w32.WM_RBUTTONUP:
		if index := menu.hit(int32(int16(lParam >> 16))); index >= 0 {
			menu.choose(index)
		}
		return 0
	case w32.WM_KEYDOWN:
		switch wParam {
		case w32.VK_ESCAPE:
			w32.DestroyWindow(hwnd)
		case w32.VK_DOWN:
			menu.highlight(menu.next(menu.hot, 1))
		case w32.VK_UP:
			if menu.hot < 0 {
				menu.highlight(menu.next(0, -1))
			} else {
				menu.highlight(menu.next(menu.hot, -1))
			}
		case w32.VK_HOME:
			menu.highlight(menu.next(-1, 1))
		case w32.VK_END:
			menu.highlight(menu.next(0, -1))
		case w32.VK_RETURN, w32.VK_SPACE:
			if menu.hot >= 0 {
				menu.choose(menu.hot)
			}
		}
		return 0
	case w32.WM_ACTIVATE:
		if wParam&0xffff == w32.WA_INACTIVE {
			w32.PostMessage(hwnd, w32.WM_CLOSE, 0, 0)
		}
	case w32.WM_DESTROY:
		w32.DeleteObject(menu.font)
		w32.DeleteObject(menu.glyph)
		activeMenu = nil
	}
	return w32.DefWindowProc(hwnd, msg, wParam, lParam)
}
