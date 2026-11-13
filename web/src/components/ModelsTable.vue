<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { parseAccountBindings } from '@/accountBindings'
import { api } from '@/api'
import { channelLabelKey, useI18n, type TranslationKey } from '@/i18n'
import { priceLines } from '@/playground/catalog'
import type { Account, Model, ServiceConfig, UpstreamChannel } from '@/types'
import { useQueryText } from '@/url'
import DetailDialog from './DetailDialog.vue'
import DisclosureChevron from './DisclosureChevron.vue'
import FilterMenu from './FilterMenu.vue'
import UiCollapse from './UiCollapse.vue'
import UiIcon from './UiIcon.vue'
import UiSelect from './UiSelect.vue'

const props = defineProps<{
  models: Model[]
  loading: boolean
  error: string
  config: ServiceConfig | null
  accounts: Account[]
}>()
const emit = defineEmits<{
  saved: [config: ServiceConfig]
  notice: [message: string, type: 'success' | 'error']
}>()

const { locale, t, errorText } = useI18n()
const query = useQueryText('q')
const selectedMethod = useQueryText('method')
const detail = ref<Model | null>(null)
// codedOpen 是详情中未命名能力码是否展开，打开另一个模型的详情时收起
const codedOpen = ref(false)
watch(detail, () => (codedOpen.value = false))
const saving = ref(false)

// modelChannels 与服务端一致解析 MODEL_CHANNELS 为模型到限定通道的映射：模型 ID 去掉 models/ 前缀，通道转为小写
function modelChannels(value: string): Map<string, string> {
  const channels = new Map<string, string>()
  for (const item of value.split(',')) {
    const index = item.lastIndexOf(':')
    if (index > 0)
      channels.set(
        item
          .slice(0, index)
          .trim()
          .replace(/^models\//, ''),
        item
          .slice(index + 1)
          .trim()
          .toLowerCase(),
      )
  }
  return channels
}

// channelChoices 是详情中可选的通道：全部通道与每个已启用通道
const channelChoices = computed<('' | UpstreamChannel)[]>(() => [
  '',
  ...(props.config?.upstream_channels ?? []),
])

// modelChannel 返回模型限定的通道，未限定时为空
function modelChannel(model: Model): string {
  return modelChannels(props.config?.model_channels ?? '').get(model.id) ?? ''
}

// setModelChannel 保存模型限定的通道，空值表示使用全部已启用通道
async function setModelChannel(model: Model, channel: string): Promise<void> {
  if (props.config === null || modelChannel(model) === channel) return
  const channels = modelChannels(props.config.model_channels)
  if (channel === '') channels.delete(model.id)
  else channels.set(model.id, channel)
  await saveConfig(
    { model_channels: [...channels].map(([id, value]) => `${id}:${value}`).join(',') },
    t('models.channelSaved'),
  )
}

// modelAccountBindings 解析 MODEL_ACCOUNTS 为模型到可用账户 ID 的映射
function modelAccountBindings(value: string): Map<string, string[]> {
  return new Map(
    [...parseAccountBindings(value)].map(([model, ids]) => [model.replace(/^models\//, ''), ids]),
  )
}

// modelAccounts 返回模型可用的账户 ID，未绑定时为空
function modelAccounts(model: Model): string[] {
  return modelAccountBindings(props.config?.model_accounts ?? '').get(model.id) ?? []
}

// accountLabel 返回账户 ID 对应的显示名称
function accountLabel(id: string): string {
  return props.accounts.find((account) => account.id === id)?.label ?? id
}

// modelAccountsLabel 是模型账户选择按钮的文字：未选时为全部账户，只选一个时为该账户
function modelAccountsLabel(model: Model): string {
  const ids = modelAccounts(model)
  if (ids.length === 0) return t('settings.keyAccountsAll')
  return ids.length === 1 ? accountLabel(ids[0]!) : t('settings.keyAccountsSome')
}

// setModelAccounts 保存模型可用的账户，空列表表示可用全部账户
async function setModelAccounts(model: Model, ids: string[]): Promise<void> {
  if (props.config === null) return
  const bindings = modelAccountBindings(props.config.model_accounts)
  if (ids.length > 0) bindings.set(model.id, ids)
  else bindings.delete(model.id)
  await saveConfig(
    { model_accounts: [...bindings].map(([id, value]) => `${id}:${value.join('|')}`).join(',') },
    t('models.accountsSaved'),
  )
}

// saveConfig 保存模型详情中修改的配置项并提示结果
async function saveConfig(changes: Partial<ServiceConfig>, savedNotice: string): Promise<void> {
  saving.value = true
  try {
    emit('saved', await api.saveConfig({ ...props.config!, ...changes }))
    emit('notice', savedNotice, 'success')
  } catch (error) {
    emit('notice', errorText(error), 'error')
  } finally {
    saving.value = false
  }
}

const methods = computed(() =>
  [...new Set(props.models.flatMap((model) => model.methods))].sort((left, right) =>
    left.localeCompare(right),
  ),
)

const filteredModels = computed(() => {
  const term = query.value.trim().toLowerCase()
  return props.models.filter((model) => {
    const capabilities = capabilityNames(model)
    const matchesQuery =
      term === '' ||
      [model.id, model.name, model.description ?? '', ...model.methods, ...capabilities].some(
        (value) => value.toLowerCase().includes(term),
      )
    const matchesMethod =
      selectedMethod.value === '' || model.methods.includes(selectedMethod.value)
    return matchesQuery && matchesMethod
  })
})

// hiddenHelp 返回在 AI Studio 官网中隐藏的模型的说明，其他模型为 null
function hiddenHelp(model: Model): TranslationKey | null {
  if (model.methods.includes('createDecision')) return 'models.decisionHiddenHelp'
  return model.hidden ? 'models.hiddenHelp' : null
}

// accountRisk 判断模型是否经 Build 写代码助手回答，使用时有账户被限制的风险
function accountRisk(model: Model): boolean {
  return model.channels?.includes('assistant') ?? false
}

// capabilityNames 返回模型明确启用的能力名称
function capabilityNames(model: Model): string[] {
  return Object.entries(model.capabilities ?? {})
    .filter(([, enabled]) => enabled)
    .map(([name]) => name)
}

// unnamedCapability 匹配目录中只有编号、没有名称的能力码
const unnamedCapability = /^(secondary_)?capability_code_\d+$/

// capabilityGroups 把模型能力分成有名称的能力与未命名的能力码
function capabilityGroups(model: Model): { named: string[]; coded: string[] } {
  const names = capabilityNames(model)
  return {
    named: names.filter((name) => !unnamedCapability.test(name)),
    coded: names.filter((name) => unnamedCapability.test(name)),
  }
}

// capabilityOptionEntries 展开模型目录返回的真实能力选项
function capabilityOptionEntries(model: Model): [string, string[]][] {
  return Object.entries(model.capability_options ?? {}).sort(([left], [right]) =>
    left.localeCompare(right),
  )
}

// outputLimit 返回模型可设置的输出上限，自动续写模型为输入上限
function outputLimit(model: Model): number | undefined {
  return model.capabilities?.output_continuation
    ? model.input_token_limit
    : model.output_token_limit
}

// tokenLimit 使用当前界面语言格式化模型限制
function tokenLimit(value: number | undefined): string {
  if (value === undefined || value === 0) return '—'
  return new Intl.NumberFormat(locale.value).format(value)
}

// dateFormat 与 monthFormat 是详情中发布日期与知识截止月份的格式
const dateFormat = computed(
  () => new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: 'short', day: 'numeric' }),
)
const monthFormat = computed(
  () => new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: 'short' }),
)

// modelSummary 返回官网模型卡片的简介，没有简介时为与名称不同的目录描述
function modelSummary(model: Model): string {
  if (model.details?.summary) return model.details.summary
  return model.description !== model.name ? (model.description ?? '') : ''
}
</script>

<template>
  <section
    class="flex min-h-0 flex-1 flex-col overflow-auto max-md:[scrollbar-gutter:stable] md:overflow-hidden"
  >
    <header
      class="page-x flex flex-wrap items-center gap-3 border-b border-line py-4 md:overflow-hidden md:[scrollbar-gutter:stable]"
    >
      <div class="min-w-0 flex-1">
        <h2 class="text-xl font-semibold text-white">{{ t('section.models.title') }}</h2>
        <p class="mt-1 text-sm text-gray-500">{{ t('section.models.description') }}</p>
      </div>
    </header>
    <div
      class="page-x flex min-h-0 flex-1 flex-col py-4 md:overflow-hidden md:py-8 md:[scrollbar-gutter:stable]"
    >
      <div class="mb-3 flex flex-wrap items-center gap-2">
        <input
          v-model="query"
          class="min-w-0 flex-1 basis-full rounded-md border border-line bg-canvas px-3 py-1 text-sm text-white transition focus:border-blue-500 focus:outline-none sm:min-w-64 sm:basis-0"
          :placeholder="t('models.search')"
          type="search"
        />
        <UiSelect
          v-if="!loading"
          v-model="selectedMethod"
          class="rounded-md border border-line bg-canvas px-2 py-1.5 text-xs text-gray-200 focus:border-blue-500 focus:outline-none"
          :aria-label="t('models.methods')"
        >
          <option value="">{{ t('models.allMethods') }}</option>
          <option v-for="method in methods" :key="method" :value="method">{{ method }}</option>
        </UiSelect>
        <span v-if="!loading" class="shrink-0 text-xs text-gray-500 tabular-nums">{{
          t(filteredModels.length === 1 ? 'models.countOne' : 'models.count').replace(
            '{count}',
            String(filteredModels.length),
          )
        }}</span>
      </div>

      <div v-if="error" class="rounded border border-red-500/40 bg-red-500/10 p-4 text-red-300">
        {{ error }}
      </div>
      <div v-else-if="loading" class="py-12 text-center text-gray-500">
        {{ t('common.loading') }}
      </div>
      <div
        v-else-if="filteredModels.length === 0"
        class="rounded border border-line bg-panel py-10 text-center text-gray-500"
      >
        {{ t('models.empty') }}
      </div>
      <div
        v-else
        class="min-h-0 flex-1 overflow-y-auto [scrollbar-gutter:stable] rounded-lg border border-line bg-panel max-md:max-h-[70vh] max-md:shrink-0 max-md:basis-auto"
      >
        <div
          class="sticky top-0 z-10 hidden items-center gap-3 border-b border-line bg-panel px-4 py-2 text-xs text-gray-500 sm:flex"
        >
          <span class="min-w-0 flex-1">{{ t('models.model') }}</span>
          <span class="w-40 shrink-0">{{ t('models.channels') }}</span>
          <span class="w-28 shrink-0 text-right">{{ t('models.context') }}</span>
          <span class="w-28 shrink-0 text-right">{{ t('models.output') }}</span>
          <span class="w-3.5 shrink-0" aria-hidden="true"></span>
        </div>
        <button
          v-for="model in filteredModels"
          :key="model.id"
          type="button"
          class="flex w-full items-center gap-3 border-b border-line/60 px-4 py-2 text-left transition hover:bg-raised"
          @click="detail = model"
        >
          <div class="min-w-0 flex-1">
            <div class="flex min-w-0 items-center gap-2">
              <span v-tooltip.overflow="model.name" class="truncate text-sm text-gray-200">{{
                model.name
              }}</span>
              <span
                v-if="model.paid"
                class="shrink-0 rounded border border-amber-700/60 bg-amber-900/20 px-1.5 py-0.5 text-xs font-medium text-amber-300"
              >
                {{ t('models.paid') }}
              </span>
              <span
                v-if="hiddenHelp(model)"
                v-tooltip="t(hiddenHelp(model)!)"
                class="shrink-0 rounded border border-amber-700/60 bg-amber-900/20 px-1.5 py-0.5 text-xs font-medium text-amber-300"
              >
                {{ t('models.hidden') }}
              </span>
              <span
                v-if="accountRisk(model)"
                v-tooltip="t('models.accountRiskHelp')"
                class="shrink-0 rounded border border-amber-700/60 bg-amber-900/20 px-1.5 py-0.5 text-xs font-medium text-amber-300"
              >
                {{ t('models.accountRisk') }}
              </span>
            </div>
            <code v-tooltip.overflow="model.id" class="block truncate text-xs text-blue-400">{{
              model.id
            }}</code>
          </div>
          <div class="hidden w-40 shrink-0 gap-1 sm:flex">
            <span
              v-for="channel in model.channels ?? []"
              :key="channel"
              class="rounded border border-green-900/50 bg-green-900/20 px-1.5 py-0.5 text-xs text-green-300"
            >
              {{ t(channelLabelKey(channel)) }}
            </span>
          </div>
          <span class="hidden w-28 shrink-0 text-right text-xs text-gray-300 tabular-nums sm:block">
            {{ tokenLimit(model.input_token_limit) }}
          </span>
          <span class="hidden w-28 shrink-0 text-right text-xs text-gray-300 tabular-nums sm:block">
            {{ tokenLimit(outputLimit(model)) }}
          </span>
          <UiIcon name="chevronRight" :size="14" class="shrink-0 text-gray-600" />
        </button>
      </div>
    </div>

    <DetailDialog
      :open="detail !== null"
      :title="detail?.name ?? ''"
      :description="detail?.id"
      @close="detail = null"
    >
      <div v-if="detail" class="space-y-4">
        <div
          v-if="detail.details?.alias || modelSummary(detail) || detail.details?.docs_url"
          class="space-y-1 text-xs leading-5 [overflow-wrap:anywhere]"
        >
          <p v-if="detail.details?.alias" class="text-gray-300">{{ detail.details.alias }}</p>
          <p v-if="modelSummary(detail) || detail.details?.docs_url" class="text-gray-400">
            {{ modelSummary(detail) }}
            <a
              v-if="detail.details?.docs_url"
              :href="detail.details.docs_url"
              target="_blank"
              rel="noopener noreferrer"
              class="inline-flex items-center gap-0.5 whitespace-nowrap text-blue-400 hover:text-blue-300"
              >{{ t('pg.developerDocs') }}<UiIcon name="up" :size="12"
            /></a>
          </p>
        </div>
        <p
          v-if="detail.details?.warning"
          class="flex items-start gap-2 text-xs leading-5 text-amber-200"
        >
          <UiIcon name="warning" :size="14" class="mt-0.5 shrink-0" />{{ detail.details.warning }}
        </p>
        <p
          v-if="hiddenHelp(detail)"
          class="flex items-start gap-2 text-xs leading-5 text-amber-200"
        >
          <UiIcon name="warning" :size="14" class="mt-0.5 shrink-0" />{{ t(hiddenHelp(detail)!) }}
        </p>
        <p
          v-if="accountRisk(detail)"
          class="flex items-start gap-2 text-xs leading-5 text-amber-200"
        >
          <UiIcon name="warning" :size="14" class="mt-0.5 shrink-0" />{{
            t('models.accountRiskHelp')
          }}
        </p>
        <div>
          <dl class="grid grid-cols-2 gap-3 font-mono text-xs sm:grid-cols-4">
            <div>
              <dt class="text-gray-500">{{ t('models.context') }}</dt>
              <dd class="text-gray-200">{{ tokenLimit(detail.input_token_limit) }}</dd>
            </div>
            <div>
              <dt class="text-gray-500">{{ t('models.output') }}</dt>
              <dd class="text-gray-200">{{ tokenLimit(outputLimit(detail)) }}</dd>
            </div>
            <div v-if="detail.details?.release_date">
              <dt class="text-gray-500">{{ t('models.releaseDate') }}</dt>
              <dd class="text-gray-200">
                {{ dateFormat.format(detail.details.release_date * 1000) }}
              </dd>
            </div>
            <div v-if="detail.details?.model_card_url || detail.details?.knowledge_cutoff">
              <dt class="text-gray-500">{{ t('models.knowledgeCutoff') }}</dt>
              <dd class="text-gray-200">
                <a
                  v-if="detail.details.model_card_url"
                  :href="detail.details.model_card_url"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="inline-flex items-center gap-0.5 font-sans text-blue-400 hover:text-blue-300"
                  >{{ t('pg.seeModelCard') }}<UiIcon name="up" :size="12"
                /></a>
                <template v-else-if="detail.details.knowledge_cutoff">{{
                  monthFormat.format(detail.details.knowledge_cutoff * 1000)
                }}</template>
              </dd>
            </div>
          </dl>
          <p v-if="detail.capabilities?.output_continuation" class="mt-2 text-xs text-gray-500">
            {{ t('pg.outputContinuation') }}
          </p>
        </div>
        <div v-if="detail.details?.pricing?.length">
          <span class="mb-2 block text-xs text-gray-500">{{ t('models.pricing') }}</span>
          <ul class="space-y-1 text-xs leading-5 [overflow-wrap:anywhere] text-gray-300">
            <li
              v-for="(line, index) in priceLines(detail, t)"
              :key="index"
              v-tooltip="line.tooltip"
            >
              {{ line.text }}
              <a
                v-if="line.link"
                :href="line.link"
                target="_blank"
                rel="noopener noreferrer"
                class="inline-flex items-center gap-0.5 whitespace-nowrap text-blue-400 hover:text-blue-300"
                >{{ t('pg.learnMore') }}<UiIcon name="up" :size="12"
              /></a>
            </li>
          </ul>
        </div>
        <div v-if="detail.channels?.length">
          <span id="model-channel-label" class="mb-2 block text-xs text-gray-500">{{
            t('models.channels')
          }}</span>
          <div
            v-if="config && channelChoices.length > 2 && detail.methods.includes('generateContent')"
            role="group"
            aria-labelledby="model-channel-label"
            class="flex flex-wrap gap-2"
          >
            <button
              v-for="channel in channelChoices"
              :key="channel"
              type="button"
              class="rounded px-2.5 py-1 text-xs transition disabled:opacity-50"
              :class="
                modelChannel(detail) === channel
                  ? 'bg-blue-600 text-white'
                  : 'bg-raised text-gray-400 hover:text-gray-200'
              "
              :aria-pressed="modelChannel(detail) === channel"
              :disabled="saving"
              @click="setModelChannel(detail, channel)"
            >
              {{ channel === '' ? t('models.channelAll') : t(channelLabelKey(channel)) }}
            </button>
          </div>
          <div v-else class="flex flex-wrap gap-2">
            <span
              v-for="channel in detail.channels"
              :key="channel"
              class="rounded border border-green-900/50 bg-green-900/20 px-2 py-1 text-xs text-green-300"
            >
              {{ t(channelLabelKey(channel)) }}
            </span>
          </div>
        </div>
        <div v-if="config && (accounts.length > 1 || modelAccounts(detail).length > 0)">
          <span class="mb-2 block text-xs text-gray-500">{{ t('models.accounts') }}</span>
          <FilterMenu
            :label="modelAccountsLabel(detail)"
            :options="accounts.map((account) => account.id)"
            :selected="modelAccounts(detail)"
            :format="accountLabel"
            :search-label="t('settings.accountSearch')"
            :empty-label="t('settings.accountNone')"
            :clear-label="t('settings.keyAccountsClear')"
            :disabled="saving"
            @change="setModelAccounts(detail!, $event)"
          />
          <p class="mt-1.5 text-xs text-gray-500">{{ t('models.accountsHelp') }}</p>
        </div>
        <div>
          <span class="mb-2 block text-xs text-gray-500">{{ t('models.methods') }}</span>
          <div class="flex flex-wrap gap-2">
            <span
              v-for="method in detail.methods"
              :key="method"
              class="rounded border border-blue-900/50 bg-blue-900/20 px-2 py-1 font-mono text-xs text-blue-300"
            >
              {{ method }}
            </span>
          </div>
        </div>
        <div>
          <span class="mb-2 block text-xs text-gray-500">{{ t('models.capabilities') }}</span>
          <div class="flex flex-wrap gap-2">
            <span
              v-for="capability in capabilityGroups(detail).named"
              :key="capability"
              class="rounded border border-line bg-canvas px-2 py-1 font-mono text-xs text-gray-300"
            >
              {{ capability }}
            </span>
            <span v-if="capabilityNames(detail).length === 0" class="text-gray-600">—</span>
          </div>
          <template v-if="capabilityGroups(detail).coded.length">
            <button
              type="button"
              class="mt-2 inline-flex items-center gap-1 text-xs text-gray-500 hover:text-gray-300"
              :aria-expanded="codedOpen"
              @click="codedOpen = !codedOpen"
            >
              <DisclosureChevron :open="codedOpen"
                ><UiIcon name="chevronRight" :size="12"
              /></DisclosureChevron>
              {{
                t('models.unnamedCapabilities').replace(
                  '{count}',
                  String(capabilityGroups(detail).coded.length),
                )
              }}
            </button>
            <UiCollapse :open="codedOpen">
              <div class="flex flex-wrap gap-2 pt-2">
                <span
                  v-for="capability in capabilityGroups(detail).coded"
                  :key="capability"
                  class="rounded border border-line bg-canvas px-2 py-1 font-mono text-xs text-gray-500"
                >
                  {{ capability }}
                </span>
              </div>
            </UiCollapse>
          </template>
          <div v-if="capabilityOptionEntries(detail).length" class="mt-3 space-y-1 text-xs">
            <div
              v-for="[name, values] in capabilityOptionEntries(detail)"
              :key="name"
              class="[overflow-wrap:anywhere]"
            >
              <code class="text-gray-500">{{ name }}:</code>
              <span class="ml-1 text-gray-400">{{ values.join(' / ') }}</span>
            </div>
          </div>
        </div>
      </div>
    </DetailDialog>
  </section>
</template>
