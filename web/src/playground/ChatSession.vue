<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import ChatTurn from './ChatTurn.vue'
import ItemsScrollbar, { type ScrollbarItem } from './ItemsScrollbar.vue'
import { usePlayground } from './session'
import type { Part } from './types'

defineProps<{ sideBySide?: boolean }>()
const { t } = useI18n()
const { session } = usePlayground()

const scroller = ref<HTMLElement>()
const content = ref<HTMLElement>()
const scrollbar = ref<InstanceType<typeof ItemsScrollbar>>()

// streamingIndex 是正在生成的模型轮次下标
const streamingIndex = computed(() => (session.running ? session.turns.length - 1 : -1))
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
  const element = scroller.value
  if (element === undefined) return
  scrollable.value = element.scrollHeight > element.clientHeight
  atBottom.value = element.scrollTop >= element.scrollHeight - element.clientHeight
}

// follow 与官网 ms-autoscroll-container 一致：没有停止跟随时滚到底部，force 先恢复跟随
function follow(force = false): void {
  const element = scroller.value
  if (element === undefined) return
  if (force) detached = false
  if (detached) return
  const max = element.scrollHeight - element.clientHeight
  if (element.scrollTop >= max) return
  lastTop = max
  lastHeight = element.clientHeight
  element.scrollTop = max
  atBottom.value = true
}

// onScroll 用户向上滚动任意距离即停止跟随，滚到底部恢复跟随；可见高度增加使位置被截短的距离不算向上滚动
function onScroll(): void {
  const element = scroller.value
  if (element === undefined) return
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
watch(
  [scroller, content],
  ([element, inner]) => {
    resize.disconnect()
    detached = false
    lastTop = 0
    lastHeight = element?.clientHeight ?? 0
    if (element === undefined || inner === undefined) return
    resize.observe(inner)
    resize.observe(element)
  },
  { flush: 'post' },
)
watch(
  () => session.running,
  (running) => {
    if (running) void nextTick(() => follow(true))
  },
)

// onScrollerKeydown 与官网一致：Alt 加上下方向键聚焦右侧轮次导航
function onScrollerKeydown(event: KeyboardEvent): void {
  const element = event.target as HTMLElement | null
  if (
    element !== null &&
    (element.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(element.tagName))
  )
    return
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
            v-if="!responseTurn(index)"
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

.chat-session-content {
  max-width: 1000px;
  margin: 0 auto;
  padding: 0 20px 24px;
}

.side-by-side .chat-session-content {
  padding: 0 0 24px;
}

@media screen and (max-width: 480px) {
  .chat-session-content {
    padding: 0 8px 24px;
  }
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
