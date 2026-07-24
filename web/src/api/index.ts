import axios from 'axios'

const http = axios.create({
  baseURL: '/api',
  withCredentials: true,
})

let unauthorizedHandler: (() => void | Promise<void>) | null = null

export function setUnauthorizedHandler(handler: (() => void | Promise<void>) | null) {
  unauthorizedHandler = handler
}

http.interceptors.response.use(
  response => response,
  async error => {
    if (axios.isAxiosError(error) && error.response?.status === 401) {
      const url = error.config?.url ?? ''
      if (!url.endsWith('/auth/login') && url !== '/auth/login') {
        await unauthorizedHandler?.()
      }
    }
    return Promise.reject(error)
  },
)

export interface FileEntry {
  id: string
  name: string
  dir: string
  filepath: string
}

export interface QueueDescription {
  id: string
  tag: string
  createdGeneration: number
  total: number
  pageSize: number
}

export interface QueueItem extends FileEntry {
  queueIndex: number
  available: boolean
}

export interface QueuePage {
  queue: QueueDescription
  items: QueueItem[]
  page: number
  libraryGeneration: number
}

export interface CreateQueueResponse extends QueuePage {
  pinApplied: boolean
}

export interface SelectQueueResponse extends QueuePage {
  queueIndex: number
}

export interface DirectoryEntry {
  name: string
  path: string
}

type BrowseFileKind = 'audio' | 'image' | 'text' | 'pdf' | 'other'

export interface BrowseFileEntry {
  id: string
  name: string
  path: string
  dir: string
  kind: BrowseFileKind
  mimeType: string
  size: number
  modified: string
  previewable: boolean
  playable: boolean
  audioId?: string
  trackName?: string
}

export interface BrowseResponse {
  directories: DirectoryEntry[]
  files: BrowseFileEntry[]
  total: number
  page: number
  generation?: number
}

export interface FileMetadata {
  title?: string
  codec: string
  bitrateKbps: number
  bitrateApproximate: boolean
  durationSeconds: number
}

interface TagInfo {
  name: string
  count: number
}

interface TagsResponse {
  tags: TagInfo[]
}

interface FileTagsResponse {
  tags: string[]
}

export interface PaginatedResponse<T> {
  items: T[]
  total: number
  page: number
  generation: number
}

export interface Status {
  fileCount?: number
  libraryReady?: boolean
  libraryGeneration?: number
  scanStatus?: 'initializing' | 'idle' | 'scanning' | 'error'
  uptime?: string
  lastScan?: string | null
  scanError?: string | null
  opusBitrate?: number
  authRequired: boolean
  authenticated: boolean
}

export async function login(password: string, remember: boolean) {
  const res = await http.post('/auth/login', { password, remember })
  return res.data
}

export async function logout() {
  await http.post('/auth/logout')
}

export async function getFiles(page?: number, limit?: number) {
  const params: Record<string, string | number> = {}
  if (page !== undefined) params.page = page
  if (limit !== undefined) params.limit = limit
  const res = await http.get<PaginatedResponse<FileEntry>>('/files', { params })
  return res.data
}

export async function getBrowse(dir: string = '.', page?: number, limit?: number, signal?: AbortSignal) {
  const params: Record<string, string | number> = { dir }
  if (page !== undefined) params.page = page
  if (limit !== undefined) params.limit = limit
  const res = await http.get<BrowseResponse>('/browse', { params, signal })
  return res.data
}

export function browseContentUrl(path: string) {
  return `/api/browse/content?path=${encodeURIComponent(path)}`
}

export function browseDownloadUrl(path: string) {
  return `/api/browse/download?path=${encodeURIComponent(path)}`
}

export async function getBrowseText(path: string, signal?: AbortSignal) {
  const res = await http.get<string>('/browse/content', {
    params: { path },
    responseType: 'text',
    signal,
  })
  return res.data
}

export async function getTags(signal?: AbortSignal) {
  const res = await http.get<TagsResponse>('/tags', { signal })
  return res.data.tags
}

export async function exportTagsCSV() {
  const res = await http.get<Blob>('/tags/export', { responseType: 'blob' })
  return res.data
}

export async function getFileTags(id: string) {
  const res = await http.get<FileTagsResponse>(`/files/${encodeURIComponent(id)}/tags`)
  return res.data.tags
}

export async function getFileMetadata(id: string, signal?: AbortSignal) {
  const res = await http.get<FileMetadata>(`/files/${encodeURIComponent(id)}/metadata`, { signal })
  return res.data
}

export async function createQueue(
  request: { tag?: string; pinFileId?: string; replaceQueueId?: string },
  signal?: AbortSignal,
) {
  const res = await http.post<CreateQueueResponse>('/queues', request, { signal })
  return res.data
}

export async function getQueuePage(id: string, page: number, signal?: AbortSignal) {
  const res = await http.get<QueuePage>(`/queues/${encodeURIComponent(id)}/items`, {
    params: { page },
    signal,
  })
  return res.data
}

export async function selectQueueItem(id: string, fileId: string, signal?: AbortSignal) {
  const res = await http.post<SelectQueueResponse>(`/queues/${encodeURIComponent(id)}/select`, { fileId }, { signal })
  return res.data
}

export async function deleteQueue(id: string, signal?: AbortSignal) {
  await http.delete(`/queues/${encodeURIComponent(id)}`, { signal })
}

export function fileCoverUrl(id: string) {
  return `/api/files/${encodeURIComponent(id)}/cover`
}

export function directoryCoverUrl(dir: string) {
  const normalized = dir.replace(/\\/g, '/') || '.'
  return `/api/covers/directory?dir=${encodeURIComponent(normalized)}`
}

export async function addTag(trackId: string, tagName: string) {
  await http.post(`/files/${encodeURIComponent(trackId)}/tags`, { tag: tagName })
}

export async function removeTag(trackId: string, tagName: string) {
  await http.delete(`/files/${encodeURIComponent(trackId)}/tags/${encodeURIComponent(tagName)}`)
}

export async function search(query: string, page = 1, limit = 50, signal?: AbortSignal) {
  const res = await http.get<PaginatedResponse<FileEntry> & { query: string }>('/search', {
    params: { q: query, page, limit },
    signal,
  })
  return res.data
}

export async function getTagFiles(tag: string, page?: number, limit?: number, signal?: AbortSignal) {
  const params: Record<string, string | number> = {}
  if (page !== undefined) params.page = page
  if (limit !== undefined) params.limit = limit
  const res = await http.get<PaginatedResponse<FileEntry>>(`/tags/${encodeURIComponent(tag)}/files`, { params, signal })
  return res.data
}

export async function getStatus(signal?: AbortSignal) {
  const res = await http.get<Status>('/status', { signal })
  return res.data
}

export async function rescan(signal?: AbortSignal) {
  await http.post('/rescan', undefined, { signal })
}

export interface GraveyardEntry {
  filepath: string
  name: string
  dir: string
  tags: string[]
}

export async function getGraveyard(page = 1, limit = 50) {
  const res = await http.get<PaginatedResponse<GraveyardEntry>>('/graveyard', {
    params: { page, limit },
  })
  return res.data
}

export async function deleteGraveyardEntry(path: string) {
  await http.delete('/graveyard', { params: { path } })
}
