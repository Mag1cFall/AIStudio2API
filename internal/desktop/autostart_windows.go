//go:build desktop

package desktop

import (
	"errors"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// autostartName 为开机自启动在 Run 与 StartupApproved\Run 中的值名
const autostartName = "AIStudio2API"

// runKey 为当前用户登录时启动的程序，approvedKey 为任务管理器与系统设置对这些程序的启用状态
const (
	runKey      = `Software\Microsoft\Windows\CurrentVersion\Run`
	approvedKey = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`
)

// autostartEnabled 返回登录时是否启动当前可执行文件：Run 中的命令指向它，且任务管理器或系统设置没有停用它
func (s *App) autostartEnabled() (bool, error) {
	executable, err := os.Executable()
	if err != nil {
		return false, err
	}
	command, err := readValue(runKey, func(key registry.Key) (string, error) {
		value, _, err := key.GetStringValue(autostartName)
		return value, err
	})
	if err != nil || !strings.EqualFold(commandExecutable(command), executable) {
		return false, ignoreMissing(err)
	}
	state, err := readValue(approvedKey, func(key registry.Key) ([]byte, error) {
		value, _, err := key.GetBinaryValue(autostartName)
		return value, err
	})
	if errors.Is(err, registry.ErrNotExist) {
		return true, nil
	}
	return err == nil && !startupDisabled(state), err
}

// setAutostart 开启时在 Run 中写入以 -open-ui=false 启动当前可执行文件的命令，关闭时删除它；两种情况都删除 StartupApproved\Run 中的启用状态
func (s *App) setAutostart(enabled bool) error {
	if approved, err := registry.OpenKey(registry.CURRENT_USER, approvedKey, registry.SET_VALUE); err == nil {
		err = approved.DeleteValue(autostartName)
		approved.Close()
		if ignoreMissing(err) != nil {
			return err
		}
	}
	run, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer run.Close()
	if !enabled {
		return ignoreMissing(run.DeleteValue(autostartName))
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	return run.SetStringValue(autostartName, `"`+executable+`" -open-ui=false`)
}

// readValue 打开当前用户的注册表键 path 并以 read 读取其中的值
func readValue[T any](path string, read func(registry.Key) (T, error)) (T, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		var zero T
		return zero, err
	}
	defer key.Close()
	return read(key)
}

// ignoreMissing 把键或值不存在视为没有错误
func ignoreMissing(err error) error {
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}

// commandExecutable 返回命令行中的可执行文件路径，路径带空格时由双引号括起
func commandExecutable(command string) string {
	if rest, quoted := strings.CutPrefix(command, `"`); quoted {
		path, _, _ := strings.Cut(rest, `"`)
		return path
	}
	path, _, _ := strings.Cut(command, " ")
	return path
}

// startupDisabled 返回 StartupApproved 中的启用状态是否为停用，首字节最低位为 1 时停用
func startupDisabled(state []byte) bool {
	return len(state) > 0 && state[0]&1 == 1
}
