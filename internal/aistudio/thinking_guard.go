package aistudio

// thinkingGuardInstruction 是思考防拦截开启时追加到系统指令的简洁抽象思考引导，防止模型在思考阶段复述敏感明文触发上游过滤截断
const thinkingGuardInstruction = "Keep internal reasoning brief and abstract using neutral English high-level beats only. Never transcribe, decode, quote, or spell out sensitive, graphic, violent, or explicit terms in internal reasoning."

// thinkingGuardMaxBudget 是只支持思考预算的旧模型开启思考防拦截时的预算上限
const thinkingGuardMaxBudget int64 = 1024

// thinkingGuardEligible 判断本次生成是否启用思考防拦截：仅限支持思考的文本生成模型
func thinkingGuardEligible(request GenerateRequest, entry modelEntry) bool {
	if !request.ThinkingGuard || nonTextGeneration(request, entry) {
		return false
	}
	return entry.defaults.Thinking || entry.defaults.ThinkingLevel || entry.defaults.ThinkingBudget
}

// applyThinkingGuard 追加简洁抽象思考引导并将高于 low 的思考强度上限收敛到 low，不抬高更低设置也不遮蔽非法参数
func applyThinkingGuard(request GenerateRequest, entry modelEntry) GenerateRequest {
	effort := normalizedReasoningEffort(request.Config.ReasoningEffort)
	switch {
	case entry.defaults.ThinkingLevel:
		switch effort {
		case "medium", "high", "xhigh", "extra_high", "max":
			request.Config.ReasoningEffort = "low"
			request.Config.ThinkingBudget = nil
		case "":
			switch {
			case request.Config.ThinkingBudget != nil:
				if *request.Config.ThinkingBudget > thinkingGuardMaxBudget {
					request.Config.ReasoningEffort = "low"
					request.Config.ThinkingBudget = nil
				}
			case entry.defaults.DefaultThinkingLevel != 4 && entry.defaults.DefaultThinkingLevel != 1:
				request.Config.ReasoningEffort = "low"
			}
		}
	case entry.defaults.ThinkingBudget:
		if effort != "none" && (request.Config.ThinkingBudget == nil || *request.Config.ThinkingBudget > thinkingGuardMaxBudget) {
			budget := thinkingGuardMaxBudget
			request.Config.ThinkingBudget = &budget
		}
	}
	instruction := thinkingGuardInstruction
	if len(request.System) > 0 {
		instruction = "\n" + instruction
	}
	request.System = appendSystem(request.System, instruction)
	return request
}
