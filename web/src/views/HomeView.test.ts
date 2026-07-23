import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import HomeView from './HomeView.vue'
import { usePlayerStore } from '../stores/player'

vi.mock('../api', () => ({
  getTags: vi.fn().mockResolvedValue([]),
  fileCoverUrl: vi.fn((id: string) => `/api/files/${id}/cover`),
  directoryCoverUrl: vi.fn((dir: string) => `/api/covers/directory?dir=${encodeURIComponent(dir)}`),
}))

async function mountHome(pinia = createPinia()) {
  setActivePinia(pinia)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: HomeView },
      { path: '/browse', name: 'browse', component: { template: '<div />' } },
    ],
  })
  await router.push('/')
  await router.isReady()
  return mount(HomeView, { global: { plugins: [pinia, router] } })
}

describe('HomeView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('keeps tag filtering without a large Randomize action', async () => {
    const wrapper = await mountHome()
    await flushPromises()

    expect(wrapper.find('#tag-filter').exists()).toBe(true)
    expect(wrapper.findAll('button').some(button => button.text().includes('Randomize'))).toBe(false)
  })

  it('delays a low-priority directory cover, then falls back to the track and placeholder', async () => {
    vi.useFakeTimers()
    const pinia = createPinia()
    setActivePinia(pinia)
    const player = usePlayerStore()
    player.currentTrack = {
      id: 'one',
      name: 'Track One',
      dir: 'Artist/Album',
      filepath: 'Artist/Album/track.flac',
      streamUrl: '/api/stream/one',
    }
    const wrapper = await mountHome(pinia)
    await wrapper.vm.$nextTick()

    const pathLink = wrapper.get('.now-playing-track-dir')
    expect(decodeURIComponent(pathLink.attributes('href') ?? '')).toBe('/browse?dir=Artist/Album')
    const cover = wrapper.get('img.now-playing-cover')
    expect(cover.attributes('src')).toBeUndefined()
    expect(cover.attributes('fetchpriority')).toBe('low')

    await vi.advanceTimersByTimeAsync(499)
    expect(cover.attributes('src')).toBeUndefined()
    await vi.advanceTimersByTimeAsync(1)
    expect(cover.attributes('src')).toBe('/api/covers/directory?dir=Artist%2FAlbum')

    await cover.trigger('error')
    expect(wrapper.get('img.now-playing-cover').attributes('src')).toBe('/api/files/one/cover')
    await wrapper.get('img.now-playing-cover').trigger('error')
    expect(wrapper.find('img.now-playing-cover').exists()).toBe(false)

    const placeholder = wrapper.get('.now-playing-cover--placeholder')
    expect(placeholder.attributes('role')).toBe('img')
    expect(placeholder.attributes('aria-label')).toBe('No cover art available for Track One')

    player.currentTrack = {
      id: 'two',
      name: 'Track Two',
      dir: 'Artist/Album',
      filepath: 'Artist/Album/track-two.flac',
      streamUrl: '/api/stream/two',
    }
    await wrapper.vm.$nextTick()
    expect(wrapper.get('img.now-playing-cover').attributes('src')).toBeUndefined()
    expect(wrapper.find('.now-playing-cover--placeholder').exists()).toBe(false)
    await vi.advanceTimersByTimeAsync(500)
    expect(wrapper.get('img.now-playing-cover').attributes('src')).toBe('/api/covers/directory?dir=Artist%2FAlbum')
  })

  it('reuses a successfully loaded directory URL and image node for the next track in that directory', async () => {
    vi.useFakeTimers()
    const pinia = createPinia()
    setActivePinia(pinia)
    const player = usePlayerStore()
    player.currentTrack = {
      id: 'one',
      name: 'Track One',
      dir: 'Artist/Album',
      filepath: 'Artist/Album/one.flac',
      streamUrl: '/api/stream/one',
    }
    const wrapper = await mountHome(pinia)
    await vi.advanceTimersByTimeAsync(500)
    const firstImage = wrapper.get('img.now-playing-cover')
    await firstImage.trigger('load')
    const element = firstImage.element

    player.currentTrack = {
      id: 'two',
      name: 'Track Two',
      dir: 'Artist/Album',
      filepath: 'Artist/Album/two.flac',
      streamUrl: '/api/stream/two',
    }
    await wrapper.vm.$nextTick()

    const secondImage = wrapper.get('img.now-playing-cover')
    expect(secondImage.element).toBe(element)
    expect(secondImage.attributes('src')).toBe('/api/covers/directory?dir=Artist%2FAlbum')
    expect(secondImage.attributes('alt')).toBe('Cover art for Track Two')
    expect(vi.getTimerCount()).toBe(0)
  })

  it('clears the previous source and pending timer when the track changes or the view unmounts', async () => {
    vi.useFakeTimers()
    const pinia = createPinia()
    setActivePinia(pinia)
    const player = usePlayerStore()
    player.currentTrack = {
      id: 'one',
      name: 'Track One',
      dir: 'First',
      filepath: 'First/one.flac',
      streamUrl: '/api/stream/one',
    }
    const wrapper = await mountHome(pinia)
    await vi.advanceTimersByTimeAsync(500)
    expect(wrapper.get('img.now-playing-cover').attributes('src')).toContain('First')

    player.currentTrack = {
      id: 'two',
      name: 'Track Two',
      dir: 'Second',
      filepath: 'Second/two.flac',
      streamUrl: '/api/stream/two',
    }
    await wrapper.vm.$nextTick()
    expect(wrapper.get('img.now-playing-cover').attributes('src')).toBeUndefined()
    expect(vi.getTimerCount()).toBe(1)

    wrapper.unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
})
