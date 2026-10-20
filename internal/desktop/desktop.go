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

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/updater"
)

// appIcon 为窗口与 Linux 托盘使用的应用图标
//
//go:embed icons/appicon.png
var appIcon []byte

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

// App 为桌面应用的窗口、托盘与托盘菜单；Windows 缺少 WebView2 运行时时 window 为 nil，管理页面在默认浏览器中打开
type App struct {
	app    *application.App
	window *application.WebviewWindow
	open   bool
	origin string
	url    string
	target string
	loaded bool
	mu     sync.Mutex
	items  [3]*application.MenuItem
	locale string
}

// HandleUpdateHelper 在更新器辅助进程中完成文件替换并退出，其他进程直接返回
func HandleUpdateHelper() {
	updater.HandleHelperMode()
}

// New 创建桌面应用、主窗口与托盘，show 为 false 时主窗口保持隐藏，WebView 数据写入 webviewDir；已有实例运行时把它的窗口带到前台并结束当前进程；Windows 缺少 WebView2 运行时时只创建托盘，show 为 true 时服务就绪后在默认浏览器打开管理页面
func New(show bool, webviewDir string) *App {
	shell := &App{}
	shell.app = application.New(application.Options{
		Name:        "AIStudio2API",
		Description: "AIStudio2API",
		Icon:        appIcon,
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyRegular,
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
			WebviewUserDataPath: webviewDir,
		},
		RawMessageHandler: shell.handleMessage,
	})
	if runtime.GOOS == "windows" && shell.app.Env.Info().PlatformInfo["WebView2"] == "" {
		slog.Warn("没有找到 WebView2 运行时，管理页面在默认浏览器中打开")
		shell.open = show
		shell.createTray()
		shell.items[0].SetHidden(true)
		return shell
	}
	shell.window = shell.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "AIStudio2API",
		Width:            1280,
		Height:           820,
		MinWidth:         800,
		MinHeight:        560,
		Hidden:           !show,
		HTML:             splashPage,
		Frameless:        runtime.GOOS != "darwin",
		BackgroundColour: application.NewRGB(13, 17, 23),
		Mac: application.MacWindow{
			TitleBar:                application.MacTitleBarHiddenInset,
			InvisibleTitleBarHeight: 40,
		},
	})
	shell.window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		shell.window.Hide()
		event.Cancel()
	})
	shell.app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) {
		shell.show()
	})
	shell.createTray()
	return shell
}

// Run 运行窗口与托盘，启动画面显示到 start 返回管理页面地址；start 出错时显示错误对话框后退出，运行到用户从托盘退出或 ctx 结束
func (s *App) Run(ctx context.Context, start func() (string, error)) error {
	s.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go func() {
			pageURL, err := start()
			if err != nil {
				if ctx.Err() == nil {
					s.app.Dialog.Error().SetTitle("AIStudio2API").SetMessage(err.Error()).Show()
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

// navigate 记录管理页面地址，启动画面已加载时让主窗口显示带桌面参数的管理页面，否则等启动画面加载后再显示；没有窗口时按启动参数在浏览器中打开
func (s *App) navigate(pageURL string) {
	if s.window == nil {
		s.mu.Lock()
		s.url = pageURL
		s.mu.Unlock()
		if s.open {
			s.openPage()
		}
		return
	}
	address, err := url.Parse(pageURL)
	if err != nil {
		return
	}
	query := address.Query()
	query.Set("shell", "desktop")
	query.Set("platform", runtime.GOOS)
	address.RawQuery = query.Encode()
	s.mu.Lock()
	s.origin, s.url, s.target = address.Scheme+"://"+address.Host, pageURL, address.String()
	loaded := s.loaded
	s.mu.Unlock()
	if loaded {
		s.window.SetURL(address.String())
	}
}

// splashLoaded 记录启动画面已加载，管理页面地址已知时让主窗口显示管理页面
func (s *App) splashLoaded() {
	s.mu.Lock()
	first, target := !s.loaded, s.target
	s.loaded = true
	s.mu.Unlock()
	if first && target != "" {
		s.window.SetURL(target)
	}
}

// createTray 创建托盘图标与菜单，单击托盘图标显示窗口
func (s *App) createTray() {
	tray := s.app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayTemplateIcon)
	} else {
		tray.SetIcon(trayIcon)
	}
	tray.SetTooltip("AIStudio2API")
	menu := s.app.NewMenu()
	s.items[0] = menu.Add("").OnClick(func(*application.Context) { s.show() })
	s.items[1] = menu.Add("").OnClick(func(*application.Context) { s.openPage() })
	menu.AddSeparator()
	s.items[2] = menu.Add("").OnClick(func(*application.Context) { s.app.Quit() })
	s.setLocale("en")
	tray.SetMenu(menu)
	tray.OnClick(s.show)
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

// show 显示、还原并聚焦主窗口，没有窗口时在浏览器中打开管理页面
func (s *App) show() {
	if s.window == nil {
		s.openPage()
		return
	}
	s.window.Show()
	s.window.UnMinimise()
	s.window.Focus()
}

// handleMessage 处理管理页面经 postMessage 发来的窗口操作与界面语言
func (s *App) handleMessage(_ application.Window, message string, origin *application.OriginInfo) {
	command, found := strings.CutPrefix(message, messagePrefix)
	if !found || origin == nil {
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
		s.window.Minimise()
	case "toggle-maximise":
		s.window.ToggleMaximise()
	case "close":
		s.window.Hide()
	default:
		if locale, ok := strings.CutPrefix(command, "locale:"); ok {
			s.setLocale(locale)
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

// setLocale 按管理页面的界面语言更新托盘菜单文字
func (s *App) setLocale(locale string) {
	labels, ok := trayLabels[locale]
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locale == locale {
		return
	}
	s.locale = locale
	for index, item := range s.items {
		item.SetLabel(labels[index])
	}
}

// trayLabels 为各界面语言的托盘菜单文字：显示窗口、在浏览器中打开、退出
var trayLabels = map[string][3]string{
	"zh-CN": {"显示窗口", "在浏览器中打开", "退出"},
	"zh-TW": {"顯示視窗", "在瀏覽器中開啟", "結束"},
	"en":    {"Show Window", "Open in Browser", "Quit"},
	"ja":    {"ウィンドウを表示", "ブラウザで開く", "終了"},
	"ko":    {"창 표시", "브라우저에서 열기", "종료"},
	"de":    {"Fenster anzeigen", "Im Browser öffnen", "Beenden"},
	"fr":    {"Afficher la fenêtre", "Ouvrir dans le navigateur", "Quitter"},
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
