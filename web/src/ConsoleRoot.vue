<script lang="ts">
import { api, type AdminSession } from '@/api'
import { router } from '@/router'

// initialSession 是页面载入时与路由分块、界面文案同时发出的会话请求
const initialSession = api.session()
// loadStudioLayout 加载 API 试用页布局，首个页面属于 API 试用页时与路由分块同时开始加载
const loadStudioLayout = () => import('./playground/StudioLayout.vue')
if (router.resolve(location.pathname).meta.studio) void loadStudioLayout()
router.beforeEach((to) => {
  if (to.meta.studio) void loadStudioLayout()
})
</script>

<script setup lang="ts">
import { defineAsyncComponent, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from '@/i18n'
import { useConsole } from '@/console'
import App from './App.vue'
import DesktopTitleBar from './components/DesktopTitleBar.vue'
import LoginPage from './components/LoginPage.vue'
import { desktopPlatform, startDesktop } from './desktop'

const StudioLayout = defineAsyncComponent(loadStudioLayout)
const { t, errorText, locale } = useI18n()
const route = useRoute()
const session = ref<AdminSession | null>(null)
const error = ref('')
const { startConsole, stopConsole } = useConsole()

// authenticated 在登录期间保持同一份管理数据与事件订阅
watch(
  () => session.value?.authenticated,
  (authenticated) => {
    if (authenticated) void startConsole()
    else stopConsole()
  },
)

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
async function loadSession(request = api.session()): Promise<void> {
  error.value = ''
  try {
    setSession(await request)
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
  startDesktop(locale, router)
  window.addEventListener('admin-session-expired', expireSession)
  void loadSession(initialSession)
})
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
        <button type="button" class="ml-3 underline" @click="loadSession()">
          {{ t('app.refresh') }}
        </button>
      </div>
      <KeepAlive v-if="session?.authenticated">
        <StudioLayout
          v-if="route.meta.studio"
          :admin-username="session.enabled ? session.username : ''"
          @logout="logout"
        />
        <App v-else :admin-username="session.enabled ? session.username : ''" @logout="logout" />
      </KeepAlive>
      <LoginPage v-else-if="session" @authenticated="setSession" />
      <div v-else class="flex h-full items-center justify-center text-gray-400">
        {{ t('common.loading') }}
      </div>
    </div>
  </div>
</template>
