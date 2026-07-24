<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as api from '../api'
import { usePlayerStore } from '../stores/player'
import { useTagsStore } from '../stores/tags'

const player = usePlayerStore()
const tagsStore = useTagsStore()
const coverFailed = ref(false)
const coverSrc = ref<string>()
const coverPhase = ref<'idle' | 'directory' | 'file' | 'failed'>('idle')
const activeCoverDirectory = ref<string>()
const loadedCoverDirectory = ref<string>()
let coverTimer: ReturnType<typeof setTimeout> | undefined
let coverEpoch = 0

const browseTarget = computed(() => {
  const dir = player.currentTrack?.dir.replace(/\\/g, '/') ?? '.'
  return dir === '.' ? { name: 'browse' } : { name: 'browse', query: { dir } }
})

function normalizeDirectory(dir: string | undefined) {
  return dir?.replace(/\\/g, '/') || '.'
}

function clearCoverTimer() {
  if (coverTimer !== undefined) {
    clearTimeout(coverTimer)
    coverTimer = undefined
  }
}

function scheduleCurrentCover() {
  const track = player.currentTrack
  const directory = normalizeDirectory(track?.dir)
  const directoryURL = api.directoryCoverUrl(directory)

  if (
    track
    && loadedCoverDirectory.value === directory
    && activeCoverDirectory.value === directory
    && coverPhase.value === 'directory'
    && coverSrc.value === directoryURL
  ) {
    return
  }

  coverEpoch += 1
  const epoch = coverEpoch
  clearCoverTimer()
  // Removing src lets the browser cancel a request belonging to the previous
  // track before the new low-priority request is scheduled.
  coverSrc.value = undefined
  coverFailed.value = false
  coverPhase.value = 'idle'
  activeCoverDirectory.value = track ? directory : undefined
  if (!track) return

  const trackID = track.id
  coverTimer = setTimeout(() => {
    coverTimer = undefined
    if (
      epoch !== coverEpoch
      || player.currentTrack?.id !== trackID
      || normalizeDirectory(player.currentTrack?.dir) !== directory
    ) return
    coverPhase.value = 'directory'
    coverSrc.value = directoryURL
  }, 500)
}

function coverEventIsCurrent(event: Event) {
  const image = event.currentTarget as HTMLImageElement | null
  return image?.getAttribute('src') === coverSrc.value
}

function handleCoverLoad(event: Event) {
  if (!coverEventIsCurrent(event) || coverPhase.value !== 'directory') return
  loadedCoverDirectory.value = activeCoverDirectory.value
}

function handleCoverError(event: Event) {
  if (!coverEventIsCurrent(event)) return
  const track = player.currentTrack
  if (coverPhase.value === 'directory' && track) {
    if (loadedCoverDirectory.value === activeCoverDirectory.value) {
      loadedCoverDirectory.value = undefined
    }
    coverPhase.value = 'file'
    coverSrc.value = api.fileCoverUrl(track.id)
    return
  }
  coverPhase.value = 'failed'
  coverFailed.value = true
}

watch(
  () => [player.currentTrack?.id, player.currentTrack?.dir] as const,
  scheduleCurrentCover,
  { immediate: true },
)

onMounted(() => {
  tagsStore.fetchTags()
})

onBeforeUnmount(() => {
  coverEpoch += 1
  clearCoverTimer()
  coverSrc.value = undefined
})

</script>

<template>
  <div class="view home-view">
    <h1 class="view-title">Welcome to ShuffleMuse</h1>
    <p class="view-subtitle">Your personal music library</p>

    <!-- Empty State -->
    <div v-if="player.playlistError" class="home-empty">
      <p class="home-empty-icon">📁</p>
      <p class="home-empty-text">{{ player.playlistError }}</p>
      <p class="home-empty-hint">Add music files to your library to get started</p>
    </div>

    <!-- Loading State -->
    <div v-else-if="player.playlistLoading && !player.queue" class="home-loading">
      <span class="spinner"></span>
      <p>Scanning library...</p>
    </div>

    <!-- Main Content -->
    <div v-else class="home-actions">
      <div class="tag-filter">
        <label class="form-label" for="tag-filter">Filter by tag (optional)</label>
        <select
          id="tag-filter"
          v-model="player.selectedTag"
          class="form-input tag-select"
          @change="player.filterPlaylistByTag(player.selectedTag)"
        >
          <option value="">All tags</option>
          <option
            v-for="tag in tagsStore.tags"
            :key="tag.id"
            :value="tag.name"
          >
            {{ tag.name }}{{ tag.count !== undefined ? ` (${tag.count})` : '' }}
          </option>
        </select>
      </div>
    </div>

    <div v-if="player.currentTrack && !player.playlistLoading" class="now-playing-info">
      <p class="now-playing-label">Now Playing</p>
      <p class="now-playing-track-name">{{ player.displayTitle }}</p>
      <router-link
        class="now-playing-track-dir"
        :to="browseTarget"
        title="Browse this folder"
      >
        {{ player.currentTrack.filepath }}
      </router-link>
      <img
        v-if="!coverFailed"
        class="now-playing-cover"
        :src="coverSrc"
        :alt="`Cover art for ${player.displayTitle}`"
        decoding="async"
        fetchpriority="low"
        @load="handleCoverLoad"
        @error="handleCoverError"
      />
      <div
        v-else
        class="now-playing-cover now-playing-cover--placeholder"
        role="img"
        :aria-label="`No cover art available for ${player.displayTitle}`"
      >
        <span class="default-cover-brand" aria-hidden="true">ShuffleMuse</span>
        <span class="default-cover-record" aria-hidden="true"></span>
        <span class="default-cover-caption" aria-hidden="true">No artwork</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.home-actions {
  display: flex;
  width: 100%;
  flex-direction: column;
  align-items: center;
  margin-top: 1rem;
}

.tag-filter {
  display: flex;
  flex-direction: column;
  gap: 0.375rem;
  width: 100%;
  max-width: 280px;
}

.tag-select {
  cursor: pointer;
}

.now-playing-info {
  width: 100%;
  margin-top: 2rem;
  text-align: center;
}

.now-playing-label {
  font-size: 0.75rem;
  text-transform: uppercase;
  letter-spacing: 0.1em;
  color: var(--accent);
  margin-bottom: 0.25rem;
}

.now-playing-track-name {
  font-size: 1.25rem;
  font-weight: 600;
  color: var(--text-primary);
}

.now-playing-track-dir {
  display: inline-block;
  max-width: min(720px, 100%);
  font-size: 0.8125rem;
  color: var(--accent);
  margin-top: 0.125rem;
  opacity: 0.72;
  overflow-wrap: anywhere;
  transition: opacity 0.15s;
}

.now-playing-track-dir:hover {
  opacity: 1;
  text-decoration: underline;
}

.now-playing-cover {
  position: relative;
  display: block;
  width: min(280px, calc(100vw - 3rem));
  aspect-ratio: 1;
  margin: 1rem auto 0;
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: #10111a;
  object-fit: contain;
}

.now-playing-cover--placeholder {
  isolation: isolate;
  text-align: left;
  background:
    radial-gradient(circle at 17% 15%, rgb(74 158 255 / 24%), transparent 31%),
    linear-gradient(145deg, #17213a 0%, #0b0d17 72%);
  box-shadow: inset 0 0 0 1px rgb(255 255 255 / 3%);
}

.now-playing-cover--placeholder::before {
  position: absolute;
  inset: 0;
  z-index: -1;
  background: repeating-linear-gradient(
    135deg,
    transparent 0 22px,
    rgb(255 255 255 / 2.5%) 22px 23px
  );
  content: '';
}

.default-cover-brand,
.default-cover-caption {
  position: absolute;
  left: 1.25rem;
  z-index: 2;
  color: var(--text-primary);
  text-transform: uppercase;
}

.default-cover-brand {
  top: 1.125rem;
  font-size: 0.625rem;
  font-weight: 700;
  letter-spacing: 0.22em;
}

.default-cover-caption {
  bottom: 1.125rem;
  max-width: 5rem;
  font-size: 0.6875rem;
  font-weight: 600;
  letter-spacing: 0.14em;
  line-height: 1.4;
  color: var(--text-secondary);
}

.default-cover-record {
  position: absolute;
  right: -14%;
  bottom: -11%;
  width: 79%;
  aspect-ratio: 1;
  border: 1px solid rgb(255 255 255 / 8%);
  border-radius: 50%;
  background:
    radial-gradient(
      circle,
      #080a10 0 2.5%,
      var(--accent) 3% 10%,
      #151a2b 10.5% 18%,
      transparent 18.5%
    ),
    repeating-radial-gradient(circle, #151824 0 2px, #07080e 3px 5px);
  box-shadow: -18px -12px 36px rgb(0 0 0 / 28%);
}

.home-empty,
.home-loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.5rem;
  padding: 3rem 1rem;
  color: var(--text-muted);
}

.home-empty-icon {
  font-size: 3rem;
  margin-bottom: 0.5rem;
}

.home-empty-text {
  font-size: 1.125rem;
  font-weight: 600;
  color: var(--text-primary);
}

.home-empty-hint {
  font-size: 0.875rem;
  color: var(--text-muted);
}

.home-loading {
  padding: 2rem;
}

.home-loading p {
  margin-top: 0.5rem;
}

.spinner {
  display: inline-block;
  width: 32px;
  height: 32px;
  border: 3px solid var(--border);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}
</style>
