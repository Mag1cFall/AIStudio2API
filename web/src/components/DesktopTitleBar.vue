<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { desktopPlatform, sendDesktop } from '@/desktop'
import { useI18n } from '@/i18n'
import UiIcon from './UiIcon.vue'

const { t } = useI18n()
const mac = desktopPlatform === 'darwin'
const maximised = ref(false)

// updateMaximised 按窗口是否占满可用屏幕区域切换最大化与还原按钮
function updateMaximised(): void {
  maximised.value =
    window.outerWidth >= window.screen.availWidth && window.outerHeight >= window.screen.availHeight
}

onMounted(() => {
  updateMaximised()
  window.addEventListener('resize', updateMaximised)
})
onUnmounted(() => window.removeEventListener('resize', updateMaximised))
</script>

<template>
  <header
    class="desktop-drag flex h-9 shrink-0 items-center border-b border-line bg-panel select-none"
    @dblclick="mac || sendDesktop('toggle-maximise')"
  >
    <div v-if="!mac" class="desktop-no-drag ml-auto flex h-full">
      <button
        type="button"
        :aria-label="t('window.minimise')"
        class="flex h-full w-11 items-center justify-center text-gray-400 transition [--wails-non-client-region:minimize] hover:bg-raised hover:text-white"
        @click="sendDesktop('minimise')"
      >
        <UiIcon name="windowMinimise" :size="14" />
      </button>
      <button
        type="button"
        :aria-label="maximised ? t('window.restore') : t('window.maximise')"
        class="flex h-full w-11 items-center justify-center text-gray-400 transition [--wails-non-client-region:maximize] hover:bg-raised hover:text-white"
        @click="sendDesktop('toggle-maximise')"
      >
        <UiIcon :name="maximised ? 'windowRestore' : 'windowMaximise'" :size="12" />
      </button>
      <button
        type="button"
        :aria-label="t('window.close')"
        class="flex h-full w-11 items-center justify-center text-gray-400 transition [--wails-non-client-region:close] hover:bg-[#c42b1c] hover:text-[#fff] active:bg-[#c42b1c]/90"
        @click="sendDesktop('close')"
      >
        <UiIcon name="close" :size="15" />
      </button>
    </div>
  </header>
</template>

<style scoped>
.desktop-drag {
  --wails-draggable: drag;
  --wails-non-client-region: caption;
}

.desktop-no-drag {
  --wails-draggable: no-drag;
}
</style>
