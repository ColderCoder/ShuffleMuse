<script setup lang="ts">
const props = defineProps<{
  name: string
  removable?: boolean
  interactive?: boolean
}>()

const emit = defineEmits<{
  remove: []
  click: []
}>()

function activate() {
  if (props.interactive) emit('click')
}
</script>

<template>
  <span
    class="tag-pill"
    :class="{ 'tag-pill--interactive': interactive }"
    :role="interactive ? 'button' : undefined"
    :tabindex="interactive ? 0 : undefined"
    @click="activate"
    @keydown.enter.prevent="activate"
    @keydown.space.prevent="activate"
  >
    <span class="tag-pill-name">{{ name }}</span>
    <button
      v-if="removable"
      class="tag-pill-remove"
      title="Remove tag"
	  :aria-label="`Remove ${name} tag`"
      @click.stop="emit('remove')"
    >
      ×
    </button>
  </span>
</template>

<style scoped>
.tag-pill {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  padding: 0.25rem 0.625rem;
  background: var(--accent);
  color: #fff;
  border-radius: var(--radius-md);
  font-size: 0.8125rem;
  font-weight: 500;
  transition: background 0.15s, opacity 0.15s;
  user-select: none;
}

.tag-pill--interactive {
  cursor: pointer;
}

.tag-pill--interactive:hover,
.tag-pill--interactive:focus-visible {
  background: var(--accent-hover);
}

.tag-pill-name {
  white-space: nowrap;
}

.tag-pill-remove {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  padding: 0;
  border: none;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.25);
  color: #fff;
  font-size: 0.75rem;
  line-height: 1;
  cursor: pointer;
  transition: background 0.15s;
}

.tag-pill-remove:hover {
  background: var(--danger);
}
</style>
