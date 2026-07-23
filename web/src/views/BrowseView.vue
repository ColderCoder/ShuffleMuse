<script setup lang="ts">
import axios from 'axios'
import { computed, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ChevronRight, Folder, RefreshCw } from 'lucide-vue-next'
import * as api from '../api'
import { usePlayerStore } from '../stores/player'
import { useTagsStore } from '../stores/tags'
import { useLibraryStore } from '../stores/library'
import FileList from '../components/FileList.vue'
import FilePreviewModal from '../components/FilePreviewModal.vue'

const player = usePlayerStore()
const tagsStore = useTagsStore()
const library = useLibraryStore()
const route = useRoute()
const router = useRouter()
const currentDir = ref('.')
const directories = ref<api.DirectoryEntry[]>([])
const files = ref<api.BrowseFileEntry[]>([])
const previewFile = ref<api.BrowseFileEntry | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const page = ref(1)
const total = ref(0)
const pageSize = 50
const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))
const breadcrumbs = ref<{ label: string; path: string }[]>([])
let browseRequest = 0
let browseController: AbortController | null = null

function buildBreadcrumbs(dir: string) {
  const parts = dir === '.' ? [] : dir.split('/').filter(Boolean)
  const crumbs: { label: string; path: string }[] = [{ label: 'Root', path: '.' }]
  let path = ''
  for (const part of parts) {
    path = path ? `${path}/${part}` : part
    crumbs.push({ label: part, path })
  }
  return crumbs
}

async function loadFiles(dir: string, pageNum = 1) {
	const requestID = ++browseRequest
	browseController?.abort()
	const controller = new AbortController()
	browseController = controller
  currentDir.value = dir
  page.value = pageNum
  directories.value = []
  files.value = []
  total.value = 0
  breadcrumbs.value = buildBreadcrumbs(dir)
  loading.value = true
  error.value = null
  try {
		const result = await api.getBrowse(dir, pageNum, pageSize, controller.signal)
    if (requestID !== browseRequest) return
    files.value = result.files
    directories.value = result.directories
    total.value = result.total
    page.value = pageNum
	} catch (caught) {
		if (axios.isCancel(caught)) return
    if (requestID === browseRequest) {
      total.value = 0
      error.value = `Failed to load ${dir === '.' ? 'the library root' : dir}`
    }
	} finally {
		if (browseController === controller) browseController = null
    if (requestID === browseRequest) loading.value = false
  }
}

function navigate(path: string) {
  previewFile.value = null
  const query = path === '.' ? {} : { dir: path }
  void router.push({ name: 'browse', query })
}

function changePage(nextPage: number) {
  if (nextPage < 1 || nextPage > totalPages.value || nextPage === page.value) return
  void loadFiles(currentDir.value, nextPage)
}

function handlePlay(file: api.BrowseFileEntry) {
  if (!file.audioId) return
  void player.playTrack({
    id: file.audioId,
    filepath: file.path,
    name: file.trackName ?? file.name,
    dir: file.dir,
  })
}

watch(() => route.query.dir, value => {
  const dir = typeof value === 'string' && value !== '' ? value : '.'
  previewFile.value = null
  void loadFiles(dir)
}, { immediate: true })

watch(() => library.libraryGeneration, (generation, previous) => {
  if (previous > 0 && generation !== previous) void loadFiles(currentDir.value)
})

onUnmounted(() => {
	browseRequest += 1
	browseController?.abort()
})
</script>

<template>
  <div class="view browse-view">
    <div class="browse-heading">
      <div>
        <h1 class="view-title">Browse Library</h1>
        <p class="view-subtitle">Original files, artwork, logs and playlists</p>
      </div>
      <button
        class="btn btn-icon rescan-button"
        :disabled="!library.canRescan"
        :aria-label="library.scanStatus === 'error' ? 'Retry library scan' : 'Rescan library'"
        :title="library.scanStatus === 'error' ? 'Retry library scan' : 'Rescan library'"
        @click="library.requestRescan()"
      >
        <RefreshCw :size="18" :class="{ spinning: library.scanActive || library.loading }" aria-hidden="true" />
      </button>
    </div>

    <p v-if="library.scanStatus === 'scanning'" class="scan-message" role="status">
      Rescanning in the background. Playback and the current queue are unchanged.
    </p>
    <p v-else-if="library.scanStatus === 'error'" class="browse-error" role="alert">
      {{ library.scanError || 'Library scan failed' }}
    </p>

    <nav class="breadcrumb" aria-label="Current music directory">
      <template v-for="(crumb, index) in breadcrumbs" :key="crumb.path">
        <ChevronRight v-if="index > 0" :size="14" class="breadcrumb-sep" aria-hidden="true" />
        <button
          v-if="index < breadcrumbs.length - 1"
          class="breadcrumb-link"
          @click="navigate(crumb.path)"
        >
          {{ crumb.label }}
        </button>
        <span v-else class="breadcrumb-current">{{ crumb.label }}</span>
      </template>
    </nav>

    <div v-if="loading && files.length === 0 && directories.length === 0" class="browse-loading">Loading...</div>
    <div v-if="error" class="browse-error">{{ error }}</div>

    <div v-if="directories.length || files.length" class="browse-list">
      <div v-if="directories.length" class="directory-list" aria-label="Directories">
        <button
          v-for="directory in directories"
          :key="directory.path"
          class="directory-row"
          @click="navigate(directory.path)"
        >
          <Folder :size="17" class="directory-icon" aria-hidden="true" />
          <span class="directory-info">
            <span class="directory-name">{{ directory.name }}</span>
            <span class="directory-meta">FOLDER</span>
          </span>
          <ChevronRight :size="16" class="directory-arrow" aria-hidden="true" />
        </button>
      </div>

      <FileList
        v-if="files.length"
        :files="files"
        @play="handlePlay"
        @preview="previewFile = $event"
        @tags-changed="tagsStore.fetchTags()"
      />
    </div>

    <div v-if="!loading && files.length === 0 && directories.length === 0 && !error" class="browse-empty">
      No files found
    </div>

    <div v-if="totalPages > 1" class="pagination" aria-label="Browse pages">
      <button class="btn btn-ghost" :disabled="loading || page <= 1" @click="changePage(page - 1)">Previous</button>
      <span>Page {{ page }} of {{ totalPages }} · {{ total }} entries</span>
      <button class="btn btn-ghost" :disabled="loading || page >= totalPages" @click="changePage(page + 1)">Next</button>
    </div>

    <FilePreviewModal v-if="previewFile" :file="previewFile" @close="previewFile = null" />
  </div>
</template>

<style scoped>
.browse-view {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.browse-heading .view-subtitle {
  margin-bottom: 0;
}

.browse-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
}

.rescan-button {
  flex: 0 0 auto;
}

.spinning {
  animation: spin 0.8s linear infinite;
}

.scan-message {
  padding: 0.75rem 1rem;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  color: var(--text-muted);
  font-size: 0.8125rem;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.breadcrumb {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.25rem;
  min-height: 32px;
  padding-bottom: 0.5rem;
  border-bottom: 1px solid var(--border);
}

.breadcrumb-link {
  padding: 0.25rem;
  border: 0;
  border-radius: var(--radius-sm);
  background: none;
  color: var(--accent);
  font-size: 0.875rem;
  cursor: pointer;
}

.breadcrumb-link:hover {
  background: var(--bg-tertiary);
}

.breadcrumb-sep {
  color: var(--text-muted);
}

.breadcrumb-current {
  color: var(--text-primary);
  font-size: 0.875rem;
  font-weight: 600;
}

.browse-list {
  border-top: 1px solid rgba(42, 42, 64, 0.75);
}

.directory-list {
  display: flex;
  flex-direction: column;
}

.directory-row {
  display: grid;
  grid-template-columns: 1.5rem minmax(0, 1fr) auto;
  align-items: center;
  gap: 0.625rem;
  min-height: 52px;
  padding: 0.5rem 0.75rem;
  border: 0;
  border-bottom: 1px solid rgba(42, 42, 64, 0.55);
  border-radius: 0;
  background: transparent;
  color: var(--text-secondary);
  text-align: left;
  cursor: pointer;
  transition: background 0.15s;
}

.directory-row:hover {
  color: var(--text-primary);
  background: var(--bg-tertiary);
}

.directory-icon,
.directory-arrow {
  color: var(--text-muted);
}

.directory-info {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 2px;
}

.directory-name,
.directory-meta {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.directory-name {
  color: var(--text-primary);
  font-size: 0.875rem;
}

.directory-meta {
  color: var(--text-muted);
  font-size: 0.6875rem;
}

.browse-loading,
.browse-empty {
  padding: 2rem;
  color: var(--text-muted);
  font-size: 0.9375rem;
  text-align: center;
}

.browse-error {
  padding: 1rem;
  border: 1px solid rgba(255, 74, 106, 0.35);
  border-radius: var(--radius-md);
  background: rgba(255, 74, 106, 0.1);
  color: var(--danger);
  font-size: 0.875rem;
  text-align: center;
}

.pagination {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  padding: 1rem 0;
  color: var(--text-muted);
  font-size: 0.8125rem;
}
</style>
