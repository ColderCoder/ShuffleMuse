import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import TagsView from './TagsView.vue'
import TagManager from '../components/TagManager.vue'
import { useTagsStore } from '../stores/tags'
import { useLibraryStore } from '../stores/library'
import * as api from '../api'

vi.mock('../api', () => ({
  getTags: vi.fn(),
  exportTagsCSV: vi.fn(),
  getTagFiles: vi.fn(),
  getFileTags: vi.fn(),
  addTag: vi.fn(),
  removeTag: vi.fn(),
}))

describe('TagsView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(api.getTags).mockReset()
    vi.mocked(api.getTags).mockResolvedValue([{ name: 'favorite', count: 1 }])
    vi.mocked(api.exportTagsCSV).mockReset()
    vi.mocked(api.exportTagsCSV).mockResolvedValue(new Blob(['tags']))
    vi.mocked(api.getFileTags).mockReset()
    vi.mocked(api.getFileTags).mockResolvedValue(['favorite'])
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('downloads the complete tag CSV export', async () => {
    const wrapper = mount(TagsView)
    await flushPromises()
    const createObjectURL = vi.fn(() => 'blob:shufflemuse-tags')
    const revokeObjectURL = vi.fn()
    vi.stubGlobal('URL', { createObjectURL, revokeObjectURL })
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

    await wrapper.get('button[aria-label="Export tags as CSV"]').trigger('click')
    await flushPromises()

    expect(api.exportTagsCSV).toHaveBeenCalledOnce()
    expect(createObjectURL).toHaveBeenCalledOnce()
    expect(click).toHaveBeenCalledOnce()
    const link = click.mock.instances[0] as HTMLAnchorElement
    expect(link.download).toBe('shufflemuse-tags.csv')
    expect(link.href).toBe('blob:shufflemuse-tags')
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:shufflemuse-tags')
  })

  it('removes a file from the selected tag and refreshes tag counts', async () => {
    const store = useTagsStore()
    store.selectedTag = 'favorite'
    store.tagFiles = [{ id: 'one', name: 'One', dir: '.', filepath: 'one.flac' }]
    const wrapper = mount(TagsView)
    await flushPromises()

    await wrapper.get('button[aria-label="Manage tags for One"]').trigger('click')
    await flushPromises()
    wrapper.getComponent(TagManager).vm.$emit('tag-removed', 'favorite')
    await flushPromises()

    expect(store.tagFiles).toEqual([])
    expect(api.getTags).toHaveBeenCalledTimes(2)
  })

  it('refreshes tag data when the library generation changes', async () => {
    const library = useLibraryStore()
    library.status = { authRequired: false, authenticated: true, libraryReady: true, libraryGeneration: 1, scanStatus: 'idle' }
    mount(TagsView)
    await flushPromises()

    library.status = { ...library.status, libraryGeneration: 2 }
    await flushPromises()

    expect(api.getTags).toHaveBeenCalledTimes(2)
  })
})
