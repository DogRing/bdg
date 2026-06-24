# SPEC — `engine/climate`

> Status: `DRAFT`
> Leaf level: `L1`  ·  Owner agent: `<filled by implementer>`

## Purpose
The **dynamic-terrain driver**: a pure, deterministic transform over a **coarse climate grid** of
`Moisture` + `Temperature` state. Given the current climate state, the time-derived forcing (rain
process + time-of-day), and the data-defined transition rules, it produces the **next state** plus
the **list of climate cells that crossed a terrain-transition threshold** (`cell, from→to`). It owns
*what the weather is and which terrain should change* — it does **not** own *when* it runs (cadence)
nor *the navmap write* (those are `engine/world`). No IO, no wall-clock, no global rand: every output
is a function of `(state, forcing/RNG, rules)` (D12). Concept & rationale: `docs/design.md §5` +
`docs/climate.md` (all resolutions are binding).

## Public Interface
```go
package climate

import (
    "github.com/dogring/bdg/backend/engine/core"
    "github.com/dogring/bdg/backend/engine/rng"
)

// ── Identity & state ──────────────────────────────────────────────────────────────

// GridCell is the integer index of a COARSE climate cell. One climate cell spans many
// navmap.Cells (RESOLVED #1: coarse grid, mapped to navmap cells only on transition).
// It is an internal index, never an agent position (D11). Distinct type from navmap.Cell
// so the two grids never get confused; world owns the climate-cell → navmap-cell mapping.
type GridCell struct{ X, Y int }

// CellState is the per-climate-cell dynamic state. Moisture/Temperature are normalized [0,1].
type CellState struct {
    Moisture    float64 // wetness; rain raises it, evaporation (∝ Temperature) lowers it
    Temperature float64 // f(time-of-day, rain); feeds evaporation rate (1b)
    Terrain     navTerrainID // current terrain type of this climate cell (drives transitions)
}

// navTerrainID is the terrain-type identifier, defined as core.Tag — IDENTICAL underlying
// type to navmap.TerrainID, but declared here so engine/climate does NOT import engine/navmap
// (one-way wiring: world bridges climate→navmap; RESOLVED #6). It names a key in
// content/terrain.yaml validated against the transition table at load (D10).
type navTerrainID = core.Tag

// State is the whole climate field for one run: the coarse grid of CellState plus the
// rain process accumulator. Snapshot-serializable (data-contracts §6). Bounds + cell
// size come from Config; world maps GridCell → []navmap.Cell when applying transitions.
type State struct{ /* opaque; see Owned Data. Holds the CellState grid + RainProcess */ }

// RainProcess is the seeded stochastic rain accumulator (1a). Captured in the snapshot so
// resume is byte-identical (D12). Exposed read-only for serialization + observability.
type RainProcess struct {
    Raining          bool  // currently raining
    RainEndsAtHour   int64 // game-hour the current rain stops (valid iff Raining); duration was uniform 2–12h
    PRain            float64 // accumulating per-game-hour rain probability (0 right after rain; → 1)
    HoursSinceRain   int64 // game-hours since last rain ended (drives the 10d expected / 30d hard cap)
}

// ── Construction ────────────────────────────────────────────────────────────────

// Config carries the climate-grid geometry + tuning RATES, injected by platform/config
// from content/climate.yaml — NEVER hardcoded (D10). The 10-day expected / 30-day forced /
// 2–12h-duration SHAPE is fixed design (1a) and lives in the formulas below, NOT a free
// design knob; only the rates are tunable. (The hard-cap/duration values still arrive via
// Config so engine logic stays literal-free, D10.)
type Config struct {
    GridCols, GridRows int     // coarse-grid dimensions (one climate cell ≫ one navmap cell)
    InitMoisture       float64 // starting Moisture for every cell (climate.yaml grid.initial_moisture; default 0.5)
    InitTemperature    float64 // starting Temperature for every cell (climate.yaml grid.initial_temperature; default 0.5)
    RainProbPerHour    float64 // per-game-hour increment to PRain, calibrated so E[first rain] ≈ 10 days
    RainHardCapHours   int64   // forced rain after this many dry game-hours (= 720, 30 days) — fixed-design, injected
    RainDurMinHours    int64   // rain duration lower bound (= 2) — fixed-design, injected
    RainDurMaxHours    int64   // rain duration upper bound (= 12) — fixed-design, injected
    MoistureRainRate   float64 // Moisture gained per game-hour while raining
    EvapBaseRate       float64 // base Moisture lost per game-hour when not raining
    EvapTempScale      float64 // extra evaporation per unit Temperature (Temperature → Moisture feedback, 1a)
    TempDayPeak        float64 // Temperature at the daily peak (1b)
    TempNightLow       float64 // Temperature at the daily trough (1b)
    TempRainDrop       float64 // Temperature reduction while raining (1b)
}

// New builds the initial climate State: every climate cell seeded from terrainAt + the
// Init* Moisture/Temperature, and a fresh dry RainProcess. terrainAt maps a climate-cell
// CENTER (in continuous coords) to its starting terrain type — the SAME per-run layout
// source navmap.New uses, so the two grids agree at t=0. Pure; no RNG draw at construction.
func New(cfg Config, terrainAt func(core.Vec2) navTerrainID) *State

// ── Forcing (time-derived; D12 — derived from worldtime, never wall-clock) ─────────

// Forcing is the per-step exogenous input. world builds it each climate step from the
// current Tick (live) or from a fixture (golden/scenario) — climate never reads a clock.
type Forcing struct {
    HourOfDay  int   // [0,24) from worldtime.Clock.HourOfDay — drives the Temperature day curve (1b)
    AbsHour    int64 // absolute game-hour count (worldtime), monotone — drives the rain process timeline
}

// ── The pure transform world calls (RESOLVED #5/#6) ────────────────────────────────

// Step advances the whole climate field ONE climate step (= N=60 ticks = 1 game-hour,
// RESOLVED #9 — world owns the cadence and only calls Step when tick % N == 0). It is a
// PURE function of (prev, f, rules, rng): it does NOT mutate prev, returns the next State
// and the terrain transitions that fired this step. rng is the per-step seeded fork world
// supplies (D12 — same seed ⇒ byte-identical next state + transitions). The rain process
// (1a), Temperature update (1b), Moisture integration, and threshold-driven transitions
// (rules, evaluated in sorted GridCell order) all happen here.
func Step(prev *State, f Forcing, rules *Rules, rng *rng.RNG) (next *State, transitions []Transition)

// Transition is one climate cell crossing a terrain threshold this step. world maps
// Cell → []navmap.Cell and calls navmap.SetTerrain(those, To) in the apply phase.
type Transition struct {
    Cell GridCell
    From navTerrainID
    To   navTerrainID
}

// ── Rules (the data-defined transition table, RESOLVED #4) ──────────────────────────

// Rules is the compiled, immutable transition table from content/climate.yaml: for each
// from-type, an ORDERED list of {condition, to-type}. Built once by platform/config (parse
// the §6-DSL boolean condition + validate every type id exists in content/terrain.yaml).
// engine/climate evaluates it read-only; it never parses YAML (D10 — content is injected).
// HOW the condition is compiled/evaluated depends on the shared §6 Formula evaluator —
// see Open Questions (the evaluator's home module is the one P1 escalation).
type Rules struct{ /* opaque; per-from-type ordered []rule{cond, to}. Deterministic eval order. */ }

// Eval returns the destination terrain for a cell given its state, or ("",false) if no rule
// fires (stays put). Conditions are §6-DSL booleans over CellState operands (moisture,
// temperature) — the boolean subset of the §6 Formula (design.md §6; glossary: GateExpr is
// that same subset). No RNG inside a condition (deterministic). The FIRST matching rule for
// the from-type wins (ordered table → deterministic; D12).
func (r *Rules) Eval(from navTerrainID, s CellState) (to navTerrainID, fired bool)

// ── Snapshot / serialization (data-contracts §6) ──────────────────────────────────

// Cells returns the coarse-grid CellState in D12-sorted (Y-major then X) GridCell order,
// for the periodic-full serialization channel (data-contracts §6: periodic full + deltas).
func (s *State) Cells() []GridCellState
type GridCellState struct{ Cell GridCell; State CellState }

// Rain exposes the RainProcess for snapshot/resume + observability (read-only copy).
func (s *State) Rain() RainProcess
```

## Dependencies
- `engine/core` — `Vec2`, `Tag` (the terrain id type). **Plus the shared §6 Formula (boolean
  subset) evaluator IF it lands in `core`** — see Open Questions; the climate condition
  (`moisture > 0.7`) is the boolean subset of the §6 DSL (glossary: `Formula`/`GateExpr` "one shared
  evaluator"). The evaluator's home module is not yet fixed by any SPEC and is this batch's one P1
  escalation; the `Rules` type isolates climate from that decision (it holds compiled conditions, so
  only `platform/config`'s compile step and `Rules.Eval` touch the evaluator).
- `engine/rng` — `*RNG` (injected seeded fork; the rain process + duration draws, D12).
- *(NOT `engine/navmap`)* — one-way: climate returns a `[]Transition`; `world` performs the
  `navmap.SetTerrain` write (RESOLVED #6, preserves "world is the sole navmap mutator").
- *(NOT `engine/worldtime`)* — climate does not import worldtime; `world` derives `Forcing`
  (`HourOfDay`, `AbsHour`) from `worldtime.Clock` and passes it in (keeps climate a pure transform).
- *(NOT `engine/gates`)* — climate must NOT import gates (gates is L2: depends on `stats`; climate is
  an L1 leaf). If the shared evaluator currently lives only in `gates`, it must be lifted to `core`
  before climate can reuse it (the escalation below).

## Owned Data
- The **coarse climate grid** (`CellState` per `GridCell`: `Moisture`, `Temperature`, `Terrain`) and
  the **`RainProcess`** accumulator — both held in `State`, both snapshot-serialized (data-contracts
  §6). `State` is owned by `engine/world` (one per run); `climate.Step` returns a fresh `next` and
  never mutates `prev` (so the plan-phase snapshot and the apply-phase write don't alias, mirroring
  `navmap.Snapshot`).
- The **`Rules`** table is owned by `platform/config` (built from `content/climate.yaml`), injected
  read-only; this module never parses or mutates it.
- This module owns **no** navmap state and writes **no** navmap data — it only emits `[]Transition`.

## Invariants
- **D12 determinism** — `Step` is a pure function of `(prev, Forcing, Rules, rng)`. Same inputs +
  same RNG fork ⇒ byte-identical `next` State and `[]Transition`. No `time.Now()`, no global rand, no
  wall-clock; the only randomness is the injected `*rng.RNG`. Forcing is `worldtime`-derived, never a
  clock read.
- **D12 no map-iteration for logic** — the grid is stored/iterated in **sorted `GridCell` order**
  (Y-major then X) for the Step sweep, transition list, and `Cells()`. Float accumulation order is
  fixed. The returned `[]Transition` is in sorted `GridCell` order.
- **D11 / grid-is-index** — `GridCell` is an internal coarse index; climate never reads or writes an
  agent `Pos`. The world maps `GridCell` → continuous-space → `navmap.Cell` set when applying.
- **D4/D10 data-defined transitions** — the from-type×condition→to-type table is `content/climate.yaml`
  data evaluated via the §6 DSL; there is **no bespoke per-terrain Go transition function**. An
  unknown terrain id is caught at load (`platform/config`), never in `Step`.
- **Rain model shape is fixed (1a)** — `PRain` = 0 right after rain; rises by `RainProbPerHour` each
  dry game-hour; expected first rain ≈ 10 days (240 game-hours) via the calibrated rate; **forced**
  rain at `RainHardCapHours` (720) dry hours; rain duration is `Uniform[RainDurMin,RainDurMax]` (2–12)
  game-hours from the seeded RNG. `Moisture` rises by `MoistureRainRate` while raining and falls by
  `EvapBaseRate + EvapTempScale·Temperature` per dry game-hour (Temperature→Moisture feedback). The
  10d/30d/2–12h **shape** is fixed; only the rates are content (D9 spirit — rate-only tuning).
- **Temperature model (1b)** — `Temperature` = a day-of-cycle curve between `TempNightLow` (trough)
  and `TempDayPeak` (peak) from `Forcing.HourOfDay`, minus `TempRainDrop` while raining; normalized
  to `[0,1]`. Seasons/wind/sun are out of scope (parked, RESOLVED #2).
- **Bounds** — `Moisture, Temperature ∈ [0,1]` (clamped); `PRain ∈ [0,1]`; every `GridCell` in
  `[0,GridCols)×[0,GridRows)`.
- **Outcome-neutral until activated** — the introduction phases ship with `Rules` empty / climate-off
  so `Step` emits **no** transitions and existing world/navmap goldens hold; activation is a deliberate
  later phase with its own re-baseline (RESOLVED #10, `docs/climate.md` §2 M-staging).
- **Read-only inputs** — `Step` never mutates `prev`, `Rules`, or `Forcing`.

## Acceptance Criteria (testable)
- [ ] **Rain accumulates then fires (1a)** — starting dry, `PRain` rises by `RainProbPerHour` each
  `Step` (HoursSinceRain++); with a forced RNG draw below `PRain` rain starts and `RainEndsAtHour` is
  `AbsHour + Uniform[2,12]`; while raining `Moisture` rises by `MoistureRainRate`. Table-driven.
- [ ] **Expected-first-rain ≈ 10 days** — over many seeds (fixed seed list) the mean `AbsHour` of
  first rain is ≈ 240 game-hours within tolerance; a statistical (not byte) assertion documents the
  `RainProbPerHour` calibration (1a).
- [ ] **30-day hard cap (1a)** — with the RNG rigged to never trigger, rain is **forced** at exactly
  `RainHardCapHours` (720) dry game-hours regardless of `PRain`.
- [ ] **Rain duration uniform 2–12h (1a)** — over many seeds the rain duration is in `[2,12]` and
  approximately uniform; `RainEndsAtHour − rainStartHour ∈ [2,12]`.
- [ ] **Evaporation scales with Temperature (1a/1b)** — after rain stops, `Moisture` falls per dry
  hour by `EvapBaseRate + EvapTempScale·Temperature`; a hotter cell (higher `Temperature`) dries
  faster than an identical cooler cell. Table-driven.
- [ ] **Temperature day curve (1b)** — `Temperature` peaks near midday `HourOfDay` and troughs at
  night between `TempNightLow`..`TempDayPeak`; drops by `TempRainDrop` while raining; clamped `[0,1]`.
- [ ] **Transition fires on threshold (D4/D10)** — with a rule `forest → swamp when moisture > τ`,
  raising a cell's `Moisture` past `τ` makes `Step` emit `Transition{cell, forest→swamp}` and set the
  cell's `Terrain` to swamp in `next`; below `τ` no transition. The reverse drying rule
  (`swamp → forest when moisture < τ'`) fires symmetrically. Rule table is loaded from a test fixture
  `content/climate.yaml` shape, NOT hardcoded.
- [ ] **First matching rule wins, sorted-cell order** — a cell matching two rules takes the first in
  the ordered table; the `[]Transition` is in sorted `GridCell` order (D12).
- [ ] **Climate-off neutrality** — with an empty `Rules`, a multi-step run emits zero transitions and
  the climate state evolution (rain/moisture/temp) does not panic; world/navmap goldens unaffected
  (RESOLVED #10).
- [ ] **Determinism golden** — a fixed `(Config, seed, Forcing sequence, Rules)` over N steps yields a
  byte-identical digest of `Cells()` + `Rain()` + the per-step `[]Transition` (D12). A second run from
  a fresh registry built from the same `content/climate.yaml` reproduces it (cross-process).
- [ ] **Resume invariant** — capturing `State` (incl. `RainProcess`) at step T and resuming yields the
  same step-T+k state + transitions as running 0→T+k uninterrupted (data-contracts §5; ties into
  `rng_state` round-trip).
- [ ] **No wall-clock / no global rand (D12 guard)** — grep guard: no `time` import for logic, no
  global `rand` call; the only randomness is the injected `*rng.RNG`. No `engine/navmap`, `worldtime`,
  or `gates` import.
- [ ] **No hardcoded constant / terrain id (D10 guard)** — grep guard: no rain-rate / threshold /
  terrain-name string literal in logic; every constant flows from `Config`/`Rules` (the 720/2/12
  fixed-shape values arrive via `Config`, injected by `platform/config`).

## Out of Scope
- *When* to call `Step` (the `tick % 60` cadence), the per-step RNG-fork derivation for the climate
  step, mapping `GridCell` → `[]navmap.Cell`, and calling `navmap.SetTerrain` in the apply phase →
  `engine/world` (`docs/climate.md` §0/§1, `backend/engine/world/SPEC-tick.md`). Climate is a pure
  transform; world owns cadence + the navmap bridge (RESOLVED #5/#6/#9).
- The navmap write itself + the terrain-override delta source → `engine/navmap` (`SetTerrain`,
  `TerrainOverrides`).
- Parsing/validating `content/climate.yaml` + `content/terrain.yaml`, compiling the `Rules`, and
  cross-checking terrain ids + condition operands → `platform/config`
  (`content/schema/climate.schema.json`).
- Serialization wire format / Redis / SSE streaming of the climate field → `platform/persist` +
  `docs/data-contracts.md §6` (this module exposes `Cells()` + `Rain()` as the sources).
- Path-cost effects of Temperature (agent comfort/stamina) → **parked** (RESOLVED #8: Temperature
  does not enter path cost in P1; only Moisture/transition do). Seasons, wind, sun, elevation →
  parked (RESOLVED #2).
- The shared §6 Formula evaluator **implementation** → its home module (escalation below); climate
  only *uses* it (via compiled `Rules`) to evaluate conditions.

## Open Questions
- **§6 Formula-evaluator home module (P1 ESCALATION — blocks M2 implementation).** Climate `Rules`
  conditions are the **boolean subset** of the §6 DSL — the same subset `engine/gates.GateExpr`
  already evaluates, which the glossary calls "one shared evaluator." Today that evaluator lives only
  in `engine/gates` (L2, depends on `stats`), which climate (an L1 `core`+`rng` leaf) **cannot**
  import without breaking the DAG. Three options for the implementer/architect/human:
  - **(a) Lift the §6-DSL boolean evaluator to `engine/core` (L0)** so both `gates` and `climate`
    reuse it (matches the glossary "one shared evaluator" intent; cleanest, but a `core`+`gates`
    SPEC refactor). **Recommended.**
  - **(b) Climate ships a tiny self-contained comparison evaluator** for `op(operand, literal)` over
    `{moisture,temperature}` only (no logical `& | !` for P1). Smallest blast radius; risks the DSL
    drift the glossary forbids — accept only as a stopgap.
  - **(c) `platform/config` compiles each `when` into a Go closure** `func(CellState) bool` stored in
    `Rules`; the engine never imports any evaluator. Keeps climate evaluator-free but moves DSL
    semantics into platform (acceptable for boolean-only P1; revisit when conditions need arithmetic).
  M1 (navmap `SetTerrain`) and M3 (world wiring, `Rules` empty) are **not** blocked. Only M2's
  condition evaluation is. **Return this to the human/architect before M2 implementation.**
- **Climate-cell size vs navmap-cell size** (non-blocking): how many navmap cells per climate cell?
  Affects transition granularity vs scan cost. Owned by `Config.GridCols/Rows` + the world mapping;
  start coarse (RESOLVED #1) and profile. Does not block M1/M2.
- **Initial Moisture/Temperature seeding** (non-blocking): uniform default vs per-terrain default
  (e.g. swamp starts wet). Default: a single `Config.Init*` value for P1 (climate.yaml `grid.initial_*`);
  per-terrain in content later.

## Notes
- `Step` deliberately mirrors `navmap`'s and `world`'s "pure read → return delta → world applies"
  shape: it returns `(next, transitions)` and never writes navmap, exactly as `pathfind` returns a
  path and never writes wear. This is what keeps `world` the single mutator (D12 apply phase).
- The rain process is the one place a probability *process* (not a sine) drives the world — RESOLVED
  #3 chose a seeded stochastic process over a deterministic curve precisely so weather feels
  non-periodic while staying byte-deterministic from the seed. Keep all rain draws on the injected
  fork.
- Tuning lives in `content/climate.yaml` (rates); the **fixed-design shape** (10d expected / 30d
  forced / 2–12h duration) is encoded in the formulas here, not as a free content knob (1a).
- Adding a new forcing/state operand (wind, season) is a `content/climate.yaml` + §6-operand data
  change (D10), not a code change — the §6 DSL is the extension seam (RESOLVED #2).
