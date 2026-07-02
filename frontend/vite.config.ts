import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  base: './',
  plugins: [react()],
  server: {
    host: true,
    allowedHosts: true,
    // Real backend and dev/mock-server.mjs both default to :8080 (SPEC).
    proxy: {
      '/sse': 'http://localhost:8080',
      '/api': 'http://localhost:8080',
      '/healthz': 'http://localhost:8080',
      '/readyz': 'http://localhost:8080',
    },
  },
})
