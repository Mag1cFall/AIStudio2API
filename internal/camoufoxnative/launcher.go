package camoufoxnative

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var bidiEndpointPattern = regexp.MustCompile(`ws://[^\s]+`)

const browserProcessCloseTimeout = 5 * time.Second

type browserProcessTerminator func(context.Context, *exec.Cmd) error

type browserProcess struct {
	command *exec.Cmd
	done    chan struct{}
	waitErr error
	profile string
	mu      sync.Mutex
	closed  bool
}

// launchBrowser 启动 Camoufox 并返回原生 WebDriver BiDi 端点
func launchBrowser(ctx context.Context, options Options, config map[string]any) (*browserProcess, string, error) {
	if _, err := os.Stat(options.ExecutablePath); err != nil {
		return nil, "", fmt.Errorf("Camoufox 不可用: %w", err)
	}
	environment, err := camoufoxEnvironment(config)
	if err != nil {
		return nil, "", err
	}
	profile, err := os.MkdirTemp("", "aistudio-camoufox-*")
	if err != nil {
		return nil, "", fmt.Errorf("创建 Camoufox profile: %w", err)
	}
	prefs, err := firefoxPreferences(options.Proxy, options.ProxyBypass)
	if err != nil {
		_ = os.RemoveAll(profile)
		return nil, "", err
	}
	if err := writeUserJS(profile, prefs); err != nil {
		_ = os.RemoveAll(profile)
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
		_ = os.RemoveAll(profile)
		return nil, "", err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = os.RemoveAll(profile)
		return nil, "", err
	}
	if err := command.Start(); err != nil {
		_ = os.RemoveAll(profile)
		return nil, "", fmt.Errorf("启动 Camoufox: %w", err)
	}
	process := &browserProcess{
		command: command,
		done:    make(chan struct{}),
		profile: profile,
	}
	go func() {
		waitErr := command.Wait()
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
		process.closed = true
		process.mu.Unlock()
		_ = os.RemoveAll(profile)
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
	var closeErr error
	select {
	case <-process.done:
	default:
		closeErr = terminate(closeCtx, process.command)
		select {
		case <-process.done:
		case <-closeCtx.Done():
			closeErr = errors.Join(closeErr, fmt.Errorf("等待 Camoufox 进程退出: %w", closeCtx.Err()))
		}
	}
	closeErr = errors.Join(closeErr, removeProfile(process.profile))
	if closeErr != nil {
		return closeErr
	}
	process.mu.Lock()
	process.closed = true
	process.mu.Unlock()
	return nil
}

func removeProfile(profile string) error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := os.RemoveAll(profile)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
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

func firefoxPreferences(proxyValue, bypass string) (map[string]any, error) {
	prefs := map[string]any{
		"remote.active-protocols":           1,
		"devtools.chrome.enabled":           true,
		"devtools.debugger.remote-enabled":  true,
		"browser.shell.checkDefaultBrowser": false,
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
