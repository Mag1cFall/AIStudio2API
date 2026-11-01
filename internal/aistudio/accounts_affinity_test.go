package aistudio

import (
	"context"
	"testing"
	"time"
)

// affinityTestPool 构造三个 gemini 账户的 key-affinity 测试池，单账户并发为 1
func affinityTestPool(t *testing.T, strategy string) *AccountPool {
	t.Helper()
	model := Model{ID: "gemini", Methods: []string{"generateContent"}, Capabilities: map[string]bool{"chat_model": true}}
	accounts := []*Account{
		{ID: "a@example.com", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1},
		{ID: "b@example.com", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1},
		{ID: "c@example.com", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1},
	}
	pool := NewAccountPool(accounts, 1)
	pool.SetRoutingStrategy(strategy)
	pool.SetUpstreamChannels([]Channel{ChannelPlayground})
	return pool
}

func affinitySelection(seed string) AccountSelection {
	return AccountSelection{ModelID: "gemini", Method: "generateContent", StickySeed: seed}
}

func acquireAffinity(t *testing.T, pool *AccountPool, selection AccountSelection) (*AccountLease, string) {
	t.Helper()
	lease, _, err := pool.TryAcquireFor(context.Background(), selection)
	if err != nil || lease == nil {
		t.Fatalf("lease=%v err=%v", lease, err)
	}
	return lease, lease.Account().ID
}

// TestKeyAffinityDeterministic 同一种子在有效期内反复获取都粘在同一账户
func TestKeyAffinityDeterministic(t *testing.T) {
	pool := affinityTestPool(t, "key-affinity")
	lease, first := acquireAffinity(t, pool, affinitySelection("seed-a"))
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		lease, got := acquireAffinity(t, pool, affinitySelection("seed-a"))
		if got != first {
			t.Fatalf("attempt %d: account=%s want=%s", attempt, got, first)
		}
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestKeyAffinitySpreadsSeeds 首次出现的不同种子按轮询依次分配到不同账户（不会碰撞扎堆）
func TestKeyAffinitySpreadsSeeds(t *testing.T) {
	pool := affinityTestPool(t, "key-affinity")
	seen := make(map[string]bool)
	for _, seed := range []string{"seed-1", "seed-2", "seed-3"} {
		lease, got := acquireAffinity(t, pool, affinitySelection(seed))
		seen[got] = true
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("3 个新种子只分到 %d 个账户: %v", len(seen), seen)
	}
}

// TestKeyAffinityTransientConcurrencyOverflowPreservesPrimary 主账户仅因并发槽满临时溢出借用其他账户时，不改绑主账户；主账户冷却或不可用时才改绑
func TestKeyAffinityTransientConcurrencyOverflowPreservesPrimary(t *testing.T) {
	pool := affinityTestPool(t, "key-affinity")
	// 1. seed-a 首次获取 -> 绑定主账户 first
	lease1, first := acquireAffinity(t, pool, affinitySelection("seed-a"))
	// 2. 不释放 lease1（模拟同一 Key 的并发子请求到达，此时 first 仅并发满但未冷却）
	lease2, second := acquireAffinity(t, pool, affinitySelection("seed-a"))
	if second == first {
		t.Fatalf("期望并发溢出借用其他账户，仍拿到 %s", second)
	}
	// 3. 两个并发请求均结束释放
	if err := lease2.Release(); err != nil {
		t.Fatal(err)
	}
	if err := lease1.Release(); err != nil {
		t.Fatal(err)
	}
	// 4. seed-a 下一轮主请求到达 -> 必须继续留在原主账户 first（不会像 A A B -> B B C 那样漂移到 second）
	lease3, third := acquireAffinity(t, pool, affinitySelection("seed-a"))
	if err := lease3.Release(); err != nil {
		t.Fatal(err)
	}
	if third != first {
		t.Fatalf("并发溢出后主账户从 %s 漂移到了 %s", first, third)
	}
	// 5. 当主账户 first 进入冷却（真正不可用）时，seed-a 切换到新账户并正式改绑到新账户（冷却恢复后也不回跳）
	if err := pool.MarkCooldownIfGeneration(first, ChannelCooldownScope(ChannelPlayground, "gemini"), 1, time.Now(), time.Now().Add(time.Hour), "rate_limit"); err != nil {
		t.Fatal(err)
	}
	lease4, rebound := acquireAffinity(t, pool, affinitySelection("seed-a"))
	if err := lease4.Release(); err != nil {
		t.Fatal(err)
	}
	if rebound == first {
		t.Fatalf("冷却账户 %s 仍被选中", first)
	}
	// 清除原账户 first 的冷却
	pool.ClearCooldown(first, ChannelCooldownScope(ChannelPlayground, "gemini"))
	lease5, afterClear := acquireAffinity(t, pool, affinitySelection("seed-a"))
	defer func() {
		if err := lease5.Release(); err != nil {
			t.Fatal(err)
		}
	}()
	if afterClear != rebound {
		t.Fatalf("冷却改绑后又跳回了 %s，期望留在新主账户 %s", afterClear, rebound)
	}
	if sticky := pool.StickyAccountID("seed-a"); sticky != rebound {
		t.Fatalf("StickyAccountID=%s want=%s", sticky, rebound)
	}
}

// TestKeyAffinityTTLExpiration 超过亲和有效期后重新按轮询分配账户
func TestKeyAffinityTTLExpiration(t *testing.T) {
	pool := affinityTestPool(t, "key-affinity")
	pool.SetKeyAffinityTTL(20 * time.Millisecond)
	lease1, first := acquireAffinity(t, pool, affinitySelection("seed-a"))
	if err := lease1.Release(); err != nil {
		t.Fatal(err)
	}
	// 等待超过 TTL
	time.Sleep(35 * time.Millisecond)
	// 过期后再次请求应推进轮询到下一个账户
	lease2, second := acquireAffinity(t, pool, affinitySelection("seed-a"))
	defer func() {
		if err := lease2.Release(); err != nil {
			t.Fatal(err)
		}
	}()
	if second == first {
		t.Fatalf("TTL 过期后仍停留在 %s，期望按轮询分配新账户", second)
	}
}

// TestKeyAffinityWithinAllowedSubset 当上次粘性账户不在 AllowedAccountIDs 子集内时从子集中轮询选取
func TestKeyAffinityWithinAllowedSubset(t *testing.T) {
	pool := affinityTestPool(t, "key-affinity")
	lease1, first := acquireAffinity(t, pool, affinitySelection("seed-a"))
	if err := lease1.Release(); err != nil {
		t.Fatal(err)
	}
	var subset []string
	for _, id := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		if id != first {
			subset = append(subset, id)
		}
	}
	selection := affinitySelection("seed-a")
	selection.AllowedAccountIDs = subset
	lease2, second := acquireAffinity(t, pool, selection)
	defer func() {
		if err := lease2.Release(); err != nil {
			t.Fatal(err)
		}
	}()
	if second == first {
		t.Fatalf("got excluded account %s", second)
	}
}

// TestKeyAffinityEmptySeedRotates 空种子回退到 round-robin 轮转，避免无 Key 时把全部流量压向单个账户
func TestKeyAffinityEmptySeedRotates(t *testing.T) {
	pool := affinityTestPool(t, "key-affinity")
	seen := make(map[string]bool)
	for attempt := 0; attempt < 4; attempt++ {
		lease, got := acquireAffinity(t, pool, affinitySelection(""))
		seen[got] = true
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) < 2 {
		t.Fatalf("key-affinity 空种子 4 次获取都落在同一账户 %v，期望回退轮转", seen)
	}
}

// TestKeyAffinityIgnoredUnderRoundRobin round-robin 策略忽略种子继续按账户与通道轮转
func TestKeyAffinityIgnoredUnderRoundRobin(t *testing.T) {
	pool := affinityTestPool(t, "round-robin")
	seen := make(map[string]bool)
	for attempt := 0; attempt < 4; attempt++ {
		lease, got := acquireAffinity(t, pool, affinitySelection("seed-a"))
		seen[got] = true
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) < 2 {
		t.Fatalf("round-robin 下同种子 4 次获取都落在 %v", seen)
	}
}
