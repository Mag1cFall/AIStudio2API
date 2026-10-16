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
  color: #e6edf3;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.stage-state {
  border: 1px solid currentColor;
  border-radius: 4px;
  padding: 0 6px;
  font-size: 11px;
  line-height: 19px;
  font-variant-numeric: tabular-nums;
}
.state-running {
  color: #79c0ff;
}
.state-completed {
  color: #56d4a0;
  background: #56d4a00c;
}
.state-cancelled {
  color: #e3b341;
}
.state-failed,
.stage-error {
  color: #ff7b72;
}
.stage-progress {
  color: #8b949e;
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
.stage-detail {
  margin: 4px 0 0;
  color: #c9d1d9;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.stage-details {
  margin-top: 5px;
  color: #8b949e;
  font-size: 11px;
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
  color: #c9d1d9;
  background: #30363d66;
}
.stage-details summary:focus-visible {
  outline: 1px solid #79c0ff;
  outline-offset: 2px;
}
.stage-disclosure-icon {
  color: #6e7681;
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
  border-left: 1px solid #30363d;
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
  color: #6e7681;
}
.stage-timeline span {
  min-width: 0;
  overflow-wrap: anywhere;
}
</style>
