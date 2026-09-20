//go:build !windows

package chromeauth

import "fmt"

func defaultChromeRoot() (string, error) {
	return "", fmt.Errorf("Chrome OAuth import is only supported on Windows")
}

func ensurePlatformImport() error {
	return fmt.Errorf("Chrome OAuth import is only supported on Windows")
}

func discoverPlatform(string) ([]Account, error) {
	return nil, fmt.Errorf("Chrome OAuth import is only supported on Windows")
}

func readTokenService(string, string) (string, []byte, []byte, error) {
	return "", nil, nil, fmt.Errorf("Chrome OAuth import is only supported on Windows")
}
