<script setup lang="ts">
defineProps<{ adminUsername?: string }>()
const emit = defineEmits<{ logout: [] }>()
import { computed, defineAsyncComponent, h, onMounted, ref, useTemplateRef, watch } from 'vue'
import type { AsyncComponentLoader } from 'vue'
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
import { useConsole } from '@/console'
import { useI18n, type TranslationKey } from '@/i18n'
import type { TabID } from '@/types'
import AccountsPanel from '@/components/AccountsPanel.vue'
import UiDisclosure from '@/components/UiDisclosure.vue'
import LogsPanel from '@/components/LogsPanel.vue'
import ModelsTable from '@/components/ModelsTable.vue'
import RequestsPanel from '@/components/RequestsPanel.vue'
import SettingsPanel from '@/components/SettingsPanel.vue'
import ThemeToggle from '@/components/ThemeToggle.vue'
import UiIcon from '@/components/UiIcon.vue'
import { useClampToggle } from '@/clamp'
import { afterTask } from '@/lazy'
import { readPreference, writePreference } from '@/preference'
import { useTabParam } from '@/url'
import { useRoute, useRouter } from 'vue-router'
import type { IconName } from '@/icons'

const { availableLocales, locale, setLocale, t, logText } = useI18n()
// lazyPanel 创建按需加载的页面组件
function lazyPanel(loader: AsyncComponentLoader) {
  return defineAsyncComponent({
    loader: () => loader().then(afterTask),
    delay: 150,
    loadingComponent: {
      render: () =>
        h('p', { class: 'p-8 text-sm text-gray-500', role: 'status' }, t('app.panelLoading')),
    },
    errorComponent: {
      render: () =>
        h('p', { class: 'p-8 text-sm text-red-300', role: 'alert' }, t('app.panelFailed')),
    },
  })
}
const UsagePanel = lazyPanel(() => import('@/components/UsagePanel.vue'))
const VersionPanel = lazyPanel(() => import('@/components/VersionPanel.vue'))
const AccessPanel = lazyPanel(() => import('@/components/AccessPanel.vue'))
const validTabs: TabID[] = [
  'logs',
  'accounts',
  'access',
  'models',
  'requests',
  'usage',
  'settings',
  'version',
]
const { tab: currentTab, select: selectTab } = useTabParam(validTabs, 'logs')
const router = useRouter()
const route = useRoute()
// currentTab 在从试用页返回时同步目标管理页面
watch(
  () => route.fullPath,
  () => {
    if (route.name === 'console') {
      const tab = route.query.tab as TabID
      currentTab.value = validTabs.includes(tab) ? tab : 'logs'
    }
  },
)

// openTab 切换页面，API 试用进入独立的试用页
function openTab(tab: TabID): void {
  if (tab === 'playground') void router.push('/prompts/new_chat')
  else selectTab(tab)
}
const navigationBar = ref<HTMLElement | null>(null)

// wideQuery 匹配导航纵向排列的宽屏
const wideQuery = window.matchMedia('(min-width: 48rem)')

// revealCurrentTab 把窄屏横向导航的当前项横向滚入视野
function revealCurrentTab(): void {
  if (wideQuery.matches) return
  const bar = navigationBar.value!
  const item = bar.querySelector('[aria-current="page"]')!
  const barRect = bar.getBoundingClientRect()
  const itemRect = item.getBoundingClientRect()
  if (itemRect.left < barRect.left) bar.scrollLeft -= barRect.left - itemRect.left
  else if (itemRect.right > barRect.right) bar.scrollLeft += itemRect.right - barRect.right
}
watch(currentTab, revealCurrentTab, { flush: 'post' })

// defaultSidebarWidth 是宽屏侧栏的默认宽度，双击拖动条恢复
const defaultSidebarWidth = 256
const minSidebarWidth = 176
const maxSidebarWidth = 360
const sidebarWidth = ref(
  readPreference('app.sidebarWidth', defaultSidebarWidth, (value) => typeof value === 'number'),
)
const resizingSidebar = ref(false)
let sidebarResizeStartX = 0
let sidebarResizeStartWidth = 0
watch(sidebarWidth, (value) => writePreference('app.sidebarWidth', value))

function clampSidebarWidth(value: number): number {
  return Math.round(Math.min(maxSidebarWidth, Math.max(minSidebarWidth, value)))
}
sidebarWidth.value = clampSidebarWidth(sidebarWidth.value)

function startSidebarResize(event: PointerEvent): void {
  if (event.button !== 0) return
  const handle = event.currentTarget as HTMLElement
  handle.setPointerCapture(event.pointerId)
  sidebarResizeStartX = event.clientX
  sidebarResizeStartWidth = sidebarWidth.value
  resizingSidebar.value = true
  event.preventDefault()
}

function moveSidebarResize(event: PointerEvent): void {
  if (!resizingSidebar.value) return
  sidebarWidth.value = clampSidebarWidth(
    sidebarResizeStartWidth + event.clientX - sidebarResizeStartX,
  )
}

function resizeSidebarWithKeyboard(event: KeyboardEvent): void {
  if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
  event.preventDefault()
  sidebarWidth.value = clampSidebarWidth(
    sidebarWidth.value + (event.key === 'ArrowRight' ? 16 : -16),
  )
}
const {
  status,
  logs,
  accounts,
  accountTasks,
  visibleAccounts,
  models,
  cooldowns,
  requests,
  config,
  update,
  loading,
  errors,
  notice,
  pendingDelete,
  serviceState,
  startPending,
  stopPending,
  showNotice,
  closeNotice,
  loadAccountData,
  loadRequestData,
  updateAccount,
  deleteAccounts,
  undoDelete,
  commitDelete,
  startService,
  stopService,
  clearLogs,
  waitForRestart,
} = useConsole()

// noticeText 是按界面语言显示的提示正文，服务文案翻译表到达后随之更新
const noticeText = computed(() => logText(notice.message))
// deletedNotice 是撤销提示的正文：删除一个账户时写账户名，批量删除时写数量
const deletedNotice = computed(() => {
  const accounts = pendingDelete.value?.accounts ?? []
  return accounts.length === 1
    ? t('accounts.deletedNamed').replace('{name}', accounts[0]!.label)
    : t('accounts.deletedCount').replace('{count}', String(accounts.length))
})
const noticeBody = useTemplateRef<HTMLElement>('noticeBody')
// noticeOpen 是提示是否显示全文
const noticeOpen = ref(false)
// noticeClamped 是提示正文当前是否截断为三行，收起过渡结束后才截断
const noticeClamped = useClampToggle(noticeBody, noticeOpen, 3)
// noticeClipped 是提示正文是否超过三行
const noticeClipped = ref(false)
watch(
  noticeText,
  () => {
    noticeOpen.value = false
    noticeClipped.value =
      noticeBody.value !== null && noticeBody.value.scrollHeight > noticeBody.value.clientHeight
  },
  { flush: 'post' },
)

const navigation: { id: TabID; label: TranslationKey; icon: IconName }[] = [
  { id: 'logs', label: 'nav.logs', icon: 'logs' },
  { id: 'accounts', label: 'nav.accounts', icon: 'accounts' },
  { id: 'access', label: 'nav.access', icon: 'access' },
  { id: 'models', label: 'nav.models', icon: 'models' },
  { id: 'requests', label: 'nav.requests', icon: 'requests' },
  { id: 'usage', label: 'nav.usage', icon: 'usage' },
  { id: 'settings', label: 'nav.settings', icon: 'settings' },
  { id: 'playground', label: 'nav.playground', icon: 'playground' },
  { id: 'version', label: 'nav.version', icon: 'version' },
]

const statusColor = computed(() => {
  if (serviceState.value === 'running') return 'bg-green-500'
  if (serviceState.value === 'launching') return 'bg-cyan-400 animate-pulse'
  return 'bg-gray-600'
})
const statusTextColor = computed(() => {
  if (serviceState.value === 'running') return 'text-green-400'
  if (serviceState.value === 'launching') return 'text-cyan-300'
  return 'text-gray-500'
})
// serviceStateLabel 是生成服务状态在当前语言下的文案
const serviceStateLabel = computed(() => t(`state.${serviceState.value}`))
// serviceStates 是状态区可能显示的服务状态，各状态文案叠放在同一格，宽度取最长的一项
const serviceStates = ['running', 'launching', 'stopped', 'unavailable'] as const

// logout 先在当前会话内提交等待中的删除再退出登录
async function logout(): Promise<void> {
  await commitDelete()
  emit('logout')
}

onMounted(revealCurrentTab)
</script>

<template>
  <div class="flex h-full w-full flex-col md:flex-row">
    <aside
      class="app-sidebar relative grid w-full min-w-0 shrink-0 border-b border-line bg-panel md:border-r md:border-b-0"
      :style="{ '--sidebar-width': `${sidebarWidth}px` }"
    >
      <div
        class="flex h-14 min-w-0 items-center gap-2.5 border-b border-line px-4 [grid-area:brand]"
      >
        <span v-tooltip="`${t('app.status')}: ${serviceStateLabel}`" class="relative shrink-0">
          <img src="/favicon.svg" alt="" class="h-6 w-6" />
          <span
            class="absolute -right-0.5 -bottom-0.5 h-2.5 w-2.5 rounded-full ring-2 ring-panel"
            :class="statusColor"
          ></span>
        </span>
        <h1 class="relative top-px min-w-0 flex-1 truncate text-base font-bold text-white">
          AIStudio2API
        </h1>
        <div class="flex shrink-0 items-center gap-1">
          <ThemeToggle />
          <DropdownMenuRoot :modal="false">
            <DropdownMenuTrigger
              v-tooltip="t('app.language')"
              :aria-label="t('app.language')"
              class="flex h-6 w-6 shrink-0 items-center justify-center rounded border border-gray-700 text-gray-500 transition hover:text-white data-[state=open]:text-white"
            >
              <UiIcon name="language" :size="13" />
            </DropdownMenuTrigger>
            <DropdownMenuPortal>
              <DropdownMenuContent
                align="end"
                :side-offset="4"
                class="z-[60] max-h-[var(--reka-dropdown-menu-content-available-height)] overflow-y-auto rounded border border-line bg-panel shadow-xl"
              >
                <DropdownMenuItem
                  v-for="item in availableLocales"
                  :key="item.code"
                  class="flex w-full cursor-pointer items-center justify-between gap-4 px-4 py-2 text-left text-xs whitespace-nowrap text-gray-300 outline-none data-[highlighted]:bg-blue-600 data-[highlighted]:text-white"
                  @select="setLocale(item.code)"
                >
                  {{ item.label }}
                  <UiIcon v-if="item.code === locale" name="check" :size="12" />
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenuPortal>
          </DropdownMenuRoot>
        </div>
      </div>

      <div
        v-if="adminUsername"
        class="flex min-w-0 items-center gap-1 border-b border-line pr-4 [grid-area:controls] md:border-t md:border-b-0 md:px-3 md:py-2"
      >
        <span
          v-tooltip.overflow="adminUsername"
          class="hidden min-w-0 flex-1 truncate text-xs text-gray-400 md:block"
          >{{ adminUsername }}</span
        >
        <button
          v-tooltip="t('auth.logout')"
          type="button"
          :aria-label="`${adminUsername} · ${t('auth.logout')}`"
          class="flex h-6 w-6 shrink-0 items-center justify-center rounded border border-gray-700 text-gray-500 transition hover:text-white"
          @click="logout"
        >
          <UiIcon name="login" :size="13" />
        </button>
      </div>

      <nav
        ref="navigationBar"
        class="flex w-full min-w-0 gap-1 overflow-x-auto p-2 [grid-area:nav] md:flex-col md:overflow-x-hidden md:overflow-y-auto"
      >
        <button
          v-for="item in navigation"
          :key="item.id"
          :class="[
            'flex w-auto shrink-0 items-center gap-2 rounded-md px-3 py-2 text-left whitespace-nowrap transition md:w-full',
            currentTab === item.id
              ? 'bg-blue-600 text-white'
              : 'text-gray-400 hover:bg-raised hover:text-white',
          ]"
          type="button"
          :aria-current="currentTab === item.id ? 'page' : undefined"
          @click="openTab(item.id)"
        >
          <UiIcon :name="item.icon" :size="16" class="shrink-0" />
          <span v-tooltip.overflow="t(item.label)" class="min-w-0 truncate md:whitespace-normal">{{
            t(item.label)
          }}</span>
          <span
            v-if="item.id === 'version' && update?.available"
            class="size-2 shrink-0 rounded-full bg-amber-400 md:ml-auto"
            aria-hidden="true"
            :title="t('version.available').replace('{version}', update.latest!)"
          />
        </button>
      </nav>

      <div class="min-w-0 overflow-hidden border-t border-line p-2 [grid-area:status] md:p-4">
        <div class="mb-2 hidden text-xs text-gray-500 md:block">{{ t('app.status') }}</div>
        <div class="flex items-center justify-between gap-2 md:mb-4">
          <div class="flex min-w-0 items-center gap-2 md:contents">
            <span
              class="grid shrink-0 font-mono font-bold whitespace-nowrap uppercase"
              :class="statusTextColor"
            >
              <span
                v-for="state in serviceStates"
                :key="state"
                class="[grid-area:1/1]"
                :class="{ invisible: loading.status || state !== serviceState }"
                >{{ t(`state.${state}`) }}</span
              >
            </span>
            <span
              v-if="!loading.status"
              v-tooltip="status?.version"
              class="max-w-[65%] truncate font-mono text-xs text-gray-500"
            >
              {{ status?.version || '—' }}
            </span>
          </div>
          <button
            v-if="serviceState === 'stopped' || serviceState === 'unavailable'"
            :class="{ invisible: serviceState === 'unavailable' }"
            class="flex shrink-0 items-center gap-1.5 rounded bg-green-600 px-3 py-1 text-xs font-bold text-white shadow transition hover:bg-green-500 disabled:opacity-50 md:hidden"
            type="button"
            :disabled="startPending || stopPending"
            @click="startService"
          >
            <UiIcon :name="startPending ? 'spinner' : 'play'" :size="13" />
            {{ t('app.start') }}
          </button>
          <button
            v-else-if="serviceState === 'launching' || serviceState === 'running'"
            class="flex shrink-0 items-center gap-1.5 rounded bg-red-600 px-3 py-1 text-xs font-bold text-white shadow transition hover:bg-red-500 disabled:opacity-50 md:hidden"
            type="button"
            :disabled="stopPending"
            @click="stopService"
          >
            <UiIcon :name="stopPending ? 'spinner' : 'stop'" :size="13" />
            {{ t('app.stop') }}
          </button>
        </div>
        <button
          v-if="serviceState === 'stopped' || serviceState === 'unavailable'"
          :class="{ invisible: serviceState === 'unavailable' }"
          class="hidden w-full items-center justify-center gap-2 rounded bg-green-600 py-2 font-bold text-white shadow transition hover:bg-green-500 disabled:opacity-50 md:flex"
          type="button"
          :disabled="startPending || stopPending"
          @click="startService"
        >
          <UiIcon :name="startPending ? 'spinner' : 'play'" :size="16" />
          {{ t('app.start') }}
        </button>
        <button
          v-else-if="serviceState === 'launching' || serviceState === 'running'"
          class="hidden w-full items-center justify-center gap-2 rounded bg-red-600 py-2 font-bold text-white shadow transition hover:bg-red-500 disabled:opacity-50 md:flex"
          type="button"
          :disabled="stopPending"
          @click="stopService"
        >
          <UiIcon :name="stopPending ? 'spinner' : 'stop'" :size="16" />
          {{ t('app.stop') }}
        </button>
      </div>
      <div
        class="sidebar-resizer"
        :class="{ 'is-resizing': resizingSidebar }"
        role="separator"
        tabindex="0"
        aria-orientation="vertical"
        :aria-label="t('app.resizeSidebar')"
        :aria-valuemin="minSidebarWidth"
        :aria-valuemax="maxSidebarWidth"
        :aria-valuenow="sidebarWidth"
        @pointerdown="startSidebarResize"
        @pointermove="moveSidebarResize"
        @pointerup="resizingSidebar = false"
        @pointercancel="resizingSidebar = false"
        @dblclick="sidebarWidth = defaultSidebarWidth"
        @keydown="resizeSidebarWithKeyboard"
      ></div>
    </aside>

    <main class="@container flex min-h-0 min-w-0 flex-1 flex-col bg-canvas">
      <div
        v-if="config?.reserved_listen_addr"
        role="alert"
        class="page-x overflow-hidden border-b border-amber-500/30 bg-amber-500/10 py-2.5 text-sm break-words text-amber-200 [scrollbar-gutter:stable]"
      >
        {{
          t('app.listenReserved')
            .replace('{reserved}', config.reserved_listen_addr)
            .replace('{active}', config.active_listen_addr)
        }}
      </div>
      <div
        v-if="config?.invalid.length"
        role="alert"
        class="page-x overflow-hidden border-b border-amber-500/30 bg-amber-500/10 py-2.5 text-sm break-words text-amber-200 [scrollbar-gutter:stable]"
      >
        <p>{{ t('app.invalidConfig') }}</p>
        <p v-for="item in config.invalid" :key="item.key" class="mt-1 font-mono text-xs">
          {{ item.value ? `${item.key}=${item.value}` : item.key }} · {{ logText(item.reason) }}
        </p>
      </div>
      <KeepAlive
        include="LogsPanel,AccountsPanel,AccessPanel,ModelsTable,RequestsPanel,SettingsPanel,VersionPanel,UsagePanel"
      >
        <LogsPanel v-if="currentTab === 'logs'" :logs="logs" @clear="clearLogs" />
        <AccountsPanel
          v-else-if="currentTab === 'accounts'"
          :accounts="visibleAccounts"
          :tasks="accountTasks"
          :loading="loading.accounts"
          :error="errors.accounts"
          :chrome-import="status?.chrome_import ?? false"
          @refresh="loadAccountData"
          @updated="updateAccount"
          @notice="showNotice"
          @delete="deleteAccounts"
        />
        <AccessPanel
          v-else-if="currentTab === 'access'"
          :config="config"
          :models="models"
          @settings="openTab('settings')"
        />
        <ModelsTable
          v-else-if="currentTab === 'models'"
          :models="models"
          :loading="loading.models"
          :error="errors.models"
          :config="config"
          :accounts="accounts"
          @saved="config = $event"
          @notice="showNotice"
        />
        <RequestsPanel
          v-else-if="currentTab === 'requests'"
          :accounts="accounts"
          :cooldowns="cooldowns"
          :requests="requests"
          :loading="loading.requests || loading.cooldowns"
          :cooldown-error="errors.cooldowns"
          :request-error="errors.requests"
          @refresh="loadRequestData"
          @notice="showNotice"
        />
        <UsagePanel v-else-if="currentTab === 'usage'" :models="models" />
        <SettingsPanel
          v-else-if="currentTab === 'settings'"
          :config="config"
          :accounts="accounts"
          :models="models"
          :loading="loading.config"
          :error="errors.config"
          @saved="config = $event"
          @notice="showNotice"
        />
        <VersionPanel
          v-else-if="currentTab === 'version'"
          :status="update"
          :active-requests="status?.active_requests ?? 0"
          @status="update = $event"
          @restarting="waitForRestart"
        />
      </KeepAlive>
    </main>

    <div
      class="pointer-events-none fixed right-5 bottom-5 left-5 z-50 flex flex-col items-end gap-2 sm:left-auto sm:max-w-md"
    >
      <Transition name="notice">
        <div
          v-if="notice.message"
          role="status"
          class="pointer-events-auto flex max-w-full items-start gap-2 rounded border bg-panel py-3 pr-2 pl-4 text-sm shadow-xl"
          :class="
            notice.tone === 'error'
              ? 'border-red-500/50 text-red-300'
              : 'border-green-500/50 text-green-300'
          "
        >
          <UiDisclosure v-if="noticeClipped" v-model="noticeOpen" />
          <p
            ref="noticeBody"
            class="min-w-0 flex-1 break-words"
            :class="noticeClamped ? 'line-clamp-3' : 'max-h-[40vh] overflow-y-auto'"
          >
            {{ noticeText }}
          </p>
          <button
            type="button"
            class="-my-1 shrink-0 rounded p-1 text-gray-500 transition hover:bg-raised hover:text-gray-200"
            :aria-label="t('common.close')"
            @click="closeNotice"
          >
            <UiIcon name="close" :size="14" />
          </button>
        </div>
      </Transition>
      <Transition name="notice">
        <div
          v-if="pendingDelete"
          class="pointer-events-auto flex max-w-full items-center gap-4 rounded border border-line-strong bg-panel px-4 py-3 text-sm text-gray-200 shadow-xl"
          role="status"
        >
          <span v-tooltip.overflow="deletedNotice" class="min-w-0 truncate">{{
            deletedNotice
          }}</span>
          <button
            type="button"
            class="flex shrink-0 items-center gap-1.5 rounded px-2 py-1 font-medium text-blue-400 hover:bg-raised"
            @click="undoDelete"
          >
            <UiIcon name="undo" :size="14" />
            {{ t('common.undo') }}
          </button>
        </div>
      </Transition>
    </div>
  </div>
</template>

<style scoped>
.app-sidebar {
  grid-template-columns: minmax(0, 1fr) auto;
  grid-template-areas:
    'brand controls'
    'nav nav'
    'status status';
}

@media (width >= 48rem) {
  .app-sidebar {
    width: var(--sidebar-width);
    grid-template-rows: auto minmax(0, 1fr) auto auto;
    grid-template-columns: minmax(0, 1fr);
    grid-template-areas:
      'brand'
      'nav'
      'controls'
      'status';
  }
}

.sidebar-resizer {
  display: none;
  position: absolute;
  z-index: 20;
  top: 0;
  right: -0.375rem;
  bottom: 0;
  width: 0.75rem;
  cursor: col-resize;
  touch-action: none;
  user-select: none;
}

.sidebar-resizer::after {
  position: absolute;
  top: 0;
  bottom: 0;
  left: calc(50% - 1px);
  width: 2px;
  background: transparent;
  content: '';
  transition: background-color 150ms ease;
}

.sidebar-resizer:hover::after,
.sidebar-resizer:focus-visible::after,
.sidebar-resizer.is-resizing::after {
  background: var(--color-blue-500);
}

.sidebar-resizer:focus-visible {
  outline: none;
}

@media (width >= 48rem) {
  .sidebar-resizer {
    display: block;
  }
}
</style>
