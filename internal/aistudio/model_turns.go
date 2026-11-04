package aistudio

import (
	"slices"
	"sync"
)

// modelTurnLimit 是本进程保存的 Playground 模型轮条数上限
const modelTurnLimit = 1000

// modelTurn 是一次 Playground 生成按上游顺序给出的模型轮 Part 与最后一帧的轮次令牌
type modelTurn struct {
	parts     []Part
	turnToken string
}

// modelTurnKey 是请求所用 API key 与到该模型轮为止的对话摘要
type modelTurnKey struct {
	apiKey string
	digest string
}

// modelTurnHistory 按 API key 与对话摘要保存本进程生成的模型轮，客户端回传同一回复时与官网一样带回思考 Part、签名 Part 与轮次令牌
type modelTurnHistory struct {
	mu      sync.Mutex
	entries map[modelTurnKey]modelTurn
	order   []modelTurnKey
}

func newModelTurnHistory() *modelTurnHistory {
	return &modelTurnHistory{entries: make(map[modelTurnKey]modelTurn)}
}

// store 保存接在 history 之后生成的模型轮；history 以模型轮结尾、回复带内联媒体或没有思考、签名与轮次令牌时不保存，超过上限时丢弃最早的一条
func (turns *modelTurnHistory) store(apiKey string, history []Content, turn modelTurn) {
	if history[len(history)-1].Role == RoleAssistant || slices.ContainsFunc(turn.parts, func(part Part) bool { return part.InlineData != nil }) ||
		turn.turnToken == "" && !slices.ContainsFunc(turn.parts, func(part Part) bool { return part.Thought || part.ThoughtSignature != "" }) {
		return
	}
	links := assistantLinks(append(slices.Clone(history), Content{Role: RoleAssistant, Parts: turn.parts}))
	key := modelTurnKey{apiKey: apiKey, digest: links[len(links)-1].digest}
	turns.mu.Lock()
	defer turns.mu.Unlock()
	if _, exists := turns.entries[key]; !exists {
		turns.order = append(turns.order, key)
	}
	turns.entries[key] = turn
	if len(turns.order) > modelTurnLimit {
		delete(turns.entries, turns.order[0])
		turns.order = turns.order[1:]
	}
}

// restore 把对话中本进程生成过的模型轮换成生成时的 Part，函数调用沿用客户端回传的 ID，返回改写后的对话与最后一个模型轮的轮次令牌
func (turns *modelTurnHistory) restore(apiKey string, contents []Content) ([]Content, string) {
	turns.mu.Lock()
	defer turns.mu.Unlock()
	restored := make([]Content, 0, len(contents))
	token := ""
	start := 0
	for _, link := range assistantLinks(contents) {
		turn, found := turns.entries[modelTurnKey{apiKey: apiKey, digest: link.digest}]
		switch {
		case link.model && found:
			restored = append(restored, Content{Role: RoleAssistant, Parts: withCallIDs(turn.parts, contents[start:link.end])})
			token = turn.turnToken
		case link.model:
			restored = append(restored, contents[start:link.end]...)
			token = ""
		default:
			restored = append(restored, contents[start:link.end]...)
		}
		start = link.end
	}
	return restored, token
}

// withCallIDs 返回 parts 的副本，其中的函数调用依次换成 echoed 中函数调用的 ID
func withCallIDs(parts []Part, echoed []Content) []Part {
	var ids []string
	for _, content := range echoed {
		for _, part := range content.Parts {
			if part.FunctionCall != nil {
				ids = append(ids, part.FunctionCall.ID)
			}
		}
	}
	restored := slices.Clone(parts)
	for index, part := range restored {
		if part.FunctionCall == nil {
			continue
		}
		call := *part.FunctionCall
		call.ID, ids = ids[0], ids[1:]
		restored[index].FunctionCall = &call
	}
	return restored
}
