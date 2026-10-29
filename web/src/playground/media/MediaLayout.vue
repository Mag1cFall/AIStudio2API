<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import { useRunSettingsPanel } from '../shortcuts'

// fadeTitle 为真时页头标题与官网音乐、Live 页一样在进入页面时淡入
defineProps<{ title?: string; fadeTitle?: boolean }>()
const { t } = useI18n()

const column = ref<HTMLElement>()
// narrow 与 settingsOpen 是运行设置面板的状态，/ 聚焦页面中第一个可用的输入框
const { narrow, settingsOpen } = useRunSettingsPanel(() => {
  const input = column.value!.querySelector<HTMLTextAreaElement>('textarea:not([disabled])')
  input?.focus()
  return input !== null
})

// closeSettings 关闭运行设置
function closeSettings(): void {
  settingsOpen.value = false
}
</script>

<template>
  <div class="playground">
    <div ref="column" class="chat-column">
      <header class="ms-header">
        <div class="page-title" :class="{ 'title-enter': fadeTitle }">
          <h1 class="mode-title">{{ title || t('nav.playground') }}</h1>
          <slot name="title" />
        </div>
        <div class="toolbar-right">
          <slot name="actions" />
          <button
            v-if="narrow || !settingsOpen"
            v-tooltip.below="t('pg.toggleSettings')"
            type="button"
            ms-button
            class="ms-button-primary ms-button-icon"
            :aria-label="t('pg.toggleSettings')"
            @click="settingsOpen = !settingsOpen"
          >
            <UiIcon name="options" :size="20" />
          </button>
        </div>
      </header>
      <slot />
    </div>
    <div v-if="narrow && settingsOpen" class="settings-overlay" @click="closeSettings"></div>
    <slot name="settings" :open="settingsOpen" :narrow="narrow" :close="closeSettings" />
  </div>
</template>

<style scoped>
.playground {
  position: relative;
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
  background-color: var(--color-v3-surface);
}

.chat-column {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

.toolbar-right {
  display: flex;
  grid-column: 3;
  align-items: center;
  gap: 4px;
  min-height: 32px;
}

.page-title {
  display: flex;
  align-items: center;
  min-height: 24px;
  margin-inline: 12px;
  overflow: hidden;
}

.mode-title {
  margin: 0;
  overflow: hidden;
  font-size: 16px;
  font-weight: 500;
  line-height: 24px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.title-enter {
  animation: fade-in 225ms cubic-bezier(0, 0, 0.2, 1) forwards;
}

.settings-overlay {
  position: fixed;
  z-index: 4;
  inset: 0;
  background-color: var(--color-overlay-background);
}

@keyframes fade-in {
  from {
    opacity: 0;
  }

  to {
    opacity: 1;
  }
}
</style>
