# SPEC — `engine/space/navmap`

> Status: `DRAFT`
> Leaf level: `L1`  ·  Owner agent: `<filled by implementer>`

## Purpose
The navigation **cost field**: a **flat-top hexagonal** grid-indexed traversal-cost model over
continuous space (axial `(q,r)` cells). It holds per-cell terrain base cost, building footprints
(impassable cells + door portals), and a **sparse `wear` field** (emergent trails). It answers "how
expensive / passable is this location?" for `pathfind` and accepts wear deposits/decay **and terrain
transitions** from `world`. It is an **index, not the world** (D11): agent positions stay continuous
`float`; this module only quantizes *cost*, exactly as `spatial` quantizes *proximity* — the hex is an
index-cell shape, NOT a tiling of the world. **navmap is the single hex-convention authority**
(orientation, neighbor set/order, pixel↔hex mapping, `CellSize`↔hex-size, D12 sort); `pathfind` and the
frontend CONSUME it (`Neighbors`/`CellCenter` + the wire), never re-derive it (`docs/plans/hex-grid.md`).

## Public Interface
```go
// Cell is the AXIAL hex index (q,r) of a continuous point — FLAT-TOP hexagons; internal
// addressing only, never exposed as an agent position. CellOf maps a Vec2 → Cell via the
// flat-top pixel→hex transform + cube rounding (navmap owns this "snap a point to its hex").
type Cell struct{ Q, R int }

// TerrainID names a terrain type from content/terrain.yaml (e.g. "plain","water","steep").
type TerrainID = core.Tag

type NavMap struct{ /* opaque; see Owned Data */ }

// New builds a NavMap for a world of the given bounds and cell size, with a terrain
// sampler (per-run layout) and the terrain type table (base cost + required tags).
func New(cfg Config, terrainAt func(core.Vec2) TerrainID, types map[TerrainID]TerrainType) *NavMap

type Config struct {
    CellSize    float64 // hex CIRCUMRADIUS (centre→vertex) in world units; adjacency spacing derives from it (tunable, hex-grid.md Q3)
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
func (m *NavMap) CellOf(p core.Vec2) Cell              // Vec2 → axial hex (flat-top pixel→hex + cube round)
func (m *NavMap) Neighbors(c Cell) []Cell             // the 6 flat-top axial neighbors in FIXED canonical order —
                                                       // the SOLE neighbor authority pathfind consumes (no hardcoded
                                                       // offsets elsewhere ⇒ no hex-convention drift)
func (m *NavMap) Passable(c Cell) bool                 // terrain passable AND no blocking footprint
func (m *NavMap) TerrainAt(c Cell) TerrainID
func (m *NavMap) StepCost(from, to Cell) float64       // base × wear-multiplier × geometric length; +Inf if impassable.
                                                       // geometric length = ‖CellCenter(from)−CellCenter(to)‖ — uniform
                                                       // for the 6 hex neighbors (the square √2-diagonal case is gone)
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

// CellCenter maps a Cell back to its continuous world-coordinate centre (the flat-top hex centre; the
// inverse of CellOf up to quantisation). navmap is the geometry authority (owns CellSize + bounds);
// `engine/space/pathfind` calls it to turn a cell path into continuous `Vec2` waypoints. Pure read,
// no bound-check (D11 — an index→coordinate read, never a snap of an agent position).
func (m *NavMap) CellCenter(c Cell) core.Vec2

// InBounds reports whether cell c's centre falls within the configured world bounds
// ([MinX,MaxX)×[MinY,MaxY)). Pure read; used by callers that walk the hex graph outside navmap
// (e.g. the world→exposure Topology adapter, docs/plans/shelter.md SH1). Index read, no agent snap (D11).
func (m *NavMap) InBounds(c Cell) bool

// MinCostFactor returns a guaranteed lower bound on a cell's effective cost per unit geometric length
// (= cfg.WearCostMin; valid because BaseCost ≥ 1 and wear-multiplier ∈ [WearCostMin,1]). pathfind uses
// it for an admissible A* heuristic + EstimateCost (so EstimateCost ≤ true Path cost). Pure read.
func (m *NavMap) MinCostFactor() float64

// ── Offset (col,row) layout — the render/wire rectangular projection (hex-grid.md Q5) ──────────────
// navmap is the SINGLE flat-top odd-q authority: worldgen (fixture terrain layout), world (TerrainRenderView
// + terrain_delta offset index), and persist/api all go through THESE instead of re-deriving the offset↔
// axial convention. offset(0,0) is the (MinX,MinY) corner (axial 0,0); render index i = row*Cols + col.
func (m *NavMap) Orientation() string                 // "flat" (flat-top); the wire+frontend mirror this
func (m *NavMap) OffsetDims() (cols, rows int)         // offset-grid dims covering the world bounds (fringe-generous)
func (m *NavMap) OffsetToCell(col, row int) Cell       // offset(col,row) → axial Cell
func (m *NavMap) CellToOffset(c Cell) (col, row int)   // axial Cell → offset(col,row) (inverse of OffsetToCell)

// Pure Config-level variants (no NavMap instance / terrain needed): let an authoring tool (worldgen)
// index an offset-layout terrain array BEFORE navmap.New exists (New samples terrainAt at construction).
// The instance methods delegate to these, so there is exactly ONE convention. OffsetIndexAt ≡
// CellToOffset∘CellOf; OffsetDimsOf ≡ OffsetDims.
func OffsetDimsOf(cfg Config) (cols, rows int)
func OffsetIndexAt(cfg Config, p core.Vec2) (col, row int)
// OffsetCenterOf is the inverse read of OffsetIndexAt: the world-space centre of the hex at
// offset(col,row) — the coordinate an authoring tool samples noise/fields at so generated terrain
// is isotropic in WORLD space, not grid space. OffsetIndexAt(cfg, OffsetCenterOf(cfg,c,r)) == (c,r).
func OffsetCenterOf(cfg Config, col, row int) core.Vec2
// OffsetNeighborsOf enumerates the 6 hex-adjacent offset coords of offset(col,row) (flat-top odd-q,
// fixed order for D12; may include out-of-grid coords — callers bound-check against OffsetDimsOf).
// Convention-only (no Config): offset adjacency is geometry-independent.
func OffsetNeighborsOf(col, row int) [6][2]int

// ── Mutations (apply phase only; serial, world-owned) ─────────────────────────────
func (m *NavMap) Deposit(cells []Cell, amount float64) // add wear along a traversed path
func (m *NavMap) Decay()                               // one fixed-order pass; drops fully-faded cells
func (m *NavMap) StampFootprint(cells []Cell, passable bool) // building place/remove (door = passable gap)

// SetTerrain rewrites the terrain base-cost layer for the given cells to `t`
// (must be a key in the `types` table passed to New). This realizes a climate
// terrain *transition* (e.g. forest→swamp): each cell's BaseCost / Passable /
// RequiredTags become those of `t`, so subsequent StepCost/Passable/RequiredTags
// reflect the new type. Apply-phase only, world-owned, serial — the world passes
// the cell slice in sorted (R-major then Q, canonical hex) order, EXACTLY like StampFootprint, so
// last-write/accumulation order is fixed and reproducible (D12). SetTerrain does
// NOT touch the wear or footprint layers; a footprint-blocked cell stays
// impassable regardless of its new terrain type. The world (NOT this module)
// decides WHICH cells transition and WHEN — that cadence and the from→to rules
// live in engine/env/climate; navmap is a passive writer here. Passing an unknown
// TerrainID panics (the content terrain catalog is load-time validated against the
// climate transition table so an unknown id is a configuration bug — D10,
// docs/core/architecture.md §7).
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
  transitions rewrite per-cell `TerrainID` (RESOLVED #7, `docs/plans/climate.md` §1). The base layout from
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
- **D11** — `Cell` is an internal AXIAL hex index; it is never written into an agent's `Pos`. Positions
  stay continuous. No public API snaps a position to a cell center. Hex is an index-cell shape, not a
  world tiling.
- **D12** — No `map` iteration drives results: `Decay`, `ActiveWear`, `TerrainOverrides`, and any
  cell enumeration iterate a **canonical sorted** order — **R-major then Q** (the hex replacement for
  the old "Y-major then X", `docs/plans/hex-grid.md` Q6). `SetTerrain` applies its cell slice in the
  world-supplied sorted order; float/last-write order is fixed.
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
- [ ] **TerrainOverrides is sparse + D12-sorted** — empty before any `SetTerrain`; after transitioning two cells it lists exactly those two in R-major/Q (canonical hex) order with their new ids; reverting one to its base type drops it from the list (delta away from base only).
- [ ] **Outcome-neutral pre-activation** — a run that never calls `SetTerrain` produces a byte-identical `ActiveWear()`/`TerrainOverrides()`/`StepCost` field to the pre-`SetTerrain` SPEC (existing goldens hold; regression guard).
- [ ] **SetTerrain unknown id panics** — `SetTerrain([c], "nope")` for an id absent from `types` panics (content-contract guard).
- [ ] **Determinism (golden)** — same `(Config, terrain, deposit/decay/SetTerrain sequence)` ⇒ byte-identical `ActiveWear()` + `TerrainOverrides()` ordering and values; no map-iteration leakage.
- [ ] **Bounds** — a hex whose CENTRE falls outside `[MinX,MaxX]×[MinY,MaxY]` is impassable.
- [ ] **Neighbors (flat-top, canonical order)** — `Neighbors(c)` returns exactly the 6 flat-top axial
  neighbors of `c` in the fixed canonical order on every call (table-driven); each is adjacent
  (`StepCost` finite when passable) and equidistant from `c`'s centre.
- [ ] **pixel↔hex round-trip** — `CellCenter(CellOf(p))` lands in `p`'s hex for random points at
  several offsets; `CellOf(CellCenter(c)) == c` for random cells (cube-round stability, incl. negative
  coords and cell-boundary points).
- [ ] **FootprintBlocked isolates the footprint layer** — `FootprintBlocked` is true ONLY for stamped wall/building cells and false for deep-water (`Passable:false`) terrain; a water cell is `!FootprintBlocked` yet `!Passable` (so the fauna `TerrainSampler` treats it as traversable-at-high-cost, not a wall). Table-driven.
- [ ] **BaseCost is the override-aware per-cell terrain cost** — `BaseCost(c)` equals the cell's terrain `BaseCost` (≥1), updates after `SetTerrain` (override-aware), is independent of wear and footprint stamps, and returns 0 out-of-bounds. Table-driven.

## Out of Scope
- Path search itself → `engine/space/pathfind`.
- *When* to deposit/decay, *who* traverses, and *which cells transition + at what cadence* →
  `engine/world` tick (`docs/plans/map.md §world`); the transition *decision* (climate state → cell
  list) → `engine/env/climate` (`backend/engine/env/climate/SPEC.md`). navmap only executes the write.
- Climate state (Moisture/Temperature), the rain model, and the from-type×condition→to-type
  transition table → `engine/env/climate` + `content/climate.yaml` (`docs/plans/climate.md`).
- Terrain **layout** generation and the terrain type **catalog** parsing → `platform/config` + `content/terrain.yaml` (this module receives `terrainAt`/`types` already built).
- Serialization wire format → `platform/persist` + `docs/core/data-contracts.md §6` (this module exposes `ActiveWear()` + `TerrainOverrides()` as the sparse sources).

## Open Questions
- **Cell size** — `CellSize` is the hex **circumradius**, = the old value for now, **tuned later** once
  cells are visible (`docs/plans/hex-grid.md` Q3); note a hex at circumradius `R` covers ~2.6× a square cell of
  edge `R`, so the grid is coarser until retuned.
- **Connectivity** — RESOLVED: **6-neighbor** flat-top hex (`Neighbors`); `StepCost` geometric length is
  the uniform centre-to-centre distance (the old square √2-diagonal case is gone).
- **Climate grid ↔ navmap cell mapping** (non-blocking): climate stays a *coarse SQUARE* grid; the
  square-climate-cell → hex-navmap-cells enumeration for the `SetTerrain` slice is owned by
  `world`/`climate` (`docs/plans/hex-grid.md` §H6), not this module. navmap stays hex-cell-granular.

## Notes
- The wear model is the ant-trail / desire-path mechanic: `Deposit` on traversal, `Decay` each tick,
  `cost = base × f(wear)`. The "road = TTL object" intuition is realized as this decaying sparse field
  (`docs/core/design.md §5`), **not** a separate object system — pathfinding reads it inline.
- Keep `wear` sparse so persist/stream cost scales with *active trail length*, not world area.
- `SetTerrain` mirrors `StampFootprint` deliberately: both are world-owned, serial, sorted-cell
  apply-phase writers over a cell slice. Terrain is now *dynamic* (carries climate-driven transitions,
  `docs/core/design.md §5`); `TerrainOverrides()` makes it stream as a sparse delta like `wear`, not as a
  one-time static layout (`docs/core/data-contracts.md §6`, RESOLVED #11).
- **Hex migration** (`docs/plans/hex-grid.md`): cells are flat-top axial `(q,r)`; navmap is the hex-convention
  authority. Engine/pathfind logic speaks **axial only** (`Cell{Q,R}`, `Neighbors`, `CellCenter`); the
  offset(col,row) rectangular layout is the render/wire projection, and its offset↔axial conversion is
  owned HERE too (`Orientation`/`OffsetDims`/`OffsetToCell`/`CellToOffset` + the pure `OffsetDimsOf`/
  `OffsetIndexAt`) so worldgen/world/persist never re-derive it — ONE convention. `spatial`/`scent`/
  `climate` stay SQUARE (surgical scope); they must not import `navmap.Cell`.
