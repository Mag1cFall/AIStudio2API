//go:build !windows

package aistudio

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultAccountLocale 返回当前用户的系统语言
func DefaultAccountLocale() string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if locale := normalizeSystemLocale(os.Getenv(name)); locale != "" {
			return locale
		}
	}
	return fallbackAccountLocale
}

// DefaultAccountTimezone 返回当前用户的 IANA 时区
func DefaultAccountTimezone() string {
	if zone := normalizeSystemTimezone(os.Getenv("TZ")); zone != "" {
		return zone
	}
	if zone := normalizeSystemTimezone(time.Local.String()); zone != "" {
		return zone
	}
	if target, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if zone := timezoneFromZoneinfoPath(target); zone != "" {
			return zone
		}
	}
	if value, err := os.ReadFile("/etc/timezone"); err == nil {
		if zone := normalizeSystemTimezone(string(value)); zone != "" {
			return zone
		}
	}
	return fallbackAccountTimezone
}

func normalizeSystemLocale(value string) string {
	value, _, _ = strings.Cut(strings.TrimSpace(value), ".")
	value, _, _ = strings.Cut(value, "@")
	if strings.EqualFold(value, "C") || strings.EqualFold(value, "POSIX") {
		return ""
	}
	return strings.ReplaceAll(value, "_", "-")
}

func normalizeSystemTimezone(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(value, ":"))
	if value == "" || value == "Local" {
		return ""
	}
	if _, err := time.LoadLocation(value); err != nil {
		return ""
	}
	return value
}

func timezoneFromZoneinfoPath(value string) string {
	const marker = "/zoneinfo/"
	index := strings.LastIndex(filepath.ToSlash(value), marker)
	if index < 0 {
		return ""
	}
	return normalizeSystemTimezone(filepath.ToSlash(value)[index+len(marker):])
}
