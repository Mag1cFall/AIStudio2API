<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue'
import { api } from '@/api'
import { useI18n } from '@/i18n'
import type { UsageDimension, UsageReport, UsageStats } from '@/types'
import UiIcon from './UiIcon.vue'
import UiSelect from './UiSelect.vue'
import FilterMenu from './usage/FilterMenu.vue'
import KpiCard from './usage/KpiCard.vue'
import RangePicker from './usage/RangePicker.vue'
import RankingTable from './usage/RankingTable.vue'
import RecordsTable from './usage/RecordsTable.vue'
import UsageCharts from './usage/UsageCharts.vue'
import { useUsageFormat } from './usage/format'
import {
  dimensions,
  liveRange,
  rateTone,
  readPreference,
  resolveRange,
  successRate,
  useUsageState,
  writePreference,
  type RangePreset,
} from './usage/model'

const { t, errorText } = useI18n()
const { compact, integer, percent, duration, dateTime, clock, ago, dimensionLabel } =
  useUsageFormat()
const state = useUsageState()
const report = shallowRef<UsageReport | null>(null)
const status = ref<'loading' | 'ready' | 'error'>('loading')
const loading = ref(false)
const error = ref('')
const updatedAt = ref(0)
const now = ref(Date.now())
const recordsRefresh = ref(0)
const refreshChoices = [0, 15, 30, 60] as const
const autoRefresh = ref<number>(
  readPreference('usage.autoRefresh', 30, (value) =>
    refreshChoices.includes(value as (typeof refreshChoices)[number]),
  ),
)
watch(autoRefresh, (value) => writePreference('usage.autoRefresh', value))
const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone
let controller: AbortController | undefined
let clockTimer: number | undefined

const resolved = computed(() => resolveRange(state, new Date(now.value)))
const live = computed(() => liveRange(state.range))
const hasFilters = computed(() => dimensions.some((item) => (state.filters[item] ?? []).length > 0))
const scope = computed(() => JSON.stringify([state.range, state.from, state.to, state.filters]))

// baseQuery 在调用时解析时间范围并写入范围与筛选参数，自定义范围无效时返回 null
function baseQuery(): URLSearchParams | null {
  const range = resolveRange(state, new Date())
  if (range === null) return null
  const query = new URLSearchParams({ from: range.from.toISOString(), to: range.to.toISOString() })
  for (const dimension of dimensions) {
    const values = state.filters[dimension] ?? []
    if (values.length > 0) query.set(dimension, values.join(','))
  }
  return query
}

// load 读取用量报告，新的请求会取消尚未完成的旧请求
async function load(): Promise<void> {
  const query = baseQuery()
  if (query === null) {
    status.value = report.value === null ? 'error' : status.value
    error.value = t('usage.rangeInvalid')
    return
  }
  query.set('tz', timezone)
  query.set('stack', state.stack)
  controller?.abort()
  const current = new AbortController()
  controller = current
  loading.value = true
  try {
    const value = await api.usage(query, current.signal)
    report.value = value
    status.value = 'ready'
    error.value = ''
    updatedAt.value = Date.now()
  } catch (reason) {
    if (current.signal.aborted) return
    error.value = errorText(reason)
    if (report.value === null) status.value = 'error'
  } finally {
    if (controller === current) loading.value = false
  }
}

// refresh 立即刷新报告与请求记录
function refresh(): void {
  void load()
  recordsRefresh.value++
}

watch(
  () => JSON.stringify([state.range, state.from, state.to, state.stack, state.filters]),
  () => void load(),
)

// tick 每秒更新时钟，到期且页面可见时自动刷新
function tick(): void {
  now.value = Date.now()
  if (
    autoRefresh.value > 0 &&
    live.value &&
    document.visibilityState === 'visible' &&
    !loading.value &&
    updatedAt.value > 0 &&
    now.value - updatedAt.value >= autoRefresh.value * 1000
  )
    refresh()
}

// resume 页面重新可见时补一次过期的刷新
function resume(): void {
  if (document.visibilityState === 'visible') tick()
}

onMounted(() => {
  void load()
  clockTimer = window.setInterval(tick, 1000)
  document.addEventListener('visibilitychange', resume)
})
onUnmounted(() => {
  controller?.abort()
  window.clearInterval(clockTimer)
  document.removeEventListener('visibilitychange', resume)
})

// applyRange 应用时间范围选择
function applyRange(range: RangePreset, from: string, to: string): void {
  Object.assign(state, { range, from, to })
}

// setFilter 替换一个维度的筛选值
function setFilter(dimension: UsageDimension, values: string[]): void {
  const filters = { ...state.filters }
  if (values.length === 0) delete filters[dimension]
  else filters[dimension] = values
  state.filters = filters
}

// addFilter 把取值加入维度筛选
function addFilter(dimension: UsageDimension, value: string): void {
  const values = state.filters[dimension] ?? []
  if (!values.includes(value)) setFilter(dimension, [...values, value])
}

// clearFilters 清除全部维度筛选
function clearFilters(): void {
  state.filters = {}
}

const activeChips = computed(() =>
  dimensions.flatMap((dimension) =>
    (state.filters[dimension] ?? []).map((value) => ({ dimension, value })),
  ),
)

const freshness = computed(() => {
  const latest = report.value?.latest_at
  if (latest === undefined) return { tone: 'bg-gray-600', text: t('usage.latestNone') }
  const age = now.value - new Date(latest).getTime()
  const tone = age < 5 * 60_000 ? 'bg-green-500' : age < 3_600_000 ? 'bg-yellow-500' : 'bg-gray-500'
  return { tone, text: t('usage.latest').replace('{time}', ago(latest, now.value)) }
})
const countdown = computed(() => {
  if (!live.value) return t('usage.fixedRange')
  if (autoRefresh.value === 0 || updatedAt.value === 0) return ''
  const seconds = Math.max(0, autoRefresh.value - Math.floor((now.value - updatedAt.value) / 1000))
  return t('usage.nextRefresh').replace('{seconds}', String(seconds))
})

// relative 返回相对变化比例，上一周期为 0 时返回 null
function relative(current: number, previous: number): number | null {
  return previous > 0 ? (current - previous) / previous : null
}

// delta 生成相对上一周期的变化标注
function delta(
  change: number | null,
  text: (value: number) => string,
  better: 'up' | 'down' | 'none',
):
  | {
      text: string
      tone: 'good' | 'bad' | 'neutral'
      direction: 'up' | 'down'
      help: string
    }
  | undefined {
  const value = report.value
  if (change === null || value === null || value.previous.requests === 0) return undefined
  const span = new Date(value.to).getTime() - new Date(value.from).getTime()
  const range = `${dateTime(new Date(new Date(value.from).getTime() - span))} – ${dateTime(value.from)}`
  const flat = Math.abs(change) < 0.0005
  return {
    text: `${change >= 0 ? '+' : '−'}${text(Math.abs(change))}`,
    tone: flat || better === 'none' ? 'neutral' : change > 0 === (better === 'up') ? 'good' : 'bad',
    direction: change >= 0 ? 'up' : 'down',
    help: t('usage.vsPrevious').replace('{range}', range),
  }
}

const kpis = computed(() => {
  const value = report.value
  if (value === null) return []
  const { totals, previous, buckets, recent } = value
  const minutes = Math.max(
    1,
    (Math.min(new Date(value.to).getTime(), new Date(value.generated_at).getTime()) -
      new Date(value.from).getTime()) /
      60_000,
  )
  const rate = successRate(totals)
  const previousRate = successRate(previous)
  const series = (pick: (bucket: UsageStats) => number | null) =>
    buckets.map(pick).filter((item): item is number => item !== null)
  return [
    {
      label: t('usage.requests'),
      icon: 'requests' as const,
      value: integer(totals.requests),
      help: t('usage.requestsHelp'),
      details: [
        { label: t('usage.succeeded'), value: compact(totals.succeeded) },
        {
          label: t('usage.failed'),
          value: compact(totals.failed),
          tone: totals.failed > 0 ? 'text-red-400' : undefined,
        },
        { label: t('usage.canceled'), value: compact(totals.canceled) },
      ],
      delta: delta(relative(totals.requests, previous.requests), (item) => percent(item), 'none'),
      spark: buckets.map((bucket) => bucket.requests),
      color: '#60a5fa',
    },
    {
      label: t('usage.successRate'),
      icon: 'success' as const,
      value: percent(rate),
      help: t('usage.successRateHelp'),
      details: [
        {
          label: t('usage.rateLimited'),
          value: compact(totals.rate_limited),
          tone: totals.rate_limited > 0 ? 'text-yellow-400' : undefined,
        },
        {
          label: t('usage.otherErrors'),
          value: compact(totals.failed - totals.rate_limited),
          tone: totals.failed - totals.rate_limited > 0 ? 'text-red-400' : undefined,
        },
      ],
      delta: delta(
        rate === null || previousRate === null ? null : rate - previousRate,
        (item) => t('usage.points').replace('{value}', (item * 100).toFixed(1)),
        'up',
      ),
      spark: series((bucket) => {
        const item = successRate(bucket)
        return item === null ? null : item * 100
      }),
      color: '#22c55e',
      tone: rateTone(rate),
    },
    {
      label: t('usage.tokens'),
      icon: 'tokens' as const,
      value: compact(totals.total_tokens),
      help: `${t('usage.input')} + ${t('usage.reasoning')} + ${t('usage.reply')}`,
      details: [
        { label: t('usage.input'), value: compact(totals.input_tokens) },
        { label: t('usage.reasoning'), value: compact(totals.reasoning_tokens) },
        { label: t('usage.reply'), value: compact(totals.reply_tokens) },
      ],
      delta: delta(
        relative(totals.total_tokens, previous.total_tokens),
        (item) => percent(item),
        'none',
      ),
      spark: buckets.map((bucket) => bucket.total_tokens),
      color: '#2dd4bf',
    },
    {
      label: t('usage.throughput'),
      icon: 'throughput' as const,
      value: `${compact(totals.requests / minutes)} RPM`,
      help: t('usage.throughputHelp').replace('{minutes}', String(recent.minutes)),
      details: [
        { label: 'TPM', value: compact(totals.total_tokens / minutes) },
        {
          label: `${t('usage.current')} RPM`,
          value: compact(recent.requests / recent.minutes),
        },
        { label: `${t('usage.current')} TPM`, value: compact(recent.tokens / recent.minutes) },
      ],
      spark: buckets.map((bucket) => bucket.requests),
      color: '#f59e0b',
    },
    {
      label: `${t('usage.duration')} P95`,
      icon: 'latency' as const,
      value: duration(totals.duration.p95_ms),
      help: t('usage.durationHelp'),
      details: [
        { label: 'P50', value: duration(totals.duration.p50_ms) },
        { label: 'P99', value: duration(totals.duration.p99_ms) },
        { label: t('usage.average'), value: duration(totals.duration.avg_ms) },
      ],
      delta: delta(
        relative(totals.duration.p95_ms, previous.duration.p95_ms),
        (item) => percent(item),
        'down',
      ),
      spark: series((bucket) => (bucket.duration.p95_ms > 0 ? bucket.duration.p95_ms : null)),
      color: '#a78bfa',
    },
    {
      label: `${t('usage.firstEvent')} P95`,
      icon: 'firstEvent' as const,
      value: duration(totals.first_event.p95_ms),
      help: t('usage.firstEventHelp'),
      details: [
        { label: 'P50', value: duration(totals.first_event.p50_ms) },
        { label: t('usage.average'), value: duration(totals.first_event.avg_ms) },
        { label: t('usage.queue'), value: duration(totals.queue_avg_ms) },
      ],
      delta: delta(
        relative(totals.first_event.p95_ms, previous.first_event.p95_ms),
        (item) => percent(item),
        'down',
      ),
      spark: series((bucket) => (bucket.first_event.p95_ms > 0 ? bucket.first_event.p95_ms : null)),
      color: '#34d399',
    },
  ]
})

const statusOptions = computed(() =>
  (report.value?.groups.status ?? []).map((group) => group.key).sort(),
)
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col overflow-auto">
    <header class="flex flex-wrap items-center gap-3 border-b border-line px-4 py-4 md:px-8">
      <div class="min-w-0 flex-1">
        <h2 class="text-xl font-semibold text-white">{{ t('usage.title') }}</h2>
        <p class="mt-1 text-sm text-gray-500">{{ t('usage.description') }}</p>
      </div>
      <div class="flex flex-wrap items-center gap-x-4 gap-y-2 text-xs text-gray-500">
        <span v-tooltip="t('usage.latestHelp')" class="flex items-center gap-1.5">
          <span class="h-2 w-2 rounded-full" :class="freshness.tone" aria-hidden="true"></span>
          {{ freshness.text }}
        </span>
        <span v-if="updatedAt > 0" class="tabular-nums" aria-live="polite">
          {{ t('usage.updatedAt').replace('{time}', clock(new Date(updatedAt))) }}
          <span v-if="countdown" class="ml-1 text-gray-600">· {{ countdown }}</span>
        </span>
        <UiSelect
          v-if="live"
          :model-value="autoRefresh"
          :aria-label="t('usage.autoRefresh')"
          class="w-32 rounded-md border border-line bg-canvas px-2 py-1 text-xs text-gray-200"
          @update:model-value="autoRefresh = Number($event)"
        >
          <option v-for="choice in refreshChoices" :key="choice" :value="choice">
            {{ t(`usage.autoRefresh.${choice}`) }}
          </option>
        </UiSelect>
        <button
          v-tooltip="t('app.refresh')"
          type="button"
          class="rounded-md border border-line p-1.5 text-gray-400 transition hover:bg-raised hover:text-white"
          :aria-label="t('app.refresh')"
          @click="refresh"
        >
          <UiIcon name="refresh" :size="14" :class="{ 'animate-spin': loading }" />
        </button>
      </div>
    </header>

    <div
      class="sticky top-0 z-10 flex flex-wrap items-center gap-2 border-b border-line bg-canvas/95 px-4 py-2.5 backdrop-blur md:px-8"
      role="toolbar"
      :aria-label="t('usage.filters')"
    >
      <RangePicker :range="state.range" :resolved="resolved" @apply="applyRange" />
      <span class="mx-1 hidden h-5 w-px bg-line sm:block" aria-hidden="true"></span>
      <FilterMenu
        v-for="dimension in dimensions"
        :key="dimension"
        :label="t(`usage.dimension.${dimension}`)"
        :options="report?.options[dimension] ?? []"
        :selected="state.filters[dimension] ?? []"
        :format="(value: string) => dimensionLabel(dimension, value)"
        @change="setFilter(dimension, $event)"
      />
      <button
        v-if="hasFilters"
        type="button"
        class="rounded-md px-2 py-1.5 text-xs text-gray-400 hover:bg-raised hover:text-white"
        @click="clearFilters"
      >
        {{ t('usage.filtersClear') }}
      </button>
    </div>
    <div v-if="activeChips.length > 0" class="flex flex-wrap gap-1.5 px-4 pt-3 md:px-8">
      <span
        v-for="chip in activeChips"
        :key="`${chip.dimension}:${chip.value}`"
        class="flex items-center gap-1 rounded-md border border-blue-500/40 bg-blue-600/10 py-0.5 pr-1 pl-2.5 text-xs text-blue-200"
      >
        <span class="text-blue-300/70">{{ t(`usage.dimension.${chip.dimension}`) }}:</span>
        {{ dimensionLabel(chip.dimension, chip.value) }}
        <button
          type="button"
          class="rounded-sm p-0.5 hover:bg-blue-500/20"
          :aria-label="`${t('common.close')} ${dimensionLabel(chip.dimension, chip.value)}`"
          @click="
            setFilter(
              chip.dimension,
              (state.filters[chip.dimension] ?? []).filter((item) => item !== chip.value),
            )
          "
        >
          <UiIcon name="close" :size="11" />
        </button>
      </span>
    </div>

    <div class="mx-auto w-full max-w-[96rem] space-y-4 p-4 md:p-8">
      <div
        v-if="error && status !== 'error'"
        class="flex items-center gap-3 rounded-lg border border-red-500/40 bg-red-500/10 px-4 py-3 text-sm text-red-300"
        role="alert"
      >
        <UiIcon name="warning" :size="16" />
        <span class="flex-1">{{ error }}</span>
        <button
          type="button"
          class="rounded-md border border-red-500/40 px-3 py-1 text-xs hover:bg-red-500/10"
          @click="load()"
        >
          {{ t('usage.retry') }}
        </button>
      </div>

      <template v-if="status === 'loading'">
        <div
          class="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-6"
          aria-hidden="true"
        >
          <div v-for="index in 6" :key="index" class="h-32 animate-pulse rounded-lg bg-panel"></div>
        </div>
        <div class="h-80 animate-pulse rounded-lg bg-panel" aria-hidden="true"></div>
        <p class="sr-only" role="status">{{ t('common.loading') }}</p>
      </template>

      <div
        v-else-if="status === 'error'"
        class="flex flex-col items-center gap-3 rounded-lg border border-red-500/40 bg-red-500/5 px-6 py-16 text-center"
        role="alert"
      >
        <UiIcon name="warning" :size="28" class="text-red-400" />
        <p class="text-sm text-red-300">{{ error }}</p>
        <button
          type="button"
          class="rounded-md border border-red-500/40 px-3 py-1 text-xs text-red-200 hover:bg-red-500/10"
          @click="load()"
        >
          {{ t('usage.retry') }}
        </button>
      </div>

      <template v-else-if="report !== null">
        <div class="space-y-4" :aria-busy="loading">
          <div
            v-if="report.totals.requests === 0"
            class="flex flex-col items-center justify-center rounded-lg border border-dashed border-line px-6 py-20 text-center"
          >
            <UiIcon name="empty" :size="40" class="mb-3 text-gray-600" />
            <p class="text-sm font-medium text-gray-300">
              {{ hasFilters ? t('usage.emptyFiltered') : t('usage.empty') }}
            </p>
            <p class="mt-1 max-w-md text-xs text-gray-500">
              {{ hasFilters ? t('usage.emptyFilteredHelp') : t('usage.emptyHelp') }}
            </p>
            <button
              v-if="hasFilters"
              type="button"
              class="mt-4 rounded-md border border-line px-3 py-1.5 text-xs text-gray-300 hover:bg-raised"
              @click="clearFilters"
            >
              {{ t('usage.filtersClear') }}
            </button>
          </div>
          <template v-else>
            <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-6">
              <KpiCard
                v-for="kpi in kpis"
                :key="kpi.label"
                :label="kpi.label"
                :icon="kpi.icon"
                :value="kpi.value"
                :help="kpi.help"
                :details="kpi.details"
                :delta="kpi.delta"
                :spark="kpi.spark"
                :color="kpi.color"
                :tone="kpi.tone"
              />
            </div>
            <UsageCharts
              :report="report"
              :stack="state.stack"
              @update:stack="state.stack = $event"
            />
            <RankingTable
              :report="report"
              :now="now"
              @filter="addFilter"
              @status="state.status = $event"
            />
            <RecordsTable
              :scope="scope"
              :base-query="baseQuery"
              :status="state.status"
              :search="state.search"
              :total="report.totals.requests"
              :status-options="statusOptions"
              :refresh-key="recordsRefresh"
              @update:status="state.status = $event"
              @update:search="state.search = $event"
            />
          </template>
        </div>
      </template>
    </div>
  </section>
</template>
