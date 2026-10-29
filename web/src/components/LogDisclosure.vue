<script setup lang="ts">
import { useI18n } from '@/i18n'
import UiIcon from './UiIcon.vue'

const open = defineModel<boolean>({ required: true })
const { t } = useI18n()
</script>

<template>
  <button
    class="log-disclosure"
    type="button"
    :aria-expanded="open"
    :aria-label="t('logs.details')"
    @click="open = !open"
  >
    <UiIcon name="chevronRight" :size="12" />
  </button>
</template>

<style scoped>
.log-disclosure {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  margin: -2px 0 -2px -4px;
  border-radius: 4px;
  color: var(--color-gray-500);
  transition:
    background-color 120ms ease,
    color 120ms ease;
}
.log-disclosure:hover {
  color: var(--color-fg);
  background: color-mix(in oklab, var(--color-line) 60%, transparent);
}
.log-disclosure:focus-visible {
  outline: 1px solid var(--color-blue-400);
  outline-offset: 1px;
}
.log-disclosure :deep(svg) {
  transition: transform 120ms ease;
}
.log-disclosure[aria-expanded='true'] :deep(svg) {
  transform: rotate(90deg);
}
</style>
