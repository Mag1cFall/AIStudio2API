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

// rerun 记录账户页面运行 contents，返回该账户此前是否运行过同一对话；对话不以 user 轮结尾时返回 false 且不记录，超过上限时丢弃最早的一条
func (runs *pageRuns) rerun(accountID string, contents []Content) bool {
	links := assistantLinks(contents)
	if len(links) == 0 || links[len(links)-1].model {
		return false
	}
	key := pageRunKey{accountID: accountID, digest: links[len(links)-1].digest}
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
