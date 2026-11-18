package app

import (
	"errors"
	"io"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// disconnectWindow 是判断成片断连时回看其他请求终态的时长
const disconnectWindow = 30 * time.Second

// disconnectTracker 记录每个模型在各通道上最近两个请求的终态：收到首个事件或以连接断开失败
type disconnectTracker struct {
	mu     sync.Mutex
	recent map[string]map[aistudio.Channel][2]disconnectOutcome
}

// disconnectOutcome 是一个请求的终态，at 为结束时刻，broken 表示以连接断开失败
type disconnectOutcome struct {
	at     time.Time
	broken bool
}

// record 记录模型在通道上一个请求的终态
func (tracker *disconnectTracker) record(model string, channel aistudio.Channel, broken bool, at time.Time) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.recent == nil {
		tracker.recent = make(map[string]map[aistudio.Channel][2]disconnectOutcome)
	}
	if tracker.recent[model] == nil {
		tracker.recent[model] = make(map[aistudio.Channel][2]disconnectOutcome)
	}
	pair := tracker.recent[model][channel]
	tracker.recent[model][channel] = [2]disconnectOutcome{pair[1], {at: at, broken: broken}}
}

// bursting 返回 started 时成片断连的通道：模型在该通道上最近两个终态都是连接断开，且都在 started 之前 disconnectWindow 内
func (tracker *disconnectTracker) bursting(model string, started time.Time) map[aistudio.Channel]bool {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	channels := make(map[aistudio.Channel]bool)
	for channel, pair := range tracker.recent[model] {
		if pair[0].broken && pair[1].broken && started.Sub(pair[0].at) <= disconnectWindow {
			channels[channel] = true
		}
	}
	return channels
}

// disconnected 判断错误是否为连接被对端断开
func disconnected(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
