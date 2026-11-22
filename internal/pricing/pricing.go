// Package pricing 按 Gemini API 与 Vertex AI 公布的文本单价历史分段估算请求费用
package pricing

import (
	"slices"
	"time"
	_ "time/tzdata"
)

// LongContext 是长上下文分档的输入 token 界限，输入超过该值的请求按长上下文单价计费
const LongContext = 200_000

// price 是一个模型在某一时段内每百万 token 的美元单价，from 为生效起始时刻（零值表示自始生效），cached 为缓存命中输入单价（0 表示无缓存单价并按输入单价计），long 前缀为长上下文分档单价（0 表示不分档）
type price struct {
	from                              time.Time
	input, cached, output             float64
	longInput, longCached, longOutput float64
}

// pacificLocation 是官方价格页调价日期所用的美国太平洋时区
var pacificLocation = func() *time.Location {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		panic(err)
	}
	return location
}()

// pacificDate 返回美国太平洋时间指定日期零点的 UTC 时刻
func pacificDate(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, pacificLocation).UTC()
}

var (
	dec2026 = pacificDate(2026, time.December, 1)
	jan2027 = pacificDate(2027, time.January, 1)
)

// prices 是按 API 模型 ID 索引的文本生成单价历史分段，每项按生效时间升序排列，输出单价包含思考 token
var prices = map[string][]price{
	"gemini-4-carbon": {
		{input: 2, cached: 0.10, output: 10},
		{from: dec2026, input: 4, cached: 0.20, output: 20},
	},
	"gemini-4-argon": {
		{input: 2, cached: 0.10, output: 10},
		{from: dec2026, input: 4, cached: 0.20, output: 20},
	},
	"gemini-4-neon": {
		{input: 3, cached: 0.15, output: 15},
	},
	"gemini-4-hydrogen": {
		{input: 1.50, cached: 0.075, output: 7.50},
	},
	"gemini-3.8-flash": {
		{input: 0.75, cached: 0.075, output: 3.75},
		{from: jan2027, input: 1.50, cached: 0.15, output: 7.50},
	},
	"gemini-3.7-flash": {
		{input: 0.75, cached: 0.075, output: 3.75},
	},
	"gemini-3.6-flash": {
		{input: 0.75, cached: 0.075, output: 3.75},
		{from: jan2027, input: 1.50, cached: 0.15, output: 7.50},
	},
	"gemini-3.5-flash": {
		{input: 1.50, cached: 0.15, output: 9},
	},
	"gemini-3.5-flash-lite": {
		{input: 0.30, cached: 0.03, output: 2.50},
	},
	"gemini-3.1-pro-preview": {
		{input: 2, cached: 0.20, output: 12, longInput: 4, longCached: 0.40, longOutput: 18},
	},
	"gemini-3.1-pro-preview-customtools": {
		{input: 2, cached: 0.20, output: 12, longInput: 4, longCached: 0.40, longOutput: 18},
	},
	"gemini-3.1-flash-lite": {
		{input: 0.25, cached: 0.025, output: 1.50},
	},
	"gemini-3-flash-preview": {
		{input: 0.50, cached: 0.05, output: 3},
	},
	"gemini-2.5-pro": {
		{input: 1.25, cached: 0.125, output: 10, longInput: 2.50, longCached: 0.25, longOutput: 15},
	},
	"gemini-2.5-flash": {
		{input: 0.30, cached: 0.03, output: 2.50},
	},
	"gemini-2.5-flash-lite": {
		{input: 0.10, cached: 0.01, output: 0.40},
	},
	"gemini-robotics-er-2-preview": {
		{input: 1, cached: 0.10, output: 5},
		{from: jan2027, input: 2, cached: 0.20, output: 10},
	},
	"gemini-robotics-er-2-streaming-preview": {
		{input: 1, output: 5},
		{from: jan2027, input: 2, output: 10},
	},
	"gemini-decision-1": {
		{input: 0.10},
	},
}

// boundaries 是全部模型单价分段切换时刻的升序去重列表
var boundaries = collectBoundaries()

// collectBoundaries 收集单价表中全部非零的生效起始时刻并按升序去重
func collectBoundaries() []time.Time {
	var result []time.Time
	for _, entries := range prices {
		for _, entry := range entries {
			if !entry.from.IsZero() {
				result = append(result, entry.from)
			}
		}
	}
	slices.SortFunc(result, time.Time.Compare)
	return slices.CompactFunc(result, time.Time.Equal)
}

// priceAt 返回在 at 时刻生效的价格段
func priceAt(entries []price, at time.Time) price {
	selected := entries[0]
	for _, entry := range entries[1:] {
		if at.Before(entry.from) {
			break
		}
		selected = entry
	}
	return selected
}

// Boundaries 返回严格落在开区间 (from, to) 内的全部单价切换时刻，按时间升序排列
func Boundaries(from, to time.Time) []time.Time {
	var result []time.Time
	for _, at := range boundaries {
		if at.After(from) && at.Before(to) {
			result = append(result, at)
		}
	}
	return result
}

// Priced 返回模型是否有单价
func Priced(model string) bool {
	return len(prices[model]) > 0
}

// TierCost 返回一组同模型同分档请求在 at 时刻的美元费用，long 表示这些请求的输入都超过 LongContext，token 为各请求之和，cached 为 input 中命中缓存的部分，output 为思考与回复 token 之和，模型没有单价时为 0
func TierCost(model string, at time.Time, long bool, input, cached, output int64) float64 {
	entries := prices[model]
	if len(entries) == 0 {
		return 0
	}
	value := priceAt(entries, at)
	if value.longInput > 0 && long {
		value.input, value.cached, value.output = value.longInput, value.longCached, value.longOutput
	}
	cachedRate := value.cached
	if cachedRate == 0 {
		cachedRate = value.input
	}
	return (float64(input-cached)*value.input + float64(cached)*cachedRate + float64(output)*value.output) / 1e6
}
