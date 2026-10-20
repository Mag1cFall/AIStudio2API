package camoufoxnative

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// FindExecutable 定位与 camoufoxRelease 同版本的 Camoufox，都没有时经 proxy 下载，proxy 为空时直连
func FindExecutable(ctx context.Context, proxy string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if configured := strings.TrimSpace(os.Getenv("CAMOUFOX_PATH")); configured != "" {
		path, err := validateCamoufoxExecutable(configured)
		if err != nil {
			return "", fmt.Errorf("CAMOUFOX_PATH: %w", err)
		}
		return path, nil
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
	path, err := installCamoufox(ctx, name, proxy)
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
	version, err := camoufoxVersion(absolute)
	if err != nil {
		return "", err
	}
	if version != camoufoxRelease {
		return "", fmt.Errorf("Camoufox 版本 %s 与要求的 %s 不一致", version, camoufoxRelease)
	}
	return absolute, nil
}

// camoufoxVersion 读取发行目录 application.ini 中的 Camoufox 版本
func camoufoxVersion(executable string) (string, error) {
	directory := filepath.Dir(executable)
	for _, candidate := range []string{
		filepath.Join(directory, "application.ini"),
		filepath.Join(filepath.Dir(directory), "Resources", "application.ini"),
	} {
		data, err := os.ReadFile(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("读取 Camoufox 版本: %w", err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if version, found := strings.CutPrefix(strings.TrimSpace(line), "Version="); found {
				return strings.TrimSpace(version), nil
			}
		}
	}
	return "", fmt.Errorf("Camoufox 缺少 application.ini 版本")
}
