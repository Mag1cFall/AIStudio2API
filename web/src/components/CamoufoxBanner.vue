<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from '@/i18n'
import type { CamoufoxStatus } from '@/types'

const props = defineProps<{ camoufox: CamoufoxStatus }>()
const emit = defineEmits<{ retry: [] }>()
const { t, locale, logText } = useI18n()

// percent 是 Camoufox 发行包的下载百分比，总大小未知时为 null
const percent = computed(() => {
  const download = props.camoufox.download
  return download && download.total > 0
    ? Math.floor((download.received / download.total) * 100)
    : null
})
// sizeText 是已下载与总大小，按界面语言格式化为 MB
const sizeText = computed(() => {
  const download = props.camoufox.download
  if (!download) return ''
  const format = new Intl.NumberFormat(locale.value, {
    style: 'unit',
    unit: 'megabyte',
    maximumFractionDigits: 0,
  })
  const received = format.format(download.received / 1e6)
  return download.total > 0 ? `${received} / ${format.format(download.total / 1e6)}` : received
})
// label 是下载或解压阶段的文字
const label = computed(() =>
  t(props.camoufox.download ? 'camoufox.downloading' : 'camoufox.preparing'),
)
</script>

<template>
  <div
    v-if="camoufox.preparing"
    role="status"
    class="page-x border-b border-blue-500/30 bg-blue-500/10 py-2.5 text-sm text-blue-200 [scrollbar-gutter:stable]"
  >
    <div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
      <span>{{ label }}</span>
      <span v-if="sizeText" class="font-mono text-xs text-blue-300 tabular-nums">
        {{ sizeText }}<template v-if="percent !== null"> · {{ percent }}%</template>
      </span>
    </div>
    <div
      role="progressbar"
      :aria-label="label"
      aria-valuemin="0"
      aria-valuemax="100"
      :aria-valuenow="percent ?? undefined"
      :aria-valuetext="sizeText || undefined"
      class="mt-2 h-1 overflow-hidden rounded-full bg-blue-500/20"
    >
      <div
        v-if="percent !== null"
        class="h-full rounded-full bg-blue-500 transition-[width] duration-200 ease-out"
        :style="{ width: `${percent}%` }"
      />
      <div v-else class="camoufox-indeterminate h-full w-1/3 rounded-full bg-blue-500" />
    </div>
  </div>
  <div
    v-else-if="camoufox.error"
    role="alert"
    class="page-x flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-red-500/30 bg-red-500/10 py-2.5 text-sm text-red-300 [scrollbar-gutter:stable]"
  >
    <p class="min-w-0 flex-1 break-words">
      {{ t('camoufox.failed').replace('{reason}', logText(camoufox.error)) }}
    </p>
    <button
      type="button"
      class="shrink-0 rounded border border-red-500/40 px-3 py-1 text-xs font-medium text-red-200 transition hover:bg-red-500/20"
      @click="emit('retry')"
    >
      {{ t('camoufox.retry') }}
    </button>
  </div>
</template>

<style scoped>
.camoufox-indeterminate {
  animation: camoufox-indeterminate 1.2s ease-in-out infinite;
}
@keyframes camoufox-indeterminate {
  from {
    transform: translateX(-100%);
  }
  to {
    transform: translateX(300%);
  }
}
@media (prefers-reduced-motion: reduce) {
  .camoufox-indeterminate {
    animation: none;
  }
}
</style>
