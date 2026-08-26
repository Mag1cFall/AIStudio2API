<script setup lang="ts">
import { reactive, ref } from 'vue'
import { api } from '@/api'
import { useI18n, type TranslationKey } from '@/i18n'
import type { Account, AccountDraft, AccountState } from '@/types'
import UiIcon from './UiIcon.vue'

defineProps<{
  accounts: Account[]
  loading: boolean
  error: string
}>()

const emit = defineEmits<{
  refresh: []
  notice: [message: string, tone: 'success' | 'error']
}>()

const { t } = useI18n()
const defaultAccountLocale = navigator.language || 'en-US'
const defaultAccountTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
const showAdd = ref(false)
const editingAccountID = ref('')
const pendingAction = ref('')
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

// actionError 将账户写操作错误发送到全局通知
function actionError(error: unknown): void {
  emit('notice', error instanceof Error ? error.message : t('common.error'), 'error')
}

function resetDraft(): void {
  draft.label = ''
  draft.enabled = true
  draft.proxy = ''
  draft.locale = defaultAccountLocale
  draft.timezone = defaultAccountTimezone
}

function beginAdd(): void {
  editingAccountID.value = ''
  resetDraft()
  showAdd.value = true
}

function beginEdit(account: Account): void {
  editingAccountID.value = account.id
  draft.label = account.label
  draft.enabled = account.enabled
  draft.proxy = account.proxy
  draft.locale = account.locale
  draft.timezone = account.timezone
  showAdd.value = true
}

function closeEditor(): void {
  showAdd.value = false
  editingAccountID.value = ''
}

// saveAccount 保存账户并刷新产品数据
async function saveAccount(): Promise<void> {
  pendingAction.value = editingAccountID.value === '' ? 'create' : `edit:${editingAccountID.value}`
  try {
    if (editingAccountID.value === '') {
      await api.createAccount({ ...draft })
    } else {
      await api.updateAccount(editingAccountID.value, { ...draft })
    }
    closeEditor()
    emit('refresh')
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

// runAccountAction 执行登录或会话验证
async function runAccountAction(account: Account, action: 'login' | 'verify'): Promise<void> {
  pendingAction.value = `${action}:${account.id}`
  try {
    if (action === 'login') {
      await api.loginAccount(account.id)
    } else {
      await api.verifyAccount(account.id)
    }
    emit('refresh')
  } catch (error) {
    actionError(error)
  } finally {
    pendingAction.value = ''
  }
}

// removeAccount 删除用户确认的账户
async function removeAccount(account: Account): Promise<void> {
  if (!window.confirm(t('accounts.deleteConfirm'))) return
  pendingAction.value = `delete:${account.id}`
  try {
    await api.deleteAccount(account.id)
    emit('refresh')
  } catch (error) {
    actionError(error)
  } finally {
    pendingAction.value = ''
  }
}
</script>

<template>
  <section class="mx-auto w-full max-w-4xl flex-1 overflow-auto p-4 md:p-8">
    <div class="mb-6 flex items-center justify-between border-b border-[#30363d] pb-2">
      <h2 class="text-2xl font-bold text-white">{{ t('section.accounts.title') }}</h2>
      <button
        class="flex items-center gap-2 rounded bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-500"
        type="button"
        @click="beginAdd"
      >
        <UiIcon name="info" :size="15" />
        {{ t('common.add') }}
      </button>
    </div>

    <div v-if="error" class="rounded border border-red-500/40 bg-red-500/10 p-4 text-red-300">
      {{ error }}
    </div>
    <div v-else-if="loading" class="py-12 text-center text-gray-500">
      {{ t('common.loading') }}
    </div>
    <div
      v-else-if="accounts.length === 0"
      class="rounded border border-[#30363d] bg-[#161b22] py-10 text-center text-gray-500"
    >
      <p class="mb-2">{{ t('accounts.empty') }}</p>
      <p class="text-xs">{{ t('accounts.emptyHint') }}</p>
    </div>
    <div v-else class="space-y-3">
      <article
        v-for="account in accounts"
        :key="account.id"
        class="rounded-lg border border-[#30363d] bg-[#161b22] p-4"
        :class="{ 'opacity-60': !account.enabled }"
      >
        <div class="mb-2 flex items-center justify-between gap-4">
          <div class="flex min-w-0 items-center gap-3">
            <div
              class="h-3 w-3 shrink-0 rounded-full"
              :class="{
                'bg-green-500': account.state === 'ready',
                'bg-blue-500': account.state === 'busy',
                'bg-yellow-500': account.state === 'cooldown',
                'bg-red-500': account.state === 'auth_required' || account.state === 'unavailable',
                'bg-gray-500': account.state === 'disabled',
              }"
            ></div>
            <div class="min-w-0">
              <strong class="block truncate font-mono text-white">{{ account.label }}</strong>
            </div>
          </div>
          <div class="shrink-0 text-xs text-gray-500">
            {{ t('accounts.models') }}:
            {{ account.models.length === 0 ? '—' : account.models.length }}
          </div>
        </div>

        <div
          class="grid grid-cols-1 gap-2 border-t border-[#30363d] pt-3 text-xs sm:grid-cols-2 md:grid-cols-4"
        >
          <div>
            <span class="text-gray-500">{{ t('accounts.proxy') }}</span>
            <div class="truncate text-gray-300">{{ account.proxy || t('accounts.direct') }}</div>
          </div>
          <div>
            <span class="text-gray-500">{{ t('accounts.locale') }}</span>
            <div class="text-gray-300">{{ account.locale }}</div>
          </div>
          <div>
            <span class="text-gray-500">{{ t('accounts.timezone') }}</span>
            <div class="text-gray-300">{{ account.timezone }}</div>
          </div>
          <div>
            <span class="text-gray-500">{{ t('accounts.benefitTier') }}</span>
            <div class="text-gray-300">{{ account.benefit_tier }}</div>
          </div>
        </div>

        <p v-if="account.message" class="mt-3 break-words text-xs text-red-400">
          {{ account.message }}
        </p>

        <div class="mt-3 flex items-center justify-between gap-3 border-t border-[#30363d] pt-3">
          <span
            class="text-xs font-medium uppercase"
            :class="{
              'text-green-400': account.state === 'ready',
              'text-blue-400': account.state === 'busy',
              'text-yellow-400': account.state === 'cooldown',
              'text-red-400': account.state === 'auth_required' || account.state === 'unavailable',
              'text-gray-500': account.state === 'disabled',
            }"
          >
            {{ t(stateKeys[account.state]) }}
          </span>
          <div class="flex flex-wrap justify-end gap-2">
            <button
              class="rounded border border-[#30363d] bg-[#21262d] px-3 py-1 text-xs text-gray-300 transition hover:bg-[#30363d] disabled:opacity-50"
              type="button"
              :disabled="pendingAction !== ''"
              @click="beginEdit(account)"
            >
              {{ t('common.edit') }}
            </button>
            <button
              class="rounded border border-[#30363d] bg-[#21262d] px-3 py-1 text-xs text-gray-300 transition hover:bg-[#30363d] disabled:opacity-50"
              type="button"
              :disabled="pendingAction !== ''"
              @click="toggleAccount(account)"
            >
              {{ t(account.enabled ? 'common.disable' : 'common.enable') }}
            </button>
            <button
              v-if="account.state === 'auth_required'"
              class="rounded bg-green-600 px-3 py-1 text-xs text-white transition hover:bg-green-500 disabled:opacity-50"
              type="button"
              :disabled="pendingAction !== '' || !account.enabled"
              @click="runAccountAction(account, 'login')"
            >
              {{ t('common.relogin') }}
            </button>
            <button
              class="rounded border border-[#30363d] bg-[#21262d] px-3 py-1 text-xs text-gray-300 transition hover:bg-[#30363d] disabled:opacity-50"
              type="button"
              :disabled="pendingAction !== '' || !account.enabled"
              @click="runAccountAction(account, 'verify')"
            >
              {{ t('common.verify') }}
            </button>
            <button
              class="rounded border border-red-900/50 bg-red-900/30 px-3 py-1 text-xs text-red-400 transition hover:bg-red-900/50 disabled:opacity-50"
              type="button"
              :disabled="pendingAction !== ''"
              @click="removeAccount(account)"
            >
              {{ t('common.delete') }}
            </button>
          </div>
        </div>
      </article>
    </div>

    <Teleport to="body">
      <div v-if="showAdd" class="modal-overlay" @click.self="closeEditor">
        <form
          class="mx-4 w-full max-w-md rounded-lg border border-[#30363d] bg-[#161b22] p-6 shadow-xl"
          @submit.prevent="saveAccount"
        >
          <div class="mb-4 flex items-center justify-between">
            <h3 class="text-lg font-bold text-white">
              {{ t(editingAccountID === '' ? 'accounts.addTitle' : 'accounts.editTitle') }}
            </h3>
            <button
              class="rounded p-1 text-gray-500 hover:bg-[#30363d] hover:text-white"
              type="button"
              :aria-label="t('common.close')"
              @click="closeEditor"
            >
              <UiIcon name="close" :size="16" />
            </button>
          </div>
          <div class="space-y-4">
            <label v-if="editingAccountID !== ''" class="flex items-center justify-between">
              <span class="text-sm font-medium text-gray-400">{{ t('common.enable') }}</span>
              <input v-model="draft.enabled" class="h-4 w-4 accent-blue-600" type="checkbox" />
            </label>
            <label class="block">
              <span class="mb-1 block text-sm font-medium text-gray-400">{{
                t('accounts.label')
              }}</span>
              <input
                v-model.trim="draft.label"
                class="w-full rounded border border-[#30363d] bg-[#0d1117] px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
                :disabled="editingAccountID !== ''"
                type="email"
                required
                autocomplete="email"
              />
            </label>
            <label class="block">
              <span class="mb-1 block text-sm font-medium text-gray-400">{{
                t('accounts.proxy')
              }}</span>
              <input
                v-model.trim="draft.proxy"
                class="w-full rounded border border-[#30363d] bg-[#0d1117] px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
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
                  class="w-full rounded border border-[#30363d] bg-[#0d1117] px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
                  required
                />
              </label>
              <label class="block">
                <span class="mb-1 block text-sm font-medium text-gray-400">{{
                  t('accounts.timezone')
                }}</span>
                <input
                  v-model.trim="draft.timezone"
                  class="w-full rounded border border-[#30363d] bg-[#0d1117] px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
                  required
                />
              </label>
            </div>
          </div>
          <div class="mt-6 flex gap-2">
            <button
              class="flex-1 rounded bg-[#21262d] py-2 text-sm text-gray-300 transition hover:bg-[#30363d]"
              type="button"
              @click="closeEditor"
            >
              {{ t('common.cancel') }}
            </button>
            <button
              class="flex-1 rounded bg-blue-600 py-2 text-sm text-white transition hover:bg-blue-500 disabled:opacity-50"
              type="submit"
              :disabled="pendingAction !== ''"
            >
              <template v-if="editingAccountID === ''">{{ t('accounts.startLogin') }}</template>
              <template v-else>{{ t('common.save') }}</template>
            </button>
          </div>
        </form>
      </div>
    </Teleport>
  </section>
</template>
