package aistudio

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

const quotaResetTimezone = "America/Los_Angeles"

// CooldownState 表示账户或模型暂时不可调度的状态
type CooldownState struct {
	Until  time.Time `json:"until"`
	Reason string    `json:"reason,omitempty"`
}

// Active 判断冷却状态当前是否生效
func (c CooldownState) Active(now time.Time) bool {
	return !c.Until.IsZero() && now.Before(c.Until)
}

// QuotaCooldown 表示上游额度限制对应的调度冷却
type QuotaCooldown struct {
	Until  time.Time
	Global bool
	Kind   string
	Reason string
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
			until, kind = nextQuotaDay(now), "每日限额"
			break
		}
		if minuteQuotaEvidence(evidence) {
			until, kind = minuteQuotaReset(rpcError.Metadata["window_start_time"], now), "分钟限额"
			break
		}
	}
	if rpcError.RetryDelay > 0 {
		until = now.Add(rpcError.RetryDelay)
	}
	return QuotaCooldown{Until: until, Global: global, Kind: kind, Reason: kind + ": " + err.Error()}, true
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
