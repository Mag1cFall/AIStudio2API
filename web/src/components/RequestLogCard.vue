<script setup lang="ts">
import { computed, ref } from 'vue'
import { api, ApiError } from '@/api'
import { channelLabelKey, useI18n, type TranslationKey } from '@/i18n'
import { formatLogNumber, type LogRow } from '@/logs'
import type { RequestBody } from '@/types'
import UiIcon from './UiIcon.vue'

const props = defineProps<{ row: LogRow }>()
const { t, locale, errorText, logText } = useI18n()
const request = computed(() => props.row.entry.request!)

const body = ref<RequestBody | null>(null)
const bodyState = ref<'idle' | 'loading' | 'missing' | 'error' | 'ready'>('idle')
const bodyError = ref('')
// detailsRendered 记录详情是否展开过，未展开的详情不渲染
const detailsRendered = ref(false)

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

// renderDetails 在详情首次展开时开始渲染内容
function renderDetails(event: Event): void {
  if ((event.target as HTMLDetailsElement).open) detailsRendered.value = true
}

// loadBody 在首次展开时读取账本保存的请求与响应正文
async function loadBody(event: Event): Promise<void> {
  if (!(event.target as HTMLDetailsElement).open) return
  if (bodyState.value === 'loading' || bodyState.value === 'ready') return
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

// pretty 缩进 JSON 正文，非 JSON 保持原文
function pretty(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return text
  }
}

// bodySize 显示保存字节数与原始字节数
function bodySize(saved: string, total: number): string {
  const bytes = new TextEncoder().encode(saved).length
  return bytes < total ? `${number(bytes)} / ${number(total)} B` : `${number(total)} B`
}

const bodyParts = computed(() =>
  body.value === null
    ? []
    : [
        {
          label: t('logs.requestBody'),
          text: pretty(body.value.request),
          size: bodySize(body.value.request, body.value.request_size),
        },
        {
          label: t('logs.responseBody'),
          text: pretty(body.value.response),
          size: bodySize(body.value.response, body.value.response_size),
        },
      ],
)
</script>

<template>
  <div class="request-log">
    <div class="request-heading">
      <span class="request-state" :class="`state-${request.state}`">{{
        request.status ?? t('logs.running')
      }}</span>
      <strong>{{ request.model || `${request.method} ${request.path}` }}</strong>
      <span v-if="request.duration_ms !== undefined" class="request-duration"
        >{{ number(request.duration_ms / 1000, 2) }} s</span
      >
      <span v-if="request.tool_calls" class="tool-count"
        >{{ t('logs.tools') }} {{ number(request.tool_calls) }}</span
      >
    </div>
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
      <span
        >{{ t('logs.averageSpeed') }}
        <b>{{ number(request.usage.average_tokens_per_second, 1) }}</b> tokens/s</span
      >
      <span v-if="request.first_event_ms !== undefined"
        >{{ t('logs.firstToken') }} <b>{{ number(request.first_event_ms / 1000, 1) }}</b> s</span
      >
    </div>
    <p v-if="request.error" class="request-error">{{ request.error }}</p>
    <p v-else-if="request.state === 'blocked' || request.state === 'limited'" class="request-error">
      {{ finishLabel }}
    </p>
    <details class="request-details" @toggle="renderDetails">
      <summary>
        <UiIcon class="request-disclosure-icon" name="chevronRight" :size="12" />
        {{ t('logs.details') }}
        <span class="request-summary-meta"
          >{{ request.method }} · HTTP {{ request.status || '…'
          }}<template v-if="request.channel">
            · {{ t(channelLabelKey(request.channel)) }}</template
          ></span
        >
      </summary>
      <template v-if="detailsRendered">
        <dl>
          <dt>ID</dt>
          <dd>{{ request.id }}</dd>
          <dt>{{ t('logs.endpoint') }}</dt>
          <dd>{{ request.method }} {{ request.path }}</dd>
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
            <span>{{ logText(event.message) }}</span>
          </li>
        </ol>
        <details class="request-json">
          <summary>
            <UiIcon class="request-disclosure-icon" name="chevronRight" :size="12" />
            JSON
          </summary>
          <pre>{{ JSON.stringify(row.entry, null, 2) }}</pre>
        </details>
        <details
          v-if="request.state !== 'running' && request.method === 'POST'"
          class="request-json"
          @toggle="loadBody"
        >
          <summary>
            <UiIcon class="request-disclosure-icon" name="chevronRight" :size="12" />
            {{ t('logs.body') }}
          </summary>
          <p v-if="bodyState === 'loading'" class="request-body-note">{{ t('common.loading') }}</p>
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
        </details>
      </template>
    </details>
  </div>
</template>

<style scoped>
.request-log {
  padding: 4px 0 6px;
}
.request-heading {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 12px;
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
  line-height: 19px;
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
.request-duration {
  color: var(--color-fg);
  margin-left: auto;
  font-variant-numeric: tabular-nums;
}
.tool-count {
  color: var(--color-blue-300);
  font-size: 13px;
}
.request-metrics {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 18px;
  margin-top: 6px;
  color: var(--color-gray-400);
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}
.request-metrics b {
  color: var(--color-gray-200);
  font-weight: 500;
}
.request-error {
  margin: 6px 0 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.request-details {
  margin-top: 5px;
  color: var(--color-gray-400);
  font-size: 13px;
}
.request-details summary {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 34px;
  padding: 6px 8px;
  border-radius: 4px;
  cursor: pointer;
  list-style: none;
  user-select: none;
  transition:
    background-color 120ms ease,
    color 120ms ease;
}
.request-details summary::-webkit-details-marker {
  display: none;
}
.request-details summary:hover {
  color: var(--color-fg);
  background: color-mix(in oklab, var(--color-line) 40%, transparent);
}
.request-details summary:focus-visible {
  outline: 1px solid var(--color-blue-400);
  outline-offset: 2px;
}
.request-disclosure-icon {
  color: var(--color-gray-500);
  transition: transform 120ms ease;
}
details[open] > summary > .request-disclosure-icon {
  transform: rotate(90deg);
}
.request-summary-meta {
  min-width: 0;
  margin-left: 6px;
  color: var(--color-gray-500);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
@media (pointer: coarse) {
  .request-details summary {
    min-height: 40px;
  }
}
@media (prefers-reduced-motion: reduce) {
  .request-details summary,
  .request-disclosure-icon {
    transition: none;
  }
}
.request-details dl {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: 4px 16px;
  margin: 10px 0;
}
.request-details dd {
  color: var(--color-fg);
  overflow-wrap: anywhere;
}
.request-timeline {
  border-left: 1px solid var(--color-line);
  padding-left: 12px;
  margin: 10px 0;
}
.request-timeline li {
  display: flex;
  align-items: baseline;
  gap: 12px;
  margin-bottom: 4px;
}
.request-timeline time {
  flex-shrink: 0;
  color: var(--color-gray-500);
}
.request-json pre {
  overflow: auto;
  max-height: 320px;
  padding: 12px;
  background: var(--color-panel);
  border: 1px solid var(--color-line);
  border-radius: 4px;
  margin-top: 6px;
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
    gap: 4px 12px;
  }
}
</style>
