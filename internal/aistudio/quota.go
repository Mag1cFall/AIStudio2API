package aistudio

import (
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

const quotaResetTimezone = "America/Los_Angeles"

// PlanLimitKind 是套餐限额冷却的类型名，也是其冷却原因的前缀
const PlanLimitKind = "套餐限额"

// DailyLimitKind 是每日限额冷却的类型名，也是其冷却原因的前缀
const DailyLimitKind = "每日限额"

// RequestQuotaCooldown 判断冷却原因是上游按每个请求判定是否放行的套餐限额或每日限额，用尽后较短的请求仍可能完成
func RequestQuotaCooldown(reason string) bool {
	return strings.HasPrefix(reason, PlanLimitKind) || strings.HasPrefix(reason, DailyLimitKind)
}

// quotaCooldownKinds 是上游以 429 限制额度时冷却的类型名
var quotaCooldownKinds = []string{"短期限额", "分钟限额", DailyLimitKind, PlanLimitKind, "无配额"}

// quotaCooldownReason 判断冷却原因是否来自上游 429 额度限制
func quotaCooldownReason(reason string) bool {
	return slices.ContainsFunc(quotaCooldownKinds, func(kind string) bool { return strings.HasPrefix(reason, kind+": ") })
}

// rejectedSizeWindow 是每日限额拒绝过的请求大小用于跳过不小于它的回退请求的时长
const rejectedSizeWindow = time.Minute

// planLimitBackoff 为没有恢复时间的套餐限额与短期限额按连续次数递增的冷却时长
var planLimitBackoff = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour}

// quotaLimitPattern 匹配额度错误中逐项列出的额度上限
var quotaLimitPattern = regexp.MustCompile(`limit: (\d+)`)

// usageLimitResetPattern 匹配套餐限额错误中 resets in 之后的小时与分钟
var usageLimitResetPattern = regexp.MustCompile(`resets in (?:(\d+) hours?)?(?:, )?(?:(\d+) minutes?)?`)

// CooldownState 表示账户或模型暂时不可调度的状态
type CooldownState struct {
	Until  time.Time `json:"until"`
	Reason string    `json:"reason,omitempty"`
	Level  int       `json:"level,omitempty"`
	// Fallback 是套餐限额或每日限额冷却期间，忽略这两类冷却的回退选号另受的较早结束的冷却
	Fallback *CooldownState `json:"fallback,omitempty"`
	// RejectedSize 是每日限额冷却期间上游在 RejectedUntil 之前的一分钟内以额度拒绝过的最小请求输入字节数
	RejectedSize int64 `json:"rejected_size,omitempty"`
	// RejectedUntil 是 RejectedSize 失效的时刻
	RejectedUntil time.Time `json:"rejected_until,omitzero"`
}

// Active 判断冷却状态当前是否生效
func (c CooldownState) Active(now time.Time) bool {
	return !c.Until.IsZero() && now.Before(c.Until)
}

// rejects 判断上游一分钟内以每日限额拒绝过不大于 size 的请求
func (c CooldownState) rejects(size int64, now time.Time) bool {
	return now.Before(c.RejectedUntil) && size >= c.RejectedSize
}

// QuotaCooldown 表示上游额度限制对应的调度冷却
type QuotaCooldown struct {
	Until    time.Time
	Global   bool
	Kind     string
	Reason   string
	Escalate bool
	// RequestSize 是被拒请求输入正文的字节数，0 表示未知
	RequestSize int64
}

// QuotaCooldownForError 解析上游分钟或每日额度限制
func QuotaCooldownForError(err error, now time.Time) (QuotaCooldown, bool) {
	var rpcError *RPCError
	if !errors.As(err, &rpcError) || rpcError.StatusCode != http.StatusTooManyRequests {
		return QuotaCooldown{}, false
	}
	metadata := strings.ToLower(strings.Join([]string{
		rpcError.Metadata["quota_metric"],
		rpcError.Metadata["quota_limit"],
		rpcError.Metadata["quota_unit"],
	}, " "))
	message := strings.ToLower(rpcError.Message)
	global := (strings.Contains(metadata, "_global") || strings.Contains(metadata, "perprojectperuser")) &&
		!strings.Contains(metadata, "per_model") && !strings.Contains(metadata, "permodel") && !strings.Contains(metadata, "{model}")
	until, kind := now.Add(time.Minute), "短期限额"
	for _, evidence := range []string{rpcError.Metadata["quota_unit"], rpcError.Metadata["quota_limit"], rpcError.Metadata["quota_metric"], message} {
		evidence = strings.ToLower(evidence)
		if dailyQuotaEvidence(evidence) {
			until, kind = nextQuotaDay(now), DailyLimitKind
			break
		}
		if minuteQuotaEvidence(evidence) {
			until, kind = minuteQuotaReset(rpcError.Metadata["window_start_time"], now), "分钟限额"
			break
		}
	}
	if zeroQuotaLimit(rpcError.Message) {
		kind = "无配额"
	}
	escalate := false
	if rpcError.RetryDelay > 0 {
		until = now.Add(rpcError.RetryDelay)
	} else if reset, ok := usageLimitReset(message); ok {
		until, kind = now.Add(reset), PlanLimitKind
	} else if kind == "短期限额" {
		escalate = true
		if rpcError.PerUserQuota || strings.Contains(message, "reached your usage limit") {
			kind = PlanLimitKind
		}
	}
	return QuotaCooldown{Until: until, Global: global, Kind: kind, Reason: kind + ": " + err.Error(), Escalate: escalate}, true
}

// BuildRejectionCooldown 返回 Build 代理以参数错误拒绝、同账户 Playground 完成的请求对应的 Build 递增冷却
func BuildRejectionCooldown(err error) (QuotaCooldown, bool) {
	var rpcError *RPCError
	if !errors.As(err, &rpcError) || rpcError.StatusCode != http.StatusBadRequest || rpcError.Code != 3 {
		return QuotaCooldown{}, false
	}
	return QuotaCooldown{Kind: "Build 拒绝", Reason: "Build 拒绝: " + err.Error(), Escalate: true}, true
}

// AccountRejectionKind 是上游以 403、协议错误码 7 暂时拒绝账户时冷却的类型名，也是其冷却原因的前缀
const AccountRejectionKind = "权限拒绝"

// AccountRejectionCooldown 返回上游以 403、协议错误码 7 拒绝账户且官网不跳转拒绝页时的冷却：账户在该模型上自上次成功后第一次被拒只记下不冷却，再被拒时以 planLimitBackoff 递增
func AccountRejectionCooldown(err error) (QuotaCooldown, bool) {
	var rpcError *RPCError
	if !errors.As(err, &rpcError) || rpcError.StatusCode != http.StatusForbidden || rpcError.Code != 7 || rpcError.accessDenial() != nil {
		return QuotaCooldown{}, false
	}
	return QuotaCooldown{Kind: AccountRejectionKind, Reason: AccountRejectionKind + ": " + rpcError.StatusError(), Escalate: true}, true
}

// BuildGenerationDisabled 判断错误是上游停用 Build 代理生成的 PROXY_GENERATION_DISABLED
func BuildGenerationDisabled(err error) bool {
	var rpcError *RPCError
	return errors.As(err, &rpcError) && rpcError.StatusCode == http.StatusBadRequest && rpcError.Code == 3 &&
		strings.Contains(rpcError.Message, "PROXY_GENERATION_DISABLED")
}

// zeroQuotaLimit 判断额度错误列出的每个额度项上限都为 0
func zeroQuotaLimit(message string) bool {
	matches := quotaLimitPattern.FindAllStringSubmatch(message, -1)
	for _, match := range matches {
		if match[1] != "0" {
			return false
		}
	}
	return len(matches) > 0
}

// usageLimitReset 返回套餐限额错误写明的恢复等待时长
func usageLimitReset(message string) (time.Duration, bool) {
	match := usageLimitResetPattern.FindStringSubmatch(message)
	if match == nil || match[1] == "" && match[2] == "" {
		return 0, false
	}
	hours, _ := strconv.Atoi(match[1])
	minutes, _ := strconv.Atoi(match[2])
	return time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute, true
}

// planLimitCooldown 按同一范围上一次套餐限额冷却计算本次冷却；上一次仍未结束时沿用它
func planLimitCooldown(previous CooldownState, exists bool, now time.Time, reason string) CooldownState {
	level := 1
	if exists && previous.Level > 0 {
		if previous.Active(now) {
			return previous
		}
		level = min(previous.Level+1, len(planLimitBackoff))
	}
	return CooldownState{Until: now.Add(planLimitBackoff[level-1]).UTC(), Reason: reason, Level: level}
}

func minuteQuotaEvidence(value string) bool {
	return strings.Contains(value, "/min/") || strings.Contains(value, "perminute") ||
		strings.Contains(value, "per_min") || strings.Contains(value, "per minute")
}

func dailyQuotaEvidence(value string) bool {
	return strings.Contains(value, "/day/") || strings.Contains(value, "perday") ||
		strings.Contains(value, "per_day") || strings.Contains(value, "per day") ||
		strings.Contains(value, "daily limit") || strings.Contains(value, "try again tomorrow")
}

func minuteQuotaReset(windowStart string, now time.Time) time.Time {
	seconds, err := strconv.ParseInt(strings.TrimSpace(windowStart), 10, 64)
	if err == nil {
		until := time.Unix(seconds, 0).Add(time.Minute)
		if until.After(now) && !until.After(now.Add(time.Minute)) {
			return until
		}
	}
	return now.Add(time.Minute)
}

func nextQuotaDay(now time.Time) time.Time {
	location, err := time.LoadLocation(quotaResetTimezone)
	if err != nil {
		panic(err)
	}
	local := now.In(location)
	return time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, location)
}
