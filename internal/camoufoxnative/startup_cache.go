package camoufoxnative

import (
	"errors"
	"io"
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
		if _, err := os.Stat(filepath.Join(source, name)); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		err := fs.WalkDir(os.DirFS(source), name, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			return copyStartupFile(filepath.Join(source, path), filepath.Join(target, path))
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// copyStartupFile 以流式读写复制单个启动缓存文件并创建目标目录
func copyStartupFile(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	target, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		return err
	}
	return target.Close()
}
