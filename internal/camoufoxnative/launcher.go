package camoufoxnative

import (
	"bufio"
	"context"
	"encoding/json"
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

type browserProcess struct {
	command *exec.Cmd
	done    chan struct{}
	profile string
	lock    *flock.Flock
	tree    browserTree
	// cacheDirectory 为账户缓存目录，浏览器退出后把 profile 的启动缓存存回这里
	cacheDirectory string
	// output 是 Camoufox 最近输出的行，drained 在标准输出与标准错误都读完后关闭
	output  *browserOutput
	drained chan struct{}
	mu      sync.Mutex
	// stopping 表示服务已开始关闭这个进程
	stopping bool
	closed   bool
}

// browserExitWait 是 BiDi 连接断开后等待 Camoufox 进程退出的最长时间
const browserExitWait = 5 * time.Second

// browserExited 是 Camoufox 进程退出导致 BiDi 连接断开的错误：code 为退出码，clean 表示退出码为 0，output 为 Camoufox 最近的输出
type browserExited struct {
	code   string
	clean  bool
	output string
}

func (exited *browserExited) Error() string {
	if exited.output == "" {
		return fmt.Sprintf("Camoufox 已退出，退出码 %s", exited.code)
	}
	return fmt.Sprintf("Camoufox 已退出，退出码 %s，最近的输出：\n%s", exited.code, exited.output)
}

// exitFailure 在 Camoufox 进程 wait 内自行退出时，在同一时限内等输出读完，返回写明退出码与最近输出的 browserExited；进程仍在运行、由服务关闭或 process 为 nil 时返回 nil
func (process *browserProcess) exitFailure(wait time.Duration) error {
	if process == nil {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-process.done:
	case <-timer.C:
		return nil
	}
	process.mu.Lock()
	stopping := process.stopping
	process.mu.Unlock()
	state := process.command.ProcessState
	if stopping || state == nil {
		return nil
	}
	select {
	case <-process.drained:
	case <-timer.C:
	}
	return &browserExited{code: exitCodeText(state), clean: state.ExitCode() == 0, output: process.output.text()}
}

// exitCodeText 返回进程的退出码：Windows 上为十六进制，其他系统为十进制，进程被信号结束时为信号名
func exitCodeText(state *os.ProcessState) string {
	code := state.ExitCode()
	switch {
	case code < 0:
		return state.String()
	case runtime.GOOS == "windows":
		return fmt.Sprintf("0x%08X", uint32(code))
	}
	return strconv.Itoa(code)
}

// launchBrowser 启动 Camoufox 并返回原生 WebDriver BiDi 端点
func launchBrowser(ctx context.Context, options Options, config map[string]any) (*browserProcess, string, error) {
	if _, err := os.Stat(options.ExecutablePath); err != nil {
		return nil, "", fmt.Errorf("Camoufox 不可用: %w", err)
	}
	config = maps.Clone(config)
	// voices:blockIfNotDefined 为 true 时语音列表只含指纹中的语音，不加入主机语音
	config["voices:blockIfNotDefined"] = true
	if runtime.GOOS != "windows" {
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
	display := false
	launched := false
	defer func() {
		if relay != nil && !launched {
			_ = relay.Close()
		}
		if display && !launched {
			releaseVirtualDisplay()
		}
	}()
	prefs, err := firefoxPreferences(proxyValue, options.ProxyBypass)
	if err != nil {
		_ = removeProfile(profile, profileLock)
		return nil, "", err
	}
	// intl.locale.requested 选用 Camoufox 内置语言包，界面语言与 Intl 默认区域随指纹语言
	prefs["intl.locale.requested"] = requestedLocale(config)
	if options.Headless && !options.liveView {
		prefs["layout.frame_rate"] = interactiveFrameRate
		// gfx.webrender.enable-subpixel-aa 为 false 时 WebRender 把字形光栅化到单通道图集，不分配 RGBA 字形图集
		prefs["gfx.webrender.enable-subpixel-aa"] = false
		if runtime.GOOS == "windows" {
			// timer.maximum_firing_delay_tolerance_ms 为 0 时 Windows 定时器线程在最早的定时器到期时唤醒，界面操作中的短定时器按期触发
			prefs["timer.maximum_firing_delay_tolerance_ms"] = "0"
		}
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
		if err := addWindowsSubstituteNames(filepath.Join(filepath.Dir(filepath.Dir(options.ExecutablePath)), "Resources", "fonts")); err != nil {
			_ = removeProfile(profile, profileLock)
			return nil, "", err
		}
		// gfx.font_loader.delay 为 0 时 Firefox 同步加载本地化与带字重的字体族名
		prefs["gfx.font_loader.delay"] = 0
		// ui.key.accelKey 为 17 时快捷键修饰键与 Windows 指纹一致为 Control
		prefs["ui.key.accelKey"] = 17
		if !options.Headless {
			// layers.gpu-process.enabled 为 false 时由注册了随包字体的主进程绘制窗口，webgl.allow-in-parent 让 WebGL 随之在主进程运行
			prefs["layers.gpu-process.enabled"] = false
			prefs["webgl.allow-in-parent"] = true
		}
	}
	if runtime.GOOS == "linux" {
		fontconfig, err := windowsFontconfig(options.ExecutablePath, profile)
		if err != nil {
			_ = removeProfile(profile, profileLock)
			return nil, "", err
		}
		environment = append(environment, "FONTCONFIG_FILE="+fontconfig)
		if options.Headless && os.Getenv("DISPLAY") == "" {
			name, err := acquireVirtualDisplay()
			if err != nil {
				_ = removeProfile(profile, profileLock)
				return nil, "", err
			}
			display = true
			environment = append(environment, "DISPLAY="+name)
		}
	}
	if err := writeUserJS(profile, prefs); err != nil {
		_ = removeProfile(profile, profileLock)
		return nil, "", err
	}
	// Camoufox 的临时目录指向自身 profile
	environment = append(environment, "TMP="+profile, "TEMP="+profile, "TMPDIR="+profile)
	if options.Headless && !options.liveView {
		// MOZ_REMOTE_ALLOW_SYSTEM_ACCESS 让 WebDriver BiDi 访问浏览器界面层，Worker 由此停止标签页的图层绘制
		environment = append(environment, "MOZ_REMOTE_ALLOW_SYSTEM_ACCESS=1")
	}
	if options.CacheDirectory != "" {
		restoreStartupCache(options.CacheDirectory, profile)
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
	// stdout 与 stderr 读到 Camoufox 及继承它们的子进程都退出为止，Wait 不关闭读端，退出前最后的输出不丢失
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		_ = removeProfile(profile, profileLock)
		return nil, "", err
	}
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdout.Close()
		_ = stdoutWriter.Close()
		_ = removeProfile(profile, profileLock)
		return nil, "", err
	}
	command.Stdout, command.Stderr = stdoutWriter, stderrWriter
	err = command.Start()
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	if err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		_ = removeProfile(profile, profileLock)
		return nil, "", fmt.Errorf("启动 Camoufox: %w", err)
	}
	tree, err := attachBrowserProcess(command)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = stdout.Close()
		_ = stderr.Close()
		_ = removeProfile(profile, profileLock)
		return nil, "", fmt.Errorf("绑定 Camoufox 进程: %w", err)
	}
	process := &browserProcess{
		command:        command,
		done:           make(chan struct{}),
		profile:        profile,
		lock:           profileLock,
		tree:           tree,
		cacheDirectory: options.CacheDirectory,
		output:         &browserOutput{},
		drained:        make(chan struct{}),
	}
	launched = true
	go func() {
		_ = command.Wait()
		if relay != nil {
			_ = relay.Close()
		}
		if display {
			releaseVirtualDisplay()
		}
		close(process.done)
	}()
	endpoints := make(chan string, 2)
	output := process.output
	var scanned sync.WaitGroup
	for _, reader := range []*os.File{stdout, stderr} {
		scanned.Go(func() {
			scanBrowserOutput(reader, options.Log, endpoints, output)
			_ = reader.Close()
		})
	}
	go func() {
		scanned.Wait()
		close(process.drained)
	}()
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
		_ = process.Close()
		select {
		case <-process.drained:
		case <-timer.C:
		case <-ctx.Done():
		}
		return nil, "", output.exitError(exitCodeText(process.command.ProcessState), options.ExecutablePath)
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
	if process == nil {
		return nil
	}
	process.mu.Lock()
	if process.closed {
		process.mu.Unlock()
		return nil
	}
	process.stopping = true
	process.mu.Unlock()
	closeCtx, cancel := context.WithTimeout(context.Background(), browserProcessCloseTimeout)
	defer cancel()
	closeErr := terminateBrowserProcess(closeCtx, process)
	select {
	case <-process.done:
	case <-closeCtx.Done():
		closeErr = errors.Join(closeErr, fmt.Errorf("等待 Camoufox 进程退出: %w", closeCtx.Err()))
	}
	if closeErr == nil && process.cacheDirectory != "" {
		saveStartupCache(process.profile, process.cacheDirectory)
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

// browserOutput 保存 Camoufox 或 Xvfb 最近输出的行，进程在报告 BiDi 端点或显示号前退出、运行中退出时写入错误
type browserOutput struct {
	mu    sync.Mutex
	lines []string
}

// browserOutputLines 是退出错误中保留的输出行数
const browserOutputLines = 20

// pageScriptMessage 匹配网页脚本的控制台消息行，它们不进入退出错误
var pageScriptMessage = regexp.MustCompile(`^JavaScript (warning|error): https?://`)

// missingLibrary 匹配动态链接器报告缺少的共享库名
var missingLibrary = regexp.MustCompile(`([^\s:]+\.so[^\s:]*): cannot open shared object file`)

// text 返回最近输出的各行
func (output *browserOutput) text() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	return strings.Join(output.lines, "\n")
}

// add 记下一行输出，网页脚本的控制台消息不记
func (output *browserOutput) add(line string) {
	if pageScriptMessage.MatchString(line) {
		return
	}
	output.mu.Lock()
	defer output.mu.Unlock()
	output.lines = append(output.lines, line)
	if len(output.lines) > browserOutputLines {
		output.lines = output.lines[1:]
	}
}

// exitError 返回 Camoufox 在报告 BiDi 端点前以退出码 code 退出的错误并附上最近的输出；输出报告缺少系统运行库时只写明 executable 缺少的全部系统库，Debian 系发行版另附安装命令
func (output *browserOutput) exitError(code string, executable string) error {
	text := output.text()
	if text == "" {
		return fmt.Errorf("Camoufox 在报告 BiDi 端点前退出，退出码 %s", code)
	}
	reported := missingLibrary.FindAllStringSubmatch(text, -1)
	if len(reported) == 0 {
		return fmt.Errorf("Camoufox 在报告 BiDi 端点前退出，退出码 %s，最近的输出：\n%s", code, text)
	}
	libraries := missingLibraries(executable)
	for _, match := range reported {
		if !slices.Contains(libraries, match[1]) {
			libraries = append(libraries, match[1])
		}
	}
	if command := runtimeInstallCommand(); command != "" {
		return fmt.Errorf("Camoufox 缺少系统运行库 %s，执行 %s 后重试（退出码 %s）", strings.Join(libraries, ", "), command, code)
	}
	return fmt.Errorf("Camoufox 缺少系统运行库 %s，安装提供这些库的系统包后重试（退出码 %s）", strings.Join(libraries, ", "), code)
}

// lddMissing 匹配 ldd 报告找不到的共享库名
var lddMissing = regexp.MustCompile(`(?m)^\s*(\S+) => not found`)

// missingLibraries 以 ldd 检查 Camoufox 主程序与 dependentlibs.list 列出的随包库，随包库从 Camoufox 目录解析，返回系统缺少的全部共享库
func missingLibraries(executable string) []string {
	directory := filepath.Dir(executable)
	targets := []string{executable}
	if list, err := os.ReadFile(filepath.Join(directory, "dependentlibs.list")); err == nil {
		for _, name := range strings.Fields(string(list)) {
			targets = append(targets, filepath.Join(directory, name))
		}
	}
	command := exec.Command("ldd", targets...)
	command.Env = append(os.Environ(), "LD_LIBRARY_PATH="+directory)
	report, _ := command.Output()
	var libraries []string
	for _, match := range lddMissing.FindAllStringSubmatch(string(report), -1) {
		if !slices.Contains(libraries, match[1]) {
			libraries = append(libraries, match[1])
		}
	}
	slices.Sort(libraries)
	return libraries
}

// aptRuntimePackages 是 Debian 与 Ubuntu 上提供 Camoufox 运行库与 Xvfb 的依赖表达式，ALSA 库自 Ubuntu 24.04 与 Debian 13 起由 libasound2t64 提供
const aptRuntimePackages = "libgtk-3-0, libasound2t64 | libasound2, libx11-xcb1, xvfb"

// debianRelease 匹配 /etc/os-release 中 Debian 系发行版的 ID 或 ID_LIKE
var debianRelease = regexp.MustCompile(`(?m)^ID(_LIKE)?=.*\b(debian|ubuntu)\b`)

// runtimeInstallCommand 返回本机安装 Camoufox 运行库与 Xvfb 的命令，不是 Debian 系发行版时返回空串
func runtimeInstallCommand() string {
	release, err := os.ReadFile("/etc/os-release")
	if err != nil || !debianRelease.Match(release) {
		return ""
	}
	sudo := "sudo "
	if os.Geteuid() == 0 {
		sudo = ""
	}
	return sudo + "apt-get update && " + sudo + `apt-get satisfy -y "` + aptRuntimePackages + `"`
}

func scanBrowserOutput(reader io.Reader, mirror io.Writer, endpoints chan<- string, output *browserOutput) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		output.add(line)
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

// windowsScrollbarStyle 为 Firefox 非原生主题中 Windows 10 滚动条样式的取值
const windowsScrollbarStyle = 4

// windowsAudioChannels 为 Windows 立体声输出设备的 AudioContext 最大声道数
const windowsAudioChannels = 2

// windowsAudioSampleRate 为 Windows 默认输出设备的 AudioContext 采样率
const windowsAudioSampleRate = 48000

// windowsChromeHeight 为 Windows 版 Camoufox 窗口中浏览器界面占用的高度
const windowsChromeHeight = 85

// windowsStorageLimit 为使站点存储配额取 Firefox 上限 10 GiB 的全局存储上限，单位 KB
const windowsStorageLimit = 50 << 20

// hostChromeHeight 为 macOS 与 Linux 版 Camoufox 窗口中浏览器界面占用的高度
var hostChromeHeight = map[string]int{"darwin": 85, "linux": 86}[runtime.GOOS]

// windowsWindowPosition 为 Windows 版按 browser.xhtml 的 screenX、screenY 放置主窗口的坐标
const windowsWindowPosition = 4

// windowsFontconfig 在 profile 中写入只加载 Camoufox 发行包中 Windows 读取的字体分组（fonts/groups.json 的 readBy.win）并带 Windows 字体替换表的 fontconfig 配置并返回其路径
func windowsFontconfig(executablePath string, profile string) (string, error) {
	directory := filepath.Dir(executablePath)
	data, err := os.ReadFile(filepath.Join(directory, "fontconfig", "windows", "fonts.conf"))
	if err != nil {
		return "", fmt.Errorf("读取 Camoufox Windows 字体配置: %w", err)
	}
	groupData, err := os.ReadFile(filepath.Join(directory, "fonts", "groups.json"))
	if err != nil {
		return "", fmt.Errorf("读取 Camoufox 字体分组: %w", err)
	}
	var groups struct {
		ReadBy map[string][]string `json:"readBy"`
	}
	if err := json.Unmarshal(groupData, &groups); err != nil {
		return "", fmt.Errorf("解析 Camoufox 字体分组: %w", err)
	}
	var dirs strings.Builder
	for _, group := range groups.ReadBy["win"] {
		dirs.WriteString("<dir>" + filepath.Join(directory, "fonts", group) + "</dir>")
	}
	content := strings.ReplaceAll(string(data), `<dir prefix="cwd">fonts</dir>`, dirs.String())
	content = strings.ReplaceAll(content, "<const>hintfull</const>", "<const>hintslight</const>")
	var substitutes strings.Builder
	// fantasy 族取 Windows 版 Firefox 的平台默认字体 Arial
	substitutes.WriteString("\t<alias binding=\"same\"><family>fantasy</family><prefer><family>Arial</family></prefer></alias>\n")
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

// virtualDisplay 是本进程全部无头 Camoufox 共用的 Xvfb 与使用中的浏览器数
var virtualDisplay struct {
	sync.Mutex
	command *exec.Cmd
	name    string
	users   int
}

// acquireVirtualDisplay 返回共用 Xvfb 的显示名，没有浏览器在用时启动 Xvfb
func acquireVirtualDisplay() (string, error) {
	virtualDisplay.Lock()
	defer virtualDisplay.Unlock()
	if virtualDisplay.users == 0 {
		command, name, err := startVirtualDisplay()
		if err != nil {
			return "", err
		}
		virtualDisplay.command, virtualDisplay.name = command, name
	}
	virtualDisplay.users++
	return virtualDisplay.name, nil
}

// releaseVirtualDisplay 释放一次共用 Xvfb，最后一个浏览器释放时结束 Xvfb
func releaseVirtualDisplay() {
	virtualDisplay.Lock()
	defer virtualDisplay.Unlock()
	virtualDisplay.users--
	if virtualDisplay.users == 0 {
		stopVirtualDisplay(virtualDisplay.command)
	}
}

// startVirtualDisplay 启动供无头 Camoufox 使用 GLX 的 Xvfb，返回进程与显示名
func startVirtualDisplay() (*exec.Cmd, string, error) {
	path, err := exec.LookPath("Xvfb")
	if err != nil {
		if command := runtimeInstallCommand(); command != "" {
			return nil, "", fmt.Errorf("没有 DISPLAY 时 Camoufox 需要 Xvfb，执行 %s 后重试: %w", command, err)
		}
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
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = writer.Close()
		return nil, "", err
	}
	err = command.Start()
	_ = writer.Close()
	if err != nil {
		return nil, "", fmt.Errorf("启动 Xvfb: %w", err)
	}
	output := &browserOutput{}
	drained := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			output.add(scanner.Text())
		}
		close(drained)
	}()
	number, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil {
		<-drained
		stopVirtualDisplay(command)
		return nil, "", fmt.Errorf("Xvfb 在报告显示号前退出：%s", strings.Join(output.lines, "\n"))
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

// requestedLocale 返回指纹 locale:language 与 locale:region 组成的界面语言
func requestedLocale(config map[string]any) string {
	language, _ := config["locale:language"].(string)
	if region, _ := config["locale:region"].(string); region != "" {
		return language + "-" + region
	}
	return language
}

func firefoxPreferences(proxyValue, bypass string) (map[string]any, error) {
	prefs := map[string]any{
		"remote.active-protocols":           1,
		"devtools.chrome.enabled":           true,
		"devtools.debugger.remote-enabled":  true,
		"browser.shell.checkDefaultBrowser": false,
		"webgl.force-enabled":               true,
		// extensions.webextensions.remote 为 false 时内置扩展在主进程运行，不单独启动扩展进程
		"extensions.webextensions.remote": false,
		// network.process.enabled 为 false 时网络在主进程运行，不单独启动 socket 进程
		"network.process.enabled": false,
		// network.http.http2.ping-threshold 为 HTTP/2 连接没有收到数据多少秒后发送 PING 探测连接是否失效
		"network.http.http2.ping-threshold": 10,
		// media.rdd-process.enabled 与 media.utility-process.enabled 为 false 时不单独启动 rdd 与媒体 utility 进程
		"media.rdd-process.enabled":     false,
		"media.utility-process.enabled": false,
		// javascript.options.mem.nursery.max_kb 为 SpiderMonkey 新生代的上限
		"javascript.options.mem.nursery.max_kb": 4096,
		// gfx.webrender.max-shared-surface-size 为 WebRender 共享渲染目标的边长上限
		"gfx.webrender.max-shared-surface-size": 512,
		// media.sanity-test.disabled 为 true 时全新 profile 启动跳过图形自检窗口
		"media.sanity-test.disabled": true,
		// browser.opaqueResponseBlocking.javascriptValidator 为 false 时不单独启动校验不透明响应的 JS 校验 utility 进程
		"browser.opaqueResponseBlocking.javascriptValidator": false,
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
