<script setup lang="ts">
import { ref } from 'vue'
import TagPill from './TagPill.vue'
import * as api from '../api'

const props = defineProps<{
  fileId: string
  currentTags: { id: string; name: string }[]
}>()

const emit = defineEmits<{
  'tag-added': [tag: { id: string; name: string }]
  'tag-removed': [tagId: string]
}>()

const newTagName = ref('')
const error = ref<string | null>(null)
const adding = ref(false)

const TAG_PATTERN = /^[a-zA-Z0-9_.-]+$/
const MAX_TAG_LENGTH = 50

function validate(name: string): string | null {
  const trimmed = name.trim()
  if (!trimmed) return 'Tag name cannot be empty'
  if (trimmed.length > MAX_TAG_LENGTH) return `Tag must be ${MAX_TAG_LENGTH} characters or less`
  if (!TAG_PATTERN.test(trimmed)) return 'Tag can only contain letters, numbers, dots, hyphens, and underscores'
  if (props.currentTags.some(t => t.name.toLowerCase() === trimmed.toLowerCase())) {
    return 'Tag already exists'
  }
  return null
}

async function addTag() {
  const name = newTagName.value.trim()
  const validationError = validate(name)
  if (validationError) {
    error.value = validationError
    return
  }

  adding.value = true
  error.value = null
  try {
    await api.addTag(props.fileId, name)
    emit('tag-added', { id: name, name })
    newTagName.value = ''
  } catch {
    error.value = 'Failed to add tag'
  } finally {
    adding.value = false
  }
}

async function removeTag(tagId: string) {
  try {
    await api.removeTag(props.fileId, tagId)
    emit('tag-removed', tagId)
  } catch {
    error.value = 'Failed to remove tag'
  }
}
</script>

<template>
  <div class="tag-manager">
    <div v-if="currentTags.length" class="tag-manager-pills">
      <TagPill
        v-for="tag in currentTags"
        :key="tag.id"
        :name="tag.name"
        :removable="true"
        @remove="removeTag(tag.id)"
      />
    </div>
    <div v-if="error" class="form-error">{{ error }}</div>
    <div class="tag-manager-input">
      <input
        v-model="newTagName"
        class="form-input tag-input"
        type="text"
        placeholder="Add a tag..."
        maxlength="50"
        @keydown.enter="addTag"
      />
      <button
        class="btn btn-primary tag-add-btn"
        :disabled="adding || !newTagName.trim()"
        @click="addTag"
      >
        {{ adding ? '...' : 'Add' }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.tag-manager {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.tag-manager-pills {
  display: flex;
  flex-wrap: wrap;
  gap: 0.375rem;
}

.tag-manager-input {
  display: flex;
  gap: 0.5rem;
}

.tag-input {
  flex: 1;
  min-width: 0;
}

.tag-add-btn {
  white-space: nowrap;
}

.tag-add-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
