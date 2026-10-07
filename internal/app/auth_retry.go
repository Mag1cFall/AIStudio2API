package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/chromeauth"
)

type chromeCookieRefreshFunc func(context.Context, aistudio.ChromeOAuthMaterial, string) ([]aistudio.StateCookie, error)

// authRuntimeRefresher 使用账户保存的 Chrome OAuth 材料原地续签
type authRuntimeRefresher struct {
	pool           *aistudio.AccountPool
	refresh        chromeCookieRefreshFunc
	importCurrent  func(context.Context, aistudio.AuthSource, *aistudio.ChromeOAuthMaterial, string) (aistudio.StorageState, error)
	chromeHas      func(string) bool
	reset          func(string) error
	prepareHeaders func(string) (func(bool), error)
	globalProxy    string
	requests       *requestRegistry
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

type bidiReleaseGate struct {
	mu        sync.Mutex
	once      sync.Once
	release   func() error
	requested bool
	committed bool
	abandoned bool
	err       error
}

func newBidiReleaseGate(release func() error) *bidiReleaseGate {
	return &bidiReleaseGate{release: release}
}

func (gate *bidiReleaseGate) Release() error {
	gate.mu.Lock()
	if gate.abandoned {
		gate.mu.Unlock()
		return nil
	}
	if !gate.committed {
		gate.requested = true
		gate.mu.Unlock()
		return nil
	}
	gate.mu.Unlock()
	return gate.releaseNow()
}

func (gate *bidiReleaseGate) Commit() error {
	gate.mu.Lock()
	gate.committed = true
	requested := gate.requested
	gate.mu.Unlock()
	if !requested {
		return nil
	}
	return gate.releaseNow()
}

func (gate *bidiReleaseGate) Abandon() {
	gate.mu.Lock()
	gate.abandoned = true
	gate.mu.Unlock()
}

func (gate *bidiReleaseGate) releaseNow() error {
	gate.once.Do(func() {
		if gate.release != nil {
			gate.err = gate.release()
		}
	})
	return gate.err
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
) (aistudio.MediaStream, error) {
	drive, ok := transport.transport.(aistudio.DriveTransport)
	if !ok {
		return aistudio.MediaStream{}, fmt.Errorf("transport 不支持 Drive 下载")
	}
	return drive.DownloadDrive(ctx, accountID, token, fileID)
}

// DeleteDrive 将 Drive 删除委托给同一认证传输
func (transport *authRetryTransport) DeleteDrive(
	ctx context.Context,
	accountID string,
	token string,
	fileID string,
) error {
	drive, ok := transport.transport.(aistudio.DriveTransport)
	if !ok {
		return fmt.Errorf("transport 不支持 Drive 删除")
	}
	return drive.DeleteDrive(ctx, accountID, token, fileID)
}

// newAuthRuntimeRefresher 创建生产环境认证续签器
func newAuthRuntimeRefresher(
	workers *accountWorkerManager,
	headers *accountHeaderProvider,
	requests *requestRegistry,
	globalProxy string,
) *authRuntimeRefresher {
	return &authRuntimeRefresher{
		pool:    workers.pool,
		refresh: chromeauth.Refresh, importCurrent: importCurrentChromeState, chromeHas: chromeIdentityImportable,
		reset: workers.Reset, prepareHeaders: headers.prepareInvalidate,
		globalProxy: globalProxy,
		requests:    requests,
	}
}

// chromeIdentityImportable 判断本机 Chrome 是否登录着该邮箱且材料可导入
func chromeIdentityImportable(email string) bool {
	root, err := chromeauth.DefaultChromeRoot()
	if err != nil {
		return false
	}
	accounts, err := chromeauth.Discover(root)
	if err != nil {
		return false
	}
	for _, account := range accounts {
		if account.Importable && strings.EqualFold(account.Email, email) {
			return true
		}
	}
	return false
}

// importCurrentChromeState 从原来源中更新同一 Google 账户的认证材料
func importCurrentChromeState(ctx context.Context, source aistudio.AuthSource, material *aistudio.ChromeOAuthMaterial, proxy string) (aistudio.StorageState, error) {
	root, err := chromeauth.DefaultChromeRoot()
	if err != nil {
		return aistudio.StorageState{}, err
	}
	accounts, err := chromeauth.Discover(root)
	if err != nil {
		return aistudio.StorageState{}, err
	}
	var selected string
	for _, account := range accounts {
		if !account.Importable || !strings.EqualFold(account.Email, source.Email) || material != nil && account.GaiaID != material.GaiaID {
			continue
		}
		selected = account.ID
		if account.Profile == source.Profile {
			break
		}
	}
	if selected == "" {
		return aistudio.StorageState{}, fmt.Errorf("Chrome 中没有可更新的原账户 %s", source.Email)
	}
	results, err := chromeauth.Import(ctx, chromeauth.ImportOptions{ChromeRoot: root, Proxy: proxy, AccountIDs: []string{selected}})
	if err != nil {
		return aistudio.StorageState{}, err
	}
	if len(results) != 1 || !strings.EqualFold(results[0].Email, source.Email) {
		return aistudio.StorageState{}, fmt.Errorf("Chrome 更新账户与原账户不匹配")
	}
	return results[0].State, nil
}

// markAuthenticationRequired 发布当前认证代际的账户状态
func (refresher *authRuntimeRefresher) markAuthenticationRequired(ctx context.Context, cause error) error {
	lease, ok := aistudio.AccountLeaseFromContext(ctx)
	if !ok || ctx.Err() != nil || !aistudio.DefinitiveAuthenticationFailure(cause) {
		return cause
	}
	err := lease.MarkAuthenticationRequired(cause.Error())
	if refresher.pool != nil {
		statuses := refresher.pool.Status()
		accounts := make([]api.AdminAccount, 0, len(statuses))
		for _, status := range statuses {
			accounts = append(accounts, adminAccountDTO(status))
		}
		refresher.requests.publish(api.AdminEvent{Type: "accounts", Data: map[string]any{"accounts": accounts}})
	}
	return errors.Join(cause, err)
}

// Recover 为当前认证代际恢复一次登录并保留最终失败原因
func (refresher *authRuntimeRefresher) Recover(ctx context.Context, cause error) error {
	var startup *accountWorkerInitError
	if errors.As(cause, &startup) && startup.authHandled {
		return cause
	}
	if refresher.Available(ctx) {
		if err := refresher.Refresh(ctx); err == nil {
			return nil
		} else {
			cause = errors.Join(cause, err)
		}
	}
	return refresher.markAuthenticationRequired(ctx, cause)
}

// do 重放认证恢复前尚未产生语义输出的普通或受保护 RPC
func (refresher *authRuntimeRefresher) do(ctx context.Context, method string, send func() (*aistudio.RPCResponse, error)) (*aistudio.RPCResponse, error) {
	for attempt := 0; ; attempt++ {
		response, err := send()
		if err == nil && authenticationFailed(response) {
			original, readErr := readAuthenticationFailure(method, response)
			if readErr != nil {
				return nil, readErr
			}
			err = original
			response = nil
		}
		if !aistudio.DefinitiveAuthenticationFailure(err) {
			return response, err
		}
		if attempt == 1 {
			return nil, refresher.markAuthenticationRequired(ctx, err)
		}
		if err := refresher.Recover(ctx, err); err != nil {
			return nil, err
		}
	}
}

func (provider *accountHeaderProvider) prepareInvalidate(accountID string) (func(bool), error) {
	provider.mu.RLock()
	account := provider.accounts[accountID]
	provider.mu.RUnlock()
	if account == nil {
		return nil, fmt.Errorf("账户固定出口不存在: %s", accountID)
	}
	account.mu.Lock()
	previous := account.headers.Clone()
	account.headers = nil
	return func(committed bool) {
		if !committed {
			account.headers = previous
		}
		account.mu.Unlock()
	}, nil
}

// Do 在 401 后续签同一账户并重放一次请求
func (transport *authRetryTransport) Do(ctx context.Context, request aistudio.RPCRequest) (*aistudio.RPCResponse, error) {
	return transport.refresher.do(ctx, request.Method, func() (*aistudio.RPCResponse, error) {
		return transport.transport.Do(ctx, request)
	})
}

// DoProtected 在 401 后续签同一账户并重放一次受保护请求
func (transport *authRetryProtectedTransport) DoProtected(
	ctx context.Context,
	request aistudio.GenerateRequest,
	rpc aistudio.RPCRequest,
) (*aistudio.RPCResponse, error) {
	return transport.refresher.do(ctx, rpc.Method, func() (*aistudio.RPCResponse, error) {
		return transport.transport.DoProtected(ctx, request, rpc)
	})
}

// OpenBidiProtected 在 401 后续签同一账户并重新建立 WebChannel
func (transport *authRetryProtectedTransport) OpenBidiProtected(
	ctx context.Context,
	request aistudio.BidiRequest,
	runtime aistudio.RequestContext,
	lease *aistudio.AccountLease,
	release func() error,
) (*aistudio.BidiSession, error) {
	bidiTransport, ok := transport.transport.(aistudio.BidiProtectedTransport)
	if !ok {
		return nil, fmt.Errorf("protected transport 不支持 BidiGenerateContent")
	}
	gate := newBidiReleaseGate(release)
	session, err := bidiTransport.OpenBidiProtected(ctx, request, runtime, lease, gate.Release)
	if err == nil {
		if releaseErr := gate.Commit(); releaseErr != nil {
			return nil, errors.Join(releaseErr, session.Close())
		}
		return session, nil
	}
	if !aistudio.DefinitiveAuthenticationFailure(err) || transport.refresher == nil {
		return nil, errors.Join(err, gate.Commit())
	}
	gate.Abandon()
	if recoverErr := transport.refresher.Recover(ctx, err); recoverErr != nil {
		return nil, recoverErr
	}
	session, err = bidiTransport.OpenBidiProtected(ctx, request, runtime, lease, release)
	if aistudio.DefinitiveAuthenticationFailure(err) {
		err = transport.refresher.markAuthenticationRequired(ctx, err)
	}
	return session, err
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
	return transport.refresher.do(ctx, rpc.Method, func() (*aistudio.RPCResponse, error) {
		return videoTransport.DoProtectedVideo(ctx, request, rpc)
	})
}

// Refresh 续签当前租约账户并保存新的 storage state
func (refresher *authRuntimeRefresher) Refresh(ctx context.Context) error {
	lease, ok := aistudio.AccountLeaseFromContext(ctx)
	if !ok {
		return fmt.Errorf("认证续签缺少账户租约")
	}
	endRefresh, ok := lease.BeginAuthRefresh()
	if !ok {
		return fmt.Errorf("%w: 账户存在活动生成", aistudio.ErrAccountLeased)
	}
	defer endRefresh()
	if err := lease.WaitForAuthRefresh(ctx); err != nil {
		return err
	}
	account := lease.Account()
	startedAt := time.Now()
	refresher.requests.log(account.Config.Label, "INFO", "账户认证续签 | 1/2 | 刷新 Cookie")
	err := lease.RefreshStorageState(func(state *aistudio.StorageState) error {
		extension, exists, err := state.AuthExtension()
		if err != nil {
			return err
		}
		if !exists {
			extension = aistudio.AuthExtension{Source: aistudio.AuthSource{Browser: "chrome", Email: account.ID}}
		}
		proxy := account.EffectiveProxy(refresher.globalProxy)
		var cookies []aistudio.StateCookie
		if extension.OAuth != nil {
			cookies, err = refresher.refresh(ctx, *extension.OAuth, proxy)
		}
		if extension.OAuth == nil || errors.Is(err, chromeauth.ErrCredentialsRejected) {
			if extension.Source.Browser != "chrome" || !strings.EqualFold(extension.Source.Email, account.ID) {
				return errors.Join(fmt.Errorf("账户 %s 缺少可更新的 Chrome 来源", account.ID), err)
			}
			refresher.requests.log(account.Config.Label, "INFO", "账户认证续签 | 更新 Chrome 来源材料")
			updated, importErr := refresher.importCurrent(ctx, extension.Source, extension.OAuth, proxy)
			if importErr != nil {
				return errors.Join(err, importErr)
			}
			current, exists, importErr := updated.AuthExtension()
			if importErr != nil || !exists || current.OAuth == nil || !strings.EqualFold(current.Source.Email, account.ID) ||
				extension.OAuth != nil && current.OAuth.GaiaID != extension.OAuth.GaiaID {
				return fmt.Errorf("Chrome 更新结果与账户 %s 不匹配", account.ID)
			}
			if err := state.SetAuthExtension(current); err != nil {
				return err
			}
			cookies, err = updated.Cookies, nil
		}
		if err != nil {
			return fmt.Errorf("续签账户 %s: %w", account.ID, err)
		}
		state.Cookies = cookies
		_, err = aistudio.NewSigner().Sign(*state)
		return err
	}, func() (func(bool), error) {
		refresher.requests.log(account.Config.Label, "INFO", "账户认证续签 | 2/2 | 重置协议运行时")
		if err := refresher.reset(account.ID); err != nil {
			return nil, fmt.Errorf("重置账户 %s runtime: %w", account.ID, err)
		}
		finish, err := refresher.prepareHeaders(account.ID)
		if err != nil {
			return nil, fmt.Errorf("刷新账户 %s 公共头: %w", account.ID, err)
		}
		return finish, nil
	})
	if err != nil {
		wrapped := fmt.Errorf("保存账户 %s 认证状态: %w", account.ID, err)
		refresher.requests.log(account.Config.Label, "ERROR", fmt.Sprintf(
			"账户认证续签失败 | 耗时=%s | 错误=%s",
			time.Since(startedAt).Round(time.Millisecond), wrapped.Error(),
		))
		return wrapped
	}
	refresher.requests.log(account.Config.Label, "INFO", fmt.Sprintf(
		"账户认证续签完成 | 耗时=%s",
		time.Since(startedAt).Round(time.Millisecond),
	))
	return nil
}

// Available 返回当前租约账户是否保存了 Chrome OAuth 续签材料
func (refresher *authRuntimeRefresher) Available(ctx context.Context) bool {
	lease, ok := aistudio.AccountLeaseFromContext(ctx)
	if !ok {
		return false
	}
	state, err := lease.ReloadStorageState()
	if err != nil {
		return false
	}
	extension, exists, err := state.AuthExtension()
	if err != nil {
		return false
	}
	if exists {
		return extension.OAuth != nil || extension.Source.Browser == "chrome"
	}
	return refresher.chromeHas != nil && refresher.chromeHas(lease.Account().ID)
}

func authenticationFailed(response *aistudio.RPCResponse) bool {
	return response != nil && response.Body != nil && response.StatusCode == http.StatusUnauthorized
}

// readAuthenticationFailure 读取并关闭认证失败响应以保留原始原因
func readAuthenticationFailure(method string, response *aistudio.RPCResponse) (*aistudio.RPCError, error) {
	body, readErr := io.ReadAll(response.Body)
	if err := errors.Join(readErr, response.Body.Close()); err != nil {
		return nil, fmt.Errorf("读取认证失败响应: %w", err)
	}
	return aistudio.DecodeRPCError(method, response.StatusCode, body), nil
}

var _ aistudio.RPCTransport = (*authRetryTransport)(nil)
var _ aistudio.DriveTransport = (*authRetryTransport)(nil)
var _ aistudio.ProtectedTransport = (*authRetryProtectedTransport)(nil)
var _ aistudio.VideoProtectedTransport = (*authRetryProtectedTransport)(nil)
var _ aistudio.BidiProtectedTransport = (*authRetryProtectedTransport)(nil)
