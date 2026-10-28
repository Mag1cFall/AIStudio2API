//go:build desktop

package desktop

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// autostartEntry 返回 XDG 自动启动目录中本应用的桌面项路径
func autostartEntry() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "autostart", "aistudio2api.desktop"), nil
}

// launcher 返回登录时启动的程序：从 AppImage 运行时为 AppImage 文件，否则为当前可执行文件
func launcher() (string, error) {
	if image := os.Getenv("APPIMAGE"); image != "" {
		return image, nil
	}
	return os.Executable()
}

// autostartEnabled 返回自动启动目录中的桌面项是否在登录时启动当前程序
func (s *App) autostartEnabled() (bool, error) {
	path, err := autostartEntry()
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	program, err := launcher()
	if err != nil {
		return false, err
	}
	return entryLaunches(string(data), program), nil
}

// setAutostart 开启时写入以 -open-ui=false 启动当前程序的桌面项，关闭时删除它
func (s *App) setAutostart(enabled bool) error {
	path, err := autostartEntry()
	if err != nil {
		return err
	}
	if !enabled {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	program, err := launcher()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	entry := "[Desktop Entry]\nType=Application\nName=AIStudio2API\nExec=" + quoteExec(program) + " -open-ui=false\nIcon=aistudio2api\nTerminal=false\nX-GNOME-Autostart-enabled=true\n"
	return os.WriteFile(path, []byte(entry), 0o644)
}

// entryLaunches 返回桌面项是否在登录时启动 program：Exec 的程序为 program，且没有以 Hidden=true 或 X-GNOME-Autostart-enabled=false 停用
func entryLaunches(entry, program string) bool {
	launches := false
	for line := range strings.Lines(entry) {
		key, value, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch {
		case key == "Exec":
			launches = execProgram(value) == program
		case key == "Hidden" && value == "true", key == "X-GNOME-Autostart-enabled" && value == "false":
			return false
		}
	}
	return launches
}

// quoteExec 按桌面项规范以双引号括起 Exec 的参数，并转义其中的保留字符
func quoteExec(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`).Replace(value) + `"`
}

// execProgram 返回 Exec 中的程序路径，带双引号时去掉引号并还原转义的字符
func execProgram(exec string) string {
	rest, quoted := strings.CutPrefix(exec, `"`)
	if !quoted {
		program, _, _ := strings.Cut(exec, " ")
		return program
	}
	var program strings.Builder
	for index := 0; index < len(rest); index++ {
		switch rest[index] {
		case '"':
			return program.String()
		case '\\':
			index++
			if index == len(rest) {
				return program.String()
			}
		}
		program.WriteByte(rest[index])
	}
	return program.String()
}
