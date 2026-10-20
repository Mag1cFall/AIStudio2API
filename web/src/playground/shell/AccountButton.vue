<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { PopoverClose, PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import { computed } from 'vue'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'

const props = defineProps<{ adminUsername: string }>()
const emit = defineEmits<{ logout: [] }>()
const { t } = useI18n()
const { status, serviceState, startPending, stopPending, startService, stopService } = useConsole()
const version = computed(() => status.value?.version || '—')
// avatarPalette 是 Google 账户默认头像的底色
const avatarPalette = ['#1a73e8', '#e8710a', '#188038', '#d93025', '#9334e6', '#e52592', '#12b5cb']
// name 是账户按钮与面板显示的名称，管理登录关闭时为产品名
const name = computed(() => props.adminUsername || 'AIStudio2API')
// avatar 是按名称首字母生成的头像文字与底色
const avatar = computed(() => ({
  letter: name.value.charAt(0).toUpperCase(),
  color: avatarPalette[name.value.charCodeAt(0) % avatarPalette.length],
}))
</script>

<template>
  <PopoverRoot>
    <PopoverTrigger
      v-tooltip="`${t('app.status')}: ${serviceState.toUpperCase()}`"
      ms-button
      class="account-switcher-button ms-button-primary"
      :aria-label="`${t('app.status')}: ${serviceState.toUpperCase()}`"
    >
      <span class="avatar" :style="{ backgroundColor: avatar.color }" aria-hidden="true">{{
        avatar.letter
      }}</span>
      <span class="account-switcher-text">{{ name }}</span>
      <span class="navbar-badge"
        ><span class="status-dot" :class="serviceState"></span>{{ serviceState }}</span
      >
    </PopoverTrigger>
    <PopoverPortal>
      <PopoverContent side="top" align="start" :side-offset="8" class="account-panel">
        <div class="panel-top">
          <span class="panel-product">AIStudio2API</span>
          <PopoverClose
            ms-button
            class="panel-close ms-button-borderless ms-button-icon"
            :aria-label="t('common.close')"
          >
            <UiIcon name="close" :size="20" />
          </PopoverClose>
        </div>
        <div class="panel-header">
          <span
            class="avatar large"
            :style="{ backgroundColor: avatar.color }"
            aria-hidden="true"
            >{{ avatar.letter }}</span
          >
          <div class="panel-title">
            <div class="panel-name">{{ name }}</div>
            <div class="panel-detail">{{ version }} · {{ serviceState.toUpperCase() }}</div>
          </div>
        </div>
        <button
          v-if="serviceState === 'stopped'"
          type="button"
          ms-button
          class="ms-button-as ms-button-large ms-button-secondary"
          :disabled="startPending || stopPending"
          @click="startService"
        >
          <UiIcon v-if="startPending" name="spinner" :size="18" />
          {{ t('app.start') }}
        </button>
        <button
          v-else-if="serviceState !== 'unavailable'"
          type="button"
          ms-button
          class="ms-button-as ms-button-large ms-button-primary"
          :disabled="stopPending"
          @click="stopService"
        >
          <UiIcon v-if="stopPending" name="spinner" :size="18" />
          {{ t('app.stop') }}
        </button>
        <button
          v-if="adminUsername"
          type="button"
          ms-button
          class="ms-button-as ms-button-large ms-button-primary"
          @click="emit('logout')"
        >
          {{ t('auth.logout') }}
        </button>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>

<style scoped>
.account-switcher-button {
  display: flex;
  align-items: center;
  justify-content: start;
  gap: 8px;
  width: 100%;
  height: auto;
  margin-top: 4px;
  padding: 8px 12px;
  border-color: var(--color-v3-outline-var);
  background-color: var(--color-v3-surface-container);
  color: var(--color-v3-text-var);
}

.account-switcher-button:hover {
  background-color: var(--color-v3-hover);
}

.account-switcher-text {
  display: block;
  min-width: 0;
  overflow: hidden;
  color: var(--color-v3-text-var);
  font-size: 12px;
  font-weight: 400;
  line-height: 26px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.navbar-badge {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 6px;
  margin-left: auto;
  padding: 4px 8px;
  border-radius: 8px;
  background-color: var(--color-v3-button-container);
  font-size: 12px;
  font-weight: 400;
  line-height: 18px;
  text-transform: uppercase;
}

.avatar {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  color: #fff;
  font-size: 13px;
  font-weight: 500;
  line-height: 1;
}

.avatar.large {
  width: 40px;
  height: 40px;
  font-size: 18px;
}

.status-dot {
  flex-shrink: 0;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background-color: var(--color-v3-text-disable);
}

.status-dot.running {
  background-color: var(--color-v3-accent-4);
}

.status-dot.launching {
  background-color: var(--color-v3-accent-1);
  animation: status-pulse 1.2s ease-in-out infinite;
}

.status-dot.unavailable {
  background-color: var(--color-v3-accent-3);
}

@keyframes status-pulse {
  50% {
    opacity: 0.4;
  }
}

.panel-top {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  height: 32px;
}

.panel-product {
  font-family: var(--font-heading);
  font-size: 20px;
  font-weight: 500;
  line-height: 28px;
  letter-spacing: -0.2px;
}

.panel-close {
  position: absolute;
  right: -8px;
  color: var(--color-v3-text-var);
}

.panel-header {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 4px 0 8px;
}

.panel-name {
  font-size: 14px;
  font-weight: 500;
}

.panel-detail {
  color: var(--color-v3-text-var);
  font-size: 12px;
  letter-spacing: 0.2px;
  line-height: 18px;
}

.account-panel [ms-button].ms-button-as {
  width: 100%;
  border-radius: 9999px;
}

.account-panel [ms-button].ms-button-as.ms-button-primary {
  border: 1px solid var(--color-v3-outline);
  background-color: transparent;
  color: var(--color-v3-text-on-button);
}

.account-panel [ms-button].ms-button-as.ms-button-primary:hover {
  background-color: var(--color-v3-hover);
}

.account-panel [ms-button].ms-button-as.ms-button-secondary {
  border: none;
  background-color: var(--color-v3-text-on-button);
  color: var(--color-v3-surface);
}

.account-panel [ms-button].ms-button-as.ms-button-secondary:hover {
  background-color: var(--color-v3-text-var);
}
</style>

<style>
.account-panel {
  position: relative;
  z-index: 1000;
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 295px;
  padding: 16px 20px 20px;
  border-radius: 20px;
  background: color-mix(in srgb, var(--color-v3-surface-container-high) 50%, transparent);
  box-shadow: 0 16px 32px -8px rgb(0 0 0 / 40%);
  backdrop-filter: blur(80px);
  transform-origin: bottom left;
  animation: account-switcher-open 0.2s cubic-bezier(0.2, 0, 0, 1) forwards;
}

@keyframes account-switcher-open {
  from {
    opacity: 0;
    transform: scale(0.95) translateY(-4px);
  }

  to {
    opacity: 1;
    transform: scale(1) translateY(0);
  }
}
</style>
