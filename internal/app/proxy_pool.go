package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

// proxyProbeInterval 为代理池的定时检测间隔
const proxyProbeInterval = 5 * time.Minute

// proxyProbeTarget 为代理检测访问的地址
const proxyProbeTarget = "https://aistudio.google.com/"

// checkProxy 经代理访问 AI Studio，收到任意 HTTP 响应即判定代理可用
func checkProxy(ctx context.Context, proxy string) error {
	client, err := aistudio.NewProxyHTTPClient(proxy)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, proxyProbeTarget, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	return response.Body.Close()
}

// proxyNetworkError 判断请求失败是否为经代理的网络错误
func proxyNetworkError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	var transportError *url.Error
	return errors.As(err, &transportError) || errors.Is(err, camoufoxnative.ErrProxyUnreachable) || strings.Contains(err.Error(), "NetworkError when attempting to fetch resource")
}

// reportProxyFailure 记录经 proxy 的网络错误，代理被判定不可用时改绑其上的账户
func (service *trackedService) reportProxyFailure(proxy string, label string, err error) {
	changed, proxyErr := service.proxies.ReportFailure(proxy, err)
	if proxyErr != nil {
		service.requests.log(label, "WARN", "代理改绑 | 错误="+proxyErr.Error())
	}
	if len(changed) > 0 && service.rebindAccounts != nil {
		service.rebindAccounts(changed)
	}
}

// probeProxies 定时检测代理池并改绑不可用代理上的账户
func (admin *runtimeAdmin) probeProxies(ctx context.Context) {
	ticker := time.NewTicker(proxyProbeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		changed, err := admin.proxies.Probe(ctx, checkProxy)
		if err != nil && ctx.Err() == nil {
			admin.requests.log("service", "WARN", "代理改绑 | 错误="+err.Error())
		}
		admin.rebindAccounts(changed)
	}
}

// rebindAccounts 在后台按代理池的新绑定重建账户的请求头客户端与 Worker
func (admin *runtimeAdmin) rebindAccounts(accountIDs []string) {
	if len(accountIDs) == 0 {
		return
	}
	go func() {
		for _, accountID := range accountIDs {
			if err := admin.rebindAccount(admin.lifecycle, accountID); err != nil && admin.lifecycle.Err() == nil {
				admin.requests.log(accountID, "WARN", "代理改绑 | 错误="+err.Error())
			}
		}
	}()
}

func (admin *runtimeAdmin) rebindAccount(ctx context.Context, accountID string) error {
	lease, err := admin.pool.AcquireAccount(ctx, accountID)
	if err != nil {
		return err
	}
	account := lease.Account()
	headerUpdate, err := admin.headers.prepareUpdate(account, account.Config)
	if err != nil {
		return errors.Join(err, lease.Release())
	}
	workerUpdate, err := admin.workers.prepareUpdate(account, account.Config)
	if err != nil {
		headerUpdate.Discard()
		return errors.Join(err, lease.Release())
	}
	workerUpdate.Commit()
	headerUpdate.Commit()
	admin.requests.log(account.Config.Label, "WARN", fmt.Sprintf("代理改绑 | 出口=%s", redactedProxy(account.EffectiveProxy(admin.globalProxy))))
	return lease.Release()
}
