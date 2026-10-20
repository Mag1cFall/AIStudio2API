import { computed, type ComputedRef } from 'vue'
import { useI18n } from '@/i18n'
import { mac, phone } from './platform'
import { submitKey } from './shell/preferences'

// commandKey 与 optionKey 是官网提示中的修饰键名称，Mac 上为 ⌘ 与 Option
const commandKey = mac ? '⌘' : 'Ctrl'
const optionKey = mac ? 'Option' : 'Alt'

// useRunTooltip 返回与官网 _.iod 一致的运行按钮提示：生成中为取消生成，否则按提交键列出运行与追加的快捷键
export function useRunTooltip(running: () => boolean): ComputedRef<string> {
  const { t } = useI18n()
  return computed(() =>
    running()
      ? t('pg.cancelGeneration')
      : t(submitKey.value === 'enter' ? 'pg.sendPromptEnter' : 'pg.sendPromptCommand')
          .replace('{command}', commandKey)
          .replace('{option}', optionKey),
  )
}

// appends 与官网 _.cjd 一致：不在输入法组合中的 Alt 加 Enter 追加
export function appends(event: KeyboardEvent): boolean {
  return event.key === 'Enter' && event.altKey && !event.isComposing
}

// submits 与官网 _.uT 一致：手机上不带修饰键的 Enter 换行，输入法组合与追加不运行；Ctrl + Enter 模式下 Mac 用 ⌘、其他平台用 Ctrl 加 Enter 运行，Enter 模式下不带 Shift 与该修饰键的 Enter 运行
export function submits(event: KeyboardEvent): boolean {
  const plain =
    event.key === 'Enter' && !event.altKey && !event.ctrlKey && !event.metaKey && !event.shiftKey
  if ((phone && plain) || event.isComposing || appends(event)) return false
  const command = mac ? event.metaKey : event.ctrlKey
  return submitKey.value === 'ctrl-enter'
    ? event.key === 'Enter' && command
    : event.key === 'Enter' && !event.shiftKey && !command
}
