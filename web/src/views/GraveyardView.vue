<script setup lang="ts">
import axios from 'axios'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { ArchiveX, Trash2 } from 'lucide-vue-next'
import * as api from '../api'
import { useLibraryStore } from '../stores/library'

const pageSize = 50
const library = useLibraryStore()
const items = ref<api.GraveyardEntry[]>([])
const total = ref(0)
const page = ref(1)
const loading = ref(false)
const deleting = ref<string | null>(null)
const error = ref<string | null>(null)
const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))
let requestID = 0

async function load(targetPage = page.value) {
  const currentRequest = ++requestID
  loading.value = true
  error.value = null
  try {
    const response = await api.getGraveyard(targetPage, pageSize)
    if (currentRequest !== requestID) return
    items.value = response.items
    total.value = response.total
    page.value = targetPage
    if (items.value.length === 0 && total.value > 0 && targetPage > totalPages.value) {
      await load(totalPages.value)
    }
  } catch {
    if (currentRequest === requestID) error.value = 'Failed to load missing tagged files'
  } finally {
    if (currentRequest === requestID) loading.value = false
  }
}

async function remove(entry: api.GraveyardEntry) {
  if (!window.confirm(`Remove all orphaned tags for “${entry.filepath}”? No disk file will be deleted.`)) return
  deleting.value = entry.filepath
  error.value = null
  try {
    await api.deleteGraveyardEntry(entry.filepath)
    const nextTotal = Math.max(total.value - 1, 0)
    const nextLastPage = Math.max(1, Math.ceil(nextTotal / pageSize))
    await load(Math.min(page.value, nextLastPage))
  } catch (caught) {
    if (axios.isAxiosError(caught) && caught.response?.status === 409 && caught.response.data?.code === 'FILE_ONLINE') {
      await load(page.value)
      error.value = 'This file is online again. Rescan results have preserved its tags.'
    } else {
      error.value = 'Failed to remove orphaned tags'
    }
  } finally {
    deleting.value = null
  }
}

watch(() => library.libraryGeneration, (generation, previous) => {
  if (previous > 0 && generation !== previous) void load(page.value)
})

onMounted(() => void load(1))
onUnmounted(() => { requestID += 1 })
</script>

<template>
  <div class="view graveyard-view">
    <header class="graveyard-heading">
      <div>
        <h1 class="view-title">Graveyard</h1>
        <p class="view-subtitle">Tagged paths no longer present in the current library</p>
      </div>
      <span class="graveyard-count">{{ total }} missing</span>
    </header>

    <p class="graveyard-note">
      Removing an entry clears only its orphaned tag records. ShuffleMuse never deletes a file from disk here.
    </p>
    <p v-if="error" class="graveyard-error" role="alert">{{ error }}</p>
    <div v-if="loading && items.length === 0" class="graveyard-empty" role="status">Loading missing files...</div>
    <div v-else-if="items.length === 0" class="graveyard-empty">
      <ArchiveX :size="34" aria-hidden="true" />
      <strong>No missing tagged files</strong>
      <span>Recovered files disappear automatically after a successful rescan.</span>
    </div>

    <ul v-else class="graveyard-list">
      <li v-for="entry in items" :key="entry.filepath" class="graveyard-row">
        <div class="graveyard-copy">
          <strong>{{ entry.name }}</strong>
          <span>{{ entry.dir }}</span>
          <code>{{ entry.filepath }}</code>
          <span class="graveyard-tags" :aria-label="`Tags: ${entry.tags.join(', ')}`">
            <span v-for="tag in entry.tags" :key="tag">{{ tag }}</span>
          </span>
        </div>
        <button
          class="btn btn-icon graveyard-delete"
          :disabled="deleting === entry.filepath"
          :aria-label="`Remove orphaned tags for ${entry.name}`"
          :title="`Remove orphaned tags for ${entry.name}`"
          @click="remove(entry)"
        >
          <Trash2 :size="16" aria-hidden="true" />
        </button>
      </li>
    </ul>

    <nav v-if="totalPages > 1" class="graveyard-pages" aria-label="Graveyard pages">
      <button class="btn btn-ghost" :disabled="loading || page <= 1" @click="load(page - 1)">Previous</button>
      <span>Page {{ page }} of {{ totalPages }}</span>
      <button class="btn btn-ghost" :disabled="loading || page >= totalPages" @click="load(page + 1)">Next</button>
    </nav>
  </div>
</template>

<style scoped>
.graveyard-view { display: flex; flex-direction: column; gap: 1rem; }
.graveyard-heading { display: flex; align-items: start; justify-content: space-between; gap: 1rem; }
.graveyard-heading .view-subtitle { margin: 0; }
.graveyard-count {
  flex: 0 0 auto; padding: 0.375rem 0.625rem; border: 1px solid var(--border); border-radius: 999px;
  color: var(--text-muted); font-size: 0.75rem; font-variant-numeric: tabular-nums;
}
.graveyard-note, .graveyard-error {
  padding: 0.75rem 1rem; border: 1px solid var(--border); border-radius: var(--radius-md);
  color: var(--text-muted); font-size: 0.8125rem;
}
.graveyard-error { border-color: rgba(255, 74, 106, 0.4); background: rgba(255, 74, 106, 0.1); color: var(--danger); }
.graveyard-empty { display: grid; place-items: center; gap: 0.5rem; min-height: 220px; color: var(--text-muted); text-align: center; }
.graveyard-list { display: flex; flex-direction: column; border-top: 1px solid var(--border); list-style: none; }
.graveyard-row {
  display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 1rem; align-items: center;
  padding: 0.875rem 0.75rem; border-bottom: 1px solid var(--border);
}
.graveyard-copy { min-width: 0; display: flex; flex-direction: column; gap: 0.2rem; }
.graveyard-copy strong { font-size: 0.9375rem; }
.graveyard-copy > span, .graveyard-copy code { overflow-wrap: anywhere; color: var(--text-muted); font-size: 0.75rem; }
.graveyard-copy code { color: var(--text-secondary); }
.graveyard-tags { display: flex; flex-wrap: wrap; gap: 0.3rem; margin-top: 0.25rem; }
.graveyard-tags span { padding: 0.15rem 0.4rem; border-radius: 999px; background: var(--bg-tertiary); color: var(--text-primary); }
.graveyard-delete:hover { border-color: var(--danger); background: var(--danger); }
.graveyard-pages { display: flex; align-items: center; justify-content: space-between; gap: 1rem; color: var(--text-muted); font-size: 0.8125rem; }
</style>
