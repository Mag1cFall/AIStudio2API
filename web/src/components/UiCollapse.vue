<script setup lang="ts">
import { collapseKeyframes, reducedMotion, standardDuration, standardEasing } from '@/motion'
import type { CollapseResize } from '@/motion'

const props = defineProps<{ open: boolean; instant?: boolean }>()
const emit = defineEmits<{ closed: [] }>()

// running 是各元素进行中的高度过渡，反向切换时先取消
const running = new WeakMap<Element, Animation>()

// resize 向上派发内容区高度变化，再以缓动过渡高度，展开时内容在后段淡入、收起时在前段淡出
function resize(element: Element, from: number, to: number, done: () => void): void {
  const target = element as HTMLElement
  const duration = props.instant || reducedMotion() ? 0 : standardDuration
  running.get(target)?.cancel()
  target.dispatchEvent(
    new CustomEvent<CollapseResize>('collapse-resize', {
      bubbles: true,
      detail: { delta: to - from, duration },
    }),
  )
  if (duration === 0) {
    done()
    return
  }
  target.style.overflow = 'hidden'
  const animation = target.animate(collapseKeyframes(from, to), {
    duration,
    easing: standardEasing,
  })
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
  <Transition :css="false" @enter="enter" @leave="leave" @after-leave="emit('closed')">
    <div v-if="open" class="ui-collapse"><slot /></div>
  </Transition>
</template>

<style scoped>
/* ui-collapse 是独立的块格式化上下文，过渡期间与结束后内容的外边距都留在区内，结束时高度不跳变 */
.ui-collapse {
  display: flow-root;
}
</style>
