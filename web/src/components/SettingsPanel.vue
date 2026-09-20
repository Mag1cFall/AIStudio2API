<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from '@/i18n'
import type { ServiceConfig } from '@/types'
import UiIcon from './UiIcon.vue'

const props = defineProps<{
  config: ServiceConfig | null
  loading: boolean
  error: string
}>()

const emit = defineEmits<{
  notice: [message: string, tone: 'success' | 'error']
}>()

const { t } = useI18n()
const revealKey = ref(false)
const copied = ref(false)

async function copyApiKey(): Promise<void> {
  if (!props.config?.proxy_api_key) return
  try {
    await navigator.clipboard.writeText(props.config.proxy_api_key)
    copied.value = true
    emit('notice', t('settings.copied'), 'success')
    setTimeout(() => {
      copied.value = false
    }, 2000)
  } catch {
    emit('notice', t('common.error'), 'error')
  }
}
</script>

<template>
  <section class="mx-auto w-full max-w-3xl flex-1 overflow-auto p-4 md:p-8">
    <div class="mb-6 border-b border-[#30363d] pb-3">
      <h2 class="text-xl font-bold text-white">{{ t('section.settings.title') }}</h2>
      <p class="mt-1 text-xs text-gray-400">{{ t('section.settings.description') }}</p>
    </div>

    <div v-if="error" class="rounded border border-red-500/40 bg-red-500/10 p-4 text-red-300">
      {{ error }}
    </div>
    <div v-else-if="loading || config === null" class="py-12 text-center text-gray-500">
      {{ t('common.loading') }}
    </div>
    <div v-else class="space-y-6">
      <!-- Read-Only Notice Callout Block -->
      <div
        class="flex items-start gap-3 rounded-lg border border-blue-500/30 bg-blue-500/10 px-4 py-3 text-sm text-blue-200"
      >
        <UiIcon name="info" :size="18" class="mt-0.5 shrink-0 text-blue-400" />
        <span class="leading-relaxed">{{ t('settings.readOnlyNotice') }}</span>
      </div>

      <!-- General & Network -->
      <div>
        <h3 class="mb-2 text-xs font-semibold uppercase tracking-wider text-gray-400">
          {{ t('settings.networkSection') }}
        </h3>
        <div class="divide-y divide-[#30363d] rounded-lg border border-[#30363d] bg-[#161b22]">
          <!-- Listen Address -->
          <div class="flex items-center justify-between px-4 py-3">
            <span class="text-sm text-gray-300">{{ t('settings.listen') }}</span>
            <code class="text-sm font-mono text-white">{{ config.listen_addr }}</code>
          </div>

          <!-- API Key -->
          <div
            class="flex flex-col gap-1.5 px-4 py-3 sm:flex-row sm:items-center sm:justify-between"
          >
            <span class="text-sm text-gray-300">{{ t('settings.apiKey') }}</span>
            <div class="flex items-center gap-2">
              <span v-if="!config.proxy_api_key" class="text-xs text-gray-500">
                ({{ t('common.empty') }})
              </span>
              <template v-else>
                <code class="text-xs font-mono text-white">
                  {{ revealKey ? config.proxy_api_key : '••••••••••••••••' }}
                </code>
                <button
                  class="rounded border border-[#30363d] bg-[#21262d] px-2 py-0.5 text-xs text-gray-300 transition hover:bg-[#30363d]"
                  type="button"
                  @click="revealKey = !revealKey"
                >
                  {{ revealKey ? t('settings.hide') : t('settings.reveal') }}
                </button>
                <button
                  class="rounded border border-[#30363d] bg-[#21262d] px-2 py-0.5 text-xs text-gray-300 transition hover:bg-[#30363d]"
                  type="button"
                  @click="copyApiKey"
                >
                  {{ copied ? t('settings.copied') : t('settings.copyKey') }}
                </button>
              </template>
            </div>
          </div>

          <!-- Proxy -->
          <div class="flex items-center justify-between px-4 py-3">
            <span class="text-sm text-gray-300">{{ t('settings.proxy') }}</span>
            <span v-if="config.proxy" class="font-mono text-sm text-white">{{ config.proxy }}</span>
            <span v-else class="text-xs text-gray-500">{{ t('settings.noProxy') }}</span>
          </div>

          <!-- Auth States -->
          <div class="flex items-center justify-between px-4 py-3">
            <span class="text-sm text-gray-300">{{ t('settings.authPath') }}</span>
            <code class="text-sm font-mono text-white">{{ config.auth_states }}</code>
          </div>
        </div>
      </div>

      <!-- Workers & Scheduling -->
      <div>
        <h3 class="mb-2 text-xs font-semibold uppercase tracking-wider text-gray-400">
          {{ t('settings.concurrencySection') }}
        </h3>
        <div class="divide-y divide-[#30363d] rounded-lg border border-[#30363d] bg-[#161b22]">
          <!-- Routing Strategy -->
          <div class="flex items-center justify-between px-4 py-3">
            <div>
              <span class="text-sm text-gray-300">{{ t('settings.routingStrategy') }}</span>
              <p class="text-xs text-gray-500">{{ t('settings.routingHelp') }}</p>
            </div>
            <span class="rounded bg-blue-500/20 px-2 py-0.5 text-xs font-medium text-blue-300">
              {{
                config.routing_strategy === 'fill-first'
                  ? t('settings.routingFillFirst')
                  : t('settings.routingRoundRobin')
              }}
            </span>
          </div>

          <!-- Headless Mode -->
          <div class="flex items-center justify-between px-4 py-3">
            <div>
              <span class="text-sm text-gray-300">{{ t('settings.headless') }}</span>
              <p class="text-xs text-gray-500">
                {{ config.headless ? t('settings.headlessDesc') : t('settings.guiDesc') }}
              </p>
            </div>
            <span
              class="rounded px-2 py-0.5 text-xs font-medium"
              :class="
                config.headless
                  ? 'bg-emerald-500/20 text-emerald-300'
                  : 'bg-amber-500/20 text-amber-300'
              "
            >
              {{ config.headless ? t('settings.enabled') : t('settings.disabled') }}
            </span>
          </div>

          <!-- Temporary Chat -->
          <div class="flex items-center justify-between px-4 py-3">
            <span class="text-sm text-gray-300">{{ t('settings.temporaryChat') }}</span>
            <span
              class="rounded px-2 py-0.5 text-xs font-medium"
              :class="
                config.temporary_chat
                  ? 'bg-emerald-500/20 text-emerald-300'
                  : 'bg-gray-500/20 text-gray-400'
              "
            >
              {{ config.temporary_chat ? t('settings.enabled') : t('settings.disabled') }}
            </span>
          </div>

          <!-- Workers stats -->
          <div class="grid grid-cols-2 divide-x divide-[#30363d] sm:grid-cols-4">
            <div class="p-3 text-center">
              <span class="block text-xs text-gray-400">{{ t('settings.warmWorkerLimit') }}</span>
              <span class="mt-1 font-mono text-lg font-semibold text-white">
                {{ config.warm_worker_limit }}
              </span>
            </div>
            <div class="p-3 text-center">
              <span class="block text-xs text-gray-400">{{ t('settings.maxActiveWorkers') }}</span>
              <span class="mt-1 font-mono text-lg font-semibold text-white">
                {{ config.max_active_workers }}
              </span>
            </div>
            <div class="p-3 text-center">
              <span class="block text-xs text-gray-400">
                {{ t('settings.warmStartupConcurrency') }}
              </span>
              <span class="mt-1 font-mono text-lg font-semibold text-white">
                {{ config.warm_startup_concurrency }}
              </span>
            </div>
            <div class="p-3 text-center">
              <span class="block text-xs text-gray-400">
                {{ t('settings.perAccountConcurrency') }}
              </span>
              <span class="mt-1 font-mono text-lg font-semibold text-white">
                {{ config.per_account_concurrency }}
              </span>
            </div>
          </div>

          <!-- Timeouts -->
          <div class="grid grid-cols-2 divide-x divide-[#30363d]">
            <div class="flex items-center justify-between p-3.5">
              <span class="text-xs text-gray-400">{{ t('settings.initTimeout') }}</span>
              <span class="font-mono text-sm text-white">{{ config.init_timeout }}</span>
            </div>
            <div class="flex items-center justify-between p-3.5">
              <span class="text-xs text-gray-400">{{ t('settings.requestTimeout') }}</span>
              <span class="font-mono text-sm text-white">{{ config.request_timeout }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>
