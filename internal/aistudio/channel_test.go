package aistudio

import (
	"reflect"
	"testing"
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
