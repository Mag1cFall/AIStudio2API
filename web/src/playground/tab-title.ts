import { computed, onScopeDispose, ref, watch, watchEffect } from 'vue'
import type { RouteLocationNormalizedLoaded } from 'vue-router'
import { useI18n } from '@/i18n'
import type { Turn } from './types'
import { useCompare } from './session'

// awaitingResponse 表示对话最后一轮是还没有函数响应的函数调用
function awaitingResponse(turns: readonly Turn[]): boolean {
  const last = turns.at(-1)
  return last?.role === 'model' && last.parts.some((part) => part.functionCall)
}

// useTabTitle 让浏览器标签页标题显示对话标题与生成状态：生成中与等待函数响应时始终显示，浏览器标签页隐藏或离开对话页期间结束的生成显示完成或出错，回到对话页后清除；route 为当前页面路由
export function useTabTitle(route: RouteLocationNormalizedLoaded): void {
  const { t } = useI18n()
  const { panes } = useCompare()

  const visible = ref(document.visibilityState === 'visible')
  const onVisibility = () => (visible.value = document.visibilityState === 'visible')
  document.addEventListener('visibilitychange', onVisibility)
  onScopeDispose(() => document.removeEventListener('visibilitychange', onVisibility))

  // chat 表示当前页面是对话页
  const chat = computed(() => route.name === 'prompt' || route.name === 'saved')
  // viewing 表示用户正看着对话页
  const viewing = computed(() => visible.value && chat.value)
  // unseen 是用户没看着对话页时结束的生成结果，任一窗格出错即为出错
  const unseen = ref<'done' | 'error' | null>(null)

  watch(
    () => panes.map((pane) => pane.session.running),
    (running, before) => {
      if (viewing.value) return
      panes.forEach((pane, index) => {
        if (!before[index] || running[index]) return
        if (pane.session.turns.at(-1)?.error !== undefined) unseen.value = 'error'
        else unseen.value ??= 'done'
      })
    },
  )
  watch(viewing, (value) => {
    if (value) unseen.value = null
  })

  // status 是标题前缀，多个窗格汇总为一个状态
  const status = computed(() => {
    if (panes.some((pane) => pane.session.running)) return t('pg.tabGenerating')
    if (unseen.value === 'error') return t('pg.tabError')
    if (panes.some((pane) => awaitingResponse(pane.session.turns))) return t('pg.tabAwaiting')
    if (unseen.value === 'done') return t('pg.tabDone')
    return ''
  })

  // page 是当前页面的标题：对话页为对话标题，未命名时与其他试用页一样为页面名，管理端页面为 AIStudio2API
  const page = computed(() => {
    if (!route.meta.studio) return 'AIStudio2API'
    const name =
      route.name === 'library'
        ? t('pg.history')
        : (chat.value && panes[0]!.session.title) || t('nav.playground')
    return `${name} | AIStudio2API`
  })

  watchEffect(() => {
    document.title = status.value === '' ? page.value : `${status.value} · ${page.value}`
  })
}
