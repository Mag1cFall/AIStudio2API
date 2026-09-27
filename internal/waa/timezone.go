package waa

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

//go:embed timezones.json.gz
var timeZoneNamesGzip []byte

// timeZoneNames 读取 Firefox Intl 导出的长时区名，按区域设置与 IANA 时区索引，值为一月与七月的名称
var timeZoneNames = sync.OnceValues(func() (map[string]map[string][]string, error) {
	reader, err := gzip.NewReader(bytes.NewReader(timeZoneNamesGzip))
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(reader)
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
	entries := names[localeKey(names, profile.Locale)][zone]
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

// localeKey 选择与账户区域设置最接近的显示名表
func localeKey(names map[string]map[string][]string, locale string) string {
	if locale == "" {
		return "en-US"
	}
	if _, ok := names[locale]; ok {
		return locale
	}
	keys := make([]string, 0, len(names))
	for key := range names {
		keys = append(keys, key)
		if strings.EqualFold(key, locale) {
			return key
		}
	}
	language, region, _ := strings.Cut(strings.ReplaceAll(locale, "_", "-"), "-")
	language = strings.ToLower(language)
	if language == "zh" {
		upper := strings.ToUpper(region)
		if upper == "HK" || upper == "MO" {
			return "zh-HK"
		}
		if upper == "TW" || strings.EqualFold(region, "Hant") {
			return "zh-TW"
		}
		return "zh-CN"
	}
	if language == "en" {
		return "en-US"
	}
	sort.Strings(keys)
	for _, key := range keys {
		if strings.HasPrefix(strings.ToLower(key), language+"-") {
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
