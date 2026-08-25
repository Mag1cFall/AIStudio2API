<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from '@/i18n'
import type { Model } from '@/types'

const props = defineProps<{
  models: Model[]
  loading: boolean
  error: string
}>()

const { locale, t } = useI18n()
const query = ref('')
const selectedMethod = ref('')

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

// capabilityOptionEntries 展开模型目录返回的真实能力选项
function capabilityOptionEntries(model: Model): [string, string[]][] {
  return Object.entries(model.capability_options ?? {}).sort(([left], [right]) =>
    left.localeCompare(right),
  )
}

// tokenLimit 使用当前界面语言格式化模型限制
function tokenLimit(value: number | undefined): string {
  if (value === undefined || value === 0) return '—'
  return new Intl.NumberFormat(locale.value).format(value)
}
</script>

<template>
  <section class="mx-auto w-full max-w-4xl flex-1 overflow-auto p-4 md:p-8">
    <h2 class="mb-6 border-b border-[#30363d] pb-2 text-2xl font-bold text-white">
      {{ t('section.models.title') }}
    </h2>

    <div class="mb-4 flex flex-wrap items-center gap-3">
      <input
        v-model="query"
        class="min-w-0 flex-1 rounded border border-[#30363d] bg-[#0d1117] px-3 py-2 text-sm text-white transition focus:border-blue-500 focus:outline-none sm:min-w-64"
        :placeholder="t('models.search')"
        type="search"
      />
      <select
        v-model="selectedMethod"
        class="rounded border border-[#30363d] bg-[#0d1117] px-3 py-2 text-sm text-gray-300 focus:border-blue-500 focus:outline-none"
        :aria-label="t('models.methods')"
      >
        <option value="">{{ t('models.allMethods') }}</option>
        <option v-for="method in methods" :key="method" :value="method">{{ method }}</option>
      </select>
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
      class="rounded border border-[#30363d] bg-[#161b22] py-10 text-center text-gray-500"
    >
      {{ t('models.empty') }}
    </div>
    <div v-else class="space-y-3">
      <article
        v-for="model in filteredModels"
        :key="model.id"
        class="overflow-hidden rounded border border-[#30363d] bg-[#0d1117]"
      >
        <div
          class="flex flex-wrap items-start justify-between gap-3 border-b border-[#30363d] bg-[#21262d] px-4 py-2"
        >
          <div class="min-w-0">
            <strong class="block text-gray-200">{{ model.name }}</strong>
            <code class="block truncate text-xs text-blue-400">{{ model.id }}</code>
          </div>
          <div class="flex gap-4 text-right font-mono text-xs text-gray-500">
            <span>
              {{ t('models.context') }}
              <b class="block font-normal text-gray-300">{{
                tokenLimit(model.input_token_limit)
              }}</b>
            </span>
            <span>
              {{ t('models.output') }}
              <b class="block font-normal text-gray-300">{{
                tokenLimit(model.output_token_limit)
              }}</b>
            </span>
          </div>
        </div>
        <div class="space-y-3 p-4">
          <p v-if="model.description" class="text-xs leading-5 text-gray-500">
            {{ model.description }}
          </p>
          <div>
            <span class="mb-2 block text-xs text-gray-500">{{ t('models.methods') }}</span>
            <div class="flex flex-wrap gap-2">
              <span
                v-for="method in model.methods"
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
                v-for="capability in capabilityNames(model)"
                :key="capability"
                class="rounded border border-[#30363d] bg-[#161b22] px-2 py-1 font-mono text-xs text-gray-300"
              >
                {{ capability }}
              </span>
              <span v-if="capabilityNames(model).length === 0" class="text-gray-600">—</span>
            </div>
            <div v-if="capabilityOptionEntries(model).length" class="mt-2 space-y-1 text-xs">
              <div v-for="[name, values] in capabilityOptionEntries(model)" :key="name">
                <code class="text-gray-500">{{ name }}:</code>
                <span class="ml-1 text-gray-400">{{ values.join(' / ') }}</span>
              </div>
            </div>
          </div>
        </div>
      </article>
    </div>
  </section>
</template>
