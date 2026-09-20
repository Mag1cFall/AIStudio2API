package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/chromeauth"
)

type chromeCookieRefreshFunc func(context.Context, aistudio.ChromeOAuthMaterial, string) ([]aistudio.StateCookie, error)

// authRuntimeRefresher renews credentials in-place using saved Chrome OAuth material.
type authRuntimeRefresher struct {
	refresh        chromeCookieRefreshFunc
	reset          func(string) error
	prepareHeaders func(string) (func(bool), error)
	globalProxy    string
	requests       *requestRegistry
}

// authRetryTransport performs an auth renewal retry for standard RPC requests.
type authRetryTransport struct {
	transport aistudio.RPCTransport
	refresher *authRuntimeRefresher
}

// authRetryProtectedTransport performs an auth renewal retry for protected RPC requests.
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

// UploadDrive delegates Drive uploads to the underlying authenticated transport.
func (transport *authRetryTransport) UploadDrive(
	ctx context.Context,
	accountID string,
	token string,
	request aistudio.UploadRequest,
) (aistudio.FileRef, error) {
	drive, ok := transport.transport.(aistudio.DriveTransport)
	if !ok {
		return aistudio.FileRef{}, fmt.Errorf("transport does not support Drive uploads")
	}

	return drive.UploadDrive(ctx, accountID, token, request)
}

// DownloadDrive delegates Drive downloads to the underlying authenticated transport.
func (transport *authRetryTransport) DownloadDrive(
	ctx context.Context,
	accountID string,
	token string,
	fileID string,
) (aistudio.MediaStream, error) {
	drive, ok := transport.transport.(aistudio.DriveTransport)
	if !ok {
		return aistudio.MediaStream{}, fmt.Errorf("transport does not support Drive downloads")
	}

	return drive.DownloadDrive(ctx, accountID, token, fileID)
}

// DeleteDrive delegates Drive file deletions to the underlying authenticated transport.
func (transport *authRetryTransport) DeleteDrive(
	ctx context.Context,
	accountID string,
	token string,
	fileID string,
) error {
	drive, ok := transport.transport.(aistudio.DriveTransport)
	if !ok {
		return fmt.Errorf("transport does not support Drive deletion")
	}

	return drive.DeleteDrive(ctx, accountID, token, fileID)
}

// newAuthRuntimeRefresher creates a production authentication refresher.
func newAuthRuntimeRefresher(
	workers *accountWorkerManager,
	headers *accountHeaderProvider,
	requests *requestRegistry,
	globalProxy string,
) *authRuntimeRefresher {
	return &authRuntimeRefresher{
		refresh:        chromeauth.Refresh,
		reset:          workers.Reset,
		prepareHeaders: headers.prepareInvalidate,
		globalProxy:    globalProxy,
		requests:       requests,
	}
}

func (provider *accountHeaderProvider) prepareInvalidate(accountID string) (func(bool), error) {
	provider.mu.RLock()
	account := provider.accounts[accountID]
	provider.mu.RUnlock()

	if account == nil {
		return nil, fmt.Errorf("fixed egress for account does not exist: %s", accountID)
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

// Do refreshes credentials for the same account upon receiving a 401 status and replays the request.
func (transport *authRetryTransport) Do(ctx context.Context, request aistudio.RPCRequest) (*aistudio.RPCResponse, error) {
	response, err := transport.transport.Do(ctx, request)
	if err != nil || !authenticationFailed(response) {
		return response, err
	}

	if !transport.refresher.Available(ctx) {
		return response, nil
	}

	originalErr, err := readAuthenticationFailure(request.Method, response)
	if err != nil {
		return nil, err
	}

	if err := transport.refresher.Refresh(ctx); err != nil {
		return nil, errors.Join(originalErr, err)
	}

	return transport.transport.Do(ctx, request)
}

// DoProtected refreshes credentials for the same account upon receiving a 401 status and replays the protected request.
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

	originalErr, err := readAuthenticationFailure(rpc.Method, response)
	if err != nil {
		return nil, err
	}

	if err := transport.refresher.Refresh(ctx); err != nil {
		return nil, errors.Join(originalErr, err)
	}

	return transport.transport.DoProtected(ctx, request, rpc)
}

// OpenBidiProtected refreshes credentials for the same account upon receiving a 401 status and re-establishes the WebChannel session.
func (transport *authRetryProtectedTransport) OpenBidiProtected(
	ctx context.Context,
	request aistudio.BidiRequest,
	runtime aistudio.RequestContext,
	lease *aistudio.AccountLease,
	release func() error,
) (*aistudio.BidiSession, error) {
	bidiTransport, ok := transport.transport.(aistudio.BidiProtectedTransport)
	if !ok {
		return nil, fmt.Errorf("protected transport does not support BidiGenerateContent")
	}

	gate := newBidiReleaseGate(release)
	session, err := bidiTransport.OpenBidiProtected(ctx, request, runtime, lease, gate.Release)
	if err == nil {
		if releaseErr := gate.Commit(); releaseErr != nil {
			return nil, errors.Join(releaseErr, session.Close())
		}
		return session, nil
	}

	if !aistudio.DefinitiveAuthenticationFailure(err) || transport.refresher == nil || !transport.refresher.Available(ctx) {
		return nil, errors.Join(err, gate.Commit())
	}

	gate.Abandon()

	if refreshErr := transport.refresher.Refresh(ctx); refreshErr != nil {
		return nil, errors.Join(err, refreshErr)
	}

	return bidiTransport.OpenBidiProtected(ctx, request, runtime, lease, release)
}

// DoProtectedVideo refreshes credentials for the same account upon authentication failure and replays the Veo request.
func (transport *authRetryProtectedTransport) DoProtectedVideo(
	ctx context.Context,
	request aistudio.VideoRequest,
	rpc aistudio.RPCRequest,
) (*aistudio.RPCResponse, error) {
	videoTransport, ok := transport.transport.(aistudio.VideoProtectedTransport)
	if !ok {
		return nil, fmt.Errorf("protected transport does not support GenerateVideo")
	}

	response, err := videoTransport.DoProtectedVideo(ctx, request, rpc)
	if err != nil || !authenticationFailed(response) {
		return response, err
	}

	if !transport.refresher.Available(ctx) {
		return response, nil
	}

	originalErr, err := readAuthenticationFailure(rpc.Method, response)
	if err != nil {
		return nil, err
	}

	if err := transport.refresher.Refresh(ctx); err != nil {
		return nil, errors.Join(originalErr, err)
	}

	return videoTransport.DoProtectedVideo(ctx, request, rpc)
}

// Refresh renews credentials for the currently leased account and saves the new storage state.
func (refresher *authRuntimeRefresher) Refresh(ctx context.Context) error {
	lease, ok := aistudio.AccountLeaseFromContext(ctx)
	if !ok {
		return fmt.Errorf("auth renewal missing account lease")
	}

	endRefresh, ok := lease.BeginAuthRefresh()
	if !ok {
		return fmt.Errorf("%w: account has active generation", aistudio.ErrAccountLeased)
	}
	defer endRefresh()

	account := lease.Account()
	startedAt := time.Now()

	refresher.requests.log(account.Config.Label, "INFO", "Account auth renewal | 1/2 | Refreshing cookies")

	err := lease.RefreshStorageState(func(state *aistudio.StorageState) error {
		extension, exists, err := state.AuthExtension()
		if err != nil {
			return err
		}
		if !exists || extension.OAuth == nil {
			return fmt.Errorf("account %s missing Chrome OAuth renewal material", account.ID)
		}

		cookies, err := refresher.refresh(ctx, *extension.OAuth, account.EffectiveProxy(refresher.globalProxy))
		if err != nil {
			return fmt.Errorf("renew account %s: %w", account.ID, err)
		}

		state.Cookies = cookies
		return nil
	}, func() (func(bool), error) {
		refresher.requests.log(account.Config.Label, "INFO", "Account auth renewal | 2/2 | Resetting protocol runtime")

		if err := refresher.reset(account.ID); err != nil {
			return nil, fmt.Errorf("reset runtime for account %s: %w", account.ID, err)
		}

		finish, err := refresher.prepareHeaders(account.ID)
		if err != nil {
			return nil, fmt.Errorf("refresh common headers for account %s: %w", account.ID, err)
		}

		return finish, nil
	})

	if err != nil {
		wrapped := fmt.Errorf("save storage state for account %s: %w", account.ID, err)
		refresher.requests.log(account.Config.Label, "ERROR", fmt.Sprintf(
			"Account auth renewal failed | duration=%s | error=%s",
			time.Since(startedAt).Round(time.Millisecond), wrapped.Error(),
		))
		return wrapped
	}

	refresher.requests.log(account.Config.Label, "INFO", fmt.Sprintf(
		"Account auth renewal completed | duration=%s",
		time.Since(startedAt).Round(time.Millisecond),
	))

	return nil
}

// Available returns whether the currently leased account has saved Chrome OAuth refresh material.
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
	return err == nil && exists && extension.OAuth != nil
}

func authenticationFailed(response *aistudio.RPCResponse) bool {
	return response != nil && response.Body != nil && response.StatusCode == http.StatusUnauthorized
}

// readAuthenticationFailure reads and closes an authentication failure response to preserve the original error reason.
func readAuthenticationFailure(method string, response *aistudio.RPCResponse) (*aistudio.RPCError, error) {
	body, readErr := io.ReadAll(response.Body)
	if err := errors.Join(readErr, response.Body.Close()); err != nil {
		return nil, fmt.Errorf("read authentication failure response: %w", err)
	}

	return aistudio.DecodeRPCError(method, response.StatusCode, body), nil
}

var _ aistudio.RPCTransport = (*authRetryTransport)(nil)
var _ aistudio.DriveTransport = (*authRetryTransport)(nil)
var _ aistudio.ProtectedTransport = (*authRetryProtectedTransport)(nil)
var _ aistudio.VideoProtectedTransport = (*authRetryProtectedTransport)(nil)
var _ aistudio.BidiProtectedTransport = (*authRetryProtectedTransport)(nil)
