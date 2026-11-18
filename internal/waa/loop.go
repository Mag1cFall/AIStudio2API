package waa

import (
	"container/heap"
	"math"
	"runtime"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/mozjs"
)

// eventLoop 在一个锁定 OS 线程的 goroutine 中按 VM 时钟顺序串行执行 VM 任务与计时器回调：任务按到期时刻排序，到期时刻晚于现在、晚于 horizon 且晚于上一个任务结束时刻的任务真实等待到期（VM 时钟领先的初始化到静置之前只等到领先 leadLimit 的时刻），其余任务立即在到期时刻与上一个任务结束时刻中较晚的 VM 时刻开始，任务中按本线程 CPU 时间的倍数走、走过任务的上限后停住；afterTask 在每个任务结束后执行，cleanup 在循环结束时于同一线程执行
type eventLoop struct {
	mu     sync.Mutex
	tasks  taskQueue
	seq    uint64
	wake   chan struct{}
	done   chan struct{}
	exited chan struct{}
	closed bool
	// last 是上一个任务结束时的 VM 时钟毫秒数，只在事件循环中读写
	last float64
	// horizon 是没有空闲时一串界面操作在 VM 时钟上结束的毫秒数，到期不晚于它的任务不等真实时间，VM 时钟因此最多领先真实时间 leadLimit 加最后一步的耗时，只在事件循环中读写
	horizon float64
	// starting 为真时任务只真实等到领先真实时间 leadLimit 的时刻，从 VM 时钟领先的初始化开始到 VM 静置为止，只在事件循环中读写
	starting  bool
	afterTask func()
	cleanup   func()
}

// task 是事件循环中的一个任务：at 为按 VM 时钟的到期毫秒数，pace 见 timedJob，limit 大于 0 时为任务中 VM 时钟最多走过的毫秒数，host 为真时是不执行 JS 的宿主任务，不推进 VM 时钟、不占用空闲区间，rest 是同一串界面操作中本步之后的各步，下一步排在本步开始后的间隔处
type task struct {
	at    float64
	seq   uint64
	job   func()
	pace  float64
	limit float64
	host  bool
	rest  []timedJob
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

// newEventLoop 创建第一个任务不早于 VM 时刻 start 开始的事件循环，start 晚于现在时 VM 时钟领先，到静置为止领先不超过 leadLimit 的任务都不等真实时间
func newEventLoop(start float64) *eventLoop {
	loop := &eventLoop{wake: make(chan struct{}, 1), done: make(chan struct{}), exited: make(chan struct{}), last: start, starting: start > mozjs.Now()}
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
		wait := next.at - mozjs.Now()
		if loop.starting {
			wait -= leadLimit
		}
		if wait > 0 && next.at > max(loop.horizon, loop.last) {
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
		pace := next.pace
		if pace == 0 {
			pace = taskPace()
		}
		limit := math.Inf(1)
		if next.limit > 0 {
			limit = next.limit
		}
		mozjs.SetClock(started, pace, limit)
		next.job()
		if loop.afterTask != nil {
			loop.afterTask()
		}
		loop.last = mozjs.Clock()
		if len(next.rest) > 0 {
			step := next.rest[0]
			loop.push(&task{at: started + milliseconds(step.after), job: step.job, pace: step.pace, limit: stepLimit(step, next.rest[1:]), rest: next.rest[1:]})
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

// timedJob 是一串界面操作中的一步：after 为与上一步的 VM 时钟间隔；pace 大于 0 时这一步中 VM 时钟按本线程 CPU 时间的 pace 倍走，pace 为 0 时按 taskPace 走；span 大于 0 时这一步的 VM 耗时为 span 毫秒，走到后停住
type timedJob struct {
	after time.Duration
	job   func()
	span  float64
	pace  float64
}

// arrange 把 build 按此前空闲的 VM 时钟毫秒数给出的 jobs 排进空闲区间：每步在上一步开始后隔 after 执行、上一步未结束时紧接其后，按各步 need 之和最后一步落在现在，第一步不早于上一个任务结束，空闲不够时整体后移并把 horizon 推到最后一步结束，各步不等真实时间、VM 时钟暂时领先，之后的空闲由真实时间追上；build 给出的各步领先不超过 leadLimit；build 在事件循环中调用，循环已关闭时返回 false
func (loop *eventLoop) arrange(build func(idle float64) []timedJob) bool {
	return loop.push(&task{at: mozjs.Now(), host: true, job: func() {
		now := mozjs.Now()
		jobs := build(now - loop.last)
		var total, end float64
		for _, item := range jobs {
			total += need(item.after, item.span, item.pace)
			end += milliseconds(item.after) + item.span
		}
		start := max(loop.last, now-total)
		loop.horizon = max(loop.horizon, start+end)
		loop.push(&task{at: start + milliseconds(jobs[0].after), job: jobs[0].job, pace: jobs[0].pace, limit: stepLimit(jobs[0], jobs[1:]), rest: jobs[1:]})
	}})
}

// leadLimit 是没有空闲时 VM 时钟最多领先真实时间的毫秒数
const leadLimit = 1000.0

// stepLimit 返回一串界面操作中一步的 VM 耗时上限：设了 span 的一步为 span，其后还有一步时为到下一步的间隔，官网页面在下一次输入之前处理完这一步，否则为 0
func stepLimit(step timedJob, rest []timedJob) float64 {
	if step.span > 0 {
		return step.span
	}
	if len(rest) > 0 {
		return milliseconds(rest[0].after)
	}
	return 0
}

// need 返回一步排进空闲区间所需的 VM 时钟毫秒数：与上一步的间隔加这一步 VM 耗时 span 超出预计真实耗时 span/pace 的部分
func need(after time.Duration, span, pace float64) float64 {
	total := milliseconds(after)
	if span > 0 && pace > 0 {
		total += max(span-span/pace, 0)
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
