//go:build desktop

package desktop

import (
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// attachMenu 记录菜单栏图标，左键单击显示窗口，右键由系统弹出 localizeMenus 按界面语言设置的菜单
func (s *App) attachMenu(tray *application.SystemTray) {
	tray.OnClick(s.show)
	s.mu.Lock()
	s.tray = tray
	s.mu.Unlock()
}

// trayMessage 只在 Windows 上拦截托盘消息
func (s *App) trayMessage(uintptr, uint32, uintptr, uintptr) (uintptr, bool) {
	return 0, false
}

// localizeMenus 按界面语言设置应用菜单与菜单栏图标的菜单
func (s *App) localizeMenus(locale string) {
	text := macMenuTexts[locale]
	s.app.Menu.Set(appMenu(text))
	s.mu.Lock()
	tray := s.tray
	s.mu.Unlock()
	if tray != nil {
		tray.SetMenu(s.trayMenu(text))
	}
}

// trayMenu 返回菜单栏图标的菜单：显示窗口、在浏览器中打开、登录时打开与退出，登录时打开的勾选取自系统设置
func (s *App) trayMenu(text macMenuText) *application.Menu {
	menu := application.NewMenu()
	menu.Add(text.ShowWindow).OnClick(func(*application.Context) { s.show() })
	menu.Add(text.OpenInBrowser).OnClick(func(*application.Context) { s.openPage() })
	enabled, err := s.autostartEnabled()
	if err != nil {
		slog.Warn("开机自启动设置读取失败", "error", err)
	}
	login := menu.AddCheckbox(text.OpenAtLogin, enabled)
	login.OnClick(func(*application.Context) {
		s.toggleAutostart()
		enabled, _ := s.autostartEnabled()
		login.SetChecked(enabled)
	})
	menu.AddSeparator()
	menu.Add(text.Quit).OnClick(func(*application.Context) { s.app.Quit() })
	return menu
}

// appMenu 返回 macOS 应用菜单：应用、文件、编辑、显示与窗口菜单，各项为系统标准操作
func appMenu(text macMenuText) *application.Menu {
	menu := application.NewMenu()
	app := menu.AddSubmenu("AIStudio2API")
	addRole(app, application.About, text.About)
	app.AddSeparator()
	addRole(app, application.ServicesMenu, text.Services)
	app.AddSeparator()
	addRole(app, application.Hide, text.Hide)
	addRole(app, application.HideOthers, text.HideOthers)
	addRole(app, application.UnHide, text.ShowAll)
	app.AddSeparator()
	addRole(app, application.Quit, text.Quit)
	file := menu.AddSubmenu(text.File)
	addRole(file, application.CloseWindow, text.CloseWindow)
	edit := menu.AddSubmenu(text.Edit)
	addRole(edit, application.Undo, text.Undo)
	addRole(edit, application.Redo, text.Redo)
	edit.AddSeparator()
	addRole(edit, application.Cut, text.Cut)
	addRole(edit, application.Copy, text.Copy)
	addRole(edit, application.Paste, text.Paste)
	addRole(edit, application.PasteAndMatchStyle, text.PasteAndMatchStyle)
	addRole(edit, application.Delete, text.Delete)
	addRole(edit, application.SelectAll, text.SelectAll)
	view := menu.AddSubmenu(text.View)
	addRole(view, application.ResetZoom, text.ActualSize)
	addRole(view, application.ZoomIn, text.ZoomIn)
	addRole(view, application.ZoomOut, text.ZoomOut)
	view.AddSeparator()
	addRole(view, application.ToggleFullscreen, text.FullScreen)
	addRole(menu, application.WindowMenu, text.Window)
	window := menu.FindByRole(application.WindowMenu).GetSubmenu()
	window.FindByRole(application.Minimise).SetLabel(text.Minimise)
	window.FindByRole(application.Zoom).SetLabel(text.Zoom)
	window.FindByRole(application.Front).SetLabel(text.BringAllToFront)
	return menu
}

// addRole 在 menu 末尾加入系统标准操作 role，文字为 label
func addRole(menu *application.Menu, role application.Role, label string) {
	menu.AddRole(role)
	menu.FindByRole(role).SetLabel(label)
}

// macMenuText 为 macOS 应用菜单与菜单栏菜单在一种界面语言下的文字
type macMenuText struct {
	About, Services, Hide, HideOthers, ShowAll, Quit                          string
	File, CloseWindow                                                         string
	Edit, Undo, Redo, Cut, Copy, Paste, PasteAndMatchStyle, Delete, SelectAll string
	View, ActualSize, ZoomIn, ZoomOut, FullScreen                             string
	Window, Minimise, Zoom, BringAllToFront                                   string
	ShowWindow, OpenInBrowser, OpenAtLogin                                    string
}

// macMenuTexts 为各界面语言的 macOS 菜单文字，用词取自同语言 macOS 的系统菜单
var macMenuTexts = map[string]macMenuText{
	"zh-CN": {
		About: "关于 AIStudio2API", Services: "服务", Hide: "隐藏 AIStudio2API", HideOthers: "隐藏其他", ShowAll: "全部显示", Quit: "退出 AIStudio2API",
		File: "文件", CloseWindow: "关闭窗口",
		Edit: "编辑", Undo: "撤销", Redo: "重做", Cut: "剪切", Copy: "拷贝", Paste: "粘贴", PasteAndMatchStyle: "粘贴并匹配样式", Delete: "删除", SelectAll: "全选",
		View: "显示", ActualSize: "实际大小", ZoomIn: "放大", ZoomOut: "缩小", FullScreen: "进入全屏幕",
		Window: "窗口", Minimise: "最小化", Zoom: "缩放", BringAllToFront: "前置全部窗口",
		ShowWindow: "显示窗口", OpenInBrowser: "在浏览器中打开", OpenAtLogin: "登录时打开",
	},
	"zh-TW": {
		About: "關於 AIStudio2API", Services: "服務", Hide: "隱藏 AIStudio2API", HideOthers: "隱藏其他", ShowAll: "顯示全部", Quit: "結束 AIStudio2API",
		File: "檔案", CloseWindow: "關閉視窗",
		Edit: "編輯", Undo: "還原", Redo: "重做", Cut: "剪下", Copy: "拷貝", Paste: "貼上", PasteAndMatchStyle: "貼上並符合樣式", Delete: "刪除", SelectAll: "全選",
		View: "顯示方式", ActualSize: "實際大小", ZoomIn: "放大", ZoomOut: "縮小", FullScreen: "進入全螢幕",
		Window: "視窗", Minimise: "縮到最小", Zoom: "縮放", BringAllToFront: "將此程式所有視窗移至最前",
		ShowWindow: "顯示視窗", OpenInBrowser: "在瀏覽器中開啟", OpenAtLogin: "在登入時打開",
	},
	"en": {
		About: "About AIStudio2API", Services: "Services", Hide: "Hide AIStudio2API", HideOthers: "Hide Others", ShowAll: "Show All", Quit: "Quit AIStudio2API",
		File: "File", CloseWindow: "Close Window",
		Edit: "Edit", Undo: "Undo", Redo: "Redo", Cut: "Cut", Copy: "Copy", Paste: "Paste", PasteAndMatchStyle: "Paste and Match Style", Delete: "Delete", SelectAll: "Select All",
		View: "View", ActualSize: "Actual Size", ZoomIn: "Zoom In", ZoomOut: "Zoom Out", FullScreen: "Enter Full Screen",
		Window: "Window", Minimise: "Minimize", Zoom: "Zoom", BringAllToFront: "Bring All to Front",
		ShowWindow: "Show Window", OpenInBrowser: "Open in Browser", OpenAtLogin: "Open at Login",
	},
	"ja": {
		About: "AIStudio2API について", Services: "サービス", Hide: "AIStudio2API を非表示", HideOthers: "ほかを非表示", ShowAll: "すべてを表示", Quit: "AIStudio2API を終了",
		File: "ファイル", CloseWindow: "ウインドウを閉じる",
		Edit: "編集", Undo: "取り消す", Redo: "やり直す", Cut: "カット", Copy: "コピー", Paste: "ペースト", PasteAndMatchStyle: "ペーストしてスタイルを合わせる", Delete: "削除", SelectAll: "すべてを選択",
		View: "表示", ActualSize: "実際のサイズ", ZoomIn: "拡大", ZoomOut: "縮小", FullScreen: "フルスクリーンにする",
		Window: "ウインドウ", Minimise: "しまう", Zoom: "拡大/縮小", BringAllToFront: "すべてを手前に移動",
		ShowWindow: "ウインドウを表示", OpenInBrowser: "ブラウザで開く", OpenAtLogin: "ログイン時に開く",
	},
	"ko": {
		About: "AIStudio2API에 관하여", Services: "서비스", Hide: "AIStudio2API 가리기", HideOthers: "기타 가리기", ShowAll: "모두 보기", Quit: "AIStudio2API 종료",
		File: "파일", CloseWindow: "윈도우 닫기",
		Edit: "편집", Undo: "실행 취소", Redo: "실행 복귀", Cut: "오려두기", Copy: "복사하기", Paste: "붙여넣기", PasteAndMatchStyle: "붙여넣고 스타일 일치", Delete: "삭제", SelectAll: "전체 선택",
		View: "보기", ActualSize: "실제 크기", ZoomIn: "확대", ZoomOut: "축소", FullScreen: "전체 화면 시작",
		Window: "윈도우", Minimise: "최소화", Zoom: "확대/축소", BringAllToFront: "모두 앞으로 가져오기",
		ShowWindow: "윈도우 보기", OpenInBrowser: "브라우저에서 열기", OpenAtLogin: "로그인 시 열기",
	},
	"de": {
		About: "Über AIStudio2API", Services: "Dienste", Hide: "AIStudio2API ausblenden", HideOthers: "Andere ausblenden", ShowAll: "Alle einblenden", Quit: "AIStudio2API beenden",
		File: "Ablage", CloseWindow: "Fenster schließen",
		Edit: "Bearbeiten", Undo: "Widerrufen", Redo: "Wiederholen", Cut: "Ausschneiden", Copy: "Kopieren", Paste: "Einsetzen", PasteAndMatchStyle: "Einsetzen und Stil anpassen", Delete: "Löschen", SelectAll: "Alles auswählen",
		View: "Darstellung", ActualSize: "Originalgröße", ZoomIn: "Vergrößern", ZoomOut: "Verkleinern", FullScreen: "Vollbildmodus",
		Window: "Fenster", Minimise: "Minimieren", Zoom: "Zoomen", BringAllToFront: "Alle nach vorne bringen",
		ShowWindow: "Fenster anzeigen", OpenInBrowser: "Im Browser öffnen", OpenAtLogin: "Bei der Anmeldung öffnen",
	},
	"fr": {
		About: "À propos d’AIStudio2API", Services: "Services", Hide: "Masquer AIStudio2API", HideOthers: "Masquer les autres", ShowAll: "Tout afficher", Quit: "Quitter AIStudio2API",
		File: "Fichier", CloseWindow: "Fermer la fenêtre",
		Edit: "Édition", Undo: "Annuler", Redo: "Rétablir", Cut: "Couper", Copy: "Copier", Paste: "Coller", PasteAndMatchStyle: "Coller et adapter le style", Delete: "Supprimer", SelectAll: "Tout sélectionner",
		View: "Présentation", ActualSize: "Taille réelle", ZoomIn: "Agrandir", ZoomOut: "Réduire", FullScreen: "Activer le mode plein écran",
		Window: "Fenêtre", Minimise: "Minimiser", Zoom: "Réduire/agrandir", BringAllToFront: "Tout ramener au premier plan",
		ShowWindow: "Afficher la fenêtre", OpenInBrowser: "Ouvrir dans le navigateur", OpenAtLogin: "Ouvrir avec la session",
	},
}
