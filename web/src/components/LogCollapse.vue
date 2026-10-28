<script setup lang="ts">
import type { LogResize } from '@/logs'
import { reducedMotion, standardDuration, standardEasing } from '@/motion'

defineProps<{ open: boolean }>()

// running 是各元素进行中的高度过渡，反向切换时先取消
const running = new WeakMap<Element, Animation>()

// resize 向上派发详情高度变化，再以缓动过渡高度与透明度
function resize(element: Element, from: number, to: number, done: () => void): void {
  const target = element as HTMLElement
  const duration = reducedMotion() ? 0 : standardDuration
  running.get(target)?.cancel()
  target.dispatchEvent(
    new CustomEvent<LogResize>('log-resize', {
      bubbles: true,
      detail: { delta: to - from, duration },
    }),
  )
  if (duration === 0) {
    done()
    return
  }
  target.style.overflow = 'hidden'
  const animation = target.animate(
    [
      { height: `${from}px`, opacity: from === 0 ? 0 : 1 },
      { height: `${to}px`, opacity: to === 0 ? 0 : 1 },
    ],
    { duration, easing: standardEasing },
  )
  running.set(target, animation)
  animation.onfinish = () => {
    target.style.overflow = ''
    running.delete(target)
    done()
  }
}

function enter(element: Element, done: () => void): void {
  resize(element, 0, (element as HTMLElement).offsetHeight, done)
}

function leave(element: Element, done: () => void): void {
  resize(element, (element as HTMLElement).offsetHeight, 0, done)
}
</script>

<template>
  <Transition :css="false" @enter="enter" @leave="leave">
    <div v-if="open" class="log-collapse"><slot /></div>
  </Transition>
</template>
