package app

import "github.com/Mag1cFall/AIStudio2API/internal/aistudio"

// upstreamChannels 把配置中的通道名称转换为调度池通道
func upstreamChannels(names []string) []aistudio.Channel {
	channels := make([]aistudio.Channel, 0, len(names))
	for _, name := range names {
		channels = append(channels, aistudio.Channel(name))
	}
	return channels
}
