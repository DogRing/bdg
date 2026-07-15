import { describe, it, expect } from 'vitest'
import { worldReducer, initialWorldState } from './useWorld'
import type { SimEvent, WorldState, TerrainGrid } from '../types'

const ev = (type: string, payload: Record<string, unknown>, tick = 1): SimEvent =>
  ({ schema_version: 1, tick, seq: 0, agent_id: null, type, payload })

const dispatch = (state: WorldState, e: SimEvent, atMs: number, streamId = ''): WorldState =>
  worldReducer(state, { type: 'EVENT', payload: e, atMs, streamId })

const agent = (id: string, over: Record<string, unknown> = {}) =>
  ({ id, pos: { x: 0, y: 0 }, goal: '', action: '', mood: 0.5, cluster: null, copingMode: null, ...over })

const frame = (animals: Array<Record<string, unknown>>, flora: Array<Record<string, unknown>> = []) =>
  ev('WorldFrame', { animals, flora_delta: flora, wind: { dir: 0, mag: 0 } })

const ready = (): WorldState =>
  worldReducer(initialWorldState, { type: 'SNAPSHOT_LOADED', payload: { agents: [], tick: 0 } })

// A world PAST the flora baseline (floraLoaded), so flora_delta applies live
// instead of buffering into pendingFloraOps (fix #1).
const liveFlora = (): WorldState =>
  worldReducer(ready(), { type: 'FLORA_LOADED', payload: { worldRevision: null, streamCursor: '', flora: [] } })

const deer = (over: Record<string, unknown> = {}) =>
  ({ id: 'd1', pos: { x: 0, y: 0 }, species: 'deer', action: 'graze', heading: 0, ...over })

describe('WorldFrame interpolation stamps (Q3)', () => {
  it('first frame has no stamps; the second shifts pos→prevPos and stamps the window', () => {
    let s = dispatch(ready(), frame([deer()]), 1000)
    expect(s.animals.get('d1')!.prevPos).toBeUndefined()

    s = dispatch(s, frame([deer({ pos: { x: 10, y: 0 }, heading: 1 })]), 1400)
    const a = s.animals.get('d1')!
    expect(a.prevPos).toEqual({ x: 0, y: 0 })
    expect(a.prevHeading).toBe(0)
    expect(a.prevFrameAtMs).toBe(1400)
    expect(a.frameAtMs).toBe(1400 + 500) // no measured gap yet → default window

    s = dispatch(s, frame([deer({ pos: { x: 20, y: 0 } })]), 1800)
    const b = s.animals.get('d1')!
    expect(b.prevPos).toEqual({ x: 10, y: 0 })
    expect(b.frameAtMs).toBe(1800 + 400) // measured gap 1800-1400
  })
})

describe('fx queue (Q4)', () => {
  it('attack-pose entry enqueues ONE attack fx (not re-armed while it stays attack)', () => {
    let s = dispatch(ready(), frame([deer({ id: 'w1', species: 'wolf', action: 'wander' })]), 0)
    s = dispatch(s, frame([deer({ id: 'w1', species: 'wolf', action: 'hunt_deer' })]), 500)
    s = dispatch(s, frame([deer({ id: 'w1', species: 'wolf', action: 'hunt_deer' })]), 600)
    expect(s.fx.filter(f => f.kind === 'attack' && f.id === 'w1')).toHaveLength(1)
  })

  it('AnimalDied removes the animal and enqueues a death fx with its last pos/heading', () => {
    let s = dispatch(ready(), frame([deer({ pos: { x: 7, y: 8 }, heading: 2 })]), 0)
    s = dispatch(s, ev('AnimalDied', { object_id: 'd1', cause: 'hunted' }), 100)
    expect(s.animals.has('d1')).toBe(false)
    const death = s.fx.find(f => f.kind === 'death')!
    expect(death).toMatchObject({ id: 'd1', species: 'deer', heading: 2, pos: { x: 7, y: 8 }, at: 100 })
  })

  it('AnimalBorn adds the animal; PlantSpawned is FX-only (state via flora_delta); PlantDied removes + death fx', () => {
    let s = dispatch(liveFlora(),
      ev('AnimalBorn', { object_id: 'd2', species: 'deer', pos: { x: 1, y: 2 } }), 0)
    expect(s.animals.get('d2')).toMatchObject({ species: 'deer', pos: { x: 1, y: 2 } })

    // PlantSpawned enqueues the spawn puff but does NOT write flora state: the
    // authoritative flora_delta upsert owns state (and carries width, which
    // PlantSpawned's payload lacks).
    s = dispatch(s, ev('PlantSpawned', { object_id: 'p1', species: 'tree', pos: { x: 3, y: 4 } }), 10)
    expect(s.flora.find(f => f.id === 'p1')).toBeUndefined()
    expect(s.fx.filter(f => f.kind === 'spawn')).toHaveLength(2)

    // The paired WorldFrame flora_delta (same tick) creates the plant with the
    // full render row (species + width present).
    s = dispatch(s, frame([], [{ id: 'p1', species: 'tree', pos: { x: 3, y: 4 }, stage: 0, width: 0.2 }]), 15)
    expect(s.flora.find(f => f.id === 'p1')).toMatchObject({ species: 'tree', width: 0.2 })

    s = dispatch(s, ev('PlantDied', { object_id: 'p1' }), 20)
    expect(s.flora.find(f => f.id === 'p1')).toBeUndefined()
    expect(s.fx.find(f => f.kind === 'death' && f.id === 'p1')).toMatchObject({ species: 'tree' })
  })

  it('a flora stage increase enqueues a grow fx', () => {
    // Establish the plant via its first flora_delta (stage 1)…
    let s = dispatch(liveFlora(),
      frame([], [{ id: 'p1', species: 'tree', pos: { x: 0, y: 0 }, stage: 1, width: 0.3 }]), 0)
    expect(s.flora.find(f => f.id === 'p1')).toMatchObject({ stage: 1 })
    // …then a later delta with a higher stage → grow fx.
    s = dispatch(s, frame([], [{ id: 'p1', species: 'tree', pos: { x: 0, y: 0 }, stage: 2, width: 0.4 }]), 100)
    expect(s.fx.find(f => f.kind === 'grow')).toMatchObject({ id: 'p1', at: 100 })
    // same stage again → no second grow
    s = dispatch(s, frame([], [{ id: 'p1', species: 'tree', pos: { x: 0, y: 0 }, stage: 2, width: 0.4 }]), 200)
    expect(s.fx.filter(f => f.kind === 'grow')).toHaveLength(1)
  })

  it('terrain deltas mutate cells + wear and replace the grid object; deltas without a base grid drop', () => {
    const grid: TerrainGrid = { cellSize: 8, cols: 2, rows: 2, orientation: 'flat', terrain: ['plain', 'plain', 'plain', 'plain'] }
    let s = worldReducer(ready(), { type: 'TERRAIN_LOADED', payload: grid })
    expect(s.terrain).toBe(grid)

    s = dispatch(s, ev('WorldFrame', {
      animals: [], flora_delta: [], wind: { dir: 0, mag: 0 },
      terrain_delta: [
        { cell: 1, terrain: 'water' },   // offset index i=row·cols+col
        { cell: 2, wear: 0.5 },
        { cell: 99, terrain: 'water' },  // out of bounds → ignored
      ],
    }), 100)
    expect(s.terrain).not.toBe(grid)             // identity replaced → raster rebuild
    expect(s.terrain!.terrain[1]).toBe('water')
    expect(s.terrain!.wear![2]).toBeCloseTo(0.5)
    expect(grid.terrain[1]).toBe('plain')        // base grid untouched (no mutation)

    const queued = dispatch(ready(), ev('WorldFrame', {
      animals: [], flora_delta: [], wind: { dir: 0, mag: 0 },
      terrain_delta: [{ cell: 0, terrain: 'water' }],
    }), 0)
    expect(queued.terrain).toBeNull()
    expect(queued.pendingTerrainDeltas).toHaveLength(1)

    const loaded = worldReducer(queued, { type: 'TERRAIN_LOADED', payload: grid })
    expect(loaded.terrain!.terrain[0]).toBe('water')
    expect(loaded.pendingTerrainDeltas).toHaveLength(0)
  })

  it('expired fx are pruned on a later reduce', () => {
    let s = dispatch(ready(), ev('AnimalDied', { object_id: 'x', species: 'deer', pos: { x: 0, y: 0 } }), 0)
    expect(s.fx).toHaveLength(1)
    s = dispatch(s, frame([deer()]), 1400)   // death still inside 1500ms
    expect(s.fx).toHaveLength(1)
    s = dispatch(s, frame([deer()]), 1600)   // past it → pruned
    expect(s.fx).toHaveLength(0)
  })

  it('AgentFrame merges sparse agent deltas', () => {
    let s = worldReducer(initialWorldState, {
      type: 'SNAPSHOT_LOADED',
      payload: { tick: 4, agents: [{ id: 'a1', pos: { x: 0, y: 0 }, goal: 'Rest', action: '', mood: 0.5, cluster: null, copingMode: null }] },
    })
    s = dispatch(s, ev('AgentFrame', { agents: [{ id: 'a1', pos: { x: 2, y: 3 }, action: 'MoveTo' }], removed: [] }, 5), 100)
    expect(s.agents.get('a1')).toMatchObject({ pos: { x: 2, y: 3 }, goal: 'Rest', action: 'MoveTo', mood: 0.5 })
  })

})

describe('stream cursor, authoritative roster, revision (SPEC §Bootstrap)', () => {
  const baseline = (): WorldState => worldReducer(initialWorldState, {
    type: 'SNAPSHOT_LOADED',
    payload: { tick: 10, agents: [agent('a1')], revision: 1, cursor: '100-0', terrain: 'off' },
  })

  it('events arriving before any snapshot baseline are dropped (SSE is gated)', () => {
    const s = dispatch(initialWorldState, ev('AgentFrame', {
      agents: [{ id: 'a1', pos: { x: 7, y: 8 } }], removed: [],
    }, 5), 100, '90-0')
    expect(s.agents.size).toBe(0)
    expect(s.snapshotLoaded).toBe(false)
  })

  it('identified frames apply exactly once, in cursor order', () => {
    let s = baseline()
    expect(s.lastAppliedStreamId).toBe('100-0')

    // At-cursor entry: already baked into the snapshot ⇒ ignored.
    s = dispatch(s, ev('AgentFrame', { agents: [{ id: 'a1', pos: { x: 5, y: 5 } }], removed: [] }, 10), 0, '100-0')
    expect(s.agents.get('a1')!.pos).toEqual({ x: 0, y: 0 })

    // Replayed post-cursor entry (emitted BEFORE the client connected —
    // recovered through server-side replay): applies.
    s = dispatch(s, ev('AgentFrame', { agents: [{ id: 'a1', pos: { x: 7, y: 8 }, goal: 'Satiety' }], removed: [] }, 11), 10, '101-0')
    expect(s.agents.get('a1')).toMatchObject({ pos: { x: 7, y: 8 }, goal: 'Satiety' })
    expect(s.lastAppliedStreamId).toBe('101-0')

    // Duplicate stream id ⇒ ignored.
    s = dispatch(s, ev('AgentFrame', { agents: [{ id: 'a1', pos: { x: 9, y: 9 } }], removed: [] }, 11), 20, '101-0')
    expect(s.agents.get('a1')!.pos).toEqual({ x: 7, y: 8 })

    // Older id (pre-cursor straggler / old-revision leftover) ⇒ ignored.
    s = dispatch(s, ev('AgentFrame', { agents: [{ id: 'a1', pos: { x: 1, y: 1 } }], removed: [] }, 9), 30, '100-5')
    expect(s.agents.get('a1')!.pos).toEqual({ x: 7, y: 8 })
    expect(s.lastAppliedStreamId).toBe('101-0')
  })

  it('an agent removed after the snapshot cursor is removed by the replayed frame', () => {
    let s = baseline()
    s = dispatch(s, ev('AgentFrame', { agents: [], removed: ['a1'] }, 11), 0, '101-0')
    expect(s.agents.has('a1')).toBe(false)
  })

  it('the snapshot roster is authoritative: ghosts do not survive a refresh', () => {
    let s = baseline()
    // A second agent appears via SSE, then dies while we are disconnected; the
    // reacquired snapshot no longer contains it.
    s = dispatch(s, ev('AgentFrame', { agents: [agent('ghost', { pos: { x: 3, y: 3 } })], removed: [] }, 11), 0, '101-0')
    expect(s.agents.has('ghost')).toBe(true)

    const refreshed = worldReducer(s, {
      type: 'SNAPSHOT_LOADED',
      payload: { tick: 30, agents: [agent('a1', { goal: 'Rest' })], revision: 1, cursor: '200-0', terrain: 'off' },
    })
    expect(refreshed.agents.has('ghost')).toBe(false) // authoritative roster
    expect(refreshed.agents.get('a1')).toMatchObject({ goal: 'Rest' })
    expect(refreshed.lastAppliedStreamId).toBe('200-0') // replay resumes after the new cursor

    // A post-refresh delta still applies (roster replacement then replay).
    const after = dispatch(refreshed, ev('AgentFrame', {
      agents: [{ id: 'a1', pos: { x: 4, y: 4 } }], removed: [],
    }, 31), 0, '201-0')
    expect(after.agents.get('a1')!.pos).toEqual({ x: 4, y: 4 })
  })

  it('a snapshot older than already-applied entries is rejected for reacquisition', () => {
    let s = baseline()
    s = dispatch(s, ev('AgentFrame', { agents: [{ id: 'a1', pos: { x: 7, y: 8 } }], removed: [] }, 11), 0, '150-0')

    const rejected = worldReducer(s, {
      type: 'SNAPSHOT_LOADED',
      payload: { tick: 9, agents: [agent('a1')], revision: 1, cursor: '120-0', terrain: 'off' },
    })
    expect(rejected.baselineRetries).toBe(1)
    expect(rejected.agents.get('a1')!.pos).toEqual({ x: 7, y: 8 }) // nothing rolled back
    expect(rejected.lastAppliedStreamId).toBe('150-0')
  })

  it('a revision switch clears old-world slices and accepts a lower cursor/tick', () => {
    let s = baseline()
    s = dispatch(s, ev('AgentFrame', { agents: [{ id: 'a1', pos: { x: 7, y: 8 } }], removed: [] }, 11), 0, '150-0')
    s = dispatch(s, ev('AnimalBorn', { object_id: 'd1', species: 'deer', pos: { x: 1, y: 2 } }, 12), 5, '151-0')
    expect(s.animals.has('d1')).toBe(true)
    expect(s.fx.length).toBeGreaterThan(0)

    const switched = worldReducer(s, {
      type: 'SNAPSHOT_LOADED',
      payload: { tick: 1, agents: [agent('b1')], revision: 2, cursor: '50-0', terrain: 'off' },
    })
    expect(switched.worldRevision).toBe(2)
    expect(switched.tick).toBe(1)                       // tick rewinds across revisions
    expect(switched.agents.has('a1')).toBe(false)       // old roster gone
    expect(switched.agents.has('b1')).toBe(true)
    expect(switched.animals.size).toBe(0)               // env slices cleared
    expect(switched.flora).toHaveLength(0)
    expect(switched.fx).toHaveLength(0)
    expect(switched.terrain).toBeNull()
    expect(switched.pendingTerrainDeltas).toHaveLength(0)
    expect(switched.lastAppliedStreamId).toBe('50-0')   // lower cursor accepted

    // Newer ids can only come from the new stream (regen deleted the old
    // entries, and their ids are all below the new cursor anyway); a replayed
    // new-world frame applies normally.
    const next = dispatch(switched, ev('AgentFrame', {
      agents: [{ id: 'b1', pos: { x: 2, y: 2 } }], removed: [],
    }, 2), 0, '51-0')
    expect(next.agents.get('b1')!.pos).toEqual({ x: 2, y: 2 })
  })

  it('StreamGap closes SSE until the fresh baseline is accepted', () => {
    const s = baseline()
    const gapped = dispatch(s, ev('StreamGap', { reason: 'cursor_trimmed' }, 0), 0, '')
    expect(gapped.baselineRetries).toBe(1)
    expect(gapped.agents.size).toBe(s.agents.size)
    expect(gapped.snapshotLoaded).toBe(false)
    expect(gapped.lastAppliedStreamId).toBe('100-0') // no id line on the control frame
  })

  it('terrain "off" clears the grid and queued deltas; a later "on" revision re-arms', () => {
    // env-on world with a loaded grid and a queued delta...
    const grid: TerrainGrid = { cellSize: 8, cols: 1, rows: 1, orientation: 'flat', terrain: ['plain'] }
    let s = worldReducer(initialWorldState, {
      type: 'SNAPSHOT_LOADED',
      payload: { tick: 5, agents: [], revision: 3, cursor: '10-0', terrain: 'on' },
    })
    s = worldReducer(s, { type: 'TERRAIN_LOADED', payload: grid })
    expect(s.terrain).not.toBeNull()
    expect(s.terrainStatus).toBe('on')

    // ...switches to an env-off revision: grid + deltas drop, status flips.
    const off = worldReducer(s, {
      type: 'SNAPSHOT_LOADED',
      payload: { tick: 1, agents: [], revision: 4, cursor: '20-0', terrain: 'off' },
    })
    expect(off.terrainStatus).toBe('off')
    expect(off.terrain).toBeNull()
    expect(off.pendingTerrainDeltas).toHaveLength(0)
  })

  it('a legacy snapshot (no wrapper) keeps the mock/live-tail behaviour', () => {
    const s = worldReducer(initialWorldState, {
      type: 'SNAPSHOT_LOADED',
      payload: { tick: 5, agents: [agent('a1')] },
    })
    expect(s.worldRevision).toBeNull()
    expect(s.snapshotCursor).toBe('')
    expect(s.terrainStatus).toBe('unknown')
    // Unidentified frames apply unconditionally (legacy transport).
    const next = dispatch(s, ev('AgentFrame', { agents: [{ id: 'a1', pos: { x: 7, y: 8 } }], removed: [] }, 6), 0, '')
    expect(next.agents.get('a1')!.pos).toEqual({ x: 7, y: 8 })
  })
})

describe('RenderConfig derivation from terrain (camera anchor)', () => {
  it('TERRAIN_LOADED derives render.bounds from the hex grid geometry', () => {
    // flat-top hex offset grid: cols apart 1.5·cellSize in x, rows apart √3·cellSize in y.
    const grid: TerrainGrid = { cellSize: 12, cols: 12, rows: 11, orientation: 'flat', terrain: Array(132).fill('soil') }
    const s = worldReducer(initialWorldState, { type: 'TERRAIN_LOADED', payload: grid })
    expect(s.render).not.toBeNull()
    expect(s.render!.bounds.min).toEqual({ x: 0, y: 0 })
    expect(s.render!.bounds.max.x).toBeCloseTo(12 * 1.5 * 12)          // 216
    expect(s.render!.bounds.max.y).toBeCloseTo(11 * Math.sqrt(3) * 12) // ≈228.6
  })

  it('TERRAIN_LOADED never clobbers an already-present RenderConfig', () => {
    const grid: TerrainGrid = { cellSize: 12, cols: 12, rows: 11, orientation: 'flat', terrain: Array(132).fill('soil') }
    const preset = { bounds: { min: { x: -5, y: -5 }, max: { x: 5, y: 5 } }, pixelsPerUnit: 40 }
    const s0: WorldState = { ...initialWorldState, render: preset }
    const s = worldReducer(s0, { type: 'TERRAIN_LOADED', payload: grid })
    expect(s.render).toBe(preset)
  })
})

describe('flora baseline (GET /api/flora; SPEC §Bootstrap)', () => {
  const onRev = (rev = 1, cursor = '100-0'): WorldState => worldReducer(initialWorldState, {
    type: 'SNAPSHOT_LOADED',
    payload: { tick: 10, agents: [agent('a1')], revision: rev, cursor, terrain: 'on', flora: 'on' },
  })

  it('FLORA_LOADED replaces flora authoritatively and marks floraLoaded', () => {
    let s = onRev()
    // A ghost plant somehow present before the baseline (e.g. a stale slice)...
    s = { ...s, flora: [{ id: 'ghost', pos: { x: 9, y: 9 }, species: 'weed', stage: 3, width: 2 }] }
    const loaded = worldReducer(s, {
      type: 'FLORA_LOADED',
      payload: { worldRevision: 1, streamCursor: '100-0', flora: [
        { id: 'grass_1', pos: { x: 1, y: 1 }, species: 'grass', stage: 2, width: 0.35 },
      ] },
    })
    expect(loaded.floraLoaded).toBe(true)
    expect(loaded.flora).toHaveLength(1)                    // authoritative REPLACE, not merge
    expect(loaded.flora.find(f => f.id === 'ghost')).toBeUndefined()
    expect(loaded.flora[0]).toMatchObject({ id: 'grass_1', species: 'grass', width: 0.35 })
  })

  it('a baseline tagged with a different revision is ignored (regen raced the fetch)', () => {
    const s = onRev(2)
    const ignored = worldReducer(s, {
      type: 'FLORA_LOADED',
      payload: { worldRevision: 1, streamCursor: '100-0', flora: [{ id: 'stale', pos: { x: 0, y: 0 }, species: 'oak', stage: 1, width: 1 }] },
    })
    expect(ignored.floraLoaded).toBe(false)
    expect(ignored.flora).toHaveLength(0)
  })

  it('fix #1: a flora_delta arriving BEFORE the baseline is buffered, then survives the authoritative replace', () => {
    // SSE beats /api/flora: a post-cursor spawn (full row) lands first. It must
    // NOT be clobbered when the (older, as-of-cursor) baseline replaces flora.
    let s = onRev()
    s = dispatch(s, frame([], [{ id: 'p9', species: 'oak', pos: { x: 4, y: 5 }, stage: 1, width: 0.8 }]), 50, '101-0')
    expect(s.floraLoaded).toBe(false)
    expect(s.flora.find(f => f.id === 'p9')).toBeUndefined()   // buffered, not live yet
    expect(s.pendingFloraOps).toHaveLength(1)

    const loaded = worldReducer(s, {
      type: 'FLORA_LOADED',
      payload: { worldRevision: 1, streamCursor: '100-0', flora: [{ id: 'fixture', pos: { x: 0, y: 0 }, species: 'grass', stage: 2, width: 0.3 }] },
    })
    // Baseline fixture AND the buffered post-cursor spawn both present; buffer cleared.
    expect(loaded.flora.find(f => f.id === 'fixture')).toBeTruthy()
    expect(loaded.flora.find(f => f.id === 'p9')).toMatchObject({ species: 'oak', width: 0.8 })
    expect(loaded.pendingFloraOps).toHaveLength(0)
  })

  it('fix #1: a PlantDied arriving BEFORE the baseline is NOT resurrected by it', () => {
    // The dreaded ghost: PlantDied applies to empty flora (no-op), then the
    // baseline still lists the plant — the buffered remove must win.
    let s = onRev()
    s = dispatch(s, ev('PlantDied', { object_id: 'doomed', species: 'oak', pos: { x: 1, y: 1 } }), 50, '101-0')
    expect(s.pendingFloraOps).toEqual([{ op: 'remove', id: 'doomed', streamId: '101-0' }])
    expect(s.fx.find(f => f.kind === 'death' && f.id === 'doomed')).toBeTruthy()   // FX fires immediately

    const loaded = worldReducer(s, {
      type: 'FLORA_LOADED',
      payload: { worldRevision: 1, streamCursor: '100-0', flora: [{ id: 'doomed', pos: { x: 1, y: 1 }, species: 'oak', stage: 2, width: 1 }] },
    })
    expect(loaded.flora.find(f => f.id === 'doomed')).toBeUndefined()   // stays dead — no resurrection
  })

  it('a revision switch re-arms the loader (floraLoaded false) and drops ghost plants', () => {
    let s = onRev(1, '100-0')
    s = worldReducer(s, {
      type: 'FLORA_LOADED',
      payload: { worldRevision: 1, streamCursor: '100-0', flora: [{ id: 'oldworld', pos: { x: 2, y: 2 }, species: 'grass', stage: 1, width: 0.2 }] },
    })
    expect(s.floraLoaded).toBe(true)

    const switched = worldReducer(s, {
      type: 'SNAPSHOT_LOADED',
      payload: { tick: 1, agents: [agent('b1')], revision: 2, cursor: '50-0', terrain: 'on' },
    })
    expect(switched.floraLoaded).toBe(false)               // loader re-fetches for the new world
    expect(switched.flora).toHaveLength(0)                  // no ghost from the old revision
    expect(switched.pendingFloraOps).toHaveLength(0)
  })

  it('fix #2: a StreamGap re-arms the flora baseline (floraLoaded false) so a trim re-converges', () => {
    let s = onRev(1, '100-0')
    s = worldReducer(s, {
      type: 'FLORA_LOADED',
      payload: { worldRevision: 1, streamCursor: '100-0', flora: [{ id: 'p1', pos: { x: 0, y: 0 }, species: 'grass', stage: 1, width: 0.2 }] },
    })
    expect(s.floraLoaded).toBe(true)

    // The trimmed-stream control frame: agents reacquire via snapshot, but flora
    // is a separate endpoint — it must be re-armed too or a dropped PlantDied
    // leaves flora permanently stale.
    const gapped = dispatch(s, ev('StreamGap', { reason: 'cursor_trimmed' }, 0), 0, '')
    expect(gapped.baselineRetries).toBe(1)
    expect(gapped.floraLoaded).toBe(false)                 // re-armed → loadFlora refires
  })

  it('a StreamGap keeps cursor-tagged ops until a sufficiently new baseline classifies them', () => {
    // The gap lands while flora is still bootstrapping (floraLoaded=false) with
    // pre-gap ops buffered. Those ops are as-of the OLD cursor; the refetched
    // replacement baseline may or may not include them, so they stay tagged by
    // stream id until the baseline cursor can classify them safely.
    let s = onRev()
    s = dispatch(s, frame([], [{ id: 'p1', species: 'oak', pos: { x: 4, y: 5 }, stage: 1, width: 0.4 }]), 50, '101-0')
    expect(s.floraLoaded).toBe(false)
    expect(s.pendingFloraOps).toHaveLength(1)               // buffered against the OLD cursor

    const gapped = dispatch(s, ev('StreamGap', { reason: 'cursor_trimmed' }, 0), 0, '')
    expect(gapped.floraLoaded).toBe(false)
    expect(gapped.snapshotLoaded).toBe(false)               // closes SSE until fresh snapshot
    expect(gapped.pendingFloraOps).toHaveLength(1)          // retained until baseline cursor proves it folded in

    // The fresh (post-gap) baseline already carries p1 grown to stage 3; the stale
    // buffered stage-1 op is gone, so it is NOT regressed back.
    const loaded = worldReducer(gapped, {
      type: 'FLORA_LOADED',
      payload: { worldRevision: 1, streamCursor: '200-0', flora: [{ id: 'p1', pos: { x: 4, y: 5 }, species: 'oak', stage: 3, width: 1.2 }] },
    })
    expect(loaded.flora.find(f => f.id === 'p1')).toMatchObject({ stage: 3, width: 1.2 })
  })

  it('fix #3: floraStatus "off" marks flora loaded-empty and never buffers (no infinite retry)', () => {
    const off = worldReducer(initialWorldState, {
      type: 'SNAPSHOT_LOADED',
      payload: { tick: 5, agents: [], revision: 1, cursor: '10-0', terrain: 'on', flora: 'off' },
    })
    expect(off.floraStatus).toBe('off')
    expect(off.floraLoaded).toBe(true)                     // authoritatively empty; loader guard skips it
    expect(off.flora).toHaveLength(0)
  })

  it('fix #3: floraStatus "on" leaves floraLoaded false so the loader fetches once', () => {
    const on = worldReducer(initialWorldState, {
      type: 'SNAPSHOT_LOADED',
      payload: { tick: 5, agents: [], revision: 1, cursor: '10-0', terrain: 'off', flora: 'on' },
    })
    expect(on.floraStatus).toBe('on')
    expect(on.floraLoaded).toBe(false)                     // even with terrain OFF — decoupled (fix #3)
  })
})
