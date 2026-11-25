package app

import (
	"context"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// SeedAccountUsage 把从请求账本恢复的账户最近使用时间与当日请求数注入账户池
func (service *trackedService) SeedAccountUsage(seed aistudio.AccountUsageSeed) {
	service.pool.SeedUsage(seed)
}

// seedAccountUsage 从请求账本读回各账户历史使用时间与当前配额日请求数，注入目标生成服务的账户池
func (manager *runtimeManager) seedAccountUsage(ctx context.Context, target *runtimeGeneration) {
	if manager.ledger == nil || target == nil {
		return
	}
	seeder, ok := target.service.(interface {
		SeedAccountUsage(aistudio.AccountUsageSeed)
	})
	if !ok {
		return
	}
	stored, err := manager.ledger.AccountUsageSeed(ctx, aistudio.QuotaDayStart(time.Now()))
	if err != nil {
		if ctx.Err() == nil {
			manager.requests.log("service", "WARN", "读取账户历史使用记录失败", "error", err)
		}
		return
	}
	entries := make([]aistudio.AccountUsageEntry, 0, len(stored.Entries))
	for _, entry := range stored.Entries {
		entries = append(entries, aistudio.AccountUsageEntry{
			Account:  entry.Account,
			Model:    entry.Model,
			Channel:  aistudio.Channel(entry.Channel),
			LastUsed: entry.LastUsed,
		})
	}
	seeder.SeedAccountUsage(aistudio.AccountUsageSeed{
		Entries:          entries,
		QuotaDayStart:    stored.QuotaDayStart,
		QuotaDayRequests: stored.QuotaDayRequests,
	})
}
