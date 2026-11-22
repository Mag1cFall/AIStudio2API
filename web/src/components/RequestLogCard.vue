<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { api, ApiError } from '@/api'
import { channelLabelKey, useI18n, type TranslationKey } from '@/i18n'
import {
  byteLength,
  formatLogDuration,
  formatLogNumber,
  isAttempt,
  logField,
  prettyBody,
  requestSources,
  requestTimeline,
  type LogRow,
} from '@/logs'
import type { AdminLog, RequestBody } from '@/types'
import DisclosureChevron from './DisclosureChevron.vue'
import LogLine, { useLogLineText } from './LogLine.vue'
import RequestStep from './RequestStep.vue'
import UiCollapse from './UiCollapse.vue'
import UiDisclosure from './UiDisclosure.vue'
import UiIcon from './UiIcon.vue'
import { useUsageFormat } from './usage/format'

const props = defineProps<{ row: LogRow }>()
// open 是详情是否展开，由日志页按行保存
const open = defineModel<boolean>('open', { required: true })
const { t, locale, errorText, logText } = useI18n()
const formatLine = useLogLineText()
const { duration } = useUsageFormat()
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

// currentStep 是运行中请求最近一条进度日志，请求刚开始或刚换账户时为空
const currentStep = computed(() => {
  const latest = props.row.events.at(-1)!
  return latest.event === 'request.started' || isAttempt(latest) ? undefined : latest
})

// sources 是请求依次用到的账户
const sources = computed(() => requestSources(props.row))
// attempts 是请求中失败后换账户的各次上游尝试
const attempts = computed(() => props.row.events.filter(isAttempt))
// attemptGroups 按 Google 拒绝账户的失败类型或上游 HTTP 状态汇总换账户次数，次数多的在前，两者都没有的归入“其他”
const attemptGroups = computed(() => {
  const groups = new Map<string, { label: string; tone: string; count: number }>()
  for (const attempt of attempts.value) {
    const cause = logField(attempt, 'cause')
    const status = logField(attempt, 'status') ?? ''
    const key = cause ?? status
    const group = groups.get(key) ?? {
      label: cause ? t(`logs.cause.${cause}` as TranslationKey) : status || t('logs.attemptOther'),
      tone: cause ? 'tone-error' : statusTone(status),
      count: 0,
    }
    group.count += 1
    groups.set(key, group)
  }
  return [...groups.values()].sort((a, b) => b.count - a.count)
})
// timeline 是详情中的时间线
const timeline = computed(() => requestTimeline(props.row.events))
// openAttempts 是时间线中展开了全部事件的尝试序号
const openAttempts = ref(new Set<number>())

// toggleAttempt 展开或收起时间线中一次尝试的全部事件
function toggleAttempt(index: number): void {
  const next = new Set(openAttempts.value)
  if (!next.delete(index)) next.add(index)
  openAttempts.value = next
}

// shortLabel 返回卡片上的字段名：界面语言有缩写时显示缩写，悬停提示为全称
function shortLabel(short: TranslationKey, full: TranslationKey): { text: string; title?: string } {
  const text = t(short)
  return text === t(full) ? { text } : { text, title: t(full) }
}
// labels 是卡片上可缩写的字段名
const labels = computed(() => ({
  tools: shortLabel('logs.toolsShort', 'logs.tools'),
  switches: shortLabel('logs.switchesShort', 'logs.switches'),
  firstToken: shortLabel('logs.firstTokenShort', 'logs.firstToken'),
}))

// causeKeys 是上游 HTTP 状态对应的失败原因文案键
const causeKeys: Record<string, TranslationKey> = {
  '400': 'logs.cause.invalid',
  '401': 'logs.cause.signedOut',
  '403': 'logs.cause.permission',
  '404': 'logs.cause.notFound',
  '429': 'logs.cause.quota',
  '500': 'logs.cause.internal',
  '503': 'logs.cause.unavailable',
}

// attemptCause 返回一次尝试的失败原因：Google 拒绝账户时按失败类型，其余按上游 HTTP 状态归类，两者都没有时为原因原文的首行
function attemptCause(attempt: AdminLog): string {
  const cause = logField(attempt, 'cause')
  if (cause !== undefined) return t(`logs.cause.${cause}` as TranslationKey)
  const status = logField(attempt, 'status')
  if (status !== undefined) return t(causeKeys[status] ?? 'logs.cause.failed')
  return logText(logField(attempt, 'reason') ?? '').split('\n')[0]!
}

// attemptReason 返回已归类的尝试按界面语言显示的完整原因，未归类时原因已作为失败原因显示
function attemptReason(attempt: AdminLog): string {
  return logField(attempt, 'status') === undefined && logField(attempt, 'cause') === undefined
    ? ''
    : logText(logField(attempt, 'reason') ?? '')
}

// upstreamMessage 返回原因中上游 RPC 错误自带的原文，即“返回 HTTP 状态、协议错误码”之后的文字，没有时为完整原因
function upstreamMessage(attempt: AdminLog): string {
  const reason = logField(attempt, 'reason') ?? ''
  return (
    /返回 HTTP \d+(?:、协议错误码 \d+)?: ([\s\S]*)$/.exec(reason)?.[1] ?? attemptReason(attempt)
  )
}

// statusTone 返回上游状态标记的颜色：额度用尽的 429 为警告色，401、403 与 5xx 为错误色，其余为中性色
function statusTone(status: string): string {
  if (status === '429') return 'tone-warn'
  return status === '401' || status === '403' || status.startsWith('5')
    ? 'tone-error'
    : 'tone-neutral'
}

// sourceName 返回时间线中的来源：账户只显示邮箱的本地部分，服务、请求与账户管理显示界面语言的名称
function sourceName(source: string): string {
  const at = source.indexOf('@')
  if (at > 0) return source.slice(0, at)
  return t(`logs.sources.${source}` as TranslationKey) ?? source
}

// trimmed 返回去掉与卡片模型相同的 model 字段后的事件
function trimmed(event: AdminLog): AdminLog {
  return event.fields?.some((field) => field.key === 'model' && field.value === request.value.model)
    ? { ...event, fields: event.fields.filter((field) => field.key !== 'model') }
    : event
}

// clock 返回事件按界面语言显示的时刻
function clock(event: AdminLog): string {
  return new Date(event.time).toLocaleTimeString(locale.value, { hour12: false })
}

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

// seconds 按当前语言格式化以秒计的时长
function seconds(value: number, digits = 0): string {
  return formatLogNumber(value, locale.value, digits, 'second')
}

// proofPhases 列出 WAA 耗时中不为零的分项
const proofPhases = computed(() => {
  const phases = request.value.proof_phases
  if (phases === undefined) return ''
  return (
    [
      ['logs.proofLock', phases.lock_ms],
      ['logs.proofInteraction', phases.interaction_ms],
      ['logs.proofRefresh', phases.refresh_ms],
      ['logs.proofCompute', phases.compute_ms],
    ] as const
  )
    .filter(([, value]) => value >= 1)
    .map(([key, value]) => `${t(key)} ${duration(value)}`)
    .join(' · ')
})

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

// hasSecondRow 判断请求卡片是否有原因、步骤或指标第二行
const hasSecondRow = computed(() =>
  Boolean(
    request.value.error ||
    request.value.state === 'blocked' ||
    request.value.state === 'limited' ||
    (running.value && currentStep.value) ||
    request.value.usage,
  ),
)

// headingTooltip 返回标题行完整文字，供截断时悬停显示
const headingTooltip = computed(() => {
  const parts = [request.value.model || `${request.value.method} ${request.value.path}`]
  if (request.value.channel) parts.push(t(channelLabelKey(request.value.channel)))
  if (request.value.tool_calls)
    parts.push(`${labels.value.tools.text} ${number(request.value.tool_calls)}`)
  if (attempts.value.length) {
    const groups = attemptGroups.value
      .map((group) => `${group.label} ×${number(group.count)}`)
      .join(' ')
    parts.push(`${labels.value.switches.text} ${number(attempts.value.length)} ${groups}`.trim())
  }
  return parts.join(' ')
})

// stepTooltip 返回运行中当前步骤的完整文字，供截断时悬停显示
const stepTooltip = computed(() =>
  currentStep.value ? formatLine(trimmed(currentStep.value), { part: 'inline' }) : '',
)

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
  <div class="log-op request-log">
    <UiDisclosure v-model="open" />
    <span class="log-state" :class="`log-state-${request.state}`">{{
      request.status ?? t('logs.running')
    }}</span>
    <span
      v-tooltip.overflow="headingTooltip"
      class="log-op-main request-heading"
      :class="hasSecondRow ? 'request-heading-single' : 'request-heading-clamp'"
      ><strong class="log-op-title">{{
        request.model || `${request.method} ${request.path}`
      }}</strong
      ><span v-if="request.channel" class="request-channel">{{
        t(channelLabelKey(request.channel))
      }}</span
      ><span v-if="request.tool_calls" class="request-tools"
        ><span class="log-field-key" :title="labels.tools.title">{{ labels.tools.text }}</span
        >&nbsp;{{ number(request.tool_calls) }}</span
      ><span v-if="attempts.length" class="request-attempts"
        ><span class="log-field-key" :title="labels.switches.title">{{ labels.switches.text }}</span
        >&nbsp;{{ number(attempts.length) }}
        <span
          v-for="group in attemptGroups"
          :key="group.label"
          class="attempt-status"
          :class="group.tone"
          >{{ group.label }} ×{{ number(group.count) }}</span
        ></span
      ></span
    >
    <span class="log-op-duration">{{
      request.duration_ms !== undefined
        ? request.duration_ms < 1000
          ? formatLogDuration(request.duration_ms, locale)
          : seconds(request.duration_ms / 1000, 2)
        : running
          ? seconds(runningSeconds)
          : ''
    }}</span>
    <p
      v-if="request.error"
      v-tooltip.overflow="logText(request.error)"
      class="log-op-row request-error request-error-summary"
    >
      {{ logText(request.error) }}
    </p>
    <p
      v-else-if="request.state === 'blocked' || request.state === 'limited'"
      v-tooltip.overflow="finishLabel"
      class="log-op-row request-error request-error-summary"
    >
      {{ finishLabel }}
    </p>
    <p
      v-else-if="running && currentStep"
      v-tooltip.overflow="stepTooltip"
      class="log-op-row request-step"
    >
      <LogLine :entry="trimmed(currentStep)" part="inline" />
    </p>
    <div v-else-if="request.usage" class="log-op-row request-metrics">
      <span class="log-field"
        ><span class="log-field-key">{{ t('logs.reasoning') }}</span
        >&nbsp;{{ number(request.usage.reasoning_tokens) }}</span
      >
      <span class="log-field"
        ><span class="log-field-key">{{ t('logs.reply') }}</span
        >&nbsp;{{ number(request.usage.reply_tokens) }}</span
      >
      <span class="log-field"
        ><span class="log-field-key">{{ t('logs.outputTotal') }}</span
        >&nbsp;{{ number(request.usage.output_tokens) }} tokens</span
      >
      <span v-if="request.usage.average_tokens_per_second !== undefined" class="log-field"
        ><span class="log-field-key">{{ t('logs.averageSpeed') }}</span
        >&nbsp;{{ number(request.usage.average_tokens_per_second, 1) }} tokens/s</span
      >
      <span v-if="request.first_event_ms !== undefined" class="log-field"
        ><span class="log-field-key" :title="labels.firstToken.title">{{
          labels.firstToken.text
        }}</span
        >&nbsp;{{ seconds(request.first_event_ms / 1000, 1) }}</span
      >
    </div>
    <UiCollapse class="log-op-row log-op-detail" :open="open">
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
          <template v-if="request.proof_ms">
            <dt>{{ t('logs.field.waa') }}</dt>
            <dd>
              {{ duration(request.proof_ms)
              }}<span v-if="proofPhases" class="text-gray-500"> · {{ proofPhases }}</span>
            </dd>
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
          <template v-for="(item, index) in timeline" :key="index">
            <RequestStep
              v-if="item.kind !== 'attempt'"
              :step="item"
              :model="request.model"
              :channel="request.channel"
              :source="
                sources.length > 1 && item.events[0]!.source.includes('@')
                  ? sourceName(item.events[0]!.source)
                  : ''
              "
            />
            <li v-else class="timeline-attempt">
              <button
                type="button"
                class="attempt-toggle"
                :aria-expanded="openAttempts.has(index)"
                @click="toggleAttempt(index)"
              >
                <time class="font-mono">{{ clock(item.steps[0]!.events[0]!) }}</time>
                <span class="request-timeline-source" :title="item.attempt.source">{{
                  sourceName(item.attempt.source)
                }}</span>
                <span class="attempt-line">
                  <span
                    v-if="logField(item.attempt, 'status')"
                    class="attempt-status"
                    :class="statusTone(logField(item.attempt, 'status')!)"
                    >{{ logField(item.attempt, 'status') }}</span
                  >
                  <span class="attempt-cause">{{ attemptCause(item.attempt) }}</span>
                  <span
                    v-if="attemptReason(item.attempt)"
                    class="attempt-detail"
                    :title="attemptReason(item.attempt)"
                    >{{ logText(upstreamMessage(item.attempt)) }}</span
                  >
                </span>
                <DisclosureChevron class="request-disclosure-icon" :open="openAttempts.has(index)"
                  ><UiIcon name="chevronRight" :size="13"
                /></DisclosureChevron>
              </button>
              <UiCollapse :open="openAttempts.has(index)">
                <ol class="attempt-events">
                  <RequestStep
                    v-for="(step, position) in item.steps"
                    :key="position"
                    :step="step"
                    :model="request.model"
                    :channel="request.channel"
                    source=""
                  />
                </ol>
              </UiCollapse>
            </li>
          </template>
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
.request-heading > * {
  margin-inline-end: 10px;
}
.request-heading > :last-child {
  margin-inline-end: 0;
}
.request-heading-single {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.request-heading-single .request-attempts {
  flex-wrap: nowrap;
}
.request-heading-clamp {
  display: -webkit-box;
  overflow: hidden;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
}
.request-error {
  color: var(--color-red-400);
}
.request-channel {
  color: var(--color-gray-500);
}
.request-tools {
  color: var(--color-gray-300);
  font-variant-numeric: tabular-nums;
}
.request-step {
  overflow: hidden;
  margin: 2px 0 0;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.request-step :deep(.log-line-message) {
  color: var(--color-gray-300);
}
.request-metrics {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 6px;
  margin-top: 2px;
}
.request-error {
  margin: 2px 0 0;
}
/* request-error-summary 为卡片上的错误或拦截原因，单行截断，悬停或展开详情看全文 */
.request-error-summary {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
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
.request-details dt {
  color: var(--color-gray-500);
}
.request-details dd {
  color: var(--color-fg);
  overflow-wrap: anywhere;
  font-variant-numeric: tabular-nums;
}
.request-timeline {
  border-left: 1px solid var(--color-line);
  padding-left: 12px;
  margin: 8px 0;
}
.attempt-toggle time,
.request-timeline-source {
  flex-shrink: 0;
  color: var(--color-gray-500);
}
.attempt-toggle time {
  font-variant-numeric: tabular-nums;
}
/* request-attempts 为卡片标题行的换账户次数与按上游状态的分组计数 */
.request-attempts {
  display: inline-flex;
  vertical-align: top;
  align-items: center;
  flex-wrap: wrap;
  gap: 2px 6px;
  color: var(--color-gray-300);
  font-variant-numeric: tabular-nums;
}
.attempt-status {
  flex-shrink: 0;
  border: 1px solid currentColor;
  border-radius: 4px;
  padding: 0 5px;
  line-height: 16px;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.tone-warn {
  color: var(--color-yellow-400);
}
.tone-error {
  color: var(--color-red-400);
}
.tone-neutral {
  color: var(--color-gray-400);
}
/* timeline-attempt 为时间线中一次失败后换账户的尝试，一行显示账户、上游状态与原因，点击展开该次尝试的全部事件 */
.timeline-attempt {
  margin-bottom: 4px;
}
.attempt-toggle {
  display: flex;
  align-items: center;
  gap: 12px;
  width: calc(100% + 6px);
  margin-left: -6px;
  padding: 0 6px;
  border-radius: 4px;
  text-align: start;
  transition: background-color 120ms ease;
}
.attempt-toggle:hover {
  background: color-mix(in oklab, var(--color-line) 40%, transparent);
}
.attempt-toggle .request-timeline-source {
  max-width: 9rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.attempt-toggle:focus-visible {
  outline: 1px solid var(--color-blue-400);
  outline-offset: 2px;
}
.attempt-line {
  display: flex;
  flex: 1;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
/* attempt-cause 与 attempt-detail 单行显示，上游原文只占剩余宽度，悬停显示全文 */
.attempt-cause {
  overflow: hidden;
  min-width: 0;
  color: var(--color-fg);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.attempt-detail {
  flex: 1 1 0;
  overflow: hidden;
  min-width: 0;
  color: var(--color-gray-500);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.attempt-events {
  margin: 4px 0 6px;
  padding-left: 12px;
  border-left: 1px solid var(--color-line);
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
  /* 窄屏下换号行只显示账户、上游状态与原因，账户名先截断，上游原文在展开的事件中 */
  .attempt-toggle .request-timeline-source {
    flex-shrink: 1;
    min-width: 0;
  }
  .attempt-line {
    flex: 0 0 auto;
    margin-inline-end: auto;
  }
  .attempt-detail {
    display: none;
  }
  .request-metrics {
    gap: 2px 12px;
  }
}
</style>
