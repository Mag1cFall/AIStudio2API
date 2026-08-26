<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import type { AdminLog } from '@/types'
import UiIcon from './UiIcon.vue'

const props = defineProps<{
  logs: AdminLog[]
}>()

defineEmits<{
  clear: []
}>()

const { t } = useI18n()
const level = ref<'ALL' | 'INFO' | 'WARN' | 'ERROR'>('ALL')
const source = ref('ALL')
const autoScroll = ref(true)
const output = ref<HTMLElement>()
const sourceWidth = ref(224)
const resizingSource = ref(false)
const levels = ['ALL', 'INFO', 'WARN', 'ERROR'] as const
const minSourceWidth = 128
const maxSourceWidth = 512

let resizeStartX = 0
let resizeStartWidth = 0

const sources = computed(() =>
  Array.from(new Set(props.logs.map((entry) => entry.source).filter(Boolean))).sort(),
)

const filteredLogs = computed(() =>
  props.logs.filter(
    (entry) =>
      (level.value === 'ALL' || entry.level.toUpperCase() === level.value) &&
      (source.value === 'ALL' || entry.source === source.value),
  ),
)

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

function scrollToBottom(): void {
  if (!autoScroll.value) return
  void nextTick(() => {
    if (output.value !== undefined) output.value.scrollTop = output.value.scrollHeight
  })
}

function displayTime(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleTimeString('en-GB', { hour12: false })
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

watch(() => props.logs.length, scrollToBottom)
watch(autoScroll, scrollToBottom)
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col bg-[#0d1117]">
    <div
      class="flex min-h-10 flex-wrap items-center justify-between gap-2 border-b border-[#30363d] bg-[#161b22] px-4 py-1"
    >
      <div class="flex min-w-0 flex-wrap items-center gap-3">
        <span class="text-xs text-gray-400">{{ t('logs.level') }}:</span>
        <div class="flex rounded border border-[#30363d] bg-[#0d1117] p-0.5">
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
            @click="level = item"
          >
            {{ levelLabel(item) }}
          </button>
        </div>
        <label class="flex items-center gap-2 text-xs text-gray-400">
          {{ t('logs.source') }}:
          <select
            v-model="source"
            class="max-w-48 rounded border border-[#30363d] bg-[#0d1117] px-2 py-0.5 text-gray-200 outline-none"
          >
            <option value="ALL">{{ t('logs.allSources') }}</option>
            <option v-for="item in sources" :key="item" :value="item">{{ item }}</option>
          </select>
        </label>
      </div>

      <div class="flex items-center gap-2">
        <button
          class="rounded px-2 py-1 text-gray-400 transition hover:bg-[#30363d] hover:text-white"
          type="button"
          :title="t('logs.clear')"
          :aria-label="t('logs.clear')"
          @click="$emit('clear')"
        >
          <UiIcon name="trash" :size="14" />
        </button>
        <button
          class="rounded border px-2 py-0.5 text-xs transition"
          :class="
            autoScroll
              ? 'border-blue-500/30 bg-blue-500/10 text-blue-400'
              : 'border-gray-700 text-gray-500'
          "
          type="button"
          @click="autoScroll = !autoScroll"
        >
          {{ t('logs.autoScroll') }}: {{ autoScroll ? 'ON' : 'OFF' }}
        </button>
      </div>
    </div>

    <div
      ref="output"
      class="min-h-0 flex-1 overflow-auto bg-[#0d1117] p-2 font-mono text-[13px] leading-5"
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
          v-for="(entry, index) in filteredLogs"
          :key="`${entry.time}:${index}`"
          class="log-entry log-grid border-l-2 px-2 py-0.5 select-text"
          :class="{
            'border-blue-500 text-gray-300': entry.level.toUpperCase() === 'INFO',
            'border-yellow-500 bg-yellow-500/5 text-yellow-100':
              entry.level.toUpperCase() === 'WARN',
            'border-red-500 bg-red-500/10 text-red-100': entry.level.toUpperCase() === 'ERROR',
            'border-gray-600 text-gray-300': !['INFO', 'WARN', 'ERROR'].includes(
              entry.level.toUpperCase(),
            ),
          }"
        >
          <span class="text-right text-gray-600">{{ displayTime(entry.time) }}</span>
          <span class="font-semibold">{{ displayLevel(entry.level) }}</span>
          <span class="log-cell log-source text-gray-500" :title="entry.source">{{
            entry.source
          }}</span>
          <span class="log-cell log-message">{{ entry.message }}</span>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.log-table {
  position: relative;
  width: 100%;
  min-width: calc(43.875rem + var(--log-source-width));
}

.log-grid {
  display: grid;
  grid-template-columns: 5rem 3.5rem var(--log-source-width) minmax(32rem, 1fr);
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
  touch-action: none;
  user-select: none;
}

.source-resizer::after {
  position: absolute;
  top: 0;
  bottom: 0;
  left: 50%;
  width: 1px;
  background: #30363d;
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
