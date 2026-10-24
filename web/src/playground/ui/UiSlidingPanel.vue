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
withDefaults(defineProps<{ title: string; width?: string }>(), { width: '550px' })

// focusInitial 打开面板时把焦点交给内容中带 autofocus 的元素，没有时保留默认的首个可聚焦元素
function focusInitial(event: Event): void {
  const field = (event.target as HTMLElement).querySelector<HTMLElement>('[autofocus]')
  if (!field) return
  event.preventDefault()
  field.focus()
}
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogPortal>
      <DialogOverlay class="ms-panel-overlay" />
      <DialogContent
        class="ms-panel"
        :style="{ width }"
        :aria-describedby="undefined"
        @open-auto-focus="focusInitial"
      >
        <div class="ms-panel-header">
          <DialogTitle class="ms-panel-title">{{ title }}</DialogTitle>
          <slot name="status" />
          <DialogClose
            type="button"
            ms-button
            class="ms-button-borderless ms-button-icon ms-button-small"
            aria-label="Close panel"
          >
            <UiIcon name="close" :size="20" />
          </DialogClose>
        </div>
        <div class="ms-panel-content"><slot /></div>
        <div v-if="$slots.footer" class="ms-panel-footer"><slot name="footer" /></div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>

<style scoped>
.ms-panel-overlay {
  position: fixed;
  z-index: 900;
  inset: 0;
  backdrop-filter: blur(8px);
  background-color: var(--color-v3-overlay-background);
}

.ms-panel {
  position: fixed;
  z-index: 901;
  top: 0;
  right: 0;
  bottom: 0;
  display: flex;
  flex-direction: column;
  max-width: 100dvw;
  border: 1px solid var(--color-v3-outline-var);
  background-color: var(--color-v3-surface);
  color: var(--color-v3-text);
  box-shadow: var(--shadow-v3-md);
  animation: ms-panel-enter 300ms ease-in-out;
}

.ms-panel[data-state='closed'] {
  animation: ms-panel-exit 300ms ease-in-out forwards;
}

.ms-panel-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 18px 12px 18px 20px;
}

.ms-panel-title {
  flex: 1;
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  line-height: 24px;
}

.ms-panel-content {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
  overflow: auto;
  padding: 0 16px 24px;
  color: var(--mat-sys-on-surface-variant);
  line-height: 17.5px;
}

.ms-panel-footer {
  padding: 0 16px 24px;
}

@keyframes ms-panel-enter {
  from {
    transform: translateX(100%);
  }
}

@keyframes ms-panel-exit {
  to {
    transform: translateX(100%);
  }
}
</style>
