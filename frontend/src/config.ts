// Runtime URL config injected by CI: <script>window.__ENV={API_URL:'...',SSE_URL:'...'}</script>
// Falls back to same-origin relative paths for local dev (Vite proxy handles them).
declare global {
  interface Window {
    __ENV?: { API_URL?: string; SSE_URL?: string }
  }
}

// SSE_URL is the base origin (e.g. "https://sse.dogring.kr"); append the path here.
// Guarded so modules importing this stay loadable without a DOM (vitest/node).
const env = typeof window !== 'undefined' ? window.__ENV : undefined
export const SSE_URL = (env?.SSE_URL ?? '.') + '/sse'
export const API_BASE = env?.API_URL ?? '.'
