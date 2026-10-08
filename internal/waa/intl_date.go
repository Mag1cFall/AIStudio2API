package waa

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// dateLocale 是 Firefox 在一个区域设置下导出的 Intl.DateTimeFormat 模式表与日期符号
type dateLocale struct {
	Resolved        string   `json:"resolved"`
	Calendar        string   `json:"calendar"`
	NumberingSystem string   `json:"numberingSystem"`
	HourCycles      []string `json:"hourCycles"`
	Months          struct {
		Format     map[string][]string `json:"format"`
		Standalone map[string][]string `json:"standalone"`
	} `json:"months"`
	Weekdays struct {
		Format     map[string][]string `json:"format"`
		Standalone map[string][]string `json:"standalone"`
	} `json:"weekdays"`
	Eras     map[string][]string `json:"eras"`
	AMPM     []string            `json:"ampm"`
	Flexible map[string]struct {
		Hour   []string `json:"hour"`
		Minute []string `json:"minute"`
	} `json:"flexible"`
	Range      string            `json:"range"`
	Patterns   []string          `json:"patterns"`
	Date       []int             `json:"date"`
	Time       []int             `json:"time"`
	Styles     map[string]int    `json:"styles"`
	Glue       map[string]string `json:"glue"`
	Exceptions map[string]int    `json:"exceptions"`
	Offset     struct {
		Prefix    string `json:"prefix"`
		Minus     string `json:"minus"`
		Separator string `json:"separator"`
	} `json:"offset"`
	Zones map[string][4][]*string `json:"zones"`
}

// dateLocales 缓存已读取的日期区域设置数据
var dateLocales sync.Map

// dateLocaleFor 读取与标签最接近的日期区域设置数据
func dateLocaleFor(tag string) (*dateLocale, error) {
	common, _ := intlCommon()
	locale := localeKey(common.DataLocales, tag)
	if cached, ok := dateLocales.Load(locale); ok {
		return cached.(*dateLocale), nil
	}
	var data dateLocale
	if err := readIntlFile(locale, &data); err != nil {
		return nil, fmt.Errorf("读取 %s 日期数据: %w", locale, err)
	}
	actual, _ := dateLocales.LoadOrStore(locale, &data)
	return actual.(*dateLocale), nil
}

// defaultDateIndex 是 year、month、day 均为 numeric 的日期模式位置
const defaultDateIndex = ((1*6)+1)*3 + 1

// dateOptionIndex 返回取值在选项取值表中的位置，未设置为 0
func dateOptionIndex(value string, values ...string) int {
	for index, item := range values {
		if item == value {
			return index + 1
		}
	}
	return 0
}

// dateClass 返回日期部分对应的日期时间连接模式类别
func dateClass(weekday, month string) string {
	switch month {
	case "long":
		if weekday != "" {
			return "full"
		}
		return "long"
	case "short":
		return "medium"
	}
	return "short"
}

// formatContext 把日期模式中的独立星期字段改为与时间组合时使用的格式字段
func formatContext(pattern string) string {
	var builder strings.Builder
	quoted := false
	runes := []rune(pattern)
	for index := 0; index < len(runes); index++ {
		character := runes[index]
		if character == '\'' {
			quoted = !quoted
			builder.WriteRune(character)
			continue
		}
		if quoted || character != 'c' {
			builder.WriteRune(character)
			continue
		}
		count := 1
		for index+count < len(runes) && runes[index+count] == character {
			count++
		}
		index += count - 1
		if count == 3 {
			builder.WriteString("E")
		} else {
			builder.WriteString(strings.Repeat("E", count))
		}
	}
	return builder.String()
}

// patternEntry 拆分模式表条目为模式与解析后的选项
func (data *dateLocale) patternEntry(index int) (string, string) {
	pattern, resolved, _ := strings.Cut(data.Patterns[index], "\t")
	return pattern, resolved
}

// pattern 按 weekday、era、year、month、day、hour、minute、second、fractionalSecondDigits、dayPeriod、timeZoneName、hourCycle、dateStyle、timeStyle 选项返回 ICU 模式与 Firefox 解析后的选项
func (data *dateLocale) pattern(key []string) (string, string) {
	weekday, era, year, month, day := key[0], key[1], key[2], key[3], key[4]
	hour, minute, second, fraction, period, zone, hourCycle := key[5], key[6], key[7], key[8], key[9], key[10], key[11]
	dateStyle, timeStyle := key[12], key[13]
	if dateStyle != "" || timeStyle != "" {
		index, ok := data.Styles[dateStyle+"|"+timeStyle+"|"+hourCycle]
		if !ok {
			index = data.Styles[dateStyle+"|"+timeStyle+"|"]
		}
		return data.patternEntry(index)
	}
	widths := []string{"narrow", "short", "long"}
	dateIndex := (((dateOptionIndex(weekday, widths...)*4+dateOptionIndex(era, widths...))*3+dateOptionIndex(year, "numeric", "2-digit"))*6+dateOptionIndex(month, "numeric", "2-digit", "narrow", "short", "long"))*3 + dateOptionIndex(day, "numeric", "2-digit")
	timeIndex := (((dateOptionIndex(minute, "numeric", "2-digit")*3+dateOptionIndex(second, "numeric", "2-digit"))*4+dateOptionIndex(fraction, "1", "2", "3"))*4+dateOptionIndex(period, widths...))*7 + dateOptionIndex(zone, "short", "long", "shortOffset", "longOffset", "shortGeneric", "longGeneric")
	switch hour {
	case "numeric":
		timeIndex = 1008 + timeIndex*5 + dateOptionIndex(hourCycle, "h11", "h12", "h23", "h24")
	case "2-digit":
		timeIndex = 6048 + timeIndex*5 + dateOptionIndex(hourCycle, "h11", "h12", "h23", "h24")
	}
	hasDate := dateIndex != 0
	hasTime := hour != "" || minute != "" || second != "" || fraction != "" || period != ""
	defaultPattern, _ := data.patternEntry(data.Date[defaultDateIndex])
	switch {
	case hasDate && !hasTime && zone != "":
		datePattern, dateResolved := data.patternEntry(data.Date[dateIndex])
		zonePattern, _ := data.patternEntry(data.Time[timeIndex])
		if !strings.Contains(zonePattern, defaultPattern) {
			return zonePattern, dateResolved + ";timeZoneName=" + zone
		}
		return strings.Replace(zonePattern, defaultPattern, datePattern, 1), dateResolved + ";timeZoneName=" + zone
	case hasDate && hasTime:
		if index, ok := data.Exceptions[strconv.Itoa(dateIndex)+","+strconv.Itoa(timeIndex)]; ok {
			return data.patternEntry(index)
		}
		datePattern, dateResolved := data.patternEntry(data.Date[dateIndex])
		if weekday == "" && year == "" && month == "" && day == "" {
			if start := strings.IndexByte(datePattern, 'G'); start >= 0 {
				end := start
				for end < len(datePattern) && datePattern[end] == 'G' {
					end++
				}
				datePattern = datePattern[start:end]
			}
			dateResolved = "era=" + era
		}
		timePattern, timeResolved := data.patternEntry(data.Time[timeIndex])
		glue := data.Glue[dateClass(weekday, month)]
		combined := strings.Replace(strings.Replace(glue, "{1}", formatContext(datePattern), 1), "{0}", timePattern, 1)
		return combined, strings.Trim(dateResolved+";"+timeResolved, ";")
	case hasTime:
		return data.patternEntry(data.Time[timeIndex])
	default:
		return data.patternEntry(data.Date[dateIndex])
	}
}

// datePart 是 formatToParts 的一个片段
type datePart = [2]string

// timeLocations 缓存按名称加载的时区
var timeLocations sync.Map

// locationOf 返回 IANA 时区或 ±HH:MM 偏移时区
func locationOf(zone string) *time.Location {
	if cached, ok := timeLocations.Load(zone); ok {
		return cached.(*time.Location)
	}
	var location *time.Location
	if len(zone) == 6 && (zone[0] == '+' || zone[0] == '-') {
		hours, _ := strconv.Atoi(zone[1:3])
		minutes, _ := strconv.Atoi(zone[4:6])
		offset := hours*3600 + minutes*60
		if zone[0] == '-' {
			offset = -offset
		}
		location = time.FixedZone(zone, offset)
	} else {
		location, _ = time.LoadLocation(zone)
	}
	actual, _ := timeLocations.LoadOrStore(zone, location)
	return actual.(*time.Location)
}

// localizeDigits 把 ASCII 数字替换为数字系统的十进制数字
func localizeDigits(text string, digits string) string {
	if digits == "" {
		return text
	}
	table := []rune(digits)
	if table[0] == '0' {
		return text
	}
	return strings.Map(func(character rune) rune {
		if character >= '0' && character <= '9' {
			return table[character-'0']
		}
		return character
	}, text)
}

// zoneName 返回时区在指定时刻的显示名，未收录时返回空串
func (data *dateLocale) zoneName(zone string, kind int, moment time.Time, location *time.Location) string {
	entry, ok := data.Zones[zone]
	if !ok || entry[kind] == nil {
		return ""
	}
	names := entry[kind]
	choice := names[0]
	if len(names) > 1 {
		january := time.Date(moment.Year(), time.January, 15, 12, 0, 0, 0, location).IsDST()
		if moment.IsDST() != january {
			choice = names[1]
		}
	}
	if choice == nil {
		return ""
	}
	return *choice
}

// offsetName 按本地化 GMT 格式返回偏移，long 时小时补足两位
func (data *dateLocale) offsetName(offset int, long bool) string {
	if offset == 0 {
		return data.Offset.Prefix
	}
	sign := "+"
	if offset < 0 {
		sign = data.Offset.Minus
		offset = -offset
	}
	hours, minutes := offset/3600, offset%3600/60
	if long {
		return fmt.Sprintf("%s%s%02d%s%02d", data.Offset.Prefix, sign, hours, data.Offset.Separator, minutes)
	}
	if minutes == 0 {
		return fmt.Sprintf("%s%s%d", data.Offset.Prefix, sign, hours)
	}
	return fmt.Sprintf("%s%s%d%s%02d", data.Offset.Prefix, sign, hours, data.Offset.Separator, minutes)
}

// widthOf 返回字段字母重复次数对应的文本宽度
func widthOf(count int) string {
	switch {
	case count == 4:
		return "long"
	case count >= 5:
		return "narrow"
	}
	return "short"
}

// format 按 ICU 模式格式化 epoch 毫秒时刻，返回 formatToParts 片段
func (data *dateLocale) format(pattern string, epoch float64, zone, calendar, digits string) []datePart {
	location := locationOf(zone)
	milliseconds := int64(math.Floor(epoch))
	moment := time.UnixMilli(milliseconds).In(location)
	year := moment.Year()
	if calendar == "buddhist" {
		year += 543
	}
	eraIndex := 1
	eraYear := year
	if year <= 0 {
		eraIndex, eraYear = 0, 1-year
	}
	hasMinute := strings.ContainsRune(pattern, 'm')
	var parts []datePart
	literal := func(text string) {
		if text == "" {
			return
		}
		if count := len(parts); count > 0 && parts[count-1][0] == "literal" {
			parts[count-1][1] += text
			return
		}
		parts = append(parts, datePart{"literal", text})
	}
	number := func(kind string, value, width int) {
		text := strconv.Itoa(value)
		for len(text) < width {
			text = "0" + text
		}
		parts = append(parts, datePart{kind, localizeDigits(text, digits)})
	}
	pick := func(values []string, index int) string {
		if index >= 0 && index < len(values) {
			return values[index]
		}
		return ""
	}
	runes := []rune(pattern)
	for index := 0; index < len(runes); {
		character := runes[index]
		if character == '\'' {
			end := index + 1
			var text strings.Builder
			for end < len(runes) {
				if runes[end] == '\'' {
					if end+1 < len(runes) && runes[end+1] == '\'' {
						text.WriteRune('\'')
						end += 2
						continue
					}
					break
				}
				text.WriteRune(runes[end])
				end++
			}
			if end == index+1 && end < len(runes) {
				text.WriteRune('\'')
			}
			literal(text.String())
			index = end + 1
			continue
		}
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z') {
			literal(string(character))
			index++
			continue
		}
		count := 1
		for index+count < len(runes) && runes[index+count] == character {
			count++
		}
		index += count
		hour := moment.Hour()
		switch character {
		case 'G':
			parts = append(parts, datePart{"era", pick(data.Eras[map[int]string{1: "short", 2: "short", 3: "short", 4: "long", 5: "narrow"}[min(count, 5)]], eraIndex)})
		case 'y':
			if count == 2 {
				number("year", eraYear%100, 2)
			} else {
				number("year", eraYear, count)
			}
		case 'M', 'L':
			month := int(moment.Month()) - 1
			if count <= 2 {
				number("month", month+1, count)
				break
			}
			table := data.Months.Format
			if character == 'L' {
				table = data.Months.Standalone
			}
			parts = append(parts, datePart{"month", pick(table[widthOf(count)], month)})
		case 'd':
			number("day", moment.Day(), count)
		case 'E', 'c':
			table := data.Weekdays.Format
			if character == 'c' {
				table = data.Weekdays.Standalone
			}
			parts = append(parts, datePart{"weekday", pick(table[widthOf(max(count, 3))], int(moment.Weekday()))})
		case 'a':
			period := 0
			if hour >= 12 {
				period = 1
			}
			parts = append(parts, datePart{"dayPeriod", pick(data.AMPM, period)})
		case 'B':
			width := widthOf(count)
			if count == 1 {
				width = "short"
			}
			table := data.Flexible[width].Hour
			if hasMinute {
				table = data.Flexible[width].Minute
			}
			half := hour * 2
			if moment.Minute() >= 30 {
				half++
			}
			parts = append(parts, datePart{"dayPeriod", pick(table, half)})
		case 'h':
			value := hour % 12
			if value == 0 {
				value = 12
			}
			number("hour", value, count)
		case 'H':
			number("hour", hour, count)
		case 'K':
			number("hour", hour%12, count)
		case 'k':
			value := hour
			if value == 0 {
				value = 24
			}
			number("hour", value, count)
		case 'm':
			number("minute", moment.Minute(), count)
		case 's':
			number("second", moment.Second(), count)
		case 'S':
			fraction := strconv.Itoa(int(((milliseconds % 1000) + 1000) % 1000))
			for len(fraction) < 3 {
				fraction = "0" + fraction
			}
			for len(fraction) < count {
				fraction += "0"
			}
			parts = append(parts, datePart{"fractionalSecond", localizeDigits(fraction[:count], digits)})
		case 'z', 'v', 'O':
			_, offset := moment.Zone()
			long := count >= 4
			name := ""
			switch character {
			case 'z':
				name = data.zoneName(zone, map[bool]int{false: 0, true: 1}[long], moment, location)
			case 'v':
				name = data.zoneName(zone, map[bool]int{false: 2, true: 3}[long], moment, location)
			}
			if name == "" {
				name = localizeDigits(data.offsetName(offset, long), digits)
			}
			parts = append(parts, datePart{"timeZoneName", name})
		default:
			literal(strings.Repeat(string(character), count))
		}
	}
	return parts
}
