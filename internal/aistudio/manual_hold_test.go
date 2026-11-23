package aistudio

import (
	"context"
	"errors"
	"testing"
)

// newManualHoldPool 构造两个可调度账户的测试池
func newManualHoldPool() *AccountPool {
	model := Model{ID: "gemini", Methods: []string{"generateContent"}, Capabilities: map[string]bool{"chat_model": true}}
	accounts := []*Account{
		{ID: "a@example.com", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1},
		{ID: "b@example.com", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1},
	}
	pool := NewAccountPool(accounts, 1)
	pool.SetUpstreamChannels([]Channel{ChannelPlayground})
	for _, account := range accounts {
		if err := pool.SetBuildCatalog(account.ID, []Model{model}); err != nil {
			panic(err)
		}
	}
	return pool
}

// TestManualHoldSkipsAccount 验证手动占用的账户不参与通用调度，释放后恢复
func TestManualHoldSkipsAccount(t *testing.T) {
	pool := newManualHoldPool()
	selection := AccountSelection{ModelID: "gemini", Method: "generateContent", PreferredChannel: ChannelBuild}

	pool.SetManualHold("a@example.com", 1)
	if !pool.ManualHold("a@example.com") {
		t.Fatal("占用标记未生效")
	}
	for range 5 {
		lease, ok, err := pool.TryAcquireFor(context.Background(), selection)
		if err != nil || !ok || lease == nil {
			t.Fatalf("占用 a 后应始终选到 b，lease=%v ok=%v err=%v", lease, ok, err)
		}
		if lease.Account().ID != "b@example.com" {
			t.Fatalf("选中了被占用的账户 %s", lease.Account().ID)
		}
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	}

	pool.SetManualHold("a@example.com", -1)
	if pool.ManualHold("a@example.com") {
		t.Fatal("释放占用后标记仍存在")
	}
	seen := map[string]bool{}
	for range 4 {
		lease, ok, err := pool.TryAcquireFor(context.Background(), selection)
		if err != nil || !ok || lease == nil {
			t.Fatalf("释放占用后应能正常选号，lease=%v ok=%v err=%v", lease, ok, err)
		}
		seen[lease.Account().ID] = true
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	}
	if !seen["a@example.com"] || !seen["b@example.com"] {
		t.Fatalf("释放占用后两个账户都应可被选中，实际 %v", seen)
	}
}

// TestManualHoldDirectSelection 验证手动占用只拦截生成请求选号、不拦截管理租约，嵌套占用可逐层释放
func TestManualHoldDirectSelection(t *testing.T) {
	pool := newManualHoldPool()
	selection := AccountSelection{AccountID: "a@example.com"}
	pool.SetManualHold("a@example.com", 1)

	// 生成请求路径指定被占用账户时返回明确错误
	_, ok, err := pool.TryAcquireFor(context.Background(), selection)
	if !errors.Is(err, ErrAccountManualHold) {
		t.Fatalf("指定被占用账户的选号错误 = %v，期望 ErrAccountManualHold", err)
	}
	if ok {
		t.Fatal("被占用账户不应返回可用租约")
	}

	// 管理操作租约不受调度占用限制，远程操作结束后的 Cookie 回写依赖它
	lease, err := pool.AcquireAccount(context.Background(), "a@example.com")
	if err != nil {
		t.Fatalf("管理租约不应被手动占用拦截: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	// 计数语义：两层占用时释放一层仍保持占用，生成请求继续被拦截
	pool.SetManualHold("a@example.com", 1)
	pool.SetManualHold("a@example.com", -1)
	if !pool.ManualHold("a@example.com") {
		t.Fatal("计数占用释放一层后不应提前解除")
	}
	if _, _, err := pool.TryAcquireFor(context.Background(), selection); !errors.Is(err, ErrAccountManualHold) {
		t.Fatalf("占用未解除时选号错误 = %v，期望 ErrAccountManualHold", err)
	}

	pool.SetManualHold("a@example.com", -1)
	if pool.ManualHold("a@example.com") {
		t.Fatal("全部释放后占用应解除")
	}
	// 释放后生成请求路径恢复
	lease, ok, err = pool.TryAcquireFor(context.Background(), selection)
	if err != nil || !ok || lease == nil {
		t.Fatalf("占用解除后应能选号，lease=%v ok=%v err=%v", lease, ok, err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}
