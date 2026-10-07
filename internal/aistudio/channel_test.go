package aistudio

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// TestChannelSelection 保留文件绑定与专属能力的通道资格
func TestChannelSelection(t *testing.T) {
	pool := NewAccountPool(nil, 1)
	pool.SetUpstreamChannels([]Channel{ChannelPlayground, ChannelBuild})
	for _, item := range []struct {
		selection AccountSelection
		want      []Channel
	}{
		{AccountSelection{ModelID: "gemini", Method: "generateContent"}, []Channel{ChannelPlayground, ChannelBuild}},
		{AccountSelection{ModelID: "gemini", Method: "generateContent", Channel: ChannelBuild}, []Channel{ChannelBuild}},
		{AccountSelection{ModelID: "gemini", Method: "generateContent", ResourceID: "file", Channel: ChannelBuild}, []Channel{ChannelPlayground}},
		{AccountSelection{ModelID: "gemini", Method: "countTokens", Channel: ChannelBuild}, []Channel{ChannelPlayground}},
		{AccountSelection{ModelID: "gemini", Method: "generateContent", PlaygroundOnly: true, Channel: ChannelBuild}, []Channel{ChannelPlayground}},
	} {
		if got := pool.selectionChannelsLocked(item.selection); !reflect.DeepEqual(got, item.want) {
			t.Errorf("selection=%+v channels=%v want=%v", item.selection, got, item.want)
		}
	}
	pool.SetUpstreamChannels([]Channel{ChannelPlayground})
	if got := pool.selectionChannelsLocked(AccountSelection{ModelID: "gemini", Method: "generateContent", Channel: ChannelBuild}); len(got) != 0 {
		t.Fatalf("disabled Build channels=%v", got)
	}
}

// TestPreferredChannelPerAccount 验证优先通道按账户生效，账户的优先通道冷却后由同账户其余通道接住
func TestPreferredChannelPerAccount(t *testing.T) {
	model := Model{ID: "gemini", Methods: []string{"generateContent"}, Capabilities: map[string]bool{"chat_model": true}}
	accounts := []*Account{
		{ID: "a@example.com", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1},
		{ID: "b@example.com", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1},
	}
	pool := NewAccountPool(accounts, 1)
	pool.SetUpstreamChannels([]Channel{ChannelPlayground, ChannelBuild})
	for _, account := range accounts {
		if err := pool.SetBuildCatalog(account.ID, []Model{model}); err != nil {
			t.Fatal(err)
		}
	}
	selection := AccountSelection{ModelID: "gemini", Method: "generateContent", PreferredChannel: ChannelBuild}
	var picks []string
	pick := func() {
		lease, _, err := pool.TryAcquireFor(context.Background(), selection)
		if err != nil || lease == nil {
			t.Fatalf("lease=%v err=%v", lease, err)
		}
		picks = append(picks, lease.Account().ID+"/"+string(lease.Channel()))
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		pick()
	}
	if err := pool.MarkCooldownIfGeneration("a@example.com", ChannelCooldownScope(ChannelBuild, "gemini"), 1, time.Now(), time.Now().Add(time.Hour), "fixture"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		pick()
	}
	want := []string{"a@example.com/build", "b@example.com/build", "a@example.com/build", "a@example.com/playground", "b@example.com/build"}
	if !reflect.DeepEqual(picks, want) {
		t.Fatalf("picks=%v want=%v", picks, want)
	}
}
