//go:build !windows

package chromeauth

import "fmt"

func defaultChromeRoot() (string, error) {
	return "", fmt.Errorf("Chrome OAuth 导入仅支持 Windows")
}

func ensurePlatformImport() error {
	return fmt.Errorf("Chrome OAuth 导入仅支持 Windows")
}

func discoverPlatform(string) ([]Account, error) {
	return nil, fmt.Errorf("Chrome OAuth 导入仅支持 Windows")
}

func readTokenService(string, string) (string, []byte, []byte, error) {
	return "", nil, nil, fmt.Errorf("Chrome OAuth 导入仅支持 Windows")
}
