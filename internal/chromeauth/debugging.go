package chromeauth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/process"
)

const (
	// firefoxDefaultDebugPort 是 Firefox 仅传 --remote-debugging-port 不给端口值时的默认端口
	firefoxDefaultDebugPort = "9222"
	// ownCamoufoxProfilePrefix 与 ownCamoufoxProfileLock 标识本服务自建的 Camoufox 临时 profile，与 camoufoxnative 保持一致
	ownCamoufoxProfilePrefix = "aistudio-camoufox-"
	ownCamoufoxProfileLock   = ".aistudio2api-profile.lock"
)

// DebugAddresses 从本机进程命令行找出开启远程调试端口的浏览器（Chromium 与 Firefox 内核），返回去重排序后的 127.0.0.1:端口
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

// debugAddress 从浏览器进程参数取出远程调试地址，端口为 0 时读取 Chromium 的 DevToolsActivePort 或 Firefox 的 WebDriverBiDiServer.json，并跳过本服务自建的 Camoufox profile
func debugAddress(args []string) string {
	port, dataDirectory, profileDirectory := "", "", ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--remote-debugging-port="):
			port = strings.TrimPrefix(arg, "--remote-debugging-port=")
		case strings.HasPrefix(arg, "-remote-debugging-port="):
			port = strings.TrimPrefix(arg, "-remote-debugging-port=")
		case arg == "--remote-debugging-port" || arg == "-remote-debugging-port":
			if i+1 < len(args) && isPortArg(args[i+1]) {
				i++
				port = args[i]
			} else {
				port = firefoxDefaultDebugPort
			}
		case strings.HasPrefix(arg, "--user-data-dir="):
			dataDirectory = strings.Trim(strings.TrimPrefix(arg, "--user-data-dir="), `"`)
		case arg == "--user-data-dir" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-"):
			i++
			dataDirectory = strings.Trim(args[i], `"`)
		case strings.HasPrefix(arg, "--profile="):
			profileDirectory = strings.Trim(strings.TrimPrefix(arg, "--profile="), `"`)
		case strings.HasPrefix(arg, "-profile="):
			profileDirectory = strings.Trim(strings.TrimPrefix(arg, "-profile="), `"`)
		case (arg == "--profile" || arg == "-profile") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-"):
			i++
			profileDirectory = strings.Trim(args[i], `"`)
		}
	}
	if profileDirectory != "" && isOwnCamoufoxProfile(profileDirectory) {
		return ""
	}
	if port == "0" {
		switch {
		case dataDirectory != "":
			data, err := os.ReadFile(filepath.Join(dataDirectory, "DevToolsActivePort"))
			if err != nil {
				return ""
			}
			port, _, _ = strings.Cut(string(data), "\n")
		case profileDirectory != "":
			port = readBiDiActivePort(profileDirectory)
		}
	}
	port = strings.TrimSpace(port)
	if number, err := strconv.Atoi(port); err != nil || number <= 0 || number > 65535 {
		return ""
	}
	return "127.0.0.1:" + port
}

// isPortArg 报告参数是否为 0..65535 的端口字符串
func isPortArg(value string) bool {
	number, err := strconv.Atoi(strings.TrimSpace(value))
	return err == nil && number >= 0 && number <= 65535
}

// isOwnCamoufoxProfile 判断 profile 目录是否属于本服务启动的 Camoufox 进程
func isOwnCamoufoxProfile(directory string) bool {
	if strings.HasPrefix(filepath.Base(filepath.Clean(directory)), ownCamoufoxProfilePrefix) {
		return true
	}
	_, err := os.Stat(filepath.Join(directory, ownCamoufoxProfileLock))
	return err == nil
}

// readBiDiActivePort 从 Firefox profile 目录的 WebDriverBiDiServer.json 读取动态分配的 BiDi 端口
func readBiDiActivePort(directory string) string {
	data, err := os.ReadFile(filepath.Join(directory, "WebDriverBiDiServer.json"))
	if err != nil {
		return ""
	}
	var server struct {
		Port int `json:"ws_port"`
	}
	if json.Unmarshal(data, &server) != nil || server.Port <= 0 || server.Port > 65535 {
		return ""
	}
	return strconv.Itoa(server.Port)
}
