<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { ArrowLeft, Download, Play, Tags } from 'lucide-vue-next'
import { useTagsStore } from '../stores/tags'
import { usePlayerStore } from '../stores/player'
import { useLibraryStore } from '../stores/library'
import TagPill from '../components/TagPill.vue'
import TagManager from '../components/TagManager.vue'
import * as api from '../api'

const tagsStore = useTagsStore()
const player = usePlayerStore()
const library = useLibraryStore()

const fileTags = ref<Record<string, { id: string; name: string }[]>>({})
const fileTagsLoading = ref<Record<string, boolean>>({})
const exporting = ref(false)
const exportError = ref<string | null>(null)

watch(() => library.libraryGeneration, (generation, previous) => {
  if (previous <= 0 || generation === previous) return
  fileTags.value = {}
  fileTagsLoading.value = {}
  void tagsStore.fetchTags()
  if (tagsStore.selectedTag) void tagsStore.selectTag(tagsStore.selectedTag)
})

onMounted(() => {
  tagsStore.fetchTags()
})

async function loadFileTags(fileId: string) {
  if (fileTags.value[fileId]) return
  fileTagsLoading.value[fileId] = true
  try {
    const file = tagsStore.tagFiles.find(f => f.id === fileId)
    if (!file) return
    const names = await api.getFileTags(file.id)
    fileTags.value[fileId] = names.map(name => ({ id: name, name }))
  } catch {
    // ignore
  } finally {
    fileTagsLoading.value[fileId] = false
  }
}

function handleTagAdded(fileId: string, tag: { id: string; name: string }) {
  if (!fileTags.value[fileId]) {
    fileTags.value[fileId] = []
  }
  fileTags.value[fileId] = [...fileTags.value[fileId], tag]
  void tagsStore.fetchTags()
}

function handleTagRemoved(fileId: string, tagId: string) {
  if (fileTags.value[fileId]) {
    fileTags.value[fileId] = fileTags.value[fileId].filter(t => t.id !== tagId)
  }
  if (tagId === tagsStore.selectedTag) {
    tagsStore.removeFileFromSelection(fileId)
  }
  void tagsStore.fetchTags()
}

function handlePlay(file: api.FileEntry) {
  player.playTrack(file)
}

function changeTagPage(page: number) {
  if (page < 1 || page > tagsStore.tagFilePageCount || page === tagsStore.tagFilePage) return
  void tagsStore.loadTagPage(page)
}

async function exportCSV() {
  if (exporting.value) return
  exporting.value = true
  exportError.value = null
  try {
    const blob = await api.exportTagsCSV()
    const url = URL.createObjectURL(blob)
    try {
      const link = document.createElement('a')
      link.href = url
      link.download = 'shufflemuse-tags.csv'
      document.body.appendChild(link)
      link.click()
      link.remove()
    } finally {
      URL.revokeObjectURL(url)
    }
  } catch {
    exportError.value = 'Failed to export tags'
  } finally {
    exporting.value = false
  }
}
</script>

<template>
  <div class="view tags-view">
    <header class="tags-heading">
      <div v-if="!tagsStore.selectedTag">
        <h1 class="view-title">Tags</h1>
        <p class="view-subtitle">Organize your music with tags</p>
      </div>
      <div v-else class="tag-detail-header">
		<button class="btn btn-ghost" @click="tagsStore.clearSelection()">
		  <ArrowLeft :size="16" aria-hidden="true" />
		  <span>Back to tags</span>
		</button>
        <h1 class="view-title tag-detail-title">
          Tag: <span class="tag-detail-name">{{ tagsStore.selectedTag }}</span>
        </h1>
      </div>
      <button
        class="btn btn-ghost tags-export"
        :disabled="exporting"
        aria-label="Export tags as CSV"
        @click="exportCSV"
      >
        <Download :size="16" aria-hidden="true" />
        <span>{{ exporting ? 'Exporting...' : 'Export CSV' }}</span>
      </button>
    </header>
    <p v-if="exportError" class="tags-error" role="alert">{{ exportError }}</p>

    <!-- Tag cloud view -->
    <template v-if="!tagsStore.selectedTag">
      <div v-if="tagsStore.loading" class="tags-loading">Loading tags...</div>

      <div v-else-if="tagsStore.error" class="tags-error">{{ tagsStore.error }}</div>

      <div v-else-if="tagsStore.tags.length === 0" class="tags-empty">
        <p>No tags yet</p>
        <p class="tags-empty-hint">Add tags to your tracks from the browse view to see them here</p>
      </div>

      <div v-else class="tag-cloud">
        <TagPill
          v-for="tag in tagsStore.tags"
          :key="tag.id"
          :name="tag.count !== undefined ? `${tag.name} (${tag.count})` : tag.name"
          :interactive="true"
          @click="tagsStore.selectTag(tag.name)"
        />
      </div>
    </template>

    <!-- Tag files view -->
    <template v-else>
      <div v-if="tagsStore.loading" class="tags-loading">Loading files...</div>

      <div v-else-if="tagsStore.error" class="tags-error">{{ tagsStore.error }}</div>

      <div v-else-if="tagsStore.tagFiles.length === 0" class="tags-empty">
        <p>No files with this tag</p>
      </div>

      <div v-else class="tag-file-list">
        <div
          v-for="file in tagsStore.tagFiles"
          :key="file.id"
          class="tag-file-row"
        >
          <div class="tag-file-info">
            <span class="tag-file-name">{{ file.name }}</span>
            <span class="tag-file-dir">{{ file.dir }}</span>
          </div>
          <div class="tag-file-actions">
			<button
			  class="btn btn-icon btn-play-sm"
			  :aria-label="`Play ${file.name}`"
			  :title="`Play ${file.name}`"
			  @click="handlePlay(file)"
			>
			  <Play :size="14" fill="currentColor" aria-hidden="true" />
			</button>
            <button
              class="btn btn-ghost btn-sm"
			  :aria-label="`Manage tags for ${file.name}`"
              @click="loadFileTags(file.id)"
            >
			  <span v-if="fileTagsLoading[file.id]">...</span>
			  <template v-else>
				<Tags :size="14" aria-hidden="true" />
				<span>Tags</span>
			  </template>
            </button>
          </div>
          <div v-if="fileTags[file.id]" class="tag-file-manager">
            <TagManager
              :file-id="file.id"
              :current-tags="fileTags[file.id]"
              @tag-added="handleTagAdded(file.id, $event)"
              @tag-removed="handleTagRemoved(file.id, $event)"
            />
          </div>
        </div>
        <nav v-if="tagsStore.tagFilePageCount > 1" class="tag-file-pages" aria-label="Tagged file pages">
          <button
            class="btn btn-ghost"
            :disabled="tagsStore.loading || tagsStore.tagFilePage <= 1"
            @click="changeTagPage(tagsStore.tagFilePage - 1)"
          >
            Previous
          </button>
          <span>Page {{ tagsStore.tagFilePage }} of {{ tagsStore.tagFilePageCount }} · {{ tagsStore.tagFileTotal }} tracks</span>
          <button
            class="btn btn-ghost"
            :disabled="tagsStore.loading || tagsStore.tagFilePage >= tagsStore.tagFilePageCount"
            @click="changeTagPage(tagsStore.tagFilePage + 1)"
          >
            Next
          </button>
        </nav>
      </div>
    </template>
  </div>
</template>

<style scoped>
.tags-view {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.tags-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
}

.tags-heading .view-subtitle {
  margin-bottom: 0;
}

.tags-export {
  flex: 0 0 auto;
  gap: 0.45rem;
}

.tag-cloud {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  padding: 0.5rem 0;
}

.tags-loading,
.tags-empty {
  text-align: center;
  padding: 2rem;
  color: var(--text-muted);
  font-size: 0.9375rem;
}

.tags-empty-hint {
  font-size: 0.8125rem;
  margin-top: 0.5rem;
}

.tags-error {
  text-align: center;
  padding: 1rem;
  color: var(--danger);
  font-size: 0.875rem;
  background: rgba(255, 74, 106, 0.1);
  border-radius: var(--radius-md);
}

.tag-detail-header {
  display: flex;
  align-items: center;
  gap: 1rem;
  min-width: 0;
}

.tag-detail-title {
  margin-bottom: 0;
}

.tag-detail-name {
  color: var(--accent);
}

@media (max-width: 640px) {
  .tags-heading {
    flex-wrap: wrap;
  }

  .tag-detail-header {
    align-items: flex-start;
    flex-direction: column;
  }

  .tags-export {
    width: 100%;
  }
}

.tag-file-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.tag-file-pages {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  margin-top: 0.75rem;
  color: var(--text-muted);
  font-size: 0.8125rem;
}

.tag-file-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  padding: 0.5rem 0.75rem;
  border-radius: var(--radius-md);
  transition: background 0.15s;
}

.tag-file-row:hover {
  background: var(--bg-tertiary);
}

.tag-file-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.tag-file-name {
  font-size: 0.9375rem;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.tag-file-dir {
  font-size: 0.75rem;
  color: var(--text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.tag-file-actions {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-left: 0.75rem;
}

.btn-sm {
  padding: 0.25rem 0.5rem;
  font-size: 0.75rem;
}

.btn-play-sm {
  width: 32px;
  height: 32px;
  font-size: 0.75rem;
  opacity: 0;
  transition: opacity 0.15s;
}

.tag-file-row:hover .btn-play-sm,
.tag-file-row:focus-within .btn-play-sm,
.btn-play-sm:focus-visible {
  opacity: 1;
}

.tag-file-manager {
  width: 100%;
  padding-top: 0.5rem;
  padding-left: 0.25rem;
}

@media (max-width: 640px) {
  .btn-play-sm {
    opacity: 1;
  }
}
</style>
