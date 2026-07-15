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
// The prev*/`*AtMs` fields are reducer-maintained interpolation stamps (plan Q3):
// on each WorldFrame the old pos/heading shift into prev* and the tween window
// [prevFrameAtMs, frameAtMs] is stamped; `src/render/animator.ts` lerps over it.
export interface AnimalState {
  id: string
  pos: AgentPos
  species: string
  action: string
  heading: number        // radians (facing / steering direction)
  stamina: number
  coverId?: string | null // occupied cover while hidden; drives a sparse bush rustle cue
  prevPos?: AgentPos
  prevHeading?: number
  prevFrameAtMs?: number // tween start (arrival of the latest frame)
  frameAtMs?: number     // tween end (arrival + measured inter-frame gap)
}

// One transient animation (plan Q4): appended by the reducer on lifecycle events /
// attack-pose entry / flora stage increase; evaluated time-parametrically by the
// render layer; pruned by the reducer once expired (FX_DEFS durations).
export interface FxInstance {
  kind: 'spawn' | 'death' | 'attack' | 'grow'
  at: number             // ms, same clock domain as the render loop (performance.now)
  pos: AgentPos
  id: string             // entity id (an entity has at most one active fx per kind)
  species?: string
  heading?: number
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

// The flora baseline (GET /api/flora; persist.FloraDoc, data-contracts §2). The
// full live plant render set for a published revision — the plants that existed
// BEFORE the client connected (fixtures + already-propagated), which no SSE
// event replays. SSE flora_delta / PlantSpawned / PlantDied keep it current
// after the baseline. Applied as an AUTHORITATIVE REPLACEMENT (never merged) so a
// regenerated world leaves no ghost plants. worldRevision gates it against the
// snapshot's (like TerrainGrid): null ⇒ legacy backend without revisions.
export interface FloraBaseline {
  worldRevision: number | null
  // Redis stream entry id through which this full flora set is authoritative.
  streamCursor: string
  flora: PlantState[]
}

// A buffered flora render-state operation. Post-cursor SSE flora mutations that
// arrive BEFORE the /api/flora baseline (FLORA_LOADED) are queued as these ops
// and replayed on top of the baseline only when streamId is strictly newer — so an
// as-of-cursor baseline can never clobber a newer spawn/death (a dead plant
// would otherwise resurrect, a new plant vanish). 'upsert' = a full flora_delta
// row; 'remove' = a PlantDied; streamId is the SSE transport cursor.
export type FloraOp =
  | { op: 'upsert'; plant: PlantState; streamId: string }
  | { op: 'remove'; id: string; streamId: string }

// Mirrors the climate ambient hash (Redis sim:{run}:climate; data-contracts §2).
export interface ClimateState {
  temperature: number           // °C (CA3; may be sub-zero)
  apparentTemp: number | null   // °C, optional (fauna F40)
  moisture: number              // [0,1]
  raining: boolean
  snowCover: number             // [0,1] world-uniform snowpack (CS2b) — drives the flora snow-sprite switch (CS4)
  windDir: number               // radians
  windMag: number               // [0,1]
  hourOfDay: number             // [0,24)
  minuteOfDay: number           // [0,DayMinutes) game-minute within the day (HH:MM clock; DayMinutes=1440 ⇒ wall-clock)
  dayNight: 'day' | 'night'     // derived from hourOfDay
  dayOfRun: number              // 0-based day index since run start (HUD shows dayOfRun+1)
  yearFraction: number          // [0,1) annual-cycle phase
}

// One terrain render delta (climate transition / emergent trail wear). cell = the
// flat-top hex OFFSET index i = row·cols + col (data-contracts §4/§6, docs/plans/hex-grid.md).
export interface TerrainDelta {
  cell: number                  // offset index i=row·cols+col into the grid
  terrain?: string              // new terrain type id (on a climate transition)
  wear?: number                 // trail wear
}

// The full terrain grid (GET /api/terrain, plan Q5 · hex; kept current by SSE
// terrain_delta). FLAT-TOP HEX cells projected to an offset (col,row) rectangular
// array (i = row·cols + col); cellSize is the hex circumradius, orientation mirrors
// navmap ('flat'). offset(0,0) hex centre at world (0,0). A render *index* — entities
// never snap (D11).
export interface TerrainGrid {
  cellSize: number
  cols: number
  rows: number
  orientation: string           // 'flat' (flat-top hex); read from the payload, never hardcoded
  terrain: string[]             // length cols*rows, offset(col,row)
  wear?: Float32Array           // trail wear per cell, [0,1]
  elevation?: Float32Array      // per-cell relief [0,1] (generated worlds; static — never in
  //                               terrain_delta). Present ⇒ 3D extrudes real per-cell height;
  //                               absent ⇒ per-type heights (data-contracts §6).
}

// The SSE graphics frame payload (data-contracts §4 `WorldFrame`; WIRE shape, snake_case to
// match the stream). God-view EXCLUDED. `day_night` derives from `hour_of_day`; `stage` from length.
export interface WorldFramePayload {
  tick: number
  day_of_run?: number           // 0-based day index since run start (WI-P4 date HUD); absent on pre-date frames ⇒ 0
  hour_of_day: number
  minute_of_day?: number        // [0,DayMinutes) game-minute within the day (HH:MM); absent on pre-date frames ⇒ 0
  day_night: 'day' | 'night'
  temperature: number
  apparent_temp?: number
  raining: boolean
  snow_cover?: number           // [0,1] world-uniform snowpack (CS2b); absent on pre-snow frames ⇒ 0
  wind: { dir: number; mag: number }
  animals: Array<{ id: string; pos: AgentPos; species: string; action: string; heading: number; cover_id?: string }>
  // Each entry is a FULL render row (data-contracts §4): the reducer upserts it
  // by id authoritatively, so a first-seen plant (no baseline / partial replay)
  // still gets species+width to pick+scale its sprite.
  flora_delta: Array<{ id: string; species: string; pos: AgentPos; stage: number; width: number }>
  terrain_delta: TerrainDelta[]
}

// Explicit terrain availability of the published world revision
// (data-contracts §2): 'off' means env-off — bootstrap completes WITHOUT
// terrain and no polling happens; 'unknown' (legacy backend/mock without the
// flag) falls back to capped-backoff retrying. NEVER inferred from one failed
// fetch.
export type TerrainStatus = 'on' | 'off' | 'unknown'

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
  terrain: TerrainGrid | null
  pendingTerrainDeltas: TerrainDelta[]
  snapshotLoaded: boolean
  // floraLoaded: the flora baseline (GET /api/flora) for the accepted revision
  // has been applied. Distinct from `flora.length === 0`, which is also the
  // (valid) state of an installed-but-empty world; the loader keys off this flag
  // so it fetches exactly once per revision. Reset to false on a revision switch
  // AND on a StreamGap reacquire (a trim may have dropped spawn/death events, so
  // the baseline must be re-fetched to re-converge).
  floraLoaded: boolean
  // floraStatus: the published revision's explicit flora availability (mirrors
  // terrainStatus, snapshot `flora` flag / meta): 'off' ⇒ flora-off, never
  // fetched; 'on' ⇒ fetch the baseline; 'unknown' ⇒ legacy backend without the
  // flag. NEVER inferred from a failed fetch, and decoupled from terrainStatus.
  floraStatus: TerrainStatus
  // pendingFloraOps: post-cursor flora mutations (flora_delta upserts / PlantDied
  // removes) that arrived before FLORA_LOADED; replayed on top of the baseline
  // then cleared (fix: baseline must not clobber newer SSE state).
  pendingFloraOps: FloraOp[]
  // ── Baseline identity + transport cursor (frontend/SPEC.md §Bootstrap) ──
  // worldRevision: the published single-world revision the accepted baseline
  // belongs to (null until the first snapshot; a snapshot with a DIFFERENT
  // revision resets the env/fx state before applying).
  worldRevision: number | null
  // snapshotCursor: the accepted snapshot's Redis stream entry ID — SSE (re)
  // connects with it and replay starts strictly after it. '' ⇒ no cursor
  // (legacy/mock backend ⇒ live tail).
  snapshotCursor: string
  // lastAppliedStreamId: the highest stream entry ID applied so far — the
  // reconnect cursor AND the duplicate guard (entries at or below it are
  // ignored; entries without an id apply unconditionally, legacy/mock).
  lastAppliedStreamId: string
  terrainStatus: TerrainStatus
  // Increments when the baseline must be reacquired: a StreamGap control frame
  // (trimmed backlog) or a stale snapshot (cursor behind what was already
  // applied). The useWorld hook watches it and refetches after a delay.
  baselineRetries: number
  fx: FxInstance[]
  render: RenderConfig | null
}

export type Theme = 'light' | 'dark'

export interface SnapshotPayloadAction {
  agents: AgentState[]
  objects?: WorldObject[]
  tick: number
  food?: number
  wood?: number
  // Publication wrapper (data-contracts §1): absent on legacy/mock backends.
  revision?: number | null
  cursor?: string
  terrain?: TerrainStatus
  flora?: TerrainStatus
}

export type WorldAction =
  | { type: 'SNAPSHOT_LOADED'; payload: SnapshotPayloadAction }
  | { type: 'AGENT_UPDATED'; payload: Partial<AgentState> & { id: string } }
  // atMs: wall-clock (performance.now) injected at the dispatch boundary so the
  // reducer stays pure/testable — it stamps interpolation windows + fx times.
  // streamId: the SSE frame's Redis entry id (MessageEvent.lastEventId); ''
  // when the transport carries none (legacy/mock).
  | { type: 'EVENT'; payload: SimEvent; atMs?: number; streamId?: string }
  | { type: 'TERRAIN_LOADED'; payload: TerrainGrid }
  | { type: 'FLORA_LOADED'; payload: FloraBaseline }
  | { type: 'SET_CONNECTION'; payload: WorldState['connectionStatus'] }
  | { type: 'SELECT_AGENT'; payload: string | null }
  | { type: 'TOGGLE_PAUSE' }
