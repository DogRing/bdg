// Capped-exponential-backoff retry for the REST baseline loaders
// (frontend/SPEC.md §Bootstrap): a transient snapshot/terrain failure must
// never permanently stall the bootstrap, and a cancelled loop (unmount,
// re-bootstrap generation bump) must never dispatch a stale response.

/** Minimal fetch surface so tests can stub responses without a Response object. */
export type FetchLike = (url: string) => Promise<{ ok: boolean; json(): Promise<unknown> }>

export interface RetryUntilOptions {
  /** First backoff delay in ms; doubles each attempt. */
  baseMs: number
  /** Backoff cap in ms — delays are bounded; attempts continue until success or cancel. */
  maxMs: number
  /** Injectable for tests; defaults to setTimeout. */
  sleep?: (ms: number) => Promise<void>
  /** Checked before/after every attempt and after every sleep; true ⇒ resolve null. */
  isCancelled?: () => boolean
}

const realSleep = (ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms))

/**
 * Repeatedly awaits `attempt` until it yields a non-null value. `attempt` must
 * not throw — map failures to null. Returns null only when cancelled (including
 * a cancellation that lands while an attempt is in flight: its late result is
 * discarded, never returned).
 */
export async function retryUntil<T>(
  attempt: () => Promise<T | null>,
  opts: RetryUntilOptions,
): Promise<T | null> {
  const sleep = opts.sleep ?? realSleep
  const cancelled = opts.isCancelled ?? (() => false)
  for (let n = 0; ; n++) {
    if (cancelled()) return null
    const result = await attempt()
    if (cancelled()) return null
    if (result !== null) return result
    await sleep(Math.min(opts.baseMs * 2 ** n, opts.maxMs))
  }
}
