import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { writeFileSync } from 'node:fs'

const preserveDistPlaceholder = {
  name: 'preserve-dist-placeholder',
  closeBundle() {
    writeFileSync(new URL('./dist/.gitkeep', import.meta.url), '')
  },
}

export default defineConfig({
  plugins: [vue(), preserveDistPlaceholder],
  build: { outDir: 'dist' },
  server: { proxy: { '/api': 'http://localhost:8080' } }
})
