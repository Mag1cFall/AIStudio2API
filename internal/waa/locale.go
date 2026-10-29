package waa

import "strings"

// camouLocale 返回账户语言对应的 Camoufox 语言、地区与 navigator.language
func camouLocale(locale string) [3]string {
	language, region, _ := strings.Cut(locale, "-")
	return [3]string{strings.ToLower(language), strings.ToUpper(region), locale}
}
