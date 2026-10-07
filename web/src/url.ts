import { onActivated, onUnmounted, ref, watch, type Ref } from 'vue'

// queryValue 读取地址栏查询参数并限制在允许值内
function queryValue<T extends string>(name: string, allowed: readonly T[], fallback: T): T {
  const value = new URLSearchParams(window.location.search).get(name) as T | null
  return value !== null && allowed.includes(value) ? value : fallback
}

// writeQuery 替换地址栏查询参数，不生成新的历史记录
function writeQuery(values: Record<string, string | null>): void {
  const url = new URL(window.location.href)
  for (const [name, value] of Object.entries(values)) {
    if (value === null) url.searchParams.delete(name)
    else url.searchParams.set(name, value)
  }
  if (url.href !== window.location.href) window.history.replaceState(null, '', url)
}

// useQueryParam 将页面内状态同步到地址栏查询参数，浏览器前进后退时更新，缓存页面重新显示时写回
export function useQueryParam<T extends string>(
  name: string,
  allowed: readonly T[],
  fallback: T,
): Ref<T> {
  const state = ref(queryValue(name, allowed, fallback)) as Ref<T>
  const write = (value: T): void => writeQuery({ [name]: value === fallback ? null : value })
  watch(state, write)
  onActivated(() => write(state.value))
  const sync = (): void => {
    state.value = queryValue(name, allowed, fallback)
  }
  window.addEventListener('popstate', sync)
  onUnmounted(() => window.removeEventListener('popstate', sync))
  return state
}

// useTabParam 以 tab 查询参数保存当前页面，切换页面时清除上一页面的查询参数
export function useTabParam<T extends string>(
  allowed: readonly T[],
  fallback: T,
): { tab: Ref<T>; select: (tab: T) => void } {
  const tab = ref(queryValue('tab', allowed, fallback)) as Ref<T>
  const sync = (): void => {
    tab.value = queryValue('tab', allowed, fallback)
  }
  window.addEventListener('popstate', sync)
  onUnmounted(() => window.removeEventListener('popstate', sync))
  const select = (next: T): void => {
    if (next === tab.value) return
    tab.value = next
    const url = new URL(window.location.href)
    url.search = ''
    if (next !== fallback) url.searchParams.set('tab', next)
    window.history.pushState(null, '', url)
  }
  return { tab, select }
}
