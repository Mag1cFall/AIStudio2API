<script setup lang="ts">
import {
  computed,
  nextTick,
  onActivated,
  onDeactivated,
  onMounted,
  onUnmounted,
  ref,
  watch,
} from 'vue'
import { useI18n } from '@/i18n'
import ChatTurn from './ChatTurn.vue'
import ItemsScrollbar, { type ScrollbarItem } from './ItemsScrollbar.vue'
import { usePlayground } from './session'
import { editable } from './shortcuts'
import type { Part } from './types'

defineProps<{ sideBySide?: boolean }>()
const { t } = useI18n()
const { session } = usePlayground()

const scroller = ref<HTMLElement>()
const content = ref<HTMLElement>()
const scrollbar = ref<InstanceType<typeof ItemsScrollbar>>()

// streamingIndex 是正在生成的模型轮次下标
const streamingIndex = computed(() => (session.running ? session.turns.length - 1 : -1))
// renderBatch 是打开对话后每帧渲染的轮次数，长对话从末尾一批开始逐帧向前补齐
const renderBatch = 1
// firstShown 是已渲染的第一个轮次下标
const firstShown = ref(0)
// eager 是首批轮次的下标范围 [起, 止)，这些轮次的正文块始终全部排版、不按视口跳过，打开对话滚到末尾时可见部分已是实际高度
const eager = ref<[number, number]>([0, 0])
let filling = 0
let fillFrame = 0
// codeFont 是代码块的字体，首批轮次等它载入后才排版，代码按最终字体换行，之后不因字体晚到而改变高度
const codeFont = '1em "Google Sans Code Variable"'

// scheduleFill 在下一帧绘制之后的任务中向前多渲染一批，每批轮次的排版各在一帧里完成
function scheduleFill(first = false): void {
  fillFrame = requestAnimationFrame(() => (filling = window.setTimeout(() => void fill(first))))
}

// fill 向前多渲染一批轮次；首批不满一屏时在同一任务里继续向前补并滚到末尾，首次绘制即铺满可见区域，之后在可见区域上方每帧补一批，由浏览器的滚动锚定保持可见内容不动
async function fill(first: boolean): Promise<void> {
  if (first) await document.fonts.load(codeFont)
  firstShown.value = Math.max(0, firstShown.value - renderBatch)
  if (first) eager.value = [firstShown.value, eager.value[1]]
  await nextTick()
  // 等待字体与更新期间切换对话时组件已卸载，补齐随之结束
  if (!scroller.value) return
  if (first) {
    const element = scroller.value
    if (firstShown.value > 0 && element.scrollHeight <= element.clientHeight) return fill(true)
    follow()
  }
  if (firstShown.value > 0) scheduleFill()
}
watch(
  () => session.turns,
  (turns) => {
    cancelAnimationFrame(fillFrame)
    window.clearTimeout(filling)
    // 长对话的末尾一批也在下一帧之后渲染，不与打开对话时的页面更新挤在同一个任务里
    firstShown.value = turns.length > renderBatch ? turns.length : 0
    eager.value = [turns.length, turns.length]
    if (firstShown.value > 0) scheduleFill(true)
  },
  { immediate: true },
)
// responseTurn 表示用户轮次只含函数响应，显示在上一轮的函数调用块内
const responseTurn = (index: number): boolean => {
  const turn = session.turns[index]
  return (
    turn?.role === 'user' &&
    turn.parts.length > 0 &&
    turn.parts.every((part) => part.functionResponse !== undefined)
  )
}
// responses 返回模型轮次之后紧跟的函数响应片段
const responses = (index: number): Part[] | undefined =>
  responseTurn(index + 1) ? session.turns[index + 1]!.parts : undefined
// scrollbarItems 与官网一致：每组连续的用户轮次一个导航点，提示为第一段文字的前 39 个字符
const scrollbarItems = computed<ScrollbarItem[]>(() => {
  const items: ScrollbarItem[] = []
  let group: string | null = null
  for (const [index, turn] of session.turns.entries()) {
    if (turn.role !== 'user' || responseTurn(index)) {
      group = null
      continue
    }
    const text = turn.parts.find((part) => part.text)?.text ?? ''
    if (group === null) {
      group = turn.id
      items.push({
        id: turn.id,
        label: text ? (text.length > 39 ? `${text.slice(0, 39)}...` : text) : t('pg.userTurn'),
      })
    }
  }
  return items
})

// detached 表示用户向上滚动后停止跟随，回到底部时恢复；lastTop 是上一次滚动事件或自动跟随设定的位置，lastHeight 是当时的可见高度
let detached = false
let lastTop = 0
let lastHeight = 0
let scrollingTimer = 0
const scrolled = ref(false)
const scrollable = ref(false)
const atBottom = ref(true)
const userScrolling = ref(false)

// measure 记录对话是否可滚动、是否位于底部
function measure(): void {
  const element = scroller.value!
  scrollable.value = element.scrollHeight > element.clientHeight
  atBottom.value = element.scrollTop >= element.scrollHeight - element.clientHeight
}

// follow 与官网 ms-autoscroll-container 一致：没有停止跟随时滚到底部，force 先恢复跟随；内容变矮使位置被截短时同样记录新的底部
function follow(force = false): void {
  const element = scroller.value!
  if (force) detached = false
  if (detached) return
  const max = element.scrollHeight - element.clientHeight
  lastTop = max
  lastHeight = element.clientHeight
  if (element.scrollTop >= max) return
  element.scrollTop = max
  atBottom.value = true
}

// onScroll 用户向上滚动任意距离即停止跟随，滚到底部恢复跟随；可见高度增加使位置被截短的距离不算向上滚动
function onScroll(): void {
  const element = scroller.value!
  const max = element.scrollHeight - element.clientHeight
  const top = Math.max(0, Math.floor(element.scrollTop))
  const bottom = top >= max
  const grown = Math.max(0, element.clientHeight - lastHeight)
  if (lastTop <= max && lastTop - top > 1 + grown) detached = true
  if (bottom) detached = false
  lastTop = top
  lastHeight = element.clientHeight
  scrolled.value = top > 0
  atBottom.value = bottom
  userScrolling.value = true
  window.clearTimeout(scrollingTimer)
  scrollingTimer = window.setTimeout(() => (userScrolling.value = false), 200)
}

// resize 在内容或容器尺寸变化时跟随到底部
const resize = new ResizeObserver(() => {
  follow()
  measure()
})
// observe 观察对话内容与滚动容器的尺寸
function observe(): void {
  resize.observe(content.value!)
  resize.observe(scroller.value!)
}
// 可见高度由尺寸观察的首次回调经 follow 记录，挂载时不读取布局
onMounted(observe)
onDeactivated(() => resize.disconnect())
onActivated(() => {
  scroller.value!.scrollTop = lastTop
  observe()
})
watch(
  () => session.running,
  (running) => {
    if (running) void nextTick(() => follow(true))
  },
)

// onScrollerKeydown 与官网一致：Alt 加上下方向键聚焦右侧轮次导航
function onScrollerKeydown(event: KeyboardEvent): void {
  if (editable(event.target)) return
  if (
    (event.key === 'ArrowUp' || event.key === 'ArrowDown') &&
    event.altKey &&
    !event.shiftKey &&
    !event.ctrlKey &&
    !event.metaKey
  ) {
    event.preventDefault()
    scrollbar.value?.focusActive()
  }
}

onUnmounted(() => {
  window.clearTimeout(scrollingTimer)
  cancelAnimationFrame(fillFrame)
  window.clearTimeout(filling)
  resize.disconnect()
})
</script>

<template>
  <div class="ms-chat-session" :class="{ 'side-by-side': sideBySide }">
    <div
      ref="scroller"
      class="chat-scroller"
      tabindex="-1"
      :style="{ '--ms-autoscroll-container-scrolled': scrolled ? 1 : 0 }"
      @scroll.passive="onScroll"
      @keydown="onScrollerKeydown"
    >
      <div ref="content" class="chat-session-content">
        <template v-for="(turn, index) in session.turns" :key="turn.id">
          <ChatTurn
            v-if="index >= firstShown && !responseTurn(index)"
            :class="{ eager: index >= eager[0] && index < eager[1] }"
            :turn="turn"
            :index="index"
            :streaming="index === streamingIndex"
            :show-label="index === 0 || session.turns[index - 1]?.role !== turn.role"
            :responses="responses(index)"
          />
        </template>
      </div>
    </div>
    <ItemsScrollbar
      v-if="scrollable && session.turns.length > 0"
      ref="scrollbar"
      :items="scrollbarItems"
      :scroller="scroller"
      :scrolling="userScrolling"
    />
    <div v-if="scrollable && !atBottom && session.turns.length > 0" class="bottom-overlay"></div>
  </div>
</template>

<style scoped>
.ms-chat-session {
  position: relative;
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
}

.chat-scroller {
  --ms-autoscroll-container-padding-top: 20px;

  flex: 1;
  min-height: 0;
  margin-top: 12px;
  overflow-y: auto;
  padding-top: var(--ms-autoscroll-container-padding-top);
  container-type: size;
  outline: none;
  scrollbar-width: none;
}

.chat-scroller::-webkit-scrollbar {
  display: none;
}

@media screen and (max-width: 768px) {
  .chat-scroller {
    --ms-autoscroll-container-padding-top: 12px;
  }
}

.side-by-side .chat-scroller {
  --ms-autoscroll-container-padding-top: 12px;

  margin-top: 0;
  padding: var(--ms-autoscroll-container-padding-top) 20px 0;
}

@media screen and (max-width: 768px) {
  .side-by-side .chat-scroller {
    padding: var(--ms-autoscroll-container-padding-top) 16px 0;
  }
}

/* 轮次之间的 8 px 间距由 gap 给出，补入轮次时已有轮次的样式不变 */
.chat-session-content {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-width: 1000px;
  margin: 0 auto;
  padding: 0 20px 24px;
}

.side-by-side .chat-session-content {
  padding: 0 0 24px;
}

.chat-session-content :deep(.chat-turn.eager .cmark-node > *) {
  content-visibility: visible;
}

.bottom-overlay {
  position: absolute;
  z-index: 2;
  right: 0;
  bottom: 0;
  left: 0;
  height: 60px;
  background: linear-gradient(to top, var(--color-v3-surface) 0, transparent 95%);
  pointer-events: none;
}
</style>
