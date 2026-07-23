import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import FileList from './FileList.vue'
import * as api from '../api'

vi.mock('../api', () => ({
  getFileTags: vi.fn(),
  addTag: vi.fn(),
  removeTag: vi.fn(),
  browseDownloadUrl: vi.fn((path: string) => `/download?path=${path}`),
}))

const file = {
  id: 'one',
  name: 'one.flac',
  path: 'Album/one.flac',
  dir: 'Album',
  kind: 'audio' as const,
  mimeType: 'audio/flac',
  size: 1024,
  modified: '2026-07-15T00:00:00Z',
  previewable: false,
  playable: true,
  audioId: 'one',
  trackName: 'Track One',
}

describe('FileList', () => {
  beforeEach(() => {
    vi.mocked(api.getFileTags).mockResolvedValue(['favorite'])
    vi.mocked(api.addTag).mockResolvedValue(undefined)
  })

  it('loads real tags and supports adding a tag', async () => {
    const wrapper = mount(FileList, { props: { files: [file] } })
    expect(wrapper.find('.tag-count').exists()).toBe(false)

    await wrapper.get('.tag-button').trigger('click')
    await flushPromises()
    expect(wrapper.get('.tag-count').text()).toBe('1')
    expect(wrapper.text()).toContain('favorite')

    await wrapper.get('input[placeholder="Add a tag..."]').setValue('roadtrip')
    await wrapper.get('.tag-add-btn').trigger('click')
    await flushPromises()

    expect(api.addTag).toHaveBeenCalledWith('one', 'roadtrip')
    expect(wrapper.get('.tag-count').text()).toBe('2')
    expect(wrapper.emitted('tags-changed')).toHaveLength(1)
  })
})
