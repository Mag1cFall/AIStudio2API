package aistudio

import (
	"slices"
	"sort"
	"strings"
	"time"
)

// Channel 表示生成请求的上游额度来源
type Channel string

const (
	// ChannelPlayground 表示官网 Playground 的 GenerateContent
	ChannelPlayground Channel = "playground"
	// ChannelBuild 表示官网 Build 应用代理的 Gemini API 调用
	ChannelBuild Channel = "build"
)

// ChannelCooldownScope 返回通道在作用域上的冷却键，Build 使用 build:<作用域>
func ChannelCooldownScope(channel Channel, scope string) string {
	scope = strings.TrimSpace(scope)
	if channel == ChannelBuild && scope != "" {
		return ModelAccessKey(string(ChannelBuild), scope)
	}
	return scope
}

// channelCandidate 表示一个账户与通道组合
type channelCandidate struct {
	index   int
	channel Channel
}

// SetUpstreamChannels 设置生成请求按顺序启用的上游通道
func (p *AccountPool) SetUpstreamChannels(channels []Channel) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.channels = append([]Channel(nil), channels...)
	p.notifyLocked()
}

// SetBuildCatalog 保存账户 Build 代理返回的 Gemini API 模型目录
func (p *AccountPool) SetBuildCatalog(accountID string, models []Model) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	account := p.byID[strings.TrimSpace(accountID)]
	if account == nil {
		return ErrAccountNotFound
	}
	account.buildModels = cloneAccountModels(models)
	p.notifyLocked()
	return nil
}

// BuildEnabled 返回 Build 通道是否启用
func (p *AccountPool) BuildEnabled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.channelEnabledLocked(ChannelBuild)
}

// Channel 返回租约本次使用的上游通道
func (l *AccountLease) Channel() Channel {
	if l == nil || l.channel == "" {
		return ChannelPlayground
	}
	return l.channel
}

// CooldownScope 返回租约通道在模型作用域上的冷却键
func (l *AccountLease) CooldownScope(scope string) string {
	return ChannelCooldownScope(l.Channel(), scope)
}

func (p *AccountPool) channelEnabledLocked(channel Channel) bool {
	if len(p.channels) == 0 {
		return channel == ChannelPlayground
	}
	return slices.Contains(p.channels, channel)
}

func (p *AccountPool) enabledChannelsLocked() []Channel {
	if len(p.channels) == 0 {
		return []Channel{ChannelPlayground}
	}
	return p.channels
}

// generationChannelSelection 判断选择是否属于按通道调度的生成请求
func generationChannelSelection(selection AccountSelection) bool {
	return strings.TrimSpace(selection.ModelID) != "" && selection.Method == "generateContent" &&
		strings.TrimSpace(selection.ModelAccessScope) == "" && strings.TrimSpace(selection.ResourceID) == "" &&
		strings.TrimSpace(selection.Capability) == "" && !selection.PlaygroundOnly
}

// buildMethods 表示经 Build 代理调用、只服务 Playground 目录没有的模型的上游方法
var buildMethods = []string{"embedContent", "bidiGenerateContent", "bidiGenerateMusic"}

// buildMethodSelection 判断选择是否为 embedding 或实时方法，这类选择的 Build 组合只服务 Build 独有模型
func buildMethodSelection(selection AccountSelection) bool {
	return strings.TrimSpace(selection.ModelID) != "" && slices.Contains(buildMethods, selection.Method) &&
		strings.TrimSpace(selection.Capability) == "" && !selection.PlaygroundOnly
}

// selectionChannelsLocked 返回选择可使用的通道顺序，优先通道排在首位；embedding 只使用 Build，实时方法依次尝试 Playground 与 Build，其余非生成请求只使用 Playground RPC
func (p *AccountPool) selectionChannelsLocked(selection AccountSelection) []Channel {
	if !generationChannelSelection(selection) {
		if buildMethodSelection(selection) && p.channelEnabledLocked(ChannelBuild) {
			if selection.Method == "embedContent" {
				return []Channel{ChannelBuild}
			}
			return []Channel{ChannelPlayground, ChannelBuild}
		}
		return []Channel{ChannelPlayground}
	}
	if selection.Channel != "" {
		if p.channelEnabledLocked(selection.Channel) {
			return []Channel{selection.Channel}
		}
		return nil
	}
	channels := p.enabledChannelsLocked()
	if selection.PreferredChannel == "" || !slices.Contains(channels, selection.PreferredChannel) {
		return channels
	}
	ordered := []Channel{selection.PreferredChannel}
	for _, channel := range channels {
		if channel != selection.PreferredChannel {
			ordered = append(ordered, channel)
		}
	}
	return ordered
}

// preferredChannelOpenLocked 判断账户的优先通道支持选择且未冷却
func (p *AccountPool) preferredChannelOpenLocked(account *Account, selection AccountSelection, now time.Time) bool {
	preferred := selection.PreferredChannel
	if preferred == "" || !slices.Contains(p.selectionChannelsLocked(selection), preferred) ||
		!p.channelSupportsLocked(account, preferred, selection) {
		return false
	}
	_, cooling := accountCooldown(account, ChannelCooldownScope(preferred, selectionAccessScope(selection)), now)
	return !cooling
}

// channelSupportsLocked 判断账户的通道目录是否支持选择
func (p *AccountPool) channelSupportsLocked(account *Account, channel Channel, selection AccountSelection) bool {
	if strings.TrimSpace(selection.ModelID) == "" {
		return channel == ChannelPlayground
	}
	switch channel {
	case ChannelPlayground:
		if generationChannelSelection(selection) && !p.channelEnabledLocked(ChannelPlayground) {
			return false
		}
		return accountSupportsSelection(account, selection)
	case ChannelBuild:
		if buildMethodSelection(selection) {
			return p.channelEnabledLocked(ChannelBuild) && p.buildSupportsMethodLocked(account, selection.ModelID, selection.Method)
		}
		return generationChannelSelection(selection) && p.channelEnabledLocked(ChannelBuild) &&
			p.buildSupportsModelLocked(account, selection.ModelID)
	default:
		return false
	}
}

// accountSupportsAnyChannelLocked 判断账户至少有一个通道支持选择
func (p *AccountPool) accountSupportsAnyChannelLocked(account *Account, selection AccountSelection) bool {
	for _, channel := range p.selectionChannelsLocked(selection) {
		if p.channelSupportsLocked(account, channel, selection) {
			return true
		}
	}
	return false
}

// accountChannelCooldownLocked 返回账户全部支持通道都冷却时的最早恢复时间
func (p *AccountPool) accountChannelCooldownLocked(account *Account, selection AccountSelection, now time.Time) (time.Time, bool) {
	scope := selectionAccessScope(selection)
	var earliest time.Time
	for _, channel := range p.selectionChannelsLocked(selection) {
		if !p.channelSupportsLocked(account, channel, selection) {
			continue
		}
		cooldown, active := accountCooldown(account, ChannelCooldownScope(channel, scope), now)
		if !active {
			return time.Time{}, false
		}
		if earliest.IsZero() || cooldown.Until.Before(earliest) {
			earliest = cooldown.Until
		}
	}
	return earliest, !earliest.IsZero()
}

// channelCandidatesLocked 按账户 ID 与通道顺序展开候选，轮询策略从上次选中组合之后开始
func (p *AccountPool) channelCandidatesLocked(indices []int, selection AccountSelection) []channelCandidate {
	channels := p.selectionChannelsLocked(selection)
	candidates := make([]channelCandidate, 0, len(indices)*len(channels))
	for _, index := range indices {
		for _, channel := range channels {
			candidates = append(candidates, channelCandidate{index: index, channel: channel})
		}
	}
	rank := func(channel Channel) int { return slices.Index(channels, channel) }
	sort.SliceStable(candidates, func(left, right int) bool {
		leftID, rightID := p.accounts[candidates[left].index].ID, p.accounts[candidates[right].index].ID
		if leftID != rightID {
			return leftID < rightID
		}
		return rank(candidates[left].channel) < rank(candidates[right].channel)
	})
	if p.routingStrategy != "round-robin" || len(indices) <= 1 && len(channels) <= 1 {
		return candidates
	}
	key := selectionAccessScope(selection)
	lastAccount := p.lastPicked[key]
	lastChannel := p.lastPickedChannel[key]
	start := sort.Search(len(candidates), func(position int) bool {
		account := p.accounts[candidates[position].index].ID
		if account != lastAccount {
			return account > lastAccount
		}
		return lastChannel != "" && rank(candidates[position].channel) > rank(lastChannel)
	})
	return append(candidates[start:], candidates[:start]...)
}

// buildSupportsModelLocked 判断账户 Build 目录可经生成请求调用模型
func (p *AccountPool) buildSupportsModelLocked(account *Account, modelID string) bool {
	modelID = strings.TrimPrefix(strings.TrimSpace(modelID), "models/")
	found := false
	for _, model := range account.buildModels {
		if model.ID == modelID && hasMethod(model, "generateContent") {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	for _, candidate := range p.accounts {
		if candidate == nil {
			continue
		}
		for _, model := range candidate.Models {
			if modelMatchesID(model, modelID) {
				return !buildExcludedPlaygroundModel(model) && modelAllowedByTier(model, account.BenefitTier)
			}
		}
	}
	return !buildRequiresSpecialTool(modelID)
}

// buildExcludedPlaygroundModel 判断 Playground 目录模型是否使用 Build 未接入的专用路由
func buildExcludedPlaygroundModel(model Model) bool {
	return model.Capabilities["interactions_api"] || model.Capabilities["interaction_route"] ||
		model.Capabilities["transcription_output"]
}

// buildRequiresSpecialTool 判断 Build 独有模型是否只能配合 Computer Use 等专用工具调用
func buildRequiresSpecialTool(modelID string) bool {
	return strings.Contains(modelID, "computer-use")
}

// buildSupportsMethodLocked 判断账户 Build 目录可经 embedding 或实时方法调用 Playground 目录没有的模型
func (p *AccountPool) buildSupportsMethodLocked(account *Account, modelID string, method string) bool {
	modelID = strings.TrimPrefix(strings.TrimSpace(modelID), "models/")
	if p.hasPlaygroundModelLocked(modelID) {
		return false
	}
	for _, model := range account.buildModels {
		if model.ID == modelID && hasMethod(model, method) {
			return true
		}
	}
	return false
}

// buildPublicMethods 返回 Build 独有 embedding 与实时模型在公开目录中可调用的方法
func buildPublicMethods(model Model) []string {
	var methods []string
	for _, method := range buildMethods {
		if !hasMethod(model, method) {
			continue
		}
		if method == "embedContent" {
			methods = append(methods, "batchEmbedContents")
		}
		methods = append(methods, method)
	}
	return methods
}

// buildOnlyModelsLocked 返回启用账户 Build 目录中 Playground 目录没有的可生成、embedding 与实时模型
func (p *AccountPool) buildOnlyModelsLocked() []Model {
	if !p.channelEnabledLocked(ChannelBuild) {
		return nil
	}
	var models []Model
	seen := make(map[string]struct{})
	for _, account := range p.accounts {
		if account == nil || !account.Config.Enabled {
			continue
		}
		for _, model := range account.buildModels {
			if _, exists := seen[model.ID]; exists || p.hasPlaygroundModelLocked(model.ID) {
				continue
			}
			listed := cloneAccountModels([]Model{model})[0]
			if !hasMethod(model, "generateContent") {
				listed.Methods = buildPublicMethods(model)
			} else if !p.buildSupportsModelLocked(account, model.ID) {
				continue
			}
			if len(listed.Methods) == 0 {
				continue
			}
			seen[model.ID] = struct{}{}
			models = append(models, listed)
		}
	}
	return models
}

func (p *AccountPool) hasPlaygroundModelLocked(modelID string) bool {
	for _, account := range p.accounts {
		if account == nil {
			continue
		}
		for _, model := range account.Models {
			if modelMatchesID(model, modelID) {
				return true
			}
		}
	}
	return false
}

// BidiMethod 返回模型的实时方法：Build 目录以 bidiGenerateMusic 提供的模型为实时音乐，其余为 bidiGenerateContent
func (p *AccountPool) BidiMethod(modelID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hasBuildModelLocked(strings.TrimPrefix(strings.TrimSpace(modelID), "models/"), "bidiGenerateMusic") {
		return "bidiGenerateMusic"
	}
	return "bidiGenerateContent"
}

func (p *AccountPool) hasBuildModelLocked(modelID string, method string) bool {
	if !p.channelEnabledLocked(ChannelBuild) {
		return false
	}
	for _, account := range p.accounts {
		if account == nil {
			continue
		}
		for _, model := range account.buildModels {
			if model.ID != modelID || method != "" && !hasMethod(model, method) {
				continue
			}
			if hasMethod(model, "generateContent") && p.buildSupportsModelLocked(account, modelID) {
				return true
			}
			for _, served := range buildMethods {
				if (method == "" || method == served) && p.buildSupportsMethodLocked(account, modelID, served) {
					return true
				}
			}
		}
	}
	return false
}

// modelChannelsLocked 返回至少一个启用账户可以调用模型的通道
func (p *AccountPool) modelChannelsLocked(model Model) []string {
	selection := AccountSelection{ModelID: model.ID}
	if hasMethod(model, "generateContent") {
		selection.Method = "generateContent"
	} else if index := slices.IndexFunc(buildMethods, func(method string) bool { return hasMethod(model, method) }); index >= 0 {
		selection.Method = buildMethods[index]
	}
	var channels []string
	for _, channel := range p.selectionChannelsLocked(selection) {
		for _, account := range p.accounts {
			if account != nil && account.Config.Enabled && p.channelSupportsLocked(account, channel, selection) {
				channels = append(channels, string(channel))
				break
			}
		}
	}
	return channels
}

// AccountChannelAvailable 返回账户是否还有支持选择且未冷却的通道
func (p *AccountPool) AccountChannelAvailable(accountID string, selection AccountSelection) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	account := p.byID[strings.TrimSpace(accountID)]
	if account == nil || !account.Config.Enabled || !p.accountSupportsAnyChannelLocked(account, selection) {
		return false
	}
	_, cooling := p.accountChannelCooldownLocked(account, selection, time.Now())
	return !cooling
}
