<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '@/api'
import { channelLabelKey, useI18n } from '@/i18n'
import type { Model, ServiceConfig, UpstreamChannel } from '@/types'
import { useQueryText } from '@/url'
import DetailDialog from './DetailDialog.vue'
import UiIcon from './UiIcon.vue'
import UiSelect from './UiSelect.vue'

const props = defineProps<{
  models: Model[]
  loading: boolean
  error: string
  config: ServiceConfig | null
}>()
const emit = defineEmits<{
  saved: [config: ServiceConfig]
  notice: [message: string, type: 'success' | 'error']
}>()

const { locale, t, errorText } = useI18n()
const query = useQueryText('q')
const selectedMethod = useQueryText('method')
const detail = ref<Model | null>(null)
const savingChannel = ref(false)

// modelChannels 与服务端一致解析 MODEL_CHANNELS 为模型到限定通道的映射：模型 ID 去掉 models/ 前缀，通道转为小写
function modelChannels(value: string): Map<string, string> {
  const channels = new Map<string, string>()
  for (const item of value.split(',')) {
    const index = item.lastIndexOf(':')
    if (index > 0)
      channels.set(
        item.slice(0, index).trim().replace(/^models\//, ''),
        item.slice(index + 1).trim().toLowerCase(),
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
  savingChannel.value = true
  try {
    const saved = await api.saveConfig({
      ...props.config,
      model_channels: [...channels].map(([id, value]) => `${id}:${value}`).join(','),
    })
    emit('saved', saved)
    emit('notice', t('models.channelSaved'), 'success')
  } catch (error) {
    emit('notice', errorText(error), 'error')
  } finally {
    savingChannel.value = false
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
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col overflow-auto md:overflow-hidden">
    <header class="flex flex-wrap items-center gap-3 border-b border-line px-4 py-4 md:px-8">
      <div class="min-w-0 flex-1">
        <h2 class="text-xl font-semibold text-white">{{ t('section.models.title') }}</h2>
        <p class="mt-1 text-sm text-gray-500">{{ t('section.models.description') }}</p>
      </div>
    </header>
    <div class="mx-auto flex min-h-0 w-full max-w-5xl flex-1 flex-col p-4 md:p-8">
      <div class="mb-4 flex flex-wrap items-center gap-3">
        <input
          v-model="query"
          class="min-w-0 flex-1 basis-full rounded border border-line bg-canvas px-3 py-2 text-sm text-white transition focus:border-blue-500 focus:outline-none sm:min-w-64 sm:basis-0"
          :placeholder="t('models.search')"
          type="search"
        />
        <UiSelect
          v-model="selectedMethod"
          class="rounded border border-line bg-canvas px-3 py-2 text-sm text-gray-300 focus:border-blue-500 focus:outline-none"
          :aria-label="t('models.methods')"
        >
          <option value="">{{ t('models.allMethods') }}</option>
          <option v-for="method in methods" :key="method" :value="method">{{ method }}</option>
        </UiSelect>
        <span class="font-mono text-xs text-gray-500">{{ filteredModels.length }}</span>
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
        class="min-h-0 flex-1 overflow-y-auto rounded-lg border border-line bg-panel max-md:max-h-[70vh]"
      >
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
            </div>
            <code v-tooltip.overflow="model.id" class="block truncate text-xs text-blue-400">{{
              model.id
            }}</code>
          </div>
          <div class="hidden shrink-0 gap-1 sm:flex">
            <span
              v-for="channel in model.channels ?? []"
              :key="channel"
              class="rounded border border-green-900/50 bg-green-900/20 px-1.5 py-0.5 text-xs text-green-300"
            >
              {{ t(channelLabelKey(channel)) }}
            </span>
          </div>
          <span class="hidden w-28 shrink-0 text-right font-mono text-xs text-gray-400 sm:block">
            <span class="block font-sans whitespace-nowrap text-gray-600">{{
              t('models.context')
            }}</span>
            {{ tokenLimit(model.input_token_limit) }}
          </span>
          <span class="hidden w-28 shrink-0 text-right font-mono text-xs text-gray-400 sm:block">
            <span class="block font-sans whitespace-nowrap text-gray-600">{{
              t('models.output')
            }}</span>
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
        <p
          v-if="detail.description && detail.description !== detail.name"
          class="text-xs leading-5 [overflow-wrap:anywhere] text-gray-400"
        >
          {{ detail.description }}
        </p>
        <dl class="grid grid-cols-2 gap-3 font-mono text-xs sm:grid-cols-4">
          <div>
            <dt class="text-gray-500">{{ t('models.context') }}</dt>
            <dd class="text-gray-200">{{ tokenLimit(detail.input_token_limit) }}</dd>
          </div>
          <div>
            <dt class="text-gray-500">{{ t('models.output') }}</dt>
            <dd class="text-gray-200">{{ tokenLimit(outputLimit(detail)) }}</dd>
            <dd
              v-if="detail.capabilities?.output_continuation"
              class="mt-0.5 font-sans text-xs text-gray-500"
            >
              {{ t('models.continuation') }}
            </dd>
          </div>
        </dl>
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
              :disabled="savingChannel"
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
          <details v-if="capabilityGroups(detail).coded.length" class="group mt-2">
            <summary
              class="inline-flex cursor-pointer list-none items-center gap-1 text-xs text-gray-500 hover:text-gray-300"
            >
              <UiIcon name="chevronRight" :size="12" class="transition group-open:rotate-90" />
              {{
                t('models.unnamedCapabilities').replace(
                  '{count}',
                  String(capabilityGroups(detail).coded.length),
                )
              }}
            </summary>
            <div class="mt-2 flex flex-wrap gap-2">
              <span
                v-for="capability in capabilityGroups(detail).coded"
                :key="capability"
                class="rounded border border-line bg-canvas px-2 py-1 font-mono text-xs text-gray-500"
              >
                {{ capability }}
              </span>
            </div>
          </details>
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
