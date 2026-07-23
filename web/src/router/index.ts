import HomeView from '../views/HomeView.vue'
import BrowseView from '../views/BrowseView.vue'
import TagsView from '../views/TagsView.vue'
import LoginView from '../views/LoginView.vue'
import GraveyardView from '../views/GraveyardView.vue'
import TagsLayout from '../views/TagsLayout.vue'
import type { Pinia } from 'pinia'
import type { RouteRecordRaw, Router } from 'vue-router'
import { useAuthStore } from '../stores/auth'

export const routes: RouteRecordRaw[] = [
  { path: '/', name: 'home', component: HomeView },
  { path: '/browse', name: 'browse', component: BrowseView },
  {
    path: '/tags',
    component: TagsLayout,
    children: [
      { path: '', name: 'tags', component: TagsView },
      { path: 'graveyard', name: 'graveyard', component: GraveyardView },
    ],
  },
  { path: '/graveyard', redirect: { name: 'graveyard' } },
  { path: '/login', name: 'login', component: LoginView },
]

export function installAuthGuard(router: Router, pinia: Pinia) {
  router.beforeEach(async (to) => {
    const auth = useAuthStore(pinia)
    if (!auth.initialized) {
      await auth.checkAuth()
    }
    if (to.name === 'login' && (!auth.authRequired || auth.isLoggedIn)) {
      return { name: 'home' }
    }
    if (to.name !== 'login' && auth.authRequired && !auth.isLoggedIn) {
      return { name: 'login', query: { redirect: to.fullPath } }
    }
  })
}
