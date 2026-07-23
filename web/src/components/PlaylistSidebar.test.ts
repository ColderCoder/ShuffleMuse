import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import PlaylistSidebar from './PlaylistSidebar.vue'
import { usePlayerStore } from '../stores/player'
import * as api from '../api'

vi.mock('../api', () => ({
  createQueue: vi.fn(),
  getQueuePage: vi.fn(),
  getFileMetadata: vi.fn(),
}))

function queueItems(page: number): api.QueueItem[] {
  return Array.from({ length: 200 }, (_, offset) => {
    const index = (page - 1) * 200 + offset
    return {
      id: `track-${index}`,
      filepath: `track-${index}.flac`,
      name: `Track ${index}`,
      dir: '.',
      queueIndex: index,
      available: true,
    }
  })
}

function response(page: number): api.CreateQueueResponse {
  return {
    queue: { id: 'large', tag: '', createdGeneration: 1, total: 1200, pageSize: 200 },
    items: queueItems(page),
    page,
    libraryGeneration: 1,
    pinApplied: false,
  }
}

describe('PlaylistSidebar', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(api.createQueue).mockResolvedValue(response(1))
    vi.mocked(api.getQueuePage).mockImplementation(async (_id, page) => response(page))
    vi.mocked(api.getFileMetadata).mockResolvedValue({
      codec: 'FLAC', bitrateKbps: 1000, bitrateApproximate: false, durationSeconds: 1,
    })
  })

  it('renders at most 200 rows while reaching tracks 201 and 1001', async () => {
    const player = usePlayerStore()
    await player.preparePlaylist()
    const wrapper = mount(PlaylistSidebar)

    expect(wrapper.findAll('.playlist-row')).toHaveLength(200)
    expect(wrapper.findAll('.playlist-row')[0].text()).toContain('Track 0')

    const nextPage = () => wrapper.findAll('.playlist-navigation button')[1]
    await nextPage().trigger('click')
    await flushPromises()
    expect(wrapper.findAll('.playlist-row')[0].text()).toContain('Track 200')

    for (let i = 0; i < 4; i += 1) {
      await nextPage().trigger('click')
      await flushPromises()
    }
    expect(wrapper.findAll('.playlist-row')[0].text()).toContain('Track 1000')
    expect(wrapper.findAll('.playlist-row')).toHaveLength(200)
    expect(player.cachedPageCount).toBeLessThanOrEqual(5)
  })
})
