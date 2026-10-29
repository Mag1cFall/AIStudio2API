package aistudio

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

// NativeLoginDriver 通过纯 Go WebDriver BiDi 完成隔离登录
type NativeLoginDriver struct {
	camoufox string
	timeout  time.Duration
}

var _ IsolatedLoginDriver = (*NativeLoginDriver)(nil)

// NewNativeLoginDriver 创建纯 Go Camoufox 登录驱动
func NewNativeLoginDriver(camoufoxPath string, timeout time.Duration) (*NativeLoginDriver, error) {
	absolute, err := filepath.Abs(camoufoxPath)
	if err != nil {
		return nil, fmt.Errorf("解析 Camoufox 路径: %w", err)
	}
	return &NativeLoginDriver{camoufox: absolute, timeout: timeout}, nil
}

// Login 启动隔离 Camoufox 供用户登录并导出认证状态
func (driver *NativeLoginDriver) Login(ctx context.Context, request IsolatedLoginRequest) (IsolatedLoginResult, error) {
	result, err := camoufoxnative.Login(ctx, camoufoxnative.LoginOptions{
		ExecutablePath: driver.camoufox,
		Directory:      request.Directory,
		Locale:         request.Locale,
		Timezone:       request.Timezone,
		Proxy:          request.Proxy,
		Timeout:        driver.timeout,
		View:           request.View,
	})
	if err != nil {
		return IsolatedLoginResult{}, err
	}
	var state StorageState
	if err := json.Unmarshal(result.StorageStateJSON, &state); err != nil {
		return IsolatedLoginResult{}, fmt.Errorf("解析隔离登录状态: %w", err)
	}
	state.SetAuthExtension(AuthExtension{
		Source: AuthSource{Browser: "camoufox", Email: result.Email},
	})
	return IsolatedLoginResult{
		StorageState: state, Email: result.Email, VerifiedAt: result.VerifiedAt,
		DriveError: result.DriveError, CloseError: result.CloseError,
	}, nil
}
