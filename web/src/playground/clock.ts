import { onUnmounted, ref, watch, type Ref } from 'vue'

// useRunningClock 返回生成期间每 100 ms 刷新的 performance.now() 时刻，轮次耗时与官网一样随之计数
export function useRunningClock(running: () => boolean): Ref<number> {
  const now = ref(performance.now())
  let ticker = 0
  watch(
    running,
    (value) => {
      window.clearInterval(ticker)
      if (!value) return
      now.value = performance.now()
      ticker = window.setInterval(() => (now.value = performance.now()), 100)
    },
    { immediate: true },
  )
  onUnmounted(() => window.clearInterval(ticker))
  return now
}
