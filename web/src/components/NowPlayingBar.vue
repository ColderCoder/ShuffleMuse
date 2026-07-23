<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Heart, Pause, Play, SkipBack, SkipForward, Volume2, VolumeX } from 'lucide-vue-next'
import { usePlayerStore } from '../stores/player'
import { useTagsStore } from '../stores/tags'
import { useLibraryStore } from '../stores/library'
import * as api from '../api'

const player = usePlayerStore()
const tagsStore = useTagsStore()
const library = useLibraryStore()
const isFavorite = ref(false)
const favoriteLoading = ref(false)
const favoriteError = ref<string | null>(null)
const seekPreview = ref(0)
const seeking = ref(false)
let favoriteRequest = 0
let favoriteMutation = 0

const isEmpty = computed(() => !player.currentTrack)
const shownTime = computed(() => seeking.value ? seekPreview.value : player.currentTime)
const opusBitrate = computed(() => library.status?.opusBitrate ?? 160)
const playbackDetails = computed(() => {
  if (!player.currentTrack) return ''
  if (player.streamMode === 'opus') return `OPUS · ${opusBitrate.value} kbps`
  if (!player.mediaMetadata) return 'Original'
  const prefix = player.mediaMetadata.bitrateApproximate ? '~' : ''
  return `${player.mediaMetadata.codec} · ${prefix}${player.mediaMetadata.bitrateKbps} kbps`
})

watch(() => player.currentTime, value => {
  if (!seeking.value) seekPreview.value = value
})

watch(() => player.duration, value => {
  if (seekPreview.value > value && value > 0) seekPreview.value = value
})

watch(() => player.currentTrack?.id, () => {
  favoriteMutation += 1
  favoriteLoading.value = false
  favoriteError.value = null
  void checkFavorite()
}, { immediate: true })

function formatTime(seconds: number) {
  if (!Number.isFinite(seconds) || seconds < 0) return '0:00'
  const total = Math.floor(seconds)
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const remainder = total % 60
  if (hours > 0) return `${hours}:${String(minutes).padStart(2, '0')}:${String(remainder).padStart(2, '0')}`
  return `${minutes}:${String(remainder).padStart(2, '0')}`
}

function handleSeekInput(event: Event) {
  seeking.value = true
  seekPreview.value = Number((event.target as HTMLInputElement).value)
}

async function commitSeek() {
  if (!seeking.value) return
  const target = seekPreview.value
  seeking.value = false
  await player.seek(target)
}

async function checkFavorite() {
  const requestID = ++favoriteRequest
  const trackID = player.currentTrack?.id
  if (!trackID) {
    isFavorite.value = false
    return
  }
  try {
    const tags = await api.getFileTags(trackID)
    if (requestID !== favoriteRequest || player.currentTrack?.id !== trackID) return
    isFavorite.value = tags.includes('favorite')
  } catch {
    if (requestID === favoriteRequest && player.currentTrack?.id === trackID) {
      isFavorite.value = false
    }
  }
}

async function toggleFavorite() {
  if (!player.currentTrack || favoriteLoading.value) return
  const trackID = player.currentTrack.id
  const shouldFavorite = !isFavorite.value
  const mutationID = ++favoriteMutation
  favoriteRequest += 1
  favoriteLoading.value = true
  favoriteError.value = null
  try {
    if (shouldFavorite) {
      await api.addTag(trackID, 'favorite')
    } else {
      await api.removeTag(trackID, 'favorite')
    }
    if (mutationID === favoriteMutation && player.currentTrack?.id === trackID) {
      isFavorite.value = shouldFavorite
    }
    await tagsStore.fetchTags()
  } catch {
    if (mutationID === favoriteMutation && player.currentTrack?.id === trackID) {
      favoriteError.value = 'Failed to update favorite'
    }
  } finally {
    if (mutationID === favoriteMutation) favoriteLoading.value = false
  }
}
</script>

<template>
  <div class="now-playing-bar">
    <div class="np-progress-row">
      <span class="np-time">{{ formatTime(shownTime) }}</span>
      <input
        type="range"
        class="progress-slider"
        min="0"
        :max="Math.max(player.duration, 0)"
        step="0.1"
        :value="shownTime"
        :disabled="!player.currentTrack || player.duration <= 0"
        aria-label="Playback position"
        :aria-valuetext="`${formatTime(shownTime)} of ${formatTime(player.duration)}`"
        @input="handleSeekInput"
        @change="commitSeek"
      />
      <span class="np-time np-time--end">{{ formatTime(player.duration) }}</span>
    </div>

    <div v-if="isEmpty" class="np-empty">
      <span class="np-empty-text">No track playing</span>
      <span class="np-empty-hint">Choose a track or press Space</span>
    </div>
    <div v-else class="np-track-info" :class="{ 'np-track-info--error': player.error }">
      <span class="np-track-name">{{ player.currentTrack?.name }}</span>
      <span class="np-track-meta">
        <span>{{ playbackDetails }}</span>
        <span class="np-track-path">· {{ player.currentTrack?.filepath }}</span>
        <span v-if="player.isBuffering" class="np-buffering">Buffering</span>
        <span v-else-if="player.error" class="np-error-text">{{ player.error }}</span>
        <span v-else-if="favoriteError" class="np-error-text">{{ favoriteError }}</span>
      </span>
    </div>

    <div class="np-controls">
      <button
        class="btn btn-icon btn-fav"
        :class="{ 'btn-fav--active': isFavorite }"
        :disabled="!player.currentTrack || favoriteLoading"
        :aria-label="isFavorite ? 'Remove from favorites' : 'Add to favorites'"
        :title="isFavorite ? 'Remove from favorites' : 'Add to favorites'"
        @click="toggleFavorite"
      >
        <Heart :size="17" :fill="isFavorite ? 'currentColor' : 'none'" aria-hidden="true" />
      </button>
      <div class="np-transport" aria-label="Playback controls">
        <button class="btn btn-icon" aria-label="Previous track" title="Previous (P)" @click="player.previous()">
          <SkipBack :size="17" fill="currentColor" aria-hidden="true" />
        </button>
        <button
          class="btn btn-icon btn-icon--play"
          :aria-label="player.isPlaying ? 'Pause' : 'Play'"
          :title="player.isPlaying ? 'Pause (Space)' : 'Play (Space)'"
          @click="player.togglePlay()"
        >
          <Pause v-if="player.isPlaying" :size="19" fill="currentColor" aria-hidden="true" />
          <Play v-else :size="19" fill="currentColor" aria-hidden="true" />
        </button>
        <button class="btn btn-icon" aria-label="Next track" title="Next (N)" @click="player.next()">
          <SkipForward :size="17" fill="currentColor" aria-hidden="true" />
        </button>
      </div>
    </div>

    <div class="np-right">
      <div class="stream-mode" aria-label="Playback quality">
        <button
          class="stream-mode-button"
          :class="{ 'stream-mode-button--active': player.streamMode === 'original' }"
          :aria-pressed="player.streamMode === 'original'"
          title="Play the original file"
          @click="player.setStreamMode('original')"
        >
          Original
        </button>
        <button
          class="stream-mode-button"
          :class="{ 'stream-mode-button--active': player.streamMode === 'opus' }"
          :aria-pressed="player.streamMode === 'opus'"
          :title="`Stream Opus at ${opusBitrate} kbps`"
          @click="player.setStreamMode('opus')"
        >
          Opus {{ opusBitrate }}k
        </button>
      </div>
      <span class="np-queue-pos">
        {{ player.currentTrack ? `${player.queuePosition}/${player.queueTotal}` : '' }}
      </span>
      <div class="np-volume">
        <button
          class="btn btn-icon btn-mute"
          :aria-label="player.isMuted ? 'Unmute' : 'Mute'"
          :title="player.isMuted ? 'Unmute (M)' : 'Mute (M)'"
          @click="player.toggleMute()"
        >
          <VolumeX v-if="player.isMuted" :size="17" aria-hidden="true" />
          <Volume2 v-else :size="17" aria-hidden="true" />
        </button>
        <input
          type="range"
          class="volume-slider"
          min="0"
          max="1"
          step="0.01"
          :value="player.isMuted ? 0 : player.volume"
          aria-label="Volume"
          @input="player.setVolume(Number(($event.target as HTMLInputElement).value))"
        />
        <span class="np-volume-pct">{{ Math.round((player.isMuted ? 0 : player.volume) * 100) }}%</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.now-playing-bar {
  position: fixed;
  right: var(--playlist-width);
  bottom: 0;
  left: 0;
  z-index: 100;
  display: grid;
  grid-template-columns: minmax(180px, 1fr) auto minmax(260px, 1fr);
  grid-template-rows: 26px minmax(0, 1fr);
  grid-template-areas:
    "progress progress progress"
    "track controls right";
  column-gap: 1rem;
  height: var(--now-playing-height);
  padding: 4px 1.5rem 8px;
  row-gap: 0;
  background: var(--bg-secondary);
  border-top: 1px solid var(--border);
}

.np-progress-row {
  grid-area: progress;
  display: grid;
  grid-template-columns: 42px minmax(80px, 1fr) 42px;
  align-items: center;
  gap: 0.625rem;
}

.np-time {
  color: var(--text-muted);
  font-size: 0.6875rem;
  font-variant-numeric: tabular-nums;
}

.np-time--end {
  text-align: right;
}

.progress-slider,
.volume-slider {
  appearance: none;
  height: 4px;
  background: var(--border);
  border-radius: 2px;
  outline: none;
  cursor: pointer;
}

.progress-slider {
  width: 100%;
  accent-color: var(--accent);
}

.progress-slider:disabled {
  cursor: default;
  opacity: 0.45;
}

.progress-slider::-webkit-slider-thumb,
.volume-slider::-webkit-slider-thumb {
  appearance: none;
  width: 12px;
  height: 12px;
  border: 0;
  border-radius: 50%;
  background: var(--accent);
  cursor: pointer;
}

.progress-slider::-moz-range-thumb,
.volume-slider::-moz-range-thumb {
  width: 12px;
  height: 12px;
  border: 0;
  border-radius: 50%;
  background: var(--accent);
  cursor: pointer;
}

.np-track-info,
.np-empty {
  grid-area: track;
  align-self: center;
  min-width: 0;
}

.np-track-info {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.np-track-name,
.np-track-meta {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.np-track-name {
  color: var(--text-primary);
  font-size: 0.9375rem;
  font-weight: 600;
}

.np-track-meta {
  color: var(--text-muted);
  font-size: 0.72rem;
}

.np-buffering,
.np-error-text {
  margin-left: 0.5rem;
}

.np-buffering {
  color: var(--accent);
}

.np-error-text {
  color: var(--danger);
}

.np-controls {
  grid-area: controls;
  display: grid;
  grid-template-columns: 36px auto 36px;
  align-items: center;
  gap: 0.75rem;
}

.np-controls::after {
  grid-column: 3;
  width: 36px;
  content: '';
}

.np-transport {
  grid-column: 2;
  justify-self: center;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.btn-fav {
  grid-column: 1;
  color: var(--text-secondary);
}

.btn-fav--active {
  color: #f5c542;
  border-color: #f5c542;
  background: rgba(245, 197, 66, 0.1);
}

.btn-fav:disabled {
  cursor: not-allowed;
  opacity: 0.3;
}

.np-right {
  grid-area: right;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  min-width: 0;
  gap: 0.75rem;
}

.stream-mode {
  display: inline-flex;
  flex: 0 0 auto;
  padding: 2px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
}

.stream-mode-button {
  min-height: 28px;
  padding: 0 0.5rem;
  border: 0;
  border-radius: 2px;
  background: transparent;
  color: var(--text-muted);
  font-size: 0.6875rem;
  cursor: pointer;
}

.stream-mode-button:hover {
  color: var(--text-primary);
}

.stream-mode-button--active {
  background: var(--bg-tertiary);
  color: var(--text-primary);
}

.np-queue-pos {
  color: var(--text-secondary);
  font-size: 0.75rem;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.np-volume {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.volume-slider {
  width: 80px;
}

.btn-mute {
  width: 32px;
  height: 32px;
}

.np-volume-pct {
  min-width: 32px;
  color: var(--text-muted);
  font-size: 0.6875rem;
  font-variant-numeric: tabular-nums;
  text-align: right;
}

.np-empty {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.np-empty-text {
  color: var(--text-muted);
  font-size: 0.9375rem;
}

.np-empty-hint {
  color: var(--text-muted);
  font-size: 0.72rem;
  opacity: 0.75;
}

@media (max-width: 960px) {
  .now-playing-bar {
    right: 0;
  }
}

@media (max-width: 760px) {
  .now-playing-bar {
    grid-template-columns: minmax(0, 1fr) auto;
    grid-template-rows: 26px 38px 54px;
    grid-template-areas:
      "progress progress"
      "track right"
      "controls controls";
    height: var(--now-playing-height);
    padding: 4px 0.75rem 6px;
    column-gap: 0.5rem;
  }

  .np-track-path,
  .np-empty-hint,
  .np-queue-pos,
  .np-volume {
    display: none;
  }

  .np-controls {
    justify-content: center;
  }

  .np-right {
    gap: 0;
  }

  .np-controls .btn-icon {
    width: 36px;
    height: 36px;
    flex: 0 0 36px;
  }

  .np-controls .btn-icon--play {
    width: 42px;
    height: 42px;
    flex-basis: 42px;
  }
}
</style>
