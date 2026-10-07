<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { api } from '@/api'
import { channelLabelKey, useI18n, type TranslationKey } from '@/i18n'
import type { IconName } from '@/icons'
import type { ServiceConfig, UpstreamChannel } from '@/types'
import { useQueryParam } from '@/url'
import SettingRow from './SettingRow.vue'
import UiIcon from './UiIcon.vue'
import UiSelect from './UiSelect.vue'
import UiSwitch from './UiSwitch.vue'

const props = defineProps<{
  config: ServiceConfig | null
  loading: boolean
  error: string
}>()

const emit = defineEmits<{
  saved: [config: ServiceConfig]
  notice: [message: string, tone: 'success' | 'error']
}>()

const { t, errorText } = useI18n()
const saving = ref(false)
const revealKey = ref(false)
const adminPassword = ref('')
const form = reactive<ServiceConfig>({
  auto_start: false,
  request_body_log: false,
  admin_auth_enabled: false,
  admin_username: 'admin',
  admin_password_set: false,
  build_native_nonstream: true,
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
  warm_startup_concurrency: 5,
  per_account_concurrency: 2,
  routing_strategy: 'round-robin',
  upstream_channels: ['playground', 'build'],
  waa_backend: 'camoufox',
  temporary_chat: false,
})

type SectionID = 'service' | 'capacity' | 'upstream' | 'network' | 'security'

// sections 是设置分组与各组包含的可编辑字段
const sections: {
  id: SectionID
  label: TranslationKey
  description: TranslationKey
  icon: IconName
  fields: (keyof ServiceConfig)[]
}[] = [
  {
    id: 'service',
    label: 'settings.section.service',
    description: 'settings.section.serviceHelp',
    icon: 'service',
    fields: ['auto_start', 'listen_addr', 'proxy_api_key', 'auth_states'],
  },
  {
    id: 'capacity',
    label: 'settings.section.capacity',
    description: 'settings.section.capacityHelp',
    icon: 'capacity',
    fields: [
      'warm_worker_limit',
      'max_active_workers',
      'warm_startup_concurrency',
      'per_account_concurrency',
      'routing_strategy',
    ],
  },
  {
    id: 'upstream',
    label: 'settings.section.upstream',
    description: 'settings.section.upstreamHelp',
    icon: 'channel',
    fields: [
      'upstream_channels',
      'build_native_nonstream',
      'waa_backend',
      'temporary_chat',
      'init_timeout',
      'request_timeout',
    ],
  },
  {
    id: 'network',
    label: 'settings.section.network',
    description: 'settings.section.networkHelp',
    icon: 'network',
    fields: ['proxy'],
  },
  {
    id: 'security',
    label: 'settings.section.security',
    description: 'settings.section.securityHelp',
    icon: 'security',
    fields: ['admin_auth_enabled', 'admin_username', 'request_body_log'],
  },
]
const section = useQueryParam<SectionID>(
  'section',
  sections.map((item) => item.id),
  'service',
)
const current = computed(() => sections.find((item) => item.id === section.value) ?? sections[0]!)

const upstreamChannelOptions: UpstreamChannel[] = ['playground', 'build']
const inputClass =
  'w-full rounded-md border border-line bg-canvas px-3 py-2 text-sm text-white transition focus:border-blue-500 focus:outline-none'

// changedFields 返回表单中与已保存配置不同的字段
const changedFields = computed(() => {
  const saved = props.config
  if (saved === null) return new Set<keyof ServiceConfig>()
  const fields = sections.flatMap((item) => item.fields)
  return new Set(
    fields.filter((field) => JSON.stringify(form[field]) !== JSON.stringify(saved[field])),
  )
})
const changedCount = computed(() => changedFields.value.size + (adminPassword.value ? 1 : 0))

// sectionChanged 判断分组是否有未保存的修改
function sectionChanged(id: SectionID): boolean {
  const target = sections.find((item) => item.id === id)
  if (id === 'security' && adminPassword.value) return true
  return target?.fields.some((field) => changedFields.value.has(field)) ?? false
}

// toggleUpstreamChannel 切换通道并保持配置顺序，至少保留一个通道
function toggleUpstreamChannel(channel: UpstreamChannel, enabled: boolean): void {
  const selected = new Set(form.upstream_channels)
  if (enabled) selected.add(channel)
  else if (selected.size > 1) selected.delete(channel)
  form.upstream_channels = upstreamChannelOptions.filter((option) => selected.has(option))
}

// resetForm 用已保存配置覆盖表单
function resetForm(config: ServiceConfig): void {
  Object.assign(form, JSON.parse(JSON.stringify(config)) as ServiceConfig)
  adminPassword.value = ''
}

watch(
  () => props.config,
  (config) => {
    if (config !== null) resetForm(config)
  },
  { immediate: true },
)

// warnUnsaved 在存在未保存修改时请求浏览器确认离开页面
function warnUnsaved(event: BeforeUnloadEvent): void {
  if (changedCount.value > 0) event.preventDefault()
}

onMounted(() => window.addEventListener('beforeunload', warnUnsaved))
onUnmounted(() => window.removeEventListener('beforeunload', warnUnsaved))

// saveConfig 原子保存全局配置
async function saveConfig(): Promise<void> {
  saving.value = true
  try {
    const saved = await api.saveConfig({
      ...form,
      ...(adminPassword.value ? { admin_password: adminPassword.value } : {}),
    })
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
    emit('notice', errorText(error), 'error')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col">
    <header class="border-b border-line px-4 py-4 md:px-8">
      <h2 class="text-xl font-semibold text-white">{{ t('section.settings.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500">{{ t('section.settings.description') }}</p>
    </header>

    <div
      v-if="error"
      class="m-4 rounded-md border border-red-500/40 bg-red-500/10 p-4 text-red-300 md:m-8"
    >
      {{ error }}
    </div>
    <div v-else-if="loading || config === null" class="py-16 text-center text-gray-500">
      <UiIcon name="spinner" :size="18" class="mx-auto mb-2" />
      {{ t('common.loading') }}
    </div>
    <form
      v-else
      class="flex min-h-0 flex-1 flex-col md:flex-row"
      novalidate
      @submit.prevent="saveConfig"
    >
      <nav
        class="flex shrink-0 gap-1 overflow-x-auto border-b border-line p-2 md:w-56 md:flex-col md:border-r md:border-b-0 md:p-4"
        :aria-label="t('section.settings.title')"
      >
        <button
          v-for="item in sections"
          :key="item.id"
          type="button"
          class="flex shrink-0 items-center gap-2 rounded-md px-3 py-2 text-left text-sm whitespace-nowrap transition-colors"
          :class="
            section === item.id
              ? 'bg-raised text-white'
              : 'text-gray-400 hover:bg-raised/60 hover:text-gray-200'
          "
          :aria-current="section === item.id ? 'page' : undefined"
          @click="section = item.id"
        >
          <UiIcon :name="item.icon" :size="15" />
          <span class="flex-1">{{ t(item.label) }}</span>
          <span
            v-if="sectionChanged(item.id)"
            v-tooltip="t('settings.unsavedSection')"
            class="h-1.5 w-1.5 rounded-full bg-amber-400"
            ><span class="sr-only">{{ t('settings.unsavedSection') }}</span></span
          >
        </button>
      </nav>

      <div class="min-h-0 flex-1 overflow-auto">
        <div class="mx-auto max-w-4xl px-4 pt-4 pb-28 md:px-8 md:pt-6">
          <div
            v-if="config.service_restart_required"
            class="mb-3 rounded-md border border-blue-500/30 bg-blue-500/10 px-4 py-2.5 text-sm text-blue-200"
          >
            {{ t('settings.pendingService') }}
          </div>
          <div
            v-if="config.management_restart_required"
            class="mb-3 rounded-md border border-amber-500/30 bg-amber-500/10 px-4 py-2.5 text-sm text-amber-200"
          >
            {{ t('settings.pendingManagement') }}
          </div>

          <h3 class="text-base font-semibold text-white">{{ t(current.label) }}</h3>
          <p class="mt-1 mb-2 text-sm text-gray-500">{{ t(current.description) }}</p>

          <template v-if="section === 'service'">
            <SettingRow
              :label="t('settings.autoStart')"
              control-id="setting-auto-start"
              :description="t('settings.autoStartHelp')"
              effect="management"
              :changed="changedFields.has('auto_start')"
            >
              <UiSwitch id="setting-auto-start" v-model="form.auto_start" />
            </SettingRow>
            <SettingRow
              :label="t('settings.listen')"
              control-id="setting-listen"
              :description="t('settings.listenHelp')"
              effect="management"
              :changed="changedFields.has('listen_addr')"
            >
              <input
                id="setting-listen"
                v-model.trim="form.listen_addr"
                :class="inputClass"
                autocomplete="off"
                spellcheck="false"
              />
              <p
                v-if="form.listen_addr !== config.active_listen_addr"
                class="mt-1 text-xs text-gray-500"
              >
                {{ t('settings.activeValue') }}: {{ config.active_listen_addr }}
              </p>
            </SettingRow>
            <SettingRow
              :label="t('settings.apiKey')"
              control-id="setting-api-key"
              :description="t('settings.apiKeyHelp')"
              effect="management"
              :changed="changedFields.has('proxy_api_key')"
            >
              <div class="flex gap-2">
                <input
                  id="setting-api-key"
                  v-model="form.proxy_api_key"
                  :type="revealKey ? 'text' : 'password'"
                  :class="inputClass"
                  autocomplete="new-password"
                  spellcheck="false"
                />
                <button
                  class="shrink-0 rounded-md border border-line bg-raised px-3 text-xs text-gray-300 transition hover:bg-line"
                  type="button"
                  @click="revealKey = !revealKey"
                >
                  {{ revealKey ? t('settings.hide') : t('settings.reveal') }}
                </button>
              </div>
              <p
                v-if="form.proxy_api_key !== config.active_proxy_api_key"
                class="mt-1 text-xs text-gray-500"
              >
                {{ t('settings.activeValue') }}:
                {{ revealKey ? config.active_proxy_api_key || t('common.empty') : '••••••••' }}
              </p>
            </SettingRow>
            <SettingRow
              :label="t('settings.authPath')"
              control-id="setting-auth-path"
              :description="t('settings.authPathHelp')"
              effect="service"
              :changed="changedFields.has('auth_states')"
            >
              <input
                id="setting-auth-path"
                v-model.trim="form.auth_states"
                :class="inputClass"
                autocomplete="off"
                spellcheck="false"
              />
            </SettingRow>
          </template>

          <template v-else-if="section === 'capacity'">
            <SettingRow
              :label="t('settings.warmWorkerLimit')"
              control-id="setting-warm"
              :description="t('settings.warmWorkerLimitHelp')"
              effect="service"
              :changed="changedFields.has('warm_worker_limit')"
            >
              <input
                id="setting-warm"
                v-model.number="form.warm_worker_limit"
                :class="inputClass"
                type="number"
                min="1"
              />
            </SettingRow>
            <SettingRow
              :label="t('settings.maxActiveWorkers')"
              control-id="setting-max"
              :description="t('settings.maxActiveWorkersHelp')"
              effect="service"
              :changed="changedFields.has('max_active_workers')"
            >
              <input
                id="setting-max"
                v-model.number="form.max_active_workers"
                :class="inputClass"
                type="number"
                :min="form.warm_worker_limit"
              />
            </SettingRow>
            <SettingRow
              :label="t('settings.warmStartupConcurrency')"
              control-id="setting-startup"
              :description="t('settings.warmStartupConcurrencyHelp')"
              effect="service"
              :changed="changedFields.has('warm_startup_concurrency')"
            >
              <input
                id="setting-startup"
                v-model.number="form.warm_startup_concurrency"
                :class="inputClass"
                type="number"
                min="1"
                :max="form.max_active_workers"
              />
            </SettingRow>
            <SettingRow
              :label="t('settings.perAccountConcurrency')"
              control-id="setting-per-account"
              :description="t('settings.perAccountConcurrencyHelp')"
              effect="service"
              :changed="changedFields.has('per_account_concurrency')"
            >
              <input
                id="setting-per-account"
                v-model.number="form.per_account_concurrency"
                :class="inputClass"
                type="number"
                min="1"
              />
            </SettingRow>
            <SettingRow
              :label="t('settings.routingStrategy')"
              control-id="setting-routing"
              :description="t('settings.routingHelp')"
              effect="immediate"
              :changed="changedFields.has('routing_strategy')"
            >
              <UiSelect id="setting-routing" v-model="form.routing_strategy" :class="inputClass">
                <option value="round-robin">{{ t('settings.routingRoundRobin') }}</option>
                <option value="fill-first">{{ t('settings.routingFillFirst') }}</option>
              </UiSelect>
            </SettingRow>
          </template>

          <template v-else-if="section === 'upstream'">
            <SettingRow
              :label="t('settings.upstreamChannels')"
              group-id="setting-channels"
              :description="t('settings.upstreamChannelsHelp')"
              effect="service"
              :changed="changedFields.has('upstream_channels')"
            >
              <div
                role="group"
                aria-labelledby="setting-channels-label"
                class="flex flex-wrap gap-4 pt-1"
              >
                <label
                  v-for="channel in upstreamChannelOptions"
                  :key="channel"
                  class="flex items-center gap-2 text-sm text-gray-300"
                >
                  <input
                    type="checkbox"
                    :checked="form.upstream_channels.includes(channel)"
                    :disabled="
                      form.upstream_channels.length === 1 &&
                      form.upstream_channels.includes(channel)
                    "
                    @change="
                      toggleUpstreamChannel(channel, ($event.target as HTMLInputElement).checked)
                    "
                  />
                  {{ t(channelLabelKey(channel)) }}
                </label>
              </div>
            </SettingRow>
            <SettingRow
              :label="t('settings.buildNative')"
              control-id="setting-build-native"
              :description="t('settings.buildNativeHelp')"
              effect="service"
              :changed="changedFields.has('build_native_nonstream')"
            >
              <UiSwitch id="setting-build-native" v-model="form.build_native_nonstream" />
            </SettingRow>
            <SettingRow
              :label="t('settings.waaBackend')"
              control-id="setting-waa-backend"
              :description="t('settings.waaBackendHelp')"
              effect="service"
              :changed="changedFields.has('waa_backend')"
            >
              <UiSelect id="setting-waa-backend" v-model="form.waa_backend" :class="inputClass">
                <option value="camoufox">{{ t('settings.waaBackendCamoufox') }}</option>
                <option value="go">{{ t('settings.waaBackendGo') }}</option>
              </UiSelect>
            </SettingRow>
            <SettingRow
              :label="t('settings.temporaryChat')"
              control-id="setting-temporary"
              :description="t('settings.temporaryChatHelp')"
              effect="service"
              :changed="changedFields.has('temporary_chat')"
            >
              <UiSwitch id="setting-temporary" v-model="form.temporary_chat" />
            </SettingRow>
            <SettingRow
              :label="t('settings.initTimeout')"
              control-id="setting-init-timeout"
              :description="t('settings.initTimeoutHelp')"
              effect="service"
              :changed="changedFields.has('init_timeout')"
            >
              <input
                id="setting-init-timeout"
                v-model.trim="form.init_timeout"
                :class="inputClass"
                autocomplete="off"
                spellcheck="false"
              />
            </SettingRow>
            <SettingRow
              :label="t('settings.requestTimeout')"
              control-id="setting-request-timeout"
              :description="t('settings.requestTimeoutHelp')"
              effect="service"
              :changed="changedFields.has('request_timeout')"
            >
              <input
                id="setting-request-timeout"
                v-model.trim="form.request_timeout"
                :class="inputClass"
                autocomplete="off"
                spellcheck="false"
              />
            </SettingRow>
          </template>

          <template v-else-if="section === 'network'">
            <SettingRow
              :label="t('settings.proxy')"
              control-id="setting-proxy"
              :description="t('settings.proxyHelp')"
              effect="service"
              :changed="changedFields.has('proxy')"
            >
              <input
                id="setting-proxy"
                v-model.trim="form.proxy"
                :class="inputClass"
                placeholder="socks5://user:pass@127.0.0.1:1080"
                autocomplete="off"
                spellcheck="false"
              />
            </SettingRow>
          </template>

          <template v-else>
            <SettingRow
              :label="t('settings.adminAuth')"
              control-id="setting-admin-auth"
              :description="t('settings.adminAuthHelp')"
              effect="management"
              :changed="changedFields.has('admin_auth_enabled')"
            >
              <UiSwitch id="setting-admin-auth" v-model="form.admin_auth_enabled" />
            </SettingRow>
            <template v-if="form.admin_auth_enabled">
              <SettingRow
                :label="t('auth.username')"
                control-id="setting-admin-username"
                effect="management"
                :changed="changedFields.has('admin_username')"
              >
                <input
                  id="setting-admin-username"
                  v-model.trim="form.admin_username"
                  name="admin_username"
                  autocomplete="username"
                  :class="inputClass"
                />
              </SettingRow>
              <SettingRow
                :label="t('settings.adminPassword')"
                control-id="setting-admin-password"
                effect="management"
                :changed="adminPassword !== ''"
              >
                <input
                  id="setting-admin-password"
                  v-model="adminPassword"
                  name="admin_password"
                  type="password"
                  autocomplete="new-password"
                  :placeholder="form.admin_password_set ? t('settings.keepPassword') : ''"
                  :class="inputClass"
                />
              </SettingRow>
            </template>
            <SettingRow
              :label="t('settings.requestBodyLog')"
              control-id="setting-body-log"
              :description="t('settings.requestBodyLogHelp')"
              effect="immediate"
              :changed="changedFields.has('request_body_log')"
            >
              <UiSwitch id="setting-body-log" v-model="form.request_body_log" />
            </SettingRow>
          </template>
        </div>
      </div>

      <Transition name="notice">
        <div
          v-if="changedCount > 0"
          class="fixed right-4 bottom-4 left-4 z-40 flex items-center gap-3 rounded-lg border border-line bg-overlay px-4 py-3 shadow-2xl shadow-black/50 md:left-auto md:min-w-[26rem]"
          role="region"
          :aria-label="t('settings.unsavedBar')"
        >
          <span class="h-2 w-2 shrink-0 rounded-full bg-amber-400"></span>
          <span class="flex-1 text-sm text-gray-300">
            {{ t('settings.unsavedCount').replace('{count}', String(changedCount)) }}
          </span>
          <button
            type="button"
            class="rounded-md px-3 py-1.5 text-sm text-gray-400 transition hover:bg-raised hover:text-white"
            :disabled="saving"
            @click="resetForm(config)"
          >
            {{ t('settings.discard') }}
          </button>
          <button
            class="flex items-center gap-2 rounded-md bg-blue-600 px-4 py-1.5 text-sm font-medium text-white transition hover:bg-blue-500 disabled:opacity-50"
            type="submit"
            :disabled="saving"
          >
            <UiIcon v-if="saving" name="spinner" :size="14" />
            {{ t('common.save') }}
          </button>
        </div>
      </Transition>
    </form>
  </section>
</template>
