package aistudio

import (
	"context"
	"errors"
	"sync"
)

// pageTab 是账户 WAA Worker 的一个页面：holder 为正在使用它的请求租约，extra 为 nil 时是 Worker 的主页面
type pageTab struct {
	holder *AccountLease
	extra  *extraPage
}

// extraPage 是账户并发时按需打开的另一个官网页面，operationMu 串行化该页面的界面操作
type extraPage struct {
	runtime     workerRuntime
	operationMu sync.Mutex
}

// tabOpener 是能像用户新开一个标签页一样载入另一个官网页面的 workerRuntime
type tabOpener interface {
	openTab(ctx context.Context) (workerRuntime, error)
}

// openTab 像用户在同一浏览器窗口新开一个标签页一样载入官网新对话页：与本页共用 Cookie、出口与页面缓存，sessionStorage 中的窗口 ID 与页面状态各自独立
func (runtime *goWAARuntime) openTab(ctx context.Context) (workerRuntime, error) {
	options := runtime.options
	options.StartupProgress = nil
	next, fingerprint, err := pageRuntime(options, runtime.client, runtime.session, runtime.pageCache)
	if err != nil {
		return nil, err
	}
	if err := next.open(ctx, fingerprint); err != nil {
		return nil, err
	}
	return next, nil
}

// leaseActive 判断租约仍未归还
func leaseActive(lease *AccountLease) bool {
	lease.operation.Lock()
	defer lease.operation.Unlock()
	return !lease.released
}

// usePage 返回 ctx 中请求使用的页面与该页面界面操作的锁：同一租约始终使用同一页面；主页面空闲时用主页面，否则用空闲的另一个页面，都在使用中时像用户新开标签页一样再载入一个页面，载入期间其他请求照常取用已有页面，排队新开的请求轮到时先取已空出的页面，Worker 关闭后不再新开；没有租约或运行时不能新开页面时用主页面
func (worker *NativeWorker) usePage(ctx context.Context) (workerRuntime, *sync.Mutex, error) {
	lease, ok := AccountLeaseFromContext(ctx)
	if _, opens := worker.current().(tabOpener); !ok || !opens {
		return worker.current(), &worker.operationMu, nil
	}
	worker.tabsMu.Lock()
	defer worker.tabsMu.Unlock()
	if len(worker.tabs) == 0 {
		worker.tabs = []*pageTab{{}}
	}
	tab := worker.pageFor(lease)
	if tab == nil {
		worker.tabsMu.Unlock()
		worker.openingMu.Lock()
		defer worker.openingMu.Unlock()
		worker.tabsMu.Lock()
		if tab = worker.pageFor(lease); tab == nil {
			if phase := worker.State().Phase; phase == WorkerClosing || phase == WorkerClosed {
				return nil, nil, errors.New("WAA runtime 已关闭")
			}
			worker.tabsMu.Unlock()
			opened, err := worker.current().(tabOpener).openTab(ctx)
			worker.tabsMu.Lock()
			if err != nil {
				return nil, nil, err
			}
			tab = &pageTab{extra: &extraPage{runtime: opened}}
			worker.tabs = append(worker.tabs, tab)
		}
	}
	tab.holder = lease
	return worker.tabRuntime(tab)
}

// pageFor 返回 lease 正在使用的页面，没有时返回第一个空闲页面，都在使用中时返回 nil；调用方持有 tabsMu
func (worker *NativeWorker) pageFor(lease *AccountLease) *pageTab {
	var free *pageTab
	for _, tab := range worker.tabs {
		if tab.holder == lease {
			return tab
		}
		if free == nil && (tab.holder == nil || !leaseActive(tab.holder)) {
			free = tab
		}
	}
	return free
}

// tabRuntime 返回页面的 runtime 与界面操作锁
func (worker *NativeWorker) tabRuntime(tab *pageTab) (workerRuntime, *sync.Mutex, error) {
	if tab.extra == nil {
		return worker.current(), &worker.operationMu, nil
	}
	return tab.extra.runtime, &tab.extra.operationMu, nil
}

// closeExtraPages 等进行中的新开页面结束后关闭按需打开的其他页面，主页面保留
func (worker *NativeWorker) closeExtraPages() error {
	worker.openingMu.Lock()
	defer worker.openingMu.Unlock()
	worker.tabsMu.Lock()
	defer worker.tabsMu.Unlock()
	var err error
	tabs := worker.tabs[:0]
	for _, tab := range worker.tabs {
		if tab.extra == nil {
			tabs = append(tabs, tab)
			continue
		}
		err = errors.Join(err, tab.extra.runtime.Close())
	}
	worker.tabs = tabs
	return err
}
