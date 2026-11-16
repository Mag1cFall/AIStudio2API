package waa

import (
	"math/rand/v2"
	"slices"
	"sync"
)

// proofCosts 是官网 Firefox 页面 1828 上报中 BotGuard 取 proof 的毫秒耗时，为向下取整后的值
var proofCosts = []float64{5, 4, 3, 5, 5, 3}

// snapshotCosts 是本进程最近各次取 proof 的真实毫秒耗时
var snapshotCosts struct {
	sync.Mutex
	recent [9]float64
	count  int
}

// newProofCost 为一个 VM 取一个官网现场的取 proof 耗时，在向下取整前的 1 ms 内均匀取值
func newProofCost() float64 {
	return proofCosts[rand.IntN(len(proofCosts))] + rand.Float64()
}

// recordSnapshotCost 记录一次取 proof 的真实毫秒耗时
func recordSnapshotCost(milliseconds float64) {
	snapshotCosts.Lock()
	snapshotCosts.recent[snapshotCosts.count%len(snapshotCosts.recent)] = milliseconds
	snapshotCosts.count++
	snapshotCosts.Unlock()
}

// proofRamp 返回取 proof 一步中 VM 时钟走到本 VM 取 proof 耗时所用的真实毫秒数：本进程最近各次取 proof 中最短真实耗时的七成，还没有取过时为 0
func proofRamp() float64 {
	snapshotCosts.Lock()
	defer snapshotCosts.Unlock()
	recent := snapshotCosts.recent[:min(snapshotCosts.count, len(snapshotCosts.recent))]
	if len(recent) == 0 {
		return 0
	}
	return slices.Min(recent) * 0.7
}
