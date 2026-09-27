package camoufoxnative

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// accountCacheDirectoryName 为账户目录下保存 Camoufox HTTP 磁盘缓存的子目录
const accountCacheDirectoryName = "camoufox-cache"

// accountCacheCapacityKB 为单个账户 HTTP 磁盘缓存的容量上限
const accountCacheCapacityKB = 262144

// lockAccountCache 独占账户 HTTP 磁盘缓存目录，同账户另一个 runtime 占用时返回空目录
func lockAccountCache(storageStatePath string) (string, *flock.Flock, error) {
	directory := filepath.Join(filepath.Dir(storageStatePath), accountCacheDirectoryName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", nil, fmt.Errorf("创建账户浏览器缓存目录: %w", err)
	}
	lock := flock.New(filepath.Join(directory, ".lock"))
	locked, err := lock.TryLock()
	if err != nil {
		return "", nil, fmt.Errorf("锁定账户浏览器缓存: %w", err)
	}
	if !locked {
		return "", nil, nil
	}
	return directory, lock, nil
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
	}
}
