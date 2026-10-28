package camoufoxnative

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// profileStartupDirectoryName 为账户缓存目录下保存 Firefox 启动缓存的子目录
const profileStartupDirectoryName = "profile-startup"

// startupCacheEntries 为 profile 中同一 Camoufox 安装的新 profile 可复用的启动缓存
var startupCacheEntries = []string{"compatibility.ini", "startupCache"}

// restoreStartupCache 把账户缓存目录中保存的启动缓存复制到新建的 profile，复制失败时 Firefox 重新生成
func restoreStartupCache(cacheDirectory, profile string) {
	_ = copyStartupCache(filepath.Join(cacheDirectory, profileStartupDirectoryName), profile)
}

// saveStartupCache 用浏览器退出后 profile 中的启动缓存替换账户缓存目录中保存的副本
func saveStartupCache(profile, cacheDirectory string) {
	if _, err := os.Stat(filepath.Join(profile, "startupCache")); err != nil {
		return
	}
	target := filepath.Join(cacheDirectory, profileStartupDirectoryName)
	staging := target + ".new"
	_ = os.RemoveAll(staging)
	if err := copyStartupCache(profile, staging); err != nil {
		_ = os.RemoveAll(staging)
		return
	}
	_ = os.RemoveAll(target)
	_ = os.Rename(staging, target)
}

// copyStartupCache 把 source 中存在的启动缓存条目复制到 target
func copyStartupCache(source, target string) error {
	for _, name := range startupCacheEntries {
		from := filepath.Join(source, name)
		info, err := os.Stat(from)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		to := filepath.Join(target, name)
		if info.IsDir() {
			err = os.CopyFS(to, os.DirFS(from))
		} else {
			err = copyStartupFile(from, to)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// copyStartupFile 复制单个启动缓存文件并创建目标目录
func copyStartupFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o600)
}
