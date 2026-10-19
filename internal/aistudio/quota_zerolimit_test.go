package aistudio

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// rpc429 构造一个 429 RPCError，message 里带 limit: N
func rpc429(message string, retry time.Duration) error {
	return &RPCError{
		Method:     "ProxyUnaryCall",
		StatusCode: 429,
		Code:       8,
		Message:    message,
		Metadata:   map[string]string{},
		RetryDelay: retry,
	}
}

// TestZeroQuotaUsesBackoff 验证 limit: 0 走递增退避，而不是等 RetryDelay
func TestZeroQuotaUsesBackoff(t *testing.T) {
	now := time.Date(2026, 10, 19, 4, 49, 51, 0, time.UTC)
	// 上游原文：所有额度项都是 limit: 0，附带 retry in 19h10m
	msg := "Quota exceeded for metric: generate_requests_per_model_per_user, limit: 0, model: gemini-4-argon\n" +
		"Please retry in 19h10m8s."
	err := rpc429(msg, 19*time.Hour+10*time.Minute+8*time.Second)

	cd, ok := QuotaCooldownForError(err, now)
	if !ok {
		t.Fatal("应识别为额度冷却")
	}
	if cd.Kind != "无配额" {
		t.Fatalf("Kind 应为 无配额，实际 %q", cd.Kind)
	}
	if !cd.Escalate {
		t.Error("无配额应设 Escalate=true 以走递增退避")
	}
	// 关键：不能等于 now+19h10m
	want := now.Add(planLimitBackoff[0]) // 1 分钟
	if !cd.Until.Equal(want) {
		t.Errorf("Until 应为 %v（%v 后），实际 %v（%v 后）",
			want, planLimitBackoff[0], cd.Until, cd.Until.Sub(now))
	}
	if cd.Until.Sub(now) > 2*time.Minute {
		t.Errorf("冷却不应超过 2 分钟，实际 %v", cd.Until.Sub(now))
	}
}

// TestRealQuotaStillUsesRetryDelay 验证真额度用完仍按上游给的时间恢复
func TestRealQuotaStillUsesRetryDelay(t *testing.T) {
	now := time.Date(2026, 10, 19, 4, 49, 51, 0, time.UTC)
	// limit: 100 —— 是真额度，不是权限问题
	msg := "Quota exceeded for metric: generate_requests_per_model_per_user, limit: 100, model: gemini-3.8-flash\n" +
		"Please retry in 19h10m3s."
	retry := 19*time.Hour + 10*time.Minute + 3*time.Second
	err := rpc429(msg, retry)

	cd, ok := QuotaCooldownForError(err, now)
	if !ok {
		t.Fatal("应识别为额度冷却")
	}
	if cd.Kind == "无配额" {
		t.Error("limit: 100 不应被判为无配额")
	}
	want := now.Add(retry)
	if !cd.Until.Equal(want) {
		t.Errorf("Until 应为 %v，实际 %v", want, cd.Until)
	}
}

// TestMixedLimitIsNotZeroQuota 验证 limit 里有非零值时不算无配额
func TestMixedLimitIsNotZeroQuota(t *testing.T) {
	now := time.Now()
	msg := "Quota exceeded for metric: a, limit: 0, model: x\n" +
		"Quota exceeded for metric: b, limit: 50, model: x\nPlease retry in 1h."
	err := rpc429(msg, time.Hour)

	cd, _ := QuotaCooldownForError(err, now)
	if cd.Kind == "无配额" {
		t.Error("有任一非零 limit 时不应判为无配额")
	}
}

// TestNoLimitTokenIsNotZeroQuota 验证错误里没有 limit 时不误判
func TestNoLimitTokenIsNotZeroQuota(t *testing.T) {
	now := time.Now()
	err := rpc429("Rate exceeded, please slow down.", time.Minute)

	cd, _ := QuotaCooldownForError(err, now)
	if cd.Kind == "无配额" {
		t.Error("没有 limit 字段时不应判为无配额")
	}
}

// TestBackoffEscalates 验证连续无配额时冷却逐级拉长
func TestBackoffEscalates(t *testing.T) {
	base := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	reason := "无配额: test"

	// 第一次：1 分钟
	first := planLimitCooldown(CooldownState{}, false, base, reason)
	if got := first.Until.Sub(base); got != time.Minute {
		t.Fatalf("第一次应为 1 分钟，实际 %v", got)
	}
	if first.Level != 1 {
		t.Fatalf("第一次 Level 应为 1，实际 %d", first.Level)
	}

	// 第二次（上次已过期）：5 分钟
	second := planLimitCooldown(first, true, base.Add(2*time.Minute), reason)
	if got := second.Until.Sub(base.Add(2 * time.Minute)); got != 5*time.Minute {
		t.Errorf("第二次应为 5 分钟，实际 %v", got)
	}
	if second.Level != 2 {
		t.Errorf("第二次 Level 应为 2，实际 %d", second.Level)
	}

	// 第三次：15 分钟
	third := planLimitCooldown(second, true, base.Add(time.Hour), reason)
	if got := third.Until.Sub(base.Add(time.Hour)); got != 15*time.Minute {
		t.Errorf("第三次应为 15 分钟，实际 %v", got)
	}

	// 封顶 1 小时
	prev := third
	at := base.Add(2 * time.Hour)
	for i := 0; i < 5; i++ {
		next := planLimitCooldown(prev, true, at, reason)
		prev = next
		at = at.Add(2 * time.Hour)
	}
	if got := prev.Until.Sub(at.Add(-2 * time.Hour)); got != time.Hour {
		t.Errorf("封顶应为 1 小时，实际 %v", got)
	}
	if prev.Level != len(planLimitBackoff) {
		t.Errorf("Level 应封顶为 %d，实际 %d", len(planLimitBackoff), prev.Level)
	}
}

// TestBackoffKeepsActiveCooldown 验证冷却未结束时沿用原值不重置
func TestBackoffKeepsActiveCooldown(t *testing.T) {
	base := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	prev := CooldownState{Until: base.Add(10 * time.Minute), Reason: "x", Level: 3}
	got := planLimitCooldown(prev, true, base, "y")
	if !got.Until.Equal(prev.Until) || got.Level != prev.Level {
		t.Errorf("冷却未结束时应原样返回，实际 %+v", got)
	}
}

// TestZeroQuotaKeepsGlobalFlag 验证 global 标记在无配额分支里仍保留
func TestZeroQuotaKeepsGlobalFlag(t *testing.T) {
	now := time.Now()
	err := &RPCError{
		Method: "GenerateContent", StatusCode: 429, Code: 8,
		Message:    "Quota exceeded, limit: 0, model: x",
		Metadata:   map[string]string{"quota_metric": "generate_requests_per_project_per_user_global"},
		RetryDelay: 19 * time.Hour,
	}
	cd, ok := QuotaCooldownForError(err, now)
	if !ok {
		t.Fatal("应识别")
	}
	if !cd.Global {
		t.Error("per_project_per_user_global 应设 Global=true")
	}
	if cd.Kind != "无配额" {
		t.Errorf("Kind 应为 无配额，实际 %q", cd.Kind)
	}
}

// TestNon429Ignored 验证非 429 不产生冷却
func TestNon429Ignored(t *testing.T) {
	now := time.Now()
	err := &RPCError{StatusCode: 500, Message: "boom"}
	if _, ok := QuotaCooldownForError(err, now); ok {
		t.Error("500 不应产生额度冷却")
	}
	var plain = errors.New("network")
	if _, ok := QuotaCooldownForError(plain, now); ok {
		t.Error("普通错误不应产生额度冷却")
	}
}

// 确保 fmt 被使用（调试时方便）
var _ = fmt.Sprintf
