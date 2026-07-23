<script setup lang="ts">
import axios from 'axios'
import { nextTick, onUnmounted, ref, useId } from 'vue'
import { Play, Search, X } from 'lucide-vue-next'
import type { FileEntry } from '../api'
import { usePlayerStore } from '../stores/player'
import * as api from '../api'

const pageSize = 50
const playerStore = usePlayerStore()
const emit = defineEmits<{ played: [] }>()
const input = ref<HTMLInputElement | null>(null)
const query = ref('')
const results = ref<FileEntry[]>([])
const total = ref(0)
const page = ref(1)
const activeIndex = ref(-1)
const isSearching = ref(false)
const showResults = ref(false)
const searchError = ref('')
const componentId = useId()
const listboxId = `search-results-${componentId}`
let debounceTimer: ReturnType<typeof setTimeout> | null = null
let searchRequest = 0
let searchController: AbortController | null = null

async function runSearch(searchTerm: string, pageNumber: number, append: boolean, requestID: number) {
	searchController?.abort()
	const controller = new AbortController()
	searchController = controller
  isSearching.value = true
  showResults.value = true
  searchError.value = ''
  try {
		const response = await api.search(searchTerm, pageNumber, pageSize, controller.signal)
    if (requestID !== searchRequest || query.value.trim() !== searchTerm) return
    results.value = append
      ? [...results.value, ...response.items.filter(item => !results.value.some(existing => existing.id === item.id))]
      : response.items
    total.value = response.total
    page.value = pageNumber
    activeIndex.value = results.value.length > 0 && activeIndex.value < 0 ? 0 : activeIndex.value
	} catch (caught) {
		if (axios.isCancel(caught)) return
    if (requestID !== searchRequest) return
    if (!append) results.value = []
    searchError.value = 'Search is unavailable'
	} finally {
		if (searchController === controller) searchController = null
    if (requestID === searchRequest) isSearching.value = false
  }
}

function onInput() {
	const requestID = ++searchRequest
	searchController?.abort()
	searchController = null
  if (debounceTimer) clearTimeout(debounceTimer)
  const searchTerm = query.value.trim()
  activeIndex.value = -1
  total.value = 0
  page.value = 1
  searchError.value = ''
  if (!searchTerm) {
    results.value = []
    showResults.value = false
    isSearching.value = false
    return
  }
  debounceTimer = setTimeout(() => {
    debounceTimer = null
    void runSearch(searchTerm, 1, false, requestID)
  }, 300)
}

function loadMore() {
  const searchTerm = query.value.trim()
  if (!searchTerm || isSearching.value || results.value.length >= total.value) return
  const requestID = ++searchRequest
  void runSearch(searchTerm, page.value + 1, true, requestID)
}

function clear() {
  searchRequest += 1
  if (debounceTimer) clearTimeout(debounceTimer)
	debounceTimer = null
	searchController?.abort()
	searchController = null
  query.value = ''
  results.value = []
  total.value = 0
  showResults.value = false
  isSearching.value = false
  activeIndex.value = -1
  searchError.value = ''
}

function handlePlay(file: FileEntry) {
  void playerStore.playTrack(file)
  showResults.value = false
  emit('played')
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    showResults.value = false
    activeIndex.value = -1
    return
  }
  if (!showResults.value || results.value.length === 0) return
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    activeIndex.value = Math.min(activeIndex.value + 1, results.value.length - 1)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    activeIndex.value = Math.max(activeIndex.value - 1, 0)
  } else if (event.key === 'Enter' && activeIndex.value >= 0) {
    event.preventDefault()
    handlePlay(results.value[activeIndex.value])
  }
}

async function focus() {
  await nextTick()
  input.value?.focus()
}

defineExpose({ clear, focus })

onUnmounted(() => {
  searchRequest += 1
	if (debounceTimer) clearTimeout(debounceTimer)
	searchController?.abort()
})
</script>

<template>
  <div class="search-bar">
    <label class="sr-only" :for="`${componentId}-input`">Search tracks</label>
    <div class="search-input-wrapper">
      <Search class="search-icon" :size="16" aria-hidden="true" />
      <input
        :id="`${componentId}-input`"
        ref="input"
        v-model="query"
        type="search"
        role="combobox"
        :aria-controls="listboxId"
        :aria-expanded="showResults"
        aria-autocomplete="list"
        :aria-activedescendant="activeIndex >= 0 ? `${componentId}-option-${activeIndex}` : undefined"
        placeholder="Search tracks..."
        class="search-input"
        autocomplete="off"
        @input="onInput"
        @focus="results.length > 0 && (showResults = true)"
        @keydown="handleKeydown"
      />
      <button v-if="query" class="search-clear" aria-label="Clear search" title="Clear search" @click="clear">
        <X :size="14" aria-hidden="true" />
      </button>
    </div>

    <div v-if="showResults" :id="listboxId" class="search-results" role="listbox" aria-label="Track results">
      <div v-if="isSearching && results.length === 0" class="search-loading" role="status">Searching...</div>
      <div v-else-if="searchError && results.length === 0" class="search-empty" role="alert">{{ searchError }}</div>
      <div v-else-if="results.length === 0" class="search-empty">No results found</div>
      <template v-else>
        <p class="search-summary">{{ results.length }} of {{ total }} results</p>
        <button
          v-for="(file, index) in results"
          :id="`${componentId}-option-${index}`"
          :key="file.id"
          class="search-result-row"
          :class="{ 'search-result-row--active': index === activeIndex }"
          role="option"
          :aria-selected="index === activeIndex"
          :aria-label="`Play ${file.name}, ${file.dir}`"
          @mouseenter="activeIndex = index"
          @click="handlePlay(file)"
        >
          <span class="search-result-info">
            <span class="search-result-name">{{ file.name }}</span>
            <span class="search-result-dir">{{ file.dir }}</span>
          </span>
          <Play :size="14" fill="currentColor" aria-hidden="true" />
        </button>
        <button
          v-if="results.length < total"
          class="search-more"
          :disabled="isSearching"
          @click="loadMore"
        >
          {{ isSearching ? 'Loading...' : `Load 50 more (${total - results.length} remaining)` }}
        </button>
      </template>
    </div>
  </div>
</template>

<style scoped>
.search-bar { position: relative; flex: 1; max-width: 400px; }
.search-input-wrapper { position: relative; display: flex; align-items: center; }
.search-icon { position: absolute; left: 0.625rem; pointer-events: none; opacity: 0.65; }
.search-input {
  width: 100%; padding: 0.5rem 2rem; background: var(--bg-primary); border: 1px solid var(--border);
  border-radius: var(--radius-md); color: var(--text-primary); font-size: 0.875rem; outline: none;
}
.search-input:focus { border-color: var(--accent); }
.search-input::placeholder { color: var(--text-muted); }
.search-clear {
  position: absolute; right: 0.375rem; display: grid; place-items: center; width: 24px; height: 24px;
  border: 0; border-radius: var(--radius-sm); background: transparent; color: var(--text-muted); cursor: pointer;
}
.search-clear:hover { color: var(--text-primary); background: var(--bg-tertiary); }
.search-results {
  position: absolute; top: calc(100% + 4px); left: 0; right: 0; max-height: 420px; overflow-y: auto;
  z-index: 50; background: var(--bg-secondary); border: 1px solid var(--border); border-radius: var(--radius-md);
  box-shadow: var(--shadow);
}
.search-loading, .search-empty, .search-summary {
  padding: 0.75rem; color: var(--text-muted); font-size: 0.75rem; text-align: center;
}
.search-summary { border-bottom: 1px solid var(--border); text-align: left; }
.search-result-row {
  display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: center; width: 100%; gap: 0.75rem;
  padding: 0.625rem 0.75rem; border: 0; background: transparent; color: var(--text-secondary); text-align: left;
  cursor: pointer;
}
.search-result-row:hover, .search-result-row--active { background: var(--bg-tertiary); color: var(--text-primary); }
.search-result-info { min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.search-result-name, .search-result-dir { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.search-result-name { color: var(--text-primary); font-size: 0.875rem; }
.search-result-dir { color: var(--text-muted); font-size: 0.75rem; }
.search-more {
  width: 100%; padding: 0.75rem; border: 0; border-top: 1px solid var(--border); background: transparent;
  color: var(--accent); cursor: pointer;
}
.search-more:hover { background: var(--bg-tertiary); }
@media (max-width: 820px) { .search-bar { max-width: none; } }
</style>
