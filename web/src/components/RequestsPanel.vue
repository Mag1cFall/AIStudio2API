<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '@/api'
import { channelLabelKey, useI18n, type TranslationKey } from '@/i18n'
import type { Account, Cooldown, RequestState, RequestSummary } from '@/types'
import DetailDialog from './DetailDialog.vue'
import UiIcon from './UiIcon.vue'

const props = defineProps<{
  accounts: Account[]
  cooldowns: Cooldown[]
  requests: RequestSummary[]
  loading: boolean
  cooldownError: string
  requestError: string
}>()

const emit = defineEmits<{
  refresh: []
  notice: [message: string, tone: 'success' | 'error']
}>()

const { locale, t, errorText, logText } = useI18n()
const cancelling = ref('')
const refreshing = ref(false)
const selectedCategory = ref('')
const detail = ref<Cooldown | null>(null)
const clearing = ref(false)

// splitReason 把冷却原因拆成冷却类型与上游错误原文
function splitReason(reason: string | undefined): { category: string; text: string } {
  const value = reason ?? ''
  const separator = value.indexOf(': ')
  if (separator <= 0) return { category: '', text: value }
  return { category: value.slice(0, separator), text: value.slice(separator + 2) }
}

// categoryLabel 返回冷却类型的当前语言名称，没有类型时为其他
function categoryLabel(category: string): string {
  return category ? logText(category) : t('cooldowns.other')
}

// categories 是全部与各冷却类型的数量，类型按数量从多到少排列
const categories = computed(() => {
  const counts = new Map<string, number>()
  for (const cooldown of props.cooldowns) {
    const category = splitReason(cooldown.reason).category
    counts.set(category, (counts.get(category) ?? 0) + 1)
  }
  return [...counts.entries()].sort((left, right) => right[1] - left[1])
})

// activeCategory 是当前生效的冷却类型，所选类型已无冷却时为全部
const activeCategory = computed(() =>
  categories.value.length > 1 &&
  categories.value.some(([category]) => category === selectedCategory.value)
    ? selectedCategory.value
    : '',
)

// visibleCooldowns 是所选类型的冷却，按恢复时间先后排列
const visibleCooldowns = computed(() =>
  props.cooldowns
    .filter(
      (cooldown) =>
        activeCategory.value === '' ||
        splitReason(cooldown.reason).category === activeCategory.value,
    )
    .sort((left, right) => Date.parse(left.until) - Date.parse(right.until)),
)

const requestStateKeys: Record<RequestState, TranslationKey> = {
  queued: 'state.queued',
  running: 'state.running',
  completed: 'state.completed',
  cancelled: 'state.cancelled',
  failed: 'state.failed',
}

const activeCount = computed(
  () =>
    props.requests.filter((request) => request.state === 'queued' || request.state === 'running')
      .length,
)

// accountLabel 将稳定账户 ID 映射为用户显示名称
function accountLabel(id: string, label: string): string {
  return label || props.accounts.find((account) => account.id === id)?.label || '—'
}

// formatTime 根据当前语言显示请求时间
function formatTime(value: string): string {
  return new Intl.DateTimeFormat(locale.value, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(new Date(value))
}

// refresh 请求刷新数据，图标短暂旋转确认点击
function refresh(): void {
  refreshing.value = true
  emit('refresh')
  window.setTimeout(() => (refreshing.value = false), 600)
}

// cancelRequest 停止活动请求并刷新摘要
async function cancelRequest(request: RequestSummary): Promise<void> {
  cancelling.value = request.id
  try {
    await api.cancelRequest(request.id)
    emit('refresh')
  } catch (error) {
    emit('notice', errorText(error), 'error')
  } finally {
    cancelling.value = ''
  }
}

// clearCooldown 手动清除详情中的冷却，成功后关闭详情并刷新列表
async function clearCooldown(cooldown: Cooldown): Promise<void> {
  clearing.value = true
  try {
    await api.clearCooldown(cooldown.account_id, cooldown.scope)
    detail.value = null
    emit('notice', t('cooldowns.cleared'), 'success')
    emit('refresh')
  } catch (error) {
    emit('notice', errorText(error), 'error')
  } finally {
    clearing.value = false
  }
}
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col overflow-auto md:overflow-hidden">
    <header class="flex flex-wrap items-center gap-3 border-b border-line px-4 py-4 md:px-8">
      <div class="min-w-0 flex-1">
        <h2 class="text-xl font-semibold text-white">{{ t('section.requests.title') }}</h2>
        <p class="mt-1 text-sm text-gray-500">{{ t('section.requests.description') }}</p>
      </div>
      <button
        class="flex items-center gap-2 rounded border border-line bg-raised px-4 py-2 text-sm font-medium text-gray-200 transition hover:bg-line disabled:opacity-50"
        type="button"
        :aria-busy="refreshing"
        @click="refresh"
      >
        <UiIcon name="refresh" :size="15" :class="{ 'animate-spin': refreshing }" />
        {{ t('app.refresh') }}
      </button>
    </header>
    <div
      class="mx-auto grid min-h-0 w-full max-w-6xl flex-1 gap-4 p-4 md:grid-rows-[minmax(0,3fr)_minmax(0,2fr)] md:p-8 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)] lg:grid-rows-1"
    >
      <article
        class="flex min-h-0 flex-col overflow-hidden rounded-lg border border-line bg-panel max-md:max-h-[70vh]"
      >
        <div class="flex flex-wrap items-center gap-2 border-b border-line px-4 py-3">
          <h3 class="font-bold text-gray-300">{{ t('cooldowns.title') }}</h3>
          <span class="font-mono text-xs text-gray-500">{{ cooldowns.length }}</span>
          <div v-if="categories.length > 1" class="ml-auto flex flex-wrap gap-1">
            <button
              v-for="[category, count] in [['', cooldowns.length] as const, ...categories]"
              :key="category || 'all'"
              type="button"
              class="rounded px-2 py-0.5 text-xs transition"
              :class="
                activeCategory === category
                  ? 'bg-blue-600 text-white'
                  : 'bg-raised text-gray-400 hover:text-gray-200'
              "
              :aria-pressed="activeCategory === category"
              @click="selectedCategory = category"
            >
              {{ category === '' ? t('cooldowns.all') : categoryLabel(category) }} {{ count }}
            </button>
          </div>
        </div>
        <div
          v-if="cooldownError"
          class="m-4 rounded border border-red-500/40 bg-red-500/10 p-3 text-red-300"
        >
          {{ cooldownError }}
        </div>
        <div v-else-if="loading" class="py-8 text-center text-gray-500">
          {{ t('common.loading') }}
        </div>
        <div v-else-if="cooldowns.length === 0" class="py-8 text-center text-xs text-gray-600">
          {{ t('cooldowns.empty') }}
        </div>
        <div v-else class="min-h-0 flex-1 overflow-y-auto">
          <button
            v-for="cooldown in visibleCooldowns"
            :key="`${cooldown.account_id}:${cooldown.channel}:${cooldown.model_id}`"
            type="button"
            class="flex w-full items-center gap-3 border-b border-line/60 px-4 py-2 text-left transition hover:bg-raised"
            @click="detail = cooldown"
          >
            <div class="min-w-0 flex-1">
              <span
                v-tooltip.overflow="cooldown.model_id"
                class="block truncate text-sm text-gray-200"
                >{{ cooldown.model_id }}</span
              >
              <span
                v-tooltip.overflow="
                  `${accountLabel(cooldown.account_id, cooldown.account_label)} · ${t(channelLabelKey(cooldown.channel))}`
                "
                class="block truncate text-xs text-gray-500"
                >{{ accountLabel(cooldown.account_id, cooldown.account_label) }} ·
                {{ t(channelLabelKey(cooldown.channel)) }}</span
              >
            </div>
            <div class="flex shrink-0 flex-col items-end gap-1">
              <span class="rounded bg-yellow-500/10 px-2 py-0.5 text-xs text-yellow-300">{{
                categoryLabel(splitReason(cooldown.reason).category)
              }}</span>
              <time class="font-mono text-xs whitespace-nowrap text-gray-400">{{
                formatTime(cooldown.until)
              }}</time>
            </div>
            <UiIcon name="chevronRight" :size="14" class="shrink-0 text-gray-600" />
          </button>
        </div>
      </article>

      <article
        class="flex min-h-0 flex-col overflow-hidden rounded-lg border border-line bg-panel max-md:max-h-[70vh]"
      >
        <div class="flex items-center justify-between border-b border-line px-4 py-3">
          <h3 class="font-bold text-gray-300">{{ t('requests.history') }}</h3>
          <span class="text-xs text-green-400"> {{ t('requests.live') }}: {{ activeCount }} </span>
        </div>
        <div
          v-if="requestError"
          class="m-4 rounded border border-red-500/40 bg-red-500/10 p-3 text-red-300"
        >
          {{ requestError }}
        </div>
        <div v-else-if="loading" class="py-8 text-center text-gray-500">
          {{ t('common.loading') }}
        </div>
        <div v-else-if="requests.length === 0" class="py-8 text-center text-xs text-gray-600">
          {{ t('requests.empty') }}
        </div>
        <div v-else class="min-h-0 flex-1 overflow-y-auto">
          <div
            v-for="request in requests"
            :key="request.id"
            class="flex items-center gap-3 border-b border-line/60 px-4 py-2 transition hover:bg-raised"
          >
            <div class="min-w-0 flex-1">
              <div class="flex min-w-0 items-center gap-2">
                <span v-tooltip.overflow="request.model" class="truncate text-sm text-gray-200">{{
                  request.model
                }}</span>
                <span class="shrink-0 text-xs text-gray-500">{{
                  t(requestStateKeys[request.state])
                }}</span>
              </div>
              <span
                v-tooltip.overflow="
                  request.channel
                    ? `${accountLabel(request.account_id, request.account_label)} · ${t(channelLabelKey(request.channel))}`
                    : accountLabel(request.account_id, request.account_label)
                "
                class="block truncate text-xs text-gray-500"
                >{{ accountLabel(request.account_id, request.account_label)
                }}<template v-if="request.channel">
                  · {{ t(channelLabelKey(request.channel)) }}</template
                ></span
              >
            </div>
            <time class="shrink-0 font-mono text-xs whitespace-nowrap text-gray-500">{{
              formatTime(request.started_at)
            }}</time>
            <button
              v-if="request.state === 'queued' || request.state === 'running'"
              class="shrink-0 rounded border border-red-900/50 bg-red-900/30 px-3 py-1 text-xs text-red-400 transition hover:bg-red-900/50 disabled:opacity-50"
              type="button"
              :disabled="cancelling !== ''"
              :aria-busy="cancelling === request.id"
              @click="cancelRequest(request)"
            >
              {{ t('requests.stop') }}
            </button>
          </div>
        </div>
      </article>
    </div>

    <DetailDialog
      :open="detail !== null"
      :title="detail?.model_id ?? ''"
      :description="
        detail
          ? `${accountLabel(detail.account_id, detail.account_label)} · ${t(channelLabelKey(detail.channel))}`
          : ''
      "
      :copy-text="detail?.reason"
      @close="detail = null"
    >
      <template v-if="detail">
        <dl class="mb-4 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-xs">
          <dt class="text-gray-500">{{ t('cooldowns.category') }}</dt>
          <dd class="text-yellow-300">{{ categoryLabel(splitReason(detail.reason).category) }}</dd>
          <dt class="text-gray-500">{{ t('cooldowns.until') }}</dt>
          <dd class="font-mono text-gray-200">{{ formatTime(detail.until) }}</dd>
        </dl>
        <pre
          class="rounded-md border border-line bg-canvas p-3 font-mono text-xs leading-5 whitespace-pre-wrap [overflow-wrap:anywhere] text-gray-300"
          >{{ splitReason(detail.reason).text || '—' }}</pre>
        <div class="mt-4 flex items-center justify-end gap-3">
          <span v-if="detail.scope === '*'" class="text-xs text-gray-500">{{
            t('cooldowns.clearAccountHint')
          }}</span>
          <button
            type="button"
            class="shrink-0 rounded border border-line bg-raised px-3 py-1.5 text-sm text-gray-200 transition hover:bg-line disabled:opacity-50"
            :disabled="clearing"
            :aria-busy="clearing"
            @click="clearCooldown(detail)"
          >
            {{ t('cooldowns.clear') }}
          </button>
        </div>
      </template>
    </DetailDialog>
  </section>
</template>
