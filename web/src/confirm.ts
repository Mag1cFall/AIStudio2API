import { reactive } from 'vue'

// confirmState 为当前确认对话框的内容与结果回调
export const confirmState = reactive({
  open: false,
  message: '',
  action: '',
  resolve: null as ((confirmed: boolean) => void) | null,
})

// confirmAction 打开确认对话框并返回用户是否确认
export function confirmAction(message: string, action: string): Promise<boolean> {
  confirmState.resolve?.(false)
  return new Promise((resolve) => {
    Object.assign(confirmState, { open: true, message, action, resolve })
  })
}

// settleConfirm 关闭确认对话框并返回结果
export function settleConfirm(confirmed: boolean): void {
  const resolve = confirmState.resolve
  Object.assign(confirmState, { open: false, resolve: null })
  resolve?.(confirmed)
}
