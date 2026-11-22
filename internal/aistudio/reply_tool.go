package aistudio

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
)

// replyToolName 是回复工具注入的内部函数名
const replyToolName = "final"

// replyToolInstruction 是回复工具开启时追加到系统指令的固定提示
const replyToolInstruction = "Deliver every reply to the user by calling the `final` function with the complete response text in its `content` argument. Do not output the reply as plain text outside `final`. When you also need to call other tools in the same turn, call them directly alongside `final` without wrapping them inside `final`."

// replyToolParameters 是 final 函数的参数 JSON Schema
var replyToolParameters = json.RawMessage(`{"type":"object","properties":{"content":{"type":"string","description":"The complete reply to the user."}},"required":["content"]}`)

type replyToolBypassContextKey struct{}

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

// applyReplyTool 返回追加了 final 函数声明与固定系统指令的请求副本
func applyReplyTool(request GenerateRequest) GenerateRequest {
	request.Tools.Functions = append(slices.Clip(request.Tools.Functions), FunctionDeclaration{
		Name:        replyToolName,
		Description: "Deliver the complete reply to the user.",
		Parameters:  replyToolParameters,
	})
	instruction := replyToolInstruction
	if len(request.System) > 0 {
		instruction = "\n" + instruction
	}
	request.System = appendSystem(request.System, instruction)
	return request
}

// replyToolText 从 final 函数调用参数中提取正文内容
func replyToolText(arguments json.RawMessage) (string, bool) {
	var payload struct {
		Content *string `json:"content"`
	}
	if err := json.Unmarshal(arguments, &payload); err != nil || payload.Content == nil {
		return "", false
	}
	return *payload.Content, true
}

// calledReplyTool 判断本轮上游输出是否包含 final 函数调用
func calledReplyTool(output Output) bool {
	return slices.ContainsFunc(output.Parts, func(part Part) bool {
		return part.FunctionCall != nil && part.FunctionCall.Name == replyToolName
	})
}
