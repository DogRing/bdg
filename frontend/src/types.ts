// Mirrors core.Event (data-contracts §4)
export interface SimEvent {
  schema_version: number
  tick: number
  seq: number
  agent_id: string | null
  type: string
  payload?: Record<string, unknown> // optional — some event types (SnapshotReady etc.) omit it
}

// Mirrors persist.AgentView (Redis hash shape)
export interface AgentPos {
  x: number
  y: number
}

export interface AgentState {
  id: string
  pos: AgentPos
  goal: string
  action: string
  mood: number
  cluster: string | null
  copingMode: string | null
  // Role label derived from goal dimension (e.g. "Farmer", "Guard")
  role?: string
}

export interface RoleHolder {
  function: string
  holder: string
  reliance_share: number
}

// A placed resource object (berry_bush / water_source / shelter). Positions are
// static, so the frontend reads them once from the snapshot.
export interface WorldObject {
  id: string
  kind: string
  pos: AgentPos
}

export interface LogEntry {
  id: number
  tick: number
  type: string
  agentId: string | null
  text: string
  variant: 'normal' | 'positive' | 'negative' | 'special'
}

export interface WorldState {
  tick: number
  agents: Map<string, AgentState>
  objects: WorldObject[]
  roles: RoleHolder[]
  log: LogEntry[]
  connectionStatus: 'connecting' | 'live' | 'reconnecting'
  selectedAgentId: string | null
  paused: boolean
  food: number | null
  wood: number | null
}

export type Theme = 'light' | 'dark'

export type WorldAction =
  | { type: 'SNAPSHOT_LOADED'; payload: { agents: AgentState[]; objects?: WorldObject[]; tick: number; food?: number; wood?: number } }
  | { type: 'AGENT_UPDATED'; payload: Partial<AgentState> & { id: string } }
  | { type: 'EVENT'; payload: SimEvent }
  | { type: 'SET_CONNECTION'; payload: WorldState['connectionStatus'] }
  | { type: 'SELECT_AGENT'; payload: string | null }
  | { type: 'TOGGLE_PAUSE' }
