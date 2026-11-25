<script lang="ts">
import type { ImportFilterKind } from '@/importCandidates'

export type { ImportFilterKind }

// isProTier 判断权益等级是否属于 Pro+（包含 Ultra、Pro 与 Plus）
export function isProTier(tier?: string): boolean {
  return tier === 'Ultra' || tier === 'Pro' || tier === 'Plus'
}

// tierBadgeClass 返回权益胶囊在深浅色主题下均满足对比度的样式类
export function tierBadgeClass(tier?: string): string {
  switch (tier) {
    case 'Ultra':
      return 'border-amber-500/40 bg-amber-500/12 text-amber-300'
    case 'Pro':
      return 'border-blue-500/40 bg-blue-500/12 text-blue-300'
    case 'Plus':
      return 'border-green-500/40 bg-green-500/12 text-green-300'
    default:
      return 'border-line bg-raised text-gray-400'
  }
}
</script>

<script setup lang="ts">
import { useI18n } from '@/i18n'

defineProps<{
  search: string
  filter: ImportFilterKind
  placeholder: string
  counts: { all: number; unimported: number; pro: number }
}>()

const emit = defineEmits<{
  'update:search': [value: string]
  'update:filter': [value: ImportFilterKind]
}>()

const { t } = useI18n()

const options: { id: ImportFilterKind; label: () => string }[] = [
  { id: 'all', label: () => t('accounts.importFilterAll') },
  { id: 'unimported', label: () => t('accounts.importFilterUnimported') },
  { id: 'pro', label: () => t('accounts.importFilterPro') },
]
</script>

<template>
  <div class="flex flex-wrap items-center gap-2">
    <input
      :value="search"
      type="search"
      :aria-label="placeholder"
      :placeholder="placeholder"
      class="min-w-0 flex-[1_1_11rem] rounded-md border border-line bg-canvas px-2.5 py-1 text-xs text-white transition focus:border-blue-500 focus:outline-none"
      @input="emit('update:search', ($event.target as HTMLInputElement).value)"
    />
    <div
      class="inline-flex shrink-0 items-center rounded-md border border-line bg-canvas p-0.5 text-xs"
      role="group"
      :aria-label="t('accounts.importFilterGroup')"
    >
      <button
        v-for="option in options"
        :key="option.id"
        type="button"
        :aria-pressed="filter === option.id"
        class="rounded px-2 py-0.5 tabular-nums transition-colors"
        :class="
          filter === option.id
            ? 'bg-raised font-medium text-white'
            : 'text-gray-400 hover:text-gray-200'
        "
        @click="emit('update:filter', option.id)"
      >
        {{ option.label() }} {{ counts[option.id] }}
      </button>
    </div>
  </div>
</template>
