package requestdb

import (
	"context"
	"errors"
	"strings"
	"time"
)

// AccountUsageEntry 表示单个账户在指定模型与通道上的最近使用时刻
type AccountUsageEntry struct {
	Account  string
	Model    string
	Channel  string
	LastUsed time.Time
}

// AccountUsageSeed 保存启动时从请求账本读回的账户最近使用记录与当前配额日请求数
type AccountUsageSeed struct {
	Entries          []AccountUsageEntry
	QuotaDayStart    time.Time
	QuotaDayRequests map[string]int64
}

// AccountUsageSeed 返回各账户按模型与通道聚合的最近使用时刻，以及自 quotaDayStart 起至今的请求数
func (store *Store) AccountUsageSeed(ctx context.Context, quotaDayStart time.Time) (AccountUsageSeed, error) {
	seed := AccountUsageSeed{
		QuotaDayStart:    quotaDayStart,
		QuotaDayRequests: map[string]int64{},
	}
	rows, err := store.db.QueryContext(ctx, `SELECT account, model, channel, MAX(last_time) FROM days WHERE account != '' GROUP BY account, model, channel`)
	if err != nil {
		return AccountUsageSeed{}, err
	}
	for rows.Next() {
		var account, model, channel string
		var lastTime int64
		if err := rows.Scan(&account, &model, &channel, &lastTime); err != nil {
			return AccountUsageSeed{}, errors.Join(err, rows.Close())
		}
		if lastTime <= 0 {
			continue
		}
		seed.Entries = append(seed.Entries, AccountUsageEntry{
			Account:  strings.TrimSpace(account),
			Model:    strings.TrimPrefix(strings.TrimSpace(model), "models/"),
			Channel:  strings.TrimSpace(channel),
			LastUsed: time.UnixMilli(lastTime).UTC(),
		})
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return AccountUsageSeed{}, err
	}
	if quotaDayStart.IsZero() {
		return seed, nil
	}
	from, to := quotaDayStart.UnixMilli(), time.Now().UnixMilli()+1
	if from >= to {
		return seed, nil
	}
	for _, part := range plan(from, to, nil) {
		if err := store.scan(ctx, part, nil, func(value *entry) {
			if account := strings.TrimSpace(value.account); account != "" {
				seed.QuotaDayRequests[account] += value.requests
			}
		}); err != nil {
			return AccountUsageSeed{}, err
		}
	}
	return seed, nil
}
