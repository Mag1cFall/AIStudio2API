<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api'
import { useDownloadProgress } from '@/camoufox'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import { maskedProxy } from '@/proxy'
import type { Account, AccountExit, PreparationStatus } from '@/types'
import CamoufoxProgressBar from './CamoufoxProgressBar.vue'
import UiIcon from './UiIcon.vue'

const props = defineProps<{ accounts: Account[]; geoip: PreparationStatus | undefined }>()
const { t, errorText, logText } = useI18n()
const { exitLookups } = useConsole()
// geoipPercent 与 geoipSize 是出口 IP 库的下载进度，查询出口所在地要等它下载完
const { percent: geoipPercent, sizeText: geoipSize } = useDownloadProgress(() => props.geoip)

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
  exitLookups.value += 1
  try {
    const result = await api.accountExits()
    if (current === sequence) exits.value = result
  } catch (error) {
    if (current === sequence) failure.value = errorText(error)
  } finally {
    exitLookups.value -= 1
    if (current === sequence) loading.value = false
  }
}
watch(egressKey, () => void loadExits(), { immediate: true })

// groups 按出口 IP 合并代理并统计账户数，账户多的出口在前
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
    .sort((left, right) => right.accounts - left.accounts)
})
// proxyText 返回出口所用代理的显示文字
function proxyText(proxies: string[]): string {
  return proxies.map((proxy) => (proxy ? maskedProxy(proxy) : t('accounts.direct'))).join(', ')
}
</script>

<template>
  <section class="mb-3 space-y-2" :aria-label="t('accounts.exits')">
    <div class="flex flex-wrap items-center gap-2 text-xs">
      <span class="text-gray-400">{{ t('accounts.exits') }}</span>
      <span v-if="loading && groups.length === 0" class="flex items-center gap-1.5 text-gray-500">
        <UiIcon name="spinner" :size="13" />{{ t('accounts.exitLooking') }}
      </span>
      <span v-else-if="failure" v-tooltip="failure" class="max-w-full truncate text-red-300">{{
        failure
      }}</span>
      <span
        v-for="group in groups"
        :key="group.key"
        v-tooltip="
          group.error
            ? `${proxyText(group.proxies)} · ${logText(group.error)}`
            : proxyText(group.proxies)
        "
        class="inline-flex max-w-full items-center gap-1.5 rounded-md border px-2 py-0.5"
        :class="
          group.error
            ? 'border-red-400/30 bg-red-400/10 text-red-300'
            : group.accounts > 1
              ? 'border-yellow-400/30 bg-yellow-400/10 text-yellow-300'
              : 'border-line bg-panel text-gray-300'
        "
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
    <div v-if="geoip?.preparing" role="status" class="max-w-xl text-xs text-gray-400">
      <div class="flex flex-wrap justify-between gap-x-3 gap-y-1">
        <span>{{ t('accounts.downloadingGeoip') }}</span>
        <span v-if="geoipSize" class="font-mono text-gray-500 tabular-nums"
          >{{ geoipSize
          }}<template v-if="geoipPercent !== null"> · {{ geoipPercent }}%</template></span
        >
      </div>
      <CamoufoxProgressBar
        class="mt-1.5"
        :label="t('accounts.downloadingGeoip')"
        :percent="geoipPercent"
        :size-text="geoipSize"
      />
    </div>
    <p
      v-else-if="geoip?.error && !geoip.ready"
      role="alert"
      class="flex items-start gap-2 text-xs break-words text-red-300"
    >
      <UiIcon name="warning" :size="14" class="mt-px shrink-0" />{{
        t('accounts.geoipFailed').replace('{reason}', logText(geoip.error))
      }}
    </p>
  </section>
</template>
