import '@fontsource-variable/inter'
import { createApp } from 'vue'
import ConsoleRoot from './ConsoleRoot.vue'
import { loadInitialLocale } from './i18n'
import { router } from './router'
import { vTooltip } from './tooltip'
import './style.css'

const app = createApp(ConsoleRoot).use(router).directive('tooltip', vTooltip)
void Promise.all([router.isReady(), loadInitialLocale()]).then(() => app.mount('#app'))
