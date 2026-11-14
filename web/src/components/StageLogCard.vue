<script setup lang="ts">
import { computed } from 'vue'
import { useI18n, type TranslationKey } from '@/i18n'
import { isBlockField, type LogRow, type LogStageState } from '@/logs'
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
</script>

<template>
  <div class="stage-log">
    <div class="stage-heading">
      <UiDisclosure v-model="open" />
      <span class="stage-state" :class="`state-${stage.state}`">{{
        stage.state === 'running' && progress ? progress : t(stateKeys[stage.state])
      }}</span>
      <strong>{{ logText(stage.name) }}</strong>
      <span v-if="ended && progress" class="stage-progress">{{ progress }}</span>
      <span class="stage-detail"><LogLine :entry="latest" :message="!ended" part="inline" /></span>
    </div>
    <p v-if="blocks" class="stage-blocks">
      <LogLine :entry="latest" :message="false" part="block" />
    </p>
    <UiCollapse :open="open">
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
.stage-heading {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 2px 10px;
}
.stage-heading :deep(.ui-disclosure) {
  align-self: center;
}
.stage-heading strong {
  color: var(--color-fg);
  font-weight: 600;
  overflow-wrap: anywhere;
}
.stage-state {
  align-self: center;
  border: 1px solid currentColor;
  border-radius: 4px;
  padding: 0 6px;
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
.state-failed {
  color: var(--color-red-400);
}
.stage-progress {
  color: var(--color-gray-500);
  font-variant-numeric: tabular-nums;
}
/* stage-detail 为最近一步的消息与短字段，最多两行，全文在步骤列表中 */
.stage-detail {
  display: -webkit-box;
  overflow: hidden;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  min-width: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
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
.stage-timeline {
  border-left: 1px solid var(--color-line);
  padding-left: 12px;
  margin: 6px 0 6px 6px;
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
