<script setup lang="ts">
import { computed, onMounted, reactive, ref, useTemplateRef, watch } from 'vue'
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from 'reka-ui'
import { api, ApiError } from '@/api'
import { useI18n, type TranslationKey } from '@/i18n'
import { usePaged } from '@/paging'
import { maskedProxy } from '@/proxy'
import type {
  Account,
  AccountDraft,
  AccountRefresh,
  AccountState,
  AccountTask,
  AccountTaskItem,
  ChromeImportProfile,
  CookieBrowser,
  PairingToken,
} from '@/types'
import type { IconName } from '@/icons'
import AccountEnvironmentFields from './AccountEnvironmentFields.vue'
import DetailDialog from './DetailDialog.vue'
import DisclosureChevron from './DisclosureChevron.vue'
import LoginViewer from './LoginViewer.vue'
import StagedRefreshIcon from './StagedRefreshIcon.vue'
import UiCollapse from './UiCollapse.vue'
import UiIcon from './UiIcon.vue'
import UiPager from './UiPager.vue'
import UiSelect from './UiSelect.vue'

// CookieCandidate 是 Cookie 中识别出的账户，source 为所在 Cookie 来源的序号
type CookieCandidate = CookieBrowser & { source: number }

const props = defineProps<{
  accounts: Account[]
  tasks: AccountTask[]
  loading: boolean
  error: string
  // chromeImport 表示服务所在平台能从 Chrome 导入账户
  chromeImport: boolean
}>()

const emit = defineEmits<{
  refresh: []
  updated: [account: Account]
  notice: [message: string, tone: 'success' | 'error']
  delete: [accounts: Account[]]
}>()

const { t, locale, errorText, logText } = useI18n()
const defaultAccountLocale = navigator.language
const showEditor = ref(false)
const showBrowserLogin = ref(false)
const showChromeImport = ref(false)
const showCookieImport = ref(false)
const cookieText = ref('')
// cookieFiles 是粘贴 Cookie 对话框中选择的 Cookie 文件，选择后代替粘贴的文本，每个文件单独识别与导入
const cookieFiles = ref<{ name: string; text: string }[]>([])
const cookieFileInput = useTemplateRef<HTMLInputElement>('cookieFileInput')
const cookieCandidates = ref<CookieCandidate[]>([])
const selectedCookieUsers = ref<string[]>([])
// discoveringCookies 表示正在识别 Cookie 中登录的账户
const discoveringCookies = ref(false)
// cookieDiscoveryError 是最近一次识别失败的原因，来自文件时以文件名开头
const cookieDiscoveryError = ref('')
// cookieDiscovery 是识别的序号，Cookie 变化后较早开始的识别结果被丢弃
let cookieDiscovery = 0
let cookieDiscoveryTimer: number | undefined
const showBrowserImport = ref(false)
// moreSettingsOpen 是导入对话框的更多设置是否展开，每次打开对话框时收起
const moreSettingsOpen = ref(false)
watch([showCookieImport, showBrowserImport], () => (moreSettingsOpen.value = false))
// cookieBrowsers 是本机找到的调试浏览器与其中登录 AI Studio 的账户
const cookieBrowsers = ref<CookieBrowser[]>([])
const scanningBrowsers = ref(false)
const selectedBrowsers = ref<string[]>([])
const editingAccountID = ref('')
const pendingAction = ref('')
// addPending 表示添加账户菜单正在生成配对令牌或读取本机 Chrome 账户
const addPending = computed(
  () => pendingAction.value === 'pairing' || pendingAction.value === 'chrome-discover',
)
const refreshing = reactive(new Set<string>())
// queued 是刷新全部中尚未轮到的账户
const queued = reactive(new Set<string>())
// refreshErrors 是最近一次刷新失败的账户与失败原因
const refreshErrors = reactive(new Map<string, string>())
// refreshTotal 与 refreshDone 是进行中的刷新全部的账户数与已完成数，没有刷新全部时 refreshTotal 为 0
const refreshTotal = ref(0)
const refreshDone = ref(0)
// toggling 是正在切换启用状态的账户
const toggling = reactive(new Set<string>())
const chromeProfiles = ref<ChromeImportProfile[]>([])
const pairing = ref<PairingToken | null>(null)
// pairingLogin 表示远程添加在新窗口登录，否则导入那台电脑 Chrome 中的账户
const pairingLogin = ref(false)
const copiedPairing = ref(false)
// pairingTimer 在远程添加打开期间于令牌到期时换新
let pairingTimer: number | undefined
// loopbackHost 判断主机名是否为本机地址
function loopbackHost(host: string): boolean {
  const name = host.replace(/^\[|\]$/g, '')
  return name === 'localhost' || name === '::1' || /^127\.\d+\.\d+\.\d+$/.test(name)
}
// pairingSystem 是运行命令的电脑的系统，在手机上打开本页时默认 Windows
const pairingSystem = ref<'windows' | 'unix'>(
  /Windows|Android|iPhone|iPad/.test(navigator.userAgent) ? 'windows' : 'unix',
)
// pairingLoopback 表示本页经本机地址打开，命令中的地址只在这台设备上有效
const pairingLoopback = loopbackHost(window.location.hostname)
// pairingAddress 是命令中的服务地址，本页经本机地址打开时可改为本服务的 HTTPS 地址
const pairingAddress = ref(window.location.origin)
// pairingCommand 是在能登录 Google 的电脑上运行的完整命令
const pairingCommand = computed(
  () =>
    `${pairingSystem.value === 'windows' ? 'cmd /c start /wait aistudio2api.exe' : './aistudio2api'} setup --remote ${pairingAddress.value.trim()} --token ${pairing.value?.token}${pairingLogin.value ? ' --login' : ''}`,
)
// pairingInsecure 表示命令中的地址不是本机地址却未使用 HTTPS，setup 会拒绝这样的地址
const pairingInsecure = computed(() => {
  const host = /^http:\/\/(\[[^\]]+\]|[^/:]+)/i.exec(pairingAddress.value.trim())?.[1]
  return host !== undefined && !loopbackHost(host)
})
// clipboardAvailable 表示页面处于可使用剪贴板 API 的安全上下文
const clipboardAvailable = window.isSecureContext
const selectedChromeProfiles = ref<string[]>([])
const allChromeProfilesSelected = computed(
  () =>
    chromeProfiles.value.length > 0 &&
    selectedChromeProfiles.value.length === chromeProfiles.value.length,
)

function toggleAllChromeProfiles(): void {
  selectedChromeProfiles.value = allChromeProfilesSelected.value
    ? []
    : chromeProfiles.value.map((profile) => profile.id)
}
const accountEnvironment = reactive({
  proxy: '',
  locale: defaultAccountLocale,
  timezone: '',
})
// accountEnvironment 的时区在挂载后的任务中填入本机时区，首次读取时区要载入时区数据，不并入挂载面板的任务
onMounted(() =>
  setTimeout(
    () => (accountEnvironment.timezone ||= Intl.DateTimeFormat().resolvedOptions().timeZone),
  ),
)
const draft = reactive<AccountDraft>({
  label: '',
  enabled: true,
  proxy: '',
  locale: defaultAccountLocale,
  timezone: '',
})

const stateKeys: Record<AccountState, TranslationKey> = {
  ready: 'state.ready',
  busy: 'state.busy',
  cooldown: 'state.cooldown',
  auth_required: 'state.auth_required',
  unavailable: 'state.unavailable',
  disabled: 'state.disabled',
}
// stateTone 是各账户状态气泡的颜色
const stateTone: Record<AccountState, string> = {
  ready: 'bg-green-400/10 text-green-400',
  busy: 'bg-blue-400/10 text-blue-400',
  cooldown: 'bg-yellow-400/10 text-yellow-400',
  auth_required: 'bg-red-400/10 text-red-400',
  unavailable: 'bg-red-400/10 text-red-400',
  disabled: 'bg-gray-500/10 text-gray-500',
}
// accountIconClass 是账户行图标按钮的样式
const accountIconClass =
  'flex size-7 items-center justify-center rounded text-gray-400 transition hover:bg-line disabled:opacity-50'
// reasonClass 是查看失败原因的文字按钮样式
const reasonClass =
  'shrink-0 text-xs text-gray-400 underline underline-offset-2 transition hover:text-gray-200'
// transitionKeys 是账户启停后台收尾阶段的文字，宽屏下各行按最长的一项留出位置，阶段出现时状态列宽度不变
const transitionKeys: TranslationKey[] = [
  'accounts.draining',
  'accounts.drainingRequests',
  'accounts.syncing',
]
// reasonKeys 是状态旁说明按钮的文字，宽屏下各行按最长的一项留出位置，按钮出现时状态列宽度不变
const reasonKeys: TranslationKey[] = ['accounts.reason', 'accounts.refreshFailed']

// transitionText 返回账户启停收尾阶段的文字，没有收尾时返回空串
function transitionText(account: Account): string {
  if (account.transition === 'syncing') return t('accounts.syncing')
  if (account.transition !== 'draining') return ''
  return account.active_requests > 0
    ? t('accounts.drainingRequests').replace('{count}', String(account.active_requests))
    : t('accounts.draining')
}

const search = ref('')
const selectedState = ref('')
const selectedTier = ref('')
const selectedEgress = ref('*')
const states = computed(() => [...new Set(props.accounts.map((account) => account.state))])
const tiers = computed(() => [...new Set(props.accounts.map((account) => account.benefit_tier))])
const egresses = computed(() => [...new Set(props.accounts.map((account) => account.egress))])

// visibleAccounts 是邮箱检索与账户属性筛选的交集
const visibleAccounts = computed(() => {
  const needle = search.value.trim().toLowerCase()
  return props.accounts.filter(
    (account) =>
      (account.label.toLowerCase().includes(needle) || account.id.toLowerCase().includes(needle)) &&
      (selectedState.value === '' || account.state === selectedState.value) &&
      (selectedTier.value === '' || account.benefit_tier === selectedTier.value) &&
      (selectedEgress.value === '*' || account.egress === selectedEgress.value),
  )
})
// 账户列表每页 50 条，检索或筛选变化时回到第一页
const {
  page: accountPage,
  pages: accountPages,
  paged: pagedAccounts,
} = usePaged(visibleAccounts, 50)
watch([search, selectedState, selectedTier, selectedEgress], () => (accountPage.value = 1))
const accountList = ref<HTMLElement>()
// 翻页后列表回到顶部
watch(accountPage, () => accountList.value?.scrollTo({ top: 0 }))

// selected 是勾选的账户
const selected = reactive(new Set<string>())
// selectedAccounts 是当前筛选结果中已勾选的账户
const selectedAccounts = computed(() =>
  visibleAccounts.value.filter((account) => selected.has(account.id)),
)
const allSelected = computed(
  () =>
    selectedAccounts.value.length > 0 &&
    selectedAccounts.value.length === visibleAccounts.value.length,
)
const someSelected = computed(() => selectedAccounts.value.length > 0 && !allSelected.value)

// toggleAccountSelected 勾选或取消一个账户
function toggleAccountSelected(accountID: string): void {
  if (selected.has(accountID)) selected.delete(accountID)
  else selected.add(accountID)
}

// toggleRowSelected 点击账户行时勾选或取消该账户，点击行内控件与拖选文字时不切换
function toggleRowSelected(account: Account, event: MouseEvent): void {
  if ((event.target as Element).closest('button, a, input')) return
  if (!getSelection()!.isCollapsed) return
  toggleAccountSelected(account.id)
}

// toggleAllSelected 勾选当前筛选结果中的全部账户，已全部勾选时取消
function toggleAllSelected(): void {
  if (allSelected.value) for (const account of visibleAccounts.value) selected.delete(account.id)
  else for (const account of visibleAccounts.value) selected.add(account.id)
}

// bulkDisabled 判断所选账户是否全部已处于目标启用状态
function bulkDisabled(enabled: boolean): boolean {
  return selectedAccounts.value.every((account) => account.enabled === enabled)
}

// bulkSetEnabled 启用或停用所选账户中状态不同的账户，结束后提示成功与失败的数量并清空选择
async function bulkSetEnabled(enabled: boolean): Promise<void> {
  const targets = selectedAccounts.value.filter((account) => account.enabled !== enabled)
  selected.clear()
  const results = await Promise.allSettled(
    targets.map((account) => updateEnabled(account, enabled)),
  )
  const failed = results.filter((result) => result.status === 'rejected').length
  emit('refresh')
  emit(
    'notice',
    t(enabled ? 'accounts.bulkEnabled' : 'accounts.bulkDisabled')
      .replace('{count}', String(targets.length - failed))
      .replace('{failed}', String(failed)),
    failed === 0 ? 'success' : 'error',
  )
}

// bulkRefresh 按并发限制刷新所选的启用账户并清空选择
function bulkRefresh(): void {
  void refreshAccounts(selectedAccounts.value.filter((account) => account.enabled))
  selected.clear()
}

// bulkDelete 把所选账户交给可撤销的删除并清空选择
function bulkDelete(): void {
  emit('delete', [...selectedAccounts.value])
  selected.clear()
}

// reason 是悬窗中显示的账户或任务、原因类别与完整原因
const reason = ref<{ title: string; kind: string; text: string } | null>(null)

// accountSummary 把账户等级、出口、语言、时区与原电脑最近续签的时间合成一行
function accountSummary(account: Account): string {
  return [
    account.benefit_tier === 'Unknown' ? t('accounts.tierUnknown') : account.benefit_tier,
    account.egress ? maskedProxy(account.egress) : t('accounts.direct'),
    account.locale,
    account.timezone,
    ...(account.pairing_renewed_at
      ? [
          t('accounts.pairingRenewed').replace(
            '{time}',
            new Date(account.pairing_renewed_at).toLocaleString(locale.value, {
              month: 'numeric',
              day: 'numeric',
              hour: '2-digit',
              minute: '2-digit',
              hour12: false,
            }),
          ),
        ]
      : []),
  ].join(' · ')
}

// actionError 将账户写操作错误发送到全局通知
function actionError(error: unknown): void {
  emit('notice', errorText(error), 'error')
}

function beginEdit(account: Account): void {
  editingAccountID.value = account.id
  draft.label = account.label
  draft.enabled = account.enabled
  draft.proxy = account.proxy
  draft.locale = account.locale
  draft.timezone = account.timezone
  showEditor.value = true
}

function closeEditor(): void {
  showEditor.value = false
  editingAccountID.value = ''
}

function openBrowserLogin(): void {
  showBrowserLogin.value = true
}

function closeBrowserLogin(): void {
  if (pendingAction.value === 'browser-login') return
  showBrowserLogin.value = false
}

// taskTitles 是各类账户任务的标题
const taskTitles: Record<AccountTask['kind'], TranslationKey> = {
  login: 'accounts.browserLogin',
  relogin: 'common.relogin',
  chrome: 'accounts.importChrome',
  cookies: 'accounts.pasteCookie',
  browser: 'accounts.readBrowser',
}

// taskStates 是任务中账户各状态的读屏文字
const taskStates: Record<AccountTaskItem['state'], TranslationKey> = {
  pending: 'accounts.queued',
  running: 'accounts.taskRunning',
  done: 'accounts.taskDone',
  failed: 'accounts.taskFailed',
}

// taskFinished 返回任务中已结束的账户数
function taskFinished(task: AccountTask): number {
  return task.items.filter((item) => item.state === 'done' || item.state === 'failed').length
}

// taskError 返回任务中一个账户的失败原因，错误码按当前语言显示
function taskError(item: AccountTaskItem): string {
  return errorText(new ApiError(item.error ?? '', 0, item.code ?? ''))
}

// showTaskError 在悬窗中显示任务中一个账户的失败原因
function showTaskError(task: AccountTask, item: AccountTaskItem): void {
  reason.value = {
    title: item.label || t(taskTitles[task.kind]),
    kind: `${t(taskTitles[task.kind])} · ${t('accounts.taskFailed')}`,
    text: taskError(item),
  }
}

// dismissTask 取消进行中的任务或关闭已结束的任务
async function dismissTask(task: AccountTask): Promise<void> {
  try {
    await api.dismissAccountTask(task.id)
  } catch (error) {
    actionError(error)
  }
}

// reloggingAccounts 是有进行中重新登录任务的账户
const reloggingAccounts = computed(
  () =>
    new Set(
      props.tasks
        .filter((task) => task.kind === 'relogin' && task.state === 'running')
        .map((task) => task.items[0]!.label),
    ),
)

// 任务结束后刷新账户、模型与服务状态
watch(
  () => props.tasks.filter((task) => task.state !== 'running').map((task) => task.id),
  (finished, previous) => {
    if (finished.some((id) => !previous.includes(id))) emit('refresh')
  },
)

// viewerTaskID 是页内登录窗口显示的登录任务，为空时登录窗口关闭
const viewerTaskID = ref('')
const viewerTask = computed(() => props.tasks.find((task) => task.id === viewerTaskID.value))

// 登录窗口显示的任务被关闭或登录完成时关闭登录窗口，登录失败时窗口显示原因
watch(viewerTask, (task) => {
  if (task === undefined || task.state === 'done') viewerTaskID.value = ''
})

// newViewID 生成登录窗口 ID
function newViewID(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) =>
    byte.toString(16).padStart(2, '0'),
  ).join('')
}

// openViewer 在登录任务带登录窗口 ID 时打开页内登录窗口
function openViewer(task: AccountTask): void {
  if (task.view) viewerTaskID.value = task.id
}

// cancelViewerTask 关闭登录窗口并取消它显示的登录
function cancelViewerTask(): void {
  void dismissTask(viewerTask.value!)
  viewerTaskID.value = ''
}

// viewerUnavailable 在登录窗口无法连接时取消登录
function viewerUnavailable(): void {
  cancelViewerTask()
  emit('notice', t('accounts.liveUnavailable'), 'error')
}

// beginBrowserLogin 在账户任务中启动浏览器登录并关闭登录设置
async function beginBrowserLogin(): Promise<void> {
  pendingAction.value = 'browser-login'
  try {
    openViewer(await api.createAccount({ ...accountEnvironment }, newViewID()))
    showBrowserLogin.value = false
  } catch (error) {
    actionError(error)
  } finally {
    pendingAction.value = ''
  }
}

// showPairing 显示配对令牌，并在它到期时换新
function showPairing(token: PairingToken): void {
  pairing.value = token
  window.clearTimeout(pairingTimer)
  pairingTimer = window.setTimeout(
    () => void renewPairing(),
    new Date(token.expires_at).getTime() - Date.now(),
  )
}

// openPairing 打开远程添加，沿用仍在有效期内的配对令牌，没有时生成新令牌
async function openPairing(): Promise<void> {
  pendingAction.value = 'pairing'
  try {
    showPairing(
      await api.activePairing().catch((error: unknown) => {
        if (error instanceof ApiError && error.status === 404) return api.createPairing()
        throw error
      }),
    )
  } catch (error) {
    actionError(error)
  } finally {
    pendingAction.value = ''
  }
}

// renewPairing 生成新的配对令牌，旧令牌随之失效
async function renewPairing(): Promise<void> {
  pendingAction.value = 'pairing-renew'
  try {
    showPairing(await api.createPairing())
  } catch (error) {
    actionError(error)
  } finally {
    pendingAction.value = ''
  }
}

// closePairing 关闭远程添加并停止令牌换新
function closePairing(): void {
  window.clearTimeout(pairingTimer)
  pairing.value = null
}

// copyPairing 复制配对命令
async function copyPairing(): Promise<void> {
  try {
    await navigator.clipboard.writeText(pairingCommand.value)
    copiedPairing.value = true
    window.setTimeout(() => (copiedPairing.value = false), 1200)
  } catch (error) {
    actionError(error)
  }
}

// openChromeImport 读取本机可导入的 Chrome 账户
async function openChromeImport(): Promise<void> {
  pendingAction.value = 'chrome-discover'
  try {
    chromeProfiles.value = await api.chromeImportProfiles()
    selectedChromeProfiles.value = []
    showChromeImport.value = true
  } catch (error) {
    actionError(error)
  } finally {
    pendingAction.value = ''
  }
}

function closeChromeImport(): void {
  if (pendingAction.value === 'chrome-import') return
  showChromeImport.value = false
  chromeProfiles.value = []
  selectedChromeProfiles.value = []
}

// importChromeAccounts 在账户任务中导入选中的 Chrome 账户并关闭对话框
async function importChromeAccounts(): Promise<void> {
  pendingAction.value = 'chrome-import'
  try {
    const chosen = new Set(selectedChromeProfiles.value)
    await api.importChromeAccounts({
      accounts: chromeProfiles.value
        .filter((profile) => chosen.has(profile.id))
        .map(({ id, email }) => ({ id, email })),
      ...accountEnvironment,
    })
    pendingAction.value = ''
    closeChromeImport()
  } catch (error) {
    actionError(error)
  } finally {
    pendingAction.value = ''
  }
}

function closeCookieImport(): void {
  if (pendingAction.value === 'cookie-import') return
  resetCookieDiscovery()
  showCookieImport.value = false
  cookieText.value = ''
  cookieFiles.value = []
}

// resetCookieDiscovery 清除识别结果并丢弃等待中与进行中的识别
function resetCookieDiscovery(): void {
  cookieDiscovery++
  window.clearTimeout(cookieDiscoveryTimer)
  discoveringCookies.value = false
  cookieDiscoveryError.value = ''
  cookieCandidates.value = []
  selectedCookieUsers.value = []
}

// scheduleCookieDiscovery 在粘贴或输入停顿后识别 Cookie
function scheduleCookieDiscovery(): void {
  resetCookieDiscovery()
  cookieDiscoveryTimer = window.setTimeout(() => void discoverCookieAccounts(), 400)
}

// cookieSources 返回待识别的 Cookie：选择的各个文件，没有选择文件时为粘贴的文本
function cookieSources(): { name: string; text: string }[] {
  return cookieFiles.value.length > 0 ? cookieFiles.value : [{ name: '', text: cookieText.value }]
}

// readCookieFiles 读取选择的 Cookie 文件并识别其中的账户
async function readCookieFiles(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  cookieFiles.value = await Promise.all(
    [...input.files!].map(async (file) => ({ name: file.name, text: await file.text() })),
  )
  input.value = ''
  void discoverCookieAccounts()
}

// clearCookieFiles 取消选择的文件，改为粘贴
function clearCookieFiles(): void {
  cookieFiles.value = []
  scheduleCookieDiscovery()
}

// discoverCookieAccounts 逐个识别 Cookie 来源中各会话登录的账户，默认勾选尚未导入的账户，失败原因显示在对话框中
async function discoverCookieAccounts(): Promise<void> {
  resetCookieDiscovery()
  const sequence = cookieDiscovery
  const sources = cookieSources()
  if (sources.every((source) => source.text.trim() === '')) return
  discoveringCookies.value = true
  // failed 是出错时正在识别的文件名
  let failed = ''
  try {
    const found: CookieCandidate[] = []
    for (const [source, item] of sources.entries()) {
      failed = item.name
      const discovered = await api.discoverCookieAccounts({
        cookies: item.text,
        proxy: accountEnvironment.proxy,
      })
      found.push(...discovered.map((candidate) => ({ ...candidate, source })))
    }
    if (sequence !== cookieDiscovery) return
    cookieCandidates.value = found
    selectedCookieUsers.value = importableCookieUsers.value
  } catch (error) {
    if (sequence === cookieDiscovery)
      cookieDiscoveryError.value =
        failed === '' ? errorText(error) : `${failed}: ${errorText(error)}`
  } finally {
    if (sequence === cookieDiscovery) discoveringCookies.value = false
  }
}

// cookieKey 是候选账户在勾选列表中的标识：Cookie 来源序号与会话序号
function cookieKey(candidate: CookieCandidate): string {
  return `${candidate.source}:${candidate.auth_user}`
}

// importableCookieUsers 是 Cookie 中尚未导入的账户
const importableCookieUsers = computed(() =>
  cookieCandidates.value.filter((item) => !item.imported).map(cookieKey),
)

const allCookieCandidatesSelected = computed(
  () =>
    importableCookieUsers.value.length > 0 &&
    selectedCookieUsers.value.length === importableCookieUsers.value.length,
)

function toggleAllCookieCandidates(): void {
  selectedCookieUsers.value = allCookieCandidatesSelected.value ? [] : importableCookieUsers.value
}

// pasteSteps 是粘贴 Cookie 的操作步骤
const pasteSteps: TranslationKey[] = ['accounts.pasteStep1', 'accounts.pasteStep2']

// openBrowserImport 打开指纹浏览器导入并查找本机调试浏览器
function openBrowserImport(): void {
  showBrowserImport.value = true
  void scanBrowsers()
}

function browserKey(browser: CookieBrowser): string {
  return `${browser.address}#${browser.auth_user}`
}

// browserSummary 返回浏览器条目第二行：未登录提示、读取错误或浏览器名、地址、u/N、语言与时区
function browserSummary(browser: CookieBrowser): string {
  if (browser.signed_out) return t('accounts.browserSignedOut')
  return (
    browser.error ||
    [
      browser.browser,
      browser.address,
      browser.email ? `u/${browser.auth_user}` : '',
      browser.locale,
      browser.timezone,
    ]
      .filter(Boolean)
      .join(' · ')
  )
}

// scanBrowsers 查找本机调试浏览器，默认勾选可导入的账户
async function scanBrowsers(): Promise<void> {
  scanningBrowsers.value = true
  try {
    cookieBrowsers.value = await api.cookieBrowsers()
    selectedBrowsers.value = cookieBrowsers.value
      .filter((browser) => browser.email !== '' && !browser.imported && browser.error === '')
      .map((browser) => browserKey(browser))
  } catch (error) {
    actionError(error)
  } finally {
    scanningBrowsers.value = false
  }
}

function closeBrowserImport(): void {
  if (pendingAction.value === 'browser-import') return
  showBrowserImport.value = false
  cookieBrowsers.value = []
  selectedBrowsers.value = []
}

// switchToCookiePaste 关闭指纹浏览器导入并打开粘贴 Cookie
function switchToCookiePaste(): void {
  closeBrowserImport()
  showCookieImport.value = true
}

// importBrowserAccounts 在账户任务中导入勾选的调试浏览器账户并关闭对话框，账户沿用浏览器的语言与时区
async function importBrowserAccounts(): Promise<void> {
  pendingAction.value = 'browser-import'
  try {
    const chosen = new Set(selectedBrowsers.value)
    await api.importCookieAccounts({
      items: cookieBrowsers.value
        .filter((browser) => chosen.has(browserKey(browser)))
        .map((browser) => ({
          email: browser.email,
          cookies: '',
          cdp: browser.address,
          auth_user: browser.auth_user,
        })),
      proxy: accountEnvironment.proxy,
      locale: '',
      timezone: '',
    })
    pendingAction.value = ''
    closeBrowserImport()
  } catch (error) {
    actionError(error)
  } finally {
    pendingAction.value = ''
  }
}

// addMethods 是“添加账户”菜单中的导入方式，顺序按常用程度；服务所在平台不能从 Chrome 导入时该项不可选并说明原因
const addMethods = computed<
  {
    label: TranslationKey
    help: TranslationKey
    icon: IconName
    disabled?: boolean
    action: () => void
  }[]
>(() => [
  {
    label: 'accounts.browserLogin',
    help: 'accounts.browserLoginHelp',
    icon: 'login',
    action: () => openBrowserLogin(),
  },
  {
    label: 'accounts.importChrome',
    help: props.chromeImport ? 'accounts.importChromeHelp' : 'accounts.importChromeUnavailable',
    icon: 'importAccounts',
    disabled: !props.chromeImport,
    action: () => void openChromeImport(),
  },
  {
    label: 'accounts.pasteCookie',
    help: 'accounts.pasteCookieHelp',
    icon: 'cookie',
    action: () => (showCookieImport.value = true),
  },
  {
    label: 'accounts.readBrowser',
    help: 'accounts.readBrowserHelp',
    icon: 'fingerprint',
    action: () => openBrowserImport(),
  },
  {
    label: 'accounts.remoteAdd',
    help: 'accounts.remoteAddHelp',
    icon: 'remote',
    action: () => void openPairing(),
  },
])

// importCookieAccounts 在账户任务中按来源与会话序号导入勾选的账户并关闭对话框，尚未识别时先识别
async function importCookieAccounts(): Promise<void> {
  if (cookieCandidates.value.length === 0) {
    await discoverCookieAccounts()
    return
  }
  pendingAction.value = 'cookie-import'
  try {
    const sources = cookieSources()
    const chosen = new Set(selectedCookieUsers.value)
    await api.importCookieAccounts({
      items: cookieCandidates.value
        .filter((item) => chosen.has(cookieKey(item)))
        .map((item) => ({
          email: item.email,
          cookies: sources[item.source]!.text,
          cdp: '',
          auth_user: item.auth_user,
        })),
      ...accountEnvironment,
    })
    pendingAction.value = ''
    closeCookieImport()
  } catch (error) {
    actionError(error)
  } finally {
    pendingAction.value = ''
  }
}

// saveAccount 保存已有账户配置并刷新产品数据
async function saveAccount(): Promise<void> {
  pendingAction.value = `edit:${editingAccountID.value}`
  try {
    await api.updateAccount(editingAccountID.value, { ...draft })
    closeEditor()
    emit('refresh')
    emit('notice', t('accounts.saved'), 'success')
  } catch (error) {
    actionError(error)
  } finally {
    pendingAction.value = ''
  }
}

// updateEnabled 保存账户是否参与请求
async function updateEnabled(account: Account, enabled: boolean): Promise<void> {
  toggling.add(account.id)
  try {
    await api.updateAccount(account.id, {
      label: account.label,
      enabled,
      proxy: account.proxy,
      locale: account.locale,
      timezone: account.timezone,
    })
  } finally {
    toggling.delete(account.id)
  }
}

// toggleAccount 切换账户是否参与请求，收尾在后台进行并显示在账户状态上
async function toggleAccount(account: Account): Promise<void> {
  try {
    await updateEnabled(account, !account.enabled)
    emit('refresh')
  } catch (error) {
    actionError(error)
  }
}

// loginAccount 在账户任务中打开浏览器重新登录账户
async function loginAccount(account: Account): Promise<void> {
  pendingAction.value = `login:${account.id}`
  try {
    openViewer(await api.loginAccount(account.id, newViewID()))
  } catch (error) {
    actionError(error)
  } finally {
    pendingAction.value = ''
  }
}

// refreshNotice 返回刷新结果提示，写出权益与模型数的变化
function refreshNotice(result: AccountRefresh): string {
  const change = (before: string, after: string) =>
    before === after ? after : `${before} → ${after}`
  const tier = (value: string) => (value === 'Unknown' ? t('accounts.tierUnknown') : value)
  return t('accounts.refreshed')
    .replace('{name}', result.account.label)
    .replace('{tier}', change(tier(result.previous_tier), tier(result.tier)))
    .replace('{models}', change(String(result.previous_models), String(result.models)))
}

// refreshOne 读取一个账户的权益与模型目录，完成后立即更新该行，失败原因记在该行
async function refreshOne(id: string): Promise<AccountRefresh> {
  refreshing.add(id)
  refreshErrors.delete(id)
  try {
    const result = await api.refreshAccount(id)
    emit('updated', result.account)
    return result
  } catch (error) {
    refreshErrors.set(id, errorText(error))
    throw error
  } finally {
    refreshing.delete(id)
  }
}

// refreshAccount 立即刷新一个账户，执行期间只禁用该账户的按钮
async function refreshAccount(account: Account): Promise<void> {
  try {
    const result = await refreshOne(account.id)
    emit('refresh')
    emit('notice', refreshNotice(result), 'success')
  } catch (error) {
    actionError(error)
  }
}

// refreshConcurrency 是刷新全部时同时刷新的账户数
const refreshConcurrency = 4

// refreshAccounts 按 refreshConcurrency 的并发依次刷新 accounts 中尚未在刷新的账户，结束后提示成功与失败的数量
async function refreshAccounts(accounts: Account[]): Promise<void> {
  const targets = accounts.filter((account) => !refreshing.has(account.id))
  for (const account of targets) queued.add(account.id)
  refreshTotal.value = targets.length
  refreshDone.value = 0
  let failed = 0
  const worker = async (): Promise<void> => {
    for (let account = targets.shift(); account; account = targets.shift()) {
      queued.delete(account.id)
      await refreshOne(account.id).catch(() => failed++)
      refreshDone.value++
    }
  }
  await Promise.all(Array.from({ length: refreshConcurrency }, worker))
  const total = refreshTotal.value
  refreshTotal.value = 0
  emit('refresh')
  emit(
    'notice',
    t('accounts.refreshedAll')
      .replace('{count}', String(total - failed))
      .replace('{failed}', String(failed)),
    failed === 0 ? 'success' : 'error',
  )
}
</script>

<template>
  <section
    class="flex min-h-0 flex-1 flex-col overflow-auto max-md:[scrollbar-gutter:stable] md:overflow-hidden"
  >
    <header
      class="page-x flex flex-wrap items-center gap-3 border-b border-line py-4 md:overflow-hidden md:[scrollbar-gutter:stable]"
    >
      <div class="min-w-0 flex-[1_1_16rem]">
        <h2 class="text-xl font-semibold text-white">{{ t('section.accounts.title') }}</h2>
        <p class="mt-1 text-sm text-gray-500">{{ t('section.accounts.description') }}</p>
      </div>
      <div class="ml-auto flex flex-wrap justify-end gap-2">
        <button
          class="relative isolate flex items-center gap-2 overflow-hidden rounded border border-line bg-raised px-4 py-2 text-sm font-medium text-gray-200 transition hover:bg-line disabled:opacity-50"
          type="button"
          :disabled="refreshTotal > 0"
          :aria-busy="refreshTotal > 0"
          :aria-describedby="refreshTotal > 0 ? 'refresh-all-progress' : undefined"
          @click="refreshAccounts(accounts.filter((account) => account.enabled))"
        >
          <span
            v-if="refreshTotal > 0"
            class="absolute inset-y-0 left-0 -z-10 bg-line transition-[width] duration-300 motion-reduce:transition-none"
            :style="{ width: `${(refreshDone / refreshTotal) * 100}%` }"
            aria-hidden="true"
          ></span>
          <StagedRefreshIcon v-if="refreshTotal > 0" />
          <UiIcon v-else name="refresh" :size="15" />
          {{ t('accounts.refreshAll') }}
        </button>
        <span v-if="refreshTotal > 0" id="refresh-all-progress" class="sr-only">{{
          t('accounts.refreshingAll')
            .replace('{done}', String(refreshDone))
            .replace('{total}', String(refreshTotal))
        }}</span>
        <DropdownMenuRoot :modal="false">
          <DropdownMenuTrigger
            class="flex items-center gap-2 rounded bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-500 disabled:opacity-50"
            :disabled="pendingAction !== ''"
            :aria-busy="addPending"
          >
            <UiIcon :name="addPending ? 'spinner' : 'add'" :size="15" />
            {{ t('accounts.add') }}
            <UiIcon name="chevronDown" :size="14" />
          </DropdownMenuTrigger>
          <DropdownMenuPortal>
            <DropdownMenuContent
              align="end"
              :side-offset="6"
              class="z-[60] max-h-[var(--reka-dropdown-menu-content-available-height)] w-72 max-w-[calc(100vw-2rem)] overflow-y-auto rounded-lg border border-line bg-panel p-1 shadow-xl"
            >
              <DropdownMenuItem
                v-for="item in addMethods"
                :key="item.label"
                :disabled="item.disabled ?? false"
                class="group flex cursor-pointer items-start gap-3 rounded-md px-3 py-2.5 text-left outline-none data-[disabled]:cursor-default data-[highlighted]:bg-raised"
                @select="item.action"
              >
                <UiIcon
                  :name="item.icon"
                  :size="15"
                  class="mt-0.5 shrink-0 text-gray-400 group-data-[disabled]:opacity-50"
                />
                <span class="min-w-0">
                  <span class="block text-sm text-gray-200 group-data-[disabled]:opacity-50">{{
                    t(item.label)
                  }}</span>
                  <span class="block text-xs text-gray-500">{{ t(item.help) }}</span>
                </span>
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenuPortal>
        </DropdownMenuRoot>
      </div>
    </header>
    <div
      class="page-x flex min-h-0 flex-1 flex-col py-4 md:overflow-hidden md:py-8 md:[scrollbar-gutter:stable]"
    >
      <ul
        v-if="tasks.length > 0"
        class="mb-3 divide-y divide-line/60 rounded-lg border border-line bg-panel"
        :aria-label="t('accounts.tasks')"
      >
        <li v-for="task in tasks" :key="task.id" class="px-4 py-2.5">
          <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
            <div class="flex min-w-0 flex-1 basis-64 items-center gap-2.5">
              <UiIcon
                :name="
                  task.state === 'running'
                    ? 'spinner'
                    : task.state === 'done'
                      ? 'success'
                      : 'warning'
                "
                :size="15"
                class="shrink-0"
                :class="task.state === 'failed' ? 'text-red-400' : 'text-gray-400'"
              />
              <span class="shrink-0 text-sm font-medium text-white">{{
                t(taskTitles[task.kind])
              }}</span>
              <span v-if="task.items.length > 1" class="shrink-0 text-xs text-gray-500 tabular-nums"
                >{{ taskFinished(task) }}/{{ task.items.length }}</span
              >
              <template v-else>
                <span
                  v-tooltip.overflow="task.items[0]!.label || t('accounts.liveWaiting')"
                  class="min-w-0 truncate text-xs text-gray-400"
                  >{{ task.items[0]!.label || t('accounts.liveWaiting') }}</span
                >
                <button
                  v-if="task.items[0]!.state === 'failed'"
                  type="button"
                  :class="reasonClass"
                  @click="showTaskError(task, task.items[0]!)"
                >
                  {{ t('accounts.reason') }}
                </button>
              </template>
            </div>
            <div class="ml-auto flex shrink-0 items-center gap-2">
              <button
                v-if="task.state === 'running' && task.view"
                type="button"
                class="rounded border border-line bg-raised px-3 py-1 text-xs text-gray-300 transition hover:bg-line"
                @click="viewerTaskID = task.id"
              >
                {{ t('accounts.liveShow') }}
              </button>
              <button
                v-if="task.state === 'running'"
                type="button"
                class="rounded border border-line bg-raised px-3 py-1 text-xs text-gray-300 transition hover:bg-line"
                @click="dismissTask(task)"
              >
                {{ t('common.cancel') }}
              </button>
              <button
                v-else
                v-tooltip="t('common.close')"
                type="button"
                class="flex size-[26px] items-center justify-center rounded text-gray-400 transition hover:bg-line hover:text-gray-200"
                :aria-label="t('common.close')"
                @click="dismissTask(task)"
              >
                <UiIcon name="close" :size="14" />
              </button>
            </div>
          </div>
          <ul
            v-if="task.items.length > 1"
            class="mt-2 max-h-40 space-y-1 overflow-y-auto border-t border-line/60 pt-2"
          >
            <li
              v-for="(item, index) in task.items"
              :key="index"
              class="flex items-center gap-2.5 text-xs"
            >
              <UiIcon
                v-if="item.state !== 'pending'"
                :name="
                  item.state === 'running' ? 'spinner' : item.state === 'done' ? 'check' : 'warning'
                "
                :size="13"
                class="shrink-0"
                :class="item.state === 'failed' ? 'text-red-400' : 'text-gray-400'"
              />
              <span v-else class="w-[13px] shrink-0"></span>
              <span class="sr-only">{{ t(taskStates[item.state]) }}</span>
              <span
                v-tooltip.overflow="item.label"
                class="min-w-0 truncate"
                :class="item.state === 'pending' ? 'text-gray-500' : 'text-gray-300'"
                >{{ item.label }}</span
              >
              <button
                v-if="item.state === 'failed'"
                type="button"
                :class="reasonClass"
                @click="showTaskError(task, item)"
              >
                {{ t('accounts.reason') }}
              </button>
            </li>
          </ul>
        </li>
      </ul>
      <div class="mb-3 flex flex-wrap items-center gap-2">
        <input
          v-model="search"
          type="search"
          :aria-label="t('accounts.search')"
          :placeholder="t('accounts.search')"
          class="min-w-0 flex-[1_1_12rem] rounded-md border border-line bg-canvas px-3 py-1 text-sm text-white focus:border-blue-500 focus:outline-none"
        />
        <UiSelect
          v-model="selectedState"
          :aria-label="t('accounts.filterState')"
          class="rounded-md border border-line bg-canvas px-2 py-1.5 text-xs text-gray-200"
        >
          <option value="">{{ t('accounts.allStates') }}</option>
          <option v-for="state in states" :key="state" :value="state">
            {{ t(stateKeys[state]) }}
          </option>
        </UiSelect>
        <UiSelect
          v-model="selectedTier"
          :aria-label="t('accounts.filterTier')"
          class="rounded-md border border-line bg-canvas px-2 py-1.5 text-xs text-gray-200"
        >
          <option value="">{{ t('accounts.allTiers') }}</option>
          <option v-for="tier in tiers" :key="tier" :value="tier">
            {{ tier === 'Unknown' ? t('accounts.tierUnknown') : tier }}
          </option>
        </UiSelect>
        <UiSelect
          v-model="selectedEgress"
          :aria-label="t('accounts.filterEgress')"
          class="max-w-60 rounded-md border border-line bg-canvas px-2 py-1.5 text-xs text-gray-200"
        >
          <option value="*">{{ t('accounts.allEgresses') }}</option>
          <option v-for="egress in egresses" :key="egress" :value="egress">
            {{ egress ? maskedProxy(egress) : t('accounts.direct') }}
          </option>
        </UiSelect>
      </div>
      <p v-if="error" class="flex items-center gap-2 text-sm text-red-300">
        <UiIcon name="warning" :size="15" class="shrink-0" />{{ error }}
      </p>
      <div v-else-if="loading" class="py-12 text-center text-gray-500">
        {{ t('common.loading') }}
      </div>
      <div
        v-else-if="accounts.length === 0"
        class="rounded border border-line bg-panel py-10 text-center text-gray-500"
      >
        <p class="mb-2">{{ t('accounts.empty') }}</p>
        <p class="text-xs">{{ t('accounts.emptyHint') }}</p>
      </div>
      <div
        v-else-if="visibleAccounts.length === 0"
        class="rounded border border-line bg-panel py-10 text-center text-sm text-gray-500"
      >
        {{ t('accounts.noMatches') }}
      </div>
      <div
        v-else
        class="flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg border border-line bg-panel max-md:max-h-[70vh] max-md:shrink-0 max-md:basis-auto"
      >
        <div class="flex min-h-10 items-center gap-3 border-b border-line px-4 py-1.5 text-xs">
          <label class="flex cursor-pointer items-center gap-3">
            <input
              class="h-4 w-4 shrink-0 accent-blue-600"
              type="checkbox"
              :checked="allSelected"
              :indeterminate.prop="someSelected"
              :aria-label="t('accounts.selectAllRows')"
              @change="toggleAllSelected"
            />
            <span class="text-gray-400 tabular-nums">{{
              selectedAccounts.length > 0
                ? t('accounts.selectedCount').replace('{count}', String(selectedAccounts.length))
                : t('accounts.totalCount').replace('{count}', String(visibleAccounts.length))
            }}</span>
          </label>
          <DropdownMenuRoot v-if="selectedAccounts.length > 0" :modal="false">
            <DropdownMenuTrigger
              class="flex items-center gap-1 rounded bg-raised px-2 py-0.5 text-xs text-gray-200 transition hover:bg-line"
            >
              {{ t('accounts.bulk') }}
              <UiIcon name="chevronDown" :size="13" />
            </DropdownMenuTrigger>
            <DropdownMenuPortal>
              <DropdownMenuContent
                align="start"
                :side-offset="4"
                class="z-[60] min-w-36 rounded-lg border border-line bg-panel p-1 shadow-xl"
              >
                <DropdownMenuItem
                  class="flex cursor-pointer items-center gap-2.5 rounded-md px-3 py-1.5 text-sm text-gray-200 outline-none data-[disabled]:cursor-default data-[disabled]:opacity-40 data-[highlighted]:bg-raised"
                  :disabled="bulkDisabled(true)"
                  @select="bulkSetEnabled(true)"
                >
                  <UiIcon name="success" :size="15" />
                  {{ t('common.enable') }}
                </DropdownMenuItem>
                <DropdownMenuItem
                  class="flex cursor-pointer items-center gap-2.5 rounded-md px-3 py-1.5 text-sm text-gray-200 outline-none data-[disabled]:cursor-default data-[disabled]:opacity-40 data-[highlighted]:bg-raised"
                  :disabled="bulkDisabled(false)"
                  @select="bulkSetEnabled(false)"
                >
                  <UiIcon name="disable" :size="15" />
                  {{ t('common.disable') }}
                </DropdownMenuItem>
                <DropdownMenuItem
                  class="flex cursor-pointer items-center gap-2.5 rounded-md px-3 py-1.5 text-sm text-gray-200 outline-none data-[disabled]:cursor-default data-[disabled]:opacity-40 data-[highlighted]:bg-raised"
                  :disabled="bulkDisabled(false) || refreshTotal > 0"
                  @select="bulkRefresh"
                >
                  <UiIcon name="refresh" :size="15" />
                  {{ t('accounts.refresh') }}
                </DropdownMenuItem>
                <DropdownMenuSeparator class="my-1 h-px bg-line" />
                <DropdownMenuItem
                  class="flex cursor-pointer items-center gap-2.5 rounded-md px-3 py-1.5 text-sm text-red-400 outline-none data-[highlighted]:bg-raised"
                  @select="bulkDelete"
                >
                  <UiIcon name="trash" :size="15" />
                  {{ t('common.delete') }}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenuPortal>
          </DropdownMenuRoot>
        </div>
        <div
          ref="accountList"
          class="min-h-0 flex-1 overflow-y-auto [scrollbar-gutter:stable] lg:grid lg:grid-cols-[minmax(0,1fr)_auto_auto_auto] lg:content-start"
        >
          <div
            v-for="account in pagedAccounts"
            :key="account.id"
            class="flex cursor-pointer flex-wrap items-center gap-x-4 gap-y-2 border-b border-line/60 px-4 py-2.5 transition-colors lg:col-span-4 lg:grid lg:grid-cols-subgrid"
            :class="selected.has(account.id) ? 'bg-blue-500/[0.06]' : 'hover:bg-raised/50'"
            @click="toggleRowSelected(account, $event)"
          >
            <div class="flex min-w-0 flex-1 basis-72 items-center gap-3">
              <input
                class="h-4 w-4 shrink-0 accent-blue-600"
                type="checkbox"
                :checked="selected.has(account.id)"
                :aria-label="t('accounts.selectRow').replace('{name}', account.label)"
                @change="toggleAccountSelected(account.id)"
              />
              <div class="min-w-0">
                <span
                  v-tooltip.overflow="account.label"
                  class="block truncate text-sm font-medium text-white"
                  >{{ account.label }}</span
                >
                <span
                  v-tooltip.overflow="accountSummary(account)"
                  class="block truncate text-xs text-gray-500"
                  >{{ accountSummary(account) }}</span
                >
              </div>
            </div>
            <span class="shrink-0 text-xs text-gray-500 md:w-20"
              >{{ t('accounts.models') }}
              {{ account.models.length === 0 ? '—' : account.models.length }}</span
            >
            <div
              class="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-1 md:w-36 lg:w-auto lg:min-w-36 lg:flex-nowrap lg:whitespace-nowrap"
            >
              <span class="grid text-xs">
                <span
                  v-for="(key, state) in stateKeys"
                  :key="state"
                  class="invisible border px-1.5 [grid-area:1/1] max-md:hidden"
                  aria-hidden="true"
                  >{{ t(key) }}</span
                >
                <span
                  v-for="key in transitionKeys"
                  :key="key"
                  class="invisible pl-[19px] [grid-area:1/1] max-lg:hidden"
                  aria-hidden="true"
                  >{{ t(key).replace('{count}', '1') }}</span
                >
                <span
                  v-if="account.transition"
                  class="flex items-center gap-1.5 text-gray-400 [grid-area:1/1]"
                  ><UiIcon name="spinner" :size="13" class="shrink-0" />{{
                    transitionText(account)
                  }}</span
                >
                <span
                  v-else
                  class="justify-self-start rounded border border-current px-1.5 [grid-area:1/1]"
                  :class="stateTone[account.state]"
                  >{{ t(stateKeys[account.state]) }}</span
                >
              </span>
              <span class="grid text-xs">
                <span
                  v-for="key in reasonKeys"
                  :key="key"
                  aria-hidden="true"
                  class="invisible [grid-area:1/1] max-lg:hidden"
                  >{{ t(key) }}</span
                >
                <button
                  v-if="account.message"
                  type="button"
                  class="[grid-area:1/1]"
                  :class="reasonClass"
                  @click="
                    reason = {
                      title: account.label,
                      kind: t(stateKeys[account.state]),
                      text: account.message,
                    }
                  "
                >
                  {{ t('accounts.reason') }}
                </button>
                <button
                  v-else-if="refreshErrors.has(account.id)"
                  type="button"
                  class="[grid-area:1/1]"
                  :class="reasonClass"
                  @click="
                    reason = {
                      title: account.label,
                      kind: t('accounts.refreshFailed'),
                      text: refreshErrors.get(account.id) ?? '',
                    }
                  "
                >
                  {{ t('accounts.refreshFailed') }}
                </button>
              </span>
            </div>
            <div
              class="ml-auto flex max-w-full flex-wrap items-center justify-end gap-1 lg:flex-nowrap"
            >
              <button
                v-if="account.state === 'auth_required'"
                class="rounded bg-blue-600 px-3 py-1 text-xs text-white transition hover:bg-blue-500 disabled:opacity-50"
                type="button"
                :disabled="
                  pendingAction !== '' ||
                  reloggingAccounts.has(account.id) ||
                  refreshing.has(account.id) ||
                  !account.enabled
                "
                :aria-busy="
                  pendingAction === `login:${account.id}` || reloggingAccounts.has(account.id)
                "
                @click="loginAccount(account)"
              >
                {{ t('common.relogin') }}
              </button>
              <button
                v-tooltip="t(queued.has(account.id) ? 'accounts.queued' : 'accounts.refresh')"
                type="button"
                :class="[accountIconClass, 'hover:text-gray-200']"
                :disabled="
                  refreshing.has(account.id) ||
                  queued.has(account.id) ||
                  pendingAction === `login:${account.id}` ||
                  reloggingAccounts.has(account.id) ||
                  !account.enabled ||
                  account.transition !== undefined
                "
                :aria-label="t(queued.has(account.id) ? 'accounts.queued' : 'accounts.refresh')"
                :aria-busy="refreshing.has(account.id)"
                @click="refreshAccount(account)"
              >
                <UiIcon
                  :name="
                    refreshing.has(account.id) || queued.has(account.id) ? 'spinner' : 'refresh'
                  "
                  :size="15"
                />
              </button>
              <button
                v-tooltip="t('common.edit')"
                type="button"
                :class="[accountIconClass, 'hover:text-gray-200']"
                :disabled="
                  pendingAction !== '' || refreshing.has(account.id) || toggling.has(account.id)
                "
                :aria-label="t('common.edit')"
                @click="beginEdit(account)"
              >
                <UiIcon name="edit" :size="15" />
              </button>
              <button
                v-tooltip="t(account.enabled ? 'common.disable' : 'common.enable')"
                type="button"
                :class="[accountIconClass, 'hover:text-gray-200']"
                :disabled="
                  pendingAction !== '' || refreshing.has(account.id) || toggling.has(account.id)
                "
                :aria-label="t(account.enabled ? 'common.disable' : 'common.enable')"
                :aria-busy="toggling.has(account.id)"
                @click="toggleAccount(account)"
              >
                <UiIcon
                  :name="
                    toggling.has(account.id) ? 'spinner' : account.enabled ? 'disable' : 'success'
                  "
                  :size="15"
                />
              </button>
              <button
                v-tooltip="t('common.delete')"
                type="button"
                :class="[accountIconClass, 'hover:text-red-400']"
                :disabled="
                  pendingAction !== '' || refreshing.has(account.id) || toggling.has(account.id)
                "
                :aria-label="t('common.delete')"
                @click="emit('delete', [account])"
              >
                <UiIcon name="trash" :size="15" />
              </button>
            </div>
          </div>
        </div>
        <UiPager v-model="accountPage" :pages="accountPages" />
      </div>

      <DetailDialog
        :open="reason !== null"
        :title="reason?.title ?? ''"
        :description="reason?.kind ?? ''"
        :copy-text="reason?.text"
        @close="reason = null"
      >
        <pre
          v-if="reason"
          class="rounded-md border border-line bg-canvas p-3 font-mono text-xs leading-5 whitespace-pre-wrap [overflow-wrap:anywhere] text-red-300"
          >{{ logText(reason.text) }}</pre>
      </DetailDialog>

      <LoginViewer
        v-if="viewerTask?.view"
        :key="viewerTask.view"
        :view="viewerTask.view"
        :error="viewerTask.state === 'failed' ? taskError(viewerTask.items[0]!) : ''"
        @unavailable="viewerUnavailable"
        @cancel="cancelViewerTask"
        @hide="viewerTaskID = ''"
        @close="viewerTaskID = ''"
      />

      <Teleport to="body">
        <div v-if="showEditor" class="modal-overlay" @click.self="closeEditor">
          <form
            class="mx-4 w-full max-w-md rounded-lg border border-line bg-panel p-6 shadow-xl"
            @submit.prevent="saveAccount"
          >
            <div class="mb-4 flex items-center justify-between">
              <h3 class="text-lg font-bold text-white">{{ t('accounts.editTitle') }}</h3>
              <button
                class="rounded p-1 text-gray-500 hover:bg-line hover:text-white"
                type="button"
                :aria-label="t('common.close')"
                @click="closeEditor"
              >
                <UiIcon name="close" :size="15" />
              </button>
            </div>
            <p v-tooltip.overflow="draft.label" class="mb-4 truncate text-sm text-gray-300">
              {{ draft.label }}
            </p>
            <div class="space-y-4">
              <label class="flex items-center justify-between">
                <span class="text-sm font-medium text-gray-400">{{ t('common.enable') }}</span>
                <input v-model="draft.enabled" class="h-4 w-4 accent-blue-600" type="checkbox" />
              </label>
              <AccountEnvironmentFields :model-value="draft" />
            </div>
            <div class="mt-6 flex gap-2">
              <button
                class="flex-1 rounded bg-raised py-2 text-sm text-gray-300 transition hover:bg-line"
                type="button"
                @click="closeEditor"
              >
                {{ t('common.cancel') }}
              </button>
              <button
                class="flex-1 rounded bg-blue-600 py-2 text-sm text-white transition hover:bg-blue-500 disabled:opacity-50"
                type="submit"
                :disabled="pendingAction !== ''"
                :aria-busy="pendingAction.startsWith('edit:')"
              >
                {{ t('common.save') }}
              </button>
            </div>
          </form>
        </div>

        <div v-if="showBrowserLogin" class="modal-overlay" @click.self="closeBrowserLogin">
          <form
            class="mx-4 w-full max-w-md rounded-lg border border-line bg-panel p-6 shadow-xl"
            @submit.prevent="beginBrowserLogin"
          >
            <div class="mb-4 flex items-center justify-between">
              <h3 class="text-lg font-bold text-white">{{ t('accounts.browserLogin') }}</h3>
              <button
                class="rounded p-1 text-gray-500 hover:bg-line hover:text-white disabled:opacity-50"
                type="button"
                :disabled="pendingAction === 'browser-login'"
                :aria-label="t('common.close')"
                @click="closeBrowserLogin"
              >
                <UiIcon name="close" :size="15" />
              </button>
            </div>
            <div class="space-y-4">
              <AccountEnvironmentFields :model-value="accountEnvironment" />
            </div>
            <div class="mt-6 flex gap-2">
              <button
                class="flex-1 rounded bg-raised py-2 text-sm text-gray-300 transition hover:bg-line disabled:opacity-50"
                type="button"
                :disabled="pendingAction === 'browser-login'"
                @click="closeBrowserLogin"
              >
                {{ t('common.cancel') }}
              </button>
              <button
                class="flex-1 rounded bg-blue-600 py-2 text-sm text-white transition hover:bg-blue-500 disabled:opacity-50"
                type="submit"
                :disabled="pendingAction !== ''"
                :aria-busy="pendingAction === 'browser-login'"
              >
                {{ t('accounts.browserLogin') }}
              </button>
            </div>
          </form>
        </div>

        <div v-if="showChromeImport" class="modal-overlay" @click.self="closeChromeImport">
          <form
            class="mx-4 w-full max-w-lg rounded-lg border border-line bg-panel p-6 shadow-xl"
            @submit.prevent="importChromeAccounts"
          >
            <div class="mb-4 flex items-center justify-between">
              <h3 class="text-lg font-bold text-white">{{ t('accounts.chromeTitle') }}</h3>
              <button
                class="rounded p-1 text-gray-500 hover:bg-line hover:text-white disabled:opacity-50"
                type="button"
                :disabled="pendingAction === 'chrome-import'"
                :aria-label="t('common.close')"
                @click="closeChromeImport"
              >
                <UiIcon name="close" :size="15" />
              </button>
            </div>
            <div class="mb-4 space-y-4">
              <AccountEnvironmentFields :model-value="accountEnvironment" />
            </div>
            <div v-if="chromeProfiles.length === 0" class="py-8 text-center text-sm text-gray-500">
              {{ t('accounts.chromeEmpty') }}
            </div>
            <div v-else class="mb-2 flex justify-end">
              <button
                class="text-xs text-blue-400 transition hover:text-blue-300"
                type="button"
                @click="toggleAllChromeProfiles"
              >
                {{
                  allChromeProfilesSelected ? t('accounts.deselectAll') : t('accounts.selectAll')
                }}
              </button>
            </div>
            <div v-if="chromeProfiles.length > 0" class="max-h-[50vh] space-y-2 overflow-auto">
              <label
                v-for="profile in chromeProfiles"
                :key="profile.id"
                class="flex cursor-pointer items-start gap-3 rounded border border-line bg-canvas p-3 transition hover:border-gray-600"
              >
                <input
                  v-model="selectedChromeProfiles"
                  class="mt-1 h-4 w-4 shrink-0 accent-blue-600"
                  type="checkbox"
                  :value="profile.id"
                />
                <span class="min-w-0 flex-1">
                  <strong
                    v-tooltip.overflow="profile.email"
                    class="block truncate text-sm text-white"
                    >{{ profile.email }}</strong
                  >
                  <span
                    v-tooltip.overflow="profile.display_name"
                    class="block truncate text-xs text-gray-400"
                    >{{ profile.display_name }}</span
                  >
                  <span
                    v-tooltip.overflow="profile.profile"
                    class="block truncate font-mono text-xs text-gray-500"
                    >{{ profile.profile }}</span
                  >
                </span>
              </label>
            </div>
            <div class="mt-6 flex gap-2">
              <button
                class="flex-1 rounded bg-raised py-2 text-sm text-gray-300 transition hover:bg-line disabled:opacity-50"
                type="button"
                :disabled="pendingAction === 'chrome-import'"
                @click="closeChromeImport"
              >
                {{ t('common.cancel') }}
              </button>
              <button
                class="flex-1 rounded bg-blue-600 py-2 text-sm text-white transition hover:bg-blue-500 disabled:opacity-50"
                type="submit"
                :disabled="pendingAction !== '' || selectedChromeProfiles.length === 0"
                :aria-busy="pendingAction === 'chrome-import'"
              >
                {{ t('accounts.importSelected') }}
              </button>
            </div>
          </form>
        </div>

        <div v-if="showCookieImport" class="modal-overlay" @click.self="closeCookieImport">
          <form
            class="mx-4 max-h-[calc(100dvh-2rem)] w-full max-w-lg overflow-y-auto rounded-lg border border-line bg-panel p-6 shadow-xl"
            @submit.prevent="importCookieAccounts"
          >
            <div class="mb-4 flex items-center justify-between">
              <h3 class="text-lg font-bold text-white">{{ t('accounts.pasteCookie') }}</h3>
              <button
                class="rounded p-1 text-gray-500 hover:bg-line hover:text-white disabled:opacity-50"
                type="button"
                :disabled="pendingAction === 'cookie-import'"
                :aria-label="t('common.close')"
                @click="closeCookieImport"
              >
                <UiIcon name="close" :size="15" />
              </button>
            </div>
            <ol class="mb-5 list-decimal space-y-1 pl-5 text-sm leading-6 text-gray-300">
              <li v-for="step in pasteSteps" :key="step">{{ t(step) }}</li>
            </ol>
            <div class="space-y-4">
              <div>
                <div class="mb-1 flex items-center justify-between gap-3">
                  <label for="cookie-text" class="text-sm font-medium text-gray-300">{{
                    t('accounts.cookies')
                  }}</label>
                  <button
                    class="inline-flex shrink-0 items-center gap-1.5 rounded px-2 py-1 text-sm text-blue-400 transition hover:bg-line hover:text-blue-300 disabled:opacity-50"
                    type="button"
                    :disabled="pendingAction !== ''"
                    @click="cookieFiles.length > 0 ? clearCookieFiles() : cookieFileInput!.click()"
                  >
                    <UiIcon :name="cookieFiles.length > 0 ? 'edit' : 'chooseFiles'" :size="14" />
                    {{
                      cookieFiles.length > 0
                        ? t('accounts.pasteCookieText')
                        : t('accounts.chooseCookieFiles')
                    }}
                  </button>
                  <input
                    ref="cookieFileInput"
                    class="hidden"
                    type="file"
                    multiple
                    accept=".json,.txt,application/json,text/plain"
                    @change="readCookieFiles"
                  />
                </div>
                <ul
                  v-if="cookieFiles.length > 0"
                  class="max-h-36 space-y-1 overflow-auto rounded border border-line bg-canvas px-3 py-2"
                >
                  <li
                    v-for="(file, index) in cookieFiles"
                    :key="index"
                    v-tooltip.overflow="file.name"
                    class="truncate font-mono text-xs text-white"
                  >
                    {{ file.name }}
                  </li>
                </ul>
                <textarea
                  v-else
                  id="cookie-text"
                  v-model="cookieText"
                  class="h-36 w-full resize-y rounded border border-line bg-canvas px-3 py-2 font-mono text-xs text-white transition focus:border-blue-500 focus:outline-none"
                  spellcheck="false"
                  autocomplete="off"
                  :placeholder="t('accounts.cookiePlaceholder')"
                  :readonly="pendingAction === 'cookie-import'"
                  required
                  @input="scheduleCookieDiscovery"
                />
                <p
                  v-if="discoveringCookies"
                  class="mt-2 flex items-center gap-2 text-xs text-gray-400"
                  aria-live="polite"
                >
                  <UiIcon name="spinner" :size="13" />
                  {{ t('accounts.cookieDiscovering') }}
                </p>
                <p
                  v-else-if="cookieDiscoveryError"
                  class="mt-2 max-h-24 overflow-y-auto text-xs leading-5 whitespace-pre-wrap [overflow-wrap:anywhere] text-red-300"
                  role="alert"
                >
                  {{ cookieDiscoveryError }}
                </p>
              </div>

              <div v-if="cookieCandidates.length > 0" class="space-y-2">
                <div class="flex items-center justify-between">
                  <span class="text-xs font-medium text-gray-400">
                    {{ t('accounts.cookieAccounts') }} ({{ cookieCandidates.length }})
                  </span>
                  <button
                    class="text-xs text-blue-400 transition hover:text-blue-300"
                    type="button"
                    @click="toggleAllCookieCandidates"
                  >
                    {{
                      allCookieCandidatesSelected
                        ? t('accounts.deselectAll')
                        : t('accounts.selectAll')
                    }}
                  </button>
                </div>
                <div class="max-h-[35vh] space-y-2 overflow-auto">
                  <label
                    v-for="candidate in cookieCandidates"
                    :key="cookieKey(candidate)"
                    class="flex items-center gap-3 rounded border border-line bg-canvas p-3 transition"
                    :class="
                      candidate.imported ? 'opacity-60' : 'cursor-pointer hover:border-gray-600'
                    "
                  >
                    <input
                      v-model="selectedCookieUsers"
                      class="h-4 w-4 shrink-0 accent-blue-600"
                      type="checkbox"
                      :value="cookieKey(candidate)"
                      :disabled="candidate.imported"
                    />
                    <span class="min-w-0 flex-1">
                      <strong
                        v-tooltip.overflow="candidate.email"
                        class="block truncate text-sm text-white"
                      >
                        {{ candidate.email }}
                      </strong>
                      <span class="block truncate font-mono text-xs text-gray-400">
                        {{
                          cookieFiles.length > 0 ? `${cookieFiles[candidate.source]!.name} · ` : ''
                        }}u/{{ candidate.auth_user }}
                      </span>
                      <span
                        v-if="candidate.device_bound"
                        class="mt-1 block text-xs leading-5 text-amber-200"
                        >{{ t('accounts.deviceBound') }}</span
                      >
                    </span>
                    <span v-if="candidate.imported" class="shrink-0 text-xs text-gray-400">{{
                      t('accounts.alreadyImported')
                    }}</span>
                  </label>
                </div>
              </div>

              <div class="rounded border border-line">
                <button
                  type="button"
                  class="flex w-full items-center gap-2 px-3 py-2 text-sm text-gray-400 hover:text-gray-200"
                  :aria-expanded="moreSettingsOpen"
                  @click="moreSettingsOpen = !moreSettingsOpen"
                >
                  <DisclosureChevron :open="moreSettingsOpen"
                    ><UiIcon name="chevronRight" :size="14"
                  /></DisclosureChevron>
                  {{ t('accounts.moreSettings') }}
                </button>
                <UiCollapse :open="moreSettingsOpen">
                  <div class="space-y-4 border-t border-line p-3">
                    <AccountEnvironmentFields :model-value="accountEnvironment" />
                  </div>
                </UiCollapse>
              </div>
            </div>
            <div class="mt-6 flex gap-2">
              <button
                class="flex-1 rounded bg-raised py-2 text-sm text-gray-300 transition hover:bg-line disabled:opacity-50"
                type="button"
                :disabled="pendingAction === 'cookie-import'"
                @click="closeCookieImport"
              >
                {{ t('common.cancel') }}
              </button>
              <button
                class="flex-1 rounded bg-blue-600 py-2 text-sm text-white transition hover:bg-blue-500 disabled:opacity-50"
                type="submit"
                :disabled="
                  pendingAction !== '' ||
                  discoveringCookies ||
                  (cookieCandidates.length > 0 && selectedCookieUsers.length === 0)
                "
                :aria-busy="pendingAction === 'cookie-import' || discoveringCookies"
              >
                {{
                  cookieCandidates.length > 0 ? t('accounts.importSelected') : t('accounts.import')
                }}
              </button>
            </div>
          </form>
        </div>
        <div v-if="showBrowserImport" class="modal-overlay" @click.self="closeBrowserImport">
          <form
            class="mx-4 max-h-[calc(100dvh-2rem)] w-full max-w-lg overflow-y-auto rounded-lg border border-line bg-panel p-6 shadow-xl"
            @submit.prevent="importBrowserAccounts"
          >
            <div class="mb-2 flex items-center justify-between">
              <h3 class="text-lg font-bold text-white">{{ t('accounts.readBrowser') }}</h3>
              <button
                class="rounded p-1 text-gray-500 hover:bg-line hover:text-white disabled:opacity-50"
                type="button"
                :disabled="pendingAction === 'browser-import'"
                :aria-label="t('common.close')"
                @click="closeBrowserImport"
              >
                <UiIcon name="close" :size="15" />
              </button>
            </div>
            <div class="mb-4 flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
              <p class="flex-1 basis-64 text-sm leading-6 text-gray-300">
                {{ t('accounts.browserHint') }}
              </p>
              <button
                class="inline-flex shrink-0 items-center gap-1.5 rounded px-2 py-1 text-sm text-blue-400 transition hover:bg-line hover:text-blue-300 disabled:opacity-50"
                type="button"
                :disabled="scanningBrowsers || pendingAction === 'browser-import'"
                @click="scanBrowsers"
              >
                <UiIcon name="refresh" :size="14" :class="{ 'animate-spin': scanningBrowsers }" />
                {{ t('accounts.rescan') }}
              </button>
            </div>
            <p class="-mt-2 mb-4 text-xs leading-5 text-gray-400">
              <template
                v-for="(part, position) in t('accounts.browserEverydayHint').split('{paste}')"
                :key="position"
              >
                <button
                  v-if="position > 0"
                  type="button"
                  class="text-blue-400 hover:underline disabled:opacity-50"
                  :disabled="pendingAction === 'browser-import'"
                  @click="switchToCookiePaste"
                >
                  {{ t('accounts.pasteCookie') }}</button
                >{{ part }}
              </template>
            </p>
            <div
              v-if="scanningBrowsers && cookieBrowsers.length === 0"
              class="py-8 text-center text-sm text-gray-400"
            >
              {{ t('accounts.browserScanning') }}
            </div>
            <div
              v-else-if="cookieBrowsers.length === 0"
              class="py-8 text-center text-sm text-gray-400"
            >
              {{ t('accounts.browserEmpty') }}
            </div>
            <div v-else class="max-h-[50vh] space-y-2 overflow-auto">
              <label
                v-for="browser in cookieBrowsers"
                :key="browserKey(browser)"
                class="flex items-center gap-3 rounded border border-line bg-canvas p-3 transition"
                :class="
                  browser.email && !browser.imported && !browser.error
                    ? 'cursor-pointer hover:border-gray-600'
                    : 'opacity-70'
                "
              >
                <input
                  v-model="selectedBrowsers"
                  class="h-4 w-4 shrink-0 accent-blue-600"
                  type="checkbox"
                  :value="browserKey(browser)"
                  :disabled="!browser.email || browser.imported || browser.error !== ''"
                />
                <span class="min-w-0 flex-1">
                  <span
                    v-tooltip.overflow="browser.email || browser.browser || browser.address"
                    class="block truncate text-sm font-medium text-white"
                    >{{ browser.email || browser.browser || browser.address }}</span
                  >
                  <span
                    v-tooltip.overflow="browserSummary(browser)"
                    class="block truncate text-xs text-gray-400"
                    >{{ browserSummary(browser) }}</span
                  >
                  <span
                    v-if="browser.device_bound"
                    class="mt-1 block text-xs leading-5 text-amber-200"
                    >{{ t('accounts.deviceBound') }}</span
                  >
                </span>
                <span v-if="browser.imported" class="shrink-0 text-xs text-gray-400">{{
                  t('accounts.alreadyImported')
                }}</span>
              </label>
            </div>
            <div class="mt-4 rounded border border-line">
              <button
                type="button"
                class="flex w-full items-center gap-2 px-3 py-2 text-sm text-gray-300 hover:text-white"
                :aria-expanded="moreSettingsOpen"
                @click="moreSettingsOpen = !moreSettingsOpen"
              >
                <DisclosureChevron :open="moreSettingsOpen"
                  ><UiIcon name="chevronRight" :size="14"
                /></DisclosureChevron>
                {{ t('accounts.moreSettings') }}
              </button>
              <UiCollapse :open="moreSettingsOpen">
                <div class="space-y-4 border-t border-line p-3">
                  <label class="block">
                    <span class="mb-1 block text-sm font-medium text-gray-300">{{
                      t('accounts.proxy')
                    }}</span>
                    <input
                      v-model.trim="accountEnvironment.proxy"
                      class="w-full rounded border border-line bg-canvas px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
                      placeholder="socks5://127.0.0.1:1080"
                    />
                  </label>
                </div>
              </UiCollapse>
            </div>
            <div class="mt-6 flex gap-2">
              <button
                class="flex-1 rounded bg-raised py-2 text-sm text-gray-300 transition hover:bg-line disabled:opacity-50"
                type="button"
                :disabled="pendingAction === 'browser-import'"
                @click="closeBrowserImport"
              >
                {{ t('common.cancel') }}
              </button>
              <button
                class="flex-1 rounded bg-blue-600 py-2 text-sm text-white transition hover:bg-blue-500 disabled:opacity-50"
                type="submit"
                :disabled="selectedBrowsers.length === 0 || pendingAction !== ''"
                :aria-busy="pendingAction === 'browser-import'"
              >
                {{ t('accounts.importSelected') }}
              </button>
            </div>
          </form>
        </div>
        <div v-if="pairing" class="modal-overlay" @click.self="closePairing">
          <div
            class="mx-4 w-full max-w-lg rounded-lg border border-line bg-panel p-6 shadow-xl"
            role="dialog"
            aria-modal="true"
            aria-labelledby="pairing-title"
          >
            <div class="mb-3 flex items-center justify-between">
              <h3 id="pairing-title" class="text-lg font-bold text-white">
                {{ t('accounts.remoteAdd') }}
              </h3>
              <button
                class="rounded p-1 text-gray-500 hover:bg-line hover:text-white"
                type="button"
                :aria-label="t('common.close')"
                @click="closePairing"
              >
                <UiIcon name="close" :size="15" />
              </button>
            </div>
            <ol class="mb-4 list-decimal space-y-1 pl-5 text-sm leading-6 text-gray-400">
              <li>
                <template
                  v-for="(part, position) in t('accounts.remoteStep1').split('{download}')"
                  :key="position"
                >
                  <a
                    v-if="position > 0"
                    class="text-blue-400 hover:underline"
                    href="https://github.com/Mag1cFall/AIStudio2API/releases/latest"
                    target="_blank"
                    rel="noreferrer"
                    >{{ t('accounts.remoteDownload') }}</a
                  >{{ part }}
                </template>
              </li>
              <li>{{ t('accounts.remoteStep2') }}</li>
            </ol>
            <label v-if="pairingLoopback" class="mb-4 block">
              <span class="mb-1 block text-xs font-medium text-gray-400">{{
                t('accounts.remoteAddress')
              }}</span>
              <input
                v-model="pairingAddress"
                type="url"
                spellcheck="false"
                autocomplete="off"
                class="w-full rounded border border-line bg-canvas px-3 py-1.5 font-mono text-sm text-gray-200 focus:border-blue-500 focus:outline-none"
              />
              <span class="mt-1 block text-xs leading-5 text-gray-500">{{
                t('accounts.remoteAddressHelp')
              }}</span>
            </label>
            <p v-if="pairingInsecure" class="mb-4 flex gap-2 text-xs leading-5 text-amber-200">
              <UiIcon name="warning" :size="14" class="mt-0.5 shrink-0" />
              {{ t(pairingLoopback ? 'accounts.remoteHttpsAddress' : 'accounts.remoteHttps') }}
            </p>
            <div
              class="mb-2 grid grid-cols-2 rounded-md border border-line bg-canvas p-0.5"
              role="radiogroup"
              :aria-label="t('accounts.remoteSource')"
            >
              <button
                v-for="login in [false, true]"
                :key="String(login)"
                type="button"
                role="radio"
                :aria-checked="pairingLogin === login"
                class="rounded px-2.5 py-1 text-xs leading-4 transition-colors"
                :class="
                  pairingLogin === login
                    ? 'bg-raised text-white'
                    : 'text-gray-400 hover:text-gray-200'
                "
                @click="pairingLogin = login"
              >
                {{ t(login ? 'accounts.remoteLogin' : 'accounts.remoteChrome') }}
              </button>
            </div>
            <div
              class="mb-2 grid grid-cols-2 rounded-md border border-line bg-canvas p-0.5"
              role="radiogroup"
              :aria-label="t('accounts.remoteSystem')"
            >
              <button
                v-for="system in ['windows', 'unix'] as const"
                :key="system"
                type="button"
                role="radio"
                :aria-checked="pairingSystem === system"
                class="rounded px-2.5 py-1 text-xs leading-4 transition-colors"
                :class="
                  pairingSystem === system
                    ? 'bg-raised text-white'
                    : 'text-gray-400 hover:text-gray-200'
                "
                @click="pairingSystem = system"
              >
                {{ t(system === 'windows' ? 'accounts.remoteWindows' : 'accounts.remoteUnix') }}
              </button>
            </div>
            <div
              class="mb-3 flex items-start gap-2 rounded border border-line bg-canvas py-1.5 pr-1.5 pl-3"
            >
              <code class="min-w-0 flex-1 py-0.5 font-mono text-sm text-gray-200 select-all"
                ><template v-for="(part, index) in pairingCommand.split(' ')" :key="index"
                  >{{ index === 0 ? '' : ' '
                  }}<span class="inline-block max-w-full [overflow-wrap:anywhere]">{{
                    part
                  }}</span></template
                ></code
              >
              <button
                v-if="clipboardAvailable"
                class="flex shrink-0 items-center gap-1 rounded px-2 py-1 text-xs text-gray-400 transition hover:bg-raised hover:text-white"
                type="button"
                @click="copyPairing"
              >
                <UiIcon :name="copiedPairing ? 'check' : 'copy'" :size="13" />
                {{ copiedPairing ? t('common.copied') : t('common.copy') }}
              </button>
            </div>
            <div class="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
              <p class="text-xs text-gray-500">
                {{
                  t('accounts.remoteExpires').replace(
                    '{time}',
                    new Date(pairing.expires_at).toLocaleTimeString(locale, { hour12: false }),
                  )
                }}
              </p>
              <button
                class="flex items-center gap-1 rounded px-2 py-1 text-xs text-gray-400 transition hover:bg-raised hover:text-white disabled:opacity-50"
                type="button"
                :disabled="pendingAction === 'pairing-renew'"
                :aria-busy="pendingAction === 'pairing-renew'"
                @click="renewPairing"
              >
                <UiIcon
                  name="refresh"
                  :size="13"
                  :class="{ 'animate-spin': pendingAction === 'pairing-renew' }"
                />
                {{ t('accounts.remoteRenew') }}
              </button>
            </div>
          </div>
        </div>
      </Teleport>
    </div>
  </section>
</template>
