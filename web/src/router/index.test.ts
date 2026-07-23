import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { installAuthGuard, routes } from './index'
import { primaryNavigation } from '../navigation'
import * as api from '../api'

vi.mock('../api', () => ({ getStatus: vi.fn() }))

describe('auth route guard', () => {
  beforeEach(() => vi.resetAllMocks())

  it('redirects protected routes to login when authentication is required', async () => {
    vi.mocked(api.getStatus).mockResolvedValue({
      fileCount: 0,
      libraryReady: true,
      libraryGeneration: 1,
      scanStatus: 'idle',
      uptime: '1s',
      lastScan: new Date().toISOString(),
      scanError: '',
      authRequired: true,
      authenticated: false,
    })
    const pinia = createPinia()
    const router = createRouter({ history: createMemoryHistory(), routes })
    installAuthGuard(router, pinia)

    await router.push('/browse')
    expect(router.currentRoute.value.name).toBe('login')
    expect(router.currentRoute.value.query.redirect).toBe('/browse')
  })

  it('redirects an unnecessary login route to home', async () => {
    vi.mocked(api.getStatus).mockResolvedValue({
      fileCount: 0,
      libraryReady: true,
      libraryGeneration: 1,
      scanStatus: 'idle',
      uptime: '1s',
      lastScan: new Date().toISOString(),
      scanError: '',
      authRequired: false,
      authenticated: true,
    })
    const pinia = createPinia()
    const router = createRouter({ history: createMemoryHistory(), routes })
    installAuthGuard(router, pinia)

    await router.push('/login')
    expect(router.currentRoute.value.name).toBe('home')
  })

  it('nests Tags and Graveyard under the Tags route', () => {
    const tagsRoute = routes.find(route => route.path === '/tags')

    expect(tagsRoute?.children).toEqual(expect.arrayContaining([
      expect.objectContaining({ path: '', name: 'tags' }),
      expect.objectContaining({ path: 'graveyard', name: 'graveyard' }),
    ]))
  })

  it('redirects the legacy Graveyard URL to the nested page', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes })

    await router.push('/graveyard')
    await router.isReady()

    expect(router.currentRoute.value.fullPath).toBe('/tags/graveyard')
    expect(router.currentRoute.value.name).toBe('graveyard')
    expect(router.currentRoute.value.redirectedFrom?.path).toBe('/graveyard')
  })

  it('keeps Graveyard out of the primary navigation', () => {
    expect(primaryNavigation.map(item => item.label)).toEqual(['Home', 'Browse', 'Tags'])
    expect(primaryNavigation.some(item => item.to.includes('graveyard'))).toBe(false)
  })
})
