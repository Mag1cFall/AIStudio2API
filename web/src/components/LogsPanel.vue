<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import type { AdminLog } from '@/types'
import UiIcon from './UiIcon.vue'
import UiSelect from './UiSelect.vue'
import RequestLogCard from './RequestLogCard.vue'
import StageLogCard from './StageLogCard.vue'
import { groupLogs, type LogResize, type LogRow } from '@/logs'
import { standardEase } from '@/motion'
import { useQueryParam, useQueryText } from '@/url'

const props = defineProps<{
  logs: AdminLog[]
}>()

defineEmits<{
  clear: []
}>()

const { t, logText } = useI18n()
const levels = ['ALL', 'INFO', 'WARN', 'ERROR'] as const
const level = useQueryParam('level', levels, 'ALL')
const source = useQueryText('source', 'ALL')
const search = useQueryText('q')
const autoScroll = ref(true)
const output = ref<HTMLElement>()
const sourceWidth = ref(224)
const resizingSource = ref(false)
const minSourceWidth = 128
const maxSourceWidth = 512

let resizeStartX = 0
let resizeStartWidth = 0

const sources = computed(() =>
  Array.from(new Set(props.logs.map((entry) => entry.source).filter(Boolean))).sort(),
)

function matchLog(entry: AdminLog, query: string): boolean {
  if (entry.message && entry.message.toLowerCase().includes(query)) return true
  if (entry.message && logText(entry.message).toLowerCase().includes(query)) return true
  if (entry.source && entry.source.toLowerCase().includes(query)) return true
  if (entry.event && entry.event.toLowerCase().includes(query)) return true
  if (entry.request) {
    const req = entry.request
    if (req.id && req.id.toLowerCase().includes(query)) return true
    if (req.model && req.model.toLowerCase().includes(query)) return true
    if (req.path && req.path.toLowerCase().includes(query)) return true
    if (req.status !== undefined && String(req.status).includes(query)) return true
    if (req.error && req.error.toLowerCase().includes(query)) return true
    if (req.error && logText(req.error).toLowerCase().includes(query)) return true
    if (req.finish_reason && req.finish_reason.toLowerCase().includes(query)) return true
  }
  return false
}

const rows = computed(() => groupLogs(props.logs))
// filteredLogs 按级别、来源与搜索筛选日志行，阶段卡片的任一步骤命中即显示
const filteredLogs = computed(() => {
  const query = search.value.trim().toLowerCase()
  return rows.value.filter(({ entry, events, stage }) => {
    const checked = stage ? events : [entry]
    return (
      (level.value === 'ALL' ||
        checked.some((event) => event.level.toUpperCase() === level.value)) &&
      (source.value === 'ALL' || events.some((event) => event.source === source.value)) &&
      (query === '' || checked.some((event) => matchLog(event, query)))
    )
  })
})

const displayLimit = ref(200)
// pinnedKey 自动滚动关闭时固定为窗口首行的日志行，新日志只追加在末尾
const pinnedKey = ref<string>()
const firstVisible = computed(() => {
  const list = filteredLogs.value
  if (pinnedKey.value !== undefined)
    return Math.max(
      0,
      list.findIndex((row) => row.key === pinnedKey.value),
    )
  return Math.max(0, list.length - displayLimit.value)
})
const hasMore = computed(() => firstVisible.value > 0)
const hiddenCount = computed(() => firstVisible.value)
const visibleLogs = computed(() => filteredLogs.value.slice(firstVisible.value))

// pinWindow 自动滚动关闭时固定当前窗口首行，开启时恢复跟随末尾
function pinWindow(): void {
  pinnedKey.value = autoScroll.value ? undefined : visibleLogs.value[0]?.key
}

function loadOlder(): void {
  displayLimit.value = visibleLogs.value.length + 200
  pinnedKey.value = undefined
  pinWindow()
}

watch([level, source, search], () => {
  displayLimit.value = 200
  pinnedKey.value = undefined
  pinWindow()
})

const logGridStyle = computed(() => ({ '--log-source-width': `${sourceWidth.value}px` }))

function levelLabel(value: (typeof levels)[number]): string {
  const keys = {
    ALL: 'logs.all',
    INFO: 'logs.info',
    WARN: 'logs.warn',
    ERROR: 'logs.error',
  } as const
  return t(keys[value])
}

function displayLevel(value: string): string {
  const normalized = value.toUpperCase()
  return levels.includes(normalized as (typeof levels)[number])
    ? levelLabel(normalized as (typeof levels)[number])
    : value
}

// glide 是进行中的滚到末尾动画帧，没有动画时为 0
let glide = 0

function scrollToBottom(): void {
  if (!autoScroll.value || glide !== 0) return
  void nextTick(() => {
    if (output.value !== undefined) output.value.scrollTop = output.value.scrollHeight
  })
}

// glideToBottom 从最多一屏之外以先快后慢的缓动滚到末尾，减少动态效果时直接到底
function glideToBottom(): void {
  const element = output.value
  if (!element) return
  cancelAnimationFrame(glide)
  glide = 0
  const end = element.scrollHeight - element.clientHeight
  if (end - element.scrollTop <= 1 || matchMedia('(prefers-reduced-motion: reduce)').matches) {
    element.scrollTop = end
    return
  }
  const start = Math.max(element.scrollTop, end - element.clientHeight)
  const duration = Math.min(360, 160 + Math.sqrt(end - start) * 6)
  const began = performance.now()
  element.scrollTop = start
  const step = (now: number) => {
    const progress = Math.min(1, (now - began) / duration)
    const target = element.scrollHeight - element.clientHeight
    element.scrollTop = start + (target - start) * (1 - (1 - progress) ** 4)
    glide = progress < 1 ? requestAnimationFrame(step) : 0
  }
  glide = requestAnimationFrame(step)
}

// openRows 是展开详情的请求与阶段日志行
const openRows = reactive(new Set<string>())
// revealMargin 是展开详情滚入视野后与日志区边缘保留的距离
const revealMargin = 8

function setRowOpen(key: string, open: boolean): void {
  if (open) openRows.add(key)
  else openRows.delete(key)
}

// toggleRow 点击请求或阶段日志行的详情以外部分时展开或收起，点击行内控件与选中文字时不切换
function toggleRow(row: LogRow, event: MouseEvent): void {
  if (!row.entry.request && !row.stage) return
  if ((event.target as Element).closest('button, a, input, textarea, select, .log-collapse')) return
  const selection = getSelection()
  if (
    selection &&
    !selection.isCollapsed &&
    (event.currentTarget as Element).contains(selection.anchorNode)
  )
    return
  setRowOpen(row.key, !openRows.has(row.key))
}

// scrollAlong 以与详情高度过渡相同的缓动把日志区滚动 shift，被尚未撑开的高度截住时继续跟到过渡结束，过渡时长为 0 时直接到位
function scrollAlong(shift: number, duration: number): void {
  const element = output.value
  if (!element) return
  cancelAnimationFrame(glide)
  glide = 0
  const start = element.scrollTop
  const target = start + shift
  if (duration === 0) {
    element.scrollTop = target
    return
  }
  const began = performance.now()
  const step = (now: number) => {
    const progress = Math.min(1, (now - began) / duration)
    element.scrollTop = start + shift * standardEase(progress)
    const settling = Math.abs(element.scrollTop - target) > 1 && now - began < duration + 100
    glide = progress < 1 || settling ? requestAnimationFrame(step) : 0
  }
  glide = requestAnimationFrame(step)
}

// followDetails 在详情展开时随过渡把整行滚入视野且不把被点击的标题滚出顶部，收起时把已滚出顶部的标题带回顶部
function followDetails(event: Event): void {
  const element = output.value
  const target = event.target as Element
  const heading = target.parentElement
  const entry = target.closest('.log-entry')
  if (!element || !heading || !entry) return
  const { delta, duration } = (event as CustomEvent<LogResize>).detail
  const view = element.getBoundingClientRect()
  const top = heading.getBoundingClientRect().top - view.top - revealMargin
  const shift =
    delta > 0
      ? Math.max(
          0,
          Math.min(entry.getBoundingClientRect().bottom - view.bottom + revealMargin, top),
        )
      : Math.min(0, top)
  if (shift !== 0) scrollAlong(shift, duration)
}

// followScroll 在日志区离开末尾时关闭自动滚动，回到末尾时开启
function followScroll(): void {
  if (!output.value || glide !== 0) return
  const element = output.value
  const distance = element.scrollHeight - element.scrollTop - element.clientHeight
  if (autoScroll.value && distance > 40) autoScroll.value = false
  else if (!autoScroll.value && distance <= 10) autoScroll.value = true
}

// timeFormat 格式化日志行的 24 小时制时间
const timeFormat = new Intl.DateTimeFormat('en-GB', {
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
})

function displayTime(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : timeFormat.format(date)
}

function clampSourceWidth(value: number): number {
  return Math.min(maxSourceWidth, Math.max(minSourceWidth, value))
}

function startSourceResize(event: PointerEvent): void {
  if (event.button !== 0) return
  const handle = event.currentTarget as HTMLElement
  handle.setPointerCapture(event.pointerId)
  resizeStartX = event.clientX
  resizeStartWidth = sourceWidth.value
  resizingSource.value = true
  event.preventDefault()
}

function moveSourceResize(event: PointerEvent): void {
  if (!resizingSource.value) return
  sourceWidth.value = clampSourceWidth(resizeStartWidth + event.clientX - resizeStartX)
}

function stopSourceResize(event: PointerEvent): void {
  if (!resizingSource.value) return
  const handle = event.currentTarget as HTMLElement
  if (handle.hasPointerCapture(event.pointerId)) handle.releasePointerCapture(event.pointerId)
  resizingSource.value = false
}

function resizeSourceWithKeyboard(event: KeyboardEvent): void {
  if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
  event.preventDefault()
  sourceWidth.value = clampSourceWidth(sourceWidth.value + (event.key === 'ArrowRight' ? 16 : -16))
}

watch(() => props.logs, scrollToBottom)
watch(autoScroll, (enabled) => {
  displayLimit.value = Math.max(200, visibleLogs.value.length)
  pinWindow()
  if (enabled) void nextTick(glideToBottom)
})
onMounted(scrollToBottom)
onUnmounted(() => cancelAnimationFrame(glide))
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col bg-canvas">
    <div
      class="flex min-h-10 flex-wrap items-center gap-x-3 gap-y-2 border-b border-line bg-panel px-4 py-1.5"
    >
      <div class="flex items-center gap-2" role="group" :aria-label="t('logs.level')">
        <span class="hidden text-xs text-gray-400 sm:inline">{{ t('logs.level') }}:</span>
        <div class="flex rounded border border-line bg-canvas p-0.5">
          <button
            v-for="item in levels"
            :key="item"
            class="rounded px-2 py-0.5 text-xs transition"
            :class="
              level === item
                ? item === 'ERROR'
                  ? 'bg-red-600 text-white'
                  : item === 'WARN'
                    ? 'bg-yellow-600 text-white'
                    : 'bg-blue-600 text-white'
                : 'text-gray-400 hover:text-gray-200'
            "
            type="button"
            :aria-pressed="level === item"
            @click="level = item"
          >
            {{ levelLabel(item) }}
          </button>
        </div>
      </div>
      <label class="flex items-center gap-2 text-xs text-gray-400">
        <span class="sr-only sm:not-sr-only">{{ t('logs.source') }}:</span>
        <UiSelect
          v-model="source"
          class="max-w-48 rounded border border-line bg-canvas px-2 py-0.5 text-gray-200 outline-none"
        >
          <option value="ALL">{{ t('logs.allSources') }}</option>
          <option v-for="item in sources" :key="item" :value="item">{{ item }}</option>
        </UiSelect>
      </label>
      <input
        v-model="search"
        type="search"
        :placeholder="t('logs.search')"
        :aria-label="t('logs.search')"
        class="min-w-32 flex-1 rounded border border-line bg-canvas px-2 py-1 text-xs text-gray-200 outline-none focus:border-blue-500 sm:w-64 sm:flex-none"
      />
      <div class="flex items-center gap-2 sm:ml-auto">
        <button
          v-tooltip="t('logs.clear')"
          class="rounded px-2 py-1 text-gray-400 transition hover:bg-line hover:text-white"
          type="button"
          :aria-label="t('logs.clear')"
          @click="$emit('clear')"
        >
          <UiIcon name="trash" :size="14" />
        </button>
        <button
          class="flex items-center gap-1.5 rounded border px-2 py-0.5 text-xs transition"
          :class="
            autoScroll
              ? 'border-blue-500/30 bg-blue-500/10 text-blue-400'
              : 'border-line text-gray-500 hover:text-gray-300'
          "
          type="button"
          :aria-pressed="autoScroll"
          @click="autoScroll = !autoScroll"
        >
          <span
            class="size-1.5 rounded-full"
            :class="autoScroll ? 'bg-blue-400' : 'bg-gray-600'"
            aria-hidden="true"
          ></span>
          {{ t('logs.autoScroll') }}
        </button>
      </div>
    </div>

    <div
      ref="output"
      class="min-h-0 flex-1 overflow-auto bg-canvas p-2 font-mono text-[13px] leading-5"
      @scroll.passive="followScroll"
      @log-resize="followDetails"
    >
      <div v-if="filteredLogs.length === 0" class="mt-10 text-center text-gray-600 italic">
        {{ t('logs.waiting') }}
      </div>
      <div v-else class="log-table" :style="logGridStyle">
        <div
          class="source-resizer"
          :class="{ 'is-resizing': resizingSource }"
          role="separator"
          tabindex="0"
          aria-orientation="vertical"
          :aria-label="t('logs.source')"
          :aria-valuemin="minSourceWidth"
          :aria-valuemax="maxSourceWidth"
          :aria-valuenow="sourceWidth"
          @pointerdown="startSourceResize"
          @pointermove="moveSourceResize"
          @pointerup="stopSourceResize"
          @pointercancel="stopSourceResize"
          @keydown="resizeSourceWithKeyboard"
        ></div>
        <div
          v-if="hasMore"
          class="flex items-center justify-center py-2 border-b border-line bg-panel/60 text-xs my-1 rounded"
        >
          <button
            type="button"
            class="rounded border border-line bg-canvas px-3 py-1 text-gray-400 hover:border-blue-500 hover:text-blue-400 transition"
            @click="loadOlder"
          >
            {{ t('logs.loadOlder').replace('{count}', String(hiddenCount)) }}
          </button>
        </div>
        <div
          v-for="row in visibleLogs"
          :key="row.key"
          class="log-entry log-grid border-l-2 px-2 py-1 select-text"
          :class="{
            'log-expandable': row.entry.request || row.stage,
            'border-blue-500 text-gray-300': row.entry.level.toUpperCase() === 'INFO',
            'border-yellow-500 bg-yellow-500/5 text-yellow-100':
              row.entry.level.toUpperCase() === 'WARN',
            'border-red-500 bg-red-500/10 text-red-100': row.entry.level.toUpperCase() === 'ERROR',
            'border-gray-600 text-gray-300': !['INFO', 'WARN', 'ERROR'].includes(
              row.entry.level.toUpperCase(),
            ),
          }"
          @click="toggleRow(row, $event)"
        >
          <span class="text-right text-gray-500">{{ displayTime(row.entry.time) }}</span>
          <span class="font-semibold">{{ displayLevel(row.entry.level) }}</span>
          <span v-tooltip="row.entry.source" class="log-cell log-source text-gray-500">{{
            row.entry.source
          }}</span>
          <div class="log-cell log-message">
            <RequestLogCard
              v-if="row.entry.request"
              :row="row"
              :open="openRows.has(row.key)"
              @update:open="setRowOpen(row.key, $event)"
            />
            <StageLogCard
              v-else-if="row.stage"
              :row="row"
              :open="openRows.has(row.key)"
              @update:open="setRowOpen(row.key, $event)"
            />
            <span v-else>{{ logText(row.entry.message) }}</span>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.log-table {
  position: relative;
  width: 100%;
  min-width: 0;
}

.log-entry {
  border-bottom: 1px solid color-mix(in oklab, var(--color-line) 50%, transparent);
}

.log-entry:hover {
  background-image: linear-gradient(
    color-mix(in oklab, var(--color-line) 22%, transparent),
    color-mix(in oklab, var(--color-line) 22%, transparent)
  );
}

.log-expandable {
  cursor: pointer;
}

.log-expandable :deep(.log-collapse) {
  cursor: auto;
}

.log-grid {
  display: grid;
  grid-template-columns: 5rem 3.5rem var(--log-source-width) minmax(0, 1fr);
  column-gap: 0.75rem;
  align-items: start;
}

.log-cell {
  min-width: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.source-resizer {
  position: absolute;
  z-index: 10;
  top: 0;
  bottom: 0;
  left: calc(10.5rem + var(--log-source-width) + 2px);
  width: 0.75rem;
  cursor: col-resize;
  overflow-anchor: none;
  touch-action: none;
  user-select: none;
}

.source-resizer::after {
  position: absolute;
  top: 0;
  bottom: 0;
  left: 50%;
  width: 1px;
  background: var(--color-line);
  content: '';
  transition: background-color 120ms ease;
}

.source-resizer:hover::after,
.source-resizer:focus-visible::after,
.source-resizer.is-resizing::after {
  background: #3b82f6;
}

.source-resizer:focus-visible {
  outline: none;
}

@media (max-width: 767px) {
  .log-table {
    min-width: 0;
  }

  .log-grid {
    grid-template-columns: 4.5rem 3.25rem minmax(0, 1fr);
    column-gap: 0.5rem;
    row-gap: 0.125rem;
  }

  .log-source {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .log-message {
    grid-column: 1 / -1;
    padding: 0.125rem 0 0.25rem;
  }

  .source-resizer {
    display: none;
  }
}
</style>
