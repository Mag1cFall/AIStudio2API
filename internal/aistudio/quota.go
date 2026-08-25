package aistudio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// BenefitTierState 保留 GetAiStudioBenefitTier 的原始枚举码
type BenefitTierState struct {
	RawCode int64 `json:"raw_code"`
}

// QuotaState 表示账户单个模型的运行时配额与冷却状态
type QuotaState struct {
	ModelID       string     `json:"model_id"`
	Limit         *int64     `json:"limit,omitempty"`
	Remaining     *int64     `json:"remaining,omitempty"`
	ResetAt       *time.Time `json:"reset_at,omitempty"`
	CooldownUntil *time.Time `json:"cooldown_until,omitempty"`
	Reason        string     `json:"reason,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// CooldownState 表示账户或模型暂时不可调度的状态
type CooldownState struct {
	Until  time.Time `json:"until"`
	Reason string    `json:"reason,omitempty"`
}

// CoolingDown 判断配额状态当前是否处于冷却期
func (q QuotaState) CoolingDown(now time.Time) bool {
	return q.CooldownUntil != nil && now.Before(*q.CooldownUntil)
}

// Active 判断冷却状态当前是否生效
func (c CooldownState) Active(now time.Time) bool {
	return !c.Until.IsZero() && now.Before(c.Until)
}

// ParseRetryAfter 解析 Retry-After 秒数或 HTTP 时间
func ParseRetryAfter(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("Retry-After 为空")
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 {
			return time.Time{}, fmt.Errorf("Retry-After 秒数不能为负数")
		}
		return now.Add(time.Duration(seconds) * time.Second), nil
	}
	parsed, err := http.ParseTime(value)
	if err != nil {
		return time.Time{}, fmt.Errorf("Retry-After 格式无效")
	}
	return parsed, nil
}

// DecodeBenefitTier 解析已验证的单枚举响应形状
func DecodeBenefitTier(data []byte) (BenefitTierState, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var payload []json.RawMessage
	if err := decoder.Decode(&payload); err != nil {
		return BenefitTierState{}, fmt.Errorf("解析 benefit tier: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return BenefitTierState{}, fmt.Errorf("解析 benefit tier: %w", err)
	}
	if len(payload) != 1 {
		return BenefitTierState{}, fmt.Errorf("benefit tier 响应形状无效")
	}
	var number json.Number
	if err := json.Unmarshal(payload[0], &number); err != nil {
		return BenefitTierState{}, fmt.Errorf("benefit tier 枚举无效")
	}
	code, err := number.Int64()
	if err != nil {
		return BenefitTierState{}, fmt.Errorf("benefit tier 枚举无效")
	}
	return BenefitTierState{RawCode: code}, nil
}
