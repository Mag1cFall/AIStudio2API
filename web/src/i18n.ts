import { readonly, ref, shallowRef } from 'vue'
import { ApiError } from '@/api'
import type { TranslationKey } from '@/locales/zh-CN'
import { Catalog } from '@/messages'
import type { Locale, UpstreamChannel } from '@/types'

export type { TranslationKey }

type Messages = Record<TranslationKey, string>

// localeLabels 是语言选择器中各语言的名称
const localeLabels: Record<Locale, string> = {
  'zh-CN': '简体中文',
  'zh-TW': '繁體中文',
  en: 'English',
  ja: '日本語',
  ko: '한국어',
  fr: 'Français',
  de: 'Deutsch',
}

// localeModules 按需加载各语言的界面文案，每种语言打包为单独的文件
const localeModules: Record<Locale, () => Promise<{ messages: Messages }>> = {
  'zh-CN': () => import('@/locales/zh-CN'),
  'zh-TW': () => import('@/locales/zh-TW'),
  en: () => import('@/locales/en'),
  ja: () => import('@/locales/ja'),
  ko: () => import('@/locales/ko'),
  fr: () => import('@/locales/fr'),
  de: () => import('@/locales/de'),
}

// catalogs 是已加载语言的界面文案
const catalogs: Partial<Record<Locale, Messages>> = {}

// loadMessages 加载一种语言的界面文案，已加载时直接返回
async function loadMessages(code: Locale): Promise<void> {
  catalogs[code] ??= (await localeModules[code]()).messages
}

export const availableLocales = (Object.keys(localeLabels) as Locale[]).map((code) => ({
  code,
  label: localeLabels[code],
}))

// matchLocale 返回与语言标签最接近的界面语言，简体中文变体为 zh-CN，繁体中文变体为 zh-TW，其余按语言主标签选择，与 internal/i18n 的 Match 相同
export function matchLocale(tag: string): Locale | undefined {
  const lower = tag.trim().replaceAll('_', '-').toLowerCase()
  if (lower === '') return undefined
  if (['zh-tw', 'zh-hk', 'zh-mo'].includes(lower) || lower.startsWith('zh-hant')) return 'zh-TW'
  if (lower === 'zh' || lower.startsWith('zh-')) return 'zh-CN'
  const primary = lower.split('-')[0] as Locale
  return Object.hasOwn(localeLabels, primary) ? primary : undefined
}

// browserLocale 按浏览器首选语言返回界面语言，都不支持时为英文
function browserLocale(): Locale {
  for (const tag of navigator.languages) {
    const match = matchLocale(tag)
    if (match) return match
  }
  return 'en'
}

const savedLocale = window.localStorage.getItem('user_lang') as Locale | null
const locale = ref<Locale>(
  savedLocale !== null && Object.hasOwn(localeLabels, savedLocale) ? savedLocale : browserLocale(),
)
document.documentElement.lang = locale.value

// loadInitialLocale 加载启动时界面语言的文案，挂载应用前调用
export function loadInitialLocale(): Promise<void> {
  return loadMessages(locale.value)
}

// serverCatalog 是当前语言的服务文案翻译表，读取完成前为 null
const serverCatalog = shallowRef<{ locale: Locale; catalog: Catalog } | null>(null)
// serverLoad 记录正在读取或上次读取失败的语言与时间
let serverLoad: { locale: Locale; pending: boolean; failedAt: number } | null = null

// serverMessages 返回当前语言的服务文案翻译表，尚未读取时开始读取，失败 10 秒后才重试
function serverMessages(): Catalog | null {
  const current = locale.value
  if (serverCatalog.value?.locale === current) return serverCatalog.value.catalog
  if (
    serverLoad?.locale === current &&
    (serverLoad.pending || Date.now() - serverLoad.failedAt < 10000)
  )
    return null
  const load = { locale: current, pending: true, failedAt: 0 }
  serverLoad = load
  void fetch(`/api/i18n/${current}`)
    .then(async (response) => {
      if (!response.ok) throw new Error(String(response.status))
      const table = (await response.json()) as Record<string, string>
      if (locale.value === current)
        serverCatalog.value = { locale: current, catalog: new Catalog(table, current) }
      load.pending = false
    })
    .catch(() => {
      load.pending = false
      load.failedAt = Date.now()
    })
  return null
}

// channelLabelKey 返回上游通道的翻译键
export function channelLabelKey(channel: UpstreamChannel): TranslationKey {
  if (channel === 'assistant') return 'channel.assistant'
  return channel === 'build' ? 'channel.build' : 'channel.playground'
}

// useI18n 提供当前语言的界面文案、错误文案与服务文案翻译
export function useI18n() {
  const t = (key: TranslationKey): string => (catalogs[locale.value] as Messages)[key]
  const setLocale = async (value: Locale): Promise<void> => {
    await loadMessages(value)
    locale.value = value
    document.documentElement.lang = value
    window.localStorage.setItem('user_lang', value)
  }

  // logText 把服务生成的日志、错误与状态说明换成当前语言，上游原文、账户、模型名与数值保持原样
  const logText = (message: string): string => {
    if (locale.value === 'zh-CN') return message
    return serverMessages()?.translate(message) ?? message
  }

  // errorText 返回界面显示的错误文本：未能连接服务时显示服务离线，管理接口错误码按当前语言显示，服务说明经翻译表翻译
  const errorText = (reason: unknown): string => {
    if (reason instanceof TypeError) return t('app.offline')
    if (reason instanceof ApiError) {
      const key = `error.${reason.code}`
      if (Object.hasOwn(catalogs[locale.value] as Messages, key)) {
        return t(key as TranslationKey).replace('{detail}', logText(reason.message))
      }
    }
    return reason instanceof Error ? logText(reason.message) : t('common.error')
  }

  // tokenLabel 返回按当前语言分组的 token 数文案
  const tokenLabel = (count: number): string =>
    t('pg.tokens').replace('{count}', new Intl.NumberFormat(locale.value).format(count))

  return {
    availableLocales,
    locale: readonly(locale),
    setLocale,
    t,
    errorText,
    logText,
    tokenLabel,
  }
}
