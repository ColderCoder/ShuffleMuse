<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { LogIn, LogOut, Menu, RefreshCw, Search, X } from 'lucide-vue-next'
import { useAuthStore } from './stores/auth'
import { usePlayerStore } from './stores/player'
import { useLibraryStore } from './stores/library'
import { useTagsStore } from './stores/tags'
import * as api from './api'
import SearchBar from './components/SearchBar.vue'
import NowPlayingBar from './components/NowPlayingBar.vue'
import AudioPlayer from './components/AudioPlayer.vue'
import PlaylistSidebar from './components/PlaylistSidebar.vue'
import { primaryNavigation } from './navigation'

const auth = useAuthStore()
const player = usePlayerStore()
const library = useLibraryStore()
const tagsStore = useTagsStore()
const route = useRoute()
const router = useRouter()
const menuOpen = ref(false)
const searchOpen = ref(false)
const mobileSearch = ref<InstanceType<typeof SearchBar> | null>(null)
const mainContent = ref<HTMLElement | null>(null)

function isInteractiveTarget(target: EventTarget | null): boolean {
  return target instanceof Element && target.closest(
    'input, textarea, select, button, a, dialog, [contenteditable="true"], [role="button"], [role="dialog"], [role="combobox"], [role="listbox"]',
  ) !== null
}

function handleKeydown(e: KeyboardEvent) {
  if (e.code === 'Escape') {
    menuOpen.value = false
    searchOpen.value = false
    return
  }
  if (isInteractiveTarget(e.target)) return
  switch (e.code) {
    case 'Space':
      e.preventDefault()
      player.togglePlay()
      break
    case 'KeyN':
      e.preventDefault()
      player.next()
      break
    case 'KeyP':
      e.preventDefault()
      player.previous()
      break
    case 'KeyM':
      e.preventDefault()
      player.toggleMute()
      break
  }
}

async function toggleSearch() {
  searchOpen.value = !searchOpen.value
  menuOpen.value = false
  if (searchOpen.value) {
    await nextTick()
    mobileSearch.value?.focus()
  }
}

async function handleLogout() {
  try {
	await player.releaseQueue()
  } catch {
	// Queue cleanup is best-effort during logout.
  }
  await auth.logout()
  player.reset()
  library.stop()
  tagsStore.reset()
  await router.push('/login')
}

async function handleSessionExpired() {
  const redirect = route.name === 'login' ? '/' : route.fullPath
  auth.expire()
  player.reset()
  library.stop()
  tagsStore.reset()
  await router.push({ name: 'login', query: { redirect } })
}

async function retryAuth() {
  await auth.checkAuth(true)
  if (auth.state === 'anonymous') {
    await router.push({ name: 'login', query: { redirect: route.fullPath } })
  }
}

api.setUnauthorizedHandler(handleSessionExpired)

watch(() => route.fullPath, async () => {
  menuOpen.value = false
  searchOpen.value = false
  await nextTick()
  mainContent.value?.scrollTo({ top: 0 })
  window.scrollTo({ top: 0 })
})

watch(() => auth.isLoggedIn, loggedIn => {
  if (loggedIn) void library.start()
  else library.stop()
}, { immediate: true })

watch(() => library.libraryReady, ready => {
	if (ready && auth.isLoggedIn && !player.queue && !player.playlistLoading) {
    void player.preparePlaylist()
  }
})

watch(() => library.libraryGeneration, generation => {
  if (generation > 0) player.syncLibraryGeneration(generation)
})

onMounted(async () => {
  await auth.checkAuth()
  window.addEventListener('keydown', handleKeydown)
})

onUnmounted(() => {
  library.stop()
  api.setUnauthorizedHandler(null)
  window.removeEventListener('keydown', handleKeydown)
})
</script>

<template>
  <div class="app-layout" :class="{ 'app-layout--player': auth.isLoggedIn }">
    <header class="navbar">
      <router-link to="/" class="navbar-brand">ShuffleMuse</router-link>

      <nav v-if="auth.isLoggedIn" class="desktop-nav" aria-label="Primary navigation">
        <router-link
          v-for="item in primaryNavigation"
          :key="item.to"
          :to="item.to"
          class="nav-link"
        >
          <component :is="item.icon" :size="16" aria-hidden="true" />
          <span>{{ item.label }}</span>
        </router-link>
      </nav>

      <SearchBar v-if="auth.isLoggedIn" class="desktop-search" />

      <div class="navbar-actions">
        <template v-if="auth.authRequired">
          <button v-if="auth.isLoggedIn" class="btn btn-ghost desktop-auth" @click="handleLogout">
            <LogOut :size="16" aria-hidden="true" />
            <span>Logout</span>
          </button>
          <router-link v-else to="/login" class="btn btn-ghost desktop-auth">
            <LogIn :size="16" aria-hidden="true" />
            <span>Login</span>
          </router-link>
        </template>

        <button
		  v-if="auth.isLoggedIn"
          class="btn btn-icon mobile-nav-button"
          :aria-expanded="searchOpen"
          aria-label="Search library"
          title="Search library"
          @click="toggleSearch"
        >
          <X v-if="searchOpen" :size="19" aria-hidden="true" />
          <Search v-else :size="19" aria-hidden="true" />
        </button>
        <button
		  v-if="auth.isLoggedIn"
          class="btn btn-icon mobile-nav-button"
          :aria-expanded="menuOpen"
          aria-controls="mobile-navigation"
          aria-label="Open navigation"
          title="Navigation"
          @click="menuOpen = !menuOpen; searchOpen = false"
        >
          <X v-if="menuOpen" :size="20" aria-hidden="true" />
          <Menu v-else :size="20" aria-hidden="true" />
        </button>
      </div>

      <div v-if="auth.isLoggedIn && searchOpen" class="mobile-search-panel">
        <SearchBar ref="mobileSearch" @played="searchOpen = false" />
      </div>

      <nav v-if="auth.isLoggedIn && menuOpen" id="mobile-navigation" class="mobile-menu" aria-label="Mobile navigation">
        <router-link
          v-for="item in primaryNavigation"
          :key="item.to"
          :to="item.to"
          class="mobile-menu-link"
        >
          <component :is="item.icon" :size="18" aria-hidden="true" />
          <span>{{ item.label }}</span>
        </router-link>
        <button v-if="auth.authRequired && auth.isLoggedIn" class="mobile-menu-link" @click="handleLogout">
          <LogOut :size="18" aria-hidden="true" />
          <span>Logout</span>
        </button>
        <router-link v-else-if="auth.authRequired" to="/login" class="mobile-menu-link">
          <LogIn :size="18" aria-hidden="true" />
          <span>Login</span>
        </router-link>
      </nav>
    </header>

    <div class="content-shell">
      <main ref="mainContent" class="main-content">
        <div v-if="auth.state === 'checking'" class="library-gate" role="status">
          <span class="spinner" aria-hidden="true"></span>
          <h1 class="view-title">Checking service</h1>
        </div>
        <div v-else-if="auth.state === 'unavailable'" class="library-gate" role="alert">
          <h1 class="view-title">Service unavailable</h1>
          <p class="view-subtitle">ShuffleMuse could not read the server status.</p>
          <button class="btn btn-primary" @click="retryAuth">
            <RefreshCw :size="16" aria-hidden="true" />
            Retry
          </button>
        </div>
        <div v-else-if="auth.isLoggedIn && !library.libraryReady && library.scanStatus === 'error'" class="library-gate" role="alert">
          <h1 class="view-title">Library scan failed</h1>
          <p class="view-subtitle">{{ library.scanError || 'The initial music scan could not complete.' }}</p>
          <button class="btn btn-primary" :disabled="library.loading" @click="library.requestRescan()">
            <RefreshCw :size="16" aria-hidden="true" />
            Retry scan
          </button>
        </div>
        <div v-else-if="auth.isLoggedIn && !library.libraryReady && library.scanActive" class="library-gate" role="status">
          <span class="spinner" aria-hidden="true"></span>
          <h1 class="view-title">Scanning library</h1>
          <p class="view-subtitle">Music will be available when the initial scan completes.</p>
        </div>
        <template v-else>
          <p v-if="auth.isLoggedIn && library.statusError" class="status-warning" role="status">
            {{ library.statusError }}. Showing the last successful state.
          </p>
          <router-view />
        </template>
      </main>
      <PlaylistSidebar v-if="auth.isLoggedIn && library.libraryReady" />
    </div>

    <template v-if="auth.isLoggedIn && library.libraryReady">
      <NowPlayingBar />
      <AudioPlayer />
    </template>
  </div>
</template>

<style scoped>
.library-gate {
  display: flex;
  min-height: 50vh;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  text-align: center;
}

.library-gate .btn { gap: 0.4rem; }

.status-warning {
  margin-bottom: 0.75rem;
  padding: 0.625rem 0.875rem;
  border: 1px solid rgba(255, 190, 74, 0.35);
  border-radius: var(--radius-md);
  color: #ffd28a;
  font-size: 0.8125rem;
}

.library-gate .view-title,
.library-gate .view-subtitle {
  margin: 0;
}

.spinner {
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

.desktop-nav {
  display: flex;
  align-items: center;
  gap: 0.25rem;
}

.nav-link,
.mobile-menu-link {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  color: var(--text-secondary);
  font-size: 0.8125rem;
  font-weight: 600;
}

.nav-link {
  min-height: 34px;
  padding: 0 0.625rem;
  border-radius: var(--radius-sm);
}

.nav-link:hover,
.nav-link.router-link-active {
  color: var(--text-primary);
  background: var(--bg-tertiary);
}

.desktop-search {
  margin-left: auto;
}

.desktop-auth {
  gap: 0.4rem;
  padding-inline: 0.625rem;
}

.mobile-nav-button,
.mobile-search-panel,
.mobile-menu {
  display: none;
}

@media (max-width: 820px) {
  .desktop-nav,
  .desktop-search,
  .desktop-auth {
    display: none;
  }

  .navbar {
    position: relative;
    flex-wrap: wrap;
	padding-inline: 1rem;
	height: auto;
	min-height: var(--navbar-height);
  }

  .navbar-actions {
    display: flex;
    align-items: center;
    gap: 0.375rem;
  }

  .mobile-nav-button {
    display: inline-flex;
    width: 34px;
    height: 34px;
    border-radius: var(--radius-sm);
  }

  .mobile-search-panel {
    display: block;
	position: relative;
	order: 3;
	flex: 0 0 calc(100% + 2rem);
	margin: 0 -1rem;
	padding: 0.625rem 1rem;
    background: var(--bg-secondary);
    border-bottom: 1px solid var(--border);
    z-index: 80;
  }

  .mobile-menu {
    display: flex;
    position: absolute;
    top: 100%;
    right: 0.75rem;
    width: 190px;
    padding: 0.375rem;
    flex-direction: column;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    box-shadow: var(--shadow);
    z-index: 90;
  }

  .mobile-menu-link {
    width: 100%;
    min-height: 42px;
    padding: 0 0.75rem;
    border: 0;
    border-radius: var(--radius-sm);
    background: transparent;
    cursor: pointer;
  }

  .mobile-menu-link:hover,
  .mobile-menu-link.router-link-active {
    color: var(--text-primary);
    background: var(--bg-tertiary);
  }
}
</style>
