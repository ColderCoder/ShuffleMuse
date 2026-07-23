import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useTagsStore } from './tags'
import * as api from '../api'

vi.mock('../api', () => ({
  getTags: vi.fn(),
  getTagFiles: vi.fn(),
}))

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => {
    resolve = done
  })
  return { promise, resolve }
}

function file(id: string): api.FileEntry {
  return { id, name: id, dir: '.', filepath: `${id}.flac` }
}

describe('tags store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(api.getTags).mockReset()
    vi.mocked(api.getTagFiles).mockReset()
  })

  it('loads only one bounded page for a selected tag', async () => {
    const firstPage = Array.from({ length: 200 }, (_, index) => file(`track-${index}`))
    vi.mocked(api.getTagFiles).mockResolvedValueOnce({ items: firstPage, total: 201, page: 1, generation: 1 })
    const store = useTagsStore()

    await store.selectTag('favorite')

    expect(api.getTagFiles).toHaveBeenCalledOnce()
    expect(api.getTagFiles).toHaveBeenCalledWith('favorite', 1, 200, expect.any(AbortSignal))
    expect(store.tagFiles).toHaveLength(200)
    expect(store.tagFileTotal).toBe(201)
    expect(store.tagFilePageCount).toBe(2)
  })

  it('ignores a selected-tag response after a newer selection', async () => {
    const oldRequest = deferred<Awaited<ReturnType<typeof api.getTagFiles>>>()
    vi.mocked(api.getTagFiles).mockImplementation(name => (
      name === 'old'
        ? oldRequest.promise
        : Promise.resolve({ items: [file('new-track')], total: 1, page: 1, generation: 1 })
    ))
    const store = useTagsStore()

    const oldSelection = store.selectTag('old')
    await store.selectTag('new')
    oldRequest.resolve({ items: [file('old-track')], total: 1, page: 1, generation: 1 })
    await oldSelection

    expect(store.selectedTag).toBe('new')
    expect(store.tagFiles.map(item => item.id)).toEqual(['new-track'])
  })

  it('replaces the bounded page when navigating', async () => {
    vi.mocked(api.getTagFiles)
      .mockResolvedValueOnce({ items: [file('first')], total: 201, page: 1, generation: 1 })
      .mockResolvedValueOnce({ items: [file('last')], total: 201, page: 2, generation: 1 })
    const store = useTagsStore()

    await store.selectTag('favorite')
    await store.loadTagPage(2)

    expect(api.getTagFiles).toHaveBeenCalledTimes(2)
    expect(api.getTagFiles).toHaveBeenLastCalledWith('favorite', 2, 200, expect.any(AbortSignal))
    expect(store.tagFiles.map(item => item.id)).toEqual(['last'])
    expect(store.tagFilePage).toBe(2)
  })
})
