package aistudio

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// pageTab 是账户 WAA Worker 的一个页面：holder 为正在使用它的请求租约，extra 为 nil 时是 Worker 的主页面；used 是 holder 在主页面上取 proof 或发送请求时的 Worker 页面，retired 表示页面在重新载入后等 holder 结束再关闭，不再分给其他请求，退役的主页面让出主页面的位置、holder 之后仍在 used 上取 proof 与发送；freed 是页面最近一次空出（请求归还租约或页面打开）的时刻，还没用过的主页面为零值
type pageTab struct {
	holder  *AccountLease
	extra   *extraPage
	used    workerRuntime
	retired bool
	freed   time.Time
}

// pageModelKey 标记请求的模型，按需新开的页面以它载入新对话页
type pageModelKey struct{}

// extraPage 是账户并发时按需打开的另一个官网页面，operationMu 串行化该页面的界面操作
type extraPage struct {
	runtime     workerRuntime
	operationMu sync.Mutex
}

// tabOpener 是能像用户新开一个标签页一样载入另一个官网页面的 workerRuntime
type tabOpener interface {
	openTab(ctx context.Context) (workerRuntime, error)
}

// openTab 像用户在同一浏览器窗口新开一个标签页一样载入官网新对话页：与本页共用 Cookie、出口与页面缓存，sessionStorage 中的窗口 ID 与页面状态各自独立；ctx 带本页 ListModels 中的请求模型时以该模型打开，页面模型与请求相同，不用再换模型
func (runtime *goWAARuntime) openTab(ctx context.Context) (workerRuntime, error) {
	options := runtime.options
	options.StartupProgress = nil
	if model, _ := ctx.Value(pageModelKey{}).(string); model != "" && runtime.elements != nil && runtime.elements.listedModel("models/"+model) {
		options.Model = model
	}
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

// usePage 返回 ctx 中请求使用的页面：同一租约始终使用同一页面；有空闲页面时用空出最早的一个，账户并发时在后台预先打开下一个页面（见 openSpare）；都在使用中时在后台像用户新开标签页一样再载入一个页面，同时等已有页面空出与新页面载入完成，先到哪个用哪个，新开失败且没有页面空出时返回失败原因；载入期间其他请求照常取用已有页面，Worker 关闭后不再新开；没有租约或运行时不能新开页面时用主页面
func (worker *NativeWorker) usePage(ctx context.Context) (*pageTab, error) {
	lease, ok := AccountLeaseFromContext(ctx)
	if _, opens := worker.current().(tabOpener); !ok || !opens {
		return &pageTab{}, nil
	}
	worker.tabsMu.Lock()
	defer worker.tabsMu.Unlock()
	if len(worker.tabs) == 0 {
		worker.tabs = []*pageTab{{}}
	}
	waited := false
	for {
		tab := worker.pageFor(lease)
		if tab != nil {
			if tab.holder != lease {
				tab.holder, tab.used = lease, nil
				lease.AfterRelease(func() {
					worker.tabsMu.Lock()
					tab.freed = time.Now()
					worker.notifyTabs()
					worker.tabsMu.Unlock()
				})
				worker.openSpare(ctx, lease)
			}
			return tab, nil
		}
		if phase := worker.State().Phase; phase == WorkerClosing || phase == WorkerClosed {
			return nil, errors.New("WAA runtime 已关闭")
		}
		if waited && !worker.spare && worker.openErr != nil {
			return nil, worker.openErr
		}
		if !worker.spare {
			worker.openPage(ctx, false)
		}
		if worker.tabsChanged == nil {
			worker.tabsChanged = make(chan struct{})
		}
		changed := worker.tabsChanged
		worker.tabsMu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			worker.tabsMu.Lock()
			return nil, ctx.Err()
		}
		worker.tabsMu.Lock()
		waited = true
	}
}

// notifyTabs 唤醒等待页面空出或新开结束的请求；调用方持有 tabsMu
func (worker *NativeWorker) notifyTabs() {
	if worker.tabsChanged != nil {
		close(worker.tabsChanged)
		worker.tabsChanged = nil
	}
}

// pageFor 返回 lease 正在使用的页面，没有时返回空出最早的未退役空闲页面，都在使用中时返回 nil；调用方持有 tabsMu
func (worker *NativeWorker) pageFor(lease *AccountLease) *pageTab {
	var free *pageTab
	for _, tab := range worker.tabs {
		if tab.holder == lease {
			return tab
		}
		if !tab.retired && (tab.holder == nil || !leaseActive(tab.holder)) && (free == nil || tab.freed.Before(free.freed)) {
			free = tab
		}
	}
	return free
}

// openSpare 在 lease 取走最后一个空闲页面、账户另有页面在用时，像用户再开一个标签页一样在后台预先载入页面，以 ctx 中请求的模型打开，账户的页面数至多比单账户并发数多 1；调用方持有 tabsMu
func (worker *NativeWorker) openSpare(ctx context.Context, lease *AccountLease) {
	if worker.spare {
		return
	}
	pages, held := 0, 0
	for _, tab := range worker.tabs {
		if tab.retired {
			continue
		}
		pages++
		if tab.holder != nil && leaseActive(tab.holder) {
			held++
		}
	}
	lease.pool.mu.Lock()
	limit := lease.pool.perAccountConcurrency
	lease.pool.mu.Unlock()
	if held < pages || held < 2 || pages > limit {
		return
	}
	worker.openPage(ctx, true)
}

// openPage 在后台像用户新开一个标签页一样以 ctx 中请求的模型载入页面，载入期间 spare 为真，结束后把页面作为空闲页面加入或记下失败原因，并唤醒等待页面的请求；spare 表示这是预先打开，失败时记日志；调用方持有 tabsMu
func (worker *NativeWorker) openPage(ctx context.Context, spare bool) {
	worker.spare, worker.openErr = true, nil
	model, _ := ctx.Value(pageModelKey{}).(string)
	go func() {
		worker.openingMu.Lock()
		defer worker.openingMu.Unlock()
		var opened workerRuntime
		err := errors.New("WAA runtime 已关闭")
		if phase := worker.State().Phase; phase != WorkerClosing && phase != WorkerClosed {
			opened, err = worker.current().(tabOpener).openTab(context.WithValue(context.Background(), pageModelKey{}, model))
		}
		if err != nil && spare {
			slog.Warn("预先打开页面失败", "account", worker.accountID, "error", err)
		}
		worker.tabsMu.Lock()
		defer worker.tabsMu.Unlock()
		worker.spare, worker.openErr = false, err
		if err == nil {
			worker.tabs = append(worker.tabs, &pageTab{extra: &extraPage{runtime: opened}, freed: time.Now()})
		}
		worker.notifyTabs()
	}()
}

// lockPage 取得页面界面操作的锁，返回此时取 proof 用的页面与解锁函数：新开的页面为它自己，主页面为取得锁时 Worker 的当前页面，并记为 holder 使用的页面，退役的主页面为 holder 此前使用的页面
func (worker *NativeWorker) lockPage(tab *pageTab) (workerRuntime, func()) {
	if tab.extra != nil {
		tab.extra.operationMu.Lock()
		return tab.extra.runtime, tab.extra.operationMu.Unlock
	}
	worker.operationMu.Lock()
	worker.tabsMu.Lock()
	if !tab.retired {
		tab.used = worker.current()
	}
	runtime := tab.used
	worker.tabsMu.Unlock()
	return runtime, worker.operationMu.Unlock
}

// tabPage 返回请求在页面上发送用的页面：新开的页面为它自己，主页面为 holder 取 proof 或此前发送时的页面，都还没有时为 Worker 的当前页面，并记为 holder 使用的页面
func (worker *NativeWorker) tabPage(tab *pageTab) workerRuntime {
	if tab.extra != nil {
		return tab.extra.runtime
	}
	worker.tabsMu.Lock()
	defer worker.tabsMu.Unlock()
	if tab.used == nil {
		tab.used = worker.current()
	}
	return tab.used
}

// retirePages 在主页面切换为新页面后处理此前的页面：仍有请求在用的新开页面与主页面上的旧页面等该请求的租约归还后关闭，在用的旧页面让出主页面的位置，新页面作为空闲的主页面分给其他请求，其余立即关闭；等进行中的新开页面结束后进行
func (worker *NativeWorker) retirePages(previous workerRuntime) error {
	worker.openingMu.Lock()
	defer worker.openingMu.Unlock()
	worker.tabsMu.Lock()
	var closing []workerRuntime
	pinned := false
	for _, tab := range worker.tabs {
		active := tab.holder != nil && leaseActive(tab.holder)
		switch {
		case tab.extra == nil:
			if active && tab.used == previous {
				pinned = true
				tab.retired = true
				tab.holder.AfterRelease(func() { worker.dropTab(tab) })
			}
		case active:
			if !tab.retired {
				tab.retired = true
				tab.holder.AfterRelease(func() { worker.dropTab(tab) })
			}
		default:
			closing = append(closing, tab.extra.runtime)
		}
	}
	worker.tabs = slices.DeleteFunc(worker.tabs, func(tab *pageTab) bool {
		return tab.extra != nil && !tab.retired
	})
	if pinned {
		worker.tabs = append([]*pageTab{{freed: time.Now()}}, worker.tabs...)
		worker.notifyTabs()
	}
	worker.tabsMu.Unlock()
	if !pinned {
		closing = append(closing, previous)
	}
	var err error
	for _, page := range closing {
		err = errors.Join(err, page.Close())
	}
	return err
}

// dropTab 关闭退役的页面并把它移出页面列表，Worker 关闭时已关闭的页面不再关闭
func (worker *NativeWorker) dropTab(tab *pageTab) {
	worker.tabsMu.Lock()
	index := slices.Index(worker.tabs, tab)
	if index >= 0 {
		worker.tabs = slices.Delete(worker.tabs, index, index+1)
	}
	worker.tabsMu.Unlock()
	if index >= 0 {
		if err := tabRuntime(tab).Close(); err != nil {
			slog.Warn("关闭重新载入前的页面失败", "account", worker.accountID, "error", err)
		}
	}
}

// tabRuntime 返回退役页面的 runtime：新开的页面为它自己，主页面为 holder 使用的页面
func tabRuntime(tab *pageTab) workerRuntime {
	if tab.extra != nil {
		return tab.extra.runtime
	}
	return tab.used
}

// closeExtraPages 等进行中的新开页面结束后关闭按需打开的其他页面与重新载入前仍有请求在用的主页面，当前主页面保留
func (worker *NativeWorker) closeExtraPages() error {
	worker.openingMu.Lock()
	defer worker.openingMu.Unlock()
	worker.tabsMu.Lock()
	defer worker.tabsMu.Unlock()
	defer worker.notifyTabs()
	var err error
	tabs := worker.tabs[:0]
	for _, tab := range worker.tabs {
		if tab.extra == nil && !tab.retired {
			tabs = append(tabs, tab)
			continue
		}
		err = errors.Join(err, tabRuntime(tab).Close())
	}
	worker.tabs = tabs
	return err
}
