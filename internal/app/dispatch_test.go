package app

import (
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// TestDispatchKeyIncludesStickySeed 仅在 key-affinity 策略下将种子纳入队列键，其他策略不受影响
func TestDispatchKeyIncludesStickySeed(t *testing.T) {
	base := aistudio.AccountSelection{ModelID: "gemini", Method: "generateContent"}
	withSeedA, withSeedB := base, base
	withSeedA.StickySeed = "seed-a"
	withSeedB.StickySeed = "seed-b"
	if dispatchKey(withSeedA, "key-affinity") == dispatchKey(withSeedB, "key-affinity") {
		t.Error("key-affinity 下不同亲和种子的 dispatchKey 相同")
	}
	if dispatchKey(withSeedA, "key-affinity") != dispatchKey(withSeedA, "key-affinity") {
		t.Error("相同亲和种子的 dispatchKey 不同")
	}
	if dispatchKey(withSeedA, "round-robin") != dispatchKey(withSeedB, "round-robin") {
		t.Error("round-robin 下不同亲和种子不应拆分队列")
	}
	if dispatchKey(withSeedA, "fill-first") != dispatchKey(withSeedB, "fill-first") {
		t.Error("fill-first 下不同亲和种子不应拆分队列")
	}
}
