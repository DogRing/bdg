import { useReducer, useCallback } from 'react'
import type { WorldState, WorldAction, SimEvent, AgentState, LogEntry } from '../types'
import { formatEvent } from '../utils/eventFormatter'
import { API_BASE } from '../config'

const MAX_LOG = 500
const TRIM_TO = 400

const initial: WorldState = {
  tick: 0,
  agents: new Map(),
  roles: [],
  log: [],
  connectionStatus: 'connecting',
  selectedAgentId: null,
  paused: false,
  food: null,
  wood: null,
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
      dispatch({ type: 'SNAPSHOT_LOADED', payload: { agents: rawAgents, tick } })
    } catch {
      // snapshot not available yet — that's OK
    }
  }, [])

  return { state, dispatchEvent, setConnection, selectAgent, togglePause, loadSnapshot }
}
