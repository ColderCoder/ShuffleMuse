import axios from 'axios'
import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import * as api from '../api'

const ACTIVE_STATUS_POLL_MS = 2000
const IDLE_STATUS_POLL_MS = 30000

export const useLibraryStore = defineStore('library', () => {
  const status = ref<api.Status | null>(null)
  const loading = ref(false)
  const statusError = ref<string | null>(null)
  const rescanError = ref<string | null>(null)
  let pollTimer: ReturnType<typeof setTimeout> | null = null
  let statusRequest: { epoch: number; controller: AbortController; promise: Promise<void> } | null = null
  let rescanController: AbortController | null = null
  let lifecycleEpoch = 0
  let running = false

  const libraryReady = computed(() => status.value?.libraryReady === true)
  const libraryGeneration = computed(() => status.value?.libraryGeneration ?? 0)
  const scanStatus = computed(() => status.value?.scanStatus ?? 'initializing')
  const scanError = computed(() => status.value?.scanError || rescanError.value)
  const scanActive = computed(() => scanStatus.value === 'initializing' || scanStatus.value === 'scanning')
  const canRescan = computed(() => !loading.value && !scanActive.value)

  function pageIsHidden() {
    return typeof document !== 'undefined' && document.visibilityState === 'hidden'
  }

  function clearPollTimer() {
    if (pollTimer) clearTimeout(pollTimer)
    pollTimer = null
  }

  function schedulePoll() {
    if (!running || loading.value || pollTimer || pageIsHidden()) return
    const delay = scanActive.value ? ACTIVE_STATUS_POLL_MS : IDLE_STATUS_POLL_MS
    pollTimer = setTimeout(() => {
      pollTimer = null
      const epoch = lifecycleEpoch
      void refreshStatus().finally(() => {
        if (epoch === lifecycleEpoch) schedulePoll()
      })
    }, delay)
  }

  function handleVisibilityChange() {
    clearPollTimer()
    if (!running || pageIsHidden()) return
    const epoch = lifecycleEpoch
    void refreshStatus().finally(() => {
      if (epoch === lifecycleEpoch) schedulePoll()
    })
  }

  async function refreshStatus() {
    const epoch = lifecycleEpoch
    if (statusRequest?.epoch === epoch) return statusRequest.promise
    const controller = new AbortController()
    const request = {
      epoch,
      controller,
      promise: Promise.resolve(),
    }
    request.promise = (async () => {
      try {
        const next = await api.getStatus(controller.signal)
        if (epoch !== lifecycleEpoch) return
        status.value = next
        statusError.value = null
      } catch (error) {
        if (epoch !== lifecycleEpoch || axios.isCancel(error)) return
        statusError.value = 'Failed to read library status'
      } finally {
        if (statusRequest === request) statusRequest = null
      }
    })()
    statusRequest = request
    return request.promise
  }

  async function start(initialStatus?: api.Status) {
    if (running) {
      if (initialStatus && !status.value) {
        status.value = initialStatus
        statusError.value = null
      }
      await statusRequest?.promise
      return
    }
    running = true
    const epoch = lifecycleEpoch
    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', handleVisibilityChange)
    }
    if (initialStatus) {
      status.value = initialStatus
      statusError.value = null
    } else if (!pageIsHidden()) {
      await refreshStatus()
    }
    if (epoch === lifecycleEpoch) schedulePoll()
  }

  function stop() {
    running = false
    lifecycleEpoch += 1
    clearPollTimer()
    if (typeof document !== 'undefined') {
      document.removeEventListener('visibilitychange', handleVisibilityChange)
    }
    statusRequest?.controller.abort()
    statusRequest = null
    rescanController?.abort()
    rescanController = null
    status.value = null
    loading.value = false
    statusError.value = null
    rescanError.value = null
  }

  async function requestRescan() {
    if (!canRescan.value) return
    clearPollTimer()
    const epoch = lifecycleEpoch
    const controller = new AbortController()
    rescanController = controller
    loading.value = true
    rescanError.value = null
    try {
      await api.rescan(controller.signal)
      if (epoch !== lifecycleEpoch) return
      const pendingStatus = statusRequest?.epoch === epoch ? statusRequest.promise : null
      if (pendingStatus) await pendingStatus
      if (epoch !== lifecycleEpoch) return
      if (status.value) {
        status.value = {
          ...status.value,
          scanStatus: status.value.libraryReady ? 'scanning' : 'initializing',
          scanError: null,
        }
      }
      await refreshStatus()
    } catch (error) {
      if (epoch !== lifecycleEpoch || axios.isCancel(error)) return
      if (axios.isAxiosError(error)) {
        const message = error.response?.data?.error
        rescanError.value = typeof message === 'string' ? message : 'Failed to start rescan'
      } else {
        rescanError.value = 'Failed to start rescan'
      }
    } finally {
      if (rescanController === controller) rescanController = null
      if (epoch === lifecycleEpoch) {
        loading.value = false
        schedulePoll()
      }
    }
  }

  return {
    status,
    loading,
    statusError,
    rescanError,
    libraryReady,
    libraryGeneration,
    scanStatus,
    scanError,
    scanActive,
    canRescan,
    refreshStatus,
    start,
    stop,
    requestRescan,
  }
})
