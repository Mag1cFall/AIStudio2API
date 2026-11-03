//go:build !windows

package chromeauth

import (
	"fmt"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func defaultChromeRoot() (string, error) {
	return defaultBrowserRoot(BrowserChrome)
}

func defaultBrowserRoot(browser string) (string, error) {
	return "", fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}

func ensurePlatformImport() error {
	return fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}

func discoverPlatform(string) ([]Account, error) {
	return nil, fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}

func discoverPlatformBrowser(browser string, root string) ([]Account, error) {
	return nil, fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}

func retrieveMasterKey(string, string) ([]byte, error) {
	return nil, fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}

func decryptDPAPIMasterKey(string) ([]byte, error) {
	return nil, fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}

func readChromiumCookies(string, string, string, []byte) ([]aistudio.StateCookie, error) {
	return nil, fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}

func readTokenService(string, string, string) (string, []byte, []byte, error) {
	return "", nil, nil, fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}
