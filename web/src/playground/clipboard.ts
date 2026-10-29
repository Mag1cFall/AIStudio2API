import { useConsole } from '@/console'
import { useI18n } from '@/i18n'

// copyToClipboard 复制文字并以提示条告知结果，非安全上下文等无法写入剪贴板时提示复制失败
export async function copyToClipboard(text: string): Promise<void> {
  const { t } = useI18n()
  const { showNotice } = useConsole()
  try {
    await navigator.clipboard.writeText(text)
    showNotice(t('pg.copied'), 'success')
  } catch {
    showNotice(t('pg.copyFailed'), 'error')
  }
}
