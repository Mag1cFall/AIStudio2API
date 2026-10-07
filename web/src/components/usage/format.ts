import { computed } from 'vue'
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

// useUsageFormat 提供随界面语言变化的数字、耗时、时间与维度取值格式
export function useUsageFormat() {
  const { t, locale } = useI18n()
  const compactFormat = computed(
    () => new Intl.NumberFormat(locale.value, { notation: 'compact', maximumFractionDigits: 1 }),
  )
  const integerFormat = computed(() => new Intl.NumberFormat(locale.value))

  // compact 以紧凑格式显示数量
  const compact = (value: number): string => compactFormat.value.format(value)

  // integer 以千分位显示整数
  const integer = (value: number): string => integerFormat.value.format(value)

  // percent 显示比例，空值显示破折号
  const percent = (value: number | null, digits = 1): string =>
    value === null
      ? '—'
      : `${(value * 100).toLocaleString(locale.value, { maximumFractionDigits: digits })}%`

  // duration 把毫秒显示为毫秒、秒或分钟
  const duration = (milliseconds: number): string => {
    if (milliseconds <= 0) return '—'
    if (milliseconds < 1000) return `${Math.round(milliseconds)} ms`
    if (milliseconds < 60_000)
      return `${(milliseconds / 1000).toLocaleString(locale.value, { maximumFractionDigits: 1 })} s`
    const minutes = Math.floor(milliseconds / 60_000)
    const seconds = Math.round((milliseconds % 60_000) / 1000)
    return `${minutes} min ${seconds} s`
  }

  // dateTime 显示带月日的本地时间，seconds 为真时显示秒
  const dateTime = (value: string | Date, seconds = false): string =>
    new Date(value).toLocaleString(locale.value, {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      ...(seconds ? { second: '2-digit' } : {}),
      hour12: false,
    })

  // clock 显示本地时分秒
  const clock = (value: string | Date): string =>
    new Date(value).toLocaleTimeString(locale.value, { hour12: false })

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
    const day = date.toLocaleDateString(locale.value, { month: '2-digit', day: '2-digit' })
    if (bucketSeconds >= 86_400) return day
    const time = date.toLocaleTimeString(locale.value, {
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    })
    return multiDay ? `${day} ${time}` : time
  }

  // dimensionLabel 返回维度取值的显示名
  const dimensionLabel = (dimension: UsageDimension | 'status', key: string): string => {
    if (key === '') return t('usage.unassigned')
    if (dimension === 'channel' && (key === 'build' || key === 'playground'))
      return t(channelLabelKey(key as UpstreamChannel))
    if (dimension === 'protocol') return protocolLabels[key] ?? key
    if (dimension === 'state') return stateKeys[key] ? t(stateKeys[key]) : key
    return key
  }

  return {
    compact,
    integer,
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
