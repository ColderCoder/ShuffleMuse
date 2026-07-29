import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { usePlayerStore } from './player'
import * as api from '../api'

vi.mock('../api', () => ({
  createQueue: vi.fn(),
  getQueuePage: vi.fn(),
  selectQueueItem: vi.fn(),
  prependQueueDirectory: vi.fn(),
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
  playCalls = 0

  constructor() {
    super()
    FakeAudio.instances.push(this)
  }

  load() {
    this.readyState = 1
    this.dispatchEvent(new Event('loadedmetadata'))
  }

  async play() {
    this.playCalls += 1
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

class FakeConstantSource {
  offset = { value: 1 }
  startCalls = 0
  stopCalls = 0
  connectCalls = 0
  disconnectCalls = 0

  connect() {
    this.connectCalls += 1
  }

  disconnect() {
    this.disconnectCalls += 1
  }

  start() {
    this.startCalls += 1
  }

  stop() {
    this.stopCalls += 1
  }
}

class FakeGain {
  gain = { value: 1 }
  connectCalls = 0
  disconnectCalls = 0

  connect() {
    this.connectCalls += 1
  }

  disconnect() {
    this.disconnectCalls += 1
  }
}

class FakeAudioContext {
  static instances: FakeAudioContext[] = []
  static rejectResume = false
  static failConstruction = false

  state: AudioContextState = 'suspended'
  destination = {}
  source = new FakeConstantSource()
  gain = new FakeGain()
  resumeCalls = 0
  suspendCalls = 0
  closeCalls = 0

  constructor() {
    if (FakeAudioContext.failConstruction) throw new Error('context unavailable')
    FakeAudioContext.instances.push(this)
  }

  createConstantSource() {
    return this.source
  }

  createGain() {
    return this.gain
  }

  async resume() {
    this.resumeCalls += 1
    if (FakeAudioContext.rejectResume) throw new Error('resume rejected')
    this.state = 'running'
  }

  async suspend() {
    this.suspendCalls += 1
    this.state = 'suspended'
  }

  async close() {
    this.closeCalls += 1
    this.state = 'closed'
  }

  static reset() {
    FakeAudioContext.instances = []
    FakeAudioContext.rejectResume = false
    FakeAudioContext.failConstruction = false
  }
}

function enableWebAudio() {
  vi.stubGlobal('AudioContext', FakeAudioContext)
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
    FakeAudioContext.reset()
    vi.stubGlobal('AudioContext', undefined)
    vi.stubGlobal('webkitAudioContext', undefined)
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

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
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

  it('does not delay media while activating a cold audio endpoint', async () => {
    vi.useFakeTimers()
    enableWebAudio()
    const player = usePlayerStore()
    await player.preparePlaylist()

    const playback = player.playAt(0)
    await vi.advanceTimersByTimeAsync(0)
    const audio = FakeAudio.instances[0]
    expect(FakeAudioContext.instances).toHaveLength(1)
    expect(audio.src).toContain('/api/stream/one')
    expect(audio.playCalls).toBe(1)
    await playback

    expect(player.isPlaying).toBe(true)
    expect(player.isBuffering).toBe(false)
    expect(vi.getTimerCount()).toBe(0)
    player.reset()
  })

  it('reuses a warm endpoint for track changes, mode changes, and Opus seeks', async () => {
    vi.useFakeTimers()
    enableWebAudio()
    vi.mocked(api.createQueue).mockResolvedValue(page(
      'two-tracks', 1, 2, [item(0, 'one'), item(1, 'two')],
    ))
    const player = usePlayerStore()
    await player.preparePlaylist()

    await player.playAt(0)
    const context = FakeAudioContext.instances[0]
    const audio = FakeAudio.instances[0]

    await player.playAt(1)
    await player.setStreamMode('opus')
    await player.seek(45)

    expect(player.currentTrack?.id).toBe('two')
    expect(audio.src).toContain('mode=opus')
    expect(audio.src).toContain('start=45.000')
    expect(audio.playCalls).toBe(4)
    expect(FakeAudioContext.instances).toHaveLength(1)
    expect(context.resumeCalls).toBe(1)
    expect(context.source.startCalls).toBe(1)
    expect(vi.getTimerCount()).toBe(0)
    player.reset()
  })

  it('keeps the endpoint active for 15 seconds and reactivates it without delaying media', async () => {
    vi.useFakeTimers()
    enableWebAudio()
    const player = usePlayerStore()
    await player.preparePlaylist()
    await player.playAt(0)
    const context = FakeAudioContext.instances[0]
    const audio = FakeAudio.instances[0]

    player.pause()
    await vi.advanceTimersByTimeAsync(14999)
    await player.resume()
    expect(audio.playCalls).toBe(2)
    expect(context.suspendCalls).toBe(0)

    player.pause()
    await vi.advanceTimersByTimeAsync(15000)
    expect(context.suspendCalls).toBe(1)
    expect(context.state).toBe('suspended')

    await player.resume()
    expect(audio.playCalls).toBe(3)
    expect(context.resumeCalls).toBe(2)
    player.reset()
  })

  it('lets pause cancel playback before pending track selection starts media', async () => {
    enableWebAudio()
    const player = usePlayerStore()
    await player.preparePlaylist()

    const playback = player.playAt(0)
    expect(player.isBuffering).toBe(true)
    await player.togglePlay()
    expect(player.isBuffering).toBe(false)

    await playback
    expect(FakeAudio.instances).toHaveLength(0)
    expect(player.isPlaying).toBe(false)
    player.reset()
  })

  it('only starts the latest track when track selections overlap', async () => {
    enableWebAudio()
    vi.mocked(api.createQueue).mockResolvedValue(page(
      'two-tracks', 1, 2, [item(0, 'one'), item(1, 'two')],
    ))
    const player = usePlayerStore()
    await player.preparePlaylist()

    const first = player.playAt(0)
    const second = player.playAt(1)
    await Promise.all([first, second])

    const audio = FakeAudio.instances[0]
    expect(player.currentTrack?.id).toBe('two')
    expect(audio.src).toContain('/api/stream/two')
    expect(audio.playCalls).toBe(1)
    expect(FakeAudioContext.instances).toHaveLength(1)
    player.reset()
  })

  it('cancels pending playback and releases endpoint resources on reset', async () => {
    vi.useFakeTimers()
    enableWebAudio()
    const player = usePlayerStore()
    await player.preparePlaylist()

    const playback = player.playAt(0)
    const context = FakeAudioContext.instances[0]

    player.reset()
    expect(context.source.stopCalls).toBe(1)
    expect(context.source.disconnectCalls).toBe(1)
    expect(context.gain.disconnectCalls).toBe(1)
    expect(context.closeCalls).toBe(1)
    expect(vi.getTimerCount()).toBe(0)

    await playback
    expect(FakeAudio.instances).toHaveLength(0)
  })

  it('falls back immediately when Web Audio is unsupported or cannot start', async () => {
    const unsupported = usePlayerStore()
    await unsupported.preparePlaylist()
    await unsupported.playAt(0)
    expect(FakeAudio.instances[0].playCalls).toBe(1)
    unsupported.reset()

    setActivePinia(createPinia())
    enableWebAudio()
    FakeAudioContext.rejectResume = true
    const rejected = usePlayerStore()
    await rejected.preparePlaylist()
    await rejected.playAt(0)

    expect(FakeAudio.instances[1].playCalls).toBe(1)
    expect(FakeAudioContext.instances).toHaveLength(1)
    expect(FakeAudioContext.instances[0].closeCalls).toBe(1)
    expect(rejected.isPlaying).toBe(true)
    rejected.reset()
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

  it('moves a directory to the queue front and immediately plays its first track', async () => {
    const player = usePlayerStore()
    await player.preparePlaylist()
    player.selectedTag = 'focus'
    vi.mocked(api.prependQueueDirectory).mockResolvedValueOnce({
      ...page('folder-queue', 1, 3, [
        item(0, 'folder-a'),
        item(1, 'folder-b'),
        item(2, 'one'),
      ]),
      queue: {
        id: 'folder-queue',
        tag: 'focus',
        createdGeneration: 1,
        total: 3,
        pageSize: 200,
      },
      directoryTrackCount: 2,
    })

    const count = await player.prependDirectory('Album')

    expect(api.prependQueueDirectory).toHaveBeenCalledWith('queue-1', 'Album', expect.any(AbortSignal))
    expect(count).toBe(2)
    expect(player.queue?.id).toBe('folder-queue')
    expect(player.selectedTag).toBe('focus')
    expect(player.activeIndex).toBe(0)
    expect(player.sidebarPage).toBe(1)
    expect(player.currentTrack?.id).toBe('folder-a')
    expect(player.currentTime).toBe(0)
    expect(player.isPlaying).toBe(true)
    expect(FakeAudio.instances[0].src).toContain('/api/stream/folder-a')
  })

  it('rebuilds an expired queue once before prepending the directory', async () => {
    const player = usePlayerStore()
    await player.preparePlaylist()
    player.selectedTag = 'focus'
    vi.mocked(api.prependQueueDirectory)
      .mockRejectedValueOnce(Object.assign(new Error('expired'), {
        isAxiosError: true,
        response: { data: { code: 'QUEUE_NOT_FOUND' } },
      }))
      .mockResolvedValueOnce({
        ...page('folder-queue', 1, 2, [item(0, 'folder-a'), item(1, 'tagged')]),
        queue: {
          id: 'folder-queue',
          tag: 'focus',
          createdGeneration: 1,
          total: 2,
          pageSize: 200,
        },
        directoryTrackCount: 1,
      })
    vi.mocked(api.createQueue).mockResolvedValueOnce(page(
      'recovered-base', 1, 1, [item(0, 'tagged')],
    ))

    const count = await player.prependDirectory('Album')

    expect(api.createQueue).toHaveBeenLastCalledWith({ tag: 'focus' }, expect.any(AbortSignal))
    expect(api.prependQueueDirectory).toHaveBeenNthCalledWith(
      2, 'recovered-base', 'Album', expect.any(AbortSignal),
    )
    expect(count).toBe(1)
    expect(player.currentTrack?.id).toBe('folder-a')
  })

  it('keeps the current queue and track when directory prepending fails', async () => {
    const player = usePlayerStore()
    await player.preparePlaylist()
    await player.playAt(0)
    const audio = FakeAudio.instances[0]
    const source = audio.src
    vi.mocked(api.prependQueueDirectory).mockRejectedValueOnce(new Error('busy'))

    await expect(player.prependDirectory('Album')).rejects.toThrow('busy')

    expect(player.queue?.id).toBe('queue-1')
    expect(player.currentTrack?.id).toBe('one')
    expect(player.isPlaying).toBe(true)
    expect(audio.src).toBe(source)
    expect(player.playlistLoading).toBe(false)
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
