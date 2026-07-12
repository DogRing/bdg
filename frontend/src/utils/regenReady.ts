// Completion-aware "new map" flow (frontend/SPEC.md §New-map flow; api SPEC
// POST /api/regen; data-contracts §2 world_revision): the regen 202 only means
// the request was ACCEPTED. Readiness is the PUBLISHED world_revision — the
// backend bumps it for a successful regen and publishes it to sim:{run}:meta
// only AFTER the regenerated snapshot+terrain baselines are servable
// (publish-last). Restart and failed/aborted regens never change it, and tick
// values are never consulted (tick is simulation time, not world identity).
//
// Flow: (1) read the published revision BEFORE submitting (unreadable ⇒ the
// caller reports a recoverable error and does NOT submit — readiness could not
// be verified); (2) POST /api/regen; (3) poll /api/meta with capped backoff
// until a DIFFERENT revision is published; (4) load the snapshot and — when
// that poll's meta says terrain "on" — the terrain whose embedded
// world_revision MATCHES the new revision (another regen may land between
// reads; the tags make mixing detectable); (5) only then reload. A bounded
// budget ends the wait: not-ready leaves the current view usable.

import { API_BASE } from '../config'
import type { FetchLike } from './retry'

export interface RegenReadyDeps {
  fetchFn?: FetchLike
  sleep?: (ms: number) => Promise<void>
}

export interface RegenReadyOptions {
  /** First poll delay in ms (default 500); doubles per poll. */
  baseMs?: number
  /** Poll delay cap in ms (default 4000). */
  maxMs?: number
  /** Total wait budget in ms (default 60000) — exceeded ⇒ not ready (false). */
  timeoutMs?: number
}

interface PublishedMeta {
  revision: string
  terrain: string // "on" | "off" | "" (unpublished/legacy)
}

const realSleep = (ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms))

async function readMeta(fetchFn: FetchLike): Promise<PublishedMeta | null> {
  try {
    const res = await fetchFn(`${API_BASE}/api/meta`)
    if (!res.ok) return null
    const doc = await res.json()
    if (typeof doc !== 'object' || doc === null) return null
    const m = doc as Record<string, unknown>
    const revision = typeof m.world_revision === 'string' ? m.world_revision : ''
    if (revision === '') return null // nothing published yet
    return { revision, terrain: typeof m.terrain === 'string' ? m.terrain : '' }
  } catch {
    return null
  }
}

/**
 * Reads the currently PUBLISHED world_revision, or null when it cannot be
 * verified (meta absent/unreachable/no revision field). The caller must NOT
 * submit a regen it cannot verify — report a recoverable error instead.
 */
export async function fetchPublishedRevision(deps: RegenReadyDeps = {}): Promise<string | null> {
  const fetchFn: FetchLike = deps.fetchFn ?? (url => fetch(url))
  const meta = await readMeta(fetchFn)
  return meta === null ? null : meta.revision
}

async function baselineHasRevision(fetchFn: FetchLike, path: string, revision: string): Promise<boolean> {
  try {
    const res = await fetchFn(`${API_BASE}${path}`)
    if (!res.ok) return false
    const doc = await res.json()
    if (typeof doc !== 'object' || doc === null) return false
    return String((doc as Record<string, unknown>).world_revision ?? '') === revision
  } catch {
    return false
  }
}

/**
 * Polls until a revision DIFFERENT from prevRevision is published AND the
 * snapshot (plus terrain, when that revision says terrain "on") carrying that
 * exact revision is servable. false ⇒ budget exhausted (regen still running,
 * aborted by the backend, or superseded) — the caller keeps the current view.
 */
export async function waitForRegenReady(
  prevRevision: string,
  deps: RegenReadyDeps = {},
  opts: RegenReadyOptions = {},
): Promise<boolean> {
  const fetchFn: FetchLike = deps.fetchFn ?? (url => fetch(url))
  const sleep = deps.sleep ?? realSleep
  const baseMs = opts.baseMs ?? 500
  const maxMs = opts.maxMs ?? 4_000
  const timeoutMs = opts.timeoutMs ?? 60_000

  let elapsed = 0
  for (let n = 0; ; n++) {
    const meta = await readMeta(fetchFn)
    if (meta !== null && meta.revision !== prevRevision) {
      // Published (baselines were servable at publication); verify the pair we
      // can actually fetch carries the SAME revision — a further regen between
      // reads shows up as a tag mismatch and we simply poll again.
      const snapshotOk = await baselineHasRevision(fetchFn, '/api/snapshot', meta.revision)
      const terrainOk = meta.terrain !== 'on'
        ? true
        : await baselineHasRevision(fetchFn, '/api/terrain', meta.revision)
      if (snapshotOk && terrainOk) return true
    }
    const delay = Math.min(baseMs * 2 ** n, maxMs)
    if (elapsed + delay > timeoutMs) return false
    elapsed += delay
    await sleep(delay)
  }
}
