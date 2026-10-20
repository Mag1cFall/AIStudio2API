//go:build desktop

package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/updater"

	"github.com/Mag1cFall/AIStudio2API/internal/desktop"
)

// platformUpdateMode 返回桌面版的更新方式：Windows 与 macOS 原地更新，Linux 桌面包下载新版本安装
func platformUpdateMode() string {
	if runtime.GOOS == "linux" {
		return "download"
	}
	return "binary"
}

// install 经 Wails 更新器暂存 release 的桌面版，重启时由更新器的辅助进程替换文件
func (u *selfUpdater) install(ctx context.Context, release *githubRelease) error {
	return desktop.Install(ctx, strings.TrimPrefix(u.version, "v"), &releaseProvider{updater: u, release: release})
}

// updateRestart 返回在线更新完成后的重启动作：更新器启动辅助进程并退出桌面应用，辅助进程替换文件后启动新版本
func updateRestart(_ func(), _ *atomic.Bool, log func(level, message string)) func() {
	return func() {
		go func() {
			if err := desktop.Restart(); err != nil {
				log("ERROR", "在线更新重启失败 | 错误="+err.Error())
			}
		}()
	}
}

// releaseProvider 把已读取的 GitHub 发行版本交给 Wails 更新器
type releaseProvider struct {
	updater *selfUpdater
	release *githubRelease
}

// Name 返回更新来源名称
func (p *releaseProvider) Name() string { return "github" }

// Check 返回发行版本中当前平台的桌面版：Windows 为发行包中的可执行文件，macOS 为只含 AIStudio2API.app 的 zip
func (p *releaseProvider) Check(context.Context, updater.CheckRequest) (*updater.Release, error) {
	name, binary := desktopPackage(p.release.TagName)
	if binary != "" {
		name = binary
	}
	return &updater.Release{
		Version:     strings.TrimPrefix(p.release.TagName, "v"),
		Notes:       p.release.Body,
		PublishedAt: p.release.PublishedAt,
		Artifact:    updater.Artifact{Filename: name, Platform: runtime.GOOS, Arch: runtime.GOARCH},
	}, nil
}

// Download 下载并按发行版本记录的 SHA-256 校验发行包，Windows 写出其中的可执行文件，macOS 写出整个 zip
func (p *releaseProvider) Download(ctx context.Context, _ *updater.Release, dst io.Writer, onProgress func(written, total int64)) error {
	name, binary := desktopPackage(p.release.TagName)
	archive, err := p.updater.download(ctx, p.release, name)
	if err != nil {
		return err
	}
	payload := archive
	if binary != "" {
		if payload, err = extractExecutable(archive, name, binary); err != nil {
			return err
		}
	}
	p.updater.log("INFO", "在线更新 | 2/2 | 暂存新版本 | 版本="+p.release.TagName)
	written, err := io.Copy(dst, bytes.NewReader(payload))
	onProgress(written, int64(len(payload)))
	if err != nil {
		return fmt.Errorf("暂存新版本: %w", err)
	}
	return nil
}

// desktopPackage 返回当前平台桌面版的发行包文件名与需要从中取出的可执行文件名，macOS 整包交给更新器解开
func desktopPackage(tag string) (string, string) {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("aistudio2api-%s-windows-%s.zip", tag, runtime.GOARCH), "aistudio2api.exe"
	}
	return fmt.Sprintf("aistudio2api-%s-darwin-universal.zip", tag), ""
}
