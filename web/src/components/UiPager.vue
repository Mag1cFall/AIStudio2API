<script setup lang="ts">
import { useI18n } from '@/i18n'
import UiIcon from './UiIcon.vue'

// UiPager 是列表底部的翻页条，page 从 1 开始，只有一页时不显示
const page = defineModel<number>({ required: true })
defineProps<{ pages: number }>()
const { t } = useI18n()
</script>

<template>
  <footer
    v-if="pages > 1"
    class="flex items-center gap-2 border-t border-line px-4 py-2 text-xs text-gray-400"
  >
    <span class="mr-auto tabular-nums">{{
      t('common.pageOf').replace('{page}', String(page)).replace('{pages}', String(pages))
    }}</span>
    <button
      type="button"
      class="flex items-center gap-1 rounded-md border border-line px-2 py-1 hover:bg-raised disabled:opacity-40"
      :disabled="page <= 1"
      @click="page--"
    >
      <UiIcon name="chevronLeft" :size="12" />{{ t('usage.previousPage') }}
    </button>
    <button
      type="button"
      class="flex items-center gap-1 rounded-md border border-line px-2 py-1 hover:bg-raised disabled:opacity-40"
      :disabled="page >= pages"
      @click="page++"
    >
      {{ t('usage.nextPage') }}<UiIcon name="chevronRight" :size="12" />
    </button>
  </footer>
</template>
