<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'

defineProps<{ title?: string }>()
const { t } = useI18n()

// narrowQuery 与官网一致，960 px 以下运行设置为右侧滑出面板
const narrowQuery = window.matchMedia('(max-width: 960px)')
const narrow = ref(narrowQuery.matches)
const settingsOpen = ref(!narrow.value)
const onNarrowChange = (event: MediaQueryListEvent): void => {
  narrow.value = event.matches
  settingsOpen.value = !event.matches
}

const column = ref<HTMLElement>()

// onKeydown 处理官网快捷键：Ctrl+Shift+S 切换运行设置，/ 聚焦页面主输入框
function onKeydown(event: KeyboardEvent): void {
  if (event.defaultPrevented || document.querySelector('[role="dialog"]')) return
  if ((event.ctrlKey || event.metaKey) && event.shiftKey && event.key.toLowerCase() === 's') {
    event.preventDefault()
    settingsOpen.value = !settingsOpen.value
    return
  }
  const target = event.target as HTMLElement | null
  const editing =
    target !== null &&
    (target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName))
  if (event.key !== '/' || editing || event.ctrlKey || event.metaKey || event.altKey) return
  const input = column.value?.querySelector<HTMLTextAreaElement>('textarea:not([disabled])')
  if (input === null || input === undefined) return
  event.preventDefault()
  input.focus()
}

// closeSettings 关闭运行设置
function closeSettings(): void {
  settingsOpen.value = false
}

onMounted(() => {
  narrowQuery.addEventListener('change', onNarrowChange)
  window.addEventListener('keydown', onKeydown)
})

onUnmounted(() => {
  narrowQuery.removeEventListener('change', onNarrowChange)
  window.removeEventListener('keydown', onKeydown)
})
</script>

<template>
  <div class="playground">
    <div ref="column" class="chat-column">
      <header class="ms-header">
        <div class="page-title">
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

.ms-header {
  position: relative;
  z-index: 2;
  display: grid;
  grid-template-columns: auto 1fr auto;
  align-items: center;
  gap: 4px;
  width: 100%;
  padding: 16px 20px 8px var(--ms-header-padding-left, 20px);
  backdrop-filter: blur(5px);
  background-color: var(--color-v3-overlay-background);
}

.ms-header::after {
  position: absolute;
  right: 0;
  bottom: 0;
  left: 0;
  height: 32px;
  background: linear-gradient(to bottom, var(--color-v3-surface) 0, transparent 100%);
  content: '';
  pointer-events: none;
  transform: translateY(100%);
}

@media screen and (max-width: 480px) {
  .ms-header {
    padding: 16px 8px 8px var(--ms-header-padding-left, 8px);
  }
}

.toolbar-right {
  display: flex;
  grid-column: 3;
  align-items: center;
  gap: 4px;
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

.settings-overlay {
  position: fixed;
  z-index: 4;
  inset: 0;
  background-color: var(--color-overlay-background);
}
</style>
