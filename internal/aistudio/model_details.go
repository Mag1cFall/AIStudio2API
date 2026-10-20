package aistudio

import (
	"encoding/json"
	"fmt"
)

// decodeModelDetails 解析 ListModels field 39：价格、简介、别名、提醒、文档与模型卡链接、发布日期与知识截止日期
func decodeModelDetails(value json.RawMessage, path string, evidence json.RawMessage) (*ModelDetails, error) {
	if isJSONNull(value) {
		return nil, nil
	}
	fields, err := rawArray(value, path, evidence)
	if err != nil {
		return nil, withMethod(err, "ListModels")
	}
	details := &ModelDetails{}
	texts := []struct {
		index  int
		target *string
	}{{9, &details.Summary}, {14, &details.Alias}, {15, &details.DocsURL}, {18, &details.Warning}, {21, &details.PricingNote}, {22, &details.ModelCardURL}}
	for _, item := range texts {
		if *item.target, err = optionalStringField(fields, item.index, path, evidence); err != nil {
			return nil, err
		}
	}
	if details.ReleaseDate, err = firstTimestamp(fields, 19, path, evidence); err != nil {
		return nil, err
	}
	if details.KnowledgeCutoff, err = firstTimestamp(fields, 20, path, evidence); err != nil {
		return nil, err
	}
	if err := decodeModelPricing(rawAt(fields, 0), path+"[0]", evidence, details); err != nil {
		return nil, err
	}
	if details.Summary == "" && details.Alias == "" && details.Warning == "" && details.DocsURL == "" && details.ModelCardURL == "" && details.ReleaseDate == 0 &&
		details.KnowledgeCutoff == 0 && len(details.Pricing) == 0 && details.PricingNote == "" {
		return nil, nil
	}
	return details, nil
}

// firstTimestamp 读取形如 ["1791907200"] 的时间戳字段
func firstTimestamp(fields []json.RawMessage, index int, path string, evidence json.RawMessage) (int64, error) {
	raw := rawAt(fields, index)
	if isJSONNull(raw) {
		return 0, nil
	}
	values, err := rawArray(raw, fmt.Sprintf("%s[%d]", path, index), evidence)
	if err != nil {
		return 0, withMethod(err, "ListModels")
	}
	return optionalIntField(values, 0, fmt.Sprintf("%s[%d]", path, index), evidence)
}

// decodeModelPricing 解析价格消息：field 4 为整句说明，field 1、2 为输入与输出分档，分档名称、上限与思考标记取自输入分档，没有输入分档时取自输出分档
func decodeModelPricing(value json.RawMessage, path string, evidence json.RawMessage, details *ModelDetails) error {
	if isJSONNull(value) {
		return nil
	}
	pricing, err := rawArray(value, path, evidence)
	if err != nil {
		return withMethod(err, "ListModels")
	}
	text, err := optionalStringField(pricing, 3, path, evidence)
	if err != nil {
		return err
	}
	if text != "" {
		details.Pricing = []ModelPrice{{Text: text}}
		details.PricingURL, err = optionalStringField(pricing, 4, path, evidence)
		return err
	}
	inputs, err := priceTiers(rawAt(pricing, 0), path+"[0]", evidence)
	if err != nil {
		return err
	}
	outputs, err := priceTiers(rawAt(pricing, 1), path+"[1]", evidence)
	if err != nil {
		return err
	}
	tiers := inputs
	if len(tiers) == 0 {
		tiers = outputs
	}
	for index, tier := range tiers {
		row := ModelPrice{Label: tier.label, UpTo: tier.upTo, Thinking: tier.thinking}
		if index < len(inputs) {
			row.Input, row.InputPerRequest = inputs[index].price, inputs[index].perRequest
		}
		if index < len(outputs) {
			row.Output, row.OutputPerRequest = outputs[index].price, outputs[index].perRequest
		}
		details.Pricing = append(details.Pricing, row)
	}
	return nil
}

// priceTier 是价格分档：[输入 token 上限, 最高档标记, 每百万 token 单价, 思考标记, 名称, 按次单价]
type priceTier struct {
	upTo       int64
	price      *float64
	perRequest bool
	thinking   bool
	label      string
}

// priceTiers 解析 [[[分档...]]] 形状的分档列表
func priceTiers(value json.RawMessage, path string, evidence json.RawMessage) ([]priceTier, error) {
	if isJSONNull(value) {
		return nil, nil
	}
	outer, err := rawArray(value, path, evidence)
	if err != nil {
		return nil, withMethod(err, "ListModels")
	}
	if isJSONNull(rawAt(outer, 0)) {
		return nil, nil
	}
	items, err := rawArray(rawAt(outer, 0), path+"[0]", evidence)
	if err != nil {
		return nil, withMethod(err, "ListModels")
	}
	tiers := make([]priceTier, 0, len(items))
	for index, item := range items {
		itemPath := fmt.Sprintf("%s[0][%d]", path, index)
		fields, err := rawArray(item, itemPath, evidence)
		if err != nil {
			return nil, withMethod(err, "ListModels")
		}
		var tier priceTier
		if tier.upTo, err = optionalIntField(fields, 0, itemPath, evidence); err != nil {
			return nil, err
		}
		if tier.label, err = optionalStringField(fields, 4, itemPath, evidence); err != nil {
			return nil, err
		}
		thinking, err := optionalIntField(fields, 3, itemPath, evidence)
		if err != nil {
			return nil, err
		}
		tier.thinking = thinking != 0
		for _, priceIndex := range []int{5, 2} {
			if raw := rawAt(fields, priceIndex); !isJSONNull(raw) {
				price, err := rawFloat64(raw, fmt.Sprintf("%s[%d]", itemPath, priceIndex), evidence)
				if err != nil {
					return nil, withMethod(err, "ListModels")
				}
				tier.price, tier.perRequest = &price, priceIndex == 5
				break
			}
		}
		tiers = append(tiers, tier)
	}
	return tiers, nil
}
