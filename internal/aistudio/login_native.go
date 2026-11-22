package aistudio

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

// loginTimeout 是隔离登录从启动浏览器到导出认证状态的期限
const loginTimeout = 30 * time.Minute

// NativeLoginDriver 通过纯 Go WebDriver BiDi 完成隔离登录
type NativeLoginDriver struct {
	camoufox string
}

var _ IsolatedLoginDriver = (*NativeLoginDriver)(nil)
var _ AccountVerifyDriver = (*NativeLoginDriver)(nil)

// NewNativeLoginDriver 创建纯 Go Camoufox 登录驱动
func NewNativeLoginDriver(camoufoxPath string) (*NativeLoginDriver, error) {
	absolute, err := filepath.Abs(camoufoxPath)
	if err != nil {
		return nil, fmt.Errorf("解析 Camoufox 路径: %w", err)
	}
	return &NativeLoginDriver{camoufox: absolute}, nil
}

// decodeIsolatedLoginState 解析隔离登录导出的 storage state 并标记 camoufox 来源
func decodeIsolatedLoginState(raw []byte, email string) (StorageState, error) {
	var state StorageState
	if err := json.Unmarshal(raw, &state); err != nil {
		return StorageState{}, fmt.Errorf("解析隔离登录状态: %w", err)
	}
	state.SetAuthExtension(AuthExtension{
		Source: AuthSource{Browser: "camoufox", Email: email},
	})
	return state, nil
}

// Login 启动隔离 Camoufox 供用户登录并导出认证状态
func (driver *NativeLoginDriver) Login(ctx context.Context, request IsolatedLoginRequest) (IsolatedLoginResult, error) {
	options := camoufoxnative.LoginOptions{
		ExecutablePath:     driver.camoufox,
		Directory:          request.Directory,
		Locale:             request.Locale,
		Timezone:           request.Timezone,
		Proxy:              request.Proxy,
		Timeout:            loginTimeout,
		View:               request.View,
		OnVerifyStep:       request.OnVerifyStep,
		VerifyPollInterval: request.VerifyPollInterval,
	}
	if request.OnLoggedIn != nil {
		options.OnLoggedIn = func(callbackCtx context.Context, raw camoufoxnative.LoginResult) error {
			state, err := decodeIsolatedLoginState(raw.StorageStateJSON, raw.Email)
			if err != nil {
				return err
			}
			return request.OnLoggedIn(callbackCtx, IsolatedLoginResult{
				StorageState: state, Email: raw.Email, VerifiedAt: raw.VerifiedAt, DriveError: raw.DriveError,
			})
		}
	}
	if request.CheckVerification != nil {
		options.CheckVerification = func(callbackCtx context.Context, raw []byte) (bool, error) {
			var state StorageState
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &state); err != nil {
					return true, fmt.Errorf("解析验证状态: %w", err)
				}
			}
			return request.CheckVerification(callbackCtx, state)
		}
	}
	result, err := camoufoxnative.Login(ctx, options)
	if err != nil {
		return IsolatedLoginResult{}, err
	}
	state, err := decodeIsolatedLoginState(result.StorageStateJSON, result.Email)
	if err != nil {
		return IsolatedLoginResult{}, err
	}
	return IsolatedLoginResult{
		StorageState: state, Email: result.Email, VerifiedAt: result.VerifiedAt,
		DriveError: result.DriveError, CloseError: result.CloseError,
		Saved: result.Saved, VerificationPending: result.VerificationPending, VerificationError: result.VerificationError,
	}, nil
}

// Verify 启动隔离 Camoufox 载入已有账户的认证状态并停在官网验证页，自动再核对直到通过或窗口关闭
func (driver *NativeLoginDriver) Verify(ctx context.Context, request AccountVerifyRequest) (bool, error) {
	encoded, err := json.Marshal(request.StorageState)
	if err != nil {
		return true, fmt.Errorf("编码账户认证状态: %w", err)
	}
	options := camoufoxnative.VerifyOptions{
		ExecutablePath:   driver.camoufox,
		Directory:        request.Directory,
		StorageStateJSON: encoded,
		AuthUser:         request.AuthUser,
		Locale:           request.Locale,
		Timezone:         request.Timezone,
		Proxy:            request.Proxy,
		Timeout:          loginTimeout,
		View:             request.View,
		OnVerifyStep:     request.OnVerifyStep,
		PollInterval:     request.PollInterval,
	}
	if request.CheckVerification != nil {
		options.CheckVerification = func(callbackCtx context.Context, raw []byte) (bool, error) {
			var state StorageState
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &state); err != nil {
					return true, fmt.Errorf("解析验证状态: %w", err)
				}
			}
			return request.CheckVerification(callbackCtx, state)
		}
	}
	return camoufoxnative.Verify(ctx, options)
}
