import { useEffect, useRef, useCallback } from 'react'
import type { SimEvent } from '../types'
import { SSE_URL } from '../config'

const MAX_BACKOFF_MS = 30_000
const BASE_BACKOFF_MS = 500

export function useSSE(
  onEvent: (ev: SimEvent) => void,
  onStatusChange: (s: 'connecting' | 'live' | 'reconnecting') => void,
) {
  const esRef = useRef<EventSource | null>(null)
  const retryRef = useRef(0)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const onEventRef = useRef(onEvent)
  const onStatusRef = useRef(onStatusChange)

  // Keep refs current without re-running the effect
  useEffect(() => { onEventRef.current = onEvent }, [onEvent])
  useEffect(() => { onStatusRef.current = onStatusChange }, [onStatusChange])

  const connect = useCallback(() => {
    if (esRef.current) {
      esRef.current.close()
      esRef.current = null
    }

    onStatusRef.current('connecting')
    const es = new EventSource(SSE_URL)
    esRef.current = es

    es.onmessage = (e: MessageEvent<string>) => {
      retryRef.current = 0
      try {
        const ev = JSON.parse(e.data) as SimEvent
        onStatusRef.current('live')
        onEventRef.current(ev)
      } catch {
        // malformed event — ignore
      }
    }

    es.onerror = () => {
      es.close()
      esRef.current = null
      onStatusRef.current('reconnecting')
      const delay = Math.min(BASE_BACKOFF_MS * 2 ** retryRef.current, MAX_BACKOFF_MS)
      retryRef.current++
      timerRef.current = setTimeout(connect, delay)
    }
  }, []) // stable — uses refs only

  useEffect(() => {
    connect()
    return () => {
      timerRef.current && clearTimeout(timerRef.current)
      esRef.current?.close()
    }
  }, [connect])
}
