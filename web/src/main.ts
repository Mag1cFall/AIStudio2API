import '@fontsource-variable/inter'
import { createApp } from 'vue'
import ConsoleRoot from './ConsoleRoot.vue'
import { router } from './router'
import { vTooltip } from './tooltip'
import './style.css'

const app = createApp(ConsoleRoot).use(router).directive('tooltip', vTooltip)
void router.isReady().then(() => app.mount('#app'))
