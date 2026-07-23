<script setup lang="ts">
import { ref } from 'vue'
import { Download, Eye, File, FileText, Image, Music, Play, Tags } from 'lucide-vue-next'
import type { BrowseFileEntry } from '../api'
import * as api from '../api'
import TagManager from './TagManager.vue'

defineProps<{
  files: BrowseFileEntry[]
}>()

const emit = defineEmits<{
  play: [file: BrowseFileEntry]
  preview: [file: BrowseFileEntry]
  'tags-changed': []
}>()

type ManagedTag = { id: string; name: string }

const tagCounts = ref<Record<string, number>>({})
const fileTags = ref<Record<string, ManagedTag[]>>({})
const tagsLoaded = ref<Record<string, boolean>>({})
const tagsExpanded = ref<Record<string, boolean>>({})
const loadingTags = ref<Record<string, boolean>>({})
const downloadUrl = api.browseDownloadUrl

function iconFor(file: BrowseFileEntry) {
  if (file.kind === 'audio') return Music
  if (file.kind === 'image') return Image
  if (file.kind === 'text' || file.kind === 'pdf') return FileText
  return File
}

function formatSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`
}

async function toggleTags(file: BrowseFileEntry) {
  if (!file.audioId) return
  if (tagsExpanded.value[file.id]) {
    tagsExpanded.value[file.id] = false
    return
  }
  tagsExpanded.value[file.id] = true
  if (tagsLoaded.value[file.id]) return

  loadingTags.value[file.id] = true
  try {
    const names = await api.getFileTags(file.audioId)
    fileTags.value[file.id] = names.map(name => ({ id: name, name }))
    tagCounts.value[file.id] = names.length
    tagsLoaded.value[file.id] = true
  } catch {
    tagsExpanded.value[file.id] = false
  } finally {
    loadingTags.value[file.id] = false
  }
}

function handleTagAdded(fileId: string, tag: ManagedTag) {
  const tags = fileTags.value[fileId] ?? []
  fileTags.value[fileId] = [...tags, tag]
  tagCounts.value[fileId] = fileTags.value[fileId].length
  emit('tags-changed')
}

function handleTagRemoved(fileId: string, tagId: string) {
  fileTags.value[fileId] = (fileTags.value[fileId] ?? []).filter(tag => tag.id !== tagId)
  tagCounts.value[fileId] = fileTags.value[fileId].length
  emit('tags-changed')
}
</script>

<template>
  <div class="file-list">
    <div v-for="file in files" :key="file.id" class="file-row">
      <component :is="iconFor(file)" :size="17" class="file-kind-icon" aria-hidden="true" />
      <div class="file-info">
        <span class="file-name">{{ file.name }}</span>
        <span class="file-meta">{{ file.kind.toUpperCase() }} · {{ formatSize(file.size) }}</span>
      </div>
      <div class="file-actions">
        <button
          v-if="file.playable"
          class="btn btn-icon file-action"
          :aria-label="`Play ${file.name}`"
          :title="`Play ${file.name}`"
          @click="emit('play', file)"
        >
          <Play :size="14" fill="currentColor" aria-hidden="true" />
        </button>
        <button
          v-if="file.previewable"
          class="btn btn-icon file-action"
          :aria-label="`Preview ${file.name}`"
          :title="`Preview ${file.name}`"
          @click="emit('preview', file)"
        >
          <Eye :size="15" aria-hidden="true" />
        </button>
        <a
          class="btn btn-icon file-action"
          :href="downloadUrl(file.path)"
          :aria-label="`Download ${file.name}`"
          :title="`Download ${file.name}`"
        >
          <Download :size="15" aria-hidden="true" />
        </a>
        <button
          v-if="file.playable"
          class="btn btn-icon file-action tag-button"
          :class="{ 'tag-button--active': tagsExpanded[file.id] }"
          :aria-expanded="!!tagsExpanded[file.id]"
          :aria-label="`Manage tags for ${file.name}`"
          :title="tagsLoaded[file.id] ? `${tagCounts[file.id]} tags` : 'Manage tags'"
          @click="toggleTags(file)"
        >
          <span v-if="loadingTags[file.id]" class="tag-button-loading">...</span>
          <template v-else>
            <Tags :size="14" aria-hidden="true" />
            <span v-if="tagsLoaded[file.id]" class="tag-count">{{ tagCounts[file.id] }}</span>
          </template>
        </button>
      </div>
      <div v-if="tagsExpanded[file.id] && file.audioId" class="file-tag-manager">
        <TagManager
          :file-id="file.audioId"
          :current-tags="fileTags[file.id] ?? []"
          @tag-added="handleTagAdded(file.id, $event)"
          @tag-removed="handleTagRemoved(file.id, $event)"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.file-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.file-row {
  display: grid;
  grid-template-columns: 1.5rem minmax(0, 1fr) auto;
  align-items: center;
  gap: 0.625rem;
  min-height: 52px;
  padding: 0.5rem 0.75rem;
  border-bottom: 1px solid rgba(42, 42, 64, 0.55);
  transition: background 0.15s;
}

.file-row:hover {
  background: var(--bg-tertiary);
}

.file-kind-icon {
  color: var(--text-muted);
}

.file-info {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 2px;
}

.file-name,
.file-meta {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-name {
  color: var(--text-primary);
  font-size: 0.875rem;
}

.file-meta {
  color: var(--text-muted);
  font-size: 0.6875rem;
}

.file-actions {
  display: flex;
  align-items: center;
  gap: 0.3rem;
}

.file-action {
  width: 31px;
  height: 31px;
  border-radius: var(--radius-sm);
  color: var(--text-secondary);
}

.tag-button {
  gap: 2px;
}

.tag-button--active {
  color: var(--accent);
  border-color: var(--accent);
}

.tag-count,
.tag-button-loading {
  font-size: 0.625rem;
  font-variant-numeric: tabular-nums;
}

.file-tag-manager {
  grid-column: 2 / -1;
  width: 100%;
  padding: 0.375rem 0 0.125rem;
}

@media (max-width: 640px) {
  .file-row {
    grid-template-columns: 1.25rem minmax(0, 1fr);
    padding-inline: 0.5rem;
  }

  .file-actions {
    grid-column: 2;
    justify-content: flex-start;
  }

  .file-tag-manager {
    grid-column: 2;
  }
}
</style>
