import type { TranslationKey } from '@/i18n'
import type { Model, ModelPrice, ThinkingLevel } from '@/types'

// hasCode 返回模型是否带某个主能力码
export function hasCode(model: Model | undefined, code: number): boolean {
  return model?.capabilities?.[`capability_code_${code}`] === true
}

// hasCategory 返回模型是否属于某个官网分类（次能力码）
export function hasCategory(model: Model, code: number): boolean {
  return model.capabilities?.[`secondary_capability_code_${code}`] === true
}

// isOmniModel 按官网规则判断 Omni 模型：属于分类 14 且带主能力码 1
export function isOmniModel(model: Model | undefined): boolean {
  return model !== undefined && hasCategory(model, 14) && hasCode(model, 1)
}

// ModelCategory 是模型选择面板的分类：Starred 为本机收藏，All 为全部，数字为官网分类码
export type ModelCategory = 'Starred' | 'All' | number

// categoryLabels 是官网分类码的名称
export const categoryLabels: Record<number, TranslationKey> = {
  10: 'pg.categoryFeatured',
  5: 'pg.categoryConfidential',
  17: 'pg.categoryGemini',
  18: 'pg.categoryLive',
  15: 'pg.categoryImages',
  14: 'pg.categoryVideo',
  13: 'pg.categoryAudio',
  16: 'pg.categoryMusic',
  19: 'pg.categoryAgents',
  3: 'pg.categoryGemma',
}

// categoryOrder 是官网分类按钮的顺序
export const categoryOrder: ModelCategory[] = [
  'Starred',
  'All',
  10,
  5,
  17,
  18,
  15,
  14,
  13,
  16,
  19,
  3,
]

// Tool 是运行设置中的工具开关
export type Tool =
  | 'structured_output'
  | 'code_execution'
  | 'function_declarations'
  | 'google_search'
  | 'image_search'
  | 'google_maps'
  | 'url_context'

// toolCodes 是各工具开关对应的模型能力码
export const toolCodes: Record<Tool, number> = {
  structured_output: 6,
  code_execution: 9,
  function_declarations: 10,
  google_search: 12,
  image_search: 54,
  url_context: 13,
  google_maps: 58,
}

// toolCompatible 按官网规则判断工具能否在当前已开启工具旁开启：本身已开启，或已开启的每个工具都与它兼容；函数调用与结构化输出恒兼容
export function toolCompatible(
  model: Model | undefined,
  tool: Tool,
  enabled: readonly Tool[],
): boolean {
  if (enabled.includes(tool)) return true
  const pairs = model?.tool_compatibility ?? []
  const compatible = new Set<string>()
  for (const [first, second] of pairs) {
    if (first === tool) compatible.add(second)
    if (second === tool) compatible.add(first)
  }
  if (tool === 'function_declarations') compatible.add('structured_output')
  if (tool === 'structured_output') compatible.add('function_declarations')
  return enabled.every((item) => compatible.has(item))
}

// thinkingLevelLabels 是思考档位的显示名
export const thinkingLevelLabels: Record<ThinkingLevel, TranslationKey> = {
  MINIMAL: 'pg.minimal',
  LOW: 'pg.low',
  MEDIUM: 'pg.medium',
  HIGH: 'pg.high',
}

// thinkingLevelTooltips 是官网思考档位选项的说明
export const thinkingLevelTooltips: Partial<Record<ThinkingLevel, TranslationKey>> = {
  LOW: 'pg.thinkingLowTip',
  MEDIUM: 'pg.thinkingMediumTip',
  HIGH: 'pg.thinkingHighTip',
}

// isChatModel 返回模型能否在 Playground 对话中使用，图片模型以图文输出参与对话
export function isChatModel(model: Model): boolean {
  const capabilities = model.capabilities ?? {}
  return (
    model.methods.includes('generateContent') &&
    capabilities.speech_route !== true &&
    capabilities.music_route !== true &&
    capabilities.video_route !== true &&
    capabilities.live_route !== true &&
    (capabilities.interactions_api !== true || isOmniModel(model))
  )
}

// defaultChatModel 返回新对话的默认模型：Featured 中第一个免费对话模型，没有时为第一个对话模型
export function defaultChatModel(models: readonly Model[]): Model | undefined {
  const chat = models.filter(isChatModel)
  return chat.find((model) => hasCategory(model, 10) && model.paid !== true) ?? chat[0]
}

// defaultImageModel 返回图片页的默认模型：目录中第一个可在对话页使用的 Images 分类模型
export function defaultImageModel(models: readonly Model[]): Model | undefined {
  return models.find((model) => isChatModel(model) && hasCategory(model, 15))
}

// modelIcon 按官网规则返回模型行图标名
export function modelIcon(
  model: Model,
):
  | 'modelGemma'
  | 'modelLive'
  | 'modelImage'
  | 'modelVideo'
  | 'modelMusic'
  | 'modelAudio'
  | 'spark' {
  if (hasCategory(model, 3)) return 'modelGemma'
  if (hasCode(model, 26)) return 'modelLive'
  if (hasCategory(model, 15)) return 'modelImage'
  if (hasCategory(model, 14)) return 'modelVideo'
  if (hasCategory(model, 16)) return 'modelMusic'
  if (hasCategory(model, 13)) return 'modelAudio'
  return 'spark'
}

// PriceLine 是模型卡片中的一行价格：tooltip 为分档价格的计价说明，link 为整句说明后的价格页链接
export interface PriceLine {
  text: string
  tooltip?: string
  link?: string
}

const priceNumber = new Intl.NumberFormat('en-US', {
  minimumFractionDigits: 2,
  maximumFractionDigits: 4,
})
const tokenNumber = new Intl.NumberFormat('en-US', { maximumFractionDigits: 2 })

// compactTokens 按官网规则把 token 数写为 K、M、B 缩写
function compactTokens(value: number): string {
  if (value < 1e3) return tokenNumber.format(value)
  if (value < 1e6) return `${tokenNumber.format(value / 1e3)}K`
  if (value < 1e9) return `${tokenNumber.format(value / 1e6)}M`
  return `${tokenNumber.format(value / 1e9)}B`
}

// priceLabel 按官网规则返回分档名称：上游名称、全部上下文、思考档或输入 token 区间
function priceLabel(
  pricing: ModelPrice[],
  index: number,
  t: (key: TranslationKey) => string,
): string {
  const row = pricing[index]
  if (row?.label) return row.label
  if (pricing.length === 1) return t('pg.allContextLengths')
  if (pricing.some((item) => item.thinking))
    return row?.thinking ? t('pg.priceThinking') : t('pg.priceNonThinking')
  const tokens = (value: number | undefined) =>
    t('pg.tokens').replace('{count}', compactTokens(value ?? 0))
  if (index === 0) return `<=${tokens(row?.up_to)}`
  const previous = tokens(pricing[index - 1]?.up_to)
  return index === pricing.length - 1 ? `> ${previous}` : `> ${previous} <= ${tokens(row?.up_to)}`
}

// priceLines 按官网规则把模型价格整理为卡片中的价格说明
export function priceLines(model: Model, t: (key: TranslationKey) => string): PriceLine[] {
  const details = model.details
  const pricing = details?.pricing ?? []
  return pricing.map((row, index) => {
    if (row.text)
      return { text: row.text, ...(details?.pricing_url ? { link: details.pricing_url } : {}) }
    const parts = [priceLabel(pricing, index, t)]
    if (row.input !== undefined) {
      parts.push(
        '•',
        `${t('pg.priceInput')}: $${priceNumber.format(row.input)}${row.input_per_request ? '/req' : ''}`,
      )
    }
    if (row.output !== undefined) {
      parts.push(
        row.input === undefined ? '•' : '/',
        `${t('pg.priceOutput')}: $${priceNumber.format(row.output)}${row.output_per_request ? '/req' : ''}`,
      )
    }
    const perRequest = row.input_per_request || row.output_per_request
    return {
      text: parts.join(' '),
      tooltip:
        details?.pricing_note || (perRequest ? t('pg.pricingPerRequest') : t('pg.pricingPerToken')),
    }
  })
}

// modelBadges 按官网规则返回模型标记：Confidential、New（Featured 发布 60 天内，其他 10 天内）与 Paid
export function modelBadges(
  model: Model,
  category: ModelCategory,
  now = Date.now(),
): ('Confidential' | 'New' | 'Paid')[] {
  const badges: ('Confidential' | 'New' | 'Paid')[] = []
  if (hasCategory(model, 5) && category !== 5) badges.push('Confidential')
  const released = model.details?.release_date
  if (released !== undefined) {
    const days = (now - released * 1000) / 86_400_000
    if (days <= (hasCategory(model, 10) ? 60 : 10)) badges.push('New')
  }
  if (model.paid) badges.push('Paid')
  return badges
}
