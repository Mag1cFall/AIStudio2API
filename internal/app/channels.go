package app

import (
	"strings"
	"unicode"

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

// modelAccounts 把 MODEL_ACCOUNTS 转成模型到可用账户 ID 的映射，配置已在保存与启动时校验
func modelAccounts(value string) map[string][]string {
	accounts, _ := config.ParseModelAccounts(value)
	return accounts
}

// accountOrder 把 ACCOUNT_ORDER 拆成依次排列的账户 ID
func accountOrder(value string) []string {
	return strings.FieldsFunc(value, func(char rune) bool { return char == ',' || unicode.IsSpace(char) })
}

// apiKeyAccounts 解析 API_KEY_ACCOUNTS 为各 API key 可用的账户 ID
func apiKeyAccounts(keys, bindings string) map[string][]string {
	accounts, _ := config.ParseAPIKeyAccounts(bindings, keys)
	return accounts
}
