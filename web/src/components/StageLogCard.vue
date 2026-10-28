<script setup lang="ts">
import { computed } from 'vue'
import { useI18n, type TranslationKey } from '@/i18n'
import type { LogRow, LogStageState } from '@/logs'
import LogCollapse from './LogCollapse.vue'
import LogDisclosure from './LogDisclosure.vue'

const props = defineProps<{ row: LogRow }>()
const { t, locale, logText } = useI18n()
const stage = computed(() => props.row.stage!)
// open 是步骤列表是否展开，由日志页按行保存
const open = defineModel<boolean>('open', { required: true })

// stateKeys 是阶段结束状态的显示文案键
const stateKeys: Record<Exclude<LogStageState, 'running'>, TranslationKey> = {
  completed: 'state.completed',
  failed: 'state.failed',
  cancelled: 'state.cancelled',
}

// progress 是阶段当前步骤与总步骤数
const progress = computed(() => `${stage.value.step}/${stage.value.steps}`)
</script>

<template>
  <div class="stage-log">
    <div class="stage-heading">
      <LogDisclosure v-model="open" />
      <span class="stage-state" :class="`state-${stage.state}`">{{
        stage.state === 'running' ? progress : t(stateKeys[stage.state])
      }}</span>
      <strong>{{ logText(stage.name) }}</strong>
      <span v-if="stage.state !== 'running'" class="stage-progress">{{ progress }}</span>
      <span
        v-if="stage.detail"
        class="stage-detail"
        :class="{ 'stage-error': stage.state === 'failed' }"
        >{{ logText(stage.detail) }}</span
      >
    </div>
    <LogCollapse :open="open">
      <ol class="stage-timeline">
        <li v-for="(event, index) in row.events" :key="index">
          <time>{{ new Date(event.time).toLocaleTimeString(locale, { hour12: false }) }}</time>
          <span>{{ logText(event.message) }}</span>
        </li>
      </ol>
    </LogCollapse>
  </div>
</template>

<style scoped>
.stage-heading {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 2px 10px;
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
  line-height: 18px;
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
  min-width: 0;
  color: var(--color-gray-400);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.stage-timeline {
  border-left: 1px solid var(--color-line);
  padding-left: 12px;
  margin: 6px 0 6px 6px;
  color: var(--color-gray-400);
  font-size: 13px;
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
