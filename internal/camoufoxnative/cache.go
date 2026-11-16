package camoufoxnative

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

// accountCacheDirectoryName 为账户目录下保存 Camoufox HTTP 磁盘缓存的子目录
const accountCacheDirectoryName = "camoufox-cache"

// accountCacheCapacityKB 为单个账户 HTTP 磁盘缓存的容量上限
const accountCacheCapacityKB = 32768

// retiringCaches 是在后台关闭中的 Worker 仍占用的账户 HTTP 磁盘缓存目录
var retiringCaches sync.Map

// cacheLockPoll 是等后台关闭中的 Worker 释放账户缓存时重试上锁的间隔
const cacheLockPoll = 50 * time.Millisecond

// lockAccountCache 独占账户 HTTP 磁盘缓存目录，同账户另一个 runtime 占用时返回空目录；目录由在后台关闭中的 Worker 占用时返回该目录与尚未上锁的锁，pending 为 true，调用方在浏览器载入页面前等它上锁
func lockAccountCache(storageStatePath string) (string, *flock.Flock, bool, error) {
	directory := filepath.Join(filepath.Dir(storageStatePath), accountCacheDirectoryName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", nil, false, fmt.Errorf("创建账户浏览器缓存目录: %w", err)
	}
	lock := flock.New(filepath.Join(directory, ".lock"))
	locked, err := lock.TryLock()
	if err != nil {
		return "", nil, false, fmt.Errorf("锁定账户浏览器缓存: %w", err)
	}
	if locked {
		return directory, lock, false, nil
	}
	if _, retiring := retiringCaches.Load(directory); retiring {
		return directory, lock, true, nil
	}
	return "", nil, false, nil
}

// RemoveAccountCache 删除账户目录下的 Camoufox HTTP 磁盘缓存与启动缓存，缓存正被另一个浏览器使用时保留
func RemoveAccountCache(storageStatePath string) error {
	directory := filepath.Join(filepath.Dir(storageStatePath), accountCacheDirectoryName)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取账户浏览器缓存: %w", err)
	}
	lock := flock.New(filepath.Join(directory, ".lock"))
	locked, err := lock.TryLock()
	if err != nil {
		return fmt.Errorf("锁定账户浏览器缓存: %w", err)
	}
	if !locked {
		return nil
	}
	for _, entry := range entries {
		if entry.Name() == ".lock" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(directory, entry.Name())); err != nil {
			_ = lock.Unlock()
			return fmt.Errorf("删除账户浏览器缓存: %w", err)
		}
	}
	if err := lock.Unlock(); err != nil {
		return fmt.Errorf("释放账户浏览器缓存: %w", err)
	}
	if err := os.RemoveAll(directory); err != nil {
		return fmt.Errorf("删除账户浏览器缓存: %w", err)
	}
	return nil
}

// releaseAccountCache 在浏览器进程退出后释放账户 HTTP 磁盘缓存目录
func releaseAccountCache(lock *flock.Flock) error {
	if lock == nil {
		return nil
	}
	if err := lock.Unlock(); err != nil {
		return fmt.Errorf("释放账户浏览器缓存: %w", err)
	}
	return nil
}

// cachePreferences 返回把 HTTP 磁盘缓存放到指定目录的 Firefox 偏好
func cachePreferences(directory string) map[string]any {
	return map[string]any{
		"browser.cache.disk.parent_directory":   directory,
		"browser.cache.disk.smart_size.enabled": false,
		"browser.cache.disk.capacity":           accountCacheCapacityKB,
		// browser.cache.jsbc_compression_level 为写入缓存的 JS 字节码的 zlib 压缩级别
		"browser.cache.jsbc_compression_level": 1,
	}
}
