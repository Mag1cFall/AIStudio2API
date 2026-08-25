package main

import (
	"archive/zip"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const camoufoxRelease = "152.0.4-beta.29"

// installCamoufox 下载当前协议传输已对齐的 Camoufox 版本
func installCamoufox(executableName string) (string, error) {
	asset, err := camoufoxAssetName()
	if err != nil {
		return "", err
	}
	root, err := camoufoxInstallRoot()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("创建 Camoufox 目录: %w", err)
	}
	archive, err := os.CreateTemp(filepath.Dir(root), "camoufox-*.zip")
	if err != nil {
		return "", fmt.Errorf("创建 Camoufox 下载文件: %w", err)
	}
	archivePath := archive.Name()
	defer os.Remove(archivePath)
	url := fmt.Sprintf("https://github.com/daijro/camoufox/releases/download/v%s/%s", camoufoxRelease, asset)
	slog.Info("正在下载 Camoufox", "version", camoufoxRelease, "platform", runtime.GOOS+"/"+runtime.GOARCH)
	client := &http.Client{Timeout: 30 * time.Minute}
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		archive.Close()
		return "", err
	}
	request.Header.Set("User-Agent", "AIStudio2API")
	response, err := client.Do(request)
	if err != nil {
		archive.Close()
		return "", fmt.Errorf("下载 Camoufox: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		archive.Close()
		return "", fmt.Errorf("下载 Camoufox: HTTP %d", response.StatusCode)
	}
	if response.ContentLength > 0 {
		slog.Info("Camoufox 下载已开始", "size_mib", response.ContentLength/(1024*1024))
	}
	_, copyErr := io.Copy(archive, response.Body)
	closeErr := response.Body.Close()
	archiveCloseErr := archive.Close()
	if copyErr != nil || closeErr != nil || archiveCloseErr != nil {
		return "", fmt.Errorf("保存 Camoufox: %w", firstError(copyErr, closeErr, archiveCloseErr))
	}
	if err := extractCamoufoxArchive(archivePath, root); err != nil {
		return "", err
	}
	executable := filepath.Join(root, executableName)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(executable, 0o755); err != nil {
			return "", fmt.Errorf("设置 Camoufox 执行权限: %w", err)
		}
	}
	slog.Info("Camoufox 已就绪", "path", executable)
	return executable, nil
}

func camoufoxInstallRoot() (string, error) {
	root, err := filepath.Abs(filepath.Join("runtime", "camoufox"))
	if err != nil {
		return "", fmt.Errorf("定位 Camoufox 目录: %w", err)
	}
	return root, nil
}

func camoufoxAssetName() (string, error) {
	platform := map[string]string{"windows": "win", "linux": "lin", "darwin": "mac"}[runtime.GOOS]
	architecture := map[string]string{"amd64": "x86_64", "386": "i686", "arm64": "arm64"}[runtime.GOARCH]
	if platform == "" || architecture == "" || runtime.GOOS == "darwin" && runtime.GOARCH == "386" {
		return "", fmt.Errorf("Camoufox 没有 %s/%s 发行包", runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Sprintf("camoufox-%s-%s.%s.zip", camoufoxRelease, platform, architecture), nil
}

func extractCamoufoxArchive(archivePath string, destination string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("打开 Camoufox 压缩包: %w", err)
	}
	defer archive.Close()
	for _, entry := range archive.File {
		target := filepath.Join(destination, filepath.FromSlash(entry.Name))
		relative, err := filepath.Rel(destination, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("Camoufox 压缩包包含无效路径 %q", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, entry.Mode()); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		source, err := entry.Open()
		if err != nil {
			return err
		}
		targetFile, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, entry.Mode())
		if err != nil {
			source.Close()
			return err
		}
		_, copyErr := io.Copy(targetFile, source)
		closeTargetErr := targetFile.Close()
		closeSourceErr := source.Close()
		if copyErr != nil || closeTargetErr != nil || closeSourceErr != nil {
			return fmt.Errorf("解压 Camoufox %s: %w", entry.Name, firstError(copyErr, closeTargetErr, closeSourceErr))
		}
	}
	return nil
}

func firstError(values ...error) error {
	for _, err := range values {
		if err != nil {
			return err
		}
	}
	return nil
}
