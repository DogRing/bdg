import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Real backend and dev/mock-server.mjs both default to :8080 (SPEC);
// BACKEND_URL overrides when 8080 is taken (e.g. by code-server).
const backend = process.env.BACKEND_URL ?? 'http://localhost:8080'

export default defineConfig({
  base: './',
  plugins: [react()],
  server: {
    host: true,
    allowedHosts: true,
    proxy: {
      '/sse': backend,
      '/api': backend,
      '/healthz': backend,
      '/readyz': backend,
    },
  },
})
