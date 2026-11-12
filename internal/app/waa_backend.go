package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

// camoufoxWorkerPath 是 Camoufox 后端 Worker 选项中的可执行文件路径，Worker 启动时换成已就绪的 Camoufox
const camoufoxWorkerPath = "camoufox"

// prepareWAABackend 按 WAA 后端返回 Worker 选项中的 Camoufox 路径与账户登录驱动，Camoufox 后端在后台准备 Camoufox
func prepareWAABackend(cfg config.Config, requests *requestRegistry, accounts int, proxy string) (string, aistudio.IsolatedLoginDriver) {
	removeStaleCamoufoxProfiles(requests)
	login := camoufoxLoginDriver{proxy: proxy}
	if cfg.WAABackend == config.WAABackendSpiderMonkey {
		requests.log("service", "INFO", fmt.Sprintf("运行时装配 | 2/3 | WAA 后端=spidermonkey | 账户=%d", accounts))
		return "", login
	}
	requests.log("service", "INFO", fmt.Sprintf("运行时装配 | 2/3 | 校验 Camoufox | 账户=%d", accounts))
	camoufoxnative.PrepareInBackground(proxy)
	return camoufoxWorkerPath, login
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

// newWAAWorker 启动账户的 Camoufox 或 SpiderMonkey Worker，Camoufox 尚未就绪时返回正在准备的进度或准备失败的原因
func newWAAWorker(ctx context.Context, accountID string, options camoufoxnative.Options) (*aistudio.NativeWorker, error) {
	if options.ExecutablePath == "" {
		return aistudio.NewSpiderMonkeyWorker(ctx, accountID, options)
	}
	path, err := camoufoxnative.ReadyPath()
	if err != nil {
		return nil, err
	}
	options.ExecutablePath = path
	return aistudio.NewNativeWorker(ctx, accountID, options)
}

// camoufoxLoginDriver 在登录时准备 Camoufox，同一进程内的登录与生成服务共用一次进行中的下载
type camoufoxLoginDriver struct {
	proxy string
}

// Login 等 Camoufox 就绪后执行隔离登录
func (driver camoufoxLoginDriver) Login(ctx context.Context, request aistudio.IsolatedLoginRequest) (aistudio.IsolatedLoginResult, error) {
	path, err := camoufoxnative.Prepare(ctx, driver.proxy)
	if err != nil {
		return aistudio.IsolatedLoginResult{}, err
	}
	login, err := aistudio.NewNativeLoginDriver(path)
	if err != nil {
		return aistudio.IsolatedLoginResult{}, err
	}
	return login.Login(ctx, request)
}
