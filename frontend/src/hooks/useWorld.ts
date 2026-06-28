import { useReducer, useCallback } from 'react'
import type {
  WorldState, WorldAction, SimEvent, AgentState, LogEntry, WorldObject,
  PlantState, ClimateState,
} from '../types'
import { formatEvent } from '../utils/eventFormatter'
import { API_BASE } from '../config'

const MAX_LOG = 500
const TRIM_TO = 400

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
  render: null,
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

function applyEvent(state: WorldState, ev: SimEvent): WorldState {
  if (state.paused) return state

  const agents = new Map(state.agents)
  let roles = state.roles
  let tick = state.tick
  let food = state.food
  let wood = state.wood

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
    return { ...state, tick, agents, roles, log: state.log, food, wood }
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
        animals.set(id, {
          id,
          pos: parsePos(raw.pos),
          species: String(raw.species ?? prev?.species ?? ''),
          action: String(raw.action ?? prev?.action ?? ''),
          heading: Number(raw.heading ?? prev?.heading ?? 0),
          stamina: Number(raw.stamina ?? prev?.stamina ?? 1),
        })
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
        byId.set(id, {
          id,
          pos: parsePos(raw.pos),
          species: prev?.species ?? '',
          stage: Number(raw.stage ?? prev?.stage ?? 0),
          width: prev?.width ?? 0,
        })
      }
      flora = Array.from(byId.values())
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

    return { ...state, tick, agents, animals, flora, climate, roles, log: state.log, food, wood }
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

  return { ...state, tick, agents, roles, log, food, wood }
}

function reducer(state: WorldState, action: WorldAction): WorldState {
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
      return applyEvent(state, action.payload)
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

export function useWorld() {
  const [state, dispatch] = useReducer(reducer, initial)

  const dispatchEvent = useCallback((ev: SimEvent) => {
    dispatch({ type: 'EVENT', payload: ev })
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
      const world = (doc.world ?? {}) as Record<string, unknown>

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

  return { state, dispatchEvent, setConnection, selectAgent, togglePause, loadSnapshot }
}
