package i18n

import "golang.org/x/sys/windows"

// SystemLocale 按 Windows 用户首选的界面语言返回最接近的支持语言，都不支持时为英文
func SystemLocale() string {
	languages, _ := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	for _, language := range languages {
		if locale := Match(language); locale != "" {
			return locale
		}
	}
	return "en"
}
