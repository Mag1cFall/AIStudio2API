import { ref, shallowRef } from 'vue'
import type { Model } from '@/types'
import type { PageKind } from './media/kinds'

// ModelChoice 是打开模型选择面板的页面：页面类型、已选模型、当前会话是否有内容与在本页面使用所选模型的处理
export interface ModelChoice {
  kind: PageKind
  selected: string
  hasContent: () => boolean
  select: (model: Model) => void
}

// modelChoice 是模型选择面板当前服务的页面
export const modelChoice = shallowRef<ModelChoice>()
// modelSelectorOpen 表示 API 试用页共用的模型选择面板是否打开
export const modelSelectorOpen = ref(false)

// openModelSelector 为页面打开模型选择面板
export function openModelSelector(choice: ModelChoice): void {
  modelChoice.value = choice
  modelSelectorOpen.value = true
}
