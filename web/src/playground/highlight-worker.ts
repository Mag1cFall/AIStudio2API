import hljs from 'highlight.js/lib/common'

// onmessage 以完整词法上下文高亮代码，空语言使用自动识别
self.onmessage = (event: MessageEvent<{ id: number; code: string; language: string }>) => {
  const { id, code, language } = event.data
  const html = hljs.getLanguage(language)
    ? hljs.highlight(code, { language, ignoreIllegals: true }).value
    : hljs.highlightAuto(code).value
  self.postMessage({ id, html })
}
