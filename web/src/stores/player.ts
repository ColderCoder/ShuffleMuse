import axios from 'axios'
import { computed, ref, shallowRef, watch } from 'vue'
import { defineStore } from 'pinia'
import * as api from '../api'

const STORAGE_KEY_VOLUME = 'shufflemuse-volume'
const STORAGE_KEY_MUTED = 'shufflemuse-muted'
const STORAGE_KEY_STREAM_MODE = 'shufflemuse-stream-mode'
const MAX_CACHED_PAGES = 5

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
  let sourceRequest = 0
  let sourceOffset = 0
  let loadedTrackID: string | null = null
  let loadedMode: StreamMode | null = null
  let queueController: AbortController | null = null
  let selectController: AbortController | null = null
  let metadataController: AbortController | null = null
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

  function apiQueuePageSize(): number {
    return queue.value?.pageSize ?? 200
  }

  function pageForIndex(index: number): number {
    return Math.floor(Math.max(index, 0) / apiQueuePageSize()) + 1
  }

  function getAudio(): HTMLAudioElement {
    if (!audio) {
      audio = new Audio()
      audio.preload = 'metadata'
      audio.volume = isMuted.value ? 0 : volume.value
      audio.addEventListener('ended', () => {
        currentTime.value = duration.value
        void next()
      })
      audio.addEventListener('playing', () => {
        isBuffering.value = false
        isPlaying.value = true
      })
      audio.addEventListener('pause', () => {
        isBuffering.value = false
        isPlaying.value = false
      })
      audio.addEventListener('waiting', () => {
        if (!audio?.paused) isBuffering.value = true
      })
      audio.addEventListener('canplay', () => { isBuffering.value = false })
      audio.addEventListener('seeked', () => { isBuffering.value = false })
      audio.addEventListener('timeupdate', () => {
        if (!audio) return
        const absoluteTime = sourceOffset + audio.currentTime
        currentTime.value = duration.value > 0
          ? clamp(absoluteTime, 0, duration.value)
          : Math.max(absoluteTime, 0)
      })
      audio.addEventListener('durationchange', () => {
        if (!audio || streamMode.value !== 'original') return
        if (Number.isFinite(audio.duration) && audio.duration > 0) duration.value = audio.duration
      })
      audio.addEventListener('error', () => {
        error.value = 'Playback error'
        isBuffering.value = false
        isPlaying.value = false
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
    }
    try {
      const metadata = await api.getFileMetadata(trackID, controller.signal)
      if (epoch !== playEpoch || currentTrack.value?.id !== trackID || controller.signal.aborted) return
      mediaMetadata.value = metadata
      duration.value = metadata.durationSeconds
    } catch (requestError) {
      if (!axios.isCancel(requestError) && epoch === playEpoch && currentTrack.value?.id === trackID) {
        mediaMetadata.value = null
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

  async function loadCurrentSource(position: number, autoplay: boolean) {
    if (!currentTrack.value) return
    const trackID = currentTrack.value.id
    const mode = streamMode.value
    const requestID = ++sourceRequest
    const target = duration.value > 0 ? clamp(position, 0, duration.value) : Math.max(position, 0)
    const element = getAudio()
    element.pause()
    sourceOffset = mode === 'opus' ? target : 0
    currentTime.value = target
    const url = sourceUrl(trackID, mode, target)
    currentTrack.value = { ...currentTrack.value, streamUrl: url }
    loadedTrackID = trackID
    loadedMode = mode
    isBuffering.value = autoplay
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
        await element.play()
        if (requestID !== sourceRequest) return
        isPlaying.value = true
      } else {
        isPlaying.value = false
        isBuffering.value = false
      }
    } catch {
      if (requestID !== sourceRequest) return
      isPlaying.value = false
      isBuffering.value = false
      error.value = 'Failed to load track'
    }
  }

  async function applyCreatedQueue(
    response: api.CreateQueueResponse,
    epoch: number,
    preserveCurrent: boolean,
    autoplay: boolean,
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
      playlistError.value = 'No tracks available'
      return
    }
    activeIndex.value = first.queueIndex
    syncCurrentTrack(first)
    currentTime.value = 0
    duration.value = 0
    void refreshMetadata(first.id, epoch)
    if (autoplay) await loadCurrentSource(0, true)
    else if (audio) audio.pause()
  }

  async function createReplacement(
    tag: string,
    options: { pinCurrent: boolean; preserveCurrent: boolean; autoplay: boolean },
  ) {
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
      await applyCreatedQueue(response, epoch, options.preserveCurrent, options.autoplay)
    } catch (requestError) {
      if (epoch !== playEpoch || axios.isCancel(requestError)) return
      selectedTag.value = previousTag
      playlistError.value = 'Failed to prepare playlist'
      if (currentTrack.value) void refreshMetadata(currentTrack.value.id, epoch, false)
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
      await applyCreatedQueue(response, epoch, true, false)
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
    if (!queue.value) await preparePlaylist()
    if (!queue.value) {
      error.value = 'No tracks available'
      return
    }
    const epoch = ++playEpoch
    selectController?.abort()
    metadataController?.abort()
    const item = await itemAt(index, epoch)
    if (epoch !== playEpoch) return
    if (!item || !item.available) {
      error.value = 'Track is unavailable'
      return
    }
    activeIndex.value = item.queueIndex
    evictPages()
    syncCurrentTrack(item)
    currentTime.value = 0
    duration.value = 0
    void refreshMetadata(item.id, epoch)
    await loadCurrentSource(0, true)
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
    const epoch = ++playEpoch
    selectController?.abort()
    metadataController?.abort()
    syncCurrentTrack(file)
    currentTime.value = 0
    duration.value = 0
    void refreshMetadata(file.id, epoch)
    await loadCurrentSource(0, true)
    if (epoch === playEpoch) void reconcileSelection(file.id, epoch)
  }

  function pause() {
    getAudio().pause()
    isBuffering.value = false
    isPlaying.value = false
  }

  async function resume() {
    if (!currentTrack.value) {
      if (!queue.value) await preparePlaylist()
      if (!queue.value || queue.value.total === 0) return
      const item = await itemAt(activeIndex.value, playEpoch)
      if (item?.available) syncCurrentTrack(item)
    }
    if (!currentTrack.value) return
    if (loadedTrackID !== currentTrack.value.id || loadedMode !== streamMode.value) {
      await loadCurrentSource(currentTime.value, true)
      return
    }
    const element = getAudio()
    isBuffering.value = true
    try {
      await element.play()
      isPlaying.value = true
      isBuffering.value = false
      error.value = null
    } catch {
      isPlaying.value = false
      isBuffering.value = false
      error.value = 'Failed to resume playback'
    }
  }

  async function togglePlay() {
    if (isPlaying.value) pause()
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
    if (!queue.value) await preparePlaylist()
    if (!queue.value || queue.value.total === 0) {
      error.value = 'No tracks available'
      return
    }
    const epoch = ++playEpoch
    const originalQueue = queue.value.id
    const item = await findAvailable(activeIndex.value + 1, 1, epoch)
    if (epoch !== playEpoch) return
    if (queue.value?.id !== originalQueue) {
      await next()
      return
    }
    if (!item) {
      await createReplacement(selectedTag.value, { pinCurrent: false, preserveCurrent: false, autoplay: true })
      return
    }
    activeIndex.value = item.queueIndex
    syncCurrentTrack(item)
    currentTime.value = 0
    duration.value = 0
    evictPages()
    void refreshMetadata(item.id, epoch)
    await loadCurrentSource(0, true)
  }

  async function previous() {
    if (!queue.value || activeIndex.value <= 0) {
      await seek(0)
      return
    }
    const epoch = ++playEpoch
    const originalQueue = queue.value.id
    const item = await findAvailable(activeIndex.value - 1, -1, epoch)
    if (epoch !== playEpoch) return
    if (queue.value?.id !== originalQueue) {
      await previous()
      return
    }
    if (!item) {
      await seek(0)
      return
    }
    activeIndex.value = item.queueIndex
    syncCurrentTrack(item)
    currentTime.value = 0
    duration.value = 0
    evictPages()
    void refreshMetadata(item.id, epoch)
    await loadCurrentSource(0, true)
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
      await loadCurrentSource(target, shouldResume)
      return
    }
    const element = getAudio()
    currentTime.value = target
    sourceOffset = 0
    try {
      element.currentTime = target
    } catch {
      await loadCurrentSource(target, shouldResume)
    }
  }

  async function setStreamMode(mode: StreamMode) {
    if (streamMode.value === mode) return
    const shouldResume = isPlaying.value
    const position = currentTime.value
    streamMode.value = mode
    try { localStorage.setItem(STORAGE_KEY_STREAM_MODE, mode) } catch { /* ignore */ }
    if (currentTrack.value) await loadCurrentSource(position, shouldResume)
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
    sourceRequest += 1
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
