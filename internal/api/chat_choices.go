package api

import (
	"context"
	"fmt"
	"sync"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// maxChatChoices 是 Chat n 的上限
const maxChatChoices = 128

// chatChoices 是一次请求按 n 并发启动的生成，任一生成失败时整个请求按首个错误失败
type chatChoices struct {
	ctx     context.Context
	cancel  context.CancelFunc
	events  []<-chan aistudio.Event
	err     error
	once    sync.Once
	running sync.WaitGroup
}

// startChatChoices 在各自 goroutine 中启动每个 choice 并立即读取其事件；非流式请求返回前等待各 Generate 返回，流式请求等待首个就绪的 choice，其余 choice 的启动错误作为该 choice 的流内错误
func (s *server) startChatChoices(ctx context.Context, request aistudio.GenerateRequest, count int, stream bool) *chatChoices {
	choices := &chatChoices{events: make([]<-chan aistudio.Event, count)}
	choices.ctx, choices.cancel = context.WithCancel(ctx)
	started := make(chan error, count)
	for index := range count {
		choice := request
		if index > 0 {
			choice.ID = fmt.Sprintf("%s-%d", request.ID, index)
			choice.Contents = cloneResponseContents(request.Contents)
			if request.Config.Seed != nil {
				seed := *request.Config.Seed + int64(index)
				choice.Config.Seed = &seed
			}
		}
		events := make(chan aistudio.Event)
		choices.events[index] = events
		choices.running.Add(1)
		go func() {
			defer choices.running.Done()
			upstream, err := s.service.Generate(choices.ctx, choice)
			source := upstream
			if err == nil && stream {
				source, err = awaitStreamStart(choices.ctx, upstream)
			}
			started <- err
			if err != nil {
				failed := make(chan aistudio.Event, 1)
				failed <- aistudio.Event{Kind: aistudio.EventError, Err: err}
				close(failed)
				source = failed
			}
			relayEvents(choices.ctx, source, events)
			if upstream != nil {
				for range upstream {
				}
			}
		}()
	}
	waits := count
	if stream {
		waits = 1
	}
	for range waits {
		if err := <-started; err != nil {
			choices.fail(err)
			break
		}
	}
	return choices
}

// relayEvents 经内存队列把 source 的事件转发到 out
func relayEvents(ctx context.Context, source <-chan aistudio.Event, out chan<- aistudio.Event) {
	var queue []aistudio.Event
	for source != nil || len(queue) > 0 {
		var send chan<- aistudio.Event
		var next aistudio.Event
		if len(queue) > 0 {
			send, next = out, queue[0]
		}
		select {
		case <-ctx.Done():
			return
		case send <- next:
			queue = queue[1:]
		case event, ok := <-source:
			if !ok {
				source = nil
				continue
			}
			queue = append(queue, event)
		}
	}
	close(out)
}

// fail 记录首个错误并取消其余生成
func (choices *chatChoices) fail(err error) {
	choices.once.Do(func() {
		choices.err = err
		choices.cancel()
	})
}

// run 并发消费各 choice 的事件流并按 index 返回结果
func (choices *chatChoices) run(consume func(int, <-chan aistudio.Event) (generationResult, error)) ([]generationResult, error) {
	results := make([]generationResult, len(choices.events))
	var wait sync.WaitGroup
	for index, events := range choices.events {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := consume(index, events)
			if err != nil {
				choices.fail(err)
			}
			results[index] = result
		}()
	}
	wait.Wait()
	return results, choices.err
}

// settle 取消未结束的生成；多个 choice 时等待各生成退出，再把访问日志的错误设为首个错误
func (choices *chatChoices) settle(ctx context.Context) {
	choices.cancel()
	if len(choices.events) < 2 {
		return
	}
	choices.running.Wait()
	SetAccessLogError(ctx, choices.err)
}

// record 在多个 choice 时把用量与工具调用数之和写入访问日志
func (choices *chatChoices) record(ctx context.Context, results []generationResult) {
	if len(choices.events) < 2 {
		return
	}
	toolCalls := 0
	for index := range results {
		toolCalls += len(results[index].toolCalls)
	}
	SetAccessLogGenerationResult(ctx, sumUsage(results), toolCalls)
}

// sumUsage 返回各次生成用量之和，均无用量时返回 nil
func sumUsage(results []generationResult) *aistudio.Usage {
	var total *aistudio.Usage
	for index := range results {
		usage := results[index].usage
		if usage == nil {
			continue
		}
		if total == nil {
			total = &aistudio.Usage{}
		}
		total.InputTokens += usage.InputTokens
		total.OutputTokens += usage.OutputTokens
		total.ReasoningTokens += usage.ReasoningTokens
		total.ToolTokens += usage.ToolTokens
		total.TotalTokens += usage.TotalTokens
		total.OutputTokensMissing = total.OutputTokensMissing || usage.OutputTokensMissing
	}
	return total
}
