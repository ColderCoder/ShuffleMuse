<script setup lang="ts">
import axios from 'axios'
import { nextTick, onMounted, onUnmounted, ref } from 'vue'
import { Download, X } from 'lucide-vue-next'
import type { BrowseFileEntry } from '../api'
import * as api from '../api'

const props = defineProps<{
  file: BrowseFileEntry
}>()

const emit = defineEmits<{
  close: []
}>()

const textContent = ref('')
const loading = ref(props.file.kind === 'text')
const error = ref<string | null>(null)
const contentUrl = api.browseContentUrl
const downloadUrl = api.browseDownloadUrl
const dialog = ref<HTMLElement | null>(null)
const closeButton = ref<HTMLButtonElement | null>(null)
let previouslyFocused: HTMLElement | null = null
let appRoot: HTMLElement | null = null
let previousOverflow = ''
let textController: AbortController | null = null

function close() {
  emit('close')
}

function focusableElements(): HTMLElement[] {
  if (!dialog.value) return []
  return Array.from(dialog.value.querySelectorAll<HTMLElement>(
    'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), iframe, [tabindex]:not([tabindex="-1"])',
  )).filter(element => !element.hasAttribute('hidden'))
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    close()
    return
  }
  if (event.key !== 'Tab') return
  const focusable = focusableElements()
  if (focusable.length === 0) {
    event.preventDefault()
    dialog.value?.focus()
    return
  }
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

onMounted(async () => {
  previouslyFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null
  appRoot = document.getElementById('app')
  if (appRoot) appRoot.inert = true
  window.addEventListener('keydown', handleKeydown)
  previousOverflow = document.body.style.overflow
  document.body.style.overflow = 'hidden'
  await nextTick()
  closeButton.value?.focus()
  if (props.file.kind !== 'text') return
  const controller = new AbortController()
  textController = controller
  try {
    textContent.value = await api.getBrowseText(props.file.path, controller.signal)
  } catch (requestError) {
    if (axios.isCancel(requestError)) return
    error.value = 'Unable to load preview'
  } finally {
    if (textController === controller) {
      textController = null
      loading.value = false
    }
  }
})

onUnmounted(() => {
  textController?.abort()
  textController = null
  window.removeEventListener('keydown', handleKeydown)
  document.body.style.overflow = previousOverflow
  if (appRoot) appRoot.inert = false
  previouslyFocused?.focus()
})
</script>

<template>
  <Teleport to="body">
    <div class="preview-backdrop" @click.self="close">
      <section ref="dialog" class="preview-dialog" role="dialog" aria-modal="true" :aria-label="`Preview ${file.name}`" tabindex="-1">
        <header class="preview-header">
          <div class="preview-title-wrap">
            <h2 class="preview-title">{{ file.name }}</h2>
            <span class="preview-meta">{{ file.mimeType }}</span>
          </div>
          <div class="preview-actions">
            <a
              class="btn btn-icon"
              :href="downloadUrl(file.path)"
              :aria-label="`Download ${file.name}`"
              :title="`Download ${file.name}`"
            >
              <Download :size="17" aria-hidden="true" />
            </a>
            <button ref="closeButton" class="btn btn-icon" aria-label="Close preview" title="Close" @click="close">
              <X :size="18" aria-hidden="true" />
            </button>
          </div>
        </header>

        <div class="preview-content">
          <div v-if="loading" class="preview-message">Loading preview...</div>
          <div v-else-if="error" class="preview-message preview-message--error">{{ error }}</div>
          <img
            v-else-if="file.kind === 'image'"
            class="preview-image"
            :src="contentUrl(file.path)"
            :alt="file.name"
          />
          <iframe
            v-else-if="file.kind === 'pdf'"
            class="preview-pdf"
            :src="contentUrl(file.path)"
            :title="file.name"
          ></iframe>
          <pre v-else-if="file.kind === 'text'" class="preview-text">{{ textContent }}</pre>
        </div>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.preview-backdrop {
  position: fixed;
  inset: 0;
  z-index: 220;
  display: grid;
  place-items: center;
  padding: 1rem;
  background: rgba(5, 8, 18, 0.82);
}

.preview-dialog {
  display: grid;
  grid-template-rows: auto minmax(0, 1fr);
  width: min(1100px, 100%);
  height: min(820px, calc(100vh - 2rem));
  overflow: hidden;
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow);
}

.preview-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  min-height: 58px;
  padding: 0.625rem 0.875rem;
  border-bottom: 1px solid var(--border);
}

.preview-title-wrap {
  min-width: 0;
}

.preview-title,
.preview-meta {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.preview-title {
  font-size: 0.9375rem;
}

.preview-meta {
  margin-top: 0.125rem;
  color: var(--text-muted);
  font-size: 0.6875rem;
}

.preview-actions {
  display: flex;
  flex: 0 0 auto;
  gap: 0.375rem;
}

.preview-content {
  display: grid;
  min-height: 0;
  overflow: auto;
  background: #10111a;
}

.preview-image {
  align-self: center;
  justify-self: center;
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
}

.preview-pdf {
  width: 100%;
  height: 100%;
  border: 0;
  background: #fff;
}

.preview-text {
  min-width: 100%;
  margin: 0;
  padding: 1rem;
  overflow: auto;
  color: #d8dbe8;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 0.8125rem;
  line-height: 1.55;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.preview-message {
  place-self: center;
  color: var(--text-muted);
  font-size: 0.875rem;
}

.preview-message--error {
  color: var(--danger);
}

@media (max-width: 640px) {
  .preview-backdrop {
    padding: 0;
  }

  .preview-dialog {
    width: 100%;
    height: 100%;
    border: 0;
    border-radius: 0;
  }
}
</style>
