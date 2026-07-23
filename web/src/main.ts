import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import { installAuthGuard, routes } from './router'
import './assets/main.css'

const router = createRouter({ history: createWebHistory(), routes })
const pinia = createPinia()
installAuthGuard(router, pinia)
const app = createApp(App)
app.use(pinia)
app.use(router)
app.mount('#app')
