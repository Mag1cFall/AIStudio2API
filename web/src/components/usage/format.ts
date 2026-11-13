import { computed, type InjectionKey, type Ref } from 'vue'
import { channelLabelKey, useI18n, type TranslationKey } from '@/i18n'
import type { UpstreamChannel, UsageDimension } from '@/types'
import { protocolLabels } from './model'

const stateKeys: Record<string, TranslationKey> = {
  completed: 'usage.state.completed',
  tool_calls: 'usage.state.tool_calls',
  limited: 'usage.state.limited',
  blocked: 'usage.state.blocked',
  failed: 'usage.state.failed',
  cancelled: 'usage.state.cancelled',
}

// Formats 是一种界面语言下用量页的全部数字与时间格式器
interface Formats {
  compact: Intl.NumberFormat
  integer: Intl.NumberFormat
  usd: Intl.NumberFormat
  percent: Record<0 | 1, Intl.NumberFormat>
  millisecond: Intl.NumberFormat
  second: Intl.NumberFormat
  minutes: Intl.DateTimeFormat
  seconds: Intl.DateTimeFormat
  clock: Intl.DateTimeFormat
  day: Intl.DateTimeFormat
  minute: Intl.DateTimeFormat
}

// usageClock 是用量页每秒更新的当前时间，只由显示相对时间的组件读取
export const usageClock: InjectionKey<Readonly<Ref<number>>> = Symbol('usageClock')

// cache 按界面语言保存格式器，用量页各组件共用
const cache = new Map<string, Formats>()

// formatsFor 返回界面语言的格式器，每种语言只创建一次
function formatsFor(locale: string): Formats {
  const cached = cache.get(locale)
  if (cached !== undefined) return cached
  const dateTime: Intl.DateTimeFormatOptions = {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }
  const formats: Formats = {
    compact: new Intl.NumberFormat(locale, { notation: 'compact', maximumFractionDigits: 1 }),
    integer: new Intl.NumberFormat(locale),
    usd: new Intl.NumberFormat(locale, { minimumFractionDigits: 2, maximumFractionDigits: 2 }),
    percent: {
      0: new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }),
      1: new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }),
    },
    millisecond: new Intl.NumberFormat(locale, { style: 'unit', unit: 'millisecond' }),
    second: new Intl.NumberFormat(locale, {
      style: 'unit',
      unit: 'second',
      maximumFractionDigits: 1,
    }),
    minutes: new Intl.DateTimeFormat(locale, dateTime),
    seconds: new Intl.DateTimeFormat(locale, { ...dateTime, second: '2-digit' }),
    clock: new Intl.DateTimeFormat(locale, {
      hour: 'numeric',
      minute: 'numeric',
      second: 'numeric',
      hour12: false,
    }),
    day: new Intl.DateTimeFormat(locale, { month: '2-digit', day: '2-digit' }),
    minute: new Intl.DateTimeFormat(locale, { hour: '2-digit', minute: '2-digit', hour12: false }),
  }
  cache.set(locale, formats)
  return formats
}

// useUsageFormat 提供随界面语言变化的数字、耗时、时间与维度取值格式
export function useUsageFormat() {
  const { t, locale } = useI18n()
  const formats = computed(() => formatsFor(locale.value))

  // compact 以紧凑格式显示数量
  const compact = (value: number): string => formats.value.compact.format(value)

  // integer 以千分位显示整数
  const integer = (value: number): string => formats.value.integer.format(value)

  // usd 以千分位与两位小数显示美元金额，不足 1 美分的正数显示为 < $0.01
  const usd = (value: number): string =>
    value > 0 && value < 0.01 ? '< $0.01' : `$${formats.value.usd.format(value)}`

  // percent 显示比例，空值显示破折号，digits 为 0 或 1 位小数
  const percent = (value: number | null, digits: 0 | 1 = 1): string =>
    value === null ? '—' : `${formats.value.percent[digits].format(value * 100)}%`

  // duration 按界面语言把毫秒显示为毫秒或秒，一分钟以上为整数秒
  const duration = (milliseconds: number): string => {
    if (milliseconds <= 0) return '—'
    if (milliseconds < 1000) return formats.value.millisecond.format(Math.round(milliseconds))
    return formats.value.second.format(
      milliseconds < 60_000 ? milliseconds / 1000 : Math.round(milliseconds / 1000),
    )
  }

  // dateTime 显示带月日的本地时间，seconds 为真时显示秒
  const dateTime = (value: string | Date, seconds = false): string =>
    formats.value[seconds ? 'seconds' : 'minutes'].format(new Date(value))

  // clock 显示本地时分秒
  const clock = (value: string | Date): string => formats.value.clock.format(new Date(value))

  // ago 显示距今的相对时间
  const ago = (value: string, now: number): string => {
    const seconds = Math.max(0, Math.round((now - new Date(value).getTime()) / 1000))
    if (seconds < 10) return t('usage.justNow')
    if (seconds < 60) return t('usage.secondsAgo').replace('{n}', String(seconds))
    if (seconds < 3600)
      return t('usage.minutesAgo').replace('{n}', String(Math.floor(seconds / 60)))
    if (seconds < 86_400)
      return t('usage.hoursAgo').replace('{n}', String(Math.floor(seconds / 3600)))
    return t('usage.daysAgo').replace('{n}', String(Math.floor(seconds / 86_400)))
  }

  // bucketLabel 按分桶粒度格式化横轴时间，跨天的分钟或小时分桶带上日期
  const bucketLabel = (at: string, bucketSeconds: number, multiDay: boolean): string => {
    const date = new Date(at)
    const day = formats.value.day.format(date)
    if (bucketSeconds >= 86_400) return day
    const time = formats.value.minute.format(date)
    return multiDay ? `${day} ${time}` : time
  }

  // dimensionLabel 返回维度取值的显示名
  const dimensionLabel = (dimension: UsageDimension | 'status', key: string): string => {
    if (key === '') return t('usage.unassigned')
    if (dimension === 'channel' && (key === 'build' || key === 'playground' || key === 'assistant'))
      return t(channelLabelKey(key as UpstreamChannel))
    if (dimension === 'protocol') return protocolLabels[key] ?? key
    if (dimension === 'state') return stateKeys[key] ? t(stateKeys[key]) : key
    return key
  }

  return {
    compact,
    integer,
    usd,
    percent,
    duration,
    dateTime,
    clock,
    ago,
    bucketLabel,
    dimensionLabel,
  }
}

// formatBytes 以 KiB 或 B 显示字节数
export function formatBytes(value: number): string {
  return value >= 1024 ? `${(value / 1024).toFixed(1)} KiB` : `${value} B`
}
