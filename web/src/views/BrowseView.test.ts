import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import BrowseView from './BrowseView.vue'
import * as api from '../api'
import { useLibraryStore } from '../stores/library'

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => {
    resolve = done
  })
  return { promise, resolve }
}

vi.mock('../api', () => ({
  getBrowse: vi.fn(),
  getFileTags: vi.fn(),
  addTag: vi.fn(),
  removeTag: vi.fn(),
  getTags: vi.fn(),
  getStatus: vi.fn(),
  rescan: vi.fn(),
  browseDownloadUrl: vi.fn((path: string) => `/download?path=${path}`),
}))

describe('BrowseView', () => {
  beforeEach(() => {
    vi.mocked(api.getBrowse).mockReset()
	vi.mocked(api.getStatus).mockReset()
	vi.mocked(api.rescan).mockReset()
    vi.mocked(api.getBrowse)
      .mockResolvedValueOnce({
        files: [],
        total: 0,
        page: 1,
        directories: [{ name: 'Artist', path: 'Artist' }],
      })
      .mockResolvedValueOnce({
        files: [{
          id: 'one',
          name: 'track.flac',
          path: 'Artist/track.flac',
          dir: 'Artist',
          kind: 'audio',
          mimeType: 'audio/flac',
          size: 1024,
          modified: '2026-07-15T00:00:00Z',
          previewable: false,
          playable: true,
          audioId: 'one',
          trackName: 'Track',
        }],
        total: 1,
        page: 1,
        directories: [],
      })
  })

  it('starts at root and navigates into a directory', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/browse', name: 'browse', component: BrowseView }],
    })
    await router.push('/browse')
    await router.isReady()
    const wrapper = mount(BrowseView, { global: { plugins: [createPinia(), router] } })
    await flushPromises()

    expect(api.getBrowse).toHaveBeenNthCalledWith(1, '.', 1, 50, expect.any(AbortSignal))
    expect(wrapper.get('.browse-list').findAll('.directory-row')).toHaveLength(1)
    await wrapper.get('.directory-row').trigger('click')
    await flushPromises()

    expect(api.getBrowse).toHaveBeenNthCalledWith(2, 'Artist', 1, 50, expect.any(AbortSignal))
    expect(router.currentRoute.value.query.dir).toBe('Artist')
    expect(wrapper.text()).toContain('track.flac')
  })

  it('ignores a late response from the previous directory', async () => {
    const root = deferred<Awaited<ReturnType<typeof api.getBrowse>>>()
    const artist = deferred<Awaited<ReturnType<typeof api.getBrowse>>>()
    vi.mocked(api.getBrowse).mockReset()
    vi.mocked(api.getBrowse).mockImplementation(dir => dir === '.' ? root.promise : artist.promise)

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/browse', name: 'browse', component: BrowseView }],
    })
    await router.push('/browse')
    await router.isReady()
    const wrapper = mount(BrowseView, { global: { plugins: [createPinia(), router] } })

    await router.push({ name: 'browse', query: { dir: 'Artist' } })
    artist.resolve({
      files: [{
        id: 'new',
        name: 'new.flac',
        path: 'Artist/new.flac',
        dir: 'Artist',
        kind: 'audio',
        mimeType: 'audio/flac',
        size: 1,
        modified: '2026-07-15T00:00:00Z',
        previewable: false,
        playable: true,
        audioId: 'new',
      }],
      total: 1,
      page: 1,
      directories: [],
    })
    await flushPromises()
    expect(wrapper.text()).toContain('new.flac')

    root.resolve({
      files: [{
        id: 'old',
        name: 'old.flac',
        path: 'old.flac',
        dir: '.',
        kind: 'audio',
        mimeType: 'audio/flac',
        size: 1,
        modified: '2026-07-15T00:00:00Z',
        previewable: false,
        playable: true,
        audioId: 'old',
      }],
      total: 1,
      page: 1,
      directories: [],
    })
    await flushPromises()

    expect(wrapper.text()).toContain('new.flac')
    expect(wrapper.text()).not.toContain('old.flac')
  })

	it('starts a rescan from the browse toolbar', async () => {
	  const router = createRouter({
		history: createMemoryHistory(),
		routes: [{ path: '/browse', name: 'browse', component: BrowseView }],
	  })
	  await router.push('/browse')
	  await router.isReady()
	  const pinia = createPinia()
	  const library = useLibraryStore(pinia)
	  library.status = {
		fileCount: 1,
		libraryReady: true,
		libraryGeneration: 1,
		scanStatus: 'idle',
		uptime: '1s',
		lastScan: '2026-07-16T00:00:00Z',
		scanError: null,
		authRequired: false,
		authenticated: true,
	  }
	  vi.mocked(api.rescan).mockResolvedValue(undefined)
	  vi.mocked(api.getStatus).mockResolvedValue({ ...library.status, scanStatus: 'scanning' })
	  const wrapper = mount(BrowseView, { global: { plugins: [pinia, router] } })
	  await flushPromises()

	  await wrapper.get('.rescan-button').trigger('click')
	  await flushPromises()

	  expect(api.rescan).toHaveBeenCalledTimes(1)
	  expect(library.scanStatus).toBe('scanning')
	})

  it('replaces the current page instead of appending results', async () => {
    vi.mocked(api.getBrowse).mockReset()
    vi.mocked(api.getBrowse)
      .mockResolvedValueOnce({
        files: [{ id: 'first', name: 'first.flac', path: 'first.flac', dir: '.', kind: 'audio', mimeType: 'audio/flac', size: 1, modified: '', previewable: false, playable: true, audioId: 'first' }],
        directories: [], total: 51, page: 1,
      })
      .mockResolvedValueOnce({
        files: [{ id: 'last', name: 'last.flac', path: 'last.flac', dir: '.', kind: 'audio', mimeType: 'audio/flac', size: 1, modified: '', previewable: false, playable: true, audioId: 'last' }],
        directories: [], total: 51, page: 2,
      })
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/browse', name: 'browse', component: BrowseView }] })
    await router.push('/browse')
    await router.isReady()
    const wrapper = mount(BrowseView, { global: { plugins: [createPinia(), router] } })
    await flushPromises()
    await wrapper.findAll('.pagination button')[1].trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('last.flac')
    expect(wrapper.text()).not.toContain('first.flac')
    expect(api.getBrowse).toHaveBeenLastCalledWith('.', 2, 50, expect.any(AbortSignal))
  })
})
