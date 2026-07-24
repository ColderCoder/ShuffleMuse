import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { usePlayerStore } from './player'
import * as api from '../api'

vi.mock('../api', () => ({
  createQueue: vi.fn(),
  getQueuePage: vi.fn(),
  selectQueueItem: vi.fn(),
  deleteQueue: vi.fn(),
  getFileMetadata: vi.fn(),
  getFiles: vi.fn(),
}))

class FakeAudio extends EventTarget {
  static instances: FakeAudio[] = []
  preload = ''
  volume = 1
  src = ''
  paused = true
  currentTime = 0
  duration = 240
  readyState = 1

  constructor() {
    super()
    FakeAudio.instances.push(this)
  }

  load() {
    this.readyState = 1
    this.dispatchEvent(new Event('loadedmetadata'))
  }

  async play() {
    this.paused = false
    this.dispatchEvent(new Event('playing'))
  }

  pause() {
    this.paused = true
    this.dispatchEvent(new Event('pause'))
  }

  removeAttribute(name: string) {
    if (name === 'src') this.src = ''
  }
}

function item(index: number, id = `track-${index}`): api.QueueItem {
  return {
    id,
    name: id,
    dir: 'Album',
    filepath: `Album/${id}.flac`,
    queueIndex: index,
    available: true,
  }
}

function page(
  id = 'queue-1',
  pageNumber = 1,
  total = 1,
  items: api.QueueItem[] = [item(0, 'one')],
  generation = 1,
): api.CreateQueueResponse {
  return {
    queue: { id, tag: '', createdGeneration: generation, total, pageSize: 200 },
    items,
    page: pageNumber,
    libraryGeneration: generation,
    pinApplied: false,
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((done, fail) => {
    resolve = done
    reject = fail
  })
  return { promise, resolve, reject }
}

describe('player store server queues', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    FakeAudio.instances = []
    vi.stubGlobal('Audio', FakeAudio)
    setActivePinia(createPinia())
    vi.mocked(api.createQueue).mockResolvedValue(page())
    vi.mocked(api.getFileMetadata).mockResolvedValue({
      codec: 'FLAC',
      bitrateKbps: 987,
      bitrateApproximate: false,
      durationSeconds: 240,
    })
    vi.mocked(api.deleteQueue).mockResolvedValue(undefined)
  })

  it('sanitizes corrupt and out-of-range stored volume before creating audio', () => {
    localStorage.setItem('shufflemuse-volume', 'not-a-number')
    let player = usePlayerStore()
    expect(player.volume).toBe(0.8)

    setActivePinia(createPinia())
    localStorage.setItem('shufflemuse-volume', '   ')
    player = usePlayerStore()
    expect(player.volume).toBe(0.8)

    setActivePinia(createPinia())
    localStorage.setItem('shufflemuse-volume', '4')
    player = usePlayerStore()
    expect(player.volume).toBe(1)
  })

  it('creates only the first server page and clears local state on reset', async () => {
    const player = usePlayerStore()
    await player.preparePlaylist()

    expect(api.createQueue).toHaveBeenCalledWith({}, expect.any(AbortSignal))
    expect(api.getFiles).not.toHaveBeenCalled()
    expect(player.queue?.id).toBe('queue-1')
    expect(player.sidebarItems.map(track => track.id)).toEqual(['one'])
    expect(player.currentTrack?.id).toBe('one')

    player.reset()
    expect(player.queue).toBeNull()
    expect(player.sidebarItems).toEqual([])
    expect(player.currentTrack).toBeNull()
  })

  it('shows the filename until the lazy metadata title arrives', async () => {
    const pending = deferred<api.FileMetadata>()
    vi.mocked(api.getFileMetadata).mockReturnValueOnce(pending.promise)
    const player = usePlayerStore()

    await player.preparePlaylist()
    expect(player.displayTitle).toBe('one')

    pending.resolve({
      title: '  Metadata Title  ',
      codec: 'FLAC',
      bitrateKbps: 987,
      bitrateApproximate: false,
      durationSeconds: 240,
    })
    await vi.waitFor(() => expect(player.displayTitle).toBe('Metadata Title'))
  })

  it('never applies a stale metadata title after changing tracks', async () => {
    const oldMetadata = deferred<api.FileMetadata>()
    vi.mocked(api.createQueue).mockResolvedValue(page(
      'two-tracks', 1, 2, [item(0, 'one'), item(1, 'two')],
    ))
    vi.mocked(api.getFileMetadata).mockImplementation(id => (
      id === 'one'
        ? oldMetadata.promise
        : Promise.resolve({
            title: 'Second Title',
            codec: 'FLAC',
            bitrateKbps: 1000,
            bitrateApproximate: false,
            durationSeconds: 180,
          })
    ))
    const player = usePlayerStore()

    await player.preparePlaylist()
    await player.playAt(1)
    await vi.waitFor(() => expect(player.displayTitle).toBe('Second Title'))

    oldMetadata.resolve({
      title: 'Stale First Title',
      codec: 'FLAC',
      bitrateKbps: 900,
      bitrateApproximate: false,
      durationSeconds: 200,
    })
    await Promise.resolve()
    expect(player.displayTitle).toBe('Second Title')
  })

  it('toggles active playback between paused and playing', async () => {
    const player = usePlayerStore()
    await player.preparePlaylist()
    await player.playAt(0)
    expect(player.isPlaying).toBe(true)

    await player.togglePlay()
    expect(player.isPlaying).toBe(false)

    await player.togglePlay()
    expect(player.isPlaying).toBe(true)
  })

  it('ignores a stale aborted queue creation', async () => {
    const old = deferred<api.CreateQueueResponse>()
    vi.mocked(api.createQueue)
      .mockReturnValueOnce(old.promise)
      .mockResolvedValueOnce(page('queue-new', 1, 1, [item(0, 'new')]))
    const player = usePlayerStore()
    const stale = player.preparePlaylist()
    player.reset()
    const current = player.preparePlaylist()
    old.resolve(page('queue-old', 1, 1, [item(0, 'old')]))
    await Promise.all([stale, current])

    expect(player.queue?.id).toBe('queue-new')
    expect(player.currentTrack?.id).toBe('new')
  })

  it('accesses tracks 201 and 1001 directly and retains at most five pages', async () => {
    vi.mocked(api.createQueue).mockResolvedValue(page(
      'large', 1, 1200,
      Array.from({ length: 200 }, (_, index) => item(index)),
    ))
    vi.mocked(api.getQueuePage).mockImplementation(async (_id, pageNumber) => ({
      ...page(
        'large', pageNumber, 1200,
        Array.from({ length: 200 }, (_, offset) => item((pageNumber - 1) * 200 + offset)),
      ),
    }))
    const player = usePlayerStore()
    await player.preparePlaylist()

    await player.playAt(200)
    expect(player.currentTrack?.id).toBe('track-200')
    expect(api.getQueuePage).toHaveBeenCalledWith('large', 2, expect.any(AbortSignal))

    await player.playAt(1000)
    expect(player.currentTrack?.id).toBe('track-1000')
    expect(api.getQueuePage).toHaveBeenCalledWith('large', 6, expect.any(AbortSignal))
    for (const pageNumber of [2, 3, 4, 5, 6]) await player.showQueuePage(pageNumber)
    expect(player.cachedPageCount).toBeLessThanOrEqual(5)
    expect(player.sidebarItems).toHaveLength(200)
  })

  it('pins a playing track during tag filtering without reloading audio', async () => {
    const player = usePlayerStore()
    await player.preparePlaylist()
    await player.playAt(0)
    const audio = FakeAudio.instances[0]
    audio.currentTime = 73
    audio.dispatchEvent(new Event('timeupdate'))
    const source = audio.src
    vi.mocked(api.createQueue).mockResolvedValueOnce({
      ...page('tagged', 1, 2, [item(0, 'one'), item(1, 'tagged')]),
      pinApplied: true,
    })

    await player.filterPlaylistByTag('focus')

    expect(api.createQueue).toHaveBeenLastCalledWith({
      tag: 'focus', pinFileId: 'one', replaceQueueId: 'queue-1',
    }, expect.any(AbortSignal))
    expect(player.currentTrack?.id).toBe('one')
    expect(player.currentTime).toBe(73)
    expect(player.isPlaying).toBe(true)
    expect(audio.src).toBe(source)
    expect(FakeAudio.instances).toHaveLength(1)
  })

  it('excludes a non-matching current track from a tag-filtered queue', async () => {
    const player = usePlayerStore()
    await player.preparePlaylist()
    await player.playAt(0)
    const audio = FakeAudio.instances[0]
    const oldSource = audio.src
    vi.mocked(api.createQueue).mockResolvedValueOnce({
      ...page('favorite-only', 1, 1, [item(0, 'favorite-track')]),
      pinApplied: false,
    })

    await player.filterPlaylistByTag('favorite')

    expect(api.createQueue).toHaveBeenLastCalledWith({
      tag: 'favorite', pinFileId: 'one', replaceQueueId: 'queue-1',
    }, expect.any(AbortSignal))
    expect(player.queueTotal).toBe(1)
    expect(player.currentTrack?.id).toBe('favorite-track')
    expect(player.selectedTag).toBe('favorite')
    expect(player.isPlaying).toBe(true)
    expect(audio.src).not.toBe(oldSource)
    expect(audio.src).toContain('/api/stream/favorite-track')
  })

  it('starts an explicit track before background selection replaces the queue', async () => {
    const selection = deferred<api.SelectQueueResponse>()
    vi.mocked(api.selectQueueItem).mockReturnValue(selection.promise)
    const player = usePlayerStore()
    await player.preparePlaylist()
    await player.playTrack({ id: 'chosen', name: 'Chosen', dir: 'Album', filepath: 'Album/chosen.flac' })

    expect(player.currentTrack?.id).toBe('chosen')
    expect(player.isPlaying).toBe(true)
    expect(api.selectQueueItem).toHaveBeenCalledWith('queue-1', 'chosen', expect.any(AbortSignal))
    selection.resolve({
      ...page('replacement', 1, 2, [item(0, 'chosen'), item(1, 'one')]),
      queueIndex: 0,
    })
    await vi.waitFor(() => expect(player.queue?.id).toBe('replacement'))
    expect(player.currentTrack?.id).toBe('chosen')
  })

  it('keeps the old queue when Randomize fails', async () => {
    const player = usePlayerStore()
    await player.preparePlaylist()
    vi.mocked(api.createQueue).mockRejectedValueOnce(new Error('busy'))

    await player.randomizePlaylist()

    expect(player.queue?.id).toBe('queue-1')
    expect(player.currentTrack?.id).toBe('one')
    expect(player.playlistError).toBe('Failed to prepare playlist')
  })

  it('creates a new independent queue after the last track ends', async () => {
    vi.mocked(api.createQueue)
      .mockResolvedValueOnce(page('cycle-one', 1, 1, [item(0, 'last')]))
      .mockResolvedValueOnce(page('cycle-two', 1, 1, [item(0, 'next-cycle')]))
    const player = usePlayerStore()
    await player.preparePlaylist()
    await player.playAt(0)

    await player.next()

    expect(api.createQueue).toHaveBeenLastCalledWith({ replaceQueueId: 'cycle-one' }, expect.any(AbortSignal))
    expect(player.queue?.id).toBe('cycle-two')
    expect(player.currentTrack?.id).toBe('next-cycle')
  })

  it('keeps a favorite-only filter when playback loops into a new queue', async () => {
    vi.mocked(api.createQueue)
      .mockResolvedValueOnce(page('favorite-cycle-one', 1, 1, [item(0, 'favorite-one')]))
      .mockResolvedValueOnce(page('favorite-cycle-two', 1, 1, [item(0, 'favorite-two')]))
    const player = usePlayerStore()

    await player.preparePlaylist('favorite')
    await player.playAt(0)
    await player.next()

    expect(api.createQueue).toHaveBeenNthCalledWith(1, { tag: 'favorite' }, expect.any(AbortSignal))
    expect(api.createQueue).toHaveBeenNthCalledWith(2, {
      tag: 'favorite',
      replaceQueueId: 'favorite-cycle-one',
    }, expect.any(AbortSignal))
    expect(player.selectedTag).toBe('favorite')
    expect(player.currentTrack?.id).toBe('favorite-two')
  })

  it('skips unavailable entries during next and previous navigation', async () => {
    vi.mocked(api.createQueue).mockResolvedValue(page('availability', 1, 3, [
      item(0, 'first'),
      { ...item(1, 'removed'), available: false },
      item(2, 'third'),
    ]))
    const player = usePlayerStore()
    await player.preparePlaylist()
    await player.playAt(0)

    await player.next()
    expect(player.currentTrack?.id).toBe('third')
    expect(player.activeIndex).toBe(2)

    await player.previous()
    expect(player.currentTrack?.id).toBe('first')
    expect(player.activeIndex).toBe(0)
  })

  it('restores an expired queue once with the same tag and current track without stopping audio', async () => {
    vi.mocked(api.createQueue)
      .mockResolvedValueOnce(page('expired', 1, 400, Array.from({ length: 200 }, (_, index) => item(index))))
      .mockResolvedValueOnce({ ...page('restored', 1, 2, [item(0, 'track-0'), item(1, 'next')]), pinApplied: true })
    vi.mocked(api.getQueuePage).mockRejectedValue(Object.assign(new Error('missing'), {
      isAxiosError: true,
      response: { data: { code: 'QUEUE_NOT_FOUND' } },
    }))
    const player = usePlayerStore()
    await player.preparePlaylist('focus')
    await player.playAt(0)
    const audio = FakeAudio.instances[0]
    const source = audio.src

    await player.showQueuePage(2)

    expect(api.createQueue).toHaveBeenLastCalledWith({ tag: 'focus', pinFileId: 'track-0' }, expect.any(AbortSignal))
    expect(player.queue?.id).toBe('restored')
    expect(player.currentTrack?.id).toBe('track-0')
    expect(audio.src).toBe(source)
    expect(player.isPlaying).toBe(true)
  })

  it('preserves absolute time when switching and seeking Opus', async () => {
    const player = usePlayerStore()
    await player.preparePlaylist()
    await player.playAt(0)
    const audio = FakeAudio.instances[0]
    audio.currentTime = 45
    audio.dispatchEvent(new Event('timeupdate'))

    await player.setStreamMode('opus')
    expect(audio.src).toContain('mode=opus')
    expect(audio.src).toContain('start=45.000')
    audio.currentTime = 5
    audio.dispatchEvent(new Event('timeupdate'))
    expect(player.currentTime).toBe(50)

    await player.seek(90)
    expect(audio.src).toContain('start=90.000')
    expect(player.currentTime).toBe(90)
  })
})
