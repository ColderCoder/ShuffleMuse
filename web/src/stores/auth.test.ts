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
    vi.resetAllMocks()
    setActivePinia(createPinia())
  })

  it('initializes from one shared status request and returns the full snapshot', async () => {
    const snapshot: api.Status = {
      fileCount: 3,
      libraryReady: true,
      libraryGeneration: 1,
      scanStatus: 'idle',
      uptime: '1s',
      lastScan: new Date().toISOString(),
      scanError: '',
      authRequired: true,
      authenticated: false,
    }
    vi.mocked(api.getStatus).mockResolvedValue(snapshot)
    const auth = useAuthStore()

    const [first, second] = await Promise.all([auth.checkAuth(), auth.checkAuth()])
    const cached = await auth.checkAuth()

    expect(api.getStatus).toHaveBeenCalledTimes(1)
    expect(first).toBe(snapshot)
    expect(second).toBe(snapshot)
    expect(cached).toEqual(snapshot)
    expect(auth.statusSnapshot).toEqual(snapshot)
    expect(auth.initialized).toBe(true)
    expect(auth.authRequired).toBe(true)
    expect(auth.isLoggedIn).toBe(false)
  })

  it('clears stale status snapshots on expiry, login, and logout', async () => {
    const anonymous: api.Status = {
      authRequired: true,
      authenticated: false,
    }
    const authenticated: api.Status = {
      fileCount: 3,
      libraryReady: true,
      libraryGeneration: 1,
      scanStatus: 'idle',
      authRequired: true,
      authenticated: true,
    }
    vi.mocked(api.getStatus)
      .mockResolvedValueOnce(anonymous)
      .mockResolvedValueOnce(authenticated)
    vi.mocked(api.login).mockResolvedValue({ status: 'logged in' })
    vi.mocked(api.logout).mockResolvedValue(undefined)
    const auth = useAuthStore()

    await auth.checkAuth()
    expect(auth.statusSnapshot).toEqual(anonymous)
    auth.expire()
    expect(auth.statusSnapshot).toBeNull()

    await auth.login('secret', true)
    expect(auth.statusSnapshot).toBeNull()
    await auth.checkAuth(true)
    expect(auth.statusSnapshot).toEqual(authenticated)

    await auth.logout()
    expect(auth.statusSnapshot).toBeNull()
    expect(auth.state).toBe('anonymous')
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

    await expect(auth.checkAuth()).resolves.toBeNull()
    expect(auth.state).toBe('unavailable')
    expect(auth.isLoggedIn).toBe(false)
    expect(auth.statusSnapshot).toBeNull()

    await expect(auth.checkAuth(true)).resolves.toEqual({ authRequired: false, authenticated: true })
    expect(auth.state).toBe('authenticated')
    expect(auth.isLoggedIn).toBe(true)
  })
})
