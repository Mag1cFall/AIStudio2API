<script setup lang="ts">
import { computed } from 'vue'
import { useI18n, type TranslationKey } from '@/i18n'
import {
  formatLogDuration,
  isBlockField,
  logDuration,
  type LogRow,
  type LogStageState,
} from '@/logs'
import LogLine from './LogLine.vue'
import UiCollapse from './UiCollapse.vue'
import UiDisclosure from './UiDisclosure.vue'

const props = defineProps<{ row: LogRow }>()
const { t, locale, logText } = useI18n()
const stage = computed(() => props.row.stage!)
// open 是步骤列表是否展开，由日志页按行保存
const open = defineModel<boolean>('open', { required: true })

// stateKeys 是阶段状态的显示文案键
const stateKeys: Record<LogStageState, TranslationKey> = {
  running: 'state.running',
  completed: 'state.completed',
  failed: 'state.failed',
  cancelled: 'state.cancelled',
}

// progress 是阶段当前步骤与总步骤数，阶段没有编号的步骤时为空
const progress = computed(() =>
  stage.value.steps > 0 ? `${stage.value.step}/${stage.value.steps}` : '',
)
// latest 是阶段最近一条日志，结束后为结束日志
const latest = computed(() => props.row.events.at(-1)!)
// ended 是阶段是否已结束，结束后标题行只显示结束日志的字段
const ended = computed(() => stage.value.state !== 'running')
// blocks 是最近一条日志是否带另起一行的长字段
const blocks = computed(() => latest.value.fields?.some(isBlockField) ?? false)
// duration 是最近一条日志的耗时，显示在行尾
const duration = computed(() => {
  const ms = logDuration(latest.value)
  return ms === undefined ? '' : formatLogDuration(ms, locale.value)
})
</script>

<template>
  <div class="log-op log-op-stage">
    <UiDisclosure v-model="open" />
    <span class="log-state" :class="`log-state-${stage.state}`">{{
      stage.state === 'running' && progress ? progress : t(stateKeys[stage.state])
    }}</span>
    <span class="log-op-main"
      ><strong class="log-op-title">{{ logText(stage.name) }}</strong
      ><span v-if="ended && progress" class="stage-progress">{{ progress }}</span
      ><span class="stage-detail"
        ><LogLine :entry="latest" :message="!ended" part="inline" :duration="false" /></span
    ></span>
    <span class="log-op-duration">{{ duration }}</span>
    <p v-if="blocks" class="log-op-row stage-blocks">
      <LogLine :entry="latest" :message="false" part="block" />
    </p>
    <UiCollapse class="log-op-row log-op-detail" :open="open">
      <ol class="stage-timeline">
        <li v-for="(event, index) in row.events" :key="index">
          <time class="font-mono">{{
            new Date(event.time).toLocaleTimeString(locale, { hour12: false })
          }}</time>
          <span><LogLine :entry="event" /></span>
        </li>
      </ol>
    </UiCollapse>
  </div>
</template>

<style scoped>
.log-op-title {
  margin-inline-end: 8px;
}
.stage-progress {
  margin-inline-end: 8px;
  color: var(--color-gray-500);
  font-variant-numeric: tabular-nums;
}
.stage-detail :deep(.log-line-message) {
  color: var(--color-gray-300);
}
/* stage-blocks 为最近一步另起一行的错误等长字段，最多两行，全文在步骤列表中 */
.stage-blocks {
  display: -webkit-box;
  overflow: hidden;
  margin: 2px 0 0;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
/* stage-timeline 从行首开始，左边线对齐展开箭头的中线 */
.stage-timeline {
  border-left: 1px solid var(--color-line);
  padding-left: 12px;
  margin: 6px 0 6px 9px;
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
  font-variant-numeric: tabular-nums;
}
.stage-timeline span {
  min-width: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
