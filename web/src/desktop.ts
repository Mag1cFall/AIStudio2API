import { shallowReactive, watch, type Ref } from 'vue'
import type { Router } from 'vue-router'
import { useTheme } from './theme'

declare global {
  interface Window {
    _wails?: { invoke?: (message: string) => void }
  }
}

// readPlatform 返回桌面窗口传入的平台：首次加载取地址栏参数并保存到会话，切换页面清除参数后从会话读取
function readPlatform(): string {
  const query = new URLSearchParams(window.location.search)
  if (query.get('shell') === 'desktop') {
    const platform = query.get('platform') ?? ''
    window.sessionStorage.setItem('desktop_platform', platform)
    return platform
  }
  return window.sessionStorage.getItem('desktop_platform') ?? ''
}

// desktopPlatform 为桌面窗口的操作系统，在浏览器中为空
export const desktopPlatform = readPlatform()

// sendDesktop 经 Wails 注入的 postMessage 向桌面壳发送窗口操作，注入完成前的消息在注入后发送
export function sendDesktop(command: string): void {
  const send = (): boolean => {
    const invoke = window._wails?.invoke
    if (invoke === undefined) return false
    invoke(`aistudio2api:${command}`)
    return true
  }
  if (!send()) window.addEventListener('wails:runtime-config-ready', send, { once: true })
}

// openExternal 把新窗口打开的链接交给系统浏览器，桌面窗口本身只显示管理页面
function openExternal(event: MouseEvent): void {
  const link = (event.target as Element).closest('a[target="_blank"]')
  if (!(link instanceof HTMLAnchorElement)) return
  event.preventDefault()
  sendDesktop(`open:${link.href}`)
}

// keepers 是关闭窗口时需要保留页面的条件：进行中的生成、保存与未保存的内容
const keepers = shallowReactive(new Set<() => boolean>())

// keepPage 登记关闭窗口时需要保留页面的条件，返回撤销登记的函数
export function keepPage(condition: () => boolean): () => void {
  keepers.add(condition)
  return () => keepers.delete(condition)
}

// startDesktop 在桌面窗口中加载 Wails 运行时的拖动与缩放处理，外部链接用系统浏览器打开，界面语言与配色同步到托盘菜单，当前路由与是否需要保留页面同步到桌面壳
export function startDesktop(locale: Readonly<Ref<string>>, router: Router): void {
  if (desktopPlatform === '') return
  void import('@wailsio/runtime')
  document.addEventListener('click', openExternal, true)
  watch(locale, (value) => sendDesktop(`locale:${value}`), { immediate: true })
  watch(useTheme().theme, (value) => sendDesktop(`theme:${value}`), { immediate: true })
  watch(
    () => [...keepers].some((keep) => keep()),
    (busy) => sendDesktop(`busy:${busy ? 1 : 0}`),
    { immediate: true },
  )
  watch(
    () => router.currentRoute.value.fullPath,
    (path) => sendDesktop(`route:${path}`),
    { immediate: true },
  )
}
