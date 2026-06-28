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

// ── Env / fauna / climate (data-contracts §2/§4/§10, WI-P4) ───────────────────
// The SSE `WorldFrame` graphics frame + the Redis env render keys. God-view
// (real_stats/drives/stats) is NEVER sent over SSE — these are render-only shapes.

// Mirrors persist animal live key (Redis sim:{run}:animal:{id}; data-contracts §2).
export interface AnimalState {
  id: string
  pos: AgentPos
  species: string
  action: string
  heading: number        // radians (facing / steering direction)
  stamina: number
}

// Mirrors persist flora render row (Redis sim:{run}:flora; data-contracts §2).
// `stage` is the DERIVED discrete growth stage (from length); `width` drives sprite scale.
export interface PlantState {
  id: string
  pos: AgentPos
  species: string
  stage: number
  width: number
}

// Mirrors the climate ambient hash (Redis sim:{run}:climate; data-contracts §2).
export interface ClimateState {
  temperature: number           // °C (CA3; may be sub-zero)
  apparentTemp: number | null   // °C, optional (fauna F40)
  moisture: number              // [0,1]
  raining: boolean
  windDir: number               // radians
  windMag: number               // [0,1]
  hourOfDay: number             // [0,24)
  dayNight: 'day' | 'night'     // derived from hourOfDay
  yearFraction: number          // [0,1) annual-cycle phase
}

// One terrain render delta (climate transition / emergent trail wear). cell = navmap index.
export interface TerrainDelta {
  cell: { x: number; y: number }
  terrain?: string              // new terrain type id (on a climate transition)
  wear?: number                 // trail wear
}

// The SSE graphics frame payload (data-contracts §4 `WorldFrame`; WIRE shape, snake_case to
// match the stream). God-view EXCLUDED. `day_night` derives from `hour_of_day`; `stage` from length.
export interface WorldFramePayload {
  tick: number
  hour_of_day: number
  day_night: 'day' | 'night'
  temperature: number
  apparent_temp?: number
  raining: boolean
  wind: { dir: number; mag: number }
  agents: Array<{ id: string; pos: AgentPos; action: string }>
  animals: Array<{ id: string; pos: AgentPos; species: string; action: string; heading: number }>
  flora_delta: Array<{ id: string; pos: AgentPos; stage: number }>
  terrain_delta: TerrainDelta[]
}

// Render config from content/world.yaml (bounds + scale): sizes the canvas + sprite scale.
// Loaded from REST (world geometry), not SSE; null until fetched.
export interface RenderConfig {
  bounds: { min: AgentPos; max: AgentPos }
  pixelsPerUnit: number
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
  // Env (WI-P4) — populated from the SSE WorldFrame; empty until the env subsystem is installed.
  animals: Map<string, AnimalState>
  flora: PlantState[]
  climate: ClimateState | null
  render: RenderConfig | null
}

export type Theme = 'light' | 'dark'

export type WorldAction =
  | { type: 'SNAPSHOT_LOADED'; payload: { agents: AgentState[]; objects?: WorldObject[]; tick: number; food?: number; wood?: number } }
  | { type: 'AGENT_UPDATED'; payload: Partial<AgentState> & { id: string } }
  | { type: 'EVENT'; payload: SimEvent }
  | { type: 'SET_CONNECTION'; payload: WorldState['connectionStatus'] }
  | { type: 'SELECT_AGENT'; payload: string | null }
  | { type: 'TOGGLE_PAUSE' }
