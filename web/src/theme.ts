import { readonly, ref } from 'vue'

// Theme 是界面配色模式
export type Theme = 'dark' | 'light'

// ThemePreference 是 User settings 中的配色选项，system 跟随系统
export type ThemePreference = Theme | 'system'

// systemQuery 是系统深色模式的媒体查询
const systemQuery = window.matchMedia('(prefers-color-scheme: dark)')

// theme 取 index.html 在首帧前按 localStorage 设置的配色模式
const theme = ref<Theme>(document.documentElement.dataset.theme === 'light' ? 'light' : 'dark')

// readStoredTheme 读取保存的配色选项，没有保存时跟随系统
function readStoredTheme(): ThemePreference {
  try {
    const stored = window.localStorage.getItem('user_theme')
    return stored === 'light' || stored === 'dark' ? stored : 'system'
  } catch {
    return 'system'
  }
}

// preference 是当前配色选项
const preference = ref<ThemePreference>(readStoredTheme())

// systemTheme 返回系统当前的配色模式
function systemTheme(): Theme {
  return systemQuery.matches ? 'dark' : 'light'
}

// applyTheme 切换根元素的配色模式
function applyTheme(next: Theme): void {
  theme.value = next
  document.documentElement.dataset.theme = next
}

systemQuery.addEventListener('change', () => {
  if (preference.value === 'system') applyTheme(systemTheme())
})

// switchTheme 切换配色模式，给出 origin 时切到浅色的浅色画面从 origin 圆形展开铺满全屏、切到深色时收回 origin，不给 origin 时直接切换
function switchTheme(next: Theme, origin?: { x: number; y: number }): void {
  if (next === theme.value) return
  const root = document.documentElement
  // themeSwitching 期间停用元素自身的颜色过渡，全部元素在同一帧换色
  root.dataset.themeSwitching = ''
  const finish = (): void => {
    delete root.dataset.themeSwitching
  }
  if (
    !origin ||
    !('startViewTransition' in document) ||
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  ) {
    applyTheme(next)
    void getComputedStyle(root).color
    finish()
    return
  }
  const radius = Math.hypot(
    Math.max(origin.x, window.innerWidth - origin.x),
    Math.max(origin.y, window.innerHeight - origin.y),
  )
  const circle = [
    `circle(0px at ${origin.x}px ${origin.y}px)`,
    `circle(${radius}px at ${origin.x}px ${origin.y}px)`,
  ]
  const transition = document.startViewTransition(() => applyTheme(next))
  void transition.ready.then(() => {
    const reveal = root.animate(
      { clipPath: next === 'light' ? circle : [...circle].reverse() },
      {
        duration: 420,
        easing: next === 'light' ? 'ease-out' : 'ease-in',
        fill: 'forwards',
        pseudoElement: `::view-transition-${next === 'light' ? 'new' : 'old'}(root)`,
      },
    )
    void transition.finished.finally(() => reveal.cancel())
  })
  void transition.finished.finally(finish)
}

// selectTheme 保存配色选项并切换到对应的配色模式
function selectTheme(next: ThemePreference, origin?: { x: number; y: number }): void {
  preference.value = next
  window.localStorage.setItem('user_theme', next)
  switchTheme(next === 'system' ? systemTheme() : next, origin)
}

// useTheme 提供当前界面配色模式、配色选项与切换方法
export function useTheme() {
  const toggleTheme = (origin: { x: number; y: number }): void =>
    selectTheme(theme.value === 'dark' ? 'light' : 'dark', origin)

  return {
    theme: readonly(theme),
    preference: readonly(preference),
    toggleTheme,
    selectTheme,
  }
}
