package waa

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"golang.org/x/text/language"
)

//go:embed timezones.json.gz
var timeZoneNamesGzip []byte

// timeZoneNames 读取 Firefox Intl 导出的长时区名，按区域设置与 IANA 时区索引，值为一月与七月的名称
var timeZoneNames = sync.OnceValues(func() (map[string]map[string][]string, error) {
	data, err := gunzip(timeZoneNamesGzip)
	if err != nil {
		return nil, err
	}
	var names map[string]map[string][]string
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, err
	}
	return names, nil
})

// timeZoneOf 返回账户时区与 Date 字符串时区注释使用的 Firefox 显示名
func timeZoneOf(profile Profile) (*time.Location, func(time.Time) string, error) {
	zone := profile.TimeZone
	if zone == "" {
		zone = "UTC"
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, nil, fmt.Errorf("加载时区 %s: %w", zone, err)
	}
	names, err := timeZoneNames()
	if err != nil {
		return nil, nil, fmt.Errorf("读取时区显示名: %w", err)
	}
	entries := names[localeKey(slices.Sorted(maps.Keys(names)), profile.Locale)][zone]
	return location, func(moment time.Time) string {
		if len(entries) == 0 {
			_, offset := moment.Zone()
			return gmtOffsetName(offset)
		}
		if len(entries) == 1 {
			return entries[0]
		}
		januaryDST := time.Date(moment.Year(), time.January, 15, 12, 0, 0, 0, location).IsDST()
		if moment.IsDST() != januaryDST {
			return entries[1]
		}
		return entries[0]
	}, nil
}

// localeKey 在按名称排序的区域设置表中选择与区域设置最接近的一个
func localeKey(keys []string, locale string) string {
	if locale == "" {
		return "en-US"
	}
	tag := baseLocale(strings.ReplaceAll(locale, "_", "-"))
	for _, key := range keys {
		if strings.EqualFold(key, tag) {
			return key
		}
	}
	parsed := language.Make(tag)
	base, _ := parsed.Base()
	switch base.String() {
	case "zh":
		script, _ := parsed.Script()
		region, _ := parsed.Region()
		switch {
		case script.String() != "Hant":
			return "zh-CN"
		case region.String() == "HK" || region.String() == "MO":
			return "zh-HK"
		}
		return "zh-TW"
	case "en":
		return "en-US"
	}
	for _, key := range keys {
		if strings.HasPrefix(key, base.String()+"-") {
			return key
		}
	}
	return "en-US"
}

// gmtOffsetName 按 ICU 的本地化 GMT 格式返回无名称时区的显示名
func gmtOffsetName(offset int) string {
	if offset == 0 {
		return "GMT"
	}
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	return fmt.Sprintf("GMT%s%02d:%02d", sign, offset/3600, offset%3600/60)
}
