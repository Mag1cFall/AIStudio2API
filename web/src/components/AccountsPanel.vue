<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { api } from '@/api'
import { useI18n, type TranslationKey } from '@/i18n'
import { maskedProxy } from '@/proxy'
import type {
  Account,
  AccountDraft,
  AccountRefresh,
  AccountState,
  ChromeImportProfile,
  PairingToken,
} from '@/types'
import DetailDialog from './DetailDialog.vue'
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
const editingAccountID = ref('')
const pendingAction = ref('')
const refreshing = reactive(new Set<string>())
const refreshingAll = ref(false)
const chromeProfiles = ref<ChromeImportProfile[]>([])
const pairing = ref<PairingToken | null>(null)
const copiedPairing = ref('')
const pairingCommand = `${navigator.userAgent.includes('Windows') ? 'start.bat' : './aistudio2api'} setup --remote ${window.location.origin}`
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

// beginBrowserLogin 启动浏览器登录并使用返回身份创建账户
async function beginBrowserLogin(): Promise<void> {
  pendingAction.value = 'browser-login'
  try {
    await api.createAccount({ ...accountEnvironment })
    showBrowserLogin.value = false
    emit('refresh')
    emit('notice', t('accounts.loginComplete'), 'success')
  } catch (error) {
    actionError(error)
  } finally {
    pendingAction.value = ''
  }
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
  try {
    await api.loginAccount(account.id)
    emit('refresh')
    emit('notice', t('accounts.loginComplete'), 'success')
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
      <div class="min-w-0 flex-1">
        <h2 class="text-xl font-semibold text-white">{{ t('section.accounts.title') }}</h2>
        <p class="mt-1 text-sm text-gray-500">{{ t('section.accounts.description') }}</p>
      </div>
      <div class="flex flex-wrap justify-end gap-2">
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
        <button
          class="flex items-center gap-2 rounded border border-line bg-raised px-4 py-2 text-sm font-medium text-gray-200 transition hover:bg-line disabled:opacity-50"
          type="button"
          :disabled="pendingAction !== ''"
          :aria-busy="pendingAction === 'pairing'"
          @click="openPairing"
        >
          <UiIcon name="remote" :size="15" />
          {{ t('accounts.remoteAdd') }}
        </button>
        <button
          class="flex items-center gap-2 rounded border border-line bg-raised px-4 py-2 text-sm font-medium text-gray-200 transition hover:bg-line disabled:opacity-50"
          type="button"
          :disabled="pendingAction !== ''"
          :aria-busy="pendingAction === 'chrome-discover'"
          @click="openChromeImport"
        >
          <UiIcon name="accounts" :size="15" />
          {{ t('accounts.importChrome') }}
        </button>
        <button
          class="flex items-center gap-2 rounded bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-500 disabled:opacity-50"
          type="button"
          :disabled="pendingAction !== ''"
          @click="openBrowserLogin"
        >
          <UiIcon name="login" :size="15" />
          {{ t('accounts.browserLogin') }}
        </button>
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
          <span class="w-16 shrink-0 text-xs text-gray-500"
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
              class="rounded border border-red-900/50 bg-red-900/20 px-1.5 py-0.5 text-[11px] text-red-300 transition hover:bg-red-900/40"
              @click="messageAccount = account"
            >
              {{ t('accounts.reason') }}
            </button>
          </div>
          <div class="flex shrink-0 flex-wrap justify-end gap-2">
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
            <p class="mb-4 text-sm leading-6 text-gray-400">{{ t('accounts.remoteHelp') }}</p>
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
