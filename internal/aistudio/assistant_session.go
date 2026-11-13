package aistudio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
)

// assistantTrajectoryLimit 是账户运行状态保存的可续接 trajectory 数上限
const assistantTrajectoryLimit = 200

// assistantApplet 是账户的 Build 写代码助手应用与其中可续接的 trajectory
type assistantApplet struct {
	// ID 是应用 ID，也是应用第一段对话的 trajectory ID
	ID string `json:"id"`
	// Trajectories 是可续接的 trajectory，最近续接的排在末尾
	Trajectories []assistantTrajectory `json:"trajectories,omitempty"`
}

// assistantTrajectory 是应用中一段可续接的对话
type assistantTrajectory struct {
	// Tip 是请求所用 API key 与到最后一轮模型回复为止的对话摘要合成的续接位置
	Tip string `json:"tip"`
	// ID 是 trajectory ID
	ID string `json:"id"`
	// HistoryGroups 是开启 trajectory 时转录在自定义指令中的开头分组数
	HistoryGroups int `json:"history_groups,omitempty"`
}

// assistantTip 返回 API key 与对话摘要合成的续接位置，运行状态中不保存明文 key
func assistantTip(apiKey string, digest string) string {
	sum := sha256.Sum256([]byte(apiKey + "\x00" + digest))
	return hex.EncodeToString(sum[:])
}

// assistantTipLink 返回 links 最后一组之前最近的模型轮分组，没有时为 -1
func assistantTipLink(links []turnLink) int {
	index := len(links) - 2
	if index >= 0 && !links[index].model {
		index--
	}
	return index
}

// matchAssistantTrajectory 返回应用中续接位置为 tip 的 trajectory
func matchAssistantTrajectory(applet *assistantApplet, tip string) (assistantTrajectory, bool) {
	if applet == nil {
		return assistantTrajectory{}, false
	}
	index := slices.IndexFunc(applet.Trajectories, func(trajectory assistantTrajectory) bool { return trajectory.Tip == tip })
	if index < 0 {
		return assistantTrajectory{}, false
	}
	return applet.Trajectories[index], true
}

// assistantAppletID 返回账户保存的应用 ID，没有时为空
func (l *AccountLease) assistantAppletID() string {
	l.pool.mu.Lock()
	defer l.pool.mu.Unlock()
	if applet := l.account.runtime.Assistant; applet != nil {
		return applet.ID
	}
	return ""
}

// findAssistantTrajectory 返回对话最后一组之前的模型轮在账户应用中可续接的 trajectory 与之后新轮次的开头，没有时返回 nil
func (l *AccountLease) findAssistantTrajectory(apiKey string, links []turnLink) (*assistantTrajectory, int) {
	index := assistantTipLink(links)
	if index < 0 {
		return nil, 0
	}
	l.pool.mu.Lock()
	defer l.pool.mu.Unlock()
	trajectory, ok := matchAssistantTrajectory(l.account.runtime.Assistant, assistantTip(apiKey, links[index].digest))
	if !ok {
		return nil, 0
	}
	return &trajectory, links[index].end
}

// setAssistantApplet 把新建的应用写入账户运行状态，替换之前的应用与其中的 trajectory
func (l *AccountLease) setAssistantApplet(applet string) error {
	_, err := l.pool.updateRuntime(l.account.ID, func(_ *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		runtimeState.Assistant = &assistantApplet{ID: applet}
		return true, nil, nil
	})
	return err
}

// storeAssistantTrajectory 把 trajectory 的新续接位置写入账户运行状态，替换同一 trajectory 之前的续接位置，超过 assistantTrajectoryLimit 时丢弃最早的；账户的应用已不是 applet 时不写入
func (l *AccountLease) storeAssistantTrajectory(applet string, trajectory assistantTrajectory) error {
	_, err := l.pool.updateRuntime(l.account.ID, func(_ *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		current := runtimeState.Assistant
		if current == nil || current.ID != applet {
			return false, nil, nil
		}
		kept := slices.DeleteFunc(current.Trajectories, func(item assistantTrajectory) bool {
			return item.ID == trajectory.ID || item.Tip == trajectory.Tip
		})
		kept = append(kept, trajectory)
		current.Trajectories = kept[max(0, len(kept)-assistantTrajectoryLimit):]
		return true, nil, nil
	})
	return err
}

// dropAssistantTrajectory 从账户运行状态删除续接位置 tip，该 trajectory 不再续接
func (l *AccountLease) dropAssistantTrajectory(tip string) error {
	_, err := l.pool.updateRuntime(l.account.ID, func(_ *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		current := runtimeState.Assistant
		if current == nil {
			return false, nil, nil
		}
		count := len(current.Trajectories)
		current.Trajectories = slices.DeleteFunc(current.Trajectories, func(item assistantTrajectory) bool { return item.Tip == tip })
		return len(current.Trajectories) != count, nil, nil
	})
	return err
}

// finishAssistantGeneration 在本轮生成结束后让账户接其他 -build 请求，租约仍保留到回复后的伴随请求结束
func (l *AccountLease) finishAssistantGeneration() {
	l.pool.mu.Lock()
	defer l.pool.mu.Unlock()
	if l.channel != ChannelAssistant || l.assistantDone {
		return
	}
	l.assistantDone = true
	l.account.assistantBusy = false
	l.pool.notifyLocked()
}

// assistantTrajectoryAccount 返回对话可续接的 trajectory 所在的账户，没有时为空
func (p *AccountPool) assistantTrajectoryAccount(apiKey string, links []turnLink) string {
	index := assistantTipLink(links)
	if index < 0 {
		return ""
	}
	tip := assistantTip(apiKey, links[index].digest)
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, account := range p.accounts {
		if _, ok := matchAssistantTrajectory(account.runtime.Assistant, tip); ok {
			return account.ID
		}
	}
	return ""
}

// AssistantService 是可以查询写代码助手对话所在账户的生成服务
type AssistantService interface {
	AssistantAccount(context.Context, GenerateRequest) string
}

// AssistantAccount 返回 -build 请求的对话可续接的 trajectory 所在账户，没有时返回空
func (s *PooledService) AssistantAccount(ctx context.Context, request GenerateRequest) string {
	apiKey, _ := ctx.Value(apiKeyContextKey{}).(string)
	_, contents := s.generateTurns(request)
	return s.pool.assistantTrajectoryAccount(apiKey, turnLinks(contents))
}
