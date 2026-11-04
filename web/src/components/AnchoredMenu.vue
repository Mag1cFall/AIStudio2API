<script lang="ts">
import { ref, shallowRef } from 'vue'

// useAnchoredMenu 记录共用菜单所属的列表项与触发按钮，再次点击同一按钮时关闭
export function useAnchoredMenu<T>() {
  const open = ref(false)
  const target = shallowRef<{ item: T; anchor: HTMLElement }>()
  function toggle(event: MouseEvent, item: T): void {
    const anchor = event.currentTarget as HTMLElement
    open.value = !(open.value && target.value?.anchor === anchor)
    target.value = { item, anchor }
  }
  return { open, target, toggle }
}
</script>

<script setup lang="ts">
import { DropdownMenuContent, DropdownMenuPortal, DropdownMenuRoot } from 'reka-ui'

// AnchoredMenu 是列表各行共用的下拉菜单，与官网 mat-menu 一样定位到所点击行的触发按钮
defineOptions({ inheritAttrs: false })
const open = defineModel<boolean>('open', { required: true })
const props = defineProps<{ anchor: HTMLElement | undefined }>()
// outside 表示菜单因点击其他位置关闭，此时焦点不回到触发按钮
let outside = false

// onInteractOutside 点击触发按钮时由按钮切换开关，点击其他位置时关闭
function onInteractOutside(event: Event): void {
  if (props.anchor?.contains(event.target as Node)) event.preventDefault()
  else outside = true
}

// onCloseAutoFocus 以键盘或菜单项关闭时焦点回到触发按钮
function onCloseAutoFocus(event: Event): void {
  event.preventDefault()
  if (!outside) props.anchor?.focus()
  outside = false
}
</script>

<template>
  <DropdownMenuRoot v-if="anchor" v-model:open="open" :modal="false">
    <DropdownMenuPortal>
      <DropdownMenuContent
        v-bind="$attrs"
        :reference="anchor"
        @interact-outside="onInteractOutside"
        @close-auto-focus="onCloseAutoFocus"
      >
        <slot />
      </DropdownMenuContent>
    </DropdownMenuPortal>
  </DropdownMenuRoot>
</template>
