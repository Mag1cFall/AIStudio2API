package app

import (
	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

// upstreamChannels 把配置中的通道名称转换为调度池通道
func upstreamChannels(names []string) []aistudio.Channel {
	channels := make([]aistudio.Channel, 0, len(names))
	for _, name := range names {
		channels = append(channels, aistudio.Channel(name))
	}
	return channels
}

// modelChannels 把 MODEL_CHANNELS 转成模型到通道的映射，配置已在保存与启动时校验
func modelChannels(value string) map[string]aistudio.Channel {
	parsed, _ := config.ParseModelChannels(value)
	channels := make(map[string]aistudio.Channel, len(parsed))
	for model, channel := range parsed {
		channels[model] = aistudio.Channel(channel)
	}
	return channels
}
