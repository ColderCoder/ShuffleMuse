import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import GraveyardView from './GraveyardView.vue'
import { useLibraryStore } from '../stores/library'
import * as api from '../api'

vi.mock('../api', () => ({
  getGraveyard: vi.fn(),
  deleteGraveyardEntry: vi.fn(),
}))

const missing = (name: string): api.GraveyardEntry => ({
  filepath: `Artist/${name}.flac`,
  name,
  dir: 'Artist',
  tags: ['favorite'],
})

describe('GraveyardView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.stubGlobal('confirm', vi.fn(() => true))
  })

  it('confirms deletion and returns from an emptied last page', async () => {
    vi.mocked(api.getGraveyard)
      .mockResolvedValueOnce({ items: [missing('first')], total: 51, page: 1, generation: 1 })
      .mockResolvedValueOnce({ items: [missing('last')], total: 51, page: 2, generation: 1 })
      .mockResolvedValueOnce({ items: [missing('first')], total: 50, page: 1, generation: 1 })
    vi.mocked(api.deleteGraveyardEntry).mockResolvedValue(undefined)
    const wrapper = mount(GraveyardView, { global: { plugins: [createPinia()] } })
    await flushPromises()

    await wrapper.findAll('.graveyard-pages button')[1].trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('last')

    await wrapper.get('.graveyard-delete').trigger('click')
    await flushPromises()
    expect(window.confirm).toHaveBeenCalledOnce()
    expect(api.deleteGraveyardEntry).toHaveBeenCalledWith('Artist/last.flac')
    expect(api.getGraveyard).toHaveBeenLastCalledWith(1, 50)
  })

  it('handles FILE_ONLINE without deleting the recovered entry', async () => {
    vi.mocked(api.getGraveyard).mockResolvedValue({ items: [missing('restored')], total: 1, page: 1, generation: 1 })
    vi.mocked(api.deleteGraveyardEntry).mockRejectedValue({
      isAxiosError: true,
      response: { status: 409, data: { code: 'FILE_ONLINE' } },
    })
    const wrapper = mount(GraveyardView, { global: { plugins: [createPinia()] } })
    await flushPromises()
    await wrapper.get('.graveyard-delete').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('online again')
    expect(wrapper.text()).toContain('restored')
  })

  it('refreshes the current page when the library generation changes', async () => {
    vi.mocked(api.getGraveyard).mockResolvedValue({ items: [], total: 0, page: 1, generation: 1 })
    const pinia = createPinia()
    const library = useLibraryStore(pinia)
    library.status = { authRequired: false, authenticated: true, libraryGeneration: 1 }
    mount(GraveyardView, { global: { plugins: [pinia] } })
    await flushPromises()

    library.status = { ...library.status, libraryGeneration: 2 }
    await flushPromises()

    expect(api.getGraveyard).toHaveBeenCalledTimes(2)
    expect(api.getGraveyard).toHaveBeenLastCalledWith(1, 50)
  })

  it('does not expose a rescan action', async () => {
    vi.mocked(api.getGraveyard).mockResolvedValue({ items: [], total: 0, page: 1, generation: 1 })
    const wrapper = mount(GraveyardView, { global: { plugins: [createPinia()] } })
    await flushPromises()

    const rescanAction = wrapper.findAll('button').find(button => (
      /rescan/i.test(`${button.attributes('aria-label') ?? ''} ${button.text()}`)
    ))
    expect(rescanAction).toBeUndefined()
  })
})
