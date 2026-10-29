<script setup lang="ts">
import '@fontsource-variable/inter'
import '@fontsource-variable/inter-tight'
import '@fontsource-variable/google-sans-code'
import '@/playground/styles/tokens.css'
import '@/playground/styles/base.css'
import { onActivated, onDeactivated, provide, ref, shallowReactive, watch } from 'vue'
import { RouterView, routeLocationKey, useRoute } from 'vue-router'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import CommandPalette, { type PaletteMode } from '@/playground/shell/CommandPalette.vue'
import LeftNav from '@/playground/shell/LeftNav.vue'
import ModelSelector from '@/playground/ModelSelector.vue'
import { useMediaQuery } from '@/playground/media-query'
import { editable } from '@/playground/shortcuts'

defineProps<{ adminUsername: string }>()
const emit = defineEmits<{ logout: [] }>()
const { t, logText } = useI18n()
const { notice } = useConsole()

// studioRoute 在管理端显示期间保留试用页及其子组件的路由
const route = useRoute()
const studioRoute = shallowReactive({ ...route })
provide(routeLocationKey, studioRoute)
watch(
  () => route.fullPath,
  () => {
    if (route.meta.studio) Object.assign(studioRoute, route)
  },
)

// narrow 与官网一致，960 px 以下导航为抽屉
const narrow = useMediaQuery('(max-width: 960px)')
const expanded = ref(!narrow.value)
watch(narrow, (value) => {
  expanded.value = !value
})

// closeDrawer 在窄屏导航跳转后收起抽屉
function closeDrawer(): void {
  if (narrow.value) expanded.value = false
}

const paletteOpen = ref(false)
const paletteMode = ref<PaletteMode>('root')

// openPalette 收起窄屏抽屉并打开命令面板
function openPalette(mode: PaletteMode): void {
  closeDrawer()
  paletteMode.value = mode
  paletteOpen.value = true
}

// onKeydown 处理官网全局快捷键：Ctrl+/ 打开命令面板，Ctrl+Shift+/ 打开快捷键列表；输入框获得焦点时 Ctrl+/ 不打开
function onKeydown(event: KeyboardEvent): void {
  if (event.code !== 'Slash' || !(event.ctrlKey || event.metaKey) || event.altKey) return
  if (!event.shiftKey && editable(event.target)) return
  event.preventDefault()
  openPalette(event.shiftKey ? 'shortcuts' : 'root')
}

onActivated(() => {
  document.documentElement.classList.add('studio')
  document.title = `${t('nav.playground')} | AIStudio2API`
  window.addEventListener('keydown', onKeydown)
})

onDeactivated(() => {
  document.documentElement.classList.remove('studio')
  document.title = 'AIStudio2API'
  window.removeEventListener('keydown', onKeydown)
})
</script>

<template>
  <div class="makersuite-layout">
    <LeftNav
      :expanded="expanded"
      :narrow="narrow"
      :admin-username="adminUsername"
      @toggle="expanded = !expanded"
      @navigate="closeDrawer"
      @logout="emit('logout')"
    />
    <div v-if="narrow && expanded" class="sidebar-overlay" @click="expanded = false"></div>
    <main class="layout-main" :class="{ 'nav-hidden': !expanded }">
      <button
        v-if="!expanded"
        v-tooltip="narrow ? t('nav.open') : t('nav.expand')"
        type="button"
        ms-button
        class="floating-toggle-button ms-button-primary ms-button-icon"
        :aria-label="narrow ? t('nav.open') : t('nav.expand')"
        @click="expanded = true"
      >
        <UiIcon name="menu" :size="20" />
      </button>
      <RouterView
        v-if="route.meta.studio || studioRoute.name === 'prompt' || studioRoute.name === 'saved'"
        :route="studioRoute"
      />
    </main>

    <CommandPalette v-model:open="paletteOpen" v-model:mode="paletteMode" />
    <ModelSelector />

    <div class="notice-stack">
      <Transition name="studio-notice">
        <div v-if="notice.message" role="status" class="callout" :class="`${notice.tone}-callout`">
          <div class="main-content">
            <UiIcon :name="notice.tone === 'error' ? 'error' : 'check'" :size="20" class="icon" />
            <div class="message">{{ logText(notice.message) }}</div>
          </div>
          <button
            type="button"
            ms-button
            class="dismiss-button ms-button-borderless ms-button-icon"
            :aria-label="t('common.close')"
            @click="notice.message = ''"
          >
            <UiIcon name="close" :size="20" />
          </button>
        </div>
      </Transition>
    </div>
  </div>
</template>

<style scoped>
.makersuite-layout {
  display: flex;
  width: 100%;
  height: 100%;
  min-height: 0;
  background: var(--color-v3-surface);
}

.sidebar-overlay {
  position: fixed;
  z-index: 4;
  inset: 0;
  background: var(--color-overlay-background);
}

.layout-main {
  position: relative;
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  background: var(--color-v3-surface);
}

.layout-main.nav-hidden {
  --ms-header-padding-left: 56px;
}

.floating-toggle-button {
  position: absolute;
  z-index: 3;
  top: 16px;
  left: 20px;
}

@media screen and (max-width: 480px) {
  .layout-main.nav-hidden {
    --ms-header-padding-left: 44px;
  }

  .floating-toggle-button {
    left: 8px;
  }
}

.notice-stack {
  position: fixed;
  z-index: 1000;
  top: 42px;
  left: 50%;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  width: min(672px, calc(100vw - 16px));
  transform: translateX(-50%);
  pointer-events: none;
}

.callout {
  position: relative;
  display: flex;
  align-items: center;
  max-width: min(100%, 400px);
  border: 1px solid var(--color-v3-outline);
  border-radius: 6px;
  background: var(--color-v3-surface-container);
  color: var(--color-v3-text);
  pointer-events: auto;
}

:root:not([data-theme='dark']) .callout {
  box-shadow: var(--shadow-v3-lg);
}

.main-content {
  display: flex;
  flex-grow: 1;
  align-items: flex-start;
  min-width: 200px;
  max-height: 20vh;
  margin-right: 4px;
  padding: 12px 40px 12px 12px;
  overflow: auto;
}

.icon {
  margin-right: 12px;
}

.success-callout .icon {
  color: var(--color-v3-accent-4);
}

.error-callout .icon {
  color: var(--color-v3-accent-3);
}

.message {
  font-size: 14px;
  line-height: 20px;
  overflow-wrap: anywhere;
}

.studio-notice-enter-active,
.studio-notice-leave-active {
  transition:
    opacity 160ms ease,
    transform 160ms ease;
}

.studio-notice-enter-from,
.studio-notice-leave-to {
  opacity: 0;
  transform: translateY(-8px);
}

.dismiss-button {
  position: absolute;
  top: 6px;
  right: 6px;
}
</style>
