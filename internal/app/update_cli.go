//go:build !desktop

package app

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"sync/atomic"

	"github.com/minio/selfupdate"
)

// platformUpdateMode 返回命令行版的更新方式：原地替换可执行文件
func platformUpdateMode() string {
	return "binary"
}

// install 下载当前系统与架构的发行包，校验 SHA-256 后替换可执行文件
func (u *selfUpdater) install(ctx context.Context, release *githubRelease) error {
	name, binary := releasePackage(release.TagName)
	archive, err := u.download(ctx, release, name)
	if err != nil {
		return err
	}
	executable, err := extractExecutable(archive, name, binary)
	if err != nil {
		return err
	}
	u.log("INFO", "在线更新 | 2/2 | 替换可执行文件 | 版本="+release.TagName)
	if err := selfupdate.Apply(bytes.NewReader(executable), selfupdate.Options{TargetPath: u.executable}); err != nil {
		if rollback := selfupdate.RollbackError(err); rollback != nil {
			return fmt.Errorf("替换可执行文件: %w；恢复原文件失败: %v", err, rollback)
		}
		return fmt.Errorf("替换可执行文件: %w", err)
	}
	return nil
}

// updateRestart 返回在线更新完成后的重启动作：结束当前服务，由 Run 以原参数启动替换后的可执行文件
func updateRestart(stop func(), restarting *atomic.Bool, _ func(level, message string)) func() {
	return func() {
		restarting.Store(true)
		stop()
	}
}

// releasePackage 返回当前系统与架构的发行包文件名与其中的可执行文件名
func releasePackage(tag string) (string, string) {
	base := fmt.Sprintf("aistudio2api-%s-%s-%s", tag, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		return base + ".zip", "aistudio2api.exe"
	}
	return base + ".tar.gz", "aistudio2api"
}
