import axios from 'axios'
import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import * as api from '../api'

export interface TagItem {
  id: string
  name: string
  count?: number
}

const TAG_FILE_PAGE_SIZE = 200

export const useTagsStore = defineStore('tags', () => {
  const tags = ref<TagItem[]>([])
  const selectedTag = ref<string | null>(null)
  const tagFiles = ref<api.FileEntry[]>([])
  const tagFileTotal = ref(0)
  const tagFilePage = ref(1)
  const tagsLoading = ref(false)
  const filesLoading = ref(false)
  const tagsError = ref<string | null>(null)
  const filesError = ref<string | null>(null)
  const loading = computed(() => selectedTag.value ? filesLoading.value : tagsLoading.value)
  const error = computed(() => selectedTag.value ? filesError.value : tagsError.value)
  const tagFilePageCount = computed(() => Math.max(1, Math.ceil(tagFileTotal.value / TAG_FILE_PAGE_SIZE)))
  let tagsRequest = 0
  let filesRequest = 0
  let tagsController: AbortController | null = null
  let filesController: AbortController | null = null

  async function fetchTags() {
    const requestID = ++tagsRequest
    tagsController?.abort()
    const controller = new AbortController()
    tagsController = controller
    tagsLoading.value = true
    tagsError.value = null
    try {
      const result = await api.getTags(controller.signal)
      if (requestID !== tagsRequest || controller.signal.aborted) return
      tags.value = result.map(t => ({
        id: t.name,
        name: t.name,
        count: t.count,
      }))
    } catch (caught) {
      if (!axios.isCancel(caught) && requestID === tagsRequest) tagsError.value = 'Failed to load tags'
    } finally {
      if (tagsController === controller) tagsController = null
      if (requestID === tagsRequest) tagsLoading.value = false
    }
  }

  async function loadTagPage(page: number) {
    const name = selectedTag.value
    if (!name || page < 1) return
    const requestID = ++filesRequest
    filesController?.abort()
    const controller = new AbortController()
    filesController = controller
    filesLoading.value = true
    filesError.value = null
    try {
      const result = await api.getTagFiles(name, page, TAG_FILE_PAGE_SIZE, controller.signal)
      if (requestID !== filesRequest || selectedTag.value !== name || controller.signal.aborted) return
      tagFiles.value = result.items
      tagFileTotal.value = result.total
      tagFilePage.value = result.page

      const lastPage = Math.max(1, Math.ceil(result.total / TAG_FILE_PAGE_SIZE))
      if (result.items.length === 0 && result.total > 0 && page > lastPage) {
        await loadTagPage(lastPage)
      }
    } catch (caught) {
      if (!axios.isCancel(caught) && requestID === filesRequest && selectedTag.value === name) {
        filesError.value = 'Failed to load files for tag'
        tagFiles.value = []
        tagFileTotal.value = 0
      }
    } finally {
      if (filesController === controller) filesController = null
      if (requestID === filesRequest) filesLoading.value = false
    }
  }

  async function selectTag(name: string) {
    selectedTag.value = name
    tagFiles.value = []
    tagFileTotal.value = 0
    tagFilePage.value = 1
    await loadTagPage(1)
  }

  function clearSelection() {
    filesRequest += 1
    filesController?.abort()
    filesController = null
    selectedTag.value = null
    tagFiles.value = []
    tagFileTotal.value = 0
    tagFilePage.value = 1
    filesLoading.value = false
    filesError.value = null
  }

  function removeFileFromSelection(fileID: string) {
    const previousLength = tagFiles.value.length
    tagFiles.value = tagFiles.value.filter(file => file.id !== fileID)
    if (tagFiles.value.length !== previousLength && tagFileTotal.value > 0) {
      tagFileTotal.value -= 1
    }
  }

  function reset() {
    tagsRequest += 1
    filesRequest += 1
    tagsController?.abort()
    filesController?.abort()
    tagsController = null
    filesController = null
    tags.value = []
    selectedTag.value = null
    tagFiles.value = []
    tagFileTotal.value = 0
    tagFilePage.value = 1
    tagsLoading.value = false
    filesLoading.value = false
    tagsError.value = null
    filesError.value = null
  }

  return {
    tags,
    selectedTag,
    tagFiles,
    tagFileTotal,
    tagFilePage,
    tagFilePageCount,
    tagFilePageSize: TAG_FILE_PAGE_SIZE,
    loading,
    error,
    fetchTags,
    selectTag,
    loadTagPage,
    clearSelection,
    removeFileFromSelection,
    reset,
  }
})
