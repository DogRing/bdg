import { useReducer, useCallback, useEffect, useRef } from 'react'
import type {
  WorldState, WorldAction, SimEvent, AgentState, AnimalState, LogEntry, WorldObject,
  PlantState, ClimateState, FxInstance, TerrainGrid, TerrainDelta, TerrainStatus,
  SnapshotPayloadAction,
} from '../types'
import { formatEvent } from '../utils/eventFormatter'
import { poseFor, FX_DEFS } from '../assets/manifest'
import { API_BASE } from '../config'
import { retryUntil, type FetchLike } from '../utils/retry'
import { compareStreamIds } from '../utils/streamId'

const MAX_LOG = 500
const TRIM_TO = 400

// Baseline-loader retry pacing (frontend/SPEC.md §Bootstrap): capped exponential
// backoff — a transient /api/snapshot or /api/terrain failure must never
// permanently stall the bootstrap, and the cap keeps a persistent 404 (fresh
// Redis) from turning into aggressive polling.
const RETRY_BASE_MS = 500
const RETRY_MAX_MS = 10_000
// Baseline reacquisition delay after a StreamGap / stale-snapshot rejection:
// the live snapshot key advances on the backup cadence, so refetching sooner
// than this gains nothing.
const BASELINE_REFETCH_MS = 2_000

// Interpolation window bounds (plan Q3): the measured inter-frame gap is
// clamped so a hiccup never schedules an absurd tween.
const GAP_MIN_MS = 100
const GAP_MAX_MS = 2000
const GAP_DEFAULT_MS = 500

const initial: WorldState = {
  tick: 0,
  agents: new Map(),
  objects: [],
  roles: [],
  log: [],
  connectionStatus: 'connecting',
  selectedAgentId: null,
  paused: false,
  food: null,
  wood: null,
  animals: new Map(),
  flora: [],
  climate: null,
  terrain: null,
  pendingTerrainDeltas: [],
  snapshotLoaded: false,
  worldRevision: null,
  snapshotCursor: '',
  lastAppliedStreamId: '',
  terrainStatus: 'unknown',
  baselineRetries: 0,
  fx: [],
  render: null,
}

// Drop fx whose timeline ended (render already skips them via fxProgress→null).
function pruneFx(fx: FxInstance[], atMs: number): FxInstance[] {
  if (fx.length === 0) return fx
  const kept = fx.filter(f => atMs - f.at <= FX_DEFS[f.kind].durationMs)
  return kept.length === fx.length ? fx : kept
}

function parsePos(raw: unknown): { x: number; y: number } {
  if (typeof raw === 'string') {
    try { return JSON.parse(raw) as { x: number; y: number } } catch { /* fall through */ }
  }
  if (typeof raw === 'object' && raw !== null) {
    const o = raw as Record<string, unknown>
    return { x: Number(o.x ?? 0), y: Number(o.y ?? 0) }
  }
  return { x: 0, y: 0 }
}

function terrainDeltasFrom(raw: unknown): TerrainDelta[] {
  if (!Array.isArray(raw)) return []
  const out: TerrainDelta[] = []
  for (const item of raw as Array<Record<string, unknown>>) {
    const cell = Number(item.cell)
    if (!Number.isInteger(cell)) continue
    const d: TerrainDelta = { cell }
    if (typeof item.terrain === 'string') d.terrain = item.terrain
    if (typeof item.wear === 'number') d.wear = item.wear
    out.push(d)
  }
  return out
}

function applyTerrainDeltas(terrain: TerrainGrid, deltas: TerrainDelta[]): TerrainGrid {
  if (deltas.length === 0) return terrain
  const cells = terrain.terrain.slice()
  const n = terrain.cols * terrain.rows
  let wear = terrain.wear
  let changed = false
  for (const d of deltas) {
    const idx = d.cell
    if (!Number.isInteger(idx) || idx < 0 || idx >= n) continue
    if (typeof d.terrain === 'string') { cells[idx] = d.terrain; changed = true }
    if (typeof d.wear === 'number') {
      if (!wear || wear === terrain.wear) {
        wear = wear ? Float32Array.from(wear) : new Float32Array(n)
      }
      wear[idx] = d.wear
      changed = true
    }
  }
  return changed ? { ...terrain, terrain: cells, wear } : terrain
}

function applyEvent(state: WorldState, ev: SimEvent, atMs: number): WorldState {
  if (state.paused) return state

  const agents = new Map(state.agents)
  let roles = state.roles
  let tick = state.tick
  let food = state.food
  let wood = state.wood
  let fx = pruneFx(state.fx, atMs)

  const p = ev.payload ?? {}

  // Update tick
  if (ev.tick > tick) tick = ev.tick

  // AgentFrame is the sparse per-agent render/status delta. The snapshot remains
  // the late-join baseline; this only carries fields that changed.
  if (ev.type === 'AgentFrame' && Array.isArray(p.agents)) {
    for (const raw of p.agents as Array<Record<string, unknown>>) {
      const id = String(raw.id ?? '')
      if (!id) continue
      const prev = agents.get(id)
      const pos = raw.pos !== undefined ? parsePos(raw.pos) : (prev?.pos ?? { x: 0, y: 0 })
      const goal = raw.goal !== undefined ? String(raw.goal) : (prev?.goal ?? '')
      const action = raw.action !== undefined ? String(raw.action) : (prev?.action ?? '')
      const mood = Number(raw.mood ?? prev?.mood ?? 0.5)
      agents.set(id, prev
        ? { ...prev, pos, goal, mood, action }
        : { id, pos, goal, action, mood, cluster: null, copingMode: null })
    }
    if (Array.isArray(p.removed)) {
      for (const id of p.removed) agents.delete(String(id))
    }
    return { ...state, tick, agents, roles, log: state.log, food, wood, fx }
  }

  // WorldFrame (data-contracts §4, WI-P4) is the env-inclusive render frame:
  // animal positions, flora stage deltas, terrain deltas, and ambient weather.
  // Agent status/position is handled by AgentFrame.
  if (ev.type === 'WorldFrame') {
    const animals = new Map(state.animals)
    if (Array.isArray(p.animals)) {
      for (const raw of p.animals as Array<Record<string, unknown>>) {
        const id = String(raw.id ?? '')
        if (!id) continue
        const prev = animals.get(id)
        const pos = parsePos(raw.pos)
        const action = String(raw.action ?? prev?.action ?? '')
        const heading = Number(raw.heading ?? prev?.heading ?? 0)
        const next: AnimalState = {
          id,
          pos,
          species: String(raw.species ?? prev?.species ?? ''),
          action,
          heading,
          stamina: Number(raw.stamina ?? prev?.stamina ?? 1),
        }
        if (prev) {
          // Shift the old target into prev* and stamp the tween window with the
          // measured inter-frame gap (adaptive lerp, plan Q3).
          const gap = prev.prevFrameAtMs !== undefined
            ? Math.min(GAP_MAX_MS, Math.max(GAP_MIN_MS, atMs - prev.prevFrameAtMs))
            : GAP_DEFAULT_MS
          next.prevPos = prev.pos
          next.prevHeading = prev.heading
          next.prevFrameAtMs = atMs
          next.frameAtMs = atMs + gap
          // Attack motion trigger (plan Q4): the pose *entering* attack enqueues
          // one lunge fx — not re-armed while the action stays attack-posed.
          if (poseFor(action) === 'attack' && poseFor(prev.action) !== 'attack') {
            fx = [...fx, { kind: 'attack', at: atMs, pos, id, species: next.species, heading }]
          }
        }
        animals.set(id, next)
      }
    }

    // flora arrives as a sparse delta — merge by id
    let flora = state.flora
    if (Array.isArray(p.flora_delta) && (p.flora_delta as unknown[]).length > 0) {
      const byId = new Map(state.flora.map((f): [string, PlantState] => [f.id, f]))
      for (const raw of p.flora_delta as Array<Record<string, unknown>>) {
        const id = String(raw.id ?? '')
        if (!id) continue
        const prev = byId.get(id)
        const pos = parsePos(raw.pos)
        const stage = Number(raw.stage ?? prev?.stage ?? 0)
        // A stage increase is the visible growth moment → grow tween (plan Q4).
        if (prev && stage > prev.stage) {
          fx = [...fx, { kind: 'grow', at: atMs, pos, id, species: prev.species }]
        }
        byId.set(id, {
          id,
          pos,
          // Prefer the wire value (a full snapshot carries species/width so late
          // joiners converge); fall back to prev for a sparse growth-only delta.
          species: String(raw.species ?? prev?.species ?? ''),
          stage,
          width: Number(raw.width ?? prev?.width ?? 0),
        })
      }
      flora = Array.from(byId.values())
    }

    // terrain arrives as sparse cell deltas over the /api/terrain base grid.
    // If SSE wins the startup race, queue deltas until the base grid arrives.
    let terrain = state.terrain
    let pendingTerrainDeltas = state.pendingTerrainDeltas
    const terrainDeltas = terrainDeltasFrom(p.terrain_delta)
    if (terrainDeltas.length > 0) {
      if (terrain) {
        terrain = applyTerrainDeltas(terrain, terrainDeltas)
      } else {
        pendingTerrainDeltas = [...pendingTerrainDeltas, ...terrainDeltas]
      }
    }

    const wind = (p.wind ?? {}) as Record<string, unknown>
    const climate: ClimateState = {
      temperature: Number(p.temperature ?? state.climate?.temperature ?? 0),
      apparentTemp: typeof p.apparent_temp === 'number' ? p.apparent_temp : (state.climate?.apparentTemp ?? null),
      moisture: state.climate?.moisture ?? 0,
      raining: Boolean(p.raining),
      windDir: Number(wind.dir ?? state.climate?.windDir ?? 0),
      windMag: Number(wind.mag ?? state.climate?.windMag ?? 0),
      hourOfDay: Number(p.hour_of_day ?? state.climate?.hourOfDay ?? 0),
      dayNight: p.day_night === 'night' ? 'night' : 'day',
      yearFraction: state.climate?.yearFraction ?? 0,
    }

    return { ...state, tick, agents, animals, flora, climate, terrain, pendingTerrainDeltas, roles, log: state.log, food, wood, fx }
  }

  // Ecosystem lifecycle events (data-contracts §4, WI-P4): mutate the live maps
  // AND enqueue the matching transition fx (plan Q4); they fall through to
  // formatEvent below so the log line still appears.
  let animals = state.animals
  let flora = state.flora
  if (ev.type === 'AnimalBorn' || ev.type === 'AnimalDied' ||
      ev.type === 'PlantSpawned' || ev.type === 'PlantDied') {
    const id = String(p.object_id ?? '')
    const species = String(p.species ?? '')
    if (id) {
      switch (ev.type) {
        case 'AnimalBorn': {
          const pos = parsePos(p.pos)
          animals = new Map(animals)
          animals.set(id, { id, pos, species, action: '', heading: 0, stamina: 1 })
          fx = [...fx, { kind: 'spawn', at: atMs, pos, id, species }]
          break
        }
        case 'AnimalDied': {
          const live = animals.get(id)
          const pos = live?.pos ?? parsePos(p.pos)
          animals = new Map(animals)
          animals.delete(id)
          fx = [...fx, {
            kind: 'death', at: atMs, pos, id,
            species: live?.species ?? species, heading: live?.heading,
          }]
          break
        }
        case 'PlantSpawned': {
          const pos = parsePos(p.pos)
          flora = [...flora.filter(f => f.id !== id), {
            id, pos, species, stage: Number(p.stage ?? 0), width: Number(p.width ?? 0),
          }]
          fx = [...fx, { kind: 'spawn', at: atMs, pos, id, species }]
          break
        }
        case 'PlantDied': {
          const live = flora.find(f => f.id === id)
          const pos = live?.pos ?? parsePos(p.pos)
          flora = flora.filter(f => f.id !== id)
          fx = [...fx, { kind: 'death', at: atMs, pos, id, species: live?.species ?? species }]
          break
        }
      }
    }
  }

  // Update agent state from event payload
  if (ev.agent_id) {
    const existing = agents.get(ev.agent_id)
    const base: AgentState = existing ?? {
      id: ev.agent_id,
      pos: { x: 0, y: 0 },
      goal: '',
      action: '',
      mood: 0.5,
      cluster: null,
      copingMode: null,
    }

    switch (ev.type) {
      case 'GoalSelected': {
        const dim = String(p.dimension ?? '')
        agents.set(ev.agent_id, { ...base, goal: dim })
        break
      }
      case 'ActionStarted': {
        const action = String(p.action ?? '')
        const pos = p.pos ? parsePos(p.pos) : base.pos
        agents.set(ev.agent_id, { ...base, action, pos })
        break
      }
      case 'ActionDone': {
        const action = String(p.action ?? '')
        agents.set(ev.agent_id, { ...base, action: `✓ ${action}` })
        break
      }
      case 'CopingEntered': {
        const mode = String(p.mode ?? '')
        agents.set(ev.agent_id, { ...base, copingMode: mode })
        break
      }
      case 'BeliefUpdated': {
        // mood drift on negative belief delta
        const delta = typeof p['new'] === 'number' && typeof p['old'] === 'number'
          ? (p['new'] as number) - (p['old'] as number) : 0
        const mood = Math.max(0, Math.min(1, base.mood + delta * 0.1))
        agents.set(ev.agent_id, { ...base, mood })
        break
      }
    }
  }

  if (ev.type === 'RoleEmerged') {
    const fn = String(p.function ?? '')
    const holder = String(p.holder ?? '')
    const share = Number(p.reliance_share ?? 0)
    // Assign cluster to holder
    const holder_agent = agents.get(holder)
    if (holder_agent) {
      agents.set(holder, { ...holder_agent, cluster: `cluster_${holder}` })
    }
    roles = [...roles.filter(r => r.function !== fn), { function: fn, holder, reliance_share: share }]
  }

  // Format log entry
  const entry: LogEntry | null = formatEvent(ev)
  let log = state.log
  if (entry) {
    log = [entry, ...state.log]
    if (log.length > MAX_LOG) log = log.slice(0, TRIM_TO)
  }

  return { ...state, tick, agents, animals, flora, roles, log, food, wood, fx }
}

// Exported for reducer unit tests (frontend/SPEC.md ACs); components use useWorld().
export function worldReducer(state: WorldState, action: WorldAction): WorldState {
  switch (action.type) {
    case 'SNAPSHOT_LOADED': {
      const p = action.payload
      const revision = p.revision ?? null
      const cursor = p.cursor ?? ''
      const revisionSwitch =
        state.worldRevision !== null && revision !== null && revision !== state.worldRevision

      // Stale-baseline guard (SPEC §Bootstrap): within the SAME revision, a
      // snapshot whose cursor is BEHIND entries we already applied would roll
      // sparse state back across deltas that will never be replayed — reject
      // and reacquire (the live key advances on the backup cadence). A
      // revision switch always accepts: it is a NEW world under a recreated
      // stream, so the old cursor space does not order against it.
      if (!revisionSwitch && state.snapshotLoaded && cursor !== '' && state.lastAppliedStreamId !== '' &&
        compareStreamIds(cursor, state.lastAppliedStreamId) < 0) {
        return { ...state, baselineRetries: state.baselineRetries + 1 }
      }

      // AUTHORITATIVE roster (SPEC §Bootstrap step 4): the snapshot's agents
      // ARE the world's agents — anything absent is gone (no ghost survives a
      // merge accident). SSE-only fields (cluster/copingMode) are re-derived
      // from post-cursor events.
      const agents = new Map<string, AgentState>()
      for (const a of p.agents) agents.set(a.id, a)

      const terrainStatus: TerrainStatus = p.terrain ?? 'unknown'
      let next: WorldState = {
        ...state,
        // Tick only advances WITHIN a revision; a revision switch legitimately
        // rewinds it (the regenerated world starts near 0).
        tick: revisionSwitch ? p.tick : Math.max(state.tick, p.tick),
        agents,
        objects: p.objects && p.objects.length > 0
          ? p.objects
          : (revisionSwitch ? [] : state.objects),
        food: p.food ?? state.food,
        wood: p.wood ?? state.wood,
        snapshotLoaded: true,
        worldRevision: revision ?? state.worldRevision,
        snapshotCursor: cursor,
        // The snapshot supersedes everything at or before its cursor; SSE
        // replay resumes strictly after it.
        lastAppliedStreamId: cursor !== '' ? cursor : (revisionSwitch ? '' : state.lastAppliedStreamId),
        terrainStatus,
      }
      if (revisionSwitch) {
        // New published world under the same run: every old-revision slice is
        // invalid — env entities, fx, terrain and its queued deltas all clear
        // (SPEC §Bootstrap revision switch). The log is a narrative and stays.
        next = {
          ...next,
          animals: new Map(), flora: [], climate: null, fx: [],
          terrain: null, pendingTerrainDeltas: [],
        }
      }
      if (terrainStatus === 'off') {
        // Explicit env-off: no terrain exists for this revision — never poll.
        next = { ...next, terrain: null, pendingTerrainDeltas: [] }
      }
      return next
    }
    case 'AGENT_UPDATED': {
      const agents = new Map(state.agents)
      const existing = agents.get(action.payload.id)
      if (existing) {
        agents.set(action.payload.id, { ...existing, ...action.payload })
      }
      return { ...state, agents }
    }
    case 'EVENT': {
      const ev = action.payload
      if (ev.type === 'StreamGap') {
        // Server control frame (api SPEC GET /sse): the retained stream can no
        // longer bridge our cursor — the backlog was trimmed. Reacquire a
        // fresh snapshot/cursor pair instead of accepting a partial sparse
        // history; the server closed the stream, so nothing arrives meanwhile.
        return { ...state, baselineRetries: state.baselineRetries + 1 }
      }
      // SSE is enabled only after the snapshot baseline (App wiring), so a
      // pre-baseline event can only be a mis-ordered straggler — drop it
      // (sparse deltas are meaningless without the roster).
      if (!state.snapshotLoaded) return state

      // Transport-cursor guard (SPEC §Bootstrap): apply an identified entry
      // only when it is strictly after the last applied one — replayed and
      // live frames arrive on one ordered stream, so this drops duplicates,
      // pre-cursor stragglers and old-revision leftovers alike. Entries
      // without an id (legacy/mock transport) apply unconditionally.
      const streamId = action.streamId ?? ''
      if (streamId !== '' && state.lastAppliedStreamId !== '' &&
        compareStreamIds(streamId, state.lastAppliedStreamId) <= 0) {
        return state
      }
      const next = applyEvent(state, ev, action.atMs ?? 0)
      if (streamId === '') return next
      return next === state
        ? { ...state, lastAppliedStreamId: streamId }
        : { ...next, lastAppliedStreamId: streamId }
    }
    case 'TERRAIN_LOADED': {
      // The terrain grid IS the world geometry (SPEC §API: RenderConfig comes
      // from REST world geometry): derive RenderConfig.bounds from it so the
      // camera anchors to the real world edge instead of staying frozen on the
      // bbox of whichever entities arrived first (SSE agents can beat the REST
      // snapshot on a slow link, leaving animals/terrain outside the viewport).
      // A future dedicated geometry endpoint may overwrite this; never clobber
      // an already-present config.
      // Flat-top hex offset grid → world extent: columns are 1.5·cellSize apart in x,
      // rows √3·cellSize apart in y (hex-grid.md). Slightly generous framing is safe
      // (entities stay visible); the real bounds come from a future geometry endpoint.
      const g = action.payload
      const render = state.render ?? {
        bounds: {
          min: { x: 0, y: 0 },
          max: { x: g.cols * 1.5 * g.cellSize, y: g.rows * Math.sqrt(3) * g.cellSize },
        },
        pixelsPerUnit: 4,
      }
      const terrain = applyTerrainDeltas(g, state.pendingTerrainDeltas)
      return { ...state, terrain, pendingTerrainDeltas: [], render }
    }
    case 'SET_CONNECTION':
      return { ...state, connectionStatus: action.payload }
    case 'SELECT_AGENT':
      return { ...state, selectedAgentId: action.payload }
    case 'TOGGLE_PAUSE':
      return { ...state, paused: !state.paused }
    default:
      return state
  }
}

export const initialWorldState: WorldState = initial

export function useWorld() {
  const [state, dispatch] = useReducer(worldReducer, initial)

  const dispatchEvent = useCallback((ev: SimEvent, streamId = '') => {
    // performance.now() shares the clock domain with the render loop's clockMs.
    // streamId is the SSE frame's Redis entry id (duplicate/ordering guard).
    dispatch({ type: 'EVENT', payload: ev, atMs: performance.now(), streamId })
  }, [])

  const setConnection = useCallback((s: WorldState['connectionStatus']) => {
    dispatch({ type: 'SET_CONNECTION', payload: s })
  }, [])

  const selectAgent = useCallback((id: string | null) => {
    dispatch({ type: 'SELECT_AGENT', payload: id })
  }, [])

  const togglePause = useCallback(() => {
    dispatch({ type: 'TOGGLE_PAUSE' })
  }, [])

  // Loader generations: bumping cancels the matching in-flight retry loop and
  // discards its late (stale) response (SPEC §Bootstrap step 3).
  const snapshotGen = useRef(0)
  const terrainGen = useRef(0)
  useEffect(() => () => { snapshotGen.current++; terrainGen.current++ }, [])

  const loadSnapshot = useCallback(async () => {
    const gen = ++snapshotGen.current
    const payload = await fetchSnapshotWithRetry({ isCancelled: () => gen !== snapshotGen.current })
    if (payload) dispatch({ type: 'SNAPSHOT_LOADED', payload })
  }, [])

  // Terrain follows the published availability flag (SPEC §Bootstrap step 6;
  // task: env-off must not poll): 'off' ⇒ never fetched; 'on'/'unknown' ⇒
  // fetched with capped-backoff retry once the snapshot (and its revision) is
  // in; a revision switch nulls the grid, which re-arms this effect for the
  // new revision.
  const loadTerrain = useCallback(async (expectedRevision: number | null) => {
    const gen = ++terrainGen.current
    const grid = await fetchTerrainWithRetry({
      isCancelled: () => gen !== terrainGen.current,
      expectedRevision,
    })
    if (grid) dispatch({ type: 'TERRAIN_LOADED', payload: grid })
  }, [])

  useEffect(() => {
    if (!state.snapshotLoaded || state.terrainStatus === 'off' || state.terrain !== null) return
    void loadTerrain(state.worldRevision)
  }, [state.snapshotLoaded, state.terrainStatus, state.terrain, state.worldRevision, loadTerrain])

  // Baseline reacquisition (SPEC §Bootstrap step 5): a StreamGap control frame
  // or a stale snapshot rejection asks for a fresh snapshot/cursor pair —
  // refetch after a modest delay (backup-cadence pacing, not per-tick polling).
  useEffect(() => {
    if (state.baselineRetries === 0) return
    const timer = setTimeout(() => { void loadSnapshot() }, BASELINE_REFETCH_MS)
    return () => clearTimeout(timer)
  }, [state.baselineRetries, loadSnapshot])

  return { state, dispatchEvent, setConnection, selectAgent, togglePause, loadSnapshot }
}

// ── Baseline loaders (exported for tests; SPEC §Bootstrap steps 3–4) ──────────

export type SnapshotPayload = SnapshotPayloadAction

export interface BaselineLoaderOptions {
  fetchFn?: FetchLike
  sleep?: (ms: number) => Promise<void>
  isCancelled?: () => boolean
}

export interface TerrainLoaderOptions extends BaselineLoaderOptions {
  // When the snapshot identified its revision, the terrain grid must belong to
  // the SAME published revision — a mismatching grid (another regen landed
  // between the two reads) counts as not-ready and is retried. null/undefined
  // ⇒ legacy backend without revision tags: accept any grid.
  expectedRevision?: number | null
}

// parseSnapshotDoc maps the /api/snapshot blob to the SNAPSHOT_LOADED payload,
// including the publication wrapper (data-contracts §1): world_revision,
// stream_cursor and the explicit terrain flag — all absent on legacy/mock
// backends (⇒ revision null, cursor '', terrain 'unknown').
// Flat {tick, agents, objects} (platform/api shape); tolerates a legacy
// {world:{...}} wrapper. Returns null only on a non-object document.
export function parseSnapshotDoc(doc: unknown): SnapshotPayload | null {
  if (typeof doc !== 'object' || doc === null) return null
  const root = doc as Record<string, unknown>
  const tick = Number(root.tick ?? 0)
  const revision = typeof root.world_revision === 'number' && Number.isFinite(root.world_revision)
    ? root.world_revision : null
  const cursor = typeof root.stream_cursor === 'string' ? root.stream_cursor : ''
  const terrain: TerrainStatus =
    root.terrain === 'on' ? 'on' : root.terrain === 'off' ? 'off' : 'unknown'
  const world = (root.world ?? root) as Record<string, unknown>

  const rawAgents: AgentState[] = []
  const agentArr = (world.Agents ?? world.agents ?? []) as Array<Record<string, unknown>>
  for (const a of agentArr) {
    const id = String(a.id ?? a.ID ?? '')
    if (!id) continue
    rawAgents.push({
      id,
      pos: parsePos(a.pos ?? a.Pos),
      goal: String(a.goal ?? a.Goal ?? ''),
      action: String(a.action ?? a.Action ?? ''),
      mood: Number(a.mood ?? a.Mood ?? 0.5),
      cluster: null,
      copingMode: null,
    })
  }

  // Placed objects (resources) — {id, kind, pos}. Static, so loaded once.
  const rawObjects: WorldObject[] = []
  const objArr = (world.objects ?? world.Objects ?? []) as Array<Record<string, unknown>>
  for (const o of objArr) {
    const id = String(o.id ?? o.ID ?? '')
    if (!id) continue
    rawObjects.push({
      id,
      kind: String(o.kind ?? o.Kind ?? ''),
      pos: parsePos(o.pos ?? o.Pos),
    })
  }

  return { agents: rawAgents, objects: rawObjects, tick, revision, cursor, terrain }
}

// parseTerrainDoc maps the /api/terrain response to a TerrainGrid, or null on a
// malformed shape (treated as a transient failure — retried).
export function parseTerrainDoc(doc: unknown): TerrainGrid | null {
  if (typeof doc !== 'object' || doc === null) return null
  const root = doc as Record<string, unknown>
  const size = (root.size ?? {}) as Record<string, unknown>
  const cols = Number(size.cols ?? 0)
  const rows = Number(size.rows ?? 0)
  const cellSize = Number(root.cell_size ?? 0)
  const orientation = typeof root.orientation === 'string' ? root.orientation : 'flat'
  const cells = Array.isArray(root.terrain) ? (root.terrain as unknown[]).map(String) : []
  if (cols <= 0 || rows <= 0 || cellSize <= 0 || cells.length !== cols * rows) return null
  const grid: TerrainGrid = { cellSize, cols, rows, orientation, terrain: cells }
  if (Array.isArray(root.wear) && (root.wear as unknown[]).length === cols * rows) {
    grid.wear = Float32Array.from((root.wear as unknown[]).map(Number))
  }
  if (Array.isArray(root.elevation) && (root.elevation as unknown[]).length === cols * rows) {
    grid.elevation = Float32Array.from((root.elevation as unknown[]).map(Number))
  }
  return grid
}

// fetchSnapshotWithRetry resolves the snapshot baseline, retrying non-OK /
// network / malformed responses with capped backoff until success or
// cancellation (⇒ null; a stale late response is never returned).
export function fetchSnapshotWithRetry(opts: BaselineLoaderOptions = {}): Promise<SnapshotPayload | null> {
  const fetchFn: FetchLike = opts.fetchFn ?? (url => fetch(url))
  return retryUntil(async () => {
    try {
      const res = await fetchFn(`${API_BASE}/api/snapshot`)
      if (!res.ok) return null
      return parseSnapshotDoc(await res.json())
    } catch {
      return null
    }
  }, { baseMs: RETRY_BASE_MS, maxMs: RETRY_MAX_MS, sleep: opts.sleep, isCancelled: opts.isCancelled })
}

// fetchTerrainWithRetry — same policy for the terrain grid, plus the revision
// match: a grid tagged with a DIFFERENT world_revision than the accepted
// snapshot's is another revision's baseline (a regen landed between the two
// reads) and is retried until the matching one is published.
export function fetchTerrainWithRetry(opts: TerrainLoaderOptions = {}): Promise<TerrainGrid | null> {
  const fetchFn: FetchLike = opts.fetchFn ?? (url => fetch(url))
  const expected = opts.expectedRevision ?? null
  return retryUntil(async () => {
    try {
      const res = await fetchFn(`${API_BASE}/api/terrain`)
      if (!res.ok) return null
      const doc = await res.json()
      if (expected !== null && (typeof doc !== 'object' || doc === null ||
        Number((doc as Record<string, unknown>).world_revision) !== expected)) {
        return null // grid belongs to another published revision — retry
      }
      return parseTerrainDoc(doc)
    } catch {
      return null
    }
  }, { baseMs: RETRY_BASE_MS, maxMs: RETRY_MAX_MS, sleep: opts.sleep, isCancelled: opts.isCancelled })
}
