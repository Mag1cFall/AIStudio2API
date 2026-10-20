<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { RouterLink, useRoute, type RouteLocationRaw } from 'vue-router'
import type { TranslationKey } from '@/i18n'
import { useI18n } from '@/i18n'
import type { IconName } from '@/playground/ui/icons'
import UiIcon from '@/playground/ui/UiIcon.vue'
import AccountButton from '@/playground/shell/AccountButton.vue'
import SettingsMenu from '@/playground/shell/SettingsMenu.vue'
import { useConsole } from '@/console'

defineProps<{ expanded: boolean; narrow: boolean; adminUsername: string }>()
const emit = defineEmits<{ toggle: []; navigate: []; logout: []; search: [] }>()
const { t } = useI18n()
const route = useRoute()
const { update } = useConsole()

// NavEntry 为导航中的一项，tab 为管理端主页面的页签，playground 为本页
interface NavEntry {
  tab: string
  label: TranslationKey
  icon: IconName
}

// entries 为导航，条目与顺序和管理端主页面一致
const entries: NavEntry[] = [
  { tab: 'logs', label: 'nav.logs', icon: 'logs' },
  { tab: 'accounts', label: 'nav.accounts', icon: 'accounts' },
  { tab: 'models', label: 'nav.models', icon: 'models' },
  { tab: 'requests', label: 'nav.requests', icon: 'requests' },
  { tab: 'usage', label: 'nav.usage', icon: 'usage' },
  { tab: 'settings', label: 'nav.settings', icon: 'settings' },
  { tab: 'playground', label: 'nav.playground', icon: 'playground' },
  { tab: 'version', label: 'nav.version', icon: 'version' },
]

// playgroundRoutes 是官网导航中高亮 Playground 的页面，音乐页不高亮
const playgroundRoutes = new Set(['prompt', 'saved', 'video', 'image', 'speech', 'live'])

// active 判断入口是否对应当前页面
function active(entry: NavEntry): boolean {
  return entry.tab === 'playground' && playgroundRoutes.has(String(route.name))
}

// target 返回入口的地址，API 试用进入新对话
function target(entry: NavEntry): RouteLocationRaw {
  return entry.tab === 'playground' ? '/prompts/new_chat' : consoleTab(entry.tab)
}

// consoleTab 返回管理端主页面指定页签的地址
function consoleTab(tab: string): RouteLocationRaw {
  return { path: '/', query: { tab } }
}
</script>

<template>
  <div
    class="nav-content"
    :class="[expanded ? 'expanded' : 'collapsed', { narrow }]"
    :inert="!expanded && narrow"
  >
    <div class="navbar-header">
      <RouterLink
        v-tooltip="'AIStudio2API'"
        to="/prompts/new_chat"
        ms-button
        class="logo-wrapper ms-button-borderless"
        aria-label="AIStudio2API"
        @click="emit('navigate')"
      >
        <img src="/favicon.svg" alt="" class="logo-icon" />
        <span class="logo-text">AIStudio2API</span>
      </RouterLink>
      <button
        v-tooltip="t('nav.collapse')"
        type="button"
        ms-button
        class="navbar-toggle-button ms-button-borderless ms-button-icon"
        :aria-label="t('nav.collapse')"
        @click="emit('toggle')"
      >
        <UiIcon name="menuOpen" :size="20" />
      </button>
    </div>
    <nav>
      <div class="nav-items">
        <RouterLink
          v-for="entry in entries"
          :key="entry.tab"
          :to="target(entry)"
          ms-button
          class="ms-button-borderless"
          :class="{ active: active(entry) }"
          :aria-current="active(entry) ? 'page' : undefined"
          @click="emit('navigate')"
        >
          <UiIcon :name="entry.icon" :size="20" class="ms-button-icon-symbol" />
          <span class="nav-item-main-text">{{ t(entry.label) }}</span>
        </RouterLink>
      </div>
      <div class="empty-space"></div>
      <div class="bottom-actions">
        <div class="icon-button-row">
          <div class="icon-box">
            <RouterLink
              v-tooltip="t('nav.whatsNew')"
              :to="consoleTab('version')"
              ms-button
              class="ms-button-borderless ms-button-icon"
              :aria-label="t('nav.whatsNew')"
              @click="emit('navigate')"
            >
              <UiIcon name="notifications" :size="20" />
              <span v-if="update?.available" class="status-badge" aria-hidden="true"></span>
            </RouterLink>
          </div>
          <div class="icon-box">
            <SettingsMenu :admin-username="adminUsername" @logout="emit('logout')" />
          </div>
          <div class="icon-box">
            <button
              v-tooltip="t('pg.searchShortcut')"
              type="button"
              ms-button
              class="ms-button-borderless ms-button-icon"
              :aria-label="t('pg.searchShortcut')"
              @click="emit('search')"
            >
              <UiIcon name="search" :size="20" />
            </button>
          </div>
          <div class="icon-box">
            <RouterLink
              v-tooltip="t('nav.apiKey')"
              :to="consoleTab('settings')"
              ms-button
              class="ms-button-borderless ms-button-icon"
              :aria-label="t('nav.apiKey')"
              @click="emit('navigate')"
            >
              <UiIcon name="key" :size="20" />
            </RouterLink>
          </div>
        </div>
        <AccountButton :admin-username="adminUsername" @logout="emit('logout')" />
      </div>
    </nav>
  </div>
</template>

<style scoped>
.nav-content {
  z-index: 5;
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden auto;
  padding: 0 8px;
  border-right: 1px solid var(--color-v3-outline-var);
  background-color: var(--color-v3-surface-left-nav);
  user-select: none;
  transition:
    width 0.2s cubic-bezier(0.4, 0, 0.2, 0),
    opacity 0.2s cubic-bezier(0.4, 0, 0.2, 0),
    padding 0.2s cubic-bezier(0.4, 0, 0.2, 0);
}

.nav-content.expanded {
  width: 220px;
}

.nav-content.collapsed {
  width: 0;
  padding: 0;
}

.nav-content.narrow {
  position: fixed;
  top: 0;
  bottom: 0;
  left: 0;
  width: 220px;
  padding: 0 8px;
  background-color: var(--color-v3-surface-container);
  transform: translate(-100%);
  transition:
    transform 0.2s ease-out,
    opacity 0.2s ease-out;
}

.nav-content.narrow.expanded {
  box-shadow: var(--shadow-v3-xl);
  opacity: 1;
  transform: translate(0);
  transition:
    transform 0.2s ease-in,
    opacity 0.2s ease-in;
}

.navbar-header {
  position: sticky;
  top: 0;
  z-index: 2;
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 16px 0;
  background: inherit;
}

.logo-wrapper {
  justify-content: start;
  width: 100%;
  min-width: 0;
  padding: 0 8px;
  color: inherit;
}

.logo-icon {
  width: 22px;
  height: 22px;
}

.logo-text {
  overflow: hidden;
  font-family: var(--font-heading);
  font-size: 17px;
  font-weight: 500;
  line-height: 22px;
  letter-spacing: -0.2px;
  text-overflow: ellipsis;
}

.navbar-toggle-button {
  flex-shrink: 0;
  margin-left: auto;
  color: var(--color-v3-text-var);
}

nav {
  display: flex;
  flex: 1 0;
  flex-direction: column;
  background: inherit;
}

.nav-items {
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.nav-items [ms-button] {
  justify-content: start;
  width: 100%;
  height: auto;
  min-height: var(--ms-button-height);
  padding: 4px 8px;
  border-radius: 12px;
}

.nav-items [ms-button] .ms-button-icon-symbol {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 20px;
}

.nav-items [ms-button]:not(.active) {
  color: var(--color-v3-text-var);
}

.nav-items [ms-button]:hover:not(.active) {
  background: var(--color-nav-item-hover);
  color: var(--color-v3-text);
}

.nav-items [ms-button].active {
  background: var(--color-nav-item-active);
  color: var(--color-v3-text);
  font-weight: 500;
}

.nav-item-main-text {
  flex: 1;
  min-width: 0;
  text-align: left;
  white-space: normal;
  overflow-wrap: anywhere;
  word-break: auto-phrase;
}

.empty-space {
  flex: 1;
}

.bottom-actions {
  position: sticky;
  bottom: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding-bottom: 16px;
  background: inherit;
}

.icon-button-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.icon-box {
  display: flex;
  flex: 1 1 0;
  align-items: center;
  justify-content: center;
  min-width: 0;
  height: 32px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 8px;
  color: var(--color-v3-text-var);
  cursor: pointer;
  transition: background-color 0.15s ease;
}

.icon-box:hover {
  background-color: var(--color-v3-hover);
}

.icon-box :deep([ms-button]) {
  position: relative;
  width: 100%;
  min-width: unset;
  height: 100%;
  padding: 0;
  border: none;
  background: none;
  box-shadow: none;
  color: inherit;
}

.status-badge {
  position: absolute;
  top: 4px;
  left: calc(50% + 3px);
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background-color: var(--color-v3-accent-4);
}
</style>
