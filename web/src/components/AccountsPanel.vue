<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from 'reka-ui'
import { api } from '@/api'
import { useI18n, type TranslationKey } from '@/i18n'
import { maskedProxy } from '@/proxy'
import type {
  Account,
  AccountDraft,
  AccountRefresh,
  AccountState,
  ChromeImportProfile,
  CookieBrowser,
  PairingToken,
} from '@/types'
import type { IconName } from '@/icons'
import AccountEnvironmentFields from './AccountEnvironmentFields.vue'
import DetailDialog from './DetailDialog.vue'
import LoginViewer from './LoginViewer.vue'
import UiIcon from './UiIcon.vue'
import UiSelect from './UiSelect.vue'

const props = defineProps<{
  accounts: Account[]
  loading: boolean
  error: string
}>()

const emit = defineEmits<{
  refresh: []
  updated: [account: Account]
  notice: [message: string, tone: 'success' | 'error']
  delete: [account: Account]
}>()

const { t, locale, errorText, logText } = useI18n()
const defaultAccountLocale = navigator.language
const defaultAccountTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone
const showEditor = ref(false)
const showBrowserLogin = ref(false)
const showChromeImport = ref(false)
const showCookieImport = ref(false)
const cookieText = ref('')
const cookieCandidates = ref<CookieBrowser[]>([])
const selectedCookieUsers = ref<number[]>([])
const showBrowserImport = ref(false)
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
const copiedPairing = ref('')
const pairingCommand = `${navigator.userAgent.includes('Windows') ? 'cmd /c start /wait aistudio2api.exe' : './aistudio2api'} setup --remote ${window.location.origin}`
const pairingHost = window.location.hostname.replace(/^\[|\]$/g, '')
const pairingInsecure =
  window.location.protocol !== 'https:' &&
  pairingHost !== 'localhost' &&
  pairingHost !== '::1' &&
  !/^127\.\d+\.\d+\.\d+$/.test(pairingHost)
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
  timezone: defaultAccountTimezone,
})
const draft = reactive<AccountDraft>({
  label: '',
  enabled: true,
  proxy: '',
  locale: defaultAccountLocale,
  timezone: defaultAccountTimezone,
})

const stateKeys: Record<AccountState, TranslationKey> = {
  ready: 'state.ready',
  busy: 'state.busy',
  cooldown: 'state.cooldown',
  auth_required: 'state.auth_required',
  unavailable: 'state.unavailable',
  disabled: 'state.disabled',
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

// reason 是悬窗中显示的账户、原因类别与完整原因
const reason = ref<{ account: Account; kind: string; text: string } | null>(null)

// accountSummary 把账户等级、出口、语言与时区合成一行
function accountSummary(account: Account): string {
  return [
    account.benefit_tier === 'Unknown' ? t('accounts.tierUnknown') : account.benefit_tier,
    account.egress ? maskedProxy(account.egress) : t('accounts.direct'),
    account.locale,
    account.timezone,
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

// LiveLogin 是进行中的浏览器登录：view 为登录窗口 ID，active 表示服务端把登录画面显示在页内登录窗口，error 为窗口中显示的失败原因
interface LiveLogin {
  view: string
  controller: AbortController
  active: boolean
  error: string
}

const liveLogin = ref<LiveLogin | null>(null)

// newViewID 生成登录窗口 ID
function newViewID(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) =>
    byte.toString(16).padStart(2, '0'),
  ).join('')
}

// runBrowserLogin 以新的登录窗口 ID 执行一次浏览器登录并返回是否成功，页内登录窗口打开时失败原因显示在窗口中
async function runBrowserLogin(
  run: (view: string, signal: AbortSignal) => Promise<unknown>,
): Promise<boolean> {
  const login = reactive<LiveLogin>({
    view: newViewID(),
    controller: new AbortController(),
    active: false,
    error: '',
  })
  liveLogin.value = login
  try {
    await run(login.view, login.controller.signal)
    liveLogin.value = null
    return true
  } catch (error) {
    if (login.controller.signal.aborted) {
      if (liveLogin.value === login) liveLogin.value = null
    } else if (login.active) {
      login.error = errorText(error)
    } else {
      liveLogin.value = null
      actionError(error)
    }
    return false
  }
}

// showLiveLogin 在服务端启用页内登录窗口后关闭登录设置对话框
function showLiveLogin(): void {
  liveLogin.value!.active = true
  showBrowserLogin.value = false
}

// liveLoginUnavailable 在登录窗口无法连接时取消登录
function liveLoginUnavailable(): void {
  liveLogin.value!.controller.abort()
  liveLogin.value = null
  emit('notice', t('accounts.liveUnavailable'), 'error')
}

// beginBrowserLogin 启动浏览器登录并使用返回身份创建账户
async function beginBrowserLogin(): Promise<void> {
  pendingAction.value = 'browser-login'
  const environment = { ...accountEnvironment }
  const done = await runBrowserLogin((view, signal) => api.createAccount(environment, view, signal))
  pendingAction.value = ''
  if (!done) return
  showBrowserLogin.value = false
  emit('refresh')
  emit('notice', t('accounts.loginComplete'), 'success')
}

// openPairing 生成远程配对令牌，旧令牌随之失效
async function openPairing(): Promise<void> {
  pendingAction.value = 'pairing'
  try {
    pairing.value = await api.createPairing()
  } catch (error) {
    actionError(error)
  } finally {
    pendingAction.value = ''
  }
}

// copyPairing 复制配对命令或令牌
async function copyPairing(kind: 'command' | 'token', text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text)
    copiedPairing.value = kind
    window.setTimeout(() => {
      if (copiedPairing.value === kind) copiedPairing.value = ''
    }, 1200)
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

// importChromeAccounts 导入用户选中的 Chrome 账户
async function importChromeAccounts(): Promise<void> {
  pendingAction.value = 'chrome-import'
  try {
    const result = await api.importChromeAccounts({
      account_ids: [...selectedChromeProfiles.value],
      ...accountEnvironment,
    })
    showChromeImport.value = false
    chromeProfiles.value = []
    selectedChromeProfiles.value = []
    emit('refresh')
    emit(
      'notice',
      t(
        result.accounts.length === 1 ? 'accounts.importCompleteOne' : 'accounts.importComplete',
      ).replace('{count}', String(result.accounts.length)),
      'success',
    )
  } catch (error) {
    actionError(error)
  } finally {
    pendingAction.value = ''
  }
}

function closeCookieImport(): void {
  if (pendingAction.value === 'cookie-import' || pendingAction.value === 'cookie-discover') return
  showCookieImport.value = false
  cookieText.value = ''
  cookieCandidates.value = []
  selectedCookieUsers.value = []
}

function resetCookieDiscovery(): void {
  cookieCandidates.value = []
  selectedCookieUsers.value = []
}

// importableCookieUsers 是 Cookie 中尚未导入的会话序号
const importableCookieUsers = computed(() =>
  cookieCandidates.value.filter((item) => !item.imported).map((item) => item.auth_user),
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

// importBrowserAccounts 依次导入勾选的调试浏览器中登录的账户
async function importBrowserAccounts(): Promise<void> {
  pendingAction.value = 'browser-import'
  try {
    const chosen = new Set(selectedBrowsers.value)
    const targets = cookieBrowsers.value.filter((browser) => chosen.has(browserKey(browser)))
    for (const target of targets) {
      await api.importCookieAccount({
        label: '',
        cookies: '',
        cdp: target.address,
        auth_user: target.auth_user,
        proxy: accountEnvironment.proxy,
        locale: '',
        timezone: '',
      })
    }
    const count = targets.length
    pendingAction.value = ''
    closeBrowserImport()
    emit('refresh')
    emit(
      'notice',
      t(count === 1 ? 'accounts.importCompleteOne' : 'accounts.importComplete').replace(
        '{count}',
        String(count),
      ),
      'success',
    )
  } catch (error) {
    actionError(error)
    void scanBrowsers()
  } finally {
    pendingAction.value = ''
  }
}

// remoteSteps 是远程添加的操作步骤
const remoteSteps: TranslationKey[] = [
  'accounts.remoteStep1',
  'accounts.remoteStep2',
  'accounts.remoteStep3',
]

// addMethods 是“添加账户”菜单中的导入方式，顺序按常用程度
const addMethods: {
  label: TranslationKey
  help: TranslationKey
  icon: IconName
  action: () => void
}[] = [
  {
    label: 'accounts.browserLogin',
    help: 'accounts.browserLoginHelp',
    icon: 'login',
    action: () => openBrowserLogin(),
  },
  {
    label: 'accounts.importChrome',
    help: 'accounts.importChromeHelp',
    icon: 'importAccounts',
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
]

// importCookieAccount 第一步识别 Cookie 中各会话登录的账户供勾选，第二步按会话序号导入勾选的账户
async function importCookieAccount(): Promise<void> {
  if (cookieCandidates.value.length === 0) {
    pendingAction.value = 'cookie-discover'
    try {
      const discovered = await api.discoverCookieAccounts({
        label: '',
        cookies: cookieText.value,
        cdp: '',
        ...accountEnvironment,
      })
      cookieCandidates.value = discovered
      selectedCookieUsers.value = importableCookieUsers.value
    } catch (error) {
      actionError(error)
    } finally {
      pendingAction.value = ''
    }
    return
  }
  pendingAction.value = 'cookie-import'
  let lastAccountID = ''
  try {
    const chosen = new Set(selectedCookieUsers.value)
    const targets = cookieCandidates.value.filter((item) => chosen.has(item.auth_user))
    for (const target of targets) {
      const { account } = await api.importCookieAccount({
        label: '',
        cookies: cookieText.value,
        cdp: '',
        auth_user: target.auth_user,
        ...accountEnvironment,
      })
      target.imported = true
      selectedCookieUsers.value = selectedCookieUsers.value.filter(
        (user) => user !== target.auth_user,
      )
      lastAccountID = account.id
    }
    const count = targets.length
    pendingAction.value = ''
    closeCookieImport()
    emit('refresh')
    if (count === 1) {
      emit('notice', t('accounts.importedAccount').replace('{email}', lastAccountID), 'success')
    } else {
      emit('notice', t('accounts.importComplete').replace('{count}', String(count)), 'success')
    }
  } catch (error) {
    if (lastAccountID !== '') emit('refresh')
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

// toggleAccount 切换账户是否参与请求
async function toggleAccount(account: Account): Promise<void> {
  toggling.add(account.id)
  try {
    await api.updateAccount(account.id, {
      label: account.label,
      enabled: !account.enabled,
      proxy: account.proxy,
      locale: account.locale,
      timezone: account.timezone,
    })
    emit('refresh')
  } catch (error) {
    actionError(error)
  } finally {
    toggling.delete(account.id)
  }
}

// loginAccount 打开浏览器重新登录账户
async function loginAccount(account: Account): Promise<void> {
  pendingAction.value = `login:${account.id}`
  const done = await runBrowserLogin((view, signal) => api.loginAccount(account.id, view, signal))
  pendingAction.value = ''
  if (!done) return
  emit('refresh')
  emit('notice', t('accounts.loginComplete'), 'success')
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

// refreshAllAccounts 按 refreshConcurrency 的并发依次刷新全部启用账户，结束后提示成功与失败的数量
async function refreshAllAccounts(): Promise<void> {
  const targets = props.accounts.filter((account) => account.enabled && !refreshing.has(account.id))
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
  <section class="flex min-h-0 flex-1 flex-col overflow-auto md:overflow-hidden">
    <header
      class="flex flex-wrap items-center gap-3 border-b border-line px-4 py-4 md:px-[max(2rem,calc((100%_-_var(--container-6xl))/2_+_2rem))]"
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
          @click="refreshAllAccounts"
        >
          <span
            v-if="refreshTotal > 0"
            class="absolute inset-y-0 left-0 -z-10 bg-blue-500/20 transition-[width] duration-300"
            :style="{ width: `${(refreshDone / refreshTotal) * 100}%` }"
            aria-hidden="true"
          ></span>
          <UiIcon :name="refreshTotal > 0 ? 'spinner' : 'refresh'" :size="15" />
          <span v-if="refreshTotal > 0" class="tabular-nums">{{
            t('accounts.refreshingAll')
              .replace('{done}', String(refreshDone))
              .replace('{total}', String(refreshTotal))
          }}</span>
          <template v-else>{{ t('accounts.refreshAll') }}</template>
        </button>
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
              class="z-[60] w-72 max-w-[calc(100vw-2rem)] overflow-hidden rounded-lg border border-line bg-panel p-1 shadow-xl"
            >
              <DropdownMenuItem
                v-for="item in addMethods"
                :key="item.label"
                class="flex cursor-pointer items-start gap-3 rounded-md px-3 py-2.5 text-left outline-none data-[highlighted]:bg-raised"
                @select="item.action"
              >
                <UiIcon :name="item.icon" :size="16" class="mt-0.5 shrink-0 text-gray-400" />
                <span class="min-w-0">
                  <span class="block text-sm text-gray-200">{{ t(item.label) }}</span>
                  <span class="block text-xs text-gray-500">{{ t(item.help) }}</span>
                </span>
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenuPortal>
        </DropdownMenuRoot>
      </div>
    </header>
    <div class="mx-auto flex min-h-0 w-full max-w-6xl flex-1 flex-col p-4 md:p-8">
      <div class="mb-3 flex flex-wrap items-center gap-2">
        <input
          v-model="search"
          type="search"
          :aria-label="t('accounts.search')"
          :placeholder="t('accounts.search')"
          class="min-w-0 flex-[1_1_12rem] rounded-md border border-line bg-canvas px-3 py-1.5 text-sm text-white focus:border-blue-500 focus:outline-none"
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
      <div v-if="error" class="rounded border border-red-500/40 bg-red-500/10 p-4 text-red-300">
        {{ error }}
      </div>
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
        class="min-h-0 flex-1 overflow-y-auto rounded-lg border border-line bg-panel max-md:max-h-[70vh]"
      >
        <div
          v-for="account in visibleAccounts"
          :key="account.id"
          class="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-line/60 px-4 py-2.5"
          :class="{ 'opacity-60': !account.enabled }"
        >
          <div class="flex min-w-0 flex-1 basis-72 items-center gap-3">
            <div
              class="h-2.5 w-2.5 shrink-0 rounded-full"
              :class="{
                'bg-green-500': account.state === 'ready',
                'bg-blue-500': account.state === 'busy',
                'bg-yellow-500': account.state === 'cooldown',
                'bg-red-500': account.state === 'auth_required' || account.state === 'unavailable',
                'bg-gray-500': account.state === 'disabled',
              }"
            ></div>
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
          <div class="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-1 md:w-36">
            <span
              class="text-xs font-medium uppercase"
              :class="{
                'text-green-400': account.state === 'ready',
                'text-blue-400': account.state === 'busy',
                'text-yellow-400': account.state === 'cooldown',
                'text-red-400':
                  account.state === 'auth_required' || account.state === 'unavailable',
                'text-gray-500': account.state === 'disabled',
              }"
            >
              {{ t(stateKeys[account.state]) }}
            </span>
            <button
              v-if="account.message"
              type="button"
              class="rounded border border-red-900/50 bg-red-900/20 px-1.5 py-0.5 text-xs text-red-300 transition hover:bg-red-900/40"
              @click="
                reason = { account, kind: t(stateKeys[account.state]), text: account.message }
              "
            >
              {{ t('accounts.reason') }}
            </button>
            <button
              v-else-if="refreshErrors.has(account.id)"
              type="button"
              class="rounded border border-red-900/50 bg-red-900/20 px-1.5 py-0.5 text-xs whitespace-nowrap text-red-300 transition hover:bg-red-900/40"
              @click="
                reason = {
                  account,
                  kind: t('accounts.refreshFailed'),
                  text: refreshErrors.get(account.id) ?? '',
                }
              "
            >
              {{ t('accounts.refreshFailed') }}
            </button>
          </div>
          <div class="ml-auto flex max-w-full flex-wrap items-center justify-end gap-2">
            <button
              v-if="account.state === 'auth_required'"
              class="rounded bg-green-600 px-3 py-1 text-xs text-white transition hover:bg-green-500 disabled:opacity-50"
              type="button"
              :disabled="pendingAction !== '' || refreshing.has(account.id) || !account.enabled"
              :aria-busy="pendingAction === `login:${account.id}`"
              @click="loginAccount(account)"
            >
              {{ t('common.relogin') }}
            </button>
            <button
              class="relative rounded border border-line bg-raised px-3 py-1 text-xs text-gray-300 transition hover:bg-line disabled:opacity-50"
              type="button"
              :disabled="
                refreshing.has(account.id) ||
                queued.has(account.id) ||
                pendingAction === `login:${account.id}` ||
                !account.enabled
              "
              :aria-busy="refreshing.has(account.id)"
              @click="refreshAccount(account)"
            >
              <span class="grid" :class="{ 'opacity-0': refreshing.has(account.id) }">
                <span class="[grid-area:1/1]" :class="{ invisible: queued.has(account.id) }">{{
                  t('accounts.refresh')
                }}</span>
                <span class="[grid-area:1/1]" :class="{ invisible: !queued.has(account.id) }">{{
                  t('accounts.queued')
                }}</span>
              </span>
              <UiIcon
                v-if="refreshing.has(account.id)"
                name="spinner"
                :size="14"
                class="absolute inset-0 m-auto"
              />
            </button>
            <DropdownMenuRoot :modal="false">
              <DropdownMenuTrigger
                v-tooltip="t('pg.moreOptions')"
                class="flex size-[26px] items-center justify-center rounded border border-line bg-raised text-gray-400 transition hover:bg-line hover:text-gray-200 disabled:opacity-50"
                :disabled="
                  pendingAction !== '' || refreshing.has(account.id) || toggling.has(account.id)
                "
                :aria-label="t('pg.moreOptions')"
                :aria-busy="toggling.has(account.id)"
              >
                <UiIcon :name="toggling.has(account.id) ? 'spinner' : 'more'" :size="15" />
              </DropdownMenuTrigger>
              <DropdownMenuPortal>
                <DropdownMenuContent
                  align="end"
                  :side-offset="4"
                  class="z-[60] min-w-40 overflow-hidden rounded-lg border border-line bg-panel p-1 shadow-xl"
                >
                  <DropdownMenuItem
                    class="flex cursor-pointer items-center gap-2.5 rounded-md px-3 py-2 text-sm outline-none data-[highlighted]:bg-raised text-gray-200"
                    @select="beginEdit(account)"
                  >
                    <UiIcon name="edit" :size="15" />
                    {{ t('common.edit') }}
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    class="flex cursor-pointer items-center gap-2.5 rounded-md px-3 py-2 text-sm outline-none data-[highlighted]:bg-raised text-gray-200"
                    @select="toggleAccount(account)"
                  >
                    <UiIcon :name="account.enabled ? 'disable' : 'success'" :size="15" />
                    {{ t(account.enabled ? 'common.disable' : 'common.enable') }}
                  </DropdownMenuItem>
                  <DropdownMenuSeparator class="my-1 h-px bg-line" />
                  <DropdownMenuItem
                    class="flex cursor-pointer items-center gap-2.5 rounded-md px-3 py-2 text-sm outline-none data-[highlighted]:bg-raised text-red-400 data-[highlighted]:bg-red-900/30"
                    @select="emit('delete', account)"
                  >
                    <UiIcon name="trash" :size="15" />
                    {{ t('common.delete') }}
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenuPortal>
            </DropdownMenuRoot>
          </div>
        </div>
      </div>

      <DetailDialog
        :open="reason !== null"
        :title="reason?.account.label ?? ''"
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
        v-if="liveLogin"
        :key="liveLogin.view"
        :view="liveLogin.view"
        :error="liveLogin.error"
        @active="showLiveLogin"
        @unavailable="liveLoginUnavailable"
        @cancel="liveLogin.controller.abort()"
        @close="liveLogin = null"
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
                <UiIcon name="close" :size="16" />
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
                <UiIcon name="close" :size="16" />
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
                <UiIcon name="close" :size="16" />
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
            @submit.prevent="importCookieAccount"
          >
            <div class="mb-4 flex items-center justify-between">
              <h3 class="text-lg font-bold text-white">{{ t('accounts.pasteCookie') }}</h3>
              <button
                class="rounded p-1 text-gray-500 hover:bg-line hover:text-white disabled:opacity-50"
                type="button"
                :disabled="pendingAction === 'cookie-import' || pendingAction === 'cookie-discover'"
                :aria-label="t('common.close')"
                @click="closeCookieImport"
              >
                <UiIcon name="close" :size="16" />
              </button>
            </div>
            <ol class="mb-5 list-decimal space-y-1 pl-5 text-sm leading-6 text-gray-300">
              <li v-for="step in pasteSteps" :key="step">{{ t(step) }}</li>
            </ol>
            <div class="space-y-4">
              <label class="block">
                <span class="mb-1 block text-sm font-medium text-gray-300">{{
                  t('accounts.cookies')
                }}</span>
                <textarea
                  v-model="cookieText"
                  class="h-36 w-full resize-y rounded border border-line bg-canvas px-3 py-2 font-mono text-xs text-white transition focus:border-blue-500 focus:outline-none"
                  spellcheck="false"
                  autocomplete="off"
                  :placeholder="t('accounts.cookiePlaceholder')"
                  :readonly="pendingAction !== ''"
                  required
                  @input="resetCookieDiscovery"
                />
              </label>

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
                    :key="candidate.auth_user"
                    class="flex items-center gap-3 rounded border border-line bg-canvas p-3 transition"
                    :class="
                      candidate.imported ? 'opacity-60' : 'cursor-pointer hover:border-gray-600'
                    "
                  >
                    <input
                      v-model="selectedCookieUsers"
                      class="h-4 w-4 shrink-0 accent-blue-600"
                      type="checkbox"
                      :value="candidate.auth_user"
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
                        u/{{ candidate.auth_user }}
                      </span>
                    </span>
                    <span v-if="candidate.imported" class="shrink-0 text-xs text-gray-400">{{
                      t('accounts.alreadyImported')
                    }}</span>
                  </label>
                </div>
              </div>

              <details class="group rounded border border-line">
                <summary
                  class="flex cursor-pointer list-none items-center gap-2 px-3 py-2 text-sm text-gray-400 hover:text-gray-200"
                >
                  <UiIcon
                    name="chevronRight"
                    :size="14"
                    class="transition-transform group-open:rotate-90"
                  />
                  {{ t('accounts.moreSettings') }}
                </summary>
                <div class="space-y-4 border-t border-line p-3">
                  <AccountEnvironmentFields :model-value="accountEnvironment" />
                </div>
              </details>
            </div>
            <div class="mt-6 flex gap-2">
              <button
                class="flex-1 rounded bg-raised py-2 text-sm text-gray-300 transition hover:bg-line disabled:opacity-50"
                type="button"
                :disabled="pendingAction === 'cookie-import' || pendingAction === 'cookie-discover'"
                @click="closeCookieImport"
              >
                {{ t('common.cancel') }}
              </button>
              <button
                class="flex-1 rounded bg-blue-600 py-2 text-sm text-white transition hover:bg-blue-500 disabled:opacity-50"
                type="submit"
                :disabled="
                  pendingAction !== '' ||
                  (cookieCandidates.length > 0 && selectedCookieUsers.length === 0)
                "
                :aria-busy="
                  pendingAction === 'cookie-import' || pendingAction === 'cookie-discover'
                "
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
                <UiIcon name="close" :size="16" />
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
                </span>
                <span v-if="browser.imported" class="shrink-0 text-xs text-gray-400">{{
                  t('accounts.alreadyImported')
                }}</span>
              </label>
            </div>
            <details class="group mt-4 rounded border border-line">
              <summary
                class="flex cursor-pointer list-none items-center gap-2 px-3 py-2 text-sm text-gray-300 hover:text-white"
              >
                <UiIcon
                  name="chevronRight"
                  :size="14"
                  class="transition-transform group-open:rotate-90"
                />
                {{ t('accounts.moreSettings') }}
              </summary>
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
            </details>
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
        <div v-if="pairing" class="modal-overlay" @click.self="pairing = null">
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
                @click="pairing = null"
              >
                <UiIcon name="close" :size="16" />
              </button>
            </div>
            <ol class="mb-4 list-decimal space-y-1 pl-5 text-sm leading-6 text-gray-400">
              <li v-for="step in remoteSteps" :key="step">
                <template v-for="(part, position) in t(step).split('`')" :key="position">
                  <code
                    v-if="position % 2"
                    class="font-mono text-xs whitespace-nowrap text-gray-300"
                    >{{ part }}</code
                  >
                  <template v-else>{{ part }}</template>
                </template>
              </li>
            </ol>
            <p
              v-if="pairingInsecure"
              class="mb-4 flex gap-2 rounded border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs leading-5 text-amber-200"
            >
              <UiIcon name="warning" :size="14" class="mt-0.5 shrink-0" />
              {{ t('accounts.remoteHttps') }}
            </p>
            <div
              v-for="item in [
                {
                  kind: 'command' as const,
                  label: t('accounts.remoteCommand'),
                  text: pairingCommand,
                },
                { kind: 'token' as const, label: t('accounts.remoteToken'), text: pairing.token },
              ]"
              :key="item.kind"
              class="mb-3"
            >
              <span class="mb-1 block text-xs font-medium text-gray-500">{{ item.label }}</span>
              <div
                class="flex items-center gap-2 rounded border border-line bg-canvas py-1.5 pr-1.5 pl-3"
              >
                <code
                  class="min-w-0 flex-1 overflow-x-auto font-mono text-sm whitespace-nowrap text-gray-200 select-all"
                  >{{ item.text }}</code
                >
                <button
                  v-if="clipboardAvailable"
                  class="flex shrink-0 items-center gap-1 rounded px-2 py-1 text-xs text-gray-400 transition hover:bg-raised hover:text-white"
                  type="button"
                  @click="copyPairing(item.kind, item.text)"
                >
                  <UiIcon :name="copiedPairing === item.kind ? 'check' : 'copy'" :size="13" />
                  {{ copiedPairing === item.kind ? t('common.copied') : t('common.copy') }}
                </button>
              </div>
            </div>
            <p class="text-xs text-gray-500">
              {{
                t('accounts.remoteExpires').replace(
                  '{time}',
                  new Date(pairing.expires_at).toLocaleTimeString(locale, { hour12: false }),
                )
              }}
            </p>
          </div>
        </div>
      </Teleport>
    </div>
  </section>
</template>
