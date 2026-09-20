package aistudio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

// NativeLoginDriver completes isolated login via pure-Go WebDriver BiDi
type NativeLoginDriver struct {
	camoufox string
	timeout  time.Duration
}

var _ IsolatedLoginDriver = (*NativeLoginDriver)(nil)

// NewNativeLoginDriver creates a pure-Go Camoufox login driver
func NewNativeLoginDriver(camoufoxPath string, timeout time.Duration) (*NativeLoginDriver, error) {
	camoufoxPath = strings.TrimSpace(camoufoxPath)
	if camoufoxPath == "" {
		return nil, errors.New("missing Camoufox path")
	}
	absolute, err := filepath.Abs(camoufoxPath)
	if err != nil {
		return nil, fmt.Errorf("resolve Camoufox path: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, fmt.Errorf("stat Camoufox: %w", err)
	}
	if info.IsDir() {
		return nil, errors.New("Camoufox path is a directory")
	}
	if timeout <= 0 {
		return nil, errors.New("Camoufox login timeout must be positive")
	}
	return &NativeLoginDriver{camoufox: absolute, timeout: timeout}, nil
}

// Login starts visible isolated Camoufox and exports authentication state
func (driver *NativeLoginDriver) Login(ctx context.Context, request IsolatedLoginRequest) (IsolatedLoginResult, error) {
	if driver == nil {
		return IsolatedLoginResult{}, errors.New("pure-Go Camoufox login driver is not initialized")
	}
	result, err := camoufoxnative.Login(ctx, driver.options(request))
	if err != nil {
		return IsolatedLoginResult{}, err
	}
	var state StorageState
	if err := json.Unmarshal(result.StorageStateJSON, &state); err != nil {
		return IsolatedLoginResult{}, fmt.Errorf("parse isolated login state: %w", err)
	}
	if err := state.Validate(); err != nil {
		return IsolatedLoginResult{}, err
	}
	if err := state.SetAuthExtension(AuthExtension{
		Source: AuthSource{Browser: "camoufox", Email: result.Email},
	}); err != nil {
		return IsolatedLoginResult{}, err
	}
	return IsolatedLoginResult{StorageState: state, Email: result.Email, VerifiedAt: result.VerifiedAt}, nil
}

// Verify uses headless isolated Camoufox to verify existing authentication state
func (driver *NativeLoginDriver) Verify(ctx context.Context, request IsolatedLoginRequest, state StorageState) (LoginVerification, error) {
	if driver == nil {
		return LoginVerification{}, errors.New("pure-Go Camoufox login driver is not initialized")
	}
	if err := state.Validate(); err != nil {
		return LoginVerification{}, err
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return LoginVerification{}, fmt.Errorf("encode isolated verification state: %w", err)
	}
	verification, err := camoufoxnative.Verify(ctx, driver.options(request), encoded)
	if err != nil {
		return LoginVerification{}, err
	}
	return LoginVerification{
		Authenticated: verification.Authenticated,
		VerifiedAt:    verification.VerifiedAt,
		Reason:        verification.Reason,
	}, nil
}

func (driver *NativeLoginDriver) options(request IsolatedLoginRequest) camoufoxnative.LoginOptions {
	return camoufoxnative.LoginOptions{
		ExecutablePath: driver.camoufox,
		Directory:      request.Directory,
		Locale:         request.Locale,
		Timezone:       request.Timezone,
		Proxy:          request.Proxy,
		Timeout:        driver.timeout,
	}
}
