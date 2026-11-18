package camoufoxnative

import (
	"context"
	"time"
)

// pageClockLeadLimit 是页面时钟领先真实时间的上限毫秒数，与 SpiderMonkey VM 时钟的领先上限相同
const pageClockLeadLimit = 1000

// pageClock 是一个标签页的页面时钟状态
type pageClock struct {
	// held 为真时界面输入结束后不追平真实时间
	held bool
	// stop 关闭时结束追平，done 在追平协程退出时关闭
	stop chan struct{}
	done chan struct{}
}

// pageClock 返回标签页 contextID 的页面时钟状态
func (client *bidiClient) pageClock(contextID string) *pageClock {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.clocks == nil {
		client.clocks = make(map[string]*pageClock)
	}
	clock := client.clocks[contextID]
	if clock == nil {
		clock = &pageClock{}
		client.clocks[contextID] = clock
	}
	return clock
}

// setPageClock 把标签页的页面时钟设为领先真实时间 offset 毫秒，返回页面时钟实际落后真实时间的毫秒数；页面时间不低于页面上次读取的时刻，设得更低时停在该时刻
func (client *bidiClient) setPageClock(ctx context.Context, contextID string, offset float64) (float64, error) {
	result, err := client.command(ctx, "input.rewindPageClock", map[string]any{"context": contextID, "duration": -offset})
	if err != nil {
		return 0, err
	}
	lag, _ := result["lag"].(float64)
	return lag, nil
}

// performInput 以一次 input.performActions 执行输入源 source，停顿与指针移动都不真实等待：页面时钟先拨回页面上次读取时间之后的空闲（最多全部时长），Firefox 在每个停顿与每帧指针移动处只推进页面时钟，下一个事件的页面时间为上一个事件的页面时间加上停顿，拨回的空闲用完后页面时钟领先真实时间；领先会超过 pageClockLeadLimit 时这组输入不停顿、紧接着派发；派发后页面时钟逐帧停在页面上次读取的时刻，直到真实时间追上；调用方持有 inputMu
func (client *bidiClient) performInput(ctx context.Context, contextID string, source map[string]any) error {
	clock := client.pageClock(contextID)
	clock.stopCatchUp()
	defer func() {
		if !clock.held {
			client.catchUpPageClock(contextID, clock)
		}
	}()
	actions := source["actions"].([]map[string]any)
	total := 0
	for _, action := range actions {
		duration, _ := action["duration"].(int)
		total += duration
	}
	if total > 0 {
		lag, err := client.setPageClock(ctx, contextID, -float64(total))
		if err != nil {
			return err
		}
		if float64(total)-lag > pageClockLeadLimit {
			compact := make([]map[string]any, 0, len(actions))
			for _, action := range actions {
				if action["type"] == "pause" {
					continue
				}
				if action["duration"] != nil {
					action = cloneAction(action)
					delete(action, "duration")
				}
				compact = append(compact, action)
			}
			actions = compact
		}
	}
	input := make(map[string]any, len(source))
	for key, value := range source {
		input[key] = value
	}
	input["actions"] = actions
	_, err := client.command(ctx, "input.performActions", map[string]any{"context": contextID, "actions": []map[string]any{input}})
	return err
}

// holdPageClock 停止标签页上进行中的追平，到 releasePageClock 为止界面输入结束后不追平，页面时钟领先真实时间时页面时间按真实速率走；调用方持有 inputMu
func (client *bidiClient) holdPageClock(contextID string) {
	clock := client.pageClock(contextID)
	clock.stopCatchUp()
	clock.held = true
}

// releasePageClock 结束 holdPageClock 并开始追平；调用方持有 inputMu
func (client *bidiClient) releasePageClock(contextID string) {
	clock := client.pageClock(contextID)
	clock.stopCatchUp()
	clock.held = false
	client.catchUpPageClock(contextID, clock)
}

// stopCatchUp 结束进行中的追平，等进行中的设置完成
func (clock *pageClock) stopCatchUp() {
	if clock.stop != nil {
		close(clock.stop)
		<-clock.done
		clock.stop = nil
	}
}

// catchUpPageClock 在后台每帧把标签页的页面时钟设回真实时间，页面时间停在页面上次读取的时刻，直到真实时间追上或下一组输入开始；下一组输入等进行中的设置完成后才开始
func (client *bidiClient) catchUpPageClock(contextID string, clock *pageClock) {
	stop, done := make(chan struct{}), make(chan struct{})
	clock.stop, clock.done = stop, done
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second / 60)
		defer ticker.Stop()
		for {
			lag, err := client.setPageClock(context.Background(), contextID, 0)
			if err != nil || lag >= 0 {
				return
			}
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
}

// cloneAction 复制一个输入动作
func cloneAction(action map[string]any) map[string]any {
	clone := make(map[string]any, len(action))
	for key, value := range action {
		clone[key] = value
	}
	return clone
}
