package chromeauth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/process"
)

// DebugAddresses 从本机进程命令行找出开启远程调试端口的 Chromium 浏览器，返回去重排序后的 127.0.0.1:端口
func DebugAddresses(ctx context.Context) ([]string, error) {
	processes, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("列出本机进程: %w", err)
	}
	var addresses []string
	for _, item := range processes {
		args, err := item.CmdlineSliceWithContext(ctx)
		if err != nil {
			continue
		}
		if address := debugAddress(args); address != "" && !slices.Contains(addresses, address) {
			addresses = append(addresses, address)
		}
	}
	slices.Sort(addresses)
	return addresses, nil
}

// debugAddress 从浏览器进程参数取出远程调试地址，端口为 0 时读取用户数据目录中的 DevToolsActivePort
func debugAddress(args []string) string {
	port, dataDirectory := "", ""
	for _, arg := range args {
		if value, ok := strings.CutPrefix(arg, "--remote-debugging-port="); ok {
			port = value
		}
		if value, ok := strings.CutPrefix(arg, "--user-data-dir="); ok {
			dataDirectory = strings.Trim(value, `"`)
		}
	}
	if port == "0" && dataDirectory != "" {
		data, err := os.ReadFile(filepath.Join(dataDirectory, "DevToolsActivePort"))
		if err != nil {
			return ""
		}
		port, _, _ = strings.Cut(string(data), "\n")
	}
	port = strings.TrimSpace(port)
	if number, err := strconv.Atoi(port); err != nil || number <= 0 || number > 65535 {
		return ""
	}
	return "127.0.0.1:" + port
}

// DebugBrowserName 返回远程调试地址对应浏览器的产品名与版本
func DebugBrowserName(ctx context.Context, address string) (string, error) {
	version, err := debugVersion(ctx, address)
	if err != nil {
		return "", err
	}
	return version.Browser, nil
}
