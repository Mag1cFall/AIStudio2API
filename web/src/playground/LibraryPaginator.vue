<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from '@/i18n'

const props = defineProps<{
  start: number
  end: number
  total?: number | undefined
  previous: boolean
  next: boolean
}>()
defineEmits<{ previous: []; next: [] }>()

const { t, locale } = useI18n()

// range 与官网分页器一样写出当前页的起止序号与总数，还有未读取的页时不写总数
const range = computed(() => {
  const number = (value: number) => value.toLocaleString(locale.value)
  const text =
    props.total === undefined
      ? t('pg.historyRangeMany')
      : t('pg.historyRange').replace('{total}', number(props.total))
  return text.replace('{start}', number(props.start)).replace('{end}', number(props.end))
})
</script>

<template>
  <div role="group" class="library-paginator">
    <div class="range-label" role="status" aria-live="polite" aria-atomic="true">
      {{ range }}
    </div>
    <button
      v-tooltip="previous ? t('pg.historyPreviousPage') : ''"
      type="button"
      ms-button
      class="ms-button-borderless ms-button-icon"
      :aria-label="t('pg.historyPreviousPage')"
      :disabled="!previous"
      @click="$emit('previous')"
    >
      <svg viewBox="0 0 24 24" focusable="false" aria-hidden="true">
        <path d="M15.41 7.41L14 6l-6 6 6 6 1.41-1.41L10.83 12z" />
      </svg>
    </button>
    <button
      v-tooltip="next ? t('pg.historyNextPage') : ''"
      type="button"
      ms-button
      class="ms-button-borderless ms-button-icon"
      :aria-label="t('pg.historyNextPage')"
      :disabled="!next"
      @click="$emit('next')"
    >
      <svg viewBox="0 0 24 24" focusable="false" aria-hidden="true">
        <path d="M10 6L8.59 7.41 13.17 12l-4.58 4.59L10 18l6-6z" />
      </svg>
    </button>
  </div>
</template>

<style scoped>
.library-paginator {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  align-items: center;
  min-height: 56px;
  padding: 0 8px;
  color: var(--mat-sys-on-surface);
  font-size: 12px;
  line-height: 16px;
  letter-spacing: 0.006rem;
}

.range-label {
  margin: 0 32px 0 24px;
}

.library-paginator [ms-button] {
  --ms-button-height-override: 40px;
}

.library-paginator svg {
  width: 28px;
  fill: var(--mat-sys-on-surface-variant);
}

.library-paginator [ms-button]:disabled svg {
  fill: color-mix(in srgb, var(--mat-sys-on-surface) 38%, transparent);
}
</style>
