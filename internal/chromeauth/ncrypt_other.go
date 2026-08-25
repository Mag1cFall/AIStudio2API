//go:build !windows

package chromeauth

import "fmt"

func openDeviceBindingKey([]byte) (deviceBindingKey, error) {
	return nil, fmt.Errorf("Chrome OAuth 导入仅支持 Windows")
}
