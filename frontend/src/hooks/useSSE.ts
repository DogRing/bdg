import { useEffect, useRef, useCallback } from 'react'
import type { SimEvent } from '../types'
import { SSE_URL } from '../config'

const MAX_BACKOFF_MS = 30_000
const BASE_BACKOFF_MS = 500

export interface UseSSEOptions {
  // SSE connects only once the snapshot baseline is in (SPEC §Bootstrap: the
  // snapshot's stream_cursor is where replay starts — connecting earlier would
  // tail from "$" and lose the [cursor, connect) window).
  enabled: boolean
  // Called at every (re)connect to build the replay cursor: the last applied
  // stream id (falling back to the snapshot cursor). '' ⇒ no cursor (legacy/
  // mock backend ⇒ live tail). Reconnects therefore resume strictly after the
  // last frame the reducer applied — no gap, no duplicates.
  getCursor?: () => string
}

export function useSSE(
  onEvent: (ev: SimEvent, streamId: string) => void,
  onStatusChange: (s: 'connecting' | 'live' | 'reconnecting') => void,
  opts: UseSSEOptions,
) {
  const esRef = useRef<EventSource | null>(null)
  const retryRef = useRef(0)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const onEventRef = useRef(onEvent)
  const onStatusRef = useRef(onStatusChange)
  const getCursorRef = useRef(opts.getCursor)

  // Keep refs current without re-running the effect
  useEffect(() => { onEventRef.current = onEvent }, [onEvent])
  useEffect(() => { onStatusRef.current = onStatusChange }, [onStatusChange])
  useEffect(() => { getCursorRef.current = opts.getCursor }, [opts.getCursor])

  const connect = useCallback(() => {
    if (esRef.current) {
      esRef.current.close()
      esRef.current = null
    }

    onStatusRef.current('connecting')
    const cursor = getCursorRef.current?.() ?? ''
    const url = cursor !== '' ? `${SSE_URL}?cursor=${encodeURIComponent(cursor)}` : SSE_URL
    const es = new EventSource(url)
    esRef.current = es

    es.onopen = () => {
      retryRef.current = 0
      onStatusRef.current('live')
    }

    es.onmessage = (e: MessageEvent<string>) => {
      retryRef.current = 0
      try {
        const ev = JSON.parse(e.data) as SimEvent
        onStatusRef.current('live')
        // lastEventId carries the frame's Redis entry id (the server's id:
        // line); the StreamGap control frame omits it, so lastEventId stays at
        // the previous value and the reducer sees the gap by type.
        onEventRef.current(ev, e.lastEventId ?? '')
      } catch {
        // malformed event — ignore
      }
    }

    es.onerror = () => {
      // Includes the server-side close after a StreamGap control frame: the
      // reducer has asked for a fresh snapshot; this reconnect (and the next,
      // if the refetch is still in flight) re-supplies the cursor via
      // getCursor, so the loop converges once the new baseline lands.
      es.close()
      esRef.current = null
      onStatusRef.current('reconnecting')
      const delay = Math.min(BASE_BACKOFF_MS * 2 ** retryRef.current, MAX_BACKOFF_MS)
      retryRef.current++
      timerRef.current = setTimeout(connect, delay)
    }
  }, []) // stable — uses refs only

  useEffect(() => {
    if (!opts.enabled) return
    connect()
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current)
      esRef.current?.close()
      esRef.current = null
    }
  }, [opts.enabled, connect])
}
