import { describe, it, expect } from 'vitest'
import { fetchPublishedRevision, waitForRegenReady } from './regenReady'

// Scripted fetch per endpoint; each sequence's last entry repeats.
interface Script {
  meta: Array<{ world_revision?: string; terrain?: string } | null> // null ⇒ 404
  snapshot?: Array<{ world_revision?: number } | null>
  terrain?: Array<{ world_revision?: number } | null>
}

function scripted(script: Script) {
  const idx = { meta: 0, snapshot: 0, terrain: 0 }
  const calls = { meta: 0, snapshot: 0, terrain: 0 }
  const pick = <T>(seq: T[] | undefined, key: 'meta' | 'snapshot' | 'terrain'): T | null => {
    calls[key]++
    if (!seq || seq.length === 0) return null
    return seq[Math.min(idx[key]++, seq.length - 1)]
  }
  const fetchFn = (url: string) => {
    let doc: unknown = null
    if (url.endsWith('/api/meta')) doc = pick(script.meta, 'meta')
    else if (url.endsWith('/api/snapshot')) doc = pick(script.snapshot, 'snapshot')
    else if (url.endsWith('/api/terrain')) doc = pick(script.terrain, 'terrain')
    return doc === null
      ? Promise.resolve({ ok: false, json: () => Promise.resolve({}) })
      : Promise.resolve({ ok: true, json: () => Promise.resolve(doc) })
  }
  return { fetchFn, calls }
}

const recordingSleep = (log: number[]) => (ms: number) => {
  log.push(ms)
  return Promise.resolve()
}

describe('fetchPublishedRevision', () => {
  it('returns the published revision', async () => {
    const { fetchFn } = scripted({ meta: [{ world_revision: '7', terrain: 'on' }] })
    expect(await fetchPublishedRevision({ fetchFn })).toBe('7')
  })

  it('returns null when meta is absent or carries no revision — the caller must NOT submit', async () => {
    const missing = scripted({ meta: [null] })
    expect(await fetchPublishedRevision({ fetchFn: missing.fetchFn })).toBeNull()

    const unpublished = scripted({ meta: [{ terrain: 'on' }] })
    expect(await fetchPublishedRevision({ fetchFn: unpublished.fetchFn })).toBeNull()

    const down = { fetchFn: () => Promise.reject(new Error('down')) }
    expect(await fetchPublishedRevision(down)).toBeNull()
  })
})

describe('waitForRegenReady (SPEC §New-map flow)', () => {
  it('an unchanged revision (rebuild running / regen aborted / restart) never reads ready; bounded budget', async () => {
    const { fetchFn, calls } = scripted({ meta: [{ world_revision: '5', terrain: 'on' }] })
    const delays: number[] = []
    const ready = await waitForRegenReady('5',
      { fetchFn, sleep: recordingSleep(delays) },
      { baseMs: 500, maxMs: 4000, timeoutMs: 10_000 })
    expect(ready).toBe(false)
    expect(delays).toEqual([500, 1000, 2000, 4000]) // next 4000 would exceed the budget → stop
    expect(calls.snapshot).toBe(0) // baselines never consulted before a NEW revision
  })

  it('ready only once the NEW revision is published AND both baselines carry it', async () => {
    // Poll 1–2: old revision (regen slower than any fixed timer). Poll 3: new
    // revision published, but the snapshot read races a further write (old tag)
    // → not ready. Poll 4: snapshot matches, terrain still old → not ready.
    // Poll 5: both match → ready.
    const { fetchFn, calls } = scripted({
      meta: [
        { world_revision: '5', terrain: 'on' },
        { world_revision: '5', terrain: 'on' },
        { world_revision: '6', terrain: 'on' },
      ],
      snapshot: [{ world_revision: 5 }, { world_revision: 6 }, { world_revision: 6 }],
      terrain: [{ world_revision: 5 }, { world_revision: 5 }, { world_revision: 6 }],
    })
    const delays: number[] = []
    const ready = await waitForRegenReady('5',
      { fetchFn, sleep: recordingSleep(delays) },
      { baseMs: 500, maxMs: 4000, timeoutMs: 60_000 })
    expect(ready).toBe(true)
    expect(calls.meta).toBe(5)
    expect(delays.reduce((a, b) => a + b, 0)).toBeGreaterThan(1500) // outlives the old fixed 1.5s
  })

  it('terrain is NOT required when the published revision says terrain off', async () => {
    const { fetchFn, calls } = scripted({
      meta: [{ world_revision: '3', terrain: 'off' }],
      snapshot: [{ world_revision: 3 }],
    })
    const ready = await waitForRegenReady('2',
      { fetchFn, sleep: () => Promise.resolve() },
      { baseMs: 1, maxMs: 1, timeoutMs: 100 })
    expect(ready).toBe(true)
    expect(calls.terrain).toBe(0) // env-off: never polled
  })

  it('terrain required by an env-on revision gates readiness until it is servable', async () => {
    const { fetchFn } = scripted({
      meta: [{ world_revision: '3', terrain: 'on' }],
      snapshot: [{ world_revision: 3 }],
      terrain: [null, null, { world_revision: 3 }], // 404s, then the tagged grid
    })
    const ready = await waitForRegenReady('2',
      { fetchFn, sleep: () => Promise.resolve() },
      { baseMs: 1, maxMs: 1, timeoutMs: 100 })
    expect(ready).toBe(true)
  })

  it('meta outages during polling are retried, not fatal', async () => {
    const { fetchFn } = scripted({
      meta: [null, null, { world_revision: '9', terrain: 'off' }],
      snapshot: [{ world_revision: 9 }],
    })
    const ready = await waitForRegenReady('8',
      { fetchFn, sleep: () => Promise.resolve() },
      { baseMs: 1, maxMs: 1, timeoutMs: 100 })
    expect(ready).toBe(true)
  })

  it('never reports ready on the OLD baseline even when it is servable', async () => {
    const { fetchFn } = scripted({
      meta: [{ world_revision: '5', terrain: 'on' }],
      snapshot: [{ world_revision: 5 }],
      terrain: [{ world_revision: 5 }],
    })
    const ready = await waitForRegenReady('5',
      { fetchFn, sleep: () => Promise.resolve() },
      { baseMs: 1, maxMs: 1, timeoutMs: 50 })
    expect(ready).toBe(false)
  })
})
