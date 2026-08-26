package aistudio

const (
	fallbackAccountLocale   = "en-US"
	fallbackAccountTimezone = "UTC"
)

// DefaultAccountLocale 返回当前用户的系统语言
func DefaultAccountLocale() string {
	return localAccountLocale()
}

// DefaultAccountTimezone 返回当前用户的 IANA 时区
func DefaultAccountTimezone() string {
	return localAccountTimezone()
}
