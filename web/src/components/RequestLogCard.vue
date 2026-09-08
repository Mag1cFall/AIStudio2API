<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from '@/i18n'
import type { LogRow } from '@/logs'
import UiIcon from './UiIcon.vue'

const props = defineProps<{ row: LogRow }>()
const { t, locale } = useI18n()
const request = computed(() => props.row.entry.request!)

// number 按当前语言格式化统计数值
function number(value: number, digits = 0): string {
  return value.toLocaleString(locale.value, { maximumFractionDigits: digits })
}
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
    </div>
    <p v-if="request.error" class="request-error">{{ request.error }}</p>
    <p v-else-if="request.state === 'blocked' || request.state === 'limited'" class="request-error">
      {{ request.finish_reason }}
    </p>
    <details class="request-details">
      <summary>
        <UiIcon class="request-disclosure-icon" name="chevronRight" :size="12" />
        {{ t('logs.details') }}
        <span class="request-summary-meta"
          >{{ request.method }} · HTTP {{ request.status || '…' }}</span
        >
      </summary>
      <dl>
        <dt>ID</dt>
        <dd>{{ request.id }}</dd>
        <dt>{{ t('logs.endpoint') }}</dt>
        <dd>{{ request.method }} {{ request.path }}</dd>
        <template v-if="request.usage">
          <dt>{{ t('logs.inputTokens') }}</dt>
          <dd>{{ number(request.usage.input_tokens) }} tokens</dd>
          <dt>{{ t('logs.totalTokens') }}</dt>
          <dd>{{ number(request.usage.total_tokens) }} tokens</dd>
        </template>
        <template v-if="request.input_messages">
          <dt>{{ t('logs.inputMessages') }}</dt>
          <dd>{{ number(request.input_messages) }}</dd>
        </template>
        <template v-if="request.finish_reason">
          <dt>{{ t('logs.finishReason') }}</dt>
          <dd>{{ request.finish_reason }}</dd>
        </template>
      </dl>
      <ol class="request-timeline">
        <li v-for="(event, index) in row.events" :key="index">
          <time>{{ new Date(event.time).toLocaleTimeString(locale, { hour12: false }) }}</time>
          <span>{{ event.message }}</span>
        </li>
      </ol>
      <details class="request-json">
        <summary>
          <UiIcon class="request-disclosure-icon" name="chevronRight" :size="12" />
          JSON
        </summary>
        <pre>{{ JSON.stringify(row.entry, null, 2) }}</pre>
      </details>
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
  color: #e6edf3;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.request-state {
  border: 1px solid currentColor;
  border-radius: 4px;
  padding: 0 6px;
  font-size: 11px;
  line-height: 19px;
}
.state-completed,
.state-tool_calls {
  color: #56d4a0;
  background: #56d4a00c;
}
.state-running {
  color: #79c0ff;
}
.state-limited,
.state-blocked,
.state-cancelled {
  color: #e3b341;
}
.state-failed,
.request-error {
  color: #ff7b72;
}
.request-duration {
  color: #c9d1d9;
  margin-left: auto;
  font-variant-numeric: tabular-nums;
}
.tool-count {
  color: #a5d6ff;
  font-size: 12px;
}
.request-metrics {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 18px;
  margin-top: 6px;
  color: #8b949e;
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
.request-metrics b {
  color: #d2dae3;
  font-weight: 500;
}
.request-error {
  margin: 6px 0 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.request-details {
  margin-top: 5px;
  color: #8b949e;
  font-size: 11px;
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
  color: #c9d1d9;
  background: #30363d66;
}
.request-details summary:focus-visible {
  outline: 1px solid #79c0ff;
  outline-offset: 2px;
}
.request-disclosure-icon {
  color: #6e7681;
  transition: transform 120ms ease;
}
details[open] > summary > .request-disclosure-icon {
  transform: rotate(90deg);
}
.request-summary-meta {
  min-width: 0;
  margin-left: 6px;
  color: #6e7681;
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
  color: #c9d1d9;
  overflow-wrap: anywhere;
}
.request-timeline {
  border-left: 1px solid #30363d;
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
  color: #6e7681;
}
.request-json pre {
  overflow: auto;
  max-height: 320px;
  padding: 12px;
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 4px;
  margin-top: 6px;
  white-space: pre;
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
