package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

// prepareWAABackend 按 WAA 后端准备浏览器依赖，go 后端只在登录账户时按需准备 Camoufox
func prepareWAABackend(ctx context.Context, cfg config.Config, requests *requestRegistry, accounts int) (string, aistudio.IsolatedLoginDriver, error) {
	if cfg.WAABackend == config.WAABackendGo {
		requests.log("service", "INFO", fmt.Sprintf("运行时装配 | 2/3 | WAA 后端=go | 账户=%d", accounts))
		return "", &lazyLoginDriver{timeout: cfg.RequestTimeout}, nil
	}
	requests.log("service", "INFO", fmt.Sprintf("运行时装配 | 2/3 | 校验 Camoufox | 账户=%d", accounts))
	camoufoxPath, err := camoufoxnative.FindExecutable(ctx)
	if err != nil {
		return "", nil, err
	}
	removeStaleCamoufoxProfiles(requests)
	login, err := aistudio.NewNativeLoginDriver(camoufoxPath, cfg.RequestTimeout)
	if err != nil {
		return "", nil, err
	}
	return camoufoxPath, login, nil
}

// removeStaleCamoufoxProfiles 清理上次进程遗留的 Camoufox profile
func removeStaleCamoufoxProfiles(requests *requestRegistry) {
	if removed, err := camoufoxnative.RemoveStaleProfiles(); err != nil {
		requests.log("service", "WARN", fmt.Sprintf(
			"遗留 Camoufox profile 清理失败 | 已删除=%d | 错误=%s", removed, strings.TrimSpace(err.Error()),
		))
	} else if removed > 0 {
		requests.log("service", "INFO", fmt.Sprintf("遗留 Camoufox profile 已清理 | 数量=%d", removed))
	}
}

// newWAAWorker 启动账户 WAA Worker，Worker 配置没有 Camoufox 路径时由纯 Go VM 承担
func newWAAWorker(ctx context.Context, accountID string, options camoufoxnative.Options) (*aistudio.NativeWorker, error) {
	if options.ExecutablePath == "" {
		return aistudio.NewGoWorker(ctx, accountID, options)
	}
	return aistudio.NewNativeWorker(ctx, accountID, options)
}

// lazyLoginDriver 在第一次登录或校验账户时定位 Camoufox 并创建登录驱动
type lazyLoginDriver struct {
	timeout time.Duration
	mu      sync.Mutex
	driver  *aistudio.NativeLoginDriver
}

func (lazy *lazyLoginDriver) resolve(ctx context.Context) (*aistudio.NativeLoginDriver, error) {
	lazy.mu.Lock()
	defer lazy.mu.Unlock()
	if lazy.driver != nil {
		return lazy.driver, nil
	}
	camoufoxPath, err := camoufoxnative.FindExecutable(ctx)
	if err != nil {
		return nil, err
	}
	driver, err := aistudio.NewNativeLoginDriver(camoufoxPath, lazy.timeout)
	if err != nil {
		return nil, err
	}
	lazy.driver = driver
	return driver, nil
}

// Login 使用按需准备的 Camoufox 执行隔离登录
func (lazy *lazyLoginDriver) Login(ctx context.Context, request aistudio.IsolatedLoginRequest) (aistudio.IsolatedLoginResult, error) {
	driver, err := lazy.resolve(ctx)
	if err != nil {
		return aistudio.IsolatedLoginResult{}, err
	}
	return driver.Login(ctx, request)
}

// Verify 使用按需准备的 Camoufox 校验账户登录态
func (lazy *lazyLoginDriver) Verify(ctx context.Context, request aistudio.IsolatedLoginRequest, state aistudio.StorageState) (aistudio.LoginVerification, error) {
	driver, err := lazy.resolve(ctx)
	if err != nil {
		return aistudio.LoginVerification{}, err
	}
	return driver.Verify(ctx, request, state)
}
