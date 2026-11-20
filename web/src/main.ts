import '@fontsource-variable/inter'
import { createApp } from 'vue'
import ConsoleRoot from './ConsoleRoot.vue'
import { loadInitialLocale } from './i18n'
import { router } from './router'
import { vTooltip } from './tooltip'
import './style.css'

// warmCjkFont 在首帧绘制之后的两帧里各排版一个隐藏的汉字，常规与粗体的中文字体提前载入，之后首次显示中文时不再承担载入字体的耗时；汉字随帧排版，不在任务中强制同步布局
function warmCjkFont(): void {
  const probe = document.createElement('span')
  probe.textContent = '中'
  probe.setAttribute('aria-hidden', 'true')
  probe.style.cssText = 'position: fixed; visibility: hidden; pointer-events: none'
  const next = (step: () => void) => requestAnimationFrame(() => setTimeout(step))
  next(() => {
    document.body.prepend(probe)
    next(() => {
      probe.style.fontWeight = '700'
      next(() => probe.remove())
    })
  })
}

// prefetchStudio 在挂载 3 s 后的空闲时间载入 API 试用页布局与对话页的分块和样式表，没有空闲回调的 WebKit 在 3 s 后直接载入
function prefetchStudio(): void {
  const load = () => {
    void import('./playground/StudioLayout.vue')
    void import('./playground/PlaygroundPage.vue')
  }
  setTimeout(() => ('requestIdleCallback' in window ? requestIdleCallback(load) : load()), 3000)
}

// 界面字体在入口求值时开始载入，与路由和界面语言的准备并行，不等首次排版用到字体时才发起
void document.fonts.load('1em "Inter Variable"').catch(() => [])

const app = createApp(ConsoleRoot).use(router).directive('tooltip', vTooltip)
void Promise.all([router.isReady(), loadInitialLocale()]).then(() => {
  app.mount('#app')
  warmCjkFont()
  prefetchStudio()
})
