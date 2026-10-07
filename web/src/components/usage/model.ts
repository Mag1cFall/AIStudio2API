import { onUnmounted, reactive, watch } from 'vue'
import type { UsageDimension, UsageFilters, UsageStats } from '@/types'

// relativePresets 与 calendarPresets 是相对时间与日历两类快捷范围
export const relativePresets = ['1h', '6h', '24h', '7d', '30d', '90d'] as const
export const calendarPresets = ['today', 'yesterday', 'week', 'month', 'lastMonth'] as const
export type RangePreset =
  (typeof relativePresets)[number] | (typeof calendarPresets)[number] | 'custom'

export const dimensions: UsageDimension[] = ['model', 'account', 'channel', 'protocol', 'state']

// maxSpan 与服务端校验一致
export const maxSpan = 93 * 86_400_000
export const retention = 90 * 86_400_000

const relativeSpans: Record<(typeof relativePresets)[number], number> = {
  '1h': 3_600_000,
  '6h': 6 * 3_600_000,
  '24h': 86_400_000,
  '7d': 7 * 86_400_000,
  '30d': 30 * 86_400_000,
  '90d': 90 * 86_400_000,
}

// UsageViewState 是用量页写入地址栏的视图状态
export interface UsageViewState {
  range: RangePreset
  from: string
  to: string
  stack: UsageDimension
  filters: UsageFilters
  status: string
  search: string
}

const defaults: UsageViewState = {
  range: '24h',
  from: '',
  to: '',
  stack: 'model',
  filters: {},
  status: '',
  search: '',
}

// readState 从地址栏读取视图状态，非法取值使用默认值
function readState(): UsageViewState {
  const query = new URLSearchParams(window.location.search)
  const range = query.get('range') as RangePreset | null
  const presets: readonly string[] = [...relativePresets, ...calendarPresets, 'custom']
  const stack = query.get('stack') as UsageDimension | null
  const filters: UsageFilters = {}
  for (const dimension of dimensions) {
    const values = (query.get(dimension) ?? '').split(',').filter((value) => value !== '')
    if (values.length > 0) filters[dimension] = values
  }
  return {
    range: range !== null && presets.includes(range) ? range : defaults.range,
    from: query.get('from') ?? '',
    to: query.get('to') ?? '',
    stack: stack !== null && dimensions.includes(stack) ? stack : defaults.stack,
    filters,
    status: /^\d{3}$/.test(query.get('status') ?? '') ? (query.get('status') ?? '') : '',
    search: query.get('q') ?? '',
  }
}

// writeState 用 replaceState 写入非默认的视图状态，保留 tab 等其他参数
function writeState(state: UsageViewState): void {
  const url = new URL(window.location.href)
  const set = (name: string, value: string, fallback = ''): void => {
    if (value === fallback) url.searchParams.delete(name)
    else url.searchParams.set(name, value)
  }
  set('range', state.range, defaults.range)
  set('from', state.range === 'custom' ? state.from : '')
  set('to', state.range === 'custom' ? state.to : '')
  set('stack', state.stack, defaults.stack)
  for (const dimension of dimensions) set(dimension, (state.filters[dimension] ?? []).join(','))
  set('status', state.status)
  set('q', state.search)
  if (url.href !== window.location.href) window.history.replaceState(null, '', url)
}

// useUsageState 返回与地址栏双向同步的视图状态
export function useUsageState(): UsageViewState {
  const state = reactive(readState())
  watch(
    () => JSON.stringify(state),
    () => writeState(state),
  )
  const sync = (): void => {
    Object.assign(state, readState())
  }
  window.addEventListener('popstate', sync)
  onUnmounted(() => window.removeEventListener('popstate', sync))
  return state
}

// localInput 把时间格式化为 datetime-local 输入框的本地时间文本
export function localInput(date: Date): string {
  const pad = (value: number): string => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

// resolveRange 把视图状态解析为具体的起止时间，自定义范围无效时返回 null
export function resolveRange(
  state: Pick<UsageViewState, 'range' | 'from' | 'to'>,
  now: Date,
): { from: Date; to: Date } | null {
  const midnight = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  switch (state.range) {
    case 'today':
      return { from: midnight, to: now }
    case 'yesterday':
      return {
        from: new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1),
        to: midnight,
      }
    case 'week': {
      const weekday = (now.getDay() + 6) % 7
      return {
        from: new Date(now.getFullYear(), now.getMonth(), now.getDate() - weekday),
        to: now,
      }
    }
    case 'month':
      return { from: new Date(now.getFullYear(), now.getMonth(), 1), to: now }
    case 'lastMonth':
      return {
        from: new Date(now.getFullYear(), now.getMonth() - 1, 1),
        to: new Date(now.getFullYear(), now.getMonth(), 1),
      }
    case 'custom': {
      const from = new Date(state.from)
      const to = new Date(state.to)
      return validRange(from, to) === '' ? { from, to } : null
    }
    default:
      return { from: new Date(now.getTime() - relativeSpans[state.range]), to: now }
  }
}

// validRange 校验自定义范围，返回错误的翻译键或空字符串
export function validRange(from: Date, to: Date): '' | 'usage.rangeInvalid' | 'usage.rangeTooLong' {
  if (Number.isNaN(from.getTime()) || Number.isNaN(to.getTime()) || from >= to)
    return 'usage.rangeInvalid'
  if (to.getTime() - from.getTime() > maxSpan) return 'usage.rangeTooLong'
  return ''
}

// liveRange 判断范围终点是否跟随当前时间，用于决定是否自动刷新
export function liveRange(range: RangePreset): boolean {
  return range !== 'yesterday' && range !== 'lastMonth' && range !== 'custom'
}

// successRate 返回排除客户端取消后的成功比例
export function successRate(stats: UsageStats): number | null {
  const counted = stats.requests - stats.canceled
  return counted > 0 ? stats.succeeded / counted : null
}

// palette 是分类序列使用的颜色，按取值哈希分配使同一取值在各图中颜色一致
export const palette = [
  '#60a5fa',
  '#34d399',
  '#f59e0b',
  '#a78bfa',
  '#f472b6',
  '#22d3ee',
  '#fb923c',
  '#a3e635',
  '#e879f9',
  '#2dd4bf',
]
export const otherColor = '#6b7280'

// assignColors 为一组取值分配颜色，哈希冲突时顺延到未使用的颜色
export function assignColors(keys: string[]): Map<string, string> {
  const result = new Map<string, string>()
  const used = new Set<number>()
  for (const key of keys) {
    let hash = 0
    for (const char of key) hash = (hash * 31 + char.charCodeAt(0)) >>> 0
    let index = hash % palette.length
    for (let probe = 0; probe < palette.length && used.has(index); probe++)
      index = (index + 1) % palette.length
    used.add(index)
    result.set(key, palette[index] ?? otherColor)
  }
  return result
}

// protocolLabels 是公开协议的显示名
export const protocolLabels: Record<string, string> = {
  'openai-chat': 'OpenAI Chat',
  'openai-responses': 'OpenAI Responses',
  anthropic: 'Anthropic',
  gemini: 'Gemini',
  interactions: 'Interactions',
  images: 'Images',
  audio: 'Audio',
  videos: 'Videos',
  files: 'Files',
  other: 'Other',
}

// latencyTone 按耗时阈值返回色调，首个事件与总耗时使用不同阈值
export function latencyTone(milliseconds: number, kind: 'duration' | 'first'): string {
  const [warn, slow] = kind === 'first' ? [10_000, 30_000] : [60_000, 180_000]
  if (milliseconds >= slow) return 'bg-red-500'
  if (milliseconds >= warn) return 'bg-yellow-500'
  return 'bg-green-500'
}

// rateTone 按成功率阈值返回文字色调
export function rateTone(rate: number | null): string {
  if (rate === null) return 'text-gray-500'
  if (rate >= 0.95) return 'text-green-400'
  if (rate >= 0.8) return 'text-yellow-400'
  return 'text-red-400'
}

// readPreference 读取浏览器保存的界面偏好，存储不可用时返回默认值
export function readPreference<T>(key: string, fallback: T, valid: (value: unknown) => boolean): T {
  try {
    const raw = window.localStorage.getItem(key)
    if (raw === null) return fallback
    const value: unknown = JSON.parse(raw)
    return valid(value) ? (value as T) : fallback
  } catch {
    return fallback
  }
}

// writePreference 保存界面偏好，存储不可用时忽略
export function writePreference(key: string, value: unknown): void {
  try {
    window.localStorage.setItem(key, JSON.stringify(value))
  } catch {
    return
  }
}
