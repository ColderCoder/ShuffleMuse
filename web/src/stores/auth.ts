import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import * as api from '../api'

export type AuthState = 'checking' | 'authenticated' | 'anonymous' | 'unavailable'

export const useAuthStore = defineStore('auth', () => {
  const state = ref<AuthState>('checking')
  const authRequired = ref(false)
  const statusSnapshot = ref<api.Status | null>(null)
  let checking: Promise<api.Status | null> | null = null

  const isLoggedIn = computed(() => state.value === 'authenticated')
  const initialized = computed(() => state.value !== 'checking')

  async function login(password: string, remember: boolean) {
    await api.login(password, remember)
    statusSnapshot.value = null
    authRequired.value = true
    state.value = 'authenticated'
  }

  async function logout() {
    try {
      await api.logout()
    } catch {
      // Logging out locally must remain available when the server is unreachable.
    } finally {
      statusSnapshot.value = null
      state.value = 'anonymous'
    }
  }

  function expire() {
    statusSnapshot.value = null
    authRequired.value = true
    state.value = 'anonymous'
  }

  async function checkAuth(force = false): Promise<api.Status | null> {
    if (!force && initialized.value) return statusSnapshot.value
    if (checking) return checking
    state.value = 'checking'
    checking = (async () => {
      try {
        const status = await api.getStatus()
        statusSnapshot.value = status
        authRequired.value = status.authRequired
        state.value = !status.authRequired || status.authenticated ? 'authenticated' : 'anonymous'
        return status
      } catch {
        statusSnapshot.value = null
        state.value = 'unavailable'
        return null
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
    statusSnapshot,
    initialized,
    login,
    logout,
    expire,
    checkAuth,
  }
})
