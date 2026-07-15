import { describe, it, expect } from 'vitest'
import { retryUntil } from './retry'
import { fetchSnapshotWithRetry, fetchTerrainWithRetry, fetchFloraWithRetry } from '../hooks/useWorld'

const recordingSleep = (log: number[]) => (ms: number) => {
  log.push(ms)
  return Promise.resolve()
}

const okJson = (doc: unknown) => Promise.resolve({ ok: true, json: () => Promise.resolve(doc) })
const notFound = () => Promise.resolve({ ok: false, json: () => Promise.resolve({}) })

describe('retryUntil (SPEC §Bootstrap step 3)', () => {
  it('resolves after transient failures with capped exponential backoff', async () => {
    const delays: number[] = []
    let calls = 0
    const result = await retryUntil(async () => (++calls < 4 ? null : 'done'), {
      baseMs: 500, maxMs: 1500, sleep: recordingSleep(delays),
    })
    expect(result).toBe('done')
    expect(calls).toBe(4)
    expect(delays).toEqual([500, 1000, 1500]) // doubled, then capped at maxMs
  })

  it('returns immediately on first success without sleeping', async () => {
    const delays: number[] = []
    const result = await retryUntil(async () => 42, { baseMs: 500, maxMs: 500, sleep: recordingSleep(delays) })
    expect(result).toBe(42)
    expect(delays).toEqual([])
  })

  it('cancellation stops retrying and discards a late success', async () => {
    let calls = 0
    let cancelled = false
    const result = await retryUntil(
      async () => { calls++; cancelled = true; return 'late' },
      { baseMs: 1, maxMs: 1, isCancelled: () => cancelled },
    )
    expect(result).toBeNull() // success landed after cancellation ⇒ discarded
    expect(calls).toBe(1)
  })
})

describe('baseline loaders survive transient failures (SPEC §Bootstrap)', () => {
  it('snapshot: initial 404 followed by success', async () => {
    const doc = {
      tick: 7,
      agents: [{ id: 'a1', pos: { x: 1, y: 2 }, goal: 'Rest', action: '', mood: 0.5 }],
      objects: [{ id: 'o1', kind: 'berry_bush', pos: { x: 3, y: 4 } }],
    }
    const responses = [notFound(), okJson(doc)]
    const delays: number[] = []
    const payload = await fetchSnapshotWithRetry({
      fetchFn: () => responses.shift()!,
      sleep: recordingSleep(delays),
    })
    expect(payload).not.toBeNull()
    expect(payload!.tick).toBe(7)
    expect(payload!.agents[0]).toMatchObject({ id: 'a1', goal: 'Rest', pos: { x: 1, y: 2 } })
    expect(payload!.objects![0]).toMatchObject({ id: 'o1', kind: 'berry_bush' })
    expect(delays).toEqual([500]) // exactly one backoff before the retry succeeded
  })

  it('snapshot: network failure followed by success', async () => {
    let calls = 0
    const payload = await fetchSnapshotWithRetry({
      fetchFn: () => {
        calls++
        return calls === 1
          ? Promise.reject(new Error('ECONNREFUSED'))
          : okJson({ tick: 3, agents: [], objects: [] })
      },
      sleep: () => Promise.resolve(),
    })
    expect(payload).toMatchObject({ tick: 3, agents: [] })
    expect(calls).toBe(2)
  })

  it('terrain: failure followed by success', async () => {
    const grid = {
      cell_size: 8, orientation: 'flat', size: { cols: 2, rows: 2 },
      terrain: ['plain', 'plain', 'water', 'plain'],
    }
    const responses = [notFound(), okJson(grid)]
    const result = await fetchTerrainWithRetry({
      fetchFn: () => responses.shift()!,
      sleep: () => Promise.resolve(),
    })
    expect(result).toMatchObject({ cols: 2, rows: 2, cellSize: 8 })
    expect(result!.terrain[2]).toBe('water')
  })

  it('snapshot: publication wrapper is parsed (revision, cursor, terrain + flora flags)', async () => {
    const doc = {
      tick: 12, world_revision: 4, stream_cursor: '1718-3', terrain: 'on', flora: 'on',
      agents: [], objects: [],
    }
    const payload = await fetchSnapshotWithRetry({
      fetchFn: () => okJson(doc),
      sleep: () => Promise.resolve(),
    })
    expect(payload).toMatchObject({ tick: 12, revision: 4, cursor: '1718-3', terrain: 'on', flora: 'on' })
  })

  it('snapshot: a legacy blob without the wrapper degrades to null/empty/unknown', async () => {
    const payload = await fetchSnapshotWithRetry({
      fetchFn: () => okJson({ tick: 3, agents: [], objects: [] }),
      sleep: () => Promise.resolve(),
    })
    expect(payload).toMatchObject({ revision: null, cursor: '', terrain: 'unknown', flora: 'unknown' })
  })

  it('terrain: a grid from ANOTHER revision is retried until the matching one is published', async () => {
    const grid = (rev: number) => ({
      cell_size: 8, orientation: 'flat', size: { cols: 1, rows: 1 }, terrain: ['plain'],
      world_revision: rev,
    })
    const responses = [okJson(grid(4)), okJson(grid(5))] // stale revision, then the match
    const result = await fetchTerrainWithRetry({
      fetchFn: () => responses.shift() ?? okJson(grid(5)),
      sleep: () => Promise.resolve(),
      expectedRevision: 5,
    })
    expect(result).toMatchObject({ cols: 1, rows: 1 })
  })

  it('malformed terrain payload is retried like a failure', async () => {
    const good = {
      cell_size: 8, orientation: 'flat', size: { cols: 1, rows: 1 }, terrain: ['plain'],
    }
    const responses = [okJson({ cell_size: 8, size: { cols: 2, rows: 2 }, terrain: ['short'] }), okJson(good)]
    const result = await fetchTerrainWithRetry({
      fetchFn: () => responses.shift()!,
      sleep: () => Promise.resolve(),
    })
    expect(result).toMatchObject({ cols: 1, rows: 1 })
  })

  it('flora: 404 then success (FloraDoc wrapper parsed to a baseline)', async () => {
    const doc = {
      world_revision: 4,
      stream_cursor: '100-0',
      flora: [{ object_id: 'grass_1', species: 'grass', pos: { x: 12, y: 7 }, stage: 2, width: 0.35 }],
    }
    const responses = [notFound(), okJson(doc)]
    const result = await fetchFloraWithRetry({
      fetchFn: () => responses.shift()!,
      sleep: () => Promise.resolve(),
    })
    expect(result).toMatchObject({ worldRevision: 4 })
    expect(result!.flora[0]).toMatchObject({ id: 'grass_1', species: 'grass', stage: 2, width: 0.35 })
  })

  it('flora: a baseline from ANOTHER revision is retried until the matching one is published', async () => {
    const doc = (rev: number) => ({ world_revision: rev, stream_cursor: '100-0', flora: [] })
    const responses = [okJson(doc(4)), okJson(doc(5))] // stale revision, then the match
    const result = await fetchFloraWithRetry({
      fetchFn: () => responses.shift() ?? okJson(doc(5)),
      sleep: () => Promise.resolve(),
      expectedRevision: 5,
    })
    expect(result).toMatchObject({ worldRevision: 5, flora: [] })
  })

  it('flora: a baseline behind the accepted snapshot cursor is retried', async () => {
    const doc = (cursor: string) => ({ world_revision: 5, stream_cursor: cursor, flora: [] })
    const responses = [okJson(doc('100-0')), okJson(doc('120-0'))]
    const result = await fetchFloraWithRetry({
      fetchFn: () => responses.shift() ?? okJson(doc('120-0')),
      sleep: () => Promise.resolve(),
      expectedRevision: 5,
      expectedCursor: '110-0',
    })
    expect(result).toMatchObject({ worldRevision: 5, streamCursor: '120-0', flora: [] })
  })

  it('flora: a legacy bare array (no wrapper) is tolerated with revision null', async () => {
    const arr = [{ object_id: 'p1', species: 'oak', pos: { x: 0, y: 0 }, stage: 1, width: 1 }]
    const result = await fetchFloraWithRetry({
      fetchFn: () => okJson(arr),
      sleep: () => Promise.resolve(),
    })
    expect(result).toMatchObject({ worldRevision: null })
    expect(result!.flora[0]).toMatchObject({ id: 'p1', species: 'oak' })
  })

  it('a cancelled loader never returns a stale response', async () => {
    let cancelled = false
    const payload = await fetchSnapshotWithRetry({
      fetchFn: () => { cancelled = true; return okJson({ tick: 1, agents: [], objects: [] }) },
      isCancelled: () => cancelled,
    })
    expect(payload).toBeNull()
  })
})
