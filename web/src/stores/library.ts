import axios from 'axios'
import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import * as api from '../api'

const STATUS_POLL_MS = 2000

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

  function schedulePoll() {
    if (!running || pollTimer) return
    pollTimer = setTimeout(() => {
      pollTimer = null
      void refreshStatus().finally(schedulePoll)
    }, STATUS_POLL_MS)
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

  async function start() {
    if (running) return refreshStatus()
    running = true
    await refreshStatus()
    schedulePoll()
  }

  function stop() {
    running = false
    lifecycleEpoch += 1
    if (pollTimer) clearTimeout(pollTimer)
    pollTimer = null
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
    const epoch = lifecycleEpoch
    const controller = new AbortController()
    rescanController = controller
    loading.value = true
    rescanError.value = null
    try {
      await api.rescan(controller.signal)
      if (epoch !== lifecycleEpoch) return
      if (status.value) {
        status.value = {
          ...status.value,
          scanStatus: status.value.libraryReady ? 'scanning' : 'initializing',
          scanError: null,
        }
      }
      await refreshStatus()
      if (epoch === lifecycleEpoch) schedulePoll()
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
      if (epoch === lifecycleEpoch) loading.value = false
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
