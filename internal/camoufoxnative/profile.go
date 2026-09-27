package camoufoxnative

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

// profilePrefix 为本服务创建的 Camoufox 临时 profile 目录名前缀
const profilePrefix = "aistudio-camoufox-"

// profileLockName 为 profile 内由创建进程持有的锁文件
const profileLockName = ".aistudio2api-profile.lock"

// createProfile 创建临时 profile 并在浏览器生命周期内持有其锁文件
func createProfile() (string, *flock.Flock, error) {
	profile, err := os.MkdirTemp("", profilePrefix+"*")
	if err != nil {
		return "", nil, fmt.Errorf("创建 Camoufox profile: %w", err)
	}
	lock := flock.New(filepath.Join(profile, profileLockName))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		_ = os.RemoveAll(profile)
		return "", nil, errors.Join(fmt.Errorf("锁定 Camoufox profile"), err)
	}
	return profile, lock, nil
}

// removeProfile 释放 profile 锁后删除目录，文件仍被占用时在 2 秒内重试
func removeProfile(profile string, lock *flock.Flock) error {
	if lock != nil {
		if err := lock.Unlock(); err != nil {
			return fmt.Errorf("释放 Camoufox profile 锁: %w", err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := os.RemoveAll(profile)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// RemoveStaleProfiles 删除创建进程已退出的临时 profile 并返回删除数量
func RemoveStaleProfiles() (int, error) {
	root := os.TempDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, fmt.Errorf("读取临时目录: %w", err)
	}
	removed := 0
	var removeErrors []error
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), profilePrefix) {
			continue
		}
		profile := filepath.Join(root, entry.Name())
		lockPath := filepath.Join(profile, profileLockName)
		if _, err := os.Stat(lockPath); err != nil {
			continue
		}
		lock := flock.New(lockPath)
		locked, err := lock.TryLock()
		if err != nil || !locked {
			continue
		}
		if err := removeProfile(profile, lock); err != nil {
			removeErrors = append(removeErrors, fmt.Errorf("删除遗留 Camoufox profile %s: %w", profile, err))
			continue
		}
		removed++
	}
	return removed, errors.Join(removeErrors...)
}
