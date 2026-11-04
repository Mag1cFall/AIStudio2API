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
    <span class="ms-switch-track" />
    <SwitchThumb class="ms-switch-handle" />
  </SwitchRoot>
</template>

<style scoped>
/* 尺寸、颜色与过渡取官网 mat-slide-toggle 与 AI Studio 主题令牌 */
.ms-switch {
  position: relative;
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  width: 34px;
  padding: 0;
  border: none;
  background: none;
  cursor: pointer;
}

.ms-switch:disabled {
  cursor: default;
}

.ms-switch:focus-visible {
  outline: 2px solid var(--color-v3-text-link);
  outline-offset: 2px;
}

.ms-switch-track {
  position: relative;
  width: 100%;
  height: 20px;
  overflow: hidden;
  border-radius: 10px;
}

.ms-switch:disabled .ms-switch-track {
  opacity: 0.12;
}

.ms-switch-track::before,
.ms-switch-track::after {
  position: absolute;
  inset: 0;
  box-sizing: border-box;
  border: 1px solid var(--color-v3-outline);
  border-radius: inherit;
  content: '';
  transition: opacity 75ms;
}

.ms-switch-track::before {
  background: var(--color-v3-surface-container-highest);
}

.ms-switch-track::after {
  border-color: transparent;
  background: var(--color-v3-text);
  opacity: 0;
}

.ms-switch[data-state='checked'] .ms-switch-track::before {
  opacity: 0;
}

.ms-switch[data-state='checked'] .ms-switch-track::after {
  opacity: 1;
}

.ms-switch:disabled .ms-switch-track::before,
.ms-switch:disabled .ms-switch-track::after {
  border-color: var(--mat-sys-on-surface);
}

.ms-switch:disabled .ms-switch-track::before {
  background: var(--mat-sys-surface-variant);
}

.ms-switch:disabled .ms-switch-track::after {
  background: var(--mat-sys-on-surface);
}

.ms-switch-handle {
  position: absolute;
  top: 50%;
  left: 0;
  width: 15px;
  height: 15px;
  margin: 0 3px;
  border-radius: 50%;
  background-color: var(--color-v3-text-var);
  transform: translateY(-50%);
  transition:
    margin 75ms cubic-bezier(0.4, 0, 0.2, 1),
    background-color 75ms cubic-bezier(0.4, 0, 0.2, 1);
}

.ms-switch[data-state='checked'] .ms-switch-handle {
  margin: 0 16px;
  background-color: var(--color-v3-surface);
}

.ms-switch:not(:disabled):active[data-state='unchecked'] .ms-switch-handle {
  background-color: var(--mat-sys-on-surface-variant);
}

.ms-switch:not(:disabled):active[data-state='checked'] .ms-switch-handle {
  background-color: var(--mat-sys-primary-container);
}

.ms-switch:disabled[data-state='unchecked'] .ms-switch-handle {
  background-color: color-mix(in srgb, var(--mat-sys-on-surface) 38%, transparent);
}

.ms-switch:disabled[data-state='checked'] .ms-switch-handle {
  background-color: var(--mat-sys-surface);
}
</style>
