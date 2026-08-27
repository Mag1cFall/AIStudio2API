package aistudio

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// BenefitTier 表示 AI Studio 返回的账户权益等级
type BenefitTier int64

const (
	// BenefitTierFree 表示账户没有 Google AI 订阅权益
	BenefitTierFree BenefitTier = 0
	// BenefitTierPro 表示 Google AI Pro 权益
	BenefitTierPro BenefitTier = 1
	// BenefitTierUltra 表示 Google AI Ultra 权益
	BenefitTierUltra BenefitTier = 2
	// BenefitTierPlus 表示 Google AI Plus 权益
	BenefitTierPlus BenefitTier = 3
)

var tieredRPCMethods = map[string]struct{}{
	"GenerateContent":           {},
	"CountTokens":               {},
	"ProxyUnaryCall":            {},
	"CodeAssistantOffline":      {},
	"CancelInteraction":         {},
	"CreateInteraction":         {},
	"CreateInteractionStream":   {},
	"GetInteractionStream":      {},
	"GenerateVideo":             {},
	"GetGenerateVideoOperation": {},
	"StreamExtractVideoFrames":  {},
}

// String 返回账户权益的稳定显示名称
func (tier BenefitTier) String() string {
	switch tier {
	case BenefitTierPro:
		return "Pro"
	case BenefitTierUltra:
		return "Ultra"
	case BenefitTierPlus:
		return "Plus"
	default:
		return "Free"
	}
}

// HeaderValue 返回官网 RPC 使用的权益请求头值
func (tier BenefitTier) HeaderValue() string {
	switch tier {
	case BenefitTierPro:
		return "TIER1"
	case BenefitTierUltra:
		return "TIER2"
	case BenefitTierPlus:
		return "TIER0"
	default:
		return ""
	}
}

// BenefitTierForAccount 读取并缓存指定账户的官网权益等级
func (c *Client) BenefitTierForAccount(ctx context.Context, accountID string) (BenefitTier, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return BenefitTierFree, fmt.Errorf("GetAiStudioBenefitTier 缺少账户 ID")
	}
	response, err := c.do(ctx, "GetAiStudioBenefitTier", accountID, "", []byte("[]"), false)
	if err != nil {
		return BenefitTierFree, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return BenefitTierFree, fmt.Errorf("读取 GetAiStudioBenefitTier: %w", err)
	}
	value, err := decodeJSONValue(raw)
	if err != nil {
		return BenefitTierFree, withMethod(err, "GetAiStudioBenefitTier")
	}
	root, err := rawArray(value, "$", value)
	if err != nil {
		return BenefitTierFree, withMethod(err, "GetAiStudioBenefitTier")
	}
	if len(root) == 0 || isJSONNull(root[0]) {
		c.tierMu.Lock()
		c.tiers[accountID] = BenefitTierFree
		c.tierMu.Unlock()
		return BenefitTierFree, nil
	}
	wire, err := rawInt64(root[0], "$[0]", raw)
	if err != nil {
		c.tierMu.Lock()
		c.tiers[accountID] = BenefitTierFree
		c.tierMu.Unlock()
		return BenefitTierFree, nil
	}
	tier := BenefitTier(wire)
	if tier < BenefitTierFree || tier > BenefitTierPlus {
		tier = BenefitTierFree
	}
	c.tierMu.Lock()
	c.tiers[accountID] = tier
	c.tierMu.Unlock()
	return tier, nil
}

func (c *Client) applyBenefitTier(method string, accountID string, header http.Header) {
	if header == nil {
		return
	}
	if _, ok := tieredRPCMethods[method]; !ok {
		return
	}
	c.tierMu.RLock()
	tier := c.tiers[strings.TrimSpace(accountID)]
	c.tierMu.RUnlock()
	if value := tier.HeaderValue(); value != "" {
		header.Set("X-AIStudio-G1-Tier", value)
	}
}

func modelAllowedByTier(model Model, tier BenefitTier) bool {
	if len(model.AccessModes) == 0 {
		return true
	}
	for _, mode := range model.AccessModes {
		switch mode {
		case 3:
			if tier == BenefitTierPro || tier == BenefitTierUltra {
				return true
			}
		case 4:
			if tier == BenefitTierUltra {
				return true
			}
		}
	}
	return false
}
