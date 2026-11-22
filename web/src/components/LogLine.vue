<script lang="ts">
import { useI18n, type TranslationKey } from '@/i18n'
import { formatLogDuration, isBlockField, parseGoDuration } from '@/logs'
import type { AdminLog } from '@/types'

// valueKeys 是取值为固定类型的字段及其取值翻译键的前缀，没有对应翻译的取值原样显示
const valueKeys: Partial<Record<string, string>> = {
  first_event: 'logs.event',
  last_event: 'logs.event',
  current_event: 'logs.event',
  mode: 'logs.mode',
  channel: 'channel',
  cause: 'logs.cause',
  trigger: 'logs.trigger',
}

// useLogFieldText 返回日志字段按界面语言显示的名称与取值，时长按界面语言的单位显示
export function useLogFieldText() {
  const { t, logText, locale } = useI18n()
  return {
    label: (key: string): string => t(`logs.field.${key}` as TranslationKey) ?? key,
    value: (key: string, value: string): string => {
      const prefix = valueKeys[key]
      const ms = parseGoDuration(value)
      if (ms !== undefined) return formatLogDuration(ms, locale.value)
      return (prefix && t(`${prefix}.${value}` as TranslationKey)) || logText(value)
    },
  }
}

// useLogLineText 返回与 LogLine 展示一致的纯文本，供截断文字悬停时显示全文
export function useLogLineText() {
  const { logText } = useI18n()
  const fieldText = useLogFieldText()
  return (
    entry: AdminLog,
    options: { message?: boolean; part?: 'all' | 'inline' | 'block'; duration?: boolean } = {},
  ): string => {
    const { message = true, part = 'all', duration = true } = options
    const parts: string[] = []
    if (message) parts.push(logText(entry.message))
    for (const field of entry.fields ?? []) {
      if (part !== 'all' && isBlockField(field) !== (part === 'block')) continue
      if (!duration && field.key === 'duration') continue
      const text = `${fieldText.label(field.key)} ${fieldText.value(field.key, field.value)}`
      parts.push(part === 'all' && isBlockField(field) ? `\n${text}` : text)
    }
    return parts.join(' ').replace(/ \n/g, '\n')
  }
}
</script>

<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    entry: AdminLog
    message?: boolean
    part?: 'all' | 'inline' | 'block'
    duration?: boolean
    title?: boolean
  }>(),
  { message: true, part: 'all', duration: true, title: false },
)
const { logText } = useI18n()
const fieldText = useLogFieldText()

// fields 是按界面语言显示的字段：part 为 inline 时只取与消息同行的短字段，为 block 时只取另起一行的长字段；duration 为 false 时不含耗时，耗时显示在行尾
const fields = computed(() =>
  (props.entry.fields ?? [])
    .filter((field) => props.part === 'all' || isBlockField(field) === (props.part === 'block'))
    .filter((field) => props.duration || field.key !== 'duration')
    .map((field) => ({
      key: field.key,
      label: fieldText.label(field.key),
      value: fieldText.value(field.key, field.value),
      block: isBlockField(field),
    })),
)
</script>

<template>
  <span v-if="message" class="log-line-message" :class="{ 'log-op-title': title }">{{
    logText(entry.message)
  }}</span>
  <template v-for="(field, index) in fields" :key="field.key"
    >{{ message || index > 0 ? ' ' : ''
    }}<span
      class="log-field"
      :class="{ 'log-field-block': field.block, 'log-field-error': field.key === 'error' }"
      ><span class="log-field-key">{{ field.label }}</span
      >&nbsp;{{ field.value }}</span
    ></template
  >
</template>

<style>
/* 日志字段为浅色字段名加取值，字段之间靠间距分隔，日志页的消息、阶段卡片与请求卡片共用 */
.log-line-message {
  margin-inline-end: 8px;
  color: var(--color-fg);
}
/* 字段名不断行，与取值以不换行空格相连，换行只发生在字段之间或取值之内 */
.log-field {
  margin-inline-end: 8px;
  color: var(--color-gray-300);
  font-variant-numeric: tabular-nums;
}
.log-field-key {
  color: var(--color-gray-500);
  white-space: nowrap;
}
.log-field-block {
  display: block;
  margin-inline-end: 0;
}
.log-field-error {
  color: var(--color-red-300);
}
</style>
