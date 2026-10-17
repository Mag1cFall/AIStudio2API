<script setup lang="ts">
import { useI18n } from '@/i18n'
import { useTheme } from '@/theme'
import UiIcon from './UiIcon.vue'

const { t } = useI18n()
const { theme, toggleTheme } = useTheme()

// toggle 以按钮中心为圆心切换配色模式
function toggle(event: MouseEvent): void {
  const rect = (event.currentTarget as HTMLElement).getBoundingClientRect()
  toggleTheme({ x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 })
}
</script>

<template>
  <button
    v-tooltip="t(theme === 'dark' ? 'app.lightMode' : 'app.darkMode')"
    type="button"
    :aria-label="t(theme === 'dark' ? 'app.lightMode' : 'app.darkMode')"
    class="theme-toggle relative flex h-6 w-6 shrink-0 items-center justify-center overflow-hidden rounded border border-gray-700 text-gray-500 transition hover:text-white"
    :data-current="theme"
    @click="toggle"
  >
    <UiIcon name="sun" :size="13" class="theme-icon theme-sun" />
    <UiIcon name="moon" :size="13" class="theme-icon theme-moon" />
  </button>
</template>

<style scoped>
.theme-icon {
  position: absolute;
  transition:
    transform 320ms cubic-bezier(0.34, 1.56, 0.64, 1),
    opacity 200ms ease;
}

.theme-toggle[data-current='dark'] .theme-moon,
.theme-toggle[data-current='light'] .theme-sun {
  opacity: 0;
  transform: rotate(90deg) scale(0.4);
}

@media (prefers-reduced-motion: reduce) {
  .theme-icon {
    transition: none;
  }
}
</style>
