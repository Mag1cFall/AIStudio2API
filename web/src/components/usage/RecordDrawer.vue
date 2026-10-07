<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
} from 'reka-ui'
import { api } from '@/api'
import { useI18n } from '@/i18n'
import type { RequestBody, UsageRecord } from '@/types'
import UiIcon from '../UiIcon.vue'
import { formatBytes, useUsageFormat } from './format'
import { latencyTone } from './model'

const props = defineProps<{ record: UsageRecord | null }>()
const emit = defineEmits<{ close: [] }>()

const { t, errorText } = useI18n()
const { integer, duration, dateTime, dimensionLabel } = useUsageFormat()
const body = ref<RequestBody | null>(null)
const bodyState = ref<'idle' | 'loading' | 'ready' | 'error'>('idle')
const bodyError = ref('')
const copied = ref('')
// clipboardAvailable 表示页面处于可使用剪贴板 API 的安全上下文
const clipboardAvailable = window.isSecureContext

watch(
  () => props.record,
  async (record) => {
    body.value = null
    bodyError.value = ''
    bodyState.value = 'idle'
    if (record === null || !record.has_body) return
    bodyState.value = 'loading'
    try {
      const value = await api.requestBody(record.id)
      if (props.record?.id !== record.id) return
      body.value = value
      bodyState.value = 'ready'
    } catch (error) {
      if (props.record?.id !== record.id) return
      bodyError.value = errorText(error)
      bodyState.value = 'error'
    }
  },
)

const summary = computed(() => {
  const record = props.record
  if (record === null) return []
  return [
    { label: t('usage.dimension.model'), value: record.model || '—' },
    { label: t('usage.dimension.account'), value: record.account || '—' },
    { label: t('usage.dimension.channel'), value: dimensionLabel('channel', record.channel) },
    { label: t('usage.dimension.protocol'), value: dimensionLabel('protocol', record.protocol) },
    { label: t('usage.path'), value: record.path },
    { label: t('usage.dimension.status'), value: String(record.status) },
    { label: t('usage.duration'), value: duration(record.duration_ms) },
    { label: t('usage.firstEvent'), value: duration(record.first_event_ms) },
    { label: t('usage.queue'), value: duration(record.queue_ms) },
    { label: t('usage.toolCalls'), value: integer(record.tool_calls) },
    { label: t('usage.input'), value: integer(record.input_tokens) },
    { label: t('usage.reasoning'), value: integer(record.reasoning_tokens) },
    { label: t('usage.reply'), value: integer(record.reply_tokens) },
    { label: t('usage.tokens'), value: integer(record.total_tokens) },
  ]
})

// byteLength 返回文本的 UTF-8 字节数
function byteLength(text: string): number {
  return new TextEncoder().encode(text).length
}

// pretty 格式化 JSON 正文，无法解析时原样返回
function pretty(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return text
  }
}

// copy 复制文本并短暂显示已复制
async function copy(kind: string, text: string): Promise<void> {
  await navigator.clipboard.writeText(text)
  copied.value = kind
  window.setTimeout(() => {
    if (copied.value === kind) copied.value = ''
  }, 1200)
}
</script>

<template>
  <DialogRoot :open="record !== null" @update:open="(open: boolean) => !open && emit('close')">
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-[70] bg-black/60" />
      <DialogContent
        v-if="record"
        class="ui-sheet fixed inset-y-0 right-0 z-[71] flex w-full max-w-2xl flex-col border-l border-line bg-panel shadow-2xl focus:outline-none"
      >
        <header class="flex items-start gap-3 border-b border-line px-5 py-4">
          <div class="min-w-0 flex-1">
            <DialogTitle class="text-base font-semibold text-white">
              {{ t('usage.detailTitle') }}
            </DialogTitle>
            <DialogDescription class="mt-1 flex flex-wrap items-center gap-2 text-xs text-gray-400">
              <code class="font-mono text-gray-300">{{ record.id }}</code>
              <button
                v-if="clipboardAvailable"
                type="button"
                class="flex items-center gap-1 rounded px-1.5 py-0.5 text-gray-500 hover:bg-raised hover:text-white"
                @click="copy('id', record.id)"
              >
                <UiIcon :name="copied === 'id' ? 'check' : 'copy'" :size="12" />
                {{ copied === 'id' ? t('common.copied') : t('common.copy') }}
              </button>
              <span class="tabular-nums">{{ dateTime(record.time, true) }}</span>
            </DialogDescription>
          </div>
          <DialogClose
            class="rounded p-1 text-gray-500 hover:bg-raised hover:text-white"
            :aria-label="t('common.close')"
          >
            <UiIcon name="close" :size="16" />
          </DialogClose>
        </header>
        <div class="flex-1 space-y-5 overflow-y-auto px-5 py-4 text-sm">
          <section>
            <h3 class="mb-2 text-xs font-medium tracking-wide text-gray-500 uppercase">
              {{ t('usage.detailSummary') }}
            </h3>
            <div class="mb-3 flex items-center gap-2">
              <span
                class="rounded px-2 py-0.5 text-xs"
                :class="
                  record.state === 'failed'
                    ? 'bg-red-500/15 text-red-300'
                    : record.state === 'cancelled'
                      ? 'bg-gray-500/15 text-gray-300'
                      : 'bg-green-500/15 text-green-300'
                "
                >{{ dimensionLabel('state', record.state) }}</span
              >
              <span class="flex items-center gap-1.5 text-xs text-gray-400">
                <span
                  class="h-1.5 w-1.5 rounded-full"
                  :class="latencyTone(record.duration_ms, 'duration')"
                ></span>
                {{ duration(record.duration_ms) }}
              </span>
            </div>
            <dl class="grid grid-cols-1 gap-x-6 gap-y-2 text-xs sm:grid-cols-2">
              <div
                v-for="item in summary"
                :key="item.label"
                class="flex min-w-0 justify-between gap-3"
              >
                <dt class="shrink-0 text-gray-500">{{ item.label }}</dt>
                <dd v-tooltip="item.value" class="truncate text-right text-gray-200 tabular-nums">
                  {{ item.value }}
                </dd>
              </div>
            </dl>
          </section>
          <section v-if="record.error">
            <h3 class="mb-2 text-xs font-medium tracking-wide text-gray-500 uppercase">
              {{ t('usage.detailError') }}
            </h3>
            <pre
              class="rounded-md border border-red-500/30 bg-red-500/5 p-3 font-mono text-xs leading-5 whitespace-pre-wrap break-words text-red-200"
              >{{ record.error }}</pre>
          </section>
          <section>
            <h3 class="mb-2 text-xs font-medium tracking-wide text-gray-500 uppercase">
              {{ t('usage.detailAttempts') }}
            </h3>
            <p v-if="record.attempts.length === 0" class="text-xs text-gray-500">
              {{ t('usage.detailAttemptsNone') }}
            </p>
            <ol v-else class="space-y-2 border-l border-line pl-4">
              <li v-for="(attempt, index) in record.attempts" :key="index" class="relative text-xs">
                <span
                  class="absolute top-1.5 -left-[1.3rem] h-2 w-2 rounded-full bg-red-500"
                  aria-hidden="true"
                ></span>
                <div class="flex flex-wrap gap-x-3 text-gray-300">
                  <span>{{ attempt.account }}</span>
                  <span class="text-gray-500">{{
                    dimensionLabel('channel', attempt.channel)
                  }}</span>
                  <span class="text-gray-500 tabular-nums">{{
                    duration(attempt.duration_ms)
                  }}</span>
                </div>
                <p class="mt-0.5 font-mono break-words text-red-300/90">{{ attempt.error }}</p>
              </li>
            </ol>
          </section>
          <section>
            <h3 class="mb-2 text-xs font-medium tracking-wide text-gray-500 uppercase">
              {{ t('usage.detailBody') }}
            </h3>
            <p v-if="!record.has_body" class="text-xs text-gray-500">
              {{ t('usage.detailBodyNone') }}
            </p>
            <p v-else-if="bodyState === 'loading'" class="text-xs text-gray-500">
              {{ t('common.loading') }}
            </p>
            <p v-else-if="bodyState === 'error'" class="text-xs text-red-300" role="alert">
              {{ bodyError }}
            </p>
            <template v-else-if="body">
              <div
                v-for="part in [
                  {
                    kind: 'request',
                    label: t('usage.detailRequest'),
                    text: body.request,
                    size: body.request_size,
                  },
                  {
                    kind: 'response',
                    label: t('usage.detailResponse'),
                    text: body.response,
                    size: body.response_size,
                  },
                ]"
                :key="part.kind"
                class="mb-3"
              >
                <div class="mb-1 flex items-center gap-2 text-xs text-gray-400">
                  <span class="font-medium text-gray-300">{{ part.label }}</span>
                  <span v-if="part.size > byteLength(part.text)" class="text-yellow-400/80">{{
                    t('usage.detailTruncated')
                      .replace('{saved}', formatBytes(byteLength(part.text)))
                      .replace('{size}', formatBytes(part.size))
                  }}</span>
                  <button
                    v-if="clipboardAvailable"
                    type="button"
                    class="ml-auto flex items-center gap-1 rounded px-1.5 py-0.5 text-gray-500 hover:bg-raised hover:text-white"
                    @click="copy(part.kind, part.text)"
                  >
                    <UiIcon :name="copied === part.kind ? 'check' : 'copy'" :size="12" />
                    {{ copied === part.kind ? t('common.copied') : t('common.copy') }}
                  </button>
                </div>
                <pre
                  class="max-h-80 overflow-auto rounded-md border border-line bg-canvas p-3 font-mono text-xs leading-5 whitespace-pre-wrap break-words text-gray-300"
                  >{{ pretty(part.text) }}</pre>
              </div>
            </template>
          </section>
        </div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
