package aistudio

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
)

// replyToolName 是回复工具注入的内部函数名
const replyToolName = "final"

// maxReplyToolContinuationRounds 是模型发出 final(more=true) 后提前结束回合时服务端自动回传 functionResponse 续接的上限
const maxReplyToolContinuationRounds = 8

// replyToolContinuationResponse 是模型以 more=true 提前结束回合时自动回传给上游的 final 工具结果
var replyToolContinuationResponse = json.RawMessage(`{"status":"delivered","instruction":"Continue immediately from where you left off. Emit ALL remaining paragraphs now as consecutive parallel final(content, more) calls in this single turn, setting more=false on the final chunk."}`)

// replyToolInstructionBase 是回复工具开启时追加到系统指令的固定提示（不含其他工具共存句）
const replyToolInstructionBase = "[DELIVERY RULE / 回复交付规则]\n" +
	"You MUST deliver 100% of your user-facing reply through the `final` tool and NEVER output reply text directly outside `final`.\n" +
	"`final` is a one-way streaming output sink and NEVER returns a tool result—there is NO follow-up turn. You MUST emit the ENTIRE complete reply in this single turn before stopping.\n" +
	"Split every multi-paragraph reply into consecutive parallel `final` calls within this SAME turn (~300–600 characters or 1–2 paragraphs per call): set `more: true` on each continuing chunk and immediately emit the next `final` call, and set `more: false` ONLY on the final chunk after the entire requested length and story beat are complete.\n"

// replyToolInstructionWithOtherTools 在客户端声明了其他工具时附加共存规则
const replyToolInstructionWithOtherTools = "When you also need to call other tools in the same turn, call them directly alongside `final` without wrapping them inside `final`.\n"

// replyToolInstructionNoOtherTools 在客户端未声明任何其他工具时附加禁止编造工具规则
const replyToolInstructionNoOtherTools = "No other tools exist—NEVER call or invent any tool other than `final`.\n"

// replyToolInstructionChinese 是中文硬性交付规则，防止长中文角色扮演设定淹没英文规则
const replyToolInstructionChinese = "【回复交付硬性要求】必须完全通过 `final(content=..., more=...)` 交付全部正文，禁止在 `final` 外直接输出正文。`final` 为单向流式输出，不会返回结果也没有后续回合；长回复必须在当前这一回合内连续并行发出全部段落的 `final` 调用（每段约 300–600 字），未写完前设 `more: true` 并紧接着发出下一个 `final`，只有整篇达到要求长度并彻底写完后的最后一段才设 `more: false` 并结束回合。"

// replyToolInstructionChineseNoOtherTools 在无其他工具时附加中文禁止编造工具提示
const replyToolInstructionChineseNoOtherTools = "没有其他工具，严禁调用或编造 `final` 以外的任何函数。"

// replyToolParameters 是 final 函数的参数 JSON Schema（more 供模型显式锚定是否还有后续分段，解包时只读取 content）
var replyToolParameters = json.RawMessage(`{"type":"object","properties":{"content":{"type":"string","description":"One chunk (1-2 natural paragraphs, ~300-600 characters) of the reply."},"more":{"type":"boolean","description":"Set true if more paragraphs will follow in additional parallel final calls in this SAME turn; set false ONLY on the very last final call that finishes the entire reply."}},"required":["content","more"]}`)

type replyToolBypassContextKey struct{}
type replyToolObserverContextKey struct{}

// ReplyToolObserver 接收回复工具本次尝试的实际启用状态、每次成功解包 final 与每次 more=true 自动续接时的回调
type ReplyToolObserver struct {
	OnActive       func(enabled bool)
	OnCall         func()
	OnContinuation func()
}

// ContextWithReplyToolObserver 绑定回复工具启用与调用次数观察器
func ContextWithReplyToolObserver(ctx context.Context, observer ReplyToolObserver) context.Context {
	return context.WithValue(ctx, replyToolObserverContextKey{}, observer)
}

func reportReplyToolActive(ctx context.Context, enabled bool) {
	if observer, ok := ctx.Value(replyToolObserverContextKey{}).(ReplyToolObserver); ok && observer.OnActive != nil {
		observer.OnActive(enabled)
	}
}

func reportReplyToolCall(ctx context.Context) {
	if observer, ok := ctx.Value(replyToolObserverContextKey{}).(ReplyToolObserver); ok && observer.OnCall != nil {
		observer.OnCall()
	}
}

func reportReplyToolContinuation(ctx context.Context) {
	if observer, ok := ctx.Value(replyToolObserverContextKey{}).(ReplyToolObserver); ok && observer.OnContinuation != nil {
		observer.OnContinuation()
	}
}

// ContextWithReplyToolBypassObserver 在开启回复工具但模型直接输出正文时调用 observer
func ContextWithReplyToolBypassObserver(ctx context.Context, observer func()) context.Context {
	return context.WithValue(ctx, replyToolBypassContextKey{}, observer)
}

func reportReplyToolBypassed(ctx context.Context) {
	if observer, ok := ctx.Value(replyToolBypassContextKey{}).(func()); ok && observer != nil {
		observer()
	}
}

// replyToolEligible 判断本次生成是否启用回复工具：仅限 Playground 通道的文本生成，排除结构化输出、强制或禁止工具调用、非文本模型与同名函数
func replyToolEligible(request GenerateRequest, entry modelEntry, build bool, assistant bool) bool {
	if !request.ReplyTool || build || assistant || entry.defaults.InteractionStream || entry.defaults.InteractionTask {
		return false
	}
	if !entry.model.Capabilities["function_declarations"] {
		return false
	}
	if nonTextGeneration(request, entry) {
		return false
	}
	if len(request.Config.ResponseSchema) > 0 || len(request.Config.ResponseJSONSchema) > 0 {
		return false
	}
	if mime := strings.TrimSpace(request.Config.ResponseMIMEType); mime != "" && !strings.EqualFold(mime, "text/plain") {
		return false
	}
	mode := request.Tools.ToolConfig.Mode
	if mode == "required" || mode == "none" || len(request.Tools.ToolConfig.AllowedFunctionNames) > 0 {
		return false
	}
	return !slices.ContainsFunc(request.Tools.Functions, func(declaration FunctionDeclaration) bool {
		return declaration.Name == replyToolName
	})
}

// nonTextGeneration 判断模型或请求配置要求图片、音频、视频、音乐或转录等非文本输出
func nonTextGeneration(request GenerateRequest, entry modelEntry) bool {
	if entry.defaults.ImageRoute ||
		entry.model.Capabilities["image_route"] ||
		entry.model.Capabilities["speech_route"] ||
		entry.model.Capabilities["video_route"] ||
		entry.model.Capabilities["music_route"] ||
		entry.model.Capabilities["transcription_output"] {
		return true
	}
	if request.Config.ImageConfig != nil || request.Config.SpeechConfig != nil || request.Config.VideoConfig != nil || request.Config.TranscriptionConfig != nil {
		return true
	}
	return slices.Contains(request.Config.ResponseModalities, ResponseModalityImage) ||
		slices.Contains(request.Config.ResponseModalities, ResponseModalityAudio)
}

// hasClientOtherTools 判断客户端请求是否声明了除内部回复工具外的其他工具
func hasClientOtherTools(tools Tools) bool {
	return len(tools.Functions) > 0 ||
		len(tools.Google) > 0 ||
		tools.GoogleSearch != nil ||
		tools.URLContext != nil ||
		len(tools.MCPServers) > 0
}

// isReplyToolName 判断上游返回的函数名是否指向内部回复工具（兼容 Gemini 偶发附带的 default_api: 前缀）
func isReplyToolName(name string) bool {
	return name == replyToolName || name == "default_api:"+replyToolName
}

// applyReplyTool 返回追加了 final 函数声明与固定系统指令的请求副本
func applyReplyTool(request GenerateRequest) GenerateRequest {
	hasOther := hasClientOtherTools(request.Tools)
	request.Tools.Functions = append(slices.Clip(request.Tools.Functions), FunctionDeclaration{
		Name:        replyToolName,
		Description: "Deliver one chunk of your reply to the user (one-way output; never returns a tool result). Emit ALL chunks as consecutive parallel calls in this single turn: set more=true on every chunk except the last, and more=false ONLY when the entire reply is finished.",
		Parameters:  replyToolParameters,
	})
	var instruction string
	if hasOther {
		instruction = replyToolInstructionBase + replyToolInstructionWithOtherTools + replyToolInstructionChinese
	} else {
		instruction = replyToolInstructionBase + replyToolInstructionNoOtherTools + replyToolInstructionChinese + replyToolInstructionChineseNoOtherTools
	}
	if len(request.System) > 0 {
		instruction = "\n\n" + instruction
	}
	request.System = appendSystem(request.System, instruction)
	return request
}

// replyToolCallArgs 从 final 函数调用参数中提取正文与 more 续段标志
func replyToolCallArgs(arguments json.RawMessage) (string, bool, bool) {
	var payload struct {
		Content *string `json:"content"`
		More    bool    `json:"more"`
	}
	if err := json.Unmarshal(arguments, &payload); err != nil || payload.Content == nil {
		return "", false, false
	}
	return *payload.Content, payload.More, true
}

// replyToolText 从 final 函数调用参数中提取正文内容
func replyToolText(arguments json.RawMessage) (string, bool) {
	content, _, ok := replyToolCallArgs(arguments)
	return content, ok
}

// replyToolContinuationResults 判断本轮上游输出是否仅包含 final 调用且最后一段声明 more=true，是则返回自动续接所需的 FunctionResult 列表
func replyToolContinuationResults(parts []Part) ([]Part, bool) {
	var results []Part
	hasNonEmpty := false
	lastMore := false
	for _, part := range parts {
		if part.Thought {
			continue
		}
		if part.Text != "" {
			return nil, false
		}
		if part.FunctionCall == nil {
			continue
		}
		if !isReplyToolName(part.FunctionCall.Name) {
			return nil, false
		}
		content, more, ok := replyToolCallArgs(part.FunctionCall.Arguments)
		if !ok {
			return nil, false
		}
		if content != "" {
			hasNonEmpty = true
		}
		lastMore = more
		results = append(results, Part{
			FunctionResult: &FunctionResult{
				ID:      part.FunctionCall.ID,
				Name:    part.FunctionCall.Name,
				Content: replyToolContinuationResponse,
			},
		})
	}
	if len(results) == 0 || !hasNonEmpty || !lastMore {
		return nil, false
	}
	return results, true
}

// calledReplyTool 判断本轮上游输出是否包含 final 函数调用
func calledReplyTool(output Output) bool {
	return slices.ContainsFunc(output.Parts, func(part Part) bool {
		return part.FunctionCall != nil && isReplyToolName(part.FunctionCall.Name)
	})
}
