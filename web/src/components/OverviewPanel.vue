<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from '@/api'
import { useI18n, type TranslationKey } from '@/i18n'
import type {
  Account,
  Cooldown,
  Model,
  RequestSummary,
  ServiceStatus,
  TabID,
  UsageRecord,
  UsageReport,
} from '@/types'
import { groupCooldowns, pacificDayStart } from '@/overview-metrics'
import UiIcon from './UiIcon.vue'

const props = defineProps<{
  status: ServiceStatus | null
  serviceState: 'unavailable' | 'running' | 'launching' | 'stopped'
  accounts: Account[]
  models: Model[]
  cooldowns: Cooldown[]
  requests: RequestSummary[]
  refreshKey: number
}>()

const emit = defineEmits<{
  refresh: []
  select: [tab: TabID]
  start: []
  stop: []
}>()

const { locale, t, errorText } = useI18n()

const accountCounts = computed(() => {
  if (props.status !== null) return props.status.accounts
  return props.accounts.reduce(
    (counts, account) => {
      counts.total++
      if (account.state === 'ready') counts.ready++
      if (account.state === 'busy') counts.busy++
      if (account.state === 'cooldown') counts.cooldown++
      if (account.state === 'auth_required') counts.auth_required++
      return counts
    },
    { total: 0, ready: 0, busy: 0, cooldown: 0, auth_required: 0 },
  )
})

const todayReport = ref<UsageReport | null>(null)
const recentRecords = ref<UsageRecord[]>([])

const requestStats = computed(() => {
  const totals = todayReport.value?.totals
  if (totals === undefined) return { total: 0, success: 0, failed: 0, cancelled: 0 }
  return {
    total: totals.requests,
    success: totals.succeeded,
    failed: totals.failed,
    cancelled: totals.canceled,
  }
})

const successRate = computed(() => {
  if (requestStats.value.total === 0) return '—'
  return `${Math.round((requestStats.value.success / requestStats.value.total) * 100)}%`
})

const accountRows = computed(() =>
  props.accounts
    .slice()
    .sort((left, right) => {
      const order = {
        auth_required: 0,
        cooldown: 1,
        unavailable: 2,
        busy: 3,
        ready: 4,
        disabled: 5,
      }
      return (
        (order[left.state] ?? 9) - (order[right.state] ?? 9) ||
        left.label.localeCompare(right.label)
      )
    })
    .slice(0, 8),
)

const recentErrors = computed(() =>
  recentRecords.value
    .filter((record) => record.state === 'failed' || record.status >= 400)
    .slice(0, 5),
)

const modelUsage = computed(() =>
  (todayReport.value?.groups.model ?? [])
    .filter((group) => group.key !== '' && !group.key.startsWith('__') && group.requests > 0)
    .slice(0, 6)
    .map((group) => [group.key, group.requests] as [string, number]),
)

const modelCooldowns = computed(() => groupCooldowns(props.cooldowns).slice(0, 6))

const todayUsage = computed(() => todayReport.value?.groups.account ?? [])
const todayUsageLoading = ref(false)
const todayUsageError = ref('')
let usageController: AbortController | undefined

// loadTodayUsage 读取太平洋时区当天的账户用量与最近请求记录
async function loadTodayUsage(): Promise<void> {
  usageController?.abort()
  const current = new AbortController()
  usageController = current
  todayUsageLoading.value = true
  try {
    const now = new Date()
    const from = pacificDayStart(now).toISOString()
    const to = now.toISOString()
    const usageQuery = new URLSearchParams({
      from,
      to,
      tz: 'America/Los_Angeles',
      stack: 'account',
      bucket: '86400',
    })
    const recordQuery = new URLSearchParams({ from, to, limit: '50' })
    const [report, records] = await Promise.all([
      api.usage(usageQuery, current.signal),
      api.usageRecords(recordQuery, current.signal),
    ])
    todayReport.value = report
    recentRecords.value = records.items
    todayUsageError.value = ''
  } catch (reason) {
    if (current.signal.aborted) return
    todayUsageError.value = errorText(reason)
  } finally {
    if (usageController === current) todayUsageLoading.value = false
  }
}

onMounted(loadTodayUsage)
watch(
  () => props.refreshKey,
  () => void loadTodayUsage(),
)
onBeforeUnmount(() => usageController?.abort())

// compactNumber 用当前语言把大数值压成万/百万级显示
function compactNumber(value: number): string {
  return new Intl.NumberFormat(locale.value, {
    notation: 'compact',
    maximumFractionDigits: 1,
  }).format(value)
}

const stateKeys: Record<Account['state'], TranslationKey> = {
  ready: 'state.ready',
  busy: 'state.busy',
  cooldown: 'state.cooldown',
  auth_required: 'state.auth_required',
  unavailable: 'state.unavailable',
  disabled: 'state.disabled',
}

const serviceKeys: Record<typeof props.serviceState, TranslationKey> = {
  running: 'overview.running',
  launching: 'overview.launching',
  stopped: 'overview.stopped',
  unavailable: 'overview.unavailable',
}

function formatTime(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.valueOf())) return '—'
  return new Intl.DateTimeFormat(locale.value, { hour: '2-digit', minute: '2-digit' }).format(date)
}

function accountStateClass(state: Account['state']): string {
  if (state === 'ready') return 'bg-green-400'
  if (state === 'busy') return 'bg-blue-400'
  if (state === 'cooldown') return 'bg-amber-400'
  if (state === 'auth_required' || state === 'unavailable') return 'bg-red-400'
  return 'bg-gray-500'
}

function open(tab: TabID): void {
  emit('select', tab)
}

// recordDot 按请求记录的终态给出状态点颜色
function recordDot(state: string): string {
  if (state === 'completed' || state === 'tool_calls') return 'bg-green-400'
  if (state === 'failed' || state === 'blocked') return 'bg-red-400'
  if (state === 'limited') return 'bg-amber-400'
  return 'bg-gray-400'
}
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col overflow-auto">
    <header
      class="flex flex-wrap items-center gap-3 border-b border-line px-4 py-4 md:px-[max(2rem,calc((100%_-_var(--container-5xl))/2_+_2rem))]"
    >
      <div class="min-w-0 flex-1">
        <h2 class="text-xl font-semibold text-white">{{ t('section.overview.title') }}</h2>
        <p class="mt-1 text-sm text-gray-500">{{ t('section.overview.description') }}</p>
      </div>
      <div class="flex shrink-0 items-center gap-2">
        <button
          type="button"
          class="flex items-center gap-2 rounded border border-line bg-panel px-3 py-2 text-sm text-gray-300 transition hover:border-blue-500 hover:text-white"
          @click="emit('refresh')"
        >
          <UiIcon name="refresh" :size="15" />
          {{ t('overview.refresh') }}
        </button>
        <button
          v-if="serviceState === 'stopped'"
          type="button"
          class="flex items-center gap-2 rounded bg-green-600 px-3 py-2 text-sm font-medium text-white transition hover:bg-green-500"
          @click="emit('start')"
        >
          <UiIcon name="play" :size="15" />
          {{ t('app.start') }}
        </button>
        <button
          v-else-if="serviceState === 'running' || serviceState === 'launching'"
          type="button"
          class="flex items-center gap-2 rounded bg-red-600 px-3 py-2 text-sm font-medium text-white transition hover:bg-red-500"
          @click="emit('stop')"
        >
          <UiIcon name="stop" :size="15" />
          {{ t('app.stop') }}
        </button>
      </div>
    </header>

    <div class="mx-auto w-full max-w-5xl p-4 md:p-8">
      <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <button
          type="button"
          class="rounded-lg border border-line bg-panel p-4 text-left transition hover:border-blue-500"
          @click="open('accounts')"
        >
          <div class="flex items-center justify-between text-xs text-gray-500">
            <span>{{ t('overview.accounts') }}</span>
            <UiIcon name="accounts" :size="16" />
          </div>
          <div class="mt-3 text-2xl font-semibold tabular-nums text-white">
            {{ accountCounts.ready
            }}<span class="text-base font-normal text-gray-500"> / {{ accountCounts.total }}</span>
          </div>
          <div class="mt-1 text-xs text-gray-500">
            {{ t('overview.ready') }} · {{ accountCounts.cooldown }} {{ t('overview.cooldowns') }}
          </div>
        </button>

        <button
          type="button"
          class="rounded-lg border border-line bg-panel p-4 text-left transition hover:border-blue-500"
          @click="open('models')"
        >
          <div class="flex items-center justify-between text-xs text-gray-500">
            <span>{{ t('overview.models') }}</span>
            <UiIcon name="models" :size="16" />
          </div>
          <div class="mt-3 text-2xl font-semibold tabular-nums text-white">{{ models.length }}</div>
          <div class="mt-1 text-xs text-gray-500">
            {{ accountCounts.auth_required }} {{ t('overview.authRequired') }}
          </div>
        </button>

        <button
          type="button"
          class="rounded-lg border border-line bg-panel p-4 text-left transition hover:border-blue-500"
          @click="open('requests')"
        >
          <div class="flex items-center justify-between text-xs text-gray-500">
            <span>{{ t('overview.activeRequests') }}</span>
            <UiIcon name="requests" :size="16" />
          </div>
          <div class="mt-3 text-2xl font-semibold tabular-nums text-white">
            {{
              status?.active_requests ??
              requests.filter((request) => request.state === 'running').length
            }}
          </div>
          <div class="mt-1 text-xs text-gray-500">
            {{ requestStats.total }} {{ t('overview.requests') }} · {{ successRate }}
            {{ t('overview.requestSuccess') }}
          </div>
        </button>

        <button
          type="button"
          class="rounded-lg border border-line bg-panel p-4 text-left transition hover:border-blue-500"
          @click="open('logs')"
        >
          <div class="flex items-center justify-between text-xs text-gray-500">
            <span>{{ t('overview.service') }}</span>
            <UiIcon name="service" :size="16" />
          </div>
          <div class="mt-3 flex items-center gap-2 text-2xl font-semibold text-white">
            <span
              class="size-2.5 rounded-full"
              :class="
                serviceState === 'running'
                  ? 'bg-green-400'
                  : serviceState === 'launching'
                    ? 'bg-cyan-400'
                    : 'bg-gray-500'
              "
            ></span>
            {{ t(serviceKeys[serviceState]) }}
          </div>
          <div class="mt-1 truncate font-mono text-xs text-gray-500">
            {{ status?.version || '—' }}
          </div>
        </button>
      </div>

      <div class="mt-4 grid gap-4 xl:grid-cols-[1.2fr_0.8fr]">
        <section class="rounded-lg border border-line bg-panel">
          <header class="flex items-center justify-between border-b border-line px-4 py-3">
            <div>
              <h3 class="font-medium text-white">{{ t('overview.accountHealth') }}</h3>
              <p class="mt-0.5 text-xs text-gray-500">{{ t('overview.accountHealthHint') }}</p>
            </div>
            <button
              type="button"
              class="shrink-0 text-xs whitespace-nowrap text-blue-400 hover:text-blue-300"
              @click="open('accounts')"
            >
              {{ t('overview.openAccounts') }}
            </button>
          </header>
          <div v-if="accountRows.length === 0" class="p-8 text-center text-sm text-gray-500">
            {{ t('common.empty') }}
          </div>
          <div v-else class="divide-y divide-line/60">
            <div
              v-for="account in accountRows"
              :key="account.id"
              class="flex items-center gap-3 px-4 py-3"
            >
              <span
                class="size-2 shrink-0 rounded-full"
                :class="accountStateClass(account.state)"
              ></span>
              <div class="min-w-0 flex-1">
                <div class="truncate text-sm text-gray-200">{{ account.label || account.id }}</div>
                <div class="truncate text-xs text-gray-500">
                  {{ account.benefit_tier || t('accounts.tierUnknown') }} ·
                  {{ account.models.length }} {{ t('overview.models') }}
                </div>
              </div>
              <span
                class="shrink-0 text-xs"
                :class="account.state === 'ready' ? 'text-green-400' : 'text-gray-400'"
              >
                {{ t(stateKeys[account.state]) }}
              </span>
            </div>
          </div>
        </section>

        <section class="rounded-lg border border-line bg-panel">
          <header class="flex items-center justify-between border-b border-line px-4 py-3">
            <div>
              <h3 class="font-medium text-white">{{ t('overview.modelCoverage') }}</h3>
              <p class="mt-0.5 text-xs text-gray-500">{{ t('overview.modelCoverageHint') }}</p>
            </div>
            <button
              type="button"
              class="shrink-0 text-xs whitespace-nowrap text-blue-400 hover:text-blue-300"
              @click="open('usage')"
            >
              {{ t('overview.openUsage') }}
            </button>
          </header>
          <div v-if="modelUsage.length === 0" class="p-8 text-center text-sm text-gray-500">
            {{ t('overview.noRequests') }}
          </div>
          <div v-else class="divide-y divide-line/60">
            <div
              v-for="[model, count] in modelUsage"
              :key="model"
              class="flex items-center gap-3 px-4 py-3"
            >
              <code class="min-w-0 flex-1 truncate text-xs text-blue-300">{{ model }}</code>
              <span class="shrink-0 text-xs tabular-nums text-gray-400"
                >{{ count }} {{ t('overview.requests') }}</span
              >
            </div>
          </div>
        </section>
      </div>

      <div class="mt-4 grid gap-4 xl:grid-cols-3">
        <section class="rounded-lg border border-line bg-panel">
          <header class="flex items-center justify-between border-b border-line px-4 py-3">
            <div>
              <h3 class="font-medium text-white">{{ t('overview.modelCooldowns') }}</h3>
              <p class="mt-0.5 text-xs text-gray-500">{{ t('overview.modelCooldownsHint') }}</p>
            </div>
            <button
              type="button"
              class="shrink-0 text-xs whitespace-nowrap text-blue-400 hover:text-blue-300"
              @click="open('requests')"
            >
              {{ t('overview.openRequests') }}
            </button>
          </header>
          <div v-if="modelCooldowns.length === 0" class="p-8 text-center text-sm text-gray-500">
            {{ t('overview.noCooldowns') }}
          </div>
          <div v-else class="divide-y divide-line/60">
            <div
              v-for="item in modelCooldowns"
              :key="item.model"
              class="flex items-center gap-3 px-4 py-3"
            >
              <span class="size-2 shrink-0 rounded-full bg-amber-400"></span>
              <div class="min-w-0 flex-1">
                <code class="block truncate text-xs text-gray-300">{{ item.model }}</code>
                <span class="block text-xs text-gray-500">
                  {{ item.accounts }} {{ t('overview.cooldownAccounts') }}
                </span>
              </div>
              <span class="shrink-0 text-xs text-gray-400">
                {{ t('overview.recoverAt') }} {{ formatTime(item.until) }}
              </span>
            </div>
          </div>
        </section>

        <section class="rounded-lg border border-line bg-panel">
          <header class="flex items-center justify-between border-b border-line px-4 py-3">
            <div>
              <h3 class="font-medium text-white">{{ t('overview.recentErrors') }}</h3>
              <p class="mt-0.5 text-xs text-gray-500">{{ t('overview.recentErrorsHint') }}</p>
            </div>
            <button
              type="button"
              class="shrink-0 text-xs whitespace-nowrap text-blue-400 hover:text-blue-300"
              @click="open('logs')"
            >
              {{ t('overview.openLogs') }}
            </button>
          </header>
          <div v-if="recentErrors.length === 0" class="p-8 text-center text-sm text-gray-500">
            {{ t('overview.noErrors') }}
          </div>
          <div v-else class="divide-y divide-line/60">
            <div v-for="request in recentErrors" :key="request.id" class="px-4 py-3">
              <div class="flex items-center gap-2 text-xs">
                <span class="rounded border border-red-500/40 px-1.5 py-0.5 text-red-300">{{
                  request.status || 'ERR'
                }}</span>
                <code class="min-w-0 flex-1 truncate text-gray-300">{{
                  request.model || request.id
                }}</code>
              </div>
              <div class="mt-1 truncate text-xs text-gray-500">
                {{ request.error || t('overview.requestFailed') }}
              </div>
            </div>
          </div>
        </section>

        <section class="rounded-lg border border-line bg-panel">
          <header class="flex items-center justify-between border-b border-line px-4 py-3">
            <div>
              <h3 class="font-medium text-white">{{ t('overview.latestRequests') }}</h3>
              <p class="mt-0.5 text-xs text-gray-500">{{ t('overview.latestRequestsHint') }}</p>
            </div>
            <button
              type="button"
              class="shrink-0 text-xs whitespace-nowrap text-blue-400 hover:text-blue-300"
              @click="open('requests')"
            >
              {{ t('overview.openRequests') }}
            </button>
          </header>
          <div v-if="recentRecords.length === 0" class="p-8 text-center text-sm text-gray-500">
            {{ t('overview.noRequests') }}
          </div>
          <div v-else class="divide-y divide-line/60">
            <div
              v-for="record in recentRecords.slice(0, 5)"
              :key="record.id"
              class="flex items-center gap-3 px-4 py-3"
            >
              <span class="size-2 shrink-0 rounded-full" :class="recordDot(record.state)"></span>
              <div class="min-w-0 flex-1">
                <code class="block truncate text-xs text-gray-300">{{ record.model || '—' }}</code>
                <span class="block truncate text-xs text-gray-500">{{
                  record.account || '—'
                }}</span>
              </div>
              <span class="shrink-0 text-xs text-gray-500">{{ formatTime(record.time) }}</span>
            </div>
          </div>
        </section>
      </div>

      <section class="mt-4 rounded-lg border border-line bg-panel">
        <header
          class="flex flex-wrap items-center justify-between gap-3 border-b border-line px-4 py-3"
        >
          <div>
            <h3 class="font-medium text-white">{{ t('overview.todayUsage') }}</h3>
            <p class="mt-0.5 text-xs text-gray-500">{{ t('overview.todayUsageHint') }}</p>
          </div>
          <span class="text-xs text-gray-400 tabular-nums">{{
            t('overview.todayTotals')
              .replace('{requests}', compactNumber(todayReport?.totals.requests ?? 0))
              .replace('{tokens}', compactNumber(todayReport?.totals.total_tokens ?? 0))
          }}</span>
        </header>
        <div v-if="todayUsageError" role="alert" class="px-4 py-6 text-sm text-red-300">
          {{ todayUsageError }}
        </div>
        <div v-else-if="todayUsageLoading && !todayReport" class="px-4 py-6 text-sm text-gray-500">
          {{ t('common.loading') }}
        </div>
        <div v-else-if="todayUsage.length === 0" class="px-4 py-6 text-sm text-gray-500">
          {{ t('overview.noRequests') }}
        </div>
        <div v-else class="divide-y divide-line/60">
          <div
            v-for="group in todayUsage"
            :key="group.key"
            class="flex items-center justify-between gap-3 px-4 py-2.5"
          >
            <span class="min-w-0 truncate text-sm text-gray-200">{{ group.key }}</span>
            <span class="shrink-0 text-xs text-gray-400 tabular-nums">{{
              t('overview.todayTotals')
                .replace('{requests}', compactNumber(group.requests))
                .replace('{tokens}', compactNumber(group.total_tokens))
            }}</span>
          </div>
        </div>
      </section>
    </div>
  </section>
</template>
