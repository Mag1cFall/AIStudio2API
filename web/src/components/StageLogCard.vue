<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n, type TranslationKey } from '@/i18n'
import type { LogRow, LogStageState } from '@/logs'
import UiIcon from './UiIcon.vue'

const props = defineProps<{ row: LogRow }>()
const { t, locale, logText } = useI18n()
const stage = computed(() => props.row.stage!)
// detailsRendered 记录步骤列表是否展开过，未展开的列表不渲染
const detailsRendered = ref(false)

// stateKeys 是阶段结束状态的显示文案键
const stateKeys: Record<Exclude<LogStageState, 'running'>, TranslationKey> = {
  completed: 'state.completed',
  failed: 'state.failed',
  cancelled: 'state.cancelled',
}

// progress 是阶段当前步骤与总步骤数
const progress = computed(() => `${stage.value.step}/${stage.value.steps}`)

// renderDetails 在步骤列表首次展开时开始渲染内容
function renderDetails(event: Event): void {
  if ((event.target as HTMLDetailsElement).open) detailsRendered.value = true
}
</script>

<template>
  <div class="stage-log">
    <div class="stage-heading">
      <span class="stage-state" :class="`state-${stage.state}`">{{
        stage.state === 'running' ? progress : t(stateKeys[stage.state])
      }}</span>
      <strong>{{ logText(stage.name) }}</strong>
      <span v-if="stage.state !== 'running'" class="stage-progress">{{ progress }}</span>
    </div>
    <p
      v-if="stage.detail"
      class="stage-detail"
      :class="{ 'stage-error': stage.state === 'failed' }"
    >
      {{ logText(stage.detail) }}
    </p>
    <details class="stage-details" @toggle="renderDetails">
      <summary>
        <UiIcon class="stage-disclosure-icon" name="chevronRight" :size="12" />
        {{ t('logs.details') }}
      </summary>
      <ol v-if="detailsRendered" class="stage-timeline">
        <li v-for="(event, index) in row.events" :key="index">
          <time>{{ new Date(event.time).toLocaleTimeString(locale, { hour12: false }) }}</time>
          <span>{{ logText(event.message) }}</span>
        </li>
      </ol>
    </details>
  </div>
</template>

<style scoped>
.stage-log {
  padding: 4px 0 6px;
}
.stage-heading {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 12px;
}
.stage-heading strong {
  color: var(--color-gray-200);
  font-weight: 600;
  overflow-wrap: anywhere;
}
.stage-state {
  border: 1px solid currentColor;
  border-radius: 4px;
  padding: 0 6px;
  font-size: 13px;
  line-height: 19px;
  font-variant-numeric: tabular-nums;
}
.state-running {
  color: var(--color-blue-400);
}
.state-completed {
  color: var(--color-green-400);
  background: color-mix(in oklab, var(--color-green-400) 8%, transparent);
}
.state-cancelled {
  color: var(--color-yellow-400);
}
.state-failed,
.stage-error {
  color: var(--color-red-400);
}
.stage-progress {
  color: var(--color-gray-400);
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}
.stage-detail {
  margin: 4px 0 0;
  color: var(--color-fg);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.stage-details {
  margin-top: 5px;
  color: var(--color-gray-400);
  font-size: 13px;
}
.stage-details summary {
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
.stage-details summary::-webkit-details-marker {
  display: none;
}
.stage-details summary:hover {
  color: var(--color-fg);
  background: color-mix(in oklab, var(--color-line) 40%, transparent);
}
.stage-details summary:focus-visible {
  outline: 1px solid var(--color-blue-400);
  outline-offset: 2px;
}
.stage-disclosure-icon {
  color: var(--color-gray-500);
  transition: transform 120ms ease;
}
details[open] > summary > .stage-disclosure-icon {
  transform: rotate(90deg);
}
@media (pointer: coarse) {
  .stage-details summary {
    min-height: 40px;
  }
}
@media (prefers-reduced-motion: reduce) {
  .stage-details summary,
  .stage-disclosure-icon {
    transition: none;
  }
}
.stage-timeline {
  border-left: 1px solid var(--color-line);
  padding-left: 12px;
  margin: 10px 0;
}
.stage-timeline li {
  display: flex;
  align-items: baseline;
  gap: 12px;
  margin-bottom: 4px;
}
.stage-timeline time {
  flex-shrink: 0;
  color: var(--color-gray-500);
}
.stage-timeline span {
  min-width: 0;
  overflow-wrap: anywhere;
}
</style>
