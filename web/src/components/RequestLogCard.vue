<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { api, ApiError } from '@/api'
import { channelLabelKey, useI18n, type TranslationKey } from '@/i18n'
import { byteLength, formatLogNumber, prettyBody, requestSources, type LogRow } from '@/logs'
import type { RequestBody } from '@/types'
import DisclosureChevron from './DisclosureChevron.vue'
import UiCollapse from './UiCollapse.vue'
import UiDisclosure from './UiDisclosure.vue'
import UiIcon from './UiIcon.vue'

const props = defineProps<{ row: LogRow }>()
// open 是详情是否展开，由日志页按行保存
const open = defineModel<boolean>('open', { required: true })
const { t, locale, errorText, logText } = useI18n()
const request = computed(() => props.row.entry.request!)
const running = computed(() => request.value.state === 'running')

// now 是运行中请求每秒更新的当前时间
const now = ref(Date.now())
let ticker: number | undefined
watch(
  running,
  (active) => {
    window.clearInterval(ticker)
    ticker = active ? window.setInterval(() => (now.value = Date.now()), 1000) : undefined
    now.value = Date.now()
  },
  { immediate: true },
)
onUnmounted(() => window.clearInterval(ticker))

// runningSeconds 是运行中请求自开始起经过的秒数
const runningSeconds = computed(() =>
  Math.max(
    0,
    Math.floor(
      (now.value - Date.parse(request.value.started_at ?? props.row.events[0]!.time)) / 1000,
    ),
  ),
)

// currentStep 是运行中请求最近一条进度日志的首行
const currentStep = computed(() => {
  const latest = props.row.events.at(-1)!
  return latest.event === 'request.started' ? '' : logText(latest.message.split('\n')[0]!)
})

// sources 是请求依次用到的账户
const sources = computed(() => requestSources(props.row))
// switchReasons 是请求结束后换下的各账户最后一条警告或错误
const switchReasons = computed(() =>
  running.value
    ? []
    : sources.value
        .filter((source) => source !== props.row.entry.source)
        .flatMap((source) => {
          const event = [...props.row.events]
            .reverse()
            .find((item) => item.source === source && item.level !== 'INFO')
          return event ? [{ source, message: logText(event.message).replaceAll('\n', ' ') }] : []
        }),
)

const body = ref<RequestBody | null>(null)
const bodyState = ref<'idle' | 'loading' | 'missing' | 'error' | 'ready'>('idle')
const bodyError = ref('')
// jsonOpen 与 bodyOpen 是详情中 JSON 与正文两节是否展开
const jsonOpen = ref(false)
const bodyOpen = ref(false)

// finishKeys 是上游终止原因的显示文案键
const finishKeys: Record<string, TranslationKey> = {
  stop: 'logs.finish.stop',
  stop_sequence: 'logs.finish.stop_sequence',
  max_tokens: 'logs.finish.max_tokens',
  unspecified: 'logs.finish.unspecified',
  other: 'logs.finish.other',
  safety: 'logs.finish.safety',
  recitation: 'logs.finish.recitation',
  language: 'logs.finish.language',
  blocklist: 'logs.finish.blocklist',
  prohibited_content: 'logs.finish.prohibited_content',
  spii: 'logs.finish.spii',
  malformed_function_call: 'logs.finish.malformed_function_call',
  image_safety: 'logs.finish.image_safety',
  unexpected_tool_call: 'logs.finish.unexpected_tool_call',
  too_many_tool_calls: 'logs.finish.too_many_tool_calls',
  image_prohibited_content: 'logs.finish.image_prohibited_content',
  image_other: 'logs.finish.image_other',
  no_image: 'logs.finish.no_image',
  image_recitation: 'logs.finish.image_recitation',
  missing_thought_signature: 'logs.finish.missing_thought_signature',
}

// finishLabel 是终止原因的显示文案，上游未命名的编号显示为内容被拦截，未知原因显示原始值
const finishLabel = computed(() => {
  const reason = request.value.finish_reason ?? ''
  const code = /^provider_(\d+)$/.exec(reason)?.[1]
  if (code !== undefined) return t('logs.finish.provider').replace('{code}', code)
  const key = finishKeys[reason]
  return key === undefined ? reason : t(key)
})

// number 按当前语言格式化统计数值
function number(value: number, digits = 0): string {
  return formatLogNumber(value, locale.value, digits)
}

// toggleBody 展开或收起正文一节，首次展开时读取账本保存的请求与响应正文
async function toggleBody(): Promise<void> {
  bodyOpen.value = !bodyOpen.value
  if (!bodyOpen.value || bodyState.value === 'loading' || bodyState.value === 'ready') return
  bodyState.value = 'loading'
  try {
    body.value = await api.requestBody(request.value.id)
    bodyState.value = 'ready'
  } catch (reason) {
    if (reason instanceof ApiError && reason.status === 404) {
      bodyState.value = 'missing'
      return
    }
    bodyError.value = errorText(reason)
    bodyState.value = 'error'
  }
}

// bodySize 显示保存字节数与原始字节数
function bodySize(saved: string, total: number): string {
  const bytes = byteLength(saved)
  return bytes < total ? `${number(bytes)} / ${number(total)} B` : `${number(total)} B`
}

const bodyParts = computed(() =>
  body.value === null
    ? []
    : [
        {
          label: t('logs.requestBody'),
          text: prettyBody(body.value.request),
          size: bodySize(body.value.request, body.value.request_size),
        },
        {
          label: t('logs.responseBody'),
          text: prettyBody(body.value.response),
          size: bodySize(body.value.response, body.value.response_size),
        },
      ],
)
</script>

<template>
  <div class="request-log">
    <div class="request-heading">
      <UiDisclosure v-model="open" />
      <span class="request-state" :class="`state-${request.state}`">{{
        request.status ?? t('logs.running')
      }}</span>
      <strong>{{ request.model || `${request.method} ${request.path}` }}</strong>
      <span v-if="request.channel" class="request-channel">{{
        t(channelLabelKey(request.channel))
      }}</span>
      <span v-if="request.duration_ms !== undefined" class="request-duration"
        >{{ number(request.duration_ms / 1000, 2) }} s</span
      >
      <span v-else-if="running" class="request-duration">{{ number(runningSeconds) }} s</span>
      <span v-if="request.tool_calls" class="tool-count"
        >{{ t('logs.tools') }} {{ number(request.tool_calls) }}</span
      >
    </div>
    <p v-if="running && currentStep" class="request-step">{{ currentStep }}</p>
    <div v-if="request.usage" class="request-metrics">
      <span
        >{{ t('logs.reasoning') }} <b>{{ number(request.usage.reasoning_tokens) }}</b></span
      >
      <span
        >{{ t('logs.reply') }} <b>{{ number(request.usage.reply_tokens) }}</b></span
      >
      <span
        >{{ t('logs.outputTotal') }} <b>{{ number(request.usage.output_tokens) }}</b> tokens</span
      >
      <span v-if="request.usage.average_tokens_per_second !== undefined"
        >{{ t('logs.averageSpeed') }}
        <b>{{ number(request.usage.average_tokens_per_second, 1) }}</b> tokens/s</span
      >
      <span v-if="request.first_event_ms !== undefined"
        >{{ t('logs.firstToken') }} <b>{{ number(request.first_event_ms / 1000, 1) }}</b> s</span
      >
    </div>
    <p v-for="reason in switchReasons" :key="reason.source" class="request-switch">
      {{ reason.source }} · {{ reason.message }}
    </p>
    <p v-if="request.error" class="request-error request-error-summary">
      {{ logText(request.error) }}
    </p>
    <p v-else-if="request.state === 'blocked' || request.state === 'limited'" class="request-error">
      {{ finishLabel }}
    </p>
    <UiCollapse :open="open">
      <div class="request-details">
        <dl>
          <dt>ID</dt>
          <dd>{{ request.id }}</dd>
          <dt>{{ t('logs.endpoint') }}</dt>
          <dd>{{ request.method }} {{ request.path }}</dd>
          <template v-if="request.status !== undefined">
            <dt>HTTP</dt>
            <dd>{{ request.status }}</dd>
          </template>
          <template v-if="request.channel">
            <dt>{{ t('logs.channel') }}</dt>
            <dd>{{ t(channelLabelKey(request.channel)) }}</dd>
          </template>
          <template v-if="request.usage">
            <dt>{{ t('logs.inputTokens') }}</dt>
            <dd>{{ number(request.usage.input_tokens) }} tokens</dd>
            <template v-if="request.usage.cached_tokens">
              <dt>{{ t('logs.cachedTokens') }}</dt>
              <dd>{{ number(request.usage.cached_tokens) }} tokens</dd>
            </template>
            <dt>{{ t('logs.totalTokens') }}</dt>
            <dd>{{ number(request.usage.total_tokens) }} tokens</dd>
          </template>
          <template v-if="request.input_messages">
            <dt>{{ t('logs.inputMessages') }}</dt>
            <dd>{{ number(request.input_messages) }}</dd>
          </template>
          <template v-if="request.error">
            <dt>{{ t('logs.errorDetail') }}</dt>
            <dd class="request-error-detail">{{ logText(request.error) }}</dd>
          </template>
          <template v-if="request.finish_reason">
            <dt>{{ t('logs.finishReason') }}</dt>
            <dd>
              {{ finishLabel
              }}<span v-if="finishLabel !== request.finish_reason" class="text-gray-500">
                · {{ request.finish_reason }}</span
              >
            </dd>
          </template>
        </dl>
        <ol class="request-timeline">
          <li v-for="(event, index) in row.events" :key="index">
            <time>{{ new Date(event.time).toLocaleTimeString(locale, { hour12: false }) }}</time>
            <span v-if="sources.length > 1" class="request-timeline-source">{{
              event.source
            }}</span>
            <span>{{ logText(event.message) }}</span>
          </li>
        </ol>
        <section class="request-json">
          <button
            type="button"
            class="request-section-toggle"
            :aria-expanded="jsonOpen"
            @click="jsonOpen = !jsonOpen"
          >
            <DisclosureChevron class="request-disclosure-icon" :open="jsonOpen"
              ><UiIcon name="chevronRight" :size="13"
            /></DisclosureChevron>
            JSON
          </button>
          <UiCollapse :open="jsonOpen">
            <pre>{{ JSON.stringify(row.entry, null, 2) }}</pre>
          </UiCollapse>
        </section>
        <section
          v-if="request.state !== 'running' && request.method === 'POST'"
          class="request-json"
        >
          <button
            type="button"
            class="request-section-toggle"
            :aria-expanded="bodyOpen"
            @click="toggleBody"
          >
            <DisclosureChevron class="request-disclosure-icon" :open="bodyOpen"
              ><UiIcon name="chevronRight" :size="13"
            /></DisclosureChevron>
            {{ t('logs.body') }}
          </button>
          <UiCollapse :open="bodyOpen">
            <p v-if="bodyState === 'loading'" class="request-body-note">
              {{ t('common.loading') }}
            </p>
            <p v-else-if="bodyState === 'missing'" class="request-body-note">
              {{ t('logs.bodyMissing') }}
            </p>
            <p v-else-if="bodyState === 'error'" class="request-body-note text-red-300">
              {{ bodyError }}
            </p>
            <div v-for="part in bodyParts" v-else :key="part.label" class="request-body-part">
              <div class="request-body-heading">
                <span>{{ part.label }}</span>
                <span>{{ part.size }}</span>
              </div>
              <pre>{{ part.text }}</pre>
            </div>
          </UiCollapse>
        </section>
      </div>
    </UiCollapse>
  </div>
</template>

<style scoped>
.request-heading {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 2px 10px;
}
.request-heading strong {
  color: var(--color-gray-200);
  font-weight: 600;
  overflow-wrap: anywhere;
}
.request-state {
  border: 1px solid currentColor;
  border-radius: 4px;
  padding: 0 6px;
  font-size: 13px;
  line-height: 18px;
  font-variant-numeric: tabular-nums;
}
.state-completed,
.state-tool_calls {
  color: var(--color-green-400);
  background: color-mix(in oklab, var(--color-green-400) 8%, transparent);
}
.state-running {
  color: var(--color-blue-400);
}
.state-limited,
.state-blocked,
.state-cancelled {
  color: var(--color-yellow-400);
}
.state-failed,
.request-error {
  color: var(--color-red-400);
}
.request-channel {
  color: var(--color-gray-500);
  font-size: 13px;
}
.request-duration {
  color: var(--color-fg);
  margin-left: auto;
  font-variant-numeric: tabular-nums;
}
.request-step {
  margin: 2px 0 0;
  color: var(--color-gray-400);
  font-size: 13px;
  overflow-wrap: anywhere;
}
.tool-count {
  color: var(--color-gray-400);
  font-size: 13px;
}
.request-metrics {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 14px;
  margin-top: 2px;
  color: var(--color-gray-400);
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}
.request-metrics b {
  color: var(--color-gray-200);
  font-weight: 500;
}
.request-error {
  margin: 2px 0 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
/* request-error-summary 为卡片上的错误，最多两行，全文在详情中 */
.request-error-summary {
  display: -webkit-box;
  overflow: hidden;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
}
.request-details .request-error-detail {
  color: var(--color-red-400);
  white-space: pre-wrap;
}
.request-details {
  margin: 6px 0 4px;
  padding: 8px 12px;
  border-left: 1px solid var(--color-line);
  background: color-mix(in oklab, var(--color-panel) 70%, transparent);
  color: var(--color-gray-400);
  font-size: 13px;
}
.request-details dl {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: 4px 16px;
  margin: 0 0 8px;
}
.request-details dd {
  color: var(--color-fg);
  overflow-wrap: anywhere;
}
.request-timeline {
  border-left: 1px solid var(--color-line);
  padding-left: 12px;
  margin: 8px 0;
}
.request-timeline li {
  display: flex;
  align-items: baseline;
  gap: 12px;
  margin-bottom: 4px;
}
.request-timeline time,
.request-timeline-source {
  flex-shrink: 0;
  color: var(--color-gray-500);
}
/* request-switch 为卡片上换下账户的原因，最多两行，全文在详情中 */
.request-switch {
  display: -webkit-box;
  overflow: hidden;
  margin: 2px 0 0;
  color: var(--color-yellow-400);
  font-size: 13px;
  overflow-wrap: anywhere;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
}
.request-section-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  width: calc(100% + 6px);
  min-height: 28px;
  padding: 4px 6px;
  margin-left: -6px;
  border-radius: 4px;
  text-align: start;
  user-select: none;
  transition:
    background-color 120ms ease,
    color 120ms ease;
}
.request-section-toggle:hover {
  color: var(--color-fg);
  background: color-mix(in oklab, var(--color-line) 40%, transparent);
}
.request-section-toggle:focus-visible {
  outline: 1px solid var(--color-blue-400);
  outline-offset: 2px;
}
.request-disclosure-icon {
  color: var(--color-gray-500);
}
@media (pointer: coarse) {
  .request-section-toggle {
    min-height: 40px;
  }
}
.request-json pre {
  overflow: auto;
  max-height: 320px;
  padding: 12px;
  background: var(--color-canvas);
  border: 1px solid var(--color-line);
  border-radius: 4px;
  margin: 4px 0 6px;
  white-space: pre;
}
.request-body-note {
  margin-top: 6px;
  color: var(--color-gray-400);
}
.request-body-part {
  margin-top: 8px;
}
.request-body-heading {
  display: flex;
  justify-content: space-between;
  color: var(--color-gray-400);
  font-size: 13px;
}
@media (max-width: 767px) {
  .request-duration {
    margin-left: 0;
  }
  .request-metrics {
    gap: 2px 12px;
  }
}
</style>
