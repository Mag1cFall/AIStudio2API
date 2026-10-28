<script setup lang="ts">
import { MODE_DRAWS, resolvePreset, type OrbSize, type OrbState } from 'thinking-orbs/engine'
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { useTheme } from '@/theme'

// ThinkingOrb 是官网 thinking-orb 的点阵球动画
const props = withDefaults(defineProps<{ state: OrbState; size?: OrbSize }>(), {
  size: 64,
})
const canvas = ref<HTMLCanvasElement>()
const { theme } = useTheme()
let stop: (() => void) | undefined

// start 按状态、尺寸与配色逐帧绘制，离开视口或页面隐藏时暂停
function start(): void {
  stop?.()
  stop = undefined
  const element = canvas.value
  const context = element?.getContext('2d')
  if (!element || !context) return
  const size = props.size
  const ratio = Math.min(2, window.devicePixelRatio || 1)
  element.width = Math.round(size * ratio)
  element.height = Math.round(size * ratio)
  const { mode, speed, opts } = resolvePreset(props.state, size)
  const dark = theme.value === 'dark'
  const paint = (time: number): void => {
    context.setTransform(ratio, 0, 0, ratio, 0, 0)
    context.clearRect(0, 0, size, size)
    MODE_DRAWS[mode](context, size, time, dark, opts)
  }
  if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
    paint(0.6)
    return
  }
  let frame = 0
  let running = false
  let visible = true
  const tick = (): void => {
    paint((performance.now() / 1000) * speed)
    if (running) frame = requestAnimationFrame(tick)
  }
  const resume = (): void => {
    if (running) return
    running = true
    frame = requestAnimationFrame(tick)
  }
  const pause = (): void => {
    running = false
    cancelAnimationFrame(frame)
  }
  const observer = new IntersectionObserver(([entry]) => {
    visible = entry?.isIntersecting ?? false
    if (visible && document.visibilityState !== 'hidden') resume()
    else pause()
  })
  const onVisibility = (): void => {
    if (document.visibilityState === 'hidden') pause()
    else if (visible) resume()
  }
  paint((performance.now() / 1000) * speed)
  observer.observe(element)
  document.addEventListener('visibilitychange', onVisibility)
  stop = () => {
    pause()
    observer.disconnect()
    document.removeEventListener('visibilitychange', onVisibility)
  }
}

onMounted(start)
watch([() => props.state, () => props.size, theme], start)
onUnmounted(() => stop?.())
</script>

<template>
  <canvas
    ref="canvas"
    class="thinking-orb"
    aria-hidden="true"
    :style="{ width: `${size}px`, height: `${size}px` }"
  ></canvas>
</template>

<style scoped>
.thinking-orb {
  display: block;
}
</style>
