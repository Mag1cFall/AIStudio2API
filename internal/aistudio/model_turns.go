package aistudio

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
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
	links := turnLinks(append(slices.Clone(history), Content{Role: RoleAssistant, Parts: turn.parts}))
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
	for _, link := range turnLinks(contents) {
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

// turnLink 是对话中一组连续的模型轮或其余轮次结束处的累积摘要
type turnLink struct {
	end    int
	model  bool
	digest string
}

// turnLinks 把对话按连续的模型轮与其余轮次分组，返回每组结束处的累积摘要
func turnLinks(contents []Content) []turnLink {
	var links []turnLink
	digest := ""
	for start := 0; start < len(contents); {
		model := contents[start].Role == RoleAssistant
		end := start
		var parts []Part
		for ; end < len(contents) && (contents[end].Role == RoleAssistant) == model; end++ {
			parts = append(parts, contents[end].Parts...)
		}
		digest = turnLinkDigest(digest, model, parts)
		links = append(links, turnLink{end: end, model: model, digest: digest})
		start = end
	}
	return links
}

// turnLinkDigest 返回在 previous 之后接上一组轮次的摘要：正文 Part 直接连接后去掉首尾空白，函数调用计入名称与规范参数，函数结果计入规范内容，其余 Part 去掉签名、内联数据以其 SHA-256 计入；思考与调用 ID 不计入，客户端回传的模型轮与服务输出的同一回复得到同一摘要
func turnLinkDigest(previous string, model bool, parts []Part) string {
	var text []string
	items := []any{}
	for _, part := range parts {
		switch {
		case part.Thought:
		case part.FunctionCall != nil:
			items = append(items, []any{part.FunctionCall.Name, canonicalJSON(part.FunctionCall.Arguments)})
		case part.FunctionResult != nil:
			items = append(items, []any{canonicalJSON(part.FunctionResult.Content)})
		case part.Text != "":
			text = append(text, part.Text)
		case !part.payload():
		default:
			part.ThoughtSignature = ""
			if part.InlineData != nil {
				sum := sha256.Sum256(part.InlineData.Data)
				part.InlineData = &Blob{MIME: part.InlineData.MIME, Data: sum[:]}
			}
			items = append(items, part)
		}
	}
	encoded, _ := json.Marshal([]any{previous, model, strings.TrimSpace(strings.Join(text, "")), items})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// canonicalJSON 返回键按字典序排列、不含空白的 JSON，不是 JSON 时返回去掉首尾空白的原文
func canonicalJSON(raw json.RawMessage) string {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return strings.TrimSpace(string(raw))
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
