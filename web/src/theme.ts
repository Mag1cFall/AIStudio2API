import { readonly, ref } from 'vue'

// Theme 是界面配色模式
export type Theme = 'dark' | 'light'

// theme 取 index.html 在首帧前按 localStorage 设置的配色模式
const theme = ref<Theme>(document.documentElement.dataset.theme === 'light' ? 'light' : 'dark')

// applyTheme 切换根元素的配色模式并保存
function applyTheme(next: Theme): void {
  theme.value = next
  document.documentElement.dataset.theme = next
  window.localStorage.setItem('user_theme', next)
}

// useTheme 提供当前界面配色模式与切换方法，切到浅色时浅色画面从 origin 圆形展开铺满全屏，切到深色时收回 origin
export function useTheme() {
  const toggleTheme = (origin: { x: number; y: number }): void => {
    const next: Theme = theme.value === 'dark' ? 'light' : 'dark'
    const root = document.documentElement
    // themeSwitching 期间停用元素自身的颜色过渡，全部元素在同一帧换色
    root.dataset.themeSwitching = ''
    const finish = (): void => {
      delete root.dataset.themeSwitching
    }
    if (
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
      root.animate(
        { clipPath: next === 'light' ? circle : [...circle].reverse() },
        {
          duration: 420,
          easing: next === 'light' ? 'ease-out' : 'ease-in',
          pseudoElement: `::view-transition-${next === 'light' ? 'new' : 'old'}(root)`,
        },
      )
    })
    void transition.finished.finally(finish)
  }

  return { theme: readonly(theme), toggleTheme }
}
