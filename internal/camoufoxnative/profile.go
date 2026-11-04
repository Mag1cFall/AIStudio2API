package camoufoxnative

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/shirou/gopsutil/v4/process"
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

// RemoveStaleProfiles 结束仍在使用创建进程已退出的临时 profile 的进程树，再删除这些 profile 并返回删除数量
func RemoveStaleProfiles() (int, error) {
	root := os.TempDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, fmt.Errorf("读取临时目录: %w", err)
	}
	stale := map[string]*flock.Flock{}
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
		if locked, err := lock.TryLock(); err == nil && locked {
			stale[profile] = lock
		}
	}
	if len(stale) == 0 {
		return 0, nil
	}
	err = endProfileProcesses(stale)
	removed := 0
	for profile, lock := range stale {
		if removeErr := removeProfile(profile, lock); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("删除遗留 Camoufox profile %s: %w", profile, removeErr))
			continue
		}
		removed++
	}
	return removed, err
}

// endProfileProcesses 结束命令行以 -profile 指向 profiles 中目录的进程及其全部子孙进程
func endProfileProcesses(profiles map[string]*flock.Flock) error {
	processes, err := process.Processes()
	if err != nil {
		return fmt.Errorf("列出进程: %w", err)
	}
	var targets []*process.Process
	for _, candidate := range processes {
		arguments, err := candidate.CmdlineSlice()
		if err != nil {
			continue
		}
		if index := slices.Index(arguments, "-profile"); index >= 0 && index+1 < len(arguments) && profiles[arguments[index+1]] != nil {
			targets = append(targets, candidate)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	children := map[int32][]*process.Process{}
	for _, candidate := range processes {
		if parent, err := candidate.Ppid(); err == nil {
			children[parent] = append(children[parent], candidate)
		}
	}
	for index := 0; index < len(targets); index++ {
		targets = append(targets, children[targets[index].Pid]...)
	}
	var killErrors []error
	for _, target := range targets {
		if err := target.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			killErrors = append(killErrors, fmt.Errorf("结束遗留 Camoufox 进程 %d: %w", target.Pid, err))
		}
	}
	return errors.Join(killErrors...)
}
