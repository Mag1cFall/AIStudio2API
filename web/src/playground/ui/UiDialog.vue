<script setup lang="ts">
import {
  DialogClose,
  DialogContent,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
} from 'reka-ui'
import UiIcon from './UiIcon.vue'

const open = defineModel<boolean>('open', { required: true })
withDefaults(defineProps<{ title: string; width?: string; closable?: boolean }>(), {
  width: '560px',
  closable: true,
})
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogPortal>
      <DialogOverlay class="ms-dialog-overlay" />
      <DialogContent class="ms-dialog" :style="{ width }" :aria-describedby="undefined">
        <div class="ms-dialog-header">
          <DialogTitle class="ms-dialog-title">{{ title }}</DialogTitle>
          <span v-if="$slots.header" class="ms-dialog-header-actions"><slot name="header" /></span>
          <DialogClose
            v-if="closable"
            type="button"
            ms-button
            class="ms-button-borderless ms-button-icon"
            aria-label="Close"
          >
            <UiIcon name="close" :size="20" />
          </DialogClose>
        </div>
        <div class="ms-dialog-content"><slot /></div>
        <div v-if="$slots.actions" class="ms-dialog-actions"><slot name="actions" /></div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>

<style scoped>
.ms-dialog-overlay {
  position: fixed;
  z-index: 900;
  inset: 0;
}

.ms-dialog {
  position: fixed;
  z-index: 901;
  top: 50%;
  left: 50%;
  display: flex;
  flex-direction: column;
  max-width: 80vw;
  max-height: calc(100dvh - 48px);
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background-color: var(--color-v3-surface-container);
  box-shadow: var(--shadow-v3-md);
  color: var(--color-v3-text);
  transform: translate(-50%, -50%);
  animation: ms-dialog-enter 150ms cubic-bezier(0, 0, 0.2, 1);
}

@media screen and (max-width: 600px) {
  .ms-dialog {
    max-width: calc(100vw - 32px);
  }
}

.ms-dialog-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 24px 16px 16px;
}

.ms-dialog-header-actions {
  display: flex;
  margin-right: -8px;
  margin-left: auto;
}

.ms-dialog-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  line-height: 24px;
}

.ms-dialog-content {
  min-height: 0;
  overflow: auto;
  padding: 0 16px;
  color: var(--color-v3-text);
}

.ms-dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 24px 16px;
}

@keyframes ms-dialog-enter {
  from {
    opacity: 0;
    transform: translate(-50%, -50%) scale(0.8);
  }
}
</style>
