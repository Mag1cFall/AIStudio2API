package waa

import (
	"math/rand/v2"
	"slices"
	"sync"
)

// proofCosts 是官网 Firefox 页面 1828 上报中 BotGuard 取 proof 的毫秒耗时，为向下取整后的值
var proofCosts = []float64{5, 4, 3, 5, 5, 3}

// firefoxProofCost 是官网取 proof 耗时在向下取整前的均值
var firefoxProofCost = func() float64 {
	var total float64
	for _, cost := range proofCosts {
		total += cost
	}
	return total/float64(len(proofCosts)) + 0.5
}()

// snapshotCosts 是本进程最近各次取 proof 的线程 CPU 毫秒耗时
var snapshotCosts struct {
	sync.Mutex
	recent [9]float64
	count  int
}

// newProofCost 为一次取 proof 取一个官网现场的取 proof 耗时，在向下取整前的 1 ms 内均匀取值
func newProofCost() float64 {
	return proofCosts[rand.IntN(len(proofCosts))] + rand.Float64()
}

// recordSnapshotCost 记录一次取 proof 的线程 CPU 毫秒耗时
func recordSnapshotCost(milliseconds float64) {
	snapshotCosts.Lock()
	snapshotCosts.recent[snapshotCosts.count%len(snapshotCosts.recent)] = milliseconds
	snapshotCosts.count++
	snapshotCosts.Unlock()
}

// recentSnapshotCosts 返回本进程最近各次取 proof 的线程 CPU 毫秒耗时，从小到大排列
func recentSnapshotCosts() []float64 {
	snapshotCosts.Lock()
	defer snapshotCosts.Unlock()
	return slices.Sorted(slices.Values(snapshotCosts.recent[:min(snapshotCosts.count, len(snapshotCosts.recent))]))
}

// proofPace 返回取 proof 一步中 VM 时钟相对线程 CPU 时间走的倍数：在本进程最近最短取 proof CPU 耗时的七成内走过 cost，取 proof 一步的 VM 耗时因此几乎总是走到 cost 后停住；还没有取过 proof 时为 1
func proofPace(cost float64) float64 {
	recent := recentSnapshotCosts()
	if len(recent) == 0 || recent[0] <= 0 {
		return 1
	}
	return cost / (0.7 * recent[0])
}

// taskPace 返回其余任务中 VM 时钟相对线程 CPU 时间走的倍数：官网取 proof 耗时均值与本进程取 proof CPU 耗时中位数之比，不大于 1，宿主比官网慢时 VM 读到的执行耗时按官网 Firefox 的执行速度呈现，VM 时钟不因倍数走到真实时间之前；还没有取过 proof 时为 1
func taskPace() float64 {
	recent := recentSnapshotCosts()
	if len(recent) == 0 || recent[len(recent)/2] <= 0 {
		return 1
	}
	return min(1, firefoxProofCost/recent[len(recent)/2])
}
