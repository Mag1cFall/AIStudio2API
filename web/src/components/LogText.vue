<script setup lang="ts">
import { computed, onMounted, onUnmounted, useTemplateRef, watch } from 'vue'
import { useClampToggle } from '@/clamp'
import { useI18n } from '@/i18n'
import { formatLogDuration, isBlockField, logDuration } from '@/logs'
import type { AdminLog } from '@/types'
import LogLine, { useLogLineText } from './LogLine.vue'
import UiDisclosure from './UiDisclosure.vue'

const props = defineProps<{ entry: AdminLog }>()
const { locale } = useI18n()
const formatLine = useLogLineText()
// open 是是否显示全文，由日志页按行保存
const open = defineModel<boolean>('open', { required: true })
// clipped 是折叠时是否截掉过内容，由日志页按行保存
const clipped = defineModel<boolean>('clipped', { required: true })
const root = useTemplateRef<HTMLElement>('root')
const body = useTemplateRef<HTMLElement>('body')
const main = useTemplateRef<HTMLElement>('main')
const reason = useTemplateRef<HTMLElement>('reason')
// hasBlock 判断日志是否带有另起一行的错误或原因字段
const hasBlock = computed(() => props.entry.fields?.some(isBlockField) ?? false)
// blockCount 是错误或原因等长字段的数量
const blockCount = computed(() => props.entry.fields?.filter(isBlockField).length ?? 0)
// clamped 是消息当前是否折叠，收起过渡结束后才折叠；带原因与无原因的行收起时都最多两行
const clamped = useClampToggle(body, open, 2)
// duration 是日志的耗时，显示在行尾
const duration = computed(() => {
  const ms = logDuration(props.entry)
  return ms === undefined ? '' : formatLogDuration(ms, locale.value)
})
const mainTooltip = computed(() =>
  hasBlock.value ? formatLine(props.entry, { part: 'inline', duration: false }) : '',
)
const reasonTooltip = computed(() =>
  hasBlock.value ? formatLine(props.entry, { message: false, part: 'block' }) : '',
)
const fullTooltip = computed(() =>
  hasBlock.value ? '' : formatLine(props.entry, { duration: false }),
)

// measure 在折叠状态下判断两行之外是否还有内容，判断为有之后不再改回
function measure(): void {
  if (!clamped.value) return
  if (hasBlock.value) {
    const mainEl = main.value!
    const reasonEl = reason.value!
    if (
      mainEl.scrollWidth > mainEl.clientWidth ||
      reasonEl.scrollWidth > reasonEl.clientWidth ||
      reasonEl.scrollHeight > reasonEl.clientHeight + 1 ||
      blockCount.value > 1 ||
      mainTooltip.value.includes('\n') ||
      reasonTooltip.value.includes('\n')
    )
      clipped.value = true
    return
  }
  const element = body.value!
  if (element.scrollHeight > element.clientHeight + 1) clipped.value = true
}

const resize = new ResizeObserver(measure)
onMounted(() => resize.observe(root.value!))
onUnmounted(() => resize.disconnect())
watch(locale, measure, { flush: 'post' })
</script>

<template>
  <div ref="root" class="log-op log-text">
    <UiDisclosure v-if="clipped" v-model="open" />
    <div
      v-if="hasBlock"
      ref="body"
      class="log-op-main log-text-body log-text-split"
      :class="{ 'log-text-clamped': clamped }"
    >
      <div ref="main" v-tooltip.overflow="mainTooltip" class="log-text-main">
        <LogLine :entry="entry" part="inline" :duration="false" title />
      </div>
      <div ref="reason" v-tooltip.overflow="reasonTooltip" class="log-text-reason">
        <LogLine :entry="entry" :message="false" part="block" />
      </div>
    </div>
    <span
      v-else
      ref="body"
      v-tooltip.overflow="fullTooltip"
      class="log-op-main log-text-body"
      :class="{ 'log-text-clamp': clamped }"
      :style="{ '--log-lines': 2 }"
      ><LogLine :entry="entry" :duration="false" title
    /></span>
    <span class="log-op-duration">{{ duration }}</span>
  </div>
</template>

<style scoped>
.log-text-body {
  grid-column: 2 / 4;
}
.log-text-clamp {
  display: -webkit-box;
  overflow: hidden;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: var(--log-lines);
  line-clamp: var(--log-lines);
}
.log-text-clamped .log-text-main,
.log-text-clamped .log-text-reason {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.log-text-clamped .log-text-reason :deep(.log-field-block) {
  display: inline;
  margin-inline-end: 8px;
}
</style>
