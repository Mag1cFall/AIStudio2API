<script setup lang="ts">
import { onActivated, onBeforeUnmount, onDeactivated, ref, useTemplateRef, watch } from 'vue'

// active 为真时开始倒计时，到时发出 dismiss
const props = defineProps<{ active: boolean }>()
const emit = defineEmits<{ dismiss: [] }>()

// duration 是自动关闭前的显示时长，单位 ms
const duration = 12000
const bar = useTemplateRef<HTMLElement>('bar')
const hovered = ref(false)
const focused = ref(false)
// shown 是所在页面是否显示，切到管理端其他页面时为假
const shown = ref(true)
const visible = ref(document.visibilityState === 'visible')
let animation: Animation | undefined

// sync 在悬停、聚焦、页面不可见或所在页面切走时暂停倒计时，其余时候继续
function sync(): void {
  if (animation === undefined) return
  if (hovered.value || focused.value || !visible.value || !shown.value) animation.pause()
  else animation.play()
}

watch(
  [() => props.active, bar],
  ([active, element]) => {
    if (!active || element === null || animation !== undefined) return
    animation = element.animate([{ transform: 'scaleX(1)' }, { transform: 'scaleX(0)' }], {
      duration,
      easing: 'linear',
      fill: 'forwards',
    })
    animation.onfinish = () => emit('dismiss')
    sync()
  },
  { flush: 'post', immediate: true },
)
watch([hovered, focused, visible, shown], sync)

// onFocusOut 在焦点移出整行时结束聚焦
function onFocusOut(event: FocusEvent): void {
  focused.value = (event.currentTarget as HTMLElement).contains(event.relatedTarget as Node | null)
}

// onVisibility 记录浏览器标签页是否可见
function onVisibility(): void {
  visible.value = document.visibilityState === 'visible'
}
document.addEventListener('visibilitychange', onVisibility)
onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', onVisibility)
  animation?.cancel()
})
onActivated(() => (shown.value = true))
onDeactivated(() => (shown.value = false))
</script>

<template>
  <li
    class="relative"
    @pointerenter="hovered = true"
    @pointerleave="hovered = false"
    @focusin="focused = true"
    @focusout="onFocusOut"
  >
    <slot />
    <span
      v-if="active"
      ref="bar"
      class="absolute inset-x-0 bottom-0 h-0.5 origin-left bg-blue-400/60"
      aria-hidden="true"
    ></span>
  </li>
</template>
