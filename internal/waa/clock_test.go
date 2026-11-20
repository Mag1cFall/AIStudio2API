package waa

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/mozjs"
	"github.com/Mag1cFall/AIStudio2API/internal/waa/rhythm"
)

// TestProofNotAheadOfRealTime 核对没有空闲时连续取 proof，snapshot 中读到的 Date.now 不晚于 RunProof 返回时的真实时间：本进程取 proof 的 CPU 耗时短于官网耗时、取 proof 一步的 VM 时钟走得比真实时间快时，这一步开始前真实等待走完时 VM 时钟会超出真实时间的部分
func TestProofNotAheadOfRealTime(t *testing.T) {
	snapshotCosts.Lock()
	saved, count := snapshotCosts.recent, snapshotCosts.count
	snapshotCosts.Unlock()
	t.Cleanup(func() {
		snapshotCosts.Lock()
		snapshotCosts.recent, snapshotCosts.count = saved, count
		snapshotCosts.Unlock()
	})
	for range 9 {
		recordSnapshotCost(1.0)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	runtime, err := NewRuntime(ctx, Options{
		Challenge: Challenge{GlobalName: "ahead", Program: "local"},
		Interpreter: `globalThis.ahead = {a(program, ready) {
			ready(done => { __aheadCPU(); done(String(Date.now())); });
		}};`,
		Profile: Profile{NavigationStart: time.Now(), Locale: "en-US"},
		Rhythm:  rhythm.Rhythm{Press: 10, Hold: 65, Proof: 50, Control: 30, Key: 100, Release: 27},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	installed := make(chan error, 1)
	runtime.loop.post(func() {
		cx, global := runtime.agent.cx, runtime.agent.top.global
		callback, err := cx.NewFunction(global, "__aheadCPU", 0, func(*mozjs.Call) error {
			started := mozjs.CPU()
			for mozjs.CPU()-started < 1.1 {
			}
			return nil
		})
		if err == nil {
			err = cx.Set(global, "__aheadCPU", mozjs.Object(callback))
			cx.Release(callback)
		}
		installed <- err
	})
	if err := <-installed; err != nil {
		t.Fatal(err)
	}
	var leads []float64
	for range 5 {
		value, err := runtime.RunProof(ctx, "local", "hello")
		returned := float64(time.Now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		var date float64
		if err := json.Unmarshal([]byte(value), &date); err != nil {
			t.Fatal(err)
		}
		leads = append(leads, date-returned)
	}
	t.Logf("snapshot 中的 Date.now 减 RunProof 返回时的真实时间：%v ms", leads)
	for _, lead := range leads {
		if lead > 1 {
			t.Fatalf("snapshot 中的 Date.now 晚于 RunProof 返回时的真实时间：%v ms", leads)
		}
	}
}
