<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api'
import { keepPage } from '@/desktop'
import { channelLabelKey, useI18n, type TranslationKey } from '@/i18n'
import type { IconName } from '@/icons'
import { maskedProxy } from '@/proxy'
import type { FirstEventLatency, ServiceConfig, UpstreamChannel } from '@/types'
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

const { t, locale, errorText } = useI18n()
const saving = ref(false)
const revealKey = ref(false)
const adminPassword = ref('')
const form = reactive<ServiceConfig>({
  auto_start: false,
  request_body_log: false,
  log_file: true,
  log_max_total_mb: 500,
  admin_auth_enabled: false,
  admin_username: 'admin',
  admin_password_set: false,
  build_native_nonstream: true,
  nonstream_keepalive: true,
  fake_stream: false,
  youtube_links: false,
  first_event_hedge: 'off',
  auth_states: 'auth',
  listen_addr: '127.0.0.1:2048',
  preview_listen_addr: '127.0.0.1:2050',
  proxy_api_key: '',
  active_listen_addr: '127.0.0.1:2048',
  reserved_listen_addr: '',
  active_preview_listen_addr: '127.0.0.1:2050',
  active_proxy_api_key: '',
  management_restart_required: false,
  service_restart_required: false,
  proxy: '',
  active_proxy: '',
  proxy_pool: [],
  init_timeout: '2m',
  request_timeout: '30m',
  warm_worker_limit: 5,
  max_active_workers: 10,
  warm_startup_concurrency: 5,
  per_account_concurrency: 2,
  routing_strategy: 'round-robin',
  upstream_channels: ['playground'],
  model_mapping: '',
  model_channels: '',
  waa_backend: 'camoufox',
  temporary_chat: false,
  media_output: 'inline',
  media_base_url: '',
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
    fields: [
      'auto_start',
      'listen_addr',
      'preview_listen_addr',
      'proxy_api_key',
      'auth_states',
      'media_output',
      'media_base_url',
    ],
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
      'model_mapping',
      'model_channels',
      'build_native_nonstream',
      'nonstream_keepalive',
      'fake_stream',
      'youtube_links',
      'waa_backend',
      'temporary_chat',
      'init_timeout',
      'request_timeout',
      'first_event_hedge',
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
    fields: [
      'admin_auth_enabled',
      'admin_username',
      'request_body_log',
      'log_file',
      'log_max_total_mb',
    ],
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

// latencies 是各模型与通道近期原请求首事件等待的 p95
const latencies = ref<FirstEventLatency[]>([])
// busiest 是近期样本最多的模型与通道
const busiest = computed(() =>
  latencies.value.reduce<FirstEventLatency | undefined>(
    (best, item) => (best === undefined || item.samples > best.samples ? item : best),
    undefined,
  ),
)
// suggestedSeconds 是 busiest 的 p95 向上取整的秒数，没有样本时为 60
const suggestedSeconds = computed(() =>
  busiest.value === undefined ? 60 : Math.max(1, Math.ceil(busiest.value.p95_ms / 1000)),
)
// hedgeMode 是首字超时备用请求的方式，选自定义时填入建议的秒数
const hedgeMode = computed({
  get: () =>
    form.first_event_hedge === 'off' || form.first_event_hedge === 'auto'
      ? form.first_event_hedge
      : 'custom',
  set: (mode: string) => {
    form.first_event_hedge = mode === 'custom' ? String(suggestedSeconds.value) : mode
  },
})

// hedgeHint 是所选方式的说明：自动时写取值规则，自定义时给出 busiest 的近期 p95 作参考
const hedgeHint = computed(() => {
  if (hedgeMode.value === 'auto') return t('settings.firstEventHedgeAutoHint')
  const item = busiest.value
  if (hedgeMode.value !== 'custom' || item === undefined) return ''
  return t('settings.firstEventHedgeCustomHint')
    .replace('{model}', item.model)
    .replace(
      '{seconds}',
      new Intl.NumberFormat(locale.value, { maximumFractionDigits: 1 }).format(item.p95_ms / 1000),
    )
})

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

// releasePage 撤销关闭桌面窗口时为未保存修改保留页面的登记
const releasePage = keepPage(() => changedCount.value > 0)

// removeLeaveGuard 撤销离开管理端时对未保存修改的确认
const removeLeaveGuard = useRouter().beforeEach(
  (to) =>
    to.name === 'console' || changedCount.value === 0 || window.confirm(t('settings.leaveUnsaved')),
)

onMounted(() => {
  window.addEventListener('beforeunload', warnUnsaved)
  void api
    .firstEventLatency()
    .then((items) => {
      latencies.value = items
    })
    .catch(() => undefined)
})
onUnmounted(() => {
  window.removeEventListener('beforeunload', warnUnsaved)
  releasePage()
  removeLeaveGuard()
})

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
    <header
      class="border-b border-line px-4 py-4 md:px-[max(2rem,calc((100%_-_var(--container-4xl)_-_14rem)/2_+_2rem))]"
    >
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
      class="min-h-0 flex-1 overflow-auto [scrollbar-gutter:stable_both-edges]"
      novalidate
      @submit.prevent="saveConfig"
    >
      <div
        class="mx-auto flex w-full max-w-[calc(var(--container-4xl)_+_14rem)] flex-col md:flex-row"
      >
        <nav
          class="sticky top-0 z-10 flex shrink-0 gap-1 overflow-x-auto border-b border-line bg-canvas p-2 md:w-56 md:flex-col md:self-start md:border-b-0 md:p-4 md:pl-5"
          :aria-label="t('section.settings.title')"
        >
          <button
            v-for="item in sections"
            :key="item.id"
            type="button"
            class="flex shrink-0 items-center gap-2 rounded-md px-3 py-2 text-left text-sm whitespace-nowrap transition-colors md:whitespace-normal"
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

        <div class="min-w-0 flex-1 px-4 pt-4 pb-28 md:px-8 md:pt-6">
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
                class="mt-1 text-xs break-all text-gray-500"
              >
                {{ t('settings.activeValue') }}: {{ config.active_listen_addr }}
              </p>
            </SettingRow>
            <SettingRow
              :label="t('settings.previewListen')"
              control-id="setting-preview-listen"
              :description="t('settings.previewListenHelp')"
              effect="management"
              :changed="changedFields.has('preview_listen_addr')"
            >
              <input
                id="setting-preview-listen"
                v-model.trim="form.preview_listen_addr"
                :class="inputClass"
                autocomplete="off"
                spellcheck="false"
              />
              <p
                v-if="form.preview_listen_addr !== config.active_preview_listen_addr"
                class="mt-1 text-xs break-all text-gray-500"
              >
                {{ t('settings.activeValue') }}: {{ config.active_preview_listen_addr }}
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
                class="mt-1 text-xs break-all text-gray-500"
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
            <SettingRow
              :label="t('settings.mediaOutput')"
              control-id="setting-media-output"
              :description="t('settings.mediaOutputHelp')"
              effect="service"
              :changed="changedFields.has('media_output')"
            >
              <UiSelect id="setting-media-output" v-model="form.media_output" :class="inputClass">
                <option value="inline">{{ t('settings.mediaOutputInline') }}</option>
                <option value="url">{{ t('settings.mediaOutputURL') }}</option>
              </UiSelect>
            </SettingRow>
            <SettingRow
              v-if="form.media_output === 'url'"
              :label="t('settings.mediaBaseURL')"
              control-id="setting-media-base-url"
              :description="t('settings.mediaBaseURLHelp')"
              effect="service"
              :changed="changedFields.has('media_base_url')"
            >
              <input
                id="setting-media-base-url"
                v-model.trim="form.media_base_url"
                :class="inputClass"
                placeholder="https://example.com"
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
              effect="immediate"
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
              effect="immediate"
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
              effect="immediate"
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
              effect="immediate"
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
                class="grid grid-cols-2 gap-2"
              >
                <label
                  v-for="channel in upstreamChannelOptions"
                  :key="channel"
                  class="flex h-9 cursor-pointer items-center gap-2.5 rounded-md border px-3 text-sm transition-colors has-[:disabled]:cursor-default"
                  :class="
                    form.upstream_channels.includes(channel)
                      ? 'border-blue-500/60 bg-blue-500/10 text-gray-100'
                      : 'border-line bg-canvas text-gray-400 hover:border-gray-600 hover:text-gray-200'
                  "
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
              :label="t('settings.modelMapping')"
              control-id="setting-model-mapping"
              :description="t('settings.modelMappingHelp')"
              effect="service"
              :changed="changedFields.has('model_mapping')"
            >
              <input
                id="setting-model-mapping"
                v-model.trim="form.model_mapping"
                :class="inputClass"
                placeholder="claude-haiku-5-5:gemini-3.8-flash"
                autocomplete="off"
                spellcheck="false"
              />
            </SettingRow>
            <SettingRow
              :label="t('settings.modelChannels')"
              control-id="setting-model-channels"
              :description="t('settings.modelChannelsHelp')"
              effect="immediate"
              :changed="changedFields.has('model_channels')"
            >
              <input
                id="setting-model-channels"
                v-model.trim="form.model_channels"
                :class="inputClass"
                placeholder="gemini-4-argon:playground"
                autocomplete="off"
                spellcheck="false"
              />
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
              :label="t('settings.nonstreamKeepalive')"
              control-id="setting-nonstream-keepalive"
              :description="t('settings.nonstreamKeepaliveHelp')"
              effect="service"
              :changed="changedFields.has('nonstream_keepalive')"
            >
              <UiSwitch id="setting-nonstream-keepalive" v-model="form.nonstream_keepalive" />
            </SettingRow>
            <SettingRow
              :label="t('settings.fakeStream')"
              control-id="setting-fake-stream"
              :description="t('settings.fakeStreamHelp')"
              effect="service"
              :changed="changedFields.has('fake_stream')"
            >
              <UiSwitch id="setting-fake-stream" v-model="form.fake_stream" />
            </SettingRow>
            <SettingRow
              :label="t('settings.youtubeLinks')"
              control-id="setting-youtube-links"
              :description="t('settings.youtubeLinksHelp')"
              effect="service"
              :changed="changedFields.has('youtube_links')"
            >
              <UiSwitch id="setting-youtube-links" v-model="form.youtube_links" />
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
            <SettingRow
              :label="t('settings.firstEventHedge')"
              control-id="setting-first-event-hedge"
              :description="t('settings.firstEventHedgeHelp')"
              effect="service"
              :changed="changedFields.has('first_event_hedge')"
            >
              <UiSelect id="setting-first-event-hedge" v-model="hedgeMode" :class="inputClass">
                <option value="off">{{ t('settings.firstEventHedgeOff') }}</option>
                <option value="auto">{{ t('settings.firstEventHedgeAuto') }}</option>
                <option value="custom">{{ t('settings.firstEventHedgeCustom') }}</option>
              </UiSelect>
              <div v-if="hedgeMode === 'custom'" class="mt-2 flex items-center gap-2">
                <input
                  id="setting-first-event-hedge-seconds"
                  v-model.trim="form.first_event_hedge"
                  :class="inputClass"
                  :aria-label="t('settings.firstEventHedgeSeconds')"
                  inputmode="numeric"
                  autocomplete="off"
                />
                <span class="shrink-0 text-sm text-gray-400">{{
                  t('settings.firstEventHedgeUnit')
                }}</span>
              </div>
              <p v-if="hedgeHint" class="mt-2 text-xs leading-5 text-gray-500">{{ hedgeHint }}</p>
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
              <p class="mt-1 text-xs break-all text-gray-500">
                {{ t('settings.activeProxy') }}:
                {{ config.active_proxy || t('settings.proxyDirect') }}
              </p>
              <div v-if="config.proxy_pool.length > 1" class="mt-2 text-xs">
                <p class="text-gray-500">{{ t('settings.proxyPool') }}</p>
                <ul class="mt-1 space-y-1">
                  <li
                    v-for="entry in config.proxy_pool"
                    :key="entry.proxy"
                    class="flex flex-wrap items-center gap-x-2 gap-y-0.5"
                    data-proxy-pool-entry
                  >
                    <span class="break-all text-gray-300">{{ maskedProxy(entry.proxy) }}</span>
                    <span
                      v-tooltip="entry.error || ''"
                      :class="entry.available ? 'text-green-400' : 'text-red-400'"
                    >
                      {{
                        entry.available
                          ? t('settings.proxyAvailable')
                          : t('settings.proxyUnavailable')
                      }}
                    </span>
                    <span class="text-gray-500">
                      {{ t('settings.proxyAccounts').replace('{count}', String(entry.accounts)) }}
                    </span>
                  </li>
                </ul>
              </div>
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
            <SettingRow
              :label="t('settings.logFile')"
              control-id="setting-log-file"
              :description="t('settings.logFileHelp')"
              effect="immediate"
              :changed="changedFields.has('log_file')"
            >
              <UiSwitch id="setting-log-file" v-model="form.log_file" />
            </SettingRow>
            <SettingRow
              :label="t('settings.logMaxTotal')"
              control-id="setting-log-max-total"
              :description="t('settings.logMaxTotalHelp')"
              effect="immediate"
              :changed="changedFields.has('log_max_total_mb')"
            >
              <input
                id="setting-log-max-total"
                v-model.number="form.log_max_total_mb"
                :class="inputClass"
                type="number"
                min="20"
              />
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
            {{
              t(changedCount === 1 ? 'settings.unsavedCountOne' : 'settings.unsavedCount').replace(
                '{count}',
                String(changedCount),
              )
            }}
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
