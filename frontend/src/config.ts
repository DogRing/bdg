// Runtime URL config injected by CI: <script>window.__ENV={API_URL:'...',SSE_URL:'...'}</script>
// Falls back to same-origin relative paths for local dev (Vite proxy handles them).
declare global {
  interface Window {
    __ENV?: { API_URL?: string; SSE_URL?: string }
  }
}

export const SSE_URL = window.__ENV?.SSE_URL ?? '/sse'
export const API_BASE = window.__ENV?.API_URL ?? ''
