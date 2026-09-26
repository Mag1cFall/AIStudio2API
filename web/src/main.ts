import { createApp } from 'vue'
import App from './App.vue'
import { vTooltip } from './tooltip'
import './style.css'

createApp(App).directive('tooltip', vTooltip).mount('#app')
