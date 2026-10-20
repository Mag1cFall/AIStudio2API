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
import type { AsyncComponentLoader } from 'vue'
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
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
  UpdateStatus,
} from '@/types'
import AccountsPanel from '@/components/AccountsPanel.vue'
import LogsPanel from '@/components/LogsPanel.vue'
import ModelsTable from '@/components/ModelsTable.vue'
import RequestsPanel from '@/components/RequestsPanel.vue'
import SettingsPanel from '@/components/SettingsPanel.vue'
import ThemeToggle from '@/components/ThemeToggle.vue'
import UiIcon from '@/components/UiIcon.vue'
import { readPreference, writePreference } from '@/preference'
import { useTabParam } from '@/url'
import { useRouter } from 'vue-router'
import type { IconName } from '@/icons'

const { availableLocales, locale, setLocale, t, errorText } = useI18n()
// lazyPanel 创建按需加载的页面组件
function lazyPanel(loader: AsyncComponentLoader) {
  return defineAsyncComponent({
    loader,
    delay: 150,
    loadingComponent: {
      render: () =>
        h('p', { class: 'p-8 text-sm text-gray-500', role: 'status' }, t('app.panelLoading')),
    },
    errorComponent: {
      render: () =>
        h('p', { class: 'p-8 text-sm text-red-300', role: 'alert' }, t('app.panelFailed')),
    },
  })
}
const UsagePanel = lazyPanel(() => import('@/components/UsagePanel.vue'))
const VersionPanel = lazyPanel(() => import('@/components/VersionPanel.vue'))
const validTabs: TabID[] = [
  'logs',
  'accounts',
  'models',
  'requests',
  'usage',
  'settings',
  'playground',
  'version',
]
const { tab: currentTab, select: selectTab } = useTabParam(validTabs, 'logs')
const router = useRouter()

// openTab 切换页面，API 试用进入独立的试用页
function openTab(tab: TabID): void {
  if (tab === 'playground') void router.push('/prompts/new_chat')
  else selectTab(tab)
}
watch(
  currentTab,
  (tab) => {
    if (tab === 'playground') void router.replace('/prompts/new_chat')
  },
  { immediate: true },
)
const navigationBar = ref<HTMLElement | null>(null)

// revealCurrentTab 把窄屏横向导航的当前项横向滚入视野
function revealCurrentTab(): void {
  const bar = navigationBar.value
  const item = bar?.querySelector<HTMLElement>('[aria-current="page"]')
  if (!bar || !item) return
  const barRect = bar.getBoundingClientRect()
  const itemRect = item.getBoundingClientRect()
  if (itemRect.left < barRect.left) bar.scrollLeft -= barRect.left - itemRect.left
  else if (itemRect.right > barRect.right) bar.scrollLeft += itemRect.right - barRect.right
}
watch(currentTab, revealCurrentTab, { flush: 'post' })

// defaultSidebarWidth 是宽屏侧栏的默认宽度，双击拖动条恢复
const defaultSidebarWidth = 256
const minSidebarWidth = 176
const maxSidebarWidth = 360
const sidebarWidth = ref(
  readPreference('app.sidebarWidth', defaultSidebarWidth, (value) => typeof value === 'number'),
)
const resizingSidebar = ref(false)
let sidebarResizeStartX = 0
let sidebarResizeStartWidth = 0
watch(sidebarWidth, (value) => writePreference('app.sidebarWidth', value))

function clampSidebarWidth(value: number): number {
  return Math.round(Math.min(maxSidebarWidth, Math.max(minSidebarWidth, value)))
}
sidebarWidth.value = clampSidebarWidth(sidebarWidth.value)

function startSidebarResize(event: PointerEvent): void {
  if (event.button !== 0) return
  const handle = event.currentTarget as HTMLElement
  handle.setPointerCapture(event.pointerId)
  sidebarResizeStartX = event.clientX
  sidebarResizeStartWidth = sidebarWidth.value
  resizingSidebar.value = true
  event.preventDefault()
}

function moveSidebarResize(event: PointerEvent): void {
  if (!resizingSidebar.value) return
  sidebarWidth.value = clampSidebarWidth(
    sidebarResizeStartWidth + event.clientX - sidebarResizeStartX,
  )
}

function stopSidebarResize(event: PointerEvent): void {
  if (!resizingSidebar.value) return
  const handle = event.currentTarget as HTMLElement
  if (handle.hasPointerCapture(event.pointerId)) handle.releasePointerCapture(event.pointerId)
  resizingSidebar.value = false
}

function resizeSidebarWithKeyboard(event: KeyboardEvent): void {
  if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
  event.preventDefault()
  sidebarWidth.value = clampSidebarWidth(
    sidebarWidth.value + (event.key === 'ArrowRight' ? 16 : -16),
  )
}
const status = ref<ServiceStatus | null>(null)
const logs = shallowRef<AdminLog[]>([])
const accounts = ref<Account[]>([])
const models = ref<Model[]>([])
const cooldowns = ref<Cooldown[]>([])
const requests = ref<RequestSummary[]>([])
const config = ref<ServiceConfig | null>(null)
const update = ref<UpdateStatus | null>(null)
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
  { id: 'version', label: 'nav.version', icon: 'version' },
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

// waitForRestart 在线更新后等新进程接管再刷新页面：状态接口返回其他版本或要求重新登录都表示旧进程已退出
function waitForRestart(previous = status.value?.version): void {
  eventConnection?.close()
  window.setTimeout(async () => {
    const response = await fetch('/api/status', { cache: 'no-store' }).catch(() => undefined)
    const current: ServiceStatus | undefined = response?.ok
      ? await response.json().catch(() => undefined)
      : undefined
    if (response?.status === 401 || (current !== undefined && current.version !== previous)) {
      window.location.reload()
      return
    }
    waitForRestart(previous)
  }, 1000)
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
  document.title = 'AIStudio2API'
  revealCurrentTab()
  await refreshAll()
  if (!mounted) return
  api.updateStatus().then(
    (value) => (update.value = value),
    () => undefined,
  )
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
      class="app-sidebar relative grid w-full min-w-0 shrink-0 border-b border-line bg-panel md:border-r md:border-b-0"
      :style="{ '--sidebar-width': `${sidebarWidth}px` }"
    >
      <div
        class="flex h-14 min-w-0 items-center gap-2.5 border-b border-line px-4 [grid-area:brand]"
      >
        <span
          v-tooltip="`${t('app.status')}: ${serviceState.toUpperCase()}`"
          class="relative shrink-0"
        >
          <img src="/favicon.svg" alt="" class="h-6 w-6" />
          <span
            class="absolute -right-0.5 -bottom-0.5 h-2.5 w-2.5 rounded-full ring-2 ring-panel"
            :class="statusColor"
          ></span>
        </span>
        <h1 class="relative top-px min-w-0 flex-1 truncate text-base font-bold text-white">
          AIStudio2API
        </h1>
        <div class="flex shrink-0 items-center gap-1">
          <ThemeToggle />
          <DropdownMenuRoot :modal="false">
            <DropdownMenuTrigger
              v-tooltip="t('app.language')"
              :aria-label="t('app.language')"
              class="flex h-6 w-6 shrink-0 items-center justify-center rounded border border-gray-700 text-gray-500 transition hover:text-white data-[state=open]:text-white"
            >
              <UiIcon name="language" :size="13" />
            </DropdownMenuTrigger>
            <DropdownMenuPortal>
              <DropdownMenuContent
                align="end"
                :side-offset="4"
                class="z-[60] overflow-hidden rounded border border-line bg-panel shadow-xl"
              >
                <DropdownMenuItem
                  v-for="item in availableLocales"
                  :key="item.code"
                  class="flex w-full cursor-pointer items-center justify-between gap-4 px-4 py-2 text-left text-xs whitespace-nowrap text-gray-300 outline-none data-[highlighted]:bg-blue-600 data-[highlighted]:text-white"
                  @select="setLocale(item.code)"
                >
                  {{ item.label }}
                  <UiIcon v-if="item.code === locale" name="check" :size="12" />
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenuPortal>
          </DropdownMenuRoot>
        </div>
      </div>

      <div
        v-if="adminUsername"
        class="flex min-w-0 items-center gap-1 border-b border-line pr-4 [grid-area:controls] md:border-t md:border-b-0 md:px-3 md:py-2"
      >
        <span
          v-tooltip.overflow="adminUsername"
          class="hidden min-w-0 flex-1 truncate text-xs text-gray-400 md:block"
          >{{ adminUsername }}</span
        >
        <button
          v-tooltip="t('auth.logout')"
          type="button"
          :aria-label="`${adminUsername} · ${t('auth.logout')}`"
          class="flex h-6 w-6 shrink-0 items-center justify-center rounded border border-gray-700 text-gray-500 transition hover:text-white"
          @click="logout"
        >
          <UiIcon name="login" :size="13" />
        </button>
      </div>

      <nav
        ref="navigationBar"
        class="flex w-full min-w-0 gap-1 overflow-x-auto p-2 [grid-area:nav] md:flex-col md:overflow-x-hidden md:overflow-y-auto"
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
          @click="openTab(item.id)"
        >
          <UiIcon :name="item.icon" :size="16" class="shrink-0" />
          <span v-tooltip.overflow="t(item.label)" class="min-w-0 truncate md:whitespace-normal">{{
            t(item.label)
          }}</span>
          <span
            v-if="item.id === 'version' && update?.available"
            class="size-2 shrink-0 rounded-full bg-amber-400 md:ml-auto"
            aria-hidden="true"
            :title="t('version.available').replace('{version}', update.latest ?? '')"
          />
        </button>
      </nav>

      <div
        class="relative min-w-0 overflow-hidden border-t border-line p-2 [grid-area:status] md:p-4"
        :class="serviceState === 'launching' ? 'launching-shell' : ''"
      >
        <div v-if="serviceState === 'launching'" class="launching-scan" aria-hidden="true"></div>
        <div class="mb-2 hidden text-xs text-gray-500 md:block">{{ t('app.status') }}</div>
        <div class="flex items-center justify-between gap-2 md:mb-4">
          <div class="flex min-w-0 items-center gap-2 md:contents">
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
            class="flex shrink-0 items-center gap-1.5 rounded bg-green-600 px-3 py-1 text-xs font-bold text-white shadow transition hover:bg-green-500 disabled:opacity-50 md:hidden"
            type="button"
            :disabled="startPending || stopPending"
            @click="startService"
          >
            <UiIcon :name="startPending ? 'spinner' : 'play'" :size="13" />
            {{ t('app.start') }}
          </button>
          <button
            v-else-if="serviceState === 'launching' || serviceState === 'running'"
            class="flex shrink-0 items-center gap-1.5 rounded bg-red-600 px-3 py-1 text-xs font-bold text-white shadow transition hover:bg-red-500 disabled:opacity-50 md:hidden"
            type="button"
            :disabled="stopPending"
            @click="stopService"
          >
            <UiIcon :name="stopPending ? 'spinner' : 'stop'" :size="13" />
            {{ t('app.stop') }}
          </button>
        </div>
        <button
          v-if="serviceState === 'stopped'"
          class="hidden w-full items-center justify-center gap-2 rounded bg-green-600 py-2 font-bold text-white shadow transition hover:bg-green-500 disabled:opacity-50 md:flex"
          type="button"
          :disabled="startPending || stopPending"
          @click="startService"
        >
          <UiIcon :name="startPending ? 'spinner' : 'play'" :size="16" />
          {{ t('app.start') }}
        </button>
        <button
          v-else-if="serviceState === 'launching' || serviceState === 'running'"
          class="hidden w-full items-center justify-center gap-2 rounded bg-red-600 py-2 font-bold text-white shadow transition hover:bg-red-500 disabled:opacity-50 md:flex"
          type="button"
          :disabled="stopPending"
          @click="stopService"
        >
          <UiIcon :name="stopPending ? 'spinner' : 'stop'" :size="16" />
          {{ t('app.stop') }}
        </button>
      </div>
      <div
        class="sidebar-resizer"
        :class="{ 'is-resizing': resizingSidebar }"
        role="separator"
        tabindex="0"
        aria-orientation="vertical"
        :aria-label="t('app.resizeSidebar')"
        :aria-valuemin="minSidebarWidth"
        :aria-valuemax="maxSidebarWidth"
        :aria-valuenow="sidebarWidth"
        @pointerdown="startSidebarResize"
        @pointermove="moveSidebarResize"
        @pointerup="stopSidebarResize"
        @pointercancel="stopSidebarResize"
        @dblclick="sidebarWidth = defaultSidebarWidth"
        @keydown="resizeSidebarWithKeyboard"
      ></div>
    </aside>

    <main class="flex min-h-0 min-w-0 flex-1 flex-col bg-canvas">
      <Transition name="page">
        <KeepAlive include="SettingsPanel,VersionPanel">
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
            :config="config"
            @saved="config = $event"
            @notice="showNotice"
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
          <UsagePanel v-else-if="currentTab === 'usage'" :models="models" />
          <SettingsPanel
            v-else-if="currentTab === 'settings'"
            :config="config"
            :loading="loading.config"
            :error="errors.config"
            @saved="config = $event"
            @notice="showNotice"
          />
          <VersionPanel
            v-else-if="currentTab === 'version'"
            :status="update"
            :active-requests="status?.active_requests ?? 0"
            @status="update = $event"
            @restarting="waitForRestart"
          />
        </KeepAlive>
      </Transition>
    </main>

    <div
      class="pointer-events-none fixed right-5 bottom-5 left-5 z-50 flex flex-col items-end gap-2 sm:left-auto sm:max-w-md"
    >
      <Transition name="notice">
        <div
          v-if="notice.message"
          role="status"
          class="pointer-events-auto max-w-full rounded border bg-panel px-4 py-3 text-sm break-words shadow-xl"
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
          class="pointer-events-auto flex max-w-full items-center gap-4 rounded border border-line-strong bg-panel px-4 py-3 text-sm text-gray-200 shadow-xl"
          role="status"
        >
          <span
            v-tooltip.overflow="
              t('accounts.deletedNamed').replace('{name}', pendingDelete.account.label)
            "
            class="min-w-0 truncate"
            >{{ t('accounts.deletedNamed').replace('{name}', pendingDelete.account.label) }}</span
          >
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
.app-sidebar {
  grid-template-columns: minmax(0, 1fr) auto;
  grid-template-areas:
    'brand controls'
    'nav nav'
    'status status';
}

@media (width >= 48rem) {
  .app-sidebar {
    width: var(--sidebar-width);
    grid-template-rows: auto minmax(0, 1fr) auto auto;
    grid-template-columns: minmax(0, 1fr);
    grid-template-areas:
      'brand'
      'nav'
      'controls'
      'status';
  }
}

.sidebar-resizer {
  display: none;
  position: absolute;
  z-index: 20;
  top: 0;
  right: -0.375rem;
  bottom: 0;
  width: 0.75rem;
  cursor: col-resize;
  touch-action: none;
  user-select: none;
}

.sidebar-resizer::after {
  position: absolute;
  top: 0;
  bottom: 0;
  left: calc(50% - 1px);
  width: 2px;
  background: transparent;
  content: '';
  transition: background-color 150ms ease;
}

.sidebar-resizer:hover::after,
.sidebar-resizer:focus-visible::after,
.sidebar-resizer.is-resizing::after {
  background: var(--color-blue-500);
}

.sidebar-resizer:focus-visible {
  outline: none;
}

@media (width >= 48rem) {
  .sidebar-resizer {
    display: block;
  }
}

.launching-shell {
  background:
    radial-gradient(circle at 12% 0%, rgb(34 211 238 / 14%), transparent 55%), var(--color-panel);
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
