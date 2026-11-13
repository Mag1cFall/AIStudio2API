<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { parseAccountBindings } from '@/accountBindings'
import { api } from '@/api'
import { maskKey, splitKeys } from '@/apikey'
import { desktopPlatform, keepPage, sendDesktop } from '@/desktop'
import { channelLabelKey, useI18n, type TranslationKey } from '@/i18n'
import type { IconName } from '@/icons'
import { maskedProxy } from '@/proxy'
import type { Account, FirstEventLatency, Model, ServiceConfig, UpstreamChannel } from '@/types'
import { useQueryParam } from '@/url'
import { SliderRange, SliderRoot, SliderThumb, SliderTrack } from 'reka-ui'
import AccountOrderList from './AccountOrderList.vue'
import ApiKeyInput from './ApiKeyInput.vue'
import DurationInput from './DurationInput.vue'
import PairRows from './PairRows.vue'
import PathList from './PathList.vue'
import SettingRow from './SettingRow.vue'
import UiIcon from './UiIcon.vue'
import UiSelect from './UiSelect.vue'
import UiSwitch from './UiSwitch.vue'
import FilterMenu from './FilterMenu.vue'

const props = defineProps<{
  config: ServiceConfig | null
  accounts: Account[]
  models: Model[]
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
// form 是编辑中的配置，读到 props.config 后由 resetForm 填入，表单只在读到配置后显示
const form = reactive({} as ServiceConfig)

type SectionID = 'service' | 'capacity' | 'runtime' | 'models' | 'network' | 'security'

// sections 是设置分组与各组包含的可编辑字段
const sections: {
  id: SectionID
  label: TranslationKey
  icon: IconName
  fields: (keyof ServiceConfig)[]
}[] = [
  {
    id: 'service',
    label: 'settings.section.service',
    icon: 'service',
    fields: [
      'auto_start',
      'hide_dock_icon',
      'listen_addr',
      'preview_listen_addr',
      'proxy_api_key',
      'api_key_accounts',
      'auth_states',
      'media_output',
      'media_base_url',
    ],
  },
  {
    id: 'capacity',
    label: 'settings.section.capacity',
    icon: 'capacity',
    fields: [
      'warm_worker_limit',
      'max_active_workers',
      'warm_startup_concurrency',
      'per_account_concurrency',
      'routing_strategy',
      'account_order',
      'max_account_switches',
    ],
  },
  {
    id: 'runtime',
    label: 'settings.section.runtime',
    icon: 'channel',
    fields: [
      'waa_backend',
      'upstream_channels',
      'build_native_nonstream',
      'init_timeout',
      'request_timeout',
      'first_event_hedge',
      'first_event_hedge_models',
    ],
  },
  {
    id: 'models',
    label: 'settings.section.models',
    icon: 'boxes',
    fields: [
      'model_mapping',
      'model_channels',
      'nonstream_keepalive',
      'fake_stream',
      'youtube_links',
      'search_model_aliases',
      'discover_hidden_models',
      'google_search',
      'continuation_rewrite',
      'continuation_prompt',
      'strip_thought_signatures',
    ],
  },
  {
    id: 'network',
    label: 'settings.section.network',
    icon: 'network',
    fields: ['proxy'],
  },
  {
    id: 'security',
    label: 'settings.section.security',
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
const current = computed(() => sections.find((item) => item.id === section.value)!)

const upstreamChannelOptions: UpstreamChannel[] = ['playground', 'build']
const waaBackendOptions: { value: ServiceConfig['waa_backend']; label: TranslationKey }[] = [
  { value: 'camoufox', label: 'settings.waaBackendCamoufox' },
  { value: 'spidermonkey', label: 'settings.waaBackendSpiderMonkey' },
]
const inputClass =
  'w-full rounded-md border border-line bg-canvas px-3 py-2 text-sm text-white transition focus:border-blue-500 focus:outline-none'

// capacityItems 是合并到一行的容量字段
const capacityItems: {
  field:
    | 'warm_worker_limit'
    | 'max_active_workers'
    | 'warm_startup_concurrency'
    | 'per_account_concurrency'
  label: TranslationKey
}[] = [
  { field: 'warm_worker_limit', label: 'settings.warmWorkerLimit' },
  { field: 'max_active_workers', label: 'settings.maxActiveWorkers' },
  { field: 'warm_startup_concurrency', label: 'settings.warmStartupConcurrency' },
  { field: 'per_account_concurrency', label: 'settings.perAccountConcurrency' },
]

// capacityLocked 是由环境变量决定的容量字段对应的变量名，没有时为 undefined
const capacityLocked = computed(() => {
  const keys = capacityItems
    .map((item) => environmentKey(item.field))
    .filter((key) => key !== undefined)
  return keys.length > 0 ? keys.join(', ') : undefined
})

// capacityLimit 是 Worker 滑块的上限：账户数、已保存峰值与当前峰值中最大的一个
const capacityLimit = computed(() =>
  Math.max(
    props.accounts.length,
    props.config?.max_active_workers ?? 0,
    form.max_active_workers,
    1,
  ),
)

// setWorkerRange 把滑块的两端写回常驻数与峰值上限
function setWorkerRange(range: number[] | undefined): void {
  if (range === undefined) return
  ;[form.warm_worker_limit, form.max_active_workers] = range as [number, number]
}

// capacitySummary 是当前容量下最多同时处理的请求数：运行的 Worker 数不超过账户数
const capacitySummary = computed(() =>
  t(props.accounts.length === 1 ? 'settings.capacitySummaryOne' : 'settings.capacitySummary')
    .replace(
      '{requests}',
      String(
        Math.min(form.max_active_workers, props.accounts.length || form.max_active_workers) *
          form.per_account_concurrency,
      ),
    )
    .replace('{accounts}', String(props.accounts.length)),
)

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
function sectionChanged(item: (typeof sections)[number]): boolean {
  if (item.id === 'security' && adminPassword.value) return true
  return item.fields.some((field) => changedFields.value.has(field))
}

// toggleUpstreamChannel 切换通道并保持配置顺序
function toggleUpstreamChannel(channel: UpstreamChannel, enabled: boolean): void {
  const selected = new Set(form.upstream_channels)
  if (enabled) selected.add(channel)
  else selected.delete(channel)
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

// hedgeDescription 是问号里的说明，自动方式时附上取值规则
const hedgeDescription = computed(() =>
  hedgeMode.value === 'auto'
    ? `${t('settings.firstEventHedgeHelp')}。${t('settings.firstEventHedgeAutoHint')}`
    : t('settings.firstEventHedgeHelp'),
)

// hedgeHint 是自定义方式下 busiest 的近期 p95 参考
const hedgeHint = computed(() => {
  const item = busiest.value
  if (hedgeMode.value !== 'custom' || item === undefined) return ''
  return t('settings.firstEventHedgeCustomHint')
    .replace('{model}', item.model)
    .replace(
      '{seconds}',
      new Intl.NumberFormat(locale.value, { maximumFractionDigits: 1 }).format(item.p95_ms / 1000),
    )
})

// environmentKey 返回决定该字段的进程环境变量名，字段不由环境变量决定时为 undefined
function environmentKey(field: keyof ServiceConfig): string | undefined {
  const key = field === 'auth_states' ? 'AISTUDIO_AUTH_STATES' : field.toUpperCase()
  return props.config?.environment.includes(key) ? key : undefined
}

// apiKeyList 是表单中逗号分隔的各个 API key
const apiKeyList = computed(() => splitKeys(form.proxy_api_key ?? ''))
// activeKeys 是生效中的各个 API key
const activeKeys = computed(() => splitKeys(props.config?.active_proxy_api_key ?? ''))

// writeKeyAccounts 按 API key 的顺序写回 api_key_accounts，去掉已不在 key 列表中的绑定
function writeKeyAccounts(bindings: Map<string, string[]>): void {
  form.api_key_accounts = apiKeyList.value
    .filter((key) => bindings.has(key))
    .map((key) => `${key}:${bindings.get(key)!.join('|')}`)
    .join(',')
}

// keyBindings 是表单中各 API key 可用的账户 ID
const keyBindings = computed(() => parseAccountBindings(form.api_key_accounts ?? ''))

// setKeyAccounts 设置 key 可用的账户，空列表表示可用全部账户
function setKeyAccounts(key: string, ids: string[]): void {
  const bindings = new Map(keyBindings.value)
  if (ids.length > 0) bindings.set(key, ids)
  else bindings.delete(key)
  writeKeyAccounts(bindings)
}

// accountLabel 返回账户 ID 对应的显示名称
function accountLabel(id: string): string {
  return props.accounts.find((account) => account.id === id)?.label ?? id
}

// keyAccountsLabel 是 key 账户选择按钮的文字：未选时为全部账户，只选一个时为该账户
function keyAccountsLabel(key: string): string {
  const ids = keyBindings.value.get(key) ?? []
  if (ids.length === 0) return t('settings.keyAccountsAll')
  return ids.length === 1 ? accountLabel(ids[0]!) : t('settings.keyAccountsSome')
}

// modelIDs 是模型目录中的全部模型 ID
const modelIDs = computed(() => props.models.map((model) => model.id))

// modelOptions 是行编辑器中可选的模型
const modelOptions = computed(() => modelIDs.value.map((id) => ({ value: id, label: id })))

// channelOptions 是行编辑器中可选的上游通道
const channelOptions = computed(() =>
  upstreamChannelOptions.map((channel) => ({ value: channel, label: t(channelLabelKey(channel)) })),
)

// hedgeModels 是表单中发起备用请求的模型
const hedgeModels = computed(() =>
  (form.first_event_hedge_models ?? '')
    .split(',')
    .map((id) => id.trim())
    .filter(Boolean),
)

// hedgeModelsLabel 是备用请求模型选择按钮的文字
const hedgeModelsLabel = computed(() => {
  if (hedgeModels.value.length === 0) return t('settings.hedgeModelsNone')
  return hedgeModels.value.length === 1 ? hedgeModels.value[0]! : t('settings.hedgeModelsSome')
})

watch(apiKeyList, () => {
  if (form.api_key_accounts !== undefined)
    writeKeyAccounts(parseAccountBindings(form.api_key_accounts))
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

// removeLeaveGuard 撤销离开管理端时对未保存修改的确认，确认离开时放弃修改
const removeLeaveGuard = useRouter().beforeEach((to) => {
  if (to.name === 'console' || changedCount.value === 0) return true
  if (!window.confirm(t('settings.leaveUnsaved'))) return false
  if (props.config !== null) resetForm(props.config)
  return true
})

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
      warm_worker_limit: Math.min(form.warm_worker_limit, form.max_active_workers),
      warm_startup_concurrency: Math.min(form.warm_startup_concurrency, form.max_active_workers),
      ...(adminPassword.value ? { admin_password: adminPassword.value } : {}),
    })
    emit('saved', saved)
    if (desktopPlatform === 'darwin') sendDesktop(`dock:${saved.hide_dock_icon ? 'hide' : 'show'}`)
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
    <header class="page-x overflow-hidden border-b border-line py-4 [scrollbar-gutter:stable]">
      <h2 class="text-xl font-semibold text-white">{{ t('section.settings.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500">{{ t('section.settings.description') }}</p>
    </header>

    <div v-if="error" class="page-x overflow-hidden py-4 [scrollbar-gutter:stable] md:py-8">
      <div class="rounded-md border border-red-500/40 bg-red-500/10 p-4 text-red-300">
        {{ error }}
      </div>
    </div>
    <div v-else-if="loading || config === null" class="py-16 text-center text-gray-500">
      <UiIcon name="spinner" :size="18" class="mx-auto mb-2" />
      {{ t('common.loading') }}
    </div>
    <form
      v-else
      class="min-h-0 flex-1 overflow-auto [scrollbar-gutter:stable]"
      novalidate
      @submit.prevent="saveConfig"
    >
      <div class="page-x flex flex-col @max-3xl:px-0 @3xl:flex-row">
        <nav
          class="sticky top-0 z-10 flex shrink-0 gap-1 overflow-x-auto border-b border-line bg-canvas px-4 py-2 @3xl:w-56 @3xl:flex-col @3xl:self-start @3xl:border-b-0 @3xl:px-0 @3xl:py-8"
          :aria-label="t('section.settings.title')"
        >
          <button
            v-for="item in sections"
            :key="item.id"
            type="button"
            class="flex shrink-0 items-center gap-2 rounded-md px-3 py-2 text-left text-sm whitespace-nowrap transition-colors @3xl:whitespace-normal"
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
              v-if="sectionChanged(item)"
              v-tooltip="t('settings.unsavedSection')"
              class="h-1.5 w-1.5 rounded-full bg-gray-400"
              ><span class="sr-only">{{ t('settings.unsavedSection') }}</span></span
            >
          </button>
        </nav>

        <div class="min-w-0 flex-1 px-4 pt-4 pb-28 @3xl:pt-8 @3xl:pr-0 @3xl:pl-8">
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

          <h3 class="mb-2 text-base font-semibold text-white">{{ t(current.label) }}</h3>

          <template v-if="section === 'service'">
            <SettingRow
              :label="t('settings.autoStart')"
              control-id="setting-auto-start"
              :description="t('settings.autoStartHelp')"
              effect="management"
              :changed="changedFields.has('auto_start')"
              :env="environmentKey('auto_start')"
            >
              <UiSwitch id="setting-auto-start" v-model="form.auto_start" />
            </SettingRow>
            <SettingRow
              v-if="desktopPlatform === 'darwin'"
              :label="t('settings.hideDockIcon')"
              control-id="setting-hide-dock-icon"
              :description="t('settings.hideDockIconHelp')"
              effect="immediate"
              :changed="changedFields.has('hide_dock_icon')"
              :env="environmentKey('hide_dock_icon')"
            >
              <UiSwitch id="setting-hide-dock-icon" v-model="form.hide_dock_icon" />
            </SettingRow>
            <SettingRow
              :label="t('settings.listen')"
              control-id="setting-listen"
              :description="t('settings.listenHelp')"
              effect="management"
              :changed="changedFields.has('listen_addr')"
              :env="environmentKey('listen_addr')"
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
              :env="environmentKey('preview_listen_addr')"
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
              :env="environmentKey('proxy_api_key')"
            >
              <ApiKeyInput
                v-model="form.proxy_api_key"
                v-model:revealed="revealKey"
                input-id="setting-api-key"
              />
              <div
                v-if="apiKeyList.join(',') !== activeKeys.join(',')"
                class="mt-1.5 flex flex-wrap items-center gap-1 text-xs text-gray-500"
              >
                {{ t('settings.activeValue') }}:
                <span
                  v-for="key in activeKeys"
                  :key="key"
                  class="rounded border border-line px-1.5 py-0.5 font-mono break-all text-gray-400"
                  >{{ revealKey ? key : maskKey(key) }}</span
                >
                <span v-if="activeKeys.length === 0">{{ t('common.empty') }}</span>
              </div>
            </SettingRow>
            <SettingRow
              v-if="apiKeyList.length > 0 && accounts.length > 0"
              :label="t('settings.apiKeyAccounts')"
              group-id="setting-api-key-accounts"
              :description="t('settings.apiKeyAccountsHelp')"
              effect="immediate"
              :changed="changedFields.has('api_key_accounts')"
              :env="environmentKey('api_key_accounts')"
            >
              <div
                role="group"
                aria-labelledby="setting-api-key-accounts-label"
                class="max-h-60 space-y-2 overflow-y-auto pr-1 [scrollbar-width:thin]"
              >
                <div
                  v-for="key in apiKeyList"
                  :key="key"
                  class="flex items-center justify-between gap-3"
                >
                  <span
                    data-api-key
                    class="min-w-0 rounded border border-line bg-raised px-2 py-0.5 font-mono text-xs break-all text-gray-200"
                    >{{ revealKey ? key : maskKey(key) }}</span
                  >
                  <FilterMenu
                    field
                    class="rounded-md border border-line bg-canvas px-2 py-1.5 text-xs text-gray-200"
                    :label="keyAccountsLabel(key)"
                    :options="accounts.map((account) => account.id)"
                    :selected="keyBindings.get(key) ?? []"
                    :format="accountLabel"
                    :search-label="t('settings.accountSearch')"
                    :empty-label="t('settings.accountNone')"
                    :clear-label="t('settings.keyAccountsClear')"
                    @change="setKeyAccounts(key, $event)"
                  />
                </div>
              </div>
            </SettingRow>
            <SettingRow
              :label="t('settings.authPath')"
              control-id="setting-auth-path"
              :description="t('settings.authPathHelp')"
              effect="service"
              :changed="changedFields.has('auth_states')"
              :env="environmentKey('auth_states')"
            >
              <PathList
                input-id="setting-auth-path"
                :input-class="inputClass"
                :value="form.auth_states"
                @change="form.auth_states = $event"
              />
            </SettingRow>
            <SettingRow
              :label="t('settings.mediaOutput')"
              control-id="setting-media-output"
              :description="t('settings.mediaOutputHelp')"
              effect="service"
              :changed="changedFields.has('media_output')"
              :env="environmentKey('media_output')"
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
              :env="environmentKey('media_base_url')"
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
              :label="t('settings.workerCapacity')"
              group-id="setting-capacity"
              :description="t('settings.workerCapacityHelp')"
              effect="immediate"
              :changed="capacityItems.some((item) => changedFields.has(item.field))"
              :env-note="capacityLocked"
            >
              <div role="group" aria-labelledby="setting-capacity-label" class="space-y-3">
                <SliderRoot
                  :model-value="[form.warm_worker_limit, form.max_active_workers]"
                  :min="1"
                  :max="capacityLimit"
                  :step="1"
                  thumb-alignment="overflow"
                  :disabled="
                    environmentKey('warm_worker_limit') !== undefined ||
                    environmentKey('max_active_workers') !== undefined
                  "
                  class="relative flex h-5 w-full touch-none items-center select-none data-[disabled]:opacity-50"
                  @update:model-value="setWorkerRange"
                >
                  <SliderTrack class="relative h-1 grow rounded-full bg-line">
                    <SliderRange class="absolute h-full rounded-full bg-blue-500" />
                  </SliderTrack>
                  <SliderThumb
                    class="block h-3.5 w-3.5 rounded-full bg-gray-100 transition-transform hover:scale-110 focus-visible:ring-2 focus-visible:ring-gray-400 focus-visible:outline-none"
                    :aria-label="t('settings.warmWorkerLimit')"
                  />
                  <SliderThumb
                    class="block h-3.5 w-3.5 rounded-full bg-gray-100 transition-transform hover:scale-110 focus-visible:ring-2 focus-visible:ring-gray-400 focus-visible:outline-none"
                    :aria-label="t('settings.maxActiveWorkers')"
                  />
                </SliderRoot>
                <div class="grid grid-cols-2 gap-x-2 gap-y-2">
                  <label
                    v-for="item in capacityItems"
                    :key="item.field"
                    :for="`setting-${item.field}`"
                    class="flex flex-col justify-between text-xs text-gray-400"
                  >
                    {{ t(item.label) }}
                    <input
                      :id="`setting-${item.field}`"
                      v-model.number="form[item.field]"
                      :class="[inputClass, 'mt-1.5 disabled:opacity-50']"
                      type="number"
                      min="1"
                      :disabled="environmentKey(item.field) !== undefined"
                    />
                  </label>
                </div>
                <p class="text-xs text-gray-500">{{ capacitySummary }}</p>
              </div>
            </SettingRow>
            <SettingRow
              :label="t('settings.routingStrategy')"
              control-id="setting-routing"
              :description="t('settings.routingHelp')"
              effect="immediate"
              :changed="changedFields.has('routing_strategy')"
              :env="environmentKey('routing_strategy')"
            >
              <UiSelect id="setting-routing" v-model="form.routing_strategy" :class="inputClass">
                <option value="round-robin">{{ t('settings.routingRoundRobin') }}</option>
                <option value="fill-first">{{ t('settings.routingFillFirst') }}</option>
              </UiSelect>
            </SettingRow>
            <SettingRow
              v-if="form.routing_strategy === 'fill-first' && accounts.length > 1"
              :label="t('settings.accountOrder')"
              group-id="setting-account-order"
              :description="t('settings.accountOrderHelp')"
              effect="immediate"
              :changed="changedFields.has('account_order')"
              :env="environmentKey('account_order')"
            >
              <AccountOrderList
                :accounts="accounts"
                :value="form.account_order ?? ''"
                @change="form.account_order = $event"
              />
            </SettingRow>
            <SettingRow
              :label="t('settings.maxAccountSwitches')"
              control-id="setting-max-account-switches"
              :description="t('settings.maxAccountSwitchesHelp')"
              effect="service"
              :changed="changedFields.has('max_account_switches')"
              :env="environmentKey('max_account_switches')"
            >
              <input
                id="setting-max-account-switches"
                v-model.number="form.max_account_switches"
                :class="inputClass"
                type="number"
                min="0"
              />
            </SettingRow>
          </template>

          <template v-else-if="section === 'runtime'">
            <SettingRow
              :label="t('settings.waaBackend')"
              control-id="setting-waa-backend"
              :description="t('settings.waaBackendHelp')"
              effect="service"
              :changed="changedFields.has('waa_backend')"
              :env="environmentKey('waa_backend')"
            >
              <UiSelect id="setting-waa-backend" v-model="form.waa_backend" :class="inputClass">
                <option
                  v-for="option in waaBackendOptions"
                  :key="option.value"
                  :value="option.value"
                >
                  {{ t(option.label) }}
                </option>
              </UiSelect>
            </SettingRow>
            <SettingRow
              v-for="channel in upstreamChannelOptions"
              :key="channel"
              :label="t('settings.channelRow').replace('{channel}', t(channelLabelKey(channel)))"
              :control-id="`setting-channel-${channel}`"
              :description="t('settings.upstreamChannelsHelp')"
              effect="service"
              :changed="
                changedFields.has('upstream_channels') &&
                form.upstream_channels.includes(channel) !==
                  config?.upstream_channels.includes(channel)
              "
              :env="environmentKey('upstream_channels')"
            >
              <template v-if="channel === 'build'" #tag>
                <span class="text-xs text-gray-500">{{ t('settings.legacy') }}</span>
              </template>
              <template
                v-if="channel === 'build' && form.upstream_channels.includes('build')"
                #note
              >
                <p class="mt-1 flex items-start gap-1.5 text-xs leading-5 text-gray-400">
                  <UiIcon name="warning" :size="13" class="mt-1 text-amber-400" />
                  {{ t('settings.buildLegacyNotice') }}
                </p>
              </template>
              <UiSwitch
                :id="`setting-channel-${channel}`"
                :model-value="form.upstream_channels.includes(channel)"
                :disabled="
                  form.upstream_channels.length === 1 && form.upstream_channels.includes(channel)
                "
                @update:model-value="toggleUpstreamChannel(channel, $event)"
              />
            </SettingRow>
            <SettingRow
              v-if="form.upstream_channels.includes('build')"
              :label="t('settings.buildNative')"
              control-id="setting-build-native"
              :description="t('settings.buildNativeHelp')"
              effect="service"
              :changed="changedFields.has('build_native_nonstream')"
              :env="environmentKey('build_native_nonstream')"
            >
              <UiSwitch id="setting-build-native" v-model="form.build_native_nonstream" />
            </SettingRow>
            <SettingRow
              :label="t('settings.initTimeout')"
              control-id="setting-init-timeout"
              :description="t('settings.initTimeoutHelp')"
              effect="service"
              :changed="changedFields.has('init_timeout')"
              :env="environmentKey('init_timeout')"
            >
              <DurationInput
                id="setting-init-timeout"
                v-model="form.init_timeout"
                :input-class="inputClass"
              />
            </SettingRow>
            <SettingRow
              :label="t('settings.requestTimeout')"
              control-id="setting-request-timeout"
              :description="t('settings.requestTimeoutHelp')"
              effect="service"
              :changed="changedFields.has('request_timeout')"
              :env="environmentKey('request_timeout')"
            >
              <DurationInput
                id="setting-request-timeout"
                v-model="form.request_timeout"
                :input-class="inputClass"
              />
            </SettingRow>
            <SettingRow
              :label="t('settings.firstEventHedge')"
              control-id="setting-first-event-hedge"
              :description="hedgeDescription"
              effect="service"
              :changed="changedFields.has('first_event_hedge')"
              :env="environmentKey('first_event_hedge')"
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
            <SettingRow
              :label="t('settings.firstEventHedgeModels')"
              group-id="setting-first-event-hedge-models"
              :description="t('settings.firstEventHedgeModelsHelp')"
              effect="service"
              :changed="changedFields.has('first_event_hedge_models')"
              :env="environmentKey('first_event_hedge_models')"
            >
              <FilterMenu
                field
                :class="inputClass"
                :label="hedgeModelsLabel"
                :options="modelIDs"
                :selected="hedgeModels"
                :format="(id: string) => id"
                :search-label="t('settings.modelSearch')"
                :empty-label="t('settings.modelNone')"
                :clear-label="t('settings.selectionClear')"
                @change="form.first_event_hedge_models = $event.join(',')"
              />
            </SettingRow>
          </template>

          <template v-else-if="section === 'models'">
            <SettingRow
              :label="t('settings.modelMapping')"
              group-id="setting-model-mapping"
              :description="t('settings.modelMappingHelp')"
              effect="service"
              :changed="changedFields.has('model_mapping')"
              :env="environmentKey('model_mapping')"
            >
              <PairRows
                role="group"
                aria-labelledby="setting-model-mapping-label"
                :value="form.model_mapping"
                :left-placeholder="t('settings.aliasPlaceholder')"
                :right-options="modelOptions"
                :right-placeholder="t('settings.selectModel')"
                :add-label="t('settings.addMapping')"
                @change="form.model_mapping = $event"
              />
            </SettingRow>
            <SettingRow
              :label="t('settings.modelChannels')"
              group-id="setting-model-channels"
              :description="t('settings.modelChannelsHelp')"
              effect="immediate"
              :changed="changedFields.has('model_channels')"
              :env="environmentKey('model_channels')"
            >
              <PairRows
                role="group"
                aria-labelledby="setting-model-channels-label"
                :value="form.model_channels"
                :left-options="modelOptions"
                :left-placeholder="t('settings.selectModel')"
                :right-options="channelOptions"
                :right-placeholder="t('settings.selectChannel')"
                :add-label="t('settings.addModelChannel')"
                @change="form.model_channels = $event"
              />
            </SettingRow>
            <SettingRow
              :label="t('settings.nonstreamKeepalive')"
              control-id="setting-nonstream-keepalive"
              :description="t('settings.nonstreamKeepaliveHelp')"
              effect="service"
              :changed="changedFields.has('nonstream_keepalive')"
              :env="environmentKey('nonstream_keepalive')"
            >
              <UiSwitch id="setting-nonstream-keepalive" v-model="form.nonstream_keepalive" />
            </SettingRow>
            <SettingRow
              :label="t('settings.fakeStream')"
              control-id="setting-fake-stream"
              :description="t('settings.fakeStreamHelp')"
              effect="service"
              :changed="changedFields.has('fake_stream')"
              :env="environmentKey('fake_stream')"
            >
              <UiSwitch id="setting-fake-stream" v-model="form.fake_stream" />
            </SettingRow>
            <SettingRow
              :label="t('settings.youtubeLinks')"
              control-id="setting-youtube-links"
              :description="t('settings.youtubeLinksHelp')"
              effect="service"
              :changed="changedFields.has('youtube_links')"
              :env="environmentKey('youtube_links')"
            >
              <UiSwitch id="setting-youtube-links" v-model="form.youtube_links" />
            </SettingRow>
            <SettingRow
              :label="t('settings.searchModelAliases')"
              control-id="setting-search-model-aliases"
              :description="t('settings.searchModelAliasesHelp')"
              effect="service"
              :changed="changedFields.has('search_model_aliases')"
              :env="environmentKey('search_model_aliases')"
            >
              <UiSwitch id="setting-search-model-aliases" v-model="form.search_model_aliases" />
            </SettingRow>
            <SettingRow
              :label="t('settings.discoverHiddenModels')"
              control-id="setting-discover-hidden-models"
              :description="t('settings.discoverHiddenModelsHelp')"
              effect="service"
              :changed="changedFields.has('discover_hidden_models')"
              :env="environmentKey('discover_hidden_models')"
            >
              <UiSwitch id="setting-discover-hidden-models" v-model="form.discover_hidden_models" />
            </SettingRow>
            <SettingRow
              :label="t('settings.googleSearch')"
              control-id="setting-google-search"
              :description="t('settings.googleSearchHelp')"
              effect="service"
              :changed="changedFields.has('google_search')"
              :env="environmentKey('google_search')"
            >
              <UiSwitch id="setting-google-search" v-model="form.google_search" />
            </SettingRow>
            <SettingRow
              :label="t('settings.continuationRewrite')"
              control-id="setting-continuation-rewrite"
              :description="t('settings.continuationRewriteHelp')"
              effect="service"
              :changed="changedFields.has('continuation_rewrite')"
              :env="environmentKey('continuation_rewrite')"
            >
              <UiSwitch id="setting-continuation-rewrite" v-model="form.continuation_rewrite" />
            </SettingRow>
            <SettingRow
              :label="t('settings.continuationPrompt')"
              control-id="setting-continuation-prompt"
              :description="t('settings.continuationPromptHelp')"
              effect="service"
              :changed="changedFields.has('continuation_prompt')"
              :env="environmentKey('continuation_prompt')"
            >
              <textarea
                id="setting-continuation-prompt"
                v-model.trim="form.continuation_prompt"
                :class="[inputClass, 'resize-y disabled:cursor-not-allowed disabled:opacity-50']"
                :disabled="!form.continuation_rewrite"
                rows="4"
                spellcheck="false"
              />
            </SettingRow>
            <SettingRow
              :label="t('settings.stripThoughtSignatures')"
              control-id="setting-strip-thought-signatures"
              :description="t('settings.stripThoughtSignaturesHelp')"
              effect="service"
              :changed="changedFields.has('strip_thought_signatures')"
              :env="environmentKey('strip_thought_signatures')"
            >
              <UiSwitch
                id="setting-strip-thought-signatures"
                v-model="form.strip_thought_signatures"
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
              :env="environmentKey('proxy')"
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
                      v-tooltip="entry.error"
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
              :env="environmentKey('admin_auth_enabled')"
            >
              <UiSwitch id="setting-admin-auth" v-model="form.admin_auth_enabled" />
            </SettingRow>
            <template v-if="form.admin_auth_enabled">
              <SettingRow
                :label="t('auth.username')"
                control-id="setting-admin-username"
                effect="management"
                :changed="changedFields.has('admin_username')"
                :env="environmentKey('admin_username')"
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
                :env="environmentKey('admin_password')"
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
              :env="environmentKey('request_body_log')"
            >
              <UiSwitch id="setting-body-log" v-model="form.request_body_log" />
            </SettingRow>
            <SettingRow
              :label="t('settings.logFile')"
              control-id="setting-log-file"
              :description="t('settings.logFileHelp')"
              effect="immediate"
              :changed="changedFields.has('log_file')"
              :env="environmentKey('log_file')"
            >
              <UiSwitch id="setting-log-file" v-model="form.log_file" />
            </SettingRow>
            <SettingRow
              :label="t('settings.logMaxTotal')"
              control-id="setting-log-max-total"
              :description="t('settings.logMaxTotalHelp')"
              effect="immediate"
              :changed="changedFields.has('log_max_total_mb')"
              :env="environmentKey('log_max_total_mb')"
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
