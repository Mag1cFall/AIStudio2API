import { onActivated, onDeactivated, onMounted, onUnmounted, ref, watch, type Ref } from 'vue'
import { useMediaQuery } from './media-query'

// editable 判断键盘事件目标是否为可输入元素
export function editable(target: EventTarget | null): boolean {
  const element = target as HTMLElement
  return element.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(element.tagName)
}

// useRunSettingsPanel 与官网一致：960 px 以下运行设置为右侧滑出面板，Ctrl+Shift+S 切换面板，/ 由 focusInput 聚焦页面主输入框并返回是否已聚焦，其余按键交给 onKey；页面显示期间监听按键
export function useRunSettingsPanel(
  focusInput: () => boolean,
  onKey?: (event: KeyboardEvent) => void,
): { narrow: Ref<boolean>; settingsOpen: Ref<boolean> } {
  const narrow = useMediaQuery('(max-width: 960px)')
  const settingsOpen = ref(!narrow.value)
  watch(narrow, (value) => {
    settingsOpen.value = !value
  })

  // onKeydown 处理运行设置面板与主输入框的快捷键，对话框打开时不处理
  function onKeydown(event: KeyboardEvent): void {
    if (event.defaultPrevented || document.querySelector('[role="dialog"]')) return
    if ((event.ctrlKey || event.metaKey) && event.shiftKey && event.key.toLowerCase() === 's') {
      event.preventDefault()
      settingsOpen.value = !settingsOpen.value
      return
    }
    if (editable(event.target) || event.ctrlKey || event.metaKey || event.altKey) return
    if (event.key !== '/') onKey?.(event)
    else if (focusInput()) event.preventDefault()
  }

  const listen = () => window.addEventListener('keydown', onKeydown)
  const unlisten = () => window.removeEventListener('keydown', onKeydown)
  onMounted(listen)
  onActivated(listen)
  onDeactivated(unlisten)
  onUnmounted(unlisten)
  return { narrow, settingsOpen }
}
