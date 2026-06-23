/// <reference types="vite/client" />

// Injected at deploy time by the GitHub Actions workflow into dist/index.html.
// Not available at Vite build time — read at runtime only.
interface Window {
  __ENV?: { API_URL?: string; SSE_URL?: string }
}
