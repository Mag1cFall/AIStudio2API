<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '@/api'
import { useI18n, type TranslationKey } from '@/i18n'
import type { Account, Cooldown, RequestState, RequestSummary } from '@/types'
import UiIcon from './UiIcon.vue'

const props = defineProps<{
  accounts: Account[]
  cooldowns: Cooldown[]
  requests: RequestSummary[]
  loading: boolean
  cooldownError: string
  requestError: string
}>()

const emit = defineEmits<{
  refresh: []
  notice: [message: string, tone: 'success' | 'error']
}>()

const { locale, t } = useI18n()
const cancelling = ref('')

const requestStateKeys: Record<RequestState, TranslationKey> = {
  queued: 'state.queued',
  running: 'state.running',
  completed: 'state.completed',
  cancelled: 'state.cancelled',
  failed: 'state.failed',
}

const activeCount = computed(
  () =>
    props.requests.filter((request) => request.state === 'queued' || request.state === 'running')
      .length,
)

// accountLabel 将稳定账户 ID 映射为用户显示名称
function accountLabel(id: string): string {
  return props.accounts.find((account) => account.id === id)?.label ?? id
}

// formatTime 根据当前语言显示请求时间
function formatTime(value: string): string {
  return new Intl.DateTimeFormat(locale.value, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(new Date(value))
}

// cancelRequest 停止活动请求并刷新摘要
async function cancelRequest(request: RequestSummary): Promise<void> {
  cancelling.value = request.id
  try {
    await api.cancelRequest(request.id)
    emit('refresh')
  } catch (error) {
    emit('notice', error instanceof Error ? error.message : t('common.error'), 'error')
  } finally {
    cancelling.value = ''
  }
}
</script>

<template>
  <section class="mx-auto w-full max-w-4xl flex-1 overflow-auto p-4 md:p-8">
    <div class="mb-6 flex items-center justify-between border-b border-[#30363d] pb-2">
      <h2 class="text-2xl font-bold text-white">{{ t('section.requests.title') }}</h2>
      <button
        class="flex items-center gap-1 rounded bg-blue-600 px-3 py-1.5 text-xs text-white transition hover:bg-blue-500"
        type="button"
        @click="emit('refresh')"
      >
        <UiIcon name="refresh" :size="13" />
        {{ t('app.refresh') }}
      </button>
    </div>

    <div class="space-y-6">
      <article class="rounded-lg border border-[#30363d] bg-[#161b22] p-4">
        <div class="mb-4 flex items-center justify-between">
          <h3 class="font-bold text-gray-300">{{ t('cooldowns.title') }}</h3>
          <span class="font-mono text-xs text-gray-500">{{ cooldowns.length }}</span>
        </div>
        <div
          v-if="cooldownError"
          class="rounded border border-red-500/40 bg-red-500/10 p-3 text-red-300"
        >
          {{ cooldownError }}
        </div>
        <div v-else-if="loading" class="py-8 text-center text-gray-500">
          {{ t('common.loading') }}
        </div>
        <div v-else-if="cooldowns.length === 0" class="py-8 text-center text-xs text-gray-600">
          {{ t('cooldowns.empty') }}
        </div>
        <div v-else class="space-y-3">
          <div
            v-for="cooldown in cooldowns"
            :key="`${cooldown.account_id}:${cooldown.model_id}`"
            class="overflow-hidden rounded border border-[#30363d] bg-[#0d1117]"
          >
            <div
              class="flex items-center justify-between gap-3 border-b border-[#30363d] bg-[#21262d] px-4 py-2"
            >
              <div class="min-w-0">
                <strong class="block truncate text-sm text-gray-200">{{
                  cooldown.model_id
                }}</strong>
                <span class="text-xs text-gray-500">{{ accountLabel(cooldown.account_id) }}</span>
              </div>
              <span class="shrink-0 text-xs text-yellow-400">
                {{ t('state.cooldown') }}
              </span>
            </div>
            <div class="flex flex-wrap justify-between gap-2 p-3 text-xs text-gray-500">
              <span class="min-w-0 break-words">{{ cooldown.reason || '—' }}</span>
              <span>{{ t('cooldowns.until') }}: {{ formatTime(cooldown.until) }}</span>
            </div>
          </div>
        </div>
      </article>

      <article class="overflow-hidden rounded-lg border border-[#30363d] bg-[#161b22]">
        <div class="flex items-center justify-between border-b border-[#30363d] px-4 py-3">
          <h3 class="font-bold text-gray-300">{{ t('requests.history') }}</h3>
          <span class="text-xs text-green-400"> {{ t('requests.live') }}: {{ activeCount }} </span>
        </div>
        <div
          v-if="requestError"
          class="m-4 rounded border border-red-500/40 bg-red-500/10 p-3 text-red-300"
        >
          {{ requestError }}
        </div>
        <div v-else-if="loading" class="py-8 text-center text-gray-500">
          {{ t('common.loading') }}
        </div>
        <div v-else-if="requests.length === 0" class="py-8 text-center text-xs text-gray-600">
          {{ t('requests.empty') }}
        </div>
        <div v-else class="space-y-1 p-2">
          <div
            v-for="request in requests"
            :key="request.id"
            class="flex flex-wrap items-center justify-between gap-3 rounded p-2 transition hover:bg-[#21262d]"
          >
            <div class="min-w-0 flex-1">
              <div class="flex min-w-0 items-center gap-2">
                <code class="max-w-48 truncate text-xs font-bold text-blue-400">{{
                  request.id
                }}</code>
                <span class="text-xs text-gray-500">{{ t(requestStateKeys[request.state]) }}</span>
              </div>
              <strong class="mt-1 block truncate text-sm text-gray-300">{{ request.model }}</strong>
            </div>
            <div class="text-right text-xs text-gray-500">
              <span class="block">{{ accountLabel(request.account_id) }}</span>
              <time class="font-mono">{{ formatTime(request.started_at) }}</time>
            </div>
            <button
              v-if="request.state === 'queued' || request.state === 'running'"
              class="rounded border border-red-900/50 bg-red-900/30 px-3 py-1 text-xs text-red-400 transition hover:bg-red-900/50 disabled:opacity-50"
              type="button"
              :disabled="cancelling !== ''"
              @click="cancelRequest(request)"
            >
              {{ t('requests.stop') }}
            </button>
          </div>
        </div>
      </article>
    </div>
  </section>
</template>
