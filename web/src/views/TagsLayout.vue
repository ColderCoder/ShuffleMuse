<script setup lang="ts">
import { ArchiveX, Tags } from 'lucide-vue-next'

const sections = [
  { to: '/tags', label: 'Tags', icon: Tags },
  { to: '/tags/graveyard', label: 'Graveyard', icon: ArchiveX },
]
</script>

<template>
  <div class="tags-workspace">
    <nav class="tags-subnav" aria-label="Tags sections">
      <router-link
        v-for="section in sections"
        :key="section.to"
        :to="section.to"
        class="tags-subnav-link"
        exact-active-class="tags-subnav-link--active"
      >
        <component :is="section.icon" :size="15" aria-hidden="true" />
        <span>{{ section.label }}</span>
      </router-link>
    </nav>

    <router-view />
  </div>
</template>

<style scoped>
.tags-workspace {
  min-width: 0;
}

.tags-subnav {
  display: inline-grid;
  grid-auto-flow: column;
  grid-auto-columns: minmax(116px, auto);
  gap: 0.25rem;
  margin-bottom: 1.25rem;
  padding: 0.25rem;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: rgba(15, 52, 96, 0.28);
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.025);
}

.tags-subnav-link {
  display: inline-flex;
  min-height: 34px;
  align-items: center;
  justify-content: center;
  gap: 0.4rem;
  padding: 0 0.75rem;
  border-radius: 5px;
  color: var(--text-muted);
  font-size: 0.8125rem;
  font-weight: 650;
  transition: background 0.16s ease, color 0.16s ease, box-shadow 0.16s ease;
}

.tags-subnav-link:hover {
  color: var(--text-primary);
  background: rgba(15, 52, 96, 0.55);
}

.tags-subnav-link--active {
  color: var(--text-primary);
  background: var(--bg-tertiary);
  box-shadow: inset 0 -2px 0 var(--accent), 0 2px 8px rgba(0, 0, 0, 0.18);
}

@media (max-width: 640px) {
  .tags-subnav {
    width: 100%;
    grid-auto-flow: row;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .tags-subnav-link {
    min-height: 40px;
  }
}
</style>
