package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/chromeauth"
)

type chromeCookieRefreshFunc func(context.Context, aistudio.ChromeOAuthMaterial, string) ([]aistudio.StateCookie, error)

// authRuntimeRefresher 使用账户保存的 Chrome OAuth 材料原地续签
type authRuntimeRefresher struct {
	refresh     chromeCookieRefreshFunc
	reset       func(string) error
	invalidate  func(string) error
	globalProxy string
}

// authRetryTransport 为普通 RPC 执行一次认证续签重试
type authRetryTransport struct {
	transport aistudio.RPCTransport
	refresher *authRuntimeRefresher
}

// authRetryProtectedTransport 为受保护 RPC 执行一次认证续签重试
type authRetryProtectedTransport struct {
	transport aistudio.ProtectedTransport
	refresher *authRuntimeRefresher
}

// UploadDrive 将 Drive 上传委托给同一认证传输
func (transport *authRetryTransport) UploadDrive(
	ctx context.Context,
	accountID string,
	token string,
	request aistudio.UploadRequest,
) (aistudio.FileRef, error) {
	drive, ok := transport.transport.(aistudio.DriveTransport)
	if !ok {
		return aistudio.FileRef{}, fmt.Errorf("transport 不支持 Drive 上传")
	}
	return drive.UploadDrive(ctx, accountID, token, request)
}

// DownloadDrive 将 Drive 下载委托给同一认证传输
func (transport *authRetryTransport) DownloadDrive(
	ctx context.Context,
	accountID string,
	token string,
	fileID string,
) (aistudio.Media, error) {
	drive, ok := transport.transport.(aistudio.DriveTransport)
	if !ok {
		return aistudio.Media{}, fmt.Errorf("transport 不支持 Drive 下载")
	}
	return drive.DownloadDrive(ctx, accountID, token, fileID)
}

// newAuthRuntimeRefresher 创建生产环境认证续签器
func newAuthRuntimeRefresher(workers *accountWorkerManager, headers *accountHeaderProvider, globalProxy string) *authRuntimeRefresher {
	return &authRuntimeRefresher{
		refresh: chromeauth.Refresh, reset: workers.Reset, invalidate: headers.Invalidate, globalProxy: globalProxy,
	}
}

// Do 在 401 或 403 后续签同一账户并重放一次请求
func (transport *authRetryTransport) Do(ctx context.Context, request aistudio.RPCRequest) (*aistudio.RPCResponse, error) {
	response, err := transport.transport.Do(ctx, request)
	if err != nil || !authenticationFailed(response) {
		return response, err
	}
	if !transport.refresher.Available(ctx) {
		return response, nil
	}
	if err := response.Body.Close(); err != nil {
		return nil, fmt.Errorf("关闭认证失败响应: %w", err)
	}
	if err := transport.refresher.Refresh(ctx); err != nil {
		return nil, authenticationRefreshError(request.Method, response.StatusCode, err)
	}
	return transport.transport.Do(ctx, request)
}

// DoProtected 在 401 或 403 后续签同一账户并重放一次受保护请求
func (transport *authRetryProtectedTransport) DoProtected(
	ctx context.Context,
	request aistudio.GenerateRequest,
	rpc aistudio.RPCRequest,
) (*aistudio.RPCResponse, error) {
	response, err := transport.transport.DoProtected(ctx, request, rpc)
	if err != nil || !authenticationFailed(response) {
		return response, err
	}
	if !transport.refresher.Available(ctx) {
		return response, nil
	}
	if err := response.Body.Close(); err != nil {
		return nil, fmt.Errorf("关闭认证失败响应: %w", err)
	}
	if err := transport.refresher.Refresh(ctx); err != nil {
		return nil, authenticationRefreshError(rpc.Method, response.StatusCode, err)
	}
	return transport.transport.DoProtected(ctx, request, rpc)
}

// DoProtectedVideo 在认证失败后续签同一账户并重放 Veo 请求
func (transport *authRetryProtectedTransport) DoProtectedVideo(
	ctx context.Context,
	request aistudio.VideoRequest,
	rpc aistudio.RPCRequest,
) (*aistudio.RPCResponse, error) {
	videoTransport, ok := transport.transport.(aistudio.VideoProtectedTransport)
	if !ok {
		return nil, fmt.Errorf("protected transport 不支持 GenerateVideo")
	}
	response, err := videoTransport.DoProtectedVideo(ctx, request, rpc)
	if err != nil || !authenticationFailed(response) {
		return response, err
	}
	if !transport.refresher.Available(ctx) {
		return response, nil
	}
	if err := response.Body.Close(); err != nil {
		return nil, fmt.Errorf("关闭认证失败响应: %w", err)
	}
	if err := transport.refresher.Refresh(ctx); err != nil {
		return nil, authenticationRefreshError(rpc.Method, response.StatusCode, err)
	}
	return videoTransport.DoProtectedVideo(ctx, request, rpc)
}

// Refresh 续签当前租约账户并保存新的 storage state
func (refresher *authRuntimeRefresher) Refresh(ctx context.Context) error {
	lease, ok := aistudio.AccountLeaseFromContext(ctx)
	if !ok {
		return fmt.Errorf("认证续签缺少账户租约")
	}
	account := lease.Account()
	state := account.StorageState
	extension, exists, err := state.AuthExtension()
	if err != nil {
		return err
	}
	if !exists || extension.OAuth == nil {
		return fmt.Errorf("账户 %s 缺少 Chrome OAuth 续签材料", account.ID)
	}
	cookies, err := refresher.refresh(ctx, *extension.OAuth, account.EffectiveProxy(refresher.globalProxy))
	if err != nil {
		return fmt.Errorf("续签账户 %s: %w", account.ID, err)
	}
	state.Cookies = cookies
	if err := lease.SaveStorageState(state); err != nil {
		return fmt.Errorf("保存账户 %s 认证状态: %w", account.ID, err)
	}
	if refresher.invalidate != nil {
		if err := refresher.invalidate(account.ID); err != nil {
			return fmt.Errorf("刷新账户 %s 公共头: %w", account.ID, err)
		}
	}
	if err := refresher.reset(account.ID); err != nil {
		return fmt.Errorf("重置账户 %s runtime: %w", account.ID, err)
	}
	return nil
}

// Available 返回当前租约账户是否保存了 Chrome OAuth 续签材料
func (refresher *authRuntimeRefresher) Available(ctx context.Context) bool {
	lease, ok := aistudio.AccountLeaseFromContext(ctx)
	if !ok {
		return false
	}
	extension, exists, err := lease.Account().StorageState.AuthExtension()
	return err == nil && exists && extension.OAuth != nil
}

func authenticationFailed(response *aistudio.RPCResponse) bool {
	return response != nil && response.Body != nil &&
		(response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden)
}

func authenticationRefreshError(method string, statusCode int, err error) error {
	return errors.Join(&aistudio.RPCError{
		Method: method, StatusCode: statusCode, Message: http.StatusText(statusCode),
	}, err)
}

var _ aistudio.RPCTransport = (*authRetryTransport)(nil)
var _ aistudio.DriveTransport = (*authRetryTransport)(nil)
var _ aistudio.ProtectedTransport = (*authRetryProtectedTransport)(nil)
var _ aistudio.VideoProtectedTransport = (*authRetryProtectedTransport)(nil)
