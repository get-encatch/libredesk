import { createApp } from 'vue'
import './app.css'
import App from './App.vue'
import { router } from './router'
import { loadSession } from './session'
import { initTheme } from './theme'

initTheme()
loadSession()
createApp(App).use(router).mount('#app')
