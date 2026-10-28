//go:build !windows

package i18n

import "os"

// SystemLocale 按 LC_ALL、LC_MESSAGES 与 LANG 返回最接近的支持语言，都不支持时为英文
func SystemLocale() string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if locale := Match(os.Getenv(name)); locale != "" {
			return locale
		}
	}
	return "en"
}
