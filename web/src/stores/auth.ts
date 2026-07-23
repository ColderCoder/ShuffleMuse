import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import * as api from '../api'

export type AuthState = 'checking' | 'authenticated' | 'anonymous' | 'unavailable'

export const useAuthStore = defineStore('auth', () => {
  const state = ref<AuthState>('checking')
  const authRequired = ref(false)
  let checking: Promise<void> | null = null

  const isLoggedIn = computed(() => state.value === 'authenticated')
  const initialized = computed(() => state.value !== 'checking')

  async function login(password: string, remember: boolean) {
    await api.login(password, remember)
    authRequired.value = true
    state.value = 'authenticated'
  }

  async function logout() {
	try {
	  await api.logout()
	} catch {
	  // Logging out locally must remain available when the server is unreachable.
	} finally {
	  state.value = 'anonymous'
	}
  }

  function expire() {
    authRequired.value = true
    state.value = 'anonymous'
  }

  async function checkAuth(force = false) {
    if (!force && initialized.value) return
    if (checking) return checking
    state.value = 'checking'
    checking = (async () => {
      try {
        const status = await api.getStatus()
        authRequired.value = status.authRequired
        state.value = !status.authRequired || status.authenticated ? 'authenticated' : 'anonymous'
      } catch {
        state.value = 'unavailable'
      } finally {
        checking = null
      }
    })()
    return checking
  }

  return {
    state,
    isLoggedIn,
    authRequired,
    initialized,
    login,
    logout,
    expire,
    checkAuth,
  }
})
