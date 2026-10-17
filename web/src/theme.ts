import { readonly, ref } from 'vue'

// Theme 是界面配色模式
export type Theme = 'dark' | 'light'

const savedTheme = window.localStorage.getItem('user_theme')
const theme = ref<Theme>(savedTheme === 'light' || savedTheme === 'dark' ? savedTheme : 'dark')
document.documentElement.dataset.theme = theme.value

// useTheme 提供当前界面配色模式与切换方法
export function useTheme() {
  const toggleTheme = (): void => {
    const next: Theme = theme.value === 'dark' ? 'light' : 'dark'
    theme.value = next
    document.documentElement.dataset.theme = next
    window.localStorage.setItem('user_theme', next)
  }

  return { theme: readonly(theme), toggleTheme }
}
