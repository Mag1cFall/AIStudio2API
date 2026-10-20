import { computed, reactive, ref, shallowRef } from 'vue'
import { api, openAdminEvents, type EventConnection } from '@/api'
import { useI18n } from '@/i18n'
import type {
  Account,
  AdminEvent,
  AdminLog,
  Cooldown,
  Model,
  RequestSummary,
  ServiceConfig,
  ServiceStatus,
  UpdateStatus,
} from '@/types'

const { t, errorText } = useI18n()

// status 为生成服务状态，null 表示管理接口不可达
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
// pendingDelete 为撤销时限内尚未提交的账户删除
const pendingDelete = shallowRef<{ account: Account; timer: number } | null>(null)
const deletingAccounts = ref(new Set<string>())
const visibleAccounts = computed(() =>
  accounts.value.filter(
    (account) =>
      account.id !== pendingDelete.value?.account.id && !deletingAccounts.value.has(account.id),
  ),
)

// serviceState 为界面显示的生成服务状态
const serviceState = computed<'unavailable' | 'running' | 'launching' | 'stopped'>(() => {
  if (status.value === null) return 'unavailable'
  if (status.value.state === 'RUNNING') return 'running'
  if (status.value.state === 'LAUNCHING') return 'launching'
  if (startPending.value && !launchCancellationRequested.value) return 'launching'
  return 'stopped'
})

let eventConnection: EventConnection | undefined
let noticeTimer: number | undefined
let pendingLogs: AdminLog[] = []
let logFlushTimer: number | undefined

// showNotice 显示一次短暂操作结果
function showNotice(message: string, tone: 'success' | 'error'): void {
  notice.message = message
  notice.tone = tone
  if (noticeTimer !== undefined) window.clearTimeout(noticeTimer)
  noticeTimer = window.setTimeout(() => {
    notice.message = ''
  }, 3200)
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
    errors.accounts = errorText(error)
  } finally {
    loading.accounts = false
  }
}

async function loadModels(): Promise<void> {
  loading.models = models.value.length === 0
  errors.models = ''
  try {
    models.value = await api.models()
  } catch (error) {
    errors.models = errorText(error)
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
    errors.cooldowns = errorText(error)
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
    errors.requests = errorText(error)
  } finally {
    loading.requests = false
  }
}

async function loadConfig(): Promise<void> {
  loading.config = config.value === null
  errors.config = ''
  try {
    config.value = await api.config()
  } catch (error) {
    errors.config = errorText(error)
  } finally {
    loading.config = false
  }
}

// loadAccountData 刷新账户、模型目录与服务状态
async function loadAccountData(): Promise<void> {
  await Promise.all([loadAccounts(), loadModels(), loadStatus()])
}

// loadRequestData 刷新请求列表与冷却
async function loadRequestData(): Promise<void> {
  await Promise.all([loadRequests(), loadCooldowns()])
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
    showNotice(errorText(error), 'error')
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

async function startService(): Promise<void> {
  startPending.value = true
  launchCancellationRequested.value = false
  try {
    status.value = await api.startService()
    if (launchCancellationRequested.value || status.value.state !== 'RUNNING') return
    showNotice(t('app.start'), 'success')
    await Promise.all([loadAccounts(), loadModels(), loadCooldowns()])
  } catch (error) {
    if (!launchCancellationRequested.value) showNotice(errorText(error), 'error')
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
    showNotice(errorText(error), 'error')
    await loadStatus()
  } finally {
    stopPending.value = false
  }
}

function flushLogs(): void {
  logFlushTimer = undefined
  if (pendingLogs.length === 0) return
  logs.value = logs.value.concat(pendingLogs).slice(-2000)
  pendingLogs = []
}

function resetLogs(): void {
  pendingLogs = []
  if (logFlushTimer !== undefined) {
    window.clearTimeout(logFlushTimer)
    logFlushTimer = undefined
  }
  logs.value = []
}

async function clearLogs(): Promise<void> {
  try {
    await api.clearLogs()
    resetLogs()
  } catch (error) {
    showNotice(errorText(error), 'error')
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
    if (logFlushTimer === undefined) logFlushTimer = window.setTimeout(flushLogs, 40)
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

// startConsole 加载全部管理数据并订阅管理事件
async function startConsole(): Promise<void> {
  window.addEventListener('pagehide', flushDelete)
  await Promise.all([
    loadStatus(),
    loadAccounts(),
    loadModels(),
    loadCooldowns(),
    loadRequests(),
    loadConfig(),
  ])
  api.updateStatus().then(
    (value) => (update.value = value),
    () => undefined,
  )
  eventConnection = openAdminEvents(handleAdminEvent, () => {
    resetLogs()
    requests.value = []
  })
}

// stopConsole 关闭事件订阅并提交等待中的删除
function stopConsole(): void {
  eventConnection?.close()
  eventConnection = undefined
  if (logFlushTimer !== undefined) window.clearTimeout(logFlushTimer)
  if (noticeTimer !== undefined) window.clearTimeout(noticeTimer)
  window.removeEventListener('pagehide', flushDelete)
  flushDelete()
}

// useConsole 返回管理端共享的服务数据与操作
export function useConsole() {
  return {
    status,
    logs,
    accounts,
    visibleAccounts,
    models,
    cooldowns,
    requests,
    config,
    update,
    loading,
    errors,
    notice,
    pendingDelete,
    serviceState,
    startPending,
    stopPending,
    showNotice,
    loadAccountData,
    loadRequestData,
    deleteAccount,
    undoDelete,
    commitDelete,
    startService,
    stopService,
    clearLogs,
    waitForRestart,
    startConsole,
    stopConsole,
  }
}
