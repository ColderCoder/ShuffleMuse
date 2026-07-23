import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import SearchBar from './SearchBar.vue'
import * as api from '../api'

vi.mock('../api', () => ({
  search: vi.fn(),
}))

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => {
    resolve = done
  })
  return { promise, resolve }
}

describe('SearchBar', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.mocked(api.search).mockReset()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('ignores results from an older query that resolves last', async () => {
    const oldSearch = deferred<Awaited<ReturnType<typeof api.search>>>()
    const newSearch = deferred<Awaited<ReturnType<typeof api.search>>>()
    vi.mocked(api.search)
      .mockReturnValueOnce(oldSearch.promise)
      .mockReturnValueOnce(newSearch.promise)
    const wrapper = mount(SearchBar, { global: { plugins: [createPinia()] } })
    const input = wrapper.get('input')

    await input.setValue('old')
    await vi.advanceTimersByTimeAsync(300)
    await input.setValue('new')
    await vi.advanceTimersByTimeAsync(300)

    newSearch.resolve({
      items: [{ id: 'new', name: 'New Track', dir: 'New', filepath: 'New/new.flac' }],
      total: 1,
      page: 1,
      generation: 1,
      query: 'new',
    })
    await flushPromises()
    expect(wrapper.text()).toContain('New Track')

    oldSearch.resolve({
      items: [{ id: 'old', name: 'Old Track', dir: 'Old', filepath: 'Old/old.flac' }],
      total: 1,
      page: 1,
      generation: 1,
      query: 'old',
    })
    await flushPromises()
    expect(wrapper.text()).toContain('New Track')
    expect(wrapper.text()).not.toContain('Old Track')
  })

  it('uses combobox semantics, keyboard selection, and paged loading', async () => {
    vi.mocked(api.search)
      .mockResolvedValueOnce({
        items: [{ id: 'one', name: 'One', dir: 'A', filepath: 'A/one.flac' }],
        total: 51,
        page: 1,
        generation: 1,
        query: 'track',
      })
      .mockResolvedValueOnce({
        items: [{ id: 'two', name: 'Two', dir: 'B', filepath: 'B/two.flac' }],
        total: 51,
        page: 2,
        generation: 1,
        query: 'track',
      })
    const wrapper = mount(SearchBar, { global: { plugins: [createPinia()] } })
    const input = wrapper.get('input')
    await input.setValue('track')
    await vi.advanceTimersByTimeAsync(300)
    await flushPromises()

    expect(input.attributes('role')).toBe('combobox')
    expect(wrapper.get('[role="listbox"]').attributes('aria-label')).toBe('Track results')
    expect(wrapper.get('[role="option"]').element.tagName).toBe('BUTTON')
    await input.trigger('keydown', { key: 'ArrowDown' })
    expect(input.attributes('aria-activedescendant')).toContain('option-0')

    await wrapper.get('.search-more').trigger('click')
    await flushPromises()
    expect(api.search).toHaveBeenNthCalledWith(2, 'track', 2, 50, expect.any(AbortSignal))
    expect(wrapper.text()).toContain('Two')
  })

  it('aborts an obsolete network search', async () => {
    const pending = deferred<Awaited<ReturnType<typeof api.search>>>()
    vi.mocked(api.search).mockReturnValue(pending.promise)
    const wrapper = mount(SearchBar, { global: { plugins: [createPinia()] } })
    const input = wrapper.get('input')

    await input.setValue('old')
    await vi.advanceTimersByTimeAsync(300)
    const oldSignal = vi.mocked(api.search).mock.calls[0][3]
    await input.setValue('new')

    expect(oldSignal?.aborted).toBe(true)
    wrapper.unmount()
  })
})
