package aistudio

const (
	fallbackAccountLocale   = "en-US"
	fallbackAccountTimezone = "UTC"
)

// DefaultAccountLocale returns the current user's system locale
func DefaultAccountLocale() string {
	return localAccountLocale()
}

// DefaultAccountTimezone returns the current user's IANA timezone
func DefaultAccountTimezone() string {
	return localAccountTimezone()
}
