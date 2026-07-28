import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useLibraryStore } from './library'
import { usePlayerStore } from './player'
import { useAuthStore } from './auth'
import * as api from '../api'

vi.mock('../api', () => ({
  getStatus: vi.fn(),
  rescan: vi.fn(),
  createQueue: vi.fn(),
  getQueuePage: vi.fn(),
  selectQueueItem: vi.fn(),
  deleteQueue: vi.fn(),
  getFileMetadata: vi.fn(),
}))

function status(overrides: Partial<api.Status> = {}): api.Status {
  return {
    fileCount: 1,
    libraryReady: true,
    libraryGeneration: 1,
    scanStatus: 'idle',
    uptime: '1s',
    lastScan: '2026-07-16T00:00:00Z',
    scanError: null,
    authRequired: false,
    authenticated: true,
    ...overrides,
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { promise, resolve }
}

function setVisibility(state: DocumentVisibilityState, notify = true) {
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    value: state,
  })
  if (notify) document.dispatchEvent(new Event('visibilitychange'))
}

describe('library store', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.resetAllMocks()
    setVisibility('visible', false)
    setActivePinia(createPinia())
  })

  afterEach(() => {
    useLibraryStore().stop()
    setVisibility('visible', false)
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('reuses the auth status snapshot and waits 30 seconds while idle', async () => {
    const initial = status()
    vi.mocked(api.getStatus).mockResolvedValue(initial)
    const auth = useAuthStore()
    const library = useLibraryStore()

    const snapshot = await auth.checkAuth()
    await library.start(snapshot ?? undefined)

    expect(library.libraryReady).toBe(true)
    expect(api.getStatus).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(29999)
    expect(api.getStatus).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(api.getStatus).toHaveBeenCalledTimes(2)
  })

  it('reschedules between the 30 second idle and 2 second scanning intervals', async () => {
    vi.mocked(api.getStatus)
      .mockResolvedValueOnce(status())
      .mockResolvedValueOnce(status({ scanStatus: 'scanning' }))
      .mockResolvedValueOnce(status())
      .mockResolvedValue(status())
    const library = useLibraryStore()

    await library.start()
    expect(api.getStatus).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(29999)
    expect(api.getStatus).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(api.getStatus).toHaveBeenCalledTimes(2)
    expect(library.scanActive).toBe(true)

    await vi.advanceTimersByTimeAsync(1999)
    expect(api.getStatus).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(api.getStatus).toHaveBeenCalledTimes(3)
    expect(library.scanActive).toBe(false)

    await vi.advanceTimersByTimeAsync(29999)
    expect(api.getStatus).toHaveBeenCalledTimes(3)
    await vi.advanceTimersByTimeAsync(1)
    expect(api.getStatus).toHaveBeenCalledTimes(4)
  })

  it('pauses while hidden and refreshes immediately when visible again', async () => {
    vi.mocked(api.getStatus)
      .mockResolvedValueOnce(status())
      .mockResolvedValue(status({ scanStatus: 'scanning' }))
    const library = useLibraryStore()

    await library.start()
    setVisibility('hidden')
    await vi.advanceTimersByTimeAsync(60000)
    expect(api.getStatus).toHaveBeenCalledTimes(1)

    setVisibility('visible')
    await vi.advanceTimersByTimeAsync(0)
    expect(api.getStatus).toHaveBeenCalledTimes(2)
    expect(library.scanActive).toBe(true)

    await vi.advanceTimersByTimeAsync(1999)
    expect(api.getStatus).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(api.getStatus).toHaveBeenCalledTimes(3)
  })

  it('cancels the idle timer when a rescan starts and enters the 2 second cycle', async () => {
    vi.mocked(api.rescan).mockResolvedValue(undefined)
    vi.mocked(api.getStatus).mockResolvedValue(status({ scanStatus: 'scanning' }))
    const library = useLibraryStore()

    await library.start(status())
    await vi.advanceTimersByTimeAsync(5000)
    await library.requestRescan()

    expect(api.rescan).toHaveBeenCalledTimes(1)
    expect(api.getStatus).toHaveBeenCalledTimes(1)
    expect(vi.getTimerCount()).toBe(1)
    await vi.advanceTimersByTimeAsync(1999)
    expect(api.getStatus).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(api.getStatus).toHaveBeenCalledTimes(2)
  })

  it('removes its visibility listener and leaves no polling behind after stop', async () => {
    const addListener = vi.spyOn(document, 'addEventListener')
    const removeListener = vi.spyOn(document, 'removeEventListener')
    vi.mocked(api.getStatus).mockResolvedValue(status())
    const library = useLibraryStore()

    await library.start(status())
    const registration = addListener.mock.calls.find(([event]) => event === 'visibilitychange')
    expect(registration).toBeDefined()

    library.stop()

    expect(removeListener).toHaveBeenCalledWith('visibilitychange', registration?.[1])
    await vi.advanceTimersByTimeAsync(60000)
    setVisibility('hidden')
    setVisibility('visible')
    await vi.advanceTimersByTimeAsync(0)
    expect(api.getStatus).not.toHaveBeenCalled()
  })

  it('tracks initialization and becomes ready after a later status update', async () => {
    vi.mocked(api.getStatus)
      .mockResolvedValueOnce(status({
        fileCount: 0,
        libraryReady: false,
        libraryGeneration: 0,
        scanStatus: 'initializing',
        lastScan: null,
      }))
      .mockResolvedValueOnce(status())
    const library = useLibraryStore()

    await library.start()
    expect(library.libraryReady).toBe(false)
    expect(library.scanActive).toBe(true)

    await library.refreshStatus()
    expect(library.libraryReady).toBe(true)
    expect(library.libraryGeneration).toBe(1)
    library.stop()
  })

  it('starts a later rescan without changing player or queue state', async () => {
    vi.mocked(api.createQueue).mockResolvedValue({
      queue: { id: 'queue', tag: '', createdGeneration: 1, total: 1, pageSize: 200 },
      items: [{ id: 'one', name: 'One', dir: '.', filepath: 'one.flac', queueIndex: 0, available: true }],
      page: 1,
      libraryGeneration: 1,
      pinApplied: false,
    })
    vi.mocked(api.getFileMetadata).mockResolvedValue({
      codec: 'FLAC',
      bitrateKbps: 1000,
      bitrateApproximate: false,
      durationSeconds: 180,
    })
    vi.mocked(api.rescan).mockResolvedValue(undefined)
    vi.mocked(api.getStatus).mockResolvedValue(status({ scanStatus: 'scanning' }))
    const player = usePlayerStore()
    const library = useLibraryStore()
    await player.preparePlaylist()
    player.currentTime = 42
    player.isPlaying = true
    library.status = status()

    await library.requestRescan()

    expect(api.rescan).toHaveBeenCalledTimes(1)
    expect(player.currentTrack?.id).toBe('one')
    expect(player.playlist.map(file => file.id)).toEqual(['one'])
    expect(player.currentTime).toBe(42)
    expect(player.isPlaying).toBe(true)
    expect(library.scanStatus).toBe('scanning')
    library.stop()
  })

  it('preserves the last status when polling fails', async () => {
    vi.mocked(api.getStatus)
      .mockResolvedValueOnce(status())
      .mockRejectedValueOnce(new Error('offline'))
    const library = useLibraryStore()
    await library.start()
    await library.refreshStatus()

    expect(library.libraryReady).toBe(true)
    expect(library.libraryGeneration).toBe(1)
    expect(library.statusError).toBe('Failed to read library status')
    library.stop()
  })

  it('does not allow a late status response to write after stop', async () => {
    const pending = deferred<api.Status>()
    vi.mocked(api.getStatus).mockReturnValueOnce(pending.promise)
    const library = useLibraryStore()
    const starting = library.start()
    library.stop()
    pending.resolve(status())
    await starting

    expect(library.status).toBeNull()
    expect(library.libraryReady).toBe(false)
  })

  it('cancels a rescan and prevents late state writes after stop', async () => {
    const pending = deferred<void>()
    vi.mocked(api.rescan).mockReturnValueOnce(pending.promise)
    const library = useLibraryStore()
    library.status = status()

    const rescanning = library.requestRescan()
    const signal = vi.mocked(api.rescan).mock.calls[0][0]
    library.stop()

    expect(signal?.aborted).toBe(true)
    pending.resolve()
    await rescanning
    expect(library.status).toBeNull()
    expect(library.loading).toBe(false)
    expect(library.rescanError).toBeNull()
  })

  it('separates a successful rescan request from a failed status refresh', async () => {
    vi.mocked(api.rescan).mockResolvedValue(undefined)
    vi.mocked(api.getStatus).mockRejectedValue(new Error('poll failed'))
    const library = useLibraryStore()
    library.status = status()

    await library.requestRescan()

    expect(library.scanStatus).toBe('scanning')
    expect(library.rescanError).toBeNull()
    expect(library.statusError).toBe('Failed to read library status')
    library.stop()
  })
})
