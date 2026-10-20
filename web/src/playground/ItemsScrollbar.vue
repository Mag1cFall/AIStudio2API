<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import { vTooltip } from '@/playground/ui/tooltip'

// ScrollbarItem 是轮次导航中的一个点：id 为该组第一个用户轮次，label 为提示文字
export interface ScrollbarItem {
  id: string
  label: string
}

const props = defineProps<{
  items: ScrollbarItem[]
  scroller: HTMLElement | undefined
  scrolling: boolean
}>()
const { t } = useI18n()
const host = ref<HTMLElement>()
const track = ref<HTMLElement>()
const handle = ref<HTMLElement>()
const active = ref<string | null>(null)
const handleTop = ref(0)
const handleHeight = ref(0)
const dragging = ref(false)
const jumping = ref(false)
const atTop = ref(true)
const atBottom = ref(false)
let grab = 0
let resize: ResizeObserver | undefined

// buttons 按显示顺序返回导航点按钮
function buttons(): HTMLButtonElement[] {
  return [
    ...(host.value?.querySelectorAll<HTMLButtonElement>('.items-scrollbar-item button') ?? []),
  ]
}

// turnElement 返回导航点对应的轮次元素
function turnElement(id: string): HTMLElement | null {
  return document.getElementById(`turn-${id}`)
}

// center 返回导航点圆心相对轨道顶部的位置
function center(index: number): number | undefined {
  const button = buttons()[index]
  const rail = track.value
  if (button === undefined || rail === undefined) return undefined
  const box = button.getBoundingClientRect()
  return box.top + box.height / 2 - rail.getBoundingClientRect().top
}

// updateActive 与官网一致：顶部时为第一项、底部时为最后一项，否则为视口内顶边最靠上的轮次，视口内没有时取视口上方最近的一项
function updateActive(): void {
  const scroller = props.scroller
  const items = props.items
  if (scroller === undefined || items.length === 0) {
    active.value = null
    return
  }
  if (scroller.scrollTop <= 0) active.value = items[0]!.id
  else if (scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 1)
    active.value = items.at(-1)!.id
  else {
    const view = scroller.getBoundingClientRect()
    let best: string | null = null
    let above: string | null = null
    let offset = Infinity
    for (const item of items) {
      const rect = turnElement(item.id)?.getBoundingClientRect()
      if (rect === undefined) continue
      if (rect.bottom <= view.top) above = item.id
      if (rect.top < view.bottom && rect.bottom > view.top && rect.top - view.top < offset) {
        offset = rect.top - view.top
        best = item.id
      }
    }
    active.value = best ?? above
  }
}

// updateHandle 按视口占比计算滑块高度，滑块停在当前轮次的点上，没有当前项时按滚动比例放置
function updateHandle(): void {
  const scroller = props.scroller
  const rail = track.value
  if (
    scroller === undefined ||
    rail === undefined ||
    scroller.scrollHeight <= scroller.clientHeight
  ) {
    handleHeight.value = 0
    handleTop.value = 0
    return
  }
  const count = props.items.length
  const first = center(0)
  const last = center(count - 1)
  if (count === 0 || first === undefined || last === undefined) return
  const max = scroller.scrollHeight - scroller.clientHeight
  const ratio = max > 0 ? scroller.scrollTop / max : 0
  if (count === 1) {
    const height = Math.min(
      Math.max((scroller.clientHeight / scroller.scrollHeight) * rail.clientHeight, 80),
      rail.clientHeight,
    )
    handleHeight.value = height
    if (dragging.value || jumping.value) return
    const room = rail.clientHeight - height
    handleTop.value = room > first ? Math.max(first, first + ratio * (room - first)) : first
    return
  }
  const span = last - first
  const height = Math.max((scroller.clientHeight / scroller.scrollHeight) * span, 80)
  handleHeight.value = height
  if (dragging.value || jumping.value) return
  const range = span - height
  const index = props.items.findIndex((item) => item.id === active.value)
  const anchor = index < 0 ? undefined : center(index)
  const snapped =
    anchor === undefined
      ? first + ratio * range
      : index === 0
        ? anchor
        : index === count - 1
          ? anchor - height
          : anchor - height / 2
  handleTop.value = Math.max(first, Math.min(snapped, first + range))
}

// update 刷新当前项、滑块与上下渐隐
function update(): void {
  updateActive()
  updateHandle()
  const element = host.value
  if (element === undefined) return
  const scrollable = element.scrollHeight > element.clientHeight
  atTop.value = !scrollable || element.scrollTop === 0
  atBottom.value =
    !scrollable || element.scrollHeight - element.scrollTop - element.clientHeight < 1
}

// jump 滚动到导航点对应的轮次：第一项到顶部，最后一项到底部，其余把轮次顶边对齐视口
function jump(id: string): void {
  const scroller = props.scroller
  const element = turnElement(id)
  if (scroller === undefined || element === null) return
  const index = props.items.findIndex((item) => item.id === id)
  active.value = id
  jumping.value = true
  scroller.addEventListener(
    'scrollend',
    () => {
      jumping.value = false
      update()
    },
    { once: true },
  )
  if (index === 0) scroller.scrollTo({ top: 0, behavior: 'smooth' })
  else if (index === props.items.length - 1)
    scroller.scrollTo({ top: scroller.scrollHeight, behavior: 'smooth' })
  else element.scrollIntoView({ behavior: 'smooth', block: 'start' })
  const anchor = center(index)
  if (anchor !== undefined)
    handleTop.value =
      index === 0
        ? anchor
        : index === props.items.length - 1
          ? anchor - handleHeight.value
          : anchor - handleHeight.value / 2
  ;(document.activeElement as HTMLElement | null)?.blur()
  scroller.focus({ preventScroll: true })
}

// drag 把指针位置换算为对话滚动位置：第一个点到最后一个点之间对应整个滚动范围
function drag(clientY: number): void {
  const scroller = props.scroller
  const rail = track.value
  if (scroller === undefined || rail === undefined) return
  const first = center(0)
  const last = center(props.items.length - 1)
  if (first === undefined || last === undefined) return
  const top = rail.getBoundingClientRect().top
  const end =
    props.items.length === 1 ? rail.clientHeight - handleHeight.value : last - handleHeight.value
  const position = Math.max(first, Math.min(clientY - top - grab, end))
  handleTop.value = position
  const room = end - first
  scroller.scrollTop =
    (room > 0 ? (position - first) / room : 0) * (scroller.scrollHeight - scroller.clientHeight)
  updateActive()
}

// startDrag 按下滑块开始拖动
function startDrag(event: MouseEvent | TouchEvent): void {
  if (event instanceof MouseEvent && event.button !== 0) return
  event.preventDefault()
  const y = event instanceof MouseEvent ? event.clientY : event.touches[0]!.clientY
  grab = y - (handle.value?.getBoundingClientRect().top ?? y)
  dragging.value = true
  document.body.style.cursor = 'grabbing'
}

// onPointerMove 拖动滑块时跟随指针
function onPointerMove(event: MouseEvent | TouchEvent): void {
  if (!dragging.value) return
  event.preventDefault()
  drag(event instanceof MouseEvent ? event.clientY : event.touches[0]!.clientY)
}

// stopDrag 松开指针结束拖动
function stopDrag(): void {
  if (!dragging.value) return
  dragging.value = false
  document.body.style.cursor = ''
}

// focusItem 聚焦一个导航点，并让它留在导航可见区域内
function focusItem(index: number): void {
  const button = buttons()[index]
  const element = host.value
  if (button === undefined || element === undefined) return
  button.focus({ preventScroll: true })
  const box = element.getBoundingClientRect()
  const rect = button.getBoundingClientRect()
  if (rect.top < box.top + 60) element.scrollTop -= box.top + 60 - rect.top
  else if (rect.bottom > box.bottom - 60) element.scrollTop += rect.bottom - (box.bottom - 60)
}

// focusActive 聚焦当前轮次的导航点
function focusActive(): void {
  update()
  const index = Math.max(
    0,
    props.items.findIndex((item) => item.id === active.value),
  )
  focusItem(index)
}

// onKeydown 方向键在导航点之间移动，回车或空格跳转，Esc 回到对话
function onKeydown(event: KeyboardEvent): void {
  const index = buttons().indexOf(document.activeElement as HTMLButtonElement)
  if (event.key === 'Escape') {
    if (host.value?.contains(document.activeElement)) {
      props.scroller?.focus({ preventScroll: true })
      event.preventDefault()
      event.stopPropagation()
    }
  } else if ((event.key === 'Enter' || event.key === ' ') && index >= 0) {
    event.preventDefault()
    jump(props.items[index]!.id)
  } else if (event.key === 'ArrowUp' || event.key === 'ArrowDown') {
    event.preventDefault()
    if (index < 0) return
    const count = props.items.length
    focusItem(event.key === 'ArrowUp' ? (index - 1 + count) % count : (index + 1) % count)
  }
}

// onFocusIn 焦点进入导航本身时转到当前轮次的点
function onFocusIn(event: FocusEvent): void {
  if (event.target === host.value) focusActive()
}

let frame = 0
// schedule 在下一帧合并刷新
function schedule(): void {
  cancelAnimationFrame(frame)
  frame = requestAnimationFrame(update)
}

watch(
  () => props.scroller,
  (scroller, previous) => {
    previous?.removeEventListener('scroll', schedule)
    resize?.disconnect()
    if (scroller === undefined) return
    scroller.addEventListener('scroll', schedule, { passive: true })
    resize = new ResizeObserver(schedule)
    resize.observe(scroller)
    if (scroller.firstElementChild) resize.observe(scroller.firstElementChild)
    schedule()
  },
  { immediate: true },
)
watch(
  () => props.items.map((item) => item.id).join(),
  () => void nextTick(schedule),
)

onMounted(() => {
  document.addEventListener('mousemove', onPointerMove)
  document.addEventListener('touchmove', onPointerMove, { passive: false })
  document.addEventListener('mouseup', stopDrag)
  document.addEventListener('touchend', stopDrag)
})

onUnmounted(() => {
  props.scroller?.removeEventListener('scroll', schedule)
  resize?.disconnect()
  cancelAnimationFrame(frame)
  document.removeEventListener('mousemove', onPointerMove)
  document.removeEventListener('touchmove', onPointerMove)
  document.removeEventListener('mouseup', stopDrag)
  document.removeEventListener('touchend', stopDrag)
})

defineExpose({ focusActive })
</script>

<template>
  <div id="prompt-scrollbar-kb-hint" class="visually-hidden">{{ t('pg.turnNavigationHint') }}</div>
  <div
    ref="host"
    class="items-scrollbar"
    role="toolbar"
    aria-orientation="vertical"
    aria-describedby="prompt-scrollbar-kb-hint"
    :aria-label="t('pg.turnNavigation')"
    :tabindex="items.length > 0 ? 0 : -1"
    :class="{
      'multiple-items': items.length > 1,
      'is-scrolled-top': atTop,
      'is-scrolled-bottom': atBottom,
      'handle-dragging': dragging,
      'user-scrolling': scrolling,
    }"
    @scroll="update"
    @keydown="onKeydown"
    @mouseenter="update"
    @focusin="onFocusIn"
  >
    <div ref="track" class="scrollbar-track">
      <div class="scrollbar-line"></div>
      <div
        ref="handle"
        class="scrollbar-handle"
        :style="{ top: `${handleTop}px`, height: `${handleHeight}px` }"
        @mousedown="startDrag"
        @touchstart="startDrag"
      ></div>
      <div v-for="item in items" :key="item.id" class="items-scrollbar-item">
        <button
          :id="`scrollbar-item-${item.id}`"
          v-tooltip.left="item.label"
          type="button"
          ms-button
          tabindex="-1"
          class="ms-button-borderless ms-button-icon"
          :class="{ 'ms-button-active': active === item.id }"
          :aria-label="item.label"
          :aria-pressed="active === item.id"
          :aria-controls="`turn-${item.id}`"
          @click="jump(item.id)"
        >
          <div class="items-scrollbar-dot"></div>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}

.items-scrollbar {
  position: absolute;
  z-index: 3;
  top: 0;
  right: 0;
  bottom: 0;
  margin: 0;
  padding: 0;
  background-color: transparent;
  opacity: 0.6;
  overflow-y: auto;
  pointer-events: auto;
  scrollbar-width: none;
  transition: opacity 1s ease-in-out;
}

.items-scrollbar.user-scrolling,
.items-scrollbar:focus-within,
.items-scrollbar:hover {
  opacity: 1;
}

.items-scrollbar::-webkit-scrollbar {
  display: none;
}

.items-scrollbar::before,
.items-scrollbar::after {
  position: sticky;
  z-index: 5;
  left: 0;
  right: 0;
  display: block;
  height: 60px;
  content: '';
  pointer-events: none;
}

.items-scrollbar::before {
  top: -8px;
  margin-bottom: -60px;
  background: linear-gradient(to bottom, var(--color-v3-surface) 3%, transparent);
}

.items-scrollbar::after {
  bottom: -8px;
  margin-top: -60px;
  background: linear-gradient(to top, var(--color-v3-surface) 3%, transparent);
}

.items-scrollbar.is-scrolled-top::before,
.items-scrollbar.is-scrolled-bottom::after {
  display: none;
}

.scrollbar-track {
  position: relative;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  gap: 28px;
  min-height: 100%;
}

.scrollbar-line {
  position: absolute;
  z-index: 0;
  top: 10px;
  bottom: 10px;
  left: 50%;
  width: 1px;
  background-color: var(--color-v3-outline);
  pointer-events: none;
  transform: translateX(-50%);
}

.scrollbar-handle {
  position: absolute;
  z-index: 1;
  left: 50%;
  width: 8px;
  min-height: 80px;
  border-radius: 4px;
  background-color: var(--color-v3-outline-var);
  cursor: grab;
  transform: translateX(-50%);
  transition:
    background-color 0.2s ease-in-out,
    top 0.6s ease-in-out;
}

.scrollbar-handle:hover {
  z-index: 3;
  background-color: var(--color-v3-text-disable);
}

.handle-dragging .scrollbar-handle {
  z-index: 3;
  background-color: var(--color-v3-text-disable);
  cursor: grabbing;
  transition: background-color 0.2s ease-in-out;
}

.items-scrollbar-item {
  position: relative;
  z-index: 2;
  display: flex;
  justify-content: flex-end;
  padding: 2px;
}

.items-scrollbar-item:has(button.ms-button-active) {
  z-index: 4;
}

.items-scrollbar-item button {
  position: relative;
  width: 24px;
  height: 24px;
  cursor: default;
}

.items-scrollbar-item button:hover {
  background-color: transparent;
}

.items-scrollbar-dot {
  position: absolute;
  top: 50%;
  left: 50%;
  box-sizing: border-box;
  width: 6px;
  height: 10px;
  border: 1px solid transparent;
  border-radius: 9999px;
  background-color: var(--color-v3-text-disable);
  transform: translate(-50%, -50%);
  transition: all 0.2s ease-in-out;
}

.items-scrollbar-item button:hover:not(.ms-button-active):not(:focus-visible) .items-scrollbar-dot {
  background-color: var(--color-v3-text-link);
  transform: translate(-50%, -50%) scale(1.25);
}

.items-scrollbar-item button:focus-visible {
  outline: none;
}

.items-scrollbar-item button:focus-visible .items-scrollbar-dot {
  background-color: var(--color-v3-text-link);
  box-shadow: 0 0 0 2px var(--color-v3-button-container-accent);
  transform: translate(-50%, -50%) scale(1.5);
}

.items-scrollbar-item button.ms-button-active {
  background-color: transparent !important;
}

.items-scrollbar-item button.ms-button-active .items-scrollbar-dot {
  border: none;
  background-color: light-dark(var(--color-v3-text-var), var(--color-v3-text-on-button));
  box-shadow: var(--shadow-v3-sm);
  transform: translate(-50%, -50%) scale(1.5);
}

.handle-dragging .items-scrollbar-item button {
  opacity: 0.5;
}
</style>
