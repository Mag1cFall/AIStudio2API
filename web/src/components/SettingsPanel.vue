<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { api } from '@/api'
import { useI18n } from '@/i18n'
import type { ServiceConfig } from '@/types'
import UiIcon from './UiIcon.vue'

const props = defineProps<{
  config: ServiceConfig | null
  loading: boolean
  error: string
}>()

const emit = defineEmits<{
  saved: [config: ServiceConfig]
  notice: [message: string, tone: 'success' | 'error']
}>()

const { t } = useI18n()
const saving = ref(false)
const revealKey = ref(false)
const form = reactive<ServiceConfig>({
  auth_states: 'auth',
  listen_addr: '127.0.0.1:2048',
  proxy_api_key: '',
  active_listen_addr: '127.0.0.1:2048',
  active_proxy_api_key: '',
  management_restart_required: false,
  service_restart_required: false,
  proxy: '',
  init_timeout: '2m',
  request_timeout: '5m',
  warm_worker_limit: 5,
  max_active_workers: 10,
  warm_startup_concurrency: 2,
  per_account_concurrency: 2,
  temporary_chat: false,
})

watch(
  () => props.config,
  (config) => {
    if (config !== null) Object.assign(form, config)
  },
  { immediate: true },
)

// saveConfig 原子保存全局配置
async function saveConfig(): Promise<void> {
  saving.value = true
  try {
    const saved = await api.saveConfig({ ...form })
    emit('saved', saved)

    if (saved.management_restart_required && saved.service_restart_required) {
      emit('notice', t('settings.savedBoth'), 'success')
    } else if (saved.management_restart_required) {
      emit('notice', t('settings.savedManagement'), 'success')
    } else if (saved.service_restart_required) {
      emit('notice', t('settings.savedService'), 'success')
    } else {
      emit('notice', t('settings.saved'), 'success')
    }
  } catch (error) {
    emit('notice', error instanceof Error ? error.message : t('common.error'), 'error')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="mx-auto w-full max-w-3xl flex-1 overflow-auto p-4 md:p-8">
    <h2 class="mb-6 border-b border-[#30363d] pb-2 text-2xl font-bold text-white">
      {{ t('section.settings.title') }}
    </h2>

    <div v-if="error" class="rounded border border-red-500/40 bg-red-500/10 p-4 text-red-300">
      {{ error }}
    </div>
    <div v-else-if="loading || config === null" class="py-12 text-center text-gray-500">
      {{ t('common.loading') }}
    </div>
    <form v-else class="space-y-6" @submit.prevent="saveConfig">
      <div
        v-if="config.service_restart_required"
        class="rounded border border-blue-500/30 bg-blue-500/10 px-4 py-3 text-sm text-blue-200"
      >
        {{ t('settings.pendingService') }}
      </div>
      <div
        v-if="config.management_restart_required"
        class="rounded border border-amber-500/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-200"
      >
        {{ t('settings.pendingManagement') }}
      </div>

      <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-400">{{
            t('settings.authPath')
          }}</span>
          <input
            v-model.trim="form.auth_states"
            class="w-full rounded border border-[#30363d] bg-[#0d1117] px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
            required
            autocomplete="off"
          />
          <span
            v-if="form.listen_addr !== config.active_listen_addr"
            class="mt-1 block text-xs text-gray-500"
          >
            {{ t('settings.activeValue') }}: {{ config.active_listen_addr }}
          </span>
        </label>
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-400">{{
            t('settings.listen')
          }}</span>
          <input
            v-model.trim="form.listen_addr"
            class="w-full rounded border border-[#30363d] bg-[#0d1117] px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
            required
            autocomplete="off"
          />
        </label>
      </div>

      <div class="rounded-lg border border-[#30363d] bg-[#161b22] p-4">
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-300">{{
            t('settings.apiKey')
          }}</span>
          <div class="flex gap-2">
            <input
              v-model="form.proxy_api_key"
              :type="revealKey ? 'text' : 'password'"
              class="min-w-0 flex-1 rounded border border-[#30363d] bg-[#0d1117] px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
              autocomplete="new-password"
            />
            <button
              class="rounded border border-[#30363d] bg-[#21262d] px-3 text-xs text-gray-300 transition hover:bg-[#30363d]"
              type="button"
              @click="revealKey = !revealKey"
            >
              {{ revealKey ? t('settings.hide') : t('settings.reveal') }}
            </button>
          </div>
          <span
            v-if="form.proxy_api_key !== config.active_proxy_api_key"
            class="mt-1 block text-xs text-gray-500"
          >
            {{ t('settings.activeValue') }}:
            {{ revealKey ? config.active_proxy_api_key || t('common.empty') : '••••••••' }}
          </span>
        </label>
      </div>

      <div class="rounded-lg border border-[#30363d] bg-[#161b22] p-4">
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-300">{{
            t('settings.proxy')
          }}</span>
          <input
            v-model.trim="form.proxy"
            class="w-full rounded border border-[#30363d] bg-[#0d1117] px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
            placeholder="http://127.0.0.1:7890"
            autocomplete="off"
          />
        </label>
      </div>

      <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-400">{{
            t('settings.initTimeout')
          }}</span>
          <input
            v-model.trim="form.init_timeout"
            class="w-full rounded border border-[#30363d] bg-[#0d1117] px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
            required
            autocomplete="off"
          />
        </label>
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-400">{{
            t('settings.requestTimeout')
          }}</span>
          <input
            v-model.trim="form.request_timeout"
            class="w-full rounded border border-[#30363d] bg-[#0d1117] px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
            required
            autocomplete="off"
          />
        </label>
      </div>

      <div class="grid grid-cols-1 gap-4 md:grid-cols-3">
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-400">{{
            t('settings.warmWorkerLimit')
          }}</span>
          <input
            v-model.number="form.warm_worker_limit"
            class="w-full rounded border border-[#30363d] bg-[#0d1117] px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
            type="number"
            min="1"
            required
          />
        </label>
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-400">{{
            t('settings.maxActiveWorkers')
          }}</span>
          <input
            v-model.number="form.max_active_workers"
            class="w-full rounded border border-[#30363d] bg-[#0d1117] px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
            type="number"
            :min="form.warm_worker_limit"
            required
          />
        </label>
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-400">{{
            t('settings.warmStartupConcurrency')
          }}</span>
          <input
            v-model.number="form.warm_startup_concurrency"
            class="w-full rounded border border-[#30363d] bg-[#0d1117] px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
            type="number"
            min="1"
            :max="form.warm_worker_limit"
            required
          />
        </label>
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-400">{{
            t('settings.perAccountConcurrency')
          }}</span>
          <input
            v-model.number="form.per_account_concurrency"
            class="w-full rounded border border-[#30363d] bg-[#0d1117] px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
            type="number"
            min="1"
            required
          />
        </label>
      </div>

      <label class="flex items-center gap-3 rounded-lg border border-[#30363d] bg-[#161b22] p-4">
        <input v-model="form.temporary_chat" class="h-4 w-4 accent-blue-500" type="checkbox" />
        <span class="text-sm font-medium text-gray-300">{{ t('settings.temporaryChat') }}</span>
      </label>

      <div class="flex justify-end pt-4">
        <button
          class="flex items-center gap-2 rounded bg-blue-600 px-6 py-2 font-medium text-white shadow-lg transition hover:bg-blue-500 disabled:opacity-50"
          type="submit"
          :disabled="saving"
        >
          <UiIcon :name="saving ? 'spinner' : 'check'" :size="16" />
          {{ saving ? t('common.loading') : t('common.save') }}
        </button>
      </div>
    </form>
  </section>
</template>
