<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import { useI18n } from '@/i18n'
import UiIcon from './UiIcon.vue'

defineProps<{ label: string }>()

const { t } = useI18n()
const open = ref(false)
// pinned 表示说明由点击打开，指针移开时保持显示
const pinned = ref(false)
// pinning 表示本次点击落在悬停打开的说明的问号上，这次点击固定说明
let pinning = false
let timer = 0

// hover 在指针进入后稍作延迟打开说明，指针离开后稍作延迟关闭，点击打开的说明不随指针关闭
function hover(entering: boolean): void {
  window.clearTimeout(timer)
  if (pinned.value) return
  timer = window.setTimeout(() => (open.value = entering), entering ? 150 : 120)
}

// armPin 在问号的点击事件到达 Popover 前记录说明是否由悬停打开
function armPin(): void {
  pinning = open.value && !pinned.value
}

// toggle 点击问号时固定打开或关闭说明，悬停打开时的点击固定说明
function toggle(value: boolean): void {
  window.clearTimeout(timer)
  if (pinning) {
    pinning = false
    pinned.value = true
    return
  }
  pinned.value = value
  open.value = value
}

onBeforeUnmount(() => window.clearTimeout(timer))
</script>

<!-- HelpHint 是字段名旁的说明入口：所在行悬停或聚焦时显示，悬停或点击打开说明，点击后保持打开 -->
<template>
  <PopoverRoot :open="open" @update:open="toggle">
    <PopoverTrigger
      type="button"
      class="flex shrink-0 items-center rounded-full text-gray-500 opacity-0 transition-opacity group-hover/row:opacity-100 hover:text-gray-200 focus-visible:text-gray-200 focus-visible:opacity-100 focus-visible:outline-none data-[state=open]:text-gray-200 data-[state=open]:opacity-100 [@media(hover:none)]:opacity-100"
      :aria-label="t('settings.helpFor').replace('{label}', label)"
      @click.capture="armPin"
      @mouseenter="hover(true)"
      @mouseleave="hover(false)"
    >
      <UiIcon name="help" :size="14" />
    </PopoverTrigger>
    <PopoverPortal>
      <PopoverContent
        side="top"
        align="start"
        :side-offset="6"
        :collision-padding="12"
        class="ui-popover z-[80] w-72 max-w-[var(--reka-popover-content-available-width)] rounded-lg border border-line bg-panel p-3 text-xs leading-5 text-gray-300 shadow-xl shadow-black/40"
        @mouseenter="hover(true)"
        @mouseleave="hover(false)"
        @open-auto-focus.prevent
        @close-auto-focus.prevent
      >
        <slot />
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>
