package camoufoxnative

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// FindExecutable 定位源码环境或 Release 自带的 Camoufox
func FindExecutable(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if configured := strings.TrimSpace(os.Getenv("CAMOUFOX_PATH")); configured != "" {
		return validateCamoufoxExecutable(configured)
	}
	name, err := camoufoxExecutablePath()
	if err != nil {
		return "", err
	}
	candidates := []string{filepath.Join("runtime", "camoufox", name)}
	if executable, executableErr := os.Executable(); executableErr == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), "runtime", "camoufox", name))
	}
	if runtime.GOOS == "windows" {
		if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
			candidates = append(candidates, filepath.Join(localAppData, "camoufox", "camoufox", "Cache", name))
		}
	}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		path, err := validateCamoufoxExecutable(candidate)
		if err == nil {
			return path, nil
		}
	}
	path, err := installCamoufox(ctx, name)
	if err != nil {
		return "", fmt.Errorf("自动准备 Camoufox: %w", err)
	}
	return path, nil
}

func camoufoxExecutablePath() (string, error) {
	switch runtime.GOOS {
	case "windows":
		return "camoufox.exe", nil
	case "linux":
		return "camoufox-bin", nil
	case "darwin":
		return filepath.Join("Camoufox.app", "Contents", "MacOS", "camoufox"), nil
	default:
		return "", fmt.Errorf("Camoufox 不支持 %s", runtime.GOOS)
	}
}

func validateCamoufoxExecutable(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("解析 Camoufox 路径: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("Camoufox 路径是目录")
	}
	return absolute, nil
}
