package waa

import (
	"container/heap"
	"math"
	"runtime"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/mozjs"
)

// eventLoop 在一个锁定 OS 线程的 goroutine 中按 VM 时钟顺序串行执行 VM 任务与计时器回调：任务按到期时刻排序，任务开始时的 VM 时刻晚于现在时真实等待，VM 时钟在每个任务开始时不超前于真实时间；afterTask 在每个任务结束后执行，cleanup 在循环结束时于同一线程执行
type eventLoop struct {
	mu     sync.Mutex
	tasks  taskQueue
	seq    uint64
	wake   chan struct{}
	done   chan struct{}
	exited chan struct{}
	closed bool
	// last 是上一个任务结束时的 VM 时钟毫秒数，只在事件循环中读写
	last      float64
	afterTask func()
	cleanup   func()
}

// task 是事件循环中的一个任务：at 为按 VM 时钟的到期毫秒数，span 与 ramp 见 timedJob，host 为真时是不执行 JS 的宿主任务，不推进 VM 时钟、不占用空闲区间，rest 是同一串界面操作中本步之后的各步，下一步排在本步开始后的间隔处
type task struct {
	at   float64
	seq  uint64
	job  func()
	span float64
	ramp float64
	host bool
	rest []timedJob
}

// taskQueue 是按到期时刻与入队顺序排列的任务小顶堆
type taskQueue []*task

func (queue taskQueue) Len() int { return len(queue) }
func (queue taskQueue) Less(i, j int) bool {
	return queue[i].at < queue[j].at || queue[i].at == queue[j].at && queue[i].seq < queue[j].seq
}
func (queue taskQueue) Swap(i, j int)   { queue[i], queue[j] = queue[j], queue[i] }
func (queue *taskQueue) Push(value any) { *queue = append(*queue, value.(*task)) }
func (queue *taskQueue) Pop() any {
	old := *queue
	last := old[len(old)-1]
	old[len(old)-1] = nil
	*queue = old[:len(old)-1]
	return last
}

func newEventLoop() *eventLoop {
	loop := &eventLoop{wake: make(chan struct{}, 1), done: make(chan struct{}), exited: make(chan struct{})}
	go loop.run()
	return loop
}

func (loop *eventLoop) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(loop.exited)
	defer func() {
		if loop.cleanup != nil {
			loop.cleanup()
		}
	}()
	var timer *time.Timer
	for {
		loop.mu.Lock()
		if loop.closed {
			loop.mu.Unlock()
			return
		}
		if len(loop.tasks) == 0 {
			loop.mu.Unlock()
			select {
			case <-loop.wake:
			case <-loop.done:
				return
			}
			continue
		}
		next := loop.tasks[0]
		started := max(next.at, loop.last)
		if wait := started - mozjs.Now(); wait > 0 {
			loop.mu.Unlock()
			if timer == nil {
				timer = time.NewTimer(time.Duration(wait * float64(time.Millisecond)))
			} else {
				timer.Reset(time.Duration(wait * float64(time.Millisecond)))
			}
			select {
			case <-timer.C:
			case <-loop.wake:
				timer.Stop()
			case <-loop.done:
				timer.Stop()
				return
			}
			continue
		}
		heap.Pop(&loop.tasks)
		loop.mu.Unlock()
		if next.host {
			next.job()
			continue
		}
		pace, limit := 1.0, math.Inf(1)
		if next.span > 0 {
			limit = next.span
			if next.ramp > 0 {
				pace = next.span / next.ramp
			}
		}
		mozjs.SetClock(started, pace, limit)
		next.job()
		if loop.afterTask != nil {
			loop.afterTask()
		}
		loop.last = mozjs.Clock()
		if len(next.rest) > 0 {
			step := next.rest[0]
			loop.push(&task{at: started + milliseconds(step.after), job: step.job, span: step.span, ramp: step.ramp, rest: next.rest[1:]})
		}
	}
}

// milliseconds 返回 duration 的毫秒数
func milliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

// push 把任务按到期时刻放入队列并唤醒事件循环，循环已关闭时返回 false
func (loop *eventLoop) push(item *task) bool {
	loop.mu.Lock()
	if loop.closed {
		loop.mu.Unlock()
		return false
	}
	loop.seq++
	item.seq = loop.seq
	heap.Push(&loop.tasks, item)
	loop.mu.Unlock()
	select {
	case loop.wake <- struct{}{}:
	default:
	}
	return true
}

// post 把任务排在现在，循环已关闭时返回 false
func (loop *eventLoop) post(job func()) bool {
	return loop.push(&task{at: mozjs.Now(), job: job})
}

// postHost 把不执行 JS 的宿主任务排在现在，循环已关闭时返回 false
func (loop *eventLoop) postHost(job func()) bool {
	return loop.push(&task{at: mozjs.Now(), job: job, host: true})
}

// now 返回当前任务中的 VM 时钟毫秒数，只在事件循环中调用
func (loop *eventLoop) now() float64 {
	return mozjs.Clock()
}

// wall 返回当前任务中 VM 时钟对应的墙上时刻，只在事件循环中调用
func (loop *eventLoop) wall() time.Time {
	return time.Now().Add(time.Duration((mozjs.Clock() - mozjs.Now()) * float64(time.Millisecond)))
}

// schedule 在 VM 时钟 delay 之后执行任务，只在事件循环中调用
func (loop *eventLoop) schedule(delay time.Duration, job func()) {
	loop.push(&task{at: loop.now() + milliseconds(max(delay, 0)), job: job})
}

// timedJob 是一串界面操作中的一步：after 为与上一步的 VM 时钟间隔；span 大于 0 时这一步中 VM 时钟在 ramp 毫秒真实时间内匀速走过 span 毫秒后停住，ramp 为 0 时按真实时间走到 span 后停住；span 为 0 时按真实时间走
type timedJob struct {
	after time.Duration
	job   func()
	span  float64
	ramp  float64
}

// arrange 把 build 按此前空闲的 VM 时钟毫秒数给出的 jobs 排进空闲区间：每步在上一步开始后隔 after 执行、上一步未结束时紧接其后，按各步 need 之和最后一步落在现在，第一步不早于上一个任务结束，空闲不够时整体后移，只真实等待差额；build 在事件循环中调用，循环已关闭时返回 false
func (loop *eventLoop) arrange(build func(idle float64) []timedJob) bool {
	return loop.push(&task{at: mozjs.Now(), host: true, job: func() {
		now := mozjs.Now()
		jobs := build(now - loop.last)
		var total float64
		for _, item := range jobs {
			total += need(item.after, item.span, item.ramp)
		}
		start := max(loop.last, now-total)
		loop.push(&task{at: start + milliseconds(jobs[0].after), job: jobs[0].job, span: jobs[0].span, ramp: jobs[0].ramp, rest: jobs[1:]})
	}})
}

// need 返回一步排进空闲区间所需的 VM 时钟毫秒数：与上一步的间隔加 span 超出 ramp 的部分
func need(after time.Duration, span, ramp float64) float64 {
	total := milliseconds(after)
	if span > 0 && ramp > 0 {
		total += max(span-ramp, 0)
	}
	return total
}

func (loop *eventLoop) close() {
	loop.mu.Lock()
	if loop.closed {
		loop.mu.Unlock()
		return
	}
	loop.closed = true
	clear(loop.tasks)
	loop.tasks = nil
	loop.mu.Unlock()
	close(loop.done)
}
