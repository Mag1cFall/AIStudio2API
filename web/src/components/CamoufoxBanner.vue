<script setup lang="ts">
import { computed } from 'vue'
import { useDownloadProgress } from '@/camoufox'
import { useI18n } from '@/i18n'
import type { PreparationStatus } from '@/types'
import CamoufoxProgressBar from './CamoufoxProgressBar.vue'

const props = defineProps<{ camoufox: PreparationStatus }>()
const emit = defineEmits<{ retry: [] }>()
const { t, logText } = useI18n()
const { percent, sizeText } = useDownloadProgress(() => props.camoufox)
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
    <CamoufoxProgressBar class="mt-2" :label="label" :percent="percent" :size-text="sizeText" />
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
