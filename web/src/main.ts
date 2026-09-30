import { createApp } from 'vue'
import ConsoleRoot from './ConsoleRoot.vue'
import { vTooltip } from './tooltip'
import './style.css'

createApp(ConsoleRoot).directive('tooltip', vTooltip).mount('#app')
