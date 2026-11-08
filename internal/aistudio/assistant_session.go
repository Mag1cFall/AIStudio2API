package aistudio

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// assistantAppletKind 是写代码助手应用在账户运行状态中的资源类型
	assistantAppletKind = "assistant-applet"
	// assistantAppletIdle 是写代码助手应用闲置后被巡检删除的时长
	assistantAppletIdle = time.Hour
	// assistantAppletDeleteAttempts 是应用服务以 HTTP 500 拒绝删除写代码助手应用时的最多尝试次数
	assistantAppletDeleteAttempts = 10
)

// assistantLink 是对话中一组连续的模型轮或其余轮次结束处的累积摘要
type assistantLink struct {
	end    int
	model  bool
	digest string
}

// assistantSessionKey 是请求所用 API key 与到应用中最后一轮模型回复为止的对话摘要
type assistantSessionKey struct {
	apiKey string
	digest string
}

// assistantSession 是一段对话所在的写代码助手应用
type assistantSession struct {
	accountID string
	applet    string
	// key 是可继续时在 tips 中的键
	key assistantSessionKey
	// historyGroupCount 是新建应用时转录在自定义指令中的开头分组数
	historyGroupCount int
	busy              bool
	usedAt            time.Time
}

// assistantSessions 是本进程创建的写代码助手应用与其中可继续的对话
type assistantSessions struct {
	mu      sync.Mutex
	tips    map[assistantSessionKey]*assistantSession
	applets map[string]*assistantSession
}

func newAssistantSessions() *assistantSessions {
	return &assistantSessions{tips: make(map[assistantSessionKey]*assistantSession), applets: make(map[string]*assistantSession)}
}

// assistantLinks 把对话按连续的模型轮与其余轮次分组，返回每组结束处的累积摘要
func assistantLinks(contents []Content) []assistantLink {
	var links []assistantLink
	digest := ""
	for start := 0; start < len(contents); {
		model := contents[start].Role == RoleAssistant
		end := start
		var parts []Part
		for ; end < len(contents) && (contents[end].Role == RoleAssistant) == model; end++ {
			parts = append(parts, contents[end].Parts...)
		}
		digest = assistantLinkDigest(digest, model, parts)
		links = append(links, assistantLink{end: end, model: model, digest: digest})
		start = end
	}
	return links
}

// assistantLinkDigest 返回在 previous 之后接上一组轮次的摘要：正文 Part 直接连接后去掉首尾空白，函数调用计入名称与规范参数，函数结果计入规范内容，其余 Part 去掉签名、内联数据以其 SHA-256 计入；思考与调用 ID 不计入，客户端回传的模型轮与服务输出的同一回复得到同一摘要
func assistantLinkDigest(previous string, model bool, parts []Part) string {
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

// findLocked 在 links 最后一组之前最近的模型轮是本进程保存过的回复时，返回所在会话、键与之后新轮次的开头
func (store *assistantSessions) findLocked(apiKey string, links []assistantLink) (*assistantSession, assistantSessionKey, int) {
	index := len(links) - 2
	if index >= 0 && !links[index].model {
		index--
	}
	if index < 0 {
		return nil, assistantSessionKey{}, 0
	}
	key := assistantSessionKey{apiKey: apiKey, digest: links[index].digest}
	if session := store.tips[key]; session != nil {
		return session, key, links[index].end
	}
	return nil, assistantSessionKey{}, 0
}

// account 返回对话可继续的应用所属账户，没有时返回空
func (store *assistantSessions) account(apiKey string, links []assistantLink) string {
	store.mu.Lock()
	defer store.mu.Unlock()
	if session, _, _ := store.findLocked(apiKey, links); session != nil {
		return session.accountID
	}
	return ""
}

// claim 取出对话可继续的会话并标为进行中，返回会话与新轮次的开头；会话属于其他账户时放弃该会话
func (store *assistantSessions) claim(apiKey string, accountID string, links []assistantLink) (*assistantSession, int) {
	store.mu.Lock()
	defer store.mu.Unlock()
	session, key, start := store.findLocked(apiKey, links)
	if session == nil {
		return nil, 0
	}
	delete(store.tips, key)
	if session.accountID != accountID {
		delete(store.applets, session.applet)
		return nil, 0
	}
	session.busy = true
	return session, start
}

// open 记录新建的应用与转录在自定义指令中的开头分组数，会话为进行中
func (store *assistantSessions) open(accountID string, applet string, historyGroupCount int) *assistantSession {
	store.mu.Lock()
	defer store.mu.Unlock()
	session := &assistantSession{accountID: accountID, applet: applet, historyGroupCount: historyGroupCount, busy: true}
	store.applets[applet] = session
	return session
}

// finish 结束会话上的请求：key 不为空时保存为可继续，否则放弃会话，应用留待巡检删除
func (store *assistantSessions) finish(session *assistantSession, key *assistantSessionKey) {
	store.mu.Lock()
	defer store.mu.Unlock()
	session.busy = false
	if key == nil {
		delete(store.applets, session.applet)
		return
	}
	session.key = *key
	session.usedAt = time.Now()
	store.tips[*key] = session
}

// expire 放弃闲置超过 assistantAppletIdle 的会话，返回 applets 中可以删除的应用：本进程没有会话或会话已闲置
func (store *assistantSessions) expire(applets []string, now time.Time) []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	var idle []string
	for _, applet := range applets {
		session := store.applets[applet]
		if session != nil && (session.busy || now.Sub(session.usedAt) < assistantAppletIdle) {
			continue
		}
		if session != nil {
			store.dropLocked(session)
		}
		idle = append(idle, applet)
	}
	return idle
}

// forget 放弃应用上的会话
func (store *assistantSessions) forget(applet string) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if session := store.applets[applet]; session != nil {
		store.dropLocked(session)
	}
}

// dropLocked 移除会话的应用记录与仍指向该会话的可继续位置
func (store *assistantSessions) dropLocked(session *assistantSession) {
	if store.tips[session.key] == session {
		delete(store.tips, session.key)
	}
	delete(store.applets, session.applet)
}

// AssistantService 是可以查询写代码助手对话所在账户的生成服务
type AssistantService interface {
	AssistantAccount(context.Context, GenerateRequest) string
}

// AssistantAccount 返回 -build 请求的历史所在应用的账户，没有可继续的对话时返回空
func (s *PooledService) AssistantAccount(ctx context.Context, request GenerateRequest) string {
	apiKey, _ := ctx.Value(apiKeyContextKey{}).(string)
	_, contents := s.generateTurns(request)
	return s.client.assistants.account(apiKey, assistantLinks(contents))
}

// ReclaimIdleAssistantApplets 删除各账户中闲置超过 assistantAppletIdle 或本进程没有对应对话的写代码助手应用，没有空闲槽位的账户跳过本轮
func (s *PooledService) ReclaimIdleAssistantApplets(ctx context.Context) error {
	var reclaimErr error
	for accountID, applets := range s.pool.resourcesOfKind(assistantAppletKind) {
		idle := s.client.assistants.expire(applets, time.Now())
		if len(idle) == 0 {
			continue
		}
		lease, _, _ := s.pool.TryAcquireFor(ctx, AccountSelection{AccountID: accountID})
		if lease == nil {
			continue
		}
		leaseCtx := ContextWithAccountLease(ctx, lease)
		for _, applet := range idle {
			reclaimErr = errors.Join(reclaimErr, s.deleteAssistantApplet(leaseCtx, accountID, applet))
		}
		reclaimErr = errors.Join(reclaimErr, lease.Release())
	}
	return reclaimErr
}

// DeleteAccountAssistantApplets 等账户上进行中的请求结束后独占账户，删除账户记录的全部写代码助手应用并解除绑定
func (s *PooledService) DeleteAccountAssistantApplets(ctx context.Context, accountID string) error {
	if len(s.pool.resourcesOfKind(assistantAppletKind)[accountID]) == 0 {
		return nil
	}
	lease, err := s.pool.AcquireAccount(ctx, accountID)
	if err != nil {
		return err
	}
	leaseCtx := ContextWithAccountLease(ctx, lease)
	var deleteErr error
	for _, applet := range s.pool.resourcesOfKind(assistantAppletKind)[accountID] {
		deleteErr = errors.Join(deleteErr, s.deleteAssistantApplet(leaseCtx, accountID, applet))
	}
	return errors.Join(deleteErr, lease.Release())
}

// deleteAssistantApplet 删除写代码助手应用，放弃其上的会话并解除账户绑定
func (s *PooledService) deleteAssistantApplet(ctx context.Context, accountID string, applet string) error {
	if err := s.client.deleteAssistantApplet(ctx, accountID, applet); err != nil {
		return err
	}
	s.client.assistants.forget(applet)
	return s.pool.UnbindResource(applet)
}

// deleteAssistantApplet 经应用服务删除写代码助手应用，应用已不存在时同样返回 nil；生成刚结束的几秒内应用服务以 HTTP 500 拒绝删除，期间每秒重试
func (c *Client) deleteAssistantApplet(ctx context.Context, accountID string, applet string) error {
	body, _ := json.Marshal([]any{applet})
	for attempt := 1; ; attempt++ {
		rpc := newRPCRequest("DeleteApplet", accountID, "", body, false)
		rpc.URL = appletServiceRPCBase + rpc.Method
		response, err := c.send(ctx, rpc)
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			err = errors.Join(err, response.Body.Close())
		}
		var rpcErr *RPCError
		if err == nil || errors.As(err, &rpcErr) && rpcErr.StatusCode == http.StatusNotFound {
			return nil
		}
		if rpcErr == nil || rpcErr.StatusCode != http.StatusInternalServerError || attempt == assistantAppletDeleteAttempts {
			return fmt.Errorf("删除写代码助手应用 %s: %w", applet, err)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("删除写代码助手应用 %s: %w", applet, errors.Join(err, ctx.Err()))
		case <-timer.C:
		}
	}
}
