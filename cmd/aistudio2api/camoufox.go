package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// findCamoufoxExecutable 定位源码环境或 Release 自带的 Camoufox
func findCamoufoxExecutable() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("CAMOUFOX_PATH")); configured != "" {
		return validateCamoufoxExecutable(configured)
	}
	name := "camoufox"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	candidates := []string{filepath.Join("runtime", "camoufox", name)}
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), "runtime", "camoufox", name))
	}
	if runtime.GOOS == "windows" {
		if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
			candidates = append(candidates, filepath.Join(localAppData, "camoufox", "camoufox", "Cache", name))
		}
	}
	for _, candidate := range candidates {
		path, err := validateCamoufoxExecutable(candidate)
		if err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("未找到 Camoufox，请将浏览器放到 runtime/camoufox")
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
