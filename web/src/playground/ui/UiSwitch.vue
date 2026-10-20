<script setup lang="ts">
import { SwitchRoot, SwitchThumb } from 'reka-ui'

const model = defineModel<boolean>({ required: true })
defineProps<{ id?: string; disabled?: boolean; label?: string }>()
</script>

<template>
  <SwitchRoot
    v-bind="id === undefined ? {} : { id }"
    v-model="model"
    :disabled="disabled"
    :aria-label="label"
    class="ms-switch"
  >
    <SwitchThumb class="ms-switch-handle" />
  </SwitchRoot>
</template>

<style scoped>
.ms-switch {
  position: relative;
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  width: 34px;
  height: 20px;
  padding: 0;
  border: 1px solid var(--color-v3-outline);
  border-radius: 10px;
  background-color: var(--color-v3-surface-container-highest);
  cursor: pointer;
  transition: background-color 75ms linear;
}

.ms-switch[data-state='checked'] {
  border-color: var(--color-v3-text);
  background-color: var(--color-v3-text);
}

.ms-switch:disabled {
  opacity: 0.38;
  cursor: default;
}

.ms-switch:focus-visible {
  outline: 2px solid var(--color-v3-text-link);
  outline-offset: 2px;
}

.ms-switch-handle {
  display: block;
  width: 15px;
  height: 15px;
  margin-left: 2px;
  border-radius: 50%;
  background-color: var(--color-v3-text-var);
  transition:
    transform 150ms cubic-bezier(0.4, 0, 0.2, 1),
    background-color 75ms linear;
}

.ms-switch[data-state='checked'] .ms-switch-handle {
  background-color: var(--color-v3-surface);
  transform: translateX(13px);
}
</style>
