import { computed, type ComputedRef } from 'vue'
import { useI18n } from '@/i18n'
import type { AccountTask, PreparationStatus } from '@/types'

// waitsForCamoufox 判断浏览器登录、重新登录或账户验证任务是否正在等待 Camoufox 下载或解压
export function waitsForCamoufox(
  task: AccountTask,
  camoufox: PreparationStatus | undefined,
): boolean {
  return (
    task.state === 'running' &&
    (task.kind === 'login' || task.kind === 'relogin' || task.kind === 'verify') &&
    camoufox?.preparing === true
  )
}

// useDownloadProgress 返回 Camoufox 发行包的下载百分比与按界面语言格式化的已下载与总大小，总大小未知时百分比为 null
export function useDownloadProgress(status: () => PreparationStatus | undefined): {
  percent: ComputedRef<number | null>
  sizeText: ComputedRef<string>
} {
  const { locale } = useI18n()
  const percent = computed(() => {
    const download = status()?.download
    return download && download.total > 0
      ? Math.floor((download.received / download.total) * 100)
      : null
  })
  const sizeText = computed(() => {
    const download = status()?.download
    if (!download) return ''
    const format = new Intl.NumberFormat(locale.value, {
      style: 'unit',
      unit: 'megabyte',
      maximumFractionDigits: 0,
    })
    const received = format.format(download.received / 1e6)
    return download.total > 0 ? `${received} / ${format.format(download.total / 1e6)}` : received
  })
  return { percent, sizeText }
}
