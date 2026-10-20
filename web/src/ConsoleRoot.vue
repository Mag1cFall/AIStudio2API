<script setup lang="ts">
import { defineAsyncComponent, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, type AdminSession } from '@/api'
import { useI18n } from '@/i18n'
import App from './App.vue'
import DesktopTitleBar from './components/DesktopTitleBar.vue'
import LoginPage from './components/LoginPage.vue'
import { desktopPlatform, startDesktop } from './desktop'

const StudioLayout = defineAsyncComponent(() => import('./playground/StudioLayout.vue'))
const { t, errorText, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const session = ref<AdminSession | null>(null)
const error = ref('')

// setSession 根据会话切换独立登录页与管理控制台，保留地址栏中的页面状态，API 试用页保留原路径
function setSession(value: AdminSession): void {
  session.value = value
  if (!value.authenticated) void router.replace({ path: '/login', query: route.query })
  else if (route.path === '/login') void router.replace({ path: '/', query: route.query })
}

// expireSession 卸载控制台及其事件订阅
function expireSession(): void {
  setSession({ enabled: true, authenticated: false, username: '' })
}

// loadSession 在加载管理数据前确认会话
async function loadSession(): Promise<void> {
  error.value = ''
  try {
    setSession(await api.session())
  } catch (cause) {
    error.value = errorText(cause)
  }
}

// logout 撤销当前会话并返回登录页
async function logout(): Promise<void> {
  try {
    await api.logout()
    expireSession()
  } catch (cause) {
    error.value = errorText(cause)
  }
}

onMounted(() => {
  document.title = 'AIStudio2API'
  startDesktop(locale)
  window.addEventListener('admin-session-expired', expireSession)
  void loadSession()
})
onUnmounted(() => window.removeEventListener('admin-session-expired', expireSession))
</script>

<template>
  <div class="flex h-full w-full flex-col">
    <DesktopTitleBar v-if="desktopPlatform" />
    <div class="min-h-0 flex-1">
      <div
        v-if="error"
        role="alert"
        class="fixed top-4 right-4 z-50 max-w-sm rounded border border-red-500/40 bg-panel p-4 text-red-300"
      >
        {{ error }}
        <button type="button" class="ml-3 underline" @click="loadSession">
          {{ t('app.refresh') }}
        </button>
      </div>
      <template v-if="session?.authenticated">
        <StudioLayout
          v-if="route.meta.studio"
          :admin-username="session.enabled ? session.username : ''"
          @logout="logout"
        />
        <App v-else :admin-username="session.enabled ? session.username : ''" @logout="logout" />
      </template>
      <LoginPage v-else-if="session" @authenticated="setSession" />
      <div v-else class="flex h-full items-center justify-center text-gray-400">
        {{ t('common.loading') }}
      </div>
    </div>
  </div>
</template>
