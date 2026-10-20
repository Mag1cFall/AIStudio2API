import { computed, onScopeDispose, ref, watch, type ComputedRef } from 'vue'

// revealInterval 是官网逐字揭示的间隔，与 60 Hz 帧同步
const revealInterval = 1000 / 60

// useReveal 与官网一致逐帧揭示流式文本：每次前进 step 个字符，step 向未显示长度的 1/80 平滑收敛；生成中开始揭示，生成结束且全部揭示后显示全文
export function useReveal(text: () => string, live: () => boolean): ComputedRef<string> {
  const shown = ref(0)
  // animating 表示正在逐帧揭示
  const animating = ref(live())
  let step = 1
  let ticks = 0
  let timer: number | undefined

  // tick 揭示下一段并按剩余长度调整步长
  function tick(): void {
    const length = text().length
    shown.value = Math.min(shown.value + step, length)
    ticks += 1
    const weight = 1 / (1 + ticks)
    step = Math.ceil(
      weight * Math.ceil(Math.max(0, length - shown.value) / 80) + step * (1 - weight),
    )
    timer = shown.value < length ? window.setTimeout(tick, revealInterval) : undefined
    if (timer === undefined && !live()) animating.value = false
  }

  watch(
    [text, live],
    () => {
      if (live()) animating.value = true
      if (!animating.value || timer !== undefined) return
      if (shown.value < text().length) tick()
      else if (!live()) animating.value = false
    },
    { immediate: true },
  )
  onScopeDispose(() => window.clearTimeout(timer))
  return computed(() => (animating.value ? text().slice(0, shown.value) : text()))
}
