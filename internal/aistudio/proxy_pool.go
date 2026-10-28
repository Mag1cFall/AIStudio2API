package aistudio

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
)

// proxyFailureThreshold 为代理判定不可用前允许的连续网络错误次数
const proxyFailureThreshold = 3

// ProxyPool 把未固定代理的账户按绑定数分配到代理池，代理不可用时改绑到其他可用代理
type ProxyPool struct {
	mu      sync.Mutex
	pool    *AccountPool
	entries []*proxyEntry
}

// proxyEntry 表示代理池中的一个代理与其健康状态
type proxyEntry struct {
	proxy     string
	down      bool
	failures  int
	checkedAt time.Time
	lastError string
}

// ProxyStatus 表示代理池中一个代理的可用性与绑定账户数
type ProxyStatus struct {
	Proxy     string     `json:"proxy"`
	Available bool       `json:"available"`
	Accounts  int        `json:"accounts"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// NewProxyPool 创建代理池，proxies 为空时账户使用系统代理或直连
func NewProxyPool(pool *AccountPool, proxies []string) *ProxyPool {
	entries := make([]*proxyEntry, 0, len(proxies))
	for _, proxy := range proxies {
		if proxy = strings.TrimSpace(proxy); proxy != "" {
			entries = append(entries, &proxyEntry{proxy: proxy})
		}
	}
	return &ProxyPool{pool: pool, entries: entries}
}

// Assign 为未固定代理的账户绑定代理：保留指向可用代理的已有绑定，其余账户选绑定数最少的可用代理并写入运行状态，返回绑定变化的账户
func (p *ProxyPool) Assign() ([]string, error) {
	if p == nil {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.assignLocked()
}

func (p *ProxyPool) assignLocked() ([]string, error) {
	if len(p.entries) == 0 {
		return nil, nil
	}
	available := p.availableLocked()
	p.pool.mu.Lock()
	accounts := slices.Clone(p.pool.accounts)
	p.pool.mu.Unlock()
	counts := make(map[string]int, len(p.entries))
	bindings := make(map[*Account]string, len(accounts))
	pending := make([]*Account, 0, len(accounts))
	for _, account := range accounts {
		if strings.TrimSpace(account.Config.Proxy) != "" {
			continue
		}
		account.runtimeMu.Lock()
		bound := account.runtime.Proxy
		account.runtimeMu.Unlock()
		if current := account.boundProxy.Load(); bound == "" && current != nil {
			bound = *current
		}
		if slices.Contains(available, bound) {
			counts[bound]++
			bindings[account] = bound
			continue
		}
		pending = append(pending, account)
	}
	for _, account := range pending {
		chosen := available[0]
		for _, proxy := range available[1:] {
			if counts[proxy] < counts[chosen] {
				chosen = proxy
			}
		}
		counts[chosen]++
		bindings[account] = chosen
	}
	var changed []string
	var errs []error
	for _, account := range accounts {
		chosen, ok := bindings[account]
		if !ok {
			continue
		}
		previous := account.boundProxy.Load()
		_, err := p.pool.updateRuntime(account.ID, func(_ *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
			if runtimeState.Proxy == chosen {
				return false, nil, nil
			}
			runtimeState.Proxy = chosen
			return true, nil, nil
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		account.boundProxy.Store(&chosen)
		if previous != nil && *previous != chosen {
			changed = append(changed, account.ID)
		}
	}
	return changed, errors.Join(errs...)
}

// Next 返回下一个新账户将绑定的代理，代理池为空时返回空字符串
func (p *ProxyPool) Next() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.entries) == 0 {
		return ""
	}
	available := p.availableLocked()
	counts := make(map[string]int, len(available))
	p.pool.mu.Lock()
	for _, account := range p.pool.accounts {
		if bound := account.boundProxy.Load(); bound != nil && strings.TrimSpace(account.Config.Proxy) == "" {
			counts[*bound]++
		}
	}
	p.pool.mu.Unlock()
	chosen := available[0]
	for _, proxy := range available[1:] {
		if counts[proxy] < counts[chosen] {
			chosen = proxy
		}
	}
	return chosen
}

// availableLocked 返回可用代理；全部不可用时返回全部代理
func (p *ProxyPool) availableLocked() []string {
	var available, all []string
	for _, entry := range p.entries {
		all = append(all, entry.proxy)
		if !entry.down {
			available = append(available, entry.proxy)
		}
	}
	if len(available) == 0 {
		return all
	}
	return available
}

// Binding 返回账户当前绑定的代理池代理，账户固定代理或未绑定时返回空字符串
func (p *ProxyPool) Binding(accountID string) string {
	if p == nil {
		return ""
	}
	p.pool.mu.Lock()
	account := p.pool.byID[strings.TrimSpace(accountID)]
	p.pool.mu.Unlock()
	if account == nil || strings.TrimSpace(account.Config.Proxy) != "" {
		return ""
	}
	if bound := account.boundProxy.Load(); bound != nil {
		return *bound
	}
	return ""
}

// Available 判断代理是否在当前参与分配的代理中
func (p *ProxyPool) Available(proxy string) bool {
	if p == nil || proxy == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Contains(p.availableLocked(), proxy)
}

// entryLocked 返回代理对应的条目，不在代理池中时返回 nil
func (p *ProxyPool) entryLocked(proxy string) *proxyEntry {
	for _, entry := range p.entries {
		if entry.proxy == proxy {
			return entry
		}
	}
	return nil
}

// ReportFailure 记录经 proxy 发出的请求的网络错误；连续达到阈值且存在其他可用代理时把该代理标为不可用并改绑，返回绑定变化的账户
func (p *ProxyPool) ReportFailure(proxy string, err error) ([]string, error) {
	if p == nil {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.entryLocked(proxy)
	if entry == nil {
		return nil, nil
	}
	entry.failures++
	if err != nil {
		entry.lastError = err.Error()
	}
	if entry.down || entry.failures < proxyFailureThreshold || !p.otherAvailableLocked(entry) {
		return nil, nil
	}
	entry.down = true
	return p.assignLocked()
}

// ReportSuccess 清零 proxy 的连续网络错误
func (p *ProxyPool) ReportSuccess(proxy string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry := p.entryLocked(proxy); entry != nil {
		entry.failures = 0
	}
}

func (p *ProxyPool) otherAvailableLocked(target *proxyEntry) bool {
	for _, entry := range p.entries {
		if entry != target && !entry.down {
			return true
		}
	}
	return false
}

// Probe 用 check 逐个检测代理，失败的代理标为不可用，恢复的代理重新参与分配，返回绑定变化的账户
func (p *ProxyPool) Probe(ctx context.Context, check func(context.Context, string) error) ([]string, error) {
	if p == nil {
		return nil, nil
	}
	p.mu.Lock()
	proxies := make([]string, 0, len(p.entries))
	for _, entry := range p.entries {
		proxies = append(proxies, entry.proxy)
	}
	p.mu.Unlock()
	results := make([]error, len(proxies))
	var wait sync.WaitGroup
	for index, proxy := range proxies {
		wait.Go(func() {
			results[index] = check(ctx, proxy)
		})
	}
	wait.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now().UTC()
	for index, proxy := range proxies {
		for _, entry := range p.entries {
			if entry.proxy != proxy {
				continue
			}
			entry.checkedAt = now
			if results[index] != nil {
				entry.down = true
				entry.lastError = results[index].Error()
				continue
			}
			entry.down = false
			entry.failures = 0
			entry.lastError = ""
		}
	}
	return p.assignLocked()
}

// Status 返回各代理的可用性与当前绑定账户数
func (p *ProxyPool) Status() []ProxyStatus {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	counts := make(map[string]int, len(p.entries))
	p.pool.mu.Lock()
	for _, account := range p.pool.accounts {
		if strings.TrimSpace(account.Config.Proxy) != "" {
			continue
		}
		if bound := account.boundProxy.Load(); bound != nil {
			counts[*bound]++
		}
	}
	p.pool.mu.Unlock()
	statuses := make([]ProxyStatus, 0, len(p.entries))
	for _, entry := range p.entries {
		status := ProxyStatus{Proxy: entry.proxy, Available: !entry.down, Accounts: counts[entry.proxy], Error: entry.lastError}
		if !entry.checkedAt.IsZero() {
			status.CheckedAt = timePointer(entry.checkedAt)
		}
		statuses = append(statuses, status)
	}
	return statuses
}
