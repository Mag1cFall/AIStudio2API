package aistudio

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// BenefitTier represents the account benefit tier returned by AI Studio
type BenefitTier int64

const (
	// BenefitTierFree indicates that the account has no Google AI subscription benefit
	BenefitTierFree BenefitTier = 0
	// BenefitTierPro indicates Google AI Pro benefit
	BenefitTierPro BenefitTier = 1
	// BenefitTierUltra indicates Google AI Ultra benefit
	BenefitTierUltra BenefitTier = 2
	// BenefitTierPlus indicates Google AI Plus benefit
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

// String returns the stable display name of the benefit tier
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

// HeaderValue returns the header value used for official website RPC
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

// BenefitTierForAccount reads and caches the official benefit tier for the specified account
func (c *Client) BenefitTierForAccount(ctx context.Context, accountID string) (BenefitTier, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return BenefitTierFree, fmt.Errorf("GetAiStudioBenefitTier missing account ID")
	}
	response, err := c.do(ctx, "GetAiStudioBenefitTier", accountID, "", []byte("[]"), false)
	if err != nil {
		return BenefitTierFree, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return BenefitTierFree, fmt.Errorf("read GetAiStudioBenefitTier: %w", err)
	}
	tier, err := decodeBenefitTier(raw)
	if err != nil {
		return BenefitTierFree, err
	}
	c.tierMu.Lock()
	c.tiers[accountID] = tier
	c.tierMu.Unlock()
	return tier, nil
}

func decodeBenefitTier(raw []byte) (BenefitTier, error) {
	value, err := decodeJSONValue(raw)
	if err != nil {
		return BenefitTierFree, withMethod(err, "GetAiStudioBenefitTier")
	}
	root, err := rawArray(value, "$", value)
	if err != nil {
		return BenefitTierFree, withMethod(err, "GetAiStudioBenefitTier")
	}
	if len(root) == 0 || isJSONNull(root[0]) {
		return BenefitTierFree, nil
	}
	wire, err := rawInt64(root[0], "$[0]", raw)
	if err != nil {
		return BenefitTierFree, withMethod(err, "GetAiStudioBenefitTier")
	}
	tier := BenefitTier(wire)
	if tier < BenefitTierFree || tier > BenefitTierPlus {
		return BenefitTierFree, &ProtocolEvidenceError{
			Method: "GetAiStudioBenefitTier",
			Path:   "$[0]",
			Detail: fmt.Sprintf("unrecognized benefit tier enum %d", wire),
			Raw:    append([]byte(nil), raw...),
		}
	}
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
