package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

// liveViewWritebackTimeout 是远程操作结束后把浏览器 Cookie 写回 storage state 的最长时间
const liveViewWritebackTimeout = 20 * time.Second

// liveViewNoWorkerReason 是账户没有就绪的常驻浏览器时的提示
const liveViewNoWorkerReason = "账户的常驻浏览器未运行，请先启动生成服务并等待该账户预热完成"

// LiveViewAvailability 返回账户常驻浏览器挂载远程操作窗口的可用性与原因
func (manager *accountWorkerManager) LiveViewAvailability(accountID string) api.LiveViewAvailability {
	worker := manager.residentWorker(accountID)
	if worker == nil {
		return api.LiveViewAvailability{Available: false, Reason: liveViewNoWorkerReason}
	}
	available, reason := worker.LiveViewAvailability()
	return api.LiveViewAvailability{Available: available, Reason: reason}
}

// AttachAccountLiveView 实现 api.AdminService，委托给 Worker 管理器
func (admin *runtimeAdmin) AttachAccountLiveView(ctx context.Context, accountID string, view *camoufoxnative.LiveView) (func(), error) {
	return admin.workers.AttachAccountLiveView(ctx, accountID, view)
}

// LiveViewAvailability 实现 api.AdminService，返回账户远程操作的可用性
func (admin *runtimeAdmin) LiveViewAvailability(_ context.Context, accountID string) (api.LiveViewAvailability, error) {
	return admin.workers.LiveViewAvailability(accountID), nil
}

// AttachAccountLiveView 把远程操作窗口接到账户的常驻 Camoufox Worker：
// 挂载期间账户不参与请求调度，窗口断开后解除占用并把浏览器当前 Cookie 写回到 storage state，返回的清理函数幂等
func (manager *accountWorkerManager) AttachAccountLiveView(ctx context.Context, accountID string, view *camoufoxnative.LiveView) (func(), error) {
	worker := manager.residentWorker(accountID)
	if worker == nil {
		return nil, errors.New(liveViewNoWorkerReason)
	}
	manager.pool.SetManualHold(accountID, 1)
	detach, err := worker.AttachLiveView(ctx, view)
	if err != nil {
		manager.pool.SetManualHold(accountID, -1)
		return nil, err
	}
	manager.requests.log(accountID, "INFO", "远程操作窗口已连接")
	var cleanupOnce sync.Once
	return func() {
		cleanupOnce.Do(func() {
			detach()
			manager.pool.SetManualHold(accountID, -1)
			manager.writebackLiveViewCookies(accountID, worker)
			manager.requests.log(accountID, "INFO", "远程操作窗口已断开")
		})
	}, nil
}

// writebackLiveViewCookies 把浏览器当前 Cookie 全量写回 storage state，保留 localStorage 与认证扩展，失败只记录日志
func (manager *accountWorkerManager) writebackLiveViewCookies(accountID string, worker *aistudio.NativeWorker) {
	writebackCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), liveViewWritebackTimeout)
	defer cancel()
	encoded, err := worker.StorageCookies(writebackCtx)
	if err != nil {
		manager.requests.log(accountID, "WARN", "远程操作 Cookie 写回跳过：读取浏览器 Cookie 失败", "error", err)
		return
	}
	var cookies []aistudio.StateCookie
	if err := json.Unmarshal(encoded, &cookies); err != nil {
		manager.requests.log(accountID, "WARN", "远程操作 Cookie 写回跳过：Cookie 格式无效", "error", err)
		return
	}
	lease, err := manager.pool.AcquireAccount(writebackCtx, accountID)
	if err != nil {
		manager.requests.log(accountID, "WARN", "远程操作 Cookie 写回跳过：获取账户租约失败", "error", err)
		return
	}
	defer func() { _ = lease.Release() }()
	// ReplaceCookies 从磁盘载入最新状态后只替换 Cookie，保留 localStorage 与认证扩展
	if err := lease.ReplaceCookies(cookies); err != nil {
		manager.requests.log(accountID, "WARN", "远程操作 Cookie 写回失败", "error", err)
		return
	}
	manager.requests.log(accountID, "INFO", "远程操作后的浏览器 Cookie 已写回", "cookies", len(cookies))
}
