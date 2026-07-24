import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import NowPlayingBar from './NowPlayingBar.vue'
import { usePlayerStore } from '../stores/player'
import { useLibraryStore } from '../stores/library'
import * as api from '../api'

vi.mock('../api', () => ({
  getFileTags: vi.fn().mockResolvedValue([]),
  addTag: vi.fn(),
  removeTag: vi.fn(),
  getTags: vi.fn().mockResolvedValue([]),
}))

async function mountBar() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/browse', name: 'browse', component: { template: '<div />' } },
    ],
  })
  await router.push('/')
  await router.isReady()
  return mount(NowPlayingBar, { global: { plugins: [router] } })
}

describe('NowPlayingBar', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(api.getFileTags).mockReset()
    vi.mocked(api.getFileTags).mockResolvedValue([])
    vi.mocked(api.getTags).mockReset()
    vi.mocked(api.getTags).mockResolvedValue([])
    vi.mocked(api.addTag).mockReset()
    vi.mocked(api.removeTag).mockReset()
  })

  it('centers play between previous and next and exposes a seekable timeline', async () => {
    const player = usePlayerStore()
    player.currentTrack = {
      id: 'one',
      name: 'Track One',
      dir: 'Album',
      filepath: 'Album/one.flac',
      streamUrl: '/api/stream/one?mode=original',
    }
    player.mediaMetadata = {
      title: 'Metadata Title',
      codec: 'FLAC',
      bitrateKbps: 987,
      bitrateApproximate: false,
      durationSeconds: 240,
    }
    player.duration = 240
    player.currentTime = 60
    const seek = vi.spyOn(player, 'seek').mockResolvedValue(undefined)

    const wrapper = await mountBar()
    const labels = wrapper.findAll('.np-transport button').map(button => button.attributes('aria-label'))
    expect(labels).toEqual(['Previous track', 'Play', 'Next track'])
    expect(wrapper.text()).toContain('FLAC · 987 kbps')
    expect(wrapper.get('.np-track-name').text()).toBe('Metadata Title')
    const path = wrapper.get('.np-track-path')
    expect(path.element.tagName).toBe('A')
    expect(decodeURIComponent(path.attributes('href') ?? '')).toBe('/browse?dir=Album')
    expect(path.attributes('aria-label')).toBe('Browse folder containing Metadata Title')

    const slider = wrapper.get('input[aria-label="Playback position"]')
    expect(slider.attributes('max')).toBe('240')
    await slider.setValue('120')
    await slider.trigger('change')
    expect(seek).toHaveBeenCalledWith(120)
  })

  it('shows the effective configured Opus bitrate', async () => {
    const library = useLibraryStore()
    library.status = {
      authRequired: false,
      authenticated: true,
      opusBitrate: 224,
    }
    const player = usePlayerStore()
    player.streamMode = 'opus'
    player.currentTrack = {
      id: 'one',
      name: 'Track One',
      dir: 'Album',
      filepath: 'Album/one.flac',
      streamUrl: '/api/stream/one?mode=opus',
    }

    const wrapper = await mountBar()

    expect(wrapper.text()).toContain('OPUS · 224 kbps')
    const opus = wrapper.get('.stream-mode-button:last-child')
    expect(opus.text()).toBe('Opus 224k')
    expect(opus.attributes('title')).toBe('Stream Opus at 224 kbps')
  })

  it('keeps the favorite state for the newest track', async () => {
    let resolveOld!: (tags: string[]) => void
    vi.mocked(api.getFileTags).mockImplementation(id => (
      id === 'one'
        ? new Promise(done => { resolveOld = done })
        : Promise.resolve(['favorite'])
    ))
    const player = usePlayerStore()
    player.currentTrack = {
      id: 'one',
      name: 'One',
      dir: '.',
      filepath: 'one.flac',
      streamUrl: '/api/stream/one?mode=original',
    }
    const wrapper = await mountBar()

    player.currentTrack = {
      id: 'two',
      name: 'Two',
      dir: '.',
      filepath: 'two.flac',
      streamUrl: '/api/stream/two?mode=original',
    }
    await flushPromises()
    expect(wrapper.get('.btn-fav').classes()).toContain('btn-fav--active')

    resolveOld([])
    await flushPromises()
    expect(wrapper.get('.btn-fav').classes()).toContain('btn-fav--active')
  })

  it('handles a failed favorite mutation without changing state', async () => {
    vi.mocked(api.addTag).mockRejectedValue(new Error('failed'))
    const player = usePlayerStore()
    player.currentTrack = {
      id: 'one',
      name: 'One',
      dir: '.',
      filepath: 'one.flac',
      streamUrl: '/api/stream/one?mode=original',
    }
    const wrapper = await mountBar()
    await flushPromises()

    await wrapper.get('.btn-fav').trigger('click')
    await flushPromises()

    expect(wrapper.get('.btn-fav').classes()).not.toContain('btn-fav--active')
    expect(wrapper.text()).toContain('Failed to update favorite')
  })
})
