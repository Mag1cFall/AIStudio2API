// Package pricing 按 Gemini API 与 Vertex AI 公布的当前文本单价估算请求费用
package pricing

// longContext 是长上下文分档的输入 token 界限，输入超过该值的请求按长上下文单价计费
const longContext = 200_000

// price 是一个模型每百万 token 的美元单价，cached 为缓存命中输入单价，long 前缀为长上下文分档单价，0 表示不分档
type price struct {
	input, cached, output             float64
	longInput, longCached, longOutput float64
}

// prices 是按 API 模型 ID 索引的文本生成单价，输出单价包含思考 token
var prices = map[string]price{
	"gemini-4-argon":                         {input: 2, cached: 0.10, output: 10},
	"gemini-3.8-flash":                       {input: 0.75, cached: 0.075, output: 3.75},
	"gemini-3.7-flash":                       {input: 0.75, cached: 0.075, output: 3.75},
	"gemini-3.6-flash":                       {input: 0.75, cached: 0.075, output: 3.75},
	"gemini-3.5-flash":                       {input: 1.50, cached: 0.15, output: 9},
	"gemini-3.5-flash-lite":                  {input: 0.30, cached: 0.03, output: 2.50},
	"gemini-3.1-pro-preview":                 {input: 2, cached: 0.20, output: 12, longInput: 4, longCached: 0.40, longOutput: 18},
	"gemini-3.1-pro-preview-customtools":     {input: 2, cached: 0.20, output: 12, longInput: 4, longCached: 0.40, longOutput: 18},
	"gemini-3.1-flash-lite":                  {input: 0.25, cached: 0.025, output: 1.50},
	"gemini-3-flash-preview":                 {input: 0.50, cached: 0.05, output: 3},
	"gemini-2.5-pro":                         {input: 1.25, cached: 0.125, output: 10, longInput: 2.50, longCached: 0.25, longOutput: 15},
	"gemini-2.5-flash":                       {input: 0.30, cached: 0.03, output: 2.50},
	"gemini-2.5-flash-lite":                  {input: 0.10, cached: 0.01, output: 0.40},
	"gemini-robotics-er-2-preview":           {input: 1, cached: 0.10, output: 5},
	"gemini-robotics-er-2-streaming-preview": {input: 1, cached: 0.10, output: 5},
}

// Cost 返回一个请求按模型单价估算的美元费用，cached 为 input 中命中缓存的部分，output 为思考与回复 token 之和，模型没有单价时为 0
func Cost(model string, input, cached, output int64) float64 {
	value := prices[model]
	if value.longInput > 0 && input > longContext {
		value.input, value.cached, value.output = value.longInput, value.longCached, value.longOutput
	}
	return (float64(input-cached)*value.input + float64(cached)*value.cached + float64(output)*value.output) / 1e6
}
