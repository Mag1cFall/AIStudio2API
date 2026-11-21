<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from '@/i18n'
import {
  formatLogDuration,
  formatLogNumber,
  logDuration,
  logField,
  pauseDuration,
  pauseResumed,
  pauseStart,
  type TimelineStep,
} from '@/logs'
import { useLogFieldText } from './LogLine.vue'

const props = defineProps<{
  step: TimelineStep
  // model 与 channel 是请求卡片的模型与通道，取值相同的字段不再逐步显示
  model: string | undefined
  channel: string | undefined
  // source 是这一步的账户简称，请求只用到一个账户时为空
  source: string
}>()
const { t, locale, logText } = useI18n()
const fieldText = useLogFieldText()

// hiddenFields 是只在 JSON 中查看的诊断字段：上游方法、流的事件计数与字节数、停顿前后的事件类型
const hiddenFields = new Set([
  'rpc',
  'mode',
  'network_bytes',
  'network_idle',
  'reasoning_events',
  'text_events',
  'last_event',
  'current_event',
])

// summaryKeys 是作为这一步摘要显示在标题后的时长字段，取第一个存在的
const summaryKeys = ['duration', 'waited']

const latest = computed(() => props.step.events.at(-1)!)
const pause = computed(() => props.step.kind === 'pause')

// time 是这一步开始的时刻，停顿为最后一次收到输出的时刻
const time = computed(() =>
  (pause.value ? pauseStart(props.step) : new Date(props.step.events[0]!.time)).toLocaleTimeString(
    locale.value,
    { hour12: false },
  ),
)

// title 是这一步的标题：停顿写明时长与是否已恢复，其余为日志消息
const title = computed(() => {
  if (!pause.value) return logText(latest.value.message)
  const duration = formatLogDuration(pauseDuration(props.step), locale.value)
  return t(pauseResumed(props.step) ? 'logs.pauseResumed' : 'logs.pause').replace(
    '{duration}',
    duration,
  )
})

// summaryKey 是摘要所用的时长字段，停顿与没有时长字段的步骤为空
const summaryKey = computed(() =>
  pause.value ? undefined : summaryKeys.find((key) => logDuration(latest.value, key) !== undefined),
)

// summary 是标题后的摘要：停顿恢复后为恢复时的输出类型，其余为时长字段的取值
const summary = computed(() => {
  if (summaryKey.value !== undefined)
    return formatLogDuration(logDuration(latest.value, summaryKey.value)!, locale.value)
  const resumed = logField(latest.value, 'current_event')
  return pause.value && pauseResumed(props.step) && resumed
    ? fieldText.value('current_event', resumed)
    : ''
})

// fields 是另起几行、名称与取值对齐显示的字段，停顿只显示标题
const fields = computed(() =>
  pause.value
    ? []
    : (latest.value.fields ?? [])
        .filter(
          (field) =>
            field.key !== summaryKey.value &&
            !hiddenFields.has(field.key) &&
            !(field.key === 'model' && field.value === props.model) &&
            !(field.key === 'channel' && field.value === props.channel),
        )
        .map((field) => ({
          key: field.key,
          label: fieldText.label(field.key),
          value: fieldText.value(field.key, field.value),
        })),
)

// repeats 是合并为这一步的同类事件数，停顿不计
const repeats = computed(() => (pause.value ? 1 : props.step.events.length))

// occurrences 列出合并的同类事件各自的时刻与字段原文
const occurrences = computed(() =>
  props.step.events
    .map((event) =>
      [
        new Date(event.time).toLocaleTimeString(locale.value, { hour12: false }),
        ...(event.fields ?? []).map(
          (field) => `${fieldText.label(field.key)} ${logText(field.value)}`,
        ),
      ].join('  '),
    )
    .join('\n'),
)
</script>

<template>
  <li class="request-step" :class="`request-step-${latest.level.toLowerCase()}`">
    <time class="font-mono">{{ time }}</time>
    <div class="request-step-main">
      <span class="request-step-title">{{ title }}</span
      ><span v-if="repeats > 1" class="request-step-count" :title="occurrences"
        >×{{ formatLogNumber(repeats, locale) }}</span
      ><span v-if="summary" class="request-step-summary">{{ summary }}</span
      ><span v-if="source" class="request-step-source" :title="latest.source">{{ source }}</span>
    </div>
    <dl v-if="fields.length" class="request-step-fields">
      <template v-for="field in fields" :key="field.key">
        <dt>{{ field.label }}</dt>
        <dd :class="{ 'field-error': field.key === 'error' }">{{ field.value }}</dd>
      </template>
    </dl>
  </li>
</template>

<style scoped>
/* request-step 是请求时间线的一步：时刻一列，标题、摘要与账户一行，字段在标题下方按名称与取值对齐 */
.request-step {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  column-gap: 12px;
  row-gap: 1px;
  margin-bottom: 6px;
}
.request-step time {
  color: var(--color-gray-500);
  font-variant-numeric: tabular-nums;
}
.request-step-main {
  min-width: 0;
  overflow-wrap: anywhere;
}
.request-step-main > * + * {
  margin-inline-start: 10px;
}
.request-step-title {
  color: var(--color-fg);
}
.request-step-warn .request-step-title {
  color: var(--color-yellow-400);
}
.request-step-error .request-step-title {
  color: var(--color-red-400);
}
.request-step-count,
.request-step-summary {
  color: var(--color-gray-400);
  font-variant-numeric: tabular-nums;
}
.request-step-source {
  color: var(--color-gray-500);
}
.request-step-fields {
  grid-column: 2;
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: 1px 12px;
  margin: 0;
  font-size: 12.5px;
}
.request-step-fields dt {
  color: var(--color-gray-500);
}
.request-step-fields dd {
  min-width: 0;
  margin: 0;
  color: var(--color-gray-300);
  font-variant-numeric: tabular-nums;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.request-step-fields .field-error {
  color: var(--color-red-300);
}
</style>
