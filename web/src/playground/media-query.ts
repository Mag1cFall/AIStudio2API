import { onMounted, onUnmounted, ref, type Ref } from 'vue'

// useMediaQuery 返回媒体查询当前是否匹配，组件卸载时停止监听
export function useMediaQuery(query: string): Ref<boolean> {
  const list = window.matchMedia(query)
  const matches = ref(list.matches)
  const update = (event: MediaQueryListEvent): void => {
    matches.value = event.matches
  }
  onMounted(() => list.addEventListener('change', update))
  onUnmounted(() => list.removeEventListener('change', update))
  return matches
}
