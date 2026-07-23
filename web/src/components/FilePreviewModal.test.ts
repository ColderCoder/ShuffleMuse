import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import FilePreviewModal from './FilePreviewModal.vue'
import * as api from '../api'

vi.mock('../api', () => ({
  browseContentUrl: (path: string) => `/content?path=${path}`,
  browseDownloadUrl: (path: string) => `/download?path=${path}`,
  getBrowseText: vi.fn(),
}))

afterEach(() => {
  document.body.innerHTML = ''
})

beforeEach(() => {
  vi.clearAllMocks()
})

describe('FilePreviewModal', () => {
  it('traps focus, closes with Escape, and restores focus', async () => {
    const opener = document.createElement('button')
    opener.textContent = 'Preview'
    document.body.appendChild(opener)
    opener.focus()
    const app = document.createElement('div')
    app.id = 'app'
    document.body.appendChild(app)

    const wrapper = mount(FilePreviewModal, {
      attachTo: app,
      props: {
        file: {
          id: 'cover', name: 'cover.jpg', path: 'cover.jpg', dir: '.', kind: 'image', mimeType: 'image/jpeg',
          size: 10, modified: '2026-01-01T00:00:00Z', previewable: true, playable: false,
        },
      },
    })
    await flushPromises()
    const close = document.querySelector<HTMLButtonElement>('[aria-label="Close preview"]')!
    const download = document.querySelector<HTMLAnchorElement>('[aria-label^="Download"]')!
    expect(document.activeElement).toBe(close)
    expect(app.inert).toBe(true)

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }))
    expect(document.activeElement).toBe(download)
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true }))
    expect(document.activeElement).toBe(close)

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
    expect(app.inert).toBe(false)
    expect(document.activeElement).toBe(opener)
  })

  it('cancels an unfinished text request when the preview closes', async () => {
    vi.mocked(api.getBrowseText).mockReturnValue(new Promise(() => {}))
    const wrapper = mount(FilePreviewModal, {
      props: {
        file: {
          id: 'notes', name: 'notes.txt', path: 'notes.txt', dir: '.', kind: 'text', mimeType: 'text/plain',
          size: 10, modified: '2026-01-01T00:00:00Z', previewable: true, playable: false,
        },
      },
    })
    await flushPromises()

    expect(api.getBrowseText).toHaveBeenCalledTimes(1)
    const signal = vi.mocked(api.getBrowseText).mock.calls[0][1]
    expect(signal?.aborted).toBe(false)

    wrapper.unmount()
    expect(signal?.aborted).toBe(true)
  })
})
