<script setup lang="ts">
import { computed, ref, shallowRef, useId, watch } from 'vue'
import { useI18n } from '@/i18n'
import { usePaged } from '@/paging'
import type { UsageDimension, UsageReport, UsageStats } from '@/types'
import DisclosureChevron from '../DisclosureChevron.vue'
import UiCollapse from '../UiCollapse.vue'
import UiIcon from '../UiIcon.vue'
import UiPager from '../UiPager.vue'
import UsageAgo from './UsageAgo.vue'
import { useUsageFormat } from './format'
import { readPreference, writePreference } from '@/preference'
import { rateTone, successRate } from './model'

type Tab = UsageDimension | 'status'
type SortKey = 'key' | 'requests' | 'rate' | 'tokens' | 'p50' | 'p95' | 'first' | 'last'

type Breakdown = 'account' | 'model'
type Pairs = UsageReport['pairs']

const props = defineProps<{
  report: UsageReport
  loadPairs: (dimension: Breakdown, key: string) => Promise<Pairs>
}>()
const emit = defineEmits<{
  filter: [dimension: UsageDimension, key: string]
  status: [code: string]
}>()

const { t, errorText } = useI18n()
const { compact, integer, duration, percent, dimensionLabel } = useUsageFormat()
const id = useId()
const tabs: Tab[] = ['account', 'model', 'channel', 'protocol', 'state', 'status']
const tab = ref<Tab>(
  readPreference('usage.rankingTab', 'account', (value) => tabs.includes(value as Tab)),
)
const sort = ref<{ key: SortKey; descending: boolean }>({ key: 'requests', descending: true })
const expanded = ref(new Set<string>())
// shown 是显示组合明细的行：展开的行与正在收起的行，收起过渡结束后移除
const shown = ref(new Set<string>())
watch(tab, (value) => {
  writePreference('usage.rankingTab', value)
  expanded.value = new Set()
  shown.value = new Set()
})

const columns: { key: SortKey; label: () => string; numeric: boolean }[] = [
  { key: 'key', label: () => t(`usage.dimension.${tab.value}`), numeric: false },
  { key: 'requests', label: () => t('usage.requests'), numeric: true },
  { key: 'rate', label: () => t('usage.successRate'), numeric: true },
  { key: 'tokens', label: () => t('usage.tokens'), numeric: true },
  { key: 'p50', label: () => `${t('usage.duration')} P50`, numeric: true },
  { key: 'p95', label: () => `${t('usage.duration')} P95`, numeric: true },
  { key: 'first', label: () => `${t('usage.firstEvent')} P95`, numeric: true },
  { key: 'last', label: () => t('usage.lastRequest'), numeric: true },
]

// sortValue 返回行在排序列上的比较值
function sortValue(key: string, stats: UsageStats, column: SortKey): number | string {
  switch (column) {
    case 'key':
      return dimensionLabel(tab.value, key).toLowerCase()
    case 'requests':
      return stats.requests
    case 'rate':
      return successRate(stats) ?? -1
    case 'tokens':
      return stats.total_tokens
    case 'p50':
      return stats.duration.p50_ms
    case 'p95':
      return stats.duration.p95_ms
    case 'first':
      return stats.first_event.p95_ms
    case 'last':
      return stats.last_at ? new Date(stats.last_at).getTime() : 0
  }
}

const rows = computed(() => {
  const { key, descending } = sort.value
  return [...props.report.groups[tab.value]].sort((a, b) => {
    const left = sortValue(a.key, a, key)
    const right = sortValue(b.key, b, key)
    const order = left < right ? -1 : left > right ? 1 : 0
    return descending ? -order : order
  })
})

// 排行每页 50 行，切换维度或排序时回到第一页
const { page, pages, paged: pagedRows } = usePaged(rows, 50)
watch([tab, sort], () => (page.value = 1))

// breakdown 是能展开组合明细的维度，其余维度为 null
const breakdown = computed<Breakdown | null>(() =>
  tab.value === 'account' || tab.value === 'model' ? tab.value : null,
)
// pairs 保存已展开行按需读取的组合明细或读取失败的原因，键为维度与行取值
const pairs = shallowRef(new Map<string, Pairs | string>())

// fetchPairs 读取一行在当前范围与筛选下的组合明细，报告已更新时丢弃结果
async function fetchPairs(dimension: Breakdown, key: string): Promise<void> {
  const report = props.report
  const result = await props.loadPairs(dimension, key).catch((reason: unknown) => errorText(reason))
  if (props.report === report) pairs.value = new Map(pairs.value).set(`${dimension}:${key}`, result)
}

// 报告更新后重新读取仍展开的行，新明细到达前保留旧明细，其余行的明细在下次展开时读取
watch(
  () => props.report,
  () => {
    const dimension = breakdown.value
    const open = new Set([...expanded.value].map((key) => `${dimension}:${key}`))
    pairs.value = new Map([...pairs.value].filter(([name]) => open.has(name)))
    if (dimension !== null) for (const key of expanded.value) void fetchPairs(dimension, key)
  },
)

// details 是各展开行的组合明细；读取中或读取失败时 note 为提示文字
const details = computed(() => {
  const result = new Map<
    string,
    { note: string | null; rows: { key: string; label: string; stats: UsageStats }[] }
  >()
  const dimension = breakdown.value
  if (dimension === null) return result
  const other = dimension === 'account' ? 'model' : 'account'
  for (const key of shown.value) {
    const list = pairs.value.get(`${dimension}:${key}`)
    if (list === undefined || typeof list === 'string') {
      result.set(key, { note: list ?? t('common.loading'), rows: [] })
      continue
    }
    const rows = list
      .filter((pair) => pair[dimension] === key)
      .map((pair) => ({ key: pair[other], label: dimensionLabel(other, pair[other]), stats: pair }))
    result.set(key, { note: null, rows })
  }
  return result
})

// toggleSort 切换排序列，再次点击同一列时反转方向
function toggleSort(key: SortKey): void {
  sort.value =
    sort.value.key === key
      ? { key, descending: !sort.value.descending }
      : { key, descending: key !== 'key' }
}

// toggleRow 展开或收起一行的组合明细，首次展开时读取
function toggleRow(key: string): void {
  const next = new Set(expanded.value)
  if (next.has(key)) next.delete(key)
  else {
    next.add(key)
    shown.value = new Set(shown.value).add(key)
    if (!pairs.value.has(`${breakdown.value}:${key}`)) void fetchPairs(breakdown.value!, key)
  }
  expanded.value = next
}

// hideRow 在一行的明细收起过渡结束后不再显示它，期间又展开时保留
function hideRow(key: string): void {
  if (expanded.value.has(key) || !shown.value.has(key)) return
  const next = new Set(shown.value)
  next.delete(key)
  shown.value = next
}

// applyFilter 把行取值加入筛选；状态码行筛选请求记录
function applyFilter(key: string): void {
  if (tab.value === 'status') emit('status', key)
  else emit('filter', tab.value, key)
}

const total = computed(() => props.report.totals.requests)

// childCells 是组合明细行在各数值列中的文字与颜色，最后请求列由 UsageAgo 显示相对时间
function childCells(stats: UsageStats): { key: string; text: string; tone?: string }[] {
  return [
    { key: 'requests', text: integer(stats.requests) },
    { key: 'rate', text: percent(successRate(stats)), tone: rateTone(successRate(stats)) },
    { key: 'tokens', text: compact(stats.total_tokens) },
    { key: 'p50', text: duration(stats.duration.p50_ms) },
    { key: 'p95', text: duration(stats.duration.p95_ms) },
    { key: 'first', text: duration(stats.first_event.p95_ms) },
    { key: 'last', text: '—' },
  ]
}
</script>

<template>
  <section class="min-w-0 rounded-lg border border-line bg-panel">
    <header class="flex flex-wrap items-center gap-3 border-b border-line px-4 pt-3">
      <h3 class="mr-auto pb-3 text-sm font-medium text-gray-200">{{ t('usage.ranking') }}</h3>
      <div role="tablist" :aria-label="t('usage.ranking')" class="flex gap-1 overflow-x-auto">
        <button
          v-for="item in tabs"
          :id="`${id}-tab-${item}`"
          :key="item"
          type="button"
          role="tab"
          :aria-selected="tab === item"
          :aria-controls="`${id}-panel`"
          :tabindex="tab === item ? 0 : -1"
          class="border-b-2 px-2.5 pb-2.5 text-xs whitespace-nowrap transition-colors"
          :class="
            tab === item
              ? 'border-blue-500 text-white'
              : 'border-transparent text-gray-400 hover:text-gray-200'
          "
          @click="tab = item"
          @keydown.right.prevent="tab = tabs[(tabs.indexOf(item) + 1) % tabs.length]!"
          @keydown.left.prevent="tab = tabs[(tabs.indexOf(item) + tabs.length - 1) % tabs.length]!"
        >
          {{ t(`usage.dimension.${item}`) }}
          <span class="ml-1 text-gray-500 tabular-nums">{{ report.groups[item].length }}</span>
        </button>
      </div>
    </header>
    <div
      :id="`${id}-panel`"
      role="tabpanel"
      :aria-labelledby="`${id}-tab-${tab}`"
      class="relative overflow-x-auto"
    >
      <table class="w-full min-w-[56rem] text-left text-xs whitespace-nowrap">
        <thead class="text-gray-500">
          <tr>
            <th
              v-for="column in columns"
              :key="column.key"
              scope="col"
              class="py-2 font-normal"
              :class="column.numeric ? 'px-2 text-right' : 'px-4'"
              :aria-sort="
                sort.key === column.key ? (sort.descending ? 'descending' : 'ascending') : 'none'
              "
            >
              <button
                type="button"
                class="inline-flex items-center gap-1 hover:text-gray-200"
                :class="sort.key === column.key ? 'text-gray-200' : ''"
                @click="toggleSort(column.key)"
              >
                {{ column.label() }}
                <UiIcon
                  v-if="sort.key === column.key"
                  name="chevronDown"
                  :size="11"
                  :class="sort.descending ? '' : 'rotate-180'"
                />
              </button>
            </th>
            <th scope="col" class="w-10 px-2">
              <span class="sr-only">{{ t('usage.onlyThis') }}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          <template v-for="row in pagedRows" :key="row.key">
            <tr class="border-t border-line/60 text-gray-300 hover:bg-raised/40">
              <th scope="row" class="w-[30%] max-w-0 px-4 py-2 font-normal">
                <div class="flex items-center gap-1.5">
                  <button
                    v-if="breakdown !== null"
                    type="button"
                    class="rounded p-0.5 text-gray-500 hover:bg-raised hover:text-white"
                    :aria-expanded="expanded.has(row.key)"
                    :aria-label="
                      expanded.has(row.key) ? t('usage.hideBreakdown') : t('usage.showBreakdown')
                    "
                    @click="toggleRow(row.key)"
                  >
                    <DisclosureChevron :open="expanded.has(row.key)"
                      ><UiIcon name="chevronRight" :size="12"
                    /></DisclosureChevron>
                  </button>
                  <span v-else class="w-4"></span>
                  <span v-tooltip="dimensionLabel(tab, row.key)" class="truncate text-gray-200">{{
                    dimensionLabel(tab, row.key)
                  }}</span>
                </div>
                <div class="mt-1 ml-5 h-1 rounded-full bg-raised">
                  <div
                    class="h-1 rounded-full bg-blue-500/70"
                    :style="{ width: `${Math.max((row.requests / total) * 100, 1)}%` }"
                  ></div>
                </div>
              </th>
              <td class="px-2 py-2 text-right tabular-nums">
                {{ integer(row.requests) }}
                <span class="ml-1 text-gray-500">{{ percent(row.requests / total, 0) }}</span>
              </td>
              <td class="px-2 py-2 text-right tabular-nums" :class="rateTone(successRate(row))">
                {{ percent(successRate(row)) }}
              </td>
              <td class="px-2 py-2 text-right tabular-nums">{{ compact(row.total_tokens) }}</td>
              <td class="px-2 py-2 text-right tabular-nums">{{ duration(row.duration.p50_ms) }}</td>
              <td class="px-2 py-2 text-right tabular-nums">{{ duration(row.duration.p95_ms) }}</td>
              <td class="px-2 py-2 text-right tabular-nums">
                {{ duration(row.first_event.p95_ms) }}
              </td>
              <td class="px-2 py-2 text-right text-gray-400">
                <UsageAgo v-if="row.last_at" :at="row.last_at" /><template v-else>—</template>
              </td>
              <td class="px-2 py-2 text-right">
                <button
                  v-tooltip="t('usage.onlyThis')"
                  type="button"
                  class="rounded p-1 text-gray-500 hover:bg-raised hover:text-white"
                  :aria-label="`${t('usage.onlyThis')}: ${dimensionLabel(tab, row.key)}`"
                  @click="applyFilter(row.key)"
                >
                  <UiIcon name="filter" :size="13" />
                </button>
              </td>
            </tr>
            <template v-if="details.has(row.key)">
              <tr v-if="details.get(row.key)!.note !== null" class="bg-canvas/60 text-gray-500">
                <td colspan="9" class="p-0">
                  <UiCollapse :open="expanded.has(row.key)" @closed="hideRow(row.key)">
                    <div class="py-1.5 pr-4 pl-11">{{ details.get(row.key)!.note }}</div>
                  </UiCollapse>
                </td>
              </tr>
              <tr
                v-for="child in details.get(row.key)!.rows"
                :key="`${row.key}/${child.key}`"
                class="bg-canvas/60 text-gray-400"
              >
                <th scope="row" class="w-[30%] max-w-0 p-0 font-normal">
                  <UiCollapse :open="expanded.has(row.key)" @closed="hideRow(row.key)">
                    <span v-tooltip="child.label" class="block truncate py-1.5 pr-4 pl-11">{{
                      child.label
                    }}</span>
                  </UiCollapse>
                </th>
                <td v-for="cell in childCells(child.stats)" :key="cell.key" class="p-0">
                  <UiCollapse :open="expanded.has(row.key)">
                    <div class="px-2 py-1.5 text-right tabular-nums" :class="cell.tone">
                      <UsageAgo
                        v-if="cell.key === 'last' && child.stats.last_at"
                        :at="child.stats.last_at"
                      />
                      <template v-else>{{ cell.text }}</template>
                    </div>
                  </UiCollapse>
                </td>
                <td></td>
              </tr>
            </template>
          </template>
        </tbody>
      </table>
    </div>
    <UiPager v-model="page" :pages="pages" />
  </section>
</template>
