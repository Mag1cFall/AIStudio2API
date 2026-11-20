//go:build desktop

// Package desktop 以 Wails 窗口与系统托盘承载管理页面
package desktop

import (
	"context"
	_ "embed"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"runtime"
	"strings"
	"sync"

	"github.com/Mag1cFall/AIStudio2API/internal/i18n"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/updater"
)

// appIcon 为 Windows 与 Linux 的窗口图标
//
//go:embed icons/appicon.png
var appIcon []byte

// macAppIcon 为 macOS 的 Dock 与对话框图标，按 macOS 图标网格留白，与应用包内的 icons.icns 同源
//
//go:embed icons/appicon-mac.png
var macAppIcon []byte

// trayIcon 为 Windows 与 Linux 托盘图标
//
//go:embed icons/tray.png
var trayIcon []byte

// splashPage 为管理服务就绪前主窗口显示的启动画面
//
//go:embed splash.html
var splashPage string

// trayTemplateIcon 为 macOS 菜单栏模板图标
//
//go:embed icons/tray-template.png
var trayTemplateIcon []byte

// messagePrefix 为管理页面发给桌面壳的消息前缀
const messagePrefix = "aistudio2api:"

// App 为桌面应用的窗口、托盘与托盘菜单，没有窗口时 window 为 nil，Windows 缺少 WebView2 运行时时 webview 为 false、询问与安装期间 installing 为 true，桌面环境没有系统托盘时 trayed 为 false，tray 为 macOS 的菜单栏图标
type App struct {
	app        *application.App
	webview    bool
	installing bool
	trayed     bool
	tray       *application.SystemTray
	window     *application.WebviewWindow
	open       bool
	origin     string
	url        string
	target     string
	loaded     bool
	busy       bool
	closing    bool
	bounds     application.Rect
	maxed      bool
	mu         sync.Mutex
	menu       *application.WebviewWindow
	locale     string
	theme      string
}

// HandleUpdateHelper 在更新器辅助进程中完成文件替换并退出，其他进程直接返回
func HandleUpdateHelper() {
	updater.HandleHelperMode()
}

// New 创建桌面应用与托盘，show 为 true 时同时创建主窗口（Windows 缺少 WebView2 运行时时改为在 Run 中询问并安装），hideDock 为 true 时 macOS 只显示菜单栏图标、不显示 Dock 图标，WebView 数据写入 webviewDir，stop 在窗口与托盘关闭后、进程退出前执行；已有实例运行时把它的窗口带到前台并结束当前进程
func New(show, hideDock bool, webviewDir string, stop func()) *App {
	shell := &App{}
	policy := application.ActivationPolicyRegular
	if hideDock {
		policy = application.ActivationPolicyAccessory
	}
	icon := appIcon
	if runtime.GOOS == "darwin" {
		icon = macAppIcon
	}
	shell.app = application.New(application.Options{
		Name:        "AIStudio2API",
		Description: "AIStudio2API",
		Icon:        icon,
		Mac: application.MacOptions{
			ActivationPolicy: policy,
		},
		Linux: application.LinuxOptions{
			DisableQuitOnLastWindowClosed: true,
			ProgramName:                   "aistudio2api",
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               "io.github.mag1cfall.aistudio2api",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { shell.show() },
		},
		Windows: application.WindowsOptions{
			WebviewUserDataPath:           webviewDir,
			DisableQuitOnLastWindowClosed: true,
			WndProcInterceptor:            shell.trayMessage,
		},
		RawMessageHandler: shell.handleMessage,
		PostShutdown:      stop,
		ErrorHandler:      func(err error) { slog.Error("桌面窗口错误", "error", err) },
		WarningHandler:    func(message string) { slog.Warn("桌面窗口警告", "message", message) },
	})
	shell.webview = runtime.GOOS != "windows" || shell.app.Env.Info().PlatformInfo["WebView2"] != ""
	shell.open = show
	shell.startTray()
	switch {
	case !shell.webview && show:
		shell.installing = true
	case !shell.webview:
		slog.Warn("没有找到 WebView2 运行时，管理页面在默认浏览器中打开")
	case show || !shell.hasTray():
		shell.createWindow()
	}
	shell.app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) {
		shell.show()
	})
	return shell
}

// hasTray 返回托盘是否已创建
func (s *App) hasTray() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.trayed
}

// windowBackground 返回与管理页面画布同色的窗口背景：亮色主题为 #f6f8fa，其余为启动画面与深色主题的 #0d1117
func windowBackground(theme string) application.RGBA {
	if theme == "light" {
		return application.NewRGB(246, 248, 250)
	}
	return application.NewRGB(13, 17, 23)
}

// createWindow 返回主窗口，没有时以上次销毁前的位置、大小与最大化状态创建
func (s *App) createWindow() *application.WebviewWindow {
	s.mu.Lock()
	window, target, bounds, maxed, theme := s.window, s.target, s.bounds, s.maxed, s.theme
	s.mu.Unlock()
	if window != nil {
		return window
	}
	options := application.WebviewWindowOptions{
		Name:             "main",
		Title:            "AIStudio2API",
		Width:            1280,
		Height:           820,
		MinWidth:         800,
		MinHeight:        560,
		HTML:             splashPage,
		Frameless:        runtime.GOOS != "darwin",
		BackgroundColour: windowBackground(theme),
		Mac: application.MacWindow{
			TitleBar:                application.MacTitleBarHiddenInset,
			InvisibleTitleBarHeight: 40,
		},
		Linux: application.LinuxWindow{WebviewGpuPolicy: application.WebviewGpuPolicyNever},
		// WebView2CompositionHosting 让标题栏按钮作为非客户区参与 Windows 命中测试，悬停最大化按钮时系统显示贴靠布局
		Windows: application.WindowsWindow{WebView2CompositionHosting: true},
	}
	if target != "" {
		options.HTML, options.URL = "", target
	}
	if bounds.Width > 0 {
		options.InitialPosition = application.WindowXY
		options.X, options.Y, options.Width, options.Height = bounds.X, bounds.Y, bounds.Width, bounds.Height
	}
	window = s.app.Window.NewWithOptions(options)
	if maxed {
		window.Maximise()
	}
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if !s.hasTray() {
			go s.app.Quit()
			return
		}
		maxed, minimised, bounds := window.IsMaximised(), window.IsMinimised(), window.Bounds()
		s.mu.Lock()
		keep := s.window == window && s.busy
		if keep {
			s.closing = true
		} else if s.window == window {
			s.window, s.loaded, s.busy, s.closing, s.maxed = nil, false, false, false, maxed
			if !maxed && !minimised {
				s.bounds = bounds
			}
		}
		s.mu.Unlock()
		if keep {
			event.Cancel()
			window.Hide()
		}
	})
	s.mu.Lock()
	s.window, s.loaded = window, target != ""
	s.mu.Unlock()
	return window
}

// setBusy 记录页面是否有进行中的工作或未保存的内容，窗口已关闭隐藏且页面空闲时销毁窗口与 WebView
func (s *App) setBusy(busy bool) {
	s.mu.Lock()
	s.busy = busy
	window, release := s.window, !busy && s.closing
	s.mu.Unlock()
	if release && window != nil {
		window.Close()
	}
}

// setRoute 记录管理页面当前的路由，重新创建窗口时打开它
func (s *App) setRoute(route string) {
	address, err := url.Parse(route)
	if err != nil || !strings.HasPrefix(address.Path, "/") {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.target = s.origin + pageAddress(address)
}

// pageAddress 返回带桌面参数的管理页面路径与查询
func pageAddress(address *url.URL) string {
	query := address.Query()
	query.Set("shell", "desktop")
	query.Set("platform", runtime.GOOS)
	return (&url.URL{Path: address.Path, RawQuery: query.Encode()}).String()
}

// Run 运行窗口与托盘，启动画面显示到 start 返回管理页面地址，需要安装 WebView2 运行时时与 start 同时询问并安装；start 出错时以系统语言显示错误对话框后退出，运行到用户从托盘退出或 ctx 结束
func (s *App) Run(ctx context.Context, start func() (string, error)) error {
	s.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		if s.installing {
			go s.installRuntime()
		}
		go func() {
			pageURL, err := start()
			if err != nil {
				if ctx.Err() == nil {
					s.app.Dialog.Error().SetTitle("AIStudio2API").SetMessage(i18n.Translate(i18n.Console, err.Error())).Show()
				}
				s.app.Quit()
				return
			}
			s.navigate(pageURL)
		}()
	})
	stop := context.AfterFunc(ctx, s.app.Quit)
	defer stop()
	return s.app.Run()
}

// navigate 记录管理页面地址，启动画面已加载时让主窗口显示带桌面参数的管理页面；没有 WebView2 且不在安装时按启动参数在浏览器中打开
func (s *App) navigate(pageURL string) {
	address, _ := url.Parse(pageURL)
	origin := address.Scheme + "://" + address.Host
	target := origin + pageAddress(address)
	s.mu.Lock()
	s.origin, s.url, s.target = origin, pageURL, target
	window, loaded, browse := s.window, s.loaded, !s.webview && !s.installing && s.open
	s.mu.Unlock()
	if browse {
		s.openPage()
	}
	if window != nil && loaded {
		window.SetURL(target)
	}
}

// installRuntime 询问并安装 WebView2 运行时，装好后显示主窗口，没有装上时按启动参数在浏览器中打开管理页面
func (s *App) installRuntime() {
	installed := installWebView2()
	s.mu.Lock()
	s.webview, s.installing = installed, false
	s.mu.Unlock()
	if installed {
		s.show()
		return
	}
	slog.Warn("没有找到 WebView2 运行时，管理页面在默认浏览器中打开")
	s.openPage()
}

// splashLoaded 记录启动画面已加载，管理页面地址已知时让主窗口显示管理页面
func (s *App) splashLoaded() {
	s.mu.Lock()
	first, target, window := !s.loaded, s.target, s.window
	s.loaded = true
	s.mu.Unlock()
	if first && target != "" && window != nil {
		window.SetURL(target)
	}
}

// createTray 创建托盘图标与托盘菜单，菜单文字在页面加载前跟随系统语言
func (s *App) createTray() {
	tray := s.app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayTemplateIcon)
	} else {
		tray.SetIcon(trayIcon)
	}
	tray.SetTooltip("AIStudio2API")
	s.attachMenu(tray)
	s.setLocale(i18n.Console)
	s.mu.Lock()
	s.trayed = true
	s.mu.Unlock()
}

// toggleAutostart 按系统中的当前设置切换开机自启动，失败时显示错误对话框
func (s *App) toggleAutostart() {
	enabled, err := s.autostartEnabled()
	if err == nil {
		err = s.setAutostart(!enabled)
	}
	if err != nil {
		slog.Warn("开机自启动设置失败", "error", err)
		s.app.Dialog.Error().SetTitle("AIStudio2API").SetMessage(err.Error()).Show()
	}
}

// openPage 在默认浏览器中打开管理页面，服务就绪前不做任何事
func (s *App) openPage() {
	s.mu.Lock()
	pageURL := s.url
	s.mu.Unlock()
	if pageURL == "" {
		return
	}
	if err := s.app.Browser.OpenURL(pageURL); err != nil {
		slog.Warn("管理页面打开失败", "error", err)
	}
}

// PickPath 以系统对话框选择账户目录，从 start 开始，start 为空时从系统默认位置开始；用户取消时返回空串
func (s *App) PickPath(start string) (string, error) {
	dialog := s.app.Dialog.OpenFile().CanChooseDirectories(true).CanChooseFiles(false).CanCreateDirectories(true).SetDirectory(start)
	s.mu.Lock()
	window := s.window
	s.mu.Unlock()
	if window != nil {
		dialog.AttachToWindow(window)
	}
	path, err := dialog.PromptForSingleSelection()
	if err != nil && err.Error() == "cancelled by user" {
		return "", nil
	}
	return path, err
}

// show 显示、还原并聚焦主窗口，没有窗口时先创建，关闭后仍隐藏保留的窗口取消销毁；没有 WebView2 或正在安装时在浏览器中打开管理页面
func (s *App) show() {
	s.mu.Lock()
	webview := s.webview
	s.mu.Unlock()
	if !webview {
		s.openPage()
		return
	}
	window := s.createWindow()
	s.mu.Lock()
	s.closing = false
	s.mu.Unlock()
	window.Show()
	window.UnMinimise()
	window.Focus()
	activateApp()
}

// handleMessage 处理管理页面经 postMessage 发来的窗口操作、界面语言、配色、Dock 图标、当前路由与是否有未完成的工作
func (s *App) handleMessage(window application.Window, message string, origin *application.OriginInfo) {
	command, found := strings.CutPrefix(message, messagePrefix)
	if !found {
		return
	}
	if window.Name() == "tray" {
		window.Close()
		switch command {
		case "tray:show":
			s.show()
		case "tray:browser":
			s.openPage()
		case "tray:autostart":
			s.toggleAutostart()
		case "tray:quit":
			s.app.Quit()
		}
		return
	}
	if command == "splash" {
		s.splashLoaded()
		return
	}
	s.mu.Lock()
	page := s.origin
	s.mu.Unlock()
	if source, err := url.Parse(origin.Origin); page != "" && (err != nil || source.Scheme+"://"+source.Host != page) {
		return
	}
	switch command {
	case "minimise":
		window.Minimise()
	case "toggle-maximise":
		window.ToggleMaximise()
	case "close":
		window.Close()
	default:
		if locale, ok := strings.CutPrefix(command, "locale:"); ok {
			s.setLocale(locale)
		} else if theme, ok := strings.CutPrefix(command, "theme:"); ok {
			s.mu.Lock()
			s.theme = theme
			s.mu.Unlock()
		} else if dock, ok := strings.CutPrefix(command, "dock:"); ok {
			setDockIcon(dock == "show")
		} else if busy, ok := strings.CutPrefix(command, "busy:"); ok {
			s.setBusy(busy == "1")
		} else if route, ok := strings.CutPrefix(command, "route:"); ok && page != "" {
			s.setRoute(route)
		} else if link, ok := strings.CutPrefix(command, "open:"); ok && page != "" {
			s.openExternal(link)
		}
	}
}

// openExternal 用系统浏览器打开管理页面中的 http 或 https 链接
func (s *App) openExternal(link string) {
	address, err := url.Parse(link)
	if err != nil || (address.Scheme != "http" && address.Scheme != "https") {
		return
	}
	if err := s.app.Browser.OpenURL(address.String()); err != nil {
		slog.Warn("链接打开失败", "error", err)
	}
}

// setLocale 记录管理页面的界面语言，托盘菜单打开时按它取文字，界面语言变化时重设 macOS 的原生菜单
func (s *App) setLocale(locale string) {
	_, ok := trayLabels[locale]
	if !ok {
		return
	}
	s.mu.Lock()
	changed := s.locale != locale
	s.locale = locale
	s.mu.Unlock()
	if changed {
		s.localizeMenus(locale)
	}
}

// labels 返回当前界面语言的托盘菜单文字
func (s *App) labels() [4]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return trayLabels[s.locale]
}

// trayLabels 为各界面语言的托盘菜单文字：显示窗口、在浏览器中打开、开机自启动、退出
var trayLabels = map[string][4]string{
	"zh-CN": {"显示窗口", "在浏览器中打开", "开机自启动", "退出"},
	"zh-TW": {"顯示視窗", "在瀏覽器中開啟", "開機自動啟動", "結束"},
	"en":    {"Show Window", "Open in Browser", "Start at Login", "Quit"},
	"ja":    {"ウィンドウを表示", "ブラウザで開く", "ログイン時に起動", "終了"},
	"ko":    {"창 표시", "브라우저에서 열기", "로그인 시 실행", "종료"},
	"de":    {"Fenster anzeigen", "Im Browser öffnen", "Bei Anmeldung starten", "Beenden"},
	"fr":    {"Afficher la fenêtre", "Ouvrir dans le navigateur", "Lancer à l’ouverture de session", "Quitter"},
}

// slot 为 Wails 更新器的唯一更新来源，转交每次安装传入的来源
var slot = &providerSlot{}

// configure 保证 Wails 更新器只配置一次
var configure sync.Once

// Install 以 provider 检查、下载、校验并暂存新版本，version 为当前版本号
func Install(ctx context.Context, version string, provider updater.Provider) error {
	app := application.Get()
	slot.set(provider)
	var err error
	configure.Do(func() {
		err = app.Updater.Init(updater.Config{CurrentVersion: version, Providers: []updater.Provider{slot}, Window: updater.WindowNone})
	})
	if err != nil {
		return err
	}
	release, err := app.Updater.Check(ctx)
	if err != nil {
		return err
	}
	if release == nil {
		return errors.New("没有可安装的新版本")
	}
	return app.Updater.DownloadAndInstall(ctx)
}

// Restart 启动更新器辅助进程并退出桌面应用，辅助进程在当前进程结束后替换文件并启动新版本
func Restart() error {
	return application.Get().Updater.Restart(context.Background())
}

// providerSlot 把调用转交给最近一次设置的更新来源
type providerSlot struct {
	mu      sync.Mutex
	current updater.Provider
}

// set 设置之后检查与下载使用的更新来源
func (s *providerSlot) set(provider updater.Provider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = provider
}

// get 返回当前更新来源
func (s *providerSlot) get() updater.Provider {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// Name 返回更新来源名称
func (s *providerSlot) Name() string { return "aistudio2api" }

// Check 转交当前更新来源
func (s *providerSlot) Check(ctx context.Context, request updater.CheckRequest) (*updater.Release, error) {
	return s.get().Check(ctx, request)
}

// Download 转交当前更新来源
func (s *providerSlot) Download(ctx context.Context, release *updater.Release, dst io.Writer, onProgress func(written, total int64)) error {
	return s.get().Download(ctx, release, dst, onProgress)
}
