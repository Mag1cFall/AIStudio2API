package camoufoxnative

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/proxydial"
	"github.com/gofrs/flock"
)

var bidiEndpointPattern = regexp.MustCompile(`ws://[^\s]+`)

const browserProcessCloseTimeout = 5 * time.Second

type browserProcessTerminator func(context.Context, *browserProcess) error

type browserProcess struct {
	command *exec.Cmd
	done    chan struct{}
	waitErr error
	profile string
	lock    *flock.Flock
	tree    browserTree
	mu      sync.Mutex
	closed  bool
}

// launchBrowser 启动 Camoufox 并返回原生 WebDriver BiDi 端点
func launchBrowser(ctx context.Context, options Options, config map[string]any) (*browserProcess, string, error) {
	if _, err := os.Stat(options.ExecutablePath); err != nil {
		return nil, "", fmt.Errorf("Camoufox 不可用: %w", err)
	}
	if runtime.GOOS != "windows" {
		config = maps.Clone(config)
		config["AudioContext:maxChannelCount"] = windowsAudioChannels
		config["AudioContext:sampleRate"] = windowsAudioSampleRate
		fonts := fontList(config["fonts"])
		config["fonts"] = slices.Concat(fonts, windowsFontAliases(fonts))
		if outer, ok := integer(config, "window.outerHeight"); ok && config["window.innerHeight"] == nil {
			config["window.innerHeight"] = outer - windowsChromeHeight
		}
	}
	environment, err := camoufoxEnvironment(config)
	if err != nil {
		return nil, "", err
	}
	profile, profileLock, err := createProfile()
	if err != nil {
		return nil, "", err
	}
	relay, proxyValue, err := browserProxy(options.Proxy)
	if err != nil {
		_ = removeProfile(profile, profileLock)
		return nil, "", err
	}
	var display *exec.Cmd
	launched := false
	defer func() {
		if relay != nil && !launched {
			_ = relay.Close()
		}
		if display != nil && !launched {
			stopVirtualDisplay(display)
		}
	}()
	prefs, err := firefoxPreferences(proxyValue, options.ProxyBypass)
	if err != nil {
		_ = removeProfile(profile, profileLock)
		return nil, "", err
	}
	if options.Headless && !options.liveView {
		prefs["layout.frame_rate"] = headlessFrameRate
	}
	if options.CacheDirectory != "" {
		for key, value := range cachePreferences(options.CacheDirectory) {
			prefs[key] = value
		}
	}
	if runtime.GOOS != "windows" {
		prefs["dom.webgpu.enabled"] = true
		prefs["widget.non-native-theme.scrollbar.style"] = windowsScrollbarStyle
		prefs["dom.quotaManager.temporaryStorage.fixedLimit"] = windowsStorageLimit
		prefs["ui.prefersReducedMotion"] = 0
		maps.Copy(prefs, windowsFontData.Prefs)
	}
	if runtime.GOOS == "darwin" {
		// gfx.font_loader.delay 为 0 时 Firefox 同步加载本地化与带字重的字体族名
		prefs["gfx.font_loader.delay"] = 0
	}
	if runtime.GOOS == "linux" {
		fontconfig, err := windowsFontconfig(options.ExecutablePath, profile)
		if err != nil {
			_ = removeProfile(profile, profileLock)
			return nil, "", err
		}
		environment = append(environment, "FONTCONFIG_FILE="+fontconfig)
		if options.Headless && os.Getenv("DISPLAY") == "" {
			var name string
			display, name, err = startVirtualDisplay()
			if err != nil {
				_ = removeProfile(profile, profileLock)
				return nil, "", err
			}
			environment = append(environment, "DISPLAY="+name)
		}
	}
	if err := writeUserJS(profile, prefs); err != nil {
		_ = removeProfile(profile, profileLock)
		return nil, "", err
	}

	arguments := []string{"--remote-debugging-port=0"}
	if options.Headless {
		arguments = append(arguments, "-no-remote", "-headless")
	} else {
		arguments = append(arguments, "-wait-for-browser")
	}
	arguments = append(arguments, "-profile", profile)
	command := exec.Command(options.ExecutablePath, arguments...)
	command.Env = environment
	configureBrowserProcess(command, options.Headless)
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = removeProfile(profile, profileLock)
		return nil, "", err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = removeProfile(profile, profileLock)
		return nil, "", err
	}
	if err := command.Start(); err != nil {
		_ = removeProfile(profile, profileLock)
		return nil, "", fmt.Errorf("启动 Camoufox: %w", err)
	}
	tree, err := attachBrowserProcess(command)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = removeProfile(profile, profileLock)
		return nil, "", fmt.Errorf("绑定 Camoufox 进程: %w", err)
	}
	process := &browserProcess{
		command: command,
		done:    make(chan struct{}),
		profile: profile,
		lock:    profileLock,
		tree:    tree,
	}
	launched = true
	go func() {
		waitErr := command.Wait()
		if relay != nil {
			_ = relay.Close()
		}
		if display != nil {
			stopVirtualDisplay(display)
		}
		process.mu.Lock()
		process.waitErr = waitErr
		process.mu.Unlock()
		close(process.done)
	}()
	endpoints := make(chan string, 2)
	go scanBrowserOutput(stdout, options.Log, endpoints)
	go scanBrowserOutput(stderr, options.Log, endpoints)
	timeout := options.ReadyTimeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case endpoint := <-endpoints:
		return process, normalizeBiDiEndpoint(endpoint), nil
	case <-process.done:
		process.mu.Lock()
		err := process.waitErr
		process.mu.Unlock()
		_ = process.Close()
		if err == nil {
			err = errors.New("Camoufox 在报告 BiDi 端点前退出")
		}
		return nil, "", err
	case <-timer.C:
		_ = process.Close()
		return nil, "", fmt.Errorf("等待 Camoufox BiDi 端点超时: %s", timeout)
	case <-ctx.Done():
		_ = process.Close()
		return nil, "", ctx.Err()
	}
}

// Close 关闭 Camoufox 并删除隔离 profile
func (process *browserProcess) Close() error {
	return process.close(browserProcessCloseTimeout, terminateBrowserProcess)
}

func (process *browserProcess) close(timeout time.Duration, terminate browserProcessTerminator) error {
	if process == nil {
		return nil
	}
	process.mu.Lock()
	if process.closed {
		process.mu.Unlock()
		return nil
	}
	process.mu.Unlock()
	if timeout <= 0 {
		timeout = browserProcessCloseTimeout
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	closeErr := terminate(closeCtx, process)
	select {
	case <-process.done:
	case <-closeCtx.Done():
		closeErr = errors.Join(closeErr, fmt.Errorf("等待 Camoufox 进程退出: %w", closeCtx.Err()))
	}
	closeErr = errors.Join(closeErr, removeProfile(process.profile, process.lock))
	if closeErr != nil {
		return closeErr
	}
	process.mu.Lock()
	process.closed = true
	process.mu.Unlock()
	releaseBrowserTree(process.tree)
	return nil
}

func scanBrowserOutput(reader io.Reader, mirror io.Writer, endpoints chan<- string) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if mirror != nil {
			_, _ = fmt.Fprintln(mirror, line)
		}
		if endpoint := bidiEndpointPattern.FindString(line); endpoint != "" {
			select {
			case endpoints <- endpoint:
			default:
			}
		}
	}
}

func normalizeBiDiEndpoint(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err == nil && (parsed.Path == "" || parsed.Path == "/") {
		parsed.Path = "/session"
		return parsed.String()
	}
	return endpoint
}

// headlessFrameRate 为无头 Worker 的页面刷新帧率
const headlessFrameRate = 1

// windowsScrollbarStyle 为 Firefox 非原生主题中 Windows 10 滚动条样式的取值
const windowsScrollbarStyle = 4

// windowsAudioChannels 为 Windows 立体声输出设备的 AudioContext 最大声道数
const windowsAudioChannels = 2

// windowsAudioSampleRate 为 Windows 默认输出设备的 AudioContext 采样率
const windowsAudioSampleRate = 48000

// windowsChromeHeight 为 Windows 版 Camoufox 窗口中浏览器界面占用的高度
const windowsChromeHeight = 57

// windowsStorageLimit 为使站点存储配额取 Firefox 上限 10 GiB 的全局存储上限，单位 KB
const windowsStorageLimit = 50 << 20

// windowsFontconfig 在 profile 中写入只加载 Camoufox 发行包 Windows 字体并带 Windows 字体替换表的 fontconfig 配置并返回其路径
func windowsFontconfig(executablePath string, profile string) (string, error) {
	directory := filepath.Dir(executablePath)
	data, err := os.ReadFile(filepath.Join(directory, "fontconfig", "windows", "fonts.conf"))
	if err != nil {
		return "", fmt.Errorf("读取 Camoufox Windows 字体配置: %w", err)
	}
	content := strings.ReplaceAll(string(data), `<dir prefix="cwd">fonts</dir>`, "<dir>"+filepath.Join(directory, "fonts", "windows")+"</dir>")
	content = strings.ReplaceAll(content, "<const>hintfull</const>", "<const>hintslight</const>")
	var substitutes strings.Builder
	for _, alias := range slices.Sorted(maps.Keys(windowsFontData.Substitutes)) {
		fmt.Fprintf(&substitutes, `	<match target="pattern"><test qual="any" name="family"><string>%s</string></test><edit name="family" mode="assign" binding="same"><string>%s</string></edit></match>`+"\n", alias, windowsFontData.Substitutes[alias])
	}
	content = strings.Replace(content, "</fontconfig>", substitutes.String()+"</fontconfig>", 1)
	path := filepath.Join(profile, "fonts.conf")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("写入 Camoufox 字体配置: %w", err)
	}
	return path, nil
}

// xvfbArguments 为 Camoufox 官方启动器虚拟显示的 Xvfb 参数
var xvfbArguments = []string{
	"-displayfd", "3", "-screen", "0", "1x1x24", "-ac", "-nolisten", "tcp",
	"-extension", "RENDER", "+extension", "GLX", "-extension", "COMPOSITE", "-extension", "XVideo",
	"-extension", "XVideo-MotionCompensation", "-extension", "XINERAMA", "-fp", "built-ins", "-nocursor", "-br",
}

// startVirtualDisplay 启动供无头 Camoufox 使用 GLX 的 Xvfb，返回进程与显示名
func startVirtualDisplay() (*exec.Cmd, string, error) {
	path, err := exec.LookPath("Xvfb")
	if err != nil {
		return nil, "", fmt.Errorf("没有 DISPLAY 时 Camoufox 需要 Xvfb: %w", err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, "", err
	}
	defer reader.Close()
	command := exec.Command(path, xvfbArguments...)
	command.ExtraFiles = []*os.File{writer}
	command.Env = append(os.Environ(), "__GLX_VENDOR_LIBRARY_NAME=mesa", "LIBGL_ALWAYS_SOFTWARE=1")
	err = command.Start()
	_ = writer.Close()
	if err != nil {
		return nil, "", fmt.Errorf("启动 Xvfb: %w", err)
	}
	number, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil {
		stopVirtualDisplay(command)
		return nil, "", fmt.Errorf("读取 Xvfb 显示号: %w", err)
	}
	return command, ":" + strings.TrimSpace(number), nil
}

// stopVirtualDisplay 结束 Xvfb 并回收进程
func stopVirtualDisplay(command *exec.Cmd) {
	_ = command.Process.Kill()
	_ = command.Wait()
}

// browserProxy 为带账号密码的代理启动本机 SOCKS5 中转，返回浏览器使用的代理 URL
func browserProxy(value string) (*proxydial.Relay, string, error) {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if value == "" || err != nil || parsed.User == nil {
		return nil, value, nil
	}
	relay, err := proxydial.StartRelay(parsed)
	if err != nil {
		return nil, "", err
	}
	return relay, "socks5://" + relay.Addr().String(), nil
}

func firefoxPreferences(proxyValue, bypass string) (map[string]any, error) {
	prefs := map[string]any{
		"remote.active-protocols":           1,
		"devtools.chrome.enabled":           true,
		"devtools.debugger.remote-enabled":  true,
		"browser.shell.checkDefaultBrowser": false,
		"webgl.force-enabled":               true,
	}
	proxyValue = strings.TrimSpace(proxyValue)
	if proxyValue == "" {
		return prefs, nil
	}
	parsed, err := url.Parse(proxyValue)
	if err != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("Camoufox 代理 URL 无效")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("Camoufox 原生代理暂不接受账号密码")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port <= 0 {
		return nil, fmt.Errorf("Camoufox 代理缺少有效端口")
	}
	prefs["network.proxy.type"] = 1
	prefs["network.proxy.no_proxies_on"] = bypass
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		prefs["network.proxy.http"] = parsed.Hostname()
		prefs["network.proxy.http_port"] = port
		prefs["network.proxy.ssl"] = parsed.Hostname()
		prefs["network.proxy.ssl_port"] = port
		prefs["network.proxy.share_proxy_settings"] = true
	case "socks4", "socks5":
		prefs["network.proxy.socks"] = parsed.Hostname()
		prefs["network.proxy.socks_port"] = port
		if strings.EqualFold(parsed.Scheme, "socks5") {
			prefs["network.proxy.socks_version"] = 5
			prefs["network.proxy.socks_remote_dns"] = true
		} else {
			prefs["network.proxy.socks_version"] = 4
		}
	default:
		return nil, fmt.Errorf("Camoufox 代理协议必须是 http、https、socks4 或 socks5")
	}
	return prefs, nil
}

func writeUserJS(profile string, prefs map[string]any) error {
	keys := make([]string, 0, len(prefs))
	for key := range prefs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		value, err := firefoxPrefLiteral(prefs[key])
		if err != nil {
			return fmt.Errorf("Firefox pref %s: %w", key, err)
		}
		builder.WriteString("user_pref(")
		builder.WriteString(strconv.Quote(key))
		builder.WriteString(", ")
		builder.WriteString(value)
		builder.WriteString(");\n")
	}
	if err := os.WriteFile(filepath.Join(profile, "user.js"), []byte(builder.String()), 0o600); err != nil {
		return fmt.Errorf("写入 Camoufox profile: %w", err)
	}
	return nil
}

func firefoxPrefLiteral(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return strconv.Quote(typed), nil
	case bool:
		return strconv.FormatBool(typed), nil
	case int:
		return strconv.Itoa(typed), nil
	default:
		return "", fmt.Errorf("不支持 %T", value)
	}
}
