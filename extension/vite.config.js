import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { fileURLToPath } from 'node:url'

export default defineConfig({
  plugins: [react()], base: './',
  build: { rollupOptions: {
    input: {
      popup: fileURLToPath(new URL('./index.html', import.meta.url)),
      background: fileURLToPath(new URL('./src/background.js', import.meta.url)),
      offscreen: fileURLToPath(new URL('./offscreen.html', import.meta.url)),
    },
    output: { entryFileNames: chunk => chunk.name === 'background' ? 'background.js' : 'assets/[name]-[hash].js' },
  } },
  server: { proxy: { '/api': 'http://localhost:8989', '/health': 'http://localhost:8989' } },
})
