<script setup lang="ts">
defineProps<{ adminUsername?: string }>()
const emit = defineEmits<{ logout: [] }>()
import {
  computed,
  defineAsyncComponent,
  h,
  onMounted,
  onUnmounted,
  reactive,
  ref,
  shallowRef,
  watch,
} from 'vue'
import { api, openAdminEvents, type EventConnection } from '@/api'
import { useI18n, type TranslationKey } from '@/i18n'
import type {
  Account,
  AdminLog,
  AdminEvent,
  Model,
  Cooldown,
  RequestSummary,
  ServiceConfig,
  ServiceStatus,
  TabID,
} from '@/types'
import AccountsPanel from '@/components/AccountsPanel.vue'
import LogsPanel from '@/components/LogsPanel.vue'
import ModelsTable from '@/components/ModelsTable.vue'
import PlaygroundPanel from '@/components/PlaygroundPanel.vue'
import RequestsPanel from '@/components/RequestsPanel.vue'
import SettingsPanel from '@/components/SettingsPanel.vue'
import UiIcon from '@/components/UiIcon.vue'
import { useTabParam } from '@/url'
import type { IconName } from '@/icons'

const { availableLocales, locale, setLocale, t, errorText } = useI18n()
const UsagePanel = defineAsyncComponent({
  loader: () => import('@/components/UsagePanel.vue'),
  delay: 150,
  loadingComponent: {
    render: () =>
      h('p', { class: 'p-8 text-sm text-gray-500', role: 'status' }, t('usage.panelLoading')),
  },
  errorComponent: {
    render: () =>
      h('p', { class: 'p-8 text-sm text-red-300', role: 'alert' }, t('usage.panelFailed')),
  },
})
const validTabs: TabID[] = [
  'logs',
  'accounts',
  'models',
  'requests',
  'usage',
  'settings',
  'playground',
]
const { tab: currentTab, select: selectTab } = useTabParam(validTabs, 'logs')
const status = ref<ServiceStatus | null>(null)
const logs = shallowRef<AdminLog[]>([])
const accounts = ref<Account[]>([])
const models = ref<Model[]>([])
const cooldowns = ref<Cooldown[]>([])
const requests = ref<RequestSummary[]>([])
const config = ref<ServiceConfig | null>(null)
const startPending = ref(false)
const stopPending = ref(false)
const launchCancellationRequested = ref(false)
const notice = reactive({ message: '', tone: 'success' as 'success' | 'error' })
const loading = reactive({
  accounts: true,
  models: true,
  requests: true,
  cooldowns: true,
  config: true,
})
const errors = reactive({ accounts: '', models: '', requests: '', cooldowns: '', config: '' })
let eventConnection: EventConnection | undefined
let mounted = false
let noticeTimer: number | undefined
const pendingDelete = shallowRef<{ account: Account; timer: number } | null>(null)
const deletingAccounts = ref(new Set<string>())
const visibleAccounts = computed(() =>
  accounts.value.filter(
    (account) =>
      account.id !== pendingDelete.value?.account.id && !deletingAccounts.value.has(account.id),
  ),
)

const navigation: { id: TabID; label: TranslationKey; icon: IconName }[] = [
  { id: 'logs', label: 'nav.logs', icon: 'logs' },
  { id: 'accounts', label: 'nav.accounts', icon: 'accounts' },
  { id: 'models', label: 'nav.models', icon: 'models' },
  { id: 'requests', label: 'nav.requests', icon: 'requests' },
  { id: 'usage', label: 'nav.usage', icon: 'usage' },
  { id: 'settings', label: 'nav.settings', icon: 'settings' },
  { id: 'playground', label: 'nav.playground', icon: 'playground' },
]

const serviceState = computed(() => {
  if (status.value === null) return 'unavailable'
  if (status.value.state === 'RUNNING') return 'running'
  if (status.value.state === 'LAUNCHING') return 'launching'
  if (startPending.value && !launchCancellationRequested.value) return 'launching'
  return 'stopped'
})
const statusColor = computed(() => {
  if (serviceState.value === 'running') return 'bg-green-500 shadow-[0_0_10px_rgba(34,197,94,0.5)]'
  if (serviceState.value === 'launching') return 'bg-cyan-400 animate-pulse'
  return 'bg-gray-600'
})
const statusTextColor = computed(() => {
  if (serviceState.value === 'running') return 'text-green-400'
  if (serviceState.value === 'launching') return 'text-cyan-300'
  return 'text-gray-500'
})

// messageOf 统一呈现服务端错误内容
function messageOf(error: unknown): string {
  return errorText(error)
}

// showNotice 显示一次短暂操作结果
function showNotice(message: string, tone: 'success' | 'error'): void {
  notice.message = message
  notice.tone = tone
  if (noticeTimer !== undefined) window.clearTimeout(noticeTimer)
  noticeTimer = window.setTimeout(() => {
    notice.message = ''
  }, 3200)
}

// deleteAccount 隐藏账户并在撤销时限结束后删除，新的删除会先提交上一项
function deleteAccount(account: Account): void {
  void commitDelete()
  pendingDelete.value = { account, timer: window.setTimeout(() => void commitDelete(), 6000) }
}

// undoDelete 撤销尚未提交的账户删除
function undoDelete(): void {
  if (pendingDelete.value === null) return
  window.clearTimeout(pendingDelete.value.timer)
  pendingDelete.value = null
}

// commitDelete 立即提交等待中的账户删除，页面关闭时使用 keepalive 请求
async function commitDelete(keepalive = false): Promise<void> {
  const pending = pendingDelete.value
  if (pending === null) return
  window.clearTimeout(pending.timer)
  pendingDelete.value = null
  const id = pending.account.id
  deletingAccounts.value = new Set(deletingAccounts.value).add(id)
  try {
    await api.deleteAccount(id, keepalive)
    await loadAccountData()
  } catch (error) {
    showNotice(messageOf(error), 'error')
  } finally {
    const deleting = new Set(deletingAccounts.value)
    deleting.delete(id)
    deletingAccounts.value = deleting
  }
}

// flushDelete 在页面隐藏时提交等待中的删除
function flushDelete(): void {
  void commitDelete(true)
}

// logout 先在当前会话内提交等待中的删除再退出登录
async function logout(): Promise<void> {
  await commitDelete()
  emit('logout')
}

async function loadStatus(): Promise<void> {
  try {
    status.value = await api.status()
  } catch {
    status.value = null
  }
}

async function loadAccounts(): Promise<void> {
  loading.accounts = accounts.value.length === 0
  errors.accounts = ''
  try {
    accounts.value = await api.accounts()
  } catch (error) {
    errors.accounts = messageOf(error)
  } finally {
    loading.accounts = false
  }
}

async function loadAccountData(): Promise<void> {
  await Promise.all([loadAccounts(), loadModels(), loadStatus()])
}

async function loadModels(): Promise<void> {
  loading.models = models.value.length === 0
  errors.models = ''
  try {
    models.value = await api.models()
  } catch (error) {
    errors.models = messageOf(error)
  } finally {
    loading.models = false
  }
}

async function loadCooldowns(): Promise<void> {
  loading.cooldowns = cooldowns.value.length === 0
  errors.cooldowns = ''
  try {
    cooldowns.value = await api.cooldowns()
  } catch (error) {
    errors.cooldowns = messageOf(error)
  } finally {
    loading.cooldowns = false
  }
}

async function loadRequests(): Promise<void> {
  loading.requests = requests.value.length === 0
  errors.requests = ''
  try {
    requests.value = await api.requests()
  } catch (error) {
    errors.requests = messageOf(error)
  } finally {
    loading.requests = false
  }
}

async function loadRequestData(): Promise<void> {
  await Promise.all([loadRequests(), loadCooldowns()])
}

async function loadConfig(): Promise<void> {
  loading.config = config.value === null
  errors.config = ''
  try {
    config.value = await api.config()
  } catch (error) {
    errors.config = messageOf(error)
  } finally {
    loading.config = false
  }
}

async function refreshAll(): Promise<void> {
  await Promise.all([
    loadStatus(),
    loadAccounts(),
    loadModels(),
    loadCooldowns(),
    loadRequests(),
    loadConfig(),
  ])
}

async function startService(): Promise<void> {
  startPending.value = true
  launchCancellationRequested.value = false
  try {
    status.value = await api.startService()
    if (launchCancellationRequested.value || status.value.state !== 'RUNNING') return
    showNotice(t('app.start'), 'success')
    await Promise.all([loadAccounts(), loadModels(), loadCooldowns()])
  } catch (error) {
    if (!launchCancellationRequested.value) showNotice(messageOf(error), 'error')
    await loadStatus()
  } finally {
    startPending.value = false
  }
}

async function stopService(): Promise<void> {
  launchCancellationRequested.value = serviceState.value === 'launching'
  stopPending.value = true
  try {
    status.value = await api.stopService()
    showNotice(t('app.stop'), 'success')
    await Promise.all([loadAccounts(), loadModels(), loadCooldowns(), loadRequests()])
  } catch (error) {
    showNotice(messageOf(error), 'error')
    await loadStatus()
  } finally {
    stopPending.value = false
  }
}

let pendingLogs: AdminLog[] = []
let logFlushTimer: number | undefined

function flushLogs(): void {
  logFlushTimer = undefined
  if (pendingLogs.length === 0) return
  logs.value = logs.value.concat(pendingLogs).slice(-2000)
  pendingLogs = []
}

async function clearLogs(): Promise<void> {
  try {
    await api.clearLogs()
    pendingLogs = []
    if (logFlushTimer !== undefined) {
      window.clearTimeout(logFlushTimer)
      logFlushTimer = undefined
    }
    logs.value = []
  } catch (error) {
    showNotice(messageOf(error), 'error')
  }
}

function replaceByID<T extends { id: string }>(items: T[], incoming: T): void {
  const index = items.findIndex((item) => item.id === incoming.id)
  if (index === -1) items.unshift(incoming)
  else items[index] = incoming
}

function handleAdminEvent(event: AdminEvent): void {
  if (event.type === 'status') {
    status.value = event.data
    return
  }
  if (event.type === 'log') {
    pendingLogs.push(event.data)
    if (logFlushTimer === undefined) {
      logFlushTimer = window.setTimeout(flushLogs, 40)
    }
    return
  }
  if (event.type === 'accounts') {
    accounts.value = event.data.accounts
    return
  }
  if (event.type === 'models') {
    models.value = event.data.models
    return
  }
  if (event.type === 'cooldowns') {
    cooldowns.value = event.data
    return
  }
  replaceByID(requests.value, event.data)
}

onMounted(async () => {
  mounted = true
  window.addEventListener('pagehide', flushDelete)
  document.title = t('app.title')
  await refreshAll()
  if (!mounted) return
  eventConnection = openAdminEvents(handleAdminEvent, () => {
    pendingLogs = []
    if (logFlushTimer !== undefined) {
      window.clearTimeout(logFlushTimer)
      logFlushTimer = undefined
    }
    logs.value = []
    requests.value = []
  })
})

watch(locale, () => {
  document.title = t('app.title')
})

onUnmounted(() => {
  mounted = false
  eventConnection?.close()
  if (logFlushTimer !== undefined) window.clearTimeout(logFlushTimer)
  if (noticeTimer !== undefined) window.clearTimeout(noticeTimer)
  window.removeEventListener('pagehide', flushDelete)
  flushDelete()
})
</script>

<template>
  <div class="flex h-full w-full flex-col md:flex-row">
    <aside
      class="flex min-w-0 w-full shrink-0 flex-col border-b border-line bg-panel md:w-64 md:border-r md:border-b-0"
    >
      <div class="flex h-14 items-center justify-between gap-2 border-b border-line px-4">
        <div class="flex min-w-0 items-center gap-2">
          <div class="h-3 w-3 rounded-full" :class="statusColor"></div>
          <h1 class="whitespace-nowrap text-lg font-bold text-white">AI Studio Proxy</h1>
        </div>
        <div class="group relative">
          <button
            class="flex shrink-0 items-center gap-1 whitespace-nowrap rounded border border-gray-700 px-1.5 py-0.5 font-mono text-xs text-gray-500 transition hover:text-white"
            type="button"
          >
            {{ locale }}
            <UiIcon name="chevronDown" :size="12" />
          </button>
          <div class="absolute top-full right-0 z-50 hidden pt-1 group-hover:block">
            <div class="overflow-hidden rounded border border-line bg-panel shadow-xl">
              <button
                v-for="item in availableLocales"
                :key="item.code"
                class="block w-full px-4 py-2 text-left text-xs whitespace-nowrap text-gray-300 hover:bg-blue-600 hover:text-white"
                type="button"
                @click="setLocale(item.code)"
              >
                {{ item.label }}
              </button>
            </div>
          </div>
        </div>
      </div>

      <nav
        class="flex w-full min-w-0 flex-none gap-1 overflow-x-auto p-2 md:flex-1 md:flex-col md:space-y-1"
      >
        <button
          v-for="item in navigation"
          :key="item.id"
          :class="[
            'flex w-auto shrink-0 items-center gap-2 rounded-md px-3 py-2 text-left whitespace-nowrap transition md:w-full',
            currentTab === item.id
              ? 'bg-blue-600 text-white'
              : 'text-gray-400 hover:bg-raised hover:text-white',
          ]"
          type="button"
          :aria-current="currentTab === item.id ? 'page' : undefined"
          @click="selectTab(item.id)"
        >
          <UiIcon :name="item.icon" :size="16" />
          {{ t(item.label) }}
        </button>
      </nav>

      <button
        v-if="adminUsername"
        type="button"
        class="mx-4 mb-3 rounded border border-line px-3 py-2 text-gray-400 hover:bg-raised hover:text-white"
        @click="logout"
      >
        {{ adminUsername }} · {{ t('auth.logout') }}
      </button>

      <div
        class="relative min-w-0 overflow-hidden border-t border-line p-2 md:p-4"
        :class="serviceState === 'launching' ? 'launching-shell' : ''"
      >
        <div v-if="serviceState === 'launching'" class="launching-scan" aria-hidden="true"></div>
        <div class="mb-2 text-xs text-gray-500">{{ t('app.status') }}</div>
        <div class="mb-4 flex items-center justify-between">
          <span class="font-mono font-bold" :class="statusTextColor">
            {{ serviceState.toUpperCase() }}
          </span>
          <span
            v-tooltip="status?.version || ''"
            class="max-w-[65%] truncate font-mono text-xs text-gray-500"
          >
            {{ status?.version || '—' }}
          </span>
        </div>
        <button
          v-if="serviceState === 'stopped'"
          class="flex w-full items-center justify-center gap-2 rounded bg-green-600 py-2 font-bold text-white shadow transition hover:bg-green-500 disabled:opacity-50"
          type="button"
          :disabled="startPending || stopPending"
          @click="startService"
        >
          <UiIcon :name="startPending ? 'spinner' : 'play'" :size="16" />
          {{ t('app.start') }}
        </button>
        <button
          v-else-if="serviceState === 'launching' || serviceState === 'running'"
          class="flex w-full items-center justify-center gap-2 rounded bg-red-600 py-2 font-bold text-white shadow transition hover:bg-red-500 disabled:opacity-50"
          type="button"
          :disabled="stopPending"
          @click="stopService"
        >
          <UiIcon :name="stopPending ? 'spinner' : 'stop'" :size="16" />
          {{ t('app.stop') }}
        </button>
      </div>
    </aside>

    <main class="flex min-w-0 flex-1 flex-col bg-canvas">
      <Transition name="page">
        <KeepAlive include="SettingsPanel">
          <LogsPanel v-if="currentTab === 'logs'" :logs="logs" @clear="clearLogs" />
          <AccountsPanel
            v-else-if="currentTab === 'accounts'"
            :accounts="visibleAccounts"
            :loading="loading.accounts"
            :error="errors.accounts"
            @refresh="loadAccountData"
            @notice="showNotice"
            @delete="deleteAccount"
          />
          <ModelsTable
            v-else-if="currentTab === 'models'"
            :models="models"
            :loading="loading.models"
            :error="errors.models"
          />
          <RequestsPanel
            v-else-if="currentTab === 'requests'"
            :accounts="accounts"
            :cooldowns="cooldowns"
            :requests="requests"
            :loading="loading.requests || loading.cooldowns"
            :cooldown-error="errors.cooldowns"
            :request-error="errors.requests"
            @refresh="loadRequestData"
            @notice="showNotice"
          />
          <UsagePanel v-else-if="currentTab === 'usage'" />
          <SettingsPanel
            v-else-if="currentTab === 'settings'"
            :config="config"
            :loading="loading.config"
            :error="errors.config"
            @saved="config = $event"
            @notice="showNotice"
          />
          <PlaygroundPanel v-else :models="models" :api-key="config?.proxy_api_key ?? ''" />
        </KeepAlive>
      </Transition>
    </main>

    <div
      class="pointer-events-none fixed right-5 bottom-5 z-50 flex max-w-md flex-col items-end gap-2"
    >
      <Transition name="notice">
        <div
          v-if="notice.message"
          role="status"
          class="pointer-events-auto rounded border bg-panel px-4 py-3 text-sm shadow-xl"
          :class="
            notice.tone === 'error'
              ? 'border-red-500/50 text-red-300'
              : 'border-green-500/50 text-green-300'
          "
        >
          {{ notice.message }}
        </div>
      </Transition>
      <Transition name="notice">
        <div
          v-if="pendingDelete"
          class="pointer-events-auto flex max-w-md items-center gap-4 rounded border border-line-strong bg-panel px-4 py-3 text-sm text-gray-200 shadow-xl"
          role="status"
        >
          <span class="min-w-0 truncate">{{
            t('accounts.deletedNamed').replace('{name}', pendingDelete.account.label)
          }}</span>
          <button
            type="button"
            class="flex shrink-0 items-center gap-1.5 rounded px-2 py-1 font-medium text-blue-400 hover:bg-raised"
            @click="undoDelete"
          >
            <UiIcon name="undo" :size="14" />
            {{ t('common.undo') }}
          </button>
        </div>
      </Transition>
    </div>
  </div>
</template>

<style scoped>
.launching-shell {
  background: radial-gradient(circle at 12% 0%, rgb(34 211 238 / 14%), transparent 55%), #161b22;
}

.launching-scan {
  position: absolute;
  top: 0;
  left: -45%;
  width: 45%;
  height: 1px;
  background: linear-gradient(90deg, transparent, rgb(103 232 249), transparent);
  animation: launching-scan 1.25s ease-in-out infinite;
}

@keyframes launching-scan {
  to {
    transform: translateX(320%);
  }
}
</style>
