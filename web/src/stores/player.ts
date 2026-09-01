import axios from 'axios'
import { computed, ref, shallowRef, watch } from 'vue'
import { defineStore } from 'pinia'
import * as api from '../api'

const STORAGE_KEY_VOLUME = 'shufflemuse-volume'
const STORAGE_KEY_MUTED = 'shufflemuse-muted'
const STORAGE_KEY_STREAM_MODE = 'shufflemuse-stream-mode'
const MAX_CACHED_PAGES = 5
const ENDPOINT_KEEPALIVE_MS = 15000

export type StreamMode = 'original' | 'opus'

export interface CurrentTrack {
  id: string
  filepath: string
  name: string
  dir: string
  streamUrl: string
}

interface CachedPage {
  items: api.QueueItem[]
  libraryGeneration: number
}

interface PlaybackIntent {
  id: number
}

function loadVolume(): number {
  try {
    const saved = localStorage.getItem(STORAGE_KEY_VOLUME)
    if (saved === null || saved.trim() === '') return 0.8
    const parsed = Number(saved)
    return Number.isFinite(parsed) ? clamp(parsed, 0, 1) : 0.8
  } catch {
    return 0.8
  }
}

function loadMuted(): boolean {
  try {
    return localStorage.getItem(STORAGE_KEY_MUTED) === 'true'
  } catch {
    return false
  }
}

function loadStreamMode(): StreamMode {
  try {
    return localStorage.getItem(STORAGE_KEY_STREAM_MODE) === 'opus' ? 'opus' : 'original'
  } catch {
    return 'original'
  }
}

function sourceUrl(id: string, mode: StreamMode, startSeconds = 0): string {
  const params = new URLSearchParams({ mode })
  if (mode === 'opus' && startSeconds > 0) params.set('start', startSeconds.toFixed(3))
  return `/api/stream/${encodeURIComponent(id)}?${params.toString()}`
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max)
}

function errorCode(error: unknown): string | undefined {
  if (!axios.isAxiosError(error)) return undefined
  const data = error.response?.data as { code?: unknown } | undefined
  return typeof data?.code === 'string' ? data.code : undefined
}

type MediaMetadataConstructor = new (init?: MediaMetadataInit) => MediaMetadata

function getBrowserMediaSession(): MediaSession | null {
  try {
    if (typeof navigator === 'undefined' || !('mediaSession' in navigator)) return null
    return navigator.mediaSession ?? null
  } catch {
    return null
  }
}

function getBrowserMediaMetadataConstructor(): MediaMetadataConstructor | null {
  const scope = globalThis as typeof globalThis & { MediaMetadata?: MediaMetadataConstructor }
  return scope.MediaMetadata ?? null
}

function absoluteMediaURL(path: string): string {
  if (typeof location === 'undefined') return path
  try {
    return new URL(path, location.href).href
  } catch {
    return path
  }
}

export const usePlayerStore = defineStore('player', () => {
  const currentTrack = ref<CurrentTrack | null>(null)
  const mediaMetadata = ref<api.FileMetadata | null>(null)
  const isPlaying = ref(false)
  const isBuffering = ref(false)
  const currentTime = ref(0)
  const duration = ref(0)
  const volume = ref(loadVolume())
  const isMuted = ref(loadMuted())
  const streamMode = ref<StreamMode>(loadStreamMode())

  const queue = ref<api.QueueDescription | null>(null)
  const activeIndex = ref(0)
  const selectedTag = ref('')
  const sidebarPage = ref(1)
  const pages = shallowRef(new Map<number, CachedPage>())
  const knownLibraryGeneration = ref(0)
  const playlistLoading = ref(false)
  const sidebarLoading = ref(false)
  const playlistError = ref<string | null>(null)
  const error = ref<string | null>(null)

  let audio: HTMLAudioElement | null = null
  let playEpoch = 0
  let playbackIntent = 0
  let activeMediaPlaybackIntent = 0
  let pendingMediaPlaybackIntent = 0
  let sourceRequest = 0
  let sourceOffset = 0
  let loadedTrackID: string | null = null
  let loadedMode: StreamMode | null = null
  let endpointContext: AudioContext | null = null
  let endpointSource: ConstantSourceNode | null = null
  let endpointGain: GainNode | null = null
  let endpointDisabled = false
  let endpointGeneration = 0
  let endpointSuspendTimer: ReturnType<typeof setTimeout> | null = null
  let queueController: AbortController | null = null
  let selectController: AbortController | null = null
  let metadataController: AbortController | null = null
  let mediaSessionHandlersInstalled = false
  const pageRequests = new Map<number, { queueID: string; controller: AbortController; promise: Promise<api.QueueItem[]> }>()
  const pageLRU = new Map<number, number>()
  let lruClock = 0
  let recoveryUsed = false

  const queuePosition = computed(() => (currentTrack.value && queue.value ? activeIndex.value + 1 : 0))
  const queueTotal = computed(() => queue.value?.total ?? 0)
  const displayTitle = computed(() => mediaMetadata.value?.title?.trim() || currentTrack.value?.name || '')
  const queuePageCount = computed(() => Math.max(1, Math.ceil(queueTotal.value / (queue.value?.pageSize ?? apiQueuePageSize()))))
  const currentPage = computed(() => pageForIndex(activeIndex.value))
  const sidebarItems = computed(() => pages.value.get(sidebarPage.value)?.items ?? [])
  const currentPageItems = computed(() => pages.value.get(currentPage.value)?.items ?? [])
  // Compatibility alias for view-level empty checks. It is never the full queue.
  const playlist = computed(() => sidebarItems.value)
  const cachedPageCount = computed(() => pages.value.size)

  function setMediaSessionPlaybackState(state: MediaSessionPlaybackState) {
    const session = getBrowserMediaSession()
    if (!session) return
    try { session.playbackState = state } catch { /* media session is best effort */ }
  }

  function clearMediaSessionPosition(session: MediaSession | null = getBrowserMediaSession()) {
    if (!session || typeof session.setPositionState !== 'function') return
    try { session.setPositionState() } catch { /* media session is best effort */ }
  }

  function updateMediaSessionPosition() {
    const session = getBrowserMediaSession()
    if (!session || typeof session.setPositionState !== 'function') return
    const total = duration.value
    if (!currentTrack.value || !Number.isFinite(total) || total <= 0) {
      clearMediaSessionPosition(session)
      return
    }
    const position = clamp(Number.isFinite(currentTime.value) ? currentTime.value : 0, 0, total)
    const rate = audio?.playbackRate
    const playbackRate = rate !== undefined && Number.isFinite(rate) && rate > 0 ? rate : 1
    try {
      session.setPositionState({ duration: total, playbackRate, position })
    } catch { /* unsupported browsers can reject invalid position state */ }
  }

  function updateMediaSessionMetadata() {
    const session = getBrowserMediaSession()
    if (!session) return
    if (!currentTrack.value) {
      try { session.metadata = null } catch { /* media session is best effort */ }
      setMediaSessionPlaybackState('none')
      clearMediaSessionPosition(session)
      return
    }

    const constructor = getBrowserMediaMetadataConstructor()
    if (!constructor) return
    const title = displayTitle.value.trim() || currentTrack.value.name
    const artist = mediaMetadata.value?.artist?.trim() || ''
    const album = mediaMetadata.value?.album?.trim() || ''
    try {
      session.metadata = new constructor({
        title,
        artist,
        album,
        artwork: [{ src: absoluteMediaURL(api.fileCoverUrl(currentTrack.value.id)) }],
      })
    } catch { /* malformed/unsupported metadata must not affect playback */ }
  }

  function invokeMediaSessionAction(action: () => Promise<void>) {
    try {
      void action().catch(() => {})
    } catch { /* media controls must never surface an action error */ }
  }

  function registerMediaSessionHandlers() {
    if (mediaSessionHandlersInstalled) return
    const session = getBrowserMediaSession()
    if (!session || typeof session.setActionHandler !== 'function') return

    const seekOffset = (details: MediaSessionActionDetails) => (
      details.seekOffset !== undefined && Number.isFinite(details.seekOffset) && details.seekOffset > 0
        ? details.seekOffset
        : 10
    )
    const handlers: Array<[MediaSessionAction, MediaSessionActionHandler]> = [
      ['play', () => {
        if (currentTrack.value) invokeMediaSessionAction(resume)
      }],
      ['pause', () => pause()],
      ['stop', () => {
        const trackID = currentTrack.value?.id
        if (!trackID) return
        invokeMediaSessionAction(async () => {
          pause()
          await seek(0)
          if (currentTrack.value?.id !== trackID || isPlaying.value || isBuffering.value) return
          setMediaSessionPlaybackState('none')
          clearMediaSessionPosition()
        })
      }],
      ['previoustrack', () => {
        if (currentTrack.value) invokeMediaSessionAction(previous)
      }],
      ['nexttrack', () => {
        if (currentTrack.value) invokeMediaSessionAction(next)
      }],
      ['seekbackward', details => {
        if (currentTrack.value) invokeMediaSessionAction(() => seek(currentTime.value - seekOffset(details)))
      }],
      ['seekforward', details => {
        if (currentTrack.value) invokeMediaSessionAction(() => seek(currentTime.value + seekOffset(details)))
      }],
      ['seekto', details => {
        const target = details.seekTime
        if (currentTrack.value && target !== undefined && Number.isFinite(target)) {
          invokeMediaSessionAction(() => seek(target))
        }
      }],
    ]
    for (const [action, handler] of handlers) {
      try { session.setActionHandler(action, handler) } catch { /* action support varies by browser/platform */ }
    }
    mediaSessionHandlersInstalled = true
  }

  function clearEndpointSuspendTimer() {
    if (endpointSuspendTimer === null) return
    clearTimeout(endpointSuspendTimer)
    endpointSuspendTimer = null
  }

  function closeEndpoint(disable: boolean) {
    endpointGeneration += 1
    clearEndpointSuspendTimer()

    const context = endpointContext
    const source = endpointSource
    const gain = endpointGain
    endpointContext = null
    endpointSource = null
    endpointGain = null
    endpointDisabled = disable

    try { source?.stop() } catch { /* already stopped */ }
    try { source?.disconnect() } catch { /* already disconnected */ }
    try { gain?.disconnect() } catch { /* already disconnected */ }
    if (context && context.state !== 'closed') {
      try { void context.close().catch(() => {}) } catch { /* already closed */ }
    }
  }

  function createEndpointContext(): AudioContext | null {
    const scope = globalThis as unknown as {
      AudioContext?: new () => AudioContext
      webkitAudioContext?: new () => AudioContext
    }
    const AudioContextConstructor = scope.AudioContext ?? scope.webkitAudioContext
    if (!AudioContextConstructor) {
      endpointDisabled = true
      return null
    }

    let context: AudioContext | null = null
    let source: ConstantSourceNode | null = null
    let gain: GainNode | null = null
    try {
      context = new AudioContextConstructor()
      source = context.createConstantSource()
      gain = context.createGain()
      source.offset.value = 0
      gain.gain.value = 0
      source.connect(gain)
      gain.connect(context.destination)
      source.start()
      endpointContext = context
      endpointSource = source
      endpointGain = gain
      return context
    } catch {
      try { source?.stop() } catch { /* not started */ }
      try { source?.disconnect() } catch { /* not connected */ }
      try { gain?.disconnect() } catch { /* not connected */ }
      if (context && context.state !== 'closed') {
        try { void context.close().catch(() => {}) } catch { /* ignore */ }
      }
      endpointDisabled = true
      return null
    }
  }

  function resumeEndpoint(context: AudioContext) {
    if (context.state === 'running') return
    try {
      void context.resume().catch(() => {
        if (endpointContext === context) closeEndpoint(true)
      })
    } catch {
      if (endpointContext === context) closeEndpoint(true)
    }
  }

  function activateEndpoint() {
    endpointGeneration += 1
    clearEndpointSuspendTimer()
    if (endpointDisabled) return

    if (endpointContext?.state === 'closed') closeEndpoint(false)
    const context = endpointContext ?? createEndpointContext()
    if (context) resumeEndpoint(context)
  }

  function scheduleEndpointSuspend() {
    clearEndpointSuspendTimer()
    const context = endpointContext
    if (!context || context.state === 'closed') return
    const generation = ++endpointGeneration
    endpointSuspendTimer = setTimeout(() => {
      endpointSuspendTimer = null
      if (endpointContext !== context || endpointGeneration !== generation) return
      try {
        void context.suspend().then(
          () => {
            if (endpointContext !== context || endpointGeneration === generation) return
            resumeEndpoint(context)
          },
          () => {},
        )
      } catch { /* keep playback independent of endpoint keepalive failures */ }
    }, ENDPOINT_KEEPALIVE_MS)
  }

  function beginPlaybackIntent(): PlaybackIntent {
    const id = ++playbackIntent
    if (audio && !audio.paused && isPlaying.value) {
      activeMediaPlaybackIntent = id
    } else if (!isPlaying.value) {
      pendingMediaPlaybackIntent = id
      isBuffering.value = true
    }
    activateEndpoint()
    return { id }
  }

  function isCurrentPlaybackIntent(intent: PlaybackIntent): boolean {
    return intent.id === playbackIntent
  }

  function clearPendingPlayback(intent: PlaybackIntent) {
    if (pendingMediaPlaybackIntent !== intent.id) return
    pendingMediaPlaybackIntent = 0
    if (!isPlaying.value) isBuffering.value = false
  }

  function abandonPlayback(intent: PlaybackIntent | null) {
    if (!intent || !isCurrentPlaybackIntent(intent)) return
    playbackIntent += 1
    activeMediaPlaybackIntent = audio && !audio.paused && isPlaying.value ? playbackIntent : 0
    clearPendingPlayback(intent)
    if (!isPlaying.value) scheduleEndpointSuspend()
  }

  function apiQueuePageSize(): number {
    return queue.value?.pageSize ?? 200
  }

  function pageForIndex(index: number): number {
    return Math.floor(Math.max(index, 0) / apiQueuePageSize()) + 1
  }

  function getAudio(): HTMLAudioElement {
    registerMediaSessionHandlers()
    if (!audio) {
      audio = new Audio()
      audio.preload = 'metadata'
      audio.volume = isMuted.value ? 0 : volume.value
      audio.addEventListener('ended', () => {
        currentTime.value = duration.value
        void next()
      })
      audio.addEventListener('playing', () => {
        if (activeMediaPlaybackIntent !== playbackIntent || audio?.paused) return
        clearEndpointSuspendTimer()
        pendingMediaPlaybackIntent = 0
        isBuffering.value = false
        isPlaying.value = true
        setMediaSessionPlaybackState('playing')
        updateMediaSessionPosition()
      })
      audio.addEventListener('pause', () => {
        isBuffering.value = false
        isPlaying.value = false
        setMediaSessionPlaybackState(currentTrack.value ? 'paused' : 'none')
      })
      audio.addEventListener('waiting', () => {
        if (!audio?.paused) isBuffering.value = true
      })
      audio.addEventListener('canplay', () => {
        if (pendingMediaPlaybackIntent !== playbackIntent) isBuffering.value = false
      })
      audio.addEventListener('seeked', () => {
        if (pendingMediaPlaybackIntent !== playbackIntent) isBuffering.value = false
      })
      audio.addEventListener('timeupdate', () => {
        if (!audio) return
        const absoluteTime = sourceOffset + audio.currentTime
        currentTime.value = duration.value > 0
          ? clamp(absoluteTime, 0, duration.value)
          : Math.max(absoluteTime, 0)
        updateMediaSessionPosition()
      })
      audio.addEventListener('durationchange', () => {
        if (!audio || streamMode.value !== 'original') return
        if (Number.isFinite(audio.duration) && audio.duration > 0) {
          duration.value = audio.duration
          updateMediaSessionPosition()
        }
      })
      audio.addEventListener('error', () => {
        pendingMediaPlaybackIntent = 0
        error.value = 'Playback error'
        isBuffering.value = false
        isPlaying.value = false
        setMediaSessionPlaybackState(currentTrack.value ? 'paused' : 'none')
        scheduleEndpointSuspend()
      })
    }
    return audio
  }

  function abortPageRequests() {
    for (const request of pageRequests.values()) request.controller.abort()
    pageRequests.clear()
  }

  function abortQueueWork() {
    queueController?.abort()
    queueController = null
    selectController?.abort()
    selectController = null
    abortPageRequests()
  }

  function touchPage(page: number) {
    pageLRU.set(page, ++lruClock)
  }

  function evictPages() {
    const pinned = new Set([currentPage.value, sidebarPage.value])
    const next = new Map(pages.value)
    while (next.size > MAX_CACHED_PAGES) {
      let victim: number | null = null
      let oldest = Number.POSITIVE_INFINITY
      for (const page of next.keys()) {
        const used = pageLRU.get(page) ?? 0
        if (!pinned.has(page) && used < oldest) {
          oldest = used
          victim = page
        }
      }
      if (victim === null) break
      next.delete(victim)
      pageLRU.delete(victim)
    }
    pages.value = next
  }

  function cachePage(response: api.QueuePage) {
    if (queue.value && response.queue.id !== queue.value.id) return
    const next = new Map(pages.value)
    next.set(response.page, { items: response.items, libraryGeneration: response.libraryGeneration })
    pages.value = next
    knownLibraryGeneration.value = Math.max(knownLibraryGeneration.value, response.libraryGeneration)
    touchPage(response.page)
    evictPages()
  }

  function resetPageCache(response: api.QueuePage) {
    abortPageRequests()
    pageLRU.clear()
    pages.value = new Map([[response.page, {
      items: response.items,
      libraryGeneration: response.libraryGeneration,
    }]])
    touchPage(response.page)
    knownLibraryGeneration.value = response.libraryGeneration
  }

  function syncCurrentTrack(item: api.FileEntry, preserveSource = false) {
    const existing = currentTrack.value
    currentTrack.value = {
      id: item.id,
      filepath: item.filepath,
      name: item.name,
      dir: item.dir,
      streamUrl: preserveSource && existing?.id === item.id
        ? existing.streamUrl
        : sourceUrl(item.id, streamMode.value),
    }
  }

  async function refreshMetadata(trackID: string, epoch: number, clearExisting = true) {
    metadataController?.abort()
    const controller = new AbortController()
    metadataController = controller
    if (clearExisting) {
      mediaMetadata.value = null
      duration.value = 0
      updateMediaSessionMetadata()
      updateMediaSessionPosition()
    }
    try {
      const metadata = await api.getFileMetadata(trackID, controller.signal)
      if (epoch !== playEpoch || currentTrack.value?.id !== trackID || controller.signal.aborted) return
      mediaMetadata.value = metadata
      duration.value = metadata.durationSeconds
      updateMediaSessionMetadata()
      updateMediaSessionPosition()
    } catch (requestError) {
      if (!axios.isCancel(requestError) && epoch === playEpoch && currentTrack.value?.id === trackID) {
        mediaMetadata.value = null
        updateMediaSessionMetadata()
      }
    } finally {
      if (metadataController === controller) metadataController = null
    }
  }

  async function waitForLoadedMetadata(element: HTMLAudioElement, requestID: number): Promise<void> {
    if (element.readyState >= HTMLMediaElement.HAVE_METADATA) return
    await new Promise<void>((resolve, reject) => {
      const cleanup = () => {
        element.removeEventListener('loadedmetadata', onLoaded)
        element.removeEventListener('error', onError)
      }
      const onLoaded = () => { cleanup(); resolve() }
      const onError = () => { cleanup(); reject(new Error('media metadata failed to load')) }
      element.addEventListener('loadedmetadata', onLoaded)
      element.addEventListener('error', onError)
      if (requestID !== sourceRequest) {
        cleanup()
        resolve()
      }
    })
  }

  async function loadCurrentSource(
    position: number,
    autoplay: boolean,
    suppliedIntent: PlaybackIntent | null = null,
  ) {
    if (!currentTrack.value) return
    const intent = autoplay ? suppliedIntent ?? beginPlaybackIntent() : null
    if (intent && !isCurrentPlaybackIntent(intent)) return
    const epoch = playEpoch
    const trackID = currentTrack.value.id
    const mode = streamMode.value
    const requestID = ++sourceRequest
    const target = duration.value > 0 ? clamp(position, 0, duration.value) : Math.max(position, 0)
    const element = getAudio()
    element.pause()
    sourceOffset = mode === 'opus' ? target : 0
    currentTime.value = target
    updateMediaSessionPosition()
    const url = sourceUrl(trackID, mode, target)
    currentTrack.value = { ...currentTrack.value, streamUrl: url }
    loadedTrackID = trackID
    loadedMode = mode
    isBuffering.value = autoplay
    pendingMediaPlaybackIntent = intent?.id ?? 0
    error.value = null
    element.src = url
    element.load()
    try {
      if (mode === 'original' && target > 0) {
        await waitForLoadedMetadata(element, requestID)
        if (requestID !== sourceRequest) return
        element.currentTime = target
      }
      if (autoplay) {
        if (!intent) return
        if (requestID !== sourceRequest || epoch !== playEpoch || !isCurrentPlaybackIntent(intent)) {
          clearPendingPlayback(intent)
          return
        }
        activeMediaPlaybackIntent = intent.id
        await element.play()
        if (requestID !== sourceRequest || epoch !== playEpoch || !isCurrentPlaybackIntent(intent)) {
          if (activeMediaPlaybackIntent === intent.id) {
            activeMediaPlaybackIntent = 0
            element.pause()
          }
          clearPendingPlayback(intent)
          return
        }
        pendingMediaPlaybackIntent = 0
        isPlaying.value = true
        isBuffering.value = false
        setMediaSessionPlaybackState('playing')
      } else {
        pendingMediaPlaybackIntent = 0
        activeMediaPlaybackIntent = 0
        isPlaying.value = false
        isBuffering.value = false
        setMediaSessionPlaybackState(currentTrack.value ? 'paused' : 'none')
        if (endpointSuspendTimer === null) scheduleEndpointSuspend()
      }
    } catch {
      if (
        requestID !== sourceRequest
        || epoch !== playEpoch
        || (intent && !isCurrentPlaybackIntent(intent))
      ) return
      pendingMediaPlaybackIntent = 0
      activeMediaPlaybackIntent = 0
      isPlaying.value = false
      isBuffering.value = false
      setMediaSessionPlaybackState(currentTrack.value ? 'paused' : 'none')
      error.value = 'Failed to load track'
      scheduleEndpointSuspend()
    }
  }

  async function applyCreatedQueue(
    response: api.CreateQueueResponse,
    epoch: number,
    preserveCurrent: boolean,
    autoplay: boolean,
    playback: PlaybackIntent | null,
  ) {
    if (epoch !== playEpoch) return
    queue.value = response.queue
    resetPageCache(response)
    activeIndex.value = 0
    sidebarPage.value = 1
    evictPages()
    playlistError.value = null
    if (response.queue.total === 0 || response.items.length === 0) {
      if (!preserveCurrent) {
        if (audio) audio.pause()
        currentTrack.value = null
        mediaMetadata.value = null
        currentTime.value = 0
        duration.value = 0
      }
      abandonPlayback(playback)
      playlistError.value = selectedTag.value ? `No tracks tagged ${selectedTag.value}` : 'No tracks available'
      return
    }
    if (preserveCurrent && currentTrack.value && response.pinApplied) {
      const selected = response.items.find(item => item.id === currentTrack.value?.id)
      if (selected) activeIndex.value = selected.queueIndex
      void refreshMetadata(currentTrack.value.id, epoch, false)
      return
    }
    const first = response.items.find(item => item.available)
    if (!first) {
      abandonPlayback(playback)
      playlistError.value = 'No tracks available'
      return
    }
    activeIndex.value = first.queueIndex
    syncCurrentTrack(first)
    currentTime.value = 0
    duration.value = 0
    void refreshMetadata(first.id, epoch)
    if (autoplay) await loadCurrentSource(0, true, playback)
    else if (audio) {
      audio.pause()
      if (!isPlaying.value && endpointSuspendTimer === null) scheduleEndpointSuspend()
    }
  }

  async function createReplacement(
    tag: string,
    options: { pinCurrent: boolean; preserveCurrent: boolean; autoplay: boolean },
    suppliedPlayback: PlaybackIntent | null = null,
  ) {
    const playback = options.autoplay ? suppliedPlayback ?? beginPlaybackIntent() : null
    if (playback && !isPlaying.value) isBuffering.value = true
    const epoch = ++playEpoch
    abortQueueWork()
    if (!options.preserveCurrent) metadataController?.abort()
    const controller = new AbortController()
    queueController = controller
    const previousTag = queue.value?.tag ?? ''
    playlistLoading.value = true
    playlistError.value = null
    const request: { tag?: string; pinFileId?: string; replaceQueueId?: string } = {}
    if (tag) request.tag = tag
    if (options.pinCurrent && currentTrack.value) request.pinFileId = currentTrack.value.id
    if (queue.value) request.replaceQueueId = queue.value.id
    try {
      let response: api.CreateQueueResponse
      try {
        response = await api.createQueue(request, controller.signal)
      } catch (requestError) {
        if (errorCode(requestError) !== 'QUEUE_NOT_FOUND' || controller.signal.aborted) throw requestError
        delete request.replaceQueueId
        response = await api.createQueue(request, controller.signal)
      }
      if (epoch !== playEpoch || controller.signal.aborted) return
      selectedTag.value = tag
      recoveryUsed = false
      await applyCreatedQueue(response, epoch, options.preserveCurrent, options.autoplay, playback)
    } catch (requestError) {
      if (epoch !== playEpoch || axios.isCancel(requestError)) return
      selectedTag.value = previousTag
      playlistError.value = 'Failed to prepare playlist'
      if (currentTrack.value) void refreshMetadata(currentTrack.value.id, epoch, false)
      if (!isPlaying.value) abandonPlayback(playback)
    } finally {
      if (queueController === controller) queueController = null
      if (epoch === playEpoch) playlistLoading.value = false
    }
  }

  async function preparePlaylist(tag: string = selectedTag.value, autoplay = false) {
    await createReplacement(tag, { pinCurrent: false, preserveCurrent: false, autoplay })
  }

  async function filterPlaylistByTag(tag: string) {
    await createReplacement(tag, {
      pinCurrent: currentTrack.value !== null,
      preserveCurrent: currentTrack.value !== null,
      autoplay: isPlaying.value,
    })
  }

  async function randomizePlaylist() {
    await createReplacement(selectedTag.value, {
      pinCurrent: false,
      preserveCurrent: false,
      autoplay: isPlaying.value,
    })
  }

  async function prependDirectory(dir: string): Promise<number | null> {
    const playback = beginPlaybackIntent()
    if (!isPlaying.value) isBuffering.value = true
    const epoch = ++playEpoch
    abortQueueWork()
    metadataController?.abort()
    const controller = new AbortController()
    queueController = controller
    playlistLoading.value = true
    playlistError.value = null
    let temporaryQueueID: string | null = null
    try {
      let response: api.PrependDirectoryResponse | null = null
      const existingQueueID = queue.value?.id
      if (existingQueueID) {
        try {
          response = await api.prependQueueDirectory(existingQueueID, dir, controller.signal)
        } catch (requestError) {
          if (errorCode(requestError) !== 'QUEUE_NOT_FOUND' || controller.signal.aborted) throw requestError
        }
      }
      if (!response) {
        const request = selectedTag.value ? { tag: selectedTag.value } : {}
        const created = await api.createQueue(request, controller.signal)
        if (epoch !== playEpoch || controller.signal.aborted) {
          void api.deleteQueue(created.queue.id)
          return null
        }
        temporaryQueueID = created.queue.id
        response = await api.prependQueueDirectory(temporaryQueueID, dir, controller.signal)
        temporaryQueueID = null
      }
      if (epoch !== playEpoch || controller.signal.aborted) {
        void api.deleteQueue(response.queue.id)
        return null
      }

      queue.value = response.queue
      resetPageCache(response)
      activeIndex.value = 0
      sidebarPage.value = 1
      evictPages()
      recoveryUsed = false
      const first = response.items.find(item => item.queueIndex === 0 && item.available)
      if (!first) throw new Error('Directory queue did not return an available first track')
      syncCurrentTrack(first)
      currentTime.value = 0
      duration.value = 0
      void refreshMetadata(first.id, epoch)
      await loadCurrentSource(0, true, playback)
      return response.directoryTrackCount
    } catch (requestError) {
      if (temporaryQueueID) {
        try { await api.deleteQueue(temporaryQueueID) } catch { /* best effort */ }
      }
      if (epoch !== playEpoch || axios.isCancel(requestError)) return null
      if (currentTrack.value) void refreshMetadata(currentTrack.value.id, epoch, false)
      if (!isPlaying.value) abandonPlayback(playback)
      throw requestError
    } finally {
      if (queueController === controller) queueController = null
      if (epoch === playEpoch) playlistLoading.value = false
    }
  }

  async function recoverQueue(epoch: number): Promise<boolean> {
    if (recoveryUsed || epoch !== playEpoch) return false
    recoveryUsed = true
    const controller = new AbortController()
    queueController?.abort()
    queueController = controller
    const request: { tag?: string; pinFileId?: string } = {}
    if (selectedTag.value) request.tag = selectedTag.value
    if (currentTrack.value) request.pinFileId = currentTrack.value.id
    try {
      const response = await api.createQueue(request, controller.signal)
      if (epoch !== playEpoch || controller.signal.aborted) return false
      await applyCreatedQueue(response, epoch, true, false, null)
      return true
    } catch (requestError) {
      if (!axios.isCancel(requestError) && epoch === playEpoch) playlistError.value = 'Playlist expired and could not be restored'
      return false
    } finally {
      if (queueController === controller) queueController = null
    }
  }

  async function loadPage(page: number, epoch = playEpoch, force = false): Promise<api.QueueItem[]> {
    const description = queue.value
    if (!description || page < 1 || page > Math.max(1, Math.ceil(description.total / description.pageSize))) return []
    const cached = pages.value.get(page)
    if (!force && cached && cached.libraryGeneration >= knownLibraryGeneration.value) {
      touchPage(page)
      return cached.items
    }
    const existing = pageRequests.get(page)
    if (existing?.queueID === description.id) return existing.promise
    existing?.controller.abort()
    const controller = new AbortController()
    const queueID = description.id
    const promise = (async () => {
      try {
        const response = await api.getQueuePage(queueID, page, controller.signal)
        if (controller.signal.aborted || queue.value?.id !== queueID) return []
        cachePage(response)
        return response.items
      } catch (requestError) {
        if (errorCode(requestError) === 'QUEUE_NOT_FOUND' && !controller.signal.aborted) {
          const recovered = await recoverQueue(epoch)
          if (recovered && queue.value) return loadPage(Math.min(page, queuePageCount.value), epoch, false)
        }
        if (!axios.isCancel(requestError) && epoch === playEpoch) playlistError.value = 'Failed to load playlist page'
        return []
      } finally {
        if (pageRequests.get(page)?.controller === controller) pageRequests.delete(page)
      }
    })()
    pageRequests.set(page, { queueID, controller, promise })
    return promise
  }

  async function itemAt(index: number, epoch: number): Promise<api.QueueItem | null> {
    if (!queue.value || index < 0 || index >= queue.value.total) return null
    const items = await loadPage(pageForIndex(index), epoch)
    if (epoch !== playEpoch) return null
    return items.find(item => item.queueIndex === index) ?? null
  }

  async function playAt(index: number) {
    const playback = beginPlaybackIntent()
    if (!isPlaying.value) isBuffering.value = true
    if (!queue.value) await preparePlaylist()
    if (!isCurrentPlaybackIntent(playback)) return
    if (!queue.value) {
      error.value = 'No tracks available'
      abandonPlayback(playback)
      return
    }
    const epoch = ++playEpoch
    selectController?.abort()
    metadataController?.abort()
    const item = await itemAt(index, epoch)
    if (epoch !== playEpoch || !isCurrentPlaybackIntent(playback)) return
    if (!item || !item.available) {
      error.value = 'Track is unavailable'
      abandonPlayback(playback)
      return
    }
    activeIndex.value = item.queueIndex
    evictPages()
    syncCurrentTrack(item)
    currentTime.value = 0
    duration.value = 0
    void refreshMetadata(item.id, epoch)
    await loadCurrentSource(0, true, playback)
  }

  async function reconcileSelection(fileID: string, epoch: number) {
    selectController?.abort()
    const controller = new AbortController()
    selectController = controller
    try {
      let response: api.SelectQueueResponse | api.CreateQueueResponse
      if (!queue.value) {
        response = await api.createQueue({
          ...(selectedTag.value ? { tag: selectedTag.value } : {}),
          pinFileId: fileID,
        }, controller.signal)
      } else {
        try {
          response = await api.selectQueueItem(queue.value.id, fileID, controller.signal)
        } catch (requestError) {
          if (errorCode(requestError) !== 'QUEUE_NOT_FOUND') throw requestError
          if (recoveryUsed) throw requestError
          recoveryUsed = true
          response = await api.createQueue({
            ...(selectedTag.value ? { tag: selectedTag.value } : {}),
            pinFileId: fileID,
          }, controller.signal)
        }
      }
      if (epoch !== playEpoch || controller.signal.aborted || currentTrack.value?.id !== fileID) return
      const queueChanged = queue.value?.id !== response.queue.id
      queue.value = response.queue
      if (queueChanged) resetPageCache(response)
      else cachePage(response)
      activeIndex.value = 'queueIndex' in response ? response.queueIndex : 0
      sidebarPage.value = pageForIndex(activeIndex.value)
      evictPages()
    } catch (requestError) {
      if (!axios.isCancel(requestError) && epoch === playEpoch) playlistError.value = 'Playing track, but failed to update playlist position'
    } finally {
      if (selectController === controller) selectController = null
    }
  }

  async function playTrack(file: api.FileEntry) {
    const playback = beginPlaybackIntent()
    if (!isPlaying.value) isBuffering.value = true
    const epoch = ++playEpoch
    selectController?.abort()
    metadataController?.abort()
    syncCurrentTrack(file)
    currentTime.value = 0
    duration.value = 0
    void refreshMetadata(file.id, epoch)
    await loadCurrentSource(0, true, playback)
    if (epoch === playEpoch) void reconcileSelection(file.id, epoch)
  }

  function pause() {
    playbackIntent += 1
    activeMediaPlaybackIntent = 0
    pendingMediaPlaybackIntent = 0
    audio?.pause()
    isBuffering.value = false
    isPlaying.value = false
    setMediaSessionPlaybackState(currentTrack.value ? 'paused' : 'none')
    scheduleEndpointSuspend()
  }

  async function resume() {
    const playback = beginPlaybackIntent()
    isBuffering.value = true
    if (!currentTrack.value) {
      if (!queue.value) await preparePlaylist()
      if (!isCurrentPlaybackIntent(playback)) return
      if (!queue.value || queue.value.total === 0) {
        abandonPlayback(playback)
        return
      }
      const item = await itemAt(activeIndex.value, playEpoch)
      if (!isCurrentPlaybackIntent(playback)) return
      if (item?.available) syncCurrentTrack(item)
    }
    if (!currentTrack.value) {
      abandonPlayback(playback)
      return
    }
    if (loadedTrackID !== currentTrack.value.id || loadedMode !== streamMode.value) {
      await loadCurrentSource(currentTime.value, true, playback)
      return
    }
    const element = getAudio()
    const epoch = playEpoch
    try {
      pendingMediaPlaybackIntent = playback.id
      if (epoch !== playEpoch || !isCurrentPlaybackIntent(playback)) {
        clearPendingPlayback(playback)
        return
      }
      activeMediaPlaybackIntent = playback.id
      await element.play()
      if (epoch !== playEpoch || !isCurrentPlaybackIntent(playback)) {
        if (activeMediaPlaybackIntent === playback.id) {
          activeMediaPlaybackIntent = 0
          element.pause()
        }
        clearPendingPlayback(playback)
        return
      }
      pendingMediaPlaybackIntent = 0
      isPlaying.value = true
      isBuffering.value = false
      setMediaSessionPlaybackState('playing')
      error.value = null
    } catch {
      if (epoch !== playEpoch || !isCurrentPlaybackIntent(playback)) return
      pendingMediaPlaybackIntent = 0
      activeMediaPlaybackIntent = 0
      isPlaying.value = false
      isBuffering.value = false
      setMediaSessionPlaybackState(currentTrack.value ? 'paused' : 'none')
      error.value = 'Failed to resume playback'
      scheduleEndpointSuspend()
    }
  }

  async function togglePlay() {
    if (isPlaying.value || isBuffering.value) pause()
    else await resume()
  }

  async function findAvailable(start: number, direction: 1 | -1, epoch: number): Promise<api.QueueItem | null> {
    const description = queue.value
    if (!description) return null
    for (let index = start; index >= 0 && index < description.total; index += direction) {
      const item = await itemAt(index, epoch)
      if (epoch !== playEpoch || queue.value?.id !== description.id) return null
      if (item?.available) return item
    }
    return null
  }

  async function next() {
    const playback = beginPlaybackIntent()
    if (!isPlaying.value) isBuffering.value = true
    if (!queue.value) await preparePlaylist()
    if (!isCurrentPlaybackIntent(playback)) return
    if (!queue.value || queue.value.total === 0) {
      error.value = 'No tracks available'
      abandonPlayback(playback)
      return
    }
    const epoch = ++playEpoch
    const originalQueue = queue.value.id
    const item = await findAvailable(activeIndex.value + 1, 1, epoch)
    if (epoch !== playEpoch || !isCurrentPlaybackIntent(playback)) return
    if (queue.value?.id !== originalQueue) {
      await next()
      return
    }
    if (!item) {
      await createReplacement(
        selectedTag.value,
        { pinCurrent: false, preserveCurrent: false, autoplay: true },
        playback,
      )
      return
    }
    activeIndex.value = item.queueIndex
    syncCurrentTrack(item)
    currentTime.value = 0
    duration.value = 0
    evictPages()
    void refreshMetadata(item.id, epoch)
    await loadCurrentSource(0, true, playback)
  }

  async function previous() {
    if (!queue.value || activeIndex.value <= 0) {
      await seek(0)
      return
    }
    const playback = beginPlaybackIntent()
    if (!isPlaying.value) isBuffering.value = true
    const epoch = ++playEpoch
    const originalQueue = queue.value.id
    const item = await findAvailable(activeIndex.value - 1, -1, epoch)
    if (epoch !== playEpoch || !isCurrentPlaybackIntent(playback)) return
    if (queue.value?.id !== originalQueue) {
      await previous()
      return
    }
    if (!item) {
      abandonPlayback(playback)
      await seek(0)
      return
    }
    activeIndex.value = item.queueIndex
    syncCurrentTrack(item)
    currentTime.value = 0
    duration.value = 0
    evictPages()
    void refreshMetadata(item.id, epoch)
    await loadCurrentSource(0, true, playback)
  }

  async function showQueuePage(page: number) {
    sidebarPage.value = clamp(page, 1, queuePageCount.value)
    sidebarLoading.value = true
    try {
      await loadPage(sidebarPage.value, playEpoch)
    } finally {
      sidebarLoading.value = false
      evictPages()
    }
  }

  async function jumpToCurrent() {
    await showQueuePage(currentPage.value)
  }

  function syncLibraryGeneration(generation: number) {
    if (generation <= knownLibraryGeneration.value) return
    knownLibraryGeneration.value = generation
    const epoch = playEpoch
    void loadPage(currentPage.value, epoch, true)
    if (sidebarPage.value !== currentPage.value) void loadPage(sidebarPage.value, epoch, true)
  }

  async function seek(seconds: number) {
    if (!currentTrack.value) return
    const target = duration.value > 0 ? clamp(seconds, 0, duration.value) : Math.max(seconds, 0)
    const shouldResume = isPlaying.value
    if (streamMode.value === 'opus') {
      const playback = shouldResume ? beginPlaybackIntent() : null
      await loadCurrentSource(target, shouldResume, playback)
      return
    }
    const element = getAudio()
    currentTime.value = target
    updateMediaSessionPosition()
    sourceOffset = 0
    try {
      element.currentTime = target
    } catch {
      const playback = shouldResume ? beginPlaybackIntent() : null
      await loadCurrentSource(target, shouldResume, playback)
    }
  }

  async function setStreamMode(mode: StreamMode) {
    if (streamMode.value === mode) return
    const shouldResume = isPlaying.value
    const playback = shouldResume ? beginPlaybackIntent() : null
    const position = currentTime.value
    streamMode.value = mode
    try { localStorage.setItem(STORAGE_KEY_STREAM_MODE, mode) } catch { /* ignore */ }
    if (currentTrack.value) await loadCurrentSource(position, shouldResume, playback)
  }

  function setVolume(value: number) {
    volume.value = clamp(value, 0, 1)
    try { localStorage.setItem(STORAGE_KEY_VOLUME, String(volume.value)) } catch { /* ignore */ }
  }

  function toggleMute() {
    isMuted.value = !isMuted.value
    try { localStorage.setItem(STORAGE_KEY_MUTED, String(isMuted.value)) } catch { /* ignore */ }
  }

  async function releaseQueue() {
    const id = queue.value?.id
    if (!id) return
    abortQueueWork()
    try { await api.deleteQueue(id) } catch { /* best effort before normal logout */ }
  }

  function reset() {
    playEpoch += 1
    playbackIntent += 1
    activeMediaPlaybackIntent = 0
    pendingMediaPlaybackIntent = 0
    sourceRequest += 1
    closeEndpoint(false)
    abortQueueWork()
    metadataController?.abort()
    metadataController = null
    if (audio) {
      audio.pause()
      audio.removeAttribute('src')
      audio.load()
    }
    currentTrack.value = null
    mediaMetadata.value = null
    isPlaying.value = false
    updateMediaSessionMetadata()
    isBuffering.value = false
    currentTime.value = 0
    duration.value = 0
    queue.value = null
    activeIndex.value = 0
    selectedTag.value = ''
    sidebarPage.value = 1
    pages.value = new Map()
    pageLRU.clear()
    knownLibraryGeneration.value = 0
    playlistLoading.value = false
    sidebarLoading.value = false
    playlistError.value = null
    error.value = null
    sourceOffset = 0
    loadedTrackID = null
    loadedMode = null
    recoveryUsed = false
  }

  watch(volume, value => {
    if (audio && !isMuted.value) audio.volume = value
  })
  watch(isMuted, muted => {
    if (audio) audio.volume = muted ? 0 : volume.value
  })
  watch([currentTrack, mediaMetadata], () => updateMediaSessionMetadata(), { immediate: true })
  watch([currentTrack, duration], () => updateMediaSessionPosition(), { immediate: true })

  registerMediaSessionHandlers()

  return {
    currentTrack,
    mediaMetadata,
    isPlaying,
    isBuffering,
    currentTime,
    duration,
    volume,
    isMuted,
    streamMode,
    queue,
    playlist,
    currentPageItems,
    sidebarItems,
    activeIndex,
    selectedTag,
    sidebarPage,
    queuePageCount,
    queuePosition,
    queueTotal,
    displayTitle,
    cachedPageCount,
    error,
    playlistLoading,
    sidebarLoading,
    playlistError,
    preparePlaylist,
    filterPlaylistByTag,
    randomizePlaylist,
    prependDirectory,
    playAt,
    playTrack,
    pause,
    resume,
    togglePlay,
    previous,
    next,
    showQueuePage,
    jumpToCurrent,
    syncLibraryGeneration,
    seek,
    setStreamMode,
    setVolume,
    toggleMute,
    releaseQueue,
    reset,
  }
})
