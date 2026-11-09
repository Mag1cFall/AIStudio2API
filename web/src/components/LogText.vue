<script setup lang="ts">
import { computed, onMounted, onUnmounted, useTemplateRef, watch } from 'vue'
import { useClampToggle } from '@/clamp'
import { useI18n } from '@/i18n'
import UiDisclosure from './UiDisclosure.vue'

const props = defineProps<{ message: string }>()
const { logText } = useI18n()
// text 是按界面语言显示的消息，服务文案翻译表到达后随之更新
const text = computed(() => logText(props.message))
// open 是是否显示全文，由日志页按行保存
const open = defineModel<boolean>('open', { required: true })
// clipped 是折叠为两行时是否截掉过内容，由日志页按行保存
const clipped = defineModel<boolean>('clipped', { required: true })
const root = useTemplateRef<HTMLElement>('root')
const body = useTemplateRef<HTMLElement>('body')
// clamped 是消息当前是否折叠为两行，收起过渡结束后才折叠
const clamped = useClampToggle(body, open, 2)

// measure 在折叠状态下判断两行之外是否还有内容，判断为有之后不再改回
function measure(): void {
  const element = body.value!
  if (clamped.value && element.scrollHeight > element.clientHeight) clipped.value = true
}

const resize = new ResizeObserver(measure)
onMounted(() => resize.observe(root.value!))
onUnmounted(() => resize.disconnect())
watch(text, measure, { flush: 'post' })
</script>

<template>
  <div ref="root" class="log-text">
    <span ref="body" class="log-text-body" :class="{ 'log-text-clamp': clamped }">{{ text }}</span>
    <UiDisclosure v-if="clipped" v-model="open" />
  </div>
</template>

<style scoped>
.log-text {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}
.log-text-body {
  display: block;
  flex: 1;
  min-width: 0;
}
.log-text-clamp {
  display: -webkit-box;
  overflow: hidden;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
}
</style>
