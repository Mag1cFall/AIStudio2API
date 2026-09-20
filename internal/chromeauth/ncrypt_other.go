//go:build !windows

package chromeauth

import "fmt"

func openDeviceBindingKey([]byte) (deviceBindingKey, error) {
	return nil, fmt.Errorf("Chrome OAuth import is only supported on Windows")
}
