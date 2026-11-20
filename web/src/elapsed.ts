import { onScopeDispose, ref, watch, type Ref } from 'vue'
import { formatLogNumber } from '@/logs'

// useSecondClock 返回 active 为真期间每秒刷新的 Date.now() 毫秒时刻，active 变化时立即刷新一次
export function useSecondClock(active: () => boolean): Readonly<Ref<number>> {
  const now = ref(Date.now())
  let timer: number | undefined
  watch(
    active,
    (running) => {
      window.clearInterval(timer)
      now.value = Date.now()
      timer = running ? window.setInterval(() => (now.value = Date.now()), 1000) : undefined
    },
    { immediate: true },
  )
  onScopeDispose(() => window.clearInterval(timer))
  return now
}

// elapsedText 返回从 start 到 now 的已用整秒数按 locale 格式化的文字
// 约束：start 与 now 为同一时钟的毫秒时刻；start 晚于 now 时为 0 秒
export function elapsedText(start: number, now: number, locale: string): string {
  return formatLogNumber(Math.floor(Math.max(0, now - start) / 1000), locale, 0, 'second')
}

// serverOffset 是服务端时钟减本机时钟的毫秒数，取各份状态中最大的差值：状态在途中耽搁只会让差值变小
let serverOffset: number | undefined

// syncServerClock 以一份服务状态的生成时刻校准服务端与本机的时钟差
export function syncServerClock(time: string): void {
  serverOffset = Math.max(serverOffset ?? -Infinity, Date.parse(time) - Date.now())
}

// serverTime 把服务端给出的时刻换算成本机时钟的毫秒时刻
export function serverTime(time: string): number {
  return Date.parse(time) - (serverOffset ?? 0)
}
