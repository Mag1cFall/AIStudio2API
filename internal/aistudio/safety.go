package aistudio

import (
	"encoding/json"
	"fmt"
	"strings"
)

// harmCategories 为 Gemini API 安全类别名与 wire 编号，前四个是官网运行设置的类别
var harmCategories = []struct {
	name string
	code int64
}{
	{"HARM_CATEGORY_HARASSMENT", 7},
	{"HARM_CATEGORY_HATE_SPEECH", 8},
	{"HARM_CATEGORY_SEXUALLY_EXPLICIT", 9},
	{"HARM_CATEGORY_DANGEROUS_CONTENT", 10},
	{"HARM_CATEGORY_CIVIC_INTEGRITY", 11},
}

// ignoredHarmCategories 为 Gemini API 中合法但 Gemini 模型不接受或没有已知 wire 编号的安全类别
var ignoredHarmCategories = map[string]bool{
	"HARM_CATEGORY_UNSPECIFIED": true, "HARM_CATEGORY_DEROGATORY": true, "HARM_CATEGORY_TOXICITY": true,
	"HARM_CATEGORY_VIOLENCE": true, "HARM_CATEGORY_SEXUAL": true, "HARM_CATEGORY_MEDICAL": true,
	"HARM_CATEGORY_DANGEROUS": true, "HARM_CATEGORY_IMAGE_HATE": true, "HARM_CATEGORY_IMAGE_DANGEROUS_CONTENT": true,
	"HARM_CATEGORY_IMAGE_HARASSMENT": true, "HARM_CATEGORY_IMAGE_SEXUALLY_EXPLICIT": true,
}

// harmProbabilities 为 SafetyRating.probability 的 wire 编号与 Gemini API 枚举名
var harmProbabilities = []string{"HARM_PROBABILITY_UNSPECIFIED", "NEGLIGIBLE", "LOW", "MEDIUM", "HIGH"}

// decodeSafetyRatings 解码 repeated SafetyRating：category、probability、blocked 位于 field 3、4、5
func decodeSafetyRatings(raw json.RawMessage, path string, evidence json.RawMessage) ([]SafetyRating, error) {
	items, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	ratings := make([]SafetyRating, 0, len(items))
	for index, item := range items {
		fields, err := rawArray(item, fmt.Sprintf("%s[%d]", path, index), evidence)
		if err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
		rating := SafetyRating{Category: "HARM_CATEGORY_UNSPECIFIED", Probability: harmProbabilities[0]}
		if value := rawAt(fields, 2); !isJSONNull(value) {
			code, err := rawInt64(value, fmt.Sprintf("%s[%d][2]", path, index), evidence)
			if err != nil {
				return nil, withMethod(err, "GenerateContent")
			}
			rating.Category = fmt.Sprintf("HARM_CATEGORY_%d", code)
			for _, category := range harmCategories {
				if category.code == code {
					rating.Category = category.name
				}
			}
		}
		if value := rawAt(fields, 3); !isJSONNull(value) {
			code, err := rawInt64(value, fmt.Sprintf("%s[%d][3]", path, index), evidence)
			if err != nil {
				return nil, withMethod(err, "GenerateContent")
			}
			if code >= 0 && code < int64(len(harmProbabilities)) {
				rating.Probability = harmProbabilities[code]
			}
		}
		_ = json.Unmarshal(rawAt(fields, 4), &rating.Blocked)
		ratings = append(ratings, rating)
	}
	return ratings, nil
}

// buildHarmCategory 为只有 Build 接受的安全类别，Playground 没有该类别的 wire 编号
const buildHarmCategory = "HARM_CATEGORY_JAILBREAK"

// harmThresholds 为 Gemini API 拦截阈值名与 wire 编号
var harmThresholds = map[string]int64{
	"BLOCK_LOW_AND_ABOVE": 1, "BLOCK_MEDIUM_AND_ABOVE": 2, "BLOCK_ONLY_HIGH": 3, "BLOCK_NONE": 4, "OFF": 5,
}

// resolveSafetySettings 校验请求的安全设置，按类别编号返回最终阈值；非图片模型未指定的官网类别为 OFF
func resolveSafetySettings(settings []SafetySetting, imageRoute bool) ([]SafetySetting, error) {
	thresholds := map[string]string{}
	if !imageRoute {
		for _, category := range harmCategories[:4] {
			thresholds[category.name] = "OFF"
		}
	}
	for _, setting := range settings {
		category := strings.ToUpper(strings.TrimSpace(setting.Category))
		threshold := strings.ToUpper(strings.TrimSpace(setting.Threshold))
		known := category == buildHarmCategory
		for _, item := range harmCategories {
			known = known || item.name == category
		}
		if !known && !ignoredHarmCategories[category] {
			return nil, fmt.Errorf("safetySettings category %q 不受支持，可用类别为 HARM_CATEGORY_HARASSMENT、HARM_CATEGORY_HATE_SPEECH、HARM_CATEGORY_SEXUALLY_EXPLICIT、HARM_CATEGORY_DANGEROUS_CONTENT、HARM_CATEGORY_CIVIC_INTEGRITY", setting.Category)
		}
		if threshold == "HARM_BLOCK_THRESHOLD_UNSPECIFIED" {
			continue
		}
		if _, ok := harmThresholds[threshold]; !ok {
			return nil, fmt.Errorf("safetySettings threshold %q 不受支持", setting.Threshold)
		}
		if !known {
			continue
		}
		thresholds[category] = threshold
	}
	resolved := make([]SafetySetting, 0, len(thresholds))
	for _, category := range harmCategories {
		if threshold, ok := thresholds[category.name]; ok {
			resolved = append(resolved, SafetySetting{Category: category.name, Threshold: threshold})
		}
	}
	if threshold, ok := thresholds[buildHarmCategory]; ok {
		resolved = append(resolved, SafetySetting{Category: buildHarmCategory, Threshold: threshold})
	}
	return resolved, nil
}

// encodeSafetySettings 编码 GenerateContent 根字段 3 的安全设置
func encodeSafetySettings(settings []SafetySetting) []any {
	if len(settings) == 0 {
		return nil
	}
	wire := make([]any, 0, len(settings))
	for _, setting := range settings {
		for _, category := range harmCategories {
			if category.name == setting.Category {
				wire = append(wire, []any{nil, nil, category.code, harmThresholds[setting.Threshold]})
			}
		}
	}
	return wire
}
