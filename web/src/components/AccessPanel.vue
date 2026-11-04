<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { capabilities, chatModel } from '@/access/capabilities'
import { clients, type CherryType, type Client, type Shell } from '@/access/clients'
import {
  bodyFields,
  endpointAuth,
  endpointExample,
  endpointGroups,
  pathParams,
  type Auth,
  type Endpoint,
  type EndpointGroup,
} from '@/access/endpoints'
import { maskKey, splitKeys } from '@/apikey'
import { useI18n, type TranslationKey } from '@/i18n'
import type { IconName } from '@/icons'
import { defaultChatModel } from '@/playground/catalog'
import { errorMessage } from '@/playground/request'
import type { Model, ServiceConfig } from '@/types'
import { useQueryParam } from '@/url'
import AccessCode from './AccessCode.vue'
import ChoiceGrid from './ChoiceGrid.vue'
import CopyButton from './CopyButton.vue'
import DisclosureChevron from './DisclosureChevron.vue'
import UiCollapse from './UiCollapse.vue'
import UiIcon from './UiIcon.vue'
import UiSelect from './UiSelect.vue'

defineOptions({ name: 'AccessPanel' })
const props = defineProps<{ config: ServiceConfig | null; models: Model[] }>()
const emit = defineEmits<{ settings: [] }>()
const { t } = useI18n()

// origin 是浏览器访问本服务所用的地址，客户端与示例代码以它为服务地址
const origin = window.location.origin
// loopback 表示服务地址为本机回环地址，在 Docker 中运行的客户端访问不到
const loopback = ['127.0.0.1', 'localhost', '[::1]'].includes(window.location.hostname)

type SectionID = 'start' | 'clients' | 'capabilities' | 'endpoints'
// sections 是接入页的分区与导航图标
const sections: {
  id: SectionID
  label: TranslationKey
  description: TranslationKey
  icon: IconName
}[] = [
  { id: 'start', label: 'access.nav.start', description: 'access.startHelp', icon: 'play' },
  {
    id: 'clients',
    label: 'access.nav.clients',
    description: 'access.clientsHelp',
    icon: 'playground',
  },
  {
    id: 'capabilities',
    label: 'access.nav.capabilities',
    description: 'access.capabilitiesHelp',
    icon: 'models',
  },
  {
    id: 'endpoints',
    label: 'access.nav.endpoints',
    description: 'access.endpointsHelp',
    icon: 'channel',
  },
]
const section = useQueryParam<SectionID>(
  'view',
  sections.map((item) => item.id),
  'start',
)
const current = computed(() => sections.find((item) => item.id === section.value)!)

// keys 是生效中的 API key，多个 key 用逗号分隔
const keys = computed(() => splitKeys(props.config?.active_proxy_api_key ?? ''))
const keyIndex = ref(0)
const keyShown = ref(false)
watch(keys, (list) => {
  if (keyIndex.value >= list.length) keyIndex.value = 0
})
// rowClass 是地址、密钥与客户端字段列表一行的布局：窄屏字段名单独一行，宽屏三列
const rowClass =
  'grid min-h-12 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 py-1.5 pr-1.5 pl-3 sm:grid-cols-[10rem_minmax(0,1fr)_auto]'

// revealClass 是显示与隐藏密钥按钮的样式
const revealClass =
  'shrink-0 rounded-md border border-line bg-raised px-2.5 py-1 text-xs text-gray-300 transition hover:bg-line'

// placeholderKey 是未设置 API key 时写入配置的 key，此时本地 key 校验关闭，任意值都能调用
const placeholderKey = 'aistudio2api'
const key = computed(() => keys.value[keyIndex.value] ?? placeholderKey)

// masked 在密钥隐藏时把文字中的 API key 显示为圆点，复制时仍用原文
function masked(text: string): string {
  return keys.value.length === 0 || keyShown.value ? text : text.replaceAll(key.value, '••••••••')
}

// baseUrls 是三种协议的 base URL 与填写说明
const baseUrls = computed(() => [
  { name: 'OpenAI', hint: t('access.openaiHint'), url: `${origin}/v1` },
  { name: 'Anthropic', hint: t('access.rootHint'), url: origin },
  { name: 'Gemini', hint: t('access.rootHint'), url: origin },
])

const shell = ref<Shell>(navigator.userAgent.includes('Windows') ? 'windows' : 'unix')
// shells 是终端写法的选项
const shells: { value: Shell; label: TranslationKey }[] = [
  { value: 'unix', label: 'access.shellUnix' },
  { value: 'windows', label: 'access.shellWindows' },
]
// envCode 是示例代码读取 key 的三个环境变量的设置命令
const envCode = computed(() =>
  ['OPENAI_API_KEY', 'ANTHROPIC_API_KEY', 'GEMINI_API_KEY']
    .map((name) =>
      shell.value === 'windows' ? `$env:${name} = "${key.value}"` : `export ${name}="${key.value}"`,
    )
    .join('\n'),
)

// chatModels 是实时目录中的普通对话模型
const chatModels = computed(() => props.models.filter(chatModel))
// modelPlaceholder 是目录中没有可用模型时示例中的模型占位
const modelPlaceholder = 'MODEL_ID'

// keepModel 在目录变化后保留仍可用的所选模型，否则与试用页一样取默认对话模型，没有时取第一个
function keepModel(selected: string, list: readonly Model[]): string {
  if (list.some((model) => model.id === selected)) return selected
  return (defaultChatModel(list) ?? list[0])?.id ?? ''
}

// TestResult 是连通测试中一个请求的结果：请求、是否成功、说明与耗时
interface TestResult {
  request: string
  ok: boolean
  detail: string
  elapsed: number
}
const testModel = ref('')
watch(chatModels, (list) => (testModel.value = keepModel(testModel.value, list)), {
  immediate: true,
})
const testing = ref(false)
const results = ref<TestResult[]>([])

// probe 发送一个测试请求并记录结果，read 读取成功响应的说明；连接失败或返回错误时记录原因并返回 false
async function probe(
  request: string,
  init: RequestInit & { path: string },
  read: (body: unknown) => string,
): Promise<boolean> {
  const started = performance.now()
  try {
    const response = await fetch(init.path, init)
    const elapsed = Math.round(performance.now() - started)
    if (!response.ok) {
      results.value.push({
        request,
        ok: false,
        detail: `${response.status} ${await errorMessage(response)}`,
        elapsed,
      })
      return false
    }
    results.value.push({ request, ok: true, detail: read(await response.json()), elapsed })
    return true
  } catch (error) {
    results.value.push({
      request,
      ok: false,
      detail: error instanceof TypeError ? t('access.testUnreachable') : String(error),
      elapsed: Math.round(performance.now() - started),
    })
    return false
  }
}

// runTest 先读取 /v1/models，成功后向所选模型发一次最短的 Chat Completions 请求
async function runTest(): Promise<void> {
  testing.value = true
  results.value = []
  const headers = { Authorization: `Bearer ${key.value}`, 'Content-Type': 'application/json' }
  const listed = await probe('GET /v1/models', { path: '/v1/models', headers }, (body) =>
    t('access.testModels').replace('{count}', String((body as { data: unknown[] }).data.length)),
  )
  if (listed && testModel.value !== '')
    await probe(
      'POST /v1/chat/completions',
      {
        path: '/v1/chat/completions',
        method: 'POST',
        headers,
        body: JSON.stringify({
          model: testModel.value,
          messages: [{ role: 'user', content: 'Reply with OK' }],
        }),
      },
      (body) => {
        const text =
          (body as { choices: { message: { content: string | null } }[] }).choices[0]?.message
            .content ?? ''
        return text.trim() === ''
          ? t('access.testEmptyReply')
          : t('access.testReply').replace('{text}', text.trim().slice(0, 200))
      },
    )
  testing.value = false
}

// groups 是客户端分组与组名
const groups: { id: Client['group']; label: TranslationKey }[] = [
  { id: 'agent', label: 'access.group.agent' },
  { id: 'chat', label: 'access.group.chat' },
  { id: 'gateway', label: 'access.group.gateway' },
]
const clientID = useQueryParam(
  'client',
  clients.map((item) => item.id),
  clients[0]!.id,
)
const client = computed(() => clients.find((item) => item.id === clientID.value)!)
// clientGroups 是客户端选择网格的各组
const clientGroups = computed(() =>
  groups.map((group) => ({
    label: t(group.label),
    items: clients
      .filter((item) => item.group === group.id)
      .map((item) => ({ id: item.id, label: item.name, svg: item.icon })),
  })),
)
const clientModel = ref('')
const tiers = reactive({ opus: '', sonnet: '', haiku: '' })
watch(
  chatModels,
  (list) => {
    clientModel.value = keepModel(clientModel.value, list)
    for (const tier of ['opus', 'sonnet', 'haiku'] as const)
      tiers[tier] = list.some((model) => model.id === tiers[tier]) ? tiers[tier] : clientModel.value
  },
  { immediate: true },
)
const cherry = ref<CherryType>('openai')
// cherryTypes 是 Cherry Studio 的接口类型选项
const cherryTypes: { value: CherryType; label: string }[] = [
  { value: 'openai', label: 'OpenAI' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'anthropic', label: 'Anthropic' },
]
// tierLabels 是 Claude Code 三档模型的选择框名称
const tierLabels: { tier: keyof typeof tiers; label: TranslationKey }[] = [
  { tier: 'opus', label: 'access.tierOpus' },
  { tier: 'sonnet', label: 'access.tierSonnet' },
  { tier: 'haiku', label: 'access.tierHaiku' },
]
// clientInput 是生成当前客户端配置的输入，目录中没有对话模型时模型为占位
const clientInput = computed(() => ({
  origin,
  key: key.value,
  model: chatModels.value.find((model) => model.id === clientModel.value),
  modelID: clientModel.value || modelPlaceholder,
  tiers: {
    opus: tiers.opus || modelPlaceholder,
    sonnet: tiers.sonnet || modelPlaceholder,
    haiku: tiers.haiku || modelPlaceholder,
  },
  models: chatModels.value,
  catalog: props.models,
  shell: shell.value,
  cherry: cherry.value,
  t,
}))
const steps = computed(() => client.value.steps(clientInput.value))
const links = computed(() => client.value.links?.(clientInput.value) ?? [])

const capabilityID = useQueryParam(
  'capability',
  capabilities.map((item) => item.id),
  'chat',
)
const capability = computed(() => capabilities.find((item) => item.id === capabilityID.value)!)
// capabilityGroups 是能力选择网格的唯一一组
const capabilityGroups = computed(() => [
  {
    label: t('access.nav.capabilities'),
    items: capabilities.map((item) => ({ id: item.id, label: t(item.label), icon: item.icon })),
  },
])
// capabilityModels 是实时目录中可用于当前能力的模型
const capabilityModels = computed(() => props.models.filter(capability.value.matches))
const capabilityModel = ref('')
watch(
  capabilityModels,
  (list) => (capabilityModel.value = keepModel(capabilityModel.value, list)),
  {
    immediate: true,
  },
)
const protocol = ref('')
watch(capability, (value) => (protocol.value = value.protocols[0]?.value ?? ''), {
  immediate: true,
})
const languages = computed(() => capability.value.languages(protocol.value))
const language = ref('')
watch(
  languages,
  (list) => {
    if (!list.some((item) => item.value === language.value)) language.value = list[0]!.value
  },
  { immediate: true },
)
// capabilityCode 是当前能力、协议、语言与模型的示例代码与高亮语言
const capabilityCode = computed(() => {
  const option =
    languages.value.find((item) => item.value === language.value) ?? languages.value[0]!
  return {
    file: option.file,
    highlight: option.highlight,
    code: capability.value.code({
      origin,
      protocol: protocol.value,
      language: option.value,
      model: capabilityModels.value.find((model) => model.id === capabilityModel.value),
      modelID: capabilityModel.value || modelPlaceholder,
    }),
  }
})

// endpointKey 是端点在展开列表中的标识
function endpointKey(method: string, path: string): string {
  return `${method} ${path}`
}
// expanded 是展开详情的端点
const expanded = reactive(new Set<string>())

// toggleEndpoint 展开或收起一个端点的详情
function toggleEndpoint(key: string): void {
  if (expanded.has(key)) expanded.delete(key)
  else expanded.add(key)
}

// openEndpoint 切到端点分区，展开该端点并滚动到它
async function openEndpoint(method: string, path: string): Promise<void> {
  const key = endpointKey(method, path)
  section.value = 'endpoints'
  expanded.add(key)
  await nextTick()
  document.getElementById(endpointID(key))?.scrollIntoView({ block: 'start' })
}

// endpointID 是端点行的元素 ID
function endpointID(key: string): string {
  return `endpoint-${key.replace(/[^a-z0-9]+/gi, '-')}`
}

// endpointModel 返回端点示例使用的模型：取含该端点的能力在实时目录中的默认模型，没有时为对话模型或占位
function endpointModel(path: string): string {
  const owner = capabilities.find((item) => item.endpoints.some(([, value]) => value === path))
  const list = owner ? props.models.filter(owner.matches) : chatModels.value
  return keepModel('', list) || keepModel('', chatModels.value) || modelPlaceholder
}

// authLabels 是各认证方式在端点详情中的写法
const authLabels: Record<Exclude<Auth, 'none'>, string> = {
  openai: 'Authorization: Bearer <API key>',
  anthropic: 'x-api-key: <API key>',
  gemini: 'x-goog-api-key: <API key>',
}

// authText 返回端点详情中的认证写法：WebSocket 端点以 key 查询参数认证
function authText(group: EndpointGroup, endpoint: Endpoint): string {
  const auth = endpointAuth(group, endpoint)
  if (auth === 'none') return t('access.detail.noAuth')
  return endpoint.socket ? '?key=<API key>' : authLabels[auth]
}

// paramHelp 是路径参数的说明文案键
const paramHelp: Record<string, TranslationKey> = {
  model: 'access.param.model',
  id: 'access.param.id',
  file: 'access.param.file',
}

// methodClass 是端点方法标记的颜色
function methodClass(method: string): string {
  if (method === 'GET') return 'text-green-400'
  if (method === 'DELETE') return 'text-red-400'
  if (method === 'PATCH') return 'text-yellow-400'
  return 'text-blue-400'
}
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col">
    <header
      class="border-b border-line px-4 py-4 md:px-[max(2rem,calc((100%_-_var(--container-4xl)_-_14rem)/2_+_2rem))]"
    >
      <h2 class="text-xl font-semibold text-white">{{ t('section.access.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500">{{ t('section.access.description') }}</p>
    </header>

    <div class="min-h-0 flex-1 overflow-auto [scrollbar-gutter:stable_both-edges]">
      <div
        class="mx-auto flex w-full max-w-[calc(var(--container-4xl)_+_14rem)] flex-col md:flex-row"
      >
        <nav
          class="sticky top-0 z-10 flex shrink-0 gap-1 overflow-x-auto border-b border-line bg-canvas p-2 md:w-56 md:flex-col md:self-start md:border-b-0 md:p-4 md:pl-5"
          :aria-label="t('section.access.title')"
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
          </button>
        </nav>

        <div class="min-w-0 flex-1 px-4 pt-4 pb-16 md:px-8 md:pt-6">
          <h3 class="text-base font-semibold text-white">{{ t(current.label) }}</h3>
          <p class="mt-1 mb-5 text-sm text-gray-500">{{ t(current.description) }}</p>

          <template v-if="section === 'start'">
            <article class="rounded-lg border border-line bg-panel p-4">
              <h4 class="text-sm font-semibold text-gray-200">{{ t('access.baseUrls') }}</h4>
              <p class="mt-1 text-xs text-gray-500">{{ t('access.baseUrlsHelp') }}</p>
              <ul class="mt-3 divide-y divide-line/60 rounded-md border border-line">
                <li v-for="item in baseUrls" :key="item.name" :class="rowClass">
                  <div class="col-span-2 sm:col-span-1">
                    <div class="text-sm text-gray-200">{{ item.name }}</div>
                    <div class="text-xs text-gray-500">{{ item.hint }}</div>
                  </div>
                  <code class="min-w-0 font-mono text-sm break-all text-white">{{ item.url }}</code>
                  <CopyButton
                    :text="item.url"
                    :label="t('access.copyValue').replace('{name}', item.name)"
                  />
                </li>
                <li v-if="config !== null && keys.length > 0" :class="rowClass">
                  <div class="col-span-2 text-sm text-gray-200 sm:col-span-1">
                    {{ t('settings.apiKey') }}
                  </div>
                  <div class="flex min-w-0 items-center gap-2">
                    <UiSelect
                      v-if="keys.length > 1"
                      v-model="keyIndex"
                      class="shrink-0 rounded border border-line bg-canvas px-2 py-1 text-xs text-gray-300"
                      :aria-label="t('access.keySelect')"
                    >
                      <option v-for="(item, index) in keys" :key="index" :value="index">
                        {{ index + 1 }} · {{ maskKey(item) }}
                      </option>
                    </UiSelect>
                    <code class="min-w-0 flex-1 truncate font-mono text-sm text-white">{{
                      masked(key)
                    }}</code>
                  </div>
                  <div class="flex items-center gap-2">
                    <button
                      type="button"
                      :class="revealClass"
                      :aria-pressed="keyShown"
                      @click="keyShown = !keyShown"
                    >
                      {{ keyShown ? t('settings.hide') : t('settings.reveal') }}
                    </button>
                    <CopyButton
                      :text="key"
                      :label="t('access.copyValue').replace('{name}', t('settings.apiKey'))"
                    />
                  </div>
                </li>
              </ul>
              <p class="mt-2 text-xs text-gray-500">{{ t('access.keyHelp') }}</p>
              <div
                v-if="config !== null && keys.length === 0"
                class="mt-3 rounded-md border border-amber-500/30 bg-amber-500/10 p-3 text-sm text-amber-200"
              >
                <p>{{ t('access.keyNone') }}</p>
                <button
                  type="button"
                  class="mt-2 text-sm font-medium text-blue-400 hover:text-blue-300"
                  @click="emit('settings')"
                >
                  {{ t('access.openSettings') }}
                </button>
              </div>
            </article>

            <article class="mt-4 rounded-lg border border-line bg-panel p-4">
              <div class="flex flex-wrap items-center gap-3">
                <div class="min-w-60 flex-1">
                  <h4 class="text-sm font-semibold text-gray-200">{{ t('access.env') }}</h4>
                  <p class="mt-1 text-xs text-gray-500">{{ t('access.envHelp') }}</p>
                </div>
                <div
                  class="flex rounded-md border border-line p-0.5"
                  role="group"
                  :aria-label="t('access.shell')"
                >
                  <button
                    v-for="item in shells"
                    :key="item.value"
                    type="button"
                    class="rounded px-2.5 py-1 text-xs transition"
                    :class="
                      shell === item.value
                        ? 'bg-raised text-white'
                        : 'text-gray-400 hover:text-gray-200'
                    "
                    :aria-pressed="shell === item.value"
                    @click="shell = item.value"
                  >
                    {{ t(item.label) }}
                  </button>
                </div>
              </div>
              <AccessCode
                class="mt-3"
                :code="masked(envCode)"
                :copy="envCode"
                language="bash"
                :label="t(shell === 'windows' ? 'access.shellWindows' : 'access.shellUnix')"
              />
            </article>

            <article class="mt-4 rounded-lg border border-line bg-panel p-4">
              <h4 class="text-sm font-semibold text-gray-200">{{ t('access.test') }}</h4>
              <p class="mt-1 text-xs text-gray-500">{{ t('access.testHelp') }}</p>
              <div class="mt-3 flex flex-wrap items-center gap-2">
                <UiSelect
                  v-if="chatModels.length > 0"
                  v-model="testModel"
                  class="w-72 max-w-full rounded border border-line bg-canvas px-3 py-2 text-sm text-gray-300"
                  :aria-label="t('access.testModel')"
                >
                  <option v-for="model in chatModels" :key="model.id" :value="model.id">
                    {{ model.id }}
                  </option>
                </UiSelect>
                <button
                  type="button"
                  class="flex items-center gap-2 rounded bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-500 disabled:opacity-50"
                  :disabled="testing"
                  :aria-busy="testing"
                  @click="runTest"
                >
                  <UiIcon :name="testing ? 'spinner' : 'play'" :size="14" />
                  {{ t('access.testRun') }}
                </button>
              </div>
              <ul v-if="results.length > 0" class="mt-3 space-y-1.5" aria-live="polite">
                <li
                  v-for="item in results"
                  :key="item.request"
                  class="flex items-start gap-2 rounded-md border px-3 py-2 text-sm"
                  :class="
                    item.ok
                      ? 'border-green-500/30 bg-green-500/5'
                      : 'border-red-500/40 bg-red-500/10'
                  "
                >
                  <UiIcon
                    :name="item.ok ? 'success' : 'warning'"
                    :size="15"
                    class="mt-0.5"
                    :class="item.ok ? 'text-green-400' : 'text-red-400'"
                  />
                  <code class="shrink-0 font-mono text-xs leading-5 text-gray-400">{{
                    item.request
                  }}</code>
                  <span
                    class="min-w-0 flex-1 break-words"
                    :class="item.ok ? 'text-gray-200' : 'text-red-300'"
                    >{{ item.detail }}</span
                  >
                  <span class="shrink-0 font-mono text-xs leading-5 text-gray-500 tabular-nums"
                    >{{ item.elapsed }} ms</span
                  >
                </li>
              </ul>
            </article>
          </template>

          <template v-else-if="section === 'clients'">
            <ChoiceGrid :groups="clientGroups" :selected="clientID" @select="clientID = $event" />

            <article class="mt-5 rounded-lg border border-line bg-panel">
              <div class="flex flex-wrap items-start gap-3 border-b border-line p-4">
                <div class="min-w-0 flex-1">
                  <div class="flex flex-wrap items-center gap-2">
                    <span
                      class="choice-svg flex h-6 w-6 items-center justify-center text-[22px]"
                      aria-hidden="true"
                      v-html="client.icon"
                    ></span>
                    <h4 class="text-base font-semibold text-white">{{ client.name }}</h4>
                    <span
                      class="rounded border border-line px-1.5 py-0.5 font-mono text-xs text-gray-400"
                      >{{ client.protocol }}</span
                    >
                  </div>
                  <p class="mt-1 text-sm text-gray-400">{{ t(client.summary) }}</p>
                </div>
                <a
                  :href="client.homepage"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="flex shrink-0 items-center gap-1 text-sm text-blue-400 hover:text-blue-300"
                >
                  {{ t('access.homepage') }}
                  <UiIcon name="up" :size="14" />
                </a>
              </div>

              <div class="flex flex-wrap items-end gap-3 border-b border-line p-4">
                <label v-if="!client.noModel" class="flex min-w-0 flex-col gap-1">
                  <span class="text-xs text-gray-500">{{ t('access.model') }}</span>
                  <UiSelect
                    v-if="chatModels.length > 0"
                    v-model="clientModel"
                    class="w-64 max-w-full rounded border border-line bg-canvas px-3 py-2 text-sm text-gray-300"
                    :aria-label="t('access.model')"
                  >
                    <option v-for="model in chatModels" :key="model.id" :value="model.id">
                      {{ model.id }}
                    </option>
                  </UiSelect>
                  <span v-else class="py-2 text-sm text-gray-500">{{ t('access.noModels') }}</span>
                </label>
                <template v-if="client.tiers && chatModels.length > 0">
                  <label
                    v-for="item in tierLabels"
                    :key="item.tier"
                    class="flex min-w-0 flex-col gap-1"
                  >
                    <span class="text-xs text-gray-500">{{ t(item.label) }}</span>
                    <UiSelect
                      v-model="tiers[item.tier]"
                      class="w-56 max-w-full rounded border border-line bg-canvas px-3 py-2 text-sm text-gray-300"
                      :aria-label="t(item.label)"
                    >
                      <option v-for="model in chatModels" :key="model.id" :value="model.id">
                        {{ model.id }}
                      </option>
                    </UiSelect>
                  </label>
                </template>
                <label v-if="client.cherry" class="flex min-w-0 flex-col gap-1">
                  <span class="text-xs text-gray-500">{{ t('access.field.cherryType') }}</span>
                  <UiSelect
                    v-model="cherry"
                    class="w-40 rounded border border-line bg-canvas px-3 py-2 text-sm text-gray-300"
                    :aria-label="t('access.field.cherryType')"
                  >
                    <option v-for="item in cherryTypes" :key="item.value" :value="item.value">
                      {{ item.label }}
                    </option>
                  </UiSelect>
                </label>
                <div
                  v-if="client.shell"
                  class="flex rounded-md border border-line p-0.5"
                  role="group"
                  :aria-label="t('access.shell')"
                >
                  <button
                    v-for="item in shells"
                    :key="item.value"
                    type="button"
                    class="rounded px-2.5 py-1.5 text-xs transition"
                    :class="
                      shell === item.value
                        ? 'bg-raised text-white'
                        : 'text-gray-400 hover:text-gray-200'
                    "
                    :aria-pressed="shell === item.value"
                    @click="shell = item.value"
                  >
                    {{ t(item.label) }}
                  </button>
                </div>
                <button
                  v-if="keys.length > 0"
                  type="button"
                  :class="revealClass"
                  class="py-1.5"
                  :aria-pressed="keyShown"
                  @click="keyShown = !keyShown"
                >
                  {{ keyShown ? t('settings.hide') : t('settings.reveal') }}
                </button>
              </div>

              <div class="space-y-5 p-4">
                <div v-if="links.length > 0" class="flex flex-wrap items-center gap-3">
                  <a
                    v-for="link in links"
                    :key="link.href"
                    :href="link.href"
                    class="flex items-center gap-2 rounded bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-500"
                  >
                    <UiIcon name="importAccounts" :size="14" />
                    {{ t(link.label) }}
                  </a>
                  <span class="text-xs text-gray-500">{{ t('access.importHelp') }}</span>
                </div>
                <p
                  v-if="client.docker && loopback"
                  class="rounded-md border border-line bg-canvas p-3 text-xs text-gray-400"
                >
                  {{ t('access.dockerNote').replace('{name}', client.name) }}
                </p>
                <ol class="space-y-5">
                  <li v-for="(step, index) in steps" :key="index" class="flex gap-3">
                    <span
                      class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-raised text-xs font-medium text-gray-300 tabular-nums"
                      >{{ index + 1 }}</span
                    >
                    <div class="min-w-0 flex-1">
                      <p class="mb-2 text-sm text-gray-300">
                        {{ t(step.text).replace('{name}', client.name) }}
                      </p>
                      <AccessCode
                        v-if="step.kind === 'code'"
                        :code="masked(step.code)"
                        :copy="step.code"
                        :language="step.language"
                        :label="
                          step.file ??
                          t(shell === 'windows' ? 'access.shellWindows' : 'access.shellUnix')
                        "
                      />
                      <dl v-else class="divide-y divide-line/60 rounded-md border border-line">
                        <div v-for="field in step.fields" :key="field.label" :class="rowClass">
                          <dt class="col-span-2 text-xs text-gray-500 sm:col-span-1">
                            {{ field.label }}
                          </dt>
                          <dd
                            v-if="field.choice"
                            class="col-span-2 min-h-7 py-1 text-sm text-gray-200 sm:col-span-2"
                          >
                            {{ field.value }}
                          </dd>
                          <template v-else>
                            <dd
                              v-tooltip.overflow="masked(field.value)"
                              class="min-w-0 truncate font-mono text-sm text-white"
                            >
                              {{ masked(field.value) }}
                            </dd>
                            <CopyButton
                              :text="field.value"
                              :label="t('access.copyValue').replace('{name}', field.label)"
                            />
                          </template>
                        </div>
                      </dl>
                    </div>
                  </li>
                </ol>
              </div>
            </article>
          </template>

          <template v-else-if="section === 'capabilities'">
            <ChoiceGrid
              :groups="capabilityGroups"
              :selected="capabilityID"
              @select="capabilityID = $event"
            />

            <article class="mt-5 rounded-lg border border-line bg-panel">
              <div class="border-b border-line p-4">
                <h4 class="text-base font-semibold text-white">{{ t(capability.label) }}</h4>
                <p class="mt-1 text-sm text-gray-400">{{ t(capability.description) }}</p>
                <ul class="mt-3 flex flex-wrap gap-1.5">
                  <li v-for="[method, path] in capability.endpoints" :key="method + path">
                    <button
                      type="button"
                      class="flex items-center gap-1.5 rounded border border-line bg-canvas px-2 py-0.5 font-mono text-xs transition hover:border-blue-500/60 hover:bg-raised"
                      :aria-label="t('access.openEndpoint').replace('{name}', `${method} ${path}`)"
                      @click="openEndpoint(method, path)"
                    >
                      <span :class="methodClass(method)">{{ method }}</span>
                      <span class="break-all text-gray-300">{{ path }}</span>
                      <UiIcon name="chevronRight" :size="12" class="text-gray-500" />
                    </button>
                  </li>
                </ul>
              </div>
              <div class="flex flex-wrap items-end gap-3 border-b border-line p-4">
                <label class="flex min-w-0 flex-col gap-1">
                  <span class="text-xs text-gray-500">{{ t('access.model') }}</span>
                  <UiSelect
                    v-if="capabilityModels.length > 0"
                    v-model="capabilityModel"
                    class="w-64 max-w-full rounded border border-line bg-canvas px-3 py-2 text-sm text-gray-300"
                    :aria-label="t('access.model')"
                  >
                    <option v-for="model in capabilityModels" :key="model.id" :value="model.id">
                      {{ model.id }}
                    </option>
                  </UiSelect>
                  <span v-else class="py-2 text-sm text-gray-500">{{ t('access.noModels') }}</span>
                </label>
                <div v-if="capability.protocols.length > 1" class="flex min-w-0 flex-col gap-1">
                  <span class="text-xs text-gray-500">{{ t('access.protocol') }}</span>
                  <div
                    class="flex flex-wrap rounded-md border border-line p-0.5"
                    role="group"
                    :aria-label="t('access.protocol')"
                  >
                    <button
                      v-for="item in capability.protocols"
                      :key="item.value"
                      type="button"
                      class="rounded px-2.5 py-1.5 text-xs transition"
                      :class="
                        protocol === item.value
                          ? 'bg-raised text-white'
                          : 'text-gray-400 hover:text-gray-200'
                      "
                      :aria-pressed="protocol === item.value"
                      @click="protocol = item.value"
                    >
                      {{ item.label }}
                    </button>
                  </div>
                </div>
              </div>
              <div class="p-4">
                <div
                  class="mb-3 flex w-fit max-w-full flex-wrap rounded-md border border-line p-0.5"
                  role="group"
                  :aria-label="t('access.language')"
                >
                  <button
                    v-for="item in languages"
                    :key="item.value"
                    type="button"
                    class="rounded px-2.5 py-1 text-xs transition"
                    :class="
                      language === item.value
                        ? 'bg-raised text-white'
                        : 'text-gray-400 hover:text-gray-200'
                    "
                    :aria-pressed="language === item.value"
                    @click="language = item.value"
                  >
                    {{ item.label }}
                  </button>
                </div>
                <AccessCode
                  :code="capabilityCode.code"
                  :language="capabilityCode.highlight"
                  :label="capabilityCode.file"
                />
              </div>
            </article>
          </template>

          <template v-else>
            <div class="space-y-4">
              <article
                v-for="group in endpointGroups"
                :key="group.name"
                class="rounded-lg border border-line bg-panel"
              >
                <div class="border-b border-line px-4 py-3">
                  <h4 class="text-sm font-semibold text-gray-200">{{ group.name }}</h4>
                  <p class="mt-0.5 text-xs text-gray-500">{{ t(group.description) }}</p>
                </div>
                <ul class="divide-y divide-line/60">
                  <li
                    v-for="endpoint in group.endpoints"
                    :id="endpointID(endpointKey(endpoint.method, endpoint.path))"
                    :key="endpoint.method + endpoint.path"
                    class="scroll-mt-16"
                  >
                    <div class="flex items-center gap-1 py-1 pr-1.5 pl-2">
                      <button
                        type="button"
                        class="flex min-h-8 min-w-0 flex-1 items-center gap-3 rounded-md px-2 py-1 text-left transition hover:bg-raised"
                        :aria-expanded="expanded.has(endpointKey(endpoint.method, endpoint.path))"
                        :aria-controls="`${endpointID(endpointKey(endpoint.method, endpoint.path))}-detail`"
                        @click="toggleEndpoint(endpointKey(endpoint.method, endpoint.path))"
                      >
                        <span
                          class="w-14 shrink-0 font-mono text-xs font-semibold"
                          :class="methodClass(endpoint.method)"
                          >{{ endpoint.method }}</span
                        >
                        <span
                          class="flex min-w-0 flex-1 flex-col gap-x-3 lg:flex-row lg:items-baseline"
                        >
                          <code class="min-w-0 font-mono text-sm break-all text-gray-200">{{
                            endpoint.path
                          }}</code>
                          <span class="min-w-0 text-xs text-gray-500 lg:ml-auto lg:text-right">{{
                            t(endpoint.summary)
                          }}</span>
                        </span>
                        <DisclosureChevron
                          :open="expanded.has(endpointKey(endpoint.method, endpoint.path))"
                          :turn="180"
                          class="text-gray-500"
                          ><UiIcon name="chevronDown" :size="14"
                        /></DisclosureChevron>
                      </button>
                      <CopyButton
                        :text="origin + endpoint.path"
                        :label="
                          t('access.copyValue').replace(
                            '{name}',
                            `${endpoint.method} ${endpoint.path}`,
                          )
                        "
                      />
                    </div>
                    <UiCollapse :open="expanded.has(endpointKey(endpoint.method, endpoint.path))">
                      <div
                        :id="`${endpointID(endpointKey(endpoint.method, endpoint.path))}-detail`"
                        class="space-y-4 border-t border-line/60 px-4 py-4"
                      >
                        <dl
                          class="grid grid-cols-[minmax(0,1fr)] gap-x-4 gap-y-1 text-sm sm:grid-cols-[8rem_minmax(0,1fr)] sm:gap-y-2"
                        >
                          <dt class="text-xs leading-5 text-gray-500">
                            {{ t('access.detail.protocol') }}
                          </dt>
                          <dd class="mb-1.5 text-sm leading-5 text-gray-200 sm:mb-0">
                            {{ group.name }}
                          </dd>
                          <dt class="text-xs leading-5 text-gray-500">
                            {{ t('access.detail.url') }}
                          </dt>
                          <dd
                            class="mb-1.5 font-mono text-xs leading-5 break-all text-gray-200 sm:mb-0"
                          >
                            <span :class="methodClass(endpoint.method)">{{ endpoint.method }}</span>
                            {{
                              (endpoint.socket ? origin.replace(/^http/, 'ws') : origin) +
                              endpoint.path
                            }}
                          </dd>
                          <dt class="text-xs leading-5 text-gray-500">
                            {{ t('access.detail.auth') }}
                          </dt>
                          <dd class="font-mono text-xs leading-5 break-all text-gray-200">
                            {{ authText(group, endpoint) }}
                          </dd>
                        </dl>
                        <section
                          v-if="
                            pathParams(endpoint.path).length > 0 || bodyFields(endpoint).length > 0
                          "
                        >
                          <h5 class="mb-2 text-xs font-semibold text-gray-400">
                            {{ t('access.detail.params') }}
                          </h5>
                          <ul class="divide-y divide-line/60 rounded-md border border-line">
                            <li
                              v-for="name in pathParams(endpoint.path)"
                              :key="`path-${name}`"
                              class="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 px-3 py-2"
                            >
                              <code class="font-mono text-xs text-white">{{ name }}</code>
                              <span class="text-xs text-gray-500">{{
                                t('access.detail.inPath')
                              }}</span>
                              <span
                                class="min-w-0 basis-full text-xs text-gray-400 sm:basis-0 sm:flex-1"
                                >{{ t(paramHelp[name] ?? 'access.param.id') }}</span
                              >
                            </li>
                            <li
                              v-for="[name, type] in bodyFields(endpoint)"
                              :key="`body-${name}`"
                              class="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 px-3 py-2"
                            >
                              <code class="font-mono text-xs text-white">{{ name }}</code>
                              <span class="text-xs text-gray-500"
                                >{{
                                  t(endpoint.form ? 'access.detail.inForm' : 'access.detail.inBody')
                                }}
                                · {{ type }}</span
                              >
                            </li>
                          </ul>
                        </section>
                        <AccessCode
                          :code="
                            endpointExample(origin, group, endpoint, endpointModel(endpoint.path))
                          "
                          language="bash"
                          :label="endpoint.socket ? 'websocat' : 'curl'"
                        />
                        <a
                          :href="endpoint.docs"
                          target="_blank"
                          rel="noopener noreferrer"
                          class="inline-flex items-center gap-1 text-sm text-blue-400 hover:text-blue-300"
                        >
                          {{ t('access.detail.docs') }}
                          <UiIcon name="up" :size="14" />
                        </a>
                      </div>
                    </UiCollapse>
                  </li>
                </ul>
              </article>
            </div>
          </template>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.choice-svg :deep(svg) {
  width: 1em;
  height: 1em;
}
</style>
