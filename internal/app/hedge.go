package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

// firstEventSampleLimit 是每个模型与通道保留的近期原请求首事件样本数
const firstEventSampleLimit = 200

// firstEventSampleAge 是首事件样本参与统计的最长时间
const firstEventSampleAge = 24 * time.Hour

// firstEventMinSamples 是自适应阈值需要的最少样本数，不足时不发起备用请求
const firstEventMinSamples = 20

// hedgeLimit 是同时进行的备用请求数上限
const hedgeLimit = 4

// firstEventKey 是首事件样本所属的模型与通道
type firstEventKey struct {
	model   string
	channel string
}

// firstEventSample 是一次原请求从开始发送到首事件或被备用请求取代的时长
type firstEventSample struct {
	at   time.Time
	wait time.Duration
}

// firstEventStats 按模型与通道保存近期原请求的首事件等待，备用请求不计入
type firstEventStats struct {
	mu      sync.Mutex
	samples map[firstEventKey][]firstEventSample
}

func newFirstEventStats() *firstEventStats {
	return &firstEventStats{samples: make(map[firstEventKey][]firstEventSample)}
}

// observe 记录一个原请求样本，每个模型与通道只保留最近 firstEventSampleLimit 个
func (stats *firstEventStats) observe(model, channel string, at time.Time, wait time.Duration) {
	stats.mu.Lock()
	defer stats.mu.Unlock()
	key := firstEventKey{model: model, channel: channel}
	samples := append(stats.samples[key], firstEventSample{at: at, wait: wait})
	stats.samples[key] = samples[max(0, len(samples)-firstEventSampleLimit):]
}

// p95 返回模型与通道 firstEventSampleAge 内样本的 p95 与样本数
func (stats *firstEventStats) p95(model, channel string, now time.Time) (time.Duration, int) {
	stats.mu.Lock()
	waits := recentWaits(stats.samples[firstEventKey{model: model, channel: channel}], now)
	stats.mu.Unlock()
	return percentile95(waits), len(waits)
}

// latencies 返回各模型与通道近期样本的 p95，按模型与通道排序
func (stats *firstEventStats) latencies(now time.Time) []api.FirstEventLatency {
	stats.mu.Lock()
	defer stats.mu.Unlock()
	latencies := make([]api.FirstEventLatency, 0, len(stats.samples))
	for key, samples := range stats.samples {
		waits := recentWaits(samples, now)
		if len(waits) == 0 {
			continue
		}
		latencies = append(latencies, api.FirstEventLatency{
			Model: key.model, Channel: key.channel, Samples: len(waits), P95MS: percentile95(waits).Milliseconds(),
		})
	}
	slices.SortFunc(latencies, func(a, b api.FirstEventLatency) int {
		return cmp.Or(strings.Compare(a.Model, b.Model), strings.Compare(a.Channel, b.Channel))
	})
	return latencies
}

// seed 从请求账本的近期记录补入原请求样本：被备用请求取代的原请求取其已等时长，其余只含落败备用请求或没有尝试记录的取首事件减排队
func (stats *firstEventStats) seed(records []api.UsageRecord) {
	slices.SortFunc(records, func(a, b api.UsageRecord) int { return a.Time.Compare(b.Time) })
	for _, record := range records {
		model := strings.TrimPrefix(record.Model, "models/")
		superseded := slices.IndexFunc(record.Attempts, func(attempt api.RequestAttempt) bool { return attempt.Superseded && !attempt.Hedge })
		switch {
		case superseded >= 0:
			attempt := record.Attempts[superseded]
			stats.observe(model, attempt.Channel, record.Time, time.Duration(attempt.DurationMS)*time.Millisecond)
		case record.FirstEventMS > 0 && !slices.ContainsFunc(record.Attempts, func(attempt api.RequestAttempt) bool { return !attempt.Hedge }):
			stats.observe(model, record.Channel, record.Time, time.Duration(record.FirstEventMS-record.QueueMS)*time.Millisecond)
		}
	}
}

// recentWaits 返回 firstEventSampleAge 内的样本时长
func recentWaits(samples []firstEventSample, now time.Time) []time.Duration {
	waits := make([]time.Duration, 0, len(samples))
	for _, sample := range samples {
		if now.Sub(sample.at) <= firstEventSampleAge {
			waits = append(waits, sample.wait)
		}
	}
	return waits
}

// percentile95 按最近秩法返回时长的 p95，没有样本时为 0
func percentile95(waits []time.Duration) time.Duration {
	if len(waits) == 0 {
		return 0
	}
	slices.Sort(waits)
	return waits[(len(waits)*95+99)/100-1]
}

// hedgeDelay 返回原请求等待首事件多久后发起备用请求，不发起时第二个返回值为 false
func (service *trackedService) hedgeDelay(modelID, channel string) (time.Duration, bool) {
	if !service.hedgeAuto {
		return service.hedgeAfter, service.hedgeAfter > 0
	}
	p95, samples := service.requests.firstEvents.p95(modelID, channel, time.Now())
	return p95, samples >= firstEventMinSamples
}

// hedgeSelections 返回备用请求依次尝试的账户选择：引用已上传文件或指定账户的请求只用原账户；其余先用尚未尝试的其他账户，再用原账户
func (service *trackedService) hedgeSelections(primary aistudio.AccountSelection, accountID string, fixed bool, attempted map[string]struct{}) []aistudio.AccountSelection {
	same := primary
	same.AllowedAccountIDs = nil
	same.AccountID = accountID
	if fixed {
		return []aistudio.AccountSelection{same}
	}
	other := primary
	other.AllowedAccountIDs = nil
	enabled, _ := service.pool.EnabledAccounts()
	for _, id := range enabled {
		if _, tried := attempted[id]; !tried {
			other.AllowedAccountIDs = append(other.AllowedAccountIDs, id)
		}
	}
	return []aistudio.AccountSelection{other, same}
}

// tryHedgeLease 按顺序立即取得备用请求的租约：其他账户只用 Worker 已就绪且没有活动请求的，原账户用其余并发槽位；都没有时返回 nil
func (service *trackedService) tryHedgeLease(ctx context.Context, selections []aistudio.AccountSelection) (*aistudio.AccountLease, aistudio.AccountSelection) {
	for _, selection := range selections {
		if selection.AccountID == "" && len(selection.AllowedAccountIDs) == 0 {
			continue
		}
		groups, err := service.pool.ClassifyCandidates(ctx, selection, service.workers.ReadyWarmAccountIDs())
		if err != nil {
			continue
		}
		idle := groups.WarmReady
		if selection.AccountID != "" {
			idle = append(slices.Clone(idle), groups.WarmAvailable...)
		}
		if len(idle) == 0 {
			continue
		}
		selection.AllowedAccountIDs = idle
		if lease, _, err := service.pool.TryAcquireFor(ctx, selection); err == nil {
			return lease, selection
		}
	}
	return nil, aistudio.AccountSelection{}
}

// hedgePlan 是原请求可以发起的备用请求：等待阈值、依次尝试的账户选择与启动方式
type hedgePlan struct {
	delay      time.Duration
	selections []aistudio.AccountSelection
	start      func(*aistudio.AccountLease, aistudio.AccountSelection) *generateAttempt
}

// awaitFirstEvent 等待原请求的首事件，plan 不为 nil 时到阈值后发起备用请求，先出首事件的一方胜出，全部失败时返回 nil；failed 的第二个参数表示同账户的另一尝试仍在等待首事件
func (service *trackedService) awaitFirstEvent(
	requestCtx context.Context,
	request aistudio.GenerateRequest,
	modelID string,
	primary *generateAttempt,
	plan *hedgePlan,
	failed func(*generateAttempt, bool),
) *generateAttempt {
	primaryDone := primary.done
	var hedge *generateAttempt
	var hedgeDone <-chan struct{}
	var check <-chan time.Time
	if plan != nil {
		timer := time.NewTimer(plan.delay - time.Since(primary.startedAt))
		defer timer.Stop()
		check = timer.C
	}
	defer func() {
		if hedge != nil {
			service.hedges.Add(-1)
		}
	}()
	for primaryDone != nil || hedgeDone != nil {
		select {
		case <-primaryDone:
			primaryDone = nil
			check = nil
			if primary.err == nil {
				service.requests.firstEvents.observe(modelID, string(primary.lease.Channel()), primary.firstAt, primary.firstAt.Sub(primary.startedAt))
				if hedgeDone != nil {
					service.supersede(requestCtx, request.ID, hedge, "原请求先返回首事件")
					service.requests.logRequestProgress(request.ID, primary.accountLabel, "INFO", fmt.Sprintf(
						"原请求胜出 | 取消备用请求 | 备用账户=%s | 模型=%s", hedge.accountLabel, modelID,
					))
				}
				return primary
			}
			failed(primary, hedgeDone != nil && hedge.lease.Account().ID == primary.lease.Account().ID)
		case <-hedgeDone:
			hedgeDone = nil
			if hedge.err == nil {
				waited := time.Since(primary.startedAt)
				if primaryDone != nil {
					service.requests.firstEvents.observe(modelID, string(primary.lease.Channel()), time.Now(), waited)
					service.supersede(requestCtx, request.ID, primary, "备用请求先返回首事件")
				}
				service.requests.logRequestProgress(request.ID, hedge.accountLabel, "INFO", fmt.Sprintf(
					"备用请求胜出 | 账户=%s | 原请求已等待=%s | 模型=%s", hedge.accountLabel, waited.Round(time.Millisecond), modelID,
				))
				return hedge
			}
			failed(hedge, primaryDone != nil && primary.lease.Account().ID == hedge.lease.Account().ID)
		case <-check:
			check = nil
			if service.hedges.Add(1) > hedgeLimit {
				service.hedges.Add(-1)
				check = time.After(schedulingRecheck)
				continue
			}
			lease, selection := service.tryHedgeLease(requestCtx, plan.selections)
			if lease == nil {
				service.hedges.Add(-1)
				check = time.After(schedulingRecheck)
				continue
			}
			hedge = plan.start(lease, selection)
			hedgeDone = hedge.done
			service.requests.logRequestProgress(request.ID, hedge.accountLabel, "WARN", fmt.Sprintf(
				"备用请求发起 | 账户=%s | 原请求已等待=%s | 阈值=%s | 模型=%s",
				hedge.accountLabel, time.Since(primary.startedAt).Round(time.Millisecond), plan.delay.Round(time.Millisecond), modelID,
			))
		}
	}
	return nil
}

// supersede 取消落败的尝试，在访问日志记为被取代，后台等它结束后清理临时文件并释放租约
func (service *trackedService) supersede(requestCtx context.Context, requestID string, loser *generateAttempt, reason string) {
	loser.cancel()
	api.AddAccessLogAttempt(requestCtx, api.RequestAttempt{
		Account: loser.accountLabel, Channel: string(loser.lease.Channel()), Error: reason,
		DurationMS: time.Since(loser.startedAt).Milliseconds(), Hedge: loser.hedge, Superseded: true,
	})
	go func() {
		<-loser.done
		if loser.source != nil {
			for range loser.source {
			}
		}
		if loser.copies != nil {
			if err := loser.copies.Cleanup(); err != nil {
				service.requests.log(loser.accountLabel, "WARN", "临时文件清理失败 | 错误="+err.Error())
			}
		}
		if err := loser.lease.Release(); err != nil {
			service.requests.logRequestProgress(requestID, loser.accountLabel, "WARN", "账户租约释放失败 | 错误="+err.Error())
		}
	}()
}

// seedFirstEvents 从请求账本读取 firstEventSampleAge 内的全部记录补入首事件样本
func (manager *runtimeManager) seedFirstEvents(ctx context.Context) error {
	now := time.Now()
	page, err := manager.ledger.Records(ctx, api.UsageRecordQuery{From: now.Add(-firstEventSampleAge), To: now, Limit: 1 << 20})
	if err != nil {
		return err
	}
	manager.requests.firstEvents.seed(page.Items)
	return nil
}

// FirstEventLatencies 返回各模型与通道近期原请求首事件等待的 p95
func (manager *runtimeManager) FirstEventLatencies() []api.FirstEventLatency {
	return manager.requests.firstEvents.latencies(time.Now())
}
