<script setup lang="ts">
import { usePlayerStore } from '../stores/player'

const player = usePlayerStore()
</script>

<template>
  <div v-if="player.error" class="audio-error-toast">
    {{ player.error }}
  </div>
</template>

<style scoped>
.audio-error-toast {
  position: fixed;
  top: 1rem;
  right: 1rem;
  background: var(--danger);
  color: #fff;
  padding: 0.75rem 1.25rem;
  border-radius: var(--radius-md);
  font-size: 0.875rem;
  z-index: 200;
  animation: slideIn 0.2s ease;
}

@keyframes slideIn {
  from {
    transform: translateX(100%);
    opacity: 0;
  }
  to {
    transform: translateX(0);
    opacity: 1;
  }
}
</style>