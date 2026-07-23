<script setup lang="ts">
import { computed } from 'vue'
import { usePlayerStore } from '../stores/player'

const player = usePlayerStore()
const visibleItems = computed(() => player.sidebarItems)

function moveChunk(direction: number) {
  void player.showQueuePage(player.sidebarPage + direction)
}

function playItem(index: number) {
  void player.playAt(index)
}
</script>

<template>
  <aside class="playlist-panel" aria-label="Playlist">
    <div class="playlist-header">
      <div>
        <h2 class="playlist-title">Playlist</h2>
        <p class="playlist-meta">{{ player.queuePosition }}/{{ player.queueTotal }}</p>
      </div>
      <button
        class="btn btn-ghost btn-sm"
        :disabled="player.playlistLoading"
        @click="player.randomizePlaylist()"
      >
        {{ player.playlistLoading ? '...' : 'Randomize' }}
      </button>
    </div>

    <div v-if="player.playlistError" class="playlist-error">{{ player.playlistError }}</div>
    <div v-else-if="player.playlistLoading && !player.queue" class="playlist-empty">Loading...</div>
    <div v-else-if="visibleItems.length === 0" class="playlist-empty">No tracks</div>

    <div v-else class="playlist-list">
	  <div class="playlist-navigation" aria-label="Playlist chunks">
		<button class="btn btn-ghost btn-sm" :disabled="player.sidebarPage <= 1 || player.sidebarLoading" @click="moveChunk(-1)">Previous 200</button>
		<span>Page {{ player.sidebarPage }}/{{ player.queuePageCount }}</span>
		<button class="btn btn-ghost btn-sm" :disabled="player.sidebarPage >= player.queuePageCount || player.sidebarLoading" @click="moveChunk(1)">Next 200</button>
		<button class="btn btn-ghost btn-sm" :disabled="player.sidebarLoading" @click="player.jumpToCurrent()">Jump to current</button>
	  </div>
      <button
		v-for="item in visibleItems"
		:key="`${item.queueIndex}:${item.id}`"
		class="playlist-row"
		:class="{ 'playlist-row--active': item.queueIndex === player.activeIndex, 'playlist-row--unavailable': !item.available }"
		:disabled="!item.available"
		@click="playItem(item.queueIndex)"
      >
		<span class="playlist-index">{{ item.queueIndex + 1 }}</span>
		<span class="playlist-copy">
		  <span class="playlist-name">{{ item.name }}</span>
		  <span class="playlist-dir">{{ item.dir }}</span>
		</span>
		<span v-if="!item.available" class="playlist-state">Offline</span>
		<span v-else-if="item.queueIndex === player.activeIndex" class="playlist-state">
		  {{ player.isPlaying ? 'Playing' : 'Ready' }}
		</span>
      </button>
    </div>
  </aside>
</template>

<style scoped>
.playlist-panel {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  z-index: 110;
  width: var(--playlist-width);
  height: 100vh;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--bg-secondary);
  border-left: 1px solid var(--border);
  border-radius: 0;
  overflow: hidden;
}

.playlist-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  padding: 0.875rem 1rem;
  border-bottom: 1px solid var(--border);
}

.playlist-title {
  font-size: 0.9375rem;
  line-height: 1.2;
}

.playlist-meta {
  margin-top: 0.125rem;
  font-size: 0.75rem;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}

.btn-sm {
  padding: 0.375rem 0.625rem;
  font-size: 0.75rem;
}

.playlist-list {
  flex: 1;
  overflow-y: auto;
  padding: 0.25rem;
}

.playlist-navigation {
  position: sticky;
  top: 0;
  z-index: 2;
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  gap: 0.25rem;
  padding: 0.375rem;
  background: var(--bg-secondary);
  border-bottom: 1px solid var(--border);
}

.playlist-navigation span {
  align-self: center;
  color: var(--text-muted);
  font-size: 0.6875rem;
  text-align: center;
}

.playlist-navigation .btn:nth-of-type(3) {
  grid-column: 1 / -1;
}

.playlist-row {
  width: 100%;
  display: grid;
  grid-template-columns: 2.25rem minmax(0, 1fr) auto;
  align-items: center;
  gap: 0.5rem;
  min-height: 48px;
  padding: 0.5rem 0.625rem;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  text-align: left;
}

.playlist-row:hover {
  background: var(--bg-tertiary);
  color: var(--text-primary);
}

.playlist-row--active {
  background: rgba(74, 158, 255, 0.14);
  color: var(--text-primary);
}

.playlist-row--unavailable {
  cursor: not-allowed;
  opacity: 0.48;
}

.playlist-index {
  color: var(--text-muted);
  font-size: 0.75rem;
  font-variant-numeric: tabular-nums;
  text-align: right;
}

.playlist-copy {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 0.125rem;
}

.playlist-name,
.playlist-dir {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.playlist-name {
  font-size: 0.8125rem;
}

.playlist-dir {
  font-size: 0.6875rem;
  color: var(--text-muted);
}

.playlist-state {
  font-size: 0.625rem;
  color: var(--accent);
  text-transform: uppercase;
}

.playlist-empty,
.playlist-error,
.playlist-more {
  padding: 1rem;
  font-size: 0.8125rem;
  color: var(--text-muted);
}

.playlist-error {
  color: var(--danger);
}


@media (max-width: 960px) {
  .playlist-panel {
    position: static;
    z-index: auto;
    width: auto;
    height: 360px;
    min-height: 0;
    margin: 0 1rem 1rem;
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
  }
}
</style>
