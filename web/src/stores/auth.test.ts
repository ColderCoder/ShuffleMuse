import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAuthStore } from './auth'
import * as api from '../api'

vi.mock('../api', () => ({
  getStatus: vi.fn(),
  login: vi.fn(),
  logout: vi.fn(),
}))

describe('auth store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('initializes from server status only once', async () => {
    vi.mocked(api.getStatus).mockResolvedValue({
      fileCount: 3,
      libraryReady: true,
      libraryGeneration: 1,
      scanStatus: 'idle',
      uptime: '1s',
      lastScan: new Date().toISOString(),
      scanError: '',
      authRequired: true,
      authenticated: false,
    })
    const auth = useAuthStore()

    await Promise.all([auth.checkAuth(), auth.checkAuth()])
    await auth.checkAuth()

    expect(api.getStatus).toHaveBeenCalledTimes(1)
    expect(auth.initialized).toBe(true)
    expect(auth.authRequired).toBe(true)
    expect(auth.isLoggedIn).toBe(false)
  })

  it('updates authentication state after login and logout', async () => {
    vi.mocked(api.login).mockResolvedValue({ status: 'logged in' })
    vi.mocked(api.logout).mockResolvedValue(undefined)
    const auth = useAuthStore()

    await auth.login('secret', true)
    expect(auth.isLoggedIn).toBe(true)
    expect(auth.authRequired).toBe(true)

    await auth.logout()
    expect(auth.isLoggedIn).toBe(false)
  })

  it('clears local authentication state when logout cannot reach the server', async () => {
    vi.mocked(api.login).mockResolvedValue({ status: 'logged in' })
    vi.mocked(api.logout).mockRejectedValue(new Error('offline'))
    const auth = useAuthStore()

    await auth.login('secret', false)
    await expect(auth.logout()).resolves.toBeUndefined()

    expect(auth.state).toBe('anonymous')
    expect(auth.isLoggedIn).toBe(false)
  })

  it('exposes an unavailable state and retries explicitly', async () => {
    vi.mocked(api.getStatus)
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({ authRequired: false, authenticated: true })
    const auth = useAuthStore()

    await auth.checkAuth()
    expect(auth.state).toBe('unavailable')
    expect(auth.isLoggedIn).toBe(false)

    await auth.checkAuth(true)
    expect(auth.state).toBe('authenticated')
    expect(auth.isLoggedIn).toBe(true)
  })
})
