import '@fontsource-variable/inter'
import { createApp } from 'vue'
import ConsoleRoot from './ConsoleRoot.vue'
import { loadInitialLocale } from './i18n'
import { router } from './router'
import { vTooltip } from './tooltip'
import './style.css'

// warmCjkFont 在首帧绘制之后的两个任务里各排版一个隐藏的汉字，常规与粗体的中文字体提前载入，之后首次显示中文时不再承担载入字体的耗时
function warmCjkFont(): void {
  const probe = document.createElement('span')
  probe.textContent = '中'
  probe.setAttribute('aria-hidden', 'true')
  probe.style.cssText = 'position: fixed; visibility: hidden; pointer-events: none'
  const next = (step: () => void) => requestAnimationFrame(() => setTimeout(step))
  next(() => {
    document.body.append(probe)
    void probe.offsetWidth
    next(() => {
      probe.style.fontWeight = '700'
      void probe.offsetWidth
      probe.remove()
    })
  })
}

const app = createApp(ConsoleRoot).use(router).directive('tooltip', vTooltip)
void Promise.all([router.isReady(), loadInitialLocale()]).then(() => {
  app.mount('#app')
  warmCjkFont()
})
