import { nextTick, ref, watch, type Ref } from 'vue'
import { reducedMotion, standardDuration, standardEasing, type CollapseResize } from '@/motion'

// useClampToggle 管理截断为 lines 行的正文的展开收起：open 变化时以展开收起的时长与缓动过渡正文高度并向上派发 collapse-resize 事件，收起时在过渡结束后才截断；返回正文当前是否截断
export function useClampToggle(
  body: Readonly<Ref<HTMLElement | null>>,
  open: Ref<boolean>,
  lines: number,
): Ref<boolean> {
  const clamped = ref(!open.value)
  // running 是进行中的展开或收起过渡
  let running: Animation | undefined
  watch(open, async (next) => {
    running?.cancel()
    const element = body.value
    // 正文已随所在提示关闭而卸载时只更新截断状态
    if (element === null) {
      clamped.value = !next
      return
    }
    const from = element.offsetHeight
    clamped.value = false
    await nextTick()
    const to = next
      ? element.offsetHeight
      : lines * parseFloat(getComputedStyle(element).lineHeight)
    const duration = reducedMotion() ? 0 : standardDuration
    element.dispatchEvent(
      new CustomEvent<CollapseResize>('collapse-resize', {
        bubbles: true,
        detail: { delta: to - from, duration },
      }),
    )
    if (duration === 0) {
      clamped.value = !next
      return
    }
    element.style.overflow = 'hidden'
    running = element.animate([{ height: `${from}px` }, { height: `${to}px` }], {
      duration,
      easing: standardEasing,
    })
    running.onfinish = () => {
      element.style.overflow = ''
      running = undefined
      clamped.value = !next
    }
  })
  return clamped
}
