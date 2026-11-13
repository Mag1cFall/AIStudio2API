<script setup lang="ts">
import { computed, inject } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '../UiIcon.vue'
import { usageClock, useUsageFormat } from './format'

// RefreshButton 是用量页的刷新按钮，提示中最新请求的距今时间随用量页时钟每秒更新
const props = defineProps<{
  loading: boolean
  live: boolean
  updatedAt: number
  latestAt: string | undefined
}>()
defineEmits<{ refresh: [] }>()
const { t } = useI18n()
const { clock, ago } = useUsageFormat()
const now = inject(usageClock)!

// tip 是刷新按钮的提示：数据更新时间与账本中最新请求距今多久
const tip = computed(() =>
  [
    t('app.refresh'),
    props.updatedAt > 0 && t('usage.updatedAt').replace('{time}', clock(new Date(props.updatedAt))),
    props.latestAt === undefined
      ? t('usage.latestNone')
      : t('usage.latest').replace('{time}', ago(props.latestAt, now.value)),
  ]
    .filter(Boolean)
    .join(' · '),
)
</script>

<template>
  <button
    v-tooltip="tip"
    type="button"
    class="flex w-10 items-center justify-center text-gray-400 transition hover:bg-raised hover:text-white"
    :class="live ? 'rounded-l' : 'rounded'"
    :aria-label="t('app.refresh')"
    @click="$emit('refresh')"
  >
    <UiIcon name="refresh" :size="15" :class="{ 'animate-spin': loading }" />
  </button>
</template>
