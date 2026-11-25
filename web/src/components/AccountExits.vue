<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useTemplateRef, watch } from 'vue'
import { api } from '@/api'
import { useI18n } from '@/i18n'
import { maskedProxy } from '@/proxy'
import type { Account, AccountExit } from '@/types'
import DisclosureChevron from './DisclosureChevron.vue'
import UiIcon from './UiIcon.vue'

const props = defineProps<{ accounts: Account[] }>()
const { t, errorText, logText } = useI18n()

// exitGroup 是同一出口 IP 的代理与经它访问上游的账户数，查询失败的代理各成一组
interface exitGroup {
  key: string
  ip: string
  country: string
  proxies: string[]
  accounts: number
  error: string
}

const exits = ref<AccountExit[]>([])
const loading = ref(false)
const failure = ref('')
// egressKey 是账户出口代理的集合，集合变化时重新查询出口
const egressKey = computed(() =>
  [...new Set(props.accounts.map((account) => account.egress))].sort().join('\n'),
)

// loadExits 查询各出口代理的公网 IP，只采用最近一次查询的结果
let sequence = 0
async function loadExits(): Promise<void> {
  const current = ++sequence
  loading.value = true
  failure.value = ''
  try {
    const result = await api.accountExits()
    if (current === sequence) exits.value = result
  } catch (error) {
    if (current === sequence) failure.value = errorText(error)
  } finally {
    if (current === sequence) loading.value = false
  }
}
watch(egressKey, () => void loadExits(), { immediate: true })

// groups 按出口 IP 合并代理并统计账户数，查询失败的在前，其余账户多的在前
const groups = computed(() => {
  const byKey = new Map<string, exitGroup>()
  for (const exit of exits.value) {
    const key = exit.ip || `error:${exit.proxy}`
    const group = byKey.get(key) ?? {
      key,
      ip: exit.ip ?? '',
      country: exit.country ?? '',
      proxies: [],
      accounts: 0,
      error: exit.error ?? '',
    }
    group.proxies.push(exit.proxy)
    group.accounts += props.accounts.filter((account) => account.egress === exit.proxy).length
    byKey.set(key, group)
  }
  return [...byKey.values()]
    .filter((group) => group.accounts > 0)
    .sort(
      (left, right) =>
        Number(right.error !== '') - Number(left.error !== '') || right.accounts - left.accounts,
    )
})

// summaryParts 是出口总数与共用、查询失败的出口数，tone 是该段文字的颜色
const summaryParts = computed(() => {
  if (groups.value.length === 0) return []
  const shared = groups.value.filter((group) => !group.error && group.accounts > 1).length
  const failed = groups.value.filter((group) => group.error).length
  const parts = [
    { text: t('accounts.exitsTotal').replace('{count}', String(groups.value.length)), tone: '' },
  ]
  if (shared > 0)
    parts.push({
      text: t('accounts.exitsShared').replace('{count}', String(shared)),
      tone: 'text-yellow-300',
    })
  if (failed > 0)
    parts.push({
      text: t('accounts.exitsFailed').replace('{count}', String(failed)),
      tone: 'text-red-300',
    })
  if (parts.length === 1) parts.push({ text: t('accounts.exitsSingle'), tone: '' })
  return parts
})
const summary = computed(() => summaryParts.value.map((part) => part.text).join(' · '))

const list = useTemplateRef<HTMLElement>('list')
// expanded 表示全部标签显示在限高的面板中
const expanded = ref(false)
// hidden 是收起时一行放不下的标签数，排在最后的这些标签不显示
const hidden = ref(0)

// measure 统计收起时一行放不下的标签数：换到第一行以外或在第一行被截断的标签及其后的标签
function measure(): void {
  if (expanded.value) return
  const items = Array.from(list.value!.children) as HTMLElement[]
  const overflow = items.findIndex(
    (item) =>
      item.offsetTop > items[0]!.offsetTop ||
      Array.from(item.querySelectorAll<HTMLElement>('.truncate')).some(
        (text) => text.scrollWidth > text.clientWidth,
      ),
  )
  hidden.value = overflow === -1 ? 0 : items.length - overflow
}
let observer: ResizeObserver | undefined
onMounted(() => {
  observer = new ResizeObserver(measure)
  observer.observe(list.value!)
})
onBeforeUnmount(() => observer?.disconnect())
watch([groups, expanded], () => void nextTick(measure))
// toggleFromRow 在出口区第一行任意处点击时展开或收起全部标签，展开后面板内的点击不收起
function toggleFromRow(event: MouseEvent): void {
  if (hidden.value === 0 && !expanded.value) return
  if (expanded.value && list.value!.contains(event.target as Node)) return
  expanded.value = !expanded.value
}
// proxyText 返回出口所用代理的显示文字
function proxyText(proxies: string[]): string {
  return proxies.map((proxy) => (proxy ? maskedProxy(proxy) : t('accounts.direct'))).join(', ')
}
</script>

<template>
  <section
    class="mb-3 grid grid-cols-[auto_minmax(0,auto)_minmax(0,1fr)_auto] items-center gap-x-2 text-xs"
    :class="{ 'cursor-pointer': hidden > 0 || expanded }"
    :aria-label="t('accounts.exits')"
    @click="toggleFromRow"
  >
    <span class="text-gray-400">{{ t('accounts.exits') }}</span>
    <span v-if="loading && groups.length === 0" class="flex items-center gap-1.5 text-gray-500">
      <UiIcon name="spinner" :size="13" />{{ t('accounts.exitLooking') }}
    </span>
    <span v-else-if="failure" v-tooltip="failure" class="truncate text-red-300">{{ failure }}</span>
    <span v-else v-tooltip.overflow="summary" class="truncate text-gray-500 tabular-nums">
      <template v-for="(part, index) in summaryParts" :key="part.text"
        ><template v-if="index > 0"> · </template
        ><span :class="part.tone">{{ part.text }}</span></template
      >
    </span>
    <div
      id="account-exit-list"
      ref="list"
      class="relative flex flex-wrap gap-2"
      :class="
        expanded
          ? 'col-span-full mt-2 max-h-40 cursor-auto overflow-y-auto rounded-md border border-line bg-panel p-2'
          : 'h-6 overflow-hidden'
      "
    >
      <span
        v-for="(group, index) in groups"
        :key="group.key"
        v-tooltip="
          group.error
            ? `${proxyText(group.proxies)} · ${logText(group.error)}`
            : proxyText(group.proxies)
        "
        class="inline-flex h-6 max-w-full items-center gap-1.5 rounded-md border px-2"
        :class="[
          group.error
            ? 'border-red-400/30 bg-red-400/10 text-red-300'
            : group.accounts > 1
              ? 'border-yellow-400/30 bg-yellow-400/10 text-yellow-300'
              : 'border-line bg-panel text-gray-300',
          { invisible: !expanded && index >= groups.length - hidden },
        ]"
      >
        <UiIcon :name="group.error ? 'warning' : 'network'" :size="13" class="shrink-0" />
        <span v-if="group.error" class="truncate">{{
          `${proxyText(group.proxies)} · ${t('accounts.exitFailed')}`
        }}</span>
        <span v-else class="truncate tabular-nums">{{ group.ip }}</span>
        <span v-if="group.country">{{ group.country }}</span>
        <span class="shrink-0 tabular-nums">{{
          t('accounts.exitAccounts').replace('{count}', String(group.accounts))
        }}</span>
      </span>
    </div>
    <button
      v-if="hidden > 0 || expanded"
      v-tooltip="
        expanded ? undefined : t('accounts.exitsExpand').replace('{count}', String(groups.length))
      "
      type="button"
      class="col-start-4 row-start-1 inline-flex h-6 items-center gap-1 rounded-md border border-line bg-panel px-2 text-gray-300 tabular-nums transition hover:bg-line hover:text-white"
      :aria-expanded="expanded"
      aria-controls="account-exit-list"
      :aria-label="
        expanded
          ? t('accounts.exitsCollapse')
          : t('accounts.exitsExpand').replace('{count}', String(groups.length))
      "
      @click.stop="expanded = !expanded"
    >
      {{ expanded ? t('accounts.exitsCollapse') : `+${hidden}`
      }}<DisclosureChevron :open="expanded" :turn="180"
        ><UiIcon name="chevronDown" :size="13"
      /></DisclosureChevron>
    </button>
  </section>
</template>
