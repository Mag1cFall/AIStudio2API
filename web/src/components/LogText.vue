<script setup lang="ts">
import { computed, onMounted, onUnmounted, useTemplateRef, watch } from 'vue'
import { useClampToggle } from '@/clamp'
import { useI18n } from '@/i18n'
import { formatLogDuration, isBlockField, logDuration } from '@/logs'
import type { AdminLog } from '@/types'
import LogLine from './LogLine.vue'
import UiDisclosure from './UiDisclosure.vue'

const props = defineProps<{ entry: AdminLog }>()
const { locale } = useI18n()
// open 是是否显示全文，由日志页按行保存
const open = defineModel<boolean>('open', { required: true })
// clipped 是折叠时是否截掉过内容，由日志页按行保存
const clipped = defineModel<boolean>('clipped', { required: true })
const root = useTemplateRef<HTMLElement>('root')
const body = useTemplateRef<HTMLElement>('body')
// lines 是折叠时显示的行数：消息与短字段两行，带另起一行的长字段时三行
const lines = props.entry.fields?.some(isBlockField) ? 3 : 2
// clamped 是消息当前是否折叠，收起过渡结束后才折叠
const clamped = useClampToggle(body, open, lines)
// duration 是日志的耗时，显示在行尾
const duration = computed(() => {
  const ms = logDuration(props.entry)
  return ms === undefined ? '' : formatLogDuration(ms, locale.value)
})

// measure 在折叠状态下判断折叠行数之外是否还有内容，判断为有之后不再改回
function measure(): void {
  const element = body.value!
  if (clamped.value && element.scrollHeight > element.clientHeight) clipped.value = true
}

const resize = new ResizeObserver(measure)
onMounted(() => resize.observe(root.value!))
onUnmounted(() => resize.disconnect())
watch(locale, measure, { flush: 'post' })
</script>

<template>
  <div ref="root" class="log-op log-text">
    <UiDisclosure v-if="clipped" v-model="open" />
    <span
      class="log-state log-state-event"
      :class="`log-state-${entry.level.toLowerCase()}`"
      aria-hidden="true"
    ></span>
    <span
      ref="body"
      class="log-op-main log-text-body"
      :class="{ 'log-text-clamp': clamped }"
      :style="{ '--log-lines': lines }"
      ><LogLine :entry="entry" :duration="false" title
    /></span>
    <span class="log-op-duration">{{ duration }}</span>
  </div>
</template>

<style scoped>
.log-text-clamp {
  display: -webkit-box;
  overflow: hidden;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: var(--log-lines);
  line-clamp: var(--log-lines);
}
</style>
