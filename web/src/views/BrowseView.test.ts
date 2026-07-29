import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import BrowseView from './BrowseView.vue'
import * as api from '../api'
import { useLibraryStore } from '../stores/library'
import { usePlayerStore } from '../stores/player'

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
        audioCount: 0,
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
        audioCount: 1,
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
      audioCount: 1,
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
      audioCount: 1,
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

  it('plays the current folder from the queue front', async () => {
    vi.mocked(api.getBrowse).mockReset()
    vi.mocked(api.getBrowse).mockResolvedValue({
      files: [{
        id: 'one',
        name: 'track.flac',
        path: 'Artist/track.flac',
        dir: 'Artist',
        kind: 'audio',
        mimeType: 'audio/flac',
        size: 1,
        modified: '',
        previewable: false,
        playable: true,
        audioId: 'one',
      }],
      directories: [],
      total: 1,
      audioCount: 1,
      page: 1,
    })
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/browse', name: 'browse', component: BrowseView }],
    })
    await router.push({ name: 'browse', query: { dir: 'Artist' } })
    await router.isReady()
    const pinia = createPinia()
    const player = usePlayerStore(pinia)
    player.queue = { id: 'queue', tag: '', createdGeneration: 1, total: 1, pageSize: 200 }
    const prepend = vi.spyOn(player, 'prependDirectory').mockResolvedValue(1)
    const wrapper = mount(BrowseView, { global: { plugins: [pinia, router] } })
    await flushPromises()

    const button = wrapper.get('.folder-play-button')
    expect(button.text()).toContain('Play folder')
    expect(button.attributes('disabled')).toBeUndefined()
    await button.trigger('click')
    await flushPromises()

    expect(prepend).toHaveBeenCalledWith('Artist')
    expect(wrapper.get('.queue-message').text()).toBe('Playing 1 track from this folder')
  })

  it('disables empty folders and reports a directory queue failure', async () => {
    vi.mocked(api.getBrowse).mockReset()
    vi.mocked(api.getBrowse)
      .mockResolvedValueOnce({
        files: [],
        directories: [],
        total: 0,
        audioCount: 0,
        page: 1,
      })
      .mockResolvedValueOnce({
        files: [{
          id: 'one',
          name: 'track.flac',
          path: 'Artist/track.flac',
          dir: 'Artist',
          kind: 'audio',
          mimeType: 'audio/flac',
          size: 1,
          modified: '',
          previewable: false,
          playable: true,
          audioId: 'one',
        }],
        directories: [],
        total: 1,
        audioCount: 1,
        page: 1,
      })
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/browse', name: 'browse', component: BrowseView }],
    })
    await router.push('/browse')
    await router.isReady()
    const pinia = createPinia()
    const player = usePlayerStore(pinia)
    player.queue = { id: 'queue', tag: '', createdGeneration: 1, total: 1, pageSize: 200 }
    const prepend = vi.spyOn(player, 'prependDirectory').mockRejectedValue(new Error('queue failed'))
    const wrapper = mount(BrowseView, { global: { plugins: [pinia, router] } })
    await flushPromises()

    expect(wrapper.get('.folder-play-button').attributes('disabled')).toBeDefined()

    await router.push({ name: 'browse', query: { dir: 'Artist' } })
    await flushPromises()
    const button = wrapper.get('.folder-play-button')
    expect(button.attributes('disabled')).toBeUndefined()
    await button.trigger('click')
    await flushPromises()

    expect(prepend).toHaveBeenCalledWith('Artist')
    expect(wrapper.get('[role="alert"]').text()).toBe('Failed to add this folder to the playlist')
  })

  it('replaces the current page instead of appending results', async () => {
    vi.mocked(api.getBrowse).mockReset()
    vi.mocked(api.getBrowse)
      .mockResolvedValueOnce({
        files: [{ id: 'first', name: 'first.flac', path: 'first.flac', dir: '.', kind: 'audio', mimeType: 'audio/flac', size: 1, modified: '', previewable: false, playable: true, audioId: 'first' }],
        directories: [], total: 51, audioCount: 51, page: 1,
      })
      .mockResolvedValueOnce({
        files: [{ id: 'last', name: 'last.flac', path: 'last.flac', dir: '.', kind: 'audio', mimeType: 'audio/flac', size: 1, modified: '', previewable: false, playable: true, audioId: 'last' }],
        directories: [], total: 51, audioCount: 51, page: 2,
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
