<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
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
import DetailDialog from './DetailDialog.vue'
import LoginViewer from './LoginViewer.vue'
import UiIcon from './UiIcon.vue'

const props = defineProps<{
  accounts: Account[]
  loading: boolean
  error: string
}>()

const emit = defineEmits<{
  refresh: []
  notice: [message: string, tone: 'success' | 'error']
  delete: [account: Account]
}>()

const { t, locale, errorText } = useI18n()
const defaultAccountLocale = navigator.language || 'en-US'
const defaultAccountTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
const showEditor = ref(false)
const showBrowserLogin = ref(false)
const showChromeImport = ref(false)
const showCookieImport = ref(false)
const cookieText = ref('')
const showBrowserImport = ref(false)
// cookieBrowsers 是本机找到的调试浏览器与其中登录 AI Studio 的账户
const cookieBrowsers = ref<CookieBrowser[]>([])
const scanningBrowsers = ref(false)
const selectedBrowsers = ref<string[]>([])
const editingAccountID = ref('')
const pendingAction = ref('')
const refreshing = reactive(new Set<string>())
const refreshingAll = ref(false)
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

// messageAccount 是悬窗中显示完整状态原因的账户
const messageAccount = ref<Account | null>(null)

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

// newViewID 生成登录窗口 ID，非 HTTPS 页面中同样可用
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
  if (liveLogin.value === null) return
  liveLogin.value.active = true
  showBrowserLogin.value = false
}

// liveLoginUnavailable 在登录窗口无法连接时取消登录
function liveLoginUnavailable(): void {
  liveLogin.value?.controller.abort()
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
  if (pendingAction.value === 'cookie-import') return
  showCookieImport.value = false
  cookieText.value = ''
}

// pasteSteps 是粘贴 Cookie 的操作步骤
const pasteSteps: TranslationKey[] = ['accounts.pasteStep1', 'accounts.pasteStep2']

// openBrowserImport 打开指纹浏览器导入并查找本机调试浏览器
function openBrowserImport(): void {
  showBrowserImport.value = true
  void scanBrowsers()
}

// browserSummary 返回浏览器条目第二行：未登录提示、读取错误或浏览器名、地址、语言与时区
function browserSummary(browser: CookieBrowser): string {
  if (browser.signed_out) return t('accounts.browserSignedOut')
  return (
    browser.error ||
    [browser.browser, browser.address, browser.locale, browser.timezone].filter(Boolean).join(' · ')
  )
}

// scanBrowsers 查找本机调试浏览器，默认勾选可导入的账户
async function scanBrowsers(): Promise<void> {
  scanningBrowsers.value = true
  try {
    cookieBrowsers.value = await api.cookieBrowsers()
    selectedBrowsers.value = cookieBrowsers.value
      .filter((browser) => browser.email !== '' && !browser.imported && browser.error === '')
      .map((browser) => browser.address)
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
    for (const address of selectedBrowsers.value) {
      await api.importCookieAccount({
        label: '',
        cookies: '',
        cdp: address,
        proxy: accountEnvironment.proxy,
        locale: '',
        timezone: '',
      })
    }
    const count = selectedBrowsers.value.length
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

// importCookieAccount 导入粘贴的 Cookie，账户由服务按 Cookie 登录的 AI Studio 识别
async function importCookieAccount(): Promise<void> {
  pendingAction.value = 'cookie-import'
  try {
    const { account } = await api.importCookieAccount({
      label: '',
      cookies: cookieText.value,
      cdp: '',
      ...accountEnvironment,
    })
    pendingAction.value = ''
    closeCookieImport()
    emit('refresh')
    emit('notice', t('accounts.importedAccount').replace('{email}', account.id), 'success')
  } catch (error) {
    actionError(error)
  } finally {
    pendingAction.value = ''
  }
}

// saveAccount 保存已有账户配置并刷新产品数据
async function saveAccount(): Promise<void> {
  if (editingAccountID.value === '') return
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
  pendingAction.value = `toggle:${account.id}`
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
    pendingAction.value = ''
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

// refreshAccount 立即读取账户权益与模型目录，执行期间只禁用该账户的按钮
async function refreshAccount(account: Account): Promise<void> {
  refreshing.add(account.id)
  try {
    const result = await api.refreshAccount(account.id)
    emit('refresh')
    emit('notice', refreshNotice(result), 'success')
  } catch (error) {
    actionError(error)
  } finally {
    refreshing.delete(account.id)
  }
}

// refreshAllAccounts 并发刷新全部启用账户，结束后提示成功与失败的数量
async function refreshAllAccounts(): Promise<void> {
  const targets = props.accounts.filter((account) => account.enabled && !refreshing.has(account.id))
  refreshingAll.value = true
  targets.forEach((account) => refreshing.add(account.id))
  const results = await Promise.allSettled(targets.map((account) => api.refreshAccount(account.id)))
  targets.forEach((account) => refreshing.delete(account.id))
  refreshingAll.value = false
  emit('refresh')
  const failed = results.filter((result) => result.status === 'rejected').length
  emit(
    'notice',
    t('accounts.refreshedAll')
      .replace('{count}', String(targets.length - failed))
      .replace('{failed}', String(failed)),
    failed === 0 ? 'success' : 'error',
  )
}
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col overflow-auto md:overflow-hidden">
    <header class="flex flex-wrap items-center gap-3 border-b border-line px-4 py-4 md:px-8">
      <div class="min-w-0 flex-[1_1_16rem]">
        <h2 class="text-xl font-semibold text-white">{{ t('section.accounts.title') }}</h2>
        <p class="mt-1 text-sm text-gray-500">{{ t('section.accounts.description') }}</p>
      </div>
      <div class="ml-auto flex flex-wrap justify-end gap-2">
        <button
          class="flex items-center gap-2 rounded border border-line bg-raised px-4 py-2 text-sm font-medium text-gray-200 transition hover:bg-line disabled:opacity-50"
          type="button"
          :disabled="refreshingAll"
          :aria-busy="refreshingAll"
          @click="refreshAllAccounts"
        >
          <UiIcon name="refresh" :size="15" />
          {{ t('accounts.refreshAll') }}
        </button>
        <DropdownMenuRoot :modal="false">
          <DropdownMenuTrigger
            class="flex items-center gap-2 rounded bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-500 disabled:opacity-50"
            :disabled="pendingAction !== ''"
            :aria-busy="pendingAction === 'pairing' || pendingAction === 'chrome-discover'"
          >
            <UiIcon name="add" :size="15" />
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
        v-else
        class="min-h-0 flex-1 overflow-y-auto rounded-lg border border-line bg-panel max-md:max-h-[70vh]"
      >
        <div
          v-for="account in accounts"
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
                class="block truncate font-mono text-sm text-white"
                >{{ account.label }}</span
              >
              <span
                v-tooltip.overflow="accountSummary(account)"
                class="block truncate text-xs text-gray-500"
                >{{ accountSummary(account) }}</span
              >
            </div>
          </div>
          <span class="w-20 shrink-0 text-xs text-gray-500"
            >{{ t('accounts.models') }}
            {{ account.models.length === 0 ? '—' : account.models.length }}</span
          >
          <div class="flex w-36 shrink-0 items-center gap-2">
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
              @click="messageAccount = account"
            >
              {{ t('accounts.reason') }}
            </button>
          </div>
          <div class="flex max-w-full flex-wrap justify-end gap-2">
            <button
              class="rounded border border-line bg-raised px-3 py-1 text-xs text-gray-300 transition hover:bg-line disabled:opacity-50"
              type="button"
              :disabled="pendingAction !== '' || refreshing.has(account.id)"
              @click="beginEdit(account)"
            >
              {{ t('common.edit') }}
            </button>
            <button
              class="rounded border border-line bg-raised px-3 py-1 text-xs text-gray-300 transition hover:bg-line disabled:opacity-50"
              type="button"
              :disabled="pendingAction !== '' || refreshing.has(account.id)"
              :aria-busy="pendingAction === `toggle:${account.id}`"
              @click="toggleAccount(account)"
            >
              {{ t(account.enabled ? 'common.disable' : 'common.enable') }}
            </button>
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
              class="rounded border border-line bg-raised px-3 py-1 text-xs text-gray-300 transition hover:bg-line disabled:opacity-50"
              type="button"
              :disabled="
                refreshing.has(account.id) ||
                pendingAction === `login:${account.id}` ||
                !account.enabled
              "
              :aria-busy="refreshing.has(account.id)"
              @click="refreshAccount(account)"
            >
              {{ t('accounts.refresh') }}
            </button>
            <button
              class="rounded border border-red-900/50 bg-red-900/30 px-3 py-1 text-xs text-red-400 transition hover:bg-red-900/50 disabled:opacity-50"
              type="button"
              :disabled="pendingAction !== '' || refreshing.has(account.id)"
              @click="emit('delete', account)"
            >
              {{ t('common.delete') }}
            </button>
          </div>
        </div>
      </div>

      <DetailDialog
        :open="messageAccount !== null"
        :title="messageAccount?.label ?? ''"
        :description="messageAccount ? t(stateKeys[messageAccount.state]) : ''"
        :copy-text="messageAccount?.message"
        @close="messageAccount = null"
      >
        <pre
          v-if="messageAccount"
          class="rounded-md border border-line bg-canvas p-3 font-mono text-xs leading-5 whitespace-pre-wrap [overflow-wrap:anywhere] text-red-300"
          >{{ messageAccount.message }}</pre>
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
            <p
              v-tooltip.overflow="draft.label"
              class="mb-4 truncate font-mono text-sm text-gray-300"
            >
              {{ draft.label }}
            </p>
            <div class="space-y-4">
              <label class="flex items-center justify-between">
                <span class="text-sm font-medium text-gray-400">{{ t('common.enable') }}</span>
                <input v-model="draft.enabled" class="h-4 w-4 accent-blue-600" type="checkbox" />
              </label>
              <label class="block">
                <span class="mb-1 block text-sm font-medium text-gray-400">{{
                  t('accounts.proxy')
                }}</span>
                <input
                  v-model.trim="draft.proxy"
                  class="w-full rounded border border-line bg-canvas px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
                  placeholder="socks5://127.0.0.1:1080"
                />
              </label>
              <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <label class="block">
                  <span class="mb-1 block text-sm font-medium text-gray-400">{{
                    t('accounts.locale')
                  }}</span>
                  <input
                    v-model.trim="draft.locale"
                    class="w-full rounded border border-line bg-canvas px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
                    required
                  />
                </label>
                <label class="block">
                  <span class="mb-1 block text-sm font-medium text-gray-400">{{
                    t('accounts.timezone')
                  }}</span>
                  <input
                    v-model.trim="draft.timezone"
                    class="w-full rounded border border-line bg-canvas px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
                    required
                  />
                </label>
              </div>
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
              <label class="block">
                <span class="mb-1 block text-sm font-medium text-gray-400">{{
                  t('accounts.proxy')
                }}</span>
                <input
                  v-model.trim="accountEnvironment.proxy"
                  class="w-full rounded border border-line bg-canvas px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
                  placeholder="socks5://127.0.0.1:1080"
                />
              </label>
              <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <label class="block">
                  <span class="mb-1 block text-sm font-medium text-gray-400">{{
                    t('accounts.locale')
                  }}</span>
                  <input
                    v-model.trim="accountEnvironment.locale"
                    class="w-full rounded border border-line bg-canvas px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
                    required
                  />
                </label>
                <label class="block">
                  <span class="mb-1 block text-sm font-medium text-gray-400">{{
                    t('accounts.timezone')
                  }}</span>
                  <input
                    v-model.trim="accountEnvironment.timezone"
                    class="w-full rounded border border-line bg-canvas px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
                    required
                  />
                </label>
              </div>
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
              <label class="block">
                <span class="mb-1 block text-sm font-medium text-gray-400">{{
                  t('accounts.proxy')
                }}</span>
                <input
                  v-model.trim="accountEnvironment.proxy"
                  class="w-full rounded border border-line bg-canvas px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
                  placeholder="socks5://127.0.0.1:1080"
                />
              </label>
              <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <label class="block">
                  <span class="mb-1 block text-sm font-medium text-gray-400">{{
                    t('accounts.locale')
                  }}</span>
                  <input
                    v-model.trim="accountEnvironment.locale"
                    class="w-full rounded border border-line bg-canvas px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
                    required
                  />
                </label>
                <label class="block">
                  <span class="mb-1 block text-sm font-medium text-gray-400">{{
                    t('accounts.timezone')
                  }}</span>
                  <input
                    v-model.trim="accountEnvironment.timezone"
                    class="w-full rounded border border-line bg-canvas px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
                    required
                  />
                </label>
              </div>
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
                :disabled="pendingAction === 'cookie-import'"
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
                  required
                />
              </label>
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
                  <label class="block">
                    <span class="mb-1 block text-sm font-medium text-gray-400">{{
                      t('accounts.proxy')
                    }}</span>
                    <input
                      v-model.trim="accountEnvironment.proxy"
                      class="w-full rounded border border-line bg-canvas px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
                      placeholder="socks5://127.0.0.1:1080"
                    />
                  </label>
                  <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
                    <label class="block">
                      <span class="mb-1 block text-sm font-medium text-gray-400">{{
                        t('accounts.locale')
                      }}</span>
                      <input
                        v-model.trim="accountEnvironment.locale"
                        class="w-full rounded border border-line bg-canvas px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
                        required
                      />
                    </label>
                    <label class="block">
                      <span class="mb-1 block text-sm font-medium text-gray-400">{{
                        t('accounts.timezone')
                      }}</span>
                      <input
                        v-model.trim="accountEnvironment.timezone"
                        class="w-full rounded border border-line bg-canvas px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
                        required
                      />
                    </label>
                  </div>
                </div>
              </details>
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
                :disabled="pendingAction !== ''"
                :aria-busy="pendingAction === 'cookie-import'"
              >
                {{ t('accounts.import') }}
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
                :key="browser.address"
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
                  :value="browser.address"
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
