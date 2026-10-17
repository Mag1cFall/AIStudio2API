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

// useTheme 提供当前界面配色模式与整页淡入淡出的切换方法
export function useTheme() {
  const toggleTheme = (): void => {
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
    void document.startViewTransition(() => applyTheme(next)).finished.finally(finish)
  }

  return { theme: readonly(theme), toggleTheme }
}
