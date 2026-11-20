import { nextTick, useTemplateRef } from 'vue'

// useAppendList 返回 reveal：新增一行后把模板中 ref 为 key 的列表滚到末行，并让列表之后的添加控件留在视口内，focus 为 true 时聚焦末行的第一个输入控件
export function useAppendList(key: string): (focus: boolean) => Promise<void> {
  const list = useTemplateRef<HTMLElement>(key)
  return async (focus) => {
    await nextTick()
    const element = list.value
    if (!element) return
    element.scrollTop = element.scrollHeight
    ;(element.nextElementSibling ?? element).scrollIntoView({ block: 'nearest' })
    if (focus)
      element.lastElementChild
        ?.querySelector<HTMLElement>('input, select, button')
        ?.focus({ preventScroll: true })
  }
}
