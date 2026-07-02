import { useReducer, useCallback } from 'react'
import type {
  WorldState, WorldAction, SimEvent, AgentState, AnimalState, LogEntry, WorldObject,
  PlantState, ClimateState, FxInstance, TerrainGrid,
} from '../types'
import { formatEvent } from '../utils/eventFormatter'
import { poseFor, FX_DEFS } from '../assets/manifest'
import { API_BASE } from '../config'

const MAX_LOG = 500
const TRIM_TO = 400

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

  // TickDone is the per-tick render frame: it carries every agent's pos/goal/mood/
  // action. This is how movement reaches the god-view (streamed over SSE, no polling).
  if (ev.type === 'TickDone' && Array.isArray(p.agents)) {
    for (const raw of p.agents as Array<Record<string, unknown>>) {
      const id = String(raw.id ?? '')
      if (!id) continue
      const prev = agents.get(id)
      const pos = parsePos(raw.pos)
      const goal = String(raw.goal ?? '')
      const action = String(raw.action ?? '')
      const mood = Number(raw.mood ?? prev?.mood ?? 0.5)
      agents.set(id, prev
        ? { ...prev, pos, goal, mood, action: action || prev.action }
        : { id, pos, goal, action, mood, cluster: null, copingMode: null })
    }
    return { ...state, tick, agents, roles, log: state.log, food, wood, fx }
  }

  // WorldFrame (data-contracts §4, WI-P4) is the env-inclusive render frame: agent + animal
  // positions, flora stage deltas, and the ambient weather (hour/day-night, temperature, rain,
  // wind). Like TickDone it returns early (no log spam) after merging the render state. God-view
  // is never present here.
  if (ev.type === 'WorldFrame') {
    if (Array.isArray(p.agents)) {
      for (const raw of p.agents as Array<Record<string, unknown>>) {
        const id = String(raw.id ?? '')
        if (!id) continue
        const prev = agents.get(id)
        const pos = parsePos(raw.pos)
        const action = String(raw.action ?? prev?.action ?? '')
        agents.set(id, prev
          ? { ...prev, pos, action }
          : { id, pos, goal: '', action, mood: 0.5, cluster: null, copingMode: null })
      }
    }

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
          species: prev?.species ?? '',
          stage,
          width: prev?.width ?? 0,
        })
      }
      flora = Array.from(byId.values())
    }

    // terrain arrives as sparse cell deltas over the /api/terrain base grid.
    // The grid OBJECT is replaced on change — the canvas re-rasterizes on
    // identity change (render/terrain.ts). Deltas without a base grid drop.
    let terrain = state.terrain
    if (terrain && Array.isArray(p.terrain_delta) && (p.terrain_delta as unknown[]).length > 0) {
      const cells = terrain.terrain.slice()
      let wear = terrain.wear
      let changed = false
      for (const raw of p.terrain_delta as Array<Record<string, unknown>>) {
        const cell = (raw.cell ?? {}) as Record<string, unknown>
        const x = Number(cell.x), y = Number(cell.y)
        if (!Number.isInteger(x) || !Number.isInteger(y) ||
            x < 0 || y < 0 || x >= terrain.w || y >= terrain.h) continue
        const idx = y * terrain.w + x
        if (typeof raw.terrain === 'string') { cells[idx] = raw.terrain; changed = true }
        if (typeof raw.wear === 'number') {
          if (!wear || wear === terrain.wear) {
            wear = wear ? Float32Array.from(wear) : new Float32Array(terrain.w * terrain.h)
          }
          wear[idx] = raw.wear
          changed = true
        }
      }
      if (changed) terrain = { ...terrain, terrain: cells, wear }
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

    return { ...state, tick, agents, animals, flora, climate, terrain, roles, log: state.log, food, wood, fx }
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
      // Merge over existing agents so periodic snapshot polls refresh authoritative
      // pos/goal/mood while preserving SSE-driven fields (cluster, copingMode, action)
      // that the snapshot does not carry.
      const agents = new Map(state.agents)
      for (const a of action.payload.agents) {
        const prev = agents.get(a.id)
        agents.set(a.id, prev ? { ...prev, pos: a.pos, goal: a.goal, mood: a.mood } : a)
      }
      return {
        ...state,
        tick: Math.max(state.tick, action.payload.tick),
        agents,
        objects: action.payload.objects && action.payload.objects.length > 0
          ? action.payload.objects
          : state.objects,
        food: action.payload.food ?? state.food,
        wood: action.payload.wood ?? state.wood,
      }
    }
    case 'AGENT_UPDATED': {
      const agents = new Map(state.agents)
      const existing = agents.get(action.payload.id)
      if (existing) {
        agents.set(action.payload.id, { ...existing, ...action.payload })
      }
      return { ...state, agents }
    }
    case 'EVENT':
      return applyEvent(state, action.payload, action.atMs ?? 0)
    case 'TERRAIN_LOADED':
      return { ...state, terrain: action.payload }
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

  const dispatchEvent = useCallback((ev: SimEvent) => {
    // performance.now() shares the clock domain with the render loop's clockMs.
    dispatch({ type: 'EVENT', payload: ev, atMs: performance.now() })
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

  const loadSnapshot = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE}/api/snapshot`)
      if (!res.ok) return
      const doc = await res.json() as Record<string, unknown>
      const tick = Number(doc.tick ?? 0)
      // Flat {tick, agents, objects} (platform/api shape); tolerate a legacy
      // {world:{...}} wrapper.
      const world = (doc.world ?? doc) as Record<string, unknown>

      // Parse agents from snapshot (array of {id, pos, goal, action, mood})
      const rawAgents: AgentState[] = []
      const agentArr = (world.Agents ?? world.agents ?? []) as Array<Record<string, unknown>>
      for (const a of agentArr) {
        const id = String(a.id ?? a.ID ?? '')
        if (!id) continue
        const pos = parsePos(a.pos ?? a.Pos)
        rawAgents.push({
          id,
          pos,
          goal: String(a.goal ?? a.Goal ?? ''),
          action: String(a.action ?? a.Action ?? ''),
          mood: Number(a.mood ?? a.Mood ?? 0.5),
          cluster: null,
          copingMode: null,
        })
      }

      // Parse placed objects (resources) — {id, kind, pos}. Static, so loaded once.
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

      dispatch({ type: 'SNAPSHOT_LOADED', payload: { agents: rawAgents, objects: rawObjects, tick } })
    } catch {
      // snapshot not available yet — that's OK
    }
  }, [])

  // Initial terrain grid (plan Q5): fetched once at connect; SSE terrain_delta
  // keeps it current. Endpoint absent (backend pre-WI-P4) → terrain stays null
  // and no terrain layer draws (env-off neutrality).
  const loadTerrain = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE}/api/terrain`)
      if (!res.ok) return
      const doc = await res.json() as Record<string, unknown>
      const size = (doc.size ?? {}) as Record<string, unknown>
      const w = Number(size.w ?? 0)
      const h = Number(size.h ?? 0)
      const cellSize = Number(doc.cell_size ?? 0)
      const cells = Array.isArray(doc.terrain) ? (doc.terrain as unknown[]).map(String) : []
      if (w <= 0 || h <= 0 || cellSize <= 0 || cells.length !== w * h) return
      const grid: TerrainGrid = { cellSize, w, h, terrain: cells }
      if (Array.isArray(doc.wear) && (doc.wear as unknown[]).length === w * h) {
        grid.wear = Float32Array.from((doc.wear as unknown[]).map(Number))
      }
      dispatch({ type: 'TERRAIN_LOADED', payload: grid })
    } catch {
      // terrain endpoint not available yet — that's OK
    }
  }, [])

  return { state, dispatchEvent, setConnection, selectAgent, togglePause, loadSnapshot, loadTerrain }
}
