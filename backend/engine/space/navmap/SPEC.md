# SPEC — `engine/space/navmap`

> Status: `DRAFT`
> Leaf level: `L1`  ·  Owner agent: `<filled by implementer>`

## Purpose
The navigation **cost field**: a grid-indexed traversal-cost model over continuous space. It holds
per-cell terrain base cost, building footprints (impassable cells + door portals), and a **sparse
`wear` field** (emergent trails). It answers "how expensive / passable is this location?" for
`pathfind` and accepts wear deposits/decay **and terrain transitions** from `world`. It is an
**index, not the world** (D11): agent positions stay continuous `float`; this module only quantizes
*cost*, exactly as `spatial` quantizes *proximity*.

## Public Interface
```go
// Cell is the integer grid index of a continuous point (internal addressing only;
// never exposed as an agent position). CellOf maps a Vec2 → Cell deterministically.
type Cell struct{ X, Y int }

// TerrainID names a terrain type from content/terrain.yaml (e.g. "plain","water","steep").
type TerrainID = core.Tag

type NavMap struct{ /* opaque; see Owned Data */ }

// New builds a NavMap for a world of the given bounds and cell size, with a terrain
// sampler (per-run layout) and the terrain type table (base cost + required tags).
func New(cfg Config, terrainAt func(core.Vec2) TerrainID, types map[TerrainID]TerrainType) *NavMap

type Config struct {
    CellSize    float64 // grid cell edge in world units (≈ spatial hash cell; tunable)
    MinX, MinY  float64 // world bounds (cells outside bounds are impassable)
    MaxX, MaxY  float64
    WearOnUse   float64 // wear added to each traversed cell per traversal tick
    WearOnPave  float64 // wear added by an explicit Pave action (≫ WearOnUse)
    WearDecay   float64 // wear removed per tick (use-it-or-lose-it; trails fade)
    WearMax     float64 // wear clamp
    WearCostMin float64 // cost multiplier floor at WearMax (e.g. 0.3 ⇒ a paved cell is 30% of base)
}

type TerrainType struct {
    BaseCost     float64    // multiplies step length; ≥1 for rough terrain
    Passable     bool       // false ⇒ never traversable (deep water w/o swim, cliff)
    RequiredTags []core.Tag // traversal needs an action carrying one of these (Swim …); empty = none
}

// ── Queries (read-only; used by pathfind during the parallel plan phase) ──────────
func (m *NavMap) CellOf(p core.Vec2) Cell
func (m *NavMap) Passable(c Cell) bool                 // terrain passable AND no blocking footprint
func (m *NavMap) TerrainAt(c Cell) TerrainID
func (m *NavMap) StepCost(from, to Cell) float64       // base × wear-multiplier × geometric length; +Inf if impassable
func (m *NavMap) RequiredTags(c Cell) []core.Tag       // capability/action tags to enter c (terrain)

// FootprintBlocked reports whether c carries a building/wall footprint — a HARD blocker for ALL
// species, independent of terrain. Unlike Passable (terrain-impassable OR footprint, conflated), this
// isolates the footprint layer so a consumer can treat deep water as traversable-at-high-cost (NOT
// !Passable) while walls stay impassable. Required by the fauna `TerrainSampler` (R2/F35/W10b — water =
// 수영 가능, 고비용; only footprints block); see `engine/world/SPEC-world-fauna.md`.
func (m *NavMap) FootprintBlocked(c Cell) bool

// BaseCost returns c's current terrain base cost (≥1; the terrain layer's BaseCost INCLUDING any
// SetTerrain override — override-aware), WITHOUT the wear multiplier or footprint. The fauna
// `TerrainSampler` reads it as the per-species effective-cost base (effective cost = BaseCost ×
// Rules.TerrainCost(species,terrain).mult, W10b). Out-of-bounds returns 0 (callers bound-check;
// StepCost still returns +Inf for OOB via Passable). This is the per-cell single read the world
// previously had to reconstruct via TerrainAt→terrainTypes join — navmap owns it (cleaner + override-aware).
func (m *NavMap) BaseCost(c Cell) float64

// CellCenter maps a Cell back to its continuous world-coordinate centre (the inverse of CellOf up to
// quantisation). navmap is the geometry authority (it owns CellSize + bounds, not otherwise public);
// `engine/space/pathfind` calls it to turn a cell path into continuous `Vec2` waypoints. Pure read,
// no bound-check (D11 — an index→coordinate read, never a snap of an agent position).
func (m *NavMap) CellCenter(c Cell) core.Vec2

// MinCostFactor returns a guaranteed lower bound on a cell's effective cost per unit geometric length
// (= cfg.WearCostMin; valid because BaseCost ≥ 1 and wear-multiplier ∈ [WearCostMin,1]). pathfind uses
// it for an admissible A* heuristic + EstimateCost (so EstimateCost ≤ true Path cost). Pure read.
func (m *NavMap) MinCostFactor() float64

// ── Mutations (apply phase only; serial, world-owned) ─────────────────────────────
func (m *NavMap) Deposit(cells []Cell, amount float64) // add wear along a traversed path
func (m *NavMap) Decay()                               // one fixed-order pass; drops fully-faded cells
func (m *NavMap) StampFootprint(cells []Cell, passable bool) // building place/remove (door = passable gap)

// SetTerrain rewrites the terrain base-cost layer for the given cells to `t`
// (must be a key in the `types` table passed to New). This realizes a climate
// terrain *transition* (e.g. forest→swamp): each cell's BaseCost / Passable /
// RequiredTags become those of `t`, so subsequent StepCost/Passable/RequiredTags
// reflect the new type. Apply-phase only, world-owned, serial — the world passes
// the cell slice in sorted (Y-major then X) order, EXACTLY like StampFootprint, so
// last-write/accumulation order is fixed and reproducible (D12). SetTerrain does
// NOT touch the wear or footprint layers; a footprint-blocked cell stays
// impassable regardless of its new terrain type. The world (NOT this module)
// decides WHICH cells transition and WHEN — that cadence and the from→to rules
// live in engine/env/climate; navmap is a passive writer here. Passing an unknown
// TerrainID panics (the content terrain catalog is load-time validated against the
// climate transition table so an unknown id is a configuration bug — D10,
// docs/architecture.md §7).
func (m *NavMap) SetTerrain(cells []Cell, t TerrainID)

// ── Snapshot (frozen read for the plan phase + serialization) ─────────────────────
func (m *NavMap) Snapshot() *NavMap                    // immutable copy-on-write view pathfind reads
func (m *NavMap) ActiveWear() []WearCell               // sparse: only cells with wear>0 (D12-sorted), for persist/stream
type WearCell struct{ Cell Cell; Wear float64 }

// TerrainOverrides returns the cells whose terrain was changed by SetTerrain away
// from the New-time base layout, in D12-sorted order. Sparse — empty when climate
// has not (yet) transitioned anything (the climate-off / pre-activation state), so
// pre-climate serialization and goldens are unaffected. This is the dynamic-terrain
// delta source for persist/stream (data-contracts §6: periodic full + sparse deltas).
func (m *NavMap) TerrainOverrides() []TerrainCell
type TerrainCell struct{ Cell Cell; Terrain TerrainID }
```

## Dependencies
- `engine/kernel/core` — `Vec2`, `Tag`.

## Owned Data
- **Terrain base-cost layer** — seeded once from `terrainAt` + `types` at `New`, then **mutated only
  by `SetTerrain`** (apply phase, world-owned). It is no longer immutable after `New`: climate
  transitions rewrite per-cell `TerrainID` (RESOLVED #7, `docs/climate.md` §1). The base layout from
  `New` is the baseline; `TerrainOverrides()` exposes the sparse delta away from it.
- **Footprint layer** — building-blocked cells + door portals; mutated only by `StampFootprint`.
  Independent of the terrain layer (a footprint-blocked cell stays impassable across a `SetTerrain`).
- **`wear` field** — `map[Cell]float64`, **sparse** (only traversed/paved cells present; absent ⇒ 0).
  This is the *road*; it is emergent runtime state, **not** content (D2/D3). Independent of terrain.
- Ownership: `world` is the sole mutator (Deposit/Decay/StampFootprint/**SetTerrain** in the apply
  phase). `pathfind` receives a `Snapshot()` and must never mutate. `engine/env/climate` does **not**
  import or touch navmap — it returns a transition cell list and `world` performs the `SetTerrain`
  write (preserves the "world is the sole mutator" invariant, RESOLVED #6).

## Invariants
- **D11** — `Cell` is an internal index; it is never written into an agent's `Pos`. Positions stay
  continuous. No public API snaps a position to a cell center.
- **D12** — No `map` iteration drives results: `Decay`, `ActiveWear`, `TerrainOverrides`, and any
  cell enumeration iterate a **sorted** cell order (Y-major then X, or sorted keys). `SetTerrain`
  applies its cell slice in the world-supplied sorted order; float/last-write order is fixed.
- `StepCost` is symmetric only up to terrain; it returns `+Inf` (not an error) for impassable so
  `pathfind` prunes uniformly.
- `Snapshot()` is cheap and isolates the plan phase from concurrent apply-phase mutation (the plan
  phase is read-only on shared state — mirrors `world.currentSnap`). A snapshot taken before a
  `SetTerrain` shows the old terrain; the next tick's snapshot shows the new one.
- `wear` ∈ `[0, WearMax]`; `wear`-cost multiplier ∈ `[WearCostMin, 1]`, monotone decreasing in wear.
- **Terrain mutation is outcome-neutral until written** — until `world`/`climate` actually calls
  `SetTerrain`, the base-cost layer equals the `New`-time layout exactly, so all existing terrain /
  pathfind goldens hold unchanged (RESOLVED #10 staged-rebaseline: introduce neutral, activate later).
- `SetTerrain` with an unknown `TerrainID` panics (content contract; never silently no-ops).

## Acceptance Criteria (testable)
- [ ] **Terrain cost** — `StepCost` over a `BaseCost=2` cell is 2× a `BaseCost=1` cell of equal length; table-driven.
- [ ] **Impassable** — `Passable` is false and `StepCost` is `+Inf` for `Passable:false` terrain and for stamped wall footprints; a door cell inside a wall is `Passable:true`.
- [ ] **Wear lowers cost** — after `Deposit` raising a cell to `WearMax`, its `StepCost` = base × `WearCostMin`; an untouched neighbour is unchanged.
- [ ] **Decay fades trails** — with no deposits, repeated `Decay` returns a cell's wear to 0 and **removes** it from `ActiveWear` (sparse); golden over N ticks.
- [ ] **SetTerrain transitions cost** — after `SetTerrain([c], "swamp")` where swamp `BaseCost`>forest, `StepCost`/`TerrainAt`/`RequiredTags` for `c` reflect swamp; a neighbour cell is unchanged; the wear value on `c` is untouched. A `SetTerrain([c], "water")` (`Passable:false`) makes `Passable(c)` false. Table-driven.
- [ ] **SetTerrain × footprint independence** — a footprint-blocked cell stays `Passable:false` after a `SetTerrain` to a passable type; un-stamping then exposes the new terrain.
- [ ] **TerrainOverrides is sparse + D12-sorted** — empty before any `SetTerrain`; after transitioning two cells it lists exactly those two in Y-major/X order with their new ids; reverting one to its base type drops it from the list (delta away from base only).
- [ ] **Outcome-neutral pre-activation** — a run that never calls `SetTerrain` produces a byte-identical `ActiveWear()`/`TerrainOverrides()`/`StepCost` field to the pre-`SetTerrain` SPEC (existing goldens hold; regression guard).
- [ ] **SetTerrain unknown id panics** — `SetTerrain([c], "nope")` for an id absent from `types` panics (content-contract guard).
- [ ] **Determinism (golden)** — same `(Config, terrain, deposit/decay/SetTerrain sequence)` ⇒ byte-identical `ActiveWear()` + `TerrainOverrides()` ordering and values; no map-iteration leakage.
- [ ] **Bounds** — cells outside `[MinX,MaxX]×[MinY,MaxY]` are impassable.
- [ ] **FootprintBlocked isolates the footprint layer** — `FootprintBlocked` is true ONLY for stamped wall/building cells and false for deep-water (`Passable:false`) terrain; a water cell is `!FootprintBlocked` yet `!Passable` (so the fauna `TerrainSampler` treats it as traversable-at-high-cost, not a wall). Table-driven.
- [ ] **BaseCost is the override-aware per-cell terrain cost** — `BaseCost(c)` equals the cell's terrain `BaseCost` (≥1), updates after `SetTerrain` (override-aware), is independent of wear and footprint stamps, and returns 0 out-of-bounds. Table-driven.

## Out of Scope
- Path search itself → `engine/space/pathfind`.
- *When* to deposit/decay, *who* traverses, and *which cells transition + at what cadence* →
  `engine/world` tick (`docs/map-plan.md §world`); the transition *decision* (climate state → cell
  list) → `engine/env/climate` (`backend/engine/env/climate/SPEC.md`). navmap only executes the write.
- Climate state (Moisture/Temperature), the rain model, and the from-type×condition→to-type
  transition table → `engine/env/climate` + `content/climate.yaml` (`docs/climate.md`).
- Terrain **layout** generation and the terrain type **catalog** parsing → `platform/config` + `content/terrain.yaml` (this module receives `terrainAt`/`types` already built).
- Serialization wire format → `platform/persist` + `docs/data-contracts.md §6` (this module exposes `ActiveWear()` + `TerrainOverrides()` as the sparse sources).

## Open Questions
- **Cell size** (P1-non-blocking): reuse `spatial` cell (8.0) or finer for path fidelity? Tradeoff fidelity vs memory/stream size. Default: start at the spatial cell, make it `Config.CellSize`.
- **8- vs 4-connectivity** is a `pathfind` concern, but `StepCost`'s geometric length term must agree (diagonal = √2). Decided in `pathfind` SPEC.
- **Climate grid ↔ navmap cell mapping granularity** (non-blocking): climate owns a *coarse* grid (one climate cell = many navmap cells, RESOLVED #1); the mapping climate-cell → navmap-cells is owned by `world`/`climate` when it builds the `SetTerrain` cell slice, not by this module. navmap stays cell-granular.

## Notes
- The wear model is the ant-trail / desire-path mechanic: `Deposit` on traversal, `Decay` each tick,
  `cost = base × f(wear)`. The "road = TTL object" intuition is realized as this decaying sparse field
  (`docs/design.md §5`), **not** a separate object system — pathfinding reads it inline.
- Keep `wear` sparse so persist/stream cost scales with *active trail length*, not world area.
- `SetTerrain` mirrors `StampFootprint` deliberately: both are world-owned, serial, sorted-cell
  apply-phase writers over a cell slice. Terrain is now *dynamic* (carries climate-driven transitions,
  `docs/design.md §5`); `TerrainOverrides()` makes it stream as a sparse delta like `wear`, not as a
  one-time static layout (`docs/data-contracts.md §6`, RESOLVED #11).
