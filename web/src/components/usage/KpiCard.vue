<script setup lang="ts">
import { computed } from 'vue'
import type { IconName } from '@/icons'
import UiIcon from '../UiIcon.vue'

const props = defineProps<{
  label: string
  icon: IconName
  value: string
  help: string
  details: { label: string; value: string; tone?: string | undefined }[]
  delta?:
    | { text: string; tone: 'good' | 'bad' | 'neutral'; direction: 'up' | 'down'; help: string }
    | undefined
  spark?: number[] | undefined
  color?: string | undefined
  tone?: string | undefined
}>()

// sparkPath 把序列归一化为 100×28 的折线路径
const sparkPath = computed(() => {
  const values = props.spark ?? []
  if (values.length < 2) return ''
  const high = Math.max(...values)
  const low = Math.min(...values)
  const span = high - low || 1
  return values
    .map((value, index) => {
      const x = (index / (values.length - 1)) * 100
      const y = 26 - ((value - low) / span) * 24
      return `${index === 0 ? 'M' : 'L'}${x.toFixed(2)},${y.toFixed(2)}`
    })
    .join(' ')
})
const deltaClass = computed(() => {
  if (props.delta === undefined) return ''
  return { good: 'text-green-400', bad: 'text-red-400', neutral: 'text-gray-500' }[props.delta.tone]
})
</script>

<template>
  <article class="flex min-w-0 flex-col rounded-lg border border-line bg-panel p-4">
    <div class="flex items-center gap-2 text-xs text-gray-500">
      <UiIcon :name="icon" :size="14" />
      <span
        v-tooltip="help"
        class="cursor-help underline decoration-gray-700 decoration-dotted underline-offset-4"
        >{{ label }}</span
      >
    </div>
    <div class="mt-2 flex items-baseline gap-2">
      <span
        class="truncate text-2xl font-semibold tracking-tight tabular-nums"
        :class="tone ?? 'text-white'"
        >{{ value }}</span
      >
      <span
        v-if="delta"
        v-tooltip="delta.help"
        class="flex shrink-0 items-center text-xs tabular-nums"
        :class="deltaClass"
      >
        <UiIcon :name="delta.direction" :size="12" />{{ delta.text }}
        <span class="sr-only">{{ delta.help }}</span>
      </span>
    </div>
    <dl class="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-xs text-gray-500">
      <div v-for="detail in details" :key="detail.label" class="flex gap-1">
        <dt>{{ detail.label }}</dt>
        <dd class="tabular-nums" :class="detail.tone ?? 'text-gray-300'">{{ detail.value }}</dd>
      </div>
    </dl>
    <svg
      v-if="sparkPath"
      class="mt-auto h-7 w-full pt-2"
      viewBox="0 0 100 28"
      preserveAspectRatio="none"
      aria-hidden="true"
    >
      <path
        :d="sparkPath"
        fill="none"
        :stroke="color ?? '#60a5fa'"
        stroke-width="1.5"
        vector-effect="non-scaling-stroke"
      />
    </svg>
  </article>
</template>
