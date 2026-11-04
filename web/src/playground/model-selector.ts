import { ref, shallowRef } from 'vue'
import { readPreference, writePreference } from '@/preference'
import type { Model } from '@/types'
import type { PageKind } from './media/kinds'

// ModelChoice 是打开模型选择面板的页面：页面类型、已选模型、当前会话是否有内容与在本页面使用所选模型的处理；对话页另提供会话为 agent 选择的模型与设置它的处理
export interface ModelChoice {
  kind: PageKind
  selected: string
  hasContent: () => boolean
  select: (model: Model) => void
  agentModel?: () => string
  setAgentModel?: (id: string) => void
}

// modelChoice 是模型选择面板当前服务的页面
export const modelChoice = shallowRef<ModelChoice>()
// modelSelectorOpen 表示 API 试用页共用的模型选择面板是否打开
export const modelSelectorOpen = ref(false)
// agentModelOpen 表示 agent 所用模型的选择面板是否打开
export const agentModelOpen = ref(false)
// agentModelTarget 是正在选择所用模型的 agent
export const agentModelTarget = shallowRef<Model>()

// openModelSelector 为页面打开模型选择面板
export function openModelSelector(choice: ModelChoice): void {
  modelChoice.value = choice
  modelSelectorOpen.value = true
}

// openAgentModel 关闭模型选择面板，打开 agent 所用模型的选择面板，choice 为发起选择的页面
export function openAgentModel(agent: Model, choice: ModelChoice): void {
  modelChoice.value = choice
  agentModelTarget.value = agent
  modelSelectorOpen.value = false
  agentModelOpen.value = true
}

// agentModelID 返回页面为 agent 选择的模型，未选择时为 agent 默认模型
export function agentModelID(agent: Model, choice: ModelChoice | undefined): string {
  return choice?.agentModel?.() || (agent.agent_model ?? '')
}

// starredKey 是本机收藏模型的存储键
const starredKey = 'playground_starred_models'

// starredModels 是本机收藏的模型
export const starredModels = ref(new Set(readPreference<string[]>(starredKey, [], Array.isArray)))

// toggleStar 收藏或取消收藏模型
export function toggleStar(model: Model): void {
  const next = new Set(starredModels.value)
  if (next.has(model.id)) next.delete(model.id)
  else next.add(model.id)
  starredModels.value = next
  writePreference(starredKey, [...next])
}
