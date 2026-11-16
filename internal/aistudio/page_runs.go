package aistudio

import "sync"

// pageRunLimit 是本进程记录的账户页面已运行对话条数上限
const pageRunLimit = 1000

// pageRunKey 是账户与以 user 轮结尾的对话摘要
type pageRunKey struct {
	accountID string
	digest    string
}

// pageRuns 记录各账户页面运行过的对话，同一账户再次运行同一对话时与官网点 Rerun 一样输入框为空
type pageRuns struct {
	mu      sync.Mutex
	entries map[pageRunKey]struct{}
	order   []pageRunKey
}

func newPageRuns() *pageRuns {
	return &pageRuns{entries: make(map[pageRunKey]struct{})}
}

// rerun 记录账户页面运行末尾摘要为 tip 的对话，返回该账户此前是否运行过同一对话；对话为空或不以 user 轮结尾时返回 false 且不记录，超过上限时丢弃最早的一条
func (runs *pageRuns) rerun(accountID string, tip turnLink) bool {
	if tip.digest == "" || tip.model {
		return false
	}
	key := pageRunKey{accountID: accountID, digest: tip.digest}
	runs.mu.Lock()
	defer runs.mu.Unlock()
	if _, exists := runs.entries[key]; exists {
		return true
	}
	runs.entries[key] = struct{}{}
	runs.order = append(runs.order, key)
	if len(runs.order) > pageRunLimit {
		delete(runs.entries, runs.order[0])
		runs.order = runs.order[1:]
	}
	return false
}

// ran 判断账户页面此前运行过末尾摘要为 tip 的对话，不记录
func (runs *pageRuns) ran(accountID string, tip turnLink) bool {
	if tip.digest == "" || tip.model {
		return false
	}
	runs.mu.Lock()
	defer runs.mu.Unlock()
	_, exists := runs.entries[pageRunKey{accountID: accountID, digest: tip.digest}]
	return exists
}

// PageRunsProtectedTransport 是记录账户页面已运行对话的受保护传输
type PageRunsProtectedTransport interface {
	// RanBefore 判断账户页面此前运行过 contents 这同一段对话，不记录
	RanBefore(accountID string, contents []Content) bool
}

// RanBefore 判断账户页面此前运行过 contents 这同一段对话，不记录
func (t *WorkerProtectedTransport) RanBefore(accountID string, contents []Content) bool {
	return t.runs.ran(accountID, turnChain{}.tipOf(contents))
}
